// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package volsync_test

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v4/apis/volumesnapshot/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/util"
	"github.com/ramendr/ramen/controllers/volsync"
)

const (
	maxWait  = 10 * time.Second
	interval = 250 * time.Millisecond

	APIGrp = "snapshot.storage.k8s.io"

	testCleanupLabel      = "clean-me-up-after-test"
	testCleanupLabelValue = "true"
)

var _ = Describe("VolSync Handler - utils", func() {
	Context("When converting scheduling interval to cronspec for VolSync", func() {
		It("Should successfully convert an interval specified in minutes", func() {
			cronSpecSchedule, err := volsync.ConvertSchedulingIntervalToCronSpec("10m")
			Expect(err).NotTo((HaveOccurred()))
			Expect(cronSpecSchedule).ToNot(BeNil())
			Expect(*cronSpecSchedule).To(Equal("*/10 * * * *"))
		})
		It("Should successfully convert an interval specified in minutes (case-insensitive)", func() {
			cronSpecSchedule, err := volsync.ConvertSchedulingIntervalToCronSpec("2M")
			Expect(err).NotTo((HaveOccurred()))
			Expect(cronSpecSchedule).ToNot(BeNil())
			Expect(*cronSpecSchedule).To(Equal("*/2 * * * *"))
		})
		It("Should successfully convert an interval specified in hours", func() {
			cronSpecSchedule, err := volsync.ConvertSchedulingIntervalToCronSpec("12h")
			Expect(err).NotTo((HaveOccurred()))
			Expect(cronSpecSchedule).ToNot(BeNil())
			Expect(*cronSpecSchedule).To(Equal("0 */12 * * *"))
		})
		It("Should successfully convert an interval specified in days", func() {
			cronSpecSchedule, err := volsync.ConvertSchedulingIntervalToCronSpec("13d")
			Expect(err).NotTo((HaveOccurred()))
			Expect(cronSpecSchedule).ToNot(BeNil())
			Expect(*cronSpecSchedule).To(Equal("0 0 */13 * *"))
		})
		It("Should fail if interval is invalid (no num)", func() {
			_, err := volsync.ConvertSchedulingIntervalToCronSpec("d")
			Expect(err).To((HaveOccurred()))
		})
		It("Should fail if interval is invalid (no m/h/d)", func() {
			_, err := volsync.ConvertSchedulingIntervalToCronSpec("123")
			Expect(err).To((HaveOccurred()))
		})
	})
})

var _ = Describe("VolSync Handler - Volume Replication Class tests", func() {
	asyncSpec := &ramendrv1alpha1.VRGAsyncSpec{
		SchedulingInterval:          "1h",
		VolumeSnapshotClassSelector: metav1.LabelSelector{},
	}

	Describe("Get volume snapshot classes", func() {
		Context("With no label selector", func() {
			var vsHandler *volsync.VSHandler

			BeforeEach(func() {
				vsHandler = volsync.NewVSHandler(ctx, k8sClient, logger, nil, asyncSpec, "none", "Snapshot", false)
			})

			It("GetVolumeSnapshotClasses() should find all volume snapshot classes", func() {
				vsClasses, err := vsHandler.GetVolumeSnapshotClasses()
				Expect(err).NotTo(HaveOccurred())

				Expect(len(vsClasses)).To(Equal(totalVolumeSnapshotClassCount))
			})

			It("GetVolumeSnapshotClassFromPVCStorageClass() should find the default volume snapshot class "+
				"that matches the driver from the storageclass", func() {
				vsClassName, err := vsHandler.GetVolumeSnapshotClassFromPVCStorageClass(&testStorageClassName)
				Expect(err).NotTo(HaveOccurred())

				Expect(vsClassName).To(Equal(testDefaultVolumeSnapshotClass.GetName()))
			})
		})

		Context("With simple label selector", func() {
			var vsHandler *volsync.VSHandler

			BeforeEach(func() {
				asyncSpec.VolumeSnapshotClassSelector = metav1.LabelSelector{
					MatchLabels: map[string]string{
						"i-like-ramen": "true",
					},
				}

				vsHandler = volsync.NewVSHandler(ctx, k8sClient, logger, nil, asyncSpec, "none", "Snapshot", false)
			})

			It("GetVolumeSnapshotClasses() should find matching volume snapshot classes", func() {
				vsClasses, err := vsHandler.GetVolumeSnapshotClasses()
				Expect(err).NotTo(HaveOccurred())

				Expect(len(vsClasses)).To(Equal(2))

				vscAFound := false
				vscBFound := false
				for _, vsc := range vsClasses {
					if vsc.GetName() == volumeSnapshotClassA.GetName() {
						vscAFound = true
					}
					if vsc.GetName() == volumeSnapshotClassB.GetName() {
						vscBFound = true
					}
				}
				Expect(vscAFound).To(BeTrue())
				Expect(vscBFound).To(BeTrue())
			})

			It("GetVolumeSnapshotClassFromPVCStorageClass() should not find a match if no volume snapshot "+
				"classes matche the driver from the storageclass", func() {
				vsClassName, err := vsHandler.GetVolumeSnapshotClassFromPVCStorageClass(&testStorageClassName)
				Expect(err).To(HaveOccurred())
				Expect(vsClassName).To(Equal(""))
				Expect(err.Error()).To(ContainSubstring("unable to find matching volumesnapshotclass"))
			})
		})

		Context("With more complex label selector", func() {
			var vsHandler *volsync.VSHandler

			BeforeEach(func() {
				asyncSpec.VolumeSnapshotClassSelector = metav1.LabelSelector{
					MatchLabels: map[string]string{
						"i-like-ramen": "true",
						"abc":          "b",
					},
				}

				vsHandler = volsync.NewVSHandler(ctx, k8sClient, logger, nil, asyncSpec, "none", "Snapshot", false)
			})

			It("GetVolumeSnapshotClasses() should find matching volume snapshot classes", func() {
				vsClasses, err := vsHandler.GetVolumeSnapshotClasses()
				Expect(err).NotTo(HaveOccurred())

				Expect(len(vsClasses)).To(Equal(1))
				Expect(vsClasses[0].GetName()).To(Equal(volumeSnapshotClassB.GetName()))
			})

			It("GetVolumeSnapshotClassFromPVCStorageClass() should not find a match if no volume snapshot "+
				"classes matche the driver from the storageclass", func() {
				storageClassName := storageClassAandB.GetName()
				vsClassName, err := vsHandler.GetVolumeSnapshotClassFromPVCStorageClass(&storageClassName)
				Expect(err).NotTo(HaveOccurred())
				Expect(vsClassName).To(Equal(volumeSnapshotClassB.GetName()))
			})
		})
	})

	Describe("ModifyRSSpecForCephFS", func() {
		var vsHandler *volsync.VSHandler
		var testNamespace *corev1.Namespace
		var testSourcePVC *corev1.PersistentVolumeClaim
		var testRsSpec ramendrv1alpha1.VolSyncReplicationSourceSpec
		var testRsSpecOrig ramendrv1alpha1.VolSyncReplicationSourceSpec

		capacity := resource.MustParse("1Gi")

		BeforeEach(func() {
			// Create namespace for test
			testNamespace = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "vh-cephfs-tests-",
				},
			}
			Expect(k8sClient.Create(ctx, testNamespace)).To(Succeed())

			// Basic source PVC
			testSourcePVC = &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "cephfs-test-pvc-",
					Namespace:    testNamespace.GetName(),
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: capacity,
						},
					},
				},
			}

			// Initialize a vshandler
			vsHandler = volsync.NewVSHandler(ctx, k8sClient, logger, nil, asyncSpec,
				"openshift-storage.cephfs.csi.ceph.com", "Snapshot", false)
		})

		JustBeforeEach(func() {
			// Create source PVC in justBeforeEach so it can be modified by tests prior to creation
			Expect(k8sClient.Create(ctx, testSourcePVC)).To(Succeed())

			// Make sure the pvc is created to avoid any timing issues
			Eventually(func() error {
				return k8sClient.Get(ctx, client.ObjectKeyFromObject(testSourcePVC), testSourcePVC)
			}, maxWait, interval).Should(Succeed())

			// Create an RSSpec from the testSourcePVC
			protectedPVC := &ramendrv1alpha1.ProtectedPVC{
				Name:               testSourcePVC.Name,
				ProtectedByVolSync: true,
				StorageClassName:   testSourcePVC.Spec.StorageClassName,
				Labels:             testSourcePVC.Labels,
				AccessModes:        testSourcePVC.Spec.AccessModes,
				Resources:          testSourcePVC.Spec.Resources,
			}
			testRsSpec = ramendrv1alpha1.VolSyncReplicationSourceSpec{
				ProtectedPVC: *protectedPVC,
			}

			testRsSpecOrig = *testRsSpec.DeepCopy() // Save copy of the original so it can be compared by tests

			// Load the storageclass to ensure we're loading the one in the rsSpec
			storageClassForTest := &storagev1.StorageClass{}
			Expect(k8sClient.Get(ctx,
				types.NamespacedName{
					Name:      *testRsSpec.ProtectedPVC.StorageClassName,
					Namespace: testNamespace.GetName(),
				},
				storageClassForTest)).To(Succeed())

			//
			// Call ModifyRSSpecForCephFS
			//
			Expect(vsHandler.ModifyRSSpecForCephFS(&testRsSpec, storageClassForTest)).To(Succeed())
		})

		Context("When the source PVC is not using a cephfs storageclass", func() {
			BeforeEach(func() {
				// Make sure the source PVC uses a non cephfs storageclass
				testSourcePVC.Spec.StorageClassName = &testStorageClassName
			})

			It("ModifyRSSpecForCephFS should not modify the rsSpec", func() {
				Expect(testRsSpecOrig).To(Equal(testRsSpec))
			})
		})

		Context("When the sourcePVC is using a cephfs storageclass", func() {
			customBackingSnapshotStorageClassName := testCephFSStorageClassName + "-vrg"

			BeforeEach(func() {
				// Make sure the source PVC uses the cephfs storageclass
				testSourcePVC.Spec.StorageClassName = &testCephFSStorageClassName
			})

			JustBeforeEach(func() {
				// Common tests - rsSpec should be modified with settings to allow pvc from snapshot
				// to use our custom cephfs storageclass and ReadOnlyMany accessModes
				Expect(testRsSpecOrig).NotTo(Equal(testRsSpec))

				// Should use the custom storageclass with backingsnapshot: true parameter
				Expect(*testRsSpec.ProtectedPVC.StorageClassName).To(Equal(customBackingSnapshotStorageClassName))

				// AccessModes should be updated to ReadOnlyMany
				Expect(testRsSpec.ProtectedPVC.AccessModes).To(Equal(
					[]corev1.PersistentVolumeAccessMode{
						corev1.ReadOnlyMany,
					}))
			})

			AfterEach(func() {
				// Delete the custom storage class that may have been created by test
				custStorageClass := &storagev1.StorageClass{
					ObjectMeta: metav1.ObjectMeta{
						Name: customBackingSnapshotStorageClassName,
					},
				}
				err := k8sClient.Delete(ctx, custStorageClass)
				if err != nil {
					Expect(kerrors.IsNotFound(err)).To(BeTrue())
				}

				Eventually(func() bool {
					err := k8sClient.Get(ctx, client.ObjectKeyFromObject(custStorageClass), custStorageClass)

					return kerrors.IsNotFound(err)
				}, maxWait, interval).Should(BeTrue())
			})

			Context("When the custom cephfs backing storage class for readonly pvc from snap does not exist", func() {
				// Delete the custom vrg storageclass if it exists
				BeforeEach(func() {
					custStorageClass := &storagev1.StorageClass{
						ObjectMeta: metav1.ObjectMeta{
							Name: customBackingSnapshotStorageClassName,
						},
					}
					err := k8sClient.Delete(ctx, custStorageClass)
					if err != nil {
						Expect(kerrors.IsNotFound(err)).To(BeTrue())
					}

					Eventually(func() bool {
						err := k8sClient.Get(ctx, client.ObjectKeyFromObject(custStorageClass), custStorageClass)

						return kerrors.IsNotFound(err)
					}, maxWait, interval).Should(BeTrue())
				})

				It("ModifyRSSpecForCephFS should modify the rsSpec and create the new storageclass", func() {
					// RSspec modification checks in the outer context JustBeforeEach()

					newStorageClass := &storagev1.StorageClass{
						ObjectMeta: metav1.ObjectMeta{
							Name: customBackingSnapshotStorageClassName,
						},
					}

					Eventually(func() error {
						return k8sClient.Get(ctx, client.ObjectKeyFromObject(newStorageClass), newStorageClass)
					}, maxWait, interval).Should(Succeed())

					Expect(newStorageClass.Parameters["backingSnapshot"]).To(Equal("true"))

					// Other parameters from the test cephfs storageclass should be copied over
					for k, v := range testCephFSStorageClass.Parameters {
						Expect(newStorageClass.Parameters[k]).To(Equal(v))
					}
				})
			})

			Context("When the custom cephfs backing storage class for readonly pvc from snap exists", func() {
				var preExistingCustStorageClass *storagev1.StorageClass

				BeforeEach(func() {
					preExistingCustStorageClass = &storagev1.StorageClass{
						ObjectMeta: metav1.ObjectMeta{
							Name: customBackingSnapshotStorageClassName,
						},
						Provisioner: testCephFSStorageDriverName,
						Parameters: map[string]string{ // Not the same params as our CephFS storageclass for test
							"different-param-1": "abc",
							"different-param-2": "def",
							"backingSnapshot":   "true",
						},
					}
					Expect(k8sClient.Create(ctx, preExistingCustStorageClass)).To(Succeed())

					// Confirm it's created
					Eventually(func() error {
						return k8sClient.Get(ctx,
							client.ObjectKeyFromObject(preExistingCustStorageClass), preExistingCustStorageClass)
					}, maxWait, interval).Should(Succeed())
				})

				It("ModifyRSSpecForCephFS should modify the rsSpec but not modify the new custom storageclass", func() {
					// Load the custom storageclass
					newStorageClass := &storagev1.StorageClass{
						ObjectMeta: metav1.ObjectMeta{
							Name: customBackingSnapshotStorageClassName,
						},
					}

					Eventually(func() error {
						return k8sClient.Get(ctx, client.ObjectKeyFromObject(newStorageClass), newStorageClass)
					}, maxWait, interval).Should(Succeed())

					// Parameters should match the original, unmodified
					Expect(newStorageClass.Parameters).To(Equal(preExistingCustStorageClass.Parameters))
				})
			})
		})
	})
})

