// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
)

// secondaryRelocateVRG builds a VRGInstance representing a demoted source cluster that has finished
// transitioning to Secondary during a Relocate action (i.e. IsDRActionInProgress() == false), with
// the supplied VolRep-protected PVCs.
func secondaryRelocateVRG(pvcs []corev1.PersistentVolumeClaim) *VRGInstance {
	return &VRGInstance{
		log: logr.Discard(),
		instance: &ramendrv1alpha1.VolumeReplicationGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "vrg",
				Namespace:  "vrg-ns",
				Generation: 1,
			},
			Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
				ReplicationState: ramendrv1alpha1.Secondary,
				Action:           ramendrv1alpha1.VRGActionRelocate,
			},
			Status: ramendrv1alpha1.VolumeReplicationGroupStatus{
				State:              ramendrv1alpha1.SecondaryState,
				ObservedGeneration: 1,
			},
		},
		volRepPVCs: pvcs,
	}
}

func terminatingPVC(name string) corev1.PersistentVolumeClaim {
	deletedAt := metav1.NewTime(time.Unix(1000, 0))

	return corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "vrg-ns",
			DeletionTimestamp: &deletedAt,
			Finalizers:        []string{"volumereplicationgroups.ramendr.openshift.io/pvc-vr-protection"},
		},
	}
}

func livePVC(name string) corev1.PersistentVolumeClaim {
	return corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "vrg-ns",
		},
	}
}

type volRepSecondaryConflictCase struct {
	name           string
	pvcs           []corev1.PersistentVolumeClaim
	wantConflict   bool // expected hasConflictingVolRepPVCsOnSecondary() result
	wantCondFalse  bool // whether the aggregated NoClusterDataConflict condition is ConditionFalse
	wantCondReason string
}

func assertVolRepSecondaryConflict(t *testing.T, tc volRepSecondaryConflictCase) {
	t.Helper()

	v := secondaryRelocateVRG(tc.pvcs)

	if got := v.hasConflictingVolRepPVCsOnSecondary(); got != tc.wantConflict {
		t.Errorf("hasConflictingVolRepPVCsOnSecondary() = %v, want %v", got, tc.wantConflict)
	}

	cond := v.aggregateVolRepClusterDataConflictCondition()
	if cond == nil {
		t.Fatal("aggregateVolRepClusterDataConflictCondition() = nil, want a condition")
	}

	wantStatus := metav1.ConditionTrue
	if tc.wantCondFalse {
		wantStatus = metav1.ConditionFalse
	}

	if cond.Status != wantStatus {
		t.Errorf("condition Status = %v, want %v", cond.Status, wantStatus)
	}

	if cond.Reason != tc.wantCondReason {
		t.Errorf("condition Reason = %q, want %q", cond.Reason, tc.wantCondReason)
	}
}

func TestVolRepSecondaryConflict(t *testing.T) {
	t.Parallel()

	tests := []volRepSecondaryConflictCase{
		{
			name:           "only terminating leftovers converge without conflict",
			pvcs:           []corev1.PersistentVolumeClaim{terminatingPVC("pvc-a"), terminatingPVC("pvc-b")},
			wantConflict:   false,
			wantCondFalse:  false,
			wantCondReason: VRGConditionReasonNoConflictDetected,
		},
		{
			name:           "a live PVC is a genuine conflict",
			pvcs:           []corev1.PersistentVolumeClaim{livePVC("pvc-a")},
			wantConflict:   true,
			wantCondFalse:  true,
			wantCondReason: VRGConditionReasonClusterDataConflictSecondary,
		},
		{
			name:           "a live PVC alongside terminating leftovers is reported",
			pvcs:           []corev1.PersistentVolumeClaim{terminatingPVC("pvc-a"), livePVC("pvc-b")},
			wantConflict:   true,
			wantCondFalse:  true,
			wantCondReason: VRGConditionReasonClusterDataConflictSecondary,
		},
		{
			name:           "no PVCs, no conflict",
			pvcs:           nil,
			wantConflict:   false,
			wantCondFalse:  false,
			wantCondReason: VRGConditionReasonNoConflictDetected,
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertVolRepSecondaryConflict(t, tt)
		})
	}
}

