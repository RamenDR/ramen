// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"

	csiaddonsv1alpha1 "github.com/csi-addons/kubernetes-csi-addons/apis/csiaddons/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
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
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
)

var baseNF = &csiaddonsv1alpha1.NetworkFence{
	TypeMeta:   metav1.TypeMeta{Kind: "NetworkFence", APIVersion: "csiaddons.openshift.io/v1alpha1"},
	ObjectMeta: metav1.ObjectMeta{Name: "network-fence-drc-cluster0"},
	Spec: csiaddonsv1alpha1.NetworkFenceSpec{
		Cidrs:      []string{"198.51.100.17/24", "198.51.100.18/24", "198.51.100.19/24"},
		FenceState: csiaddonsv1alpha1.Fenced,
	},
}

func (f FakeMCVGetter) GetNFFromManagedCluster(resourceName, resourceNamespace, managedCluster string,
	annotations map[string]string,
) (*csiaddonsv1alpha1.NetworkFence, error) {
	nfStatus := csiaddonsv1alpha1.NetworkFenceStatus{
		Result:  csiaddonsv1alpha1.FencingOperationResultSucceeded,
		Message: "Success",
	}

	nf := baseNF.DeepCopy()

	callerName := getFunctionNameAtIndex(2)
	if callerName == "fenceClusterOnCluster" {
		nf.Spec.FenceState = csiaddonsv1alpha1.Fenced
	} else if callerName == "unfenceClusterOnCluster" {
		nf.Spec.FenceState = csiaddonsv1alpha1.Unfenced
	}

	nf.Status = nfStatus

	nf.Generation = 1

	return nf, nil
}

func (f FakeMCVGetter) DeleteNFManagedClusterView(
	resourceName, resourceNamespace, clusterName, resourceType string,
) error {
	return nil
}

func drclusterConditionExpectEventually(
	drcluster *ramen.DRCluster,
	disabled bool,
	status metav1.ConditionStatus,
	reasonMatcher,
	messageMatcher gomegaTypes.GomegaMatcher,
	conditionType string,
) {
	drclusterConditionExpect(
		drcluster,
		disabled,
		status,
		reasonMatcher,
		messageMatcher,
		conditionType,
		false,
	)
}

func drclusterConditionExpectConsistently(
	drcluster *ramen.DRCluster,
	disabled bool,
	reasonMatcher,
	messageMatcher gomegaTypes.GomegaMatcher,
) {
	drclusterConditionExpect(
		drcluster,
		disabled,
		metav1.ConditionTrue,
		reasonMatcher,
		messageMatcher,
		ramen.DRClusterValidated,
		true,
	)
}

func drclusterConditionExpect(
	drcluster *ramen.DRCluster,
	disabled bool,
	status metav1.ConditionStatus,
	reasonMatcher,
	messageMatcher gomegaTypes.GomegaMatcher,
	conditionType string,
	always bool,
) {
	testFunc := func() []metav1.Condition {
		Expect(apiReader.Get(context.TODO(), types.NamespacedName{
			Namespace: drcluster.Namespace,
			Name:      drcluster.Name,
		}, drcluster)).To(Succeed())

		return drcluster.Status.Conditions
	}

	matchElements := MatchElements(
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
	)

	switch always {
	case false:
		Eventually(testFunc, timeout, interval).Should(matchElements)
	case true:
		Consistently(testFunc, timeout, interval).Should(matchElements)
	}

	// TODO: Validate finaliziers and labels
	if status == metav1.ConditionFalse {
		return
	}

	validateClusterManifest(drcluster, disabled)
}

func validateClusterManifest(drcluster *ramen.DRCluster, disabled bool) {
	expectedCount := 10
	if disabled {
		expectedCount = 4
	}

	Eventually(
		func(g Gomega) []workv1.Manifest {
			clusterName := drcluster.Name
			manifestWork := &workv1.ManifestWork{}
			g.Expect(apiReader.Get(context.TODO(), types.NamespacedName{
				Name:      util.DrClusterManifestWorkName,
				Namespace: clusterName,
			}, manifestWork)).To(Succeed())

			return manifestWork.Spec.Workload.Manifests
		}, timeout, interval,
	).Should(HaveLen(expectedCount))
	// TODO: Validate fencing status
}

