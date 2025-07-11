// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"time"

	volrep "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	groupsnapv1beta1 "github.com/red-hat-storage/external-snapshotter/client/v8/apis/volumegroupsnapshot/v1beta1"
	"golang.org/x/time/rate"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	clusterv1alpha1 "open-cluster-management.io/api/cluster/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/util"
)

const (
	drCConfigFinalizerName = "drclusterconfigs.ramendr.openshift.io/finalizer"
	drCConfigOwnerLabel    = "drclusterconfigs.ramendr.openshift.io/owner"
	drCConfigOwnerName     = "ramen"

	maxReconcileBackoff = 5 * time.Minute
)

// DRClusterConfig condition reasons
const (
	DRClusterConfigConditionReasonInitializing = "Initializing"

	DRClusterConfigConditionConfigurationProcessed = "Succeeded"
	DRClusterConfigConditionConfigurationFailed    = "Failed"

	DRClusterConfigS3Reachable   = "Reachable"
	DRClusterConfigS3Unreachable = "Unreachable"
)

// DRClusterConfigReconciler reconciles a DRClusterConfig object
type DRClusterConfigReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Log         logr.Logger
	RateLimiter *workqueue.TypedRateLimiter[reconcile.Request]
}

//nolint:lll
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drclusterconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drclusterconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drclusterconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=snapshot.storage.k8s.io,resources=volumesnapshotclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=replication.storage.openshift.io,resources=volumereplicationclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=clusterclaims,verbs=get;list;watch;create;update;delete

func (r *DRClusterConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("drcc", req.NamespacedName.Name, "rid", util.GetRID())
	log.Info("reconcile enter")

	defer log.Info("reconcile exit")

	drCConfig := &ramen.DRClusterConfig{}
	if err := r.Client.Get(ctx, req.NamespacedName, drCConfig); err != nil {
		log.Info("Reconcile error", "error", err)

		return ctrl.Result{}, client.IgnoreNotFound(fmt.Errorf("get: %w", err))
	}

	// Ensure there is ony one DRClusterConfig for the cluster
	if _, err := r.GetDRClusterConfig(ctx); err != nil {
		log.Info("Reconcile error", "error", err)

		return ctrl.Result{}, err
	}

	// save status prior to update and do deepEqual pre returning from processing funcs (in each ones' status.update())
	savedDRCConfigStatus := &ramen.DRClusterConfigStatus{}
	drCConfig.Status.DeepCopyInto(savedDRCConfigStatus)

	if savedDRCConfigStatus.Conditions == nil {
		savedDRCConfigStatus.Conditions = []metav1.Condition{}
	}

	if drCConfig.Status.Conditions == nil {
		// Set the DRClusterConfig conditions to unknown as nothing is known at this point
		msg := "Initializing DRClusterConfig"
		setDRClusterConfigInitialCondition(&drCConfig.Status.Conditions, drCConfig.Generation, msg)
	}

	var (
		res ctrl.Result
		err error
	)

	if util.ResourceIsDeleted(drCConfig) {
		res, err = r.processDeletion(ctx, log, drCConfig)
	} else {
		res, err = r.processCreateOrUpdate(ctx, log, drCConfig)

		// Update status
		if err := r.statusUpdate(ctx, drCConfig, savedDRCConfigStatus); err != nil {
			r.Log.Info("failed to update status", "failure", err)
		}
	}

	return res, err
}

func (r *DRClusterConfigReconciler) statusUpdate(ctx context.Context, obj *ramen.DRClusterConfig,
	savedStatus *ramen.DRClusterConfigStatus,
) error {
	if !reflect.DeepEqual(obj.Status, savedStatus) {
		if err := r.Client.Status().Update(ctx, obj); err != nil {
			r.Log.Info("Failed to update drClusterConfig status", "name", obj.Name, "namespace", obj.Namespace,
				"error", err)

			return fmt.Errorf("failed to update drClusterConfig status (%s/%s)", obj.Name, obj.Namespace)
		}
	}

	return nil
}

func setDRClusterConfigInitialCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	util.SetStatusConditionIfNotFound(conditions, metav1.Condition{
		Type:               ramen.DRClusterConfigConfigurationProcessed,
		Reason:             DRClusterConfigConditionReasonInitializing,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionUnknown,
		Message:            message,
	})
	util.SetStatusConditionIfNotFound(conditions, metav1.Condition{
		Type:               ramen.DRClusterConfigS3Reachable,
		Reason:             DRClusterConfigConditionReasonInitializing,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionUnknown,
		Message:            message,
	})
}

func setDRClusterConfigConfigurationProcessedCondition(conditions *[]metav1.Condition, observedGeneration int64,
	message string, conditionStatus metav1.ConditionStatus, reason string,
) {
	util.SetStatusCondition(conditions, metav1.Condition{
		Type:               ramen.DRClusterConfigConfigurationProcessed,
		Reason:             reason,
		ObservedGeneration: observedGeneration,
		Status:             conditionStatus,
		Message:            message,
	})
}

func (r *DRClusterConfigReconciler) GetDRClusterConfig(ctx context.Context) (*ramen.DRClusterConfig, error) {
	drcConfigs := &ramen.DRClusterConfigList{}
	if err := r.Client.List(ctx, drcConfigs); err != nil {
		return nil, fmt.Errorf("failed to list DRClusterConfig, %w", err)
	}

	if len(drcConfigs.Items) == 0 {
		return nil, fmt.Errorf("failed to find DRClusterConfig")
	}

	if len(drcConfigs.Items) > 1 {
		return nil, fmt.Errorf("multiple DRClusterConfigs found")
	}

	return &drcConfigs.Items[0], nil
}

// processDeletion ensures all cluster claims created by drClusterConfig are deleted, before removing the finalizer on
// the resource itself
func (r *DRClusterConfigReconciler) processDeletion(
	ctx context.Context,
	log logr.Logger,
	drCConfig *ramen.DRClusterConfig,
) (ctrl.Result, error) {
	if err := util.NewResourceUpdater(drCConfig).
		RemoveFinalizer(drCConfigFinalizerName).
		Update(ctx, r.Client); err != nil {
		log.Info("Reconcile error", "error", err)

		return ctrl.Result{Requeue: true},
			fmt.Errorf("failed to remove finalizer for DRClusterConfig resource, %w", err)
	}

	return ctrl.Result{}, nil
}

// pruneClusterClaims will prune all ClusterClaims created by drClusterConfig that are not in the
// passed in survivor list
func (r *DRClusterConfigReconciler) pruneClusterClaims(ctx context.Context, log logr.Logger, survivors []string) error {
	matchLabels := map[string]string{
		drCConfigOwnerLabel: drCConfigOwnerName,
	}

	listOptions := []client.ListOption{
		client.MatchingLabels(matchLabels),
	}

	claims := &clusterv1alpha1.ClusterClaimList{}
	if err := r.Client.List(ctx, claims, listOptions...); err != nil {
		return fmt.Errorf("failed to list ClusterClaims, %w", err)
	}

	for idx := range claims.Items {
		if slices.Contains(survivors, claims.Items[idx].GetName()) {
			continue
		}

		if err := r.Client.Delete(ctx, &claims.Items[idx]); err != nil {
			return fmt.Errorf("failed to delete ClusterClaim %s, %w", claims.Items[idx].GetName(), err)
		}

		log.Info("Pruned ClusterClaim", "claimName", claims.Items[idx].GetName())
	}

	return nil
}

