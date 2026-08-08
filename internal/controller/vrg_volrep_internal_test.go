// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"strings"
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
		wantPrefix    string
		wantContains  []string
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
			want: "PVCs not ready: bad-pvc (failed to enable volume replication)",
		},
		{
			name:          "multiple error PVCs with different messages",
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
			wantPrefix:   "PVCs not ready: ",
			wantContains: []string{"pvc-a (image not found)", "pvc-b (VR not promoted)"},
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
			want: "PVCs not ready: error-pvc (promote failed)",
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
			want: "PVCs not ready: pvc-x (sync failed)",
		},
		{
			name:          "PVCs with no conditions returns fallback",
			conditionType: VRGConditionTypeDataReady,
			pvcs: []ramendrv1alpha1.ProtectedPVC{
				{Name: "no-cond-pvc"},
			},
			want: "All PVCs of the VolumeReplicationGroup are not ready",
		},
		{
			name:          "groups PVCs with same error message",
			conditionType: VRGConditionTypeDataReady,
			pvcs: []ramendrv1alpha1.ProtectedPVC{
				{Name: "pvc-1", Conditions: []metav1.Condition{
					{Type: VRGConditionTypeDataReady, Reason: VRGConditionReasonError, Message: "replication failed"},
				}},
				{Name: "pvc-2", Conditions: []metav1.Condition{
					{Type: VRGConditionTypeDataReady, Reason: VRGConditionReasonError, Message: "replication failed"},
				}},
				{Name: "pvc-3", Conditions: []metav1.Condition{
					{Type: VRGConditionTypeDataReady, Reason: VRGConditionReasonError, Message: "replication failed"},
				}},
			},
			want: "PVCs not ready: 3 PVCs including pvc-1, pvc-2 (replication failed)",
		},
		{
			name:          "groups by unique error with counts sorted descending",
			conditionType: VRGConditionTypeDataReady,
			pvcs: func() []ramendrv1alpha1.ProtectedPVC {
				var pvcs []ramendrv1alpha1.ProtectedPVC
				for i := 0; i < 5; i++ {
					pvcs = append(pvcs, ramendrv1alpha1.ProtectedPVC{
						Name: "pvc-repl-" + strings.Repeat("x", i),
						Conditions: []metav1.Condition{
							{Type: VRGConditionTypeDataReady, Reason: VRGConditionReasonError, Message: "replication failed"},
						},
					})
				}
				for i := 0; i < 2; i++ {
					pvcs = append(pvcs, ramendrv1alpha1.ProtectedPVC{
						Name: "pvc-vrc-" + strings.Repeat("y", i),
						Conditions: []metav1.Condition{
							{Type: VRGConditionTypeDataReady, Reason: VRGConditionReasonError, Message: "no VRC found"},
						},
					})
				}
				return pvcs
			}(),
			wantPrefix:   "PVCs not ready: 5 PVCs including ",
			wantContains: []string{"(replication failed)", "2 PVCs including", "(no VRC found)"},
		},
		{
			name:          "namespace included for multi-namespace PVCs",
			conditionType: VRGConditionTypeDataReady,
			pvcs: []ramendrv1alpha1.ProtectedPVC{
				{Namespace: "ns-a", Name: "data-pvc", Conditions: []metav1.Condition{
					{Type: VRGConditionTypeDataReady, Reason: VRGConditionReasonError, Message: "error"},
				}},
				{Namespace: "ns-b", Name: "data-pvc", Conditions: []metav1.Condition{
					{Type: VRGConditionTypeDataReady, Reason: VRGConditionReasonError, Message: "error"},
				}},
			},
			want: "PVCs not ready: 2 PVCs including ns-a/data-pvc, ns-b/data-pvc (error)",
		},
		{
			name:          "no namespace prefix for single-namespace PVCs",
			conditionType: VRGConditionTypeDataReady,
			pvcs: []ramendrv1alpha1.ProtectedPVC{
				{Name: "pvc-1", Conditions: []metav1.Condition{
					{Type: VRGConditionTypeDataReady, Reason: VRGConditionReasonError, Message: "error"},
				}},
				{Name: "pvc-2", Conditions: []metav1.Condition{
					{Type: VRGConditionTypeDataReady, Reason: VRGConditionReasonError, Message: "error"},
				}},
			},
			want: "PVCs not ready: 2 PVCs including pvc-1, pvc-2 (error)",
		},
		{
			name:          "max 2 exemplar PVCs per group",
			conditionType: VRGConditionTypeDataReady,
			pvcs: func() []ramendrv1alpha1.ProtectedPVC {
				var pvcs []ramendrv1alpha1.ProtectedPVC
				for i := 0; i < 10; i++ {
					pvcs = append(pvcs, ramendrv1alpha1.ProtectedPVC{
						Name: "pvc-" + string(rune('a'+i)),
						Conditions: []metav1.Condition{
							{Type: VRGConditionTypeDataReady, Reason: VRGConditionReasonError, Message: "same error"},
						},
					})
				}
				return pvcs
			}(),
			wantPrefix:   "PVCs not ready: 10 PVCs including ",
			wantContains: []string{"(same error)"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildPVCErrorMessage(tt.pvcs, tt.conditionType)

			if tt.want != "" {
				if got != tt.want {
					t.Errorf("buildPVCErrorMessage() = %q, want %q", got, tt.want)
				}

				return
			}

			if tt.wantPrefix != "" && !strings.HasPrefix(got, tt.wantPrefix) {
				t.Errorf("buildPVCErrorMessage() = %q, want prefix %q", got, tt.wantPrefix)
			}

			for _, s := range tt.wantContains {
				if !strings.Contains(got, s) {
					t.Errorf("buildPVCErrorMessage() = %q, want to contain %q", got, s)
				}
			}
		})
	}
}

func TestBuildPVCErrorMessageTruncation(t *testing.T) {
	var pvcs []ramendrv1alpha1.ProtectedPVC
	for i := 0; i < 1000; i++ {
		pvcs = append(pvcs, ramendrv1alpha1.ProtectedPVC{
			Namespace: "very-long-namespace-name-that-takes-up-space",
			Name:      "pvc-with-a-really-long-name-" + strings.Repeat("x", 50),
			Conditions: []metav1.Condition{
				{Type: VRGConditionTypeDataReady, Reason: VRGConditionReasonError,
					Message: "a very long error message that repeats itself " + strings.Repeat("detail ", 20)},
			},
		})
	}

	got := buildPVCErrorMessage(pvcs, VRGConditionTypeDataReady)

	if len(got) > maxConditionMessageBytes {
		t.Errorf("buildPVCErrorMessage() len = %d, want <= %d", len(got), maxConditionMessageBytes)
	}

	if !strings.HasPrefix(got, "PVCs not ready:") {
		t.Errorf("buildPVCErrorMessage() should start with 'PVCs not ready:', got %q", got[:50])
	}
}
