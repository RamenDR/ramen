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
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"time"

	"github.com/go-logr/logr"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	viewv1beta1 "github.com/open-cluster-management/multicloud-operators-foundation/pkg/apis/view/v1beta1"
	plrv1 "github.com/open-cluster-management/multicloud-operators-placementrule/pkg/apis/apps/v1"
	errorswrapper "github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
)

const (
	// DRPC CR finalizer
	DRPCFinalizer string = "drpc.ramendr.openshift.io/finalizer"

	// Ramen scheduler
	RamenScheduler string = "ramen"

	ClonedPlacementRuleNameFormat string = "clonedprule-%s-%s"

	// SanityCheckDelay is used to frequencly update the DRPC status when the reconciler is idle.
	// This is needed in order to sync up the DRPC status and the VRG status.
	SanityCheckDelay = time.Minute * 10
)

var WaitForPVRestoreToComplete = errorswrapper.New("Waiting for PV restore to complete")

// ProgressCallback of function type
type ProgressCallback func(string, string)

type ManagedClusterViewGetter interface {
	GetVRGFromManagedCluster(
		resourceName, resourceNamespace, managedCluster string) (*rmn.VolumeReplicationGroup, error)
}

type ManagedClusterViewGetterImpl struct {
	client.Client
}

func (m ManagedClusterViewGetterImpl) GetVRGFromManagedCluster(
	resourceName, resourceNamespace, managedCluster string) (*rmn.VolumeReplicationGroup, error) {
	logger := ctrl.Log.WithName("MCV").WithValues("resouceName", resourceName)
	// get VRG and verify status through ManagedClusterView
	mcvMeta := metav1.ObjectMeta{
		Name:      BuildManagedClusterViewName(resourceName, resourceNamespace, "vrg"),
		Namespace: managedCluster,
		Annotations: map[string]string{
			rmnutil.DRPCNameAnnotation:      resourceName,
			rmnutil.DRPCNamespaceAnnotation: resourceNamespace,
		},
	}

	mcvViewscope := viewv1beta1.ViewScope{
		Resource:  "VolumeReplicationGroup",
		Name:      resourceName,
		Namespace: resourceNamespace,
	}

	vrg := &rmn.VolumeReplicationGroup{}

	err := m.getManagedClusterResource(mcvMeta, mcvViewscope, vrg, logger)

	return vrg, err
}

/*
Description: queries a managed cluster for a resource type, and populates a variable with the results.
Requires:
	1) meta: information of the new/existing resource; defines which cluster(s) to search
	2) viewscope: query information for managed cluster resource. Example: resource, name.
	3) interface: empty variable to populate results into
Returns: error if encountered (nil if no error occurred). See results on interface object.
*/
func (m ManagedClusterViewGetterImpl) getManagedClusterResource(
	meta metav1.ObjectMeta, viewscope viewv1beta1.ViewScope, resource interface{}, logger logr.Logger) error {
	// create MCV first
	mcv, err := m.getOrCreateManagedClusterView(meta, viewscope, logger)
	if err != nil {
		return errorswrapper.Wrap(err, "getManagedClusterResource failed")
	}

	logger.Info(fmt.Sprintf("MCV condtions: %v", mcv.Status.Conditions))

	// want single recent Condition with correct Type; otherwise: bad path
	switch len(mcv.Status.Conditions) {
	case 0:
		err = fmt.Errorf("missing ManagedClusterView conditions")
	case 1:
		switch {
		case mcv.Status.Conditions[0].Type != viewv1beta1.ConditionViewProcessing:
			err = fmt.Errorf("found invalid condition (%s) in ManagedClusterView", mcv.Status.Conditions[0].Type)
		case mcv.Status.Conditions[0].Reason == viewv1beta1.ReasonGetResourceFailed:
			err = errors.NewNotFound(schema.GroupResource{}, "requested resource not found in ManagedCluster")
		case mcv.Status.Conditions[0].Status != metav1.ConditionTrue:
			err = fmt.Errorf("ManagedClusterView is not ready (reason: %s)", mcv.Status.Conditions[0].Reason)
		}
	default:
		err = fmt.Errorf("found multiple status conditions with ManagedClusterView")
	}

	if err != nil {
		return errorswrapper.Wrap(err, "getManagedClusterResource results")
	}

	// good path: convert raw data to usable object
	err = json.Unmarshal(mcv.Status.Result.Raw, resource)
	if err != nil {
		return errorswrapper.Wrap(err, "failed to Unmarshal data from ManagedClusterView to resource")
	}

	return nil // success
}

