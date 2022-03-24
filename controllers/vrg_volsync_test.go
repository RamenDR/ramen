package controllers_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
)

const (
	testMaxWait          = 20 * time.Second
	testInterval         = 250 * time.Millisecond
	testStorageClassName = "fakestorageclass"
)

var _ = Describe("VolumeReplicationGroupController", func() {
	var testNamespace *corev1.Namespace
	testLogger := zap.New(zap.UseDevMode(true), zap.WriteTo(GinkgoWriter))
	var testCtx context.Context
	var cancel context.CancelFunc
	//PVsToRestore = []string{}

	BeforeEach(func() {
		testCtx, cancel = context.WithCancel(context.TODO())

		// Create namespace for test
		testNamespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "vh-",
			},
		}
		Expect(k8sClient.Create(testCtx, testNamespace)).To(Succeed())
		Expect(testNamespace.GetName()).NotTo(BeEmpty())
	})

	AfterEach(func() {
		// All resources are namespaced, so this should clean it all up
		Expect(k8sClient.Delete(testCtx, testNamespace)).To(Succeed())

		cancel()
	})

	Describe("Primary initial setup", func() {
		testMatchLabels := map[string]string{
			"ramentest": "backmeup",
		}

		var testVsrg *ramendrv1alpha1.VolumeReplicationGroup

		Context("When VRG created on primary", func() {
			JustBeforeEach(func() {
				testVsrg = &ramendrv1alpha1.VolumeReplicationGroup{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "test-vrg-east-",
						Namespace:    testNamespace.GetName(),
					},
					Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
						ReplicationState: ramendrv1alpha1.Primary,
						Async: ramendrv1alpha1.VRGAsyncSpec{
							Mode:               ramendrv1alpha1.AsyncModeEnabled,
							SchedulingInterval: "1h",
						},
						Sync: ramendrv1alpha1.VRGSyncSpec{
							Mode: ramendrv1alpha1.SyncModeDisabled,
						},
						PVCSelector: metav1.LabelSelector{
							MatchLabels: testMatchLabels,
						},
						S3Profiles: []string{"fakeS3Profile"},
						VolSync:    ramendrv1alpha1.VolSyncSpec{},
					},
				}

				Expect(k8sClient.Create(testCtx, testVsrg)).To(Succeed())

				Eventually(func() []string {
					err := k8sClient.Get(testCtx, client.ObjectKeyFromObject(testVsrg), testVsrg)
					if err != nil {
						return []string{}
					}
					return testVsrg.GetFinalizers()
				}, testMaxWait, testInterval).Should(
					ContainElement("volumereplicationgroups.ramendr.openshift.io/vrg-protection"))
			})

			Context("When no matching PVCs are bound", func() {
				It("Should not update status with protected PVCs", func() {
					Expect(len(testVsrg.Status.ProtectedPVCs)).To(Equal(0))
				})
			})

			Context("When matching PVCs are bound", func() {
				var boundPvcs []corev1.PersistentVolumeClaim
				JustBeforeEach(func() {
					boundPvcs = []corev1.PersistentVolumeClaim{} // Reset for each test

					// Create some PVCs that are bound
					for i := 0; i < 3; i++ {
						newPvc := createPVC(testCtx, testNamespace.GetName(), testMatchLabels, corev1.ClaimBound)
						boundPvcs = append(boundPvcs, *newPvc)
					}

					Eventually(func() int {
						err := k8sClient.Get(testCtx, client.ObjectKeyFromObject(testVsrg), testVsrg)
						if err != nil {
							return 0
						}
						return len(testVsrg.Status.ProtectedPVCs)
					}, testMaxWait, testInterval).Should(Equal(len(boundPvcs)))
				})

				It("Should find the bound PVCs and report in Status", func() {
					// Check the volsync pvcs
					foundBoundPVC0 := false
					foundBoundPVC1 := false
					foundBoundPVC2 := false
					for _, vsPvc := range testVsrg.Status.ProtectedPVCs {
						switch vsPvc.Name {
						case boundPvcs[0].GetName():
							foundBoundPVC0 = true
						case boundPvcs[1].GetName():
							foundBoundPVC1 = true
						case boundPvcs[2].GetName():
							foundBoundPVC2 = true
						}
					}
					Expect(foundBoundPVC0).To(BeTrue())
					Expect(foundBoundPVC1).To(BeTrue())
					Expect(foundBoundPVC2).To(BeTrue())
				})

				Context("When RSSpec entries are added to vrg spec", func() {
					It("Should create ReplicationSources for each", func() {
						allRSs := &volsyncv1alpha1.ReplicationSourceList{}
						Eventually(func() int {
							Expect(k8sClient.List(testCtx, allRSs,
								client.InNamespace(testNamespace.GetName()))).To(Succeed())
							return len(allRSs.Items)
						}, testMaxWait, testInterval).Should(Equal(len(testVsrg.Status.ProtectedPVCs)))

						rs0 := &volsyncv1alpha1.ReplicationSource{}
						Expect(k8sClient.Get(testCtx, types.NamespacedName{
							Name: boundPvcs[0].GetName(), Namespace: testNamespace.GetName()}, rs0)).To(Succeed())
						Expect(rs0.Spec.SourcePVC).To(Equal(boundPvcs[0].GetName()))
						Expect(rs0.Spec.Trigger).NotTo(BeNil())
						Expect(*rs0.Spec.Trigger.Schedule).To(Equal("* */1 * * *")) // scheduling interval was set to 1h

						rs1 := &volsyncv1alpha1.ReplicationSource{}
						Expect(k8sClient.Get(testCtx, types.NamespacedName{
							Name: boundPvcs[1].GetName(), Namespace: testNamespace.GetName()}, rs1)).To(Succeed())
						Expect(rs1.Spec.SourcePVC).To(Equal(boundPvcs[1].GetName()))
						Expect(rs1.Spec.Trigger).NotTo(BeNil())
						Expect(*rs1.Spec.Trigger.Schedule).To(Equal("* */1 * * *")) // scheduling interval was set to 1h

						rs2 := &volsyncv1alpha1.ReplicationSource{}
						Expect(k8sClient.Get(testCtx, types.NamespacedName{
							Name: boundPvcs[2].GetName(), Namespace: testNamespace.GetName()}, rs2)).To(Succeed())
						Expect(rs2.Spec.SourcePVC).To(Equal(boundPvcs[2].GetName()))
						Expect(rs2.Spec.Trigger).NotTo(BeNil())
						Expect(*rs2.Spec.Trigger.Schedule).To(Equal("* */1 * * *")) // scheduling interval was set to 1h
					})
				})
			})
		})
	})

	Describe("Secondary initial setup", func() {
		testMatchLabels := map[string]string{
			"ramentest": "backmeup",
		}

		var testVsrg *ramendrv1alpha1.VolumeReplicationGroup

		testAccessModes := []corev1.PersistentVolumeAccessMode{
			corev1.ReadWriteOnce,
		}

		testSshKeys := "secondarytestsshkeys"

		Context("When VRG created on secondary", func() {
			JustBeforeEach(func() {
				testVsrg = &ramendrv1alpha1.VolumeReplicationGroup{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "test-vrg-east-",
						Namespace:    testNamespace.GetName(),
					},
					Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
						ReplicationState: ramendrv1alpha1.Secondary,
						Async: ramendrv1alpha1.VRGAsyncSpec{
							Mode:               ramendrv1alpha1.AsyncModeEnabled,
							SchedulingInterval: "1h",
						},
						Sync: ramendrv1alpha1.VRGSyncSpec{
							Mode: ramendrv1alpha1.SyncModeDisabled,
						},
						PVCSelector: metav1.LabelSelector{
							MatchLabels: testMatchLabels,
						},
						S3Profiles: []string{"fakeS3Profile"},
					},
				}

				Expect(k8sClient.Create(testCtx, testVsrg)).To(Succeed())

				Eventually(func() []string {
					err := k8sClient.Get(testCtx, client.ObjectKeyFromObject(testVsrg), testVsrg)
					if err != nil {
						return []string{}
					}
					return testVsrg.GetFinalizers()
				}, testMaxWait, testInterval).Should(
					ContainElement("volumereplicationgroups.ramendr.openshift.io/vrg-protection"))
			})

			Context("When RDSpec entries are added to vrg spec", func() {
				storageClassName := testStorageClassName

				testCapacity0 := corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("1Gi"),
				}
				testCapacity1 := corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("20Gi"),
				}

				rd0 := &volsyncv1alpha1.ReplicationDestination{}
				rd1 := &volsyncv1alpha1.ReplicationDestination{}

				JustBeforeEach(func() {
					// Update the vrg spec with some RDSpec entries
					Expect(k8sClient.Get(testCtx, client.ObjectKeyFromObject(testVsrg), testVsrg)).To(Succeed())
					testVsrg.Spec.VolSync.RDSpec = []ramendrv1alpha1.VolSyncReplicationDestinationSpec{
						{
							ProtectedPVC: ramendrv1alpha1.ProtectedPVC{
								Name:               "testingpvc-a",
								ProtectedByVolSync: true,
								StorageClassName:   &storageClassName,
								AccessModes:        testAccessModes,

								Resources: corev1.ResourceRequirements{Requests: testCapacity0},
							},
							SSHKeys: testSshKeys,
						},
						{
							ProtectedPVC: ramendrv1alpha1.ProtectedPVC{
								Name:               "testingpvc-b",
								ProtectedByVolSync: true,
								StorageClassName:   &storageClassName,
								AccessModes:        testAccessModes,

								Resources: corev1.ResourceRequirements{Requests: testCapacity1},
							},
							SSHKeys: testSshKeys,
						},
					}

					Eventually(func() error {
						return k8sClient.Update(testCtx, testVsrg)
					}, testMaxWait, testInterval).Should(Succeed())

					Expect(k8sClient.Update(testCtx, testVsrg)).To(Succeed())

					allRDs := &volsyncv1alpha1.ReplicationDestinationList{}
					Eventually(func() int {
						Expect(k8sClient.List(testCtx, allRDs,
							client.InNamespace(testNamespace.GetName()))).To(Succeed())
						return len(allRDs.Items)
					}, testMaxWait, testInterval).Should(Equal(len(testVsrg.Spec.VolSync.RDSpec)))

					testLogger.Info("Found RDs", "allRDs", allRDs)

					Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: testVsrg.Spec.VolSync.RDSpec[0].ProtectedPVC.Name,
						Namespace: testNamespace.GetName()}, rd0)).To(Succeed())
					Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: testVsrg.Spec.VolSync.RDSpec[1].ProtectedPVC.Name,
						Namespace: testNamespace.GetName()}, rd1)).To(Succeed())
				})

				It("Should create ReplicationDestinations for each", func() {
					Expect(rd0.Spec.Trigger).To(BeNil()) // Rsync, so destination will not have schedule
					Expect(rd0.Spec.Rsync).NotTo(BeNil())
					Expect(*rd0.Spec.Rsync.SSHKeys).To(Equal(testSshKeys))
					Expect(*rd0.Spec.Rsync.StorageClassName).To(Equal(testStorageClassName))
					Expect(rd0.Spec.Rsync.AccessModes).To(Equal(testAccessModes))
					Expect(rd0.Spec.Rsync.CopyMethod).To(Equal(volsyncv1alpha1.CopyMethodSnapshot))
					Expect(*rd0.Spec.Rsync.ServiceType).To(Equal(corev1.ServiceTypeLoadBalancer))

					Expect(rd1.Spec.Trigger).To(BeNil()) // Rsync, so destination will not have schedule
					Expect(rd1.Spec.Rsync).NotTo(BeNil())
					Expect(*rd1.Spec.Rsync.SSHKeys).To(Equal(testSshKeys))
					Expect(*rd1.Spec.Rsync.StorageClassName).To(Equal(testStorageClassName))
					Expect(rd1.Spec.Rsync.AccessModes).To(Equal(testAccessModes))
					Expect(rd1.Spec.Rsync.CopyMethod).To(Equal(volsyncv1alpha1.CopyMethodSnapshot))
					Expect(*rd1.Spec.Rsync.ServiceType).To(Equal(corev1.ServiceTypeLoadBalancer))
				})

				Context("When ReplicationDestinations have address set in status", func() {
					rd0Address := "99.98.97.96"
					rd1Address := "99.88.77.66"
					JustBeforeEach(func() {
						// fake address set in status on the ReplicationDestinations
						rd0.Status = &volsyncv1alpha1.ReplicationDestinationStatus{
							Rsync: &volsyncv1alpha1.ReplicationDestinationRsyncStatus{
								SSHKeys: &testSshKeys,
								Address: &rd0Address,
							},
						}
						Expect(k8sClient.Status().Update(testCtx, rd0)).To(Succeed())

						rd1.Status = &volsyncv1alpha1.ReplicationDestinationStatus{
							Rsync: &volsyncv1alpha1.ReplicationDestinationRsyncStatus{
								SSHKeys: &testSshKeys,
								Address: &rd1Address,
							},
						}
						Expect(k8sClient.Status().Update(testCtx, rd1)).To(Succeed())

					})
				})
			})
		})
	})

})

func createPVC(ctx context.Context, namespace string, labels map[string]string,
	bindInfo corev1.PersistentVolumeClaimPhase) *corev1.PersistentVolumeClaim {

	capacity := corev1.ResourceList{
		corev1.ResourceStorage: resource.MustParse("1Gi"),
	}
	accessModes := []corev1.PersistentVolumeAccessMode{
		corev1.ReadWriteOnce,
	}

	storageClassName := testStorageClassName

	pvc := &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "testpvc-",
			Labels:       labels,
			Namespace:    namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      accessModes,
			Resources:        corev1.ResourceRequirements{Requests: capacity},
			StorageClassName: &storageClassName,
		},
	}

	Expect(k8sClient.Create(context.TODO(), pvc)).To(Succeed())

	pvc.Status.Phase = bindInfo
	pvc.Status.AccessModes = accessModes
	pvc.Status.Capacity = capacity
	Expect(k8sClient.Status().Update(ctx, pvc)).To(Succeed())

	return pvc
}
