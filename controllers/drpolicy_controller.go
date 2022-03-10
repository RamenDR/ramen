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
	"net"
	"strings"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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

	u := &objectUpdater{ctx, drpolicy, r.Client, log}

	_, ramenConfig, err := ConfigMapGet(ctx, r.APIReader)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("config map get: %w", u.validatedSetFalse("ConfigMapGetFailed", err))
	}

	manifestWorkUtil := util.MWUtil{Client: r.Client, Ctx: ctx, Log: log, InstName: "", InstNamespace: ""}

	// DRPolicy is marked for deletion
	if !drpolicy.ObjectMeta.DeletionTimestamp.IsZero() {
		log.Info("delete")

		if err := drClustersUndeploy(drpolicy, &manifestWorkUtil, ramenConfig); err != nil {
			return ctrl.Result{}, fmt.Errorf("drclusters undeploy: %w", err)
		}

		if err := u.finalizerRemove(); err != nil {
			return ctrl.Result{}, fmt.Errorf("finalizer remove update: %w", err)
		}

		return ctrl.Result{}, nil
	}

	log.Info("create/update")

	if err := u.addLabelsAndFinalizers(); err != nil {
		return ctrl.Result{}, fmt.Errorf("finalizer add update: %w", u.validatedSetFalse("FinalizerAddFailed", err))
	}

	reason, err := validateDRPolicy(ctx, drpolicy, r.APIReader, r.ObjectStoreGetter, req.NamespacedName.String(), log)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("validate: %w", u.validatedSetFalse(reason, err))
	}

	// TODO: New condition type is needed for clusters deploy and fencing
	// handled after this function.
	if err := drClustersDeploy(drpolicy, &manifestWorkUtil, ramenConfig); err != nil {
		return ctrl.Result{}, fmt.Errorf("drclusters deploy: %w", u.validatedSetFalse("DrClustersDeployFailed", err))
	}

	if err := u.clusterFenceHandle(); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to handle cluster fencing: %w",
			u.validatedSetFalse("FencingHandlingFailed", err))
	}

	return ctrl.Result{}, u.validatedSetTrue("Succeeded", "drpolicy validated")
}

func validateDRPolicy(ctx context.Context, drpolicy *ramen.DRPolicy, apiReader client.Reader,
	objectStoreGetter ObjectStoreGetter, listKeyPrefix string,
	log logr.Logger,
) (string, error) {
	reason, err := validateS3Profiles(ctx, apiReader, objectStoreGetter, drpolicy, listKeyPrefix, log)
	if err != nil {
		return reason, err
	}

	err = validateManagedClusters(ctx, apiReader, drpolicy)
	if err != nil {
		return ReasonValidationFailed, err
	}

	err = validateCIDRsFormat(drpolicy, log)
	if err != nil {
		return ReasonValidationFailed, err
	}

	return "", nil
}

func haveOverlappingMetroZones(d1 *ramen.DRPolicy, d2 *ramen.DRPolicy) bool {
	d1ClusterNames := sets.NewString(util.DrpolicyClusterNames(d1)...)
	d1SupportsMetro, d1MetroRegions := dRPolicySupportsMetro(d1)
	d2ClusterNames := sets.NewString(util.DrpolicyClusterNames(d2)...)
	d2SupportsMetro, d2MetroRegions := dRPolicySupportsMetro(d2)
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

// If two drpolicies have common managed cluster(s) and at least one of them is
// a metro supported drpolicy, then fail.
func hasConflictingDRPolicy(match *ramen.DRPolicy, list ramen.DRPolicyList) error {
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
		if haveOverlappingMetroZones(match, drp) {
			return fmt.Errorf("drpolicy: %v has overlapping metro region with another drpolicy %v", match.Name, drp.Name)
		}
	}

	return nil
}

func validateManagedClusters(ctx context.Context, apiReader client.Reader, drpolicy *ramen.DRPolicy) error {
	drpolicies, err := util.GetAllDRPolicies(ctx, apiReader)
	if err != nil {
		return fmt.Errorf("validate managed cluster in drpolicy %v failed: %w", drpolicy.Name, err)
	}

	err = hasConflictingDRPolicy(drpolicy, drpolicies)
	if err != nil {
		return fmt.Errorf("validate managed cluster in drpolicy failed: %w", err)
	}

	return nil
}

