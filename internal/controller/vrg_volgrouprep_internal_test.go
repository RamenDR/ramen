// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"testing"

	volrep "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
)

// TestReconcileMissingVGRUnprotectsOffloadedSyncPVCs covers VRG deletion for offloaded sync, which
// does have a VolumeGroupReplication. Reporting it as present leaves the PVCs protected, so they
// keep the VRG owner reference and are garbage collected with it.
func TestReconcileMissingVGRUnprotectsOffloadedSyncPVCs(t *testing.T) {
	t.Parallel()

	v, pvc := missingVGRSyncVRG(t, true)

	vrMissing, requeue := v.reconcileMissingVGR(types.NamespacedName{Name: "vgr", Namespace: "vrg-ns"},
		[]*corev1.PersistentVolumeClaim{pvc}, logr.Discard())

	if !vrMissing || requeue {
		t.Fatalf("reconcileMissingVGR() = (%t, %t), want (true, false)", vrMissing, requeue)
	}

	got := &corev1.PersistentVolumeClaim{}
	if err := v.reconciler.Get(context.TODO(),
		types.NamespacedName{Name: "pvc-a", Namespace: "vrg-ns"}, got); err != nil {
		t.Fatalf("Get PVC: %v", err)
	}

	if len(got.Finalizers) != 0 {
		t.Errorf("finalizers = %v, want the PVC unprotected", got.Finalizers)
	}

	gotPV := &corev1.PersistentVolume{}
	if err := v.reconciler.Get(context.TODO(), types.NamespacedName{Name: "pv-a"}, gotPV); err != nil {
		t.Fatalf("Get PV: %v", err)
	}

	// A secondary keeps the PV, because the peer cluster reaches the same volume.
	if gotPV.Spec.PersistentVolumeReclaimPolicy != corev1.PersistentVolumeReclaimRetain {
		t.Errorf("reclaim policy = %q, want Retain", gotPV.Spec.PersistentVolumeReclaimPolicy)
	}

	if _, ok := gotPV.Labels[pvLeftBehindLabelKey]; !ok {
		t.Errorf("label %s missing, PV cannot be swept when the VRG is deleted", pvLeftBehindLabelKey)
	}
}

// TestReconcileMissingVGRReturnsEarlyForSync pins metro DR, which has no VolumeGroupReplication to
// look for and must leave its PVCs alone.
func TestReconcileMissingVGRReturnsEarlyForSync(t *testing.T) {
	t.Parallel()

	v, pvc := missingVGRSyncVRG(t, false)

	vrMissing, requeue := v.reconcileMissingVGR(types.NamespacedName{Name: "vgr", Namespace: "vrg-ns"},
		[]*corev1.PersistentVolumeClaim{pvc}, logr.Discard())

	if vrMissing || requeue {
		t.Fatalf("reconcileMissingVGR() = (%t, %t), want (false, false)", vrMissing, requeue)
	}

	got := &corev1.PersistentVolumeClaim{}
	if err := v.reconciler.Get(context.TODO(),
		types.NamespacedName{Name: "pvc-a", Namespace: "vrg-ns"}, got); err != nil {
		t.Fatalf("Get PVC: %v", err)
	}

	if len(got.Finalizers) != 1 {
		t.Errorf("finalizers = %v, want the PVC left protected", got.Finalizers)
	}
}

// Test helpers

// missingVGRSyncVRG builds a secondary sync VRGInstance and a protected PVC bound to a retained PV,
// with no VolumeGroupReplication present, backed by a fake client.
func missingVGRSyncVRG(t *testing.T, offloaded bool) (*VRGInstance, *corev1.PersistentVolumeClaim) {
	t.Helper()

	scName := "sc"

	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, volrep.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatalf("AddToScheme: %v", err)
		}
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