/*
Description: create a new ManagedClusterView object, or update the existing one with the same name.
Requires:
	1) meta: specifies MangedClusterView name and managed cluster search information
	2) viewscope: once the managed cluster is found, use this information to find the resource.
		Optional params: Namespace, Resource, Group, Version, Kind. Resource can be used by itself, Kind requires Version
Returns: ManagedClusterView, error
*/
func (m ManagedClusterViewGetterImpl) getOrCreateManagedClusterView(
	meta metav1.ObjectMeta, viewscope viewv1beta1.ViewScope, logger logr.Logger) (*viewv1beta1.ManagedClusterView, error) {
	mcv := &viewv1beta1.ManagedClusterView{
		ObjectMeta: meta,
		Spec: viewv1beta1.ViewSpec{
			Scope: viewscope,
		},
	}

	err := m.Get(context.TODO(), types.NamespacedName{Name: meta.Name, Namespace: meta.Namespace}, mcv)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info(fmt.Sprintf("Creating ManagedClusterView %v", mcv))
			err = m.Create(context.TODO(), mcv)
		}

		if err != nil {
			return nil, errorswrapper.Wrap(err, "failed to getOrCreateManagedClusterView")
		}
	}

	if mcv.Spec.Scope != viewscope {
		logger.Info("WARNING: existing ManagedClusterView has different ViewScope than desired one")
	}

	return mcv, nil
}

// DRPlacementControlReconciler reconciles a DRPlacementControl object
type DRPlacementControlReconciler struct {
	client.Client
	APIReader     client.Reader
	Log           logr.Logger
	MCVGetter     ManagedClusterViewGetter
	Scheme        *runtime.Scheme
	Callback      ProgressCallback
	eventRecorder *rmnutil.EventReporter
}

func ManifestWorkPredicateFunc() predicate.Funcs {
	mwPredicate := predicate.Funcs{
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
	if mw.Annotations[rmnutil.DRPCNameAnnotation] == "" ||
		mw.Annotations[rmnutil.DRPCNamespaceAnnotation] == "" {
		return []ctrl.Request{}
	}

	return []ctrl.Request{
		reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      mw.Annotations[rmnutil.DRPCNameAnnotation],
				Namespace: mw.Annotations[rmnutil.DRPCNamespaceAnnotation],
			},
		},
	}
}

func ManagedClusterViewPredicateFunc() predicate.Funcs {
	log := ctrl.Log.WithName("MCV")
	mcvPredicate := predicate.Funcs{
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
		DeleteFunc: func(e event.DeleteEvent) bool {
			log.Info("Delete event for MCV")

			return false
		},
	}

	return mcvPredicate
}

func filterMCV(mcv *viewv1beta1.ManagedClusterView) []ctrl.Request {
	if mcv.Annotations[rmnutil.DRPCNameAnnotation] == "" ||
		mcv.Annotations[rmnutil.DRPCNamespaceAnnotation] == "" {
		return []ctrl.Request{}
	}

	return []ctrl.Request{
		reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      mcv.Annotations[rmnutil.DRPCNameAnnotation],
				Namespace: mcv.Annotations[rmnutil.DRPCNamespaceAnnotation],
			},
		},
	}
}