func validateS3Profiles(ctx context.Context, apiReader client.Reader,
	objectStoreGetter ObjectStoreGetter, drpolicy *ramen.DRPolicy, listKeyPrefix string, log logr.Logger) (string, error) {
	for i := range drpolicy.Spec.DRClusterSet {
		cluster := &drpolicy.Spec.DRClusterSet[i]
		if cluster.ClusterFence == ramen.ClusterFenceStateFenced ||
			cluster.ClusterFence == ramen.ClusterFenceStateManuallyFenced {
			continue
		}

		if reason, err := s3ProfileValidate(ctx, apiReader, objectStoreGetter,
			cluster.S3ProfileName, listKeyPrefix, log); err != nil {
			return reason, err
		}
	}

	return "", nil
}

func s3ProfileValidate(ctx context.Context, apiReader client.Reader,
	objectStoreGetter ObjectStoreGetter, s3ProfileName, listKeyPrefix string,
	log logr.Logger,
) (string, error) {
	objectStore, err := objectStoreGetter.ObjectStore(ctx, apiReader, s3ProfileName, "drpolicy validation", log)
	if err != nil {
		return "s3ConnectionFailed", fmt.Errorf("%s: %w", s3ProfileName, err)
	}

	if _, err := objectStore.ListKeys(listKeyPrefix); err != nil {
		return "s3ListFailed", fmt.Errorf("%s: %w", s3ProfileName, err)
	}

	return "", nil
}

func validateCIDRsFormat(drpolicy *ramen.DRPolicy, log logr.Logger) error {
	// validate the CIDRs format
	for i := range drpolicy.Spec.DRClusterSet {
		cluster := &drpolicy.Spec.DRClusterSet[i]
		if err := ParseCIDRs(cluster, log); err != nil {
			return err
		}
	}

	return nil
}

func ParseCIDRs(cluster *ramen.ManagedCluster, log logr.Logger) error {
	invalidCidrs := []string{}

	for i := range cluster.CIDRs {
		if _, _, err := net.ParseCIDR(cluster.CIDRs[i]); err != nil {
			invalidCidrs = append(invalidCidrs, cluster.CIDRs[i])

			log.Error(err, ReasonValidationFailed)
		}
	}

	if len(invalidCidrs) > 0 {
		return fmt.Errorf("cluster %s has invalid CIDRs %s", cluster.Name, strings.Join(invalidCidrs, ", "))
	}

	return nil
}

type objectUpdater struct {
	ctx    context.Context
	object *ramen.DRPolicy
	client client.Client
	log    logr.Logger
}

func (u *objectUpdater) clusterFenceHandle() error {
	if u.object.Status.DRClusters == nil {
		u.object.Status.DRClusters = make(map[string]ramen.ClusterStatus)
	}

	for _, managedCluster := range u.object.Spec.DRClusterSet {
		// TODO:
		// 1) For now by default fenceStatus is ClusterFenceStateUnfenced.
		//    However, we need to handle explicit unfencing operation to unfence
		//    a fenced cluster below, by deleting the fencing CR created by
		//    ramen.
		//
		// 2) How to differentiate between ClusterFenceStateUnfenced being
		//    set because a manually fenced cluster is manually unfenced against the
		//    requirement to unfence a cluster that has been fenced by ramen.
		//
		// 3) Handle Ramen driven fencing here
		if managedCluster.ClusterFence == ramen.ClusterFenceStateUnfenced ||
			managedCluster.ClusterFence == ramen.ClusterFenceState("") {
			clusterStatus := ramen.ClusterStatus{Name: managedCluster.Name}
			clusterStatus.Status = ramen.ClusterUnfenced
			u.object.Status.DRClusters[managedCluster.Name] = clusterStatus
		}

		if managedCluster.ClusterFence == ramen.ClusterFenceStateManuallyFenced {
			clusterStatus := ramen.ClusterStatus{Name: managedCluster.Name}
			clusterStatus.Status = ramen.ClusterFenced
			u.object.Status.DRClusters[managedCluster.Name] = clusterStatus
		}

		if managedCluster.ClusterFence == ramen.ClusterFenceStateFenced {
			return fmt.Errorf("currently DRPolicy cant handle ClusterFenceStateFenced")
		}
	}

	return nil
}

