// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers //nolint:testpackage

import (
	"context"

	volrep "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
)

// missingVGRSyncVRG builds a secondary sync VRGInstance and a protected PVC bound to a retained PV,
// with no VolumeGroupReplication present, backed by a fake client.
func missingVGRSyncVRG(offloaded bool) (*VRGInstance, *corev1.PersistentVolumeClaim) {
	scName := "sc"

	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, volrep.AddToScheme} {
		Expect(add(scheme)).To(Succeed())
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "pvc-a",
			Namespace:  "vrg-ns",
			Finalizers: []string{PvcVRFinalizerProtected},
		},
		Spec: corev1.PersistentVolumeClaimSpec{StorageClassName: &scName, VolumeName: "pv-a"},
	}

	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "pv-a"},
		Spec: corev1.PersistentVolumeSpec{
			StorageClassName:              scName,
			PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimRetain,
		},
	}

	labels := map[string]string{}
	if offloaded {
		labels[StorageOffloadedLabel] = ""
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pvc, pv).Build()

	return &VRGInstance{
		reconciler: &VolumeReplicationGroupReconciler{Client: client, APIReader: client},
		ctx:        context.TODO(),
		log:        logr.Discard(),
		storageClassCache: map[string]*storagev1.StorageClass{
			scName: {ObjectMeta: metav1.ObjectMeta{Name: scName, Labels: labels}},
		},
		instance: &ramendrv1alpha1.VolumeReplicationGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "vrg", Namespace: "vrg-ns", UID: "vrg-uid"},
			Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
				ReplicationState: ramendrv1alpha1.Secondary,
				Sync:             &ramendrv1alpha1.VRGSyncSpec{},
			},
		},
	}, pvc
}

var _ = Describe("reconcileMissingVGR for a sync VRG", func() {
	vgrName := types.NamespacedName{Name: "vgr", Namespace: "vrg-ns"}

	var (
		v   *VRGInstance
		pvc *corev1.PersistentVolumeClaim
	)

	getPVC := func() *corev1.PersistentVolumeClaim {
		got := &corev1.PersistentVolumeClaim{}
		Expect(v.reconciler.Get(context.TODO(),
			types.NamespacedName{Name: "pvc-a", Namespace: "vrg-ns"}, got)).To(Succeed())

		return got
	}

	// Offloaded sync does have a VolumeGroupReplication. Reporting it as present on deletion leaves
	// the PVCs protected, so they keep the VRG owner reference and are garbage collected with it.
	Context("with an offloaded StorageClass", func() {
		BeforeEach(func() {
			v, pvc = missingVGRSyncVRG(true)
		})

		It("unprotects the PVCs and retains the PV", func() {
			vrMissing, requeue := v.reconcileMissingVGR(vgrName, []*corev1.PersistentVolumeClaim{pvc}, logr.Discard())

			Expect(vrMissing).To(BeTrue())
			Expect(requeue).To(BeFalse())
			Expect(getPVC().Finalizers).To(BeEmpty())

			gotPV := &corev1.PersistentVolume{}
			Expect(v.reconciler.Get(context.TODO(), types.NamespacedName{Name: "pv-a"}, gotPV)).To(Succeed())

			// A secondary keeps the PV, because the peer cluster reaches the same volume.
			Expect(gotPV.Spec.PersistentVolumeReclaimPolicy).To(Equal(corev1.PersistentVolumeReclaimRetain))
			// Without the label the PV cannot be swept when the VRG is deleted.
			Expect(gotPV.Labels).To(HaveKey(pvLeftBehindLabelKey))
		})
	})

	// Metro DR has no VolumeGroupReplication to look for and must leave its PVCs alone.
	Context("without an offloaded StorageClass", func() {
		BeforeEach(func() {
			v, pvc = missingVGRSyncVRG(false)
		})

		It("returns early and leaves the PVCs protected", func() {
			vrMissing, requeue := v.reconcileMissingVGR(vgrName, []*corev1.PersistentVolumeClaim{pvc}, logr.Discard())

			Expect(vrMissing).To(BeFalse())
			Expect(requeue).To(BeFalse())
			Expect(getPVC().Finalizers).To(HaveLen(1))
		})
	})
})

const (
	offloadedTestProvisioner  = "k8s.io/minikube-hostpath"
	offloadedTestStorageID    = "sid-1"
	offloadedTestGroupID      = "grid-1"
	offloadedTestSCName       = "standard"
	offloadedTestSelectorKey  = "ramendr.openshift.io/class"
	offloadedTestSelectorVal  = "chosen"
	offloadedTestInterval     = "5m"
	offloadedTestZeroInterval = "0m"
)

func offloadedTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()

	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	Expect(storagev1.AddToScheme(scheme)).To(Succeed())
	Expect(volrep.AddToScheme(scheme)).To(Succeed())

	return scheme
}

