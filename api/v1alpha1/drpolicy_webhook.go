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
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// log is for logging in this package.
var drpolicylog = logf.Log.WithName("drpolicy-resource")

func (r *DRPolicy) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

//nolint
//+kubebuilder:webhook:path=/validate-ramendr-openshift-io-v1alpha1-drpolicy,mutating=false,failurePolicy=fail,sideEffects=None,groups=ramendr.openshift.io,resources=drpolicies,verbs=update,versions=v1alpha1,name=vdrpolicy.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &DRPolicy{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *DRPolicy) ValidateCreate() (admission.Warnings, error) {
	drpolicylog.Info("validate create", "name", r.Name)

	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *DRPolicy) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	drpolicylog.Info("validate update", "name", r.Name)

	oldDRPolicy, ok := old.(*DRPolicy)

	if !ok {
		return nil, fmt.Errorf("error casting old DRPolicy")
	}

	// checks for immutability
	if r.Spec.SchedulingInterval != oldDRPolicy.Spec.SchedulingInterval {
		return nil, fmt.Errorf("SchedulingInterval cannot be changed")
	}

	if !reflect.DeepEqual(r.Spec.ReplicationClassSelector, oldDRPolicy.Spec.ReplicationClassSelector) {
		return nil, fmt.Errorf("ReplicationClassSelector cannot be changed")
	}

	if !reflect.DeepEqual(r.Spec.VolumeSnapshotClassSelector, oldDRPolicy.Spec.VolumeSnapshotClassSelector) {
		return nil, fmt.Errorf("VolumeSnapshotClassSelector cannot be changed")
	}

	if !reflect.DeepEqual(r.Spec.DRClusters, oldDRPolicy.Spec.DRClusters) {
		return nil, fmt.Errorf("DRClusters cannot be changed")
	}

	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *DRPolicy) ValidateDelete() (admission.Warnings, error) {
	drpolicylog.Info("validate delete", "name", r.Name)

	return nil, nil
}
