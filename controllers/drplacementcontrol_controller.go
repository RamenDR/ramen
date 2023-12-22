// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	errorswrapper "github.com/pkg/errors"
	viewv1beta1 "github.com/stolostron/multicloud-operators-foundation/pkg/apis/view/v1beta1"
	plrv1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	argov1alpha1 "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	clrapiv1beta1 "github.com/open-cluster-management-io/api/cluster/v1beta1"
	rmn "github.com/ramendr/ramen/api/v1alpha1"
	rmnutil "github.com/ramendr/ramen/controllers/util"
	"github.com/ramendr/ramen/controllers/volsync"
)

const (
	// DRPC CR finalizer
	DRPCFinalizer string = "drpc.ramendr.openshift.io/finalizer"

	// Ramen scheduler
	RamenScheduler string = "ramen"

	ClonedPlacementRuleNameFormat string = "drpc-plrule-%s-%s"

	// StatusCheckDelay is used to frequencly update the DRPC status when the reconciler is idle.
	// This is needed in order to sync up the DRPC status and the VRG status.
	StatusCheckDelay = time.Minute * 10

	// PlacementDecisionName format, prefix is the Placement name, and suffix is a PlacementDecision index
	PlacementDecisionName = "%s-decision-%d"

	// Maximum retries to create PlacementDecisionName with an increasing index in case of conflicts
	// with existing PlacementDecision resources
	MaxPlacementDecisionConflictCount = 5

	DestinationClusterAnnotationKey = "drplacementcontrol.ramendr.openshift.io/destination-cluster"
)

var InitialWaitTimeForDRPCPlacementRule = errorswrapper.New("Waiting for DRPC Placement to produces placement decision")

// ProgressCallback of function type
type ProgressCallback func(string, string)

// DRPlacementControlReconciler reconciles a DRPlacementControl object
type DRPlacementControlReconciler struct {
	client.Client
	APIReader           client.Reader
	Log                 logr.Logger
	MCVGetter           rmnutil.ManagedClusterViewGetter
	Scheme              *runtime.Scheme
	Callback            ProgressCallback
	eventRecorder       *rmnutil.EventReporter
	savedInstanceStatus rmn.DRPlacementControlStatus
	ObjStoreGetter      ObjectStoreGetter
}

func ManifestWorkPredicateFunc() predicate.Funcs {
	mwPredicate := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			log := ctrl.Log.WithName("Predicate").WithName("ManifestWork")

			oldMW, ok := e.ObjectOld.DeepCopyObject().(*ocmworkv1.ManifestWork)
			if !ok {
				log.Info("Failed to deep copy older ManifestWork")

				return false
			}
			newMW, ok := e.ObjectNew.DeepCopyObject().(*ocmworkv1.ManifestWork)
			if !ok {
				log.Info("Failed to deep copy newer ManifestWork")

				return false
			}

			log.Info(fmt.Sprintf("Update event for MW %s/%s", oldMW.Name, oldMW.Namespace))

			return !reflect.DeepEqual(oldMW.Status, newMW.Status)
		},
	}

	return mwPredicate
}

func filterMW(mw *ocmworkv1.ManifestWork) []ctrl.Request {
	if mw.Annotations[DRPCNameAnnotation] == "" ||
		mw.Annotations[DRPCNamespaceAnnotation] == "" {
		return []ctrl.Request{}
	}

	return []ctrl.Request{
		reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      mw.Annotations[DRPCNameAnnotation],
				Namespace: mw.Annotations[DRPCNamespaceAnnotation],
			},
		},
	}
}

func ManagedClusterViewPredicateFunc() predicate.Funcs {
	log := ctrl.Log.WithName("Predicate").WithName("MCV")
	mcvPredicate := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldMCV, ok := e.ObjectOld.DeepCopyObject().(*viewv1beta1.ManagedClusterView)
			if !ok {
				log.Info("Failed to deep copy older MCV")

				return false
			}
			newMCV, ok := e.ObjectNew.DeepCopyObject().(*viewv1beta1.ManagedClusterView)
			if !ok {
				log.Info("Failed to deep copy newer MCV")

				return false
			}

			log.Info(fmt.Sprintf("Update event for MCV %s/%s", oldMCV.Name, oldMCV.Namespace))

			return !reflect.DeepEqual(oldMCV.Status, newMCV.Status)
		},
	}

	return mcvPredicate
}

func filterMCV(mcv *viewv1beta1.ManagedClusterView) []ctrl.Request {
	if mcv.Annotations[DRPCNameAnnotation] == "" ||
		mcv.Annotations[DRPCNamespaceAnnotation] == "" {
		return []ctrl.Request{}
	}

	return []ctrl.Request{
		reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      mcv.Annotations[DRPCNameAnnotation],
				Namespace: mcv.Annotations[DRPCNamespaceAnnotation],
			},
		},
	}
}

func PlacementRulePredicateFunc() predicate.Funcs {
	log := ctrl.Log.WithName("DRPCPredicate").WithName("UserPlRule")
	usrPlRulePredicate := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			log.Info("Delete event")

			return true
		},
	}

	return usrPlRulePredicate
}

func filterUsrPlRule(usrPlRule *plrv1.PlacementRule) []ctrl.Request {
	if usrPlRule.Annotations[DRPCNameAnnotation] == "" ||
		usrPlRule.Annotations[DRPCNamespaceAnnotation] == "" {
		return []ctrl.Request{}
	}

	return []ctrl.Request{
		reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      usrPlRule.Annotations[DRPCNameAnnotation],
				Namespace: usrPlRule.Annotations[DRPCNamespaceAnnotation],
			},
		},
	}
}

func PlacementPredicateFunc() predicate.Funcs {
	log := ctrl.Log.WithName("DRPCPredicate").WithName("UserPlmnt")
	usrPlmntPredicate := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			log.Info("Delete event")

			return true
		},
	}

	return usrPlmntPredicate
}

func filterUsrPlmnt(usrPlmnt *clrapiv1beta1.Placement) []ctrl.Request {
	if usrPlmnt.Annotations[DRPCNameAnnotation] == "" ||
		usrPlmnt.Annotations[DRPCNamespaceAnnotation] == "" {
		return []ctrl.Request{}
	}

	return []ctrl.Request{
		reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      usrPlmnt.Annotations[DRPCNameAnnotation],
				Namespace: usrPlmnt.Annotations[DRPCNamespaceAnnotation],
			},
		},
	}
}

func DRClusterPredicateFunc() predicate.Funcs {
	log := ctrl.Log.WithName("DRPCPredicate").WithName("DRCluster")
	drClusterPredicate := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			log.Info("Update event")

			return DRClusterUpdateOfInterest(e.ObjectOld.(*rmn.DRCluster), e.ObjectNew.(*rmn.DRCluster))
		},
	}

	return drClusterPredicate
}

// DRClusterUpdateOfInterest checks if the new DRCluster resource as compared to the older version
// requires any attention, it checks for the following updates:
//   - If any maintenance mode is reported as activated
//
// TODO: Needs some logs for easier troubleshooting
func DRClusterUpdateOfInterest(oldDRCluster, newDRCluster *rmn.DRCluster) bool {
	for _, mModeNew := range newDRCluster.Status.MaintenanceModes {
		// Check if new conditions have failover activated, if not this maintenance mode is NOT of interest
		conditionNew := getFailoverActivatedCondition(mModeNew)
		if conditionNew == nil ||
			conditionNew.Status == metav1.ConditionFalse ||
			conditionNew.Status == metav1.ConditionUnknown {
			continue
		}

		// Check if failover maintenance mode was already activated as part of an older update to DRCluster, if NOT
		// this change is of interest
		if activated := checkFailoverActivation(oldDRCluster, mModeNew.StorageProvisioner, mModeNew.TargetID); !activated {
			return true
		}
	}

	// Exhausted all failover activation checks, this update is NOT of interest
	return false
}

// checkFailoverActivation checks if provided provisioner and storage instance is activated as per the
// passed in DRCluster resource status. It currently only checks for:
//   - Failover activation condition
func checkFailoverActivation(drcluster *rmn.DRCluster, provisioner string, targetID string) bool {
	for _, mMode := range drcluster.Status.MaintenanceModes {
		if !(mMode.StorageProvisioner == provisioner && mMode.TargetID == targetID) {
			continue
		}

		condition := getFailoverActivatedCondition(mMode)
		if condition == nil ||
			condition.Status == metav1.ConditionFalse ||
			condition.Status == metav1.ConditionUnknown {
			return false
		}

		return true
	}

	return false
}

// getFailoverActivatedCondition is a helper routine that returns the FailoverActivated condition
// from a given ClusterMaintenanceMode if found, or nil otherwise
func getFailoverActivatedCondition(mMode rmn.ClusterMaintenanceMode) *metav1.Condition {
	for _, condition := range mMode.Conditions {
		if condition.Type != string(rmn.MModeConditionFailoverActivated) {
			continue
		}

		return &condition
	}

	return nil
}

// FilterDRCluster filters for DRPC resources that should be reconciled due to a DRCluster watch event
func (r *DRPlacementControlReconciler) FilterDRCluster(drcluster *rmn.DRCluster) []ctrl.Request {
	log := ctrl.Log.WithName("DRPCFilter").WithName("DRCluster").WithValues("cluster", drcluster)

	drpcCollections, err := DRPCsFailingOverToCluster(r.Client, log, drcluster.GetName())
	if err != nil {
		log.Info("Failed to process filter")

		return nil
	}

	requests := make([]reconcile.Request, 0)
	for idx := range drpcCollections {
		requests = append(requests,
			reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      drpcCollections[idx].drpc.GetName(),
					Namespace: drpcCollections[idx].drpc.GetNamespace(),
				},
			})
	}

	return requests
}

type DRPCAndPolicy struct {
	drpc     *rmn.DRPlacementControl
	drPolicy *rmn.DRPolicy
}

