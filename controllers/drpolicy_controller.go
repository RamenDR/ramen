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

	manifestWorkUtil := util.MWUtil{Client: r.Client, Ctx: ctx, Log: log, InstName: "", InstNamespace: ""}

	switch drpolicy.ObjectMeta.DeletionTimestamp.IsZero() {
	case true:
		log.Info("create/update")

		b := &bar{ctx, drpolicy, r.Client, log}

		listKeyPrefix := req.NamespacedName.String()
		if reason, err := validate(ctx, drpolicy, r.APIReader, r.ObjectStoreGetter, listKeyPrefix); err != nil {
			return ctrl.Result{}, fmt.Errorf("validate: %w", b.validatedSetFalse(reason, err))
		}

		if err := finalizerAdd(ctx, drpolicy, r.Client, log); err != nil {
			return ctrl.Result{}, fmt.Errorf("finalizer add update: %w",
				b.validatedSetFalse("FinalizerAddFailed", err))
		}

		if err := manifestWorkUtil.ClusterRolesCreate(drpolicy); err != nil {
			return ctrl.Result{}, fmt.Errorf("cluster roles create: %w",
				b.validatedSetFalse("ClusterRolesCreateFailed", err))
		}

		return ctrl.Result{}, b.validatedSetTrue("Succeeded", "drpolicy validated")
	default:
		log.Info("delete")

		if err := manifestWorkUtil.ClusterRolesDelete(drpolicy); err != nil {
			return ctrl.Result{}, fmt.Errorf("cluster roles delete: %w", err)
		}

		if err := finalizerRemove(ctx, drpolicy, r.Client, log); err != nil {
			return ctrl.Result{}, fmt.Errorf("finalizer remove update: %w", err)
		}
	}

	return ctrl.Result{}, nil
}

type bar struct {
	ctx      context.Context
	drpolicy *ramen.DRPolicy
	client   client.Client
	log      logr.Logger
}

func (b *bar) validatedSetTrue(reason, message string) error {
	return b.validatedSet(
		func(condition *metav1.Condition) error {
			b.log.Info("valid -> valid")

			return nil
		},
		func(condition *metav1.Condition) error {
			b.log.Info("invalid -> valid")

			return b.statusConditionUpdate(condition, metav1.ConditionTrue, reason, message)
		},
		func() error {
			b.log.Info("empty -> valid")

			return b.statusConditionAppend(ramen.DRPolicyValidated, metav1.ConditionTrue, reason, message)
		},
	)
}

func (b *bar) validatedSetFalse(reason string, err error) error {
	return b.validatedSet(
		func(condition *metav1.Condition) error {
			b.log.Info("valid -> invalid")

			return error1stUnless2ndNotNil(
				err,
				b.statusConditionUpdate(condition, metav1.ConditionTrue, reason, err.Error()),
			)
		},
		func(condition *metav1.Condition) error {
			return b.statusConditionUpdateIfReasonOrMessageDiffers(condition, reason, err, "invalid -> invalid")
		},
		func() error {
			message := err.Error()
			b.log.Info("empty -> invalid", "reason", reason, "message", message)

			return error1stUnless2ndNotNil(
				err,
				b.statusConditionAppend(ramen.DRPolicyValidated, metav1.ConditionFalse, reason, message),
			)
		},
	)
}

func (b *bar) validatedSet(foo1, foo2 func(*metav1.Condition) error, foo3 func() error) error {
	if condition := util.DrpolicyValidatedConditionGet(b.drpolicy); condition != nil {
		if condition.Status == metav1.ConditionTrue {
			return foo1(condition)
		}

		return foo2(condition)
	}

	return foo3()
}

func validate(ctx context.Context, drpolicy *ramen.DRPolicy, apiReader client.Reader,
	objectStoreGetter ObjectStoreGetter, listKeyPrefix string,
) (string, error) {
	for i := range drpolicy.Spec.DRClusterSet {
		cluster := drpolicy.Spec.DRClusterSet[i]
		if reason, err := validateS3Profile(ctx, apiReader, objectStoreGetter,
			cluster.S3ProfileName, listKeyPrefix); err != nil {
			return reason, err
		}
	}

	return "", nil
}

func (b *bar) statusConditionUpdateIfReasonOrMessageDiffers(
	condition *metav1.Condition,
	reason string,
	err error,
	logMessage string,
) error {
	message := err.Error()
	if condition.Reason == reason && condition.Message == message {
		b.log.Info(logMessage, "reason", reason, "message", message)

		return err
	}

	b.log.Info(logMessage,
		"old reason", condition.Reason,
		"new reason", reason,
		"old message", condition.Message,
		"new message", message,
	)

	return error1stUnless2ndNotNil(
		err,
		b.statusConditionUpdate(condition, condition.Status, reason, message),
	)
}

func (b *bar) statusConditionUpdate(
	condition *metav1.Condition,
	status metav1.ConditionStatus, reason, message string,
) error {
	util.ConditionUpdate(b.drpolicy, condition, status, reason, message)

	return b.client.Status().Update(b.ctx, b.drpolicy)
}

func (b *bar) statusConditionAppend(
	conditionType string,
	status metav1.ConditionStatus, reason, message string,
) error {
	util.ConditionAppend(b.drpolicy, &b.drpolicy.Status.Conditions, conditionType, status, reason, message)

	return b.client.Status().Update(b.ctx, b.drpolicy)
}

func error1stUnless2ndNotNil(a, b error) error {
	if b != nil {
		return b
	}

	return a
}

func validateS3Profile(ctx context.Context, apiReader client.Reader,
	objectStoreGetter ObjectStoreGetter, s3ProfileName, listKeyPrefix string,
) (string, error) {
	objectStore, err := objectStoreGetter.ObjectStore(ctx, apiReader, s3ProfileName, `drpolicy validation`)
	if err != nil {
		return `s3ConnectionFailed`, fmt.Errorf(`%s: %w`, s3ProfileName, err)
	}

	if _, err := objectStore.ListKeys(listKeyPrefix); err != nil {
		return `s3ListFailed`, fmt.Errorf(`%s: %w`, s3ProfileName, err)
	}

	return "", nil
}

const finalizerName = "drpolicies.ramendr.openshift.io/ramen"

func finalizerAdd(ctx context.Context, drpolicy *ramen.DRPolicy, client client.Client, log logr.Logger) error {
	finalizerCount := len(drpolicy.ObjectMeta.Finalizers)
	controllerutil.AddFinalizer(drpolicy, finalizerName)

	if len(drpolicy.ObjectMeta.Finalizers) != finalizerCount {
		log.Info("finalizer add")

		return client.Update(ctx, drpolicy)
	}

	return nil
}

func finalizerRemove(ctx context.Context, drpolicy *ramen.DRPolicy, client client.Client, log logr.Logger) error {
	finalizerCount := len(drpolicy.ObjectMeta.Finalizers)
	controllerutil.RemoveFinalizer(drpolicy, finalizerName)

	if len(drpolicy.ObjectMeta.Finalizers) != finalizerCount {
		log.Info("finalizer remove")

		return client.Update(ctx, drpolicy)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DRPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ramen.DRPolicy{}).
		Complete(r)
}
