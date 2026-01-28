// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package cephfscg

import (
	"context"
	"fmt"

	vgsv1beta1 "github.com/red-hat-storage/external-snapshotter/client/v8/apis/volumegroupsnapshot/v1beta1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/volsync"
)

// ------------- [Begin] Copied from existing code in Ramen ----
func isFinalSyncComplete(replicationGroupSource *ramendrv1alpha1.ReplicationGroupSource) bool {
	return replicationGroupSource.Status.LastManualSync == volsync.FinalSyncTriggerString
}

func isLatestImageReady(latestImage *corev1.TypedLocalObjectReference) bool {
	if latestImage == nil || latestImage.Name == "" || latestImage.Kind != volsync.VolumeSnapshotKind {
		return false
	}

	return true
}

// Copied from func (v *VSHandler) getStorageClass(
func GetStorageClass(
	ctx context.Context, k8sClient client.Client, storageClassName *string,
) (*storagev1.StorageClass, error) {
	if storageClassName == nil || *storageClassName == "" {
		err := fmt.Errorf("no storageClassName given, cannot proceed")

		return nil, err
	}

	storageClass := &storagev1.StorageClass{}
	if err := k8sClient.Get(ctx,
		types.NamespacedName{Name: *storageClassName},
		storageClass); err != nil {
		return nil, fmt.Errorf("error getting storage class (%w)", err)
	}

	return storageClass, nil
}

// ------------- [End] Edited from existing code in Ramen ----

// IsVGSReady checks if a VolumeGroupSnapshot is ready to use
func IsVGSReady(vgs *vgsv1beta1.VolumeGroupSnapshot) bool {
	if vgs == nil {
		return false
	}

	return vgs.Status != nil && vgs.Status.ReadyToUse != nil && *vgs.Status.ReadyToUse
}