func offloadedTestVRG(async *ramendrv1alpha1.VRGAsyncSpec, objects ...client.Object) *VRGInstance {
	return &VRGInstance{
		ctx: context.Background(),
		log: logr.Discard(),
		reconciler: &VolumeReplicationGroupReconciler{
			Client: fake.NewClientBuilder().WithScheme(offloadedTestScheme()).WithObjects(objects...).Build(),
		},
		instance: &ramendrv1alpha1.VolumeReplicationGroup{
			Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{Async: async},
		},
		storageClassCache: map[string]*storagev1.StorageClass{},
	}
}

func offloadedTestStorageClass(offloaded bool) *storagev1.StorageClass {
	labels := map[string]string{
		StorageIDLabel:          offloadedTestStorageID,
		GroupReplicationIDLabel: offloadedTestGroupID,
	}
	if offloaded {
		labels[StorageOffloadedLabel] = ""
	}

	return &storagev1.StorageClass{
		ObjectMeta:  metav1.ObjectMeta{Name: offloadedTestSCName, Labels: labels},
		Provisioner: offloadedTestProvisioner,
	}
}

func offloadedTestPVC() *corev1.PersistentVolumeClaim {
	scName := offloadedTestSCName

	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "busybox-pvc", Namespace: "app"},
		Spec:       corev1.PersistentVolumeClaimSpec{StorageClassName: &scName},
	}
}

func offloadedTestClass(
	name, interval, storageID string, selected, defaultClass bool,
) *volrep.VolumeGroupReplicationClass {
	labels := map[string]string{
		StorageIDLabel:          storageID,
		GroupReplicationIDLabel: offloadedTestGroupID,
	}
	if selected {
		labels[offloadedTestSelectorKey] = offloadedTestSelectorVal
	}

	var annotations map[string]string
	if defaultClass {
		annotations = map[string]string{defaultVRCAnnotationKey: "true"}
	}

	parameters := map[string]string{}
	if interval != "" {
		parameters[ReplicationClassScheduleKey] = interval
	}

	return &volrep.VolumeGroupReplicationClass{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels, Annotations: annotations},
		Spec: volrep.VolumeGroupReplicationClassSpec{
			Provisioner: offloadedTestProvisioner,
			Parameters:  parameters,
		},
	}
}

func offloadedTestAsync() *ramendrv1alpha1.VRGAsyncSpec {
	return &ramendrv1alpha1.VRGAsyncSpec{
		SchedulingInterval: offloadedTestInterval,
		ReplicationClassSelector: metav1.LabelSelector{
			MatchLabels: map[string]string{offloadedTestSelectorKey: offloadedTestSelectorVal},
		},
	}
}

func offloadedTestClasses(classes ...*volrep.VolumeGroupReplicationClass) []*volrep.VolumeGroupReplicationClass {
	return classes
}

// syncRestoreVRG builds a VRGInstance for a sync VRG restoring PVs, backed by a fake client
// holding the supplied existing PV, with its StorageClass offloaded or not.
func syncRestoreVRG(pv *corev1.PersistentVolume, offloaded bool) *VRGInstance {
	scheme := runtime.NewScheme()
	Expect(corev1.AddToScheme(scheme)).To(Succeed())

	return &VRGInstance{
		reconciler: &VolumeReplicationGroupReconciler{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(pv).Build(),
		},
		ctx: context.TODO(),
		log: logr.Discard(),
		instance: &ramendrv1alpha1.VolumeReplicationGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "vrg", Namespace: "vrg-ns"},
			Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
				ReplicationState: ramendrv1alpha1.Primary,
				Sync:             &ramendrv1alpha1.VRGSyncSpec{},
			},
		},
		storageClassCache: map[string]*storagev1.StorageClass{"sc": syncRestoreSC(offloaded)},
	}
}

// syncRestoreSC builds the StorageClass of the restored PVs, carrying a clusterID for this cluster.
func syncRestoreSC(offloaded bool) *storagev1.StorageClass {
	labels := map[string]string{}
	if offloaded {
		labels[StorageOffloadedLabel] = ""
	}

	return &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{Name: "sc", Labels: labels},
		Parameters: map[string]string{clusterIDKey: "target-cluster"},
	}
}

// syncRestorePVC builds a PVC as restored from S3, before it is recorded as protected.
func syncRestorePVC() *corev1.PersistentVolumeClaim {
	scName := "sc"

	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "pvc-a", Namespace: "vrg-ns"},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: &scName,
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
		},
	}
}

