// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package cephfscg_test

import (
	"context"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	vsv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	internalController "github.com/ramendr/ramen/internal/controller"
	"github.com/ramendr/ramen/internal/controller/cephfscg"
	"github.com/ramendr/ramen/internal/controller/util"
	"github.com/ramendr/ramen/internal/controller/volsync"
	groupsnapv1beta1 "github.com/red-hat-storage/external-snapshotter/client/v8/apis/volumegroupsnapshot/v1beta1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	vgdName = "vgd"
	vrgName = "vrg"
)

var _ = Describe("Cghandler", func() {
	var vsCGHandler cephfscg.VSCGHandler

	Describe("CreateOrUpdateReplicationGroupDestination", func() {
		It("Should be success", func() {
			vsCGHandler = cephfscg.NewVSCGHandler(Ctx, k8sClient, &ramendrv1alpha1.VolumeReplicationGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      vgdName,
					Namespace: "default",
					UID:       "123",
				},
				Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
					Async: &ramendrv1alpha1.VRGAsyncSpec{},
				},
			}, nil, nil, "0", testLogger)
			rgd, err := vsCGHandler.CreateOrUpdateReplicationGroupDestination(vgdName, "default", nil)
			Expect(err).To(BeNil())
			Expect(len(rgd.Spec.RDSpecs)).To(Equal(0))
		})
		It("Should be success", func() {
			vsCGHandler = cephfscg.NewVSCGHandler(Ctx, k8sClient, &ramendrv1alpha1.VolumeReplicationGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      vgdName,
					Namespace: "default",
					UID:       "123",
				},
				Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
					Async: &ramendrv1alpha1.VRGAsyncSpec{},
				},
			}, nil, nil, "0", testLogger)
			rgd, err := vsCGHandler.CreateOrUpdateReplicationGroupDestination(vgdName, "default",
				[]ramendrv1alpha1.VolSyncReplicationDestinationSpec{{
					ProtectedPVC: ramendrv1alpha1.ProtectedPVC{
						Name:      vsName,
						Namespace: "default",
					},
				}})
			Expect(err).To(BeNil())
			Expect(len(rgd.Spec.RDSpecs)).To(Equal(1))
		})
	})
	Describe("CreateOrUpdateReplicationGroupSource", func() {
		BeforeEach(func() {
			scName := "test"
			sc := &storagev1.StorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: scName,
				},
				Provisioner: "testProvisioner",
			}

			Eventually(func() error {
				err := k8sClient.Create(context.TODO(), sc)

				return client.IgnoreAlreadyExists(err)
			}, timeout, interval).Should(BeNil())

			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pvc",
					Namespace: "default",
					Labels:    map[string]string{"testpvc": "testpvc"},
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					StorageClassName: &scName,
					AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadOnlyMany},
					Resources: corev1.VolumeResourceRequirements{
						Limits: map[corev1.ResourceName]resource.Quantity{
							corev1.ResourceStorage: *resource.NewQuantity(1, resource.BinarySI),
						},
						Requests: map[corev1.ResourceName]resource.Quantity{
							corev1.ResourceStorage: *resource.NewQuantity(1, resource.BinarySI),
						},
					},
				},
			}

			Eventually(func() error {
				err := k8sClient.Create(context.TODO(), pvc)

				return client.IgnoreAlreadyExists(err)
			}, timeout, interval).Should(BeNil())

			vgsc := &groupsnapv1beta1.VolumeGroupSnapshotClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "vgsc",
					Labels: map[string]string{"test": "test"},
				},
				Driver:         "testProvisioner",
				DeletionPolicy: vsv1.VolumeSnapshotContentDelete,
			}

			Eventually(func() error {
				err := k8sClient.Create(context.TODO(), vgsc)

				return client.IgnoreAlreadyExists(err)
			}, timeout, interval).Should(BeNil())
		})
		AfterEach(func() {
			Eventually(func() error {
				err := k8sClient.Delete(context.TODO(), &corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pvc",
						Namespace: "default",
					},
				})

				return client.IgnoreNotFound(err)
			}, timeout, interval).Should(BeNil())
			Eventually(func() error {
				err := k8sClient.Delete(context.TODO(), &storagev1.StorageClass{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test",
					},
				})

				return client.IgnoreNotFound(err)
			}, timeout, interval).Should(BeNil())

			Eventually(func() error {
				err := k8sClient.Delete(context.TODO(), &groupsnapv1beta1.VolumeGroupSnapshotClass{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "vgsc",
						Labels: map[string]string{"test": "test"},
					},
				})

				return client.IgnoreNotFound(err)
			}, timeout, interval).Should(BeNil())
		})
		It("Should be success", func() {
			vsCGHandler = cephfscg.NewVSCGHandler(Ctx, k8sClient, &ramendrv1alpha1.VolumeReplicationGroup{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      vrgName,
					UID:       "123",
				},
				Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
					Async: &ramendrv1alpha1.VRGAsyncSpec{},
				},
			}, &metav1.LabelSelector{}, nil, "0", testLogger)
			rgs, finalSync, err := vsCGHandler.CreateOrUpdateReplicationGroupSource(rgsName, "default", false)
			Expect(err).To(BeNil())
			Expect(finalSync).To(BeFalse())
			Expect(rgs.Spec.Trigger.Schedule).NotTo(BeNil())
			Expect(*rgs.Spec.Trigger.Schedule).NotTo(BeEmpty())
		})
	})
	Describe("GetLatestImageFromRGD", func() {
		It("Should be failed", func() {
			vsCGHandler = cephfscg.NewVSCGHandler(Ctx, k8sClient, &ramendrv1alpha1.VolumeReplicationGroup{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      vrgName,
					UID:       "123",
				},
				Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
					Async: &ramendrv1alpha1.VRGAsyncSpec{},
				},
			}, &metav1.LabelSelector{}, nil, "0", testLogger)
			image, err := vsCGHandler.GetLatestImageFromRGD(Ctx, "notexist", "default")
			Expect(err).NotTo(BeNil())
			Expect(image).To(BeNil())
		})
		Context("rd exist but rgd not", func() {
			BeforeEach(func() {
				Eventually(func() error {
					err := k8sClient.Create(context.TODO(), &volsyncv1alpha1.ReplicationDestination{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "pvc1",
							Namespace: "default",
							Labels:    map[string]string{util.RGDOwnerLabel: rgdName},
						},
					})

					return client.IgnoreAlreadyExists(err)
				}, timeout, interval).Should(BeNil())
			})
			AfterEach(func() {
				Eventually(func() error {
					err := k8sClient.Delete(context.TODO(), &volsyncv1alpha1.ReplicationDestination{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "pvc1",
							Namespace: "default",
						},
					})

					return client.IgnoreNotFound(err)
				}, timeout, interval).Should(BeNil())
			})
			It("Should be failed", func() {
				vsCGHandler = cephfscg.NewVSCGHandler(Ctx, k8sClient, &ramendrv1alpha1.VolumeReplicationGroup{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      vrgName,
						UID:       "123",
					},
					Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
						Async: &ramendrv1alpha1.VRGAsyncSpec{},
					},
				}, &metav1.LabelSelector{}, nil, "0", testLogger)
				image, err := vsCGHandler.GetLatestImageFromRGD(Ctx, "pvc1", "default")
				Expect(err).NotTo(BeNil())
				Expect(image).To(BeNil())
			})
			Context("rd,rgd exist", func() {
				BeforeEach(func() {
					rgd := &ramendrv1alpha1.ReplicationGroupDestination{
						ObjectMeta: metav1.ObjectMeta{
							Name:      rgdName,
							Namespace: "default",
						},
					}
					Eventually(func() error {
						err := k8sClient.Create(Ctx, rgd)

						return client.IgnoreAlreadyExists(err)
					}, timeout, interval).Should(BeNil())
					Eventually(func() error {
						err := k8sClient.Get(Ctx, types.NamespacedName{
							Name:      rgdName,
							Namespace: "default",
						}, rgd)
						if err != nil {
							return err
						}

						rgd.Status = ramendrv1alpha1.ReplicationGroupDestinationStatus{
							LatestImages: map[string]*corev1.TypedLocalObjectReference{
								"pvc1": {
									Name: "image1",
									Kind: volsync.VolumeSnapshotKind,
								},
							},
						}

						return k8sClient.Status().Update(Ctx, rgd)
					}, timeout, interval).Should(BeNil())
				})
				AfterEach(func() {
					Eventually(func() error {
						err := k8sClient.Delete(context.TODO(), &ramendrv1alpha1.ReplicationGroupDestination{
							ObjectMeta: metav1.ObjectMeta{
								Name:      rgdName,
								Namespace: "default",
							},
						})

						return client.IgnoreNotFound(err)
					}, timeout, interval).Should(BeNil())
				})
				It("Should be success", func() {
					vsCGHandler = cephfscg.NewVSCGHandler(Ctx, k8sClient, &ramendrv1alpha1.VolumeReplicationGroup{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "default",
							Name:      vrgName,
							UID:       "123",
						},
						Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
							Async: &ramendrv1alpha1.VRGAsyncSpec{},
						},
					}, &metav1.LabelSelector{}, nil, "0", testLogger)
					image, err := vsCGHandler.GetLatestImageFromRGD(Ctx, "pvc1", "default")
					Expect(err).To(BeNil())
					Expect(image.Name).To(Equal("image1"))
				})
				Describe("EnsurePVCfromRGD", func() {
					It("Should be success", func() {
						CreateVS("image1", "", "")
						UpdateVS("image1")
						CreatePVC("pvc1")

						vsCGHandler = cephfscg.NewVSCGHandler(Ctx, k8sClient, &ramendrv1alpha1.VolumeReplicationGroup{
							ObjectMeta: metav1.ObjectMeta{
								Namespace: "default",
								Name:      vrgName,
								UID:       "123",
							},
							Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
								Async: &ramendrv1alpha1.VRGAsyncSpec{},
							},
						}, &metav1.LabelSelector{},
							volsync.NewVSHandler(context.Background(), k8sClient, testLogger, &ramendrv1alpha1.VolumeReplicationGroup{
								ObjectMeta: metav1.ObjectMeta{
									Namespace: "default",
									Name:      vrgName,
									UID:       "123",
								},
							}, &ramendrv1alpha1.VRGAsyncSpec{}, internalController.DefaultCephFSCSIDriverName,
								"Direct", false,
							), "0", testLogger)
						err := vsCGHandler.EnsurePVCfromRGD(ramendrv1alpha1.VolSyncReplicationDestinationSpec{
							ProtectedPVC: ramendrv1alpha1.ProtectedPVC{
								Name:               "pvc1",
								Namespace:          "default",
								ProtectedByVolSync: true,
								StorageClassName:   &scName,
								AccessModes:        []corev1.PersistentVolumeAccessMode{corev1.ReadOnlyMany},
								Resources: corev1.VolumeResourceRequirements{
									Limits: map[corev1.ResourceName]resource.Quantity{
										corev1.ResourceStorage: *resource.NewQuantity(1, resource.BinarySI),
									},
									Requests: map[corev1.ResourceName]resource.Quantity{
										corev1.ResourceStorage: *resource.NewQuantity(1, resource.BinarySI),
									},
								},
							},
						}, false)
						Expect(err).To(BeNil())
					})
				})
				Describe("DeleteLocalRDAndRS", func() {
					It("Should be success", func() {
						vsCGHandler = cephfscg.NewVSCGHandler(
							Ctx, k8sClient, &ramendrv1alpha1.VolumeReplicationGroup{
								ObjectMeta: metav1.ObjectMeta{
									Namespace: "default",
									Name:      vrgName,
									UID:       "123",
								},
								Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
									Async: &ramendrv1alpha1.VRGAsyncSpec{},
								},
							}, &metav1.LabelSelector{},
							volsync.NewVSHandler(
								context.Background(), k8sClient, testLogger,
								&ramendrv1alpha1.VolumeReplicationGroup{
									ObjectMeta: metav1.ObjectMeta{
										Namespace: "default",
										Name:      vrgName,
										UID:       "123",
									},
								}, &ramendrv1alpha1.VRGAsyncSpec{}, internalController.DefaultCephFSCSIDriverName,
								"Direct", false,
							), "0", testLogger)
						err := vsCGHandler.DeleteLocalRDAndRS(&volsyncv1alpha1.ReplicationDestination{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "pvc1",
								Namespace: "default",
							},
						})
						Expect(err).To(BeNil())
					})
				})
			})
		})
	})
	Describe("CheckIfPVCMatchLabel", func() {
		It("Should be success", func() {
			vsCGHandler = cephfscg.NewVSCGHandler(
				Ctx, k8sClient, &ramendrv1alpha1.VolumeReplicationGroup{
					Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
						Async: &ramendrv1alpha1.VRGAsyncSpec{},
					},
				}, nil, nil, "0", testLogger,
			)
			match, err := vsCGHandler.CheckIfPVCMatchLabel(nil)
			Expect(err).To(BeNil())
			Expect(match).To(BeFalse())
		})
		It("Should be success", func() {
			vsCGHandler = cephfscg.NewVSCGHandler(
				Ctx, k8sClient, &ramendrv1alpha1.VolumeReplicationGroup{
					Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
						Async: &ramendrv1alpha1.VRGAsyncSpec{},
					},
				}, nil, nil, "0", testLogger,
			)
			match, err := vsCGHandler.CheckIfPVCMatchLabel(map[string]string{"test": "test"})
			Expect(err).To(BeNil())
			Expect(match).To(BeFalse())
		})
		It("Should be success", func() {
			vsCGHandler = cephfscg.NewVSCGHandler(Ctx, k8sClient, &ramendrv1alpha1.VolumeReplicationGroup{
				Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
					Async: &ramendrv1alpha1.VRGAsyncSpec{},
				},
			},
				&metav1.LabelSelector{MatchLabels: map[string]string{"test": ""}}, nil, "0", testLogger)
			match, err := vsCGHandler.CheckIfPVCMatchLabel(map[string]string{"test": "test"})
			Expect(err).To(BeNil())
			Expect(match).To(BeFalse())
		})
		It("Should be success", func() {
			vsCGHandler = cephfscg.NewVSCGHandler(Ctx, k8sClient, &ramendrv1alpha1.VolumeReplicationGroup{
				Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
					Async: &ramendrv1alpha1.VRGAsyncSpec{},
				},
			},
				&metav1.LabelSelector{MatchLabels: map[string]string{"test": "test"}}, nil, "0", testLogger)
			match, err := vsCGHandler.CheckIfPVCMatchLabel(map[string]string{"test": "test"})
			Expect(err).To(BeNil())
			Expect(match).To(BeTrue())
		})
	})
	Describe("GetRDInCG", func() {
		It("Should be success", func() {
			vsCGHandler = cephfscg.NewVSCGHandler(Ctx, k8sClient, &ramendrv1alpha1.VolumeReplicationGroup{
				Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
					Async: &ramendrv1alpha1.VRGAsyncSpec{},
				},
			},
				nil, nil, "0", testLogger)
			rdSpecs, err := vsCGHandler.GetRDInCG()
			Expect(err).To(BeNil())
			Expect(len(rdSpecs)).To(Equal(0))
		})
		It("Should be success", func() {
			vsCGHandler = cephfscg.NewVSCGHandler(Ctx, k8sClient, &ramendrv1alpha1.VolumeReplicationGroup{
				Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
					VolSync: ramendrv1alpha1.VolSyncSpec{
						RDSpec: []ramendrv1alpha1.VolSyncReplicationDestinationSpec{{
							ProtectedPVC: ramendrv1alpha1.ProtectedPVC{
								Labels: map[string]string{"test": "test"},
							},
						}},
					},
					Async: &ramendrv1alpha1.VRGAsyncSpec{},
				},
			}, nil, nil, "0", testLogger)
			rdSpecs, err := vsCGHandler.GetRDInCG()
			Expect(err).To(BeNil())
			Expect(len(rdSpecs)).To(Equal(0))
		})
		It("Should be success", func() {
			vsCGHandler = cephfscg.NewVSCGHandler(Ctx, k8sClient, &ramendrv1alpha1.VolumeReplicationGroup{
				Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
					VolSync: ramendrv1alpha1.VolSyncSpec{
						RDSpec: []ramendrv1alpha1.VolSyncReplicationDestinationSpec{{
							ProtectedPVC: ramendrv1alpha1.ProtectedPVC{
								Labels: map[string]string{"test": "test"},
							},
						}},
					},
					Async: &ramendrv1alpha1.VRGAsyncSpec{},
				},
			}, &metav1.LabelSelector{
				MatchLabels: map[string]string{"test": "test"},
			}, nil, "0", testLogger)
			rdSpecs, err := vsCGHandler.GetRDInCG()
			Expect(err).To(BeNil())
			Expect(len(rdSpecs)).To(Equal(1))
		})
	})
})
