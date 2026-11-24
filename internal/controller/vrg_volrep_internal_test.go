// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"testing"
	"time"

	volrep "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	rmnutil "github.com/ramendr/ramen/internal/controller/util"
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

const (
	resyncTestVRGeneration = 2
	resyncTestVGRCName     = "vgrc-resync"
)

func resyncTestCondition(conditionType string, status metav1.ConditionStatus, generation int64) metav1.Condition {
	return metav1.Condition{
		Type:               conditionType,
		Status:             status,
		ObservedGeneration: generation,
		Reason:             "Test",
		LastTransitionTime: metav1.Now(),
	}
}

func notReplicatingConditions(generation int64) []metav1.Condition {
	return []metav1.Condition{
		resyncTestCondition(volrep.ConditionValidated, metav1.ConditionTrue, generation),
		resyncTestCondition(volrep.ConditionCompleted, metav1.ConditionTrue, generation),
		resyncTestCondition(volrep.ConditionReplicating, metav1.ConditionFalse, generation),
	}
}

func replicatingConditions() []metav1.Condition {
	return []metav1.Condition{
		resyncTestCondition(volrep.ConditionValidated, metav1.ConditionTrue, resyncTestVRGeneration),
		resyncTestCondition(volrep.ConditionCompleted, metav1.ConditionTrue, resyncTestVRGeneration),
		resyncTestCondition(volrep.ConditionReplicating, metav1.ConditionTrue, resyncTestVRGeneration),
	}
}

func resyncingConditions() []metav1.Condition {
	return []metav1.Condition{
		resyncTestCondition(volrep.ConditionCompleted, metav1.ConditionTrue, resyncTestVRGeneration),
		resyncTestCondition(volrep.ConditionDegraded, metav1.ConditionTrue, resyncTestVRGeneration),
		resyncTestCondition(volrep.ConditionResyncing, metav1.ConditionTrue, resyncTestVRGeneration),
	}
}

func resyncCompletedConditions() []metav1.Condition {
	return []metav1.Condition{
		resyncTestCondition(volrep.ConditionCompleted, metav1.ConditionTrue, resyncTestVRGeneration),
		resyncTestCondition(volrep.ConditionDegraded, metav1.ConditionFalse, resyncTestVRGeneration),
		resyncTestCondition(volrep.ConditionResyncing, metav1.ConditionFalse, resyncTestVRGeneration),
	}
}

func promotedConditions() []metav1.Condition {
	return []metav1.Condition{
		resyncTestCondition(volrep.ConditionCompleted, metav1.ConditionTrue, resyncTestVRGeneration),
		resyncTestCondition(volrep.ConditionDegraded, metav1.ConditionFalse, resyncTestVRGeneration),
		resyncTestCondition(volrep.ConditionResyncing, metav1.ConditionFalse, resyncTestVRGeneration),
		resyncTestCondition(volrep.ConditionReplicating, metav1.ConditionFalse, resyncTestVRGeneration),
	}
}

func resyncTestVR(state volrep.ReplicationState, conditions []metav1.Condition) *volrep.VolumeReplication {
	return &volrep.VolumeReplication{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "pvc-a",
			Namespace:  "vrg-ns",
			Generation: resyncTestVRGeneration,
		},
		Spec: volrep.VolumeReplicationSpec{
			ReplicationState: state,
		},
		Status: volrep.VolumeReplicationStatus{
			ObservedGeneration: resyncTestVRGeneration,
			Conditions:         conditions,
		},
	}
}

// resyncTestVGR returns a VGR in the given state, whose status conditions are all stamped with the VGR
// generation, as csi-addons does when copying the status from the backing VR.
func resyncTestVGR(state volrep.ReplicationState, vrName string, conditions []metav1.Condition,
) *volrep.VolumeGroupReplication {
	vgrConditions := make([]metav1.Condition, len(conditions))
	for i := range conditions {
		vgrConditions[i] = conditions[i]
		vgrConditions[i].ObservedGeneration = resyncTestVRGeneration
	}

	return &volrep.VolumeGroupReplication{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "vgr-a",
			Namespace:  "vrg-ns",
			Generation: resyncTestVRGeneration,
		},
		Spec: volrep.VolumeGroupReplicationSpec{
			ReplicationState:                state,
			VolumeReplicationName:           vrName,
			VolumeGroupReplicationClassName: resyncTestVGRCName,
		},
		Status: volrep.VolumeGroupReplicationStatus{
			VolumeReplicationStatus: volrep.VolumeReplicationStatus{
				ObservedGeneration: resyncTestVRGeneration,
				Conditions:         vgrConditions,
			},
		},
	}
}

