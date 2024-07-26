package cephfscg

import (
	"context"
	"fmt"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/volsync"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// ------------- [Begin] Copied from existing code in Ramen ----
func isFinalSyncComplete(replicationGroupSource *ramendrv1alpha1.ReplicationGroupSource) bool {
	return replicationGroupSource.Status.LastManualSync == volsync.FinalSyncTriggerString
}

func getLocalReplicationName(pvcName string) string {
	return pvcName + "-local" // Use PVC name as name plus -local for local RD and RS
}

func isLatestImageReady(latestImage *corev1.TypedLocalObjectReference) bool {
	if latestImage == nil || latestImage.Name == "" || latestImage.Kind != volsync.VolumeSnapshotKind {
		return false
	}

	return true
}

func getReplicationDestinationName(pvcName string) string {
	return pvcName // Use PVC name as name of ReplicationDestination
}

// This is the remote service name that can be accessed from another cluster.  This assumes submariner and that
// a ServiceExport is created for the service on the cluster that has the ReplicationDestination
func getRemoteServiceNameForRDFromPVCName(pvcName, rdNamespace string) string {
	return fmt.Sprintf("%s.%s.svc.clusterset.local", getLocalServiceNameForRDFromPVCName(pvcName), rdNamespace)
}

// Service name that VolSync will create locally in the same namespace as the ReplicationDestination
func getLocalServiceNameForRDFromPVCName(pvcName string) string {
	return getLocalServiceNameForRD(getReplicationDestinationName(pvcName))
}

func getLocalServiceNameForRD(rdName string) string {
	// This is the name VolSync will use for the service
	return fmt.Sprintf("volsync-rsync-tls-dst-%s", rdName)
}

// ------------- [End] Copied from existing code in Ramen ----

// ------------- [Begin] Edited from existing code in Ramen ----

// Copied from func (v *VSHandler) ModifyRSSpecForCephFS
func GetRestoreStorageClass(
	ctx context.Context, k8sClient client.Client, storageClassName string,
	defaultCephFSCSIDriverName string,
) (*storagev1.StorageClass, error) {
	storageClass, err := GetStorageClass(ctx, k8sClient, &storageClassName)
	if err != nil {
		return nil, err
	}

	if storageClass.Provisioner != defaultCephFSCSIDriverName {
		return storageClass, nil // No workaround required
	}

	// Create/update readOnlyPVCStorageClass
	readOnlyPVCStorageClass := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: storageClass.GetName() + "-vrg",
		},
	}

	_, err = ctrlutil.CreateOrUpdate(ctx, k8sClient, readOnlyPVCStorageClass, func() error {
		// Do not update the storageclass if it already exists - Provisioner and Parameters are immutable anyway
		if readOnlyPVCStorageClass.CreationTimestamp.IsZero() {
			readOnlyPVCStorageClass.Provisioner = storageClass.Provisioner

			// Copy other parameters from the original storage class
			readOnlyPVCStorageClass.Parameters = map[string]string{}
			for k, v := range storageClass.Parameters {
				readOnlyPVCStorageClass.Parameters[k] = v
			}

			// Set backingSnapshot parameter to true
			readOnlyPVCStorageClass.Parameters["backingSnapshot"] = "true"
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}

	return readOnlyPVCStorageClass, nil
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
