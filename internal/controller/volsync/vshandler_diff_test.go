// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package volsync_test

import (
	"fmt"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/util"
	"github.com/ramendr/ramen/internal/controller/volsync"
)

var _ = Describe("VolSync Handler - Diff sync rollback", func() {
	var (
		testNamespace *corev1.Namespace
		owner         client.Object
		vsHandler     *volsync.VSHandler
	)

	asyncSpec := &ramendrv1alpha1.VRGAsyncSpec{
		SchedulingInterval:          "5m",
		VolumeSnapshotClassSelector: metav1.LabelSelector{},
	}

	capacity := resource.MustParse("2Gi")

	BeforeEach(func() {
		testNamespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "vh-diff-",
			},
		}
		Expect(k8sClient.Create(ctx, testNamespace)).To(Succeed())
		Expect(testNamespace.GetName()).NotTo(BeEmpty())

		// Create owner with diff sync annotation
		ownerCm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "dummycm-diff-owner-",
				Namespace:    testNamespace.GetName(),
				Annotations: map[string]string{
					util.EnableDiffAnnotation: "true",
				},
			},
		}
		Expect(k8sClient.Create(ctx, ownerCm)).To(Succeed())
		Expect(ownerCm.GetName()).NotTo(BeEmpty())
		owner = ownerCm

		vsHandler = volsync.NewVSHandler(ctx, k8sClient, logger, owner, asyncSpec,
			testCephFSStorageDriverName, "Direct", false)
	})

	AfterEach(func() {
		Expect(k8sClient.Delete(ctx, testNamespace)).To(Succeed())
	})

	Describe("createCurrentStateSnapshot", func() {
		It("Should create a VolumeSnapshot of the app PVC's current state", func() {
			pvcName := "test-app-pvc"

			// Create a dummy PVC first
			createDummyPVC(pvcName, testNamespace.GetName(), capacity, nil)

			rdSpec := ramendrv1alpha1.VolSyncReplicationDestinationSpec{
				ProtectedPVC: ramendrv1alpha1.ProtectedPVC{
					Name:             pvcName,
					Namespace:        testNamespace.GetName(),
					StorageClassName: &testCephFSStorageClassName,
				},
			}

			snapName, err := vsHandler.CreateCurrentStateSnapshot(rdSpec)
			Expect(err).NotTo(HaveOccurred())
			Expect(snapName).To(Equal(fmt.Sprintf("current-state-%s", pvcName)))

			// Verify the snapshot was created
			snap := &snapv1.VolumeSnapshot{}

			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name: snapName, Namespace: testNamespace.GetName(),
				}, snap)
			}, maxWait, interval).Should(Succeed())

			Expect(snap.Spec.Source.PersistentVolumeClaimName).NotTo(BeNil())
			Expect(*snap.Spec.Source.PersistentVolumeClaimName).To(Equal(pvcName))
			Expect(snap.Spec.VolumeSnapshotClassName).NotTo(BeNil())
			Expect(*snap.Spec.VolumeSnapshotClassName).To(Equal(testCephFSVolumeSnapshotClassName))
			Expect(snap.Labels[util.CreatedByRamenLabel]).To(Equal("true"))
		})
	})

	Describe("isSnapshotReady", func() {
		It("Should return false when snapshot is not ready", func() {
			snap := createSnapshot("test-snap-not-ready", testNamespace.GetName())
			// Snapshot created without status — should not be ready
			ready, err := vsHandler.IsSnapshotReady(snap.Name, snap.Namespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(ready).To(BeFalse())
		})

		It("Should return true when snapshot has ReadyToUse=true", func() {
			snap := createSnapshot("test-snap-ready", testNamespace.GetName())

			// Update status to ready
			snap.Status = &snapv1.VolumeSnapshotStatus{
				ReadyToUse: ptr.To(true),
			}
			Expect(k8sClient.Status().Update(ctx, snap)).To(Succeed())

			Eventually(func() bool {
				ready, err := vsHandler.IsSnapshotReady(snap.Name, snap.Namespace)
				if err != nil {
					return false
				}

				return ready
			}, maxWait, interval).Should(BeTrue())
		})
	})

	Describe("reconcileDiffLocalRD", func() {
		It("Should create a local RD with External spec", func() {
			pvcName := "test-diff-rd-pvc"

			// Create the app PVC
			createDummyPVC(pvcName, testNamespace.GetName(), capacity, nil)

			// Create PSK secret
			pskSecretName := volsync.GetVolSyncPSKSecretNameFromVRGName(owner.GetName())
			Expect(k8sClient.Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: pskSecretName, Namespace: testNamespace.GetName()},
			})).To(Succeed())

			rdSpec := ramendrv1alpha1.VolSyncReplicationDestinationSpec{
				ProtectedPVC: ramendrv1alpha1.ProtectedPVC{
					Name:               pvcName,
					Namespace:          testNamespace.GetName(),
					ProtectedByVolSync: true,
					StorageClassName:   &testCephFSStorageClassName,
					AccessModes:        []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: capacity,
						},
					},
				},
			}

			lrd, err := vsHandler.ReconcileDiffLocalRD(rdSpec, pskSecretName)
			// RD is created but address may not be populated yet — expect waiting error
			if err != nil {
				Expect(err.Error()).To(ContainSubstring("waiting for address"))
			}

			// Verify the RD was created with External spec
			rd := &volsyncv1alpha1.ReplicationDestination{}

			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      util.GetLocalReplicationName(pvcName),
					Namespace: testNamespace.GetName(),
				}, rd)
			}, maxWait, interval).Should(Succeed())

			Expect(rd.Spec.External).NotTo(BeNil())
			Expect(rd.Spec.RsyncTLS).To(BeNil())
			Expect(rd.Spec.External.Provider).To(Equal(testCephFSStorageDriverName))
			Expect(rd.Spec.External.Parameters).To(HaveKeyWithValue("copyMethod", "Direct"))
			Expect(rd.Spec.External.Parameters).To(HaveKeyWithValue("destinationPVC", pvcName))
			Expect(rd.Spec.External.Parameters).To(HaveKeyWithValue("keySecret", pskSecretName))
			Expect(rd.Spec.External.Parameters).To(HaveKeyWithValue("storageClassName", testCephFSStorageClassName))
			Expect(rd.Spec.External.Parameters).To(
				HaveKeyWithValue("volumeSnapshotClassName", testCephFSVolumeSnapshotClassName))

			// Verify labels
			Expect(rd.Labels[volsync.VolSyncDoNotDeleteLabel]).To(Equal(volsync.VolSyncDoNotDeleteLabelVal))
			Expect(rd.Labels[util.CreatedByRamenLabel]).To(Equal("true"))

			_ = lrd // may be nil if address not populated
		})
	})

	Describe("reconcileDiffLocalRS", func() {
		It("Should create a local RS with External spec and diff params", func() {
			pvcName := "test-diff-rs-pvc"

			// Create the app PVC
			createDummyPVC(pvcName, testNamespace.GetName(), capacity, nil)

			// Create PSK secret
			pskSecretName := volsync.GetVolSyncPSKSecretNameFromVRGName(owner.GetName())
			Expect(k8sClient.Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: pskSecretName, Namespace: testNamespace.GetName()},
			})).To(Succeed())

			// Create a fake LatestImage snapshot
			latestSnap := createSnapshot("latest-image-snap", testNamespace.GetName())

			// Create a dummy RD that the local RS references
			rd := &volsyncv1alpha1.ReplicationDestination{
				ObjectMeta: metav1.ObjectMeta{
					Name:      util.GetReplicationDestinationName(pvcName),
					Namespace: testNamespace.GetName(),
				},
			}
			Expect(k8sClient.Create(ctx, rd)).To(Succeed())

			// Update RD status with LatestImage
			rd.Status = &volsyncv1alpha1.ReplicationDestinationStatus{
				RsyncTLS: &volsyncv1alpha1.ReplicationDestinationRsyncTLSStatus{
					Address: ptr.To("10.0.0.1"),
				},
				LatestImage: &corev1.TypedLocalObjectReference{
					Kind:     "VolumeSnapshot",
					Name:     latestSnap.Name,
					APIGroup: ptr.To(snapv1.GroupName),
				},
			}
			Expect(k8sClient.Status().Update(ctx, rd)).To(Succeed())

			// Update snapshot status to ready
			latestSnap.Status = &snapv1.VolumeSnapshotStatus{
				ReadyToUse:  ptr.To(true),
				RestoreSize: &capacity,
			}
			Expect(k8sClient.Status().Update(ctx, latestSnap)).To(Succeed())

			rdSpec := &ramendrv1alpha1.VolSyncReplicationDestinationSpec{
				ProtectedPVC: ramendrv1alpha1.ProtectedPVC{
					Name:               pvcName,
					Namespace:          testNamespace.GetName(),
					ProtectedByVolSync: true,
					StorageClassName:   &testCephFSStorageClassName,
					AccessModes:        []corev1.PersistentVolumeAccessMode{corev1.ReadOnlyMany},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: capacity,
						},
					},
				},
			}

			snapshotRef := &corev1.TypedLocalObjectReference{
				Kind:     "VolumeSnapshot",
				Name:     latestSnap.Name,
				APIGroup: ptr.To(snapv1.GroupName),
			}

			currentStateSnapName := "current-state-" + pvcName
			address := "10.0.0.1"

			// Use retry to handle potential conflicts when updating snapshot labels
			// The conflict can occur in validateAndProtectSnapshot when it tries to update
			// the snapshot with labels. Each retry will fetch a fresh snapshot object.
			var lrs *volsyncv1alpha1.ReplicationSource

			err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
				var retryErr error

				lrs, retryErr = vsHandler.ReconcileDiffLocalRS(rd, rdSpec, snapshotRef,
					currentStateSnapName, pskSecretName, address)

				return retryErr
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(lrs).NotTo(BeNil())

			// Verify RS has External spec
			rs := &volsyncv1alpha1.ReplicationSource{}

			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      util.GetLocalReplicationName(pvcName),
					Namespace: testNamespace.GetName(),
				}, rs)
			}, maxWait, interval).Should(Succeed())

			Expect(rs.Spec.External).NotTo(BeNil())
			Expect(rs.Spec.RsyncTLS).To(BeNil())
			Expect(rs.Spec.External.Provider).To(Equal(testCephFSStorageDriverName))
			Expect(rs.Spec.External.Parameters).To(HaveKeyWithValue("copyMethod", "Direct"))
			Expect(rs.Spec.External.Parameters).To(HaveKeyWithValue("volumeName", pvcName))
			Expect(rs.Spec.External.Parameters).To(HaveKeyWithValue("baseSnapshotName", currentStateSnapName))
			Expect(rs.Spec.External.Parameters).To(HaveKeyWithValue("targetSnapshotName", latestSnap.Name))
			Expect(rs.Spec.External.Parameters).To(HaveKeyWithValue("address", address))
			Expect(rs.Spec.External.Parameters).To(HaveKeyWithValue("keySecret", pskSecretName))
		})
	})
})