func (u *objectUpdater) validatedSetTrue(reason, message string) error {
	return u.validatedSet(metav1.ConditionTrue, reason, message)
}

func (u *objectUpdater) validatedSetFalse(reason string, err error) error {
	if err1 := u.validatedSet(metav1.ConditionFalse, reason, err.Error()); err1 != nil {
		return err1
	}

	return err
}

func (u *objectUpdater) validatedSet(status metav1.ConditionStatus, reason, message string) error {
	return u.statusConditionSet(ramen.DRPolicyValidated, status, reason, message)
}

func (u *objectUpdater) statusConditionSet(conditionType string, status metav1.ConditionStatus, reason, message string,
) error {
	conditions := &u.object.Status.Conditions
	generation := u.object.GetGeneration()

	if condition := meta.FindStatusCondition(*conditions, conditionType); condition != nil {
		if condition.Status == status &&
			condition.Reason == reason &&
			condition.Message == message &&
			condition.ObservedGeneration == generation {
			u.log.Info("condition unchanged", "type", conditionType,
				"status", status, "reason", reason, "message", message, "generation", generation,
			)

			return nil
		}

		u.log.Info("condition update", "type", conditionType,
			"old status", condition.Status, "new status", status,
			"old reason", condition.Reason, "new reason", reason,
			"old message", condition.Message, "new message", message,
			"old generation", condition.ObservedGeneration, "new generation", generation,
		)
		util.ConditionUpdate(u.object, condition, status, reason, message)
	} else {
		u.log.Info("condition append", "type", conditionType,
			"status", status, "reason", reason, "message", message, "generation", generation,
		)
		util.ConditionAppend(u.object, conditions, conditionType, status, reason, message)
	}

	return u.statusUpdate()
}

func (u *objectUpdater) statusUpdate() error {
	return u.client.Status().Update(u.ctx, u.object)
}

const finalizerName = "drpolicies.ramendr.openshift.io/ramen"

func (u *objectUpdater) addLabelsAndFinalizers() error {
	labelAdded, labels := util.AddLabel(&u.object.ObjectMeta, util.OCMBackupLabelKey, util.OCMBackupLabelValue)
	finalizerAdded := util.AddFinalizer(u.object, finalizerName)

	if finalizerAdded || labelAdded {
		u.log.Info("finalizer or label add")

		if labelAdded {
			u.object.SetLabels(labels)
		}

		return u.client.Update(u.ctx, u.object)
	}

	return nil
}

func (u *objectUpdater) finalizerRemove() error {
	finalizerCount := len(u.object.ObjectMeta.Finalizers)
	controllerutil.RemoveFinalizer(u.object, finalizerName)

	if len(u.object.ObjectMeta.Finalizers) != finalizerCount {
		u.log.Info("finalizer remove")

		return u.client.Update(u.ctx, u.object)
	}

	return nil
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

func (r *DRPolicyReconciler) secretMapFunc(secret client.Object) (requests []reconcile.Request) {
	if secret.GetNamespace() != NamespaceName() {
		return
	}

	drpolicies := &ramen.DRPolicyList{}
	if err := r.Client.List(context.TODO(), drpolicies); err != nil {
		return
	}

	_, ramenConfig, err := ConfigMapGet(context.TODO(), r.APIReader)
	if err != nil {
		return
	}

	for _, drpolicy := range drpolicies.Items {
		for _, drCluster := range drpolicy.Spec.DRClusterSet {
			s3Profile := RamenConfigS3StoreProfilePointerGet(ramenConfig, drCluster.S3ProfileName)
			if s3Profile == nil {
				continue
			}

			if s3Profile.S3SecretRef.Namespace == secret.GetNamespace() &&
				s3Profile.S3SecretRef.Name == secret.GetName() {
				requests = append(requests,
					reconcile.Request{
						NamespacedName: types.NamespacedName{
							Name: drpolicy.GetName(),
						},
					},
				)

				break
			}
		}
	}

	return requests
}
