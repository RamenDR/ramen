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
	plrv1 "github.com/open-cluster-management/multicloud-operators-placementrule/pkg/apis/apps/v1"
	errorswrapper "github.com/pkg/errors"
	fndv2 "github.com/tjanssen3/multicloud-operators-foundation/v2/pkg/apis/view/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
)

var ErrSameHomeCluster = errorswrapper.New("new home cluster is the same as current home cluster")

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

	wrapper.reconcileState = rmn.Initial // should never use a timer from Initial state; "reserved"

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

	failbackTime = newTimerWrapper(
		prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "ramen_failback_time",
			Help: "Duration of the last failback event",
		}),
		prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "ramen_failback_histogram",
			Help:    "Histogram of all failback timers (seconds)",
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
)

func init() {
	// register custom metrics with the global Prometheus registry
	metrics.Registry.MustRegister(failbackTime.gauge, failoverTime.gauge, relocateTime.gauge)
}

type PVDownloader interface {
	DownloadPVs(ctx context.Context, r client.Reader, objStoreGetter ObjectStoreGetter,
		s3Profile string, callerTag string, s3Bucket string) ([]corev1.PersistentVolume, error)
}

// ProgressCallback of function type
type ProgressCallback func(string)

// DRPlacementControlReconciler reconciles a DRPlacementControl object
type DRPlacementControlReconciler struct {
	client.Client
	Log            logr.Logger
	PVDownloader   PVDownloader
	ObjStoreGetter ObjectStoreGetter
	Scheme         *runtime.Scheme
	Callback       ProgressCallback
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

			log.Info(fmt.Sprintf("Update event for MW %s/%s: oldStatus %+v, newStatus %+v",
				oldMW.Name, oldMW.Namespace, oldMW.Status, newMW.Status))

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
	mcvPredicate := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			log := ctrl.Log.WithName("MCV")

			oldMCV, ok := e.ObjectOld.DeepCopyObject().(*fndv2.ManagedClusterView)
			if !ok {
				log.Info("Failed to deep copy older MCV")

				return false
			}
			newMCV, ok := e.ObjectNew.DeepCopyObject().(*fndv2.ManagedClusterView)
			if !ok {
				log.Info("Failed to deep copy newer MCV")

				return false
			}

			log.Info(fmt.Sprintf("Update event for MCV %s/%s: oldStatus %+v, newStatus %+v",
				oldMCV.Name, oldMCV.Namespace, oldMCV.Status, newMCV.Status))

			return !reflect.DeepEqual(oldMCV.Status, newMCV.Status)
		},
	}

	return mcvPredicate
}

func filterMCV(mcv *fndv2.ManagedClusterView) []ctrl.Request {
	return []ctrl.Request{
		reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      mcv.Annotations[rmnutil.DRPCNameAnnotation],
				Namespace: mcv.Annotations[rmnutil.DRPCNamespaceAnnotation],
			},
		},
	}
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

		// ctrl.Log.Info(fmt.Sprintf("MapFunc for ManifestWork (%v)", mw))
		return filterMW(mw)
	}))

	mcvPred := ManagedClusterViewPredicateFunc()

	mcvMapFun := handler.EnqueueRequestsFromMapFunc(handler.MapFunc(func(obj client.Object) []reconcile.Request {
		mcv, ok := obj.(*fndv2.ManagedClusterView)
		if !ok {
			ctrl.Log.Info("ManagedClusterView map function received non-MCV resource")

			return []reconcile.Request{}
		}

		return filterMCV(mcv)
	}))

	return ctrl.NewControllerManagedBy(mgr).
		For(&rmn.DRPlacementControl{}).
		Watches(&source.Kind{Type: &ocmworkv1.ManifestWork{}}, mwMapFun, builder.WithPredicates(mwPred)).
		Watches(&source.Kind{Type: &fndv2.ManagedClusterView{}}, mcvMapFun, builder.WithPredicates(mcvPred)).
		Complete(r)
}

