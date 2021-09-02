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

	"github.com/ghodss/yaml"
	"github.com/go-logr/logr"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	viewv1beta1 "github.com/open-cluster-management/multicloud-operators-foundation/pkg/apis/view/v1beta1"
	plrv1 "github.com/open-cluster-management/multicloud-operators-placementrule/pkg/apis/apps/v1"
	errorswrapper "github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
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

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
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

// prometheus metrics
type timerState string

const (
	timerStart timerState = "start"
	timerStop  timerState = "stop"
)

type timerWrapper struct {
	gauge          prometheus.Gauge     // used for "last only" fine-grained timer
	histogram      prometheus.Histogram // used for cumulative data
	timer          prometheus.Timer     // use prometheus.NewTimer to use/reuse this timer across reconciles
	reconcileState rmn.DRState          // used to track for spurious reconcile avoidance
}

// set default values for guageWrapper
func newTimerWrapper(gauge prometheus.Gauge, histogram prometheus.Histogram) timerWrapper {
	wrapper := timerWrapper{}

	wrapper.gauge = gauge
	wrapper.timer = prometheus.Timer{}

	wrapper.histogram = histogram

	wrapper.reconcileState = rmn.Deployed // should never use a timer from Initial state; "reserved"

	return wrapper
}

var (
	failoverTime = newTimerWrapper(
		prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "ramen_failover_time",
			Help: "Duration of the last failover event",
		}),
		prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "ramen_failover_histogram",
			Help:    "Histogram of all failover timers (seconds)",
			Buckets: prometheus.ExponentialBuckets(1.0, 2.0, 12), // start=1.0, factor=2.0, buckets=12
		}),
	)

	relocateTime = newTimerWrapper(
		prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "ramen_relocate_time",
			Help: "Duration of the last relocate time",
		}),
		prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "ramen_relocate_histogram",
			Help:    "Histogram of all relocate timers (seconds)",
			Buckets: prometheus.ExponentialBuckets(1.0, 2.0, 12), // start=1.0, factor=2.0, buckets=12
		}),
	)

	deployTime = newTimerWrapper(
		prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "ramen_initial_deploy_time",
			Help: "Duration of the last initial deploy time",
		}),
		prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "ramen_initial_deploy_histogram",
			Help:    "Histogram of all initial deploymet timers (seconds)",
			Buckets: prometheus.ExponentialBuckets(1.0, 2.0, 12), // start=1.0, factor=2.0, buckets=12
		}),
	)
)

func init() {
	// register custom metrics with the global Prometheus registry
	metrics.Registry.MustRegister(failoverTime.gauge, relocateTime.gauge, deployTime.gauge)
}

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

type DRPCInstance struct {
	reconciler           *DRPlacementControlReconciler
	ctx                  context.Context
	log                  logr.Logger
	instance             *rmn.DRPlacementControl
	drPolicy             *rmn.DRPolicy
	needStatusUpdate     bool
	mcvRequestInProgress bool
	userPlacementRule    *plrv1.PlacementRule
	drpcPlacementRule    *plrv1.PlacementRule
	vrgs                 map[string]*rmn.VolumeReplicationGroup
	mwu                  rmnutil.MWUtil
}

func (d *DRPCInstance) startProcessing() bool {
	d.log.Info("Starting to process placement")

	requeue := true

	done, err := d.processPlacement()
	if err != nil {
		d.log.Info("Process placement", "error", err.Error())

		return requeue
	}

	if d.needStatusUpdate || d.statusUpdateTimeElapsed() {
		if err := d.updateDRPCStatus(); err != nil {
			d.log.Error(err, "failed to update status")

			return requeue
		}
	}

	requeue = !done
	d.log.Info("Completed processing placement", "requeue", requeue)

	return requeue
}

func (d *DRPCInstance) processPlacement() (bool, error) {
	d.log.Info("Process DRPC Placement", "DRAction", d.instance.Spec.Action)

	switch d.instance.Spec.Action {
	case rmn.ActionFailover:
		return d.runFailover()
	case rmn.ActionRelocate:
		return d.runRelocate()
	}

	// Not a failover or a relocation.  Must be an initial deployment.
	return d.runInitialDeployment()
}

func (d *DRPCInstance) runInitialDeployment() (bool, error) {
	d.log.Info("Running initial deployment")

	const done = true

	homeCluster, homeClusterNamespace := d.getHomeCluster()

	if homeCluster == "" {
		err := fmt.Errorf("PreferredCluster not set and unable to find home cluster in DRPCPlacementRule (%v)",
			d.drpcPlacementRule)
		// needStatusUpdate is not set. Still better to capture the event to report later
		rmnutil.ReportIfNotPresent(d.reconciler.eventRecorder, d.instance, corev1.EventTypeWarning,
			rmnutil.EventReasonDeployFail, err.Error())

		return !done, err
	}

	d.log.Info(fmt.Sprintf("Using homeCluster %s for initial deployment, uPlRule Decision %v",
		homeCluster, d.userPlacementRule.Status.Decisions))

	clusterName, deployed := d.isDeployed(homeCluster)
	if deployed && clusterName != homeCluster {
		return done, nil
	}

	if deployed && d.isUserPlRuleUpdated(homeCluster) {
		// If for whatever reason, the DRPC status is missing (i.e. DRPC could have been deleted mistakingly and
		// recreated again), we should update it with whatever status we are at.
		if d.getLastDRState() == rmn.DRState("") {
			d.instance.Status.PreferredDecision = d.userPlacementRule.Status.Decisions[0]
			d.setDRState(rmn.Deployed)
		}

		return done, nil
	}

	return d.startDeploying(homeCluster, homeClusterNamespace)
}

func (d *DRPCInstance) getHomeCluster() (string, string) {
	// Check if the user wants to use the preferredCluster
	homeCluster := ""
	homeClusterNamespace := ""

	if d.instance.Spec.PreferredCluster != "" {
		homeCluster = d.instance.Spec.PreferredCluster
		homeClusterNamespace = homeCluster
	}

	if homeCluster == "" && d.drpcPlacementRule != nil && len(d.drpcPlacementRule.Status.Decisions) != 0 {
		homeCluster = d.drpcPlacementRule.Status.Decisions[0].ClusterName
		homeClusterNamespace = d.drpcPlacementRule.Status.Decisions[0].ClusterNamespace
	}

	return homeCluster, homeClusterNamespace
}

