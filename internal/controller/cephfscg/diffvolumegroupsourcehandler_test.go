// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package cephfscg_test

import (
	"context"
	"fmt"

	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	vgsv1beta1 "github.com/red-hat-storage/external-snapshotter/client/v8/apis/volumegroupsnapshot/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ramendr/ramen/api/v1alpha1"
	internalController "github.com/ramendr/ramen/internal/controller"
	"github.com/ramendr/ramen/internal/controller/cephfscg"
	"github.com/ramendr/ramen/internal/controller/util"
)

var (
	diffVGSName  = "diff-vgs"
	diffVGSCName = "diff-vgsc"
	diffVGSLabel = map[string]string{"diff-test": "diff-test"}
)

var _ = Describe("DiffVolumeGroupSourceHandler", func() {
	var diffHandler cephfscg.VolumeGroupSourceHandler

	rgs := GenerateReplicationGroupSource(diffVGSName, diffVGSCName, diffVGSLabel)

	BeforeEach(func() {
		diffHandler = cephfscg.NewDiffVolumeGroupSourceHandler(
			k8sClient, rgs, internalController.DefaultCephFSCSIDriverName, nil, testLogger,
		)

		CreatePVC("diff-apppvc")
	})

	Describe("CreateOrUpdateVolumeGroupSnapshot", func() {
		It("Should create a VGS with status=current label and timestamp suffix", func() {
			createdOrUpdated, err := diffHandler.CreateOrUpdateVolumeGroupSnapshot(context.TODO(), rgs)
			Expect(err).To(BeNil())
			Expect(createdOrUpdated).To(BeTrue())

			// Verify a VGS was created with the expected labels
			Eventually(func() bool {
				vgsList := &vgsv1beta1.VolumeGroupSnapshotList{}

				err := k8sClient.List(context.TODO(), vgsList,
					client.InNamespace("default"),
					client.MatchingLabels{
						cephfscg.VGSStatusLabelKey: cephfscg.VGSStatusCurrent,
						util.RGSOwnerLabel:         diffVGSName,
					},
				)
				if err != nil {
					return false
				}

				return len(vgsList.Items) == 1
			}, timeout, interval).Should(BeTrue())
		})

		Context("When a current VGS already exists and is not ready", func() {
			BeforeEach(func() {
				_, err := diffHandler.CreateOrUpdateVolumeGroupSnapshot(context.TODO(), rgs)
				Expect(err).To(BeNil())
			})

			It("Should return true (wait) without creating another VGS", func() {
				createdOrUpdated, err := diffHandler.CreateOrUpdateVolumeGroupSnapshot(context.TODO(), rgs)
				Expect(err).To(BeNil())
				Expect(createdOrUpdated).To(BeTrue())

				// Should still be exactly 1 VGS
				vgsList := &vgsv1beta1.VolumeGroupSnapshotList{}
				Expect(k8sClient.List(context.TODO(), vgsList,
					client.InNamespace("default"),
					client.MatchingLabels{
						cephfscg.VGSStatusLabelKey: cephfscg.VGSStatusCurrent,
						util.RGSOwnerLabel:         diffVGSName,
					},
				)).To(Succeed())
				Expect(vgsList.Items).To(HaveLen(1))
			})
		})

		Context("When current VGS exists and is ready", func() {
			BeforeEach(func() {
				_, err := diffHandler.CreateOrUpdateVolumeGroupSnapshot(context.TODO(), rgs)
				Expect(err).To(BeNil())
				updateDiffVGSReady(diffVGSName)
			})

			It("Should return false (reuse existing)", func() {
				createdOrUpdated, err := diffHandler.CreateOrUpdateVolumeGroupSnapshot(context.TODO(), rgs)
				Expect(err).To(BeNil())
				Expect(createdOrUpdated).To(BeFalse())
			})
		})
	})

	Describe("CleanVolumeGroupSnapshot", func() {
		It("Should succeed with no VGS", func() {
			err := diffHandler.CleanVolumeGroupSnapshot(context.TODO())
			Expect(err).To(BeNil())
		})

		Context("When a current VGS exists", func() {
			BeforeEach(func() {
				// Clean up any previous VGS from prior test contexts
				cleanupAllDiffVGS(diffVGSName)

				_, err := diffHandler.CreateOrUpdateVolumeGroupSnapshot(context.TODO(), rgs)
				Expect(err).To(BeNil())
				updateDiffVGSReady(diffVGSName)
			})

			It("Should transition current VGS to previous status", func() {
				err := diffHandler.CleanVolumeGroupSnapshot(context.TODO())
				Expect(err).To(BeNil())

				// No current VGS should remain
				Eventually(func() int {
					vgsList := &vgsv1beta1.VolumeGroupSnapshotList{}
					Expect(k8sClient.List(context.TODO(), vgsList,
						client.InNamespace("default"),
						client.MatchingLabels{
							cephfscg.VGSStatusLabelKey: cephfscg.VGSStatusCurrent,
							util.RGSOwnerLabel:         diffVGSName,
						},
					)).To(Succeed())

					return len(vgsList.Items)
				}, timeout, interval).Should(Equal(0))

				// At least one previous VGS should exist (the transitioned one)
				vgsList := &vgsv1beta1.VolumeGroupSnapshotList{}
				Expect(k8sClient.List(context.TODO(), vgsList,
					client.InNamespace("default"),
					client.MatchingLabels{
						cephfscg.VGSStatusLabelKey: cephfscg.VGSStatusPrevious,
						util.RGSOwnerLabel:         diffVGSName,
					},
				)).To(Succeed())
				Expect(len(vgsList.Items)).To(BeNumerically(">=", 1))
			})
		})

		Context("When multiple previous VGS exist", func() {
			BeforeEach(func() {
				cleanupAllDiffVGS(diffVGSName)

				// Create 2 previous VGS directly with distinct names
				for _, name := range []string{diffVGSName + "-old", diffVGSName + "-newer"} {
					createPreviousVGS(name, diffVGSName)
				}

				// Create a current VGS and mark ready
				_, err := diffHandler.CreateOrUpdateVolumeGroupSnapshot(context.TODO(), rgs)
				Expect(err).To(BeNil())
				updateDiffVGSReady(diffVGSName)
			})

			It("Should keep only the newest previous VGS and delete older ones", func() {
				err := diffHandler.CleanVolumeGroupSnapshot(context.TODO())
				Expect(err).To(BeNil())

				// After cleanup: cleanOlderPreviousVGS keeps 1 of the 2 previous,
				// then current is transitioned to previous → 2 total previous VGS
				Eventually(func() int {
					vgsList := &vgsv1beta1.VolumeGroupSnapshotList{}
					Expect(k8sClient.List(context.TODO(), vgsList,
						client.InNamespace("default"),
						client.MatchingLabels{
							cephfscg.VGSStatusLabelKey: cephfscg.VGSStatusPrevious,
							util.RGSOwnerLabel:         diffVGSName,
						},
					)).To(Succeed())

					return len(vgsList.Items)
				}, timeout, interval).Should(Equal(2))

				// No current VGS should remain
				vgsList := &vgsv1beta1.VolumeGroupSnapshotList{}
				Expect(k8sClient.List(context.TODO(), vgsList,
					client.InNamespace("default"),
					client.MatchingLabels{
						cephfscg.VGSStatusLabelKey: cephfscg.VGSStatusCurrent,
						util.RGSOwnerLabel:         diffVGSName,
					},
				)).To(Succeed())
				Expect(vgsList.Items).To(BeEmpty())
			})
		})
	})

	Describe("RestoreVolumesFromVolumeGroupSnapshot", func() {
		BeforeEach(func() {
			cleanupAllDiffVGS(diffVGSName)
		})

		It("Should fail when no current VGS exists", func() {
			_, err := diffHandler.RestoreVolumesFromVolumeGroupSnapshot(context.Background(), rgs)
			Expect(err).To(HaveOccurred())
		})

		Context("When current VGS exists but is not ready", func() {
			BeforeEach(func() {
				_, err := diffHandler.CreateOrUpdateVolumeGroupSnapshot(context.TODO(), rgs)
				Expect(err).To(BeNil())
			})

			It("Should fail", func() {
				_, err := diffHandler.RestoreVolumesFromVolumeGroupSnapshot(context.Background(), rgs)
				Expect(err).To(HaveOccurred())
			})
		})

		Context("When current VGS is ready with volume snapshots", func() {
			BeforeEach(func() {
				CreateStorageClass()

				_, err := diffHandler.CreateOrUpdateVolumeGroupSnapshot(context.TODO(), rgs)
				Expect(err).To(BeNil())

				vgsList := &vgsv1beta1.VolumeGroupSnapshotList{}

				Eventually(func() int {
					Expect(k8sClient.List(context.TODO(), vgsList,
						client.InNamespace("default"),
						client.MatchingLabels{
							cephfscg.VGSStatusLabelKey: cephfscg.VGSStatusCurrent,
							util.RGSOwnerLabel:         diffVGSName,
						},
					)).To(Succeed())

					return len(vgsList.Items)
				}, timeout, interval).Should(Equal(1))

				createdVGSName := vgsList.Items[0].Name
				updateDiffVGSReadyByName(createdVGSName)
				createVSWithPVC("diff-vs", createdVGSName, "default", "diff-apppvc")
			})

			It("Should restore volumes successfully", func() {
				restoredPVCs, err := diffHandler.RestoreVolumesFromVolumeGroupSnapshot(context.Background(), rgs)
				Expect(err).To(BeNil())
				Expect(restoredPVCs).To(HaveLen(1))
			})
		})
	})

	Describe("CreateOrUpdateReplicationSourceForRestoredPVCs with External spec", func() {
		It("Should create replication source with External spec for diff sync", func() {
			vrg := &v1alpha1.VolumeReplicationGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "diff-vrg",
					Namespace: "default",
					Annotations: map[string]string{
						util.EnableDiffAnnotation: "true",
					},
				},
				Spec: v1alpha1.VolumeReplicationGroupSpec{
					VolSync: v1alpha1.VolSyncSpec{
						RSSpec: []v1alpha1.VolSyncReplicationSourceSpec{
							{
								ProtectedPVC: v1alpha1.ProtectedPVC{
									Name: "diff-source",
								},
								RsyncTLS: &v1alpha1.RsyncTLSConfig{
									Address: "dummy-address.default.svc",
								},
							},
						},
					},
				},
			}

			// Create the restored PVC that resolveProvisioner will look up
			restoredPVC := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "vs-diff-source",
					Namespace: "default",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					StorageClassName: &scName,
					AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadOnlyMany},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("1Gi"),
						},
					},
				},
			}
			Expect(client.IgnoreAlreadyExists(k8sClient.Create(context.Background(), restoredPVC))).To(BeNil())

			rsList, srcCreatedOrUpdated, err := diffHandler.CreateOrUpdateReplicationSourceForRestoredPVCs(
				context.Background(), "manual",
				[]cephfscg.RestoredPVC{{
					SourcePVCName:      "diff-source",
					RestoredPVCName:    "vs-diff-source",
					VolumeSnapshotName: "diff-snap",
				}},
				rgs, vrg, true)
			Expect(err).To(BeNil())
			Expect(srcCreatedOrUpdated).To(BeTrue())
			Expect(rsList).To(HaveLen(1))
		})
	})
})

