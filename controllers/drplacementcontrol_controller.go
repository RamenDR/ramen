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
	"github.com/prometheus/client_golang/prometheus"
	viewv1beta1 "github.com/stolostron/multicloud-operators-foundation/pkg/apis/view/v1beta1"
	plrv1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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

	PlacementDecisionName = "%s-decision-1"
)

var InitialWaitTimeForDRPCPlacementRule = errorswrapper.New("Waiting for DRPC Placement to produces placement decision")

// ProgressCallback of function type
type ProgressCallback func(string, string)

// DRPlacementControlReconciler reconciles a DRPlacementControl object
type DRPlacementControlReconciler struct {
	client.Client
	APIReader     client.Reader
	Log           logr.Logger
	MCVGetter     rmnutil.ManagedClusterViewGetter
	Scheme        *runtime.Scheme
	Callback      ProgressCallback
	eventRecorder *rmnutil.EventReporter
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
			log := ctrl.Log.WithName("ManifestWork")

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
	log := ctrl.Log.WithName("MCV")
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
	log := ctrl.Log.WithName("UserPlRule")
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
	log := ctrl.Log.WithName("UserPlmnt")
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

func SetDRPCStatusCondition(conditions *[]metav1.Condition, conditionType string,
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

		ctrl.Log.Info(fmt.Sprintf("Filtering ManifestWork (%s/%s)", mw.Name, mw.Namespace))

		return filterMW(mw)
	}))

	mcvPred := ManagedClusterViewPredicateFunc()

	mcvMapFun := handler.EnqueueRequestsFromMapFunc(handler.MapFunc(func(obj client.Object) []reconcile.Request {
		mcv, ok := obj.(*viewv1beta1.ManagedClusterView)
		if !ok {
			return []reconcile.Request{}
		}

		ctrl.Log.Info(fmt.Sprintf("Filtering MCV (%s/%s)", mcv.Name, mcv.Namespace))

		return filterMCV(mcv)
	}))

	usrPlRulePred := PlacementRulePredicateFunc()

	usrPlRuleMapFun := handler.EnqueueRequestsFromMapFunc(handler.MapFunc(func(obj client.Object) []reconcile.Request {
		usrPlRule, ok := obj.(*plrv1.PlacementRule)
		if !ok {
			return []reconcile.Request{}
		}

		ctrl.Log.Info(fmt.Sprintf("Filtering User PlacementRule (%s/%s)", usrPlRule.Name, usrPlRule.Namespace))

		return filterUsrPlRule(usrPlRule)
	}))

	usrPlmntPred := PlacementPredicateFunc()

	usrPlmntMapFun := handler.EnqueueRequestsFromMapFunc(handler.MapFunc(func(obj client.Object) []reconcile.Request {
		usrPlmnt, ok := obj.(*clrapiv1beta1.Placement)
		if !ok {
			return []reconcile.Request{}
		}

		ctrl.Log.Info(fmt.Sprintf("Filtering User Placement (%s/%s)", usrPlmnt.Name, usrPlmnt.Namespace))

		return filterUsrPlmnt(usrPlmnt)
	}))

	r.eventRecorder = rmnutil.NewEventReporter(mgr.GetEventRecorderFor("controller_DRPlacementControl"))

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(ctrlcontroller.Options{MaxConcurrentReconciles: getMaxConcurrentReconciles(ctrl.Log)}).
		For(&rmn.DRPlacementControl{}).
		Watches(&source.Kind{Type: &ocmworkv1.ManifestWork{}}, mwMapFun, builder.WithPredicates(mwPred)).
		Watches(&source.Kind{Type: &viewv1beta1.ManagedClusterView{}}, mcvMapFun, builder.WithPredicates(mcvPred)).
		Watches(&source.Kind{Type: &plrv1.PlacementRule{}}, usrPlRuleMapFun, builder.WithPredicates(usrPlRulePred)).
		Watches(&source.Kind{Type: &clrapiv1beta1.Placement{}}, usrPlmntMapFun, builder.WithPredicates(usrPlmntPred)).
		Complete(r)
}