// isDeployed check to see if the initial deployment is already complete to this
// homeCluster or elsewhere
func (d *DRPCInstance) isDeployed(homeCluster string) (string, bool) {
	if d.isAlreadyDeployedAndProtected(homeCluster) {
		d.log.Info(fmt.Sprintf("Already deployed to %s. Last state: %s",
			homeCluster, d.getLastDRState()))

		return homeCluster, true
	}

	clusterName, found := d.isAlreadyDeployedElsewhere(homeCluster)
	if found {
		errMsg := fmt.Sprintf("Failed to place deployment on cluster %s, as it is active on another cluster",
			homeCluster)
		d.log.Info(errMsg)

		return clusterName, true
	}

	return "", false
}

func (d *DRPCInstance) isUserPlRuleUpdated(homeCluster string) bool {
	return len(d.userPlacementRule.Status.Decisions) > 0 &&
		d.userPlacementRule.Status.Decisions[0].ClusterName == homeCluster
}

// isAlreadyFullyDeployedAndProtected will check whether a VRG exists in the homeCluster and
// it is in protected state, and primary.
func (d *DRPCInstance) isAlreadyDeployedAndProtected(homeCluster string) bool {
	d.log.Info(fmt.Sprintf("isAlreadyDeployedAndProtected? - %+v", d.vrgs))

	vrg, found := d.vrgs[homeCluster]
	if !found {
		d.log.Info("VRG not found on cluster", "clusterName", homeCluster)

		return false
	}

	// TODO:
	// 		UNCOMMENT THIS CODE ONCE PR #255 IS MERGED
	//
	// dataProtectedCondition := findCondition(vrg.Status.Conditions, VRGConditionTypeClusterDataProtected)
	// if dataProtectedCondition == nil {
	// 	d.log.Info("VRG is not protected yet", "cluster", homeCluster)
	//
	// 	return false
	// }

	return d.isVRGPrimary(vrg)
}

func (d *DRPCInstance) isAlreadyDeployedElsewhere(clusterToSkip string) (string, bool) {
	for clusterName := range d.vrgs {
		if clusterName == clusterToSkip {
			continue
		}

		return clusterName, true
	}

	return "", false
}

func (d *DRPCInstance) startDeploying(homeCluster, homeClusterNamespace string) (bool, error) {
	const done = true

	// Make sure we record the state that we are deploying
	d.setDRState(rmn.Deploying)
	setMetricsTimerFromDRState(rmn.Deploying, timerStart)

	// Create VRG first, to leverage user PlacementRule decision to skip placement and move to cleanup
	err := d.createVRGManifestWork(homeCluster)
	if err != nil {
		return false, err
	}

	// We have a home cluster
	err = d.updateUserPlacementRule(homeCluster, homeClusterNamespace)
	if err != nil {
		rmnutil.ReportIfNotPresent(d.reconciler.eventRecorder, d.instance, corev1.EventTypeWarning,
			rmnutil.EventReasonDeployFail, err.Error())

		return !done, err
	}

	// All good, update the preferred decision and state
	if len(d.userPlacementRule.Status.Decisions) > 0 {
		d.instance.Status.PreferredDecision = d.userPlacementRule.Status.Decisions[0]
	}

	d.advanceToNextDRState()

	d.log.Info(fmt.Sprintf("DRPC (%+v)", d.instance))
	setMetricsTimerFromDRState(rmn.Deployed, timerStop)

	return done, nil
}

func setMetricsTimerFromDRState(stateDR rmn.DRState, stateTimer timerState) {
	switch stateDR {
	case rmn.FailingOver:
		setMetricsTimer(&failoverTime, stateTimer, stateDR)
	case rmn.Relocating:
		setMetricsTimer(&relocateTime, stateTimer, stateDR)
	case rmn.Deploying:
		setMetricsTimer(&deployTime, stateTimer, stateDR)
	case rmn.FailedOver:
		fallthrough
	case rmn.Deployed:
		fallthrough
	case rmn.Relocated:
		fallthrough
	default:
		// not supported
	}
}

func setMetricsTimer(wrapper *timerWrapper, desiredTimerState timerState, reconcileState rmn.DRState) {
	switch desiredTimerState {
	case timerStart:
		if reconcileState != wrapper.reconcileState {
			wrapper.reconcileState = reconcileState
			wrapper.timer = *prometheus.NewTimer(prometheus.ObserverFunc(wrapper.gauge.Set))
		}
	case timerStop:
		wrapper.timer.ObserveDuration()
		wrapper.histogram.Observe(wrapper.timer.ObserveDuration().Seconds()) // add timer to histogram
		wrapper.reconcileState = rmn.Deployed                                // "reserved" value
	}
}

//
// runFailover:
// 1. If failoverCluster empty, then fail it and we are done
// 2. If already failed over, then ensure clean up and we are done
// 3. Set VRG for the preferredCluster to secondary
// 4. Restore PV to failoverCluster
// 5. Update UserPlacementRule decision to failoverCluster
// 6. Create VRG for the failoverCluster as Primary
// 7. Update DRPC status
// 8. Delete VRG MW from preferredCluster once the VRG state has changed to Secondary
//
func (d *DRPCInstance) runFailover() (bool, error) {
	d.log.Info("Entering runFailover", "state", d.getLastDRState())

	const done = true

	if d.isRelocationInProgress() {
		return done, fmt.Errorf("invalid state %s for the selected action %v",
			d.getLastDRState(), d.instance.Spec.Action)
	}

	// We are done if empty
	if d.instance.Spec.FailoverCluster == "" {
		return done, fmt.Errorf("failover cluster not set. FailoverCluster is a mandatory field")
	}

	// We are done if we have already failed over
	if len(d.userPlacementRule.Status.Decisions) > 0 {
		if d.instance.Spec.FailoverCluster == d.userPlacementRule.Status.Decisions[0].ClusterName {
			d.log.Info(fmt.Sprintf("Already failed over to %s. Last state: %s",
				d.userPlacementRule.Status.Decisions[0].ClusterName, d.getLastDRState()))

			err := d.ensureCleanup(d.instance.Spec.FailoverCluster)
			if err != nil {
				return !done, err
			}

			return done, nil
		}
	}

	return d.switchToFailoverCluster()
}

