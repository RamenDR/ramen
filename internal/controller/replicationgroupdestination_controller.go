// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	"github.com/backube/volsync/controllers/statemachine"
	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/cephfscg"
	"github.com/ramendr/ramen/internal/controller/util"
	"github.com/ramendr/ramen/internal/controller/volsync"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ReplicationGroupDestinationReconciler reconciles a ReplicationGroupDestination object
type ReplicationGroupDestinationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=ramendr.openshift.io,resources=replicationgroupdestinations,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ramendr.openshift.io,resources=replicationgroupdestinations/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ramendr.openshift.io,resources=replicationgroupdestinations/finalizers,verbs=update
//+kubebuilder:rbac:groups=volsync.backube,resources=replicationdestinations,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=snapshot.storage.k8s.io,resources=volumesnapshots,verbs=get;list;watch;delete

func (r *ReplicationGroupDestinationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Get ReplicationGroupDestination")

	rgd := &ramendrv1alpha1.ReplicationGroupDestination{}
	if err := r.Client.Get(ctx, req.NamespacedName, rgd); err != nil {
		if !errors.IsNotFound(err) {
			logger.Error(err, "Failed to get ReplicationGroupDestination")
		}

		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.Info("Get vrg from ReplicationGroupDestination")

	vrg := &ramendrv1alpha1.VolumeReplicationGroup{}
	if err := r.Client.Get(ctx, types.NamespacedName{
		Name:      rgd.GetLabels()[volsync.VRGOwnerNameLabel],
		Namespace: rgd.GetLabels()[volsync.VRGOwnerNamespaceLabel],
	}, vrg); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("Get ramen config from configmap")

	_, ramenConfig, err := ConfigMapGet(ctx, r.Client)
	if err != nil {
		logger.Error(err, "Failed to get ramen config")

		return ctrl.Result{}, err
	}

	defaultCephFSCSIDriverName := cephFSCSIDriverNameOrDefault(ramenConfig)

	logger.Info("Run ReplicationGroupDestination state machine", "DefaultCephFSCSIDriverName", defaultCephFSCSIDriverName)
	result, err := statemachine.Run(
		ctx,
		cephfscg.NewRGDMachine(r.Client, rgd,
			volsync.NewVSHandler(ctx, r.Client, logger, vrg,
				&ramendrv1alpha1.VRGAsyncSpec{
					VolumeSnapshotClassSelector: rgd.Spec.VolumeSnapshotClassSelector,
				}, defaultCephFSCSIDriverName, volSyncDestinationCopyMethodOrDefault(ramenConfig), false,
			),
			logger,
		),
		logger,
	)
	// Update instance status
	statusErr := r.Client.Status().Update(ctx, rgd)

	if err == nil { // Don't mask previous error
		err = statusErr
	}

	if err != nil {
		logger.Error(err, "Failed to reconcile ReplicationGroupDestination")
	}

	return result, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *ReplicationGroupDestinationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	vsMapFun := handler.EnqueueRequestsFromMapFunc(handler.MapFunc(
		func(ctx context.Context, obj client.Object) []reconcile.Request {
			if rgdName, ok := obj.GetLabels()[util.RGDOwnerLabel]; ok {
				return []reconcile.Request{{NamespacedName: types.NamespacedName{
					Name:      rgdName,
					Namespace: obj.GetNamespace(),
				}}}
			}

			return []reconcile.Request{}
		}))

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(ctrlcontroller.Options{
			MaxConcurrentReconciles: getMaxConcurrentReconciles(ctrl.Log),
		}).
		Owns(&volsyncv1alpha1.ReplicationDestination{}).
		For(&ramendrv1alpha1.ReplicationGroupDestination{}).
		Watches(&snapv1.VolumeSnapshot{}, vsMapFun, builder.WithPredicates(predicate.NewPredicateFuncs(
			func(object client.Object) bool {
				return object.GetLabels()[util.RGDOwnerLabel] != ""
			},
		))).
		Complete(r)
}
