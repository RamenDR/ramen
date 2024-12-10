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
	"github.com/google/uuid"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/core"
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
	log := r.Log.WithValues("name", req.NamespacedName.Name, "rid", uuid.New())
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
	if err := r.pruneClusterClaims(ctx, log, []string{}); err != nil {
		log.Info("Reconcile error", "error", err)

		return ctrl.Result{Requeue: true}, err
	}

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

// processCreateOrUpdate protects the resource with a finalizer and creates ClusterClaims for various storage related
// classes in the cluster. It would finally prune stale ClusterClaims from previous reconciliations.
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

	allSurvivors, err := r.CreateClassClaims(ctx, log)
	if err != nil {
		log.Info("Reconcile error", "error", err)
		setDRClusterConfigConfigurationProcessedCondition(&drCConfig.Status.Conditions, drCConfig.Generation,
			err.Error(), metav1.ConditionFalse, DRClusterConfigConditionConfigurationFailed)

		return ctrl.Result{Requeue: true}, err
	}

	if err := r.pruneClusterClaims(ctx, log, allSurvivors); err != nil {
		log.Info("Reconcile error", "error", err)
		setDRClusterConfigConfigurationProcessedCondition(&drCConfig.Status.Conditions, drCConfig.Generation,
			err.Error(), metav1.ConditionFalse, DRClusterConfigConditionConfigurationFailed)

		return ctrl.Result{Requeue: true}, err
	}

	setDRClusterConfigConfigurationProcessedCondition(&drCConfig.Status.Conditions, drCConfig.Generation,
		"Configuration processed and validated", metav1.ConditionTrue, DRClusterConfigConditionConfigurationProcessed)

	return ctrl.Result{}, nil
}

// CreateClassClaims creates cluster claims for various storage related classes of interest
func (r *DRClusterConfigReconciler) CreateClassClaims(ctx context.Context, log logr.Logger) ([]string, error) {
	allSurvivors := []string{}

	survivors, err := r.createSCClusterClaims(ctx, log)
	if err != nil {
		return nil, err
	}

	allSurvivors = append(allSurvivors, survivors...)

	survivors, err = r.createVSCClusterClaims(ctx, log)
	if err != nil {
		return nil, err
	}

	allSurvivors = append(allSurvivors, survivors...)

	survivors, err = r.createVRCClusterClaims(ctx, log)
	if err != nil {
		return nil, err
	}

	allSurvivors = append(allSurvivors, survivors...)

	return allSurvivors, nil
}

// createSCClusterClaims lists StorageClasses and creates ClusterClaims for ones marked for ramen
func (r *DRClusterConfigReconciler) createSCClusterClaims(
	ctx context.Context, log logr.Logger,
) ([]string, error) {
	claims := []string{}

	sClasses := &storagev1.StorageClassList{}
	if err := r.Client.List(ctx, sClasses); err != nil {
		return nil, fmt.Errorf("failed to list StorageClasses, %w", err)
	}

	for i := range sClasses.Items {
		if !util.HasLabel(&sClasses.Items[i], StorageIDLabel) {
			continue
		}

		if err := r.ensureClusterClaim(ctx, log, util.CCSCPrefix, sClasses.Items[i].GetName()); err != nil {
			return nil, err
		}

		claims = append(claims, claimName(util.CCSCPrefix, sClasses.Items[i].GetName()))
	}

	return claims, nil
}

// createVSCClusterClaims lists VolumeSnapshotClasses and creates ClusterClaims for ones marked for ramen
func (r *DRClusterConfigReconciler) createVSCClusterClaims(
	ctx context.Context, log logr.Logger,
) ([]string, error) {
	claims := []string{}

	vsClasses := &snapv1.VolumeSnapshotClassList{}
	if err := r.Client.List(ctx, vsClasses); err != nil {
		return nil, fmt.Errorf("failed to list VolumeSnapshotClasses, %w", err)
	}

	for i := range vsClasses.Items {
		if !util.HasLabel(&vsClasses.Items[i], StorageIDLabel) {
			continue
		}

		if err := r.ensureClusterClaim(ctx, log, util.CCVSCPrefix, vsClasses.Items[i].GetName()); err != nil {
			return nil, err
		}

		claims = append(claims, claimName(util.CCVSCPrefix, vsClasses.Items[i].GetName()))
	}

	return claims, nil
}

// createVRCClusterClaims lists VolumeReplicationClasses and creates ClusterClaims for ones marked for ramen
func (r *DRClusterConfigReconciler) createVRCClusterClaims(
	ctx context.Context, log logr.Logger,
) ([]string, error) {
	claims := []string{}

	vrClasses := &volrep.VolumeReplicationClassList{}
	if err := r.Client.List(ctx, vrClasses); err != nil {
		return nil, fmt.Errorf("failed to list VolumeReplicationClasses, %w", err)
	}

	for i := range vrClasses.Items {
		if !util.HasLabel(&vrClasses.Items[i], VolumeReplicationIDLabel) {
			continue
		}

		if err := r.ensureClusterClaim(ctx, log, util.CCVRCPrefix, vrClasses.Items[i].GetName()); err != nil {
			return nil, err
		}

		claims = append(claims, claimName(util.CCVRCPrefix, vrClasses.Items[i].GetName()))
	}

	return claims, nil
}

// ensureClusterClaim is a generic ClusterClaim creation function, that creates a claim named "prefix.name", with
// the passed in name as the ClusterClaim spec.Value
func (r *DRClusterConfigReconciler) ensureClusterClaim(
	ctx context.Context,
	log logr.Logger,
	prefix, name string,
) error {
	cc := &clusterv1alpha1.ClusterClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: claimName(prefix, name),
		},
	}

	core.ObjectCreatedByRamenSetLabel(cc)

	if _, err := ctrl.CreateOrUpdate(ctx, r.Client, cc, func() error {
		util.NewResourceUpdater(cc).AddLabel(drCConfigOwnerLabel, drCConfigOwnerName)

		cc.Spec.Value = name

		return nil
	}); err != nil {
		return fmt.Errorf("failed to create or update ClusterClaim %s, %w", claimName(prefix, name), err)
	}

	log.Info("Created ClusterClaim", "claimName", cc.GetName())

	return nil
}

func claimName(prefix, name string) string {
	return prefix + "." + name
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
		Complete(r)
}
