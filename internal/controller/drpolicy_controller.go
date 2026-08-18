// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	ocmv1 "open-cluster-management.io/api/cluster/v1"
	viewv1beta1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/view/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/util"
)

// DRPolicyReconciler reconciles a DRPolicy object
type DRPolicyReconciler struct {
	client.Client
	APIReader         client.Reader
	Log               logr.Logger
	Scheme            *runtime.Scheme
	MCVGetter         util.ManagedClusterViewGetter
	ObjectStoreGetter ObjectStoreGetter
	RateLimiter       *workqueue.TypedRateLimiter[reconcile.Request]
}

// ReasonValidationFailed is set when the DRPolicy could not be validated or is not valid
const ReasonValidationFailed = "ValidationFailed"

// ReasonDRClusterNotFound is set when the DRPolicy could not find the referenced DRCluster(s)
const ReasonDRClusterNotFound = "DRClusterNotFound"

// ReasonDRClustersUnavailable is set when the DRPolicy has none of the referenced DRCluster(s) are in a validated state
const ReasonDRClustersUnavailable = "DRClustersUnavailable"

// ReasonDRPolicyConflictFound is set when the DRPolicy has overlapping metro clusters with another DRPolicy
const ReasonDRPolicyConflictFound = "DRPolicyConflictFound"

// ReasonNoEligibleNetworkAttachments is set on NetworkAttachmentsValidated when no NADs eligible for static-IP
// translation were discovered on either cluster; symmetry is trivially satisfied.
const ReasonNoEligibleNetworkAttachments = "NoEligibleNetworkAttachments"

// ReasonSucceeded is set when the DRPolicy validation completes successfully
const ReasonSucceeded = "Succeeded"

// AllDRPolicyAnnotation is added to related resources that can be watched to reconcile all related DRPolicy resources
const AllDRPolicyAnnotation = "drpolicy.ramendr.openshift.io"

// ConditionNetworkAttachmentsValidated is set on DRPolicy.Status.Conditions when NAD-pair
// validation has run.
//   - True:  all NADs used by static-IP VMs are present on both clusters.
//   - False: one or more NADs are missing; the message lists them.
const ConditionNetworkAttachmentsValidated = "NetworkAttachmentsValidated"

//nolint:lll
//+kubebuilder:rbac:groups=ramendr.openshift.io,resources=drpolicies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ramendr.openshift.io,resources=drpolicies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ramendr.openshift.io,resources=drpolicies/finalizers,verbs=update
// +kubebuilder:rbac:groups=work.open-cluster-management.io,resources=manifestworks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups="policy.open-cluster-management.io",resources=placementbindings,verbs=list;watch
// +kubebuilder:rbac:groups="policy.open-cluster-management.io",resources=policies,verbs=list;watch
// +kubebuilder:rbac:groups="",namespace=system,resources=secrets,verbs=get;update
// +kubebuilder:rbac:groups="policy.open-cluster-management.io",namespace=system,resources=placementbindings,verbs=get;create;update;delete
// +kubebuilder:rbac:groups="policy.open-cluster-management.io",namespace=system,resources=policies,verbs=get;create;update;delete
// +kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=managedclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=view.open-cluster-management.io,resources=managedclusterviews,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the DRPolicy object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.9.2/pkg/reconcile
//
//nolint:cyclop,funlen
func (r *DRPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("drp", req.NamespacedName.Name, "rid", util.GetRID())
	log.Info("Entering reconcile loop")

	defer log.Info("Exiting reconcile loop")

	// Recompute on every reconcile, including the not-found path after a
	// DRPolicy deletion, so the metrics track the cluster state
	defer func() {
		if err := UpdateDRTelemetryMetrics(ctx, r.Client); err != nil {
			log.Info("Failed to update DR telemetry metrics", "error", err)
		}
	}()

	drpolicy := &ramen.DRPolicy{}
	if err := r.Client.Get(ctx, req.NamespacedName, drpolicy); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(fmt.Errorf("get: %w", err))
	}

	u := &drpolicyUpdater{ctx, drpolicy, r.Client, log}

	_, ramenConfig, err := ConfigMapGet(ctx, r.APIReader)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("config map get: %w", u.validatedSetFalse("ConfigMapGetFailed", err))
	}

	if err := util.CreateRamenOpsNamespace(ctx, r.Client, ramenConfig); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create RamenOpsNamespace: %w",
			u.validatedSetFalse("NamespaceCreateFailed", err))
	}

	drclusters, drClusterIDsToNames, err := r.getDRClusterDetails(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("drclusters details: %w", u.validatedSetFalse("drClusterDetailsFailed", err))
	}

	secretsUtil := &util.SecretsUtil{Client: r.Client, APIReader: r.APIReader, Ctx: ctx, Log: log}
	// DRPolicy is marked for deletion
	if util.ResourceIsDeleted(drpolicy) &&
		controllerutil.ContainsFinalizer(drpolicy, drPolicyFinalizerName) {
		return ctrl.Result{}, u.deleteDRPolicy(drclusters, secretsUtil, ramenConfig)
	}

	log.Info("create/update")

	reason, err := validateDRPolicy(drpolicy, drclusters)
	if err != nil {
		statusErr := u.validatedSetFalse(reason, err)
		if !errors.Is(statusErr, err) || reason != ReasonDRClusterNotFound {
			return ctrl.Result{}, fmt.Errorf("validate: %w", statusErr)
		}

		log.Error(err, "Missing dependent resources")

		// will be reconciled later based on DRCluster watch events
		return ctrl.Result{}, nil
	}

	if err := u.addLabelsAndFinalizers(); err != nil {
		return ctrl.Result{}, fmt.Errorf("finalizer add update: %w", u.validatedSetFalse("FinalizerAddFailed", err))
	}

	return r.reconcile(u, drclusters, secretsUtil, ramenConfig, drClusterIDsToNames)
}