func (d *DRPCInstance) switchToFailoverCluster() (bool, error) {
	const done = true
	// Make sure we record the state that we are failing over
	d.setDRState(rmn.FailingOver)
	setMetricsTimerFromDRState(rmn.FailingOver, timerStart)

	// Save the current home cluster
	curHomeCluster := d.getCurrentHomeClusterName()

	if curHomeCluster == "" {
		d.log.Info("Invalid Failover request. Current home cluster does not exists")
		err := fmt.Errorf("failover requested on invalid state %v", d.instance.Status)
		rmnutil.ReportIfNotPresent(d.reconciler.eventRecorder, d.instance, corev1.EventTypeWarning,
			rmnutil.EventReasonSwitchFailed, err.Error())

		return done, err
	}

	// Set VRG in the failed cluster (preferred cluster) to secondary
	err := d.updateVRGStateToSecondary(curHomeCluster)
	if err != nil {
		d.log.Error(err, "Failed to update existing VRG manifestwork to secondary")
		rmnutil.ReportIfNotPresent(d.reconciler.eventRecorder, d.instance, corev1.EventTypeWarning,
			rmnutil.EventReasonSwitchFailed, err.Error())

		return !done, err
	}

	newHomeCluster := d.instance.Spec.FailoverCluster

	const restorePVs = true

	result, err := d.runPlacementTask(newHomeCluster, "", restorePVs)
	if err != nil {
		rmnutil.ReportIfNotPresent(d.reconciler.eventRecorder, d.instance, corev1.EventTypeWarning,
			rmnutil.EventReasonSwitchFailed, err.Error())

		return !done, err
	}

	d.advanceToNextDRState()
	d.log.Info("Exiting runFailover", "state", d.getLastDRState())
	setMetricsTimerFromDRState(rmn.FailingOver, timerStop)

	return result, nil
}

func (d *DRPCInstance) getCurrentHomeClusterName() string {
	curHomeCluster := ""
	if len(d.userPlacementRule.Status.Decisions) > 0 {
		curHomeCluster = d.userPlacementRule.Status.Decisions[0].ClusterName
	}

	if curHomeCluster == "" {
		curHomeCluster = d.instance.Status.PreferredDecision.ClusterName
	}

	return curHomeCluster
}

func (d *DRPCInstance) runRelocate() (bool, error) {
	d.log.Info("Entering runRelocate", "state", d.getLastDRState())

	const done = true

	if d.isFailoverInProgress() {
		return done, fmt.Errorf("invalid state %s for the selected action %v",
			d.getLastDRState(), d.instance.Spec.Action)
	}

	preferredCluster := d.instance.Spec.PreferredCluster
	preferredClusterNamespace := preferredCluster
	// We are done if empty. Relocation requires preferredCluster to be configured
	if preferredCluster == "" {
		return !done, fmt.Errorf("preferred cluster not valid")
	}

	if preferredCluster == d.instance.Spec.FailoverCluster {
		return done, fmt.Errorf("failoverCluster and preferredCluster can't be the same %s/%s",
			d.instance.Spec.FailoverCluster, preferredCluster)
	}

	// We are done if already failed back
	if d.hasAlreadySwitchedOver(preferredCluster) {
		err := d.ensureCleanup(preferredCluster)
		if err != nil {
			return !done, err
		}

		return done, nil
	}

	curHomeCluster := d.getCurrentHomeClusterName()

	if curHomeCluster == "" {
		d.log.Info("Invalid Relocation request. Current home cluster does not exists")
		err := fmt.Errorf("relocation requested on invalid state %v", d.instance.Status)
		rmnutil.ReportIfNotPresent(d.reconciler.eventRecorder, d.instance, corev1.EventTypeWarning,
			rmnutil.EventReasonSwitchFailed, err.Error())

		return done, err
	}

	// IF it has already started relocation, then the curHomeCluster will not be valid.
	if !d.isRelocationInProgress() && !d.readyToSwitchOver(curHomeCluster) {
		return !done, fmt.Errorf(
			fmt.Sprintf("relocation to %s not allowed at this time. VRG on %s not ready",
				preferredCluster, curHomeCluster))
	}

	return d.switchToPreferredCluster(preferredCluster, preferredClusterNamespace, rmn.Relocating)
}

func (d *DRPCInstance) switchToPreferredCluster(preferredCluster, preferredClusterNamespace string,
	drState rmn.DRState) (bool, error) {
	const done = true
	// Make sure we record the state that we are failing over
	d.setDRState(drState)
	setMetricsTimerFromDRState(drState, timerStart)

	// During relocate, the preferredCluster does not contain a VRG or the VRG is already
	// secondary. We need to skip checking if the VRG for it is secondary to avoid messing up with the
	// order of execution (it could be refactored better to avoid this complexity). IOW, if we first update
	// VRG in all clusters to secondaries, then we call runPlacementTask. If runPlacementTask does not complete
	// in one shot, then coming back to this loop will reset the preferredCluster to secondary again.
	clusterToSkip := preferredCluster
	if !d.ensureVRGIsSecondary(clusterToSkip) {
		// During relocation, both clusters should be up and both must be secondaries before we proceed.
		ensured, err := d.processVRGForSecondaries()
		if err != nil || !ensured {
			return !done, err
		}
	}

	const restorePVs = true

	result, err := d.runPlacementTask(preferredCluster, preferredClusterNamespace, restorePVs)
	if err != nil {
		return !done, err
	}

	// All good so far, update DRPC decision and state
	if len(d.userPlacementRule.Status.Decisions) > 0 {
		d.instance.Status.PreferredDecision = d.userPlacementRule.Status.Decisions[0]
	}

	d.advanceToNextDRState()
	d.log.Info("Done", "Last known state", d.getLastDRState())
	setMetricsTimerFromDRState(drState, timerStop)

	return result, nil
}