//nolint:lll
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drplacementcontrols,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drplacementcontrols/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drplacementcontrols/finalizers,verbs=update
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drpolicies,verbs=get;list;watch
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
// +kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=placements,verbs=get;list;watch
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

	placementObj, err := r.getPlacementOrPlacementRule(ctx, drpc, logger)
	if err != nil && !(errors.IsNotFound(err) && !drpc.GetDeletionTimestamp().IsZero()) {
		r.recordFailure(drpc, placementObj, "Error", err.Error(), nil, logger)

		return ctrl.Result{}, err
	}

	if r.isBeingDeleted(drpc, placementObj) {
		// DPRC depends on User PlacementRule/Placement. If DRPC or/and the User PlacementRule is deleted,
		// then the DRPC should be deleted as well. The least we should do here is to clean up DPRC.
		return r.processDeletion(ctx, drpc, placementObj, logger)
	}

	d, err := r.createDRPCInstance(ctx, drpc, placementObj, logger)
	if err != nil && !errorswrapper.Is(err, InitialWaitTimeForDRPCPlacementRule) {
		err = r.updateDRPCStatus(drpc, placementObj, nil, logger)

		return ctrl.Result{}, err
	}

	if errorswrapper.Is(err, InitialWaitTimeForDRPCPlacementRule) {
		const initialWaitTime = 5

		r.recordFailure(drpc, placementObj, "Waiting",
			fmt.Sprintf("%v - wait time: %v", InitialWaitTimeForDRPCPlacementRule, initialWaitTime), nil, logger)

		return ctrl.Result{RequeueAfter: time.Second * initialWaitTime}, nil
	}

	return r.reconcileDRPCInstance(d, logger)
}