//nolint:unparam
func (r *DRPolicyReconciler) reconcile(
	u *drpolicyUpdater,
	drclusters *ramen.DRClusterList,
	secretsUtil *util.SecretsUtil,
	ramenConfig *ramen.RamenConfig,
	drClusterIDsToNames map[string]string,
) (ctrl.Result, error) {
	if err := setValidationSucceeded(u); err != nil {
		return ctrl.Result{}, fmt.Errorf(
			"unable to set drpolicy validation: %w", err,
		)
	}

	if err := updatePeerClasses(u, r.MCVGetter); err != nil {
		return ctrl.Result{}, fmt.Errorf("drpolicy peerClass update: %w", err)
	}

	if err := propagateS3Secret(u.object, drclusters, secretsUtil, ramenConfig, u.log); err != nil {
		return ctrl.Result{}, fmt.Errorf("drpolicy deploy: %w", err)
	}

	// we will be able to validate conflicts only after PeerClasses are updated
	err := validatePolicyConflicts(u.ctx, r.APIReader, u.object, drClusterIDsToNames)
	if err != nil {
		return ctrl.Result{}, u.validatedSetFalse(ReasonDRPolicyConflictFound, err)
	}

	result, err := r.validateNADs(u)
	if err != nil || !result.IsZero() {
		return result, err
	}

	if err := u.validatedSetTrue(); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to set drpolicy validation: %w", err)
	}

	if err := r.initiateDRPolicyMetrics(u.object); err != nil {
		return ctrl.Result{}, fmt.Errorf("error in intiating policy metrics: %w", err)
	}

	return ctrl.Result{}, nil
}

func setValidationSucceeded(u *drpolicyUpdater) error {
	if !isNetworkMappingEnabled(u.object) {
		if util.FindCondition(u.object.Status.Conditions, ConditionNetworkAttachmentsValidated) != nil {
			return clearNADStatus(u)
		}

		if !u.isConflictFound() {
			return u.validatedSetTrue()
		}

		return nil
	}

	if u.isConflictFound() || u.isNADsMissing() {
		return nil
	}

	return u.validatedSetTrue()
}

func isNetworkMappingEnabled(drPolicy *ramen.DRPolicy) bool {
	if drPolicy.Spec.NetworkMappingRef == nil || len(drPolicy.Spec.NetworkMappingRef.Name) == 0 {
		return false
	}

	return true
}

func (r *DRPolicyReconciler) validateNADs(
	u *drpolicyUpdater,
) (ctrl.Result, error) {
	if !isNetworkMappingEnabled(u.object) {
		return ctrl.Result{}, nil
	}

	// NAD validation: ensure that NADs referenced by the network mapping
	// configuration are present on all clusters in the DRPolicy. Missing NADs
	// are surfaced through a DRPolicy condition to help users identify and
	// correct configuration gaps before protecting applications.
	nadValidator := NewNetworkMappingValidator(r.Client, r.MCVGetter, u.log)

	nadsValid, err := nadValidator.UpdateNADValidationCondition(u)
	if err != nil {
		return ctrl.Result{Requeue: true}, err
	}

	if !nadsValid {
		return ctrl.Result{}, u.validatedSetFalse(
			"NADsMissing",
			fmt.Errorf(
				"one or more NADs are absent on a peer cluster; see NetworkAttachmentsValidated condition",
			),
		)
	}

	return ctrl.Result{}, nil
}

