// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/ramendr/ramen/controllers/util"
	plrv1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gppv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
)

var _ = Describe("Secrets_Util", func() {
	const (
		secretsCount  = 3
		clustersCount = 2
	)

	var (
		secretNames = [secretsCount]string{
			"secreta",
			"secretb",
			"0123456789012345678901234567890123456789012345678900000", // 55 chars
		}
		clusterNames                          = [clustersCount]string{"clusterEast", "clusterWest"}
		policyName, plBindingName, plRuleName [secretsCount]string
		secrets                               [secretsCount]*corev1.Secret
		tstNamespace                          = "default" // 7 chars
	)

	BeforeEach(func() {
		for idx := range secretNames {
			policyName[idx], plBindingName[idx], plRuleName[idx], _ = util.GeneratePolicyResourceNames(secretNames[idx])
			secrets[idx] = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretNames[idx],
					Namespace: tstNamespace,
				},
				StringData: map[string]string{
					"AWS_ACCESS_KEY_ID":     "id",
					"AWS_SECRET_ACCESS_KEY": "key",
				},
			}
		}
	})

	plRuleAbsent := func(plRuleName, namespace string) bool {
		plRule := &plrv1.PlacementRule{}

		return errors.IsNotFound(k8sClient.Get(context.TODO(),
			types.NamespacedName{Name: plRuleName, Namespace: namespace},
			plRule))
	}

	plRuleContains := func(plRuleName, namespace string, clusters []string) bool {
		plRule := &plrv1.PlacementRule{}
		if err := k8sClient.Get(
			context.TODO(),
			types.NamespacedName{Name: plRuleName, Namespace: namespace},
			plRule); err != nil {
			return false
		}

		for _, cluster := range clusters {
			found := false
			for _, specCluster := range plRule.Spec.Clusters {
				if specCluster.Name == cluster {
					found = true

					break
				}
			}

			if !found {
				return false
			}
		}

		return true
	}

	policyContains := func(policyName, namespace string, secret *corev1.Secret) bool {
		policyObject := &gppv1.Policy{}
		if err := k8sClient.Get(
			context.TODO(),
			types.NamespacedName{Name: policyName, Namespace: namespace},
			policyObject); err != nil {
			return false
		}

		apiSecret := &corev1.Secret{}
		if err := k8sClient.Get(
			context.TODO(),
			types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace},
			apiSecret); err != nil {
			return false
		}

		for annotation, value := range policyObject.Annotations {
			if annotation == util.PolicyTriggerAnnotation {
				return apiSecret.ResourceVersion == value
			}
		}

		return false
	}

	finalizerPresent := func(secretName string) bool {
		secret := &corev1.Secret{}
		if err := k8sClient.Get(
			context.TODO(),
			types.NamespacedName{
				Name:      secretName,
				Namespace: tstNamespace,
			},
			secret); err != nil {
			return false
		}
		for _, finalizer := range secret.Finalizers {
			if finalizer == util.SecretPolicyFinalizer {
				return true
			}
		}

		return false
	}

	finalizerAbsent := func(secretName string) bool {
		secret := &corev1.Secret{}
		if err := k8sClient.Get(context.TODO(),
			types.NamespacedName{
				Name:      secretName,
				Namespace: tstNamespace,
			},
			secret); err != nil {
			return false
		}
		for _, finalizer := range secret.Finalizers {
			if finalizer == util.SecretPolicyFinalizer {
				return false
			}
		}

		return true
	}

	updateSecret := func(secret *corev1.Secret, value string) bool {
		secretFetched := &corev1.Secret{}
		if err := k8sClient.Get(
			context.TODO(),
			types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace},
			secretFetched); err != nil {
			return false
		}

		secretFetched.StringData = map[string]string{
			"AWS_ACCESS_KEY_ID":     value,
			"AWS_SECRET_ACCESS_KEY": value,
		}

		return k8sClient.Update(context.TODO(), secretFetched) == nil
	}

	secretAbsent := func(secretName string) bool {
		secret := &corev1.Secret{}

		return errors.IsNotFound(k8sClient.Get(
			context.TODO(),
			types.NamespacedName{
				Name:      secretName,
				Namespace: tstNamespace,
			}, secret))
	}

	Context("AddSecretToCluster", func() {
		When("Secret namespace.name length exceeds limits (> 63 characters)", func() {
			It("Returns an error", func() {
				Expect(secretsUtil.AddSecretToCluster(
					secretNames[2]+"00000", // 60 chars
					clusterNames[0],
					tstNamespace, // "default" 7 chars
					tstNamespace)).Should(HaveOccurred())
			})
			It("Does not create an associated secret policy", func() {
				Expect(plRuleAbsent(plRuleName[2], tstNamespace)).Should(BeTrue())
			})
		})
		When("Secret namespace.name length exceeds limits by 1 (", func() {
			It("Returns an error", func() {
				Expect(secretsUtil.AddSecretToCluster(
					secretNames[2]+"0", // 56 chars
					clusterNames[0],
					tstNamespace, // "default" 7 chars
					tstNamespace)).Should(HaveOccurred())
			})
			It("Does not create an associated secret policy", func() {
				Expect(plRuleAbsent(plRuleName[2], tstNamespace)).Should(BeTrue())
			})
		})
		When("Secret namespace.name length is exactly 63 characters", func() {
			Specify("Create the secret", func() {
				Expect(k8sClient.Create(context.TODO(), secrets[2])).To(Succeed())
			})
			It("Returns success", func() {
				Expect(secretsUtil.AddSecretToCluster(
					secretNames[2],
					clusterNames[0],
					tstNamespace,
					tstNamespace)).To(Succeed())
			})
			It("Protects the secret with a finalizer", func() {
				Expect(finalizerPresent(secretNames[2])).Should(BeTrue())
			})
			It("Creates a associated policy for the secret including the cluster", func() {
				Expect(plRuleContains(plRuleName[2], tstNamespace, clusterNames[:1])).Should(BeTrue())
				Expect(policyContains(policyName[2], tstNamespace, secrets[2])).Should(BeTrue())
			})
		})
		When("Secret is missing", func() {
			It("Returns an error", func() {
				Expect(secretsUtil.AddSecretToCluster(
					secretNames[0],
					clusterNames[0],
					tstNamespace,
					tstNamespace)).Should(HaveOccurred())
			})
			It("Does not create an associated secret policy", func() {
				Expect(plRuleAbsent(plRuleName[0], tstNamespace)).Should(BeTrue())
			})
		})
		When("Secret is present", func() {
			Specify("Create the secret", func() {
				Expect(k8sClient.Create(context.TODO(), secrets[0])).To(Succeed())
			})
			It("Returns success", func() {
				Expect(secretsUtil.AddSecretToCluster(
					secretNames[0],
					clusterNames[0],
					tstNamespace,
					tstNamespace)).To(Succeed())
			})
			It("Protects the secret with a finalizer", func() {
				Expect(finalizerPresent(secretNames[0])).Should(BeTrue())
			})
			It("Creates a associated policy for the secret including the cluster", func() {
				Expect(plRuleContains(plRuleName[0], tstNamespace, clusterNames[:1])).Should(BeTrue())
				Expect(policyContains(policyName[0], tstNamespace, secrets[0])).Should(BeTrue())
			})
		})
		When("Secret is removed", func() {
			Specify("Delete the secret", func() {
				Expect(k8sClient.Delete(context.TODO(), secrets[0])).To(Succeed())
			})
			It("Returns an error", func() {
				Expect(secretsUtil.AddSecretToCluster(
					secretNames[0],
					clusterNames[0],
					tstNamespace,
					tstNamespace)).Should(HaveOccurred())
			})
			It("No longer protects the secret with a finalizer", func() {
				By("Ensuring secret is deleted")
				Eventually(secretAbsent(secretNames[0]), timeout, interval).Should(BeTrue())
			})
			It("Cleans up the associated policy for the secret", func() {
				Expect(plRuleAbsent(plRuleName[0], tstNamespace)).Should(BeTrue())
			})
		})
		When("Secret is recreated", func() {
			Specify("Create the secret", func() {
				Expect(k8sClient.Create(context.TODO(), secrets[0])).To(Succeed())
			})
			It("Returns success", func() {
				Expect(secretsUtil.AddSecretToCluster(
					secretNames[0],
					clusterNames[0],
					tstNamespace,
					tstNamespace)).To(Succeed())
			})
			It("Protects the secret with a finalizer", func() {
				Expect(finalizerPresent(secretNames[0])).Should(BeTrue())
			})
			It("Creates a associated policy for the secret including the cluster", func() {
				Expect(plRuleContains(plRuleName[0], tstNamespace, clusterNames[:1])).Should(BeTrue())
				Expect(policyContains(policyName[0], tstNamespace, secrets[0])).Should(BeTrue())
			})
		})
		When("Secret is updated", func() {
			Specify("Update the secret", func() {
				Expect(updateSecret(secrets[0], "update1")).Should(BeTrue())
			})
			It("Returns success", func() {
				Expect(secretsUtil.AddSecretToCluster(
					secretNames[0],
					clusterNames[0],
					tstNamespace,
					tstNamespace)).To(Succeed())
			})
			It("Protects the secret with a finalizer", func() {
				Expect(finalizerPresent(secretNames[0])).Should(BeTrue())
			})
			It("Creates a associated policy for the secret including the cluster", func() {
				Expect(plRuleContains(plRuleName[0], tstNamespace, clusterNames[:1])).Should(BeTrue())
				Expect(policyContains(policyName[0], tstNamespace, secrets[0])).Should(BeTrue())
			})
		})
		When("Another cluster is added to the secret", func() {
			It("Returns success", func() {
				Expect(secretsUtil.AddSecretToCluster(
					secretNames[0],
					clusterNames[1],
					tstNamespace,
					tstNamespace)).To(Succeed())
			})
			It("Protects the secret with a finalizer", func() {
				Expect(finalizerPresent(secretNames[0])).Should(BeTrue())
			})
			It("Creates a associated policy for the secret including the cluster", func() {
				Expect(plRuleContains(plRuleName[0], tstNamespace, clusterNames[0:])).Should(BeTrue())
				Expect(policyContains(policyName[0], tstNamespace, secrets[0])).Should(BeTrue())
			})
		})
		// Second policy
		When("A cluster is added to another secret", func() {
			Specify("Create the secret", func() {
				Expect(k8sClient.Create(context.TODO(), secrets[1])).To(Succeed())
			})
			It("Returns success", func() {
				Expect(secretsUtil.AddSecretToCluster(
					secretNames[1],
					clusterNames[0],
					tstNamespace,
					tstNamespace)).To(Succeed())
			})
			It("Protects the secret with a finalizer", func() {
				Expect(finalizerPresent(secretNames[1])).Should(BeTrue())
			})
			It("Creates a associated policy for the secret including the cluster", func() {
				Expect(plRuleContains(plRuleName[1], tstNamespace, clusterNames[:1])).Should(BeTrue())
				Expect(policyContains(policyName[1], tstNamespace, secrets[1])).Should(BeTrue())
			})
			It("Retains the associated policy with the older secret", func() {
				Expect(plRuleContains(plRuleName[0], tstNamespace, clusterNames[0:])).Should(BeTrue())
				Expect(policyContains(policyName[0], tstNamespace, secrets[0])).Should(BeTrue())
			})
		})
	})
	Context("Removing secrets from clusters", func() {
		When("The only cluster is removed from a secret", func() {
			It("Returns success", func() {
				Expect(secretsUtil.RemoveSecretFromCluster(
					secretNames[2],
					clusterNames[0],
					tstNamespace)).To(Succeed())
			})
			It("No longer protects the secret with a finalizer", func() {
				Expect(finalizerAbsent(secretNames[2])).To(BeTrue())
			})
			It("Cleans up the associated policy for the secret", func() {
				Expect(plRuleAbsent(plRuleName[2], tstNamespace)).Should(BeTrue())
			})
			It("Does not block deletion of the secret", func() {
				By("Delete the secret")
				Expect(k8sClient.Delete(context.TODO(), secrets[2])).To(Succeed())
				By("Ensuring secret is deleted")
				Eventually(secretAbsent(secretNames[2]), timeout, interval).Should(BeTrue())
			})
		})
		When("A cluster is removed from an updated secret with multiple cluster associations", func() {
			Specify("Update the secret", func() {
				Expect(updateSecret(secrets[0], "update2")).Should(BeTrue())
			})
			It("Returns success", func() {
				Expect(secretsUtil.RemoveSecretFromCluster(
					secretNames[0],
					clusterNames[0],
					tstNamespace)).To(Succeed())
			})
			It("Continues protecting the secret with a finalizer", func() {
				Expect(finalizerPresent(secretNames[0])).Should(BeTrue())
			})
			It("Updates the associated policy for the secret excuding the cluster", func() {
				Expect(plRuleContains(plRuleName[0], tstNamespace, clusterNames[1:2])).Should(BeTrue())
				Expect(policyContains(policyName[0], tstNamespace, secrets[0])).Should(BeTrue())
			})
		})
		When("A cluster is removed again from a secret with multiple cluster associations", func() {
			It("Returns success", func() {
				Expect(secretsUtil.RemoveSecretFromCluster(
					secretNames[0],
					clusterNames[0],
					tstNamespace)).To(Succeed())
			})
			It("Continues protecting the secret with a finalizer", func() {
				Expect(finalizerPresent(secretNames[0])).Should(BeTrue())
			})
			It("Updates the associated policy for the secret excuding the cluster", func() {
				Expect(plRuleContains(plRuleName[0], tstNamespace, clusterNames[1:2])).Should(BeTrue())
				Expect(policyContains(policyName[0], tstNamespace, secrets[0])).Should(BeTrue())
			})
		})
		When("The last cluster is removed from the secret", func() {
			It("Returns success", func() {
				Expect(secretsUtil.RemoveSecretFromCluster(
					secretNames[0],
					clusterNames[1],
					tstNamespace)).To(Succeed())
			})
			It("No longer protects the secret with a finalizer", func() {
				Expect(finalizerAbsent(secretNames[0])).To(BeTrue())
			})
			It("Cleans up the associated policy for the secret", func() {
				Expect(plRuleAbsent(plRuleName[0], tstNamespace)).Should(BeTrue())
			})
		})
		When("The last cluster is removed again from the secret", func() {
			It("Returns success", func() {
				Expect(secretsUtil.RemoveSecretFromCluster(
					secretNames[0],
					clusterNames[1],
					tstNamespace)).To(Succeed())
			})
			It("No longer protects the secret with a finalizer", func() {
				Expect(finalizerAbsent(secretNames[0])).To(BeTrue())
			})
			It("Cleans up the associated policy for the secret", func() {
				Expect(plRuleAbsent(plRuleName[0], tstNamespace)).Should(BeTrue())
			})
			It("Does not block deletion of the secret", func() {
				By("Delete the secret")
				Expect(k8sClient.Delete(context.TODO(), secrets[0])).To(Succeed())
				By("Ensuring secret is deleted")
				Eventually(secretAbsent(secretNames[0]), timeout, interval).Should(BeTrue())
			})
		})
		When("A cluster from a non-existent secret is removed", func() {
			It("Returns success", func() {
				Expect(secretsUtil.RemoveSecretFromCluster(
					secretNames[0]+"-missing",
					clusterNames[1],
					tstNamespace)).To(Succeed())
			})
			It("Does not create the associated policy for the secret", func() {
				Expect(plRuleAbsent(plRuleName[0]+"-missing", tstNamespace)).Should(BeTrue())
			})
		})
		// Second policy
		When("A secret is deleted and a unassociated cluster is removed", func() {
			Specify("Delete the secret", func() {
				Expect(k8sClient.Delete(context.TODO(), secrets[1])).To(Succeed())
			})
			It("Returns success", func() {
				Expect(secretsUtil.RemoveSecretFromCluster(
					secretNames[1],
					clusterNames[1],
					tstNamespace)).To(Succeed())
			})
			It("No longer protects the secret with a finalizer", func() {
				By("Ensuring secret is deleted")
				Eventually(secretAbsent(secretNames[1]), timeout, interval).Should(BeTrue())
			})
			It("Cleans up the associated policy for the secret", func() {
				Expect(plRuleAbsent(plRuleName[1], tstNamespace)).Should(BeTrue())
			})
		})
	})
})