var _ = Describe("selectOffloadedVGRClass", func() {
	DescribeTable("selects the VolumeGroupReplicationClass",
		func(
			async *ramendrv1alpha1.VRGAsyncSpec,
			classes []*volrep.VolumeGroupReplicationClass,
			wantName, wantErr, errWithout string,
		) {
			objects := make([]client.Object, 0, 1+len(classes))
			objects = append(objects, offloadedTestStorageClass(true))

			for _, class := range classes {
				objects = append(objects, class)
			}

			got, err := offloadedTestVRG(async, objects...).selectOffloadedVGRClass(offloadedTestPVC())
			if wantErr != "" {
				Expect(err).To(MatchError(ContainSubstring(wantErr)))

				if errWithout != "" {
					Expect(err.Error()).NotTo(ContainSubstring(errWithout))
				}

				return
			}

			Expect(err).NotTo(HaveOccurred())
			Expect(got.GetName()).To(Equal(wantName))
		},
		Entry("async selects the class whose interval matches",
			offloadedTestAsync(),
			offloadedTestClasses(
				offloadedTestClass("other-interval", "1m", offloadedTestStorageID, true, false),
				offloadedTestClass("matched", offloadedTestInterval, offloadedTestStorageID, true, false),
			),
			"matched", "", ""),
		Entry("async rejects a different interval",
			offloadedTestAsync(),
			offloadedTestClasses(
				offloadedTestClass("other-interval", "1m", offloadedTestStorageID, true, false),
			),
			"", "schedulingInterval "+offloadedTestInterval, ""),
		Entry("async honors the replication class selector",
			offloadedTestAsync(),
			offloadedTestClasses(
				offloadedTestClass("unlabeled", offloadedTestInterval, offloadedTestStorageID, false, false),
				offloadedTestClass("labeled", offloadedTestInterval, offloadedTestStorageID, true, false),
			),
			"labeled", "", ""),
		Entry("sync selects a zero interval class",
			nil,
			offloadedTestClasses(
				offloadedTestClass("sync-class", offloadedTestZeroInterval, offloadedTestStorageID, false, false),
			),
			"sync-class", "", ""),
		Entry("sync rejects a scheduled class",
			nil,
			offloadedTestClasses(
				offloadedTestClass("scheduled", offloadedTestInterval, offloadedTestStorageID, false, false),
			),
			"", "zero schedulingInterval", offloadedTestInterval),
		Entry("sync prefers the zero interval class over a scheduled one",
			nil,
			offloadedTestClasses(
				offloadedTestClass("scheduled", offloadedTestInterval, offloadedTestStorageID, false, false),
				offloadedTestClass("zero", offloadedTestZeroInterval, offloadedTestStorageID, false, false),
			),
			"zero", "", ""),
		Entry("sync selects a class without a schedulingInterval",
			nil,
			offloadedTestClasses(
				offloadedTestClass("no-interval", "", offloadedTestStorageID, false, false),
			),
			"no-interval", "", ""),
		Entry("sync reports a miss without quoting an interval",
			nil,
			offloadedTestClasses(
				offloadedTestClass("wrong-storage", offloadedTestInterval, "other-sid", false, false),
			),
			"", "provisioner, storageID and groupReplicationID", offloadedTestInterval),
		Entry("several matches use the default class",
			nil,
			offloadedTestClasses(
				offloadedTestClass("plain", offloadedTestZeroInterval, offloadedTestStorageID, false, false),
				offloadedTestClass("defaulted", offloadedTestZeroInterval, offloadedTestStorageID, false, true),
			),
			"defaulted", "", ""),
		Entry("several matches without a default annotation fail",
			nil,
			offloadedTestClasses(
				offloadedTestClass("one", offloadedTestZeroInterval, offloadedTestStorageID, false, false),
				offloadedTestClass("two", offloadedTestZeroInterval, offloadedTestStorageID, false, false),
			),
			"", "no default annotation", ""),
	)
})

var _ = Describe("processOffloadedPVCs", func() {
	pvcList := &corev1.PersistentVolumeClaimList{Items: []corev1.PersistentVolumeClaim{*offloadedTestPVC()}}

	peer := func(offloaded bool) ramendrv1alpha1.PeerClass {
		return ramendrv1alpha1.PeerClass{
			StorageClassName: offloadedTestSCName,
			StorageID:        []string{offloadedTestStorageID},
			Offloaded:        offloaded,
		}
	}

	DescribeTable("compares the peer classes with the StorageClass",
		func(offloaded bool, peers []ramendrv1alpha1.PeerClass, want bool, wantErr string) {
			v := offloadedTestVRG(nil, offloadedTestStorageClass(offloaded))

			got, err := v.processOffloadedPVCs(pvcList, peers)
			if wantErr != "" {
				Expect(err).To(MatchError(ContainSubstring(wantErr)))

				return
			}

			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(want))
		},
		Entry("an empty peer list is not offloaded", true, nil, false, ""),
		Entry("a non-offloaded peer stays not offloaded",
			false, []ramendrv1alpha1.PeerClass{peer(false)}, false, ""),
		Entry("the peer and the storage class must agree",
			false, []ramendrv1alpha1.PeerClass{peer(true)}, false, "mismatched peerClass offload support"),
	)
})

