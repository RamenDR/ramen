// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

// Disabling testpackage linter to test unexported functions in the util package.
//
//nolint:testpackage
package util

import (
	"testing"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestVRGSecondaryReady(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		vrg  ramen.VolumeReplicationGroup
		want bool
	}{
		{
			name: "secondary with matching generation",
			vrg: ramen.VolumeReplicationGroup{
				ObjectMeta: metav1.ObjectMeta{Generation: 2},
				Status: ramen.VolumeReplicationGroupStatus{
					State:              ramen.SecondaryState,
					ObservedGeneration: 2,
				},
			},
			want: true,
		},
		{
			name: "secondary with stale generation",
			vrg: ramen.VolumeReplicationGroup{
				ObjectMeta: metav1.ObjectMeta{Generation: 2},
				Status: ramen.VolumeReplicationGroupStatus{
					State:              ramen.SecondaryState,
					ObservedGeneration: 1,
				},
			},
			want: false,
		},
		{
			name: "unknown state",
			vrg: ramen.VolumeReplicationGroup{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Status: ramen.VolumeReplicationGroupStatus{
					State:              ramen.UnknownState,
					ObservedGeneration: 1,
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := vrgSecondaryReady(&tt.vrg); got != tt.want {
				t.Fatalf("vrgSecondaryReady() = %v, want %v", got, tt.want)
			}
		})
	}
}