func GetDRPCCondition(status *rmn.DRPlacementControlStatus, conditionType string) (int, *metav1.Condition) {
	if len(status.Conditions) == 0 {
		return -1, nil
	}

	for i := range status.Conditions {
		if status.Conditions[i].Type == conditionType {
			return i, &status.Conditions[i]
		}
	}

	return -1, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DRPlacementControlReconciler) SetupWithManager(mgr ctrl.Manager) error {
	mwPred := ManifestWorkPredicateFunc()

	mwMapFun := handler.EnqueueRequestsFromMapFunc(handler.MapFunc(func(obj client.Object) []reconcile.Request {
		mw, ok := obj.(*ocmworkv1.ManifestWork)
		if !ok {
			ctrl.Log.Info("ManifestWork map function received non-MW resource")

			return []reconcile.Request{}
		}

		ctrl.Log.Info(fmt.Sprintf("Filtering ManifestWork (%s/%s)", mw.Name, mw.Namespace))

		return filterMW(mw)
	}))

	mcvPred := ManagedClusterViewPredicateFunc()

	mcvMapFun := handler.EnqueueRequestsFromMapFunc(handler.MapFunc(func(obj client.Object) []reconcile.Request {
		mcv, ok := obj.(*viewv1beta1.ManagedClusterView)
		if !ok {
			ctrl.Log.Info("ManagedClusterView map function received non-MCV resource")

			return []reconcile.Request{}
		}

		ctrl.Log.Info(fmt.Sprintf("Filtering MCV (%s/%s)", mcv.Name, mcv.Namespace))

		return filterMCV(mcv)
	}))

	r.eventRecorder = rmnutil.NewEventReporter(mgr.GetEventRecorderFor("controller_DRPlacementControl"))

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(ctrlcontroller.Options{MaxConcurrentReconciles: getMaxConcurrentReconciles()}).
		For(&rmn.DRPlacementControl{}).
		Watches(&source.Kind{Type: &ocmworkv1.ManifestWork{}}, mwMapFun, builder.WithPredicates(mwPred)).
		Watches(&source.Kind{Type: &viewv1beta1.ManagedClusterView{}}, mcvMapFun, builder.WithPredicates(mcvPred)).
		Complete(r)
}

//nolint:lll
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drplacementcontrols,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drplacementcontrols/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drplacementcontrols/finalizers,verbs=update
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drpolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps.open-cluster-management.io,resources=placementrules,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps.open-cluster-management.io,resources=placementrules/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=managedclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=work.open-cluster-management.io,resources=manifestworks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=view.open-cluster-management.io,resources=managedclusterviews,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=get;create;patch;update

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
	logger := r.Log.WithValues("DRPC", req.NamespacedName)

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

	return r.reconcileDRPCInstance(ctx, drpc)
}

func (r *DRPlacementControlReconciler) reconcileDRPCInstance(ctx context.Context,
	drpc *rmn.DRPlacementControl) (ctrl.Result, error) {
	drPolicy, err := r.getDRPolicy(ctx, drpc.Spec.DRPolicyRef.Name, drpc.Spec.DRPolicyRef.Namespace)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get DRPolicy %w", err)
	}

	// Currently validation of schedule in DRPolicy is done here. When
	// there is a reconciler for DRPolicy, then probably this validation
	// has to be done there and can be removed from here.
	err = r.validateSchedule(drPolicy)
	if err != nil {
		r.Log.Error(err, "failed to validate schedule")

		// Should it be no requeue? as there is no reconcile till user
		// changes desired spec to a valid value
		return ctrl.Result{}, err
	}

	// Check if the drpc instance is marked for deletion, which is indicated by the
	// deletion timestamp being set.
	if drpc.GetDeletionTimestamp() != nil {
		return r.processDeletion(ctx, drpc)
	}

	err = r.addDRPCFinalizer(ctx, drpc)
	if err != nil {
		return ctrl.Result{}, err
	}

	drpcPlRule, userPlRule, err := r.getPlacementRules(ctx, drpc, drPolicy)
	if err != nil {
		r.Log.Error(err, "failed to get PlacementRules")

		return ctrl.Result{}, err
	}

	// Make sure that we give time to the cloned PlacementRule to run and produces decisions
	if drpcPlRule != nil && len(drpcPlRule.Status.Decisions) == 0 {
		const initialWaitTime = 5

		return ctrl.Result{RequeueAfter: time.Second * initialWaitTime}, nil
	}

	vrgs, err := r.getVRGsFromManagedClusters(drpc, drPolicy)
	if err != nil {
		r.Log.Error(err, "Failed to query for VRGs in the ManagedClusters", "Clusters", drPolicy.Spec.DRClusterSet)

		return ctrl.Result{}, err
	}

	d := DRPCInstance{
		reconciler: r, ctx: ctx, log: r.Log, instance: drpc, needStatusUpdate: false,
		userPlacementRule: userPlRule, drpcPlacementRule: drpcPlRule, drPolicy: drPolicy, vrgs: vrgs,
		mwu: rmnutil.MWUtil{Client: r.Client, Ctx: ctx, Log: r.Log, InstName: drpc.Name, InstNamespace: drpc.Namespace},
	}

	return r.processAndHandleResponse(&d)
}

