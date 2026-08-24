// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"testing"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
