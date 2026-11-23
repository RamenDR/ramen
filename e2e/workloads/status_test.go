// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package workloads

import (
	"testing"

	"github.com/ramendr/ramen/e2e/types"
)

func TestNewWorkloadStatus(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		workload bool
		pvcs     map[string]bool
		want     types.ApplicationStatus
	}{
		{"workload alone", true, nil, types.ApplicationFound},
		{"workload alone missing", false, nil, types.ApplicationNotFound},
		{"all present", true, map[string]bool{"data": true, "logs": true}, types.ApplicationFound},
		{"all missing", false, map[string]bool{"data": false, "logs": false}, types.ApplicationNotFound},
		{"missing PVC", true, map[string]bool{"data": true, "logs": false}, types.ApplicationPartial},
		{"remaining PVC", false, map[string]bool{"data": false, "logs": true}, types.ApplicationPartial},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := newWorkloadStatus("c1", tc.workload, tc.pvcs)
			want := types.WorkloadStatus{ClusterName: "c1", Status: tc.want}
			if got != want {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
	}
}
