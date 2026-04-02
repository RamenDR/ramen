// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"

	volrep "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
)

const (
	dryRunSnapshotLabel = "ramendr.openshift.io/dry-run-snapshot"
	dryRunVRGLabel      = "ramendr.openshift.io/dry-run-vrg"
)

var _ = Describe("VRGDryRunSnapshotManagement", func() {
	var (
		vrg                    *ramendrv1alpha1.VolumeReplicationGroup
		vrgNamespacedName      types.NamespacedName
		namespace              string
		storageClass           *storagev1.StorageClass
		volumeSnapshotClass    *snapv1.VolumeSnapshotClass
		volumeReplicationClass *volrep.VolumeReplicationClass
		pvc1, pvc2             *corev1.PersistentVolumeClaim
		storageClassName       string
		cephfsStorageClass     *storagev1.StorageClass
		cephfsClassName        string
		snapshotClassName      string
		replicationClassName   string
	)

	BeforeEach(func() {
		namespace = "test-dryrun-" + newRandomNamespaceSuffix()
		storageClassName = "test-storage-class-" + newRandomNamespaceSuffix()
		cephfsClassName = "test-cephfs-class-" + newRandomNamespaceSuffix()
		snapshotClassName = "test-snapshot-class-" + newRandomNamespaceSuffix()
		replicationClassName = "test-replication-class-" + newRandomNamespaceSuffix()

		// Create namespace
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		}
		Expect(k8sClient.Create(context.TODO(), ns)).To(Succeed())

		// Create StorageClass for RBD (non-CephFS)
		storageClass = &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: storageClassName,
				Labels: map[string]string{
					"ramendr.openshift.io/storageid": "test-storage-id",
				},
			},
			Provisioner: "rbd.csi.ceph.com",
		}
		Expect(k8sClient.Create(context.TODO(), storageClass)).To(Succeed())

		// Create StorageClass for CephFS
		cephfsStorageClass = &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: cephfsClassName,
				Labels: map[string]string{
					"ramendr.openshift.io/storageid": "test-cephfs-storage-id",
				},
			},
			Provisioner: "cephfs.csi.ceph.com",
		}
		Expect(k8sClient.Create(context.TODO(), cephfsStorageClass)).To(Succeed())

		// Create VolumeSnapshotClass
		volumeSnapshotClass = &snapv1.VolumeSnapshotClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: snapshotClassName,
				Labels: map[string]string{
					"velero.io/csi-volumesnapshot-class": "true",
					"ramendr.openshift.io/storageid":     "test-storage-id",
				},
			},
			Driver:         "rbd.csi.ceph.com",
			DeletionPolicy: snapv1.VolumeSnapshotContentDelete,
		}
		Expect(k8sClient.Create(context.TODO(), volumeSnapshotClass)).To(Succeed())

		// Create VolumeReplicationClass
		volumeReplicationClass = &volrep.VolumeReplicationClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: replicationClassName,
				Labels: map[string]string{
					"ramendr.openshift.io/storageid":     "test-storage-id",
					"ramendr.openshift.io/replicationid": "test-replication-id",
				},
			},
			Spec: volrep.VolumeReplicationClassSpec{
				Provisioner: "rbd.csi.ceph.com",
			},
		}
		Expect(k8sClient.Create(context.TODO(), volumeReplicationClass)).To(Succeed())

		// Create PVCs for RBD
		pvc1 = createTestPVC("test-pvc-1", namespace, storageClassName)
		pvc2 = createTestPVC("test-pvc-2", namespace, storageClassName)

		// Create VRG with Async mode enabled
		vrg = &ramendrv1alpha1.VolumeReplicationGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-vrg-dryrun",
				Namespace: namespace,
			},
			Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
				PVCSelector: metav1.LabelSelector{
					MatchLabels: map[string]string{
						"appname": "testapp",
					},
				},
				ReplicationState: ramendrv1alpha1.Primary,
				S3Profiles:       []string{s3Profiles[0].S3ProfileName},
				Async: &ramendrv1alpha1.VRGAsyncSpec{
					SchedulingInterval: "1h",
					PeerClasses: []ramendrv1alpha1.PeerClass{
						{
							ReplicationID:    "test-replication-id",
							StorageID:        []string{"test-storage-id"},
							StorageClassName: storageClassName,
						},
					},
				},
			},
		}
		vrgNamespacedName = types.NamespacedName{Name: vrg.Name, Namespace: vrg.Namespace}
	})

	AfterEach(func() {
		// Cleanup - errors are ignored as resources may not exist
		if vrg != nil {
			Expect(k8sClient.Delete(context.TODO(), vrg)).To(Or(Succeed(), MatchError(ContainSubstring("not found"))))
		}

		if pvc1 != nil {
			Expect(k8sClient.Delete(context.TODO(), pvc1)).To(Or(Succeed(), MatchError(ContainSubstring("not found"))))
		}

		if pvc2 != nil {
			Expect(k8sClient.Delete(context.TODO(), pvc2)).To(Or(Succeed(), MatchError(ContainSubstring("not found"))))
		}

		if volumeReplicationClass != nil {
			Expect(k8sClient.Delete(context.TODO(), volumeReplicationClass)).To(
				Or(Succeed(), MatchError(ContainSubstring("not found"))))
		}

		if volumeSnapshotClass != nil {
			Expect(k8sClient.Delete(context.TODO(), volumeSnapshotClass)).To(
				Or(Succeed(), MatchError(ContainSubstring("not found"))))
		}

		if storageClass != nil {
			Expect(k8sClient.Delete(context.TODO(), storageClass)).To(
				Or(Succeed(), MatchError(ContainSubstring("not found"))))
		}

		if cephfsStorageClass != nil {
			Expect(k8sClient.Delete(context.TODO(), cephfsStorageClass)).To(
				Or(Succeed(), MatchError(ContainSubstring("not found"))))
		}

		if namespace != "" {
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			Expect(k8sClient.Delete(context.TODO(), ns)).To(
				Or(Succeed(), MatchError(ContainSubstring("not found"))))
		}
	})

	Describe("Snapshot creation during dry-run test failover", func() {
		Context("When VRG is Primary with DryRun=true and Action=Failover", func() {
			It("should create snapshots for RBD PVCs", func() {
				// Set VRG to dry-run mode
				vrg.Spec.DryRun = true
				vrg.Spec.Action = ramendrv1alpha1.VRGActionFailover
				Expect(k8sClient.Create(context.TODO(), vrg)).To(Succeed())

				// Wait for VRG to be processed
				Eventually(func() bool {
					err := apiReader.Get(context.TODO(), vrgNamespacedName, vrg)

					return err == nil && len(vrg.Status.Conditions) > 0
				}, timeout, interval).Should(BeTrue())

				// Check that snapshots are created for RBD PVCs (2 PVCs)
				Eventually(func() int {
					snapshots := &snapv1.VolumeSnapshotList{}

					err := k8sClient.List(context.TODO(), snapshots,
						client.InNamespace(namespace),
						client.MatchingLabels{
							dryRunSnapshotLabel: "true",
							dryRunVRGLabel:      vrg.Name,
						},
					)
					if err != nil {
						return 0
					}

					return len(snapshots.Items)
				}, timeout, interval).Should(Equal(2), "Should create snapshots for 2 RBD PVCs")

				// Verify snapshot labels and properties
				snapshots := &snapv1.VolumeSnapshotList{}
				Expect(k8sClient.List(context.TODO(), snapshots,
					client.InNamespace(namespace),
					client.MatchingLabels{
						dryRunSnapshotLabel: "true",
						dryRunVRGLabel:      vrg.Name,
					},
				)).To(Succeed())

				for _, snapshot := range snapshots.Items {
					// Verify dry-run labels
					Expect(snapshot.Labels).To(HaveKeyWithValue(dryRunSnapshotLabel, "true"))
					Expect(snapshot.Labels).To(HaveKeyWithValue(dryRunVRGLabel, vrg.Name))

					// Verify snapshot source
					Expect(snapshot.Spec.Source.PersistentVolumeClaimName).NotTo(BeNil())

					// Verify it's one of the RBD PVCs
					Expect(*snapshot.Spec.Source.PersistentVolumeClaimName).To(
						Or(Equal(pvc1.Name), Equal(pvc2.Name)),
					)
				}
			})

			It("should not create duplicate snapshots on subsequent reconciliations", func() {
				vrg.Spec.DryRun = true
				vrg.Spec.Action = ramendrv1alpha1.VRGActionFailover
				Expect(k8sClient.Create(context.TODO(), vrg)).To(Succeed())

				// Wait for initial snapshots
				Eventually(func() int {
					snapshots := &snapv1.VolumeSnapshotList{}
					if err := k8sClient.List(context.TODO(), snapshots,
						client.InNamespace(namespace),
						client.MatchingLabels{
							dryRunSnapshotLabel: "true",
							dryRunVRGLabel:      vrg.Name,
						},
					); err != nil {
						return -1
					}

					return len(snapshots.Items)
				}, timeout, interval).Should(Equal(2))

				// Trigger another reconciliation by updating VRG
				Eventually(func() error {
					if err := apiReader.Get(context.TODO(), vrgNamespacedName, vrg); err != nil {
						return err
					}

					vrg.Annotations = map[string]string{"test": "trigger-reconcile"}

					return k8sClient.Update(context.TODO(), vrg)
				}, timeout, interval).Should(Succeed())

				// Verify snapshot count remains the same (no duplicates)
				Consistently(func() int {
					snapshots := &snapv1.VolumeSnapshotList{}
					if err := k8sClient.List(context.TODO(), snapshots,
						client.InNamespace(namespace),
						client.MatchingLabels{
							dryRunSnapshotLabel: "true",
							dryRunVRGLabel:      vrg.Name,
						},
					); err != nil {
						return -1
					}

					return len(snapshots.Items)
				}, "5s", interval).Should(Equal(2), "Should not create duplicate snapshots")
			})
		})

		//nolint:dupl // Test setup intentionally similar but testing different conditions
		Context("When VRG is Primary but DryRun=false", func() {
			It("should not create any snapshots", func() {
				vrg.Spec.DryRun = false
				vrg.Spec.Action = ramendrv1alpha1.VRGActionFailover
				Expect(k8sClient.Create(context.TODO(), vrg)).To(Succeed())

				// Wait for VRG to be processed
				Eventually(func() bool {
					err := apiReader.Get(context.TODO(), vrgNamespacedName, vrg)

					return err == nil && len(vrg.Status.Conditions) > 0
				}, timeout, interval).Should(BeTrue())

				// Verify no snapshots are created
				Consistently(func() int {
					snapshots := &snapv1.VolumeSnapshotList{}
					if err := k8sClient.List(context.TODO(), snapshots,
						client.InNamespace(namespace),
						client.MatchingLabels{
							dryRunSnapshotLabel: "true",
							dryRunVRGLabel:      vrg.Name,
						},
					); err != nil {
						return -1
					}

					return len(snapshots.Items)
				}, "5s", interval).Should(Equal(0), "Should not create snapshots when DryRun=false")
			})
		})

		Context("When VRG is Secondary with DryRun=true", func() {
			It("should not create snapshots", func() {
				vrg.Spec.ReplicationState = ramendrv1alpha1.Secondary
				vrg.Spec.DryRun = true
				vrg.Spec.Action = ramendrv1alpha1.VRGActionFailover
				Expect(k8sClient.Create(context.TODO(), vrg)).To(Succeed())

				// Wait for VRG to be processed
				Eventually(func() bool {
					err := apiReader.Get(context.TODO(), vrgNamespacedName, vrg)

					return err == nil && len(vrg.Status.Conditions) > 0
				}, timeout, interval).Should(BeTrue())

				// Verify no snapshots are created for Secondary VRG
				Consistently(func() int {
					snapshots := &snapv1.VolumeSnapshotList{}
					if err := k8sClient.List(context.TODO(), snapshots,
						client.InNamespace(namespace),
						client.MatchingLabels{
							dryRunSnapshotLabel: "true",
							dryRunVRGLabel:      vrg.Name,
						},
					); err != nil {
						return -1
					}

					return len(snapshots.Items)
				}, "5s", interval).Should(Equal(0), "Should not create snapshots for Secondary VRG")
			})
		})

		//nolint:dupl // Test setup intentionally similar but testing different conditions
		Context("When Action is not Failover", func() {
			It("should not create snapshots even with DryRun=true", func() {
				vrg.Spec.DryRun = true
				vrg.Spec.Action = ramendrv1alpha1.VRGActionRelocate
				Expect(k8sClient.Create(context.TODO(), vrg)).To(Succeed())

				// Wait for VRG to be processed
				Eventually(func() bool {
					err := apiReader.Get(context.TODO(), vrgNamespacedName, vrg)

					return err == nil && len(vrg.Status.Conditions) > 0
				}, timeout, interval).Should(BeTrue())

				// Verify no snapshots are created
				Consistently(func() int {
					snapshots := &snapv1.VolumeSnapshotList{}
					if err := k8sClient.List(context.TODO(), snapshots,
						client.InNamespace(namespace),
						client.MatchingLabels{
							dryRunSnapshotLabel: "true",
							dryRunVRGLabel:      vrg.Name,
						},
					); err != nil {
						return -1
					}

					return len(snapshots.Items)
				}, "5s", interval).Should(Equal(0), "Should not create snapshots for non-Failover action")
			})
		})
	})

	Describe("Snapshot cleanup when aborting or promoting test failover", func() {
		BeforeEach(func() {
			// Create VRG in dry-run mode first
			vrg.Spec.DryRun = true
			vrg.Spec.Action = ramendrv1alpha1.VRGActionFailover
			Expect(k8sClient.Create(context.TODO(), vrg)).To(Succeed())

			// Wait for snapshots to be created
			Eventually(func() int {
				snapshots := &snapv1.VolumeSnapshotList{}
				if err := k8sClient.List(context.TODO(), snapshots,
					client.InNamespace(namespace),
					client.MatchingLabels{
						dryRunSnapshotLabel: "true",
						dryRunVRGLabel:      vrg.Name,
					},
				); err != nil {
					return -1
				}

				return len(snapshots.Items)
			}, timeout, interval).Should(Equal(2))
		})

		Context("When removing DryRun while keeping VRG as Primary (promote to real failover)", func() {
			It("should cleanup dry-run snapshots and VRG remains Primary", func() {
				// Update VRG to remove DryRun but keep Primary state (promote to real failover)
				Eventually(func() error {
					if err := apiReader.Get(context.TODO(), vrgNamespacedName, vrg); err != nil {
						return err
					}

					vrg.Spec.DryRun = false
					// Keep ReplicationState as Primary and Action as Failover

					return k8sClient.Update(context.TODO(), vrg)
				}, timeout, interval).Should(Succeed())

				// Verify snapshots are cleaned up
				Eventually(func() int {
					snapshots := &snapv1.VolumeSnapshotList{}
					if err := k8sClient.List(context.TODO(), snapshots,
						client.InNamespace(namespace),
						client.MatchingLabels{
							dryRunSnapshotLabel: "true",
							dryRunVRGLabel:      vrg.Name,
						},
					); err != nil {
						return -1
					}

					return len(snapshots.Items)
				}, timeout*3, interval).Should(Equal(0), "Snapshots should be cleaned up when promoting to real failover")

				// Verify VRG is still Primary
				Expect(apiReader.Get(context.TODO(), vrgNamespacedName, vrg)).To(Succeed())
				Expect(vrg.Spec.ReplicationState).To(Equal(ramendrv1alpha1.Primary))
				Expect(vrg.Spec.DryRun).To(BeFalse())
			})
		})

		Context("When aborting test failover (transitioning to Secondary with DryRun=false)", func() {
			It("should cleanup all dry-run snapshots", func() {
				// Update VRG to Secondary and remove DryRun (abort test failover)
				Eventually(func() error {
					if err := apiReader.Get(context.TODO(), vrgNamespacedName, vrg); err != nil {
						return err
					}

					vrg.Spec.ReplicationState = ramendrv1alpha1.Secondary
					vrg.Spec.DryRun = false

					return k8sClient.Update(context.TODO(), vrg)
				}, timeout, interval).Should(Succeed())

				// Verify all snapshots are cleaned up
				Eventually(func() int {
					snapshots := &snapv1.VolumeSnapshotList{}
					if err := k8sClient.List(context.TODO(), snapshots,
						client.InNamespace(namespace),
						client.MatchingLabels{
							dryRunSnapshotLabel: "true",
							dryRunVRGLabel:      vrg.Name,
						},
					); err != nil {
						return -1
					}

					return len(snapshots.Items)
				}, timeout*3, interval).Should(Equal(0), "All snapshots should be cleaned up when aborting test failover")

				// Verify VRG is now Secondary
				Expect(apiReader.Get(context.TODO(), vrgNamespacedName, vrg)).To(Succeed())
				Expect(vrg.Spec.ReplicationState).To(Equal(ramendrv1alpha1.Secondary))
				Expect(vrg.Spec.DryRun).To(BeFalse())
			})
		})

		Context("When changing action but keeping DryRun=true", func() {
			It("should NOT cleanup snapshots", func() {
				// Update VRG to different action but keep DryRun
				Eventually(func() error {
					if err := apiReader.Get(context.TODO(), vrgNamespacedName, vrg); err != nil {
						return err
					}

					vrg.Spec.Action = ramendrv1alpha1.VRGActionRelocate

					return k8sClient.Update(context.TODO(), vrg)
				}, timeout, interval).Should(Succeed())

				// Verify snapshots are NOT cleaned up
				Consistently(func() int {
					snapshots := &snapv1.VolumeSnapshotList{}
					Expect(k8sClient.List(context.TODO(), snapshots,
						client.InNamespace(namespace),
						client.MatchingLabels{
							dryRunSnapshotLabel: "true",
							dryRunVRGLabel:      vrg.Name,
						},
					)).To(Succeed())

					return len(snapshots.Items)
				}, "5s", interval).Should(Equal(2), "Snapshots should remain when DryRun is still true")
			})
		})
	})

	Describe("CephFS PVC filtering", func() {
		Context("When all PVCs are CephFS", func() {
			It("should not create any snapshots", func() {
				// Delete non-CephFS PVCs
				Expect(k8sClient.Delete(context.TODO(), pvc1)).To(Succeed())
				Expect(k8sClient.Delete(context.TODO(), pvc2)).To(Succeed())

				// Create PVC for CephFS
				cephfsPVC := createTestPVC("test-cephfs-pvc", namespace, cephfsClassName)

				// Create VRG with only CephFS PVC
				vrg.Spec.DryRun = true
				vrg.Spec.Action = ramendrv1alpha1.VRGActionFailover
				Expect(k8sClient.Create(context.TODO(), vrg)).To(Succeed())

				// Wait for VRG to be processed
				Eventually(func() bool {
					err := apiReader.Get(context.TODO(), vrgNamespacedName, vrg)

					return err == nil && len(vrg.Status.Conditions) > 0
				}, timeout, interval).Should(BeTrue())

				// Verify no snapshots are created for CephFS PVCs
				Consistently(func() int {
					snapshots := &snapv1.VolumeSnapshotList{}
					Expect(k8sClient.List(context.TODO(), snapshots,
						client.InNamespace(namespace),
						client.MatchingLabels{
							dryRunSnapshotLabel: "true",
							dryRunVRGLabel:      vrg.Name,
						},
					)).To(Succeed())

					return len(snapshots.Items)
				}, "5s", interval).Should(Equal(0), "Should not create snapshots for CephFS PVCs")

				// Cleanup
				Expect(k8sClient.Delete(context.TODO(), cephfsPVC)).To(
					Or(Succeed(), MatchError(ContainSubstring("not found"))))
			})
		})
	})

	Describe("Snapshot label selection", func() {
		It("should only select snapshots with both dry-run labels", func() {
			// Create VRG in dry-run mode
			vrg.Spec.DryRun = true
			vrg.Spec.Action = ramendrv1alpha1.VRGActionFailover
			Expect(k8sClient.Create(context.TODO(), vrg)).To(Succeed())

			// Wait for snapshots
			Eventually(func() int {
				snapshots := &snapv1.VolumeSnapshotList{}
				Expect(k8sClient.List(context.TODO(), snapshots,
					client.InNamespace(namespace),
					client.MatchingLabels{
						dryRunSnapshotLabel: "true",
						dryRunVRGLabel:      vrg.Name,
					},
				)).To(Succeed())

				return len(snapshots.Items)
			}, timeout, interval).Should(Equal(2))

			// Create a snapshot without the VRG label (should not be selected)
			manualSnapshot := &snapv1.VolumeSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "manual-snapshot",
					Namespace: namespace,
					Labels: map[string]string{
						dryRunSnapshotLabel: "true",
						// Missing dryRunVRGLabel
					},
				},
				Spec: snapv1.VolumeSnapshotSpec{
					Source: snapv1.VolumeSnapshotSource{
						PersistentVolumeClaimName: &pvc1.Name,
					},
					VolumeSnapshotClassName: &snapshotClassName,
				},
			}
			Expect(k8sClient.Create(context.TODO(), manualSnapshot)).To(Succeed())

			// Verify only VRG-labeled snapshots are selected
			snapshots := &snapv1.VolumeSnapshotList{}
			Expect(k8sClient.List(context.TODO(), snapshots,
				client.InNamespace(namespace),
				client.MatchingLabels{
					dryRunSnapshotLabel: "true",
					dryRunVRGLabel:      vrg.Name,
				},
			)).To(Succeed())
			Expect(len(snapshots.Items)).To(Equal(2), "Should only select snapshots with VRG label")

			// Cleanup manual snapshot
			Expect(k8sClient.Delete(context.TODO(), manualSnapshot)).To(
				Or(Succeed(), MatchError(ContainSubstring("not found"))))
		})
	})
})

// Helper function to create a PVC for testing
func createTestPVC(name, namespace, storageClassName string) *corev1.PersistentVolumeClaim {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"appname": "testapp",
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("1Gi"),
				},
			},
			StorageClassName: &storageClassName,
		},
	}
	Expect(k8sClient.Create(context.TODO(), pvc)).To(Succeed())

	// Set PVC status to Bound (VRG controller only processes bound PVCs)
	pvc.Status.Phase = corev1.ClaimBound
	Expect(k8sClient.Status().Update(context.TODO(), pvc)).To(Succeed())

	return pvc
}