func (r *DRPlacementControlReconciler) processAndHandleResponse(d *DRPCInstance) (ctrl.Result, error) {
	// Last status update time BEFORE we start processing
	beforeProcessing := d.instance.Status.LastUpdateTime

	requeue := d.startProcessing()
	r.Log.Info("Finished processing", "Requeue?", requeue)

	if !requeue {
		r.Log.Info("Done reconciling")
		r.Callback(d.instance.Name, string(d.getLastDRState()))
	}

	if d.mcvRequestInProgress {
		duration := d.getRequeueDuration()
		r.Log.Info(fmt.Sprintf("Requeing after %v", duration))

		return reconcile.Result{RequeueAfter: duration}, nil
	}

	if requeue {
		r.Log.Info("Requeing...")

		return ctrl.Result{Requeue: true}, nil
	}

	// Last status update time AFTER processing
	afterProcessing := d.instance.Status.LastUpdateTime
	requeueTimeDuration := r.getSanityCheckDelay(beforeProcessing, afterProcessing)
	r.Log.Info("Requeue time", "duration", requeueTimeDuration)

	return ctrl.Result{RequeueAfter: requeueTimeDuration}, nil
}

func (r *DRPlacementControlReconciler) getDRPolicy(ctx context.Context,
	name, namespace string) (*rmn.DRPolicy, error) {
	drPolicy := &rmn.DRPolicy{}

	err := r.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, drPolicy)
	if err != nil {
		r.Log.Error(err, "failed to get DRPolicy")

		return nil, fmt.Errorf("%w", err)
	}

	return drPolicy, nil
}

func (r *DRPlacementControlReconciler) addDRPCFinalizer(ctx context.Context, drpc *rmn.DRPlacementControl) error {
	if !controllerutil.ContainsFinalizer(drpc, DRPCFinalizer) {
		controllerutil.AddFinalizer(drpc, DRPCFinalizer)

		if err := r.Update(ctx, drpc); err != nil {
			r.Log.Error(err, "Failed to add finalizer", "finalizer", DRPCFinalizer)

			return fmt.Errorf("%w", err)
		}
	}

	return nil
}

func (r *DRPlacementControlReconciler) processDeletion(ctx context.Context,
	drpc *rmn.DRPlacementControl) (ctrl.Result, error) {
	r.Log.Info("Processing DRPC deletion")

	if controllerutil.ContainsFinalizer(drpc, DRPCFinalizer) {
		// Run finalization logic for dprc.
		// If the finalization logic fails, don't remove the finalizer so
		// that we can retry during the next reconciliation.
		if err := r.finalizeDRPC(ctx, drpc); err != nil {
			return ctrl.Result{}, err
		}

		// Remove DRPCFinalizer. The object will be deleted once
		// the finalizer is removed
		controllerutil.RemoveFinalizer(drpc, DRPCFinalizer)

		err := r.Update(ctx, drpc)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update drpc %w", err)
		}

		r.Callback(drpc.Name, "deleted")
	}

	return ctrl.Result{}, nil
}

