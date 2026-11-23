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

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	"github.com/ramendr/ramen/internal/controller/util"
)

const (
	// BackupSyncPeriodRestore is the sync period used during restore operations.
	// Set to 1 second for fast failover - Velero will sync backup CRs from S3
	// every second, minimizing the time to detect new backups during restore.
	BackupSyncPeriodRestore       = 1 * time.Second
	LastSyncEnabledTimeAnnotation = "ramendr.openshift.io/sync-enabled-time"
)

func (w objectWriter) bslCreateOrUpdate(
	loc kubeobjects.StorageLocation,
	labels map[string]string,
	backupLocation *velero.BackupStorageLocation,
) error {
	op, err := controllerruntime.CreateOrUpdate(w.ctx, w.Client, backupLocation, func() error {
		alreadyInRestoreMode := backupLocation.Spec.BackupSyncPeriod != nil &&
			backupLocation.Spec.BackupSyncPeriod.Duration == BackupSyncPeriodRestore
		backupLocation.TypeMeta = veleroTypeMeta("BackupStorageLocation")
		backupLocation.ObjectMeta.Labels = labels
		backupLocation.Spec = BSLSpec(loc)
		util.AddLabel(backupLocation, util.CreatedByRamenLabel, "true")

		// Track when we enable sync for restore to verify subsequent syncs are fresh
		if loc.ReplicationState == kubeobjects.ReplicationStateSecondaryRestore {
			if !alreadyInRestoreMode {
				// Transitioning INTO secondary-restore → NEW restore
				util.AddAnnotation(backupLocation, LastSyncEnabledTimeAnnotation, time.Now().Format(time.RFC3339))
			}
		}

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
	// - Secondary: ReadOnly access, BackupSyncPeriod=0s (no sync in steadystate)
	// - Secondary-restore: ReadOnly access, BackupSyncPeriod=1s (sync during restore only)
	var (
		accessMode       velero.BackupStorageLocationAccessMode
		backupSyncPeriod *metav1.Duration
	)

	switch loc.ReplicationState {
	case string(ramen.Secondary):
		accessMode = velero.BackupStorageLocationAccessModeReadOnly
		backupSyncPeriod = &metav1.Duration{} // 0s - no sync in steady state
	case kubeobjects.ReplicationStateSecondaryRestore:
		// Special mode: ReadOnly with sync enabled for restore operations
		accessMode = velero.BackupStorageLocationAccessModeReadOnly
		backupSyncPeriod = &metav1.Duration{Duration: BackupSyncPeriodRestore}
	default:
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
	currentBSL, err := w.getCurrentBSL(bsl)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return w.createBSLForPrimary(bsl, loc, labels)
		}

		return err
	}

	if w.isBSLInPrimaryMode(currentBSL) {
		return nil
	}

	if w.needsToWaitForSync(currentBSL) {
		return w.waitForBSLSync(bsl.Name, currentBSL.Spec.AccessMode)
	}

	return w.updateBSLToPrimaryMode(bsl, loc, labels, currentBSL)
}

func (w objectWriter) getCurrentBSL(bsl *velero.BackupStorageLocation) (*velero.BackupStorageLocation, error) {
	reader, ok := w.Client.(client.Reader)
	if !ok {
		return nil, fmt.Errorf("writer does not implement client.Reader interface")
	}

	key := client.ObjectKey{Namespace: bsl.Namespace, Name: bsl.Name}
	currentBSL := &velero.BackupStorageLocation{}

	if err := reader.Get(w.ctx, key, currentBSL); err != nil {
		return nil, err
	}

	return currentBSL, nil
}

func (w objectWriter) createBSLForPrimary(
	bsl *velero.BackupStorageLocation,
	loc kubeobjects.StorageLocation,
	labels map[string]string,
) error {
	w.log.Info("BSL not found, creating with Primary config", "bsl", bsl.Name)

	return w.bslCreateOrUpdate(loc, labels, bsl)
}

func (w objectWriter) isBSLInPrimaryMode(bsl *velero.BackupStorageLocation) bool {
	if bsl.Spec.AccessMode == velero.BackupStorageLocationAccessModeReadWrite &&
		bsl.Spec.BackupSyncPeriod != nil &&
		bsl.Spec.BackupSyncPeriod.Duration == 0 {
		w.log.V(1).Info("BSL already in Primary mode (ReadWrite, sync disabled)", "bsl", bsl.Name)

		return true
	}

	return false
}

func (w objectWriter) needsToWaitForSync(bsl *velero.BackupStorageLocation) bool {
	needsSync := bsl.Spec.BackupSyncPeriod != nil && bsl.Spec.BackupSyncPeriod.Duration > 0
	if !needsSync {
		return false
	}

	// No lastSyncedTime yet
	if bsl.Status.LastSyncedTime == nil || bsl.Status.LastSyncedTime.IsZero() {
		return true
	}

	// Check if sync happened AFTER we enabled it for this restore
	syncEnabledTimeStr, ok := bsl.Annotations[LastSyncEnabledTimeAnnotation]
	if !ok {
		return false // No tracking annotation, assume old behavior
	}

	syncEnabledTime, err := time.Parse(time.RFC3339, syncEnabledTimeStr)
	if err != nil {
		w.log.Info("Failed to parse sync-enabled-time annotation, waiting for sync to be safe",
			"bsl", bsl.Name,
			"annotation", syncEnabledTimeStr,
			"error", err)

		return true // Parse error, wait to be safe
	}

	// Wait if lastSyncedTime is before we enabled sync
	return bsl.Status.LastSyncedTime.Time.Before(syncEnabledTime)
}

func (w objectWriter) updateBSLToPrimaryMode(
	bsl *velero.BackupStorageLocation,
	loc kubeobjects.StorageLocation,
	labels map[string]string,
	currentBSL *velero.BackupStorageLocation,
) error {
	logArgs := []any{"bsl", bsl.Name}
	if currentBSL.Status.LastSyncedTime != nil {
		logArgs = append(logArgs, "lastSyncedTime", currentBSL.Status.LastSyncedTime.Time)
	}

	w.log.Info("Updating BSL to Primary mode (ReadWrite, sync disabled)", logArgs...)

	return w.bslCreateOrUpdate(loc, labels, bsl)
}

func (w objectWriter) waitForBSLSync(
	bslName string,
	currentAccessMode velero.BackupStorageLocationAccessMode,
) error {
	w.log.Info("Waiting for BSL sync to complete before switching to Primary mode",
		"bsl", bslName,
		"currentAccessMode", currentAccessMode)

	return kubeobjects.RequestProcessingErrorCreate("waiting for BSL sync before Primary backup")
}
