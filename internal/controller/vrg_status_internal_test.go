// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"testing"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
)

const statusTestGeneration = 2

func primaryVRGWithConditions(conditions ...metav1.Condition) *VRGInstance {
	return &VRGInstance{
		log: logr.Discard(),
		instance: &ramendrv1alpha1.VolumeReplicationGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "vrg",
				Namespace:  "vrg-ns",
				Generation: statusTestGeneration,
			},
			Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
				ReplicationState: ramendrv1alpha1.Primary,
				Action:           ramendrv1alpha1.VRGActionFailover,
			},
			Status: ramendrv1alpha1.VolumeReplicationGroupStatus{
				Conditions: conditions,
			},
		},
	}
}

func statusTestCondition(conditionType string, status metav1.ConditionStatus, generation int64) metav1.Condition {
	return metav1.Condition{
		Type:               conditionType,
		Status:             status,
		ObservedGeneration: generation,
		Reason:             "Test",
	}
}

// A Primary VRG reports the Primary state, which the hub waits for to complete a failover or relocate, only
// once both its data and its kube objects are ready at its current generation.
func TestUpdateStatusStatePrimaryWaitsForKubeObjects(t *testing.T) {
	dataReady := statusTestCondition(VRGConditionTypeDataReady, metav1.ConditionTrue, statusTestGeneration)

	cases := []struct {
		name       string
		conditions []metav1.Condition
		want       ramendrv1alpha1.State
	}{
		{
			name: "data and kube objects ready",
			conditions: []metav1.Condition{
				dataReady,
				statusTestCondition(VRGConditionTypeKubeObjectsReady, metav1.ConditionTrue, statusTestGeneration),
			},
			want: ramendrv1alpha1.PrimaryState,
		},
		{
			name: "kube objects restore failed",
			conditions: []metav1.Condition{
				dataReady,
				statusTestCondition(VRGConditionTypeKubeObjectsReady, metav1.ConditionFalse, statusTestGeneration),
			},
			want: ramendrv1alpha1.UnknownState,
		},
		{
			name: "kube objects not yet restored",
			conditions: []metav1.Condition{
				dataReady,
				statusTestCondition(VRGConditionTypeKubeObjectsReady, metav1.ConditionUnknown, statusTestGeneration),
			},
			want: ramendrv1alpha1.UnknownState,
		},
		{
			name: "kube objects ready for an older generation",
			conditions: []metav1.Condition{
				dataReady,
				statusTestCondition(VRGConditionTypeKubeObjectsReady, metav1.ConditionTrue, statusTestGeneration-1),
			},
			want: ramendrv1alpha1.UnknownState,
		},
		{
			name:       "kube objects condition missing",
			conditions: []metav1.Condition{dataReady},
			want:       ramendrv1alpha1.UnknownState,
		},
		{
			name: "data not ready",
			conditions: []metav1.Condition{
				statusTestCondition(VRGConditionTypeDataReady, metav1.ConditionFalse, statusTestGeneration),
				statusTestCondition(VRGConditionTypeKubeObjectsReady, metav1.ConditionTrue, statusTestGeneration),
			},
			want: ramendrv1alpha1.UnknownState,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := primaryVRGWithConditions(tc.conditions...)

			v.updateStatusState()

			if got := v.instance.Status.State; got != tc.want {
				t.Errorf("status.state = %q, want %q", got, tc.want)
			}
		})
	}
}