func updateDiffVGSReady(ownerName string) {
	vgsList := &vgsv1beta1.VolumeGroupSnapshotList{}

	Eventually(func() int {
		Expect(k8sClient.List(context.TODO(), vgsList,
			client.InNamespace("default"),
			client.MatchingLabels{
				cephfscg.VGSStatusLabelKey: cephfscg.VGSStatusCurrent,
				util.RGSOwnerLabel:         ownerName,
			},
		)).To(Succeed())

		return len(vgsList.Items)
	}, timeout, interval).Should(BeNumerically(">=", 1))

	updateDiffVGSReadyByName(vgsList.Items[0].Name)
}

func updateDiffVGSReadyByName(name string) {
	retryErr := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		vgs := &vgsv1beta1.VolumeGroupSnapshot{}

		err := k8sClient.Get(context.TODO(), types.NamespacedName{
			Name:      name,
			Namespace: "default",
		}, vgs)
		if err != nil {
			return err
		}

		ready := true
		vgs.Status = &vgsv1beta1.VolumeGroupSnapshotStatus{
			ReadyToUse: &ready,
		}

		return k8sClient.Status().Update(context.TODO(), vgs)
	})
	Expect(retryErr).To(BeNil())
}

func cleanupAllDiffVGS(ownerName string) {
	vgsList := &vgsv1beta1.VolumeGroupSnapshotList{}

	Expect(k8sClient.List(context.TODO(), vgsList,
		client.InNamespace("default"),
		client.MatchingLabels{util.RGSOwnerLabel: ownerName},
	)).To(Succeed())

	for i := range vgsList.Items {
		Expect(client.IgnoreNotFound(
			k8sClient.Delete(context.TODO(), &vgsList.Items[i]),
		)).To(Succeed())
	}

	// Wait for all to be gone
	Eventually(func() int {
		list := &vgsv1beta1.VolumeGroupSnapshotList{}

		Expect(k8sClient.List(context.TODO(), list,
			client.InNamespace("default"),
			client.MatchingLabels{util.RGSOwnerLabel: ownerName},
		)).To(Succeed())

		return len(list.Items)
	}, timeout, interval).Should(Equal(0))
}

