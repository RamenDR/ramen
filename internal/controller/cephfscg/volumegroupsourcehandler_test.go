// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package cephfscg_test

import (
	"context"
	"fmt"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	vgsv1alphfa1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumegroupsnapshot/v1alpha1"
	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ramendr/ramen/api/v1alpha1"
	internalController "github.com/ramendr/ramen/internal/controller"
	"github.com/ramendr/ramen/internal/controller/cephfscg"
	"github.com/ramendr/ramen/internal/controller/util"
	"github.com/ramendr/ramen/internal/controller/volsync"
)

var (
	vgsName           = "vgs"
	vgscName          = "vgsc"
	vsName            = "vs"
	anotherVSName     = "another-vs"
	vgsLabel          = map[string]string{"test": "test"}
	scName            = "sc"
	appPVCName        = "apppvc"
	anotherAppPVCName = "another-apppvc"
	rsName            = "rs"
	manualString      = "manual"
)

var _ = Describe("Volumegroupsourcehandler", func() {
	var volumeGroupSourceHandler cephfscg.VolumeGroupSourceHandler

	rgs := GenerateReplicationGroupSource(vgsName, vgscName, vgsLabel)

	BeforeEach(func() {
		volumeGroupSourceHandler = cephfscg.NewVolumeGroupSourceHandler(
			k8sClient, rgs, internalController.DefaultCephFSCSIDriverName, testLogger,
		)

		CreatePVC(appPVCName)
		CreatePVC(anotherAppPVCName)
	})
	Describe("CreateOrUpdateVolumeGroupSnapshot", func() {
		It("Should be successful", func() {
			err := volumeGroupSourceHandler.CreateOrUpdateVolumeGroupSnapshot(context.TODO(), rgs)
			Expect(err).To(BeNil())
			Eventually(func() []string {
				volumeGroupSnapshot := &vgsv1alphfa1.VolumeGroupSnapshot{}

				err := k8sClient.Get(
					context.TODO(), types.NamespacedName{
						Name:      fmt.Sprintf(cephfscg.VolumeGroupSnapshotNameFormat, rgs.Name),
						Namespace: rgs.Namespace,
					}, volumeGroupSnapshot)
				if err != nil {
					return nil
				}

				return []string{
					*volumeGroupSnapshot.Spec.VolumeGroupSnapshotClassName,
					volumeGroupSnapshot.Spec.Source.Selector.MatchLabels["test"],
					volumeGroupSnapshot.GetLabels()[util.RGSOwnerLabel],
					volumeGroupSnapshot.GetAnnotations()[volsync.OwnerNameAnnotation],
					volumeGroupSnapshot.GetAnnotations()[volsync.OwnerNamespaceAnnotation],
					volumeGroupSnapshot.GetOwnerReferences()[0].Name,
				}
			}, timeout, interval).Should(Equal([]string{"vgsc", "test", "vgs", "vgs", "default", "vgs"}))
		})
	})
	Describe("CleanVolumeGroupSnapshot", func() {
		It("Should be successful", func() {
			err := volumeGroupSourceHandler.CleanVolumeGroupSnapshot(context.TODO())
			Expect(err).To(BeNil())
		})
		Context("Restored PVC exist", func() {
			BeforeEach(func() {
				err := volumeGroupSourceHandler.CreateOrUpdateVolumeGroupSnapshot(context.TODO(), rgs)
				Expect(err).To(BeNil())
				UpdateVGS(rgs, vsName, appPVCName)

				CreateRestoredPVC(vsName)
			})
			It("Should be successful", func() {
				err := volumeGroupSourceHandler.CleanVolumeGroupSnapshot(context.TODO())
				Expect(err).To(BeNil())

				Eventually(func() bool {
					pvc := &corev1.PersistentVolumeClaim{}
					err := k8sClient.Get(context.Background(), types.NamespacedName{
						Name:      fmt.Sprintf(cephfscg.RestorePVCinCGNameFormat, "test"),
						Namespace: "default",
					}, pvc)
					if err != nil {
						return !errors.IsNotFound(err)
					}

					return pvc.DeletionTimestamp.IsZero()
				}, timeout, interval).Should(BeFalse())
			})
		})
	})

	Describe("RestoreVolumesFromVolumeGroupSnapshot", func() {
		It("Should be failed", func() {
			_, err := volumeGroupSourceHandler.RestoreVolumesFromVolumeGroupSnapshot(context.Background(), rgs)
			Expect(err).NotTo(BeNil())
		})
		Context("There is VolumeGroupSnapshot, but not ready", func() {
			BeforeEach(func() {
				err := volumeGroupSourceHandler.CreateOrUpdateVolumeGroupSnapshot(context.TODO(), rgs)
				Expect(err).To(BeNil())
			})
			It("Should be failed", func() {
				_, err := volumeGroupSourceHandler.RestoreVolumesFromVolumeGroupSnapshot(context.Background(), rgs)
				Expect(err).NotTo(BeNil())
			})
			Context("VolumeGroupSnapshot is ready", func() {
				BeforeEach(func() {
					CreateStorageClass()
					CreateVS(anotherVSName)
					UpdateVGS(rgs, anotherVSName, anotherAppPVCName)
				})
				It("Should be failed", func() {
					restoredPVCs, err := volumeGroupSourceHandler.RestoreVolumesFromVolumeGroupSnapshot(context.Background(), rgs)
					Expect(err).To(BeNil())
					Expect(len(restoredPVCs)).To(Equal(1))
				})
			})
		})
	})

	Describe("CreateOrUpdateReplicationSourceForRestoredPVCs", func() {
		It("Should be successful", func() {
			rsList, err := volumeGroupSourceHandler.CreateOrUpdateReplicationSourceForRestoredPVCs(
				context.Background(), "maunal",
				[]cephfscg.RestoredPVC{{SourcePVCName: "source", RestoredPVCName: "resource", VolumeSnapshotName: "vs"}}, rgs)
			Expect(err).To(BeNil())
			Expect(len(rsList)).To(Equal(1))
		})
	})

	Describe("CheckReplicationSourceForRestoredPVCsCompleted", func() {
		It("Should be successful", func() {
			completed, err := volumeGroupSourceHandler.CheckReplicationSourceForRestoredPVCsCompleted(
				context.Background(), nil)
			Expect(err).To(BeNil())
			Expect(completed).To(BeTrue())
		})
		It("Should be fail", func() {
			_, err := volumeGroupSourceHandler.CheckReplicationSourceForRestoredPVCsCompleted(
				context.Background(), []*corev1.ObjectReference{{Name: "notexist", Namespace: "notexist"}})
			Expect(err).NotTo(BeNil())
		})

		Context("rs exist but not completed", func() {
			BeforeEach(func() {
				CreateRS(rsName)
			})
			It("Should be successful", func() {
				Eventually(func() (bool, error) {
					return volumeGroupSourceHandler.CheckReplicationSourceForRestoredPVCsCompleted(
						context.Background(), []*corev1.ObjectReference{{Name: rsName, Namespace: "default"}})
				}).Should(BeElementOf(false, nil))
			})
			Context("rs exist and it's completed", func() {
				BeforeEach(func() {
					UpdateRS(rsName)
				})
				It("Should be successful", func() {
					Eventually(func() (bool, error) {
						return volumeGroupSourceHandler.CheckReplicationSourceForRestoredPVCsCompleted(
							context.Background(), []*corev1.ObjectReference{{Name: rsName, Namespace: "default"}})
					}).Should(BeElementOf(true, nil))
				})
			})
		})
	})
})