func resyncTestVGRC(labeled bool) *volrep.VolumeGroupReplicationClass {
	vgrc := &volrep.VolumeGroupReplicationClass{
		ObjectMeta: metav1.ObjectMeta{Name: resyncTestVGRCName},
		Spec: volrep.VolumeGroupReplicationClassSpec{
			Provisioner: "test.provisioner",
		},
	}
	if labeled {
		vgrc.SetLabels(map[string]string{ResyncOnPrimaryLabel: "true"})
	}

	return vgrc
}

// resyncTestVRGInstanceWithObjects builds a primary async VRGInstance backed by a fake client holding the
// supplied objects.
func resyncTestVRGInstanceWithObjects(t *testing.T, deleted bool, objs ...client.Object) *VRGInstance {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	if err := volrep.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	vrg := &ramendrv1alpha1.VolumeReplicationGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "vrg", Namespace: "vrg-ns", Generation: 1},
		Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
			ReplicationState: ramendrv1alpha1.Primary,
			Async:            &ramendrv1alpha1.VRGAsyncSpec{},
		},
	}

	if deleted {
		deletedAt := metav1.NewTime(time.Unix(1000, 0))
		vrg.SetDeletionTimestamp(&deletedAt)
	}

	return &VRGInstance{
		reconciler: &VolumeReplicationGroupReconciler{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build(),
		},
		ctx:              context.TODO(),
		log:              logr.Discard(),
		instance:         vrg,
		grpReplClassList: &volrep.VolumeGroupReplicationClassList{},
	}
}

type resyncOnPrimaryVRCase struct {
	name       string
	vrState    volrep.ReplicationState
	conditions []metav1.Condition
	wantState  volrep.ReplicationState
	wantReady  bool
}

func assertResyncOnPrimaryVR(t *testing.T, tc resyncOnPrimaryVRCase) {
	t.Helper()

	v := resyncTestVRGInstanceWithObjects(t, false, resyncTestVR(tc.vrState, tc.conditions))
	pvc := livePVC("pvc-a")

	volRep := &volrep.VolumeReplication{}
	if err := v.reconciler.Get(v.ctx, types.NamespacedName{Name: "pvc-a", Namespace: "vrg-ns"}, volRep); err != nil {
		t.Fatalf("Get VR: %v", err)
	}

	_, ready, err := v.updateVR(&pvc, volRep, volrep.Primary, logr.Discard())
	if err != nil {
		t.Fatalf("updateVR() error = %v", err)
	}

	if ready != tc.wantReady {
		t.Errorf("updateVR() ready = %v, want %v", ready, tc.wantReady)
	}

	got := &volrep.VolumeReplication{}
	if err := v.reconciler.Get(v.ctx, types.NamespacedName{Name: "pvc-a", Namespace: "vrg-ns"}, got); err != nil {
		t.Fatalf("Get VR: %v", err)
	}

	if got.Spec.ReplicationState != tc.wantState {
		t.Errorf("VR replicationState = %q, want %q", got.Spec.ReplicationState, tc.wantState)
	}
}

// TestResyncOnPrimaryVR confirms the VR-only path does not enter Spec=Resync (unsupported; VGRC-only).
func TestResyncOnPrimaryVR(t *testing.T) {
	t.Parallel()

	tests := []resyncOnPrimaryVRCase{
		{
			name: "not replicating, stays primary", vrState: volrep.Primary,
			conditions: notReplicatingConditions(resyncTestVRGeneration), wantState: volrep.Primary, wantReady: true,
		},
		{
			name: "existing Resync Spec forced back to Primary (VR path unsupported)", vrState: volrep.Resync,
			conditions: resyncingConditions(), wantState: volrep.Primary,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertResyncOnPrimaryVR(t, tt)
		})
	}
}

