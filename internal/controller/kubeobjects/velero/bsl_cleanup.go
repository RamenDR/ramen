// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package velero

import (
	velero "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

// IsBSLStale reports whether an existing BackupStorageLocation should be removed
// before creating or updating a new one on the Primary protect path.
//
// Staleness uses CreationTimestamp and phase:
//   - non-Available phases are always stale;
//   - Available BSL with no paired Backup (same name) is stale — left from a crash
//     after BSL create and before Backup create;
//   - Available BSL whose CreationTimestamp is after the Backup's is stale (invalid pair).
func IsBSLStale(bsl *velero.BackupStorageLocation, backup *velero.Backup) bool {
	if bsl.Status.Phase != velero.BackupStorageLocationPhaseAvailable {
		return true
	}

	if backup == nil {
		return true
	}

	return bsl.CreationTimestamp.Time.After(backup.CreationTimestamp.Time)
}
