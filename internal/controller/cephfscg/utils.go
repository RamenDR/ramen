// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package cephfscg

import (
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/volsync"
)

func isFinalSyncComplete(replicationGroupSource *ramendrv1alpha1.ReplicationGroupSource) bool {
	return replicationGroupSource.Status.LastManualSync == volsync.FinalSyncTriggerString
}