// runPlacementTask is a series of steps to creating, updating, and cleaning up
// the necessary objects for the failover or relocation
//nolint:cyclop
func (d *DRPCInstance) runPlacementTask(targetCluster, targetClusterNamespace string, restorePVs bool) (bool, error) {
	d.log.Info("runPlacementTask", "cluster", targetCluster, "restorePVs", restorePVs)

	created, err := d.createManifestWorks(targetCluster)
	if err != nil {
		return false, err
	}

	if created && restorePVs {
		// We just created MWs. Give it time until the PV restore is complete
		return false, fmt.Errorf("%w)", WaitForPVRestoreToComplete)
	}

	if !created {
		updated, err := d.updateManifestWorkToPrimary(targetCluster)
		if err != nil {
			return false, err
		}

		if updated && restorePVs {
			d.log.Info("runPlacementTask", "updated", updated, "restorePVs", restorePVs)
			// We just updated MWs. Give it time until the PV restore is complete
			return false, fmt.Errorf("%w)", WaitForPVRestoreToComplete)
		}
	}

	// already a primary
	if restorePVs {
		restored, err := d.checkPVsHaveBeenRestored(targetCluster)
		if err != nil {
			return false, err
		}

		d.log.Info("Checked whether PVs have been restored", "Yes?", restored)

		if !restored {
			return false, fmt.Errorf("%w)", WaitForPVRestoreToComplete)
		}
	}

	err = d.updateUserPlacementRule(targetCluster, targetClusterNamespace)
	if err != nil {
		return false, err
	}

	// Attempt to delete VRG MW from failed clusters
	// This is attempt to clean up is not guaranteed to complete at this stage. Deleting the old VRG
	// requires guaranteeing that the VRG has transitioned to secondary.
	clusterToSkip := targetCluster

	return d.cleanup(clusterToSkip)
}

func (d *DRPCInstance) createManifestWorks(targetCluster string) (bool, error) {
	mwName := d.mwu.BuildManifestWorkName(rmnutil.MWTypeVRG)

	const created = true

	_, err := d.mwu.FindManifestWork(mwName, targetCluster)
	if err != nil {
		if errors.IsNotFound(err) {
			// Create VRG first, to leverage user PlacementRule decision to skip placement and move to cleanup
			err := d.createVRGManifestWork(targetCluster)
			if err != nil {
				return !created, err
			}

			return created, nil
		}

		d.log.Error(err, "failed to retrieve ManifestWork")

		return !created, fmt.Errorf("failed to retrieve ManifestWork %s (%w)", mwName, err)
	}

	return !created, nil
}

func (d *DRPCInstance) updateManifestWorkToPrimary(targetCluster string) (bool, error) {
	mwName := d.mwu.BuildManifestWorkName(rmnutil.MWTypeVRG)

	const updated = true

	mw, err := d.mwu.FindManifestWork(mwName, targetCluster)
	if err != nil {
		return !updated, fmt.Errorf("failed to find MW (%w)", err)
	}

	vrg, err := d.extractVRGFromManifestWork(mw)
	if err != nil {
		return !updated, err
	}

	if !d.isVRGPrimary(vrg) {
		err := d.updateVRGManifestWork(targetCluster)
		if err != nil {
			return !updated, err
		}

		return updated, nil
	}

	// not updated
	return !updated, nil
}

func (d *DRPCInstance) hasAlreadySwitchedOver(targetCluster string) bool {
	if len(d.userPlacementRule.Status.Decisions) > 0 &&
		targetCluster == d.userPlacementRule.Status.Decisions[0].ClusterName {
		d.log.Info(fmt.Sprintf("Already switched over to cluster %s. Last state: %v",
			targetCluster, d.getLastDRState()))

		return true
	}

	return false
}

func (d *DRPCInstance) readyToSwitchOver(homeCluster string) bool {
	const ready = true

	d.log.Info("Checking whether VRG is available", "cluster", homeCluster)

	vrg, err := d.reconciler.MCVGetter.GetVRGFromManagedCluster(d.instance.Name, d.instance.Namespace, homeCluster)
	if err != nil {
		d.log.Info("Failed to get VRG through MCV", "error", err)
		// Only NotFound error is accepted
		if errors.IsNotFound(err) {
			return ready
		}

		return !ready
	}

	dataReadyCondition := findCondition(vrg.Status.Conditions, VRGConditionTypeDataReady)
	if dataReadyCondition == nil {
		d.log.Info("VRG Condition not available", "cluster", homeCluster)

		return !ready
	}

	clusterDataReadyCondition := findCondition(vrg.Status.Conditions, VRGConditionTypeClusterDataReady)
	if clusterDataReadyCondition == nil {
		d.log.Info("VRG Condition ClusterData not available", "cluster", homeCluster)

		return !ready
	}

	return dataReadyCondition.Status == metav1.ConditionTrue &&
		dataReadyCondition.ObservedGeneration == vrg.Generation &&
		clusterDataReadyCondition.Status == metav1.ConditionTrue &&
		clusterDataReadyCondition.ObservedGeneration == vrg.Generation
}

func (d *DRPCInstance) processVRGForSecondaries() (bool, error) {
	const ensured = true

	if len(d.userPlacementRule.Status.Decisions) != 0 {
		// clear current user PlacementRule's decision
		err := d.clearUserPlacementRuleStatus()
		if err != nil {
			return !ensured, err
		}
	}

	failedCount := 0

	for _, drCluster := range d.drPolicy.Spec.DRClusterSet {
		clusterName := drCluster.Name

		err := d.updateVRGStateToSecondary(clusterName)
		if err != nil {
			d.log.Error(err, "Failed to update VRG to secondary", "cluster", clusterName)

			failedCount++
		}
	}

	if failedCount != 0 {
		d.log.Info("Failed to update VRG to secondary", "FailedCount", failedCount)

		return !ensured, nil
	}

	return d.ensureVRGIsSecondary(""), nil
}

// outputs a string for use in creating a ManagedClusterView name
// example: when looking for a vrg with name 'demo' in the namespace 'ramen', input: ("demo", "ramen", "vrg")
// this will give output "demo-ramen-vrg-mcv"
func BuildManagedClusterViewName(resourceName, resourceNamespace, resource string) string {
	return fmt.Sprintf("%s-%s-%s-mcv", resourceName, resourceNamespace, resource)
}

