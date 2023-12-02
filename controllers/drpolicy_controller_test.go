// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	gomegaTypes "github.com/onsi/gomega/types"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/util"
	plrv1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	validationErrors "k8s.io/kube-openapi/pkg/validation/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("DrpolicyController", func() {
	drpolicyGet := func(drpolicy *ramen.DRPolicy) error {
		return apiReader.Get(context.TODO(), types.NamespacedName{Name: drpolicy.Name}, drpolicy)
	}
	validatedConditionExpect := func(drpolicy *ramen.DRPolicy, status metav1.ConditionStatus,
		messageMatcher gomegaTypes.GomegaMatcher,
	) {
		Eventually(
			func(g Gomega) {
				g.Expect(drpolicyGet(drpolicy)).To(Succeed())
				g.Expect(drpolicy.Status.Conditions).To(MatchElements(
					func(element interface{}) string {
						return element.(metav1.Condition).Type
					},
					IgnoreExtras,
					Elements{
						ramen.DRPolicyValidated: MatchAllFields(Fields{
							"Type":               Ignore(),
							"Status":             Equal(status),
							"ObservedGeneration": Equal(drpolicy.Generation),
							"LastTransitionTime": Ignore(),
							"Reason":             Ignore(),
							"Message":            messageMatcher,
						}),
					},
				))
			},
			timeout,
			interval,
		).Should(Succeed())
	}
	drpolicyCreate := func(drpolicy *ramen.DRPolicy) {
		Expect(k8sClient.Create(context.TODO(), drpolicy)).To(Succeed())
	}
	drpolicyDeleteAndConfirm := func(drpolicy *ramen.DRPolicy) {
		Expect(k8sClient.Delete(context.TODO(), drpolicy)).To(Succeed())
		Eventually(func() bool {
			return errors.IsNotFound(drpolicyGet(drpolicy))
		}, timeout, interval).Should(BeTrue())
	}
	drpolicyDelete := func(drpolicy *ramen.DRPolicy) {
		drpolicyDeleteAndConfirm(drpolicy)
	}

	// For each policy combination that may exist, add an entry for use in ensuring secret is created as desired:
	// - Initial map takes keys that are ordered combinations of drPolicy names that may co-exist
	// - Internal map takes keys that are secret names with a list of strings as its value containing the cluster
	// list that it should be available on
	drPoliciesAndSecrets := map[string]map[string][]string{
		"drpolicy0": {
			"s3secret0": {"drp-cluster0", "drp-cluster1"},
		},
		"drpolicy1": {
			"s3secret0": {"drp-cluster1", "drp-cluster2"},
		},
		"drpolicy0drpolicy1": {
			"s3secret0": {"drp-cluster0", "drp-cluster1", "drp-cluster2"},
		},
	}

	getPlRuleForSecrets := func() map[string]plrv1.PlacementRule {
		plRuleList := &plrv1.PlacementRuleList{}
		listOptions := &client.ListOptions{Namespace: ramenNamespace}

		Expect(apiReader.List(context.TODO(), plRuleList, listOptions)).NotTo(HaveOccurred())

		foundPlRules := make(map[string]plrv1.PlacementRule, len(plRuleList.Items))
		for _, plRule := range plRuleList.Items {
			if _, ok := plRuleNames[plRule.Name]; !ok {
				continue
			}
			foundPlRules[plRule.Name] = plRule
		}

		return foundPlRules
	}
	validateSecretDistribution := func(drPolicies []ramen.DRPolicy) {
		plRules := getPlRuleForSecrets()

		// If no policies are present, expect no secret placement rules
		if drPolicies == nil {
			Expect(len(plRules)).To(Equal(0))

			return
		}

		// Construct drpolicies name
		policyCombinationName := ""
		for _, drpolicy := range drPolicies {
			policyCombinationName += drpolicy.Name
		}

		// Ensure list of secrets for the policy name has as many placement rules
		Eventually(func() bool {
			plRules = getPlRuleForSecrets()

			return len(plRules) == len(drPoliciesAndSecrets[policyCombinationName])
		}, timeout, interval).Should(BeTrue())

		// Range through secrets in drpolicies name and ensure cluster list is the same
		for secretName, clusterList := range drPoliciesAndSecrets[policyCombinationName] {
			_, _, plRuleName, _ := util.GeneratePolicyResourceNames(secretName)

			Eventually(func() (clusterNames []string) {
				plRules = getPlRuleForSecrets()
				for _, cluster := range plRules[plRuleName].Spec.Clusters {
					clusterNames = append(clusterNames, cluster.Name)
				}

				return
			}, timeout, interval).Should(ConsistOf(clusterList))
		}
	}

	clusterNames := [...]string{
		"drp-cluster0",
		"drp-cluster1",
		"drp-cluster2",
	}
	createDRClustersAndDeferCleanup := func(from, to int, drclusterCleanup func(*ramen.DRCluster)) []ramen.DRCluster {
		drClusters := [...]ramen.DRCluster{
			{
				ObjectMeta: metav1.ObjectMeta{Name: clusterNames[0]},
				Spec:       ramen.DRClusterSpec{S3ProfileName: s3Profiles[0].S3ProfileName, Region: "east"},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: clusterNames[1]},
				Spec:       ramen.DRClusterSpec{S3ProfileName: s3Profiles[0].S3ProfileName, Region: "west"},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: clusterNames[2]},
				Spec:       ramen.DRClusterSpec{S3ProfileName: s3Profiles[0].S3ProfileName, Region: "east"},
			},
		}
		for idx := range drClusters[from:to] {
			drcluster := &drClusters[idx+from]
			namespaceCreateAndDeferDeleteOrConfirmAlreadyExists(drcluster.Name)
			Expect(k8sClient.Create(context.TODO(), drcluster)).To(Succeed())
			DeferCleanup(drclusterCleanup, drcluster)
			updateDRClusterManifestWorkStatus(drcluster.Name)
			drclusterConditionExpectEventually(
				drcluster,
				!ramenConfig.DrClusterOperator.DeploymentAutomationEnabled,
				metav1.ConditionTrue,
				Equal("Succeeded"),
				Ignore(),
				ramen.DRClusterValidated,
			)
		}

		return drClusters[from:to]
	}
	drclusterDelete := func(drcluster *ramen.DRCluster) {
		Expect(k8sClient.Delete(context.TODO(), drcluster)).To(Succeed())
		Eventually(apiReader.Get).WithArguments(context.TODO(), types.NamespacedName{Name: drcluster.Name}, drcluster).
			Should(MatchError(errors.NewNotFound(
				schema.GroupResource{Group: ramen.GroupVersion.Group, Resource: "drclusters"},
				drcluster.Name,
			)))
	}
	createDRClustersAndDeferDelete := func(from, to int) []ramen.DRCluster {
		return createDRClustersAndDeferCleanup(from, to, drclusterDelete)
	}
	createDRClusters := func(from, to int) []ramen.DRCluster {
		return createDRClustersAndDeferCleanup(from, to, func(*ramen.DRCluster) {})
	}

	drpoliciesDefault := [...]ramen.DRPolicy{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "drpolicy0"},
			Spec:       ramen.DRPolicySpec{DRClusters: clusterNames[0:2], SchedulingInterval: "00m"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "drpolicy1"},
			Spec:       ramen.DRPolicySpec{DRClusters: clusterNames[1:3], SchedulingInterval: "9999999d"},
		},
	}
	var drpolicies [len(drpoliciesDefault)]ramen.DRPolicy
	var drpolicy *ramen.DRPolicy
	BeforeEach(OncePerOrdered, func() {
		drpolicies = drpoliciesDefault
		drpolicy = &drpolicies[0]
	})

	When("a drpolicy creation request is submitted, and its", func() {
		var err error
		JustBeforeEach(OncePerOrdered, func() {
			err = k8sClient.Create(context.TODO(), drpolicy)
			DeferCleanup(func() {
				validateSecretDistribution(nil)
			})
		})
		Context("scheduling interval value is", func() {
			invalid := func(value string) *errors.StatusError {
				path := field.NewPath("spec", "schedulingInterval")

				return errors.NewInvalid(
					schema.GroupKind{
						Group: ramen.GroupVersion.Group,
						Kind:  "DRPolicy",
					},
					drpolicy.Name,
					field.ErrorList{
						field.Invalid(
							path,
							value,
							validationErrors.FailedPattern(
								path.String(),
								"body",
								`^\d+[mhd]$`,
								value,
							).Error(),
						),
					},
				)
			}
			Context("3s", func() {
				BeforeEach(func() {
					drpolicy.Spec.SchedulingInterval = "3s"
				})
				It("denies it", func() {
					Expect(err).To(MatchError(invalid(drpolicy.Spec.SchedulingInterval)))
				})
			})
			Context("0", func() {
				BeforeEach(func() {
					drpolicy.Spec.SchedulingInterval = "0"
				})
				It("denies it", func() {
					Expect(err).To(MatchError(invalid(drpolicy.Spec.SchedulingInterval)))
				})
			})
		})
		Context("drclusters value is", func() {
			Context("nil", func() {
				BeforeEach(func() {
					drpolicy.Spec.DRClusters = nil
				})
				It("fails the request specifying the absent required drclusters field", func() {
					Expect(err).To(MatchError(errors.NewInvalid(
						schema.GroupKind{
							Group: ramen.GroupVersion.Group,
							Kind:  "DRPolicy",
						},
						drpolicy.Name,
						field.ErrorList{
							field.Required(
								field.NewPath("spec", "drClusters"),
								"",
							),
						},
					)))
				})
			})
			Context("admissible, but", func() {
				JustBeforeEach(OncePerOrdered, func() {
					Expect(err).To(BeNil())
				})
				Context("contains undefined drclusters", Ordered, func() {
					Context("initially", func() {
						It("sets its validated status condition's status to false", func() {
							validatedConditionExpect(drpolicy, metav1.ConditionFalse, Ignore())
						})
					})
					var drcluster1 *ramen.DRCluster
					Context("then one is defined", func() {
						JustBeforeEach(OncePerOrdered, func() {
							drcluster1 = &createDRClusters(1, 2)[0]
						})
						It("sets its validated status condition's status to false", func() {
							validatedConditionExpect(drpolicy, metav1.ConditionFalse, Ignore())
						})
					})
					var drcluster0 *ramen.DRCluster
					Context("then the last one is defined", func() {
						JustBeforeEach(OncePerOrdered, func() {
							drcluster0 = &createDRClusters(0, 1)[0]
						})
						It("sets its validated status condition's status to true", func() {
							validatedConditionExpect(drpolicy, metav1.ConditionTrue, Ignore())
						})
					})
					AfterAll(func() {
						drpolicyDelete(drpolicy)
						drclusterDelete(drcluster1)
						drclusterDelete(drcluster0)
					})
				})
				Context("empty", func() {
					BeforeEach(func() {
						drpolicy.Spec.DRClusters = []string{}
						createDRClustersAndDeferDelete(0, 2)
					})
					JustBeforeEach(func() {
						DeferCleanup(drpolicyDelete, drpolicy)
					})
					It("sets its validated status condition's status to false", func() {
						validatedConditionExpect(drpolicy, metav1.ConditionFalse, Ignore())
					})
				})
			})
		})
	})
	Context("drpolicies sharing a drcluster", Ordered, func() {
		BeforeAll(func() {
			createDRClustersAndDeferDelete(0, 3)
		})
		When("a 1st drpolicy is created", func() {
			BeforeAll(func() {
				drpolicyCreate(drpolicy)
			})
			It("sets its validated status condition's status to true", func() {
				validatedConditionExpect(drpolicy, metav1.ConditionTrue, Ignore())
			})
			It("creates a secret placement rule for each cluster specified in a 1st drpolicy", func() {
				validateSecretDistribution(drpolicies[0:1])
			})
		})
		When("a 2nd drpolicy is created specifying some clusters in a 1st drpolicy and some not", func() {
			BeforeAll(func() {
				drpolicyCreate(&drpolicies[1])
			})
			It("sets its validated status condition's status to true", func() {
				validatedConditionExpect(&drpolicies[1], metav1.ConditionTrue, Ignore())
			})
			It("creates a secret placement rule for each cluster specified in a 2nd drpolicy but not a 1st drpolicy", func() {
				validateSecretDistribution(drpolicies[0:2])
			})
		})
		When("a 1st drpolicy is deleted", func() {
			BeforeAll(func() {
				drpolicyDelete(drpolicy)
			})
			It("deletes a secret placement rule for each cluster specified in a 1st drpolicy but not a 2nd drpolicy", func() {
				validateSecretDistribution(drpolicies[1:2])
			})
		})
		When("a 2nd drpolicy is deleted", func() {
			BeforeAll(func() {
				drpolicyDelete(&drpolicies[1])
			})
			It("deletes a secret placement rule for each cluster specified in a 2nd drpolicy", func() {
				validateSecretDistribution(nil)
			})
		})
	})
})
