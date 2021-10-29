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

		listKeyPrefix := req.NamespacedName.String()
		if err := validate(ctx, drpolicy, r.APIReader, r.Client, r.ObjectStoreGetter, log, listKeyPrefix); err != nil {
			return ctrl.Result{}, fmt.Errorf(`validate: %w`, err)
		}

		if err := finalizerAdd(ctx, drpolicy, r.Client, log); err != nil {
			return ctrl.Result{}, fmt.Errorf("finalizer add update: %w", err)
		}

		if err := manifestWorkUtil.ClusterRolesCreate(drpolicy); err != nil {
			return ctrl.Result{}, fmt.Errorf("cluster roles create: %w", err)
		}
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

func validate(ctx context.Context, drpolicy *ramen.DRPolicy, apiReader client.Reader,
	client client.Client, objectStoreGetter ObjectStoreGetter, log logr.Logger, listKeyPrefix string,
) error {
	var (
		conditionSetTrue  func(reason, message string) error
		conditionSetFalse func(reason string, err error) error
	)

	if condition := util.DrpolicyValidatedConditionGet(drpolicy); condition != nil {
		if condition.Status == metav1.ConditionTrue {
			log.Info(`valid -> valid`)

			return nil
		}

		conditionSetFalse = func(reason string, err error) error {
			return statusConditionUpdateIfReasonOrMessageDiffers(ctx, drpolicy, client, condition, reason, err, log,
				"invalid -> invalid",
			)
		}
		conditionSetTrue = func(reason, message string) error {
			log.Info(`invalid -> valid`)

			return statusConditionUpdate(ctx, drpolicy, client, condition, metav1.ConditionTrue, reason, message)
		}
	} else {
		conditionSetFalse = func(reason string, err error) error {
			message := err.Error()
			log.Info("empty -> invalid", "reason", reason, "message", message)
			if err1 := statusConditionAppend(ctx, drpolicy, client, ramen.DRPolicyValidated, metav1.ConditionFalse, reason,
				message,
			); err1 != nil {
				return err1
			}

			return err
		}
		conditionSetTrue = func(reason, message string) error {
			log.Info(`empty -> valid`)

			return statusConditionAppend(ctx, drpolicy, client, ramen.DRPolicyValidated, metav1.ConditionTrue, reason, message)
		}
	}

	for i := range drpolicy.Spec.DRClusterSet {
		cluster := drpolicy.Spec.DRClusterSet[i]
		if reason, err := validateS3Profile(ctx, apiReader, objectStoreGetter,
			cluster.S3ProfileName, listKeyPrefix); err != nil {
			return conditionSetFalse(reason, err)
		}
	}

	return conditionSetTrue(`Succeeded`, `drpolicy validated`)
}

func statusConditionUpdateIfReasonOrMessageDiffers(
	ctx context.Context,
	drpolicy *ramen.DRPolicy,
	client client.Client,
	condition *metav1.Condition,
	reason string,
	err error,
	log logr.Logger,
	logMessage string,
) error {
	message := err.Error()
	if condition.Reason == reason && condition.Message == message {
		log.Info(logMessage, "reason", reason, "message", message)

		return err
	}

	log.Info(logMessage,
		"old reason", condition.Reason,
		"new reason", reason,
		"old message", condition.Message,
		"new message", message,
	)

	if err1 := statusConditionUpdate(ctx, drpolicy, client, condition, condition.Status, reason, message); err1 != nil {
		return err1
	}

	return err
}

func statusConditionUpdate(
	ctx context.Context,
	drpolicy *ramen.DRPolicy,
	client client.Client,
	condition *metav1.Condition,
	status metav1.ConditionStatus, reason, message string,
) error {
	util.ConditionUpdate(drpolicy, condition, status, reason, message)

	return client.Status().Update(ctx, drpolicy)
}

func statusConditionAppend(
	ctx context.Context,
	drpolicy *ramen.DRPolicy,
	client client.Client,
	conditionType string,
	status metav1.ConditionStatus, reason, message string,
) error {
	util.ConditionAppend(drpolicy, &drpolicy.Status.Conditions, conditionType, status, reason, message)

	return client.Status().Update(ctx, drpolicy)
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
