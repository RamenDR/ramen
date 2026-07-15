// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"fmt"
	"time"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	"github.com/backube/volsync/controllers/statemachine"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/cephfscg"
	"github.com/ramendr/ramen/internal/controller/util"
	"github.com/ramendr/ramen/internal/controller/volsync"
)

/*
Naming:

The naming follow the volsync handler. Currently, in volsync handler:
1. the replicationsource and replicationdestination have the same name with application PVC name
2. the snapshot name of source application pvc is volsync-<PVC_NAME>-src
3. the name of tmp pvc restored by volsync is volsync-<PVC_NAME>-src

In this design:

1. ReplicationGroupSource Name = ReplicationGroupDestination Name = <VRG Name = Application Name>+cgName

ReplicationGroupSource create VolumeGroupSnapshot, Restored PVC and ReplicationSource in each sync.
At the end of each sync, VolumeGroupSnapshot, Restored PVC will be deleted by ramen,
ReplicationSource will not be deleted.

2. VolumeGroupSnapshot Name = cephfscg-<ReplicationGroupSource Name>
3. Restored PVC Name = cephfscg-<Application PVC Name>
4. ReplicationSource Name = ReplicationDestination Name = <Application PVC Name>

5. ReplicationDestinationServiceName = volsync-rsync-tls-dst-<Application PVC Name>.<RD Namespace>.svc.clusterset.local
6. Volsync Secret Name = <VRG Name>-vs-secret

ReplicationGroupDestination will create application PVC which is the same with current implementation.
*/

// ReplicationGroupSourceReconciler reconciles a ReplicationGroupSource object
type ReplicationGroupSourceReconciler struct {
	client.Client
	APIReader                        client.Reader
	Scheme                           *runtime.Scheme
	volumeGroupSnapshotCRsAreWatched bool
	Log                              logr.Logger
}

// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=replicationgroupsources,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=replicationgroupsources/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=replicationgroupsources/finalizers,verbs=update
// +kubebuilder:rbac:groups=groupsnapshot.storage.k8s.io,resources=volumegroupsnapshots,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=groupsnapshot.storage.k8s.io,resources=volumegroupsnapshotclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=groupsnapshot.storage.k8s.io,resources=volumegroupsnapshotcontents,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=volsync.backube,resources=replicationsources,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=snapshot.storage.k8s.io,resources=volumesnapshots,verbs=get;list;watch
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get

func (r *ReplicationGroupSourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.Log.WithValues("rgs", req.NamespacedName, "rid", util.GetRID())
	logger.Info("Entering reconcile loop")

	defer logger.Info("Exiting reconcile loop")

	rgs, vrg, ramenConfig, done, err := r.getRGSConfig(ctx, req, logger)
	if done {
		return ctrl.Result{}, err
	}

	defaultCephFSCSIDriverName := cephFSCSIDriverNameOrDefault(ramenConfig)
	vsHandler, vgsHandler := r.buildVGSHandler(ctx, logger, rgs, vrg, ramenConfig, defaultCephFSCSIDriverName)

	return r.runRGSReconcile(ctx, logger, rgs, vrg, vsHandler, vgsHandler, defaultCephFSCSIDriverName)
}

// getRGSConfig fetches the RGS object, ramen config, and the owning VRG.
// done=true means the caller should return result/err immediately.
func (r *ReplicationGroupSourceReconciler) getRGSConfig(
	ctx context.Context,
	req ctrl.Request,
	logger logr.Logger,
) (
	rgs *ramendrv1alpha1.ReplicationGroupSource,
	vrg *ramendrv1alpha1.VolumeReplicationGroup,
	ramenConfig *ramendrv1alpha1.RamenConfig,
	done bool,
	err error,
) {
	if !r.volumeGroupSnapshotCRsAreWatched {
		return nil, nil, nil, true,
			fmt.Errorf("ReplicationGroupSource {%s/%s} doesn't work if VolumeGroupSnapshot CRD is not installed. "+
				"Please install VolumeGroupSnapshot CRD and restart the operator", req.Namespace, req.Name)
	}

	logger.Info("Get ReplicationGroupSource")

	rgs = &ramendrv1alpha1.ReplicationGroupSource{}
	if err = r.Client.Get(ctx, req.NamespacedName, rgs); err != nil {
		if !k8serrors.IsNotFound(err) {
			logger.Error(err, "Failed to get ReplicationGroupSource")
		}

		return nil, nil, nil, true, client.IgnoreNotFound(err)
	}

	logger.Info("Get ramen config from configmap")

	_, ramenConfig, err = ConfigMapGet(ctx, r.Client)
	if err != nil {
		logger.Error(err, "Failed to get ramen config")

		return nil, nil, nil, true, err
	}

	logger.Info("Get vrg from ReplicationGroupSource")

	vrg = &ramendrv1alpha1.VolumeReplicationGroup{}
	if err = r.Client.Get(ctx, types.NamespacedName{
		Name:      rgs.GetLabels()[util.VRGOwnerNameLabel],
		Namespace: rgs.GetLabels()[util.VRGOwnerNamespaceLabel],
	}, vrg); err != nil {
		return nil, nil, nil, true, err
	}

	if util.ResourceIsDeleted(vrg) {
		logger.Info("VRG is deleted, skipping RGSreconciliation", "vrg", types.NamespacedName{
			Name:      vrg.GetName(),
			Namespace: vrg.GetNamespace(),
		})

		return nil, nil, nil, true, nil
	}

	return rgs, vrg, ramenConfig, false, nil
}