// isNADsMissing returns true when the Validated condition was last set to False
// by NAD validation.  This prevents the unconditional validatedSetTrue calls at
// the top of reconcile from overwriting a False written by UpdateNADValidationCondition,
// which would cause Validated to oscillate True/False on every reconcile cycle.
func (u *drpolicyUpdater) isNADsMissing() bool {
	for _, condition := range u.object.Status.Conditions {
		if condition.Type == ramen.DRPolicyValidated &&
			condition.Status == metav1.ConditionFalse &&
			condition.Reason == "NADsMissing" {
			return true
		}
	}

	return false
}

func (r *DRPolicyReconciler) initiateDRPolicyMetrics(drpolicy *ramen.DRPolicy) error {
	isMetro, _, err := dRPolicySupportsMetro(drpolicy, nil)
	if err != nil {
		return fmt.Errorf("failed to check if DRPolicy supports Metro: %w", err)
	}

	// Do not set metric for metro-dr
	if !isMetro {
		if err := r.setDRPolicyMetrics(drpolicy); err != nil {
			return fmt.Errorf("error in setting drpolicy metrics: %w", err)
		}
	}

	return nil
}

func (r *DRPolicyReconciler) getDRClusterDetails(ctx context.Context) (*ramen.DRClusterList, map[string]string, error) {
	drClusters := &ramen.DRClusterList{}
	if err := r.Client.List(ctx, drClusters); err != nil {
		return nil, nil, fmt.Errorf("drclusters list: %w", err)
	}

	drClusterIDsToNames := map[string]string{}

	for idx := range drClusters.Items {
		mc, err := util.NewManagedClusterInstance(ctx, r.Client, drClusters.Items[idx].GetName())
		if err != nil {
			r.Log.Error(err, "Failed to get a new MC instance", "drcluster", drClusters.Items[idx].GetName())

			continue
		}

		clID, err := mc.ClusterID()
		if err != nil {
			return nil, nil, fmt.Errorf("drclusters cluster ID (%s): %w", drClusters.Items[idx].GetName(), err)
		}

		drClusterIDsToNames[clID] = drClusters.Items[idx].GetName()
	}

	if len(drClusterIDsToNames) == 0 {
		return nil, nil, fmt.Errorf("no DRClusters found")
	}

	return drClusters, drClusterIDsToNames, nil
}

func validateDRPolicy(drpolicy *ramen.DRPolicy,
	drclusters *ramen.DRClusterList,
) (string, error) {
	// TODO: Ensure DRClusters exist and are validated? Also ensure they are not in a deleted state!?
	// If new DRPolicy and clusters are deleted, then fail reconciliation?
	if len(drpolicy.Spec.DRClusters) == 0 {
		return ReasonValidationFailed, fmt.Errorf("missing DRClusters list in policy")
	}

	reason, err := ensureDRClustersAvailable(drpolicy, drclusters)
	if err != nil {
		return reason, err
	}

	return "", nil
}

func (r *DRPolicyReconciler) setDRPolicyMetrics(drPolicy *ramen.DRPolicy) error {
	r.Log.Info(fmt.Sprintf("Setting metric: (%v)", DRPolicySyncIntervalSeconds))

	syncIntervalMetricsLabels := DRPolicySyncIntervalMetricLabels(drPolicy)
	metric := NewDRPolicySyncIntervalMetrics(syncIntervalMetricsLabels)

	schedulingIntervalSeconds, err := util.GetSecondsFromSchedulingInterval(drPolicy)
	if err != nil {
		return fmt.Errorf("unable to convert scheduling interval to seconds: %w", err)
	}

	metric.DRPolicySyncInterval.Set(schedulingIntervalSeconds)

	return nil
}

func ensureDRClustersAvailable(drpolicy *ramen.DRPolicy, drclusters *ramen.DRClusterList) (string, error) {
	found := 0
	validated := 0

	for _, specCluster := range drpolicy.Spec.DRClusters {
		for _, cluster := range drclusters.Items {
			if cluster.Name == specCluster {
				found++

				condition := util.FindCondition(cluster.Status.Conditions, ramen.DRClusterValidated)
				if condition != nil && condition.Status == metav1.ConditionTrue {
					validated++
				}
			}
		}
	}

	if found != len(drpolicy.Spec.DRClusters) {
		return ReasonDRClusterNotFound, fmt.Errorf("failed to find DRClusters specified in policy (%v)",
			drpolicy.Spec.DRClusters)
	}

	if validated == 0 {
		return ReasonDRClustersUnavailable, fmt.Errorf("none of the DRClusters are validated (%v)",
			drpolicy.Spec.DRClusters)
	}

	return "", nil
}

