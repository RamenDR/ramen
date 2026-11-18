// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package velero

import (
	"fmt"
	"time"

	velero "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	"github.com/ramendr/ramen/internal/controller/util"
)

func (w objectWriter) bslCreateOrUpdate(
	loc kubeobjects.StorageLocation,
	labels map[string]string,
	backupLocation *velero.BackupStorageLocation,
) error {
	op, err := controllerruntime.CreateOrUpdate(w.ctx, w.Client, backupLocation, func() error {
		backupLocation.TypeMeta = veleroTypeMeta("BackupStorageLocation")
		backupLocation.ObjectMeta.Labels = labels
		backupLocation.Spec = BSLSpec(loc)
		util.AddLabel(backupLocation, util.CreatedByRamenLabel, "true")

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create or update BackupStorageLocation: %w", err)
	}

	w.log.Info("backupstoragelocation reconciled", "operation", op, "name", backupLocation.GetName())

	return nil
}

// BSLSpec returns the spec of the BackupStorageLocation that stores kube objects
// protect and recover requests at loc.
func BSLSpec(loc kubeobjects.StorageLocation) velero.BackupStorageLocationSpec {
	// Configure BSL based on VRG replication state:
	// - Primary: ReadWrite access, BackupSyncPeriod=0s (no sync needed)
	// - Secondary: ReadOnly access, BackupSyncPeriod=10s (sync from S3)
	var (
		accessMode       velero.BackupStorageLocationAccessMode
		backupSyncPeriod *metav1.Duration
	)

	if loc.ReplicationState == "secondary" {
		accessMode = velero.BackupStorageLocationAccessModeReadOnly
		backupSyncPeriod = &metav1.Duration{Duration: 10 * time.Second}
	} else {
		// Primary or unspecified (backward compatibility)
		accessMode = velero.BackupStorageLocationAccessModeReadWrite
		backupSyncPeriod = &metav1.Duration{} // 0s - no sync
	}

	return velero.BackupStorageLocationSpec{
		Provider: "aws",
		StorageType: velero.StorageType{
			ObjectStorage: &velero.ObjectStorageLocation{
				Bucket: loc.Bucket,
				Prefix: loc.KeyPrefix + path,
				CACert: loc.CACertificates,
			},
		},
		Config: map[string]string{
			"region":            loc.Region,
			"s3ForcePathStyle":  "true",
			"s3Url":             loc.S3URL,
			"checksumAlgorithm": "",
		},
		Credential:       loc.SecretKeyRef,
		AccessMode:       accessMode,
		BackupSyncPeriod: backupSyncPeriod,
	}
}

func isBSLAvailable(bsl velero.BackupStorageLocation) bool {
	return bsl.Status.Phase == velero.BackupStorageLocationPhaseAvailable
}

// checkBSLStatus checks if BSL is Available
func (w objectWriter) checkBSLStatus(
	reader client.Reader,
	key client.ObjectKey,
	currentBSL *velero.BackupStorageLocation,
	bslName string,
) (bool, error) {
	if err := reader.Get(w.ctx, key, currentBSL); err != nil {
		if k8serrors.IsNotFound(err) {
			w.log.V(1).Info("BSL not found", "bsl", bslName, "error", err)
		} else {
			w.log.V(1).Info("Failed to get BSL", "bsl", bslName, "error", err)
		}

		return false, err
	}

	w.log.V(1).Info("BSL status check", "bsl", bslName, "phase", currentBSL.Status.Phase)

	return isBSLAvailable(*currentBSL), nil
}

// checkBSLAvailability checks if BSL is Available and returns error if not ready
func (w objectWriter) checkBSLAvailability(bsl *velero.BackupStorageLocation) error {
	reader, ok := w.Client.(client.Reader)
	if !ok {
		return fmt.Errorf("writer does not implement client.Reader interface")
	}

	key := client.ObjectKey{
		Namespace: bsl.Namespace,
		Name:      bsl.Name,
	}

	currentBSL := &velero.BackupStorageLocation{}

	available, err := w.checkBSLStatus(reader, key, currentBSL, bsl.Name)
	if err != nil {
		w.log.V(1).Info("Failed to check BSL status, will retry in next reconcile", "bsl", bsl.Name, "error", err)

		return fmt.Errorf("failed to check BSL status: %w", err)
	}

	if !available {
		w.log.Info("BSL not yet available, will retry in next reconcile", "bsl", bsl.Name,
			"phase", currentBSL.Status.Phase)

		return fmt.Errorf("BSL %s/%s not yet available", bsl.Namespace, bsl.Name)
	}

	w.log.Info("BSL is available, proceeding with backup creation", "bsl", bsl.Name)

	return nil
}

// ensureBSLReadyForPrimaryBackup ensures the BSL is in ReadWrite mode with sync disabled
// before creating a backup on a Primary cluster. When transitioning from Secondary to Primary,
// the BSL starts in ReadOnly mode with sync enabled (for restore). Before the first backup,
// we wait for the final sync to complete (verified by lastSyncTime), then update to ReadWrite/0s.
func (w objectWriter) ensureBSLReadyForPrimaryBackup(
	bsl *velero.BackupStorageLocation,
	loc kubeobjects.StorageLocation,
	labels map[string]string,
) error {
	reader, ok := w.Client.(client.Reader)
	if !ok {
		return fmt.Errorf("writer does not implement client.Reader interface")
	}

	key := client.ObjectKey{
		Namespace: bsl.Namespace,
		Name:      bsl.Name,
	}

	currentBSL := &velero.BackupStorageLocation{}

	if err := reader.Get(w.ctx, key, currentBSL); err != nil {
		if k8serrors.IsNotFound(err) {
			// BSL doesn't exist yet, create it with Primary config
			w.log.Info("BSL not found, creating with Primary config", "bsl", bsl.Name)

			return w.bslCreateOrUpdate(loc, labels, bsl)
		}

		return fmt.Errorf("failed to get BSL: %w", err)
	}

	// Check if BSL is already in ReadWrite mode with sync disabled
	if currentBSL.Spec.AccessMode == velero.BackupStorageLocationAccessModeReadWrite &&
		currentBSL.Spec.BackupSyncPeriod != nil &&
		currentBSL.Spec.BackupSyncPeriod.Duration == 0 {
		w.log.V(1).Info("BSL already in Primary mode (ReadWrite, sync disabled)", "bsl", bsl.Name)

		return nil
	}

	// BSL exists but not in Primary mode. This happens when transitioning from Secondary
	// to Primary. We need to wait for the last sync to complete before switching modes.
	if currentBSL.Status.LastSyncedTime == nil || currentBSL.Status.LastSyncedTime.IsZero() {
		w.log.Info("Waiting for BSL sync to complete before switching to Primary mode",
			"bsl", bsl.Name,
			"currentAccessMode", currentBSL.Spec.AccessMode,
			"lastSyncedTime", currentBSL.Status.LastSyncedTime)

		return kubeobjects.RequestProcessingErrorCreate("waiting for BSL sync before Primary backup")
	}

	// Last sync has completed, safe to update BSL to Primary mode
	w.log.Info("BSL sync completed, updating to Primary mode (ReadWrite, sync disabled)",
		"bsl", bsl.Name,
		"lastSyncedTime", currentBSL.Status.LastSyncedTime)

	return w.bslCreateOrUpdate(loc, labels, bsl)
}
