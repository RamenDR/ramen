// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package volsync_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ramendr/ramen/internal/controller/util"
	"github.com/ramendr/ramen/internal/controller/volsync"
	plrulev1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	cfgpolicyv1 "open-cluster-management.io/config-policy-controller/api/v1"
	policyv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
)

var _ = Describe("Secret_propagator", func() {
	genericCodecs := serializer.NewCodecFactory(scheme.Scheme)
	genericCodec := genericCodecs.UniversalDeserializer()

	var testNamespace *corev1.Namespace
	var testLongNamespaceName *corev1.Namespace
	var owner metav1.Object
	var ownerWithLongNameNamespace metav1.Object

	BeforeEach(func() {
		// Create namespace for test
		testNamespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "sec-prop-test-",
			},
		}
		Expect(k8sClient.Create(ctx, testNamespace)).To(Succeed())
		Expect(testNamespace.GetName()).NotTo(BeEmpty())

		// Create namespace for testing long namespace name
		testLongNamespaceName = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "dummy-cm-namespace-with-len-of-45-characters",
			},
		}
		Expect(k8sClient.Create(ctx, testLongNamespaceName)).To(Succeed())
		Expect(testLongNamespaceName.GetName()).NotTo(BeEmpty())

		// Create dummy resource to be the "owner" of the generated secret
		// Using a configmap for now - in reality this owner resource will
		// be a DRPC
		ownerCm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "dummycm-owner-",
				Namespace:    testNamespace.GetName(),
			},
		}
		Expect(k8sClient.Create(ctx, ownerCm)).To(Succeed())
		Expect(ownerCm.GetName()).NotTo(BeEmpty())
		owner = ownerCm

		// Create a new owner for testing long name/namespace
		newOwnerCM := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "dummy-cm-name-with-length-of-43-characters-",
				Namespace:    testLongNamespaceName.GetName(),
			},
		}
		Expect(k8sClient.Create(ctx, newOwnerCM)).To(Succeed())
		Expect(newOwnerCM.GetName()).NotTo(BeEmpty())
		ownerWithLongNameNamespace = newOwnerCM
	})

	AfterEach(func() {
		// All resources are namespaced, so this should clean it all up
		Expect(k8sClient.Delete(ctx, testNamespace)).To(Succeed())

		// All resources are namespaced, so this should clean it all up
		Expect(k8sClient.Delete(ctx, testLongNamespaceName)).To(Succeed())
	})

	Describe("Secret propagation from hub", func() {
		var testSecret *corev1.Secret

		BeforeEach(func() {
			testSecret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "dummy-hub-vs-secret-",
					Namespace:    testNamespace.GetName(),
				},
				StringData: map[string]string{
					"abc": "def",
					"123": "456",
				},
			}
			Expect(k8sClient.Create(ctx, testSecret)).To(Succeed())
		})

		Context("When the policy does not already exist", func() {
			Context("When propagate secret to clusters is run", func() {
				destClusters := []string{"cluster-1", "cluster-2"}
				destSecName := "my-secret-on-mgd"
				destSecNamespace := "managed-cluster-ns-1"

				var createdPolicy *policyv1.Policy
				var createdPlacementRule *plrulev1.PlacementRule
				var createdPlacementBinding *policyv1.PlacementBinding

				BeforeEach(func() {
					policyRuleAndBindingName := owner.GetName() + "-vs-secret"

					Eventually(func() bool {
						// Run in eventually loop to make sure the cache loads the newly created resources
						err := volsync.PropagateSecretToClusters(ctx, k8sClient, testSecret, owner,
							destClusters, destSecName, destSecNamespace, logger)
						if err != nil {
							return false
						}

						// Find the new policy, pl rule and pl binding
						createdPolicy = &policyv1.Policy{}
						err = k8sClient.Get(ctx, types.NamespacedName{
							Name:      policyRuleAndBindingName,
							Namespace: testNamespace.GetName(),
						}, createdPolicy)
						if err != nil {
							return false
						}

						createdPlacementRule = &plrulev1.PlacementRule{}
						err = k8sClient.Get(ctx, types.NamespacedName{
							Name:      policyRuleAndBindingName,
							Namespace: testNamespace.GetName(),
						}, createdPlacementRule)
						if err != nil {
							return false
						}

						createdPlacementBinding = &policyv1.PlacementBinding{}
						err = k8sClient.Get(ctx, types.NamespacedName{
							Name:      policyRuleAndBindingName,
							Namespace: testNamespace.GetName(),
						}, createdPlacementBinding)

						return err == nil
					}, maxWait, interval).Should(BeTrue())
				})

				It("Should create a policy, placementrule and placement binding", func() {
					//
					// Verify created policy
					//
					Expect(createdPolicy.Spec.Disabled).To(Equal(false))
					Expect(len(createdPolicy.Spec.PolicyTemplates)).To(Equal(1))
					policyTemplate := createdPolicy.Spec.PolicyTemplates[0]

					// Decode the embedded config policy and confirm it looks good
					embeddedObj, _, err := genericCodec.Decode(policyTemplate.ObjectDefinition.Raw, nil, nil)
					Expect(err).NotTo(HaveOccurred())
					embeddedConfigPolicy, ok := embeddedObj.(*cfgpolicyv1.ConfigurationPolicy)
					Expect(ok).To(BeTrue())

					Expect(embeddedConfigPolicy.Spec.RemediationAction).To(Equal(cfgpolicyv1.Enforce))
					Expect(len(embeddedConfigPolicy.Spec.ObjectTemplates)).To(Equal(1))
					secretObjectTemplate := embeddedConfigPolicy.Spec.ObjectTemplates[0]
					Expect(secretObjectTemplate.ComplianceType).To(Equal(cfgpolicyv1.MustHave))

					// Can't decode the embedded secret as we did above with the configpolicy as
					// the encoded version isn't a real secret - it has special strings in the data for ACM
					// rather than byte[]
					type secretDefinition struct {
						APIVersion string `json:"apiVersion"`
						Type       string `json:"type"`
						Kind       string `json:"kind"`
						Metadata   struct {
							Name      string `json:"name"`
							Namespace string `json:"namespace"`
						} `json:"metadata"`
						Data map[string]string `json:"data"`
					}

					embeddedSecret := secretDefinition{}
					Expect(json.Unmarshal(secretObjectTemplate.ObjectDefinition.Raw, &embeddedSecret)).To(Succeed())

					Expect(embeddedSecret.Type).To(Equal("Opaque"))
					Expect(embeddedSecret.Kind).To(Equal("Secret"))
					Expect(embeddedSecret.Metadata.Name).To(Equal(destSecName))
					Expect(embeddedSecret.Metadata.Namespace).To(Equal(destSecNamespace))

					Expect(embeddedSecret.Data["abc"]).To(ContainSubstring("hub fromSecret"))
					Expect(embeddedSecret.Data["123"]).To(ContainSubstring("hub fromSecret"))

					//
					// Verify created placementRule
					//
					Expect(verifyPlacementRuleClusters(
						createdPlacementRule.Spec.GenericPlacementFields.Clusters, destClusters)).To(BeTrue())

					//
					// Verify placement binding
					//
					Expect(createdPlacementBinding.PlacementRef.APIGroup).To(Equal("apps.open-cluster-management.io"))
					Expect(createdPlacementBinding.PlacementRef.Kind).To(Equal("PlacementRule"))
					Expect(createdPlacementBinding.PlacementRef.Name).To(Equal(createdPlacementRule.GetName()))

					Expect(len(createdPlacementBinding.Subjects)).To(Equal(1))
					plBindingSubject := createdPlacementBinding.Subjects[0]
					Expect(plBindingSubject.APIGroup).To(Equal("policy.open-cluster-management.io"))
					Expect(plBindingSubject.Kind).To(Equal("Policy"))
					Expect(plBindingSubject.Name).To(Equal(createdPolicy.GetName()))
					Expect(createdPolicy.GetLabels()[util.ExcludeFromVeleroBackup]).Should(Equal("true"))
				})

				Context("When Policy name combined with namespace is longer than 62 characters", func() {
					It("Should regenerate and trim the Policy name", func() {
						destClustersUpdated := []string{"cluster-2", "cluster-3"}
						policyName := ownerWithLongNameNamespace.GetName() + "-vs-secret"

						generatedPolicyName := util.GeneratePolicyName(policyName,
							62-len(ownerWithLongNameNamespace.GetNamespace()))

						Eventually(func() bool {
							// Run in eventually loop to make sure the cache loads the newly created resources
							err := volsync.PropagateSecretToClusters(ctx, k8sClient, testSecret, ownerWithLongNameNamespace,
								destClustersUpdated, destSecName, destSecNamespace, logger)
							if err != nil {
								return false
							}

							// Find the new policy, pl rule and pl binding
							createdPolicy = &policyv1.Policy{}
							err = k8sClient.Get(ctx, types.NamespacedName{
								Name:      generatedPolicyName,
								Namespace: ownerWithLongNameNamespace.GetNamespace(),
							}, createdPolicy)
							if err != nil {
								return false
							}

							return err == nil
						}, maxWait, interval).Should(BeTrue())
					})
				})

				Context("When secret policy prop run again when policy/rule/binding already exist", func() {
					It("Should reconcile and update properly", func() {
						destClustersUpdated := []string{"cluster-3", "cluster-4"}

						Eventually(func() bool {
							// Run in eventually loop to make sure the cache loads the newly created resources
							err := volsync.PropagateSecretToClusters(ctx, k8sClient, testSecret, owner,
								destClustersUpdated, destSecName, destSecNamespace, logger)
							if err != nil {
								return false
							}

							// Find the new pl rule to check if its been updated
							updatedPlacementRule := &plrulev1.PlacementRule{}
							err = k8sClient.Get(ctx, types.NamespacedName{
								Name:      createdPlacementRule.GetName(),
								Namespace: testNamespace.GetName(),
							}, updatedPlacementRule)
							if err != nil {
								return false
							}

							return verifyPlacementRuleClusters(
								updatedPlacementRule.Spec.GenericPlacementFields.Clusters, destClustersUpdated)
						}, maxWait, interval).Should(BeTrue())
					})
				})

				Context("When cleanup is run and policy/rule/binding exist", func() {
					// Policy/placementrule/placementbinding were all created at this point
					It("Should cleanup the policy/rule/binding", func() {
						Expect(volsync.CleanupSecretPropagation(ctx, k8sClient, owner, logger)).To(Succeed())

						Eventually(func() bool {
							policyErr := k8sClient.Get(ctx, client.ObjectKeyFromObject(createdPolicy), createdPolicy)
							ruleErr := k8sClient.Get(ctx, client.ObjectKeyFromObject(createdPlacementRule),
								createdPlacementRule)
							bindingErr := k8sClient.Get(ctx, client.ObjectKeyFromObject(createdPlacementBinding),
								createdPlacementBinding)

							return kerrors.IsNotFound(policyErr) && kerrors.IsNotFound(ruleErr) &&
								kerrors.IsNotFound(bindingErr)
						}, maxWait, interval).Should(BeTrue())
					})
				})
			})

			Context("When cleanup is run with no policy/rule/binding", func() {
				It("Should return successfully with no error", func() {
					Expect(volsync.CleanupSecretPropagation(ctx, k8sClient, owner, logger)).To(Succeed())
				})
			})
		})
	})
})

func verifyPlacementRuleClusters(placementRuleClusters []plrulev1.GenericClusterReference,
	expectedDestClusters []string,
) bool {
	if len(placementRuleClusters) != len(expectedDestClusters) {
		return false
	}

	foundDestClusters := true

	for _, destClusterName := range expectedDestClusters {
		found := false

		for _, placementCluster := range placementRuleClusters {
			if destClusterName == placementCluster.Name {
				found = true
			}
		}

		foundDestClusters = foundDestClusters && found
	}

	return foundDestClusters
}