func (r *DRPlacementControlReconciler) finalizeDRPC(ctx context.Context, drpc *rmn.DRPlacementControl) error {
	r.Log.Info("Finalizing DRPC")

	clonedPlRuleName := fmt.Sprintf(ClonedPlacementRuleNameFormat, drpc.Name, drpc.Namespace)
	mwu := rmnutil.MWUtil{Client: r.Client, Ctx: ctx, Log: r.Log, InstName: drpc.Name, InstNamespace: drpc.Namespace}

	preferredCluster := drpc.Spec.PreferredCluster
	if preferredCluster == "" {
		clonedPlRule, err := r.getClonedPlacementRule(ctx, clonedPlRuleName, drpc.Namespace)
		if err != nil {
			r.Log.Info("Cloned placement rule not found")

			return nil
		}

		if len(clonedPlRule.Status.Decisions) != 0 {
			preferredCluster = clonedPlRule.Status.Decisions[0].ClusterName
		}
	}

	clustersToClean := []string{preferredCluster}
	if drpc.Spec.FailoverCluster != "" {
		clustersToClean = append(clustersToClean, drpc.Spec.FailoverCluster)
	}

	// delete manifestworks (VRG)
	for idx := range clustersToClean {
		err := mwu.DeleteManifestWorksForCluster(clustersToClean[idx])
		if err != nil {
			return fmt.Errorf("%w", err)
		}

		mcvName := BuildManagedClusterViewName(drpc.Name, drpc.Namespace, "vrg")
		// Delete MCV for the VRG
		err = r.deleteManagedClusterView(clustersToClean[idx], mcvName)
		if err != nil {
			return err
		}
	}

	// delete cloned placementrule, if created
	if drpc.Spec.PreferredCluster == "" {
		return r.deleteClonedPlacementRule(ctx, clonedPlRuleName, drpc.Namespace)
	}

	return nil
}

func (r *DRPlacementControlReconciler) deleteManagedClusterView(clusterName, mcvName string) error {
	r.Log.Info("Delete ManagedClusterView from", "namespace", clusterName, "name", mcvName)

	mcv := &viewv1beta1.ManagedClusterView{}

	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: mcvName, Namespace: clusterName}, mcv)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}

		return fmt.Errorf("failed to retrieve ManagedClusterView for type: %s. Error: %w", mcvName, err)
	}

	r.Log.Info("Deleting ManagedClusterView", "name", mcv.Name, "namespace", mcv.Namespace)

	return r.Client.Delete(context.TODO(), mcv)
}

func (r *DRPlacementControlReconciler) getPlacementRules(ctx context.Context,
	drpc *rmn.DRPlacementControl,
	drPolicy *rmn.DRPolicy) (*plrv1.PlacementRule, *plrv1.PlacementRule, error) {
	userPlRule, err := r.getUserPlacementRule(ctx, drpc)
	if err != nil {
		return nil, nil, err
	}

	if err = r.annotatePlacementRule(ctx, drpc, userPlRule); err != nil {
		return nil, nil, err
	}

	var drpcPlRule *plrv1.PlacementRule
	// create the cloned placementrule if and only if the Spec.PreferredCluster is not provided
	if drpc.Spec.PreferredCluster == "" {
		drpcPlRule, err = r.getOrClonePlacementRule(ctx, drpc, drPolicy, userPlRule)
		if err != nil {
			return nil, nil, err
		}
	} else {
		r.Log.Info("Preferred cluster is configured. Dynamic selection is disabled",
			"PreferredCluster", drpc.Spec.PreferredCluster)
	}

	return drpcPlRule, userPlRule, nil
}

func (r *DRPlacementControlReconciler) getUserPlacementRule(ctx context.Context,
	drpc *rmn.DRPlacementControl) (*plrv1.PlacementRule, error) {
	r.Log.Info("Getting User PlacementRule", "placement", drpc.Spec.PlacementRef)

	if drpc.Spec.PlacementRef.Namespace == "" {
		drpc.Spec.PlacementRef.Namespace = drpc.Namespace
	}

	userPlacementRule := &plrv1.PlacementRule{}

	err := r.Client.Get(ctx,
		types.NamespacedName{Name: drpc.Spec.PlacementRef.Name, Namespace: drpc.Spec.PlacementRef.Namespace},
		userPlacementRule)
	if err != nil {
		return nil, fmt.Errorf("failed to get placementrule error: %w", err)
	}

	scName := userPlacementRule.Spec.SchedulerName
	if scName != RamenScheduler {
		return nil, fmt.Errorf("placementRule %s does not have the ramen scheduler. Scheduler used %s",
			userPlacementRule.Name, scName)
	}

	if userPlacementRule.Spec.ClusterReplicas == nil || *userPlacementRule.Spec.ClusterReplicas != 1 {
		r.Log.Info("User PlacementRule replica count is not set to 1, reconciliation will only" +
			" schedule it to a single cluster")
	}

	return userPlacementRule, nil
}

