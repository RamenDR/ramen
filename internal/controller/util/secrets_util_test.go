// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/ramendr/ramen/internal/controller/util"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	clrapiv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	clrapiv1beta2 "open-cluster-management.io/api/cluster/v1beta2"
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
			"012345678901234567890123456789012345678901234567890000", // 54 chars
		}
		clusterNames                         = [clustersCount]string{"clusterEast", "clusterWest"}
		managedClusterSetName                = "default"
		policyName, plBindingName, plName    [secretsCount]string
		policyNameV, plBindingNameV, plNameV [secretsCount]string
		secrets                              [secretsCount]*corev1.Secret
		tstNamespace                         = "default" // 7 chars
		veleroNS                             = "default" // 7 chars
	)

	BeforeEach(func() {
		for idx := range secretNames {
			policyName[idx], plBindingName[idx], plName[idx], _ = util.GeneratePolicyResourceNames(
				secretNames[idx],
				util.SecretFormatRamen)
			policyNameV[idx], plBindingNameV[idx], plNameV[idx], _ = util.GeneratePolicyResourceNames(
				secretNames[idx],
				util.SecretFormatVelero)
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

	plAbsent := func(plName, namespace string) bool {
		pl := &clrapiv1beta1.Placement{}

		return k8serrors.IsNotFound(k8sClient.Get(context.TODO(),
			types.NamespacedName{Name: plName, Namespace: namespace},
			pl))
	}

	clusterSetContains := func(managedClusterSet, namespace string, clusters []string) bool {
		managedClusterSetObject := &clrapiv1beta2.ManagedClusterSet{}
		if err := k8sClient.Get(
			context.TODO(),
			types.NamespacedName{Name: managedClusterSet, Namespace: namespace},
			managedClusterSetObject); err != nil {
			return false
		}

		for _, cluster := range clusters {
			managedClusterObject := &clusterv1.ManagedCluster{}
			if err := k8sClient.Get(
				context.TODO(),
				types.NamespacedName{Name: cluster},
				managedClusterObject); err != nil {
				return false
			}

			if managedClusterObject.Labels["cluster.open-cluster-management.io/clusterset"] != managedClusterSet {
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

	finalizerPresent := func(secretName string, format util.TargetSecretFormat) bool {
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
			if finalizer == util.SecretFinalizer(format) {
				return true
			}
		}

		return false
	}

	finalizerAbsent := func(secretName string, format util.TargetSecretFormat) bool {
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
			if finalizer == util.SecretFinalizer(format) {
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

		return k8serrors.IsNotFound(k8sClient.Get(
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
					secretNames[2]+"00000", // 59 chars
					clusterNames[0],
					managedClusterSetName,
					tstNamespace, // "default" 7 chars
					tstNamespace,
					util.SecretFormatRamen, "")).Should(HaveOccurred())
			})
			It("Does not create an associated secret policy", func() {
				Expect(plAbsent(plName[2], tstNamespace)).Should(BeTrue())
			})
		})
		When("Secret namespace.name length exceeds limits by 1 (", func() {
			It("Returns an error", func() {
				Expect(secretsUtil.AddSecretToCluster(
					secretNames[2]+"0", // 55 chars
					clusterNames[0],
					managedClusterSetName,
					tstNamespace, // "default" 7 chars
					tstNamespace,
					util.SecretFormatRamen, "")).Should(HaveOccurred())
			})
			It("Does not create an associated secret policy", func() {
				Expect(plAbsent(plName[2], tstNamespace)).Should(BeTrue())
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
					managedClusterSetName,
					tstNamespace,
					tstNamespace,
					util.SecretFormatRamen, "")).To(Succeed())
			})
			It("Protects the secret with a finalizer", func() {
				Expect(finalizerPresent(secretNames[2], util.SecretFormatRamen)).Should(BeTrue())
			})
			It("Creates a associated policy for the secret including the cluster", func() {
				Expect(clusterSetContains(managedClusterSetName, tstNamespace, clusterNames[:1])).Should(BeTrue())
				Expect(policyContains(policyName[2], tstNamespace, secrets[2])).Should(BeTrue())
			})
		})
		When("Secret is missing", func() {
			It("Returns an error", func() {
				Expect(secretsUtil.AddSecretToCluster(
					secretNames[0],
					clusterNames[0],
					managedClusterSetName,
					tstNamespace,
					tstNamespace,
					util.SecretFormatRamen, "")).Should(HaveOccurred())
			})
			It("Does not create an associated secret policy", func() {
				Expect(plAbsent(plName[0], tstNamespace)).Should(BeTrue())
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
					managedClusterSetName,
					tstNamespace,
					tstNamespace,
					util.SecretFormatRamen, "")).To(Succeed())
			})
			It("Protects the secret with a finalizer", func() {
				Expect(finalizerPresent(secretNames[0], util.SecretFormatRamen)).Should(BeTrue())
			})
			It("Creates a associated policy for the secret including the cluster", func() {
				Expect(clusterSetContains(managedClusterSetName, tstNamespace, clusterNames[:1])).Should(BeTrue())
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
					managedClusterSetName,
					tstNamespace,
					tstNamespace,
					util.SecretFormatRamen, "")).Should(HaveOccurred())
			})
			It("No longer protects the secret with a finalizer", func() {
				By("Ensuring secret is deleted")
				Eventually(secretAbsent(secretNames[0]), timeout, interval).Should(BeTrue())
			})
			It("Cleans up the associated policy for the secret", func() {
				Expect(plAbsent(plName[0], tstNamespace)).Should(BeTrue())
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
					managedClusterSetName,
					tstNamespace,
					tstNamespace,
					util.SecretFormatRamen, "")).To(Succeed())
			})
			It("Protects the secret with a finalizer", func() {
				Expect(finalizerPresent(secretNames[0], util.SecretFormatRamen)).Should(BeTrue())
			})
			It("Creates a associated policy for the secret including the cluster", func() {
				Expect(clusterSetContains(managedClusterSetName, tstNamespace, clusterNames[:1])).Should(BeTrue())
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
					managedClusterSetName,
					tstNamespace,
					tstNamespace,
					util.SecretFormatRamen, "")).To(Succeed())
			})
			It("Protects the secret with a finalizer", func() {
				Expect(finalizerPresent(secretNames[0], util.SecretFormatRamen)).Should(BeTrue())
			})
			It("Creates a associated policy for the secret including the cluster", func() {
				Expect(clusterSetContains(managedClusterSetName, tstNamespace, clusterNames[:1])).Should(BeTrue())
				Expect(policyContains(policyName[0], tstNamespace, secrets[0])).Should(BeTrue())
			})
		})
		When("Another cluster is added to the secret", func() {
			It("Returns success", func() {
				Expect(secretsUtil.AddSecretToCluster(
					secretNames[0],
					clusterNames[1],
					managedClusterSetName,
					tstNamespace,
					tstNamespace,
					util.SecretFormatRamen, "")).To(Succeed())
			})
			It("Protects the secret with a finalizer", func() {
				Expect(finalizerPresent(secretNames[0], util.SecretFormatRamen)).Should(BeTrue())
			})
			It("Creates a associated policy for the secret including the cluster", func() {
				Expect(clusterSetContains(managedClusterSetName, tstNamespace, clusterNames[0:])).Should(BeTrue())
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
					managedClusterSetName,
					tstNamespace,
					tstNamespace,
					util.SecretFormatRamen, "")).To(Succeed())
			})
			It("Protects the secret with a finalizer", func() {
				Expect(finalizerPresent(secretNames[1], util.SecretFormatRamen)).Should(BeTrue())
			})
			It("Creates a associated policy for the secret including the cluster", func() {
				Expect(clusterSetContains(managedClusterSetName, tstNamespace, clusterNames[:1])).Should(BeTrue())
				Expect(policyContains(policyName[1], tstNamespace, secrets[1])).Should(BeTrue())
			})
			It("Retains the associated policy with the older secret", func() {
				Expect(clusterSetContains(managedClusterSetName, tstNamespace, clusterNames[0:])).Should(BeTrue())
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
					managedClusterSetName,
					tstNamespace,
					util.SecretFormatRamen)).To(Succeed())
			})
			It("No longer protects the secret with a finalizer", func() {
				Expect(finalizerAbsent(secretNames[2], util.SecretFormatRamen)).To(BeTrue())
			})
			It("Cleans up the associated policy for the secret", func() {
				Expect(plAbsent(plName[2], tstNamespace)).Should(BeTrue())
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
					managedClusterSetName,
					tstNamespace,
					util.SecretFormatRamen)).To(Succeed())
			})
			It("Continues protecting the secret with a finalizer", func() {
				Expect(finalizerPresent(secretNames[0], util.SecretFormatRamen)).Should(BeTrue())
			})
			It("Updates the associated policy for the secret excuding the cluster", func() {
				Expect(clusterSetContains(managedClusterSetName, tstNamespace, clusterNames[1:2])).Should(BeTrue())
				Expect(policyContains(policyName[0], tstNamespace, secrets[0])).Should(BeTrue())
			})
		})
		When("A cluster is removed again from a secret with multiple cluster associations", func() {
			It("Returns success", func() {
				Expect(secretsUtil.RemoveSecretFromCluster(
					secretNames[0],
					clusterNames[0],
					managedClusterSetName,
					tstNamespace,
					util.SecretFormatRamen)).To(Succeed())
			})
			It("Continues protecting the secret with a finalizer", func() {
				Expect(finalizerPresent(secretNames[0], util.SecretFormatRamen)).Should(BeTrue())
			})
			It("Updates the associated policy for the secret excuding the cluster", func() {
				Expect(clusterSetContains(managedClusterSetName, tstNamespace, clusterNames[1:2])).Should(BeTrue())
				Expect(policyContains(policyName[0], tstNamespace, secrets[0])).Should(BeTrue())
			})
		})
		When("The last cluster is removed from the secret", func() {
			It("Returns success", func() {
				Expect(secretsUtil.RemoveSecretFromCluster(
					secretNames[0],
					clusterNames[1],
					managedClusterSetName,
					tstNamespace,
					util.SecretFormatRamen)).To(Succeed())
			})
			It("No longer protects the secret with a finalizer", func() {
				Expect(finalizerAbsent(secretNames[0], util.SecretFormatRamen)).To(BeTrue())
			})
			It("Cleans up the associated policy for the secret", func() {
				Expect(plAbsent(plName[0], tstNamespace)).Should(BeTrue())
			})
		})
		When("The last cluster is removed again from the secret", func() {
			It("Returns success", func() {
				Expect(secretsUtil.RemoveSecretFromCluster(
					secretNames[0],
					clusterNames[1],
					managedClusterSetName,
					tstNamespace,
					util.SecretFormatRamen)).To(Succeed())
			})
			It("No longer protects the secret with a finalizer", func() {
				Expect(finalizerAbsent(secretNames[0], util.SecretFormatRamen)).To(BeTrue())
			})
			It("Cleans up the associated policy for the secret", func() {
				Expect(plAbsent(plName[0], tstNamespace)).Should(BeTrue())
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
					managedClusterSetName,
					tstNamespace,
					util.SecretFormatRamen)).To(Succeed())
			})
			It("Does not create the associated policy for the secret", func() {
				Expect(plAbsent(plName[0]+"-missing", tstNamespace)).Should(BeTrue())
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
					managedClusterSetName,
					tstNamespace,
					util.SecretFormatRamen)).To(Succeed())
			})
			It("No longer protects the secret with a finalizer", func() {
				By("Ensuring secret is deleted")
				Eventually(secretAbsent(secretNames[1]), timeout, interval).Should(BeTrue())
			})
			It("Cleans up the associated policy for the secret", func() {
				Expect(plAbsent(plName[1], tstNamespace)).Should(BeTrue())
			})
		})
	})
	Context("AddSecretToCluster to VeleroNS", func() {
		When("Secret is initially created in the ramen namespace", func() {
			Specify("Create the secret", func() {
				Expect(k8sClient.Create(context.TODO(), secrets[0])).To(Succeed())
			})
			It("Returns success", func() {
				Expect(secretsUtil.AddSecretToCluster(
					secretNames[0],
					clusterNames[0],
					managedClusterSetName,
					tstNamespace,
					tstNamespace,
					util.SecretFormatRamen,
					"")).To(Succeed())
			})
			It("Protects the secret with a finalizer", func() {
				Expect(finalizerPresent(secretNames[0], util.SecretFormatRamen)).Should(BeTrue())
			})
			It("Creates an associated policy for the secret including the cluster", func() {
				Expect(clusterSetContains(managedClusterSetName, tstNamespace, clusterNames[:1])).Should(BeTrue())
				Expect(policyContains(policyName[0], tstNamespace, secrets[0])).Should(BeTrue())
			})
		})
		When("Secret is created in the Velero namespace", func() {
			It("Succeeds", func() {
				Expect(secretsUtil.AddSecretToCluster(
					secretNames[0],
					clusterNames[0],
					managedClusterSetName,
					tstNamespace,
					tstNamespace,
					util.SecretFormatVelero,
					veleroNS)).To(Succeed())
			})
			It("Protects the secret with an additional finalizer", func() {
				Expect(finalizerPresent(secretNames[0], util.SecretFormatVelero)).Should(BeTrue())
			})
			It("Creates an associated policy for the secret including the cluster", func() {
				Expect(clusterSetContains(managedClusterSetName, tstNamespace, clusterNames[:1])).Should(BeTrue())
				Expect(policyContains(policyNameV[0], tstNamespace, secrets[0])).Should(BeTrue())
			})
		})
		When("Secret is removed", func() {
			Specify("Delete the secret", func() {
				Expect(k8sClient.Delete(context.TODO(), secrets[0])).To(Succeed())
			})
			It("Returns an error when added again to the velero namespace", func() {
				Expect(secretsUtil.AddSecretToCluster(
					secretNames[0],
					clusterNames[0],
					managedClusterSetName,
					tstNamespace,
					tstNamespace,
					util.SecretFormatVelero,
					veleroNS)).Should(HaveOccurred())
			})
			It("Cleans up the associated velero policy for the secret", func() {
				Expect(plAbsent(plNameV[0], tstNamespace)).Should(BeTrue())
				Expect(clusterSetContains(managedClusterSetName, tstNamespace, clusterNames[:1])).Should(BeTrue())
			})
			It("Continues protecting the secret with the ramen namespace finalizer", func() {
				Expect(finalizerPresent(secretNames[0], util.SecretFormatRamen)).Should(BeTrue())
			})
			It("Returns an error when added again to the ramen namespace", func() {
				Expect(secretsUtil.AddSecretToCluster(
					secretNames[0],
					clusterNames[0],
					managedClusterSetName,
					tstNamespace,
					tstNamespace,
					util.SecretFormatRamen,
					veleroNS)).Should(HaveOccurred())
			})
			It("No longer protects the secret with a finalizer", func() {
				By("Ensuring secret is deleted")
				Eventually(secretAbsent(secretNames[0]), timeout, interval).Should(BeTrue())
			})
			It("Cleans up the associated policy for the secret", func() {
				Expect(plAbsent(plNameV[0], tstNamespace)).Should(BeTrue())
				Expect(plAbsent(plName[0], tstNamespace)).Should(BeTrue())
			})
		})
	})
})
