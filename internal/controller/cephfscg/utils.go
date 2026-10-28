// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package cephfscg

import (
	"sigs.k8s.io/controller-runtime/pkg/client"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/util"
	"github.com/ramendr/ramen/internal/controller/volsync"
)

func isFinalSyncComplete(replicationGroupSource *ramendrv1alpha1.ReplicationGroupSource) bool {
	return replicationGroupSource.Status.LastManualSync == volsync.FinalSyncTriggerString
}

// IsVGSReady checks if a VolumeGroupSnapshot is ready to use
func IsVGSReady(vgs client.Object) bool {
	if vgs == nil {
		return false
	}

	return util.VolumeGroupSnapshotIsReady(vgs)
}