func CreateRS(rsName string) {
	rs := &volsyncv1alpha1.ReplicationSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rsName,
			Namespace: "default",
		},
		Spec: volsyncv1alpha1.ReplicationSourceSpec{
			Trigger: &volsyncv1alpha1.ReplicationSourceTriggerSpec{
				Manual: manualString,
			},
		},
	}

	Eventually(func() error {
		err := k8sClient.Create(context.TODO(), rs)

		return client.IgnoreAlreadyExists(err)
	}, timeout, interval).Should(BeNil())
}

func UpdateRS(rsName string) {
	retryErr := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		rs := &volsyncv1alpha1.ReplicationSource{}

		err := k8sClient.Get(context.TODO(), types.NamespacedName{
			Name:      rsName,
			Namespace: "default",
		}, rs)
		if err != nil {
			return err
		}

		rs.Status = &volsyncv1alpha1.ReplicationSourceStatus{
			LastManualSync: manualString,
		}

		return k8sClient.Status().Update(context.TODO(), rs)
	})
	Expect(retryErr).To(BeNil())
}

func GenerateReplicationGroupSource(
	vgsName, vgscName string, vgsLabel map[string]string,
) *v1alpha1.ReplicationGroupSource {
	return &v1alpha1.ReplicationGroupSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      vgsName,
			Namespace: "default",
			UID:       types.UID("123"),
			Labels:    map[string]string{volsync.VRGOwnerNameLabel: vrgName},
		},
		Spec: v1alpha1.ReplicationGroupSourceSpec{
			VolumeGroupSnapshotClassName: vgscName,
			VolumeGroupSnapshotSource: &metav1.LabelSelector{
				MatchLabels: vgsLabel,
			},
		},
	}
}

