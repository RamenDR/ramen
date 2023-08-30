// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v4/apis/volumesnapshot/v1"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/volsync"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
)

const (
	testMaxWait             = 20 * time.Second
	testInterval            = 250 * time.Millisecond
	testStorageClassName    = "fakestorageclass"
	testVolumeSnapshotClass = "fakevolumesnapshotclass"
)

var _ = Describe("VolumeReplicationGroupVolSyncController", func() {
	var testNamespace *corev1.Namespace
	var testCtx context.Context
	var cancel context.CancelFunc

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
						Async: &ramendrv1alpha1.VRGAsyncSpec{
							SchedulingInterval: "1h",
						},
						PVCSelector: metav1.LabelSelector{
							MatchLabels: testMatchLabels,
						},
						S3Profiles: []string{s3Profiles[0].S3ProfileName},
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

				createSecret(testVsrg.GetName(), testNamespace.Name)
				createSC()
				createVSC()
			})

			Context("When no matching PVCs are bound", func() {
				It("Should not update status with protected PVCs", func() {
					Expect(len(testVsrg.Status.ProtectedPVCs)).To(Equal(0))
				})
			})

			Context("When matching PVCs are bound", func() {
				var boundPvcs []corev1.PersistentVolumeClaim

				pvcAnnotations := map[string]string{
					"apps.open-cluster-management.io/hosting-subscription": "sub-name",
					"apps.open-cluster-management.io/reconcile-option":     "merge",
					volsync.ACMAppSubDoNotDeleteAnnotation:                 volsync.ACMAppSubDoNotDeleteAnnotationVal,
					"pv.kubernetes.io/bind-completed":                      "yes",
					"volume.kubernetes.io/storage-provisioner":             "provisioner",
				}

				JustBeforeEach(func() {
					// Reset for each test
					boundPvcs = []corev1.PersistentVolumeClaim{}

					// Create some PVCs that are bound
					for i := 0; i < 3; i++ {
						newPvc := createPVCBoundToRunningPod(testCtx, testNamespace.GetName(),
							testMatchLabels, pvcAnnotations)
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

				It("Should report only OCM annotaions in Status", func() {
					for _, vsPvc := range testVsrg.Status.ProtectedPVCs {
						// OCM annontations are propagated.
						Expect(vsPvc.Annotations).To(HaveKeyWithValue(
							"apps.open-cluster-management.io/hosting-subscription", "sub-name"))
						Expect(vsPvc.Annotations).To(HaveKeyWithValue(
							"apps.open-cluster-management.io/reconcile-option", "merge"))

						// Except the do-no-delete annotion
						Expect(vsPvc.Annotations).NotTo(HaveKey(volsync.ACMAppSubDoNotDeleteAnnotation))

						// Other annotations are droopped.
						Expect(vsPvc.Annotations).NotTo(HaveKey("pv.kubernetes.io/bind-completed"))
						Expect(vsPvc.Annotations).NotTo(HaveKey("volume.kubernetes.io/storage-provisioner"))
					}
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
							Name: boundPvcs[0].GetName(), Namespace: testNamespace.GetName(),
						}, rs0)).To(Succeed())
						Expect(rs0.Spec.SourcePVC).To(Equal(boundPvcs[0].GetName()))
						Expect(rs0.Spec.Trigger).NotTo(BeNil())
						Expect(*rs0.Spec.Trigger.Schedule).To(Equal("0 */1 * * *")) // scheduling interval was set to 1h

						rs1 := &volsyncv1alpha1.ReplicationSource{}
						Expect(k8sClient.Get(testCtx, types.NamespacedName{
							Name: boundPvcs[1].GetName(), Namespace: testNamespace.GetName(),
						}, rs1)).To(Succeed())
						Expect(rs1.Spec.SourcePVC).To(Equal(boundPvcs[1].GetName()))
						Expect(rs1.Spec.Trigger).NotTo(BeNil())
						Expect(*rs1.Spec.Trigger.Schedule).To(Equal("0 */1 * * *")) // scheduling interval was set to 1h

						rs2 := &volsyncv1alpha1.ReplicationSource{}
						Expect(k8sClient.Get(testCtx, types.NamespacedName{
							Name: boundPvcs[2].GetName(), Namespace: testNamespace.GetName(),
						}, rs2)).To(Succeed())
						Expect(rs2.Spec.SourcePVC).To(Equal(boundPvcs[2].GetName()))
						Expect(rs2.Spec.Trigger).NotTo(BeNil())
						Expect(*rs2.Spec.Trigger.Schedule).To(Equal("0 */1 * * *")) // scheduling interval was set to 1h
					})
				})
			})
		})
	})

	Describe("Secondary initial setup", func() {
		testMatchLabels := map[string]string{
			"ramentest": "backmeup",
		}

		var testVrg *ramendrv1alpha1.VolumeReplicationGroup

		testAccessModes := []corev1.PersistentVolumeAccessMode{
			corev1.ReadWriteOnce,
		}

		Context("When VRG created on secondary", func() {
			JustBeforeEach(func() {
				testVrg = &ramendrv1alpha1.VolumeReplicationGroup{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "test-vrg-east-",
						Namespace:    testNamespace.GetName(),
					},
					Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
						ReplicationState: ramendrv1alpha1.Secondary,
						Async: &ramendrv1alpha1.VRGAsyncSpec{
							SchedulingInterval: "1h",
						},
						PVCSelector: metav1.LabelSelector{
							MatchLabels: testMatchLabels,
						},
						S3Profiles: []string{"fakeS3Profile"},
					},
				}

				Expect(k8sClient.Create(testCtx, testVrg)).To(Succeed())

				Eventually(func() []string {
					err := k8sClient.Get(testCtx, client.ObjectKeyFromObject(testVrg), testVrg)
					if err != nil {
						return []string{}
					}

					return testVrg.GetFinalizers()
				}, testMaxWait, testInterval).Should(
					ContainElement("volumereplicationgroups.ramendr.openshift.io/vrg-protection"))

				createSecret(testVrg.GetName(), testNamespace.Name)
				createSC()
				createVSC()
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
					Expect(k8sClient.Get(testCtx, client.ObjectKeyFromObject(testVrg), testVrg)).To(Succeed())
					testVrg.Spec.VolSync.RDSpec = []ramendrv1alpha1.VolSyncReplicationDestinationSpec{
						{
							ProtectedPVC: ramendrv1alpha1.ProtectedPVC{
								Name:               "testingpvc-a",
								ProtectedByVolSync: true,
								StorageClassName:   &storageClassName,
								AccessModes:        testAccessModes,

								Resources: corev1.ResourceRequirements{Requests: testCapacity0},
							},
						},
						{
							ProtectedPVC: ramendrv1alpha1.ProtectedPVC{
								Name:               "testingpvc-b",
								ProtectedByVolSync: true,
								StorageClassName:   &storageClassName,
								AccessModes:        testAccessModes,

								Resources: corev1.ResourceRequirements{Requests: testCapacity1},
							},
						},
					}

					Eventually(func() error {
						return k8sClient.Update(testCtx, testVrg)
					}, testMaxWait, testInterval).Should(Succeed())

					Expect(k8sClient.Update(testCtx, testVrg)).To(Succeed())

					allRDs := &volsyncv1alpha1.ReplicationDestinationList{}
					Eventually(func() int {
						Expect(k8sClient.List(testCtx, allRDs,
							client.InNamespace(testNamespace.GetName()))).To(Succeed())

						return len(allRDs.Items)
					}, testMaxWait, testInterval).Should(Equal(len(testVrg.Spec.VolSync.RDSpec)))

					testLogger.Info("Found RDs", "allRDs", allRDs)

					Expect(k8sClient.Get(testCtx, types.NamespacedName{
						Name:      testVrg.Spec.VolSync.RDSpec[0].ProtectedPVC.Name,
						Namespace: testNamespace.GetName(),
					}, rd0)).To(Succeed())
					Expect(k8sClient.Get(testCtx, types.NamespacedName{
						Name:      testVrg.Spec.VolSync.RDSpec[1].ProtectedPVC.Name,
						Namespace: testNamespace.GetName(),
					}, rd1)).To(Succeed())
				})

				It("Should create ReplicationDestinations for each", func() {
					Expect(rd0.Spec.Trigger).To(BeNil()) // Rsync, so destination will not have schedule
					Expect(rd0.Spec.RsyncTLS).NotTo(BeNil())
					Expect(*rd0.Spec.RsyncTLS.KeySecret).To(Equal(volsync.GetVolSyncPSKSecretNameFromVRGName(testVrg.GetName())))
					Expect(*rd0.Spec.RsyncTLS.StorageClassName).To(Equal(testStorageClassName))
					Expect(rd0.Spec.RsyncTLS.AccessModes).To(Equal(testAccessModes))
					Expect(rd0.Spec.RsyncTLS.CopyMethod).To(Equal(volsyncv1alpha1.CopyMethodSnapshot))
					Expect(*rd0.Spec.RsyncTLS.ServiceType).To(Equal(corev1.ServiceTypeClusterIP))

					Expect(rd1.Spec.Trigger).To(BeNil()) // Rsync, so destination will not have schedule
					Expect(rd1.Spec.RsyncTLS).NotTo(BeNil())
					Expect(*rd1.Spec.RsyncTLS.KeySecret).To(Equal(volsync.GetVolSyncPSKSecretNameFromVRGName(testVrg.GetName())))
					Expect(*rd1.Spec.RsyncTLS.StorageClassName).To(Equal(testStorageClassName))
					Expect(rd1.Spec.RsyncTLS.AccessModes).To(Equal(testAccessModes))
					Expect(rd1.Spec.RsyncTLS.CopyMethod).To(Equal(volsyncv1alpha1.CopyMethodSnapshot))
					Expect(*rd1.Spec.RsyncTLS.ServiceType).To(Equal(corev1.ServiceTypeClusterIP))
				})

				Context("When ReplicationDestinations have address set in status", func() {
					rd0Address := "99.98.97.96"
					rd1Address := "99.88.77.66"
					JustBeforeEach(func() {
						// fake address set in status on the ReplicationDestinations
						rd0.Status = &volsyncv1alpha1.ReplicationDestinationStatus{
							RsyncTLS: &volsyncv1alpha1.ReplicationDestinationRsyncTLSStatus{
								Address: &rd0Address,
							},
						}
						Expect(k8sClient.Status().Update(testCtx, rd0)).To(Succeed())

						rd1.Status = &volsyncv1alpha1.ReplicationDestinationStatus{
							RsyncTLS: &volsyncv1alpha1.ReplicationDestinationRsyncTLSStatus{
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

//nolint:funlen
func createPVCBoundToRunningPod(ctx context.Context, namespace string,
	labels map[string]string, annotations map[string]string,
) *corev1.PersistentVolumeClaim {
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
			Annotations:  annotations,
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

	pvc.Status.Phase = corev1.ClaimBound
	pvc.Status.AccessModes = accessModes
	pvc.Status.Capacity = capacity
	Expect(k8sClient.Status().Update(ctx, pvc)).To(Succeed())

	// Create the pod which is mounting the pvc
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-mounting-pod-",
			Namespace:    namespace,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "c1",
					Image: "testimage123",
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "testvolume",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: pvc.GetName(),
						},
					},
				},
			},
		},
	}

	Expect(k8sClient.Create(ctx, pod)).To(Succeed())

	// Set the pod phase
	pod.Status.Phase = corev1.PodRunning

	pod.Status.Conditions = []corev1.PodCondition{
		{
			Type:   corev1.PodReady,
			Status: corev1.ConditionTrue,
		},
	}

	Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())

	return pvc
}

func createSecret(vrgName, namespace string) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      volsync.GetVolSyncPSKSecretNameFromVRGName(vrgName),
			Namespace: namespace,
		},
	}
	Expect(k8sClient.Create(context.TODO(), secret)).To(Succeed())
	Expect(secret.GetName()).NotTo(BeEmpty())
}

func createSC() {
	sc := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: testStorageClassName,
		},
		Provisioner: "manual.storage.com",
	}

	err := k8sClient.Create(context.TODO(), sc)
	if err != nil {
		if errors.IsAlreadyExists(err) {
			err = k8sClient.Get(context.TODO(), types.NamespacedName{Name: sc.Name}, sc)
		}
	}

	Expect(err).NotTo(HaveOccurred(),
		"failed to create/get StorageClass %s", sc.Name)
}

func createVSC() {
	vsc := &snapv1.VolumeSnapshotClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: testVolumeSnapshotClass,
		},
		Driver:         "manual.storage.com",
		DeletionPolicy: snapv1.VolumeSnapshotContentDelete,
	}

	err := k8sClient.Create(context.TODO(), vsc)
	if err != nil {
		if errors.IsAlreadyExists(err) {
			err = k8sClient.Get(context.TODO(), types.NamespacedName{Name: vsc.Name}, vsc)
		}
	}

	Expect(err).NotTo(HaveOccurred(),
		"failed to create/get StorageClass %s", vsc.Name)
}
