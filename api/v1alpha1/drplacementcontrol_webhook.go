// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"fmt"
	"reflect"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// log is for logging in this package.
var drplacementcontrollog = logf.Log.WithName("drplacementcontrol-resource")

func (r *DRPlacementControl) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

//nolint
//+kubebuilder:webhook:path=/validate-ramendr-openshift-io-v1alpha1-drplacementcontrol,mutating=false,failurePolicy=fail,sideEffects=None,groups=ramendr.openshift.io,resources=drplacementcontrols,verbs=update,versions=v1alpha1,name=vdrplacementcontrol.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &DRPlacementControl{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *DRPlacementControl) ValidateCreate() error {
	drplacementcontrollog.Info("validate create", "name", r.Name)

	return nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *DRPlacementControl) ValidateUpdate(old runtime.Object) error {
	drplacementcontrollog.Info("validate update", "name", r.Name)

	oldDRPC, ok := old.(*DRPlacementControl)
	if !ok {
		return fmt.Errorf("error casting old DRPC")
	}

	// checks for immutability
	if !reflect.DeepEqual(r.Spec.PlacementRef, oldDRPC.Spec.PlacementRef) {
		drplacementcontrollog.Info("detected PlacementRef updates, which is disallowed", "name", r.Name,
			"old", oldDRPC.Spec.PlacementRef,
			"new", r.Spec.PlacementRef)

		return fmt.Errorf("PlacementRef cannot be changed")
	}

	if !reflect.DeepEqual(r.Spec.DRPolicyRef, oldDRPC.Spec.DRPolicyRef) {
		drplacementcontrollog.Info("detected DRPolicyRef updates, which is disallowed", "name", r.Name,
			"old", oldDRPC.Spec.DRPolicyRef,
			"new", r.Spec.DRPolicyRef)

		return fmt.Errorf("DRPolicyRef cannot be changed")
	}

	if !reflect.DeepEqual(r.Spec.PVCSelector, oldDRPC.Spec.PVCSelector) {
		drplacementcontrollog.Info("detected PVCSelector updates, which is disallowed", "name", r.Name,
			"old", oldDRPC.Spec.PVCSelector,
			"new", r.Spec.PVCSelector)

		return fmt.Errorf("PVCSelector cannot be changed")
	}

	return nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *DRPlacementControl) ValidateDelete() error {
	drplacementcontrollog.Info("validate delete", "name", r.Name)

	return nil
}
