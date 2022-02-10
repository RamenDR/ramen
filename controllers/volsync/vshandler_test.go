package volsync_test

import (
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/volsync"
)

const (
	maxWait  = 20 * time.Second
	interval = 250 * time.Millisecond
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
			cronSpecSchedule, err := volsync.ConvertSchedulingIntervalToCronSpec("31h")
			Expect(err).NotTo((HaveOccurred()))
			Expect(cronSpecSchedule).ToNot(BeNil())
			Expect(*cronSpecSchedule).To(Equal("* */31 * * *"))
		})
		It("Should successfully convert an interval specified in days", func() {
			cronSpecSchedule, err := volsync.ConvertSchedulingIntervalToCronSpec("229d")
			Expect(err).NotTo((HaveOccurred()))
			Expect(cronSpecSchedule).ToNot(BeNil())
			Expect(*cronSpecSchedule).To(Equal("* * */229 * *"))
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

var _ = Describe("VolSync Handler", func() {
	var testNamespace *corev1.Namespace
	logger := zap.New(zap.UseDevMode(true), zap.WriteTo(GinkgoWriter))

	var owner metav1.Object
	var vsHandler *volsync.VSHandler

	schedulingInterval := "5m"
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
		// be a DRPC
		ownerCm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "dummycm-owner-",
				Namespace:    testNamespace.GetName(),
			},
		}
		Expect(k8sClient.Create(ctx, ownerCm)).To(Succeed())
		Expect(ownerCm.GetName()).NotTo(BeEmpty())
		owner = ownerCm

		vsHandler = volsync.NewVSHandler(ctx, k8sClient, logger, owner, schedulingInterval, &ramendrv1alpha1.VolSyncProfile{})
	})

	AfterEach(func() {
		// All resources are namespaced, so this should clean it all up
		Expect(k8sClient.Delete(ctx, testNamespace)).To(Succeed())
	})

	Describe("Reconcile ReplicationDestination", func() {
		Context("When reconciling RDSpec", func() {
			capacity := resource.MustParse("2Gi")

			rdSpec := ramendrv1alpha1.VolSyncReplicationDestinationSpec{
				ProtectedPVC: ramendrv1alpha1.ProtectedPVC{
					Name:               "mytestpvc",
					ProtectedByVolSync: true,
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: capacity,
						},
					},
				},
				SSHKeys: "testkey123",
			}

			var returnedRDInfo *ramendrv1alpha1.VolSyncReplicationDestinationInfo
			createdRD := &volsyncv1alpha1.ReplicationDestination{}

			JustBeforeEach(func() {
				// Run ReconcileRD
				var err error
				returnedRDInfo, err = vsHandler.ReconcileRD(rdSpec)
				Expect(err).ToNot(HaveOccurred())

				// RD should be created with name=PVCName
				Eventually(func() error {
					return k8sClient.Get(ctx,
						types.NamespacedName{Name: rdSpec.ProtectedPVC.Name, Namespace: testNamespace.GetName()}, createdRD)
				}, maxWait, interval).Should(Succeed())

				// Expect the RD should be owned by owner
				Expect(ownerMatches(createdRD, owner.GetName(), "ConfigMap"))

				// Check common fields
				Expect(createdRD.Spec.Rsync.CopyMethod).To(Equal(volsyncv1alpha1.CopyMethodSnapshot))
				Expect(*createdRD.Spec.Rsync.SSHKeys).To(Equal(rdSpec.SSHKeys))
				Expect(*createdRD.Spec.Rsync.Capacity).To(Equal(capacity))
				Expect(createdRD.Spec.Rsync.AccessModes).To(Equal([]corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}))
				Expect(createdRD.Spec.Trigger).To(BeNil()) // No schedule should be set
				Expect(createdRD.GetLabels()).To(HaveKeyWithValue(volsync.VSRGReplicationSourceLabel, owner.GetName()))
			})

			Context("When empty volsyncProfile is specified", func() {
				It("Should use the default rsync service type in the ReplicationDestination", func() {
					Expect(*createdRD.Spec.Rsync.ServiceType).To(Equal(volsync.DefaultRsyncServiceType))
				})
			})

			Context("When no volsyncProfile is specified", func() {
				BeforeEach(func() {
					vsHandler.SetVolSyncProfile(nil)
				})
				It("Should use the default rsync service type in the ReplicationDestination", func() {
					Expect(*createdRD.Spec.Rsync.ServiceType).To(Equal(volsync.DefaultRsyncServiceType))
				})
			})

			Context("When a volsyncProfile is specified with serviceType", func() {
				var typeClusterIP = corev1.ServiceTypeClusterIP
				BeforeEach(func() {
					vsHandler.SetVolSyncProfile(&ramendrv1alpha1.VolSyncProfile{
						VolSyncProfileName: "default",
						ServiceType:        &typeClusterIP,
					})
				})
				It("Should use the rsync service type in the VolSyncProfile", func() {
					Expect(*createdRD.Spec.Rsync.ServiceType).To(Equal(typeClusterIP))
				})
			})

			Context("When storageClassName is not specified", func() {
				It("Should create an RD with no storage class name specified", func() {
					Expect(createdRD.Spec.Rsync.StorageClassName).To(BeNil())
					// Expect RDInfo to be nil (only should return an RDInfo if the RD.Status.Address is set)
					Expect(returnedRDInfo).To(BeNil())
				})
			})
			Context("When storageClassName is specified", func() {
				scName := "mystorageclass1"
				BeforeEach(func() {
					// Set a storageclass for the PVC in the RDSpec
					rdSpec.ProtectedPVC.StorageClassName = &scName
				})
				It("Should create an RD with proper storage class name", func() {
					Expect(*createdRD.Spec.Rsync.StorageClassName).To(Equal(scName))
					// Expect RDInfo to be nil (only should return an RDInfo if the RD.Status.Address is set)
					Expect(returnedRDInfo).To(BeNil())
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
						// Fake the address in the status
						rdPrecreate.Status = &volsyncv1alpha1.ReplicationDestinationStatus{
							Rsync: &volsyncv1alpha1.ReplicationDestinationRsyncStatus{
								Address: &myTestAddress,
								SSHKeys: &rdSpec.SSHKeys,
							},
						}
						Expect(k8sClient.Status().Update(ctx, rdPrecreate)).To(Succeed())
						Eventually(func() *string {
							err := k8sClient.Get(ctx, client.ObjectKeyFromObject(rdPrecreate), rdPrecreate)
							if err != nil || rdPrecreate.Status == nil || rdPrecreate.Status.Rsync == nil {
								return nil
							}
							return rdPrecreate.Status.Rsync.Address
						}, maxWait, interval).Should(Not(BeNil()))
					})

					It("Should properly update Replication destination and return rdInfo", func() {
						// Common JustBeforeEach will run reconcileRD and check spec is proper

						Expect(*createdRD.Spec.Rsync.StorageClassName).To(Equal(scName)) // Check storage class
						// Expect RDInfo to NOT be nil - address was filled out so it should have been returned
						Expect(returnedRDInfo).ToNot(BeNil())
					})
				})
			})
		})
	})

	Describe("Reconcile ReplicationSource", func() {
		Context("When reconciling RSSpec", func() {
			rsSpec := ramendrv1alpha1.VolSyncReplicationSourceSpec{
				PVCName: "mytestpvc",
				Address: "https://testing.abc.org",
				SSHKeys: "testkey123",
			}

			createdRS := &volsyncv1alpha1.ReplicationSource{}

			JustBeforeEach(func() {
				// Run ReconcileRS - Not running final sync so this should return false
				finalSyncDone, err := vsHandler.ReconcileRS(rsSpec, false)
				Expect(err).ToNot(HaveOccurred())
				Expect(finalSyncDone).To(BeFalse())

				// RS should be created with name=PVCName
				Eventually(func() error {
					return k8sClient.Get(ctx,
						types.NamespacedName{Name: rsSpec.PVCName, Namespace: testNamespace.GetName()}, createdRS)
				}, maxWait, interval).Should(Succeed())

				// Expect the RS should be owned by owner
				Expect(ownerMatches(createdRS, owner.GetName(), "ConfigMap"))

				// Check common fields
				Expect(createdRS.Spec.SourcePVC).To(Equal(rsSpec.PVCName))
				Expect(createdRS.Spec.Rsync.CopyMethod).To(Equal(volsyncv1alpha1.CopyMethodSnapshot))
				Expect(*createdRS.Spec.Rsync.SSHKeys).To(Equal(rsSpec.SSHKeys))
				Expect(*createdRS.Spec.Rsync.Address).To(Equal(rsSpec.Address))

				Expect(createdRS.Spec.Trigger).ToNot(BeNil())
				Expect(createdRS.Spec.Trigger).To(Equal(&volsyncv1alpha1.ReplicationSourceTriggerSpec{
					Schedule: &expectedCronSpecSchedule,
				}))
				Expect(createdRS.GetLabels()).To(HaveKeyWithValue(volsync.VSRGReplicationSourceLabel, owner.GetName()))
			})

			It("Should create an ReplicationSource if one does not exist", func() {
				// All checks here performed in the JustBeforeEach(common checks)
			})

			Context("When replication source already exists", func() {
				BeforeEach(func() {
					// Pre-create a replication destination - and fill out Status.Address
					rsPrecreate := &volsyncv1alpha1.ReplicationSource{
						ObjectMeta: metav1.ObjectMeta{
							Name:      rsSpec.PVCName,
							Namespace: testNamespace.GetName(),
							Labels: map[string]string{
								"customlabel1": "somevaluehere",
							},
						},
						// Will expect the reconcile to fill this out properly for us (i.e. update)
						Spec: volsyncv1alpha1.ReplicationSourceSpec{
							Rsync: &volsyncv1alpha1.ReplicationSourceRsyncSpec{},
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
					// All checks here performed in the JustBeforeEach(common checks)
				})

				Context("When running a final sync", func() {
					It("Should update the trigger on the RS and return true when replication is complete", func() {
						// Run ReconcileRS - indicate final sync
						finalSyncDone, err := vsHandler.ReconcileRS(rsSpec, true)
						Expect(err).ToNot(HaveOccurred())
						Expect(finalSyncDone).To(BeFalse()) // Should not return true since sync has not completed

						// Check that the manual sync triggger is set correctly on the RS
						Eventually(func() string {
							err := k8sClient.Get(ctx,
								types.NamespacedName{Name: rsSpec.PVCName, Namespace: testNamespace.GetName()}, createdRS)
							if err != nil || createdRS.Spec.Trigger == nil {
								return ""
							}
							return createdRS.Spec.Trigger.Manual
						}, maxWait, interval).Should(Equal(volsync.FinalSyncTriggerString))

						// We have triggered a final sync - manually update the status on the RS to
						// simulate that it has completed the sync and confirm ReconcileRS correctly sees the update
						createdRS.Status = &volsyncv1alpha1.ReplicationSourceStatus{
							LastManualSync: volsync.FinalSyncTriggerString,
						}
						Expect(k8sClient.Status().Update(ctx, createdRS)).To(Succeed())

						finalSyncDone, err = vsHandler.ReconcileRS(rsSpec, true)
						Expect(err).ToNot(HaveOccurred())
						Expect(finalSyncDone).To(BeTrue())
					})
				})
			})
		})
	})

	Describe("Ensure PVC from ReplicationDestination", func() {
		pvcName := "testpvc1"
		pvcCapacity := resource.MustParse("1Gi")
		pvcStorageClassName := "teststorageclass"

		var rdSpec ramendrv1alpha1.VolSyncReplicationDestinationSpec
		BeforeEach(func() {
			rdSpec = ramendrv1alpha1.VolSyncReplicationDestinationSpec{
				ProtectedPVC: ramendrv1alpha1.ProtectedPVC{
					Name:               pvcName,
					ProtectedByVolSync: true,
					StorageClassName:   &pvcStorageClassName,
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: pvcCapacity,
						},
					},
				},
				SSHKeys: "testsecret",
			}
		})

		var ensurePVCErr error
		JustBeforeEach(func() {
			ensurePVCErr = vsHandler.EnsurePVCfromRD(rdSpec)
		})

		Context("When ReplicationDestination Does not exist", func() {
			It("Should not throw an error", func() { // Ignoring if RD is not there right now
				Expect(ensurePVCErr).NotTo(HaveOccurred())
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
						Rsync: &volsyncv1alpha1.ReplicationDestinationRsyncSpec{},
					},
				}
				Expect(k8sClient.Create(ctx, rd)).To(Succeed())
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
						Rsync: &volsyncv1alpha1.ReplicationDestinationRsyncSpec{},
					},
				}
				Expect(k8sClient.Create(ctx, rd)).To(Succeed())

				apiGrp := volsync.VolumeSnapshotGroup
				// Now force update the status to report a volume snapshot as latestImage
				rd.Status = &volsyncv1alpha1.ReplicationDestinationStatus{
					LatestImage: &corev1.TypedLocalObjectReference{
						Kind:     volsync.VolumeSnapshotKind,
						APIGroup: &apiGrp,
						Name:     latestImageSnapshotName,
					},
				}
				Expect(k8sClient.Status().Update(ctx, rd)).To(Succeed())
			})

			Context("When the latest image volume snapshot does not exist", func() {
				It("Should fail to ensure PVC", func() {
					Expect(ensurePVCErr).To(HaveOccurred())
					Expect(ensurePVCErr.Error()).To(ContainSubstring("volumesnapshots"))
					Expect(ensurePVCErr.Error()).To(ContainSubstring("not found"))
					Expect(ensurePVCErr.Error()).To(ContainSubstring(latestImageSnapshotName))
				})
			})

			Context("When the latest image volume snapshot exists", func() {
				var latestImageSnap *unstructured.Unstructured
				BeforeEach(func() {
					// Create a fake volume snapshot
					var err error
					latestImageSnap, err = createSnapshot(latestImageSnapshotName, testNamespace.GetName())
					Expect(err).NotTo(HaveOccurred())
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
					Expect(*pvc.Spec.StorageClassName).To(Equal(pvcStorageClassName))
					apiGrp := volsync.VolumeSnapshotGroup
					Expect(pvc.Spec.DataSource).To(Equal(&corev1.TypedLocalObjectReference{
						Name:     latestImageSnapshotName,
						APIGroup: &apiGrp,
						Kind:     volsync.VolumeSnapshotKind,
					}))
					Expect(pvc.Spec.Resources.Requests).To(Equal(corev1.ResourceList{
						corev1.ResourceStorage: pvcCapacity,
					}))
				})

				It("Should create PVC, latestImage VolumeSnapshot should have a finalizer added", func() {
					Eventually(func() bool {
						err := k8sClient.Get(ctx, types.NamespacedName{
							Name:      latestImageSnapshotName,
							Namespace: testNamespace.GetName(),
						}, latestImageSnap)
						if err != nil {
							return false
						}
						return len(latestImageSnap.GetFinalizers()) == 1 &&
							latestImageSnap.GetFinalizers()[0] == volsync.VolumeSnapshotProtectFinalizerName
					}, maxWait, interval).Should(BeTrue())
				})

				//TODO:
				/*
					Context("When pvc to be restored has labels", func() {
						BeforeEach(func() {
							rdSpec.Labels = map[string]string{
								"testlabel1": "mylabel1",
								"testlabel2": "protecthisPVC",
							}
						})

						It("Should create PVC with labels", func() {
							Expect(pvc.Labels).To(Equal(rdSpec.Labels))
						})
					})
				*/

				Context("When pvc to be restored has already been created", func() {
					It("ensure PVC should not fail", func() {
						// Previous ensurePVC will already have created the PVC (see parent context)
						// Now run ensurePVC again - additional runs should just ensure the PVC is ok
						Expect(vsHandler.EnsurePVCfromRD(rdSpec)).To(Succeed())
					})
				})
			})
		})
	})

	Describe("Cleanup ReplicationDestination", func() {
		pvcNamePrefix := "test-pvc-rdcleanuptests-"
		pvcNamePrefixOtherOwner := "otherowner-test-pvc-rdcleanuptests-"
		pvcCapacity := resource.MustParse("1Gi")
		pvcStorageClassName := "teststorageclass"

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
						ProtectedByVolSync: true,
						StorageClassName:   &pvcStorageClassName,
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: pvcCapacity,
							},
						},
					},
					SSHKeys: "testsecret",
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
			otherVSHandler := volsync.NewVSHandler(ctx, k8sClient, logger, otherOwnerCm, schedulingInterval, nil)

			for i := 0; i < 2; i++ {
				otherOwnerRdSpec := ramendrv1alpha1.VolSyncReplicationDestinationSpec{
					ProtectedPVC: ramendrv1alpha1.ProtectedPVC{
						Name:               pvcNamePrefixOtherOwner + strconv.Itoa(i),
						ProtectedByVolSync: true,
						StorageClassName:   &pvcStorageClassName,
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: pvcCapacity,
							},
						},
					},
					SSHKeys: "testsecret",
				}
				rdSpecListOtherOwner = append(rdSpecListOtherOwner, otherOwnerRdSpec)
			}

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
					PVCName: pvcNamePrefix + strconv.Itoa(i),
					Address: "10.1.2.3",
					SSHKeys: "thisismykey",
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
			otherVSHandler := volsync.NewVSHandler(ctx, k8sClient, logger, otherOwnerCm, schedulingInterval, nil)

			for i := 0; i < 2; i++ {
				otherOwnerRsSpec := ramendrv1alpha1.VolSyncReplicationSourceSpec{
					PVCName: pvcNamePrefixOtherOwner + strconv.Itoa(i),
					Address: "9.9.9.9",
					SSHKeys: "testsecret",
				}
				rsSpecListOtherOwner = append(rsSpecListOtherOwner, otherOwnerRsSpec)
			}

			for _, rsSpec := range rsSpecList {
				// create RSs using our vsHandler
				_, err := vsHandler.ReconcileRS(rsSpec, false)
				Expect(err).NotTo(HaveOccurred())
			}
			for _, rsSpecOtherOwner := range rsSpecListOtherOwner {
				// create other RSs using another vsHandler (will be owned by another VRG)
				_, err := otherVSHandler.ReconcileRS(rsSpecOtherOwner, false)
				Expect(err).NotTo(HaveOccurred())
			}

			allRSs := &volsyncv1alpha1.ReplicationSourceList{}
			Eventually(func() int {
				Expect(k8sClient.List(ctx, allRSs, client.InNamespace(testNamespace.GetName()))).To(Succeed())
				return len(allRSs.Items)
			}, maxWait, interval).Should(Equal(len(rsSpecList) + len(rsSpecListOtherOwner)))
		})

		Context("When rsSpec List is empty", func() {
			It("Should clean up all rs instances for the VRG", func() {
				// Empty RSSpec list
				Expect(vsHandler.CleanupRSNotInSpecList([]ramendrv1alpha1.VolSyncReplicationSourceSpec{})).To(Succeed())

				rsList := &volsyncv1alpha1.ReplicationSourceList{}
				Eventually(func() int {
					Expect(k8sClient.List(ctx, rsList, client.InNamespace(testNamespace.GetName()))).To(Succeed())
					return len(rsList.Items)
				}, maxWait, interval).Should(Equal(len(rsSpecListOtherOwner)))

				// The only ReplicationSources left should be owned by the other VRG
				for _, rs := range rsList.Items {
					Expect(rs.GetName()).To(HavePrefix(pvcNamePrefixOtherOwner))
				}
			})
		})

		Context("When rsSpec List has some entries", func() {
			It("Should clean up the proper rs instances for the VRG", func() {
				// List with only entries 2, 5 and 6 - the others should be cleaned up
				sList := []ramendrv1alpha1.VolSyncReplicationSourceSpec{
					rsSpecList[9],
					rsSpecList[0],
				}
				Expect(vsHandler.CleanupRSNotInSpecList(sList)).To(Succeed())

				rsList := &volsyncv1alpha1.ReplicationSourceList{}
				Eventually(func() int {
					Expect(k8sClient.List(ctx, rsList, client.InNamespace(testNamespace.GetName()))).To(Succeed())
					return len(rsList.Items)
				}, maxWait, interval).Should(Equal(2 + len(rsSpecListOtherOwner)))

				// Check remaining RSs - check the correct ones were deleted
				for _, rs := range rsList.Items {
					Expect(strings.HasPrefix(rs.GetName(), pvcNamePrefixOtherOwner) ||
						rs.GetName() == rsSpecList[0].PVCName ||
						rs.GetName() == rsSpecList[9].PVCName).To(Equal(true))
				}
			})
		})
	})
})

func ownerMatches(obj metav1.Object, ownerName, ownerKind string) bool {
	for _, ownerRef := range obj.GetOwnerReferences() {
		if ownerRef.Name == ownerName && ownerRef.Kind == ownerKind {
			return true
		}
	}

	return false
}

func createSnapshot(snapshotName, namespace string) (*unstructured.Unstructured, error) {
	volSnap := &unstructured.Unstructured{}
	volSnap.Object = map[string]interface{}{
		"metadata": map[string]interface{}{
			"name":      snapshotName,
			"namespace": namespace,
		},
		"spec": map[string]interface{}{
			"source": map[string]interface{}{
				"persistentVolumeClaimName": "fakepvcnamehere",
			},
		},
	}
	volSnap.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   volsync.VolumeSnapshotGroup,
		Kind:    volsync.VolumeSnapshotKind,
		Version: volsync.VolumeSnapshotVersion,
	})

	return volSnap, k8sClient.Create(ctx, volSnap)
}