func (d *DRPCInstance) cleanup(skipCluster string) (bool, error) {
	for _, drCluster := range d.drPolicy.Spec.DRClusterSet {
		clusterName := drCluster.Name
		if skipCluster == clusterName {
			continue
		}

		// If VRG hasn't been deleted, then make sure that the MW for it is deleted and
		// return and wait
		mwDeleted, err := d.ensureVRGManifestWorkOnClusterDeleted(clusterName)
		if err != nil {
			return false, nil
		}

		if !mwDeleted {
			return false, nil
		}

		d.log.Info("MW has been deleted. Check the VRG")

		vrgDeleted, err := d.ensureVRGOnClusterDeleted(clusterName)
		if err != nil {
			d.log.Error(err, "failed to ensure that the VRG MW is deleted")

			return false, err
		}

		if !vrgDeleted {
			d.log.Info("VRG has not been deleted yet", "cluster", clusterName)

			return false, nil
		}

		mcvName := BuildManagedClusterViewName(d.instance.Name, d.instance.Namespace, "vrg")
		// MW is deleted, VRG is deleted, so we no longer need MCV for the VRG
		err = d.reconciler.deleteManagedClusterView(clusterName, mcvName)
		if err != nil {
			return false, err
		}
	}

	return true, nil
}

func (d *DRPCInstance) updateUserPlacementRule(homeCluster, homeClusterNamespace string) error {
	d.log.Info("Updating userPlacementRule", "name", d.userPlacementRule.Name)

	if homeClusterNamespace == "" {
		homeClusterNamespace = homeCluster
	}

	newPD := []plrv1.PlacementDecision{
		{
			ClusterName:      homeCluster,
			ClusterNamespace: homeClusterNamespace,
		},
	}

	status := plrv1.PlacementRuleStatus{
		Decisions: newPD,
	}

	return d.updateUserPlacementRuleStatus(status)
}

func (d *DRPCInstance) clearUserPlacementRuleStatus() error {
	d.log.Info("Updating userPlacementRule", "name", d.userPlacementRule.Name)

	status := plrv1.PlacementRuleStatus{}

	return d.updateUserPlacementRuleStatus(status)
}

func (d *DRPCInstance) updateUserPlacementRuleStatus(status plrv1.PlacementRuleStatus) error {
	d.log.Info("Updating userPlacementRule", "name", d.userPlacementRule.Name)

	if !reflect.DeepEqual(status, d.userPlacementRule.Status) {
		d.userPlacementRule.Status = status
		if err := d.reconciler.Status().Update(d.ctx, d.userPlacementRule); err != nil {
			d.log.Error(err, "failed to update user PlacementRule")

			return fmt.Errorf("failed to update userPlRule %s (%w)", d.userPlacementRule.Name, err)
		}

		d.log.Info("Updated user PlacementRule status", "Decisions", d.userPlacementRule.Status.Decisions)
	}

	return nil
}

func (d *DRPCInstance) createVRGManifestWork(homeCluster string) error {
	d.log.Info("Creating VRG ManifestWork",
		"Last State:", d.getLastDRState(), "cluster", homeCluster)

	if err := d.mwu.CreateOrUpdateVRGManifestWork(
		d.instance.Name, d.instance.Namespace,
		homeCluster, d.drPolicy,
		d.instance.Spec.PVCSelector); err != nil {
		d.log.Error(err, "failed to create or update VolumeReplicationGroup manifest")

		return fmt.Errorf("failed to create or update VolumeReplicationGroup manifest in namespace %s (%w)", homeCluster, err)
	}

	return nil
}

func (d *DRPCInstance) updateVRGManifestWork(homeCluster string) error {
	if err := d.mwu.CreateOrUpdateVRGManifestWork(
		d.instance.Name, d.instance.Namespace,
		homeCluster, d.drPolicy,
		d.instance.Spec.PVCSelector); err != nil {
		d.log.Error(err, "failed to update VolumeReplicationGroup manifest")

		return fmt.Errorf("failed to update VolumeReplicationGroup manifest in namespace %s (%w)", homeCluster, err)
	}

	return nil
}

func (d *DRPCInstance) isVRGPrimary(vrg *rmn.VolumeReplicationGroup) bool {
	return (vrg.Spec.ReplicationState == rmn.Primary)
}

func (d *DRPCInstance) checkPVsHaveBeenRestored(homeCluster string) (bool, error) {
	d.log.Info("Checking whether PVs have been restored", "cluster", homeCluster)

	vrg, err := d.reconciler.MCVGetter.GetVRGFromManagedCluster(d.instance.Name,
		d.instance.Namespace, homeCluster)
	if err != nil {
		d.log.Info("Failed to get VRG through MCV", "error", err)

		return false, fmt.Errorf("%w", err)
	}

	clusterDataReady := findCondition(vrg.Status.Conditions, VRGConditionTypeClusterDataReady)
	if clusterDataReady == nil {
		d.log.Info("Waiting for PVs to be restored", "cluster", homeCluster)

		return false, nil
	}

	return clusterDataReady.Status == metav1.ConditionTrue && clusterDataReady.ObservedGeneration == vrg.Generation, nil
}

func (d *DRPCInstance) ensureCleanup(clusterToSkip string) error {
	d.log.Info("ensure cleanup on secondaries")

	idx, condition := GetDRPCCondition(&d.instance.Status, rmn.ConditionReconciling)

	if idx == -1 {
		d.log.Info("Generating new condition")
		condition = d.newCondition(rmn.ConditionReconciling)
		d.instance.Status.Conditions = append(d.instance.Status.Conditions, *condition)
		idx = len(d.instance.Status.Conditions) - 1
		d.needStatusUpdate = true
	}

	d.log.Info(fmt.Sprintf("Condition %v", condition))

	if condition.Reason == rmn.ReasonSuccess &&
		condition.Status == metav1.ConditionTrue &&
		condition.ObservedGeneration == d.instance.Generation {
		d.log.Info("Condition values tallied, cleanup is considered complete")

		return nil
	}

	d.updateCondition(condition, idx, rmn.ReasonCleaning, metav1.ConditionFalse)

	clean, err := d.cleanup(clusterToSkip)
	if err != nil {
		return err
	}

	if !clean {
		return fmt.Errorf("failed to clean secondaries")
	}

	d.updateCondition(condition, idx, rmn.ReasonSuccess, metav1.ConditionTrue)

	return nil
}