// DRPCsFailingOverToCluster lists DRPC resources that are failing over to the passed in drcluster
//
//nolint:gocognit
func DRPCsFailingOverToCluster(k8sclient client.Client, log logr.Logger, drcluster string) ([]DRPCAndPolicy, error) {
	drpolicies := &rmn.DRPolicyList{}
	if err := k8sclient.List(context.TODO(), drpolicies); err != nil {
		// TODO: If we get errors, do we still get an event later and/or for all changes from where we
		// processed the last DRCluster update?
		log.Error(err, "Failed to list DRPolicies")

		return nil, err
	}

	drpcCollections := make([]DRPCAndPolicy, 0)

	for drpolicyIdx, drpolicy := range drpolicies.Items {
		drClusters, err := getDRClusters(context.TODO(), k8sclient, &drpolicies.Items[drpolicyIdx])
		if err != nil || len(drClusters) <= 1 {
			log.Error(err, "Failed to get DRClusters")

			return nil, err
		}

		// Skip if policy is of type metro, fake the from and to cluster
		if isMetroAction(&drpolicies.Items[drpolicyIdx], drClusters, drClusters[0].GetName(), drClusters[1].GetName()) {
			log.Info("Sync DRPolicy detected, skipping!")

			continue
		}

		for _, specCluster := range drpolicy.Spec.DRClusters {
			if specCluster != drcluster {
				continue
			}

			log.Info("Processing DRPolicy referencing DRCluster", "drpolicy", drpolicy.GetName())

			drpcs, err := DRPCsFailingOverToClusterForPolicy(k8sclient, log, &drpolicies.Items[drpolicyIdx], drcluster)
			if err != nil {
				return nil, err
			}

			for idx := range drpcs {
				dprcCollection := DRPCAndPolicy{
					drpc:     drpcs[idx],
					drPolicy: &drpolicies.Items[drpolicyIdx],
				}

				drpcCollections = append(drpcCollections, dprcCollection)
			}

			break
		}
	}

	return drpcCollections, nil
}

// DRPCsFailingOverToClusterForPolicy filters DRPC resources that reference the DRPolicy and are failing over
// to the target cluster passed in
//
//nolint:gocognit
func DRPCsFailingOverToClusterForPolicy(
	k8sclient client.Client,
	log logr.Logger,
	drpolicy *rmn.DRPolicy,
	drcluster string,
) ([]*rmn.DRPlacementControl, error) {
	drpcs := &rmn.DRPlacementControlList{}
	if err := k8sclient.List(context.TODO(), drpcs); err != nil {
		log.Error(err, "Failed to list DRPCs", "drpolicy", drpolicy.GetName())

		return nil, err
	}

	filteredDRPCs := make([]*rmn.DRPlacementControl, 0)

	for idx, drpc := range drpcs.Items {
		if drpc.Spec.DRPolicyRef.Name != drpolicy.GetName() {
			continue
		}

		if !(drpc.Spec.Action == rmn.ActionFailover && drpc.Spec.FailoverCluster == drcluster) {
			continue
		}

		if condition := meta.FindStatusCondition(drpc.Status.Conditions, rmn.ConditionAvailable); condition != nil &&
			condition.Status == metav1.ConditionTrue &&
			condition.ObservedGeneration == drpc.Generation {
			continue
		}

		log.Info("DRPC detected as failing over to cluster",
			"name", drpc.GetName(),
			"namespace", drpc.GetNamespace(),
			"drpolicy", drpolicy.GetName())

		filteredDRPCs = append(filteredDRPCs, &drpcs.Items[idx])
	}

	return filteredDRPCs, nil
}

// SetupWithManager sets up the controller with the Manager.
//
//nolint:funlen
func (r *DRPlacementControlReconciler) SetupWithManager(mgr ctrl.Manager) error {
	mwPred := ManifestWorkPredicateFunc()

	mwMapFun := handler.EnqueueRequestsFromMapFunc(handler.MapFunc(func(obj client.Object) []reconcile.Request {
		mw, ok := obj.(*ocmworkv1.ManifestWork)
		if !ok {
			return []reconcile.Request{}
		}

		ctrl.Log.Info(fmt.Sprintf("DRPC: Filtering ManifestWork (%s/%s)", mw.Name, mw.Namespace))

		return filterMW(mw)
	}))

	mcvPred := ManagedClusterViewPredicateFunc()

	mcvMapFun := handler.EnqueueRequestsFromMapFunc(handler.MapFunc(func(obj client.Object) []reconcile.Request {
		mcv, ok := obj.(*viewv1beta1.ManagedClusterView)
		if !ok {
			return []reconcile.Request{}
		}

		ctrl.Log.Info(fmt.Sprintf("DRPC: Filtering MCV (%s/%s)", mcv.Name, mcv.Namespace))

		return filterMCV(mcv)
	}))

	usrPlRulePred := PlacementRulePredicateFunc()

	usrPlRuleMapFun := handler.EnqueueRequestsFromMapFunc(handler.MapFunc(func(obj client.Object) []reconcile.Request {
		usrPlRule, ok := obj.(*plrv1.PlacementRule)
		if !ok {
			return []reconcile.Request{}
		}

		ctrl.Log.Info(fmt.Sprintf("DRPC: Filtering User PlacementRule (%s/%s)", usrPlRule.Name, usrPlRule.Namespace))

		return filterUsrPlRule(usrPlRule)
	}))

	usrPlmntPred := PlacementPredicateFunc()

	usrPlmntMapFun := handler.EnqueueRequestsFromMapFunc(handler.MapFunc(func(obj client.Object) []reconcile.Request {
		usrPlmnt, ok := obj.(*clrapiv1beta1.Placement)
		if !ok {
			return []reconcile.Request{}
		}

		ctrl.Log.Info(fmt.Sprintf("DRPC: Filtering User Placement (%s/%s)", usrPlmnt.Name, usrPlmnt.Namespace))

		return filterUsrPlmnt(usrPlmnt)
	}))

	drClusterPred := DRClusterPredicateFunc()

	drClusterMapFun := handler.EnqueueRequestsFromMapFunc(handler.MapFunc(func(obj client.Object) []reconcile.Request {
		drCluster, ok := obj.(*rmn.DRCluster)
		if !ok {
			return []reconcile.Request{}
		}

		ctrl.Log.Info(fmt.Sprintf("DRPC Map: Filtering DRCluster (%s)", drCluster.Name))

		return r.FilterDRCluster(drCluster)
	}))

	r.eventRecorder = rmnutil.NewEventReporter(mgr.GetEventRecorderFor("controller_DRPlacementControl"))

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(ctrlcontroller.Options{MaxConcurrentReconciles: getMaxConcurrentReconciles(ctrl.Log)}).
		For(&rmn.DRPlacementControl{}).
		Watches(&source.Kind{Type: &ocmworkv1.ManifestWork{}}, mwMapFun, builder.WithPredicates(mwPred)).
		Watches(&source.Kind{Type: &viewv1beta1.ManagedClusterView{}}, mcvMapFun, builder.WithPredicates(mcvPred)).
		Watches(&source.Kind{Type: &plrv1.PlacementRule{}}, usrPlRuleMapFun, builder.WithPredicates(usrPlRulePred)).
		Watches(&source.Kind{Type: &clrapiv1beta1.Placement{}}, usrPlmntMapFun, builder.WithPredicates(usrPlmntPred)).
		Watches(&source.Kind{Type: &rmn.DRCluster{}}, drClusterMapFun, builder.WithPredicates(drClusterPred)).
		Complete(r)
}

//nolint:lll
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drplacementcontrols,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drplacementcontrols/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drplacementcontrols/finalizers,verbs=update
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drpolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps.open-cluster-management.io,resources=placementrules,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps.open-cluster-management.io,resources=placementrules/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps.open-cluster-management.io,resources=placementrules/finalizers,verbs=get;create;update;patch;delete
// +kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=managedclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=work.open-cluster-management.io,resources=manifestworks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=view.open-cluster-management.io,resources=managedclusterviews,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=placementbindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=get;create;patch;update
// +kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=placementdecisions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=placementdecisions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=placements,verbs=get;list;watch;update
// +kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=placements/finalizers,verbs=update
// +kubebuilder:rbac:groups=argoproj.io,resources=applicationsets,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the DRPlacementControl object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.7.0/pkg/reconcile
//
//nolint:funlen,gocognit,gocyclo,cyclop
func (r *DRPlacementControlReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.Log.WithValues("DRPC", req.NamespacedName, "rid", uuid.New())

	logger.Info("Entering reconcile loop")
	defer logger.Info("Exiting reconcile loop")

	drpc := &rmn.DRPlacementControl{}

	err := r.APIReader.Get(ctx, req.NamespacedName, drpc)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info(fmt.Sprintf("DRPC object not found %v", req.NamespacedName))
			// Request object not found, could have been deleted after reconcile request.
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, errorswrapper.Wrap(err, "failed to get DRPC object")
	}

	// Save a copy of the instance status to be used for the VRG status update comparison
	drpc.Status.DeepCopyInto(&r.savedInstanceStatus)

	ensureDRPCConditionsInited(&drpc.Status.Conditions, drpc.Generation, "Initialization")

	placementObj, err := getPlacementOrPlacementRule(ctx, r.Client, drpc, logger)
	if err != nil && !(errors.IsNotFound(err) && !drpc.GetDeletionTimestamp().IsZero()) {
		r.recordFailure(ctx, drpc, placementObj, "Error", err.Error(), logger)

		return ctrl.Result{}, err
	}

	if isBeingDeleted(drpc, placementObj) {
		// DPRC depends on User PlacementRule/Placement. If DRPC or/and the User PlacementRule is deleted,
		// then the DRPC should be deleted as well. The least we should do here is to clean up DPRC.
		err := r.processDeletion(ctx, drpc, placementObj, logger)
		if err != nil {
			// update drpc progression only on err
			logger.Info(fmt.Sprintf("Error in deleting DRPC: (%v)", err))

			return ctrl.Result{}, r.setProgressionAndUpdate(ctx, drpc, rmn.ProgressionDeleting)
		}

		return ctrl.Result{}, nil
	}

	drPolicy, err := r.getAndEnsureValidDRPolicy(ctx, drpc, logger)
	if err != nil {
		r.recordFailure(ctx, drpc, placementObj, "Error", err.Error(), logger)

		return ctrl.Result{}, err
	}

	// Updates labels, finalizers and set the placement as the owner of the DRPC
	updated, err := r.updateAndSetOwner(ctx, drpc, placementObj, logger)
	if err != nil {
		return ctrl.Result{}, err
	}

	if updated {
		// Reload before proceeding
		return ctrl.Result{Requeue: true}, nil
	}

	// Rebuild DRPC state if needed
	requeue, err := r.ensureDRPCStatusConsistency(ctx, drpc, drPolicy, placementObj, logger)
	if err != nil {
		return ctrl.Result{}, err
	}

	if requeue {
		return ctrl.Result{Requeue: true}, r.updateDRPCStatus(ctx, drpc, placementObj, logger)
	}

	d, err := r.createDRPCInstance(ctx, drPolicy, drpc, placementObj, logger)
	if err != nil && !errorswrapper.Is(err, InitialWaitTimeForDRPCPlacementRule) {
		err2 := r.updateDRPCStatus(ctx, drpc, placementObj, logger)

		return ctrl.Result{}, fmt.Errorf("failed to create DRPC instance (%w) and (%v)", err, err2)
	}

	if errorswrapper.Is(err, InitialWaitTimeForDRPCPlacementRule) {
		const initialWaitTime = 5

		r.recordFailure(ctx, drpc, placementObj, "Waiting",
			fmt.Sprintf("%v - wait time: %v", InitialWaitTimeForDRPCPlacementRule, initialWaitTime), logger)

		return ctrl.Result{RequeueAfter: time.Second * initialWaitTime}, nil
	}

	return r.reconcileDRPCInstance(d, logger)
}