func (r *DRPlacementControlReconciler) annotatePlacementRule(ctx context.Context,
	drpc *rmn.DRPlacementControl, plRule *plrv1.PlacementRule) error {
	if plRule.ObjectMeta.Annotations == nil {
		plRule.ObjectMeta.Annotations = map[string]string{}
	}

	ownerName := plRule.ObjectMeta.Annotations[rmnutil.DRPCNameAnnotation]
	ownerNamespace := plRule.ObjectMeta.Annotations[rmnutil.DRPCNamespaceAnnotation]

	if ownerName == "" {
		plRule.ObjectMeta.Annotations[rmnutil.DRPCNameAnnotation] = drpc.Name
		plRule.ObjectMeta.Annotations[rmnutil.DRPCNamespaceAnnotation] = drpc.Namespace

		err := r.Update(ctx, plRule)
		if err != nil {
			r.Log.Error(err, "Failed to update PlacementRule annotation", "PlRuleName", plRule.Name)

			return fmt.Errorf("failed to update PlacementRule %s annotation '%s/%s' (%w)",
				plRule.Name, rmnutil.DRPCNameAnnotation, drpc.Name, err)
		}

		return nil
	}

	if ownerName != drpc.Name || ownerNamespace != drpc.Namespace {
		r.Log.Info("PlacementRule not owned by this DRPC", "PlRuleName", plRule.Name)

		return fmt.Errorf("PlacementRule %s not owned by this DRPC '%s/%s'",
			plRule.Name, drpc.Name, drpc.Namespace)
	}

	return nil
}

func (r *DRPlacementControlReconciler) getOrClonePlacementRule(ctx context.Context,
	drpc *rmn.DRPlacementControl, drPolicy *rmn.DRPolicy,
	userPlRule *plrv1.PlacementRule) (*plrv1.PlacementRule, error) {
	r.Log.Info("Getting PlacementRule or cloning it", "placement", drpc.Spec.PlacementRef)

	clonedPlRuleName := fmt.Sprintf(ClonedPlacementRuleNameFormat, drpc.Name, drpc.Namespace)

	clonedPlRule, err := r.getClonedPlacementRule(ctx, clonedPlRuleName, drpc.Namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			clonedPlRule, err = r.clonePlacementRule(ctx, drPolicy, userPlRule, clonedPlRuleName)
			if err != nil {
				return nil, fmt.Errorf("failed to create cloned placementrule error: %w", err)
			}
		} else {
			r.Log.Error(err, "Failed to get drpc placementRule", "name", clonedPlRuleName)

			return nil, err
		}
	}

	return clonedPlRule, nil
}

func (r *DRPlacementControlReconciler) getClonedPlacementRule(ctx context.Context,
	clonedPlRuleName, namespace string) (*plrv1.PlacementRule, error) {
	r.Log.Info("Getting cloned PlacementRule", "name", clonedPlRuleName)

	clonedPlRule := &plrv1.PlacementRule{}

	err := r.Client.Get(ctx, types.NamespacedName{Name: clonedPlRuleName, Namespace: namespace}, clonedPlRule)
	if err != nil {
		return nil, fmt.Errorf("failed to get placementrule error: %w", err)
	}

	return clonedPlRule, nil
}