func validatePolicyConflicts(ctx context.Context,
	apiReader client.Reader,
	drpolicy *ramen.DRPolicy,
	drClusterIDsToNames map[string]string,
) error {
	// DRPolicy does not support both Sync and Async configurations in one single DRPolicy
	if len(drpolicy.Status.Sync.PeerClasses) > 0 && len(drpolicy.Status.Async.PeerClasses) > 0 {
		return fmt.Errorf("invalid DRPolicy: a policy cannot contain both sync and async configurations")
	}

	drpolicies, err := util.GetAllDRPolicies(ctx, apiReader)
	if err != nil {
		return fmt.Errorf("validate managed cluster in drpolicy %v failed: %w", drpolicy.Name, err)
	}

	err = HasConflictingDRPolicy(drpolicy, drpolicies, drClusterIDsToNames)
	if err != nil {
		return fmt.Errorf("validate managed cluster in drpolicy failed: %w", err)
	}

	return nil
}

// If two drpolicies have common managed cluster(s) and at least one of them is
// a metro supported drpolicy, then fail.
func HasConflictingDRPolicy(
	match *ramen.DRPolicy,
	list ramen.DRPolicyList,
	drClusterIDsToNames map[string]string,
) error {
	// Valid cases
	// [e1,w1] [e1,c1]
	// [e1,w1] [e1,w1]
	// [e1,w1] [e2,e3,w1]
	// [e1,e2,w1] [e3,e4,w1]
	// [e1,e2,w1,w2,c1] [e3,e4,w3,w4,c1]
	//
	// Failure cases
	// [e1,e2] [e1,e3] intersection e1, east=e1,e2 east=e1,e3
	// [e1,e2] [e1,w1]
	// [e1,e2,w1] [e1,e2,w1]
	// [e1,e2,c1] [e1,w1]
	for i := range list.Items {
		drp := &list.Items[i]

		if drp.ObjectMeta.Name == match.ObjectMeta.Name {
			continue
		}

		// Only the newer policy is invalidated to avoid oscillation where
		// both policies keep toggling between validated and invalid.
		if match.CreationTimestamp.Before(&drp.CreationTimestamp) {
			continue
		}

		// None of the common managed clusters should belong to Metro clusters in either of the drpolicies.
		if haveOverlappingMetroZones(match, drp, drClusterIDsToNames) {
			return fmt.Errorf("drpolicy: %v has overlapping clusters with another drpolicy %v", match.Name, drp.Name)
		}
	}

	return nil
}

//nolint:errcheck
func haveOverlappingMetroZones(
	d1, d2 *ramen.DRPolicy,
	drClusterIDsToNames map[string]string,
) bool {
	d1ClusterNames := sets.NewString(util.DRPolicyClusterNames(d1)...)
	d1SupportsMetro, d1MetroClusters, _ := dRPolicySupportsMetro(d1, drClusterIDsToNames)
	d2ClusterNames := sets.NewString(util.DRPolicyClusterNames(d2)...)
	d2SupportsMetro, d2MetroClusters, _ := dRPolicySupportsMetro(d2, drClusterIDsToNames)
	commonClusters := d1ClusterNames.Intersection(d2ClusterNames)

	// No common managed clusters, so we are good
	if commonClusters.Len() == 0 {
		return false
	}

	// Lets check if the metro clusters in DRPolicy d2 belong to common managed clusters list
	if d2SupportsMetro {
		for _, v := range d2MetroClusters {
			if sets.NewString(v...).HasAny(commonClusters.List()...) {
				return true
			}
		}
	}

	// Lets check if the metro clusters in DRPolicy d1 belong to common managed clusters list
	if d1SupportsMetro {
		for _, v := range d1MetroClusters {
			if sets.NewString(v...).HasAny(commonClusters.List()...) {
				return true
			}
		}
	}

	return false
}

type drpolicyUpdater struct {
	ctx    context.Context
	object *ramen.DRPolicy
	client client.Client
	log    logr.Logger
}