func (r *DRPlacementControlReconciler) setProgressionAndUpdate(
	ctx context.Context, drpc *rmn.DRPlacementControl, progressionStatus rmn.ProgressionStatus,
) error {
	updated := updateDRPCProgression(drpc, progressionStatus, r.Log)

	if updated {
		if err := r.Status().Update(ctx, drpc); err != nil {
			return fmt.Errorf("failed to update DRPC status: (%w)", err)
		}
	}

	return nil
}

func (r *DRPlacementControlReconciler) recordFailure(ctx context.Context, drpc *rmn.DRPlacementControl,
	placementObj client.Object, reason, msg string, log logr.Logger,
) {
	needsUpdate := addOrUpdateCondition(&drpc.Status.Conditions, rmn.ConditionAvailable,
		drpc.Generation, metav1.ConditionFalse, reason, msg)
	if needsUpdate {
		err := r.updateDRPCStatus(ctx, drpc, placementObj, log)
		if err != nil {
			log.Info(fmt.Sprintf("Failed to update DRPC status (%v)", err))
		}
	}
}

func (r *DRPlacementControlReconciler) setLastSyncTimeMetric(syncMetrics *SyncMetrics,
	t *metav1.Time, log logr.Logger,
) {
	if syncMetrics == nil {
		return
	}

	log.Info(fmt.Sprintf("Setting metric: (%s)", LastSyncTimestampSeconds))

	if t == nil {
		syncMetrics.LastSyncTime.Set(0)

		return
	}

	syncMetrics.LastSyncTime.Set(float64(t.ProtoTime().Seconds))
}

func (r *DRPlacementControlReconciler) setLastSyncDurationMetric(syncDurationMetrics *SyncDurationMetrics,
	t *metav1.Duration, log logr.Logger,
) {
	if syncDurationMetrics == nil {
		return
	}

	log.Info(fmt.Sprintf("setting metric: (%s)", LastSyncDurationSeconds))

	if t == nil {
		syncDurationMetrics.LastSyncDuration.Set(0)

		return
	}

	syncDurationMetrics.LastSyncDuration.Set(t.Seconds())
}

func (r *DRPlacementControlReconciler) setLastSyncBytesMetric(syncDataBytesMetrics *SyncDataBytesMetrics,
	b *int64, log logr.Logger,
) {
	if syncDataBytesMetrics == nil {
		return
	}

	log.Info(fmt.Sprintf("setting metric: (%s)", LastSyncDataBytes))

	if b == nil {
		syncDataBytesMetrics.LastSyncDataBytes.Set(0)

		return
	}

	syncDataBytesMetrics.LastSyncDataBytes.Set(float64(*b))
}

//nolint:funlen
func (r *DRPlacementControlReconciler) createDRPCInstance(
	ctx context.Context,
	drPolicy *rmn.DRPolicy,
	drpc *rmn.DRPlacementControl,
	placementObj client.Object,
	log logr.Logger,
) (*DRPCInstance, error) {
	drClusters, err := getDRClusters(ctx, r.Client, drPolicy)
	if err != nil {
		return nil, err
	}

	// We only create DRPC PlacementRule if the preferred cluster is not configured
	err = r.getDRPCPlacementRule(ctx, drpc, placementObj, drPolicy, log)
	if err != nil {
		return nil, err
	}

	vrgNamespace, err := selectVRGNamespace(r.Client, r.Log, drpc, placementObj)
	if err != nil {
		return nil, err
	}

	vrgs, _, _, err := getVRGsFromManagedClusters(r.MCVGetter, drpc, drClusters, vrgNamespace, log)
	if err != nil {
		return nil, err
	}

	_, ramenConfig, err := ConfigMapGet(ctx, r.APIReader)
	if err != nil {
		return nil, fmt.Errorf("configmap get: %w", err)
	}

	d := &DRPCInstance{
		reconciler:      r,
		ctx:             ctx,
		log:             log,
		instance:        drpc,
		userPlacement:   placementObj,
		drPolicy:        drPolicy,
		drClusters:      drClusters,
		vrgs:            vrgs,
		vrgNamespace:    vrgNamespace,
		volSyncDisabled: ramenConfig.VolSync.Disabled,
		mwu: rmnutil.MWUtil{
			Client:          r.Client,
			APIReader:       r.APIReader,
			Ctx:             ctx,
			Log:             log,
			InstName:        drpc.Name,
			TargetNamespace: vrgNamespace,
		},
	}

	isMetro, _ := dRPolicySupportsMetro(drPolicy, drClusters)
	if isMetro {
		d.volSyncDisabled = true

		log.Info("volsync is set to disabled")
	}

	// Save the instance status
	d.instance.Status.DeepCopyInto(&d.savedInstanceStatus)

	return d, nil
}

func (r *DRPlacementControlReconciler) createDRPCMetricsInstance(
	drPolicy *rmn.DRPolicy, drpc *rmn.DRPlacementControl,
) *DRPCMetrics {
	syncMetricLabels := SyncMetricLabels(drPolicy, drpc)
	syncMetrics := NewSyncMetrics(syncMetricLabels)

	syncDurationMetricLabels := SyncDurationMetricLabels(drPolicy, drpc)
	syncDurationMetrics := NewSyncDurationMetric(syncDurationMetricLabels)

	syncDataBytesLabels := SyncDataBytesMetricLabels(drPolicy, drpc)
	syncDataMetrics := NewSyncDataBytesMetric(syncDataBytesLabels)

	return &DRPCMetrics{
		SyncMetrics:          syncMetrics,
		SyncDurationMetrics:  syncDurationMetrics,
		SyncDataBytesMetrics: syncDataMetrics,
	}
}

// isBeingDeleted returns true if either DRPC, user placement, or both are being deleted
func isBeingDeleted(drpc *rmn.DRPlacementControl, usrPl client.Object) bool {
	return !drpc.GetDeletionTimestamp().IsZero() ||
		(usrPl != nil && !usrPl.GetDeletionTimestamp().IsZero())
}

func (r *DRPlacementControlReconciler) reconcileDRPCInstance(d *DRPCInstance, log logr.Logger) (ctrl.Result, error) {
	// Last status update time BEFORE we start processing
	var beforeProcessing metav1.Time
	if d.instance.Status.LastUpdateTime != nil {
		beforeProcessing = *d.instance.Status.LastUpdateTime
	}

	requeue := d.startProcessing()
	log.Info("Finished processing", "Requeue?", requeue)

	if !requeue {
		log.Info("Done reconciling", "state", d.getLastDRState())
		r.Callback(d.instance.Name, string(d.getLastDRState()))
	}

	if d.mcvRequestInProgress && d.getLastDRState() != "" {
		duration := d.getRequeueDuration()
		log.Info(fmt.Sprintf("Requeing after %v", duration))

		return reconcile.Result{RequeueAfter: duration}, nil
	}

	if requeue {
		log.Info("Requeing...")

		return ctrl.Result{Requeue: true}, nil
	}

	// Last status update time AFTER processing
	var afterProcessing metav1.Time
	if d.instance.Status.LastUpdateTime != nil {
		afterProcessing = *d.instance.Status.LastUpdateTime
	}

	requeueTimeDuration := r.getStatusCheckDelay(beforeProcessing, afterProcessing)
	log.Info("Requeue time", "duration", requeueTimeDuration)

	return ctrl.Result{RequeueAfter: requeueTimeDuration}, nil
}

func (r *DRPlacementControlReconciler) getAndEnsureValidDRPolicy(ctx context.Context,
	drpc *rmn.DRPlacementControl, log logr.Logger,
) (*rmn.DRPolicy, error) {
	drPolicy, err := r.getDRPolicy(ctx, drpc, log)
	if err != nil {
		return nil, fmt.Errorf("failed to get DRPolicy %w", err)
	}

	if !drPolicy.ObjectMeta.DeletionTimestamp.IsZero() {
		// If drpolicy is deleted then return
		// error to fail drpc reconciliation
		return nil, fmt.Errorf("drPolicy '%s' referred by the DRPC is deleted, DRPC reconciliation would fail",
			drpc.Spec.DRPolicyRef.Name)
	}

	if err := rmnutil.DrpolicyValidated(drPolicy); err != nil {
		return nil, fmt.Errorf("DRPolicy not valid %w", err)
	}

	return drPolicy, nil
}

func (r *DRPlacementControlReconciler) getDRPolicy(ctx context.Context,
	drpc *rmn.DRPlacementControl, log logr.Logger,
) (*rmn.DRPolicy, error) {
	drPolicy := &rmn.DRPolicy{}
	name := drpc.Spec.DRPolicyRef.Name
	namespace := drpc.Spec.DRPolicyRef.Namespace

	err := r.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, drPolicy)
	if err != nil {
		log.Error(err, "failed to get DRPolicy")

		return nil, fmt.Errorf("%w", err)
	}

	return drPolicy, nil
}

