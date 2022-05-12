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

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/util"
)

// DRPolicyReconciler reconciles a DRPolicy object
type DRPolicyReconciler struct {
	client.Client
	APIReader         client.Reader
	Scheme            *runtime.Scheme
	ObjectStoreGetter ObjectStoreGetter
}

// ReasonValidationFailed is set when the DRPolicy could not be validated or is not valid
const ReasonValidationFailed = "ValidationFailed"

//nolint:lll
//+kubebuilder:rbac:groups=ramendr.openshift.io,resources=drpolicies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ramendr.openshift.io,resources=drpolicies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ramendr.openshift.io,resources=drpolicies/finalizers,verbs=update
// +kubebuilder:rbac:groups=work.open-cluster-management.io,resources=manifestworks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=list;watch
// +kubebuilder:rbac:groups="policy.open-cluster-management.io",resources=placementbindings,verbs=list;watch
// +kubebuilder:rbac:groups="policy.open-cluster-management.io",resources=policies,verbs=list;watch
// +kubebuilder:rbac:groups="",namespace=system,resources=secrets,verbs=get;update
// +kubebuilder:rbac:groups="policy.open-cluster-management.io",namespace=system,resources=placementbindings,verbs=get;create;update;delete
// +kubebuilder:rbac:groups="policy.open-cluster-management.io",namespace=system,resources=policies,verbs=get;create;update;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the DRPolicy object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.9.2/pkg/reconcile
func (r *DRPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.Log.WithName("controllers").WithName("drpolicy").WithValues("name", req.NamespacedName.Name)
	log.Info("reconcile enter")

	defer log.Info("reconcile exit")

	drpolicy := &ramen.DRPolicy{}
	if err := r.Client.Get(ctx, req.NamespacedName, drpolicy); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(fmt.Errorf("get: %w", err))
	}

	u := &drpolicyUpdater{ctx, drpolicy, r.Client, log}

	_, ramenConfig, err := ConfigMapGet(ctx, r.APIReader)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("config map get: %w", u.validatedSetFalse("ConfigMapGetFailed", err))
	}

	drclusters := &ramen.DRClusterList{}

	// TODO: Is this namespaced listing?
	if err := r.Client.List(ctx, drclusters); err != nil {
		return ctrl.Result{}, fmt.Errorf("drclusters list: %w", u.validatedSetFalse("drClusterListFailed", err))
	}

	secretsUtil := &util.SecretsUtil{Client: r.Client, Ctx: ctx, Log: log}
	// DRPolicy is marked for deletion
	if !drpolicy.ObjectMeta.DeletionTimestamp.IsZero() &&
		controllerutil.ContainsFinalizer(drpolicy, drPolicyFinalizerName) {
		return ctrl.Result{}, u.deleteDRPolicy(drclusters, secretsUtil, ramenConfig)
	}

	log.Info("create/update")

	reason, err := validateDRPolicy(ctx, drpolicy, drclusters, r.APIReader)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("validate: %w", u.validatedSetFalse(reason, err))
	}

	if err := u.addLabelsAndFinalizers(); err != nil {
		return ctrl.Result{}, fmt.Errorf("finalizer add update: %w", u.validatedSetFalse("FinalizerAddFailed", err))
	}

	if err := drPolicyDeploy(drpolicy, drclusters, secretsUtil, ramenConfig); err != nil {
		return ctrl.Result{}, fmt.Errorf("drpolicy deploy: %w", u.validatedSetFalse("DrClustersDeployFailed", err))
	}

	return ctrl.Result{}, u.validatedSetTrue("Succeeded", "drpolicy validated")
}

func validateDRPolicy(ctx context.Context,
	drpolicy *ramen.DRPolicy,
	drclusters *ramen.DRClusterList,
	apiReader client.Reader) (string, error) {
	// TODO: Ensure DRClusters exist and are validated? Also ensure they are not in a deleted state!?
	// If new DRPolicy and clusters are deleted, then fail reconciliation?
	found := 0

	for _, specCluster := range drpolicy.Spec.DRClusters {
		for _, cluster := range drclusters.Items {
			if cluster.Name == specCluster {
				found++
			}
		}
	}

	if found != len(drpolicy.Spec.DRClusters) {
		return ReasonValidationFailed, fmt.Errorf("failed to find DRClusters specified in policy (%v)",
			drpolicy.Spec.DRClusters)
	}

	err := validatePolicyConflicts(ctx, apiReader, drpolicy, drclusters)
	if err != nil {
		return ReasonValidationFailed, err
	}

	return "", nil
}