// processCreateOrUpdate protects the resource with a finalizer and updates DRClusterConfig for various storage related
// classes in the cluster. It would finally prune stale ClusterClaims from previous reconciliations, to cleanup upgraded
// clusters which had OCM based claims created for the same.
func (r *DRClusterConfigReconciler) processCreateOrUpdate(
	ctx context.Context,
	log logr.Logger,
	drCConfig *ramen.DRClusterConfig,
) (ctrl.Result, error) {
	if err := util.NewResourceUpdater(drCConfig).
		AddFinalizer(drCConfigFinalizerName).
		Update(ctx, r.Client); err != nil {
		log.Info("Reconcile error", "error", err)
		setDRClusterConfigConfigurationProcessedCondition(&drCConfig.Status.Conditions, drCConfig.Generation,
			err.Error(), metav1.ConditionFalse, DRClusterConfigConditionConfigurationFailed)

		return ctrl.Result{Requeue: true}, fmt.Errorf("failed to add finalizer for DRClusterConfig resource, %w", err)
	}

	err := r.UpdateSupportedClasses(ctx, drCConfig)
	if err != nil {
		log.Info("Reconcile error", "error", err)
		setDRClusterConfigConfigurationProcessedCondition(&drCConfig.Status.Conditions, drCConfig.Generation,
			err.Error(), metav1.ConditionFalse, DRClusterConfigConditionConfigurationFailed)

		return ctrl.Result{Requeue: true}, err
	}

	// As an earlier version is out with ClusterClaims, ensure we prune all claims going forward to address orphaned
	// claims due to upgrades.
	if err := r.pruneClusterClaims(ctx, log, []string{}); err != nil {
		log.Info("Reconcile error", "error", err)
		setDRClusterConfigConfigurationProcessedCondition(&drCConfig.Status.Conditions, drCConfig.Generation,
			err.Error(), metav1.ConditionFalse, DRClusterConfigConditionConfigurationFailed)

		return ctrl.Result{Requeue: true}, err
	}

	setDRClusterConfigConfigurationProcessedCondition(&drCConfig.Status.Conditions, drCConfig.Generation,
		"Configuration processed and validated", metav1.ConditionTrue, DRClusterConfigConditionConfigurationProcessed)

	return ctrl.Result{}, nil
}

// UpdateSupportedClasses updates DRClusterConfig status with a list of storage related classes that are marked for DR
// support. The list is sorted alphabetically to avoid out of order listing and status updates due to the same
func (r *DRClusterConfigReconciler) UpdateSupportedClasses(
	ctx context.Context,
	drCConfig *ramen.DRClusterConfig,
) error {
	sClasses, err := r.listDRSupportedSCs(ctx)
	if err != nil {
		return err
	}

	drCConfig.Status.StorageClasses = sClasses
	slices.Sort(drCConfig.Status.StorageClasses)

	vsClasses, err := r.listDRSupportedVSCs(ctx)
	if err != nil {
		return err
	}

	drCConfig.Status.VolumeSnapshotClasses = vsClasses
	slices.Sort(drCConfig.Status.VolumeSnapshotClasses)

	vrClasses, err := r.listDRSupportedVRCs(ctx)
	if err != nil {
		return err
	}

	drCConfig.Status.VolumeReplicationClasses = vrClasses
	slices.Sort(drCConfig.Status.VolumeReplicationClasses)

	vgrClasses, err := r.listDRSupportedVGRCs(ctx)
	if err != nil {
		return err
	}

	drCConfig.Status.VolumeGroupReplicationClasses = vgrClasses
	slices.Sort(drCConfig.Status.VolumeGroupReplicationClasses)

	vgsClasses, err := r.listDRSupportedVGSCs(ctx)
	if err != nil {
		return err
	}

	drCConfig.Status.VolumeGroupSnapshotClasses = vgsClasses
	slices.Sort(drCConfig.Status.VolumeGroupSnapshotClasses)

	return nil
}

// listDRSupportedSCs returns a list of StorageClasses that are marked as DR supported
func (r *DRClusterConfigReconciler) listDRSupportedSCs(ctx context.Context) ([]string, error) {
	scs := []string{}

	sClasses := &storagev1.StorageClassList{}
	if err := r.Client.List(ctx, sClasses); err != nil {
		return nil, fmt.Errorf("failed to list StorageClasses, %w", err)
	}

	for i := range sClasses.Items {
		if !util.HasLabel(&sClasses.Items[i], StorageIDLabel) {
			continue
		}

		scs = append(scs, sClasses.Items[i].Name)
	}

	return scs, nil
}

// listDRSupportedVSCs returns a list of VolumeSnapshotClasses that are marked as DR supported
func (r *DRClusterConfigReconciler) listDRSupportedVSCs(ctx context.Context) ([]string, error) {
	vscs := []string{}

	vsClasses := &snapv1.VolumeSnapshotClassList{}
	if err := r.Client.List(ctx, vsClasses); err != nil {
		return nil, fmt.Errorf("failed to list VolumeSnapshotClasses, %w", err)
	}

	for i := range vsClasses.Items {
		if !util.HasLabel(&vsClasses.Items[i], StorageIDLabel) {
			continue
		}

		vscs = append(vscs, vsClasses.Items[i].Name)
	}

	return vscs, nil
}

