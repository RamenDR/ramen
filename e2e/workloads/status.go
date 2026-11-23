// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package workloads

import "github.com/ramendr/ramen/e2e/types"

func newWorkloadStatus(clusterName string, workloadExists bool, pvcInfo map[string]bool) types.WorkloadStatus {
	pvcsFound := 0

	for _, exists := range pvcInfo {
		if exists {
			pvcsFound++
		}
	}

	var status types.ApplicationStatus

	switch {
	case workloadExists && pvcsFound == len(pvcInfo):
		status = types.ApplicationFound
	case workloadExists || pvcsFound > 0:
		status = types.ApplicationPartial
	default:
		status = types.ApplicationNotFound
	}

	return types.WorkloadStatus{ClusterName: clusterName, Status: status}
}