func validatePolicyConflicts(ctx context.Context,
	apiReader client.Reader,
	drpolicy *ramen.DRPolicy,
	drclusters *ramen.DRClusterList) error {
	drpolicies, err := util.GetAllDRPolicies(ctx, apiReader)
	if err != nil {
		return fmt.Errorf("validate managed cluster in drpolicy %v failed: %w", drpolicy.Name, err)
	}

	err = hasConflictingDRPolicy(drpolicy, drclusters, drpolicies)
	if err != nil {
		return fmt.Errorf("validate managed cluster in drpolicy failed: %w", err)
	}

	return nil
}

// If two drpolicies have common managed cluster(s) and at least one of them is
// a metro supported drpolicy, then fail.
func hasConflictingDRPolicy(match *ramen.DRPolicy, drclusters *ramen.DRClusterList, list ramen.DRPolicyList) error {
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

		// None of the common managed clusters should belong to Metro Regions in either of the drpolicies.
		if haveOverlappingMetroZones(match, drp, drclusters) {
			return fmt.Errorf("drpolicy: %v has overlapping metro region with another drpolicy %v", match.Name, drp.Name)
		}
	}

	return nil
}

func haveOverlappingMetroZones(d1 *ramen.DRPolicy, d2 *ramen.DRPolicy, drclusters *ramen.DRClusterList) bool {
	d1ClusterNames := sets.NewString(util.DrpolicyClusterNames(d1)...)
	d1SupportsMetro, d1MetroRegions := dRPolicySupportsMetro(d1, drclusters.Items)
	d2ClusterNames := sets.NewString(util.DrpolicyClusterNames(d2)...)
	d2SupportsMetro, d2MetroRegions := dRPolicySupportsMetro(d2, drclusters.Items)
	commonClusters := d1ClusterNames.Intersection(d2ClusterNames)

	// No common managed clusters, so we are good
	if commonClusters.Len() == 0 {
		return false
	}

	// Lets check if the metro clusters in DRPolicy d2 belong to common managed clusters list
	if d2SupportsMetro {
		for _, v := range d2MetroRegions {
			if sets.NewString(v...).HasAny(commonClusters.List()...) {
				return true
			}
		}
	}

	// Lets check if the metro clusters in DRPolicy d1 belong to common managed clusters list
	if d1SupportsMetro {
		for _, v := range d1MetroRegions {
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
	ramenConfig *ramen.RamenConfig) error {
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

	if err := drPolicyUndeploy(u.object, drclusters, secretsUtil, ramenConfig); err != nil {
		return fmt.Errorf("drpolicy undeploy: %w", err)
	}

	if err := u.finalizerRemove(); err != nil {
		return fmt.Errorf("finalizer remove update: %w", err)
	}

	return nil
}

func (u *drpolicyUpdater) validatedSetTrue(reason, message string) error {
	return u.statusConditionSet(ramen.DRPolicyValidated, metav1.ConditionTrue, reason, message)
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
	return util.GenericAddLabelsAndFinalizers(u.ctx, u.object, drPolicyFinalizerName, u.client, u.log)
}

func (u *drpolicyUpdater) finalizerRemove() error {
	return util.GenericFinalizerRemove(u.ctx, u.object, drPolicyFinalizerName, u.client, u.log)
}

// SetupWithManager sets up the controller with the Manager.
func (r *DRPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ramen.DRPolicy{}).
		Watches(
			&source.Kind{Type: &corev1.ConfigMap{}},
			handler.EnqueueRequestsFromMapFunc(r.configMapMapFunc),
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{}),
		).
		Watches(
			&source.Kind{Type: &corev1.Secret{}},
			handler.EnqueueRequestsFromMapFunc(r.secretMapFunc),
			builder.WithPredicates(createOrDeleteOrResourceVersionUpdatePredicate{}),
		).
		Complete(r)
}

func (r *DRPolicyReconciler) configMapMapFunc(configMap client.Object) []reconcile.Request {
	if configMap.GetName() != HubOperatorConfigMapName || configMap.GetNamespace() != NamespaceName() {
		return []reconcile.Request{}
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

func (r *DRPolicyReconciler) secretMapFunc(secret client.Object) []reconcile.Request {
	if secret.GetNamespace() != NamespaceName() {
		return []reconcile.Request{}
	}

	drpolicies := &ramen.DRPolicyList{}
	if err := r.Client.List(context.TODO(), drpolicies); err != nil {
		return []reconcile.Request{}
	}

	// TODO: Add optimzation to only reconcile polocies that refer to the changed secret
	requests := make([]reconcile.Request, len(drpolicies.Items))
	for i, drpolicy := range drpolicies.Items {
		requests[i].Name = drpolicy.GetName()
	}

	return requests
}

// TODO: Add a watcher for DRCluster changes (possibly its s3store value changes)