func (u *drpolicyUpdater) deleteDRPolicy(drclusters *ramen.DRClusterList,
	secretsUtil *util.SecretsUtil,
	ramenConfig *ramen.RamenConfig,
) error {
	u.log.Info("delete")

	drpcs := ramen.DRPlacementControlList{}
	if err := secretsUtil.Client.List(secretsUtil.Ctx, &drpcs); err != nil {
		return fmt.Errorf("drpcs list: %w", err)
	}

	for i := range drpcs.Items {
		drpc1 := &drpcs.Items[i]
		if u.object.ObjectMeta.Name == drpc1.Spec.DRPolicyRef.Name {
			return fmt.Errorf("this drpolicy is referenced in existing drpc resource name '%v' ", drpc1.Name)
		}
	}

	if err := drPolicyUndeploy(u.object, drclusters, secretsUtil, ramenConfig, u.log); err != nil {
		return fmt.Errorf("drpolicy undeploy: %w", err)
	}

	if err := u.finalizerRemove(); err != nil {
		return fmt.Errorf("finalizer remove update: %w", err)
	}

	// proceed to delete metrics if non-metro-dr
	isMetro, _, err := dRPolicySupportsMetro(u.object,
		nil)
	if err != nil {
		return fmt.Errorf("failed to check if DRPolicy supports Metro: %w", err)
	}

	if !isMetro {
		// delete metrics if matching labels are found
		metricLabels := DRPolicySyncIntervalMetricLabels(u.object)
		DeleteDRPolicySyncIntervalMetrics(metricLabels)
	}

	return nil
}

func (u *drpolicyUpdater) isConflictFound() bool {
	for _, condition := range u.object.Status.Conditions {
		if condition.Type == ramen.DRPolicyValidated && condition.Reason == ReasonDRPolicyConflictFound {
			return true
		}
	}

	return false
}

func (u *drpolicyUpdater) validatedSetTrue() error {
	return u.statusConditionSet(ramen.DRPolicyValidated, metav1.ConditionTrue, ReasonSucceeded, "drpolicy validated")
}

func (u *drpolicyUpdater) validatedSetFalse(reason string, err error) error {
	if err1 := u.statusConditionSet(ramen.DRPolicyValidated, metav1.ConditionFalse, reason, err.Error()); err1 != nil {
		return err1
	}

	return err
}

func (u *drpolicyUpdater) statusConditionSet(conditionType string,
	status metav1.ConditionStatus,
	reason, message string,
) error {
	conditions := &u.object.Status.Conditions

	if util.GenericStatusConditionSet(u.object, conditions, conditionType,
		status, reason, message, u.log) {
		return u.statusUpdate()
	}

	return nil
}

func (u *drpolicyUpdater) statusUpdate() error {
	return u.client.Status().Update(u.ctx, u.object)
}

const drPolicyFinalizerName = "drpolicies.ramendr.openshift.io/ramen"

func (u *drpolicyUpdater) addLabelsAndFinalizers() error {
	return util.NewResourceUpdater(u.object).
		AddLabel(util.OCMBackupLabelKey, util.OCMBackupLabelValue).
		AddFinalizer(drPolicyFinalizerName).
		Update(u.ctx, u.client)
}

func (u *drpolicyUpdater) finalizerRemove() error {
	return util.NewResourceUpdater(u.object).
		RemoveFinalizer(drPolicyFinalizerName).
		Update(u.ctx, u.client)
}

// SetupWithManager sets up the controller with the Manager.
func (r *DRPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	controller := ctrl.NewControllerManagedBy(mgr)
	if r.RateLimiter != nil {
		controller.WithOptions(ctrlcontroller.Options{
			RateLimiter: *r.RateLimiter,
		})
	}

	return controller.
		For(&ramen.DRPolicy{}).
		Watches(
			&corev1.ConfigMap{},
			handler.EnqueueRequestsFromMapFunc(r.configMapMapFunc),
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{}),
		).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.secretMapFunc),
			builder.WithPredicates(util.CreateOrDeleteOrResourceVersionUpdatePredicate{}),
		).
		Watches(
			&ramen.DRCluster{},
			handler.EnqueueRequestsFromMapFunc(r.objectNameAsClusterMapFunc),
			builder.WithPredicates(util.CreateOrDeleteOrResourceVersionUpdatePredicate{}),
		).
		Watches(
			&ocmv1.ManagedCluster{},
			handler.EnqueueRequestsFromMapFunc(r.objectNameAsClusterMapFunc),
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{}),
		).
		Watches(
			&viewv1beta1.ManagedClusterView{},
			handler.EnqueueRequestsFromMapFunc(r.mcvMapFun),
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{})).
		Complete(r)
}

func (r *DRPolicyReconciler) configMapMapFunc(ctx context.Context, configMap client.Object) []reconcile.Request {
	if configMap.GetName() != HubOperatorConfigMapName || configMap.GetNamespace() != RamenOperatorNamespace() {
		return []reconcile.Request{}
	}

	labelAdded := util.AddLabel(configMap, util.OCMBackupLabelKey, util.OCMBackupLabelValue)

	if labelAdded {
		if err := r.Update(context.TODO(), configMap); err != nil {
			r.Log.Error(err, "Failed to add OCM backup label to ramen-hub-operator-config map")

			return []reconcile.Request{}
		}
	}

	drpolicies := &ramen.DRPolicyList{}
	if err := r.Client.List(context.TODO(), drpolicies); err != nil {
		return []reconcile.Request{}
	}

	requests := make([]reconcile.Request, len(drpolicies.Items))
	for i, drpolicy := range drpolicies.Items {
		requests[i].Name = drpolicy.GetName()
	}

	return requests
}