// updateObjectMetadata updates drpc labels, annotations and finalizer, and also updates placementObj finalizer
func (r DRPlacementControlReconciler) updateObjectMetadata(ctx context.Context,
	drpc *rmn.DRPlacementControl, placementObj client.Object, log logr.Logger,
) error {
	update := false

	update = rmnutil.AddLabel(drpc, rmnutil.OCMBackupLabelKey, rmnutil.OCMBackupLabelValue)
	update = rmnutil.AddFinalizer(drpc, DRPCFinalizer) || update

	vrgNamespace, err := selectVRGNamespace(r.Client, r.Log, drpc, placementObj)
	if err != nil {
		return err
	}

	update = rmnutil.AddAnnotation(drpc, DRPCAppNamespace, vrgNamespace) || update

	if update {
		if err := r.Update(ctx, drpc); err != nil {
			log.Error(err, "Failed to add annotations, labels, or finalizer to drpc")

			return fmt.Errorf("%w", err)
		}
	}

	// add finalizer to User PlacementRule/Placement
	finalizerAdded := rmnutil.AddFinalizer(placementObj, DRPCFinalizer)
	if finalizerAdded {
		if err := r.Update(ctx, placementObj); err != nil {
			log.Error(err, "Failed to add finalizer to user placement rule")

			return fmt.Errorf("%w", err)
		}
	}

	return nil
}

func getDRClusters(ctx context.Context, client client.Client, drPolicy *rmn.DRPolicy) ([]rmn.DRCluster, error) {
	drClusters := []rmn.DRCluster{}

	for _, managedCluster := range rmnutil.DrpolicyClusterNames(drPolicy) {
		drCluster := &rmn.DRCluster{}

		err := client.Get(ctx, types.NamespacedName{Name: managedCluster}, drCluster)
		if err != nil {
			return nil, fmt.Errorf("failed to get DRCluster (%s) %w", managedCluster, err)
		}

		// TODO: What if the DRCluster is deleted? If new DRPC fail reconciliation
		drClusters = append(drClusters, *drCluster)
	}

	return drClusters, nil
}

func (r *DRPlacementControlReconciler) processDeletion(ctx context.Context,
	drpc *rmn.DRPlacementControl, placementObj client.Object, log logr.Logger,
) error {
	log.Info("Processing DRPC deletion")

	if !controllerutil.ContainsFinalizer(drpc, DRPCFinalizer) {
		return nil
	}

	// Run finalization logic for dprc.
	// If the finalization logic fails, don't remove the finalizer so
	// that we can retry during the next reconciliation.
	if err := r.finalizeDRPC(ctx, drpc, placementObj, log); err != nil {
		return err
	}

	if placementObj != nil && controllerutil.ContainsFinalizer(placementObj, DRPCFinalizer) {
		// Remove DRPCFinalizer from User PlacementRule/Placement.
		controllerutil.RemoveFinalizer(placementObj, DRPCFinalizer)

		err := r.Update(ctx, placementObj)
		if err != nil {
			return fmt.Errorf("failed to update User PlacementRule/Placement %w", err)
		}
	}

	// Remove DRPCFinalizer from DRPC.
	controllerutil.RemoveFinalizer(drpc, DRPCFinalizer)

	if err := r.Update(ctx, drpc); err != nil {
		return fmt.Errorf("failed to update drpc %w", err)
	}

	r.Callback(drpc.Name, "deleted")

	return nil
}

//nolint:funlen,cyclop
func (r *DRPlacementControlReconciler) finalizeDRPC(ctx context.Context, drpc *rmn.DRPlacementControl,
	placementObj client.Object, log logr.Logger,
) error {
	log.Info("Finalizing DRPC")

	clonedPlRuleName := fmt.Sprintf(ClonedPlacementRuleNameFormat, drpc.Name, drpc.Namespace)
	// delete cloned placementrule, if one created.
	if drpc.Spec.PreferredCluster == "" {
		err := r.deleteClonedPlacementRule(ctx, clonedPlRuleName, drpc.Namespace, log)
		if err != nil {
			return err
		}
	}

	// Cleanup volsync secret-related resources (policy/plrule/binding)
	err := volsync.CleanupSecretPropagation(ctx, r.Client, drpc, r.Log)
	if err != nil {
		return fmt.Errorf("failed to clean up volsync secret-related resources (%w)", err)
	}

	vrgNamespace, err := selectVRGNamespace(r.Client, r.Log, drpc, placementObj)
	if err != nil {
		return err
	}

	mwu := rmnutil.MWUtil{
		Client:          r.Client,
		APIReader:       r.APIReader,
		Ctx:             ctx,
		Log:             r.Log,
		InstName:        drpc.Name,
		TargetNamespace: vrgNamespace,
	}

	drPolicy, err := r.getDRPolicy(ctx, drpc, log)
	if err != nil {
		return fmt.Errorf("failed to get DRPolicy while finalizing DRPC (%w)", err)
	}

	// delete manifestworks (VRGs)
	for _, drClusterName := range rmnutil.DrpolicyClusterNames(drPolicy) {
		err := mwu.DeleteManifestWorksForCluster(drClusterName)
		if err != nil {
			return fmt.Errorf("%w", err)
		}
	}

	drClusters, err := getDRClusters(ctx, r.Client, drPolicy)
	if err != nil {
		return fmt.Errorf("failed to get drclusters. Error (%w)", err)
	}

	// Verify VRGs have been deleted
	vrgs, _, _, err := getVRGsFromManagedClusters(r.MCVGetter, drpc, drClusters, vrgNamespace, log)
	if err != nil {
		return fmt.Errorf("failed to retrieve VRGs. We'll retry later. Error (%w)", err)
	}

	if len(vrgs) != 0 {
		return fmt.Errorf("waiting for VRGs count to go to zero")
	}

	// delete MCVs used in the previous call
	if err := r.deleteAllManagedClusterViews(drpc, rmnutil.DrpolicyClusterNames(drPolicy)); err != nil {
		return fmt.Errorf("error in deleting MCV (%w)", err)
	}

	// delete metrics if matching labels are found
	syncMetricLabels := SyncMetricLabels(drPolicy, drpc)
	DeleteSyncMetric(syncMetricLabels)

	syncDurationMetricLabels := SyncDurationMetricLabels(drPolicy, drpc)
	DeleteSyncDurationMetric(syncDurationMetricLabels)

	syncDataBytesMetricLabels := SyncDataBytesMetricLabels(drPolicy, drpc)
	DeleteSyncDataBytesMetric(syncDataBytesMetricLabels)

	return nil
}

func (r *DRPlacementControlReconciler) deleteAllManagedClusterViews(
	drpc *rmn.DRPlacementControl, clusterNames []string,
) error {
	// Only after the VRGs have been deleted, we delete the MCVs for the VRGs and the NS
	for _, drClusterName := range clusterNames {
		err := r.MCVGetter.DeleteVRGManagedClusterView(drpc.Name, drpc.Namespace, drClusterName, rmnutil.MWTypeVRG)
		// Delete MCV for the VRG
		if err != nil {
			return fmt.Errorf("failed to delete VRG MCV %w", err)
		}

		err = r.MCVGetter.DeleteNamespaceManagedClusterView(drpc.Name, drpc.Namespace, drClusterName, rmnutil.MWTypeNS)
		// Delete MCV for Namespace
		if err != nil {
			return fmt.Errorf("failed to delete namespace MCV %w", err)
		}
	}

	return nil
}

func (r *DRPlacementControlReconciler) getDRPCPlacementRule(ctx context.Context,
	drpc *rmn.DRPlacementControl, placementObj client.Object,
	drPolicy *rmn.DRPolicy, log logr.Logger,
) error {
	var drpcPlRule *plrv1.PlacementRule
	// create the cloned placementrule if and only if the Spec.PreferredCluster is not provided
	if drpc.Spec.PreferredCluster == "" {
		var err error

		plRule := ConvertToPlacementRule(placementObj)
		if plRule == nil {
			return fmt.Errorf("invalid user PlacementRule")
		}

		drpcPlRule, err = r.getOrClonePlacementRule(ctx, drpc, drPolicy, plRule, log)
		if err != nil {
			log.Error(err, "failed to get DRPC PlacementRule")

			return err
		}

		// Make sure that we give time to the DRPC PlacementRule to run and produces decisions
		if drpcPlRule != nil && len(drpcPlRule.Status.Decisions) == 0 {
			return fmt.Errorf("%w", InitialWaitTimeForDRPCPlacementRule)
		}
	} else {
		log.Info("Preferred cluster is configured. Dynamic selection is disabled",
			"PreferredCluster", drpc.Spec.PreferredCluster)
	}

	return nil
}

func (r *DRPlacementControlReconciler) updateAndSetOwner(
	ctx context.Context,
	drpc *rmn.DRPlacementControl,
	usrPlacement client.Object,
	log logr.Logger,
) (bool, error) {
	if err := r.annotateObject(ctx, drpc, usrPlacement, log); err != nil {
		return false, err
	}

	if err := r.updateObjectMetadata(ctx, drpc, usrPlacement, log); err != nil {
		return false, err
	}

	return r.setDRPCOwner(ctx, drpc, usrPlacement, log)
}

func getPlacementOrPlacementRule(
	ctx context.Context,
	k8sclient client.Client,
	drpc *rmn.DRPlacementControl,
	log logr.Logger,
) (client.Object, error) {
	log.Info("Getting user placement object", "placementRef", drpc.Spec.PlacementRef)

	var usrPlacement client.Object

	var err error

	usrPlacement, err = getPlacementRule(ctx, k8sclient, drpc, log)
	if err != nil {
		if errors.IsNotFound(err) {
			// PacementRule not found. Check Placement instead
			usrPlacement, err = getPlacement(ctx, k8sclient, drpc, log)
		}

		if err != nil {
			return nil, err
		}
	} else {
		// Assert that there is no Placement object in the same namespace and with the same name as the PlacementRule
		_, err = getPlacement(ctx, k8sclient, drpc, log)
		if err == nil {
			return nil, fmt.Errorf(
				"can't proceed. PlacementRule and Placement CR with the same name exist on the same namespace")
		}
	}

	return usrPlacement, nil
}