var _ = Describe("VolSync_Handler", func() {
	var testNamespace *corev1.Namespace
	var owner metav1.Object
	var vsHandler *volsync.VSHandler

	asyncSpec := &ramendrv1alpha1.VRGAsyncSpec{
		SchedulingInterval:          "5m",
		VolumeSnapshotClassSelector: metav1.LabelSelector{},
	}
	expectedCronSpecSchedule := "*/5 * * * *"

	BeforeEach(func() {
		// Create namespace for test
		testNamespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "vh-",
			},
		}
		Expect(k8sClient.Create(ctx, testNamespace)).To(Succeed())
		Expect(testNamespace.GetName()).NotTo(BeEmpty())

		// Create dummy resource to be the "owner" of the RDs and RSs
		// Using a configmap for now - in reality this owner resource will
		// be a VRG
		ownerCm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "dummycm-owner-",
				Namespace:    testNamespace.GetName(),
			},
		}
		Expect(k8sClient.Create(ctx, ownerCm)).To(Succeed())
		Expect(ownerCm.GetName()).NotTo(BeEmpty())
		owner = ownerCm

		vsHandler = volsync.NewVSHandler(ctx, k8sClient, logger, owner, asyncSpec, "none", "Snapshot", false)
	})

	AfterEach(func() {
		// All resources are namespaced, so this should clean it all up
		Expect(k8sClient.Delete(ctx, testNamespace)).To(Succeed(),
			"none")
	})

	Describe("Reconcile ReplicationDestination", func() {
		Context("When reconciling RDSpec", func() {
			capacity := resource.MustParse("2Gi")

			rdSpec := ramendrv1alpha1.VolSyncReplicationDestinationSpec{
				ProtectedPVC: ramendrv1alpha1.ProtectedPVC{
					Name:               "mytestpvc",
					ProtectedByVolSync: true,
					StorageClassName:   &testStorageClassName,
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: capacity,
						},
					},
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				},
			}

			createdRD := &volsyncv1alpha1.ReplicationDestination{}
			var returnedRD *volsyncv1alpha1.ReplicationDestination

			Context("When the psk secret for volsync does not exist", func() {
				JustBeforeEach(func() {
					// Run ReconcileRD
					var err error
					rdSpec.ProtectedPVC.Namespace = testNamespace.GetName()
					returnedRD, err = vsHandler.ReconcileRD(rdSpec)
					Expect(err).ToNot(HaveOccurred())
				})

				It("Should return a nil replication destination and not create an RD yet", func() {
					Expect(returnedRD).To(BeNil())

					// ReconcileRD should not have created the replication destination - since the secret isn't there
					Consistently(func() error {
						return k8sClient.Get(ctx,
							types.NamespacedName{Name: rdSpec.ProtectedPVC.Name, Namespace: testNamespace.GetName()}, createdRD)
					}, 1*time.Second, interval).ShouldNot(BeNil())
				})
			})

			Context("When the psk secret for volsync exists (will be pushed down by drpc from hub", func() {
				var dummyPSKSecret *corev1.Secret
				JustBeforeEach(func() {
					rdSpec.ProtectedPVC.Namespace = testNamespace.GetName()
					// Create a dummy volsync psk secret so the reconcile can proceed properly
					dummyPSKSecret = &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      volsync.GetVolSyncPSKSecretNameFromVRGName(owner.GetName()),
							Namespace: testNamespace.GetName(),
						},
					}
					Expect(k8sClient.Create(ctx, dummyPSKSecret)).To(Succeed())
					Expect(dummyPSKSecret.GetName()).NotTo(BeEmpty())

					// Make sure the secret is created to avoid any timing issues
					Eventually(func() error {
						return k8sClient.Get(ctx,
							types.NamespacedName{
								Name:      dummyPSKSecret.GetName(),
								Namespace: dummyPSKSecret.GetNamespace(),
							}, dummyPSKSecret)
					}, maxWait, interval).Should(Succeed())
				})

				Context("When a RS exists for the pvc, failover scenario (primary -> secondary)", func() {
					var rs *volsyncv1alpha1.ReplicationSource
					JustBeforeEach(func() {
						// Pre-create an RS for the PVC (simulate scenario where primary has failed over to secondary)
						// so the replication source is still present
						rs = &volsyncv1alpha1.ReplicationSource{
							ObjectMeta: metav1.ObjectMeta{
								Name:      rdSpec.ProtectedPVC.Name, // RS should be named based on the pvc
								Namespace: testNamespace.GetName(),
								Labels: map[string]string{
									// Need to simulate that it's owned by our VRG by using our label
									volsync.VRGOwnerLabel: owner.GetName(),
								},
							},
							Spec: volsyncv1alpha1.ReplicationSourceSpec{},
						}
						Expect(k8sClient.Create(ctx, rs)).To(Succeed())

						// Make sure the replicationsource is created to avoid any timing issues
						Eventually(func() error {
							return k8sClient.Get(ctx, client.ObjectKeyFromObject(rs), rs)
						}, maxWait, interval).Should(Succeed())

						// Run ReconcileRD
						var err error
						_, err = vsHandler.ReconcileRD(rdSpec)
						Expect(err).ToNot(HaveOccurred())
					})

					It("Should delete the existing ReplicationSource", func() {
						Eventually(func() bool {
							err := k8sClient.Get(ctx, client.ObjectKeyFromObject(rs), rs)

							return kerrors.IsNotFound(err)
						}, maxWait, interval).Should(BeTrue())
					})
				})

				Context("When reconciling RD with no previous RS", func() {
					JustBeforeEach(func() {
						// Run ReconcileRD
						var err error
						returnedRD, err = vsHandler.ReconcileRD(rdSpec)
						Expect(err).ToNot(HaveOccurred())

						// RD should be created with name=PVCName
						Eventually(func() error {
							return k8sClient.Get(ctx, types.NamespacedName{
								Name:      rdSpec.ProtectedPVC.Name,
								Namespace: testNamespace.GetName(),
							}, createdRD)
						}, maxWait, interval).Should(Succeed())

						// Expect the RD should be owned by owner
						Expect(ownerMatches(createdRD, owner.GetName(), "ConfigMap", true /*should be controller*/)).To(BeTrue())

						// Check common fields
						Expect(createdRD.Spec.RsyncTLS).NotTo(BeNil())
						Expect(createdRD.Spec.RsyncTLS.CopyMethod).To(Equal(volsyncv1alpha1.CopyMethodSnapshot))
						// Note owner here is faking out a VRG - psk key name will be based on the owner (VRG) name
						Expect(*createdRD.Spec.RsyncTLS.KeySecret).To(Equal(volsync.GetVolSyncPSKSecretNameFromVRGName(owner.GetName())))
						Expect(*createdRD.Spec.RsyncTLS.Capacity).To(Equal(capacity))
						Expect(createdRD.Spec.RsyncTLS.AccessModes).To(Equal([]corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}))
						Expect(*createdRD.Spec.RsyncTLS.StorageClassName).To(Equal(testStorageClassName))
						Expect(*createdRD.Spec.RsyncTLS.VolumeSnapshotClassName).To(Equal(testVolumeSnapshotClassName))
						Expect(createdRD.Spec.Trigger).To(BeNil()) // No schedule should be set
						Expect(createdRD.GetLabels()).To(HaveKeyWithValue(volsync.VRGOwnerLabel, owner.GetName()))
						Expect(*createdRD.Spec.RsyncTLS.ServiceType).To(Equal(volsync.DefaultRsyncServiceType))

						// Check that the secret has been updated to have our vrg as owner
						Eventually(func() bool {
							err := k8sClient.Get(ctx, client.ObjectKeyFromObject(dummyPSKSecret), dummyPSKSecret)
							if err != nil {
								return false
							}

							// The psk secret should be updated to be owned by the VRG
							return ownerMatches(dummyPSKSecret, owner.GetName(), "ConfigMap", false)
						}, maxWait, interval).Should(BeTrue())

						// Check that the service export is created for this RD
						svcExport := &unstructured.Unstructured{}
						svcExport.SetGroupVersionKind(schema.GroupVersionKind{
							Group:   volsync.ServiceExportGroup,
							Kind:    volsync.ServiceExportKind,
							Version: volsync.ServiceExportVersion,
						})
						Eventually(func() error {
							return k8sClient.Get(ctx, client.ObjectKey{
								Name:      fmt.Sprintf("volsync-rsync-tls-dst-%s", createdRD.GetName()),
								Namespace: createdRD.GetNamespace(),
							}, svcExport)
						}, maxWait, interval).Should(Succeed())

						// The created service export should be owned by the replication destination, not our VRG
						Expect(ownerMatches(svcExport, createdRD.GetName(), "ReplicationDestination", false)).To(BeTrue())
					})

					Context("When replication destination already exists with status.address specified", func() {
						myTestAddress := "https://fakeaddress.abc.org:8888"
						BeforeEach(func() {
							// Pre-create a replication destination - and fill out Status.Address
							rdPrecreate := &volsyncv1alpha1.ReplicationDestination{
								ObjectMeta: metav1.ObjectMeta{
									Name:      rdSpec.ProtectedPVC.Name,
									Namespace: testNamespace.GetName(),
								},
								// Empty spec - will expect the reconcile to fill this out properly for us (i.e. update)
								Spec: volsyncv1alpha1.ReplicationDestinationSpec{},
							}
							Expect(k8sClient.Create(ctx, rdPrecreate)).To(Succeed())

							//
							// Make sure the RD is created and update Status to set an address
							// (Simulating what the volsync controller would do)
							//
							Eventually(func() error {
								return k8sClient.Get(ctx, client.ObjectKeyFromObject(rdPrecreate), rdPrecreate)
							}, maxWait, interval).Should(Succeed())

							// Fake the address and latestImage in the status
							rdPrecreate.Status = &volsyncv1alpha1.ReplicationDestinationStatus{
								RsyncTLS: &volsyncv1alpha1.ReplicationDestinationRsyncTLSStatus{
									Address: &myTestAddress,
								},
							}
							Expect(k8sClient.Status().Update(ctx, rdPrecreate)).To(Succeed())
							Eventually(func() *string {
								err := k8sClient.Get(ctx, client.ObjectKeyFromObject(rdPrecreate), rdPrecreate)
								if err != nil || rdPrecreate.Status == nil || rdPrecreate.Status.RsyncTLS == nil {
									return nil
								}

								return rdPrecreate.Status.RsyncTLS.Address
							}, maxWait, interval).Should(Not(BeNil()))
						})

						It("Should properly update Replication destination and return rd", func() {
							// Common JustBeforeEach will run reconcileRD and check spec is proper
							// Expect RDInfo to NOT be nil - address was filled out so it should have been returned
							Expect(returnedRD).ToNot(BeNil())
						})
					})
				})
			})

			Context("With CopyMethod 'Direct'", func() {
				var vsHandler *volsync.VSHandler

				BeforeEach(func() {
					rdSpec.ProtectedPVC.Namespace = testNamespace.GetName()
					vsHandler = volsync.NewVSHandler(ctx, k8sClient, logger, owner, asyncSpec, "none", "Direct", false)
				})

				It("PrecreateDestPVCIfEnabled() should return CopyMethod Snapshot and App PVC name", func() {
					dstPVC, err := vsHandler.PrecreateDestPVCIfEnabled(rdSpec)
					Expect(err).NotTo(HaveOccurred())

					Expect(*dstPVC).To(Equal(rdSpec.ProtectedPVC.Name))
					pvc := &corev1.PersistentVolumeClaim{}
					Eventually(func() error {
						return k8sClient.Get(ctx, types.NamespacedName{
							Name:      rdSpec.ProtectedPVC.Name,
							Namespace: testNamespace.GetName(),
						}, pvc)
					}, maxWait, interval).Should(Succeed())

					Expect(pvc.GetName()).To(Equal(rdSpec.ProtectedPVC.Name))
					Expect(pvc.GetOwnerReferences()[0].Kind).To(Equal("ConfigMap"))
				})
			})
		})
	})

	Describe("Reconcile ReplicationSource", func() {
		Context("When reconciling RSSpec", func() {
			capacity := resource.MustParse("3Gi")
			testPVCName := "mytestpvc"

			rsSpec := ramendrv1alpha1.VolSyncReplicationSourceSpec{
				ProtectedPVC: ramendrv1alpha1.ProtectedPVC{
					Name:               testPVCName,
					ProtectedByVolSync: true,
					StorageClassName:   &testStorageClassName,
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: capacity,
						},
					},
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				},
			}

			createdRS := &volsyncv1alpha1.ReplicationSource{}

			Context("When the psk secret for volsync does not exist", func() {
				var returnedRS *volsyncv1alpha1.ReplicationSource
				JustBeforeEach(func() {
					// Run ReconcileRD
					var err error
					var finalSyncCompl bool
					rsSpec.ProtectedPVC.Namespace = testNamespace.GetName()
					finalSyncCompl, returnedRS, err = vsHandler.ReconcileRS(rsSpec, false)
					Expect(err).ToNot(HaveOccurred())
					Expect(finalSyncCompl).To(BeFalse())
				})

				It("Should return a nil replication source and not create an RS yet", func() {
					Expect(returnedRS).To(BeNil())

					// ReconcileRS should not have created the replication source - since the secret isn't there
					Consistently(func() error {
						return k8sClient.Get(ctx,
							types.NamespacedName{Name: rsSpec.ProtectedPVC.Name, Namespace: testNamespace.GetName()}, createdRS)
					}, 1*time.Second, interval).ShouldNot(BeNil())
				})
			})

			Context("When the psk secret for volsync exists (will be pushed down by drpc from hub", func() {
				var dummyPSKSecret *corev1.Secret
				JustBeforeEach(func() {
					rsSpec.ProtectedPVC.Namespace = testNamespace.GetName()
					// Create a dummy volsync psk secret so the reconcile can proceed properly
					dummyPSKSecret = &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      volsync.GetVolSyncPSKSecretNameFromVRGName(owner.GetName()),
							Namespace: testNamespace.GetName(),
						},
					}
					Expect(k8sClient.Create(ctx, dummyPSKSecret)).To(Succeed())
					Expect(dummyPSKSecret.GetName()).NotTo(BeEmpty())

					// Make sure the secret is created to avoid any timing issues
					Eventually(func() error {
						return k8sClient.Get(ctx, types.NamespacedName{
							Name:      dummyPSKSecret.GetName(),
							Namespace: dummyPSKSecret.GetNamespace(),
						}, dummyPSKSecret)
					}, maxWait, interval).Should(Succeed())
				})

				Context("When no running pod is mounting the PVC to be protected", func() {
					It("Should return a nil replication source and no RS should be created", func() {
						// Run another reconcile - we have the psk secret now but the pvc is not in use by
						// a running pod
						finalSyncCompl, rs, err := vsHandler.ReconcileRS(rsSpec, false)
						Expect(err).ToNot(HaveOccurred())
						Expect(finalSyncCompl).To(BeFalse())
						Expect(rs).To(BeNil())

						// ReconcileRS should not have created the replication source - since the secret isn't there
						Consistently(func() error {
							return k8sClient.Get(ctx,
								types.NamespacedName{Name: rsSpec.ProtectedPVC.Name, Namespace: testNamespace.GetName()}, createdRS)
						}, 1*time.Second, interval).ShouldNot(BeNil())
					})
				})

				Context("When the PVC to be protected is mounted by a pod that is NOT in running phase", func() {
					JustBeforeEach(func() {
						// Create PVC and pod that is mounting it - pod phase will be "Pending"
						createDummyPVCAndMountingPod(testPVCName, testNamespace.GetName(),
							capacity, map[string]string{"a": "b"}, corev1.PodPending, false)
					})

					It("Should return a nil replication source and no RS should be created", func() {
						// Run another reconcile - a pod is mounting the PVC but it is not in running phase
						finalSyncCompl, rs, err := vsHandler.ReconcileRS(rsSpec, false)
						Expect(err).ToNot(HaveOccurred())
						Expect(finalSyncCompl).To(BeFalse())
						Expect(rs).To(BeNil())

						// ReconcileRS should not have created the RS - since the pod is not in running phase
						Consistently(func() error {
							return k8sClient.Get(ctx,
								types.NamespacedName{Name: rsSpec.ProtectedPVC.Name, Namespace: testNamespace.GetName()}, createdRS)
						}, 1*time.Second, interval).ShouldNot(BeNil())
					})
				})

				Context("When the PVC to be protected is mounted by a pod that is NOT Ready", func() {
					JustBeforeEach(func() {
						// Create PVC and pod that is mounting it (pod phase will be "Pending" by default)
						createDummyPVCAndMountingPod(testPVCName, testNamespace.GetName(),
							capacity, map[string]string{"a": "b"}, corev1.PodRunning, false /* not ready */)
					})

					It("Should return a nil replication source and no RS should be created", func() {
						// Run another reconcile - a pod is mounting the PVC but it is not in running state
						// a running pod
						finalSyncCompl, rs, err := vsHandler.ReconcileRS(rsSpec, false)
						Expect(err).ToNot(HaveOccurred())
						Expect(finalSyncCompl).To(BeFalse())
						Expect(rs).To(BeNil())

						// ReconcileRS should not have created the RS - since the pod is not Ready
						Consistently(func() error {
							return k8sClient.Get(ctx,
								types.NamespacedName{Name: rsSpec.ProtectedPVC.Name, Namespace: testNamespace.GetName()}, createdRS)
						}, 1*time.Second, interval).ShouldNot(BeNil())
					})
				})

				Context("When the PVC to be protected is mounted by a running and Ready pod", func() {
					var podMountingPVC *corev1.Pod
					var testPVC *corev1.PersistentVolumeClaim

					// Fake out pod mounting and in Running/Ready state
					JustBeforeEach(func() {
						// Create PVC and pod that is mounting it (and set pod phase to "Running")
						testPVC, podMountingPVC = createDummyPVCAndMountingPod(testPVCName, testNamespace.GetName(),
							capacity, nil, corev1.PodRunning, true /* pod should be Ready */)
					})

					Context("When a RD exists for the pvc to protect, failover scenario (secondary -> primary)", func() {
						var rd *volsyncv1alpha1.ReplicationDestination
						JustBeforeEach(func() {
							// Pre-create an RD for the PVC (simulate scenario where secondary has failed over to primary)
							rd = &volsyncv1alpha1.ReplicationDestination{
								ObjectMeta: metav1.ObjectMeta{
									Name:      rsSpec.ProtectedPVC.Name,
									Namespace: testNamespace.GetName(),
									Labels: map[string]string{
										// Need to simulate that it's owned by our VRG by using our label
										volsync.VRGOwnerLabel: owner.GetName(),
									},
								},
								Spec: volsyncv1alpha1.ReplicationDestinationSpec{},
							}
							Expect(k8sClient.Create(ctx, rd)).To(Succeed())

							// Make sure the replicationdestination is created to avoid any timing issues
							Eventually(func() error {
								return k8sClient.Get(ctx, client.ObjectKeyFromObject(rd), rd)
							}, maxWait, interval).Should(Succeed())

							// Run ReconcileRS again - Not running final sync so this should return false
							finalSyncDone, returnedRS, err := vsHandler.ReconcileRS(rsSpec, false)
							Expect(err).ToNot(HaveOccurred())
							Expect(finalSyncDone).To(BeFalse())
							Expect(returnedRS).NotTo(BeNil())

							// RS should be created with name=PVCName
							Eventually(func() error {
								return k8sClient.Get(ctx, types.NamespacedName{
									Name:      rsSpec.ProtectedPVC.Name,
									Namespace: testNamespace.GetName(),
								}, createdRS)
							}, maxWait, interval).Should(Succeed())

							Expect(createdRS.Spec.RsyncTLS.AccessModes).To(Equal(rsSpec.ProtectedPVC.AccessModes))
							Expect(createdRS.Spec.RsyncTLS.StorageClassName).To(Equal(rsSpec.ProtectedPVC.StorageClassName))
						})

						It("Should delete the existing ReplicationDestination", func() {
							Eventually(func() bool {
								err := k8sClient.Get(ctx, client.ObjectKeyFromObject(rd), rd)

								return kerrors.IsNotFound(err)
							}, maxWait, interval).Should(BeTrue())
						})
					})

					Context("When reconciling RS with no previous RD", func() {
						var returnedRS *volsyncv1alpha1.ReplicationSource

						JustBeforeEach(func() {
							finalSyncDone := false
							var err error

							// Run ReconcileRS - Not running final sync so this should return false
							finalSyncDone, returnedRS, err = vsHandler.ReconcileRS(rsSpec, false)
							Expect(err).ToNot(HaveOccurred())
							Expect(finalSyncDone).To(BeFalse())

							// RS should be created with name=PVCName and owner is our vrg
							Eventually(func() bool {
								err := k8sClient.Get(ctx,
									types.NamespacedName{
										Name:      rsSpec.ProtectedPVC.Name,
										Namespace: testNamespace.GetName(),
									},
									createdRS)
								if err != nil {
									return false
								}

								return ownerMatches(createdRS, owner.GetName(), "ConfigMap",
									true /* Should be controller */)
							}, maxWait, interval).Should(BeTrue())

							// Check that the volsync psk secret has been updated to have our vrg as owner
							Eventually(func() bool {
								err := k8sClient.Get(ctx, client.ObjectKeyFromObject(dummyPSKSecret), dummyPSKSecret)
								if err != nil {
									return false
								}

								// The psk secret should be updated to be owned by the VRG
								return ownerMatches(dummyPSKSecret, owner.GetName(), "ConfigMap", false)
							}, maxWait, interval).Should(BeTrue())

							// Check common fields
							Expect(createdRS.Spec.SourcePVC).To(Equal(rsSpec.ProtectedPVC.Name))
							Expect(createdRS.Spec.RsyncTLS).NotTo(BeNil())
							Expect(createdRS.Spec.RsyncTLS.CopyMethod).To(Equal(volsyncv1alpha1.CopyMethodSnapshot))
							// Note owner here is faking out a VRG - psk secret name will be based on the owner (VRG) name
							Expect(*createdRS.Spec.RsyncTLS.KeySecret).To(Equal(volsync.GetVolSyncPSKSecretNameFromVRGName(owner.GetName())))
							Expect(*createdRS.Spec.RsyncTLS.Address).To(Equal("volsync-rsync-tls-dst-" +
								rsSpec.ProtectedPVC.Name + "." + testNamespace.GetName() + ".svc.clusterset.local"))

							Expect(*createdRS.Spec.RsyncTLS.VolumeSnapshotClassName).To(Equal(testVolumeSnapshotClassName))

							Expect(createdRS.Spec.Trigger).ToNot(BeNil())
							Expect(createdRS.Spec.Trigger).To(Equal(&volsyncv1alpha1.ReplicationSourceTriggerSpec{
								Schedule: &expectedCronSpecSchedule,
							}))
							Expect(createdRS.GetLabels()).To(HaveKeyWithValue(volsync.VRGOwnerLabel, owner.GetName()))
						})

						It("Should create an ReplicationSource if one does not exist", func() {
							// All checks here performed in the JustBeforeEach(common checks)
							Expect(returnedRS).NotTo(BeNil())
						})

						Context("When replication source already exists", func() {
							var rsPrecreate *volsyncv1alpha1.ReplicationSource

							BeforeEach(func() {
								// Pre-create a replication destination - and fill out Status.Address
								rsPrecreate = &volsyncv1alpha1.ReplicationSource{
									ObjectMeta: metav1.ObjectMeta{
										Name:      rsSpec.ProtectedPVC.Name,
										Namespace: testNamespace.GetName(),
										Labels: map[string]string{
											"customlabel1": "somevaluehere",
										},
									},
									// Will expect the reconcile to fill this out properly for us (i.e. update)
									Spec: volsyncv1alpha1.ReplicationSourceSpec{
										RsyncTLS: &volsyncv1alpha1.ReplicationSourceRsyncTLSSpec{},
									},
								}
								Expect(k8sClient.Create(ctx, rsPrecreate)).To(Succeed())

								//
								// Make sure the RS is created
								//
								Eventually(func() error {
									return k8sClient.Get(ctx, client.ObjectKeyFromObject(rsPrecreate), rsPrecreate)
								}, maxWait, interval).Should(Succeed())
							})

							It("Should properly update ReplicationSource and return rsInfo", func() {
								// all checks here performed in the JustBeforeEach(common checks)
								Expect(returnedRS).NotTo(BeNil())
							})

							It("Should expect reconcileRS to return a replicationsource", func() {
								// reconcile should return our RS
								Expect(returnedRS).NotTo(BeNil())
							})

							Context("When running a final sync", func() {
								// For these tests, final sync should look at pods to determine whether the PVC
								// is still in-use before running the final sync - it should first check if any pods
								// are mounting the PVC, if not, then also check volume attachments
								// volume attachments
								Context("When the pvc is still in use by a pod", func() {
									It("Should not complete the final sync", func() {
										finalSyncDone, returnedRS, err := vsHandler.ReconcileRS(rsSpec, true)
										Expect(err).NotTo(HaveOccurred()) // Not considered an error, we should just wait
										Expect(returnedRS).NotTo(BeNil()) // Should return the existing RS
										Expect(finalSyncDone).To(BeFalse())
									})
								})

								Context("When the pvc is no longer in use by a pod", func() {
									JustBeforeEach(func() {
										// Pod mounting the PVC is created above - delete the pod to simulate removing app
										Expect(k8sClient.Delete(ctx, podMountingPVC)).To(Succeed())
										Eventually(func() bool {
											err := k8sClient.Get(ctx, client.ObjectKeyFromObject(podMountingPVC), podMountingPVC)

											return kerrors.IsNotFound(err)
										}, maxWait, interval).Should(BeTrue())
									})

									Context("When a volumeattachment exists for the PV backing the PVC", func() {
										JustBeforeEach(func() {
											// Create a volume attachment - even though the pod is not mounting,
											// the PVC anymore, the presence of the volume attachment means we should still
											// wait before running the final sync
											createDummyVolumeAttachmentForPVC(testPVC)
										})

										AfterEach(func() {
											// Cleans up the volume attachment created above if it's left behind
											cleanupNonNamespacedResources()
										})

										It("Should not complete the final sync", func() {
											finalSyncDone, returnedRS, err := vsHandler.ReconcileRS(rsSpec, true)
											Expect(err).NotTo(HaveOccurred()) // Not considered an error, we should just wait
											Expect(returnedRS).NotTo(BeNil()) // Should return existing RS
											Expect(finalSyncDone).To(BeFalse())
										})
									})

									Context("When no volumeattachment exists for the PV backing the PVC", func() {
										It("Should update the trigger on the RS and return true when replication is complete"+
											" and also delete the pvc after replication complete", func() {
											// Run ReconcileRS - indicate final sync
											finalSyncDone, returnedRS, err := vsHandler.ReconcileRS(rsSpec, true)
											Expect(err).ToNot(HaveOccurred())
											Expect(finalSyncDone).To(BeFalse()) // Should not return true since sync has not completed
											Expect(returnedRS).NotTo(BeNil())

											// Check that the manual sync triggger is set correctly on the RS
											Eventually(func() string {
												err := k8sClient.Get(ctx,
													types.NamespacedName{
														Name:      rsSpec.ProtectedPVC.Name,
														Namespace: testNamespace.GetName(),
													},
													createdRS)
												if err != nil || createdRS.Spec.Trigger == nil {
													return ""
												}

												return createdRS.Spec.Trigger.Manual
											}, maxWait, interval).Should(Equal(volsync.FinalSyncTriggerString))

											// We have triggered a final sync - manually update the status on the RS to
											// simulate that it has completed the sync and confirm ReconcileRS correctly sees the update
											now := metav1.Now()
											createdRS.Status = &volsyncv1alpha1.ReplicationSourceStatus{
												LastManualSync: volsync.FinalSyncTriggerString,
												LastSyncTime:   &now,
											}
											Expect(k8sClient.Status().Update(ctx, createdRS)).To(Succeed())

											Eventually(func() bool {
												// Make sure the update has been picked up by the client cache
												err := k8sClient.Get(ctx, client.ObjectKeyFromObject(createdRS), createdRS)
												if err != nil {
													return false
												}

												return createdRS.Status != nil && createdRS.Status.LastManualSync != ""
											}, maxWait, interval).Should(BeTrue())

											finalSyncDone, returnedRS, err = vsHandler.ReconcileRS(rsSpec, true)
											Expect(err).ToNot(HaveOccurred())
											Expect(finalSyncDone).To(BeTrue())
											Expect(returnedRS).NotTo(BeNil())

											// Now check to see if the pvc was removed
											Eventually(func() bool {
												err := k8sClient.Get(ctx, client.ObjectKeyFromObject(testPVC), testPVC)
												if err == nil {
													if util.ResourceIsDeleted(testPVC) {
														// PVC protection finalizer is added automatically to PVC - but testenv
														// doesn't have anything that will remove it for us - we're good as long
														// as the pvc is marked for deletion

														testPVC.Finalizers = []string{} // Clear finalizers
														Expect(k8sClient.Update(ctx, testPVC)).To(Succeed())
													}

													return false // try again
												}

												return kerrors.IsNotFound(err)
											}, maxWait, interval).Should(BeTrue())

											// Run reconcileRS with final sync again, even with PVC removed it should be able to
											// reconcile RS and check from the status that the final sync is complete
											finalSyncDone, returnedRS, err = vsHandler.ReconcileRS(rsSpec, true)
											Expect(err).ToNot(HaveOccurred())
											Expect(finalSyncDone).To(BeTrue())
											Expect(returnedRS).NotTo(BeNil())
										})
									})
								})
							})
						})
					})
				})
			})
		})
	})

	Describe("Ensure PVC from ReplicationDestination", func() {
		pvcName := "testpvc1"
		pvcCapacity := resource.MustParse("1Gi")

		var rdSpec ramendrv1alpha1.VolSyncReplicationDestinationSpec
		BeforeEach(func() {
			rdSpec = ramendrv1alpha1.VolSyncReplicationDestinationSpec{
				ProtectedPVC: ramendrv1alpha1.ProtectedPVC{
					Name:               pvcName,
					Namespace:          testNamespace.GetName(),
					ProtectedByVolSync: true,
					StorageClassName:   &testStorageClassName,
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: pvcCapacity,
						},
					},
				},
			}
		})

		var ensurePVCErr error
		JustBeforeEach(func() {
			ensurePVCErr = vsHandler.EnsurePVCfromRD(rdSpec, false)
		})

		Context("When ReplicationDestination Does not exist", func() {
			It("Should throw an error", func() {
				Expect(ensurePVCErr).To(HaveOccurred())
			})
		})

		Context("When ReplicationDestination exists with no latestImage", func() {
			BeforeEach(func() {
				// Pre-create the replication destination
				rd := &volsyncv1alpha1.ReplicationDestination{
					ObjectMeta: metav1.ObjectMeta{
						Name:      pvcName,
						Namespace: testNamespace.GetName(),
					},
					Spec: volsyncv1alpha1.ReplicationDestinationSpec{
						RsyncTLS: &volsyncv1alpha1.ReplicationDestinationRsyncTLSSpec{},
					},
				}
				Expect(k8sClient.Create(ctx, rd)).To(Succeed())

				// Make sure it's been created to avoid timing issues
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKeyFromObject(rd), rd)
				}, maxWait, interval).Should(Succeed())
			})
			It("Should fail to ensure PVC", func() {
				Expect(ensurePVCErr).To(HaveOccurred())
				Expect(ensurePVCErr.Error()).To(ContainSubstring("unable to find LatestImage"))
			})
		})

		Context("When ReplicationDestination exists with snapshot latestImage", func() {
			latestImageSnapshotName := "testingsnap001"

			BeforeEach(func() {
				// Pre-create the replication destination
				rd := &volsyncv1alpha1.ReplicationDestination{
					ObjectMeta: metav1.ObjectMeta{
						Name:      pvcName,
						Namespace: testNamespace.GetName(),
					},
					Spec: volsyncv1alpha1.ReplicationDestinationSpec{
						RsyncTLS: &volsyncv1alpha1.ReplicationDestinationRsyncTLSSpec{},
					},
				}
				Expect(k8sClient.Create(ctx, rd)).To(Succeed())
				apiGrp := APIGrp
				// Now force update the status to report a volume snapshot as latestImage
				rd.Status = &volsyncv1alpha1.ReplicationDestinationStatus{
					LatestImage: &corev1.TypedLocalObjectReference{
						Kind:     volsync.VolumeSnapshotKind,
						APIGroup: &apiGrp,
						Name:     latestImageSnapshotName,
					},
				}
				Expect(k8sClient.Status().Update(ctx, rd)).To(Succeed())

				// Make sure the update is picked up by the cache before proceeding
				Eventually(func() bool {
					err := k8sClient.Get(ctx, client.ObjectKeyFromObject(rd), rd)
					if err != nil {
						return false
					}

					return rd.Status != nil && rd.Status.LatestImage != nil
				}, maxWait, interval).Should(BeTrue())
			})

			Context("When the latest image volume snapshot does not exist", func() {
				It("Should fail to ensure PVC", func() {
					Expect(ensurePVCErr).To(HaveOccurred())
					Expect(ensurePVCErr.Error()).To(ContainSubstring("snapshot"))
					Expect(ensurePVCErr.Error()).To(ContainSubstring("not found"))
					Expect(ensurePVCErr.Error()).To(ContainSubstring(latestImageSnapshotName))
				})
			})

			Context("When the latest image volume snapshot exists", func() {
				var latestImageSnap *snapv1.VolumeSnapshot

				BeforeEach(func() {
					// Create a fake volume snapshot
					latestImageSnap = createSnapshot(latestImageSnapshotName, testNamespace.GetName())
				})

				pvc := &corev1.PersistentVolumeClaim{}
				JustBeforeEach(func() {
					// Common checks for everything in this context - pvc should be created with correct spec
					Expect(ensurePVCErr).NotTo(HaveOccurred())

					Eventually(func() error {
						return k8sClient.Get(ctx, types.NamespacedName{
							Name:      pvcName,
							Namespace: testNamespace.GetName(),
						}, pvc)
					}, maxWait, interval).Should(Succeed())

					Expect(pvc.GetName()).To(Equal(pvcName))
					Expect(pvc.Spec.AccessModes).To(Equal([]corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}))
					Expect(*pvc.Spec.StorageClassName).To(Equal(testStorageClassName))
					apiGrp := APIGrp
					Expect(pvc.Spec.DataSource).To(Equal(&corev1.TypedLocalObjectReference{
						Name:     latestImageSnapshotName,
						APIGroup: &apiGrp,
						Kind:     volsync.VolumeSnapshotKind,
					}))

					// Check that the snapshot ownership has been updated properly
					Eventually(func() bool {
						err := k8sClient.Get(ctx, types.NamespacedName{
							Name:      latestImageSnapshotName,
							Namespace: testNamespace.GetName(),
						}, latestImageSnap)
						if err != nil {
							return false
						}

						// Expect that the new pvc has been added as an owner
						// on the VolumeSnapshot - it should NOT be a controller, as the replicationdestination
						// will be the controller owning it
						return ownerMatches(latestImageSnap, owner.GetName(), "ConfigMap", false /* not controller */)
					}, maxWait, interval).Should(BeTrue())
				})

				Context("When the snapshot has restoreSize specified in Gi but PVC had storage in G", func() {
					// See: https://github.com/RamenDR/ramen/issues/578

					sizeGB := resource.MustParse("3G")
					sizeGi := resource.MustParse("3Gi")

					BeforeEach(func() {
						// Doublecheck here - 3Gi should be bigger than 3G
						Expect(sizeGi.Cmp(sizeGB)).To(Equal(1))

						// Update RdSpec before ensuringPVC to set the PVC size in GB
						rdSpec.ProtectedPVC.Resources.Requests = corev1.ResourceList{
							corev1.ResourceStorage: sizeGB,
						}

						// Update the status on the snapshot to show a restoreSize in Gi
						latestImageSnap.Status = &snapv1.VolumeSnapshotStatus{
							RestoreSize: &sizeGi,
						}

						Expect(k8sClient.Status().Update(ctx, latestImageSnap)).To(Succeed())

						// Make sure the update is picked up by the cache before proceeding
						Eventually(func() bool {
							err := k8sClient.Get(ctx, client.ObjectKeyFromObject(latestImageSnap), latestImageSnap)
							if err != nil {
								return false
							}

							return latestImageSnap.Status != nil && latestImageSnap.Status.RestoreSize != nil &&
								*latestImageSnap.Status.RestoreSize == sizeGi
						}, maxWait, interval).Should(BeTrue())
					})

					It("Should create the PVC with the snap restoreSize if restoreSize > pvc original size", func() {
						Expect(*pvc.Spec.Resources.Requests.Storage()).To(Equal(sizeGi))
					})
				})

				It("Should create PVC, latestImage VolumeSnapshot should have VRG owner ref added", func() {
					// snapshot ownership check done in JustBeforeEach() above

					// The volumesnapshot should also have the volsync do-not-delete label added
					snapLabels := latestImageSnap.GetLabels()
					val, ok := snapLabels["volsync.backube/do-not-delete"]
					Expect(ok).To(BeTrue())
					Expect(val).To(Equal("true"))

					Expect(pvc.Spec.Resources.Requests).To(Equal(corev1.ResourceList{
						corev1.ResourceStorage: pvcCapacity,
					}))
				})

				Context("When pvc to be restored has labels", func() {
					BeforeEach(func() {
						rdSpec.ProtectedPVC.Labels = map[string]string{
							"testlabel1": "mylabel1",
							"testlabel2": "protecthisPVC",
						}
					})

					It("Should create PVC with labels", func() {
						for k, v := range rdSpec.ProtectedPVC.Labels {
							Expect(pvc.Labels).To(HaveKeyWithValue(k, v))
						}
					})
				})

				Context("When pvc to be restored has annotations", func() {
					BeforeEach(func() {
						rdSpec.ProtectedPVC.Annotations = map[string]string{
							"include.me1": "value1",
							"include.me2": "value2",
						}
					})

					It("Should create PVC with annnotation", func() {
						for k, v := range rdSpec.ProtectedPVC.Annotations {
							Expect(pvc.Annotations).To(HaveKeyWithValue(k, v))
						}
					})
				})

				Context("When pvc to be restored has already been created", func() {
					It("ensure PVC should not fail", func() {
						// Previous ensurePVC will already have created the PVC (see parent context)
						// Now run ensurePVC again - additional runs should just ensure the PVC is ok
						Expect(vsHandler.EnsurePVCfromRD(rdSpec, false)).To(Succeed())
					})
				})

				Context("When pvc to be restored has already been created but has incorrect datasource", func() {
					var updatedImageSnap *snapv1.VolumeSnapshot

					JustBeforeEach(func() {
						// Simulate incorrect datasource by changing the latestImage in the replicationdestionation
						// status - this way the datasource on the previously created PVC will no longer match
						// our desired datasource
						updatedImageSnap = createSnapshot("new-snap-00001", testNamespace.GetName())

						// Update the replication destination to point to this new image
						rd := &volsyncv1alpha1.ReplicationDestination{}
						Expect(k8sClient.Get(ctx, types.NamespacedName{
							Name:      pvcName,
							Namespace: testNamespace.GetName(),
						}, rd)).To(Succeed())
						rd.Status.LatestImage.Name = updatedImageSnap.GetName()
						Expect(k8sClient.Status().Update(ctx, rd)).To(Succeed())

						// Make sure the update is picked up by the cache before proceeding
						Eventually(func() bool {
							err := k8sClient.Get(ctx, client.ObjectKeyFromObject(rd), rd)
							if err != nil {
								return false
							}

							return rd.Status != nil && rd.Status.LatestImage.Name == updatedImageSnap.GetName()
						}, maxWait, interval).Should(BeTrue())
					})

					It("ensure PVC should delete the pvc with incorrect datasource and return err", func() {
						// At this point we should have a PVC from previous but it should have a datasource
						// that maches our old snapshot - the rd has been updated with a new latest image
						// Expect ensurePVC from RD to remove the old one and return an error
						err := vsHandler.EnsurePVCfromRD(rdSpec, false)
						Expect(err).To(HaveOccurred())
						Expect(err.Error()).To(ContainSubstring("incorrect datasource"))

						// Check that the PVC was deleted
						Eventually(func() bool {
							err := k8sClient.Get(ctx, client.ObjectKeyFromObject(pvc), pvc)
							if err == nil {
								if util.ResourceIsDeleted(pvc) {
									// PVC protection finalizer is added automatically to PVC - but testenv
									// doesn't have anything that will remove it for us - we're good as long
									// as the pvc is marked for deletion

									pvc.Finalizers = []string{} // Clear finalizers
									Expect(k8sClient.Update(ctx, pvc)).To(Succeed())
								}

								return false // try again
							}

							return kerrors.IsNotFound(err)
						}, maxWait, interval).Should(BeTrue())

						//
						// Now should be able to re-try ensurePVC and get a new one with proper datasource
						//
						Expect(vsHandler.EnsurePVCfromRD(rdSpec, false)).NotTo(HaveOccurred())

						pvcNew := &corev1.PersistentVolumeClaim{}
						Eventually(func() error {
							return k8sClient.Get(ctx, types.NamespacedName{
								Name:      pvcName,
								Namespace: testNamespace.GetName(),
							}, pvcNew)
						}, maxWait, interval).Should(Succeed())

						Expect(pvcNew.GetName()).To(Equal(pvcName))
						Expect(pvcNew.Spec.AccessModes).To(Equal([]corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}))
						Expect(*pvcNew.Spec.StorageClassName).To(Equal(testStorageClassName))
						apiGrp := APIGrp
						Expect(pvcNew.Spec.DataSource).To(Equal(&corev1.TypedLocalObjectReference{
							Name:     updatedImageSnap.GetName(),
							APIGroup: &apiGrp,
							Kind:     volsync.VolumeSnapshotKind,
						}))

						Expect(pvcNew.Spec.Resources.Requests).To(Equal(corev1.ResourceList{
							corev1.ResourceStorage: pvcCapacity,
						}))
					})
				})
			})
		})
	})

	Describe("Cleanup ReplicationDestination", func() {
		pvcNamePrefix := "test-pvc-rdcleanuptests-"
		pvcNamePrefixOtherOwner := "otherowner-test-pvc-rdcleanuptests-"
		pvcCapacity := resource.MustParse("1Gi")

		var rdSpecList []ramendrv1alpha1.VolSyncReplicationDestinationSpec
		var rdSpecListOtherOwner []ramendrv1alpha1.VolSyncReplicationDestinationSpec

		BeforeEach(func() {
			rdSpecList = []ramendrv1alpha1.VolSyncReplicationDestinationSpec{}
			rdSpecListOtherOwner = []ramendrv1alpha1.VolSyncReplicationDestinationSpec{}

			// Precreate some ReplicationDestinations
			for i := 0; i < 10; i++ {
				rdSpec := ramendrv1alpha1.VolSyncReplicationDestinationSpec{
					ProtectedPVC: ramendrv1alpha1.ProtectedPVC{
						Name:               pvcNamePrefix + strconv.Itoa(i),
						Namespace:          testNamespace.GetName(),
						ProtectedByVolSync: true,
						StorageClassName:   &testStorageClassName,
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: pvcCapacity,
							},
						},
					},
				}
				rdSpecList = append(rdSpecList, rdSpec)
			}

			// Also create another vshandler with different owner - to simulate another VRG in the
			// same namespace.  Any RDs owned by this other owner should not be touched
			otherOwnerCm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "other-dummycm-owner-",
					Namespace:    testNamespace.GetName(),
				},
			}
			Expect(k8sClient.Create(ctx, otherOwnerCm)).To(Succeed())
			Expect(otherOwnerCm.GetName()).NotTo(BeEmpty())
			otherVSHandler := volsync.NewVSHandler(ctx, k8sClient, logger, otherOwnerCm, asyncSpec,
				"none", "Snapshot", false)

			for i := 0; i < 2; i++ {
				otherOwnerRdSpec := ramendrv1alpha1.VolSyncReplicationDestinationSpec{
					ProtectedPVC: ramendrv1alpha1.ProtectedPVC{
						Name:               pvcNamePrefixOtherOwner + strconv.Itoa(i),
						Namespace:          testNamespace.GetName(),
						ProtectedByVolSync: true,
						StorageClassName:   &testStorageClassName,
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: pvcCapacity,
							},
						},
					},
				}
				rdSpecListOtherOwner = append(rdSpecListOtherOwner, otherOwnerRdSpec)
			}

			// Create dummy volsync secrets - will need one per vrg
			dummyPSKSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      volsync.GetVolSyncPSKSecretNameFromVRGName(owner.GetName()),
					Namespace: testNamespace.GetName(),
				},
			}
			Expect(k8sClient.Create(ctx, dummyPSKSecret)).To(Succeed())
			Expect(dummyPSKSecret.GetName()).NotTo(BeEmpty())

			// Make sure the secret is created to avoid any timing issues
			Eventually(func() error {
				return k8sClient.Get(ctx,
					types.NamespacedName{
						Name:      dummyPSKSecret.GetName(),
						Namespace: dummyPSKSecret.GetNamespace(),
					}, dummyPSKSecret)
			}, maxWait, interval).Should(Succeed())

			dummyPSKSecretOtherOwner := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      volsync.GetVolSyncPSKSecretNameFromVRGName(otherOwnerCm.GetName()),
					Namespace: testNamespace.GetName(),
				},
			}
			Expect(k8sClient.Create(ctx, dummyPSKSecretOtherOwner)).To(Succeed())
			Expect(dummyPSKSecretOtherOwner.GetName()).NotTo(BeEmpty())

			// Make sure the secret is created to avoid any timing issues
			Eventually(func() error {
				return k8sClient.Get(ctx,
					types.NamespacedName{
						Name:      dummyPSKSecretOtherOwner.GetName(),
						Namespace: dummyPSKSecretOtherOwner.GetNamespace(),
					}, dummyPSKSecretOtherOwner)
			}, maxWait, interval).Should(Succeed())

			for _, rdSpec := range rdSpecList {
				// create RDs using our vsHandler
				_, err := vsHandler.ReconcileRD(rdSpec)
				Expect(err).NotTo(HaveOccurred())
			}
			for _, rdSpecOtherOwner := range rdSpecListOtherOwner {
				// create other RDs using another vsHandler (will be owned by another VRG)
				_, err := otherVSHandler.ReconcileRD(rdSpecOtherOwner)
				Expect(err).NotTo(HaveOccurred())
			}

			// Check the RDs were created correctly
			allRDs := &volsyncv1alpha1.ReplicationDestinationList{}
			Eventually(func() int {
				Expect(k8sClient.List(ctx, allRDs, client.InNamespace(testNamespace.GetName()))).To(Succeed())

				return len(allRDs.Items)
			}, maxWait, interval).Should(Equal(len(rdSpecList) + len(rdSpecListOtherOwner)))
		})

		Context("When rdSpec List is empty", func() {
			It("Should clean up all rd instances for the VRG", func() {
				// Empty RDSpec list
				Expect(vsHandler.CleanupRDNotInSpecList([]ramendrv1alpha1.VolSyncReplicationDestinationSpec{})).To(Succeed())

				rdList := &volsyncv1alpha1.ReplicationDestinationList{}
				Eventually(func() int {
					Expect(k8sClient.List(ctx, rdList, client.InNamespace(testNamespace.GetName()))).To(Succeed())

					return len(rdList.Items)
				}, maxWait, interval).Should(Equal(len(rdSpecListOtherOwner)))

				// The only ReplicationDestinations left should be owned by the other VRG
				for _, rd := range rdList.Items {
					Expect(rd.GetName()).To(HavePrefix(pvcNamePrefixOtherOwner))
				}
			})
		})

		Context("When rdSpec List has some entries", func() {
			It("Should clean up the proper rd instances for the VRG", func() {
				// List with only entries 2, 5 and 6 - the others should be cleaned up
				sList := []ramendrv1alpha1.VolSyncReplicationDestinationSpec{
					rdSpecList[2],
					rdSpecList[5],
					rdSpecList[6],
				}
				Expect(vsHandler.CleanupRDNotInSpecList(sList)).To(Succeed())

				rdList := &volsyncv1alpha1.ReplicationDestinationList{}
				Eventually(func() int {
					Expect(k8sClient.List(ctx, rdList, client.InNamespace(testNamespace.GetName()))).To(Succeed())

					return len(rdList.Items)
				}, maxWait, interval).Should(Equal(3 + len(rdSpecListOtherOwner)))

				// Check remaining RDs - check the correct ones were deleted
				for _, rd := range rdList.Items {
					Expect(strings.HasPrefix(rd.GetName(), pvcNamePrefixOtherOwner) ||
						rd.GetName() == rdSpecList[2].ProtectedPVC.Name ||
						rd.GetName() == rdSpecList[5].ProtectedPVC.Name ||
						rd.GetName() == rdSpecList[6].ProtectedPVC.Name).To(Equal(true))
				}
			})
		})

		It("Should delete an RD when it belongs to the VRG", func() {
			rdToDelete1 := rdSpecList[3].ProtectedPVC.Name // rd name should == pvc name
			Expect(vsHandler.DeleteRD(rdToDelete1)).To(Succeed())

			rdToDelete2 := rdSpecList[5].ProtectedPVC.Name // rd name should == pvc name
			Expect(vsHandler.DeleteRD(rdToDelete2)).To(Succeed())

			remainingRDs := &volsyncv1alpha1.ReplicationDestinationList{}
			Eventually(func() int {
				Expect(k8sClient.List(ctx, remainingRDs, client.InNamespace(testNamespace.GetName()))).To(Succeed())

				return len(remainingRDs.Items)
			}, maxWait, interval).Should(Equal(len(rdSpecList) + len(rdSpecListOtherOwner) - 2))

			for _, rd := range remainingRDs.Items {
				Expect(rd.GetName).NotTo(Equal(rdToDelete1))
				Expect(rd.GetName).NotTo(Equal(rdToDelete2))
			}
		})

		It("Should not delete an RD when it does not belong to the VRG", func() {
			rdToDelete := rdSpecListOtherOwner[1].ProtectedPVC.Name // rd name should == pvc name
			Expect(vsHandler.DeleteRD(rdToDelete)).To(Succeed())    // Should not return err

			// No RDs should have been deleted
			remainingRDs := &volsyncv1alpha1.ReplicationDestinationList{}
			Eventually(func() int {
				Expect(k8sClient.List(ctx, remainingRDs, client.InNamespace(testNamespace.GetName()))).To(Succeed())

				return len(remainingRDs.Items)
			}, maxWait, interval).Should(Equal(len(rdSpecList) + len(rdSpecListOtherOwner)))
		})
	})

	Describe("Cleanup ReplicationSource", func() {
		pvcNamePrefix := "test-pvc-rscleanuptests-"
		pvcNamePrefixOtherOwner := "otherowner-test-pvc-rscleanuptests-"

		var rsSpecList []ramendrv1alpha1.VolSyncReplicationSourceSpec
		var rsSpecListOtherOwner []ramendrv1alpha1.VolSyncReplicationSourceSpec

		BeforeEach(func() {
			rsSpecList = []ramendrv1alpha1.VolSyncReplicationSourceSpec{}
			rsSpecListOtherOwner = []ramendrv1alpha1.VolSyncReplicationSourceSpec{}

			// Precreate some ReplicationSources
			for i := 0; i < 10; i++ {
				rsSpec := ramendrv1alpha1.VolSyncReplicationSourceSpec{
					ProtectedPVC: ramendrv1alpha1.ProtectedPVC{
						Name:               pvcNamePrefix + strconv.Itoa(i),
						Namespace:          testNamespace.GetName(),
						ProtectedByVolSync: true,
						StorageClassName:   &testStorageClassName,
					},
				}

				rsSpecList = append(rsSpecList, rsSpec)
			}

			// Also create another vshandler with different owner - to simulate another VRG in the
			// same namespace.  Any RSs owned by this other owner should not be touched
			otherOwnerCm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "other-cm-owner-",
					Namespace:    testNamespace.GetName(),
				},
			}
			Expect(k8sClient.Create(ctx, otherOwnerCm)).To(Succeed())
			Expect(otherOwnerCm.GetName()).NotTo(BeEmpty())
			otherVSHandler := volsync.NewVSHandler(ctx, k8sClient, logger, otherOwnerCm, asyncSpec,
				"none", "Snapshot", false)

			for i := 0; i < 2; i++ {
				otherOwnerRsSpec := ramendrv1alpha1.VolSyncReplicationSourceSpec{
					ProtectedPVC: ramendrv1alpha1.ProtectedPVC{
						Name:               pvcNamePrefixOtherOwner + strconv.Itoa(i),
						Namespace:          testNamespace.GetName(),
						ProtectedByVolSync: true,
						StorageClassName:   &testStorageClassName,
					},
				}
				rsSpecListOtherOwner = append(rsSpecListOtherOwner, otherOwnerRsSpec)
			}

			// Create dummy volsync secrets - will need one per vrg
			dummyPSKSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      volsync.GetVolSyncPSKSecretNameFromVRGName(owner.GetName()),
					Namespace: testNamespace.GetName(),
				},
			}
			Expect(k8sClient.Create(ctx, dummyPSKSecret)).To(Succeed())
			Expect(dummyPSKSecret.GetName()).NotTo(BeEmpty())

			// Make sure the secret is created to avoid any timing issues
			Eventually(func() error {
				return k8sClient.Get(ctx,
					types.NamespacedName{
						Name:      dummyPSKSecret.GetName(),
						Namespace: dummyPSKSecret.GetNamespace(),
					}, dummyPSKSecret)
			}, maxWait, interval).Should(Succeed())

			dummyPSKSecretOtherOwner := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      volsync.GetVolSyncPSKSecretNameFromVRGName(otherOwnerCm.GetName()),
					Namespace: testNamespace.GetName(),
				},
			}
			Expect(k8sClient.Create(ctx, dummyPSKSecretOtherOwner)).To(Succeed())
			Expect(dummyPSKSecretOtherOwner.GetName()).NotTo(BeEmpty())

			// Make sure the secret is created to avoid any timing issues
			Eventually(func() error {
				return k8sClient.Get(ctx,
					types.NamespacedName{
						Name:      dummyPSKSecretOtherOwner.GetName(),
						Namespace: dummyPSKSecretOtherOwner.GetNamespace(),
					}, dummyPSKSecretOtherOwner)
			}, maxWait, interval).Should(Succeed())

			capacity := resource.MustParse("50Mi")

			for _, rsSpec := range rsSpecList {
				// Create the PVC to be protected and pod that is mounting it (and set pod Running/Ready)
				createDummyPVCAndMountingPod(rsSpec.ProtectedPVC.Name, testNamespace.GetName(),
					capacity, nil, corev1.PodRunning, true)

				// create RSs using our vsHandler
				_, returnedRS, err := vsHandler.ReconcileRS(rsSpec, false)
				Expect(err).NotTo(HaveOccurred())
				Expect(returnedRS).NotTo(BeNil())
			}
			for _, rsSpecOtherOwner := range rsSpecListOtherOwner {
				// Create the PVC to be protected and pod that is mounting it (and set pod Running/Ready)
				createDummyPVCAndMountingPod(rsSpecOtherOwner.ProtectedPVC.Name, testNamespace.GetName(),
					capacity, nil, corev1.PodRunning, true)

				// create other RSs using another vsHandler (will be owned by another VRG)
				_, returnedRS, err := otherVSHandler.ReconcileRS(rsSpecOtherOwner, false)
				Expect(err).NotTo(HaveOccurred())
				Expect(returnedRS).NotTo(BeNil())
			}

			allRSs := &volsyncv1alpha1.ReplicationSourceList{}
			Eventually(func() int {
				Expect(k8sClient.List(ctx, allRSs, client.InNamespace(testNamespace.GetName()))).To(Succeed())

				return len(allRSs.Items)
			}, maxWait, interval).Should(Equal(len(rsSpecList) + len(rsSpecListOtherOwner)))
		})

		It("Should delete an RS when it belongs to the VRG", func() {
			rsToDelete1 := rsSpecList[3].ProtectedPVC.Name // rs name should == pvc name
			Expect(vsHandler.DeleteRS(rsToDelete1)).To(Succeed())

			rsToDelete2 := rsSpecList[5].ProtectedPVC.Name // rs name should == pvc name
			Expect(vsHandler.DeleteRS(rsToDelete2)).To(Succeed())

			remainingRSs := &volsyncv1alpha1.ReplicationSourceList{}
			Eventually(func() int {
				Expect(k8sClient.List(ctx, remainingRSs, client.InNamespace(testNamespace.GetName()))).To(Succeed())

				return len(remainingRSs.Items)
			}, maxWait, interval).Should(Equal(len(rsSpecList) + len(rsSpecListOtherOwner) - 2))
		})

		It("Should not delete an RS when it does not belong to the VRG", func() {
			rsToDelete := rsSpecListOtherOwner[1].ProtectedPVC.Name // rs name should == pvc name
			Expect(vsHandler.DeleteRS(rsToDelete)).To(Succeed())    // Should not return err

			// No RSs should have been deleted
			remainingRSs := &volsyncv1alpha1.ReplicationSourceList{}
			Eventually(func() int {
				Expect(k8sClient.List(ctx, remainingRSs, client.InNamespace(testNamespace.GetName()))).To(Succeed())

				return len(remainingRSs.Items)
			}, maxWait, interval).Should(Equal(len(rsSpecList) + len(rsSpecListOtherOwner)))
		})
	})

	Describe("Prepare PVC for final sync", func() {
		Context("When the PVC does not exist", func() {
			It("Should assume preparationForFinalSync is complete", func() {
				pvcNamespacedName := types.NamespacedName{
					Name:      "this-pvc-does-not-exist",
					Namespace: testNamespace.GetName(),
				}
				pvcPreparationComplete, err := vsHandler.TakePVCOwnership(pvcNamespacedName)
				Expect(err).To(HaveOccurred())
				Expect(kerrors.IsNotFound(err)).To(BeTrue())
				Expect(pvcPreparationComplete).To(BeFalse())
			})
		})

		Context("When the PVC exists", func() {
			var testPVC *corev1.PersistentVolumeClaim
			initialAnnotations := map[string]string{
				"pv.kubernetes.io/bind-completed":                      "yes",
				"apps.open-cluster-management.io/cluster-admin":        "true",
				"apps.open-cluster-management.io/hosting-subscription": "busybox-sample/busybox-sub",
				"pv.kubernetes.io/bound-by-controller":                 "yes",
				"volume.beta.kubernetes.io/storage-provisioner":        "ebs.csi.aws.com",
				"apps.open-cluster-management.io/reconcile-option":     "mergeAndOwn",
			}
			BeforeEach(func() {
				testPVCName := "my-test-pvc-aabbcc"
				capacity := resource.MustParse("1Gi")
				testPVC = createDummyPVC(testPVCName, testNamespace.GetName(), capacity, initialAnnotations)
			})

			var pvcPreparationComplete bool
			var pvcPreparationErr error

			JustBeforeEach(func() {
				pvcNamespacedName := types.NamespacedName{
					Name:      testPVC.GetName(),
					Namespace: testPVC.GetNamespace(),
				}

				pvcPreparationComplete, pvcPreparationErr = vsHandler.TakePVCOwnership(pvcNamespacedName)

				// In all cases at this point we should expect that the PVC has ownership taken over by our owner VRG
				Eventually(func() bool {
					err := k8sClient.Get(ctx, client.ObjectKeyFromObject(testPVC), testPVC)
					if err != nil {
						return false
					}
					// configmap owner is faking out VRG
					return ownerMatches(testPVC, owner.GetName(), "ConfigMap", false)
				}, maxWait, interval).Should(BeTrue())
			})

			It("Should complete successfully, return true and remove ACM annotations", func() {
				Expect(pvcPreparationErr).ToNot(HaveOccurred())
				Expect(pvcPreparationComplete).To(BeTrue())

				// ACM do-not-delete annotation should be added to the PVC
				pvcAnnotations := testPVC.GetAnnotations()
				val, ok := pvcAnnotations[volsync.ACMAppSubDoNotDeleteAnnotation]
				Expect(ok).To(BeTrue())
				Expect(val).To(Equal(volsync.ACMAppSubDoNotDeleteAnnotationVal))
			})
		})
	})
})

