package volsync_test

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/volsync"
)

const (
	maxWait  = 10 * time.Second
	interval = 250 * time.Millisecond

	APIGrp = "snapshot.storage.k8s.io"
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
	logger := zap.New(zap.UseDevMode(true), zap.WriteTo(GinkgoWriter))
	schedulingInterval := "1h"

	Describe("Get volume snapshot classes", func() {
		Context("With no label selector", func() {
			var vsHandler *volsync.VSHandler

			BeforeEach(func() {
				vsHandler = volsync.NewVSHandler(ctx, k8sClient, logger, nil,
					schedulingInterval, metav1.LabelSelector{})
			})

			It("GetVolumeSnapshotClasses() should find all volume snapshot classes", func() {
				vsClasses, err := vsHandler.GetVolumeSnapshotClasses()
				Expect(err).NotTo(HaveOccurred())

				Expect(len(vsClasses)).To(Equal(totalStorageClassCount))
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
				vsClassLabelSelector := metav1.LabelSelector{
					MatchLabels: map[string]string{
						"i-like-ramen": "true",
					},
				}

				vsHandler = volsync.NewVSHandler(ctx, k8sClient, logger, nil,
					schedulingInterval, vsClassLabelSelector)
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
				vsClassLabelSelector := metav1.LabelSelector{
					MatchLabels: map[string]string{
						"i-like-ramen": "true",
						"abc":          "b",
					},
				}

				vsHandler = volsync.NewVSHandler(ctx, k8sClient, logger, nil,
					schedulingInterval, vsClassLabelSelector)
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

		vsHandler = volsync.NewVSHandler(ctx, k8sClient, logger, owner, schedulingInterval, metav1.LabelSelector{})
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
					StorageClassName:   &testStorageClassName,
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: capacity,
						},
					},
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				},
			}

			createdRD := &volsyncv1alpha1.ReplicationDestination{}
			var returnedRD *volsyncv1alpha1.ReplicationDestination

			Context("When the ssh secret for volsync does not exist", func() {
				JustBeforeEach(func() {
					// Run ReconcileRD
					var err error
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

			Context("When the ssh secret for volsync exists (will be pushed down by drpc from hub", func() {
				var dummySSHSecret *corev1.Secret
				JustBeforeEach(func() {
					// Create a dummy volsync ssh secret so the reconcile can proceed properly
					dummySSHSecret = &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      volsync.GetVolSyncSSHSecretNameFromVRGName(owner.GetName()),
							Namespace: testNamespace.GetName(),
						},
					}
					Expect(k8sClient.Create(ctx, dummySSHSecret)).To(Succeed())
					Expect(dummySSHSecret.GetName()).NotTo(BeEmpty())

					// Make sure the secret is created to avoid any timing issues
					Eventually(func() error {
						return k8sClient.Get(ctx,
							types.NamespacedName{
								Name:      dummySSHSecret.GetName(),
								Namespace: dummySSHSecret.GetNamespace(),
							}, dummySSHSecret)
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
						Expect(createdRD.Spec.Rsync.CopyMethod).To(Equal(volsyncv1alpha1.CopyMethodSnapshot))
						// Note owner here is faking out a VRG - ssh key name will be based on the owner (VRG) name
						Expect(*createdRD.Spec.Rsync.SSHKeys).To(Equal(volsync.GetVolSyncSSHSecretNameFromVRGName(owner.GetName())))
						Expect(*createdRD.Spec.Rsync.Capacity).To(Equal(capacity))
						Expect(createdRD.Spec.Rsync.AccessModes).To(Equal([]corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}))
						Expect(*createdRD.Spec.Rsync.StorageClassName).To(Equal(testStorageClassName))
						Expect(*createdRD.Spec.Rsync.VolumeSnapshotClassName).To(Equal(testVolumeSnapshotClassName))
						Expect(createdRD.Spec.Trigger).To(BeNil()) // No schedule should be set
						Expect(createdRD.GetLabels()).To(HaveKeyWithValue(volsync.VRGOwnerLabel, owner.GetName()))
						Expect(*createdRD.Spec.Rsync.ServiceType).To(Equal(volsync.DefaultRsyncServiceType))

						// Check that the secret has been updated to have our vrg as owner
						Eventually(func() bool {
							err := k8sClient.Get(ctx, client.ObjectKeyFromObject(dummySSHSecret), dummySSHSecret)
							if err != nil {
								return false
							}

							// The ssh secret should be updated to be owned by the VRG
							return ownerMatches(dummySSHSecret, owner.GetName(), "ConfigMap", false)
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
								Name:      fmt.Sprintf("volsync-rsync-dst-%s", createdRD.GetName()),
								Namespace: createdRD.GetNamespace(),
							}, svcExport)
						}, maxWait, interval).Should(Succeed())

						// The created service export should be owned by the replication destination, not our VRG
						Expect(ownerMatches(svcExport, createdRD.GetName(), "ReplicationDestination", false)).To(BeTrue())
					})

					/*
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
							typeLoadBalancer := corev1.ServiceTypeLoadBalancer
							BeforeEach(func() {
								vsHandler.SetVolSyncProfile(&ramendrv1alpha1.VolSyncProfile{
									VolSyncProfileName: "default",
									ServiceType:        &typeLoadBalancer,
								})
							})
							It("Should use the rsync service type in the VolSyncProfile", func() {
								Expect(*createdRD.Spec.Rsync.ServiceType).To(Equal(typeLoadBalancer))
							})
						})
					*/

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

						It("Should properly update Replication destination and return rd", func() {
							// Common JustBeforeEach will run reconcileRD and check spec is proper
							// Expect RDInfo to NOT be nil - address was filled out so it should have been returned
							Expect(returnedRD).ToNot(BeNil())
						})
					})
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
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: capacity,
						},
					},
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				},
			}

			createdRS := &volsyncv1alpha1.ReplicationSource{}

			Context("When the ssh secret for volsync does not exist", func() {
				var returnedRS *volsyncv1alpha1.ReplicationSource
				JustBeforeEach(func() {
					// Run ReconcileRD
					var err error
					var finalSyncCompl bool
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

			Context("When the ssh secret for volsync exists (will be pushed down by drpc from hub", func() {
				var dummySSHSecret *corev1.Secret
				JustBeforeEach(func() {
					// Create a dummy volsync ssh secret so the reconcile can proceed properly
					dummySSHSecret = &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      volsync.GetVolSyncSSHSecretNameFromVRGName(owner.GetName()),
							Namespace: testNamespace.GetName(),
						},
					}
					Expect(k8sClient.Create(ctx, dummySSHSecret)).To(Succeed())
					Expect(dummySSHSecret.GetName()).NotTo(BeEmpty())

					// Make sure the secret is created to avoid any timing issues
					Eventually(func() error {
						return k8sClient.Get(ctx, types.NamespacedName{
							Name:      dummySSHSecret.GetName(),
							Namespace: dummySSHSecret.GetNamespace(),
						}, dummySSHSecret)
					}, maxWait, interval).Should(Succeed())
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

						// Run ReconcileRS - Not running final sync so this should return false
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
					})

					It("Should delete the existing ReplicationDestination", func() {
						Eventually(func() bool {
							err := k8sClient.Get(ctx, client.ObjectKeyFromObject(rd), rd)

							return kerrors.IsNotFound(err)
						}, maxWait, interval).Should(BeTrue())
					})
				})

				Context("When reconciling RS with no previous RD", func() {
					JustBeforeEach(func() {
						// Run ReconcileRS - Not running final sync so this should return false
						finalSyncDone, returnedRS, err := vsHandler.ReconcileRS(rsSpec, false)
						Expect(err).ToNot(HaveOccurred())
						Expect(finalSyncDone).To(BeFalse())
						Expect(returnedRS).NotTo(BeNil())

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

						// Check that the volsync ssh secret has been updated to have our vrg as owner
						Eventually(func() bool {
							err := k8sClient.Get(ctx, client.ObjectKeyFromObject(dummySSHSecret), dummySSHSecret)
							if err != nil {
								return false
							}

							// The ssh secret should be updated to be owned by the VRG
							return ownerMatches(dummySSHSecret, owner.GetName(), "ConfigMap", false)
						}, maxWait, interval).Should(BeTrue())

						// Check common fields
						Expect(createdRS.Spec.SourcePVC).To(Equal(rsSpec.ProtectedPVC.Name))
						Expect(createdRS.Spec.Rsync.CopyMethod).To(Equal(volsyncv1alpha1.CopyMethodSnapshot))
						// Note owner here is faking out a VRG - ssh key name will be based on the owner (VRG) name
						Expect(*createdRS.Spec.Rsync.SSHKeys).To(Equal(volsync.GetVolSyncSSHSecretNameFromVRGName(owner.GetName())))
						Expect(*createdRS.Spec.Rsync.Address).To(Equal("volsync-rsync-dst-" +
							rsSpec.ProtectedPVC.Name + "." + testNamespace.GetName() + ".svc.clusterset.local"))

						Expect(*createdRS.Spec.Rsync.VolumeSnapshotClassName).To(Equal(testVolumeSnapshotClassName))

						Expect(createdRS.Spec.Trigger).ToNot(BeNil())
						Expect(createdRS.Spec.Trigger).To(Equal(&volsyncv1alpha1.ReplicationSourceTriggerSpec{
							Schedule: &expectedCronSpecSchedule,
						}))
						Expect(createdRS.GetLabels()).To(HaveKeyWithValue(volsync.VRGOwnerLabel, owner.GetName()))
					})

					It("Should create an ReplicationSource if one does not exist", func() {
						// All checks here performed in the JustBeforeEach(common checks)
					})

					Context("When replication source already exists", func() {
						BeforeEach(func() {
							// Pre-create a replication destination - and fill out Status.Address
							rsPrecreate := &volsyncv1alpha1.ReplicationSource{
								ObjectMeta: metav1.ObjectMeta{
									Name:      rsSpec.ProtectedPVC.Name,
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
							var testPVC *corev1.PersistentVolumeClaim
							BeforeEach(func() {
								// Create dummy PVC
								pvcCapacity := resource.MustParse("1Gi")
								testPVC = createDummyPVC(rsSpec.ProtectedPVC.Name, testNamespace.GetName(), pvcCapacity, nil)
							})

							Context("When the pvc is still in use", func() {
								BeforeEach(func() {
									// Create a pod to mount the PVC so it shows up as in-use
									// Create a dummy pvc to protect so the reconcile can proceed properly
									dummyPod := &corev1.Pod{
										ObjectMeta: metav1.ObjectMeta{
											GenerateName: "test-pod-",
											Namespace:    testNamespace.GetName(),
										},
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{
												{
													Name:  "testcontainer1",
													Image: "testimage1",
												},
											},
											Volumes: []corev1.Volume{
												{
													Name: "testvol1",
													VolumeSource: corev1.VolumeSource{
														PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
															ClaimName: testPVCName,
														},
													},
												},
											},
										},
									}
									Expect(k8sClient.Create(ctx, dummyPod)).To(Succeed())

									// Make sure the pod is created to avoid any timing issues
									Eventually(func() error {
										return k8sClient.Get(ctx, client.ObjectKeyFromObject(dummyPod), dummyPod)
									}, maxWait, interval).Should(Succeed())
								})

								It("Should not complete the final sync", func() {
									finalSyncDone, returnedRS, err := vsHandler.ReconcileRS(rsSpec, true)
									Expect(err).NotTo(HaveOccurred()) // Not considered an error, we should just wait
									Expect(returnedRS).To(BeNil())
									Expect(finalSyncDone).To(BeFalse())
								})
							})

							Context("When the pvc is no longer in use", func() {
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
									createdRS.Status = &volsyncv1alpha1.ReplicationSourceStatus{
										LastManualSync: volsync.FinalSyncTriggerString,
									}
									Expect(k8sClient.Status().Update(ctx, createdRS)).To(Succeed())

									finalSyncDone, returnedRS, err = vsHandler.ReconcileRS(rsSpec, true)
									Expect(err).ToNot(HaveOccurred())
									Expect(finalSyncDone).To(BeTrue())
									Expect(returnedRS).NotTo(BeNil())

									// Now check to see if the pvc was removed
									Eventually(func() bool {
										err := k8sClient.Get(ctx, client.ObjectKeyFromObject(testPVC), testPVC)
										if err == nil {
											if !testPVC.GetDeletionTimestamp().IsZero() {
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

	Describe("Ensure PVC from ReplicationDestination", func() {
		pvcName := "testpvc1"
		pvcCapacity := resource.MustParse("1Gi")

		var rdSpec ramendrv1alpha1.VolSyncReplicationDestinationSpec
		BeforeEach(func() {
			rdSpec = ramendrv1alpha1.VolSyncReplicationDestinationSpec{
				ProtectedPVC: ramendrv1alpha1.ProtectedPVC{
					Name:               pvcName,
					ProtectedByVolSync: true,
					StorageClassName:   &testStorageClassName,
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: pvcCapacity,
						},
					},
				},
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
						Rsync: &volsyncv1alpha1.ReplicationDestinationRsyncSpec{},
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
				var latestImageSnap *unstructured.Unstructured
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
					Expect(pvc.Spec.Resources.Requests).To(Equal(corev1.ResourceList{
						corev1.ResourceStorage: pvcCapacity,
					}))
				})

				It("Should create PVC, latestImage VolumeSnapshot should have VRG owner ref added", func() {
					Eventually(func() bool {
						err := k8sClient.Get(ctx, types.NamespacedName{
							Name:      latestImageSnapshotName,
							Namespace: testNamespace.GetName(),
						}, latestImageSnap)
						if err != nil {
							return false
						}

						// Expect that owner configmap (which is faking our vrg) has been added as an owner
						// on the VolumeSnapshot - it should NOT be a controller, as the replicationdestination
						// will be the controller owning it
						return ownerMatches(latestImageSnap, owner.GetName(), "ConfigMap", false /* not controller */)
					}, maxWait, interval).Should(BeTrue())

					// The volumesnapshot should also have the volsync do-not-delete label added
					snapLabels := latestImageSnap.GetLabels()
					val, ok := snapLabels["volsync.backube/do-not-delete"]
					Expect(ok).To(BeTrue())
					Expect(val).To(Equal("true"))
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

				Context("When pvc to be restored has already been created", func() {
					It("ensure PVC should not fail", func() {
						// Previous ensurePVC will already have created the PVC (see parent context)
						// Now run ensurePVC again - additional runs should just ensure the PVC is ok
						Expect(vsHandler.EnsurePVCfromRD(rdSpec)).To(Succeed())
					})
				})

				Context("When pvc to be restored has already been created but has incorrect datasource", func() {
					var updatedImageSnap *unstructured.Unstructured

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
						err := vsHandler.EnsurePVCfromRD(rdSpec)
						Expect(err).To(HaveOccurred())
						Expect(err.Error()).To(ContainSubstring("incorrect datasource"))

						// Check that the PVC was deleted
						Eventually(func() bool {
							err := k8sClient.Get(ctx, client.ObjectKeyFromObject(pvc), pvc)
							if err == nil {
								if !pvc.GetDeletionTimestamp().IsZero() {
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
						Expect(vsHandler.EnsurePVCfromRD(rdSpec)).NotTo(HaveOccurred())

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
						ProtectedByVolSync: true,
						StorageClassName:   &testStorageClassName,
						Resources: corev1.ResourceRequirements{
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
			otherVSHandler := volsync.NewVSHandler(ctx, k8sClient, logger, otherOwnerCm,
				schedulingInterval, metav1.LabelSelector{})

			for i := 0; i < 2; i++ {
				otherOwnerRdSpec := ramendrv1alpha1.VolSyncReplicationDestinationSpec{
					ProtectedPVC: ramendrv1alpha1.ProtectedPVC{
						Name:               pvcNamePrefixOtherOwner + strconv.Itoa(i),
						ProtectedByVolSync: true,
						StorageClassName:   &testStorageClassName,
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: pvcCapacity,
							},
						},
					},
				}
				rdSpecListOtherOwner = append(rdSpecListOtherOwner, otherOwnerRdSpec)
			}

			// Create dummy volsync ssh secrets - will need one per vrg
			dummySSHSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      volsync.GetVolSyncSSHSecretNameFromVRGName(owner.GetName()),
					Namespace: testNamespace.GetName(),
				},
			}
			Expect(k8sClient.Create(ctx, dummySSHSecret)).To(Succeed())
			Expect(dummySSHSecret.GetName()).NotTo(BeEmpty())

			// Make sure the secret is created to avoid any timing issues
			Eventually(func() error {
				return k8sClient.Get(ctx,
					types.NamespacedName{
						Name:      dummySSHSecret.GetName(),
						Namespace: dummySSHSecret.GetNamespace(),
					}, dummySSHSecret)
			}, maxWait, interval).Should(Succeed())

			dummySSHSecretOtherOwner := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      volsync.GetVolSyncSSHSecretNameFromVRGName(otherOwnerCm.GetName()),
					Namespace: testNamespace.GetName(),
				},
			}
			Expect(k8sClient.Create(ctx, dummySSHSecretOtherOwner)).To(Succeed())
			Expect(dummySSHSecretOtherOwner.GetName()).NotTo(BeEmpty())

			// Make sure the secret is created to avoid any timing issues
			Eventually(func() error {
				return k8sClient.Get(ctx,
					types.NamespacedName{
						Name:      dummySSHSecretOtherOwner.GetName(),
						Namespace: dummySSHSecretOtherOwner.GetNamespace(),
					}, dummySSHSecretOtherOwner)
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
			otherVSHandler := volsync.NewVSHandler(ctx, k8sClient, logger, otherOwnerCm,
				schedulingInterval, metav1.LabelSelector{})

			for i := 0; i < 2; i++ {
				otherOwnerRsSpec := ramendrv1alpha1.VolSyncReplicationSourceSpec{
					ProtectedPVC: ramendrv1alpha1.ProtectedPVC{
						Name:               pvcNamePrefixOtherOwner + strconv.Itoa(i),
						ProtectedByVolSync: true,
						StorageClassName:   &testStorageClassName,
					},
				}
				rsSpecListOtherOwner = append(rsSpecListOtherOwner, otherOwnerRsSpec)
			}

			// Create dummy volsync ssh secrets - will need one per vrg
			dummySSHSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      volsync.GetVolSyncSSHSecretNameFromVRGName(owner.GetName()),
					Namespace: testNamespace.GetName(),
				},
			}
			Expect(k8sClient.Create(ctx, dummySSHSecret)).To(Succeed())
			Expect(dummySSHSecret.GetName()).NotTo(BeEmpty())

			// Make sure the secret is created to avoid any timing issues
			Eventually(func() error {
				return k8sClient.Get(ctx,
					types.NamespacedName{
						Name:      dummySSHSecret.GetName(),
						Namespace: dummySSHSecret.GetNamespace(),
					}, dummySSHSecret)
			}, maxWait, interval).Should(Succeed())

			dummySSHSecretOtherOwner := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      volsync.GetVolSyncSSHSecretNameFromVRGName(otherOwnerCm.GetName()),
					Namespace: testNamespace.GetName(),
				},
			}
			Expect(k8sClient.Create(ctx, dummySSHSecretOtherOwner)).To(Succeed())
			Expect(dummySSHSecretOtherOwner.GetName()).NotTo(BeEmpty())

			// Make sure the secret is created to avoid any timing issues
			Eventually(func() error {
				return k8sClient.Get(ctx,
					types.NamespacedName{
						Name:      dummySSHSecretOtherOwner.GetName(),
						Namespace: dummySSHSecretOtherOwner.GetNamespace(),
					}, dummySSHSecretOtherOwner)
			}, maxWait, interval).Should(Succeed())

			for _, rsSpec := range rsSpecList {
				// create RSs using our vsHandler
				_, returnedRS, err := vsHandler.ReconcileRS(rsSpec, false)
				Expect(err).NotTo(HaveOccurred())
				Expect(returnedRS).NotTo(BeNil())
			}
			for _, rsSpecOtherOwner := range rsSpecListOtherOwner {
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
				pvcPreparationComplete, err := vsHandler.PreparePVCForFinalSync("this-pvc-does-not-exist")
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
			}
			BeforeEach(func() {
				testPVCName := "my-test-pvc-aabbcc"
				capacity := resource.MustParse("1Gi")
				testPVC = createDummyPVC(testPVCName, testNamespace.GetName(), capacity, initialAnnotations)
			})

			var pvcPreparationComplete bool
			var pvcPreparationErr error

			JustBeforeEach(func() {
				pvcPreparationComplete, pvcPreparationErr = vsHandler.PreparePVCForFinalSync(testPVC.GetName())

				// In all cases at this point we should expect that the PVC has ownership taken over by our owner VRG
				Eventually(func() bool {
					err := k8sClient.Get(ctx, client.ObjectKeyFromObject(testPVC), testPVC)
					if err != nil {
						return false
					}
					// configmap owner is faking out VRG
					return ownerMatches(testPVC, owner.GetName(), "ConfigMap", false)
				}, maxWait, interval).Should(BeTrue())

				// do-not-delete label should be added to the PVC
				pvcLabels := testPVC.GetLabels()
				val, ok := pvcLabels["do-not-delete"]
				Expect(ok).To(BeTrue())
				Expect(val).To(Equal("true"))
			})

			Context("When the pvc reconcile-option annotation does not exist", func() {
				It("Should complete successfully, return true and remove ACM annotations", func() {
					Expect(pvcPreparationErr).ToNot(HaveOccurred())
					Expect(pvcPreparationComplete).To(BeTrue())

					Eventually(func() int {
						err := k8sClient.Get(ctx, client.ObjectKeyFromObject(testPVC), testPVC)
						if err != nil {
							return 0
						}

						return len(testPVC.Annotations)
					}, maxWait, interval).Should(Equal(len(initialAnnotations) - 2))
					// We had 2 acm annotations in initialAnnotations

					for key, val := range testPVC.Annotations {
						Expect(strings.HasPrefix(key, "apps.open-cluster-management.io")).To(BeFalse())
						Expect(initialAnnotations[key]).To(Equal(val)) // Other annotations should still be there
					}
				})
			})

			Context("When the pvc reconcile-option annotation is set to merge", func() {
				BeforeEach(func() {
					// test pvc has been created, update it with the annotation prior to running prepavePVCForFinalSync
					testPVC.Annotations["apps.open-cluster-management.io/reconcile-option"] = "merge"
					Expect(k8sClient.Update(ctx, testPVC)).To(Succeed())

					// Make sure the PVC annotations have been updated before proceeding to avoid timing issues
					Eventually(func() bool {
						err := k8sClient.Get(ctx, client.ObjectKeyFromObject(testPVC), testPVC)
						if err != nil {
							return false
						}
						val, ok := testPVC.Annotations["apps.open-cluster-management.io/reconcile-option"]
						if ok {
							Expect(val).To(Equal("merge"))
						}

						return ok
					}, maxWait, interval).Should(BeTrue())
				})

				It("Should complete successfully, return true and remove ACM annotations", func() {
					Expect(pvcPreparationErr).ToNot(HaveOccurred())
					Expect(pvcPreparationComplete).To(BeTrue())

					Eventually(func() int {
						err := k8sClient.Get(ctx, client.ObjectKeyFromObject(testPVC), testPVC)
						if err != nil {
							return 0
						}

						return len(testPVC.Annotations)
					}, maxWait, interval).Should(Equal(len(initialAnnotations) - 2))
					// We had 2 acm annotations in initialAnnotations

					for key, val := range testPVC.Annotations {
						Expect(strings.HasPrefix(key, "apps.open-cluster-management.io")).To(BeFalse())
						Expect(initialAnnotations[key]).To(Equal(val)) // Other annotations should still be there
					}
				})
			})

			Context("When the pvc reconcile-option annotation is set to 'mergeAndOwn'", func() {
				BeforeEach(func() {
					// test pvc has been created, update it with the annotation prior to running prepavePVCForFinalSync
					testPVC.Annotations["apps.open-cluster-management.io/reconcile-option"] = "mergeAndOwn"
					Expect(k8sClient.Update(ctx, testPVC)).To(Succeed())

					// Make sure the PVC annotations have been updated before proceeding to avoid timing issues
					Eventually(func() bool {
						err := k8sClient.Get(ctx, client.ObjectKeyFromObject(testPVC), testPVC)
						if err != nil {
							return false
						}

						_, ok := testPVC.Annotations["apps.open-cluster-management.io/reconcile-option"]

						return ok
					}, maxWait, interval).Should(BeTrue())
				})

				It("Should indicate pvcPreparationComplete is false (need to wait for annotation update)", func() {
					Expect(pvcPreparationErr).ToNot(HaveOccurred())
					Expect(pvcPreparationComplete).To(BeFalse())
				})
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

func createSnapshot(snapshotName, namespace string) *unstructured.Unstructured {
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
		Group:   "snapshot.storage.k8s.io",
		Kind:    volsync.VolumeSnapshotKind,
		Version: "v1",
	})

	Expect(k8sClient.Create(ctx, volSnap)).To(Succeed())

	// Make sure the volume snapshot is created to avoid any timing issues
	Eventually(func() error {
		return k8sClient.Get(ctx, client.ObjectKeyFromObject(volSnap), volSnap)
	}, maxWait, interval).Should(Succeed())

	return volSnap
}

func createDummyPVC(pvcName, namespace string, capacity resource.Quantity,
	annotations map[string]string) *corev1.PersistentVolumeClaim {
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
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: capacity,
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, dummyPVC)).To(Succeed())

	// Make sure the PVC is created to avoid any timing issues
	Eventually(func() error {
		return k8sClient.Get(ctx, client.ObjectKeyFromObject(dummyPVC), dummyPVC)
	}, maxWait, interval).Should(Succeed())

	return dummyPVC
}