// TestVolRepSecondaryConflictSuppressedWhileDRActionInProgress ensures the conflict is never raised
// while the DR action is still transitioning, regardless of PVC deletion state.
func TestVolRepSecondaryConflictSuppressedWhileDRActionInProgress(t *testing.T) {
	t.Parallel()

	// A live PVC would normally be a conflict, but the VRG has not finished transitioning to
	// Secondary (Status.State is Unknown), so IsDRActionInProgress() == true.
	v := secondaryRelocateVRG([]corev1.PersistentVolumeClaim{livePVC("pvc-a")})
	v.instance.Status.State = ramendrv1alpha1.UnknownState

	if !v.IsDRActionInProgress() {
		t.Fatal("expected IsDRActionInProgress() == true for a VRG still transitioning to Secondary")
	}

	if cond := v.validateSecondaryPVCConflictForVolRep(); cond != nil {
		t.Errorf("validateSecondaryPVCConflictForVolRep() = %+v, want nil while DR action is in progress", cond)
	}
}

func TestAggregateVolRepReplicationHealthyCondition(t *testing.T) {
	t.Parallel()

	vrg := &VRGInstance{instance: &ramendrv1alpha1.VolumeReplicationGroup{
		ObjectMeta: metav1.ObjectMeta{Generation: 1},
		Status: ramendrv1alpha1.VolumeReplicationGroupStatus{
			ProtectedPVCs: []ramendrv1alpha1.ProtectedPVC{{
				Name: "pvc-a",
				Conditions: []metav1.Condition{{
					Type:               VRGConditionTypeReplicationHealthy,
					Status:             metav1.ConditionFalse,
					Reason:             VRGConditionReasonError,
					ObservedGeneration: 1,
				}},
			}},
		},
	}}

	cond := vrg.aggregateVolRepReplicationHealthyCondition()
	if cond == nil || cond.Status != metav1.ConditionFalse {
		t.Fatalf("got %+v, want False", cond)
	}
}

func TestUpdateDRPCReplicationHealthyCondition(t *testing.T) {
	t.Parallel()

	drpc := &ramendrv1alpha1.DRPlacementControl{ObjectMeta: metav1.ObjectMeta{Generation: 1}}
	vrg := &ramendrv1alpha1.VolumeReplicationGroup{
		ObjectMeta: metav1.ObjectMeta{Generation: 1},
		Status: ramendrv1alpha1.VolumeReplicationGroupStatus{
			Conditions: []metav1.Condition{{
				Type:               VRGConditionTypeReplicationHealthy,
				Status:             metav1.ConditionFalse,
				Reason:             VRGConditionReasonError,
				ObservedGeneration: 1,
			}},
		},
	}

	updateDRPCReplicationHealthyCondition(drpc, vrg)

	if cond := meta.FindStatusCondition(drpc.Status.Conditions, ramendrv1alpha1.ConditionReplicationHealthy); cond == nil {
		t.Fatal("ReplicationHealthy missing")
	}

	updateDRPCReplicationHealthyCondition(drpc, &ramendrv1alpha1.VolumeReplicationGroup{})

	if cond := meta.FindStatusCondition(drpc.Status.Conditions, ramendrv1alpha1.ConditionReplicationHealthy); cond != nil {
		t.Errorf("ReplicationHealthy = %+v, want absent", cond)
	}
}

// existingPV builds a PV, as found on the cluster, left behind by a deleted claim.
func existingPV(phase corev1.PersistentVolumePhase, claimRef *corev1.ObjectReference) *corev1.PersistentVolume {
	return &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "pv-a"},
		Spec: corev1.PersistentVolumeSpec{
			StorageClassName:              "sc",
			PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimRetain,
			ClaimRef:                      claimRef,
		},
		Status: corev1.PersistentVolumeStatus{Phase: phase},
	}
}

func staleClaimRef() *corev1.ObjectReference {
	return &corev1.ObjectReference{
		Kind:            "PersistentVolumeClaim",
		APIVersion:      "v1",
		Namespace:       "vrg-ns",
		Name:            "pvc-a",
		UID:             "stale-uid",
		ResourceVersion: "12345",
	}
}

// asyncRestoreVRG builds a VRGInstance for an async VRG restoring PVs, backed by a fake client
// holding the supplied existing PV.
func asyncRestoreVRG(t *testing.T, pv *corev1.PersistentVolume) *VRGInstance {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

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
				Async:            &ramendrv1alpha1.VRGAsyncSpec{},
			},
		},
	}
}