func UpdateVGS(rgs *v1alpha1.ReplicationGroupSource, vsName, pvcName string) {
	retryErr := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		volumeGroupSnapshot := &vgsv1alphfa1.VolumeGroupSnapshot{}

		err := k8sClient.Get(context.TODO(), types.NamespacedName{
			Name:      fmt.Sprintf(cephfscg.VolumeGroupSnapshotNameFormat, rgs.Name),
			Namespace: rgs.Namespace,
		}, volumeGroupSnapshot)
		if err != nil {
			return err
		}

		ready := true
		volumeGroupSnapshot.Status = &vgsv1alphfa1.VolumeGroupSnapshotStatus{
			ReadyToUse: &ready,
			PVCVolumeSnapshotRefList: []vgsv1alphfa1.PVCVolumeSnapshotPair{{
				VolumeSnapshotRef:        corev1.LocalObjectReference{Name: vsName},
				PersistentVolumeClaimRef: corev1.LocalObjectReference{Name: pvcName},
			}},
		}

		return k8sClient.Status().Update(context.TODO(), volumeGroupSnapshot)
	})
	Expect(retryErr).To(BeNil())
}

func CreateRestoredPVC(pvcName string) {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf(cephfscg.RestorePVCinCGNameFormat, pvcName),
			Namespace: "default",
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
}

func CreatePVC(pvcName string) {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: "default",
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
}

func CreateStorageClass() {
	readOnlyPVCStorageClass := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: scName,
		},
		Provisioner: internalController.DefaultCephFSCSIDriverName,
	}

	Eventually(func() error {
		err := k8sClient.Create(context.TODO(), readOnlyPVCStorageClass)

		return client.IgnoreAlreadyExists(err)
	}, timeout, interval).Should(BeNil())
}

func CreateVS(name string) {
	vs := &snapv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: snapv1.VolumeSnapshotSpec{
			Source: snapv1.VolumeSnapshotSource{PersistentVolumeClaimName: &appPVCName},
		},
	}

	Eventually(func() error {
		err := k8sClient.Create(context.TODO(), vs)

		return client.IgnoreAlreadyExists(err)
	}, timeout, interval).Should(BeNil())
}

func UpdateVS(name string) {
	Eventually(func() error {
		vs := &snapv1.VolumeSnapshot{}
		err := k8sClient.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: "default"}, vs)
		if err != nil {
			return err
		}

		ReadyToUse := true
		vs.Status = &snapv1.VolumeSnapshotStatus{
			ReadyToUse: &ReadyToUse,
		}

		err = k8sClient.Status().Update(context.TODO(), vs)
		if err != nil {
			return err
		}

		return nil
	}, timeout, interval).Should(BeNil())
}