// rejectOffloadedPVCsWithoutPeers covers a sync VolumeReplicationGroup that did not take the
// offloaded path. Nothing creates a VolumeGroupReplication there, so the PVC must not be accepted.
var _ = Describe("rejectOffloadedPVCsWithoutPeers", func() {
	DescribeTable("validates the PVCs of a sync VRG",
		func(offloaded, noSC, noSCName bool, wantErr string) {
			pvc := offloadedTestPVC()
			if noSCName {
				pvc.Spec.StorageClassName = nil
			}

			pvcList := &corev1.PersistentVolumeClaimList{Items: []corev1.PersistentVolumeClaim{*pvc}}

			objects := []client.Object{offloadedTestStorageClass(offloaded)}
			if noSC {
				objects = nil
			}

			v := offloadedTestVRG(nil, objects...)
			v.instance.Spec.Sync = &ramendrv1alpha1.VRGSyncSpec{}

			err := v.updateSyncPVCs(pvcList)
			if wantErr != "" {
				Expect(err).To(MatchError(ContainSubstring(wantErr)))

				return
			}

			Expect(err).NotTo(HaveOccurred())
		},
		Entry("an offloaded PVC is rejected", true, false, false, "has no peer classes"),
		Entry("a non-offloaded PVC is accepted", false, false, false, ""),
		Entry("a named StorageClass that cannot be read is an error",
			false, true, false, "failed to get the storageclass"),
		Entry("a PVC without a StorageClass name is accepted", false, true, true, ""),
	)
})

var _ = Describe("VRG restore for sync", func() {
	Describe("validateExistingPV", func() {
		// Failback onto a cluster still holding the PV retained when it went secondary. Nothing
		// annotates that PV as restored by Ramen, so rejecting it would leave the VRG unable to
		// become primary.
		It("adopts a Released PV for offloaded sync", func() {
			v := syncRestoreVRG(existingPV(corev1.VolumeReleased, staleClaimRef()), true)

			Expect(v.validateExistingPV(existingPV(corev1.VolumeReleased, staleClaimRef()))).To(Succeed())

			got := &corev1.PersistentVolume{}
			Expect(v.reconciler.Get(context.TODO(), types.NamespacedName{Name: "pv-a"}, got)).To(Succeed())

			Expect(got.Spec.ClaimRef.UID).To(BeEmpty())
			Expect(got.Annotations).To(HaveKeyWithValue(RestoreAnnotation, RestoredByRamen))
		})

		// Treating an unreadable StorageClass as not offloaded sends the PV down the shared storage
		// path, which rewrites it against the class the cluster still has.
		It("errors when the StorageClass of the PV cannot be read", func() {
			v := syncRestoreVRG(existingPV(corev1.VolumeReleased, staleClaimRef()), true)

			backedUp := existingPV(corev1.VolumeReleased, staleClaimRef())
			backedUp.Spec.StorageClassName = "sc-gone"

			Expect(v.validateExistingPV(backedUp)).NotTo(Succeed())
		})
	})

	// Metro DR records nothing, because it reuses the same volume on both clusters.
	DescribeTable("updateProtectedPVCs records the PVC fields",
		func(offloaded bool, want int) {
			v := syncRestoreVRG(existingPV(corev1.VolumeAvailable, nil), offloaded)

			Expect(v.updateProtectedPVCs(syncRestorePVC())).To(Succeed())

			protected := v.instance.Status.ProtectedPVCs
			Expect(protected).To(HaveLen(want))

			if want == 0 {
				return
			}

			Expect(protected[0].StorageClassName).To(HaveValue(Equal("sc")))
			Expect(protected[0].AccessModes).To(HaveLen(1))
		},
		Entry("offloaded sync records the PVC", true, 1),
		Entry("sync records nothing", false, 0),
	)

	DescribeTable("updatePVClusterIDForRestore rewrites the PV clusterID for the target cluster",
		func(offloaded bool, want string) {
			v := syncRestoreVRG(existingPV(corev1.VolumeAvailable, nil), offloaded)

			pv := existingPV(corev1.VolumeAvailable, nil)
			pv.Spec.CSI = &corev1.CSIPersistentVolumeSource{
				Driver:           "driver",
				VolumeHandle:     "handle",
				VolumeAttributes: map[string]string{clusterIDKey: "source-cluster"},
			}

			v.updatePVClusterIDForRestore(pv, syncRestoreSC(offloaded))

			Expect(pv.Spec.CSI.VolumeAttributes).To(HaveKeyWithValue(clusterIDKey, want))
		},
		Entry("offloaded sync rewrites clusterID", true, "target-cluster"),
		Entry("sync keeps the clusterID", false, "source-cluster"),
	)
})