func (r *DRPolicyReconciler) secretMapFunc(ctx context.Context, secret client.Object) []reconcile.Request {
	if secret.GetNamespace() != RamenOperatorNamespace() {
		return []reconcile.Request{}
	}

	drpolicies := &ramen.DRPolicyList{}
	if err := r.Client.List(context.TODO(), drpolicies); err != nil {
		return []reconcile.Request{}
	}

	// TODO: Add optimzation to only reconcile policies that refer to the changed secret
	requests := make([]reconcile.Request, len(drpolicies.Items))
	for i, drpolicy := range drpolicies.Items {
		requests[i].Name = drpolicy.GetName()
	}

	return requests
}

// objectNameAsClusterMapFunc returns a list of DRPolicies that contain the object.Name. A DRCluster or a
// ManagedCluster object can be passed in as the cluster to find the list of policies to reconcile
func (r *DRPolicyReconciler) objectNameAsClusterMapFunc(
	ctx context.Context, cluster client.Object,
) []reconcile.Request {
	return r.getDRPoliciesForCluster(cluster.GetName())
}

func (r *DRPolicyReconciler) mcvMapFun(ctx context.Context, obj client.Object) []reconcile.Request {
	mcv, ok := obj.(*viewv1beta1.ManagedClusterView)
	if !ok {
		return []reconcile.Request{}
	}

	if _, ok := mcv.Annotations[AllDRPolicyAnnotation]; !ok {
		return []ctrl.Request{}
	}

	return r.getDRPoliciesForCluster(obj.GetNamespace())
}

func (r *DRPolicyReconciler) getDRPoliciesForCluster(clusterName string) []reconcile.Request {
	drpolicies := &ramen.DRPolicyList{}
	if err := r.Client.List(context.TODO(), drpolicies); err != nil {
		return []reconcile.Request{}
	}

	requests := make([]reconcile.Request, 0)

	for idx := range drpolicies.Items {
		drpolicy := &drpolicies.Items[idx]
		if util.DrpolicyContainsDrcluster(drpolicy, clusterName) {
			add := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name: drpolicy.GetName(),
				},
			}
			requests = append(requests, add)
		}
	}

	return requests
}

// NetworkMappingValidator handles network-mapping validation for DRPolicy.
// Its sole responsibility is NAD-pair validation: verifying that every NAD
// used by static-IP VMs under a policy is present on both clusters.
//
// IP translation rules live in the per-DRPC network-mapping ConfigMap
// (drpc_networkmapping.go) and are never touched here.
type NetworkMappingValidator struct {
	client    client.Client
	mcvGetter util.ManagedClusterViewGetter
	log       logr.Logger
}

// NADMissingEntry describes a NAD that is present on one cluster but absent from the other.
type NADMissingEntry struct {
	NADName      string
	NADNamespace string
	MissingOnA   bool
	ClusterA     string
	MissingOnB   bool
	ClusterB     string
}

// NewNetworkMappingValidator creates a new network mapping validator.
func NewNetworkMappingValidator(c client.Client, mcvGetter util.ManagedClusterViewGetter,
	log logr.Logger,
) *NetworkMappingValidator {
	return &NetworkMappingValidator{client: c, mcvGetter: mcvGetter, log: log}
}

