// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package cephfscg_test

import (
	"testing"

	vgsv1beta1 "github.com/red-hat-storage/external-snapshotter/client/v8/apis/volumegroupsnapshot/v1beta1"

	"github.com/ramendr/ramen/internal/controller/cephfscg"
)

func TestIsVGSReady(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		vgs      *vgsv1beta1.VolumeGroupSnapshot
		expected bool
	}{
		{
			name:     "nil VGS",
			vgs:      nil,
			expected: false,
		},
		{
			name:     "nil Status",
			vgs:      &vgsv1beta1.VolumeGroupSnapshot{},
			expected: false,
		},
		{
			name: "nil ReadyToUse",
			vgs: &vgsv1beta1.VolumeGroupSnapshot{
				Status: &vgsv1beta1.VolumeGroupSnapshotStatus{},
			},
			expected: false,
		},
		{
			name: "ReadyToUse false",
			vgs: &vgsv1beta1.VolumeGroupSnapshot{
				Status: &vgsv1beta1.VolumeGroupSnapshotStatus{
					ReadyToUse: boolPtr(false),
				},
			},
			expected: false,
		},
		{
			name: "ReadyToUse true",
			vgs: &vgsv1beta1.VolumeGroupSnapshot{
				Status: &vgsv1beta1.VolumeGroupSnapshotStatus{
					ReadyToUse: boolPtr(true),
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := cephfscg.IsVGSReady(tt.vgs); got != tt.expected {
				t.Errorf("IsVGSReady() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func boolPtr(b bool) *bool {
	return &b
}
