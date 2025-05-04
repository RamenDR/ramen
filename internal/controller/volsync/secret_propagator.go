// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package volsync

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/ramendr/ramen/internal/controller/util"
	plrulev1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	cfgpolicyv1 "open-cluster-management.io/config-policy-controller/api/v1"
	policyv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
)

func GetVolSyncPSKSecretNameFromVRGName(vrgName string) string {
	return fmt.Sprintf("%s-vs-secret", vrgName)
}

// Should be run from a hub - assumes the source secret exists on the hub cluster and should be propagated
// to destClusters.
// Creates Policy/PlacementRule/PlacementBinding on the hub in the same namespace as the source secret
func PropagateSecretToClusters(ctx context.Context, k8sClient client.Client, sourceSecret *corev1.Secret,
	ownerObject metav1.Object, destClusters []string, destSecretName, destSecretNamespace string,
	log logr.Logger,
) error {
	sp := newSecretPropagator(ctx, k8sClient, sourceSecret, ownerObject,
		destClusters, destSecretName, destSecretNamespace, log)

	// Needed on hub to propagate the secret to managed clusters
	// 1 - Policy - embedded here will be a configpolicy which contains the secret
	// 2 - PlacementRule - governs which mgd clusters get the secret
	// 3 - PlacementBinding
	if err := sp.reconcileSecretPropagationPolicy(); err != nil {
		return err
	}

	if err := sp.reconcileSecretPropagationPlacementRule(); err != nil {
		return err
	}

	return sp.reconcileSecretPropagationPlacementBinding()
}

// Cleans up policy, placementrule and placementbinding used to replicate the volsync secret (if they exist)
// does not throw an error if they do not exist
func CleanupSecretPropagation(ctx context.Context, k8sClient client.Client,
	ownerObject metav1.Object, log logr.Logger,
) error {
	// For cleanup we don't need sourceSecret, destclusters, etc
	sp := newSecretPropagator(ctx, k8sClient, nil, ownerObject, nil, "", "", log)

	return sp.cleanup()
}

type secretPropagator struct {
	Context              context.Context
	Client               client.Client
	Log                  logr.Logger
	Owner                metav1.Object
	SourceSecret         *corev1.Secret
	DestClusters         []string
	DestSecretName       string
	DestSecretNamespace  string
	PolicyName           string
	PlacementRuleName    string
	PlacementBindingName string
}

const policyNameMaxLength = 62

func newSecretPropagator(ctx context.Context, k8sClient client.Client, sourceSecret *corev1.Secret,
	ownerObject metav1.Object, destClusters []string, destSecretName, destSecretNamespace string,
	log logr.Logger,
) secretPropagator {
	secretPropagationPolicyName := util.GeneratePolicyName(ownerObject.GetName()+"-vs-secret",
		policyNameMaxLength-len(ownerObject.GetNamespace()))
	secretPropagationPolicyPlacementRuleName := secretPropagationPolicyName
	secretPropagationPolicyPlacementBindingName := secretPropagationPolicyName

	logWithValues := log.WithValues("sourceNamespace", ownerObject.GetNamespace(),
		"policyName", secretPropagationPolicyName, "placementRuleName", secretPropagationPolicyPlacementRuleName,
		"placementBindingName", secretPropagationPolicyPlacementBindingName)

	if sourceSecret != nil {
		logWithValues = logWithValues.WithValues("sourceSecretName", sourceSecret.GetName(),
			"destinationClusters", destClusters)
	}

	return secretPropagator{
		Context:              ctx,
		Client:               k8sClient,
		Log:                  logWithValues,
		Owner:                ownerObject,
		SourceSecret:         sourceSecret,
		DestClusters:         destClusters,
		DestSecretName:       destSecretName,
		DestSecretNamespace:  destSecretNamespace,
		PolicyName:           secretPropagationPolicyName,
		PlacementRuleName:    secretPropagationPolicyPlacementRuleName,
		PlacementBindingName: secretPropagationPolicyPlacementBindingName,
	}
}

func (sp *secretPropagator) cleanup() error {
	// clean up placement binding
	placementBinding := &policyv1.PlacementBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sp.PlacementBindingName,
			Namespace: sp.Owner.GetNamespace(),
		},
	}
	if err := sp.deleteIgnoreNotFound(placementBinding); err != nil {
		return err
	}

	// Clean up placement rule
	placementRule := &plrulev1.PlacementRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sp.PlacementRuleName,
			Namespace: sp.Owner.GetNamespace(),
		},
	}
	if err := sp.deleteIgnoreNotFound(placementRule); err != nil {
		return err
	}

	// Clean up policy
	policy := &policyv1.Policy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sp.PolicyName,
			Namespace: sp.Owner.GetNamespace(),
		},
	}
	if err := sp.deleteIgnoreNotFound(policy); err != nil {
		return err
	}

	sp.Log.V(1).Info("Secret propagation policy, pl binding and rule cleanup Complete")

	return nil
}

func (sp *secretPropagator) deleteIgnoreNotFound(obj client.Object) error {
	return client.IgnoreNotFound(sp.Client.Delete(sp.Context, obj))
}