func getPlacementRule(ctx context.Context, k8sclient client.Client,
	drpc *rmn.DRPlacementControl, log logr.Logger,
) (*plrv1.PlacementRule, error) {
	log.Info("Trying user PlacementRule", "usrPR", drpc.Spec.PlacementRef.Name+"/"+drpc.Spec.PlacementRef.Namespace)

	plRuleNamespace := drpc.Spec.PlacementRef.Namespace
	if plRuleNamespace == "" {
		plRuleNamespace = drpc.Namespace
	}

	if plRuleNamespace != drpc.Namespace {
		return nil, fmt.Errorf("referenced PlacementRule namespace (%s)"+
			" differs from DRPlacementControl resource namespace (%s)",
			drpc.Spec.PlacementRef.Namespace, drpc.Namespace)
	}

	usrPlRule := &plrv1.PlacementRule{}

	err := k8sclient.Get(ctx,
		types.NamespacedName{Name: drpc.Spec.PlacementRef.Name, Namespace: plRuleNamespace}, usrPlRule)
	if err != nil {
		log.Info(fmt.Sprintf("Get PlacementRule returned: %v", err))

		return nil, err
	}

	// If either DRPC or PlacementRule are being deleted, disregard everything else.
	if !isBeingDeleted(drpc, usrPlRule) {
		scName := usrPlRule.Spec.SchedulerName
		if scName != RamenScheduler {
			return nil, fmt.Errorf("placementRule %s does not have the ramen scheduler. Scheduler used %s",
				usrPlRule.Name, scName)
		}

		if usrPlRule.Spec.ClusterReplicas == nil || *usrPlRule.Spec.ClusterReplicas != 1 {
			log.Info("User PlacementRule replica count is not set to 1, reconciliation will only" +
				" schedule it to a single cluster")
		}

		log.Info(fmt.Sprintf("PlacementRule Status is: (%+v)", usrPlRule.Status))
	}

	return usrPlRule, nil
}

func getPlacement(ctx context.Context, k8sclient client.Client,
	drpc *rmn.DRPlacementControl, log logr.Logger,
) (*clrapiv1beta1.Placement, error) {
	log.Info("Trying user Placement", "usrP", drpc.Spec.PlacementRef.Name+"/"+drpc.Spec.PlacementRef.Namespace)

	plmntNamespace := drpc.Spec.PlacementRef.Namespace
	if plmntNamespace == "" {
		plmntNamespace = drpc.Namespace
	}

	if plmntNamespace != drpc.Namespace {
		return nil, fmt.Errorf("referenced Placement namespace (%s)"+
			" differs from DRPlacementControl resource namespace (%s)",
			drpc.Spec.PlacementRef.Namespace, drpc.Namespace)
	}

	usrPlmnt := &clrapiv1beta1.Placement{}

	err := k8sclient.Get(ctx,
		types.NamespacedName{Name: drpc.Spec.PlacementRef.Name, Namespace: plmntNamespace}, usrPlmnt)
	if err != nil {
		log.Info(fmt.Sprintf("Get Placement returned: %v", err))

		return nil, err
	}

	// If either DRPC or Placement are being deleted, disregard everything else.
	if !isBeingDeleted(drpc, usrPlmnt) {
		if value, ok := usrPlmnt.GetAnnotations()[clrapiv1beta1.PlacementDisableAnnotation]; !ok || value == "false" {
			return nil, fmt.Errorf("placement %s must be disabled in order for Ramen to be the scheduler",
				usrPlmnt.Name)
		}

		if usrPlmnt.Spec.NumberOfClusters == nil || *usrPlmnt.Spec.NumberOfClusters != 1 {
			log.Info("User Placement number of clusters is not set to 1, reconciliation will only" +
				" schedule it to a single cluster")
		}
	}

	return usrPlmnt, nil
}

func (r *DRPlacementControlReconciler) annotateObject(ctx context.Context,
	drpc *rmn.DRPlacementControl, obj client.Object, log logr.Logger,
) error {
	if !obj.GetDeletionTimestamp().IsZero() {
		return nil
	}

	if obj.GetAnnotations() == nil {
		obj.SetAnnotations(map[string]string{})
	}

	ownerName := obj.GetAnnotations()[DRPCNameAnnotation]
	ownerNamespace := obj.GetAnnotations()[DRPCNamespaceAnnotation]

	if ownerName == "" {
		obj.GetAnnotations()[DRPCNameAnnotation] = drpc.Name
		obj.GetAnnotations()[DRPCNamespaceAnnotation] = drpc.Namespace

		err := r.Update(ctx, obj)
		if err != nil {
			log.Error(err, "Failed to update Object annotation", "objName", obj.GetName())

			return fmt.Errorf("failed to update Object %s annotation '%s/%s' (%w)",
				obj.GetName(), DRPCNameAnnotation, drpc.Name, err)
		}

		return nil
	}

	if ownerName != drpc.Name || ownerNamespace != drpc.Namespace {
		log.Info("Object not owned by this DRPC", "objName", obj.GetName())

		return fmt.Errorf("object %s not owned by this DRPC '%s/%s'",
			obj.GetName(), drpc.Name, drpc.Namespace)
	}

	return nil
}

func (r *DRPlacementControlReconciler) setDRPCOwner(
	ctx context.Context, drpc *rmn.DRPlacementControl, owner client.Object, log logr.Logger,
) (bool, error) {
	const updated = true

	for _, ownerReference := range drpc.GetOwnerReferences() {
		if ownerReference.Name == owner.GetName() {
			return !updated, nil // ownerreference already set
		}
	}

	err := ctrl.SetControllerReference(owner, drpc, r.Client.Scheme())
	if err != nil {
		return !updated, fmt.Errorf("failed to set DRPC owner %w", err)
	}

	err = r.Update(ctx, drpc)
	if err != nil {
		return !updated, fmt.Errorf("failed to update drpc %s (%w)", drpc.GetName(), err)
	}

	log.Info(fmt.Sprintf("Object %s owns DRPC %s", owner.GetName(), drpc.GetName()))

	return updated, nil
}

func (r *DRPlacementControlReconciler) getOrClonePlacementRule(ctx context.Context,
	drpc *rmn.DRPlacementControl, drPolicy *rmn.DRPolicy,
	userPlRule *plrv1.PlacementRule, log logr.Logger,
) (*plrv1.PlacementRule, error) {
	log.Info("Getting PlacementRule or cloning it", "placement", drpc.Spec.PlacementRef)

	clonedPlRuleName := fmt.Sprintf(ClonedPlacementRuleNameFormat, drpc.Name, drpc.Namespace)

	clonedPlRule, err := r.getClonedPlacementRule(ctx, clonedPlRuleName, drpc.Namespace, log)
	if err != nil {
		if errors.IsNotFound(err) {
			clonedPlRule, err = r.clonePlacementRule(ctx, drPolicy, userPlRule, clonedPlRuleName, log)
			if err != nil {
				return nil, fmt.Errorf("failed to create cloned placementrule error: %w", err)
			}
		} else {
			log.Error(err, "Failed to get drpc placementRule", "name", clonedPlRuleName)

			return nil, err
		}
	}

	return clonedPlRule, nil
}

func (r *DRPlacementControlReconciler) getClonedPlacementRule(ctx context.Context,
	clonedPlRuleName, namespace string, log logr.Logger,
) (*plrv1.PlacementRule, error) {
	log.Info("Getting cloned PlacementRule", "name", clonedPlRuleName)

	clonedPlRule := &plrv1.PlacementRule{}

	err := r.Client.Get(ctx, types.NamespacedName{Name: clonedPlRuleName, Namespace: namespace}, clonedPlRule)
	if err != nil {
		return nil, fmt.Errorf("failed to get placementrule error: %w", err)
	}

	return clonedPlRule, nil
}

func (r *DRPlacementControlReconciler) clonePlacementRule(ctx context.Context,
	drPolicy *rmn.DRPolicy, userPlRule *plrv1.PlacementRule,
	clonedPlRuleName string, log logr.Logger,
) (*plrv1.PlacementRule, error) {
	log.Info("Creating a clone placementRule from", "name", userPlRule.Name)

	clonedPlRule := &plrv1.PlacementRule{}

	userPlRule.DeepCopyInto(clonedPlRule)

	clonedPlRule.Name = clonedPlRuleName
	clonedPlRule.ResourceVersion = ""
	clonedPlRule.Spec.SchedulerName = ""

	err := r.addClusterPeersToPlacementRule(drPolicy, clonedPlRule, log)
	if err != nil {
		log.Error(err, "Failed to add cluster peers to cloned placementRule", "name", clonedPlRuleName)

		return nil, err
	}

	err = r.Create(ctx, clonedPlRule)
	if err != nil {
		log.Error(err, "failed to clone placement rule", "name", clonedPlRule.Name)

		return nil, errorswrapper.Wrap(err, "failed to create PlacementRule")
	}

	return clonedPlRule, nil
}

func getVRGsFromManagedClusters(
	mcvGetter rmnutil.ManagedClusterViewGetter,
	drpc *rmn.DRPlacementControl,
	drClusters []rmn.DRCluster,
	vrgNamespace string,
	log logr.Logger,
) (map[string]*rmn.VolumeReplicationGroup, int, string, error) {
	vrgs := map[string]*rmn.VolumeReplicationGroup{}

	annotations := make(map[string]string)

	annotations[DRPCNameAnnotation] = drpc.Name
	annotations[DRPCNamespaceAnnotation] = drpc.Namespace

	var clustersQueriedSuccessfully int

	var failedCluster string

	for i := range drClusters {
		drCluster := &drClusters[i]

		vrg, err := mcvGetter.GetVRGFromManagedCluster(drpc.Name, vrgNamespace, drCluster.Name, annotations)
		if err != nil {
			// Only NotFound error is accepted
			if errors.IsNotFound(err) {
				log.Info(fmt.Sprintf("VRG not found on %q", drCluster.Name))
				clustersQueriedSuccessfully++

				continue
			}

			failedCluster = drCluster.Name

			log.Info(fmt.Sprintf("failed to retrieve VRG from %s. err (%v).", drCluster.Name, err))

			continue
		}

		clustersQueriedSuccessfully++

		if drClusterIsDeleted(drCluster) {
			log.Info("Skipping VRG on deleted drcluster", "drcluster", drCluster.Name, "vrg", vrg.Name)

			continue
		}

		vrgs[drCluster.Name] = vrg

		log.Info("VRG location", "VRG on", drCluster.Name)
	}

	// We are done if we successfully queried all drClusters
	if clustersQueriedSuccessfully == len(drClusters) {
		return vrgs, clustersQueriedSuccessfully, "", nil
	}

	if clustersQueriedSuccessfully == 0 {
		return vrgs, 0, "", fmt.Errorf("failed to retrieve VRGs from clusters")
	}

	return vrgs, clustersQueriedSuccessfully, failedCluster, nil
}