type resyncOnPrimaryVGRCase struct {
	name         string
	vgrcLabeled  bool
	global       bool
	deleted      bool
	vgrState     volrep.ReplicationState
	vrName       string
	vrState      volrep.ReplicationState
	vrConditions []metav1.Condition
	wantState    volrep.ReplicationState
}

func assertResyncOnPrimaryVGR(t *testing.T, tc resyncOnPrimaryVGRCase) {
	t.Helper()

	vgr := resyncTestVGR(tc.vgrState, tc.vrName, tc.vrConditions)
	objs := []client.Object{vgr, resyncTestVR(tc.vrState, tc.vrConditions), resyncTestVGRC(tc.vgrcLabeled)}
	v := resyncTestVRGInstanceWithObjects(t, tc.deleted, objs...)

	if tc.global {
		v.instance.SetLabels(map[string]string{GlobalVGRLabel: "global-id"})
	}

	pvc := livePVC("pvc-a")
	key := types.NamespacedName{Name: "vgr-a", Namespace: "vrg-ns"}

	volGroupRep := &volrep.VolumeGroupReplication{}
	if err := v.reconciler.Get(v.ctx, key, volGroupRep); err != nil {
		t.Fatalf("Get VGR: %v", err)
	}

	if _, _, err := v.updateVGR([]*corev1.PersistentVolumeClaim{&pvc}, volGroupRep, volrep.Primary,
		logr.Discard()); err != nil {
		t.Fatalf("updateVGR() error = %v", err)
	}

	got := &volrep.VolumeGroupReplication{}
	if err := v.reconciler.Get(v.ctx, key, got); err != nil {
		t.Fatalf("Get VGR: %v", err)
	}

	if got.Spec.ReplicationState != tc.wantState {
		t.Errorf("VGR replicationState = %q, want %q", got.Spec.ReplicationState, tc.wantState)
	}
}

func TestResyncOnPrimaryVGR(t *testing.T) {
	t.Parallel()

	tests := []resyncOnPrimaryVGRCase{
		{
			name: "no VGRC label, not replicating, stays primary", vgrState: volrep.Primary, vrName: "pvc-a",
			vrState: volrep.Primary, vrConditions: promotedConditions(), wantState: volrep.Primary,
		},
		{
			name: "VGRC labeled, not replicating, moves to resync", vgrcLabeled: true, vgrState: volrep.Primary,
			vrName: "pvc-a", vrState: volrep.Primary, vrConditions: promotedConditions(), wantState: volrep.Resync,
		},
		{
			name: "VGRC labeled, replicating, stays primary", vgrcLabeled: true, vgrState: volrep.Primary,
			vrName: "pvc-a", vrState: volrep.Primary, vrConditions: replicatingConditions(), wantState: volrep.Primary,
		},
		{
			name: "VGRC labeled, global VGR, not replicating, stays primary", vgrcLabeled: true, global: true,
			vgrState: volrep.Primary, vrName: "pvc-a", vrState: volrep.Primary, vrConditions: promotedConditions(),
			wantState: volrep.Primary,
		},
		{
			name: "VGRC labeled, backing VR not yet known, stays primary", vgrcLabeled: true, vgrState: volrep.Primary,
			vrState: volrep.Primary, vrConditions: promotedConditions(), wantState: volrep.Primary,
		},
		{
			name: "VGRC labeled, resync not yet propagated to backing VR, stays resync", vgrcLabeled: true,
			vgrState: volrep.Resync, vrName: "pvc-a", vrState: volrep.Primary, vrConditions: promotedConditions(),
			wantState: volrep.Resync,
		},
		{
			name: "VGRC labeled, resync in progress, stays resync", vgrcLabeled: true, vgrState: volrep.Resync,
			vrName: "pvc-a", vrState: volrep.Resync, vrConditions: resyncingConditions(), wantState: volrep.Resync,
		},
		{
			name: "VGRC labeled, resync completed, returns to primary", vgrcLabeled: true, vgrState: volrep.Resync,
			vrName: "pvc-a", vrState: volrep.Resync, vrConditions: resyncCompletedConditions(),
			wantState: volrep.Primary,
		},
		{
			name: "VGRC label removed, resync in progress, returns to primary", vgrState: volrep.Resync,
			vrName: "pvc-a", vrState: volrep.Resync, vrConditions: resyncingConditions(), wantState: volrep.Primary,
		},
		{
			name: "VGRC labeled, VRG deleted, resync in progress, returns to primary", vgrcLabeled: true, deleted: true,
			vgrState: volrep.Resync, vrName: "pvc-a", vrState: volrep.Resync, vrConditions: resyncingConditions(),
			wantState: volrep.Primary,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertResyncOnPrimaryVGR(t, tt)
		})
	}
}

