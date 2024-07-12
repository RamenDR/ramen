// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/time/rate"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/util"
)

const (
	drCConfigFinalizerName = "drclusterconfigs.ramendr.openshift.io/ramen"

	maxReconcileBackoff = 5 * time.Minute
)

// DRClusterConfigReconciler reconciles a DRClusterConfig object
type DRClusterConfigReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Log         logr.Logger
	RateLimiter *workqueue.RateLimiter
}

//nolint:lll
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drclusterconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drclusterconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drclusterconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=snapshot.storage.k8s.io,resources=volumesnapshotclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=replication.storage.openshift.io,resources=volumereplicationclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=clusterclaims,verbs=get;list;watch;create;update;delete

func (r *DRClusterConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("name", req.NamespacedName.Name, "rid", uuid.New())
	log.Info("reconcile enter")

	defer log.Info("reconcile exit")

	drCConfig := &ramen.DRClusterConfig{}
	if err := r.Client.Get(ctx, req.NamespacedName, drCConfig); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(fmt.Errorf("get: %w", err))
	}

	if util.ResourceIsDeleted(drCConfig) {
		return r.processDeletion(ctx, log, drCConfig)
	}

	return r.processCreateOrUpdate(ctx, log, drCConfig)
}

func (r *DRClusterConfigReconciler) processDeletion(
	ctx context.Context,
	log logr.Logger,
	drCConfig *ramen.DRClusterConfig,
) (ctrl.Result, error) {
	if err := util.NewResourceUpdater(drCConfig).
		RemoveFinalizer(drCConfigFinalizerName).
		Update(ctx, r.Client); err != nil {
		log.Info("Failed to remove finalizer for DRClusterConfig resource", "error", err)

		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
}

func (r *DRClusterConfigReconciler) processCreateOrUpdate(
	ctx context.Context,
	log logr.Logger,
	drCConfig *ramen.DRClusterConfig,
) (ctrl.Result, error) {
	if err := util.NewResourceUpdater(drCConfig).
		AddFinalizer(drCConfigFinalizerName).
		Update(ctx, r.Client); err != nil {
		log.Info("Failed to add finalizer for DRClusterConfig resource", "error", err)

		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DRClusterConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	controller := ctrl.NewControllerManagedBy(mgr)

	rateLimiter := workqueue.NewMaxOfRateLimiter(
		workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, maxReconcileBackoff),
		// defaults from client-go
		//nolint: gomnd
		&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(10), 100)},
	)

	if r.RateLimiter != nil {
		rateLimiter = *r.RateLimiter
	}

	return controller.WithOptions(ctrlcontroller.Options{
		RateLimiter: rateLimiter,
	}).For(&ramen.DRClusterConfig{}).Complete(r)
}