func (r *DRPlacementControlReconciler) deleteClonedPlacementRule(ctx context.Context,
	name, namespace string, log logr.Logger,
) error {
	plRule, err := r.getClonedPlacementRule(ctx, name, namespace, log)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}

		return err
	}

	err = r.Client.Delete(ctx, plRule)
	if err != nil {
		return fmt.Errorf("failed to delete cloned plRule %w", err)
	}

	return nil
}

func (r *DRPlacementControlReconciler) addClusterPeersToPlacementRule(
	drPolicy *rmn.DRPolicy, plRule *plrv1.PlacementRule, log logr.Logger,
) error {
	if len(rmnutil.DrpolicyClusterNames(drPolicy)) == 0 {
		return fmt.Errorf("DRPolicy %s is missing DR clusters", drPolicy.Name)
	}

	for _, v := range rmnutil.DrpolicyClusterNames(drPolicy) {
		plRule.Spec.Clusters = append(plRule.Spec.Clusters, plrv1.GenericClusterReference{Name: v})
	}

	log.Info(fmt.Sprintf("Added clusters %v to placementRule from DRPolicy %s", plRule.Spec.Clusters, drPolicy.Name))

	return nil
}

// statusUpdateTimeElapsed returns whether it is time to update DRPC status or not
// DRPC status is updated at least once every StatusCheckDelay in order to refresh
// the VRG status.
func (d *DRPCInstance) statusUpdateTimeElapsed() bool {
	if d.instance.Status.LastUpdateTime == nil {
		return false
	}

	return d.instance.Status.LastUpdateTime.Add(StatusCheckDelay).Before(time.Now())
}

// getStatusCheckDelay returns the reconciliation requeue time duration when no requeue
// has been requested. We want the reconciliation to run at least once every StatusCheckDelay
// in order to refresh DRPC status with VRG status. The reconciliation will be called at any time.
// If it is called before the StatusCheckDelay has elapsed, and the DRPC status was not updated,
// then we must return the remaining time rather than the full StatusCheckDelay to prevent
// starving the status update, which is scheduled for at least once every StatusCheckDelay.
//
// Example: Assume at 10:00am was the last time when the reconciler ran and updated the status.
// The StatusCheckDelay is hard coded to 10 minutes.  If nothing is happening in the system that
// requires the reconciler to run, then the next run would be at 10:10am. If however, for any reason
// the reconciler is called, let's say, at 10:08am, and no update to the DRPC status was needed,
// then the requeue time duration should be 2 minutes and NOT the full StatusCheckDelay. That is:
// 10:00am + StatusCheckDelay - 10:08am = 2mins
func (r *DRPlacementControlReconciler) getStatusCheckDelay(
	beforeProcessing metav1.Time, afterProcessing metav1.Time,
) time.Duration {
	if beforeProcessing != afterProcessing {
		// DRPC's VRG status update processing time has changed during this
		// iteration of the reconcile loop.  Hence, the next attempt to update
		// the status should be after a delay of a standard polling interval
		// duration.
		return StatusCheckDelay
	}

	// DRPC's VRG status update processing time has NOT changed during this
	// iteration of the reconcile loop.  Hence, the next attempt to update the
	// status should be after the remaining duration of this polling interval has
	// elapsed: (beforeProcessing + StatusCheckDelay - time.Now())
	return time.Until(beforeProcessing.Add(StatusCheckDelay))
}

func (r *DRPlacementControlReconciler) updateDRPCStatus(
	ctx context.Context, drpc *rmn.DRPlacementControl, userPlacement client.Object, log logr.Logger,
) error {
	log.Info("Updating DRPC status")

	vrgNamespace, err := selectVRGNamespace(r.Client, r.Log, drpc, userPlacement)
	if err != nil {
		log.Info("Failed to select VRG namespace", "error", err)
	}

	clusterDecision := r.getClusterDecision(userPlacement)
	if clusterDecision != nil && clusterDecision.ClusterName != "" && vrgNamespace != "" {
		r.updateResourceCondition(ctx, drpc, clusterDecision.ClusterName, vrgNamespace, log)
	}

	for i, condition := range drpc.Status.Conditions {
		if condition.ObservedGeneration != drpc.Generation {
			drpc.Status.Conditions[i].ObservedGeneration = drpc.Generation
		}
	}

	if reflect.DeepEqual(r.savedInstanceStatus, drpc.Status) {
		log.Info("No need to update DRPC Status")

		return nil
	}

	now := metav1.Now()
	drpc.Status.LastUpdateTime = &now

	if err := r.Status().Update(context.TODO(), drpc); err != nil {
		return errorswrapper.Wrap(err, "failed to update DRPC status")
	}

	log.Info(fmt.Sprintf("Updated DRPC Status %+v", drpc.Status))

	return nil
}

func (r *DRPlacementControlReconciler) updateResourceCondition(
	ctx context.Context,
	drpc *rmn.DRPlacementControl,
	clusterName, vrgNamespace string,
	log logr.Logger,
) {
	annotations := make(map[string]string)

	annotations[DRPCNameAnnotation] = drpc.Name
	annotations[DRPCNamespaceAnnotation] = drpc.Namespace

	vrg, err := r.MCVGetter.GetVRGFromManagedCluster(drpc.Name, vrgNamespace,
		clusterName, annotations)
	if err != nil {
		log.Info("Failed to get VRG from managed cluster", "errMsg", err)

		drpc.Status.ResourceConditions = rmn.VRGConditions{}
	} else {
		drpc.Status.ResourceConditions.ResourceMeta.Kind = vrg.Kind
		drpc.Status.ResourceConditions.ResourceMeta.Name = vrg.Name
		drpc.Status.ResourceConditions.ResourceMeta.Namespace = vrg.Namespace
		drpc.Status.ResourceConditions.ResourceMeta.Generation = vrg.Generation
		drpc.Status.ResourceConditions.Conditions = vrg.Status.Conditions

		protectedPVCs := []string{}
		for _, protectedPVC := range vrg.Status.ProtectedPVCs {
			protectedPVCs = append(protectedPVCs, protectedPVC.Name)
		}

		drpc.Status.ResourceConditions.ResourceMeta.ProtectedPVCs = protectedPVCs

		if vrg.Status.LastGroupSyncTime != nil || drpc.Spec.Action != rmn.ActionRelocate {
			drpc.Status.LastGroupSyncTime = vrg.Status.LastGroupSyncTime
			drpc.Status.LastGroupSyncDuration = vrg.Status.LastGroupSyncDuration
			drpc.Status.LastGroupSyncBytes = vrg.Status.LastGroupSyncBytes
		}
	}

	if err := r.setDRPCMetrics(ctx, drpc, log); err != nil {
		// log the error but do not return the error
		log.Info("failed to set drpc metrics", "errMSg", err)
	}
}

func (r *DRPlacementControlReconciler) setDRPCMetrics(ctx context.Context,
	drpc *rmn.DRPlacementControl, log logr.Logger,
) error {
	log.Info("setting drpc metrics")

	drPolicy, err := r.getDRPolicy(ctx, drpc, log)
	if err != nil {
		return fmt.Errorf("failed to get DRPolicy %w", err)
	}

	drClusters, err := getDRClusters(ctx, r.Client, drPolicy)
	if err != nil {
		return err
	}

	// do not set netrics if metro-dr
	isMetro, _ := dRPolicySupportsMetro(drPolicy, drClusters)
	if isMetro {
		return nil
	}

	drpcMetrics := r.createDRPCMetricsInstance(drPolicy, drpc)

	if drpcMetrics != nil {
		r.setLastSyncTimeMetric(&drpcMetrics.SyncMetrics, drpc.Status.LastGroupSyncTime, log)
		r.setLastSyncDurationMetric(&drpcMetrics.SyncDurationMetrics, drpc.Status.LastGroupSyncDuration, log)
		r.setLastSyncBytesMetric(&drpcMetrics.SyncDataBytesMetrics, drpc.Status.LastGroupSyncBytes, log)
	}

	return nil
}

func ConvertToPlacementRule(placementObj interface{}) *plrv1.PlacementRule {
	var pr *plrv1.PlacementRule

	if obj, ok := placementObj.(*plrv1.PlacementRule); ok {
		pr = obj
	}

	return pr
}

func ConvertToPlacement(placementObj interface{}) *clrapiv1beta1.Placement {
	var p *clrapiv1beta1.Placement

	if obj, ok := placementObj.(*clrapiv1beta1.Placement); ok {
		p = obj
	}

	return p
}

func (r *DRPlacementControlReconciler) getClusterDecision(placementObj interface{},
) *clrapiv1beta1.ClusterDecision {
	switch obj := placementObj.(type) {
	case *plrv1.PlacementRule:
		return r.getClusterDecisionFromPlacementRule(obj)
	case *clrapiv1beta1.Placement:
		return r.getClusterDecisionFromPlacement(obj)
	default:
		return &clrapiv1beta1.ClusterDecision{}
	}
}

func (r *DRPlacementControlReconciler) getClusterDecisionFromPlacementRule(plRule *plrv1.PlacementRule,
) *clrapiv1beta1.ClusterDecision {
	var clusterName string
	if len(plRule.Status.Decisions) > 0 {
		clusterName = plRule.Status.Decisions[0].ClusterName
	}

	return &clrapiv1beta1.ClusterDecision{
		ClusterName: clusterName,
		Reason:      "PlacementRule decision",
	}
}

// getPlacementDecisionFromPlacement returns a PlacementDecision for the passed in Placement if found, and nil otherwise
// - The PlacementDecision is determined by listing all PlacementDecisions in the Placement namespace filtered on the
// Placement label as set by OCM
// - Function also ensures there is only one decision for a Placement, as the needed by the Ramen orchestrators, and
// if not returns an error
func (r *DRPlacementControlReconciler) getPlacementDecisionFromPlacement(placement *clrapiv1beta1.Placement,
) (*clrapiv1beta1.PlacementDecision, error) {
	matchLabels := map[string]string{
		clrapiv1beta1.PlacementLabel: placement.GetName(),
	}

	listOptions := []client.ListOption{
		client.InNamespace(placement.GetNamespace()),
		client.MatchingLabels(matchLabels),
	}

	plDecisions := &clrapiv1beta1.PlacementDecisionList{}
	if err := r.List(context.TODO(), plDecisions, listOptions...); err != nil {
		return nil, fmt.Errorf("failed to list PlacementDecisions (placement: %s)",
			placement.GetNamespace()+"/"+placement.GetName())
	}

	if len(plDecisions.Items) == 0 {
		return nil, nil
	}

	if len(plDecisions.Items) > 1 {
		return nil, fmt.Errorf("multiple PlacementDecisions found for Placement (count: %d, placement: %s)",
			len(plDecisions.Items), placement.GetNamespace()+"/"+placement.GetName())
	}

	plDecision := plDecisions.Items[0]
	r.Log.Info("Found ClusterDecision", "ClsDedicision", plDecision.Status.Decisions)

	if len(plDecision.Status.Decisions) > 1 {
		return nil, fmt.Errorf("multiple placements found in PlacementDecision"+
			" (count: %d, Placement: %s, PlacementDecision: %s)",
			len(plDecision.Status.Decisions),
			placement.GetNamespace()+"/"+placement.GetName(),
			plDecision.GetName()+"/"+plDecision.GetNamespace())
	}

	return &plDecision, nil
}