func (sp *secretPropagator) reconcileSecretPropagationPolicy() error {
	embeddedConfigPolicy, err := sp.getEmbeddedConfigPolicy()
	if err != nil {
		return err
	}

	policy := &policyv1.Policy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sp.PolicyName,
			Namespace: sp.Owner.GetNamespace(),
		},
	}

	util.AddLabel(policy, util.CreatedByRamenLabel, "true")

	op, err := ctrlutil.CreateOrUpdate(sp.Context, sp.Client, policy, func() error {
		if err := ctrl.SetControllerReference(sp.Owner, policy, sp.Client.Scheme()); err != nil {
			sp.Log.Error(err, "unable to set controller reference on policy")

			return fmt.Errorf("%w", err)
		}

		util.AddLabel(policy, util.ExcludeFromVeleroBackup, "true")

		policy.Spec = policyv1.PolicySpec{
			Disabled: false,
			PolicyTemplates: []*policyv1.PolicyTemplate{
				{
					ObjectDefinition: runtime.RawExtension{
						Object: embeddedConfigPolicy,
					},
				},
			},
		}

		return nil
	})
	if err != nil {
		sp.Log.Error(err, "Error creating or updating secret propagation policy")

		return fmt.Errorf("error creating or updating secret propagation policy (%w)", err)
	}

	sp.Log.V(1).Info("Secret propagation policy createOrUpdate Complete", "op", op)

	return nil
}

func (sp *secretPropagator) getEmbeddedConfigPolicy() (*cfgpolicyv1.ConfigurationPolicy, error) {
	secretData := map[string]interface{}{}
	for key := range sp.SourceSecret.Data {
		secretData[key] = fmt.Sprintf("{{hub fromSecret \"%s\" \"%s\" \"%s\" hub}}",
			sp.SourceSecret.GetNamespace(), sp.SourceSecret.GetName(), key)
	}

	// Build Secret as map[string]interface{} as we need to encode data as string for this replacement to work
	secretObjDefinition := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]interface{}{
			"name":      sp.DestSecretName,
			"namespace": sp.DestSecretNamespace,
		},
		"type": "Opaque",
		"data": secretData,
	}

	secretObjDefinitionRaw, err := json.Marshal(secretObjDefinition)
	if err != nil {
		sp.Log.Error(err, "Unable to encode object definition for secret")

		return nil, fmt.Errorf("unable to encode secret (%w)", err)
	}

	embeddedConfigPolicy := &cfgpolicyv1.ConfigurationPolicy{
		TypeMeta: metav1.TypeMeta{ // Include type meta so that after converting to RawExtension, apiVersion/Kind is set
			APIVersion: cfgpolicyv1.GroupVersion.String(),
			Kind:       "ConfigurationPolicy",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "rmn-" + sp.DestSecretNamespace + "-" + sp.DestSecretName,
		},
		Spec: &cfgpolicyv1.ConfigurationPolicySpec{
			ObjectTemplates: []*cfgpolicyv1.ObjectTemplate{
				{
					ComplianceType: cfgpolicyv1.MustHave,
					ObjectDefinition: runtime.RawExtension{
						Raw: secretObjDefinitionRaw,
					},
				},
			},
			RemediationAction: cfgpolicyv1.Enforce,
			Severity:          "low",
		},
	}

	return embeddedConfigPolicy, nil
}

func (sp *secretPropagator) reconcileSecretPropagationPlacementRule() error {
	placementRule := &plrulev1.PlacementRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sp.PlacementRuleName,
			Namespace: sp.Owner.GetNamespace(),
		},
	}

	util.AddLabel(placementRule, util.CreatedByRamenLabel, "true")

	clustersToApply := []plrulev1.GenericClusterReference{}
	for _, clusterName := range sp.DestClusters {
		clustersToApply = append(clustersToApply, plrulev1.GenericClusterReference{Name: clusterName})
	}

	op, err := ctrlutil.CreateOrUpdate(sp.Context, sp.Client, placementRule, func() error {
		if err := ctrl.SetControllerReference(sp.Owner, placementRule, sp.Client.Scheme()); err != nil {
			sp.Log.Error(err, "unable to set controller reference")

			return fmt.Errorf("%w", err)
		}

		placementRule.Spec = plrulev1.PlacementRuleSpec{
			ClusterConditions: []plrulev1.ClusterConditionFilter{
				{
					Status: metav1.ConditionTrue,
					Type:   "ManagedClusterConditionAvailable",
				},
			},
			GenericPlacementFields: plrulev1.GenericPlacementFields{
				Clusters: clustersToApply,
			},
		}

		return nil
	})
	if err != nil {
		sp.Log.Error(err, "Error creating or updating secret propagation placement rule")

		return fmt.Errorf("error creating or updating secret propagation placement rule (%w)", err)
	}

	sp.Log.V(1).Info("Secret propagation policy placementrule createOrUpdate Complete", "op", op)

	return nil
}

func (sp *secretPropagator) reconcileSecretPropagationPlacementBinding() error {
	placementBinding := &policyv1.PlacementBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sp.PlacementBindingName,
			Namespace: sp.Owner.GetNamespace(),
		},
	}

	util.AddLabel(placementBinding, util.CreatedByRamenLabel, "true")

	op, err := ctrlutil.CreateOrUpdate(sp.Context, sp.Client, placementBinding, func() error {
		if err := ctrl.SetControllerReference(sp.Owner, placementBinding, sp.Client.Scheme()); err != nil {
			sp.Log.Error(err, "unable to set controller reference")

			return fmt.Errorf("%w", err)
		}

		placementBinding.PlacementRef = policyv1.PlacementSubject{
			APIGroup: "apps.open-cluster-management.io",
			Kind:     "PlacementRule",
			Name:     sp.PlacementRuleName,
		}

		placementBinding.Subjects = []policyv1.Subject{
			{
				APIGroup: "policy.open-cluster-management.io",
				Kind:     "Policy",
				Name:     sp.PolicyName,
			},
		}

		return nil
	})
	if err != nil {
		sp.Log.Error(err, "Error creating or updating secret propagation placement binding")

		return fmt.Errorf("error creating or updating secret propagation placement binding (%w)", err)
	}

	sp.Log.V(1).Info("Secret propagation policy placementbinding createOrUpdate Complete", "op", op)

	return nil
}
