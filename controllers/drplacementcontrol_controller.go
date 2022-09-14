/*
Copyright 2021 The RamenDR authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

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

	// SanityCheckDelay is used to frequencly update the DRPC status when the reconciler is idle.
	// This is needed in order to sync up the DRPC status and the VRG status.
	SanityCheckDelay = time.Minute * 10
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

func SetDRPCStatusCondition(conditions *[]metav1.Condition, condType string,
	observedGeneration int64, status metav1.ConditionStatus, reason, msg string) bool {
	newCondition := metav1.Condition{
		Type:               condType,
		Status:             status,
		ObservedGeneration: observedGeneration,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            msg,
	}

	existingCondition := findCondition(*conditions, condType)
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

	r.eventRecorder = rmnutil.NewEventReporter(mgr.GetEventRecorderFor("controller_DRPlacementControl"))

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(ctrlcontroller.Options{MaxConcurrentReconciles: getMaxConcurrentReconciles(ctrl.Log)}).
		For(&rmn.DRPlacementControl{}).
		Watches(&source.Kind{Type: &ocmworkv1.ManifestWork{}}, mwMapFun, builder.WithPredicates(mwPred)).
		Watches(&source.Kind{Type: &viewv1beta1.ManagedClusterView{}}, mcvMapFun, builder.WithPredicates(mcvPred)).
		Watches(&source.Kind{Type: &plrv1.PlacementRule{}}, usrPlRuleMapFun, builder.WithPredicates(usrPlRulePred)).
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
			logger.Info(fmt.Sprintf("DRCP object not found %v", req.NamespacedName))
			// Request object not found, could have been deleted after reconcile request.
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, errorswrapper.Wrap(err, "failed to get DRPC object")
	}

	usrPlRule, err := r.getUserPlacementRule(ctx, drpc, logger)
	if err != nil {
		r.recordFailure(drpc, usrPlRule, "Error", err.Error(), logger)

		return ctrl.Result{}, err
	}

	// If either drpc or User PlacementRule is deleted, then we must cleanup.
	if r.isBeingDeleted(drpc, usrPlRule) {
		// DPRC depends on User PlacementRule. If DRPC or/and the User PlacementRule is deleted,
		// then the DRPC should be deleted as well. The least we should do here is to clean up DPRC.
		return r.processDeletion(ctx, drpc, usrPlRule, logger)
	}

	d, err := r.createDRPCInstance(ctx, drpc, usrPlRule, logger)
	if err != nil && !errorswrapper.Is(err, InitialWaitTimeForDRPCPlacementRule) {
		r.recordFailure(drpc, usrPlRule, "Error", err.Error(), logger)

		return ctrl.Result{}, err
	}

	if errorswrapper.Is(err, InitialWaitTimeForDRPCPlacementRule) {
		const initialWaitTime = 5

		r.recordFailure(drpc, usrPlRule, "Waiting",
			fmt.Sprintf("%v - wait time: %v", InitialWaitTimeForDRPCPlacementRule, initialWaitTime), logger)

		return ctrl.Result{RequeueAfter: time.Second * initialWaitTime}, nil
	}

	return r.reconcileDRPCInstance(d, logger)
}

func (r *DRPlacementControlReconciler) recordFailure(drpc *rmn.DRPlacementControl,
	usrPlRule *plrv1.PlacementRule, reason, msg string, log logr.Logger) {
	needsUpdate := SetDRPCStatusCondition(&drpc.Status.Conditions, rmn.ConditionAvailable,
		drpc.Generation, metav1.ConditionFalse, reason, msg)
	if needsUpdate {
		err := r.updateDRPCStatus(drpc, usrPlRule, log)
		if err != nil {
			log.Info(fmt.Sprintf("Failed to update DRPC status (%v)", err))
		}
	}
}

//nolint:funlen,cyclop
func (r *DRPlacementControlReconciler) createDRPCInstance(ctx context.Context,
	drpc *rmn.DRPlacementControl, usrPlRule *plrv1.PlacementRule, log logr.Logger) (*DRPCInstance, error) {
	drPolicy, err := r.getDRPolicy(ctx, drpc, log)
	if err != nil {
		return nil, fmt.Errorf("failed to get DRPolicy %w", err)
	}

	if err := r.addLabelsAndFinalizers(ctx, drpc, usrPlRule, log); err != nil {
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
	drpcPlRule, err := r.getDRPCPlacementRule(ctx, drpc, usrPlRule, drPolicy, log)
	if err != nil {
		return nil, err
	}

	// Make sure that we give time to the DRPC PlacementRule to run and produces decisions
	if drpcPlRule != nil && len(drpcPlRule.Status.Decisions) == 0 {
		return nil, fmt.Errorf("%w", InitialWaitTimeForDRPCPlacementRule)
	}

	vrgs, err := r.getVRGsFromManagedClusters(drpc, drPolicy, log)
	if err != nil {
		return nil, err
	}

	_, ramenConfig, err := ConfigMapGet(ctx, r.APIReader)
	if err != nil {
		return nil, fmt.Errorf("configmap get: %w", err)
	}

	d := &DRPCInstance{
		reconciler:        r,
		ctx:               ctx,
		log:               log,
		instance:          drpc,
		userPlacementRule: usrPlRule,
		drpcPlacementRule: drpcPlRule,
		drPolicy:          drPolicy,
		drClusters:        drClusters,
		vrgs:              vrgs,
		volSyncDisabled:   ramenConfig.VolSync.Disabled,
		mwu: rmnutil.MWUtil{
			Client:        r.Client,
			Ctx:           ctx,
			Log:           log,
			InstName:      drpc.Name,
			InstNamespace: drpc.Namespace,
		},
	}

	isMetro, _ := dRPolicySupportsMetro(drPolicy, drClusters)
	if isMetro {
		d.volSyncDisabled = true

		log.Info("volsync is set to disabled")
	}

	// Save the instance status
	d.instance.Status.DeepCopyInto(&d.savedInstanceStatus)
	log.Info(fmt.Sprintf("PlacementRule Status is: (%+v)", usrPlRule.Status))

	return d, nil
}

// isBeingDeleted returns true if DRPC or User PlacementRule are being deleted
func (r *DRPlacementControlReconciler) isBeingDeleted(drpc *rmn.DRPlacementControl,
	usrPlRule *plrv1.PlacementRule) bool {
	return !drpc.GetDeletionTimestamp().IsZero() ||
		(usrPlRule != nil && !usrPlRule.GetDeletionTimestamp().IsZero())
}

func (r *DRPlacementControlReconciler) reconcileDRPCInstance(d *DRPCInstance, log logr.Logger) (ctrl.Result, error) {
	// Last status update time BEFORE we start processing
	beforeProcessing := d.instance.Status.LastUpdateTime

	requeue := d.startProcessing()
	log.Info("Finished processing", "Requeue?", requeue)

	if !requeue {
		log.Info("Done reconciling", "state", d.getLastDRState())
		r.Callback(d.instance.Name, string(d.getLastDRState()))
	}

	if d.mcvRequestInProgress {
		duration := d.getRequeueDuration()
		log.Info(fmt.Sprintf("Requeing after %v", duration))

		return reconcile.Result{RequeueAfter: duration}, nil
	}

	if requeue {
		log.Info("Requeing...")

		return ctrl.Result{Requeue: true}, nil
	}

	// Last status update time AFTER processing
	afterProcessing := d.instance.Status.LastUpdateTime
	requeueTimeDuration := r.getSanityCheckDelay(beforeProcessing, afterProcessing)
	log.Info("Requeue time", "duration", requeueTimeDuration)

	return ctrl.Result{RequeueAfter: requeueTimeDuration}, nil
}

func (r *DRPlacementControlReconciler) getDRPolicy(ctx context.Context,
	drpc *rmn.DRPlacementControl, log logr.Logger) (*rmn.DRPolicy, error) {
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
	drpc *rmn.DRPlacementControl, usrPlRule *plrv1.PlacementRule, log logr.Logger) error {
	// add label and finalizer to DRPC
	labelAdded := rmnutil.AddLabel(drpc, rmnutil.OCMBackupLabelKey, rmnutil.OCMBackupLabelValue)
	finalizerAdded := rmnutil.AddFinalizer(drpc, DRPCFinalizer)

	if labelAdded || finalizerAdded {
		if err := r.Update(ctx, drpc); err != nil {
			log.Error(err, "Failed to add label and finalizer to drpc")

			return fmt.Errorf("%w", err)
		}
	}

	// add finalizer to User PlacementRule
	finalizerAdded = rmnutil.AddFinalizer(usrPlRule, DRPCFinalizer)
	if finalizerAdded {
		if err := r.Update(ctx, usrPlRule); err != nil {
			log.Error(err, "Failed to add finalizer to user placement rule")

			return fmt.Errorf("%w", err)
		}
	}

	return nil
}

func (r *DRPlacementControlReconciler) processDeletion(ctx context.Context,
	drpc *rmn.DRPlacementControl, usrPlRule *plrv1.PlacementRule, log logr.Logger) (ctrl.Result, error) {
	log.Info("Processing DRPC deletion")

	if !controllerutil.ContainsFinalizer(drpc, DRPCFinalizer) {
		return ctrl.Result{}, nil
	}

	// Run finalization logic for dprc.
	// If the finalization logic fails, don't remove the finalizer so
	// that we can retry during the next reconciliation.
	if err := r.finalizeDRPC(ctx, drpc, log); err != nil {
		return ctrl.Result{}, err
	}

	if usrPlRule != nil && controllerutil.ContainsFinalizer(usrPlRule, DRPCFinalizer) {
		// Remove DRPCFinalizer from User PlacementRule.
		controllerutil.RemoveFinalizer(usrPlRule, DRPCFinalizer)

		err := r.Update(ctx, usrPlRule)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update User PlacementRule %w", err)
		}
	}

	// Remove DRPCFinalizer from DRPC.
	controllerutil.RemoveFinalizer(drpc, DRPCFinalizer)

	err := r.Update(ctx, drpc)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update drpc %w", err)
	}

	r.Callback(drpc.Name, "deleted")

	return ctrl.Result{}, nil
}

func (r *DRPlacementControlReconciler) finalizeDRPC(ctx context.Context, drpc *rmn.DRPlacementControl,
	log logr.Logger) error {
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

	mwu := rmnutil.MWUtil{Client: r.Client, Ctx: ctx, Log: r.Log, InstName: drpc.Name, InstNamespace: drpc.Namespace}

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
	vrgs, err := r.getVRGsFromManagedClusters(drpc, drPolicy, log)
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
	drpc *rmn.DRPlacementControl, clusterNames []string) error {
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
	drpc *rmn.DRPlacementControl, usrPlRule *plrv1.PlacementRule,
	drPolicy *rmn.DRPolicy, log logr.Logger) (*plrv1.PlacementRule, error) {
	var drpcPlRule *plrv1.PlacementRule
	// create the cloned placementrule if and only if the Spec.PreferredCluster is not provided
	if drpc.Spec.PreferredCluster == "" {
		var err error

		drpcPlRule, err = r.getOrClonePlacementRule(ctx, drpc, drPolicy, usrPlRule, log)
		if err != nil {
			log.Error(err, "failed to get DRPC PlacementRule")

			return nil, err
		}
	} else {
		log.Info("Preferred cluster is configured. Dynamic selection is disabled",
			"PreferredCluster", drpc.Spec.PreferredCluster)
	}

	return drpcPlRule, nil
}

func (r *DRPlacementControlReconciler) getUserPlacementRule(ctx context.Context,
	drpc *rmn.DRPlacementControl, log logr.Logger) (*plrv1.PlacementRule, error) {
	log.Info("Getting User PlacementRule", "placement", drpc.Spec.PlacementRef)

	if drpc.Spec.PlacementRef.Namespace == "" {
		drpc.Spec.PlacementRef.Namespace = drpc.Namespace
	}

	usrPlRule := &plrv1.PlacementRule{}

	err := r.Client.Get(ctx,
		types.NamespacedName{Name: drpc.Spec.PlacementRef.Name, Namespace: drpc.Spec.PlacementRef.Namespace},
		usrPlRule)
	if err != nil {
		if errors.IsNotFound(err) && !drpc.GetDeletionTimestamp().IsZero() {
			return nil, nil
		}

		return nil, fmt.Errorf("failed to get placementrule error: %w", err)
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

	if usrPlRule.GetDeletionTimestamp().IsZero() {
		if err = r.annotatePlacementRule(ctx, drpc, usrPlRule, log); err != nil {
			return nil, err
		}
	}

	return usrPlRule, nil
}

func (r *DRPlacementControlReconciler) annotatePlacementRule(ctx context.Context,
	drpc *rmn.DRPlacementControl, plRule *plrv1.PlacementRule, log logr.Logger) error {
	if plRule.ObjectMeta.Annotations == nil {
		plRule.ObjectMeta.Annotations = map[string]string{}
	}

	ownerName := plRule.ObjectMeta.Annotations[DRPCNameAnnotation]
	ownerNamespace := plRule.ObjectMeta.Annotations[DRPCNamespaceAnnotation]

	if ownerName == "" {
		plRule.ObjectMeta.Annotations[DRPCNameAnnotation] = drpc.Name
		plRule.ObjectMeta.Annotations[DRPCNamespaceAnnotation] = drpc.Namespace

		err := r.Update(ctx, plRule)
		if err != nil {
			log.Error(err, "Failed to update PlacementRule annotation", "PlRuleName", plRule.Name)

			return fmt.Errorf("failed to update PlacementRule %s annotation '%s/%s' (%w)",
				plRule.Name, DRPCNameAnnotation, drpc.Name, err)
		}

		return nil
	}

	if ownerName != drpc.Name || ownerNamespace != drpc.Namespace {
		log.Info("PlacementRule not owned by this DRPC", "PlRuleName", plRule.Name)

		return fmt.Errorf("PlacementRule %s not owned by this DRPC '%s/%s'",
			plRule.Name, drpc.Name, drpc.Namespace)
	}

	return nil
}

func (r *DRPlacementControlReconciler) getOrClonePlacementRule(ctx context.Context,
	drpc *rmn.DRPlacementControl, drPolicy *rmn.DRPolicy,
	userPlRule *plrv1.PlacementRule, log logr.Logger) (*plrv1.PlacementRule, error) {
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
	clonedPlRuleName, namespace string, log logr.Logger) (*plrv1.PlacementRule, error) {
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
	clonedPlRuleName string, log logr.Logger) (*plrv1.PlacementRule, error) {
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
	drPolicy *rmn.DRPolicy, log logr.Logger) (map[string]*rmn.VolumeReplicationGroup, error) {
	vrgs := map[string]*rmn.VolumeReplicationGroup{}

	annotations := make(map[string]string)

	annotations[DRPCNameAnnotation] = drpc.Name
	annotations[DRPCNamespaceAnnotation] = drpc.Namespace

	for _, drCluster := range rmnutil.DrpolicyClusterNames(drPolicy) {
		vrg, err := r.MCVGetter.GetVRGFromManagedCluster(drpc.Name, drpc.Namespace, drCluster, annotations)
		if err != nil {
			// Only NotFound error is accepted
			if errors.IsNotFound(err) {
				log.Info(fmt.Sprintf("VRG not found on %q", drCluster))

				continue
			}

			if drpc.Spec.Action == rmn.ActionFailover && drpc.Spec.FailoverCluster != drCluster {
				log.Info(fmt.Sprintf("Skipping fetching VRG from %s due to failure. Error (%v)",
					drCluster, err))

				continue
			}

			return vrgs, fmt.Errorf("failed to retrieve VRG from %s. err (%w)", drCluster, err)
		}

		vrgs[drCluster] = vrg

		log.Info("VRG location", "VRG on", drCluster)
	}

	return vrgs, nil
}

func (r *DRPlacementControlReconciler) deleteClonedPlacementRule(ctx context.Context,
	name, namespace string, log logr.Logger) error {
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
	drPolicy *rmn.DRPolicy, plRule *plrv1.PlacementRule, log logr.Logger) error {
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
// DRPC status is updated at least once every SanityCheckDelay in order to refresh
// the VRG status.
func (d *DRPCInstance) statusUpdateTimeElapsed() bool {
	return d.instance.Status.LastUpdateTime.Add(SanityCheckDelay).Before(time.Now())
}

// getSanityCheckDelay returns the reconciliation requeue time duration when no requeue
// has been requested. We want the reconciliation to run at least once every SanityCheckDelay
// in order to refresh DRPC status with VRG status. The reconciliation will be called at any time.
// If it is called before the SanityCheckDelay has elapsed, and the DRPC status was not updated,
// then we must return the remaining time rather than the full SanityCheckDelay to prevent
// starving the status update, which is scheduled for at least once every SanityCheckDelay.
//
// Example: Assume at 10:00am was the last time when the reconciler ran and updated the status.
// The SanityCheckDelay is hard coded to 10 minutes.  If nothing is happening in the system that
// requires the reconciler to run, then the next run would be at 10:10am. If however, for any reason
// the reconciler is called, let's say, at 10:08am, and no update to the DRPC status was needed,
// then the requeue time duration should be 2 minutes and NOT the full SanityCheckDelay. That is:
// 10:00am + SanityCheckDelay - 10:08am = 2mins
func (r *DRPlacementControlReconciler) getSanityCheckDelay(
	beforeProcessing metav1.Time, afterProcessing metav1.Time) time.Duration {
	if beforeProcessing != afterProcessing {
		// DRPC's VRG status update processing time has changed during this
		// iteration of the reconcile loop.  Hence, the next attempt to update
		// the status should be after a delay of a standard polling interval
		// duration.
		return SanityCheckDelay
	}

	// DRPC's VRG status update processing time has NOT changed during this
	// iteration of the reconcile loop.  Hence, the next attempt to update the
	// status should be after the remaining duration of this polling interval has
	// elapsed: (beforeProcessing + SantityCheckDelay - time.Now())
	return time.Until(beforeProcessing.Add(SanityCheckDelay))
}

func (r *DRPlacementControlReconciler) updateUserPlacementRuleStatus(
	usrPlRule *plrv1.PlacementRule, newStatus plrv1.PlacementRuleStatus, log logr.Logger) error {
	if !reflect.DeepEqual(newStatus, usrPlRule.Status) {
		usrPlRule.Status = newStatus
		if err := r.Status().Update(context.TODO(), usrPlRule); err != nil {
			log.Error(err, "failed to update user PlacementRule")

			return fmt.Errorf("failed to update userPlRule %s (%w)", usrPlRule.Name, err)
		}

		log.Info("Updated user PlacementRule status", "Decisions", usrPlRule.Status.Decisions)
	}

	return nil
}

func (r *DRPlacementControlReconciler) updateDRPCStatus(
	drpc *rmn.DRPlacementControl, usrPlRule *plrv1.PlacementRule, log logr.Logger) error {
	log.Info("Updating DRPC status")

	annotations := make(map[string]string)

	annotations[DRPCNameAnnotation] = drpc.Name
	annotations[DRPCNamespaceAnnotation] = drpc.Namespace

	if usrPlRule != nil && len(usrPlRule.Status.Decisions) != 0 {
		vrg, err := r.MCVGetter.GetVRGFromManagedCluster(drpc.Name, drpc.Namespace,
			usrPlRule.Status.Decisions[0].ClusterName, annotations)
		if err != nil {
			// VRG must have been deleted if the error is NotFound. In either case,
			// we don't have a VRG
			log.Info("Failed to get VRG from managed cluster", "errMsg", err)

			drpc.Status.ResourceConditions = rmn.VRGConditions{}
			drpc.Status.LastGroupSyncTime = nil
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
		}
	}

	drpc.Status.LastUpdateTime = metav1.Now()
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