// getClusterDecisionFromPlacement returns the cluster decision for a given Placement if found
func (r *DRPlacementControlReconciler) getClusterDecisionFromPlacement(placement *clrapiv1beta1.Placement,
) *clrapiv1beta1.ClusterDecision {
	plDecision, err := r.getPlacementDecisionFromPlacement(placement)
	if err != nil {
		// TODO: err ignored by this caller
		r.Log.Info("failed to get placement decision", "error", err)

		return &clrapiv1beta1.ClusterDecision{}
	}

	if plDecision == nil || len(plDecision.Status.Decisions) == 0 {
		return &clrapiv1beta1.ClusterDecision{}
	}

	return &plDecision.Status.Decisions[0]
}

func (r *DRPlacementControlReconciler) updateUserPlacementStatusDecision(ctx context.Context,
	userPlacement interface{}, newCD *clrapiv1beta1.ClusterDecision,
) error {
	switch obj := userPlacement.(type) {
	case *plrv1.PlacementRule:
		return r.createOrUpdatePlacementRuleDecision(ctx, obj, newCD)
	case *clrapiv1beta1.Placement:
		return r.createOrUpdatePlacementDecision(ctx, obj, newCD)
	default:
		return fmt.Errorf("failed to find Placement or PlacementRule")
	}
}

func (r *DRPlacementControlReconciler) createOrUpdatePlacementRuleDecision(ctx context.Context,
	plRule *plrv1.PlacementRule, newCD *clrapiv1beta1.ClusterDecision,
) error {
	newStatus := plrv1.PlacementRuleStatus{}

	if newCD != nil {
		newStatus = plrv1.PlacementRuleStatus{
			Decisions: []plrv1.PlacementDecision{
				{
					ClusterName:      newCD.ClusterName,
					ClusterNamespace: newCD.ClusterName,
				},
			},
		}
	}

	if !reflect.DeepEqual(newStatus, plRule.Status) {
		plRule.Status = newStatus
		if err := r.Status().Update(ctx, plRule); err != nil {
			r.Log.Error(err, "failed to update user PlacementRule")

			return fmt.Errorf("failed to update userPlRule %s (%w)", plRule.GetName(), err)
		}

		r.Log.Info("Updated user PlacementRule status", "Decisions", plRule.Status.Decisions)
	}

	return nil
}

// createOrUpdatePlacementDecision updates the PlacementDecision status for the given Placement with the passed
// in new decision. If an existing PlacementDecision is not found, ad new Placement decision is created.
func (r *DRPlacementControlReconciler) createOrUpdatePlacementDecision(ctx context.Context,
	placement *clrapiv1beta1.Placement, newCD *clrapiv1beta1.ClusterDecision,
) error {
	plDecision, err := r.getPlacementDecisionFromPlacement(placement)
	if err != nil {
		return err
	}

	if plDecision == nil {
		if plDecision, err = r.createPlacementDecision(ctx, placement); err != nil {
			return err
		}
	}

	plDecision.Status = clrapiv1beta1.PlacementDecisionStatus{
		Decisions: []clrapiv1beta1.ClusterDecision{},
	}

	if newCD != nil {
		plDecision.Status = clrapiv1beta1.PlacementDecisionStatus{
			Decisions: []clrapiv1beta1.ClusterDecision{
				{
					ClusterName: newCD.ClusterName,
					Reason:      newCD.Reason,
				},
			},
		}
	}

	if err := r.Status().Update(ctx, plDecision); err != nil {
		return fmt.Errorf("failed to update placementDecision status (%w)", err)
	}

	r.Log.Info("Created/Updated PlacementDecision", "PlacementDecision", plDecision)

	return nil
}

// createPlacementDecision creates a new PlacementDecision for the given Placement. The PlacementDecision is
// named in a predetermined format, and is searchable using the Placement name label against the PlacementDecision.
// On conflicts with existing PlacementDecisions, the function retries, with limits, with different names to generate
// a new PlacementDecision.
func (r *DRPlacementControlReconciler) createPlacementDecision(ctx context.Context,
	placement *clrapiv1beta1.Placement,
) (*clrapiv1beta1.PlacementDecision, error) {
	index := 1

	plDecision := &clrapiv1beta1.PlacementDecision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf(PlacementDecisionName, placement.GetName(), index),
			Namespace: placement.Namespace,
		},
	}

	// Set the Placement object to be the owner.  When it is deleted, the PlacementDecision is deleted
	err := ctrl.SetControllerReference(placement, plDecision, r.Client.Scheme())
	if err != nil {
		return nil, fmt.Errorf("failed to set controller reference %w", err)
	}

	plDecision.ObjectMeta.Labels = map[string]string{
		clrapiv1beta1.PlacementLabel:    placement.GetName(),
		"velero.io/exclude-from-backup": "true",
	}

	owner := metav1.NewControllerRef(placement, clrapiv1beta1.GroupVersion.WithKind("Placement"))
	plDecision.ObjectMeta.OwnerReferences = []metav1.OwnerReference{*owner}

	for index <= MaxPlacementDecisionConflictCount {
		if err = r.Create(ctx, plDecision); err == nil {
			return plDecision, nil
		}

		if !errors.IsAlreadyExists(err) {
			return nil, err
		}

		index++

		plDecision.ObjectMeta.Name = fmt.Sprintf(PlacementDecisionName, placement.GetName(), index)
	}

	return nil, fmt.Errorf("multiple PlacementDecision conflicts found, unable to create a new"+
		" PlacementDecision for Placement %s", placement.GetNamespace()+"/"+placement.GetName())
}

func getApplicationDestinationNamespace(
	client client.Client,
	log logr.Logger,
	placement client.Object,
) (string, error) {
	appSetList := argov1alpha1.ApplicationSetList{}
	if err := client.List(context.TODO(), &appSetList); err != nil {
		// If ApplicationSet CRD is not found in the API server,
		// default to Subscription behavior, and return the placement namespace as the target VRG namespace
		if meta.IsNoMatchError(err) {
			return placement.GetNamespace(), nil
		}

		return "", fmt.Errorf("ApplicationSet list: %w", err)
	}

	log.Info("Retrieved ApplicationSets", "count", len(appSetList.Items))
	//
	// TODO: change the following loop to use an index field on AppSet instead for faster lookup
	//
	for i := range appSetList.Items {
		appSet := &appSetList.Items[i]
		if len(appSet.Spec.Generators) > 0 &&
			appSet.Spec.Generators[0].ClusterDecisionResource != nil {
			pn := appSet.Spec.Generators[0].ClusterDecisionResource.LabelSelector.MatchLabels[clrapiv1beta1.PlacementLabel]
			if pn == placement.GetName() {
				log.Info("Found ApplicationSet for Placement", "name", appSet.Name, "placement", placement.GetName())
				// Retrieving the Destination.Namespace from Application.Spec requires iterating through all Applications
				// and checking their ownerReferences, which can be time-consuming. Alternatively, we can get the same
				// information from the ApplicationSet spec template section as it is done here.
				return appSet.Spec.Template.Spec.Destination.Namespace, nil
			}
		}
	}

	log.Info(fmt.Sprintf("Placement %s does not belong to any ApplicationSet. Defaulting the dest namespace to %s",
		placement.GetName(), placement.GetNamespace()))

	// Didn't find any ApplicationSet using this Placement. Assuming it is for Subscription.
	// Returning its own namespace as the default namespace
	return placement.GetNamespace(), nil
}

func selectVRGNamespace(
	client client.Client,
	log logr.Logger,
	drpc *rmn.DRPlacementControl,
	placementObj client.Object,
) (string, error) {
	if drpc.GetAnnotations() != nil && drpc.GetAnnotations()[DRPCAppNamespace] != "" {
		return drpc.GetAnnotations()[DRPCAppNamespace], nil
	}

	switch placementObj.(type) {
	case *clrapiv1beta1.Placement:
		vrgNamespace, err := getApplicationDestinationNamespace(client, log, placementObj)
		if err != nil {
			return "", err
		}

		return vrgNamespace, nil
	default:
		return drpc.Namespace, nil
	}
}

func addOrUpdateCondition(conditions *[]metav1.Condition, conditionType string,
	observedGeneration int64, status metav1.ConditionStatus, reason, msg string,
) bool {
	newCondition := metav1.Condition{
		Type:               conditionType,
		Status:             status,
		ObservedGeneration: observedGeneration,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            msg,
	}

	existingCondition := findCondition(*conditions, conditionType)
	if existingCondition == nil ||
		existingCondition.Status != newCondition.Status ||
		existingCondition.ObservedGeneration != newCondition.ObservedGeneration ||
		existingCondition.Reason != newCondition.Reason ||
		existingCondition.Message != newCondition.Message {
		setStatusCondition(conditions, newCondition)

		return true
	}

	return false
}

// Initial creation of the DRPC status condition. This will also preserve the ordering of conditions in the array
func ensureDRPCConditionsInited(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	const DRPCTotalConditions = 2
	if len(*conditions) == DRPCTotalConditions {
		return
	}

	time := metav1.NewTime(time.Now())

	setStatusConditionIfNotFound(conditions, metav1.Condition{
		Type:               rmn.ConditionAvailable,
		Reason:             string(rmn.Initiating),
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionTrue,
		LastTransitionTime: time,
		Message:            message,
	})
	setStatusConditionIfNotFound(conditions, metav1.Condition{
		Type:               rmn.ConditionPeerReady,
		Reason:             string(rmn.Initiating),
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionTrue,
		LastTransitionTime: time,
		Message:            message,
	})
}

