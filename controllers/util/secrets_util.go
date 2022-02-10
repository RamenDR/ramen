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

package util

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	errorswrapper "github.com/pkg/errors"
	cpcv1 "github.com/stolostron/config-policy-controller/api/v1"
	gppv1 "github.com/stolostron/governance-policy-propagator/api/v1"
	plrv1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	secretResourcesBaseName    = "ramen-secret"
	secretPolicyBaseName       = secretResourcesBaseName + "-policy"
	secretPlRuleBaseName       = secretResourcesBaseName + "-plrule"
	secretPlBindingBaseName    = secretResourcesBaseName + "-plbinding"
	secretConfigPolicyBaseName = secretResourcesBaseName + "-config-policy"

	secretResourceNameFormat string = "%s-%s"
)

type SecretsUtil struct {
	client.Client
	Ctx context.Context
	Log logr.Logger
}

func newPlacementRuleBinding(
	name, namespace, placementRuleName string,
	subjects []gppv1.Subject) *gppv1.PlacementBinding {
	return &gppv1.PlacementBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		PlacementRef: gppv1.PlacementSubject{
			APIGroup: plrv1.Resource("PlacementRule").Group,
			Kind:     plrv1.Resource("PlacementRule").Resource,
			Name:     placementRuleName,
		},
		Subjects: subjects,
	}
}

func newPlacementRule(name string, namespace string,
	clusters []string) *plrv1.PlacementRule {
	plRuleClusters := []plrv1.GenericClusterReference{}
	for _, clusterRef := range clusters {
		plRuleClusters = append(plRuleClusters, plrv1.GenericClusterReference{
			Name: clusterRef,
		})
	}

	return &plrv1.PlacementRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: plrv1.PlacementRuleSpec{
			GenericPlacementFields: plrv1.GenericPlacementFields{
				Clusters: plRuleClusters,
			},
		},
	}
}

func newS3ConfigurationSecret(s3SecretRef corev1.SecretReference) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s3SecretRef.Name,
			Namespace: s3SecretRef.Namespace,
		},
		Data: map[string][]byte{
			"AWS_ACCESS_KEY_ID": []byte("'{{ fromSecret " +
				"\"" + s3SecretRef.Namespace + "\"" +
				"\"" + s3SecretRef.Name + "\"" +
				"\"AWS_ACCESS_KEY_ID\" }}'"),
			"AWS_SECRET_ACCESS_KEY": []byte("'{{ fromSecret " +
				"\"" + s3SecretRef.Namespace + "\"" +
				"\"" + s3SecretRef.Name + "\"" +
				"\"AWS_SECRET_ACCESS_KEY\" }}'"),
		},
	}
}

func newConfigurationPolicy(name string, object runtime.RawExtension) *cpcv1.ConfigurationPolicy {
	return &cpcv1.ConfigurationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: cpcv1.ConfigurationPolicySpec{
			RemediationAction: cpcv1.Enforce,
			Severity:          "high",
			ObjectTemplates: []*cpcv1.ObjectTemplate{
				{
					ComplianceType:   cpcv1.MustHave,
					ObjectDefinition: object,
				},
			},
		},
	}
}

func newPolicy(name, namespace string, object runtime.RawExtension) *gppv1.Policy {
	return &gppv1.Policy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: gppv1.PolicySpec{
			RemediationAction: gppv1.Enforce,
			Disabled:          false,
			PolicyTemplates: []*gppv1.PolicyTemplate{
				{
					ObjectDefinition: object,
				},
			},
		},
	}
}

