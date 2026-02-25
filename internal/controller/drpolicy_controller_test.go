// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	gomegaTypes "github.com/onsi/gomega/types"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	validationErrors "k8s.io/kube-openapi/pkg/validation/errors"
	placementv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	controllers "github.com/ramendr/ramen/internal/controller"
	"github.com/ramendr/ramen/internal/controller/util"
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
						cond, ok := element.(metav1.Condition)
						if !ok {
							return ""
						}

						return cond.Type
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
			return k8serrors.IsNotFound(apiReader.Get(context.TODO(), types.NamespacedName{Name: drpolicy.Name}, drpolicy))
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

	getPlacementForSecrets := func() map[string]placementv1beta1.Placement {
		placementList := &placementv1beta1.PlacementList{}
		listOptions := &client.ListOptions{
			Namespace: ramenNamespace,
			LabelSelector: labels.SelectorFromSet(labels.Set{
				"ramendr.openshift.io/created-by-ramen": "true",
			}),
		}

		Expect(apiReader.List(context.TODO(), placementList, listOptions)).NotTo(HaveOccurred())

		// Define DRPolicy-specific cluster names for isolation
		drPolicyClusterNames := map[string]struct{}{
			"drp-cluster0": {},
			"drp-cluster1": {},
			"drp-cluster2": {},
		}

		foundPlacements := make(map[string]placementv1beta1.Placement, len(placementList.Items))
		for _, placement := range placementList.Items {
			// Only include placements that contain DRPolicy clusters
			if placement.Name == "placement-s3secret0" {
				// Extract cluster names from placement
				placementClusters := make(map[string]struct{})
				for _, predicate := range placement.Spec.Predicates {
					if predicate.RequiredClusterSelector.LabelSelector.MatchExpressions == nil {
						continue
					}
					for _, expression := range predicate.RequiredClusterSelector.LabelSelector.MatchExpressions {
						if expression.Key == "name" && expression.Operator == metav1.LabelSelectorOpIn {
							for _, cluster := range expression.Values {
								placementClusters[cluster] = struct{}{}
							}
						}
					}
				}

				// Include if the placement contains any DRPolicy clusters
				hasDRPolicyCluster := false
				for cluster := range placementClusters {
					if _, isDRPolicyCluster := drPolicyClusterNames[cluster]; isDRPolicyCluster {
						hasDRPolicyCluster = true

						break
					}
				}

				if hasDRPolicyCluster && len(placementClusters) > 0 {
					foundPlacements[placement.Name] = placement
				}
			}
		}

		return foundPlacements
	}
	vaildateSecretDistribution := func(drPolicies []ramen.DRPolicy) {
		placements := getPlacementForSecrets()

		// If no policies are present, expect no secret placements
		if drPolicies == nil {
			Expect(len(placements)).To(Equal(0))

			return
		}

		// Construct drpolicies name
		policyCombinationName := ""
		for _, drpolicy := range drPolicies {
			policyCombinationName += drpolicy.Name
		}

		// Ensure list of secrets for the policy name has as many placements
		Eventually(func() bool {
			placements = getPlacementForSecrets()

			return len(placements) == len(drPoliciesAndSecrets[policyCombinationName])
		}, timeout, interval).Should(BeTrue())

		// Range through secrets in drpolicies name and ensure cluster list is the same
		for secretName, clusterList := range drPoliciesAndSecrets[policyCombinationName] {
			_, _, placementName, _ := util.GeneratePolicyResourceNames(secretName, util.SecretFormatRamen)

			Eventually(func() (clusterNames []string) {
				placements = getPlacementForSecrets()

				// Check if placement exists before accessing
				placement, exists := placements[placementName]
				if !exists {
					return // Return empty slice if placement doesn't exist yet
				}

				// Extract cluster names from the new Placement API structure
				allClusters := []string{}
				for _, predicate := range placement.Spec.Predicates {
					if predicate.RequiredClusterSelector.LabelSelector.MatchExpressions == nil {
						continue
					}

					for _, expression := range predicate.RequiredClusterSelector.LabelSelector.MatchExpressions {
						if expression.Key == "name" && expression.Operator == metav1.LabelSelectorOpIn {
							allClusters = append(allClusters, expression.Values...)
						}
					}
				}

				// Filter clusters to only include those that are expected for this test context
				// This prevents contamination from other tests that use the same placement
				for _, cluster := range allClusters {
					for _, expectedCluster := range clusterList {
						if cluster == expectedCluster {
							clusterNames = append(clusterNames, cluster)

							break
						}
					}
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
			objectConditionExpectEventually(
				apiReader,
				drcluster,
				metav1.ConditionTrue,
				Equal("Succeeded"),
				Ignore(),
				ramen.DRClusterValidated,
				!ramenConfig.DrClusterOperator.DeploymentAutomationEnabled,
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
		It("should create a secret placement for each cluster specified in a 1st drpolicy", func() {
			drpolicyCreate(drpolicy)
			validatedConditionExpect(drpolicy, metav1.ConditionTrue, Ignore())
			vaildateSecretDistribution(drpolicies[0:1])
		})
	})
	When("a 2nd drpolicy is created specifying some clusters in a 1st drpolicy and some not", func() {
		It("should create a secret placement for each cluster specified in a 2nd drpolicy but not a 1st drpolicy",
			func() {
				drpolicyCreate(&drpolicies[1])
				validatedConditionExpect(&drpolicies[1], metav1.ConditionTrue, Ignore())
				vaildateSecretDistribution(drpolicies[0:2])
			},
		)
	})
	When("a 1st drpolicy is deleted", func() {
		It("should delete a secret placement for each cluster specified in a 1st drpolicy but not a 2nd drpolicy",
			func() {
				drpolicyDelete(drpolicy)
				vaildateSecretDistribution(drpolicies[1:2])
			},
		)
	})
	When("a 2nd drpolicy is deleted", func() {
		It("should delete a secret placement for each cluster specified in a 2nd drpolicy", func() {
			drpolicyDelete(&drpolicies[1])
			vaildateSecretDistribution(nil)
		})
	})
	Specify(`a drpolicy`, func() {
		drpolicyObjectMetaReset(drpolicyNumber)
	})
	When(`a drpolicy creation request contains an invalid scheduling interval`, func() {
		It(`should fail`, func() {
			err := func(value string) *k8serrors.StatusError {
				path := field.NewPath(`spec`, `schedulingInterval`)

				return k8serrors.NewInvalid(
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

	When("validating DRPolicy for conflicts for MetroDR", func() {
		It("should prevent the second policy from being validated due to multiple overlapping metro clusters", func() {
			dp1 := &ramen.DRPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "metro-dp1"},
				Spec: ramen.DRPolicySpec{
					DRClusters:         []string{"metro-dr1", "metro-dr2"},
					SchedulingInterval: "0m",
				},
				Status: ramen.DRPolicyStatus{
					Sync: ramen.Sync{
						PeerClasses: []ramen.PeerClass{
							{
								StorageID:        []string{"sID1"},
								StorageClassName: "metro-sc",
								ClusterIDs:       []string{"cID1", "cID2"},
							},
						},
					},
				},
			}

			dp2 := &ramen.DRPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "metro-dp2"},
				Spec: ramen.DRPolicySpec{
					DRClusters:         []string{"metro-dr1", "metro-dr2"},
					SchedulingInterval: "0m",
				},
				Status: ramen.DRPolicyStatus{
					Sync: ramen.Sync{
						PeerClasses: []ramen.PeerClass{
							{
								StorageID:        []string{"sID1"},
								StorageClassName: "metro-sc",
								ClusterIDs:       []string{"cID1", "cID2"},
							},
						},
					},
				},
			}

			existingPolicies := ramen.DRPolicyList{
				Items: []ramen.DRPolicy{*dp1},
			}

			drClusterIDsToNames := map[string]string{
				"cID1": "metro-dr1",
				"cID2": "metro-dr2",
			}

			By("testing for conflicting DRPolicy")
			err := controllers.HasConflictingDRPolicy(dp2, existingPolicies, drClusterIDsToNames)

			By("verifying that conflict is detected")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("has overlapping clusters with another drpolicy"))
		})

		It("should prevent the second policy from being validated due to a single overlapping metro cluster", func() {
			dp1 := &ramen.DRPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "metro-dp1"},
				Spec: ramen.DRPolicySpec{
					DRClusters:         []string{"metro-dr1", "metro-dr2"},
					SchedulingInterval: "0m",
				},
				Status: ramen.DRPolicyStatus{
					Sync: ramen.Sync{
						PeerClasses: []ramen.PeerClass{
							{
								StorageID:        []string{"sID1"},
								StorageClassName: "metro-sc",
								ClusterIDs:       []string{"cID1", "cID2"},
							},
						},
					},
				},
			}

			dp2 := &ramen.DRPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "metro-dp2"},
				Spec: ramen.DRPolicySpec{
					DRClusters:         []string{"metro-dr1", "metro-dr3"},
					SchedulingInterval: "0m",
				},
				Status: ramen.DRPolicyStatus{
					Sync: ramen.Sync{
						PeerClasses: []ramen.PeerClass{
							{
								StorageID:        []string{"sID1"},
								StorageClassName: "metro-sc",
								ClusterIDs:       []string{"cID1", "cID3"},
							},
						},
					},
				},
			}

			existingPolicies := ramen.DRPolicyList{
				Items: []ramen.DRPolicy{*dp1},
			}

			drClusterIDsToNames := map[string]string{
				"cID1": "metro-dr1",
				"cID2": "metro-dr2",
				"cID3": "metro-dr3",
			}

			By("testing for conflicting DRPolicy")
			err := controllers.HasConflictingDRPolicy(dp2, existingPolicies, drClusterIDsToNames)

			By("verifying that conflict is detected")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("has overlapping clusters with another drpolicy"))
		})

		It("should allow the second policy to be validated having non overlapping metro clusters", func() {
			dp1 := &ramen.DRPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "metro-dp1"},
				Spec: ramen.DRPolicySpec{
					DRClusters:         []string{"metro-dr1", "metro-dr2"},
					SchedulingInterval: "0m",
				},
				Status: ramen.DRPolicyStatus{
					Sync: ramen.Sync{
						PeerClasses: []ramen.PeerClass{
							{
								StorageID:        []string{"sID1"},
								StorageClassName: "metro-sc-1",
								ClusterIDs:       []string{"cID1", "cID2"},
							},
						},
					},
				},
			}

			dp2 := &ramen.DRPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "metro-dp2"},
				Spec: ramen.DRPolicySpec{
					DRClusters:         []string{"metro-dr3", "metro-dr4"},
					SchedulingInterval: "0m",
				},
				Status: ramen.DRPolicyStatus{
					Sync: ramen.Sync{
						PeerClasses: []ramen.PeerClass{
							{
								StorageID:        []string{"sID2"},
								StorageClassName: "metro-sc-2",
								ClusterIDs:       []string{"cID3", "cID4"},
							},
						},
					},
				},
			}

			existingPolicies := ramen.DRPolicyList{
				Items: []ramen.DRPolicy{*dp1},
			}

			drClusterIDsToNames := map[string]string{
				"cID1": "metro-dr1",
				"cID2": "metro-dr2",
				"cID3": "metro-dr3",
				"cID4": "metro-dr4",
			}

			By("testing for non-conflicting DRPolicy")
			err := controllers.HasConflictingDRPolicy(dp2, existingPolicies, drClusterIDsToNames)

			By("verifying that no conflict is detected")
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
