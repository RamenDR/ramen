// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package velero

import (
	"fmt"

	velero "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ramendr/ramen/internal/controller/util"
)

func (w objectWriter) bslCreateOrUpdate(s3Url string,
	s3BucketName string,
	s3RegionName string,
	s3KeyPrefix string,
	secretKeyRef *corev1.SecretKeySelector,
	caCertificates []byte,
	labels map[string]string,
	backupLocation *velero.BackupStorageLocation,
) error {
	op, err := controllerruntime.CreateOrUpdate(w.ctx, w.Client, backupLocation, func() error {
		backupLocation.TypeMeta = veleroTypeMeta("BackupStorageLocation")
		backupLocation.ObjectMeta.Labels = labels
		backupLocation.Spec = velero.BackupStorageLocationSpec{
			Provider: "aws",
			StorageType: velero.StorageType{
				ObjectStorage: &velero.ObjectStorageLocation{
					Bucket: s3BucketName,
					Prefix: s3KeyPrefix + path,
					CACert: caCertificates,
				},
			},
			Config: map[string]string{
				"region":            s3RegionName,
				"s3ForcePathStyle":  "true",
				"s3Url":             s3Url,
				"checksumAlgorithm": "",
			},
			Credential: secretKeyRef,
			// Disable Velero's backup-sync for this BSL.  Ramen explicitly manages the
			// Backup CR lifecycle (delete after each cycle); if sync were enabled Velero
			// would re-create deleted Backup CRs from S3, confusing the capture-resume logic
			// and inflating the recipeRetries counter.
			BackupSyncPeriod: &metav1.Duration{},
		}
		util.AddLabel(backupLocation, util.CreatedByRamenLabel, "true")

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create or update BackupStorageLocation: %w", err)
	}

	w.log.Info("backupstoragelocation reconciled", "operation", op, "name", backupLocation.GetName())

	return nil
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
