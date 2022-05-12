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

package controllers_test

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	gomegaTypes "github.com/onsi/gomega/types"
	workv1 "github.com/open-cluster-management/api/work/v1"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers"
	"github.com/ramendr/ramen/controllers/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("DRClusterController", func() {
	conditionExpect := func(drcluster *ramen.DRCluster, disabled bool, status metav1.ConditionStatus,
		reasonMatcher, messageMatcher gomegaTypes.GomegaMatcher, conditionType string,
	) {
		Eventually(
			func(g Gomega) {
				g.Expect(apiReader.Get(
					context.TODO(),
					types.NamespacedName{Namespace: drcluster.Namespace, Name: drcluster.Name},
					drcluster,
				)).To(Succeed())
				g.Expect(drcluster.Status.Conditions).To(MatchElements(
					func(element interface{}) string {
						return element.(metav1.Condition).Type
					},
					IgnoreExtras,
					Elements{
						conditionType: MatchAllFields(Fields{
							`Type`:               Ignore(),
							`Status`:             Equal(status),
							`ObservedGeneration`: Equal(drcluster.Generation),
							`LastTransitionTime`: Ignore(),
							`Reason`:             reasonMatcher,
							`Message`:            messageMatcher,
						}),
					},
				))
				// TODO: Validate finaliziers and labels
				if status == metav1.ConditionFalse {
					return
				}

				expectedCount := 8
				if disabled {
					expectedCount = 2
				}
				clusterName := drcluster.Name
				manifestWork := &workv1.ManifestWork{}
				g.Expect(apiReader.Get(
					context.TODO(),
					types.NamespacedName{
						Name:      util.DrClusterManifestWorkName,
						Namespace: clusterName,
					},
					manifestWork,
				)).To(Succeed())
				g.Expect(manifestWork.Spec.Workload.Manifests).To(HaveLen(expectedCount))
				// TODO: Validate fencing status
			},
			timeout,
			interval,
		).Should(Succeed())
	}

	drclusterDelete := func(drcluster *ramen.DRCluster) {
		clusterName := drcluster.Name
		Expect(k8sClient.Delete(context.TODO(), drcluster)).To(Succeed())
		Eventually(func() bool {
			return errors.IsNotFound(apiReader.Get(context.TODO(), types.NamespacedName{
				Namespace: drcluster.Namespace,
				Name:      drcluster.Name,
			}, drcluster))
		}, timeout, interval).Should(BeTrue())
		manifestWork := &workv1.ManifestWork{}
		Expect(errors.IsNotFound(apiReader.Get(
			context.TODO(),
			types.NamespacedName{
				Name:      util.DrClusterManifestWorkName,
				Namespace: clusterName,
			},
			manifestWork))).To(BeTrue())
	}

	cidrs := [][]string{
		{"198.51.100.17/24", "198.51.100.18/24", "198.51.100.19/24"}, // valid CIDR
		{"1111.51.100.14/24", "aaa.51.100.15/24", "00.51.100.16/24"}, // invalid CIDR

		{"198.51.100.20/24", "198.51.100.21/24", "198.51.100.22/24"}, // valid CIDR
		{"198.51.100.23/24", "198.51.100.24/24", "198.51.100.25/24"}, // valid CIDR
	}

	drclusters := []ramen.DRCluster{}
	populateDRClusters := func() {
		drclusters = nil
		drclusters = append(drclusters,
			ramen.DRCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "drc-cluster0",
					Namespace: ramenNamespace,
				},
				Spec: ramen.DRClusterSpec{
					S3ProfileName: s3Profiles[0].S3ProfileName,
					CIDRs:         cidrs[0],
					Region:        "east",
				},
			},
			ramen.DRCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "drc-cluster1",
					Namespace: ramenNamespace,
				},
				Spec: ramen.DRClusterSpec{
					S3ProfileName: s3Profiles[0].S3ProfileName,
					CIDRs:         cidrs[2],
					Region:        "east",
				},
			},
		)
	}

	syncDRPolicy := &ramen.DRPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sync-drpolicy-drcluster-tests",
		},
		Spec: ramen.DRPolicySpec{
			DRClusters:         []string{"drc-cluster0", "drc-cluster1"},
			SchedulingInterval: schedulingInterval,
		},
	}

	createDRClusterNamespaces := func() {
		for _, drcluster := range drclusters {
			Expect(k8sClient.Create(
				context.TODO(),
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: drcluster.Name}},
			)).To(Succeed())
		}
	}

	deleteDRClusterNamespaces := func() {
		for _, drcluster := range drclusters {
			Expect(k8sClient.Delete(
				context.TODO(),
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: drcluster.Name}},
			)).To(Succeed())
		}
	}

	createPolicies := func() {
		policy := syncDRPolicy.DeepCopy()
		Expect(k8sClient.Create(
			context.TODO(), policy)).To(Succeed())
	}

	createOtherDRClusters := func() {
		for i := range drclusters {
			if i == 0 {
				continue
			}
			cluster := drclusters[i].DeepCopy()
			Expect(k8sClient.Create(context.TODO(), cluster)).To(Succeed())
		}
	}

	deleteOtherDRClusters := func() {
		for i := range drclusters {
			if i == 0 {
				continue
			}
			cluster := drclusters[i].DeepCopy()
			Expect(k8sClient.Delete(context.TODO(), cluster)).To(Succeed())
		}
	}

	drpolicyDeleteAndConfirm := func(drpolicy *ramen.DRPolicy) {
		policy := drpolicy.DeepCopy()
		Expect(k8sClient.Delete(context.TODO(), policy)).To(Succeed())
		Eventually(func() bool {
			return errors.IsNotFound(apiReader.Get(context.TODO(), types.NamespacedName{Name: policy.Name}, policy))
		}, timeout, interval).Should(BeTrue())
	}

	drpolicyDelete := func(drpolicy *ramen.DRPolicy) {
		drpolicyDeleteAndConfirm(drpolicy)
	}

	var drcluster *ramen.DRCluster
	Specify("initialize tests", func() {
		populateDRClusters()
		createDRClusterNamespaces()
		createOtherDRClusters()
	})

	Context("DRCluster resource S3Profile validation", func() {
		Specify("create a drcluster copy for changes", func() {
			createPolicies()
			drcluster = drclusters[0].DeepCopy()
		})
		When("an S3Profile is missing in config", func() {
			It("reports NOT validated with reason s3ConnectionFailed", func() {
				By("creating a new DRCluster with an invalid S3Profile")
				drcluster.Spec.S3ProfileName = "missing"
				Expect(k8sClient.Create(context.TODO(), drcluster)).To(Succeed())
				conditionExpect(drcluster, false, metav1.ConditionFalse, Equal("s3ConnectionFailed"), Ignore(),
					ramen.DRClusterValidated)
			})
		})
		When("an S3Profile fails listing", func() {
			It("reports NOT validated with reason s3ListFailed", func() {
				By("modifying a DRCluster with an invalid S3Profile that fails listing")
				drcluster.Spec.S3ProfileName = s3Profiles[4].S3ProfileName
				Expect(k8sClient.Update(context.TODO(), drcluster)).To(Succeed())
				conditionExpect(drcluster, false, metav1.ConditionFalse, Equal("s3ListFailed"), Ignore(),
					ramen.DRClusterValidated)
			})
		})
		When("fenced", func() {
			It("reports validated with reason Succeeded and ignores S3Profile errors", func() {
				By("fencing an existing DRCluster with an invalid S3Profile")
				drcluster.Spec.ClusterFence = ramen.ClusterFenceStateManuallyFenced
				Expect(k8sClient.Update(context.TODO(), drcluster)).To(Succeed())
				conditionExpect(drcluster, false, metav1.ConditionTrue,
					Equal(controllers.DRClusterConditionReasonFenced), Ignore(),
					ramen.DRClusterConditionTypeFenced)
			})
		})
		When("S3Profile is valid", func() {
			It("reports validated with reason Succeeded", func() {
				By("modifying a DRCluster with a valid S3Profile and no cluster fencing")
				drcluster.Spec.S3ProfileName = s3Profiles[0].S3ProfileName
				drcluster.Spec.ClusterFence = ""
				Expect(k8sClient.Update(context.TODO(), drcluster)).To(Succeed())
				conditionExpect(drcluster, false, metav1.ConditionTrue, Equal("Succeeded"), Ignore(),
					ramen.DRClusterValidated)
			})
		})
		When("S3Profile is changed to an invalid profile in ramen config", func() {
			It("reports NOT validated with reason s3ConnectionFailed", func() {
				By("modifying a DRCluster with the new valid S3Profile")
				drcluster.Spec.S3ProfileName = s3Profiles[5].S3ProfileName
				Expect(k8sClient.Update(context.TODO(), drcluster)).To(Succeed())
				conditionExpect(drcluster, false, metav1.ConditionTrue, Equal("Succeeded"), Ignore(),
					ramen.DRClusterValidated)
				By("changing the S3Profile in ramen config to an invalid value")
				newS3Profiles := s3Profiles[0:]
				s3Profiles[5].S3Bucket = bucketNameFail
				s3ProfilesStore(newS3Profiles)
				conditionExpect(drcluster, false, metav1.ConditionFalse, Equal("s3ConnectionFailed"), Ignore(),
					ramen.DRClusterValidated)
				// TODO: Ensure when changing S3Profile, dr-cluster's ramen config is updated in MW
			})
		})
		When("S3Profile is changed to an invalid profile in DRCluster", func() {
			It("reports NOT validated with reason s3ListFailed", func() {
				By("modifying a DRCluster with a valid S3Profile")
				drcluster.Spec.S3ProfileName = s3Profiles[0].S3ProfileName
				Expect(k8sClient.Update(context.TODO(), drcluster)).To(Succeed())
				conditionExpect(drcluster, false, metav1.ConditionTrue, Equal("Succeeded"), Ignore(),
					ramen.DRClusterValidated)
				By("modifying a DRCluster with an invalid S3Profile that fails listing")
				drcluster.Spec.S3ProfileName = s3Profiles[4].S3ProfileName
				Expect(k8sClient.Update(context.TODO(), drcluster)).To(Succeed())
				conditionExpect(drcluster, false, metav1.ConditionFalse, Equal("s3ListFailed"), Ignore(),
					ramen.DRClusterValidated)
			})
		})
		When("deleting a DRCluster with an invalid s3Profile", func() {
			It("is successful", func() {
				drpolicyDelete(syncDRPolicy)
				drclusterDelete(drcluster)
			})
		})
	})

	Context("DRCluster resource CIDR validation", func() {
		Specify("create a drcluster copy for changes", func() {
			createPolicies()
			drcluster = drclusters[0].DeepCopy()
		})
		When("provided CIDR value is incorrect", func() {
			It("reports NOT validated with reason ValidationFailed", func() {
				By("creating a new DRCluster with an invalid CIDR")
				drcluster.Spec.CIDRs = cidrs[1]
				Expect(k8sClient.Create(context.TODO(), drcluster)).To(Succeed())
				conditionExpect(drcluster, false, metav1.ConditionFalse, Equal("ValidationFailed"), Ignore(),
					ramen.DRClusterValidated)
			})
		})
		When("provided CIDR value is changed to be correct", func() {
			It("reports validated", func() {
				drcluster.Spec.CIDRs = cidrs[0]
				Expect(k8sClient.Update(context.TODO(), drcluster)).To(Succeed())
				conditionExpect(drcluster, false, metav1.ConditionTrue, Equal("Succeeded"), Ignore(),
					ramen.DRClusterValidated)
			})
		})
		When("deleting a DRCluster with a valid CIDR value", func() {
			It("is successful", func() {
				// For DRCluster deletion, it should not be referenced
				// by any DRPolicy. Hence remove the DRPolicy as well
				// before deleting DRCluster
				drpolicyDelete(syncDRPolicy)
				drclusterDelete(drcluster)

				// recreate DRPolicy
				createPolicies()
				drcluster = drclusters[0].DeepCopy()
			})
		})
		When("provided CIDR value is correct", func() {
			It("reports validated", func() {
				By("creating a new DRCluster with an valid CIDR")
				Expect(k8sClient.Create(context.TODO(), drcluster)).To(Succeed())
				conditionExpect(drcluster, false, metav1.ConditionTrue, Equal("Succeeded"), Ignore(),
					ramen.DRClusterValidated)
			})
		})
		When("provided CIDR value is changed to be incorrect", func() {
			It("reports NOT validated with reason ValidationFailed", func() {
				drcluster.Spec.CIDRs = cidrs[1]
				Expect(k8sClient.Update(context.TODO(), drcluster)).To(Succeed())
				conditionExpect(drcluster, false, metav1.ConditionFalse,
					Equal("ValidationFailed"), Ignore(), ramen.DRClusterValidated)
			})
		})
		When("deleting a DRCluster with an invalid CIDR value", func() {
			It("is successful", func() {
				drpolicyDelete(syncDRPolicy)
				drclusterDelete(drcluster)
			})
		})
	})

	Context("DRCluster resource fencing validation", func() {
		Specify("create a drcluster copy for changes", func() {
			createPolicies()
			drcluster = drclusters[0].DeepCopy()
		})
		When("provided Fencing value is empty", func() {
			It("reports validated with status fencing as Unfenced", func() {
				drcluster.Spec.ClusterFence = ""
				Expect(k8sClient.Create(context.TODO(), drcluster)).To(Succeed())
				conditionExpect(drcluster, false, metav1.ConditionFalse,
					Equal(controllers.DRClusterConditionReasonClean), Ignore(),
					ramen.DRClusterConditionTypeFenced)
			})
		})
		When("provided Fencing value is Unfenced", func() {
			It("reports validated with status fencing as Unfenced", func() {
				drcluster.Spec.ClusterFence = "Unfenced"
				Expect(k8sClient.Update(context.TODO(), drcluster)).To(Succeed())
				// When Unfence is set, DRCluster controller first unfences the
				// cluster (i.e. itself through a peer cluster) and then cleans
				// up the fencing resource. So, by the time this check is made,
				// either the cluster should have been unfenced or completely
				// cleaned
				conditionExpect(drcluster, false, metav1.ConditionFalse,
					BeElementOf(controllers.DRClusterConditionReasonUnfenced, controllers.DRClusterConditionReasonCleaning,
						controllers.DRClusterConditionReasonClean),
					Ignore(), ramen.DRClusterConditionTypeFenced)
			})
		})
		When("provided Fencing value is ManuallyFenced", func() {
			It("reports validated with status fencing as Fenced", func() {
				drcluster.Spec.ClusterFence = "ManuallyFenced"
				Expect(k8sClient.Update(context.TODO(), drcluster)).To(Succeed())
				conditionExpect(drcluster, false, metav1.ConditionTrue,
					Equal(controllers.DRClusterConditionReasonFenced), Ignore(),
					ramen.DRClusterConditionTypeFenced)
			})
		})
		When("provided Fencing value is Fenced", func() {
			It("reports NOT validated with reason FencingHandlingFailed", func() {
				drcluster.Spec.ClusterFence = "Fenced"
				Expect(k8sClient.Update(context.TODO(), drcluster)).To(Succeed())
				conditionExpect(drcluster, false, metav1.ConditionTrue,
					Equal(controllers.DRClusterConditionReasonFenced), Ignore(),
					ramen.DRClusterConditionTypeFenced)
			})
		})
		When("deleting a DRCluster with an invalid fencing status", func() {
			It("is successful", func() {
				drpolicyDelete(syncDRPolicy)
				drclusterDelete(drcluster)
			})
		})
	})

	Context("DRCluster resource cluster name validation", func() {
		Specify("create a drcluster copy for changes", func() {
			createPolicies()
			drcluster = drclusters[0].DeepCopy()
		})
		// TODO: We need ManagedCluster validation and tests, just not namespace validation
		When("provided resource name is NOT an existing namespace", func() {
			It("reports NOT validated with reason DrClustersDeployFailed", func() {
				drcluster.Name = "drc-missing"
				Expect(k8sClient.Create(context.TODO(), drcluster)).To(Succeed())
				conditionExpect(drcluster, false, metav1.ConditionFalse, Equal("DrClustersDeployFailed"), Ignore(),
					ramen.DRClusterValidated)
				drclusterDelete(drcluster)
			})
		})
		When("provided resource name is an existing namespace", func() {
			It("reports validated", func() {
				drcluster = drclusters[0].DeepCopy()
				Expect(k8sClient.Create(context.TODO(), drcluster)).To(Succeed())
				conditionExpect(drcluster, false, metav1.ConditionTrue, Equal("Succeeded"), Ignore(),
					ramen.DRClusterValidated)
			})
		})
		When("deleting a DRCluster with all valid values", func() {
			It("is successful", func() {
				drpolicyDelete(syncDRPolicy)
				drclusterDelete(drcluster)
			})
		})
	})

	Context("DRCluster resource configuration deployment automation", func() {
		Specify("create a drcluster copy for changes", func() {
			drcluster = drclusters[0].DeepCopy()
		})
		// TODO: We need ManagedCluster validation and tests, just not namespace validation
		// TODO: Should this depend on referencing DRPolicies, and if they exist leave it as is?
		When("provided resource name is a namespace and configuration automation is turned off", func() {
			It("does NOT create Subscription related manifests", func() {
				By("creating a valid DRCluster")
				Expect(k8sClient.Create(context.TODO(), drcluster)).To(Succeed())
				conditionExpect(drcluster, false, metav1.ConditionTrue, Equal("Succeeded"), Ignore(),
					ramen.DRClusterValidated)
				ramenConfig.DrClusterOperator.DeploymentAutomationEnabled = false
				ramenConfig.DrClusterOperator.S3SecretDistributionEnabled = false
				configMapUpdate()
				conditionExpect(drcluster, true, metav1.ConditionTrue, Equal("Succeeded"), Ignore(),
					ramen.DRClusterValidated)
				ramenConfig.DrClusterOperator.DeploymentAutomationEnabled = true
				ramenConfig.DrClusterOperator.S3SecretDistributionEnabled = true
				configMapUpdate()
			})
		})
		When("deleting a DRCluster with all valid values", func() {
			It("is successful", func() {
				drclusterDelete(drcluster)
				deleteOtherDRClusters()
				deleteDRClusterNamespaces()
			})
		})
	})

	Context("DRCluster resource deletion validation", func() {
		// TODO: We need ManagedCluster validation and tests, just not namespace validation
		When("deleting a DRCluster that has DRPolicy references to it", func() {
			It("is not deleted", func() {
			})
			When("the referencing DRPolicy is deleted", func() {
				It("is deleted", func() {
				})
			})
		})
	})

	// TODO s3Secret missing/failing/deleted/recreated
})
