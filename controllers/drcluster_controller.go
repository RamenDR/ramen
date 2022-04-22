/*
Copyright 2022 The RamenDR authors.

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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/go-logr/logr"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/util"
)

// DRClusterReconciler reconciles a DRCluster object
type DRClusterReconciler struct {
	client.Client
	APIReader         client.Reader
	Scheme            *runtime.Scheme
	ObjectStoreGetter ObjectStoreGetter
}

//nolint:lll
//+kubebuilder:rbac:groups=ramendr.openshift.io,resources=drclusters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ramendr.openshift.io,resources=drclusters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ramendr.openshift.io,resources=drclusters/finalizers,verbs=update
//+kubebuilder:rbac:groups=work.open-cluster-management.io,resources=manifestworks,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the DRCluster object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.9.2/pkg/reconcile
func (r *DRClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.Log.WithName("controllers").WithName("drcluster").WithValues("name", req.NamespacedName.Name)
	log.Info("reconcile enter")

	defer log.Info("reconcile exit")

	drcluster := &ramen.DRCluster{}
	if err := r.Client.Get(ctx, req.NamespacedName, drcluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(fmt.Errorf("get: %w", err))
	}

	u := &drclusterUpdater{ctx, drcluster, r.Client, log}

	_, ramenConfig, err := ConfigMapGet(ctx, r.APIReader)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("config map get: %w", u.validatedSetFalse("ConfigMapGetFailed", err))
	}

	manifestWorkUtil := util.MWUtil{Client: r.Client, Ctx: ctx, Log: log, InstName: "", InstNamespace: ""}

	// DRCluster is marked for deletion
	if !drcluster.ObjectMeta.DeletionTimestamp.IsZero() {
		log.Info("delete")

		// Undeploy manifests
		if err := drClusterUndeploy(drcluster, &manifestWorkUtil); err != nil {
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

	reason, err := validateDRCluster(ctx, drcluster, r.APIReader, r.ObjectStoreGetter, req.NamespacedName.String(), log)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("validate: %w", u.validatedSetFalse(reason, err))
	}

	if err := drClusterDeploy(drcluster, &manifestWorkUtil, ramenConfig); err != nil {
		return ctrl.Result{}, fmt.Errorf("drclusters deploy: %w", u.validatedSetFalse("DrClustersDeployFailed", err))
	}

	if err := u.clusterFenceHandle(); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to handle cluster fencing: %w",
			u.validatedSetFalse("FencingHandlingFailed", err))
	}

	// TODO: Setup views for storage class and VRClass to read and report IDs

	return ctrl.Result{}, u.validatedSetTrue("Succeeded", "drcluster validated")
}

func validateDRCluster(ctx context.Context, drcluster *ramen.DRCluster, apiReader client.Reader,
	objectStoreGetter ObjectStoreGetter, listKeyPrefix string,
	log logr.Logger,
) (string, error) {
	reason, err := validateS3Profile(ctx, apiReader, objectStoreGetter, drcluster, listKeyPrefix, log)
	if err != nil {
		return reason, err
	}

	err = validateCIDRsFormat(drcluster, log)
	if err != nil {
		return ReasonValidationFailed, err
	}

	// TODO: Validate managedCluster name? and also ensure it is not deleted!

	return "", nil
}

func validateS3Profile(ctx context.Context, apiReader client.Reader,
	objectStoreGetter ObjectStoreGetter,
	drcluster *ramen.DRCluster, listKeyPrefix string, log logr.Logger) (string, error) {
	if drcluster.Spec.ClusterFence == ramen.ClusterFenceStateFenced ||
		drcluster.Spec.ClusterFence == ramen.ClusterFenceStateManuallyFenced {
		return "", nil
	}

	if reason, err := s3ProfileValidate(ctx, apiReader, objectStoreGetter,
		drcluster.Spec.S3ProfileName, listKeyPrefix, log); err != nil {
		return reason, err
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

func validateCIDRsFormat(drcluster *ramen.DRCluster, log logr.Logger) error {
	// validate the CIDRs format
	invalidCidrs := []string{}

	for i := range drcluster.Spec.CIDRs {
		if _, _, err := net.ParseCIDR(drcluster.Spec.CIDRs[i]); err != nil {
			invalidCidrs = append(invalidCidrs, drcluster.Spec.CIDRs[i])

			log.Error(err, ReasonValidationFailed)
		}
	}

	if len(invalidCidrs) > 0 {
		return fmt.Errorf("invalid CIDRs specified %s", strings.Join(invalidCidrs, ", "))
	}

	return nil
}

type drclusterUpdater struct {
	ctx    context.Context
	object *ramen.DRCluster
	client client.Client
	log    logr.Logger
}

func (u *drclusterUpdater) validatedSetTrue(reason, message string) error {
	return u.statusConditionSet(ramen.DRClusterValidated, metav1.ConditionTrue, reason, message)
}

func (u *drclusterUpdater) validatedSetFalse(reason string, err error) error {
	if err1 := u.statusConditionSet(ramen.DRClusterValidated, metav1.ConditionFalse, reason, err.Error()); err1 != nil {
		return err1
	}

	return err
}

func (u *drclusterUpdater) statusConditionSet(
	conditionType string,
	status metav1.ConditionStatus,
	reason, message string,
) error {
	conditions := &u.object.Status.Conditions

	if util.GenericStatusConditionSet(u.object, conditions, conditionType, status, reason, message, u.log) {
		return u.statusUpdate()
	}

	return nil
}

func (u *drclusterUpdater) statusUpdate() error {
	return u.client.Status().Update(u.ctx, u.object)
}

const drClusterFinalizerName = "drclusters.ramendr.openshift.io/ramen"

func (u *drclusterUpdater) addLabelsAndFinalizers() error {
	return util.GenericAddLabelsAndFinalizers(u.ctx, u.object, drClusterFinalizerName, u.client, u.log)
}

func (u *drclusterUpdater) finalizerRemove() error {
	return util.GenericFinalizerRemove(u.ctx, u.object, drClusterFinalizerName, u.client, u.log)
}

func (u *drclusterUpdater) clusterFenceHandle() error {
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
	if u.object.Spec.ClusterFence == ramen.ClusterFenceStateUnfenced ||
		u.object.Spec.ClusterFence == ramen.ClusterFenceState("") {
		u.object.Status.Fenced = ramen.ClusterUnfenced
	}

	if u.object.Spec.ClusterFence == ramen.ClusterFenceStateManuallyFenced {
		u.object.Status.Fenced = ramen.ClusterFenced
	}

	if u.object.Spec.ClusterFence == ramen.ClusterFenceStateFenced {
		return fmt.Errorf("currently DRPolicy cant handle ClusterFenceStateFenced")
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DRClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ramen.DRCluster{}).
		Watches(
			&source.Kind{Type: &corev1.ConfigMap{}},
			handler.EnqueueRequestsFromMapFunc(r.drClusterConfigMapMapFunc),
		).
		Complete(r)
}

func (r *DRClusterReconciler) drClusterConfigMapMapFunc(configMap client.Object) []reconcile.Request {
	if configMap.GetName() != HubOperatorConfigMapName || configMap.GetNamespace() != NamespaceName() {
		return []reconcile.Request{}
	}

	drcusters := &ramen.DRClusterList{}
	if err := r.Client.List(context.TODO(), drcusters); err != nil {
		return []reconcile.Request{}
	}

	requests := make([]reconcile.Request, len(drcusters.Items))
	for i, drcluster := range drcusters.Items {
		requests[i].Name = drcluster.GetName()
		requests[i].Namespace = drcluster.GetNamespace()
	}

	return requests
}