func (d *DRPCInstance) updateCondition(condition *metav1.Condition, idx int, reason string,
	status metav1.ConditionStatus) {
	condition.Reason = reason
	condition.Status = status
	condition.ObservedGeneration = d.instance.Generation
	d.instance.Status.Conditions[idx] = *condition
	d.needStatusUpdate = true
}

func (d *DRPCInstance) ensureVRGOnClusterDeleted(clusterName string) (bool, error) {
	d.log.Info("Ensuring VRG for the previous cluster is deleted", "cluster", clusterName)

	const done = true

	mwName := d.mwu.BuildManifestWorkName(rmnutil.MWTypeVRG)
	mw := &ocmworkv1.ManifestWork{}

	err := d.reconciler.Get(d.ctx, types.NamespacedName{Name: mwName, Namespace: clusterName}, mw)
	if err != nil {
		if errors.IsNotFound(err) {
			d.log.Info("MW has been Deleted. Ensure VRG deleted as well", "cluster", clusterName)
			// the MW is gone.  Check the VRG if it is deleted
			if d.ensureVRGDeleted(clusterName) {
				return done, nil
			}

			return !done, nil
		}

		return !done, fmt.Errorf("failed to retrieve manifestwork %s from %s (%w)", mwName, clusterName, err)
	}

	d.log.Info("VRG ManifestWork exists", "name", mw.Name, "clusterName", clusterName)

	return !done, nil
}

func (d *DRPCInstance) ensureVRGManifestWorkOnClusterDeleted(clusterName string) (bool, error) {
	d.log.Info("Ensuring MW for the VRG is deleted", "cluster", clusterName)

	const done = true

	mwName := d.mwu.BuildManifestWorkName(rmnutil.MWTypeVRG)
	mw := &ocmworkv1.ManifestWork{}

	err := d.reconciler.Get(d.ctx, types.NamespacedName{Name: mwName, Namespace: clusterName}, mw)
	if err != nil {
		if errors.IsNotFound(err) {
			return done, nil
		}

		return !done, fmt.Errorf("failed to retrieve ManifestWork (%w)", err)
	}

	// We have to make sure that the VRG for the MW was set to secondary,
	updated, err := d.hasVRGStateBeenUpdatedToSecondary(clusterName)
	if err != nil {
		return !done, fmt.Errorf("failed to check whether VRG replication state has been updated to secondary (%w)", err)
	}

	// If it is not set to secondary, then update it
	if !updated {
		err = d.updateVRGStateToSecondary(clusterName)
		// We need to wait for the MW to go to applied state
		return !done, err
	}

	// if !IsManifestInAppliedState(mw) {
	// 	d.log.Info(fmt.Sprintf("ManifestWork %s/%s NOT in Applied state", mw.Namespace, mw.Name))
	// 	// Wait for MW to be applied. The DRPC reconciliation will be called then
	// 	return done, nil
	// }

	// d.log.Info("VRG ManifestWork is in Applied state", "name", mw.Name, "cluster", clusterName)

	if d.ensureVRGIsSecondaryOnCluster(clusterName) {
		err := d.mwu.DeleteManifestWorksForCluster(clusterName)
		if err != nil {
			return !done, fmt.Errorf("%w", err)
		}

		return done, nil
	}

	d.log.Info("Request not complete yet", "cluster", clusterName)
	// IF we get here, either the VRG has not transitioned to secondary (yet) or delete didn't succeed. In either cases,
	// we need to make sure that the VRG object is deleted. IOW, we still have to wait
	return !done, nil
}

// ensureVRGIsSecondary iterates through all the clusters int he DRCluster set, and for each cluster,
// it checks whether the VRG (if exists) is secondary.
// It returns true if all clusters report secondary for the VRG, otherwise, it returns false
func (d *DRPCInstance) ensureVRGIsSecondary(clusterToSkip string) bool {
	for _, drCluster := range d.drPolicy.Spec.DRClusterSet {
		clusterName := drCluster.Name
		if clusterToSkip == clusterName {
			continue
		}

		if !d.ensureVRGIsSecondaryOnCluster(clusterName) {
			d.log.Info("Still waiting for VRG to transition to secondary", "cluster", clusterName)

			return false
		}
	}

	return true
}

//
// ensureVRGIsSecondaryOnCluster returns true whether the VRG is secondary or it does not exists on the cluster
//
func (d *DRPCInstance) ensureVRGIsSecondaryOnCluster(clusterName string) bool {
	d.log.Info(fmt.Sprintf("Ensure VRG %s is secondary on cluster %s", d.instance.Name, clusterName))

	d.mcvRequestInProgress = false

	vrg, err := d.reconciler.MCVGetter.GetVRGFromManagedCluster(d.instance.Name,
		d.instance.Namespace, clusterName)
	if err != nil {
		if errors.IsNotFound(err) {
			return true // ensured
		}

		d.log.Info("Failed to get VRG", "errorValue", err)

		d.mcvRequestInProgress = true

		return false
	}

	if vrg.Status.State != rmn.SecondaryState {
		d.log.Info(fmt.Sprintf("vrg status replication state for cluster %s is %v",
			clusterName, vrg))

		return false
	}

	return true
}

func (d *DRPCInstance) ensureVRGDeleted(clusterName string) bool {
	d.mcvRequestInProgress = false

	vrg, err := d.reconciler.MCVGetter.GetVRGFromManagedCluster(d.instance.Name,
		d.instance.Namespace, clusterName)
	if err != nil {
		d.log.Info("Failed to get VRG using MCV", "error", err)
		// Only NotFound error is accepted
		if errors.IsNotFound(err) {
			return true // ensured
		}

		d.mcvRequestInProgress = true
	}

	d.log.Info(fmt.Sprintf("Got VRG using MCV -- %v", vrg))

	return false
}

