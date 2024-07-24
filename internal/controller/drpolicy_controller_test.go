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
	"github.com/ramendr/ramen/internal/controller/util"
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

var _ = Describe("DRPolicyController", func() {
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
		Eventually(func() bool {
			plRules = getPlRuleForSecrets()

			return len(plRules) == len(drPoliciesAndSecrets[policyCombinationName])
		}, timeout, interval).Should(BeTrue())

		// Range through secrets in drpolicies name and ensure cluster list is the same
		for secretName, clusterList := range drPoliciesAndSecrets[policyCombinationName] {
			_, _, plRuleName, _ := util.GeneratePolicyResourceNames(secretName, util.SecretFormatRamen)

			Eventually(func() (clusterNames []string) {
				plRules = getPlRuleForSecrets()
				for _, cluster := range plRules[plRuleName].Spec.Clusters {
					clusterNames = append(clusterNames, cluster.Name)
				}

				return
			}, timeout, interval).Should(ConsistOf(clusterList))
		}
	}

	clusters := [...]string{
		"drp-cluster0",
		"drp-cluster1",
		"drp-cluster2",
		"drp-cluster-late-create-0",
		"drp-cluster-late-create-1",
	}
	drClusters := []ramen.DRCluster{}
	populateDRClusters := func() {
		drClusters = nil
		drClusters = append(drClusters,
			ramen.DRCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "drp-cluster0"},
				Spec:       ramen.DRClusterSpec{S3ProfileName: s3Profiles[0].S3ProfileName, Region: "east"},
			},
			ramen.DRCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "drp-cluster1"},
				Spec:       ramen.DRClusterSpec{S3ProfileName: s3Profiles[0].S3ProfileName, Region: "west"},
			},
			ramen.DRCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "drp-cluster2"},
				Spec:       ramen.DRClusterSpec{S3ProfileName: s3Profiles[0].S3ProfileName, Region: "east"},
			},
			ramen.DRCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "drp-cluster-late-create-0"},
				Spec:       ramen.DRClusterSpec{S3ProfileName: s3Profiles[0].S3ProfileName, Region: "east"},
			},
			ramen.DRCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "drp-cluster-late-create-1"},
				Spec:       ramen.DRClusterSpec{S3ProfileName: s3Profiles[0].S3ProfileName, Region: "west"},
			},
		)
	}

	createManagedClusters := func() {
		for _, cluster := range clusters {
			ensureManagedCluster(k8sClient, cluster)
		}
	}

	createDRClusters := func(from, to int) {
		for idx := range drClusters[from:to] {
			drcluster := &drClusters[idx+from]
			Expect(k8sClient.Create(
				context.TODO(),
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: drcluster.Name}},
			)).To(Succeed())
			Expect(k8sClient.Create(context.TODO(), drcluster)).To(Succeed())
			updateDRClusterManifestWorkStatus(k8sClient, apiReader, drcluster.Name)
			updateDRClusterConfigMWStatus(k8sClient, apiReader, drcluster.Name)
			drclusterConditionExpectEventually(
				apiReader,
				drcluster,
				!ramenConfig.DrClusterOperator.DeploymentAutomationEnabled,
				metav1.ConditionTrue,
				Equal("Succeeded"),
				Ignore(),
				ramen.DRClusterValidated,
			)
		}
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
		createManagedClusters()
		createDRClusters(0, 3)
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
		It("should create a secret placement rule for each cluster specified in a 1st drpolicy", func() {
			drpolicyCreate(drpolicy)
			validatedConditionExpect(drpolicy, metav1.ConditionTrue, Ignore())
			vaildateSecretDistribution(drpolicies[0:1])
		})
	})
	When("a 2nd drpolicy is created specifying some clusters in a 1st drpolicy and some not", func() {
		It("should create a secret placement rule for each cluster specified in a 2nd drpolicy but not a 1st drpolicy",
			func() {
				drpolicyCreate(&drpolicies[1])
				validatedConditionExpect(&drpolicies[1], metav1.ConditionTrue, Ignore())
				vaildateSecretDistribution(drpolicies[0:2])
			},
		)
	})
	When("a 1st drpolicy is deleted", func() {
		It("should delete a secret placement rule for each cluster specified in a 1st drpolicy but not a 2nd drpolicy",
			func() {
				drpolicyDelete(drpolicy)
				vaildateSecretDistribution(drpolicies[1:2])
			},
		)
	})
	When("a 2nd drpolicy is deleted", func() {
		It("should delete a secret placement rule for each cluster specified in a 2nd drpolicy", func() {
			drpolicyDelete(&drpolicies[1])
			vaildateSecretDistribution(nil)
		})
	})
	Specify(`a drpolicy`, func() {
		drpolicyObjectMetaReset(drpolicyNumber)
	})
	When(`a drpolicy creation request contains an invalid scheduling interval`, func() {
		It(`should fail`, func() {
			err := func(value string) *errors.StatusError {
				path := field.NewPath(`spec`, `schedulingInterval`)

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
								`^(|\d+[mhd])$`,
								value,
							).Error(),
						),
					},
				)
			}
			drp := drpolicy.DeepCopy()
			drp.Spec.SchedulingInterval = `3s`
			Expect(k8sClient.Create(context.TODO(), drp)).To(MatchError(err(drp.Spec.SchedulingInterval)))
			drp.Spec.SchedulingInterval = `0`
			Expect(k8sClient.Create(context.TODO(), drp)).To(MatchError(err(drp.Spec.SchedulingInterval)))
		})
	})
	When("a drpolicy is created before DRClusters are created", func() {
		It("should start as invalidated and transition to validated", func() {
			drp := drpolicy.DeepCopy()
			drp.Spec.DRClusters = clusters[3:5]
			By("creating the DRPolicy first")
			Expect(k8sClient.Create(context.TODO(), drp)).To(Succeed())
			By("ensuring DRPolicy is not validated")
			validatedConditionExpect(drp, metav1.ConditionFalse, Ignore())
			By("creating the DRClusters")
			createDRClusters(3, 5)
			By("ensuring DRPolicy is validated")
			validatedConditionExpect(drp, metav1.ConditionTrue, Ignore())
			drpolicyDeleteAndConfirm(drp)
			vaildateSecretDistribution(nil)
		})
	})
})