func (r *DRPlacementControlReconciler) clonePlacementRule(ctx context.Context,
	drPolicy *rmn.DRPolicy, userPlRule *plrv1.PlacementRule,
	clonedPlRuleName string) (*plrv1.PlacementRule, error) {
	r.Log.Info("Creating a clone placementRule from", "name", userPlRule.Name)

	clonedPlRule := &plrv1.PlacementRule{}

	userPlRule.DeepCopyInto(clonedPlRule)

	clonedPlRule.Name = clonedPlRuleName
	clonedPlRule.ResourceVersion = ""
	clonedPlRule.Spec.SchedulerName = ""

	err := r.addClusterPeersToPlacementRule(drPolicy, clonedPlRule)
	if err != nil {
		r.Log.Error(err, "Failed to add cluster peers to cloned placementRule", "name", clonedPlRuleName)

		return nil, err
	}

	err = r.Create(ctx, clonedPlRule)
	if err != nil {
		r.Log.Error(err, "failed to clone placement rule", "name", clonedPlRule.Name)

		return nil, errorswrapper.Wrap(err, "failed to create PlacementRule")
	}

	return clonedPlRule, nil
}

func (r *DRPlacementControlReconciler) getVRGsFromManagedClusters(drpc *rmn.DRPlacementControl,
	drPolicy *rmn.DRPolicy) (map[string]*rmn.VolumeReplicationGroup, error) {
	vrgs := map[string]*rmn.VolumeReplicationGroup{}

	for _, drCluster := range drPolicy.Spec.DRClusterSet {
		// Only fetch failover cluster VRG if action is Failover
		if drpc.Spec.Action == rmn.ActionFailover && drpc.Spec.FailoverCluster != drCluster.Name {
			r.Log.Info("Skipping fetching VRG", "cluster", drCluster.Name)

			continue
		}

		vrg, err := r.MCVGetter.GetVRGFromManagedCluster(drpc.Name, drpc.Namespace, drCluster.Name)
		if err != nil {
			r.Log.Info("Failed to get VRG", "cluster", drCluster.Name, "error", err)
			// Only NotFound error is accepted
			if errors.IsNotFound(err) {
				continue
			}

			return vrgs, fmt.Errorf("failed to retrieve VRG from %s. err (%w)", drCluster.Name, err)
		}

		vrgs[drCluster.Name] = vrg
	}

	r.Log.Info("VRGs location", "VRGs", vrgs)

	return vrgs, nil
}

func (r *DRPlacementControlReconciler) validateSchedule(drPolicy *rmn.DRPolicy) error {
	r.Log.Info("Validating schedule from DRPolicy")

	if drPolicy.Spec.SchedulingInterval == "" {
		return fmt.Errorf("scheduling interval empty for the DRPolicy (%s)", drPolicy.Name)
	}

	re := regexp.MustCompile(`^\d+[mhd]$`)
	if !re.MatchString(drPolicy.Spec.SchedulingInterval) {
		return fmt.Errorf("failed to match the scheduling interval string %s", drPolicy.Spec.SchedulingInterval)
	}

	return nil
}

func (r *DRPlacementControlReconciler) deleteClonedPlacementRule(ctx context.Context,
	name, namespace string) error {
	plRule, err := r.getClonedPlacementRule(ctx, name, namespace)
	if err != nil {
		return err
	}

	err = r.Client.Delete(ctx, plRule)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}

		return fmt.Errorf("failed to delete cloned plRule %w", err)
	}

	return nil
}

func (r *DRPlacementControlReconciler) addClusterPeersToPlacementRule(
	drPolicy *rmn.DRPolicy, plRule *plrv1.PlacementRule) error {
	if len(drPolicy.Spec.DRClusterSet) == 0 {
		return fmt.Errorf("DRPolicy %s is missing DR clusters", drPolicy.Name)
	}

	for idx := range drPolicy.Spec.DRClusterSet {
		plRule.Spec.Clusters = append(plRule.Spec.Clusters, plrv1.GenericClusterReference{
			Name: drPolicy.Spec.DRClusterSet[idx].Name,
		})
	}

	r.Log.Info(fmt.Sprintf("Added clusters %v to placementRule from DRPolicy %s", plRule.Spec.Clusters, drPolicy.Name))

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