func (d *DRPCInstance) hasVRGStateBeenUpdatedToSecondary(clusterName string) (bool, error) {
	vrgMWName := d.mwu.BuildManifestWorkName(rmnutil.MWTypeVRG)
	d.log.Info("Check if VRG has been updated to secondary", "name", vrgMWName, "cluster", clusterName)

	mw, err := d.mwu.FindManifestWork(vrgMWName, clusterName)
	if err != nil {
		d.log.Error(err, "failed to check whether VRG state is secondary")

		return false, fmt.Errorf("failed to check whether VRG state for %s is secondary, in namespace %s (%w)",
			vrgMWName, clusterName, err)
	}

	vrg, err := d.extractVRGFromManifestWork(mw)
	if err != nil {
		d.log.Error(err, "failed to check whether VRG state is secondary")

		return false, err
	}

	if vrg.Spec.ReplicationState == rmn.Secondary {
		d.log.Info("VRG MW already secondary on this cluster", "name", vrg.Name, "cluster", mw.Namespace)

		return true, nil
	}

	return false, err
}

func (d *DRPCInstance) updateVRGStateToSecondary(clusterName string) error {
	vrgMWName := d.mwu.BuildManifestWorkName(rmnutil.MWTypeVRG)
	d.log.Info(fmt.Sprintf("Updating VRG ownedby MW %s to secondary for cluster %s", vrgMWName, clusterName))

	mw, err := d.mwu.FindManifestWork(vrgMWName, clusterName)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}

		d.log.Error(err, "failed to update VRG state")

		return fmt.Errorf("failed to update VRG state for %s, in namespace %s (%w)",
			vrgMWName, clusterName, err)
	}

	vrg, err := d.extractVRGFromManifestWork(mw)
	if err != nil {
		d.log.Error(err, "failed to update VRG state")

		return err
	}

	if vrg.Spec.ReplicationState == rmn.Secondary {
		d.log.Info(fmt.Sprintf("VRG %s already secondary on this cluster %s", vrg.Name, mw.Namespace))

		return nil
	}

	vrg.Spec.ReplicationState = rmn.Secondary

	vrgClientManifest, err := d.mwu.GenerateManifest(vrg)
	if err != nil {
		d.log.Error(err, "failed to generate manifest")

		return fmt.Errorf("failed to generate VRG manifest (%w)", err)
	}

	mw.Spec.Workload.Manifests[0] = *vrgClientManifest

	err = d.reconciler.Update(d.ctx, mw)
	if err != nil {
		return fmt.Errorf("failed to update MW (%w)", err)
	}

	d.log.Info(fmt.Sprintf("Updated VRG running in cluster %s to secondary. VRG (%v)", clusterName, vrg))

	return nil
}

func (d *DRPCInstance) extractVRGFromManifestWork(mw *ocmworkv1.ManifestWork) (*rmn.VolumeReplicationGroup, error) {
	if len(mw.Spec.Workload.Manifests) == 0 {
		return nil, fmt.Errorf("invalid VRG ManifestWork for type: %s", mw.Name)
	}

	vrgClientManifest := &mw.Spec.Workload.Manifests[0]
	vrg := &rmn.VolumeReplicationGroup{}

	err := yaml.Unmarshal(vrgClientManifest.RawExtension.Raw, &vrg)
	if err != nil {
		return nil, fmt.Errorf("unable to unmarshal VRG object (%w)", err)
	}

	return vrg, nil
}

func (d *DRPCInstance) updateDRPCStatus() error {
	d.log.Info("Updating DRPC status")

	if len(d.userPlacementRule.Status.Decisions) != 0 {
		vrg, err := d.reconciler.MCVGetter.GetVRGFromManagedCluster(d.instance.Name, d.instance.Namespace,
			d.userPlacementRule.Status.Decisions[0].ClusterName)
		if err != nil {
			// VRG must have been deleted if the error is NotFound. In either case,
			// we don't have a VRG
			d.log.Info("Failed to get VRG from managed cluster", "errMsg", err)

			d.instance.Status.ResourceConditions = rmn.VRGConditions{}
		} else {
			d.instance.Status.ResourceConditions.ResourceMeta.Kind = vrg.Kind
			d.instance.Status.ResourceConditions.ResourceMeta.Name = vrg.Name
			d.instance.Status.ResourceConditions.ResourceMeta.Namespace = vrg.Namespace
			d.instance.Status.ResourceConditions.Conditions = vrg.Status.Conditions
		}
	}

	d.instance.Status.LastUpdateTime = metav1.Now()

	if err := d.reconciler.Status().Update(d.ctx, d.instance); err != nil {
		return errorswrapper.Wrap(err, "failed to update DRPC status")
	}

	d.log.Info(fmt.Sprintf("Updated DRPC Status %+v", d.instance.Status))

	return nil
}

func (d *DRPCInstance) advanceToNextDRState() {
	lastDRState := d.getLastDRState()
	nextState := lastDRState

	switch lastDRState {
	case rmn.Deploying:
		nextState = rmn.Deployed
	case rmn.FailingOver:
		nextState = rmn.FailedOver
	case rmn.Relocating:
		nextState = rmn.Relocated
	case rmn.Deployed:
	case rmn.FailedOver:
	case rmn.Relocated:
	}

	d.setDRState(nextState)
}

func (d *DRPCInstance) setDRState(nextState rmn.DRState) {
	if d.instance.Status.Phase != nextState {
		d.log.Info(fmt.Sprintf("Phase: Current '%s'. Next '%s'",
			d.instance.Status.Phase, nextState))

		d.instance.Status.Phase = nextState
		d.updateConditions()

		d.reportEvent(nextState)

		d.needStatusUpdate = true
	}
}