func assertPVAdopted(t *testing.T, claimRef *corev1.ObjectReference) {
	t.Helper()

	v := asyncRestoreVRG(t, existingPV(corev1.VolumeReleased, claimRef))

	// The backed up PV references the claim being restored.
	if err := v.validateExistingPV(existingPV(corev1.VolumeReleased, staleClaimRef())); err != nil {
		t.Fatalf("validateExistingPV() = %v, want nil", err)
	}

	got := &corev1.PersistentVolume{}
	if err := v.reconciler.Get(context.TODO(), types.NamespacedName{Name: "pv-a"}, got); err != nil {
		t.Fatalf("Get PV: %v", err)
	}

	if got.Annotations[RestoreAnnotation] != RestoredByRamen {
		t.Errorf("annotation %s = %q, want %q", RestoreAnnotation, got.Annotations[RestoreAnnotation], RestoredByRamen)
	}

	if claimRef == nil {
		if got.Spec.ClaimRef != nil {
			t.Errorf("ClaimRef = %+v, want nil", got.Spec.ClaimRef)
		}

		return
	}

	// UID, ResourceVersion and APIVersion are cleared so the PV can bind to the restored claim.
	want := corev1.ObjectReference{Kind: "PersistentVolumeClaim", Namespace: "vrg-ns", Name: "pvc-a"}
	if got.Spec.ClaimRef == nil || *got.Spec.ClaimRef != want {
		t.Errorf("ClaimRef = %+v, want %+v", got.Spec.ClaimRef, want)
	}
}

func TestValidateExistingPVAdoptsReleasedPV(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		claimRef *corev1.ObjectReference
	}{
		{name: "stale claimRef is cleared for rebinding", claimRef: staleClaimRef()},
		{name: "nil claimRef", claimRef: nil},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertPVAdopted(t, tt.claimRef)
		})
	}
}

// TestValidateExistingPVAdoptsReleasedPVForOffloadedSync covers failback onto a cluster still
// holding the PV retained when it went secondary. Nothing annotates that PV as restored by Ramen,
// so rejecting it would leave the VRG unable to become primary.
func TestValidateExistingPVAdoptsReleasedPVForOffloadedSync(t *testing.T) {
	t.Parallel()

	v := syncRestoreVRG(t, existingPV(corev1.VolumeReleased, staleClaimRef()), true)

	if err := v.validateExistingPV(existingPV(corev1.VolumeReleased, staleClaimRef())); err != nil {
		t.Fatalf("validateExistingPV() = %v, want nil", err)
	}

	got := &corev1.PersistentVolume{}
	if err := v.reconciler.Get(context.TODO(), types.NamespacedName{Name: "pv-a"}, got); err != nil {
		t.Fatalf("Get PV: %v", err)
	}

	if got.Spec.ClaimRef.UID != "" {
		t.Errorf("ClaimRef.UID = %q, want cleared", got.Spec.ClaimRef.UID)
	}

	if got.Annotations[RestoreAnnotation] != RestoredByRamen {
		t.Errorf("annotation %s = %q, want %q", RestoreAnnotation, got.Annotations[RestoreAnnotation], RestoredByRamen)
	}
}

// TestValidateExistingPVErrorsOnUnreadableStorageClass covers a sync VRG that cannot read the
// StorageClass of the PV being restored. Treating that as not offloaded sends the PV down the
// shared storage path, which rewrites it against the class the cluster still has.
func TestValidateExistingPVErrorsOnUnreadableStorageClass(t *testing.T) {
	t.Parallel()

	v := syncRestoreVRG(t, existingPV(corev1.VolumeReleased, staleClaimRef()), true)

	backedUp := existingPV(corev1.VolumeReleased, staleClaimRef())
	backedUp.Spec.StorageClassName = "sc-gone"

	if err := v.validateExistingPV(backedUp); err == nil {
		t.Fatal("validateExistingPV() = nil, want an error for an unreadable StorageClass")
	}
}

// TestValidateExistingPVDoesNotAdoptAvailablePV ensures only Released PVs are adopted; an unbound PV
// in any other phase that was not restored by Ramen is still rejected.
func TestValidateExistingPVDoesNotAdoptAvailablePV(t *testing.T) {
	t.Parallel()

	v := asyncRestoreVRG(t, existingPV(corev1.VolumeAvailable, staleClaimRef()))

	if err := v.validateExistingPV(existingPV(corev1.VolumeAvailable, staleClaimRef())); err == nil {
		t.Fatal("validateExistingPV() = nil, want error for unannotated Available PV")
	}

	got := &corev1.PersistentVolume{}
	if err := v.reconciler.Get(context.TODO(), types.NamespacedName{Name: "pv-a"}, got); err != nil {
		t.Fatalf("Get PV: %v", err)
	}

	if _, ok := got.Annotations[RestoreAnnotation]; ok {
		t.Errorf("annotation %s set on PV that should not be adopted", RestoreAnnotation)
	}

	if got.Spec.ClaimRef.UID != "stale-uid" {
		t.Errorf("ClaimRef.UID = %q, want unchanged %q", got.Spec.ClaimRef.UID, "stale-uid")
	}
}

