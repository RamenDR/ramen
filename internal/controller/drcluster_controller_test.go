// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"
	"strings"

	csiaddonsv1alpha1 "github.com/csi-addons/kubernetes-csi-addons/api/csiaddons/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	workv1 "open-cluster-management.io/api/work/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	controllers "github.com/ramendr/ramen/internal/controller"
	"github.com/ramendr/ramen/internal/controller/util"
)

var (
	NFClassAvailable bool
	S3HealthyInDRCC  bool
)

var cidrs = [][]string{
	{"198.51.100.17/24", "198.51.100.18/24", "198.51.100.19/24"}, // valid CIDR
	{"198.51.100.20/24", "198.51.100.21/24", "198.51.100.22/24"}, // valid CIDR
	{"198.51.100.23/24", "198.51.100.24/24", "198.51.100.25/24"}, // valid CIDR

	{"1111.51.100.14/24", "aaa.51.100.15/24", "00.51.100.16/24"}, // invalid CIDR
}

var baseNF = &csiaddonsv1alpha1.NetworkFence{
	TypeMeta:   metav1.TypeMeta{Kind: "NetworkFence", APIVersion: "csiaddons.openshift.io/v1alpha1"},
	ObjectMeta: metav1.ObjectMeta{Name: strings.Join([]string{controllers.NetworkFencePrefix, "drc-cluster0"}, "-")},
	Spec: csiaddonsv1alpha1.NetworkFenceSpec{
		Cidrs:      cidrs[0],
		FenceState: csiaddonsv1alpha1.Fenced,
	},
}

var baseSC = &storagev1.StorageClass{
	TypeMeta: metav1.TypeMeta{Kind: "StorageClass", APIVersion: "storage.k8s.io/v1"},
	ObjectMeta: metav1.ObjectMeta{
		Name: "baseSC",
		Labels: map[string]string{
			controllers.StorageIDLabel: "fake",
		},
	},
	Provisioner: "fake.ramen.com",
}

var baseNFClass = csiaddonsv1alpha1.NetworkFenceClass{
	TypeMeta:   metav1.TypeMeta{Kind: "NetworkFenceClass", APIVersion: "csiaddons.openshift.io/v1alpha1"},
	ObjectMeta: metav1.ObjectMeta{Name: "networkfenceclass-fake"},
	Spec: csiaddonsv1alpha1.NetworkFenceClassSpec{
		Parameters: map[string]string{
			"clusterID": "rook-ceph",
			"csiaddons.openshift.io/networkfence-secret-name":      "rook-csi-rbd-provisioner",
			"csiaddons.openshift.io/networkfence-secret-namespace": "rook-ceph",
		},
		Provisioner: "fake.ramen.com",
	},
}

var baseDRCConfig = &ramen.DRClusterConfig{
	ObjectMeta: metav1.ObjectMeta{Name: "drcc-local"},
	Spec:       ramen.DRClusterConfigSpec{ClusterID: "drcc-local-cid"},
}

func generateDRCC() *ramen.DRClusterConfig {
	drcc := baseDRCConfig.DeepCopy()
	drcc.Status.StorageClasses = []string{"sc1"}
	drcc.Status.NetworkFenceClasses = []string{"nfc1"}
	drcc.Status.StorageAccessDetails = []ramen.StorageAccessDetail{
		{
			StorageProvisioner: "fake.ramen.com",
			CIDRs:              cidrs[0],
		},
	}

	return drcc
}

func generateSC() *storagev1.StorageClass {
	sc := baseSC.DeepCopy()
	sc.Name = "sc1"
	sid := "sc1" + "-sid"
	sc.Labels = map[string]string{
		controllers.StorageIDLabel: sid,
	}

	return sc
}

func generateNFC() *csiaddonsv1alpha1.NetworkFenceClass {
	nfc := baseNFClass.DeepCopy()
	nfc.Name = "nfc1"
	nfc.Annotations = map[string]string{
		controllers.StorageIDLabel: "sc1-sid",
	}

	return nfc
}

func (f FakeMCVGetter) GetNFFromManagedCluster(resourceName, networkFenceClass, resourceNamespace,
	managedCluster string, annotations map[string]string,
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

	if NFClassAvailable {
		nf.Name = strings.Join([]string{controllers.NetworkFencePrefix, "drc-cluster0", "nfc1"}, "-")
		nf.Spec.NetworkFenceClassName = "nfc1"
	}

	return nf, nil
}