func (r *DRPlacementControlReconciler) recordFailure(drpc *rmn.DRPlacementControl,
	placementObj client.Object, reason, msg string, syncMetrics *SyncMetrics, log logr.Logger,
) {
	needsUpdate := SetDRPCStatusCondition(&drpc.Status.Conditions, rmn.ConditionAvailable,
		drpc.Generation, metav1.ConditionFalse, reason, msg)
	if needsUpdate {
		err := r.updateDRPCStatus(drpc, placementObj, syncMetrics, log)
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

//nolint:funlen,cyclop
func (r *DRPlacementControlReconciler) createDRPCInstance(ctx context.Context,
	drpc *rmn.DRPlacementControl, placementObj client.Object, log logr.Logger,
) (*DRPCInstance, error) {
	drPolicy, err := r.getDRPolicy(ctx, drpc, log)
	if err != nil {
		return nil, fmt.Errorf("failed to get DRPolicy %w", err)
	}

	if err := r.addLabelsAndFinalizers(ctx, drpc, placementObj, log); err != nil {
		return nil, err
	}

	if err := rmnutil.DrpolicyValidated(drPolicy); err != nil {
		return nil, fmt.Errorf("DRPolicy not valid %w", err)
	}

	drClusters := []rmn.DRCluster{}

	for _, managedCluster := range rmnutil.DrpolicyClusterNames(drPolicy) {
		drCluster := &rmn.DRCluster{}

		err := r.Client.Get(ctx, types.NamespacedName{Name: managedCluster}, drCluster)
		if err != nil {
			return nil, fmt.Errorf("failed to get DRCluster (%s) %w", managedCluster, err)
		}

		// TODO: What if the DRCluster is deleted? If new DRPC fail reconciliation
		drClusters = append(drClusters, *drCluster)
	}

	// We only create DRPC PlacementRule if the preferred cluster is not configured
	err = r.getDRPCPlacementRule(ctx, drpc, placementObj, drPolicy, log)
	if err != nil {
		return nil, err
	}

	vrgNamespace, err := r.selectVRGNamespace(drpc, placementObj)
	if err != nil {
		return nil, err
	}

	vrgs, err := r.getVRGsFromManagedClusters(drpc, drPolicy, vrgNamespace, log)
	if err != nil {
		return nil, err
	}

	_, ramenConfig, err := ConfigMapGet(ctx, r.APIReader)
	if err != nil {
		return nil, fmt.Errorf("configmap get: %w", err)
	}

	metrics := NewSyncMetrics(prometheus.Labels{
		ObjType:            "DRPlacementControl",
		ObjName:            drpc.Name,
		ObjNamespace:       drpc.Namespace,
		Policyname:         drPolicy.Name,
		SchedulingInterval: drPolicy.Spec.SchedulingInterval,
	})

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
		metrics:         &metrics,
		mwu: rmnutil.MWUtil{
			Client:          r.Client,
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

// isBeingDeleted returns true if either DRPC, user placement, or both are being deleted
func (r *DRPlacementControlReconciler) isBeingDeleted(drpc *rmn.DRPlacementControl, usrPl client.Object) bool {
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

	if !drPolicy.ObjectMeta.DeletionTimestamp.IsZero() &&
		!controllerutil.ContainsFinalizer(drpc, DRPCFinalizer) {
		// If drpolicy is deleted and drpc finalizer is not present then return
		// error to fail drpc reconciliation
		return nil, fmt.Errorf("drPolicy '%s/%s' referred by the DRPC is deleted, DRPC reconciliation would fail",
			name, namespace)
	}

	return drPolicy, nil
}

func (r DRPlacementControlReconciler) addLabelsAndFinalizers(ctx context.Context,
	drpc *rmn.DRPlacementControl, placementObj client.Object, log logr.Logger,
) error {
	// add label and finalizer to DRPC
	labelAdded := rmnutil.AddLabel(drpc, rmnutil.OCMBackupLabelKey, rmnutil.OCMBackupLabelValue)
	finalizerAdded := rmnutil.AddFinalizer(drpc, DRPCFinalizer)

	if labelAdded || finalizerAdded {
		if err := r.Update(ctx, drpc); err != nil {
			log.Error(err, "Failed to add label and finalizer to drpc")

			return fmt.Errorf("%w", err)
		}
	}

	// add finalizer to User PlacementRule/Placement
	finalizerAdded = rmnutil.AddFinalizer(placementObj, DRPCFinalizer)
	if finalizerAdded {
		if err := r.Update(ctx, placementObj); err != nil {
			log.Error(err, "Failed to add finalizer to user placement rule")

			return fmt.Errorf("%w", err)
		}
	}

	return nil
}

func (r *DRPlacementControlReconciler) processDeletion(ctx context.Context,
	drpc *rmn.DRPlacementControl, placementObj client.Object, log logr.Logger,
) (ctrl.Result, error) {
	log.Info("Processing DRPC deletion")

	if !controllerutil.ContainsFinalizer(drpc, DRPCFinalizer) {
		return ctrl.Result{}, nil
	}

	// Run finalization logic for dprc.
	// If the finalization logic fails, don't remove the finalizer so
	// that we can retry during the next reconciliation.
	if err := r.finalizeDRPC(ctx, drpc, placementObj, log); err != nil {
		return ctrl.Result{}, err
	}

	if placementObj != nil && controllerutil.ContainsFinalizer(placementObj, DRPCFinalizer) {
		// Remove DRPCFinalizer from User PlacementRule/Placement.
		controllerutil.RemoveFinalizer(placementObj, DRPCFinalizer)

		err := r.Update(ctx, placementObj)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update User PlacementRule/Placement %w", err)
		}
	}

	// Remove DRPCFinalizer from DRPC.
	controllerutil.RemoveFinalizer(drpc, DRPCFinalizer)

	if err := r.Update(ctx, drpc); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update drpc %w", err)
	}

	r.Callback(drpc.Name, "deleted")

	return ctrl.Result{}, nil
}

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

	vrgNamespace, err := r.selectVRGNamespace(drpc, placementObj)
	if err != nil {
		return err
	}

	mwu := rmnutil.MWUtil{
		Client:          r.Client,
		Ctx:             ctx,
		Log:             r.Log,
		InstName:        drpc.Name,
		TargetNamespace: drpc.Status.PreferredDecision.ClusterNamespace,
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

	// Verify VRGs have been deleted
	vrgs, err := r.getVRGsFromManagedClusters(drpc, drPolicy, vrgNamespace, log)
	if err != nil {
		return fmt.Errorf("failed to retrieve VRGs. We'll retry later. Error (%w)", err)
	}

	if len(vrgs) != 0 {
		return fmt.Errorf("waiting for VRGs count to go to zero")
	}

	// delete MCVs used in the previous call
	return r.deleteAllManagedClusterViews(drpc, rmnutil.DrpolicyClusterNames(drPolicy))
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

func (r *DRPlacementControlReconciler) getPlacementOrPlacementRule(ctx context.Context,
	drpc *rmn.DRPlacementControl, log logr.Logger,
) (client.Object, error) {
	log.Info("Getting user placement object", "placementRef", drpc.Spec.PlacementRef)

	var usrPlacement client.Object

	var err error

	usrPlacement, err = r.getPlacementRule(ctx, drpc, log)
	if err != nil {
		if errors.IsNotFound(err) {
			// PacementRule not found. Check Placement instead
			usrPlacement, err = r.getPlacement(ctx, drpc, log)
		}

		if err != nil {
			return nil, err
		}
	} else {
		// Assert that there is no Placement object in the same namespace and with the same name as the PlacementRule
		_, err = r.getPlacement(ctx, drpc, log)
		if err == nil {
			return nil, fmt.Errorf(
				"can't proceed. PlacementRule and Placement CR with the same name exist on the same namespace")
		}
	}

	if err = r.annotateUserPlacement(ctx, drpc, usrPlacement, log); err != nil {
		return nil, err
	}

	log.Info(fmt.Sprintf("Using placement %v", usrPlacement))

	return usrPlacement, nil
}

func (r *DRPlacementControlReconciler) getPlacementRule(ctx context.Context,
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

	err := r.Client.Get(ctx,
		types.NamespacedName{Name: drpc.Spec.PlacementRef.Name, Namespace: plRuleNamespace}, usrPlRule)
	if err != nil {
		log.Info(fmt.Sprintf("Get PlacementRule returned: %v", err))

		return nil, err
	}

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

	return usrPlRule, nil
}

func (r *DRPlacementControlReconciler) getPlacement(ctx context.Context,
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

	err := r.Client.Get(ctx,
		types.NamespacedName{Name: drpc.Spec.PlacementRef.Name, Namespace: plmntNamespace}, usrPlmnt)
	if err != nil {
		log.Info(fmt.Sprintf("Get Placement returned: %v", err))

		return nil, err
	}

	if value, ok := usrPlmnt.GetAnnotations()[clrapiv1beta1.PlacementDisableAnnotation]; !ok || value == "false" {
		return nil, fmt.Errorf("placement %s must be disabled in order for Ramen to be the scheduler",
			usrPlmnt.Name)
	}

	if usrPlmnt.Spec.NumberOfClusters == nil || *usrPlmnt.Spec.NumberOfClusters != 1 {
		log.Info("User Placement number of clusters is not set to 1, reconciliation will only" +
			" schedule it to a single cluster")
	}

	return usrPlmnt, nil
}

func (r *DRPlacementControlReconciler) annotateUserPlacement(ctx context.Context,
	drpc *rmn.DRPlacementControl, placementObj client.Object, log logr.Logger,
) error {
	if !placementObj.GetDeletionTimestamp().IsZero() {
		return nil
	}

	if placementObj.GetAnnotations() == nil {
		placementObj.SetAnnotations(map[string]string{})
	}

	ownerName := placementObj.GetAnnotations()[DRPCNameAnnotation]
	ownerNamespace := placementObj.GetAnnotations()[DRPCNamespaceAnnotation]

	if ownerName == "" {
		placementObj.GetAnnotations()[DRPCNameAnnotation] = drpc.Name
		placementObj.GetAnnotations()[DRPCNamespaceAnnotation] = drpc.Namespace

		err := r.Update(ctx, placementObj)
		if err != nil {
			log.Error(err, "Failed to update PlacementRule annotation", "placementObjName", placementObj.GetName())

			return fmt.Errorf("failed to update PlacementRule %s annotation '%s/%s' (%w)",
				placementObj.GetName(), DRPCNameAnnotation, drpc.Name, err)
		}

		return nil
	}

	if ownerName != drpc.Name || ownerNamespace != drpc.Namespace {
		log.Info("PlacementRule not owned by this DRPC", "placementObjName", placementObj.GetName())

		return fmt.Errorf("PlacementRule %s not owned by this DRPC '%s/%s'",
			placementObj.GetName(), drpc.Name, drpc.Namespace)
	}

	return nil
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

func (r *DRPlacementControlReconciler) getVRGsFromManagedClusters(drpc *rmn.DRPlacementControl,
	drPolicy *rmn.DRPolicy, vrgNamespace string, log logr.Logger,
) (map[string]*rmn.VolumeReplicationGroup, error) {
	vrgs := map[string]*rmn.VolumeReplicationGroup{}

	annotations := make(map[string]string)

	annotations[DRPCNameAnnotation] = drpc.Name
	annotations[DRPCNamespaceAnnotation] = drpc.Namespace

	var failedClusterToQuery string

	var clustersQueriedSuccessfully int

	for _, drCluster := range rmnutil.DrpolicyClusterNames(drPolicy) {
		vrg, err := r.MCVGetter.GetVRGFromManagedCluster(drpc.Name, vrgNamespace, drCluster, annotations)
		if err != nil {
			// Only NotFound error is accepted
			if errors.IsNotFound(err) {
				log.Info(fmt.Sprintf("VRG not found on %q", drCluster))
				clustersQueriedSuccessfully++

				continue
			}

			failedClusterToQuery = drCluster

			log.Info(fmt.Sprintf("failed to retrieve VRG from %s. err (%v)", drCluster, err))

			continue
		}

		clustersQueriedSuccessfully++

		vrgs[drCluster] = vrg

		log.Info("VRG location", "VRG on", drCluster)
	}

	// We are done if we successfully queried all drClusters
	if clustersQueriedSuccessfully == len(rmnutil.DrpolicyClusterNames(drPolicy)) {
		return vrgs, nil
	}

	if clustersQueriedSuccessfully == 0 {
		return vrgs, fmt.Errorf("failed to retrieve VRGs from clusters")
	}

	if len(vrgs) == 0 && failedClusterToQuery != drpc.Spec.FailoverCluster {
		condition := findCondition(drpc.Status.Conditions, rmn.ConditionPeerReady)
		if condition == nil {
			SetDRPCStatusCondition(&drpc.Status.Conditions, rmn.ConditionPeerReady, drpc.Generation,
				metav1.ConditionTrue, rmn.ReasonSuccess, "Forcing Ready")
			SetDRPCStatusCondition(&drpc.Status.Conditions, rmn.ConditionAvailable,
				drpc.Generation, metav1.ConditionTrue, rmn.ReasonSuccess, "Forcing Available")
		}
	}

	return vrgs, nil
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
	drpc *rmn.DRPlacementControl, userPlacement client.Object, syncmetric *SyncMetrics, log logr.Logger,
) error {
	log.Info("Updating DRPC status")

	vrgNamespace, err := r.selectVRGNamespace(drpc, userPlacement)
	if err != nil {
		log.Info("Failed to select VRG namespace", "errMsg", err)
	}

	clusterDecision := r.getClusterDecision(userPlacement)
	if clusterDecision != nil && clusterDecision.ClusterName != "" && vrgNamespace != "" {
		r.updateResourceCondition(drpc, clusterDecision.ClusterName, vrgNamespace, syncmetric, log)
	}

	now := metav1.Now()
	drpc.Status.LastUpdateTime = &now

	for i, condition := range drpc.Status.Conditions {
		if condition.ObservedGeneration != drpc.Generation {
			drpc.Status.Conditions[i].ObservedGeneration = drpc.Generation
		}
	}

	if err := r.Status().Update(context.TODO(), drpc); err != nil {
		return errorswrapper.Wrap(err, "failed to update DRPC status")
	}

	log.Info(fmt.Sprintf("Updated DRPC Status %+v", drpc.Status))

	return nil
}

func (r *DRPlacementControlReconciler) updateResourceCondition(
	drpc *rmn.DRPlacementControl,
	clusterName, vrgNamespace string,
	syncmetric *SyncMetrics, log logr.Logger,
) {
	annotations := make(map[string]string)

	annotations[DRPCNameAnnotation] = drpc.Name
	annotations[DRPCNamespaceAnnotation] = drpc.Namespace

	vrg, err := r.MCVGetter.GetVRGFromManagedCluster(drpc.Name, vrgNamespace,
		clusterName, annotations)
	if err != nil {
		log.Info("Failed to get VRG from managed cluster", "errMsg", err)

		drpc.Status.ResourceConditions = rmn.VRGConditions{}
		drpc.Status.LastGroupSyncTime = nil

		r.setLastSyncTimeMetric(syncmetric, nil, log)
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
		drpc.Status.LastGroupSyncTime = vrg.Status.LastGroupSyncTime

		r.setLastSyncTimeMetric(syncmetric, vrg.Status.LastGroupSyncTime, log)
	}
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

func (r *DRPlacementControlReconciler) getClusterDecisionFromPlacement(placement *clrapiv1beta1.Placement,
) *clrapiv1beta1.ClusterDecision {
	plDecisionName := fmt.Sprintf(PlacementDecisionName, placement.GetName())
	plDecision := &clrapiv1beta1.PlacementDecision{}

	err := r.Get(context.TODO(), types.NamespacedName{Name: plDecisionName, Namespace: placement.Namespace}, plDecision)
	if err != nil {
		if !errors.IsNotFound(err) {
			r.Log.Info("Failed to retrieve PlacementDecision", "pdName", plDecisionName)
		}

		return &clrapiv1beta1.ClusterDecision{}
	}

	r.Log.Info("Found ClusterDecision", "ClsDedicision", plDecision.Status.Decisions)

	var clusterName, reason string
	if len(plDecision.Status.Decisions) > 0 {
		clusterName = plDecision.Status.Decisions[0].ClusterName
		reason = plDecision.Status.Decisions[0].Reason
	}

	return &clrapiv1beta1.ClusterDecision{
		ClusterName: clusterName,
		Reason:      reason,
	}
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

func (r *DRPlacementControlReconciler) createOrUpdatePlacementDecision(ctx context.Context,
	placement *clrapiv1beta1.Placement, newCD *clrapiv1beta1.ClusterDecision,
) error {
	plDecisionName := fmt.Sprintf(PlacementDecisionName, placement.GetName())
	plDecision := &clrapiv1beta1.PlacementDecision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      plDecisionName,
			Namespace: placement.Namespace,
		},
	}

	key := client.ObjectKeyFromObject(plDecision)
	if err := r.Get(ctx, key, plDecision); err != nil {
		if !errors.IsNotFound(err) {
			return err
		}

		// Set the Placement object to be the owner.  When it is deleted, the PlacementDecision is deleted
		if err := ctrl.SetControllerReference(placement, plDecision, r.Client.Scheme()); err != nil {
			return fmt.Errorf("failed to set controller reference %w", err)
		}

		plDecision.ObjectMeta.Labels = map[string]string{
			clrapiv1beta1.PlacementLabel: placement.GetName(),
		}

		owner := metav1.NewControllerRef(placement, clrapiv1beta1.GroupVersion.WithKind("Placement"))
		plDecision.ObjectMeta.OwnerReferences = []metav1.OwnerReference{*owner}

		if err := r.Create(ctx, plDecision); err != nil {
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

func (r *DRPlacementControlReconciler) getApplicationDestinationNamespace(placement client.Object) (string, error) {
	appSetList := argov1alpha1.ApplicationSetList{}
	if err := r.Client.List(context.TODO(), &appSetList); err != nil {
		return "", fmt.Errorf("ApplicationSet list: %w", err)
	}

	r.Log.Info("Retrieved ApplicationSets", "count", len(appSetList.Items))
	//
	// TODO: change the following loop to use an index field on AppSet instead for faster lookup
	//
	for i := range appSetList.Items {
		appSet := &appSetList.Items[i]
		if len(appSet.Spec.Generators) > 0 &&
			appSet.Spec.Generators[0].ClusterDecisionResource != nil {
			pn := appSet.Spec.Generators[0].ClusterDecisionResource.LabelSelector.MatchLabels[clrapiv1beta1.PlacementLabel]
			if pn == placement.GetName() {
				r.Log.Info("Found ApplicationSet for Placement", "name", appSet.Name, "placement", placement.GetName())
				// Retrieving the Destination.Namespace from Application.Spec requires iterating through all Applications
				// and checking their ownerReferences, which can be time-consuming. Alternatively, we can get the same
				// information from the ApplicationSet spec template section as it is done here.
				return appSet.Spec.Template.Spec.Destination.Namespace, nil
			}
		}
	}

	return "", fmt.Errorf("failed to find ApplicationSet/Application for Placement %s", placement.GetName())
}

func (r *DRPlacementControlReconciler) selectVRGNamespace(drpc *rmn.DRPlacementControl, placementObj client.Object,
) (string, error) {
	if drpc.Status.PreferredDecision.ClusterNamespace != "" {
		return drpc.Status.PreferredDecision.ClusterNamespace, nil
	}

	switch placementObj.(type) {
	case *clrapiv1beta1.Placement:
		vrgNamespace, err := r.getApplicationDestinationNamespace(placementObj)
		if err != nil {
			return "", err
		}

		return vrgNamespace, nil
	default:
		return drpc.Namespace, nil
	}
}