// buildVGSHandler constructs the VSHandler and VolumeGroupSource handler appropriate for this RGS.
func (r *ReplicationGroupSourceReconciler) buildVGSHandler(
	ctx context.Context,
	logger logr.Logger,
	rgs *ramendrv1alpha1.ReplicationGroupSource,
	vrg *ramendrv1alpha1.VolumeReplicationGroup,
	ramenConfig *ramendrv1alpha1.RamenConfig,
	defaultCephFSCSIDriverName string,
) (*volsync.VSHandler, cephfscg.VolumeGroupSourceHandler) {
	adminNamespaceVRG := vrgInAdminNamespace(vrg, ramenConfig)

	vsHandler := volsync.NewVSHandler(ctx, r.Client, logger, vrg,
		&ramendrv1alpha1.VRGAsyncSpec{}, defaultCephFSCSIDriverName,
		volSyncDestinationCopyMethodOrDefault(ramenConfig), adminNamespaceVRG,
	)

	if util.IsDiffSyncEnabled(rgs.GetAnnotations()) {
		return vsHandler,
			cephfscg.NewDiffVolumeGroupSourceHandler(r.Client, rgs, defaultCephFSCSIDriverName, vsHandler, logger)
	}

	return vsHandler, cephfscg.NewVolumeGroupSourceHandler(r.Client, rgs, defaultCephFSCSIDriverName, vsHandler, logger)
}

// runRGSReconcile drives the RGS state machine (or the final-sync cleanup path) and updates status.
func (r *ReplicationGroupSourceReconciler) runRGSReconcile(
	ctx context.Context,
	logger logr.Logger,
	rgs *ramendrv1alpha1.ReplicationGroupSource,
	vrg *ramendrv1alpha1.VolumeReplicationGroup,
	vsHandler *volsync.VSHandler,
	vgsHandler cephfscg.VolumeGroupSourceHandler,
	defaultCephFSCSIDriverName string,
) (ctrl.Result, error) {
	if cephfscg.IsPrepareForFinalSyncTriggered(rgs) {
		logger.Info("Detected request for final sync preparation, waiting for confirmation to continue")

		const retryDelay = 5 * time.Second

		return ctrl.Result{RequeueAfter: retryDelay}, vgsHandler.CleanVolumeGroupSnapshot(ctx)
	}

	logger.Info("Run ReplicationGroupSource state machine", "DefaultCephFSCSIDriverName", defaultCephFSCSIDriverName)

	result, err := statemachine.Run(
		ctx,
		cephfscg.NewRGSMachine(r.Client, rgs, vrg, vsHandler, vgsHandler, logger),
		logger,
	)
	// Update instance status
	statusErr := r.Client.Status().Update(ctx, rgs)
	if err == nil { // Don't mask previous error
		err = statusErr
	}

	if err != nil {
		logger.Error(err, "Failed to reconcile ReplicationGroupSource")
	}

	return result, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *ReplicationGroupSourceReconciler) SetupWithManager(mgr ctrl.Manager,
	ramenConfig *ramendrv1alpha1.RamenConfig,
) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		WithOptions(ctrlcontroller.Options{
			MaxConcurrentReconciles: getMaxConcurrentReconciles(ramenConfig),
		}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&volsyncv1alpha1.ReplicationSource{}).
		For(&ramendrv1alpha1.ReplicationGroupSource{})

	if err := util.EnsureLocalVGSAPI(context.TODO(), r.APIReader); err != nil {
		return fmt.Errorf("VolSync is enabled but VolumeGroupSnapshot API is unavailable: %w", err)
	}

	r.volumeGroupSnapshotCRsAreWatched = util.OwnsVolumeGroupSnapshot(builder)

	return builder.Complete(r)
}