func (f FakeMCVGetter) GetNFClassFromManagedCluster(resourceName, managedCluster string, annotations map[string]string,
) (*csiaddonsv1alpha1.NetworkFenceClass, error) {
	// Guard against test interference: Only return populated NetworkFenceClass when
	// NFClass is being tested to maintain test isolation for other test suites.
	if NFClassAvailable {
		return generateNFC(), nil
	}

	return nil, nil
}

func (f FakeMCVGetter) GetDRClusterConfigFromManagedCluster(
	resourceName string,
	annotations map[string]string,
) (*ramen.DRClusterConfig, error) {
	// Guard against test interference: Only return populated DRClusterConfig when
	// NFClass is being tested to maintain test isolation for other test suites.
	if NFClassAvailable {
		return generateDRCC(), nil
	}
	// Enable tests to force S3 healthy in the returned DRClusterConfig
	if S3HealthyInDRCC {
		drcc := generateDRCC()
		drcc.Status.Conditions = append(drcc.Status.Conditions, metav1.Condition{
			Type:    ramen.DRClusterConfigS3Healthy,
			Status:  metav1.ConditionTrue,
			Reason:  controllers.DRClusterReasonS3Reachable,
			Message: "All S3 profiles are healthy",
		})

		return drcc, nil
	}

	return &ramen.DRClusterConfig{}, nil
}

func (f FakeMCVGetter) GetSClassFromManagedCluster(resourceName, managedCluster string, annotations map[string]string,
) (*storagev1.StorageClass, error) {
	// Guard against test interference: Only return populated StorageClass when
	// NFClass is being tested to maintain test isolation for other test suites.
	if NFClassAvailable {
		return generateSC(), nil
	}

	return nil, nil
}