func ownerMatches(obj metav1.Object, ownerName, ownerKind string, ownerIsController bool) bool {
	for _, ownerRef := range obj.GetOwnerReferences() {
		if ownerRef.Name == ownerName && ownerRef.Kind == ownerKind {
			// owner is there, check if controller or not
			isController := ownerRef.Controller != nil && *ownerRef.Controller

			return ownerIsController == isController
		}
	}

	return false
}

func createSnapshot(snapshotName, namespace string) *snapv1.VolumeSnapshot {
	pvcClaimName := "fakepvcnamehere"
	volSnap := &snapv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      snapshotName,
			Namespace: namespace,
		},
		Spec: snapv1.VolumeSnapshotSpec{
			Source: snapv1.VolumeSnapshotSource{
				PersistentVolumeClaimName: &pvcClaimName,
			},
		},
	}

	Expect(k8sClient.Create(ctx, volSnap)).To(Succeed())

	// Make sure the volume snapshot is created to avoid any timing issues
	Eventually(func() error {
		return k8sClient.Get(ctx, client.ObjectKeyFromObject(volSnap), volSnap)
	}, maxWait, interval).Should(Succeed())

	return volSnap
}

func createDummyPVC(pvcName, namespace string, capacity resource.Quantity,
	annotations map[string]string,
) *corev1.PersistentVolumeClaim {
	// Create a dummy pvc to protect so the reconcile can proceed properly
	dummyPVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:        pvcName,
			Namespace:   namespace,
			Annotations: annotations,
			Labels: map[string]string{
				"testlabel1": "testlabelvalue",
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: capacity,
				},
			},
			VolumeName: pvcName + "-pvname", // Simulate the PV
		},
	}
	Expect(k8sClient.Create(ctx, dummyPVC)).To(Succeed())

	// Make sure the PVC is created to avoid any timing issues
	Eventually(func() error {
		return k8sClient.Get(ctx, client.ObjectKeyFromObject(dummyPVC), dummyPVC)
	}, maxWait, interval).Should(Succeed())

	return dummyPVC
}

