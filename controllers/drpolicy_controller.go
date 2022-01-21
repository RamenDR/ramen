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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

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

//nolint:lll
//+kubebuilder:rbac:groups=ramendr.openshift.io,resources=drpolicies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ramendr.openshift.io,resources=drpolicies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ramendr.openshift.io,resources=drpolicies/finalizers,verbs=update
// +kubebuilder:rbac:groups=work.open-cluster-management.io,resources=manifestworks,verbs=get;list;watch;create;update;patch;delete

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

	ramenConfig, err := ReadRamenConfig(log)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ramen config read: %w", u.validatedSetFalse("RamenConfigReadFailed", err))
	}

	manifestWorkUtil := util.MWUtil{Client: r.Client, Ctx: ctx, Log: log, InstName: "", InstNamespace: ""}

	switch drpolicy.ObjectMeta.DeletionTimestamp.IsZero() {
	case true:
		log.Info("create/update")

		if err := u.finalizerAdd(); err != nil {
			return ctrl.Result{}, fmt.Errorf("finalizer add update: %w", u.validatedSetFalse("FinalizerAddFailed", err))
		}

		reason, err := validateDRPolicy(ctx, drpolicy, r.APIReader, r.ObjectStoreGetter, req.NamespacedName.String(), log)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("validate: %w", u.validatedSetFalse(reason, err))
		}

		// TODO: New condition type is needed for clusters deploy and fencing
		// handled after this function.
		if err := drClustersDeploy(drpolicy, &manifestWorkUtil, &ramenConfig); err != nil {
			return ctrl.Result{}, fmt.Errorf("drclusters deploy: %w", u.validatedSetFalse("DrClustersDeployFailed", err))
		}

		if err := u.clusterFenceHandle(); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to handle cluster fencing: %w",
				u.validatedSetFalse("FencingHandlingFailed", err))
		}

		return ctrl.Result{}, u.validatedSetTrue("Succeeded", "drpolicy validated")
	default:
		log.Info("delete")

		if err := drClustersUndeploy(drpolicy, &manifestWorkUtil); err != nil {
			return ctrl.Result{}, fmt.Errorf("drclusters undeploy: %w", err)
		}

		if err := u.finalizerRemove(); err != nil {
			return ctrl.Result{}, fmt.Errorf("finalizer remove update: %w", err)
		}
	}

	return ctrl.Result{}, nil
}

func validateDRPolicy(ctx context.Context, drpolicy *ramen.DRPolicy, apiReader client.Reader,
	objectStoreGetter ObjectStoreGetter, listKeyPrefix string,
	log logr.Logger,
) (string, error) {
	reason, err := validateS3Profiles(ctx, apiReader, objectStoreGetter, drpolicy, listKeyPrefix, log)
	if err != nil {
		return reason, err
	}

	return "", nil
}

func validateS3Profiles(ctx context.Context, apiReader client.Reader,
	objectStoreGetter ObjectStoreGetter, drpolicy *ramen.DRPolicy, listKeyPrefix string, log logr.Logger) (string, error) {
	for i := range drpolicy.Spec.DRClusterSet {
		cluster := &drpolicy.Spec.DRClusterSet[i]
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

func (u *objectUpdater) finalizerAdd() error {
	finalizerCount := len(u.object.ObjectMeta.Finalizers)
	controllerutil.AddFinalizer(u.object, finalizerName)

	if len(u.object.ObjectMeta.Finalizers) != finalizerCount {
		u.log.Info("finalizer add")

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
		Complete(r)
}