// UpdateNADValidationCondition runs NAD validation and writes the result as a standard
// Kubernetes condition on DRPolicy.Status.Conditions (type NetworkAttachmentsValidated).
//
//   - True  — all NADs used by static-IP VMs are present on both clusters.
//   - False — one or more NADs are missing; message lists each one.
//
// Transient errors (for example, DRClusterConfig or ManagedClusterView
// not yet available) are logged and returned without modifying the
// existing NetworkAttachmentsValidated condition.
func (v *NetworkMappingValidator) UpdateNADValidationCondition(u *drpolicyUpdater,
) (nadsValid bool, err error) {
	drPolicy := u.object
	// Nothing to validate when network mapping is not configured for this policy.
	if !isNetworkMappingEnabled(drPolicy) {
		return false, clearNADStatus(u)
	}

	missing, err := v.validateNADsAcrossClusters(drPolicy)
	if err != nil {
		v.log.Error(err, "NAD validation transient error",
			"drpolicy", drPolicy.Name)

		// Preserve the existing condition state and retry later. Transient
		// failures (for example, DRClusterConfig/MCV not yet available)
		// should not overwrite a previously successful validation result.
		return false, err
	}

	// Distinguish three outcomes:
	//  1. Both clusters have zero eligible NADs — trivially symmetric but nothing
	//     usable for static-IP translation.  Surface this explicitly so users are
	//     not misled into thinking translation is active.
	//  2. NADs are present and fully symmetric — validation succeeded.
	//  3. One or more NADs are missing on a peer cluster — validation failed.
	noEligibleNADs := len(missing) == 0 && len(drPolicy.Status.NetworkPeers) == 0

	switch {
	case noEligibleNADs:
		util.GenericStatusConditionSet(drPolicy, &drPolicy.Status.Conditions,
			ConditionNetworkAttachmentsValidated,
			metav1.ConditionTrue, ReasonNoEligibleNetworkAttachments,
			"No NADs eligible for static-IP translation were discovered on either cluster; "+
				"symmetry is trivially satisfied but no IP translation will occur.",
			v.log)

	case len(missing) == 0:
		util.GenericStatusConditionSet(drPolicy, &drPolicy.Status.Conditions,
			ConditionNetworkAttachmentsValidated,
			metav1.ConditionTrue, "Validated",
			"NADs are symmetric across both clusters",
			v.log)

	default:
		util.GenericStatusConditionSet(drPolicy, &drPolicy.Status.Conditions,
			ConditionNetworkAttachmentsValidated,
			metav1.ConditionFalse, "NADsMissing",
			buildMissingNADsMessage(missing),
			v.log)

		util.GenericStatusConditionSet(drPolicy, &drPolicy.Status.Conditions,
			ramen.DRPolicyValidated,
			metav1.ConditionFalse, "NADsMissing",
			"one or more NADs are absent on a peer cluster; see NetworkAttachmentsValidated condition",
			v.log)
	}

	// Always persist: NetworkPeers (populated by validateNADsAcrossClusters) must
	// be written back even when the condition itself did not change, because
	// GenericStatusConditionSet only writes when the condition transitions.
	if err := v.client.Status().Update(u.ctx, drPolicy); err != nil {
		return false, fmt.Errorf("status update for NAD validation: %w", err)
	}

	return len(missing) == 0, nil
}

func (v *NetworkMappingValidator) validateNADsAcrossClusters(
	drPolicy *ramen.DRPolicy,
) ([]NADMissingEntry, error) {
	if len(drPolicy.Spec.DRClusters) < DRClusterPairCount {
		return nil, fmt.Errorf(
			"drpolicy %s has %d clusters, expected at least %d",
			drPolicy.Name,
			len(drPolicy.Spec.DRClusters),
			DRClusterPairCount,
		)
	}

	clusterA, clusterB := drPolicy.Spec.DRClusters[0], drPolicy.Spec.DRClusters[1]

	infoA, err := v.clusterNADInfo(clusterA)
	if err != nil {
		return nil, fmt.Errorf("reading DRClusterConfig for %s: %w", clusterA, err)
	}

	infoB, err := v.clusterNADInfo(clusterB)
	if err != nil {
		return nil, fmt.Errorf("reading DRClusterConfig for %s: %w", clusterB, err)
	}

	// Populate Status.NetworkPeers — NADs symmetric across both clusters.
	v.updateNetworkPeers(drPolicy, clusterA, clusterB, infoA, infoB)

	// Check: every NAD on A must be on B, and vice versa.
	var missing []NADMissingEntry

	for key, naA := range infoA {
		if _, onB := infoB[key]; !onB {
			missing = append(missing, NADMissingEntry{
				NADName:      naA.Name,
				NADNamespace: naA.Namespace,
				MissingOnB:   true,
				ClusterB:     clusterB,
			})
		}
	}

	for key, naB := range infoB {
		if _, onA := infoA[key]; !onA {
			missing = append(missing, NADMissingEntry{
				NADName:      naB.Name,
				NADNamespace: naB.Namespace,
				MissingOnA:   true,
				ClusterA:     clusterA,
			})
		}
	}

	sort.Slice(missing, func(i, j int) bool {
		if missing[i].NADNamespace != missing[j].NADNamespace {
			return missing[i].NADNamespace < missing[j].NADNamespace
		}

		return missing[i].NADName < missing[j].NADName
	})

	return missing, nil
}