func (d *DRPCInstance) reportEvent(nextState rmn.DRState) {
	eventReason := "unknown state"
	eventType := corev1.EventTypeWarning
	msg := "next state not known"

	switch nextState {
	case rmn.Deploying:
		eventReason = rmnutil.EventReasonDeploying
		eventType = corev1.EventTypeNormal
		msg = "Deploying the application and VRG"
	case rmn.Deployed:
		eventReason = rmnutil.EventReasonDeploySuccess
		eventType = corev1.EventTypeNormal
		msg = "Successfully deployed the application and VRG"
	case rmn.FailingOver:
		eventReason = rmnutil.EventReasonFailingOver
		eventType = corev1.EventTypeWarning
		msg = "Failing over the application and VRG"
	case rmn.FailedOver:
		eventReason = rmnutil.EventReasonFailoverSuccess
		eventType = corev1.EventTypeNormal
		msg = "Successfully failedover the application and VRG"
	case rmn.Relocating:
		eventReason = rmnutil.EventReasonRelocating
		eventType = corev1.EventTypeNormal
		msg = "Relocating the application and VRG"
	case rmn.Relocated:
		eventReason = rmnutil.EventReasonRelocationSuccess
		eventType = corev1.EventTypeNormal
		msg = "Successfully relocated the application and VRG"
	}

	rmnutil.ReportIfNotPresent(d.reconciler.eventRecorder, d.instance, eventType,
		eventReason, msg)
}

func (d *DRPCInstance) updateConditions() {
	d.log.Info(fmt.Sprintf("Current Conditions '%v'", d.instance.Status.Conditions))

	for _, condType := range []string{rmn.ConditionAvailable, rmn.ConditionReconciling} {
		condition := d.newCondition(condType)

		idx, _ := GetDRPCCondition(&d.instance.Status, condType)
		if idx == -1 {
			d.instance.Status.Conditions = append(d.instance.Status.Conditions, *condition)
		} else {
			d.instance.Status.Conditions[idx] = *condition
		}
	}

	d.log.Info(fmt.Sprintf("Updated Conditions '%v'", d.instance.Status.Conditions))
}

func (d *DRPCInstance) newCondition(condType string) *metav1.Condition {
	return &metav1.Condition{
		Type:   condType,
		Status: d.getConditionStatus(condType),
		// ObservedGeneration: d.getObservedGeneration(condType),
		LastTransitionTime: metav1.Now(),
		Reason:             d.getConditionReason(condType),
		Message:            d.getConditionMessage(condType),
		ObservedGeneration: d.instance.Generation,
	}
}

func (d *DRPCInstance) getConditionStatus(condType string) metav1.ConditionStatus {
	if condType == rmn.ConditionAvailable {
		return d.getConditionStatusForTypeAvailable()
	} else if condType == rmn.ConditionReconciling {
		return d.getConditionStatusForTypeReconciling()
	}

	return metav1.ConditionUnknown
}

func (d *DRPCInstance) getConditionStatusForTypeAvailable() metav1.ConditionStatus {
	if d.isInFinalPhase() {
		return metav1.ConditionTrue
	}

	if d.isInProgressingPhase() {
		return metav1.ConditionFalse
	}

	return metav1.ConditionUnknown
}

func (d *DRPCInstance) getConditionStatusForTypeReconciling() metav1.ConditionStatus {
	if d.isInFinalPhase() {
		if d.reconciling() {
			return metav1.ConditionFalse
		}

		return metav1.ConditionTrue
	}

	if d.isInProgressingPhase() {
		return metav1.ConditionFalse
	}

	return metav1.ConditionUnknown
}

func (d *DRPCInstance) reconciling() bool {
	// Deployed phase requires no cleanup and further reconcile for cleanup
	if d.instance.Status.Phase == rmn.Deployed {
		d.log.Info("Initial deployed phase detected")

		return false
	}

	idx, condition := GetDRPCCondition(&d.instance.Status, rmn.ConditionReconciling)
	d.log.Info(fmt.Sprintf("Condition %v", condition))

	if idx == -1 {
		d.log.Info("Found missing reconciling condition")

		return true
	}

	if condition.Reason == rmn.ReasonSuccess &&
		condition.Status == metav1.ConditionTrue &&
		condition.ObservedGeneration == d.instance.Generation {
		return false
	}

	return true
}

func (d *DRPCInstance) getConditionReason(condType string) string {
	if condType == rmn.ConditionAvailable {
		return string(d.instance.Status.Phase)
	} else if condType == rmn.ConditionReconciling {
		return d.getReasonForConditionTypeReconciling()
	}

	return rmn.ReasonUnknown
}

func (d *DRPCInstance) getReasonForConditionTypeReconciling() string {
	if d.isInFinalPhase() {
		if d.reconciling() {
			return rmn.ReasonCleaning
		}

		return rmn.ReasonSuccess
	}

	if d.isInProgressingPhase() {
		return rmn.ReasonProgressing
	}

	return rmn.ReasonUnknown
}

func (d *DRPCInstance) getConditionMessage(condType string) string {
	return fmt.Sprintf("Condition type %s", condType)
}

//nolint:exhaustive
func (d *DRPCInstance) isInFinalPhase() bool {
	switch d.instance.Status.Phase {
	case rmn.Deployed:
		fallthrough
	case rmn.FailedOver:
		fallthrough
	case rmn.Relocated:
		return true
	default:
		return false
	}
}

//nolint:exhaustive
func (d *DRPCInstance) isInProgressingPhase() bool {
	switch d.instance.Status.Phase {
	case rmn.Deploying:
		fallthrough
	case rmn.FailingOver:
		fallthrough
	case rmn.Relocating:
		return true
	default:
		return false
	}
}

func (d *DRPCInstance) getLastDRState() rmn.DRState {
	return d.instance.Status.Phase
}

//nolint:exhaustive
func (d *DRPCInstance) getRequeueDuration() time.Duration {
	d.log.Info("Getting requeue duration", "last known DR state", d.getLastDRState())

	const (
		failoverRequeueDelay   = time.Minute * 5
		relocationRequeueDelay = time.Second * 2
	)

	duration := time.Second // second

	switch d.getLastDRState() {
	case rmn.FailingOver:
		duration = failoverRequeueDelay
	case rmn.Relocating:
		duration = relocationRequeueDelay
	}

	return duration
}

func (d *DRPCInstance) isFailoverInProgress() bool {
	return rmn.FailingOver == d.getLastDRState()
}

func (d *DRPCInstance) isRelocationInProgress() bool {
	return rmn.Relocating == d.getLastDRState()
}