//nolint:lll
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drplacementcontrols,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drplacementcontrols/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drplacementcontrols/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps.open-cluster-management.io,resources=placementrules,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps.open-cluster-management.io,resources=placementrules/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=managedclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=work.open-cluster-management.io,resources=manifestworks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=view.open-cluster-management.io,resources=managedclusterviews,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

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

	err := r.Client.Get(ctx, req.NamespacedName, drpc)
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
		return ctrl.Result{}, err
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

	d := DRPCInstance{
		reconciler: r, ctx: ctx, log: r.Log, instance: drpc, needStatusUpdate: false,
		userPlacementRule: userPlRule, drpcPlacementRule: drpcPlRule, drPolicy: drPolicy,
		mwu: rmnutil.MWUtil{Client: r.Client, Ctx: ctx, Log: r.Log, InstName: drpc.Name, InstNamespace: drpc.Namespace},
	}

	return r.processAndHandleResponse(&d)
}

func (r *DRPlacementControlReconciler) processAndHandleResponse(d *DRPCInstance) (ctrl.Result, error) {
	requeue := d.startProcessing()
	r.Log.Info("Finished processing", "Requeue?", requeue)

	if !requeue {
		r.Callback(d.instance.Name)

		return ctrl.Result{}, nil
	}

	if d.mcvRequestInProgress {
		duration := d.getRequeueDuration()
		r.Log.Info(fmt.Sprintf("Requeing after %v", duration))

		return reconcile.Result{RequeueAfter: duration}, nil
	}

	return ctrl.Result{Requeue: true}, nil
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

		r.Callback(drpc.Name)
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
			return fmt.Errorf("couldn't determine the preferred cluster name (%w)", err)
		}

		if len(clonedPlRule.Status.Decisions) != 0 {
			preferredCluster = clonedPlRule.Status.Decisions[0].ClusterName
		}
	}

	clustersToClean := []string{preferredCluster}
	if drpc.Spec.FailoverCluster != "" {
		clustersToClean = append(clustersToClean, drpc.Spec.FailoverCluster)
	}

	// delete manifestworks (VRG, PV, Roles)
	for idx := range clustersToClean {
		err := mwu.DeleteManifestWorksForCluster(clustersToClean[idx])
		if err != nil {
			return fmt.Errorf("%w", err)
		}
	}

	// delete cloned placementrule
	return r.deleteClonedPlacementRule(ctx, clonedPlRuleName, drpc.Namespace)
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
	mwu                  rmnutil.MWUtil
}

