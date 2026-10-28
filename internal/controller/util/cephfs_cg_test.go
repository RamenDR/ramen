// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util_test

import (
	"context"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	ramenutils "github.com/backube/volsync/controllers/utils"
	vsv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	groupsnapv1beta1 "github.com/red-hat-storage/external-snapshotter/client/v8/apis/volumegroupsnapshot/v1beta1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/util"
)

var _ = Describe("CephfsCg", func() {
	Describe("IsReplicationGroupDestinationReady", func() {
		Describe("ReplicationGroupDestination is empty", func() {
			It("Should be false", func() {
				isReplicationGroupDestinationReady, err := util.IsReplicationGroupDestinationReady(
					context.TODO(), k8sClient, &v1alpha1.ReplicationGroupDestination{})
				Expect(err).To(BeNil())
				Expect(isReplicationGroupDestinationReady).To(BeFalse())
			})
		})
		Describe("the status of ReplicationGroupDestination is empty", func() {
			It("Should be false", func() {
				isReplicationGroupDestinationReady, err := util.IsReplicationGroupDestinationReady(
					context.TODO(), k8sClient, &v1alpha1.ReplicationGroupDestination{
						Spec: v1alpha1.ReplicationGroupDestinationSpec{
							RDSpecs: []v1alpha1.VolSyncReplicationDestinationSpec{{
								ProtectedPVC: v1alpha1.ProtectedPVC{Name: "pvc1", Namespace: "default"},
							}, {
								ProtectedPVC: v1alpha1.ProtectedPVC{Name: "pvc2", Namespace: "default"},
							}},
						},
					})
				Expect(err).To(BeNil())
				Expect(isReplicationGroupDestinationReady).To(BeFalse())
			})
		})
		Describe("the numb of RD in status != the numb of RDSpecs in Spec", func() {
			It("Should be false", func() {
				isReplicationGroupDestinationReady, err := util.IsReplicationGroupDestinationReady(
					context.TODO(), k8sClient, &v1alpha1.ReplicationGroupDestination{
						Spec: v1alpha1.ReplicationGroupDestinationSpec{
							RDSpecs: []v1alpha1.VolSyncReplicationDestinationSpec{{
								ProtectedPVC: v1alpha1.ProtectedPVC{Name: "pvc1", Namespace: "default"},
							}, {
								ProtectedPVC: v1alpha1.ProtectedPVC{Name: "pvc2", Namespace: "default"},
							}},
						},
						Status: v1alpha1.ReplicationGroupDestinationStatus{
							ReplicationDestinations: []*corev1.ObjectReference{{Name: "pvc2", Namespace: "default"}},
						},
					})
				Expect(err).To(BeNil())
				Expect(isReplicationGroupDestinationReady).To(BeFalse())
			})
		})
		Describe("the numb of RD in status == the numb of RDSpecs in Spec, but RDs donot exist", func() {
			It("Should be false", func() {
				_, err := util.IsReplicationGroupDestinationReady(
					context.TODO(), k8sClient, &v1alpha1.ReplicationGroupDestination{
						Spec: v1alpha1.ReplicationGroupDestinationSpec{
							RDSpecs: []v1alpha1.VolSyncReplicationDestinationSpec{{
								ProtectedPVC: v1alpha1.ProtectedPVC{Name: "pvc1", Namespace: "default"},
							}, {
								ProtectedPVC: v1alpha1.ProtectedPVC{Name: "pvc2", Namespace: "default"},
							}},
						},
						Status: v1alpha1.ReplicationGroupDestinationStatus{
							ReplicationDestinations: []*corev1.ObjectReference{
								{Name: "pvc1", Namespace: "default"},
								{Name: "pvc2", Namespace: "default"},
							},
						},
					})
				Expect(err).NotTo(BeNil())
				Expect(k8serrors.IsNotFound(err)).To(BeTrue())
			})
		})
		Describe("the numb of RD in status == the numb of RDSpecs in Spec, but RDs is not ready", func() {
			BeforeEach(func() {
				Eventually(func() error {
					err := k8sClient.Create(context.TODO(), &volsyncv1alpha1.ReplicationDestination{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "pvc1",
							Namespace: "default",
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

			It("Should be false", func() {
				isReplicationGroupDestinationReady, err := util.IsReplicationGroupDestinationReady(
					context.TODO(), k8sClient, &v1alpha1.ReplicationGroupDestination{
						Spec: v1alpha1.ReplicationGroupDestinationSpec{
							RDSpecs: []v1alpha1.VolSyncReplicationDestinationSpec{
								{ProtectedPVC: v1alpha1.ProtectedPVC{Name: "pvc1", Namespace: "default"}},
							},
						},
						Status: v1alpha1.ReplicationGroupDestinationStatus{
							ReplicationDestinations: []*corev1.ObjectReference{
								{Name: "pvc1", Namespace: "default"},
							},
						},
					})
				Expect(err).To(BeNil())
				Expect(isReplicationGroupDestinationReady).To(BeFalse())
			})
		})

		Describe("the numb of RD in status == the numb of RDSpecs in Spec, but RDs are ready", func() {
			BeforeEach(func() {
				rd := &volsyncv1alpha1.ReplicationDestination{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pvc1",
						Namespace: "default",
					},
				}

				Eventually(func() error {
					err := k8sClient.Create(context.TODO(), rd)

					return client.IgnoreAlreadyExists(err)
				}, timeout, interval).Should(BeNil())

				retryErr := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
					err := k8sClient.Get(context.TODO(), types.NamespacedName{
						Name: "pvc1", Namespace: "default",
					}, rd)
					if err != nil {
						return err
					}

					address := "address"
					rd.Status = &volsyncv1alpha1.ReplicationDestinationStatus{
						RsyncTLS: &volsyncv1alpha1.ReplicationDestinationRsyncTLSStatus{
							Address: &address,
						},
					}

					return k8sClient.Status().Update(context.TODO(), rd)
				})

				Expect(retryErr).NotTo(HaveOccurred())
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
			It("Should be true", func() {
				isReplicationGroupDestinationReady, err := util.IsReplicationGroupDestinationReady(
					context.TODO(), k8sClient, &v1alpha1.ReplicationGroupDestination{
						Spec: v1alpha1.ReplicationGroupDestinationSpec{
							RDSpecs: []v1alpha1.VolSyncReplicationDestinationSpec{
								{ProtectedPVC: v1alpha1.ProtectedPVC{Name: "pvc1", Namespace: "default"}},
							},
						},
						Status: v1alpha1.ReplicationGroupDestinationStatus{
							ReplicationDestinations: []*corev1.ObjectReference{
								{Name: "pvc1", Namespace: "default"},
							},
						},
					})
				Expect(err).To(BeNil())
				Expect(isReplicationGroupDestinationReady).To(BeTrue())
			})
		})
	})

	Describe("DeleteReplicationGroupSource", func() {
		It("Should be successful", func() {
			err := util.DeleteReplicationGroupSource(
				context.Background(), k8sClient, "notexist", "notexist")
			Expect(err).To(BeNil())
		})
	})

	Describe("DeleteReplicationGroupDestination", func() {
		It("Should be successful", func() {
			err := util.DeleteReplicationGroupDestination(
				context.Background(), k8sClient, "notexist", "notexist")
			Expect(err).To(BeNil())
		})
	})

	Context("vgsc exists", func() {
		BeforeEach(func() {
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
				err := k8sClient.Delete(context.TODO(), &groupsnapv1beta1.VolumeGroupSnapshotClass{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "vgsc",
						Labels: map[string]string{"test": "test"},
					},
				})

				return client.IgnoreNotFound(err)
			}, timeout, interval).Should(BeNil())
		})
		Describe("GetVolumeGroupSnapshotClassFromPVCsStorageClass", func() {
			vgscSelector := metav1.LabelSelector{MatchLabels: map[string]string{"test": "test"}}

			getVGSC := func(selector metav1.LabelSelector, scName string) (string, error) {
				return util.GetVolumeGroupSnapshotClassFromPVCsStorageClass(
					context.Background(), k8sClient, selector, scName, testLogger)
			}

			It("Should fail when no storage class name is given", func() {
				volumeGroupSnapshotClassName, err := getVGSC(vgscSelector, "")
				Expect(volumeGroupSnapshotClassName).To(Equal(""))
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("no storageClassName given"))
			})
			It("Should fail when the storage class does not exist", func() {
				volumeGroupSnapshotClassName, err := getVGSC(vgscSelector, "notexist")
				Expect(volumeGroupSnapshotClassName).To(Equal(""))
				Expect(k8serrors.IsNotFound(err)).To(BeTrue())
			})
			Context("storage class provisioner does not match the vgsc driver", func() {
				BeforeEach(func() { CreateSC(SCName, "testProvisioner1", nil) })
				AfterEach(func() { DeleteSC(SCName) })
				It("Should fail", func() {
					volumeGroupSnapshotClassName, err := getVGSC(metav1.LabelSelector{}, SCName)
					Expect(volumeGroupSnapshotClassName).To(Equal(""))
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring("unable to find matching volumegroupsnapshotclass"))
				})
			})
			Context("storage class exists", func() {
				BeforeEach(func() { CreateSC(SCName, "testProvisioner", nil) })
				AfterEach(func() { DeleteSC(SCName) })
				It("Should fail when no vgsc matches the selector", func() {
					volumeGroupSnapshotClassName, err := getVGSC(
						metav1.LabelSelector{MatchLabels: map[string]string{"test": "testxxxx"}}, SCName)
					Expect(volumeGroupSnapshotClassName).To(Equal(""))
					Expect(err).To(HaveOccurred())
				})
				It("Should return the vgsc with the matching driver", func() {
					volumeGroupSnapshotClassName, err := getVGSC(metav1.LabelSelector{}, SCName)
					Expect(err).NotTo(HaveOccurred())
					Expect(volumeGroupSnapshotClassName).To(Equal("vgsc"))

					volumeGroupSnapshotClassName, err = getVGSC(vgscSelector, SCName)
					Expect(err).NotTo(HaveOccurred())
					Expect(volumeGroupSnapshotClassName).To(Equal("vgsc"))
				})
			})
			Context("storage class has a storageID", func() {
				const (
					vgscWithSIDName = "vgsc-sid"
					storageID       = "sid1"
				)

				sidSelector := metav1.LabelSelector{MatchLabels: map[string]string{"test": "sid"}}

				BeforeEach(func() {
					Eventually(func() error {
						err := k8sClient.Create(context.TODO(), &groupsnapv1beta1.VolumeGroupSnapshotClass{
							ObjectMeta: metav1.ObjectMeta{
								Name:   vgscWithSIDName,
								Labels: map[string]string{"test": "sid", util.StorageIDLabel: storageID},
							},
							Driver:         "testProvisioner",
							DeletionPolicy: vsv1.VolumeSnapshotContentDelete,
						})

						return client.IgnoreAlreadyExists(err)
					}, timeout, interval).Should(BeNil())
				})
				AfterEach(func() {
					DeleteSC(SCName)
					Eventually(func() error {
						err := k8sClient.Delete(context.TODO(), &groupsnapv1beta1.VolumeGroupSnapshotClass{
							ObjectMeta: metav1.ObjectMeta{Name: vgscWithSIDName},
						})

						return client.IgnoreNotFound(err)
					}, timeout, interval).Should(BeNil())
				})
				It("Should return the vgsc with the same storageID", func() {
					CreateSC(SCName, "testProvisioner", map[string]string{util.StorageIDLabel: storageID})

					volumeGroupSnapshotClassName, err := getVGSC(sidSelector, SCName)
					Expect(err).NotTo(HaveOccurred())
					Expect(volumeGroupSnapshotClassName).To(Equal(vgscWithSIDName))
				})
				It("Should skip the vgsc with a different storageID", func() {
					CreateSC(SCName, "testProvisioner", map[string]string{util.StorageIDLabel: "other"})

					volumeGroupSnapshotClassName, err := getVGSC(sidSelector, SCName)
					Expect(volumeGroupSnapshotClassName).To(Equal(""))
					Expect(err).To(HaveOccurred())
				})
				It("Should skip the vgsc with a storageID when the storage class has none", func() {
					CreateSC(SCName, "testProvisioner", nil)

					volumeGroupSnapshotClassName, err := getVGSC(sidSelector, SCName)
					Expect(volumeGroupSnapshotClassName).To(Equal(""))
					Expect(err).To(HaveOccurred())
				})
				It("Should not check the storageID when the vgsc has none", func() {
					CreateSC(SCName, "testProvisioner", map[string]string{util.StorageIDLabel: storageID})

					volumeGroupSnapshotClassName, err := getVGSC(vgscSelector, SCName)
					Expect(err).NotTo(HaveOccurred())
					Expect(volumeGroupSnapshotClassName).To(Equal("vgsc"))
				})
			})
		})
		Describe("GetVolumeGroupSnapshotClasses", func() {
			It("Should be successful", func() {
				volumeGroupSnapshotClasses, err := util.GetVolumeGroupSnapshotClasses(
					context.Background(), k8sClient, metav1.LabelSelector{MatchLabels: map[string]string{"test": "test"}})
				Expect(err).To(BeNil())
				Expect(len(volumeGroupSnapshotClasses)).To(Equal(1))
			})
		})
	})
	Describe("IsRDExist", func() {
		It("Should be false", func() {
			exist := util.IsRDExist(v1alpha1.VolSyncReplicationDestinationSpec{},
				[]v1alpha1.VolSyncReplicationDestinationSpec{})
			Expect(exist).To(BeFalse())
		})
		It("Should be false", func() {
			exist := util.IsRDExist(
				v1alpha1.VolSyncReplicationDestinationSpec{
					ProtectedPVC: v1alpha1.ProtectedPVC{Name: "test", Namespace: "ns"},
				}, []v1alpha1.VolSyncReplicationDestinationSpec{{
					ProtectedPVC: v1alpha1.ProtectedPVC{Name: "test", Namespace: "ns1"},
				}},
			)
			Expect(exist).To(BeFalse())
		})
		It("Should be false", func() {
			exist := util.IsRDExist(
				v1alpha1.VolSyncReplicationDestinationSpec{
					ProtectedPVC: v1alpha1.ProtectedPVC{Name: "test", Namespace: "ns"},
				}, []v1alpha1.VolSyncReplicationDestinationSpec{{
					ProtectedPVC: v1alpha1.ProtectedPVC{Name: "test"},
				}},
			)
			Expect(exist).To(BeFalse())
		})
		It("Should be true", func() {
			exist := util.IsRDExist(
				v1alpha1.VolSyncReplicationDestinationSpec{
					ProtectedPVC: v1alpha1.ProtectedPVC{Name: "test", Namespace: "ns"},
				},
				[]v1alpha1.VolSyncReplicationDestinationSpec{
					{ProtectedPVC: v1alpha1.ProtectedPVC{Name: "test", Namespace: "ns"}},
				},
			)
			Expect(exist).To(BeTrue())
		})
	})

	Describe("CheckImagesReadyToUse", func() {
		It("should be not ready", func() {
			ready, err := util.CheckImagesReadyToUse(context.TODO(), k8sClient, nil, "", testLogger)
			Expect(ready).To(BeFalse())
			Expect(err).To(BeNil())
		})
		It("should be not ready", func() {
			ready, err := util.CheckImagesReadyToUse(context.TODO(), k8sClient,
				map[string]*corev1.TypedLocalObjectReference{"test": nil}, "", testLogger)
			Expect(ready).To(BeFalse())
			Expect(err).To(BeNil())
		})
		It("should be failed", func() {
			ready, err := util.CheckImagesReadyToUse(context.TODO(), k8sClient,
				map[string]*corev1.TypedLocalObjectReference{"test": {
					Name: "test",
				}}, "default", testLogger)
			Expect(ready).To(BeFalse())
			Expect(err).NotTo(BeNil())
		})
		Context("volumesnapshot exists but not ready", func() {
			BeforeEach(func() {
				fakePVCname := "test"
				vs := &vsv1.VolumeSnapshot{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "vs",
						Namespace: "default",
					},
					Spec: vsv1.VolumeSnapshotSpec{
						Source: vsv1.VolumeSnapshotSource{PersistentVolumeClaimName: &fakePVCname},
					},
				}

				Eventually(func() error {
					err := k8sClient.Create(context.TODO(), vs)

					return client.IgnoreAlreadyExists(err)
				}, timeout, interval).Should(BeNil())
			})
			AfterEach(func() {
				Eventually(func() error {
					err := k8sClient.Delete(context.TODO(), &vsv1.VolumeSnapshot{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "vs",
							Namespace: "default",
						},
					})

					return client.IgnoreNotFound(err)
				}, timeout, interval).Should(BeNil())
			})
			It("Should be false", func() {
				ready, err := util.CheckImagesReadyToUse(context.TODO(), k8sClient,
					map[string]*corev1.TypedLocalObjectReference{"vs": {
						Name: "vs",
					}}, "default", testLogger)
				Expect(ready).To(BeFalse())
				Expect(err).To(BeNil())
			})
			Context("volumesnapshot ready", func() {
				BeforeEach(func() {
					vs := &vsv1.VolumeSnapshot{}
					readyToUse := true
					retryErr := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
						err := k8sClient.Get(context.TODO(), types.NamespacedName{
							Name: "vs", Namespace: "default",
						}, vs)
						if err != nil {
							return err
						}

						vs.Status = &vsv1.VolumeSnapshotStatus{
							ReadyToUse: &readyToUse,
						}

						return k8sClient.Status().Update(context.TODO(), vs)
					})
					Expect(retryErr).To(BeNil())
				})
				It("Should be false", func() {
					ready, err := util.CheckImagesReadyToUse(context.TODO(), k8sClient,
						map[string]*corev1.TypedLocalObjectReference{"vs": {
							Name: "vs",
						}}, "default", testLogger)
					Expect(ready).To(BeTrue())
					Expect(err).To(BeNil())
				})
				Describe("DeferDeleteImage with vs exist", func() {
					It("Should be success", func() {
						err := util.DeferDeleteImage(context.Background(), k8sClient, "vs", "default",
							"rgdName", "vrgName", "vrgNamespace")
						Expect(err).To(BeNil())
						Eventually(func() []string {
							vs := &vsv1.VolumeSnapshot{}
							if err := k8sClient.Get(
								context.Background(),
								types.NamespacedName{Name: "vs", Namespace: "default"}, vs,
							); err != nil {
								return nil
							}

							return []string{vs.Labels[ramenutils.DoNotDeleteLabelKey], vs.Labels[util.RGDOwnerLabel]}
						}, timeout, interval).Should(ContainElements("true", "rgdName"))
					})
				})
			})
		})
	})
	Describe("DeferDeleteImage with vs not exist", func() {
		It("Should be success", func() {
			err := util.DeferDeleteImage(context.Background(), k8sClient, "vsnotexist", "default",
				"rgdName", "vrgName", "vrgNamespace")
			Expect(err).NotTo(BeNil())
		})
	})
	Describe("VSInRGD", func() {
		It("Should be success", func() {
			vsInRGD := util.VSInRGD(vsv1.VolumeSnapshot{}, nil)
			Expect(vsInRGD).To(BeFalse())
		})
		It("Should be success", func() {
			vsInRGD := util.VSInRGD(vsv1.VolumeSnapshot{}, &v1alpha1.ReplicationGroupDestination{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
			})
			Expect(vsInRGD).To(BeFalse())
		})
		It("Should be success", func() {
			vsInRGD := util.VSInRGD(vsv1.VolumeSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
			}, &v1alpha1.ReplicationGroupDestination{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
			})
			Expect(vsInRGD).To(BeFalse())
		})
		It("Should be success", func() {
			vsInRGD := util.VSInRGD(vsv1.VolumeSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
			}, &v1alpha1.ReplicationGroupDestination{
				Status: v1alpha1.ReplicationGroupDestinationStatus{
					LatestImages: map[string]*corev1.TypedLocalObjectReference{
						"test": {Name: "test"},
					},
				},
			})
			Expect(vsInRGD).To(BeTrue())
		})
	})

	Describe("CleanExpiredRDImages", func() {
		It("Should be success", func() {
			err := util.CleanExpiredRDImages(context.Background(), k8sClient, &v1alpha1.ReplicationGroupDestination{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
			})
			Expect(err).To(BeNil())
		})
	})
})

const SCName = "test"

func CreateSC(scName, provisioner string, labels map[string]string) {
	sc := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:   scName,
			Labels: labels,
		},
		Provisioner: provisioner,
	}

	Eventually(func() error {
		err := k8sClient.Create(context.TODO(), sc)

		return client.IgnoreAlreadyExists(err)
	}, timeout, interval).Should(BeNil())
}

func DeleteSC(scName string) {
	Eventually(func() error {
		err := k8sClient.Delete(context.TODO(), &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: scName,
			},
		})

		return client.IgnoreNotFound(err)
	}, timeout, interval).Should(BeNil())
}