//nolint:funlen
func createDummyPVCAndMountingPod(pvcName, namespace string, capacity resource.Quantity, annotations map[string]string,
	desiredPodPhase corev1.PodPhase, podReady bool,
) (*corev1.PersistentVolumeClaim, *corev1.Pod) {
	// Create the PVC
	pvc := createDummyPVC(pvcName, namespace, capacity, annotations)

	// Create the pod which is mounting the pvc
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-pod-",
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
					Name: "myvol1",
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

	Eventually(func() error {
		// Make sure the pod has been picked up by the cache
		return k8sClient.Get(ctx, client.ObjectKeyFromObject(pod), pod)
	}, maxWait, interval).Should(Succeed())

	prevResourceVersion := pod.ResourceVersion
	statusUpdateRequired := false

	// Default phase will be "Pending"
	if desiredPodPhase != corev1.PodPending {
		// Set the pod phase
		pod.Status.Phase = desiredPodPhase

		statusUpdateRequired = true
	}

	if podReady {
		if pod.Status.Conditions == nil {
			pod.Status.Conditions = []corev1.PodCondition{}
		}

		updatedCondition := false

		for _, podCondition := range pod.Status.Conditions {
			if podCondition.Type == corev1.PodReady {
				podCondition.Status = corev1.ConditionTrue
				updatedCondition = true

				break
			}
		}

		if !updatedCondition {
			pod.Status.Conditions = append(pod.Status.Conditions, corev1.PodCondition{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
			})
		}
	}

	if statusUpdateRequired {
		Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())

		Eventually(func() bool {
			// Make sure the pod status update has been picked up by the cache
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(pod), pod)
			if err != nil {
				return false
			}

			return pod.ResourceVersion != prevResourceVersion
		}, maxWait, interval).Should(BeTrue())
	}

	return pvc, pod
}

