// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
)

func TestBuildPVCErrorMessage(t *testing.T) {
	tests := []struct {
		name          string
		pvcs          []ramendrv1alpha1.ProtectedPVC
		conditionType string
		want          string
	}{
		{
			name:          "no PVCs with error returns fallback",
			conditionType: VRGConditionTypeDataReady,
			pvcs: []ramendrv1alpha1.ProtectedPVC{
				{
					Name: "healthy-pvc",
					Conditions: []metav1.Condition{
						{Type: VRGConditionTypeDataReady, Status: metav1.ConditionTrue, Reason: VRGConditionReasonReady},
					},
				},
			},
			want: "All PVCs of the VolumeReplicationGroup are not ready",
		},
		{
			name:          "single error PVC includes name and message",
			conditionType: VRGConditionTypeDataReady,
			pvcs: []ramendrv1alpha1.ProtectedPVC{
				{
					Name: "good-pvc",
					Conditions: []metav1.Condition{
						{Type: VRGConditionTypeDataReady, Status: metav1.ConditionTrue, Reason: VRGConditionReasonReady},
					},
				},
				{
					Name: "bad-pvc",
					Conditions: []metav1.Condition{
						{Type: VRGConditionTypeDataReady, Status: metav1.ConditionFalse, Reason: VRGConditionReasonError,
							Message: "failed to enable volume replication"},
					},
				},
			},
			want: "PVCs not ready: bad-pvc(failed to enable volume replication)",
		},
		{
			name:          "multiple error PVCs",
			conditionType: VRGConditionTypeDataReady,
			pvcs: []ramendrv1alpha1.ProtectedPVC{
				{
					Name: "pvc-a",
					Conditions: []metav1.Condition{
						{Type: VRGConditionTypeDataReady, Status: metav1.ConditionFalse, Reason: VRGConditionReasonError,
							Message: "image not found"},
					},
				},
				{
					Name: "pvc-b",
					Conditions: []metav1.Condition{
						{Type: VRGConditionTypeDataReady, Status: metav1.ConditionFalse, Reason: VRGConditionReasonError,
							Message: "VR not promoted"},
					},
				},
			},
			want: "PVCs not ready: pvc-a(image not found), pvc-b(VR not promoted)",
		},
		{
			name:          "skips progressing PVCs",
			conditionType: VRGConditionTypeDataReady,
			pvcs: []ramendrv1alpha1.ProtectedPVC{
				{
					Name: "progressing-pvc",
					Conditions: []metav1.Condition{
						{Type: VRGConditionTypeDataReady, Status: metav1.ConditionFalse, Reason: VRGConditionReasonProgressing,
							Message: "VolumeReplication generation not updated"},
					},
				},
				{
					Name: "error-pvc",
					Conditions: []metav1.Condition{
						{Type: VRGConditionTypeDataReady, Status: metav1.ConditionFalse, Reason: VRGConditionReasonError,
							Message: "promote failed"},
					},
				},
			},
			want: "PVCs not ready: error-pvc(promote failed)",
		},
		{
			name:          "works with DataProtected condition",
			conditionType: VRGConditionTypeDataProtected,
			pvcs: []ramendrv1alpha1.ProtectedPVC{
				{
					Name: "pvc-x",
					Conditions: []metav1.Condition{
						{Type: VRGConditionTypeDataProtected, Status: metav1.ConditionFalse, Reason: VRGConditionReasonError,
							Message: "sync failed"},
					},
				},
			},
			want: "PVCs not ready: pvc-x(sync failed)",
		},
		{
			name:          "PVCs with no conditions returns fallback",
			conditionType: VRGConditionTypeDataReady,
			pvcs: []ramendrv1alpha1.ProtectedPVC{
				{Name: "no-cond-pvc"},
			},
			want: "All PVCs of the VolumeReplicationGroup are not ready",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildPVCErrorMessage(tt.pvcs, tt.conditionType)
			if got != tt.want {
				t.Errorf("buildPVCErrorMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}
