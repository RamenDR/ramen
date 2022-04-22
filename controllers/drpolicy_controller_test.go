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

package controllers_test

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	gomegaTypes "github.com/onsi/gomega/types"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/util"
	plrv1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	validationErrors "k8s.io/kube-openapi/pkg/validation/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("DrpolicyController", func() {
	validatedConditionExpect := func(drpolicy *ramen.DRPolicy, status metav1.ConditionStatus,
		messageMatcher gomegaTypes.GomegaMatcher,
	) {
		Eventually(
			func(g Gomega) {
				g.Expect(apiReader.Get(
					context.TODO(),
					types.NamespacedName{Name: drpolicy.Name},
					drpolicy,
				)).To(Succeed())
				g.Expect(drpolicy.Status.Conditions).To(MatchElements(
					func(element interface{}) string {
						return element.(metav1.Condition).Type
					},
					IgnoreExtras,
					Elements{
						ramen.DRPolicyValidated: MatchAllFields(Fields{
							`Type`:               Ignore(),
							`Status`:             Equal(status),
							`ObservedGeneration`: Equal(drpolicy.Generation),
							`LastTransitionTime`: Ignore(),
							`Reason`:             Ignore(),
							`Message`:            messageMatcher,
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
			return errors.IsNotFound(apiReader.Get(context.TODO(), types.NamespacedName{Name: drpolicy.Name}, drpolicy))
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

	plRuleContains := func(plRule plrv1.PlacementRule, clusters []string) bool {
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
	getPlRuleForSecrets := func() []plrv1.PlacementRule {
		plRuleList := &plrv1.PlacementRuleList{}
		listOptions := &client.ListOptions{Namespace: configMap.Namespace}

		Expect(apiReader.List(context.TODO(), plRuleList, listOptions)).NotTo(HaveOccurred())

		foundPlRules := []plrv1.PlacementRule{}
		for _, plRule := range plRuleList.Items {
			for _, plRuleName := range plRuleNames {
				if plRule.Name != plRuleName {
					continue
				}
				foundPlRules = append(foundPlRules, plRule)

				break
			}
		}

		return foundPlRules
	}
	vaildateSecretDistribution := func(drPolicies []ramen.DRPolicy) {
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
		Expect(len(plRules) == len(drPoliciesAndSecrets[policyCombinationName])).To(BeTrue())

		// Range through secrets in drpolicies name and ensure cluster list is the same
		for secretName, clusterList := range drPoliciesAndSecrets[policyCombinationName] {
			found := false
			_, _, plRuleName, _ := util.GeneratePolicyResourceNames(secretName)

			for _, plRule := range plRules {
				if plRule.Name != plRuleName {
					continue
				}
				Expect(plRuleContains(plRule, clusterList)).To(BeTrue())
				found = true

				break
			}
			Expect(found).To(BeTrue())
		}
	}

	clusters := [...]string{"drp-cluster0", "drp-cluster1", "drp-cluster2"}
	drClusters := []ramen.DRCluster{}
	populateDRClusters := func() {
		drClusters = nil
		drClusters = append(drClusters,
			ramen.DRCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "drp-cluster0", Namespace: ramenNamespace},
				Spec:       ramen.DRClusterSpec{S3ProfileName: s3Profiles[0].S3ProfileName, Region: "east"},
			},
			ramen.DRCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "drp-cluster1", Namespace: ramenNamespace},
				Spec:       ramen.DRClusterSpec{S3ProfileName: s3Profiles[0].S3ProfileName, Region: "west"},
			},
			ramen.DRCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "drp-cluster2", Namespace: ramenNamespace},
				Spec:       ramen.DRClusterSpec{S3ProfileName: s3Profiles[0].S3ProfileName, Region: "east"},
			},
		)
	}
	drpolicies := [...]ramen.DRPolicy{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "drpolicy0"},
			Spec:       ramen.DRPolicySpec{DRClusters: clusters[0:2], SchedulingInterval: `00m`},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "drpolicy1"},
			Spec:       ramen.DRPolicySpec{DRClusters: clusters[1:3], SchedulingInterval: `9999999d`},
		},
	}
	var drpolicyObjectMetas [len(drpolicies)]metav1.ObjectMeta
	func() {
		for i := range drpolicies {
			drpolicyObjectMetas[i] = drpolicies[i].ObjectMeta
		}
	}()
	drpolicyObjectMetaReset := func(i uint) {
		drpolicies[i].ObjectMeta = drpolicyObjectMetas[i]
	}
	var drpolicy *ramen.DRPolicy
	var drpolicyNumber uint
	Specify("initialize tests", func() {
		populateDRClusters()
		for idx := range drClusters {
			Expect(k8sClient.Create(
				context.TODO(),
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: drClusters[idx].Name}},
			)).To(Succeed())
			Expect(k8sClient.Create(context.TODO(), &drClusters[idx])).To(Succeed())
			// TODO: Validate cluster resource is reconciled
		}
	})
	Specify(`a drpolicy`, func() {
		drpolicyNumber = 0
		drpolicy = &drpolicies[drpolicyNumber]
	})

	When("a drpolicy is created specifying a cluster name and a namespace of the same name does not exist", func() {
		It("should set its validated status condition's status to false", func() {
			drp := drpolicy.DeepCopy()
			drp.Spec.DRClusters = []string{"missing", "drp-cluster0"}
			Expect(k8sClient.Create(context.TODO(), drp)).To(Succeed())
			validatedConditionExpect(drp, metav1.ConditionFalse, Ignore())
		})
	})
	Specify("drpolicy delete", func() {
		drpolicyDeleteAndConfirm(drpolicy)
		vaildateSecretDistribution(nil)
	})
	Specify("a drpolicy", func() {
		drpolicyObjectMetaReset(drpolicyNumber)
	})
	When("a 1st drpolicy is created", func() {
		It("should create a drcluster manifest work for each cluster specified in a 1st drpolicy", func() {
			drpolicyCreate(drpolicy)
			validatedConditionExpect(drpolicy, metav1.ConditionTrue, Ignore())
			vaildateSecretDistribution(drpolicies[0:1])
		})
	})
	When("a 2nd drpolicy is created specifying some clusters in a 1st drpolicy and some not", func() {
		It("should create a drcluster manifest work for each cluster specified in a 2nd drpolicy but not a 1st drpolicy",
			func() {
				drpolicyCreate(&drpolicies[1])
				validatedConditionExpect(&drpolicies[1], metav1.ConditionTrue, Ignore())
				vaildateSecretDistribution(drpolicies[0:2])
			},
		)
	})
	When("a 1st drpolicy is deleted", func() {
		It("should delete a drcluster manifest work for each cluster specified in a 1st drpolicy but not a 2nd drpolicy",
			func() {
				drpolicyDelete(drpolicy)
				vaildateSecretDistribution(drpolicies[1:2])
			},
		)
	})
	When("a 2nd drpolicy is deleted", func() {
		It("should delete a drcluster manifest work for each cluster specified in a 2nd drpolicy", func() {
			drpolicyDelete(&drpolicies[1])
			vaildateSecretDistribution(nil)
		})
	})
	Specify(`a drpolicy`, func() {
		drpolicyObjectMetaReset(drpolicyNumber)
	})
	When(`a drpolicy creation request contains an invalid scheduling interval`, func() {
		It(`should fail`, func() {
			err := func() *errors.StatusError {
				path := field.NewPath(`spec`, `schedulingInterval`)
				value := ``

				return errors.NewInvalid(
					schema.GroupKind{
						Group: ramen.GroupVersion.Group,
						Kind:  `DRPolicy`,
					},
					drpolicy.Name,
					field.ErrorList{
						field.Invalid(
							path,
							value,
							validationErrors.FailedPattern(
								path.String(),
								`body`,
								`^\d+[mhd]$`,
								value,
							).Error(),
						),
					},
				)
			}()
			drpolicy.Spec.SchedulingInterval = `3s`
			Expect(k8sClient.Create(context.TODO(), drpolicy)).To(MatchError(err))
			drpolicy.Spec.SchedulingInterval = `0`
			Expect(k8sClient.Create(context.TODO(), drpolicy)).To(MatchError(err))
		})
	})
})