func (sutil *SecretsUtil) createPolicyResources(s3Secret, cluster, namespace string) error {
	policyName := fmt.Sprintf(secretResourceNameFormat, secretPolicyBaseName, s3Secret)
	plBindingName := fmt.Sprintf(secretResourceNameFormat, secretPlBindingBaseName, s3Secret)
	plRuleName := fmt.Sprintf(secretResourceNameFormat, secretPlRuleBaseName, s3Secret)
	configPolicyName := fmt.Sprintf(secretResourceNameFormat, secretConfigPolicyBaseName, s3Secret)

	// Create a PlacementBinding for the Policy object and the placement rule
	subjects := []gppv1.Subject{
		{
			Name:     policyName,
			APIGroup: gppv1.SchemeGroupVersion.WithResource("Policy").GroupResource().Group,
			Kind:     gppv1.SchemeGroupVersion.WithResource("Policy").GroupResource().Resource,
		},
	}

	plRuleBindingObject := newPlacementRuleBinding(plBindingName, namespace, plRuleName, subjects)
	if err := sutil.Client.Create(sutil.Ctx, plRuleBindingObject); !errors.IsAlreadyExists(err) {
		sutil.Log.Error(err, "unable to create placement binding", "secret", s3Secret, "cluster", cluster)

		return errorswrapper.Wrap(err, fmt.Sprintf("unable to create placement binding (secret: %s, cluster: %s)",
			s3Secret, cluster))
	}

	// Create a Policy object for the secret
	s3SecretRef := corev1.SecretReference{Name: s3Secret, Namespace: namespace}
	secretObject := newS3ConfigurationSecret(s3SecretRef)
	configObject := newConfigurationPolicy(configPolicyName, runtime.RawExtension{Object: secretObject})

	policyObject := newPolicy(policyName, namespace, runtime.RawExtension{Object: configObject})
	if err := sutil.Client.Create(sutil.Ctx, policyObject); !errors.IsAlreadyExists(err) {
		sutil.Log.Error(err, "unable to create policy", "secret", s3Secret, "cluster", cluster)

		return errorswrapper.Wrap(err, fmt.Sprintf("unable to create policy (secret: %s, cluster: %s)",
			s3Secret, cluster))
	}

	// Create a PlacementRule, including cluster
	plRuleObject := newPlacementRule(plRuleName, namespace, []string{cluster})
	if err := sutil.Client.Create(sutil.Ctx, plRuleObject); !errors.IsAlreadyExists(err) {
		sutil.Log.Error(err, "unable to create placement rule", "secret", s3Secret, "cluster", cluster)

		return errorswrapper.Wrap(err, fmt.Sprintf("unable to create placement rule (secret: %s, cluster: %s)",
			s3Secret, cluster))
	}

	return nil
}

func (sutil *SecretsUtil) updatePlacementRule(plRule *plrv1.PlacementRule, cluster string, add bool) error {
	found := false
	survivors := []plrv1.GenericClusterReference{}

	// Check if cluster is already part of placement rule
	for _, plCluster := range plRule.Spec.Clusters {
		if plCluster.Name == cluster {
			found = true

			if add {
				break
			}

			continue
		}
		// Build a potential surviving cluster list
		survivors = append(survivors, plCluster)
	}

	switch add {
	case true:
		if found {
			return nil
		}

		plRule.Spec.Clusters = append(plRule.Spec.Clusters, plrv1.GenericClusterReference{Name: cluster})
	case false:
		if !found {
			return nil
		}

		plRule.Spec.Clusters = survivors
	}

	err := sutil.Client.Update(sutil.Ctx, plRule)
	if err != nil {
		sutil.Log.Error(err, "unable to update placement rule", "placementRule", plRule.Name, "cluster", cluster)

		return errorswrapper.Wrap(err, fmt.Sprintf("unable to update placement rule (placementRule: %s, cluster: %s)",
			plRule.Name, cluster))
	}

	return nil
}

func (sutil *SecretsUtil) AddSecretToCluster(s3Secret, clusterName, namespace string) error {
	sutil.Log.Info("Add Secret", "cluster", clusterName, "s3Secret", s3Secret)

	// TODO: Proceed if secret is found, else error and reconcile later
	plRule := &plrv1.PlacementRule{}
	plRuleName := types.NamespacedName{
		Namespace: namespace,
		Name:      fmt.Sprintf(secretResourceNameFormat, secretPlRuleBaseName, s3Secret),
	}

	// Fetch secret placement rule, create if not found
	err := sutil.Client.Get(sutil.Ctx, plRuleName, plRule)
	if err != nil {
		if !errors.IsNotFound(err) {
			return errorswrapper.Wrap(err, "failed to get placementRule object")
		}

		return sutil.createPolicyResources(s3Secret, clusterName, namespace)
	}

	return sutil.updatePlacementRule(plRule, clusterName, true)
}

func (sutil *SecretsUtil) RemoveSecretFromCluster(s3Secret, clusterName, namespace string) error {
	sutil.Log.Info("Delete Secret", "cluster", clusterName, "s3Secret", s3Secret)

	plRule := &plrv1.PlacementRule{}
	plRuleName := types.NamespacedName{
		Namespace: namespace,
		Name:      fmt.Sprintf(secretResourceNameFormat, secretPlRuleBaseName, s3Secret),
	}

	// Fetch secret placement rule, success if not found
	err := sutil.Client.Get(sutil.Ctx, plRuleName, plRule)
	if err != nil {
		if !errors.IsNotFound(err) {
			return errorswrapper.Wrap(err, "failed to get placementRule object")
		}

		return nil
	}

	return sutil.updatePlacementRule(plRule, clusterName, false)
}
