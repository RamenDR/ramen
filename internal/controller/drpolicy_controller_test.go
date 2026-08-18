// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	gomegaTypes "github.com/onsi/gomega/types"
	plrv1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	validationErrors "k8s.io/kube-openapi/pkg/validation/errors"
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
		drClusters = make([]ramen.DRCluster, 0, 5)
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

	var (
		drpolicy       *ramen.DRPolicy
		drpolicyNumber uint
	)

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

var _ = Describe("DRPolicyController NAD validation", Ordered, func() {
	const (
		clusterA = "nad-cluster-a"
		clusterB = "nad-cluster-b"
	)

	// nadEntry builds a NetworkAttachment suitable for DRClusterConfig status.
	nadEntry := func(name, ns, cniType string) ramen.NetworkAttachment {
		return ramen.NetworkAttachment{Name: name, Namespace: ns, CNIType: cniType}
	}

	// drccWith builds a minimal DRClusterConfig whose status carries the given NADs.
	drccWith := func(cluster string, nads ...ramen.NetworkAttachment) *ramen.DRClusterConfig {
		return &ramen.DRClusterConfig{
			ObjectMeta: metav1.ObjectMeta{Name: cluster},
			Status:     ramen.DRClusterConfigStatus{NetworkAttachments: nads},
		}
	}

	// nadConditionExpect polls until the DRPolicy carries the expected
	// NetworkAttachmentsValidated condition status and message.
	nadConditionExpect := func(drp *ramen.DRPolicy, status metav1.ConditionStatus,
		msgMatcher gomegaTypes.GomegaMatcher,
	) {
		Eventually(func(g Gomega) {
			g.Expect(apiReader.Get(context.TODO(), types.NamespacedName{Name: drp.Name}, drp)).To(Succeed())
			cond := util.FindCondition(drp.Status.Conditions, controllers.ConditionNetworkAttachmentsValidated)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(status))
			g.Expect(cond.Message).To(msgMatcher)
		}, timeout, interval).Should(Succeed())
	}

	// deletePolicy deletes a DRPolicy and waits for it to disappear.
	deletePolicy := func(drp *ramen.DRPolicy) {
		Expect(k8sClient.Delete(context.TODO(), drp)).To(Succeed())
		Eventually(func() bool {
			return k8serrors.IsNotFound(apiReader.Get(context.TODO(), types.NamespacedName{Name: drp.Name}, drp))
		}, timeout, interval).Should(BeTrue())
	}

	// newNetworkPolicy creates a DRPolicy spec referencing both NAD clusters
	// and a NetworkMappingRef so that NAD validation is triggered.
	newNetworkPolicy := func(name string) *ramen.DRPolicy {
		return &ramen.DRPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: ramen.DRPolicySpec{
				DRClusters:         []string{clusterA, clusterB},
				SchedulingInterval: "1h",
				NetworkMappingRef:  &corev1.LocalObjectReference{Name: "dummy-network-map"},
			},
		}
	}

	BeforeAll(func() {
		By("creating ManagedClusters and DRClusters for NAD validation tests")

		for _, name := range []string{clusterA, clusterB} {
			ensureManagedCluster(k8sClient, name)
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
			_ = k8sClient.Create(context.TODO(), ns) // ignore already-exists

			drc := &ramen.DRCluster{
				ObjectMeta: metav1.ObjectMeta{Name: name},
				Spec:       ramen.DRClusterSpec{S3ProfileName: s3Profiles[0].S3ProfileName, Region: "east"},
			}

			if err := k8sClient.Create(context.TODO(), drc); err != nil && !k8serrors.IsAlreadyExists(err) {
				Expect(err).NotTo(HaveOccurred())
			}

			updateDRClusterManifestWorkStatus(k8sClient, apiReader, name)
			updateDRClusterConfigMWStatus(k8sClient, apiReader, name)
			objectConditionExpectEventually(
				apiReader,
				drc,
				metav1.ConditionTrue,
				Equal("Succeeded"),
				Ignore(),
				ramen.DRClusterValidated,
				!ramenConfig.DrClusterOperator.DeploymentAutomationEnabled,
			)
		}
	})

	AfterAll(func() {
		By("removing DRClusters created for NAD validation tests")

		for _, name := range []string{clusterA, clusterB} {
			drc := &ramen.DRCluster{ObjectMeta: metav1.ObjectMeta{Name: name}}
			_ = k8sClient.Delete(context.TODO(), drc)
		}
	})

	AfterEach(func() {
		// Reset shared NAD map so each It starts clean.
		nadDataByCluster = map[string]*ramen.DRClusterConfig{}
	})

	When("DRPolicy has no NetworkMappingRef", func() {
		It("does not set NetworkAttachmentsValidated condition", func() {
			drp := &ramen.DRPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "nad-no-mapping"},
				Spec: ramen.DRPolicySpec{
					DRClusters:         []string{clusterA, clusterB},
					SchedulingInterval: "1h",
				},
			}
			Expect(k8sClient.Create(context.TODO(), drp)).To(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(apiReader.Get(context.TODO(), types.NamespacedName{Name: drp.Name}, drp)).To(Succeed())
				cond := util.FindCondition(drp.Status.Conditions, controllers.ConditionNetworkAttachmentsValidated)
				g.Expect(cond).To(BeNil())
				validated := util.FindCondition(drp.Status.Conditions, ramen.DRPolicyValidated)
				g.Expect(validated).NotTo(BeNil())
				g.Expect(validated.Status).To(Equal(metav1.ConditionTrue))
			}, timeout, interval).Should(Succeed())

			deletePolicy(drp)
		})
	})

	When("NADs are symmetric across both clusters", func() {
		It("sets NetworkAttachmentsValidated=True and populates NetworkPeers", func() {
			nad := nadEntry("macvlan-net", "vm-ns", "macvlan")
			nadDataByCluster[clusterA] = drccWith(clusterA, nad)
			nadDataByCluster[clusterB] = drccWith(clusterB, nad)

			drp := newNetworkPolicy("nad-symmetric")
			Expect(k8sClient.Create(context.TODO(), drp)).To(Succeed())

			nadConditionExpect(drp, metav1.ConditionTrue, ContainSubstring("symmetric"))

			Eventually(func(g Gomega) {
				g.Expect(apiReader.Get(context.TODO(), types.NamespacedName{Name: drp.Name}, drp)).To(Succeed())
				g.Expect(drp.Status.NetworkPeers).To(HaveLen(1))
				peer := drp.Status.NetworkPeers[0]
				g.Expect(peer.NADName).To(Equal("macvlan-net"))
				g.Expect(peer.NADNamespace).To(Equal("vm-ns"))
				g.Expect(peer.ClusterCNITypes).To(HaveKeyWithValue(clusterA, "macvlan"))
				g.Expect(peer.ClusterCNITypes).To(HaveKeyWithValue(clusterB, "macvlan"))
				validated := util.FindCondition(drp.Status.Conditions, ramen.DRPolicyValidated)
				g.Expect(validated).NotTo(BeNil())
				g.Expect(validated.Status).To(Equal(metav1.ConditionTrue))
			}, timeout, interval).Should(Succeed())

			deletePolicy(drp)
		})
	})

	When("a NAD is present on clusterA but missing on clusterB", func() {
		It("sets NetworkAttachmentsValidated=False and DRPolicyValidated=False with reason NADsMissing", func() {
			nadDataByCluster[clusterA] = drccWith(clusterA, nadEntry("net1", "vm-ns", "macvlan"))
			nadDataByCluster[clusterB] = drccWith(clusterB) // empty

			drp := newNetworkPolicy("nad-missing-on-b")
			Expect(k8sClient.Create(context.TODO(), drp)).To(Succeed())

			nadConditionExpect(drp, metav1.ConditionFalse, ContainSubstring("missing"))

			Eventually(func(g Gomega) {
				g.Expect(apiReader.Get(context.TODO(), types.NamespacedName{Name: drp.Name}, drp)).To(Succeed())
				validated := util.FindCondition(drp.Status.Conditions, ramen.DRPolicyValidated)
				g.Expect(validated).NotTo(BeNil())
				g.Expect(validated.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(validated.Reason).To(Equal("NADsMissing"))
				g.Expect(drp.Status.NetworkPeers).To(BeEmpty())
			}, timeout, interval).Should(Succeed())

			deletePolicy(drp)
		})
	})

	When("a NAD is present on clusterB but missing on clusterA", func() {
		It("sets NetworkAttachmentsValidated=False listing the missing NAD by name", func() {
			nadDataByCluster[clusterA] = drccWith(clusterA) // empty
			nadDataByCluster[clusterB] = drccWith(clusterB, nadEntry("bridge-net", "vm-ns", "bridge"))

			drp := newNetworkPolicy("nad-missing-on-a")
			Expect(k8sClient.Create(context.TODO(), drp)).To(Succeed())

			nadConditionExpect(drp, metav1.ConditionFalse, ContainSubstring("missing"))

			deletePolicy(drp)
		})
	})

	When("multiple NADs are all symmetric across both clusters", func() {
		It("populates NetworkPeers for each NAD", func() {
			nads := []ramen.NetworkAttachment{
				nadEntry("net1", "vm-ns", "macvlan"),
				nadEntry("net2", "vm-ns", "bridge"),
			}
			nadDataByCluster[clusterA] = drccWith(clusterA, nads...)
			nadDataByCluster[clusterB] = drccWith(clusterB, nads...)

			drp := newNetworkPolicy("nad-multi-symmetric")
			Expect(k8sClient.Create(context.TODO(), drp)).To(Succeed())

			nadConditionExpect(drp, metav1.ConditionTrue, ContainSubstring("symmetric"))

			Eventually(func(g Gomega) {
				g.Expect(apiReader.Get(context.TODO(), types.NamespacedName{Name: drp.Name}, drp)).To(Succeed())
				g.Expect(drp.Status.NetworkPeers).To(HaveLen(2))
			}, timeout, interval).Should(Succeed())

			deletePolicy(drp)
		})
	})

	When("NADs are corrected to be symmetric after an initial mismatch", func() {
		It("transitions NetworkAttachmentsValidated from False to True", func() {
			// Start with NAD only on clusterA — mismatch.
			nadDataByCluster[clusterA] = drccWith(clusterA, nadEntry("heal-net", "vm-ns", "macvlan"))
			nadDataByCluster[clusterB] = drccWith(clusterB)

			drp := newNetworkPolicy("nad-healed")
			Expect(k8sClient.Create(context.TODO(), drp)).To(Succeed())

			nadConditionExpect(drp, metav1.ConditionFalse, ContainSubstring("missing"))

			// Fix: add the NAD to clusterB, then trigger a reconcile via annotation update.
			nadDataByCluster[clusterB] = drccWith(clusterB, nadEntry("heal-net", "vm-ns", "macvlan"))

			Eventually(func(g Gomega) {
				g.Expect(apiReader.Get(context.TODO(), types.NamespacedName{Name: drp.Name}, drp)).To(Succeed())
				if drp.Annotations == nil {
					drp.Annotations = map[string]string{}
				}
				drp.Annotations["test.ramendr.io/trigger"] = "healed"
				g.Expect(k8sClient.Update(context.TODO(), drp)).To(Succeed())
			}, timeout, interval).Should(Succeed())

			nadConditionExpect(drp, metav1.ConditionTrue, ContainSubstring("symmetric"))

			deletePolicy(drp)
		})
	})
})