// listDRSupportedVRCs returns a list of VolumeReplicationClasses that are marked as DR supported
func (r *DRClusterConfigReconciler) listDRSupportedVRCs(ctx context.Context) ([]string, error) {
	vrcs := []string{}

	vrClasses := &volrep.VolumeReplicationClassList{}
	if err := r.Client.List(ctx, vrClasses); err != nil {
		return nil, fmt.Errorf("failed to list VolumeReplicationClasses, %w", err)
	}

	for i := range vrClasses.Items {
		if !util.HasLabel(&vrClasses.Items[i], ReplicationIDLabel) {
			continue
		}

		vrcs = append(vrcs, vrClasses.Items[i].Name)
	}

	return vrcs, nil
}

// listDRSupportedVGRCs returns a list of VolumeGroupReplicationClasses that are marked as DR supported
func (r *DRClusterConfigReconciler) listDRSupportedVGRCs(ctx context.Context) ([]string, error) {
	vgrcs := []string{}

	vgrClasses := &volrep.VolumeGroupReplicationClassList{}
	if err := r.Client.List(ctx, vgrClasses); err != nil {
		return nil, fmt.Errorf("failed to list VolumeGroupReplicationClasses, %w", err)
	}

	for i := range vgrClasses.Items {
		if !util.HasLabel(&vgrClasses.Items[i], ReplicationIDLabel) {
			continue
		}

		vgrcs = append(vgrcs, vgrClasses.Items[i].Name)
	}

	return vgrcs, nil
}

// listDRSupportedVGSCs returns a list of VolumeGroupSnapshotClasses that are marked as DR supported
func (r *DRClusterConfigReconciler) listDRSupportedVGSCs(ctx context.Context) ([]string, error) {
	vgscs := []string{}

	vgsClasses := &groupsnapv1beta1.VolumeGroupSnapshotClassList{}
	if err := r.Client.List(ctx, vgsClasses); err != nil {
		return nil, fmt.Errorf("failed to list VolumeGroupSnapshotClasses, %w", err)
	}

	for i := range vgsClasses.Items {
		if !util.HasLabel(&vgsClasses.Items[i], StorageIDLabel) {
			continue
		}

		vgscs = append(vgscs, vgsClasses.Items[i].Name)
	}

	return vgscs, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DRClusterConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	drccMapFn := handler.EnqueueRequestsFromMapFunc(handler.MapFunc(
		func(ctx context.Context, obj client.Object) []reconcile.Request {
			drcConfig, err := r.GetDRClusterConfig(ctx)
			if err != nil {
				ctrl.Log.Info(fmt.Sprintf("failed processing DRClusterConfig mapping, %v", err))

				return []ctrl.Request{}
			}

			return []ctrl.Request{
				reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name: drcConfig.GetName(),
					},
				},
			}
		}),
	)

	drccPredFn := builder.WithPredicates(predicate.NewPredicateFuncs(
		func(object client.Object) bool {
			return true
		}),
	)

	rateLimiter := workqueue.NewTypedMaxOfRateLimiter(
		workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](1*time.Second, maxReconcileBackoff),
		// defaults from client-go
		//nolint:mnd
		&workqueue.TypedBucketRateLimiter[reconcile.Request]{Limiter: rate.NewLimiter(rate.Limit(10), 100)},
	)

	if r.RateLimiter != nil {
		rateLimiter = *r.RateLimiter
	}

	controller := ctrl.NewControllerManagedBy(mgr)

	return controller.WithOptions(ctrlcontroller.Options{
		RateLimiter: rateLimiter,
	}).For(&ramen.DRClusterConfig{}).
		Watches(&storagev1.StorageClass{}, drccMapFn, drccPredFn).
		Watches(&snapv1.VolumeSnapshotClass{}, drccMapFn, drccPredFn).
		Watches(&volrep.VolumeReplicationClass{}, drccMapFn, drccPredFn).
		Watches(&volrep.VolumeGroupReplicationClass{}, drccMapFn, drccPredFn).
		Watches(&groupsnapv1beta1.VolumeGroupSnapshotClass{}, drccMapFn, drccPredFn).
		Complete(r)
}