func (f FakeMCVGetter) DeleteNFManagedClusterView(
	resourceName, resourceNamespace, clusterName, resourceType string,
) error {
	return nil
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

// Helper assertions for DRCluster S3 condition (the controller uses "S3Healthy")
func expectClusterS3Unreachable(drcluster *ramen.DRCluster) {
	objectConditionExpectEventually(
		apiReader,
		drcluster,
		metav1.ConditionFalse,
		Equal(controllers.DRClusterReasonS3Unreachable),
		Ignore(),
		ramen.DRClusterConditionS3Healthy,
		false,
	)
}

// Helper for positive path where DRClusterConfig reports S3Healthy=True → DRCluster mirrors it
func expectClusterS3Reachable(drcluster *ramen.DRCluster) {
	objectConditionExpectEventually(
		apiReader,
		drcluster,
		metav1.ConditionTrue,
		Equal(controllers.DRClusterReasonS3Reachable),
		Ignore(),
		ramen.DRClusterConditionS3Healthy,
		false,
	)
}

var _ = Describe("DRClusterController", func() {
	drclusterDelete := func(drcluster *ramen.DRCluster) {
		clusterName := drcluster.Name
		Expect(k8sClient.Delete(context.TODO(), drcluster)).To(Succeed())
		Eventually(func() bool {
			return k8serrors.IsNotFound(apiReader.Get(context.TODO(), types.NamespacedName{
				Namespace: drcluster.Namespace,
				Name:      drcluster.Name,
			}, drcluster))
		}, timeout, interval).Should(BeTrue())
		manifestWork := &workv1.ManifestWork{}
		Expect(k8serrors.IsNotFound(apiReader.Get(
			context.TODO(),
			types.NamespacedName{
				Name:      util.DrClusterManifestWorkName,
				Namespace: clusterName,
			},
			manifestWork))).To(BeTrue())
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

	createManagedClusters := func() {
		for _, drcluster := range drclusters {
			ensureManagedCluster(k8sClient, drcluster.Name)
		}
	}

	namespaceDeletionConfirm := func(name string) {
		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
		Eventually(func() error {
			return k8sClient.Get(context.TODO(), types.NamespacedName{Name: namespace.Name}, namespace)
		}, timeout, interval).Should(
			MatchError(k8serrors.NewNotFound(schema.GroupResource{Resource: "namespaces"}, namespace.Name)),
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
			updateDRClusterManifestWorkStatus(k8sClient, apiReader, cluster.Name)
			updateDRClusterConfigMWStatus(k8sClient, apiReader, cluster.Name)
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
			return k8serrors.IsNotFound(apiReader.Get(context.TODO(), types.NamespacedName{Name: policy.Name}, policy))
		}, timeout, interval).Should(BeTrue())
	}

	drpolicyDelete := func(drpolicy *ramen.DRPolicy) {
		drpolicyDeleteAndConfirm(drpolicy)
	}

	var drcluster *ramen.DRCluster
	Specify("DRCluster initialize tests", func() {
		populateDRClusters()
		createDRClusterNamespaces()
		createManagedClusters()
		createOtherDRClusters()
	})

	// -------------------------------------------------------------------------
	// DRCluster S3Profile validation
	// -------------------------------------------------------------------------
	Context("DRCluster resource S3Profile validation", func() {
		Specify("create a drcluster copy for changes", func() {
			createPolicies()
			drcluster = drclusters[0].DeepCopy()
		})
		When("an S3Profile is missing in config", func() {
			It("reports S3 unhealthy", func() {
				By("creating a new DRCluster with an invalid S3Profile")
				drcluster.Spec.S3ProfileName = "missing"
				Expect(k8sClient.Create(context.TODO(), drcluster)).To(Succeed())
				updateDRClusterManifestWorkStatus(k8sClient, apiReader, drcluster.Name)
				// Ensure DRCConfig is applied so DRCluster consumes its S3 status
				updateDRClusterConfigMWStatus(k8sClient, apiReader, drcluster.Name)

				// New behavior: S3 condition on DRCluster
				expectClusterS3Unreachable(drcluster)
			})
		})
		When("deleting a DRCluster", func() {
			It("is successful", func() {
				drpolicyDelete(syncDRPolicy)
				drclusterDelete(drcluster)
				// recreate DRPolicy
				createPolicies()
				drcluster = drclusters[0].DeepCopy()
			})
		})
		When("an S3Profile fails listing", func() {
			It("reports S3 unhealthy", func() {
				By("creating a DRCluster with an invalid S3Profile that fails listing")
				drcluster.Spec.S3ProfileName = s3Profiles[4].S3ProfileName
				Expect(k8sClient.Create(context.TODO(), drcluster)).To(Succeed())
				updateDRClusterManifestWorkStatus(k8sClient, apiReader, drcluster.Name)
				updateDRClusterConfigMWStatus(k8sClient, apiReader, drcluster.Name)

				expectClusterS3Unreachable(drcluster)
			})
		})
		When("fenced with invalid S3Profile", func() {
			It("reports S3 unhealthy and does not process fencing", func() {
				By("updating DRCluster to ManuallyFenced with an invalid S3Profile")
				drcluster.Spec.ClusterFence = ramen.ClusterFenceStateManuallyFenced
				drcluster = updateDRClusterParameters(drcluster)
				updateDRClusterConfigMWStatus(k8sClient, apiReader, drcluster.Name)
				expectClusterS3Unreachable(drcluster)
			})
		})

		When("deleting a DRCluster", func() {
			It("is successful", func() {
				drpolicyDelete(syncDRPolicy)
				drclusterDelete(drcluster)
				// recreate DRPolicy
				createPolicies()
				drcluster = drclusters[0].DeepCopy()
			})
		})

		When("S3Profile is valid", func() {
			It("reports validated with reason Succeeded", func() {
				By("creating a DRCluster with a valid S3Profile and no cluster fencing")
				drcluster.Spec.S3ProfileName = s3Profiles[0].S3ProfileName
				drcluster.Spec.ClusterFence = ""
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
					false,
				)
			})
		})

		When("deleting a DRCluster", func() {
			It("is successful", func() {
				drpolicyDelete(syncDRPolicy)
				drclusterDelete(drcluster)
				// recreate DRPolicy
				createPolicies()
				drcluster = drclusters[0].DeepCopy()
			})
		})

		When("S3Profile is changed to an invalid profile in ramen config", func() {
			It("reports S3 unhealthy", func() {
				By("creating a DRCluster with the new valid S3Profile")
				drcluster.Spec.S3ProfileName = s3Profiles[5].S3ProfileName
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
					false,
				)
				By("changing the S3Profile in ramen config to an invalid value")
				newS3Profiles := s3Profiles[0:]
				s3Profiles[5].S3Bucket = bucketNameFail
				s3ProfilesStore(newS3Profiles)

				updateDRClusterConfigMWStatus(k8sClient, apiReader, drcluster.Name)
				expectClusterS3Unreachable(drcluster)
				// TODO: Ensure when changing S3Profile, dr-cluster's ramen config is updated in MW
			})
		})

		When("deleting a DRCluster", func() {
			It("is successful", func() {
				drpolicyDelete(syncDRPolicy)
				drclusterDelete(drcluster)
				// recreate DRPolicy
				createPolicies()
				drcluster = drclusters[0].DeepCopy()
			})
		})

		When("when adding an invalid S3Profile in DRCLuster", func() {
			It("reports S3 unhealthy", func() {
				By("creating a DRCluster with an invalid S3Profile that fails listing")
				drcluster.Spec.S3ProfileName = s3Profiles[4].S3ProfileName
				Expect(k8sClient.Create(context.TODO(), drcluster)).To(Succeed())
				updateDRClusterManifestWorkStatus(k8sClient, apiReader, drcluster.Name)
				updateDRClusterConfigMWStatus(k8sClient, apiReader, drcluster.Name)
				expectClusterS3Unreachable(drcluster)
			})
		})
		When("deleting a DRCluster with an invalid s3Profile", func() {
			It("is successful", func() {
				drpolicyDelete(syncDRPolicy)
				drclusterDelete(drcluster)
			})
		})

		When("DRClusterConfig reports S3 healthy", func() {
			It("sets S3Healthy True in DRCluster status", func() {
				S3HealthyInDRCC = true
				defer func() { S3HealthyInDRCC = false }()
				drcluster = drclusters[0].DeepCopy()

				Expect(k8sClient.Create(context.TODO(), drcluster)).To(Succeed())
				updateDRClusterManifestWorkStatus(k8sClient, apiReader, drcluster.Name)
				updateDRClusterConfigMWStatus(k8sClient, apiReader, drcluster.Name)

				expectClusterS3Reachable(drcluster)
			})
		})
		When("deleting a DRCluster with S3 healthy", func() {
			It("is successful", func() {
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
				drcluster.Spec.CIDRs = cidrs[3]
				Expect(k8sClient.Create(context.TODO(), drcluster)).To(Succeed())
				updateDRClusterManifestWorkStatus(k8sClient, apiReader, drcluster.Name)
				updateDRClusterConfigMWStatus(k8sClient, apiReader, drcluster.Name)
				objectConditionExpectEventually(
					apiReader,
					drcluster,
					metav1.ConditionFalse,
					Equal("ValidationFailed"),
					Ignore(),
					ramen.DRClusterValidated,
					false,
				)
			})
		})
		When("provided CIDR value is changed to be correct", func() {
			It("reports validated", func() {
				drcluster.Spec.CIDRs = cidrs[0]
				drcluster = updateDRClusterParameters(drcluster)
				updateDRClusterManifestWorkStatus(k8sClient, apiReader, drcluster.Name)
				updateDRClusterConfigMWStatus(k8sClient, apiReader, drcluster.Name)
				objectConditionExpectEventually(
					apiReader,
					drcluster,
					metav1.ConditionTrue,
					Equal("Succeeded"),
					Ignore(),
					ramen.DRClusterValidated,
					false,
				)
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
				updateDRClusterManifestWorkStatus(k8sClient, apiReader, drcluster.Name)
				updateDRClusterConfigMWStatus(k8sClient, apiReader, drcluster.Name)
				objectConditionExpectEventually(
					apiReader,
					drcluster,
					metav1.ConditionTrue,
					Equal("Succeeded"),
					Ignore(),
					ramen.DRClusterValidated,
					false,
				)
			})
		})
		When("provided CIDR value is changed to be incorrect", func() {
			It("reports NOT validated with reason ValidationFailed", func() {
				drcluster.Spec.CIDRs = cidrs[3]
				drcluster = updateDRClusterParameters(drcluster)
				objectConditionExpectEventually(
					apiReader,
					drcluster,
					metav1.ConditionFalse,
					Equal("ValidationFailed"),
					Ignore(),
					ramen.DRClusterValidated,
					false,
				)
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
				updateDRClusterManifestWorkStatus(k8sClient, apiReader, drcluster.Name)
				updateDRClusterConfigMWStatus(k8sClient, apiReader, drcluster.Name)
				objectConditionExpectEventually(
					apiReader,
					drcluster,
					metav1.ConditionTrue,
					Equal(controllers.DRClusterConditionReasonFenced),
					Ignore(),
					ramen.DRClusterConditionTypeFenced,
					false,
				)
			})
		})
		When("provided Fencing value is ManuallyUnfenced", func() {
			It("reports validated with status fencing as Unfenced", func() {
				drcluster.Spec.ClusterFence = ramen.ClusterFenceStateManuallyUnfenced
				drcluster = updateDRClusterParameters(drcluster)
				objectConditionExpectEventually(
					apiReader,
					drcluster,
					metav1.ConditionFalse,
					Equal(controllers.DRClusterConditionReasonClean),
					Ignore(),
					ramen.DRClusterConditionTypeFenced,
					false,
				)
			})
		})
		When("provided Fencing value is Fenced with an empty CIDR", func() {
			It("reports error in generating networkFence", func() {
				drcluster.Spec.ClusterFence = ramen.ClusterFenceStateFenced
				drcluster.Spec.CIDRs = []string{}
				drcluster = updateDRClusterParameters(drcluster)
				objectConditionExpectEventually(
					apiReader,
					drcluster,
					metav1.ConditionFalse,
					Equal(controllers.DRClusterConditionReasonFenceError),
					Ignore(),
					ramen.DRClusterConditionTypeFenced,
					false,
				)
			})
		})
		When("CIDRs value is provided", func() {
			It("reports validated", func() {
				drcluster.Spec.ClusterFence = ramen.ClusterFenceStateFenced
				drcluster.Spec.CIDRs = cidrs[0]
				drcluster = updateDRClusterParameters(drcluster)
				objectConditionExpectEventually(
					apiReader,
					drcluster,
					metav1.ConditionTrue,
					Equal("Succeeded"),
					Ignore(),
					ramen.DRClusterValidated,
					false,
				)
			})
		})
		When("provided Fencing value is Fenced", func() {
			It("reports fenced with reason Fencing success", func() {
				drcluster.Spec.ClusterFence = ramen.ClusterFenceStateFenced
				drcluster = updateDRClusterParameters(drcluster)
				objectConditionExpectEventually(
					apiReader,
					drcluster,
					metav1.ConditionTrue,
					Equal(controllers.DRClusterConditionReasonFenced),
					Ignore(),
					ramen.DRClusterConditionTypeFenced,
					false,
				)
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
				objectConditionExpectEventually(
					apiReader,
					drcluster,
					metav1.ConditionFalse,
					BeElementOf(controllers.DRClusterConditionReasonUnfenced, controllers.DRClusterConditionReasonCleaning,
						controllers.DRClusterConditionReasonClean),
					Ignore(),
					ramen.DRClusterConditionTypeFenced,
					false,
				)
			})
		})
		When("provided Fencing value is empty", func() {
			It("reports validated with status fencing as Unfenced", func() {
				drcluster.Spec.ClusterFence = ""
				drcluster = updateDRClusterParameters(drcluster)
				objectConditionExpectEventually(
					apiReader,
					drcluster,
					metav1.ConditionFalse,
					Equal(controllers.DRClusterConditionReasonClean),
					Ignore(),
					ramen.DRClusterConditionTypeFenced,
					false,
				)
			})
		})
		When("deleting a DRCluster with empty fencing value", func() {
			It("is successful", func() {
				drpolicyDelete(syncDRPolicy)
				drcluster = getLatestDRCluster(drcluster.Name)
				drclusterDelete(drcluster)
			})
		})

		Specify("create a drcluster copy for changes", func() {
			createPolicies()
			drcluster = drclusters[0].DeepCopy()
		})

		When("provided fencing value is Fenced with invalid S3Profile", func() {
			It("reports S3 unhealthy and does not process fencing", func() {
				drcluster.Spec.ClusterFence = ramen.ClusterFenceStateFenced
				drcluster.Spec.S3ProfileName = s3Profiles[4].S3ProfileName
				Expect(k8sClient.Create(context.TODO(), drcluster)).To(Succeed())
				updateDRClusterManifestWorkStatus(k8sClient, apiReader, drcluster.Name)
				updateDRClusterConfigMWStatus(k8sClient, apiReader, drcluster.Name)
				expectClusterS3Unreachable(drcluster)
			})
		})

		// this test is needed to delete the DRCluster in the above scenario,
		// ref issue: https://github.com/RamenDR/ramen/issues/1264, because of this
		// we need to remove the change the fenced status to "", so that ramen will not
		// look for the peer cluster in this situtaion
		When("provided Fencing value is empty", func() {
			It("reports S3 unhealthy and fencing is cleared", func() {
				drcluster.Spec.ClusterFence = ""
				drcluster = updateDRClusterParameters(drcluster)
				updateDRClusterConfigMWStatus(k8sClient, apiReader, drcluster.Name)
				expectClusterS3Unreachable(drcluster)
			})
		})

		When("deleting a DRCluster", func() {
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
				objectConditionExpectEventually(
					apiReader,
					drcluster,
					metav1.ConditionFalse,
					Equal("DrClustersDeployFailed"),
					Ignore(),
					ramen.DRClusterValidated,
					false,
				)
				drclusterDelete(drcluster)
			})
		})
		When("provided resource name is an existing namespace", func() {
			It("reports validated", func() {
				drcluster = drclusters[0].DeepCopy()
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
					false,
				)
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
				updateDRClusterManifestWorkStatus(k8sClient, apiReader, drcluster.Name)
				updateDRClusterConfigMWStatus(k8sClient, apiReader, drcluster.Name)
				objectConditionExpectEventually(
					apiReader,
					drcluster,
					metav1.ConditionTrue,
					Equal("Succeeded"),
					Ignore(),
					ramen.DRClusterValidated,
					false,
				)
				By("updating ramen config to NOT auto deploy managed cluster components")
				ramenConfig.DrClusterOperator.DeploymentAutomationEnabled = false
				ramenConfig.DrClusterOperator.S3SecretDistributionEnabled = false
				configMapUpdate()
				drclusterConditionExpectConsistently(
					apiReader,
					drcluster,
					true,
					Equal("Succeeded"),
					Ignore())
			})
		})
		When("configuration automation is turned ON after being OFF", func() {
			It("creates Subscription related manifests", func() {
				By("updating ramen config to auto deploy managed cluster components")
				ramenConfig.DrClusterOperator.DeploymentAutomationEnabled = true
				ramenConfig.DrClusterOperator.S3SecretDistributionEnabled = true
				configMapUpdate()
				drclusterConditionExpectConsistently(
					apiReader,
					drcluster,
					false,
					Equal("Succeeded"),
					Ignore())
			})
		})
		When("configuration automation is ON and CSVersion in configuration changes", func() {
			It("does NOT update Subscription CSVersion", func() {
				By("updating ramen config to change CSV version")
				ramenConfig.DrClusterOperator.ClusterServiceVersionName = "fake.v0.0.2"
				configMapUpdate()
				drclusterConditionExpectConsistently(
					apiReader,
					drcluster,
					false,
					Equal("Succeeded"),
					Ignore())
				inspectClusterManifestSubscriptionCSV(false, "fake.v0.0.2", drcluster)
			})
		})
		When("configuration automation is ON and Channel in configuration changes", func() {
			It("updates Subscription CSVersion", func() {
				By("updating ramen config to change Channel")
				ramenConfig.DrClusterOperator.ChannelName = "fake"
				configMapUpdate()
				drclusterConditionExpectConsistently(
					apiReader,
					drcluster,
					false,
					Equal("Succeeded"),
					Ignore())
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

	// nfclases tests
	Context("NFClass Tests", func() {
		Specify("create a drcluster copy for changes", func() {
			createPolicies()
			createOtherDRClusters()
			drcluster = drclusters[0].DeepCopy()
		})

		When("provided Fencing value is Fenced where NFClass is available", func() {
			It("should update NetworkFence correctly with NetworkFenceClass Name", func() {
				NFClassAvailable = true
				defer func() { NFClassAvailable = false }()

				drcluster.Spec.ClusterFence = ramen.ClusterFenceStateFenced
				Expect(k8sClient.Create(context.TODO(), drcluster)).To(Succeed())
				updateDRClusterManifestWorkStatus(k8sClient, apiReader, drcluster.Name)
				updateDRClusterConfigMWStatus(k8sClient, apiReader, drcluster.Name)

				objectConditionExpectEventually(
					apiReader,
					drcluster,
					metav1.ConditionTrue,
					Equal(controllers.DRClusterConditionReasonFenced),
					Ignore(),
					ramen.DRClusterConditionTypeFenced,
					false,
				)
			})
		})

		When("provided Fencing value is Unfenced where NFClass is available", func() {
			It("reports validated with status fencing as Unfenced", func() {
				NFClassAvailable = true
				defer func() { NFClassAvailable = false }()

				drcluster.Spec.ClusterFence = ramen.ClusterFenceStateUnfenced
				drcluster = updateDRClusterParameters(drcluster)
				updateDRClusterManifestWorkStatus(k8sClient, apiReader, drcluster.Name)
				updateDRClusterConfigMWStatus(k8sClient, apiReader, drcluster.Name)

				objectConditionExpectEventually(
					apiReader,
					drcluster,
					metav1.ConditionFalse,
					BeElementOf(controllers.DRClusterConditionReasonUnfenced, controllers.DRClusterConditionReasonCleaning,
						controllers.DRClusterConditionReasonClean),
					Ignore(),
					ramen.DRClusterConditionTypeFenced,
					false,
				)
			})
		})
		When("provided Fencing value is empty", func() {
			It("reports validated with status fencing as Unfenced", func() {
				drcluster.Spec.ClusterFence = ""
				drcluster = updateDRClusterParameters(drcluster)
				objectConditionExpectEventually(
					apiReader,
					drcluster,
					metav1.ConditionFalse,
					Equal(controllers.DRClusterConditionReasonClean),
					Ignore(),
					ramen.DRClusterConditionTypeFenced,
					false,
				)
			})
		})
		When("deleting a DRCluster", func() {
			It("is successful", func() {
				drpolicyDelete(syncDRPolicy)
				drclusterDelete(drcluster)
			})
		})

		Specify("Delete other DRClusters", func() {
			deleteOtherDRClusters()
		})

		Specify("Delete namespaces named the same as DRClusters", func() {
			deleteDRClusterNamespaces()
		})
	})

	// InvalidCIDRsDetected tests
	Context("InvalidCIDRsDetected Tests", func() {
		Specify("create a drcluster copy for changes", func() {
			createPolicies()
			createOtherDRClusters()
			drcluster = drclusters[0].DeepCopy()
		})

		When("provided CIDRs match detected StorageAccessDetails", func() {
			It("reports validated with reason Succeeded", func() {
				NFClassAvailable = true
				defer func() { NFClassAvailable = false }()

				drcluster.Spec.CIDRs = cidrs[0]
				Expect(k8sClient.Create(context.TODO(), drcluster)).To(Succeed())
				updateDRClusterManifestWorkStatus(k8sClient, apiReader, drcluster.Name)
				updateDRClusterConfigMWStatus(k8sClient, apiReader, drcluster.Name)

				objectConditionExpectEventually(
					apiReader,
					drcluster,
					metav1.ConditionTrue,
					Equal(controllers.DRClusterConditionReasonValidated),
					Ignore(),
					ramen.DRClusterValidated,
					false,
				)
			})
		})

		When("provided CIDRs do not match detected StorageAccessDetails", func() {
			It("reports NOT validated with reason ValidationFailed", func() {
				NFClassAvailable = true
				defer func() { NFClassAvailable = false }()

				drcluster.Spec.CIDRs = []string{"192.168.1.0/24"} // CIDR not in StorageAccessDetails
				drcluster = updateDRClusterParameters(drcluster)
				updateDRClusterManifestWorkStatus(k8sClient, apiReader, drcluster.Name)
				updateDRClusterConfigMWStatus(k8sClient, apiReader, drcluster.Name)

				objectConditionExpectEventually(
					apiReader,
					drcluster,
					metav1.ConditionFalse,
					Equal(controllers.ReasonValidationFailed),
					ContainSubstring("undetected CIDRs specified"),
					ramen.DRClusterValidated,
					false,
				)
			})
		})

		When("deleting a DRCluster", func() {
			It("is successful", func() {
				drpolicyDelete(syncDRPolicy)
				drclusterDelete(drcluster)
			})
		})

		Specify("Delete other DRClusters", func() {
			deleteOtherDRClusters()
		})

		Specify("Delete namespaces named the same as DRClusters", func() {
			deleteDRClusterNamespaces()
		})
	})
})