func (d *DRPCInstance) startProcessing() bool {
	d.log.Info("Starting to process placement")

	requeue := true

	done, err := d.processPlacement()
	if err != nil {
		d.log.Info("Process placement", "error", err)

		return requeue
	}

	if d.needStatusUpdate {
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
	d.log.Info("Process DRPC Placement")

	switch d.instance.Spec.Action {
	case rmn.ActionFailover:
		return d.runFailover()
	case rmn.ActionFailback:
		return d.runFailback()
	case rmn.ActionRelocate:
		return d.runRelocate()
	}

	// Not a failover, a failback, or a relocation.  Must be an initial deployment.
	return d.runInitialDeployment()
}

func (d *DRPCInstance) runInitialDeployment() (bool, error) {
	d.log.Info("Running initial deployment")
	d.resetDRState()

	const done = true

	// 1. Check if the user wants to use the preferredCluster
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

	if homeCluster == "" {
		return !done, fmt.Errorf("PreferredCluster not set and unable to find home cluster in DRPCPlacementRule (%v)",
			d.drpcPlacementRule)
	}

	// We have a home cluster
	err := d.updateUserPlRuleAndCreateVRGMW(homeCluster, homeClusterNamespace)
	if err != nil {
		return !done, err
	}

	// All good, update the preferred decision and state
	if len(d.userPlacementRule.Status.Decisions) > 0 {
		d.instance.Status.PreferredDecision = d.userPlacementRule.Status.Decisions[0]
	}

	d.advanceToNextDRState()

	d.log.Info(fmt.Sprintf("DRPC (%+v)", d.instance))

	return done, nil
}

func setMetricsTimerFromDRState(stateDR rmn.DRState, stateTimer timerState) {
	switch stateDR {
	case rmn.FailingOver:
		setMetricsTimer(&failoverTime, stateTimer, stateDR)
	case rmn.FailingBack:
		setMetricsTimer(&failbackTime, stateTimer, stateDR)
	case rmn.Relocating:
		setMetricsTimer(&relocateTime, stateTimer, stateDR)
	case rmn.FailedBack:
		fallthrough
	case rmn.FailedOver:
		fallthrough
	case rmn.Initial:
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
		wrapper.reconcileState = rmn.Initial                                 // "reserved" value
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
	d.log.Info("Entering runFailover", "state", d.instance.Status.LastKnownDRState)

	const done = true
	// We are done if empty
	if d.instance.Spec.FailoverCluster == "" {
		return done, fmt.Errorf("failover cluster not set. FailoverCluster is a mandatory field")
	}

	newHomeCluster := d.instance.Spec.FailoverCluster

	// We are done if we have already failed over
	if len(d.userPlacementRule.Status.Decisions) > 0 {
		if d.instance.Spec.FailoverCluster == d.userPlacementRule.Status.Decisions[0].ClusterName {
			d.log.Info("Already failed over", "state", d.instance.Status.LastKnownDRState)

			err := d.ensureCleanup(d.instance.Spec.FailoverCluster)
			if err != nil {
				return !done, err
			}

			return done, nil
		}
	}

	// Make sure we record the state that we are failing over
	d.setDRState(rmn.FailingOver)
	setMetricsTimerFromDRState(rmn.FailingOver, timerStart)

	// Save the current home cluster
	curHomeCluster := d.getCurrentHomeClusterName()

	if curHomeCluster == "" {
		d.log.Info("Invalid Failover request. Current home cluster does not exists")

		return done, fmt.Errorf("failover requested on invalid state %v", d.instance.Status)
	}

	// Set VRG in the failed cluster (preferred cluster) to secondary
	err := d.updateVRGStateToSecondary(curHomeCluster)
	if err != nil {
		d.log.Error(err, "Failed to update existing VRG manifestwork to secondary")

		return !done, err
	}

	result, err := d.runPlacementTask(newHomeCluster, "")
	if err != nil {
		return !done, err
	}

	d.advanceToNextDRState()
	d.log.Info("Exiting runFailover", "state", d.instance.Status.LastKnownDRState)
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

//
// runFailback:
// 1. If preferredCluster not set, get it from DRPC status
// 2. If still empty, fail it
// 3. If the preferredCluster is the failoverCluster, fail it
// 4. If preferredCluster is the same as the userPlacementRule decision, do nothing
// 5. Clear the user PlacementRule decision
// 6. Update VRG.Spec.ReplicationState to secondary for all DR clusters
// 7. Ensure that the VRG status reflects the previous step.  If not, then wait.
// 8. Restore PV to preferredCluster
// 9. Update UserPlacementRule decision to preferredCluster
// 10. Create VRG for the preferredCluster as Primary
// 11. Update DRPC status
// 12. Delete VRG MW from failoverCluster once the VRG state has changed to Secondary
//
func (d *DRPCInstance) runFailback() (bool, error) {
	d.log.Info("Entering runFailback", "state", d.instance.Status.LastKnownDRState)

	return d.switchToPreferredCluster(rmn.FailingBack)
}

func (d *DRPCInstance) runRelocate() (bool, error) {
	d.log.Info("Entering runRelocate", "state", d.instance.Status.LastKnownDRState)

	return d.switchToPreferredCluster(rmn.Relocating)
}

func (d *DRPCInstance) switchToPreferredCluster(drState rmn.DRState) (bool, error) {
	const done = true

	// 1. We are done if empty
	preferredCluster, preferredClusterNamespace := d.getPreferredClusterNamespaced()

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

	// Make sure we record the state that we are failing over
	d.setDRState(drState)
	setMetricsTimerFromDRState(drState, timerStart)

	// During failback or relocation, both clusters should be up and both must be secondaries before we proceed
	ensured, err := d.processVRGForSecondaries()
	if err != nil || !ensured {
		return !done, err
	}

	result, err := d.runPlacementTask(preferredCluster, preferredClusterNamespace)
	if err != nil {
		return !done, err
	}

	// 8. All good so far, update DRPC decision and state
	if len(d.userPlacementRule.Status.Decisions) > 0 {
		d.instance.Status.PreferredDecision = d.userPlacementRule.Status.Decisions[0]
	}

	d.advanceToNextDRState()
	d.log.Info("Done", "Last known state", d.instance.Status.LastKnownDRState)
	setMetricsTimerFromDRState(drState, timerStop)

	return result, nil
}

// runPlacementTast is a series of steps to creating, updating, and cleaning up
// the necessary objects for the failover, failback, or relocation
func (d *DRPCInstance) runPlacementTask(targetCluster, targetClusterNamespace string) (bool, error) {
	// Restore PV to preferredCluster
	err := d.createPVManifestWorkForRestore(targetCluster)
	if err != nil {
		return false, err
	}

	err = d.updateUserPlRuleAndCreateVRGMW(targetCluster, targetClusterNamespace)
	if err != nil {
		return false, err
	}

	// 9. Attempt to delete VRG MW from failed clusters
	// This is attempt to clean up is not guaranteed to complete at this stage. Deleting the old VRG
	// requires guaranteeing that the VRG has transitioned to secondary.
	clusterToSkip := targetCluster

	return d.cleanup(clusterToSkip)
}

func (d *DRPCInstance) hasAlreadySwitchedOver(targetCluster string) bool {
	if len(d.userPlacementRule.Status.Decisions) > 0 &&
		targetCluster == d.userPlacementRule.Status.Decisions[0].ClusterName {
		d.log.Info(fmt.Sprintf("Already switched over to cluster %s. LastKnownState %v",
			targetCluster, d.instance.Status.LastKnownDRState))

		return true
	}

	return false
}

func (d *DRPCInstance) getPreferredClusterNamespaced() (string, string) {
	preferredCluster := d.instance.Spec.PreferredCluster
	preferredClusterNamespace := preferredCluster

	if preferredCluster == "" {
		if d.instance.Status.PreferredDecision != (plrv1.PlacementDecision{}) {
			preferredCluster = d.instance.Status.PreferredDecision.ClusterName
			preferredClusterNamespace = d.instance.Status.PreferredDecision.ClusterNamespace
		}
	}

	return preferredCluster, preferredClusterNamespace
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

	for _, drCluster := range d.drPolicy.Spec.DRClusterSet {
		clusterName := drCluster.Name
		if !d.ensureVRGIsSecondary(clusterName) {
			d.log.Info("Still waiting for VRG to transition to secondary", "cluster", clusterName)

			return !ensured, nil
		}
	}

	return ensured, nil
}

// outputs a string for use in creating a ManagedClusterView name
// example: when looking for a vrg with name 'demo' in the namespace 'ramen', input: ("demo", "ramen", "vrg")
// this will give output "demo-ramen-vrg-mcv"
func BuildManagedClusterViewName(resourceName, resourceNamespace, resource string) string {
	return fmt.Sprintf("%s-%s-%s-mcv", resourceName, resourceNamespace, resource)
}

func (r *DRPlacementControlReconciler) getVRGFromManagedCluster(
	resourceName string, resourceNamespace string, managedCluster string) (*rmn.VolumeReplicationGroup, error) {
	// get VRG and verify status through ManagedClusterView
	mcvMeta := metav1.ObjectMeta{
		Name:      BuildManagedClusterViewName(resourceName, resourceNamespace, "vrg"),
		Namespace: managedCluster,
		Annotations: map[string]string{
			rmnutil.DRPCNameAnnotation:      resourceName,
			rmnutil.DRPCNamespaceAnnotation: resourceNamespace,
		},
	}

	mcvViewscope := fndv2.ViewScope{
		Resource:  "VolumeReplicationGroup",
		Name:      resourceName,
		Namespace: resourceNamespace,
	}

	vrg := &rmn.VolumeReplicationGroup{}

	err := r.getManagedClusterResource(mcvMeta, mcvViewscope, vrg)

	return vrg, err
}

func (d *DRPCInstance) updateUserPlRuleAndCreateVRGMW(homeCluster, homeClusterNamespace string) error {
	// Create VRG first, to leverage user PlacementRule decision to skip placement and move to cleanup
	err := d.createVRGManifestWork(homeCluster)
	if err != nil {
		return err
	}

	err = d.updateUserPlacementRule(homeCluster, homeClusterNamespace)
	if err != nil {
		return err
	}

	return nil
}

func (d *DRPCInstance) createPVManifestWorkForRestore(newPrimary string) error {
	if err := d.mwu.CreateOrUpdatePVRolesManifestWork(newPrimary); err != nil {
		d.log.Error(err, "failed to create or update PersistentVolume Roles manifest")

		return fmt.Errorf("failed to create or update PersistentVolume Roles manifest in namespace %s (%w)",
			newPrimary, err)
	}

	pvMWName := d.mwu.BuildManifestWorkName(rmnutil.MWTypePV)

	existAndApplied, err := d.mwu.ManifestExistAndApplied(pvMWName, newPrimary)
	if err != nil {
		if errors.IsNotFound(err) {
			d.log.Info("Restore PVs", "newPrimary", newPrimary)

			if err = d.restore(newPrimary); err != nil {
				return err
			}

			// Just restored, wait for MW to generate change event and move to applied
			return fmt.Errorf("created PV manifestwork (%s), waiting for status to be applied", pvMWName)
		}

		return fmt.Errorf("failed to check PV manifestwork status %s (%w)", pvMWName, err)
	}

	if existAndApplied {
		d.log.Info("MW for PVs exists and applied", "newPrimary", newPrimary)

		return nil
	}

	return fmt.Errorf("waiting for PV manifestwork (%s) status to be applied", pvMWName)
}

func (d *DRPCInstance) restore(newHomeCluster string) error {
	// Restore from PV backup location
	err := d.restorePVFromBackup(newHomeCluster)
	if err != nil {
		d.log.Error(err, "Failed to restore PVs from backup")

		return err
	}

	return nil
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

		// MW is deleted, VRG is deleted, so we no longer need MCV for the VRG
		err = d.deleteManagedClusterView(clusterName)
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
	d.log.Info("Processing deployment",
		"Last State", d.instance.Status.LastKnownDRState, "cluster", homeCluster)

	mw, err := d.vrgManifestWorkExists(homeCluster)
	if err != nil {
		return err
	}

	if mw != nil {
		primary, err := d.isVRGPrimary(mw)
		if err != nil {
			d.log.Error(err, "Failed to check whether the VRG is primary or not", "mw", mw.Name)

			return err
		}

		if primary {
			// Found MW and the VRG already a primary
			return nil
		}
	}

	// VRG ManifestWork does not exist, create it.
	return d.processVRGManifestWork(homeCluster)
}

func (d *DRPCInstance) vrgManifestWorkExists(homeCluster string) (*ocmworkv1.ManifestWork, error) {
	if d.instance.Status.PreferredDecision == (plrv1.PlacementDecision{}) {
		return nil, nil
	}

	mwName := d.mwu.BuildManifestWorkName(rmnutil.MWTypeVRG)

	mw, err := d.mwu.FindManifestWork(mwName, homeCluster)
	if err != nil {
		if !errors.IsNotFound(err) {
			d.log.Error(err, "failed to find ManifestWork")

			return nil, fmt.Errorf("failed to find ManifestWork %s (%w)", mwName, err)
		}
	}

	if mw == nil {
		d.log.Info(fmt.Sprintf("Manifestwork %s does not exist for cluster %s", mwName, homeCluster))

		return nil, nil
	}

	d.log.Info(fmt.Sprintf("Manifestwork %s exists (%v)", mw.Name, mw))

	return mw, nil
}

func (d *DRPCInstance) isVRGPrimary(mw *ocmworkv1.ManifestWork) (bool, error) {
	vrgClientManifest := &mw.Spec.Workload.Manifests[0]
	vrg := &rmn.VolumeReplicationGroup{}

	err := yaml.Unmarshal(vrgClientManifest.RawExtension.Raw, &vrg)
	if err != nil {
		return false, errorswrapper.Wrap(err, fmt.Sprintf("unable to get vrg from ManifestWork %s", mw.Name))
	}

	return (vrg.Spec.ReplicationState == rmn.Primary), nil
}

func (d *DRPCInstance) ensureCleanup(clusterToSkip string) error {
	d.log.Info("ensure cleanup on secondaries")

	clean, err := d.cleanup(clusterToSkip)
	if err != nil {
		return err
	}

	if !clean {
		return fmt.Errorf("failed to clean secondaries")
	}

	return nil
}

// cleanupPVForRestore cleans up required PV fields, to ensure restore succeeds to a new cluster, and
// rebinding the PV to a newly created PVC with the same claimRef succeeds
func (d *DRPCInstance) cleanupPVForRestore(pv *corev1.PersistentVolume) {
	pv.ResourceVersion = ""
	if pv.Spec.ClaimRef != nil {
		pv.Spec.ClaimRef.UID = ""
		pv.Spec.ClaimRef.ResourceVersion = ""
		pv.Spec.ClaimRef.APIVersion = ""
	}
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
	d.log.Info("Ensuring that MW for the VRG is deleted", "cluster", clusterName)

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

	if d.ensureVRGIsSecondary(clusterName) {
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

func (d *DRPCInstance) ensureVRGIsSecondary(clusterName string) bool {
	d.log.Info(fmt.Sprintf("Ensure VRG %s is secondary on cluster %s", d.instance.Name, clusterName))

	d.mcvRequestInProgress = false

	vrg, err := d.reconciler.getVRGFromManagedCluster(d.instance.Name, d.instance.Namespace, clusterName)
	if err != nil {
		// VRG must have been deleted if the error is NotFound
		if !errors.IsNotFound(err) {
			d.log.Info("Failed to get VRG", "errorValue", err)
		}

		if errors.IsNotFound(err) {
			return true // ensured
		}

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

	vrg, err := d.reconciler.getVRGFromManagedCluster(d.instance.Name, d.instance.Namespace, clusterName)
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

func (d *DRPCInstance) deleteManagedClusterView(clusterName string) error {
	mcvName := BuildManagedClusterViewName(d.instance.Name, d.instance.Namespace, "vrg")

	d.log.Info("Delete ManagedClusterView from", "namespace", clusterName, "name", mcvName)

	mcv := &fndv2.ManagedClusterView{}

	err := d.reconciler.Client.Get(d.ctx, types.NamespacedName{Name: mcvName, Namespace: clusterName}, mcv)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}

		return fmt.Errorf("failed to retrieve ManagedClusterView for type: %s. Error: %w", mcvName, err)
	}

	d.log.Info("Deleting ManagedClusterView", "name", mcv.Name, "namespace", mcv.Namespace)

	return d.reconciler.Client.Delete(d.ctx, mcv)
}

func (d *DRPCInstance) restorePVFromBackup(homeCluster string) error {
	d.log.Info("Restoring PVs to new managed cluster", "name", homeCluster)

	pvList, err := d.listPVsFromS3Store(homeCluster)
	if err != nil {
		return errorswrapper.Wrap(err, "failed to retrieve PVs from S3 store")
	}

	d.log.Info(fmt.Sprintf("Found %d PVs", len(pvList)))

	if len(pvList) == 0 {
		return nil
	}

	for idx := range pvList {
		d.cleanupPVForRestore(&pvList[idx])
	}

	// Create manifestwork for all PVs for this DRPC
	return d.mwu.CreateOrUpdatePVsManifestWork(d.instance.Name, d.instance.Namespace, homeCluster, pvList)
}

func (d *DRPCInstance) processVRGManifestWork(homeCluster string) error {
	d.log.Info("Processing VRG ManifestWork", "cluster", homeCluster)

	if err := d.mwu.CreateOrUpdateVRGRolesManifestWork(homeCluster); err != nil {
		d.log.Error(err, "failed to create or update VolumeReplicationGroup Roles manifest")

		return fmt.Errorf("failed to create or update VolumeReplicationGroup Roles manifest in namespace %s (%w)",
			homeCluster, err)
	}

	if err := d.mwu.CreateOrUpdateVRGManifestWork(
		d.instance.Name, d.instance.Namespace,
		homeCluster, d.drPolicy, d.instance.Spec.PVCSelector); err != nil {
		d.log.Error(err, "failed to create or update VolumeReplicationGroup manifest")

		return fmt.Errorf("failed to create or update VolumeReplicationGroup manifest in namespace %s (%w)", homeCluster, err)
	}

	return nil
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

	vrg, err := d.getVRGFromManifestWork(mw)
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

	vrg, err := d.getVRGFromManifestWork(mw)
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

func (d *DRPCInstance) getVRGFromManifestWork(mw *ocmworkv1.ManifestWork) (*rmn.VolumeReplicationGroup, error) {
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

	d.instance.Status.LastUpdateTime = metav1.Now()

	if err := d.reconciler.Status().Update(d.ctx, d.instance); err != nil {
		return errorswrapper.Wrap(err, "failed to update DRPC status")
	}

	d.log.Info(fmt.Sprintf("Updated DRPC Status %+v", d.instance.Status))

	return nil
}

/*
Description: queries a managed cluster for a resource type, and populates a variable with the results.
Requires:
	1) meta: information of the new/existing resource; defines which cluster(s) to search
	2) viewscope: query information for managed cluster resource. Example: resource, name.
	3) interface: empty variable to populate results into
Returns: error if encountered (nil if no error occurred). See results on interface object.
*/
func (r *DRPlacementControlReconciler) getManagedClusterResource(
	meta metav1.ObjectMeta, viewscope fndv2.ViewScope, resource interface{}) error {
	// create MCV first
	mcv, err := r.getOrCreateManagedClusterView(meta, viewscope)
	if err != nil {
		return errorswrapper.Wrap(err, "getManagedClusterResource failed")
	}

	r.Log.Info(fmt.Sprintf("MCV condtions: %v", mcv.Status.Conditions))

	// want single recent Condition with correct Type; otherwise: bad path
	switch len(mcv.Status.Conditions) {
	case 0:
		err = fmt.Errorf("missing ManagedClusterView conditions")
	case 1:
		switch {
		case mcv.Status.Conditions[0].Type != fndv2.ConditionViewProcessing:
			err = fmt.Errorf("found invalid condition (%s) in ManagedClusterView", mcv.Status.Conditions[0].Type)
		case mcv.Status.Conditions[0].Reason == fndv2.ReasonGetResourceFailed:
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
func (r *DRPlacementControlReconciler) getOrCreateManagedClusterView(
	meta metav1.ObjectMeta, viewscope fndv2.ViewScope) (*fndv2.ManagedClusterView, error) {
	mcv := &fndv2.ManagedClusterView{
		ObjectMeta: meta,
		Spec: fndv2.ViewSpec{
			Scope: viewscope,
		},
	}

	err := r.Get(context.TODO(), types.NamespacedName{Name: meta.Name, Namespace: meta.Namespace}, mcv)
	if err != nil {
		if errors.IsNotFound(err) {
			r.Log.Info("Creating ManagedClusterView", "mcv", mcv)
			err = r.Create(context.TODO(), mcv)
		}

		if err != nil {
			return nil, errorswrapper.Wrap(err, "failed to getOrCreateManagedClusterView")
		}
	}

	if mcv.Spec.Scope != viewscope {
		r.Log.Info("WARNING: existing ManagedClusterView has different ViewScope than desired one")
	}

	return mcv, nil
}

func (d *DRPCInstance) listPVsFromS3Store(homeCluster string) ([]corev1.PersistentVolume, error) {
	s3Bucket := constructBucketName(d.instance.Namespace, d.instance.Name)

	return d.reconciler.PVDownloader.DownloadPVs(
		d.ctx, d.reconciler.Client, d.reconciler.ObjStoreGetter, rmnutil.S3DownloadProfile(*d.drPolicy, homeCluster),
		d.instance.Name, s3Bucket)
}

type ObjectStorePVDownloader struct{}

func (s ObjectStorePVDownloader) DownloadPVs(ctx context.Context, r client.Reader,
	objStoreGetter ObjectStoreGetter, s3Profile string,
	callerTag string, s3Bucket string) ([]corev1.PersistentVolume, error) {
	objectStore, err := objStoreGetter.objectStore(ctx, r, s3Profile, callerTag)
	if err != nil {
		return nil, fmt.Errorf("error when downloading PVs, err %w", err)
	}

	return objectStore.downloadPVs(s3Bucket)
}

func (d *DRPCInstance) advanceToNextDRState() {
	var nextState rmn.DRState

	switch d.instance.Status.LastKnownDRState {
	case rmn.DRState(""):
		nextState = rmn.Initial
	case rmn.FailingOver:
		nextState = rmn.FailedOver
	case rmn.FailingBack:
		nextState = rmn.FailedBack
	case rmn.Relocating:
		nextState = rmn.Relocated
	case rmn.Initial:
	case rmn.FailedOver:
	case rmn.FailedBack:
	case rmn.Relocated:
	default:
		nextState = rmn.DRState("")
	}

	d.setDRState(nextState)
}

func (d *DRPCInstance) resetDRState() {
	d.log.Info("Resetting last known DR state", "lndrs", d.instance.Status.LastKnownDRState)

	d.setDRState(rmn.DRState(""))
}

func (d *DRPCInstance) setDRState(nextState rmn.DRState) {
	if d.instance.Status.LastKnownDRState != nextState {
		d.log.Info(fmt.Sprintf("LastKnownDRState: Current State '%s'. Next State '%s'",
			d.instance.Status.LastKnownDRState, nextState))

		d.instance.Status.LastKnownDRState = nextState
		d.needStatusUpdate = true
	}
}

func (d *DRPCInstance) getRequeueDuration() time.Duration {
	d.log.Info("Getting requeue duration", "last known DR state", d.instance.Status.LastKnownDRState)

	const (
		failoverRequeueDelay = 5
		failbackRequeueDelay = 2
	)

	duration := time.Second // 1s

	switch d.instance.Status.LastKnownDRState {
	case rmn.FailingOver:
		duration = time.Minute * failoverRequeueDelay
	case rmn.FailingBack:
	case rmn.Relocating:
		duration = time.Second * failbackRequeueDelay
	case rmn.Initial:
	case rmn.FailedOver:
	case rmn.FailedBack:
	case rmn.Relocated:
	}

	return duration
}