func assertProtectedPVCsRecorded(t *testing.T, offloaded bool, want int) {
	t.Helper()

	v := syncRestoreVRG(t, existingPV(corev1.VolumeAvailable, nil), offloaded)

	if err := v.updateProtectedPVCs(restoredPVC()); err != nil {
		t.Fatalf("updateProtectedPVCs() = %v, want nil", err)
	}

	protected := v.instance.Status.ProtectedPVCs
	if len(protected) != want {
		t.Fatalf("ProtectedPVCs = %d, want %d", len(protected), want)
	}

	if want == 0 {
		return
	}

	if protected[0].StorageClassName == nil || *protected[0].StorageClassName != "sc" {
		t.Errorf("StorageClassName = %v, want sc", protected[0].StorageClassName)
	}

	if len(protected[0].AccessModes) != 1 {
		t.Errorf("AccessModes = %v, want the PVC access modes", protected[0].AccessModes)
	}
}

// TestUpdateProtectedPVCsForSync covers recording the PVC fields on restore. Metro DR records
// nothing, because it reuses the same volume on both clusters.
func TestUpdateProtectedPVCsForSync(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		offloaded bool
		want      int
	}{
		{name: "offloaded sync records the PVC", offloaded: true, want: 1},
		{name: "sync records nothing", offloaded: false, want: 0},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProtectedPVCsRecorded(t, tt.offloaded, tt.want)
		})
	}
}

// TestUpdatePVClusterIDForRestoreForSync covers rewriting the PV clusterID for the target cluster.
func TestUpdatePVClusterIDForRestoreForSync(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		offloaded bool
		want      string
	}{
		{name: "offloaded sync rewrites clusterID", offloaded: true, want: "target-cluster"},
		{name: "sync keeps the clusterID", offloaded: false, want: "source-cluster"},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			v := syncRestoreVRG(t, existingPV(corev1.VolumeAvailable, nil), tt.offloaded)

			pv := existingPV(corev1.VolumeAvailable, nil)
			pv.Spec.CSI = &corev1.CSIPersistentVolumeSource{
				Driver:           "driver",
				VolumeHandle:     "handle",
				VolumeAttributes: map[string]string{clusterIDKey: "source-cluster"},
			}

			v.updatePVClusterIDForRestore(pv, offloadedSC(tt.offloaded))

			if got := pv.Spec.CSI.VolumeAttributes[clusterIDKey]; got != tt.want {
				t.Errorf("clusterID = %q, want %q", got, tt.want)
			}
		})
	}
}

// Test helpers

// syncRestoreVRG builds a VRGInstance for a sync VRG restoring PVs, backed by a fake client
// holding the supplied existing PV, with its StorageClass offloaded or not.
func syncRestoreVRG(t *testing.T, pv *corev1.PersistentVolume, offloaded bool) *VRGInstance {
	t.Helper()

	v := asyncRestoreVRG(t, pv)
	v.instance.Spec.Async = nil
	v.instance.Spec.Sync = &ramendrv1alpha1.VRGSyncSpec{}
	v.storageClassCache = map[string]*storagev1.StorageClass{"sc": offloadedSC(offloaded)}

	return v
}

// offloadedSC builds the StorageClass of the restored PVs, carrying a clusterID for this cluster.
func offloadedSC(offloaded bool) *storagev1.StorageClass {
	labels := map[string]string{}
	if offloaded {
		labels[StorageOffloadedLabel] = ""
	}

	return &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{Name: "sc", Labels: labels},
		Parameters: map[string]string{clusterIDKey: "target-cluster"},
	}
}

// restoredPVC builds a PVC as restored from S3, before it is recorded as protected.
func restoredPVC() *corev1.PersistentVolumeClaim {
	scName := "sc"

	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "pvc-a", Namespace: "vrg-ns"},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: &scName,
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
		},
	}
}