func inspectClusterManifestSubscriptionCSV(match bool, value string, drcluster *ramen.DRCluster) {
	mwu := &util.MWUtil{
		Client:          k8sClient,
		APIReader:       apiReader,
		Ctx:             context.TODO(),
		Log:             ctrl.Log.WithName("MWUtilTest"),
		InstName:        "",
		TargetNamespace: "",
	}

	sub, err := controllers.SubscriptionFromDrClusterManifestWork(mwu, drcluster.Name)
	Expect(err).Should(BeNil())
	Expect(sub).ShouldNot(BeNil())

	switch match {
	case true:
		Expect(value == sub.Spec.StartingCSV).To(BeTrue())
	case false:
		Expect(value == sub.Spec.StartingCSV).To(BeFalse())
	}
}

var _ = Describe("DRClusterController", func() {
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
					Name: "drc-cluster0",
					Annotations: map[string]string{
						"drcluster.ramendr.openshift.io/storage-secret-name":      "tmp",
						"drcluster.ramendr.openshift.io/storage-secret-namespace": "tmp",
						"drcluster.ramendr.openshift.io/storage-clusterid":        "tmp",
						"drcluster.ramendr.openshift.io/storage-driver":           "tmp.storage.com",
					},
				},
				Spec: ramen.DRClusterSpec{
					S3ProfileName: s3Profiles[0].S3ProfileName,
					CIDRs:         cidrs[0],
					Region:        "east",
				},
			},
			ramen.DRCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "drc-cluster1",
					Annotations: map[string]string{
						"drcluster.ramendr.openshift.io/storage-secret-name":      "tmp",
						"drcluster.ramendr.openshift.io/storage-secret-namespace": "tmp",
						"drcluster.ramendr.openshift.io/storage-clusterid":        "tmp",
						"drcluster.ramendr.openshift.io/storage-driver":           "tmp.storage.com",
					},
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

	namespaceDeletionConfirm := func(name string) {
		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
		Eventually(func() error {
			return k8sClient.Get(context.TODO(), types.NamespacedName{Name: namespace.Name}, namespace)
		}, timeout, interval).Should(
			MatchError(errors.NewNotFound(schema.GroupResource{Resource: "namespaces"}, namespace.Name)),
			"%v", namespace,
		)
	}

	deleteDRClusterNamespaces := func() {
		if !namespaceDeletionSupported {
			return
		}
		for _, drcluster := range drclusters {
			Expect(k8sClient.Delete(
				context.TODO(),
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: drcluster.Name}},
			)).To(Succeed())
		}
		for i := range drclusters {
			namespaceDeletionConfirm(drclusters[i].Name)
		}
	}

	createPolicies := func() {
		policy := syncDRPolicy.DeepCopy()
		Expect(k8sClient.Create(
			context.TODO(), policy)).To(Succeed())
	}

	createOtherDRClusters := func() {
		for i := 1; i < len(drclusters); i++ {
			cluster := drclusters[i].DeepCopy()
			Expect(k8sClient.Create(context.TODO(), cluster)).To(Succeed())
			updateDRClusterManifestWorkStatus(cluster.Name)
		}
	}

	deleteOtherDRClusters := func() {
		for i := 1; i < len(drclusters); i++ {
			cluster := drclusters[i].DeepCopy()
			drclusterDelete(cluster)
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
	Specify("DRCluster initialize tests", func() {
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
				updateDRClusterManifestWorkStatus(drcluster.Name)
				drclusterConditionExpectEventually(drcluster, false, metav1.ConditionFalse, Equal("s3ConnectionFailed"), Ignore(),
					ramen.DRClusterValidated)
			})
		})
		When("an S3Profile fails listing", func() {
			It("reports NOT validated with reason s3ListFailed", func() {
				By("modifying a DRCluster with an invalid S3Profile that fails listing")
				drcluster.Spec.S3ProfileName = s3Profiles[4].S3ProfileName
				drcluster = updateDRClusterParameters(drcluster)
				drclusterConditionExpectEventually(drcluster, false, metav1.ConditionFalse, Equal("s3ListFailed"), Ignore(),
					ramen.DRClusterValidated)
			})
		})
		When("fenced", func() {
			It("reports validated with reason Succeeded and ignores S3Profile errors", func() {
				By("fencing an existing DRCluster with an invalid S3Profile")
				drcluster.Spec.ClusterFence = ramen.ClusterFenceStateManuallyFenced
				drcluster = updateDRClusterParameters(drcluster)
				drclusterConditionExpectEventually(drcluster, false, metav1.ConditionTrue,
					Equal(controllers.DRClusterConditionReasonFenced), Ignore(),
					ramen.DRClusterConditionTypeFenced)
			})
		})
		When("S3Profile is valid", func() {
			It("reports validated with reason Succeeded", func() {
				By("modifying a DRCluster with a valid S3Profile and no cluster fencing")
				drcluster.Spec.S3ProfileName = s3Profiles[0].S3ProfileName
				drcluster.Spec.ClusterFence = ""
				drcluster = updateDRClusterParameters(drcluster)
				drclusterConditionExpectEventually(drcluster, false, metav1.ConditionTrue, Equal("Succeeded"), Ignore(),
					ramen.DRClusterValidated)
			})
		})
		When("S3Profile is changed to an invalid profile in ramen config", func() {
			It("reports NOT validated with reason s3ConnectionFailed", func() {
				By("modifying a DRCluster with the new valid S3Profile")
				drcluster.Spec.S3ProfileName = s3Profiles[5].S3ProfileName
				drcluster = updateDRClusterParameters(drcluster)
				drclusterConditionExpectEventually(drcluster, false, metav1.ConditionTrue, Equal("Succeeded"), Ignore(),
					ramen.DRClusterValidated)
				By("changing the S3Profile in ramen config to an invalid value")
				newS3Profiles := s3Profiles[0:]
				s3Profiles[5].S3Bucket = bucketNameFail
				s3ProfilesStore(newS3Profiles)
				drclusterConditionExpectEventually(drcluster, false, metav1.ConditionFalse, Equal("s3ConnectionFailed"), Ignore(),
					ramen.DRClusterValidated)
				// TODO: Ensure when changing S3Profile, dr-cluster's ramen config is updated in MW
			})
		})
		When("S3Profile is changed to an invalid profile in DRCluster", func() {
			It("reports NOT validated with reason s3ListFailed", func() {
				By("modifying a DRCluster with a valid S3Profile")
				drcluster.Spec.S3ProfileName = s3Profiles[0].S3ProfileName
				drcluster = updateDRClusterParameters(drcluster)
				drclusterConditionExpectEventually(drcluster, false, metav1.ConditionTrue, Equal("Succeeded"), Ignore(),
					ramen.DRClusterValidated)
				By("modifying a DRCluster with an invalid S3Profile that fails listing")
				drcluster.Spec.S3ProfileName = s3Profiles[4].S3ProfileName
				drcluster = updateDRClusterParameters(drcluster)
				drclusterConditionExpectEventually(drcluster, false, metav1.ConditionFalse, Equal("s3ListFailed"), Ignore(),
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
				drclusterConditionExpectEventually(drcluster, false, metav1.ConditionFalse, Equal("ValidationFailed"), Ignore(),
					ramen.DRClusterValidated)
				updateDRClusterManifestWorkStatus(drcluster.Name)
			})
		})
		When("provided CIDR value is changed to be correct", func() {
			It("reports validated", func() {
				drcluster.Spec.CIDRs = cidrs[0]
				drcluster = updateDRClusterParameters(drcluster)
				drclusterConditionExpectEventually(drcluster, false, metav1.ConditionTrue, Equal("Succeeded"), Ignore(),
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
				updateDRClusterManifestWorkStatus(drcluster.Name)
				drclusterConditionExpectEventually(drcluster, false, metav1.ConditionTrue, Equal("Succeeded"), Ignore(),
					ramen.DRClusterValidated)
			})
		})
		When("provided CIDR value is changed to be incorrect", func() {
			It("reports NOT validated with reason ValidationFailed", func() {
				drcluster.Spec.CIDRs = cidrs[1]
				drcluster = updateDRClusterParameters(drcluster)
				drclusterConditionExpectEventually(drcluster, false, metav1.ConditionFalse,
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
		When("provided Fencing value is ManuallyFenced", func() {
			It("reports validated with status fencing as Fenced", func() {
				drcluster.Spec.ClusterFence = ramen.ClusterFenceStateManuallyFenced
				Expect(k8sClient.Create(context.TODO(), drcluster)).To(Succeed())
				updateDRClusterManifestWorkStatus(drcluster.Name)
				drclusterConditionExpectEventually(drcluster, false, metav1.ConditionTrue,
					Equal(controllers.DRClusterConditionReasonFenced), Ignore(),
					ramen.DRClusterConditionTypeFenced)
			})
		})
		When("provided Fencing value is ManuallyUnfenced", func() {
			It("reports validated with status fencing as Unfenced", func() {
				drcluster.Spec.ClusterFence = ramen.ClusterFenceStateManuallyUnfenced
				drcluster = updateDRClusterParameters(drcluster)
				drclusterConditionExpectEventually(drcluster, false, metav1.ConditionFalse,
					Equal(controllers.DRClusterConditionReasonClean), Ignore(),
					ramen.DRClusterConditionTypeFenced)
			})
		})
		When("provided Fencing value is Fenced with an empty CIDR", func() {
			It("reports error in generating networkFence", func() {
				drcluster.Spec.ClusterFence = ramen.ClusterFenceStateFenced
				drcluster.Spec.CIDRs = []string{}
				drcluster = updateDRClusterParameters(drcluster)
				drclusterConditionExpectEventually(drcluster, false, metav1.ConditionFalse,
					Equal(controllers.DRClusterConditionReasonFenceError), Ignore(),
					ramen.DRClusterConditionTypeFenced)
			})
		})
		When("CIDRs value is provided", func() {
			It("reports validated", func() {
				drcluster.Spec.ClusterFence = ramen.ClusterFenceStateFenced
				drcluster.Spec.CIDRs = cidrs[0]
				drcluster = updateDRClusterParameters(drcluster)
				drclusterConditionExpectEventually(drcluster, false, metav1.ConditionTrue, Equal("Succeeded"), Ignore(),
					ramen.DRClusterValidated)
			})
		})
		When("provided Fencing value is Fenced", func() {
			It("reports fenced with reason Fencing success", func() {
				drcluster.Spec.ClusterFence = ramen.ClusterFenceStateFenced
				drcluster = updateDRClusterParameters(drcluster)
				drclusterConditionExpectEventually(drcluster, false, metav1.ConditionTrue,
					Equal(controllers.DRClusterConditionReasonFenced), Ignore(),
					ramen.DRClusterConditionTypeFenced)
			})
		})
		When("provided Fencing value is Unfenced", func() {
			It("reports Unfenced false with status fenced as false", func() {
				drcluster.Spec.ClusterFence = ramen.ClusterFenceStateUnfenced
				drcluster = updateDRClusterParameters(drcluster)
				// When Unfence is set, DRCluster controller first unfences the
				// cluster (i.e. itself through a peer cluster) and then cleans
				// up the fencing resource. So, by the time this check is made,
				// either the cluster should have been unfenced or completely
				// cleaned
				drclusterConditionExpectEventually(drcluster, false, metav1.ConditionFalse,
					BeElementOf(controllers.DRClusterConditionReasonUnfenced, controllers.DRClusterConditionReasonCleaning,
						controllers.DRClusterConditionReasonClean),
					Ignore(), ramen.DRClusterConditionTypeFenced)
			})
		})
		When("provided Fencing value is Fenced and the s3 validation fails", func() {
			It("reports fenced with reason Fencing success but validated condition should be false", func() {
				drcluster.Spec.ClusterFence = ramen.ClusterFenceStateFenced
				drcluster.Spec.S3ProfileName = s3Profiles[4].S3ProfileName
				drcluster = updateDRClusterParameters(drcluster)
				drclusterConditionExpectEventually(drcluster, false, metav1.ConditionTrue,
					Equal(controllers.DRClusterConditionReasonFenced), Ignore(),
					ramen.DRClusterConditionTypeFenced)
			})
		})
		When("provided Fencing value is Unfenced", func() {
			It("reports Unfenced false with status fenced as false", func() {
				drcluster.Spec.ClusterFence = ramen.ClusterFenceStateUnfenced
				drcluster = updateDRClusterParameters(drcluster)
				// When Unfence is set, DRCluster controller first unfences the
				// cluster (i.e. itself through a peer cluster) and then cleans
				// up the fencing resource. So, by the time this check is made,
				// either the cluster should have been unfenced or completely
				// cleaned
				drclusterConditionExpectEventually(drcluster, false, metav1.ConditionFalse,
					BeElementOf(controllers.DRClusterConditionReasonUnfenced, controllers.DRClusterConditionReasonCleaning,
						controllers.DRClusterConditionReasonClean),
					Ignore(), ramen.DRClusterConditionTypeFenced)
			})
		})
		When("provided Fencing value is empty", func() {
			It("reports validated with status fencing as Unfenced", func() {
				drcluster.Spec.ClusterFence = ""
				drcluster = updateDRClusterParameters(drcluster)
				drclusterConditionExpectEventually(drcluster, false, metav1.ConditionFalse,
					Equal(controllers.DRClusterConditionReasonClean), Ignore(),
					ramen.DRClusterConditionTypeFenced)
			})
		})
		When("deleting a DRCluster with empty fencing value", func() {
			It("is successful", func() {
				drpolicyDelete(syncDRPolicy)
				drcluster = getLatestDRCluster(drcluster.Name)
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
				drclusterConditionExpectEventually(drcluster,
					false,
					metav1.ConditionFalse,
					Equal("DrClustersDeployFailed"),
					Ignore(),
					ramen.DRClusterValidated)
				drclusterDelete(drcluster)
			})
		})
		When("provided resource name is an existing namespace", func() {
			It("reports validated", func() {
				drcluster = drclusters[0].DeepCopy()
				Expect(k8sClient.Create(context.TODO(), drcluster)).To(Succeed())
				updateDRClusterManifestWorkStatus(drcluster.Name)
				drclusterConditionExpectEventually(drcluster, false, metav1.ConditionTrue, Equal("Succeeded"), Ignore(),
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

	Context("DRCluster resource deployment configuration automation", func() {
		Specify("create a drcluster copy for changes", func() {
			drcluster = drclusters[0].DeepCopy()
		})
		// TODO: We need ManagedCluster validation and tests, just not namespace validation
		// TODO: Should this depend on referencing DRPolicies, and if they exist leave it as is?
		When("configuration automation is turned OFF after being initially ON", func() {
			It("does NOT create Subscription related manifests", func() {
				By("creating a valid DRCluster")
				Expect(k8sClient.Create(context.TODO(), drcluster)).To(Succeed())
				updateDRClusterManifestWorkStatus(drcluster.Name)
				drclusterConditionExpectEventually(drcluster, false, metav1.ConditionTrue, Equal("Succeeded"), Ignore(),
					ramen.DRClusterValidated)
				By("updating ramen config to NOT auto deploy managed cluster components")
				ramenConfig.DrClusterOperator.DeploymentAutomationEnabled = false
				ramenConfig.DrClusterOperator.S3SecretDistributionEnabled = false
				configMapUpdate()
				drclusterConditionExpectConsistently(drcluster, true, Equal("Succeeded"), Ignore())
			})
		})
		When("configuration automation is turned ON after being OFF", func() {
			It("creates Subscription related manifests", func() {
				By("updating ramen config to auto deploy managed cluster components")
				ramenConfig.DrClusterOperator.DeploymentAutomationEnabled = true
				ramenConfig.DrClusterOperator.S3SecretDistributionEnabled = true
				configMapUpdate()
				drclusterConditionExpectConsistently(drcluster, false, Equal("Succeeded"), Ignore())
			})
		})
		When("configuration automation is ON and CSVersion in configuration changes", func() {
			It("does NOT update Subscription CSVersion", func() {
				By("updating ramen config to change CSV version")
				ramenConfig.DrClusterOperator.ClusterServiceVersionName = "fake.v0.0.2"
				configMapUpdate()
				drclusterConditionExpectConsistently(drcluster, false, Equal("Succeeded"), Ignore())
				inspectClusterManifestSubscriptionCSV(false, "fake.v0.0.2", drcluster)
			})
		})
		When("configuration automation is ON and Channel in configuration changes", func() {
			It("updates Subscription CSVersion", func() {
				By("updating ramen config to change Channel")
				ramenConfig.DrClusterOperator.ChannelName = "fake"
				configMapUpdate()
				drclusterConditionExpectConsistently(drcluster, false, Equal("Succeeded"), Ignore())
				inspectClusterManifestSubscriptionCSV(true, "fake.v0.0.2", drcluster)
			})
		})
		When("deleting a DRCluster with all valid values", func() {
			It("is successful", func() {
				drclusterDelete(drcluster)
			})
		})
	})

	Specify("Delete other DRClusters", func() {
		deleteOtherDRClusters()
	})

	Specify("Delete namespaces named the same as DRClusters", func() {
		deleteDRClusterNamespaces()
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
