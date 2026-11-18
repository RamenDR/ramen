// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"
	"fmt"

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

// dryRunSnapshotLabelKey is the label key the production code stamps on every
// VolumeSnapshot it creates during a dryRun (defined in vrg_volrep.go as dryRunSnapshotLabel).
// dryRunVRGLabelKey tags each snapshot with the owning VRG name for scoped cleanup.
const (
	dryRunSnapshotLabelKey = "ramendr.openshift.io/dry-run-snapshot"
	dryRunVRGLabelKey      = "ramendr.openshift.io/dry-run-vrg"
)

// VRG DryRun VolumeSnapshot tests.
//
// These tests create a real VolumeReplicationGroup object so the VRG reconciler
// (registered in suite_test.go) runs against it.  They verify the snapshot
// behavior that is exercised inside the VRG reconciler — specifically
// ensureSnapshotsForDryRun() in vrg_volrep.go — which is unreachable from the
// DRPC-level tests because DRPC stores VRG specs only inside ManifestWork objects.
var _ = Describe("VolumeReplicationGroup DryRun snapshots", func() {
	Context("VRG DryRun - RBD PVC gets a VolumeSnapshot when Primary+DryRun+Failover", func() {
		// The VRG reconciler calls ensureSnapshotsForDryRun() inside
		// reconcileVolRepsAsPrimary() whenever shouldTakeDryRunSnapshots() is true:
		//   Spec.ReplicationState == Primary && Spec.DryRun == true && Spec.Action == Failover
		//
		// It calls createSnapshotForPVC() → createSnapshot() which creates a
		// VolumeSnapshot labeled dryRunSnapshotLabel="true".
		//
		// Setup objects created here (all unique-suffixed to avoid collisions):
		//   - Namespace
		//   - StorageClass   (provisioner = testRBDProvisioner)
		//   - VolumeSnapshotClass (driver = testRBDProvisioner, no label selector needed)
		//   - PersistentVolume + PersistentVolumeClaim (Bound, storageClass=above)
		//   - VolumeReplicationGroup (Primary, DryRun=true, Action=Failover)
		var (
			testNS            *corev1.Namespace
			testSC            *storagev1.StorageClass
			testVSC           *snapv1.VolumeSnapshotClass
			testPV            *corev1.PersistentVolume
			testPVC           *corev1.PersistentVolumeClaim
			testVRC           *volrep.VolumeReplicationClass
			testVRG           *ramendrv1alpha1.VolumeReplicationGroup
			rbdProvisioner    string
			vrgNamespacedName types.NamespacedName
		)

		BeforeEach(func() {
			// Unique suffix per spec to avoid cross-test collisions.
			suffix := newRandomNamespaceSuffix()
			rbdProvisioner = fmt.Sprintf("rbd.csi.ceph.com/%s", suffix)

			// Namespace
			testNS = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("dryrun-snap-ns-%s", suffix)},
			}
			Expect(k8sClient.Create(context.TODO(), testNS)).To(Succeed())

			// StorageClass — provisioner matches the VolumeSnapshotClass driver and VRC below.
			testSC = &storagev1.StorageClass{
				ObjectMeta:  metav1.ObjectMeta{Name: fmt.Sprintf("rbd-sc-%s", suffix)},
				Provisioner: rbdProvisioner,
			}
			Expect(k8sClient.Create(context.TODO(), testSC)).To(Succeed())

			// VolumeReplicationClass — provisioner must match StorageClass.Provisioner so the
			// VRG reconciler classifies the PVC as a VolRep PVC (not VolSync).
			// Without this the PVC goes into volSyncPVCs and reconcileVolRepsAsPrimary() is never called.
			testVRC = &volrep.VolumeReplicationClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf("rbd-vrc-%s", suffix),
					Annotations: map[string]string{
						"replication.storage.openshift.io/is-default-class": "true",
					},
				},
				Spec: volrep.VolumeReplicationClassSpec{
					Provisioner: rbdProvisioner,
					Parameters:  map[string]string{"schedulingInterval": "1h"},
				},
			}
			Expect(k8sClient.Create(context.TODO(), testVRC)).To(Succeed())

			// VolumeSnapshotClass — driver == StorageClass.Provisioner so
			// GetVolumeSnapshotClassFromPVCStorageClass() finds a match.
			// An empty VolumeSnapshotClassSelector on the VRG Async spec means
			// the VSHandler lists ALL VolumeSnapshotClasses and picks by driver.
			testVSC = &snapv1.VolumeSnapshotClass{
				ObjectMeta:     metav1.ObjectMeta{Name: fmt.Sprintf("rbd-vsc-%s", suffix)},
				Driver:         rbdProvisioner,
				DeletionPolicy: snapv1.VolumeSnapshotContentDelete,
			}
			Expect(k8sClient.Create(context.TODO(), testVSC)).To(Succeed())

			// PVC bound to the RBD StorageClass.
			pvName := fmt.Sprintf("rbd-pv-%s", suffix)
			pvcName := fmt.Sprintf("rbd-pvc-%s", suffix)
			storageClassName := testSC.Name

			testPV = &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: pvName},
				Spec: corev1.PersistentVolumeSpec{
					Capacity:                      corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
					AccessModes:                   []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimRetain,
					StorageClassName:              storageClassName,
					PersistentVolumeSource: corev1.PersistentVolumeSource{
						HostPath: &corev1.HostPathVolumeSource{Path: "/tmp/rbd-dryrun"},
					},
					ClaimRef: &corev1.ObjectReference{
						Namespace: testNS.Name,
						Name:      pvcName,
					},
				},
			}
			Expect(k8sClient.Create(context.TODO(), testPV)).To(Succeed())
			testPV.Status.Phase = corev1.VolumeBound
			Expect(k8sClient.Status().Update(context.TODO(), testPV)).To(Succeed())

			testPVC = &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pvcName,
					Namespace: testNS.Name,
					Labels:    map[string]string{"dryrun-test": suffix},
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("1Gi"),
						},
					},
					VolumeName:       pvName,
					StorageClassName: &storageClassName,
				},
			}
			Expect(k8sClient.Create(context.TODO(), testPVC)).To(Succeed())
			testPVC.Status.Phase = corev1.ClaimBound
			testPVC.Status.AccessModes = testPVC.Spec.AccessModes
			testPVC.Status.Capacity = testPVC.Spec.Resources.Requests
			Expect(k8sClient.Status().Update(context.TODO(), testPVC)).To(Succeed())

			// VRG — Primary + DryRun=true + Action=Failover.
			// PVCSelector matches the label on testPVC.
			// Empty VolumeSnapshotClassSelector: VSHandler lists all VolumeSnapshotClasses.
			// S3Profiles is required by the webhook — use the shared test profile.
			vrgName := fmt.Sprintf("vrg-dryrun-%s", suffix)
			testVRG = &ramendrv1alpha1.VolumeReplicationGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      vrgName,
					Namespace: testNS.Name,
				},
				Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
					PVCSelector:      metav1.LabelSelector{MatchLabels: map[string]string{"dryrun-test": suffix}},
					ReplicationState: ramendrv1alpha1.Primary,
					DryRun:           true,
					Action:           ramendrv1alpha1.VRGActionFailover,
					Async: &ramendrv1alpha1.VRGAsyncSpec{
						SchedulingInterval:          "1h",
						VolumeSnapshotClassSelector: metav1.LabelSelector{},
					},
					VolSync:    ramendrv1alpha1.VolSyncSpec{Disabled: true},
					S3Profiles: []string{s3Profiles[vrgS3ProfileNumber].S3ProfileName},
				},
			}
			Expect(k8sClient.Create(context.TODO(), testVRG)).To(Succeed())

			vrgNamespacedName = types.NamespacedName{Name: vrgName, Namespace: testNS.Name}
		})

		AfterEach(func() {
			// Clean up in reverse order; ignore NotFound.
			// Cluster-scoped resources (VRC, PV) must be deleted explicitly — they are not
			// namespaced and are not cleaned up when the namespace is removed.
			Expect(client.IgnoreNotFound(k8sClient.Delete(context.TODO(), testVRG))).To(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(context.TODO(), testPVC))).To(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(context.TODO(), testPV))).To(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(context.TODO(), testVSC))).To(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(context.TODO(), testVRC))).To(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(context.TODO(), testSC))).To(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(context.TODO(), testNS))).To(Succeed())
		})

		It("should create a VolumeSnapshot labeled dry-run-snapshot=true for the RBD PVC", func() {
			By("waiting for the VRG reconciler to create a VolumeReplication for the PVC")
			// The VRG reconciler creates a VolumeReplication (VR) for each VolRep PVC.
			// In envtest there is no real CSI driver, so we must manually simulate its status
			// to make the VRG transition Status.State to PrimaryState, which is required by
			// the shouldTakeDryRunSnapshots() guard at vrg_volrep.go:93.
			vrKey := types.NamespacedName{Name: testPVC.Name, Namespace: testNS.Name}
			vr := &volrep.VolumeReplication{}

			Eventually(func() error {
				return k8sClient.Get(context.TODO(), vrKey, vr)
			}, timeout, interval).Should(Succeed(),
				"VRG reconciler must create a VolumeReplication for the RBD PVC")

			By("simulating CSI driver: patch VolumeReplication status to Primary+Ready")
			// promoteVolRepsAndDo pattern from vrg_volrep_test.go:
			// Patch VR.Status with Validated=True, Completed=True, State=Primary.
			// This causes the VRG reconciler to set DataReady=True → Status.State=PrimaryState.
			now := metav1.Now()
			vr.Status = volrep.VolumeReplicationStatus{
				ObservedGeneration: vr.Generation,
				State:              volrep.PrimaryState,
				Message:            "volume is marked primary",
				Conditions: []metav1.Condition{
					{
						Type:               volrep.ConditionValidated,
						Status:             metav1.ConditionTrue,
						Reason:             volrep.PrerequisiteMet,
						ObservedGeneration: vr.Generation,
						LastTransitionTime: now,
						Message:            "volume is validated",
					},
					{
						Type:               volrep.ConditionCompleted,
						Status:             metav1.ConditionTrue,
						Reason:             volrep.Promoted,
						ObservedGeneration: vr.Generation,
						LastTransitionTime: now,
						Message:            "volume is marked primary",
					},
					{
						Type:               volrep.ConditionDegraded,
						Status:             metav1.ConditionFalse,
						Reason:             volrep.Healthy,
						ObservedGeneration: vr.Generation,
						LastTransitionTime: now,
						Message:            "volume is healthy",
					},
					{
						Type:               volrep.ConditionResyncing,
						Status:             metav1.ConditionFalse,
						Reason:             volrep.NotResyncing,
						ObservedGeneration: vr.Generation,
						LastTransitionTime: now,
						Message:            "volume is not resyncing",
					},
				},
			}
			Expect(k8sClient.Status().Update(context.TODO(), vr)).To(Succeed())

			By("waiting for VRG Status.State to reach PrimaryState")
			// updateStatusState() at vrg_controller.go:1972 sets State=PrimaryState only when
			// DataReady condition is True. DataReady becomes True after all VRs report Completed.
			Eventually(func() bool {
				current := &ramendrv1alpha1.VolumeReplicationGroup{}
				if err := apiReader.Get(context.TODO(), vrgNamespacedName, current); err != nil {
					return false
				}

				return current.Status.State == ramendrv1alpha1.PrimaryState
			}, timeout, interval).Should(BeTrue(),
				"VRG must reach Status.State=PrimaryState after VolumeReplication is promoted")

			By("waiting for the VRG reconciler to create a VolumeSnapshot in the PVC namespace")
			// reconcileVolRepsAsPrimary() calls ensureSnapshotsForDryRun() once Status.State==Primary.
			// createSnapshot() creates a VolumeSnapshot named "<pvcName>-snapshot" labeled
			// dryRunSnapshotLabel="true" and dryRunVRGLabel=vrgName.
			Eventually(func() bool {
				snapList := &snapv1.VolumeSnapshotList{}
				if err := k8sClient.List(context.TODO(), snapList,
					client.InNamespace(testNS.Name),
					client.MatchingLabels{dryRunSnapshotLabelKey: "true"},
				); err != nil {
					return false
				}

				return len(snapList.Items) == 1 &&
					*snapList.Items[0].Spec.Source.PersistentVolumeClaimName == testPVC.Name
			}, timeout, interval).Should(BeTrue(),
				"VRG reconciler must create a VolumeSnapshot labeled dry-run-snapshot=true "+
					"for the RBD PVC when Primary+DryRun+Failover")

			By("verifying the snapshot names the correct VolumeSnapshotClass")
			// createSnapshotForPVC() calls GetVolumeSnapshotClassFromPVCStorageClass which
			// finds testVSC because its Driver == StorageClass.Provisioner.
			snapList := &snapv1.VolumeSnapshotList{}
			Expect(k8sClient.List(context.TODO(), snapList,
				client.InNamespace(testNS.Name),
				client.MatchingLabels{dryRunSnapshotLabelKey: "true"},
			)).To(Succeed())
			Expect(snapList.Items).To(HaveLen(1))
			Expect(*snapList.Items[0].Spec.VolumeSnapshotClassName).To(Equal(testVSC.Name),
				"VolumeSnapshot must reference the RBD VolumeSnapshotClass matching the PVC's provisioner")

			By("verifying the snapshot carries the VRG name label")
			// createSnapshot() stamps dryRunVRGLabel=vrgName so cleanup can scope deletions to one VRG.
			Expect(snapList.Items[0].Labels[dryRunVRGLabelKey]).To(Equal(testVRG.Name),
				"VolumeSnapshot must carry dryRunVRGLabel=vrgName for scoped cleanup")
		})
	})
})