// clusterNADInfo returns the full NetworkAttachment inventory for the given
// cluster, keyed by "namespace/name".
//
// DRClusterConfig is a cluster-scoped resource that lives on the managed cluster.
// The hub reads it via a ManagedClusterView (created by the DRCluster controller);
// a direct hub-local client.Get would not reach the managed-cluster copy.
func (v *NetworkMappingValidator) clusterNADInfo(clusterName string) (map[string]ramen.NetworkAttachment, error) {
	// AllDRPolicyAnnotation is used by the MCV watch to re-trigger DRPolicy reconciliation
	// when the MCV result changes — consistent with how peerclass discovery uses MCVs.
	annotations := map[string]string{AllDRPolicyAnnotation: clusterName}

	drcc, err := v.mcvGetter.GetDRClusterConfigFromManagedCluster(clusterName, annotations)
	if err != nil {
		return nil, fmt.Errorf("ManagedClusterView for DRClusterConfig %q: %w", clusterName, err)
	}

	nads := make(map[string]ramen.NetworkAttachment, len(drcc.Status.NetworkAttachments))
	for _, na := range drcc.Status.NetworkAttachments {
		nads[na.Namespace+"/"+na.Name] = na
	}

	return nads, nil
}

// updateNetworkPeers rebuilds DRPolicy.Status.NetworkPeers from the NAD inventory
// on both clusters.  It follows the same pattern as PeerClass population for
// StorageClasses: intersect the two cluster inventories and record each common entry.
//
// An entry appears in NetworkPeers when the NAD is present on BOTH clusters.
// NADs present on only one cluster are captured by the NetworkAttachmentsValidated
// condition (NADsMissing) but are not listed here.
func (v *NetworkMappingValidator) updateNetworkPeers(
	drPolicy *ramen.DRPolicy,
	clusterA, clusterB string,
	nadsA, nadsB map[string]ramen.NetworkAttachment,
) {
	var peers []ramen.NetworkPeer

	for key, naA := range nadsA {
		naB, onB := nadsB[key]
		if !onB {
			continue // missing on clusterB — surfaced via condition, not NetworkPeers
		}

		peer := ramen.NetworkPeer{
			NADName:      naA.Name,
			NADNamespace: naA.Namespace,
			ClusterCNITypes: map[string]string{
				clusterA: naA.CNIType,
				clusterB: naB.CNIType,
			},
		}

		peers = append(peers, peer)
	}

	sort.Slice(peers, func(i, j int) bool {
		if peers[i].NADNamespace != peers[j].NADNamespace {
			return peers[i].NADNamespace < peers[j].NADNamespace
		}

		return peers[i].NADName < peers[j].NADName
	})

	drPolicy.Status.NetworkPeers = peers
}

// buildMissingNADsMessage formats a human-readable condition message listing each missing NAD.
func buildMissingNADsMessage(missing []NADMissingEntry) string {
	var sb strings.Builder

	sb.WriteString("NADs missing on one or more clusters: ")

	for i, m := range missing {
		if i > 0 {
			sb.WriteString("; ")
		}

		nadID := m.NADName
		if len(m.NADNamespace) == 0 {
			nadID = m.NADNamespace + "/" + m.NADName
		}

		sb.WriteString(nadID)

		if m.MissingOnA {
			sb.WriteString(" missing on " + m.ClusterA)
		}

		if m.MissingOnB {
			if m.MissingOnA {
				sb.WriteString(" and")
			}

			sb.WriteString(" missing on " + m.ClusterB)
		}
	}

	return sb.String()
}

// clearNADStatus removes the NetworkAttachmentsValidated condition and clears
// NetworkPeers when NetworkMappingRef is absent.  It is a no-op (no API call)
// when neither field is set, so it does not cause unnecessary reconcile loops.
// func (v *NetworkMappingValidator) clearNADStatus(ctx context.Context, drPolicy *ramen.DRPolicy) error {
func clearNADStatus(u *drpolicyUpdater) error {
	drPolicy := u.object
	nadConditionIdx := -1

	for i, c := range u.object.Status.Conditions {
		if c.Type == ConditionNetworkAttachmentsValidated {
			nadConditionIdx = i

			break
		}
	}

	hasCondition := nadConditionIdx != -1
	hasPeers := len(drPolicy.Status.NetworkPeers) > 0

	if !hasCondition && !hasPeers {
		return nil // nothing stale — skip the API call
	}

	if hasCondition {
		drPolicy.Status.Conditions = append(
			drPolicy.Status.Conditions[:nadConditionIdx],
			drPolicy.Status.Conditions[nadConditionIdx+1:]...,
		)
	}

	u.object.Status.NetworkPeers = nil

	if err := u.client.Status().Update(u.ctx, drPolicy); err != nil {
		return fmt.Errorf("clearing stale NAD status for %s: %w", u.object.Name, err)
	}

	return nil
}