func createDummyVolumeAttachmentForPVC(pvc *corev1.PersistentVolumeClaim) {
	pvName := pvc.Spec.VolumeName
	Expect(pvName).NotTo(Equal("")) // Test should be setting volumename for us

	// Create a VolumeAttachment object to point to our PV and fake out that it's mounted to a node
	dummyVolumeAttachment := &storagev1.VolumeAttachment{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "vol-attach-",
			Labels: map[string]string{
				testCleanupLabel: testCleanupLabelValue, // Add label we'll use to cleanup at the end of the test
			},
		},
		Spec: storagev1.VolumeAttachmentSpec{
			Attacher: "test-attacher",
			NodeName: "node-for-test",
			Source: storagev1.VolumeAttachmentSource{
				PersistentVolumeName: &pvName,
			},
		},
	}

	Expect(k8sClient.Create(ctx, dummyVolumeAttachment)).To(Succeed())

	Eventually(func() error {
		// Make sure the pod status update has been picked up by the cache
		return k8sClient.Get(ctx, client.ObjectKeyFromObject(dummyVolumeAttachment), dummyVolumeAttachment)
	}, maxWait, interval).Should(BeNil())
}

func cleanupNonNamespacedResources() {
	options := []client.DeleteAllOfOption{
		client.MatchingLabels{testCleanupLabel: testCleanupLabelValue},
		client.PropagationPolicy(metav1.DeletePropagationBackground),
	}

	// Right now only cleaning up volume attachments - these are currently the only cluster scoped resources
	// created for tests
	obj := &storagev1.VolumeAttachment{}
	err := k8sClient.DeleteAllOf(ctx, obj, options...)
	Expect(client.IgnoreNotFound(err)).To(BeNil())
}
