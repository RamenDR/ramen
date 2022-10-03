// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// log is for logging in this package.
var drclusterlog = logf.Log.WithName("drcluster-webhook")

func (r *DRCluster) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

//nolint
//+kubebuilder:webhook:path=/validate-ramendr-openshift-io-v1alpha1-drcluster,mutating=false,failurePolicy=fail,sideEffects=None,groups=ramendr.openshift.io,resources=drclusters,verbs=create;update,versions=v1alpha1,name=vdrcluster.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &DRCluster{}

// ValidateCreate checks
func (r *DRCluster) ValidateCreate() error {
	drclusterlog.Info("validate create", "name", r.Name)

	return r.ValidateDRCluster()
}

// ValidateUpdate checks
func (r *DRCluster) ValidateUpdate(old runtime.Object) error {
	drclusterlog.Info("validate update", "name", r.Name)

	oldDRCluster, ok := old.(*DRCluster)
	if !ok {
		return fmt.Errorf("error casting old DRCluster")
	}

	// check for immutability for Region and S3ProfileName
	if r.Spec.Region != oldDRCluster.Spec.Region {
		return fmt.Errorf("Region cannot be changed")
	}

	if r.Spec.S3ProfileName != oldDRCluster.Spec.S3ProfileName {
		return fmt.Errorf("S3ProfileName cannot be changed")
	}

	return r.ValidateDRCluster()
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *DRCluster) ValidateDelete() error {
	drclusterlog.Info("validate delete", "name", r.Name)

	return nil
}

func (r *DRCluster) ValidateDRCluster() error {
	if r.Spec.Region == "" {
		return fmt.Errorf("Region cannot be empty")
	}

	if r.Spec.S3ProfileName == "" {
		return fmt.Errorf("S3ProfileName cannot be empty")
	}

	// TODO: We can add other validations like validation of CIDRs format

	return nil
}