func AvailableS3Profiles(drClusters []rmn.DRCluster) []string {
	profiles := sets.New[string]()

	for i := range drClusters {
		drCluster := &drClusters[i]
		if drClusterIsDeleted(drCluster) {
			continue
		}

		profiles.Insert(drCluster.Spec.S3ProfileName)
	}

	return sets.List(profiles)
}

type Progress int

const (
	Continue      = 1
	AllowFailover = 2
	Stop          = 3
)

func (r *DRPlacementControlReconciler) ensureDRPCStatusConsistency(
	ctx context.Context,
	drpc *rmn.DRPlacementControl,
	drPolicy *rmn.DRPolicy,
	placementObj client.Object,
	log logr.Logger,
) (bool, error) {
	requeue := true

	// This will always be false the first time the DRPC resource is first created OR after hub recovery
	if drpc.Status.Phase != "" {
		return !requeue, nil
	}

	dstCluster := drpc.Spec.PreferredCluster
	if drpc.Spec.Action == rmn.ActionFailover {
		dstCluster = drpc.Spec.FailoverCluster
	}

	progress, err := r.determineDRPCState(ctx, drpc, drPolicy, placementObj, dstCluster, log)
	if err != nil {
		return requeue, err
	}

	switch progress {
	case Continue:
		return !requeue, nil
	case AllowFailover:
		updateDRPCProgression(drpc, rmn.ProgressionActionPaused, log)
		addOrUpdateCondition(&drpc.Status.Conditions, rmn.ConditionAvailable,
			drpc.Generation, metav1.ConditionTrue, rmn.ReasonSuccess, "Failover allowed")
		addOrUpdateCondition(&drpc.Status.Conditions, rmn.ConditionPeerReady, drpc.Generation,
			metav1.ConditionTrue, rmn.ReasonSuccess, "Failover allowed")

		return requeue, nil
	default:
		msg := "Operation Paused - User Intervention Required."

		log.Info(fmt.Sprintf("err:%v - msg:%s", err, msg))
		updateDRPCProgression(drpc, rmn.ProgressionActionPaused, log)
		addOrUpdateCondition(&drpc.Status.Conditions, rmn.ConditionAvailable,
			drpc.Generation, metav1.ConditionFalse, rmn.ReasonPaused, msg)
		addOrUpdateCondition(&drpc.Status.Conditions, rmn.ConditionPeerReady, drpc.Generation,
			metav1.ConditionFalse, rmn.ReasonPaused, msg)

		return requeue, nil
	}
}

// determineDRPCState runs the following algorithm
// 1. Stop Condition for Both Failed Queries:
//    If attempts to query 2 clusters result in failure for both, the process is halted.

// 2. Initial Deployment without VRGs:
//    If 2 clusters are successfully queried, and no VRGs are found, proceed with the
//    initial deployment.

// 3. Handling Failures with S3 Store Check:
//    - If 2 clusters are queried, 1 fails, and 0 VRGs are found, perform the following checks:
//       - If the VRG is found in the S3 store, ensure that the DRPC action matches the VRG action.
//       If not, stop until the action is corrected, allowing failover if necessary (set PeerReady).
//       - If the VRG is not found in the S3 store and the failed cluster is not the destination
//       cluster, continue with the initial deployment.

// 4. Verification and Failover for VRGs on Failover Cluster:
//    If 2 clusters are queried, 1 fails, and 1 VRG is found on the failover cluster, check
//    the action:
//       - If the actions don't match, stop until corrected by the user.
//       - If they match, also stop but allow failover if the VRG in-hand is a secondary.
//       Otherwise, continue.

// 5. Handling VRGs on Destination Cluster:
//    If 2 clusters are queried successfully and 1 or more VRGs are found, and one of the
//    VRGs is on the destination cluster, perform the following checks:
//       - Continue with the action only if the DRPC and the found VRG action match.
//       - Stop until someone investigates if there is a mismatch, but allow failover to
//       take place (set PeerReady).

//  6. Otherwise, default to allowing Failover:
//     If none of the above conditions apply, allow failover (set PeerReady) but stop until
//     someone makes the necessary change.
//
//nolint:funlen,nestif,gocognit,gocyclo,cyclop
func (r *DRPlacementControlReconciler) determineDRPCState(
	ctx context.Context,
	drpc *rmn.DRPlacementControl,
	drPolicy *rmn.DRPolicy,
	placementObj client.Object,
	dstCluster string,
	log logr.Logger,
) (Progress, error) {
	log.Info("Rebuild DRPC state")

	vrgNamespace, err := selectVRGNamespace(r.Client, log, drpc, placementObj)
	if err != nil {
		log.Info("Failed to select VRG namespace")

		return Stop, err
	}

	drClusters, err := getDRClusters(ctx, r.Client, drPolicy)
	if err != nil {
		return Stop, err
	}

	vrgs, successfullyQueriedClusterCount, failedCluster, err := getVRGsFromManagedClusters(
		r.MCVGetter, drpc, drClusters, vrgNamespace, log)
	if err != nil {
		log.Info("Failed to get a list of VRGs")

		return Stop, err
	}

	// IF 2 clusters queried, and both queries failed, then STOP
	if successfullyQueriedClusterCount == 0 {
		log.Info("Number of clusters queried is 0. Stop...")

		return Stop, nil
	}

	// IF 2 clusters queried successfully and no VRGs, then continue with initial deployment
	if successfullyQueriedClusterCount == 2 && len(vrgs) == 0 {
		log.Info("Queried 2 clusters successfully")

		return Continue, nil
	}

	// IF queried 2 clusters queried, 1 failed and 0 VRG found, then check s3 store.
	// IF the VRG found in the s3 store, ensure that the DRPC action and the VRG action match. IF not, stop until
	// the action is corrected, but allow failover to take place if needed (set PeerReady)
	// If the VRG is not found in the s3 store and the failedCluster is not the destination cluster, then continue
	// with initial deploy
	if successfullyQueriedClusterCount == 1 && len(vrgs) == 0 {
		vrg := GetLastKnownVRGPrimaryFromS3(ctx, r.APIReader,
			AvailableS3Profiles(drClusters), drpc.GetName(), vrgNamespace, r.ObjStoreGetter, log)
		if vrg == nil {
			// IF the failed cluster is not the dest cluster, then this could be an initial deploy
			if failedCluster != dstCluster {
				return Continue, nil
			}

			log.Info("Unable to query all clusters and failed to get VRG from s3 store")

			return Stop, nil
		}

		log.Info("VRG From s3", "VRG Spec", vrg.Spec, "VRG Annotations", vrg.GetAnnotations())

		if drpc.Spec.Action != rmn.DRAction(vrg.Spec.Action) {
			log.Info(fmt.Sprintf("Two different actions - drpc action is '%s'/vrg action from s3 is '%s'",
				drpc.Spec.Action, vrg.Spec.Action))

			return AllowFailover, nil
		}

		if dstCluster == vrg.GetAnnotations()[DestinationClusterAnnotationKey] &&
			dstCluster != failedCluster {
			log.Info(fmt.Sprintf("VRG from s3. Same dstCluster %s/%s. Proceeding...",
				dstCluster, vrg.GetAnnotations()[DestinationClusterAnnotationKey]))

			return Continue, nil
		}

		log.Info(fmt.Sprintf("VRG from s3. DRPCAction/vrgAction/DRPCDstClstr/vrgDstClstr %s/%s/%s/%s. Allow Failover...",
			drpc.Spec.Action, vrg.Spec.Action, dstCluster, vrg.GetAnnotations()[DestinationClusterAnnotationKey]))

		return AllowFailover, nil
	}

	// IF 2 clusters queried, 1 failed and 1 VRG found on the failover cluster, then check the action, if they don't
	// match, stop until corrected by the user. If they do match, then also stop but allow failover if the VRG in-hand
	// is a secondary. Othewise, continue...
	if successfullyQueriedClusterCount == 1 && len(vrgs) == 1 {
		var clusterName string

		var vrg *rmn.VolumeReplicationGroup
		for k, v := range vrgs {
			clusterName, vrg = k, v

			break
		}

		if drpc.Spec.Action != rmn.DRAction(vrg.Spec.Action) {
			log.Info(fmt.Sprintf("Stop! Two different actions - drpc action is '%s'/vrg action is '%s'",
				drpc.Spec.Action, vrg.Spec.Action))

			return Stop, nil
		}

		if dstCluster != clusterName && vrg.Spec.ReplicationState == rmn.Secondary {
			log.Info(fmt.Sprintf("Same Action and dstCluster and ReplicationState %s/%s/%s",
				drpc.Spec.Action, dstCluster, vrg.Spec.ReplicationState))

			log.Info("Failover is allowed - Primary is assumed in the failed cluster")

			return AllowFailover, nil
		}

		log.Info("Allow to continue")

		return Continue, nil
	}

	// Finally, IF 2 clusters queried successfully and 1 or more VRGs found, and if one of the VRGs is on the dstCluster,
	// then continue with action if and only if DRPC and the found VRG action match. otherwise, stop until someone
	// investigates but allow failover to take place (set PeerReady)
	if successfullyQueriedClusterCount == 2 && len(vrgs) >= 1 {
		var clusterName string

		var vrg *rmn.VolumeReplicationGroup

		for k, v := range vrgs {
			clusterName, vrg = k, v
			if vrg.Spec.ReplicationState == rmn.Primary {
				break
			}
		}

		// This can happen if a hub is recovered in the middle of a Relocate
		if vrg.Spec.ReplicationState == rmn.Secondary && len(vrgs) == 2 {
			log.Info("Both VRGs are in secondary state")

			return Stop, nil
		}

		if drpc.Spec.Action == rmn.DRAction(vrg.Spec.Action) && dstCluster == clusterName {
			log.Info(fmt.Sprintf("Same Action %s", drpc.Spec.Action))

			return Continue, nil
		}

		log.Info("Failover is allowed", "vrgs count", len(vrgs), "drpc action",
			drpc.Spec.Action, "vrg action", vrg.Spec.Action, "dstCluster/clusterName", dstCluster+"/"+clusterName)

		return AllowFailover, nil
	}

	// IF none of the above, then allow failover (set PeerReady), but stop until someone makes the change
	log.Info("Failover is allowed, but user intervention is required")

	return AllowFailover, nil
}