func createPreviousVGS(name, ownerName string) {
	ready := true
	vgs := &vgsv1beta1.VolumeGroupSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Labels: map[string]string{
				cephfscg.VGSStatusLabelKey: cephfscg.VGSStatusPrevious,
				util.RGSOwnerLabel:         ownerName,
				util.CreatedByRamenLabel:   "true",
			},
		},
		Spec: vgsv1beta1.VolumeGroupSnapshotSpec{
			VolumeGroupSnapshotClassName: &diffVGSCName,
			Source: vgsv1beta1.VolumeGroupSnapshotSource{
				Selector: &metav1.LabelSelector{MatchLabels: diffVGSLabel},
			},
		},
	}

	Expect(k8sClient.Create(context.TODO(), vgs)).To(Succeed())

	retryErr := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := k8sClient.Get(context.TODO(), types.NamespacedName{
			Name: name, Namespace: "default",
		}, vgs); err != nil {
			return err
		}

		vgs.Status = &vgsv1beta1.VolumeGroupSnapshotStatus{ReadyToUse: &ready}

		return k8sClient.Status().Update(context.TODO(), vgs)
	})
	Expect(retryErr).To(BeNil())
}

func createVSWithPVC(name, vgsName, namespace, pvcName string) {
	vgs := &vgsv1beta1.VolumeGroupSnapshot{}
	Expect(k8sClient.Get(context.TODO(), types.NamespacedName{
		Name:      vgsName,
		Namespace: namespace,
	}, vgs)).To(Succeed())

	vs := &snapv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: fmt.Sprintf(
						"%s/%s",
						vgsv1beta1.SchemeGroupVersion.Group,
						vgsv1beta1.SchemeGroupVersion.Version,
					),
					Kind: "VolumeGroupSnapshot",
					Name: vgs.Name,
					UID:  vgs.UID,
				},
			},
		},
		Spec: snapv1.VolumeSnapshotSpec{
			Source: snapv1.VolumeSnapshotSource{PersistentVolumeClaimName: &pvcName},
		},
	}

	Eventually(func() error {
		err := k8sClient.Create(context.TODO(), vs)

		return client.IgnoreAlreadyExists(err)
	}, timeout, interval).Should(BeNil())
}
