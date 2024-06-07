// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"

	vgsv1alphfa1 "github.com/kubernetes-csi/external-snapshotter/client/v7/apis/volumegroupsnapshot/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/cephfscg"
	"github.com/ramendr/ramen/controllers/volsync"

	"github.com/backube/volsync/controllers/statemachine"
)

/*
Naming:

The naming follow the volsync handler. Currently, in volsync handler:
1. the replicationsource and replicationdestination have the same name with application PVC name
1. the snapshot name of source application pvc is volsync-<PVC_NAME>-src
2. the name of tmp pvc restored by volsync is volsync-<PVC_NAME>-src

In this design:

1. ReplicationGroupSource Name = ReplicationGroupDestination Name = VRG Name = Application Name

ReplicationGroupSource create VolumeGroupSnapshot, Restored PVC and ReplicationSource in each sync.
At the end of each sync, VolumeGroupSnapshot, Restored PVC and ReplicationSource will be deleted by ramen.

2. VolumeGroupSnapshot Name = cephfscg-<ReplicationGroupSource Name>
3. Restored PVC Name = cephfscg-<Application PVC Name>
4. ReplicationSource Name = cephfscg-<Application PVC Name>

5. ReplicationDestinationServiceName = volsync-rsync-tls-dst-<Application PVC Name>.<RD Namespace>.svc.clusterset.local
6. Volsync Secret Name = <VRG Name>-vs-secret

ReplicationGroupDestination will create application PVC which is the same with current implementation.
*/

// ReplicationGroupSourceReconciler reconciles a ReplicationGroupSource object
type ReplicationGroupSourceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=ramendr.openshift.io,resources=replicationgroupsources,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ramendr.openshift.io,resources=replicationgroupsources/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ramendr.openshift.io,resources=replicationgroupsources/finalizers,verbs=update
//+kubebuilder:rbac:groups=groupsnapshot.storage.k8s.io,resources=volumegroupsnapshots,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=groupsnapshot.storage.k8s.io,resources=volumegroupsnapshotclasses,verbs=get;list;watch
//+kubebuilder:rbac:groups=groupsnapshot.storage.k8s.io,resources=volumegroupsnapshotcontents,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=volsync.backube,resources=replicationsources,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=snapshot.storage.k8s.io,resources=volumesnapshots,verbs=get;list;watch
//+kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get

func (r *ReplicationGroupSourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Get ReplicationGroupSource")

	rgs := &ramendrv1alpha1.ReplicationGroupSource{}
	if err := r.Client.Get(ctx, req.NamespacedName, rgs); err != nil {
		if !errors.IsNotFound(err) {
			logger.Error(err, "Failed to get ReplicationGroupSource")
		}

		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.Info("Get ramen config from configmap")

	_, ramenConfig, err := ConfigMapGet(ctx, r.Client)
	if err != nil {
		logger.Error(err, "Failed to get ramen config")

		return ctrl.Result{}, err
	}

	defaultCephFSCSIDriverName := cephFSCSIDriverNameOrDefault(ramenConfig)

	logger.Info("Run ReplicationGroupSource state machine", "DefaultCephFSCSIDriverName", defaultCephFSCSIDriverName)
	result, err := statemachine.Run(
		ctx,
		cephfscg.NewRGSMachine(r.Client, rgs,
			volsync.NewVSHandler(ctx, r.Client, logger, rgs,
				&ramendrv1alpha1.VRGAsyncSpec{}, defaultCephFSCSIDriverName,
				volSyncDestinationCopyMethodOrDefault(ramenConfig), false,
			),
			cephfscg.NewVolumeGroupSourceHandler(r.Client, rgs, defaultCephFSCSIDriverName, logger),
			logger,
		),
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
func (r *ReplicationGroupSourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(ctrlcontroller.Options{
			MaxConcurrentReconciles: getMaxConcurrentReconciles(ctrl.Log),
		}).
		Owns(&vgsv1alphfa1.VolumeGroupSnapshot{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&volsyncv1alpha1.ReplicationSource{}).
		For(&ramendrv1alpha1.ReplicationGroupSource{}).
		Complete(r)
}