// assertResyncOnPrimaryConditions checks PVC and VRG conditions while a primary VGR is resyncing.
// DataReady stays True so kube objects can be restored. DataProtected and ReplicationHealthy stay False.
func assertResyncOnPrimaryConditions(t *testing.T, vgrState, vrState volrep.ReplicationState,
	conditions []metav1.Condition,
) {
	t.Helper()

	v := resyncTestVRGInstanceWithObjects(t, false,
		resyncTestVGR(vgrState, "pvc-a", conditions),
		resyncTestVR(vrState, conditions),
		resyncTestVGRC(true))
	pvc := livePVC("pvc-a")
	key := types.NamespacedName{Name: "vgr-a", Namespace: "vrg-ns"}

	volGroupRep := &volrep.VolumeGroupReplication{}
	if err := v.reconciler.Get(v.ctx, key, volGroupRep); err != nil {
		t.Fatalf("Get VGR: %v", err)
	}

	if _, _, err := v.updateVGR([]*corev1.PersistentVolumeClaim{&pvc}, volGroupRep, volrep.Primary,
		logr.Discard()); err != nil {
		t.Fatalf("updateVGR() error = %v", err)
	}

	got := &volrep.VolumeGroupReplication{}
	if err := v.reconciler.Get(v.ctx, key, got); err != nil {
		t.Fatalf("Get VGR: %v", err)
	}

	if got.Spec.ReplicationState != volrep.Resync {
		t.Fatalf("VGR replicationState = %q, want %q", got.Spec.ReplicationState, volrep.Resync)
	}

	protectedPVC := v.findProtectedPVC(pvc.Namespace, pvc.Name)
	if protectedPVC == nil {
		t.Fatal("protected PVC not found")
	}

	assertCondition(t, protectedPVC.Conditions, VRGConditionTypeDataReady, metav1.ConditionTrue, VRGConditionReasonReady)
	assertCondition(t, protectedPVC.Conditions, VRGConditionTypeDataProtected, metav1.ConditionFalse, "")
	assertCondition(t, protectedPVC.Conditions, VRGConditionTypeReplicationHealthy, metav1.ConditionFalse, "")

	v.volRepPVCs = []corev1.PersistentVolumeClaim{pvc}

	vrgDataReady := v.aggregateVolRepDataReadyCondition()
	if vrgDataReady == nil {
		t.Fatal("aggregateVolRepDataReadyCondition() = nil")
	}

	if vrgDataReady.Status != metav1.ConditionTrue || vrgDataReady.Reason != VRGConditionReasonReady {
		t.Errorf("VRG DataReady = %s/%s, want True/%s", vrgDataReady.Status, vrgDataReady.Reason, VRGConditionReasonReady)
	}
}

func assertCondition(t *testing.T, conditions []metav1.Condition, conditionType string,
	status metav1.ConditionStatus, reason string,
) {
	t.Helper()

	got := rmnutil.FindCondition(conditions, conditionType)
	if got == nil {
		t.Fatalf("condition %s not found", conditionType)
	}

	if got.Status != status {
		t.Errorf("condition %s status = %s, want %s", conditionType, got.Status, status)
	}

	if reason != "" && got.Reason != reason {
		t.Errorf("condition %s reason = %s, want %s", conditionType, got.Reason, reason)
	}
}

func TestResyncOnPrimaryVGRConditions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		vgrState   volrep.ReplicationState
		vrState    volrep.ReplicationState
		conditions []metav1.Condition
	}{
		{
			name: "DataReady when moved from primary to resync", vgrState: volrep.Primary,
			vrState: volrep.Primary, conditions: promotedConditions(),
		},
		{
			name: "DataReady while resync is in progress", vgrState: volrep.Resync,
			vrState: volrep.Resync, conditions: resyncingConditions(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertResyncOnPrimaryConditions(t, tt.vgrState, tt.vrState, tt.conditions)
		})
	}
}