// VRG DryRun CephFS tests.
//
// CephFS PVCs are protected via VolSync (not VolRep) because there is no
// VolumeReplicationClass for the CephFS provisioner.  The VRG reconciler
// therefore never calls reconcileVolRepsAsPrimary() for CephFS PVCs, so
// ensureSnapshotsForDryRun() is never invoked.  Zero VolumeSnapshots must
// be created even when DryRun=true + Primary + Failover.
var _ = Describe("VolumeReplicationGroup DryRun snapshots - CephFS", func() {
	Context("VRG DryRun - CephFS PVC must NOT get a VolumeSnapshot (no VolRep class)", func() {
		// CephFS uses a provisioner for which no VolumeReplicationClass exists.
		// The VRG reconciler routes CephFS PVCs to VolSync, bypassing
		// reconcileVolRepsAsPrimary() entirely, so ensureSnapshotsForDryRun()
		// is never reached and zero snapshots are created.
		//
		// Setup objects created here (all unique-suffixed to avoid collisions):
		//   - Namespace
		//   - StorageClass   (provisioner = cephfs.csi.ceph.com/<suffix>)
		//   - VolumeSnapshotClass (driver = cephfs.csi.ceph.com/<suffix>)
		//   - PersistentVolume + PersistentVolumeClaim (Bound, storageClass=above)
		//   - VolumeReplicationGroup (Primary, DryRun=true, Action=Failover)
		//   NOTE: NO VolumeReplicationClass is created — this is what keeps the
		//         PVC on the VolSync path and out of reconcileVolRepsAsPrimary().
		var (
			testNS            *corev1.Namespace
			testSC            *storagev1.StorageClass
			testVSC           *snapv1.VolumeSnapshotClass
			testPV            *corev1.PersistentVolume
			testPVC           *corev1.PersistentVolumeClaim
			testVRG           *ramendrv1alpha1.VolumeReplicationGroup
			cephFSProvisioner string
			vrgNamespacedName types.NamespacedName
		)

		BeforeEach(func() {
			suffix := newRandomNamespaceSuffix()
			cephFSProvisioner = fmt.Sprintf("cephfs.csi.ceph.com/%s", suffix)

			// Namespace
			testNS = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("dryrun-cephfs-ns-%s", suffix)},
			}
			Expect(k8sClient.Create(context.TODO(), testNS)).To(Succeed())

			// StorageClass — CephFS provisioner, no matching VolumeReplicationClass.
			testSC = &storagev1.StorageClass{
				ObjectMeta:  metav1.ObjectMeta{Name: fmt.Sprintf("cephfs-sc-%s", suffix)},
				Provisioner: cephFSProvisioner,
			}
			Expect(k8sClient.Create(context.TODO(), testSC)).To(Succeed())

			// VolumeSnapshotClass — present so the absence of a snapshot is not due to
			// a missing VSC; it is solely because no VolumeReplicationClass exists for
			// the CephFS provisioner, keeping the PVC on the VolSync path.
			testVSC = &snapv1.VolumeSnapshotClass{
				ObjectMeta:     metav1.ObjectMeta{Name: fmt.Sprintf("cephfs-vsc-%s", suffix)},
				Driver:         cephFSProvisioner,
				DeletionPolicy: snapv1.VolumeSnapshotContentDelete,
			}
			Expect(k8sClient.Create(context.TODO(), testVSC)).To(Succeed())

			// PVC bound to the CephFS StorageClass.
			pvName := fmt.Sprintf("cephfs-pv-%s", suffix)
			pvcName := fmt.Sprintf("cephfs-pvc-%s", suffix)
			storageClassName := testSC.Name

			testPV = &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: pvName},
				Spec: corev1.PersistentVolumeSpec{
					Capacity:                      corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
					AccessModes:                   []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
					PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimRetain,
					StorageClassName:              storageClassName,
					PersistentVolumeSource: corev1.PersistentVolumeSource{
						HostPath: &corev1.HostPathVolumeSource{Path: "/tmp/cephfs-dryrun"},
					},
					ClaimRef: &corev1.ObjectReference{
						Namespace: testNS.Name,
						Name:      pvcName,
					},
				},
			}
			Expect(k8sClient.Create(context.TODO(), testPV)).To(Succeed())
			testPV.Status.Phase = corev1.VolumeBound
			Expect(k8sClient.Status().Update(context.TODO(), testPV)).To(Succeed())

			testPVC = &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pvcName,
					Namespace: testNS.Name,
					Labels:    map[string]string{"dryrun-cephfs-test": suffix},
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("1Gi"),
						},
					},
					VolumeName:       pvName,
					StorageClassName: &storageClassName,
				},
			}
			Expect(k8sClient.Create(context.TODO(), testPVC)).To(Succeed())
			testPVC.Status.Phase = corev1.ClaimBound
			testPVC.Status.AccessModes = testPVC.Spec.AccessModes
			testPVC.Status.Capacity = testPVC.Spec.Resources.Requests
			Expect(k8sClient.Status().Update(context.TODO(), testPVC)).To(Succeed())

			// VRG — Primary + DryRun=true + Action=Failover, same as the RBD test.
			// The key difference: no VolumeReplicationClass exists for cephFSProvisioner,
			// so the reconciler never calls reconcileVolRepsAsPrimary() for this PVC.
			vrgName := fmt.Sprintf("vrg-cephfs-dryrun-%s", suffix)
			testVRG = &ramendrv1alpha1.VolumeReplicationGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      vrgName,
					Namespace: testNS.Name,
				},
				Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
					PVCSelector:      metav1.LabelSelector{MatchLabels: map[string]string{"dryrun-cephfs-test": suffix}},
					ReplicationState: ramendrv1alpha1.Primary,
					DryRun:           true,
					Action:           ramendrv1alpha1.VRGActionFailover,
					Async: &ramendrv1alpha1.VRGAsyncSpec{
						SchedulingInterval:          "1h",
						VolumeSnapshotClassSelector: metav1.LabelSelector{},
					},
					VolSync:    ramendrv1alpha1.VolSyncSpec{Disabled: true},
					S3Profiles: []string{s3Profiles[vrgS3ProfileNumber].S3ProfileName},
				},
			}
			Expect(k8sClient.Create(context.TODO(), testVRG)).To(Succeed())

			vrgNamespacedName = types.NamespacedName{Name: vrgName, Namespace: testNS.Name}
		})

		AfterEach(func() {
			Expect(client.IgnoreNotFound(k8sClient.Delete(context.TODO(), testVRG))).To(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(context.TODO(), testPVC))).To(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(context.TODO(), testPV))).To(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(context.TODO(), testVSC))).To(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(context.TODO(), testSC))).To(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(context.TODO(), testNS))).To(Succeed())
		})

		It("should NOT create any VolumeSnapshot for a CephFS PVC even with DryRun=true+Failover", func() {
			By("waiting for the VRG to be reconciled at least once (Generation > 0)")
			// Ensure the reconciler has had a chance to run before asserting absence.
			Eventually(func() bool {
				current := &ramendrv1alpha1.VolumeReplicationGroup{}
				if err := apiReader.Get(context.TODO(), vrgNamespacedName, current); err != nil {
					return false
				}

				return current.Generation > 0
			}, timeout, interval).Should(BeTrue(),
				"VRG must be reconciled at least once before asserting no snapshots")

			By("consistently verifying zero VolumeSnapshots are created for the CephFS PVC")
			// No VolumeReplicationClass exists for the CephFS provisioner, so the VRG
			// reconciler never calls reconcileVolRepsAsPrimary() → ensureSnapshotsForDryRun()
			// is never invoked.  The absence of snapshots must hold across multiple reconcile
			// cycles, not just at one point in time.
			Consistently(func() int {
				snapList := &snapv1.VolumeSnapshotList{}
				if err := k8sClient.List(context.TODO(), snapList,
					client.InNamespace(testNS.Name),
					client.MatchingLabels{dryRunSnapshotLabelKey: "true"},
				); err != nil {
					return -1
				}

				return len(snapList.Items)
			}, timeout/2, interval).Should(Equal(0),
				"no VolumeSnapshot with dry-run-snapshot=true must be created for a CephFS PVC")
		})
	})
})
