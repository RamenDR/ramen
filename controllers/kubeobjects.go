/*
Copyright 2022 The RamenDR authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// +kubebuilder:rbac:groups=velero.io,resources=backups,verbs=create;delete;get;patch;update
// +kubebuilder:rbac:groups=velero.io,resources=backupstoragelocations,verbs=create;delete;get;patch;update
// +kubebuilder:rbac:groups=velero.io,resources=deletebackuprequests,verbs=create;delete;get;patch;update
// +kubebuilder:rbac:groups=velero.io,resources=downloadrequests,verbs=create;delete;get;patch;update
// +kubebuilder:rbac:groups=velero.io,resources=restores,verbs=create;delete;get;patch;update

package controllers

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-logr/logr"
	pkgerrors "github.com/pkg/errors"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	velero "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func kubeObjectsRecover(
	ctx context.Context,
	writer client.Writer,
	reader client.Reader,
	log logr.Logger,
	s3Url string,
	s3BucketName string,
	s3KeyPrefix string,
	sourceNamespacedName types.NamespacedName,
	targetNamespaceName string,
	restoreSpec ramendrv1alpha1.ResourceRecoveryGroupSpec,
	index int,
	captureNumber int64,
) error {
	log.Info("kube objects recover",
		"s3 url", s3Url,
		"s3 bucket", s3BucketName,
		"s3 key prefix", s3KeyPrefix,
		"source namespace", sourceNamespacedName.Namespace,
		"target namespace", targetNamespaceName,
		"capture number", captureNumber,
	)

	// generate backup name
	restoreNamespacedName := getBackupNamespacedName(sourceNamespacedName.Name,
		sourceNamespacedName.Namespace, fmt.Sprintf("%d", index), captureNumber)

	backupNamespacedName := getBackupNamespacedName(sourceNamespacedName.Name,
		sourceNamespacedName.Namespace, restoreSpec.BackupName, captureNumber)

	return backupDummyCreateAndIfAlreadyExistsThenRestore(
		restoreNamespacedName,
		backupNamespacedName,
		objectWriter{ctx: ctx, Writer: writer, log: log},
		reader,
		s3Url,
		s3BucketName,
		s3KeyPrefix,
		sourceNamespacedName.Namespace,
		targetNamespaceName,
		restoreSpec,
		captureNumber,
	)
}

func backupDummyCreateAndIfAlreadyExistsThenRestore(
	restoreNamespacedName types.NamespacedName,
	backupNamespacedName types.NamespacedName,
	w objectWriter,
	reader client.Reader,
	s3Url string,
	s3BucketName string,
	s3KeyPrefix string,
	sourceNamespaceName string,
	targetNamespaceName string,
	restoreSpec ramendrv1alpha1.ResourceRecoveryGroupSpec,
	captureNumber int64,
) error {
	backupLocation, backup, err := w.backupCreate(
		backupNamespacedName, s3Url, s3BucketName, s3KeyPrefix, backupSpecDummy(), sourceNamespaceName, captureNumber,
	)
	if err != nil {
		return err
	}

	backupObjectNamespacedName := types.NamespacedName{
		Name:      backupNamespacedName.Name,
		Namespace: VeleroNamespaceNameDefault,
	}
	if err := reader.Get(w.ctx, backupObjectNamespacedName, backup); err != nil {
		return pkgerrors.Wrap(err, "dummy backup get")
	}

	// checks status of dummy backup and possibly starts restore
	return backupDummyStatusProcess(backupLocation, backup, backupNamespacedName,
		restoreNamespacedName,
		w, reader,
		sourceNamespaceName,
		targetNamespaceName,
		restoreSpec,
	)
}

func backupDummyStatusProcess(
	backupLocation *velero.BackupStorageLocation,
	backup *velero.Backup,
	backupNamespacedName types.NamespacedName,
	restoreNamespacedName types.NamespacedName,
	w objectWriter,
	reader client.Reader,
	sourceNamespaceName string,
	targetNamespaceName string,
	restoreSpec ramendrv1alpha1.ResourceRecoveryGroupSpec,
) error {
	backupStatusLog(backup, w.log)

	switch backup.Status.Phase {
	case velero.BackupPhaseCompleted:
		// deletes backup and backup object TODO delete bsl also after both are gone?
		// disabled so successful backup status from existing object doesn't delete contents
		fallthrough
	case velero.BackupPhasePartiallyFailed:
		fallthrough
	case velero.BackupPhaseFailed:
		// TODO if failed because backup already exists
		w.log.Info(fmt.Sprintf("backupDummyStatusProcess: BackupPhaseFailed on '%s'. Creating restore '%s'",
			backupNamespacedName.Name, restoreNamespacedName.Name))

		return backupRestore(backupLocation, backup, restoreNamespacedName, w, reader,
			sourceNamespaceName, targetNamespaceName, restoreSpec)
	case velero.BackupPhaseNew:
		fallthrough
	case velero.BackupPhaseInProgress:
		fallthrough
	case velero.BackupPhaseUploading:
		fallthrough
	case velero.BackupPhaseUploadingPartialFailure:
		fallthrough
	case velero.BackupPhaseDeleting:
		return errors.New("temporary: backup" + string(backup.Status.Phase))
	case velero.BackupPhaseFailedValidation:
		return errors.New("permanent: backup" + string(backup.Status.Phase))
	}

	return errors.New("temporary: backup.status.phase absent")
}

func BackupAndBackupObjectsDelete(
	backupLocation *velero.BackupStorageLocation,
	backup *velero.Backup,
	namespacedName types.NamespacedName,
	w objectWriter,
	reader client.Reader,
) error {
	if err := backupDelete(namespacedName, w, reader); err != nil {
		return err
	}

	return w.backupObjectsDelete(backupLocation, backup)
}

func backupDelete(
	namespacedName types.NamespacedName,
	w objectWriter,
	reader client.Reader,
) error {
	backupDeletion := backupDeletion(namespacedName)
	if err := w.objectCreate(backupDeletion); err != nil {
		return err
	}

	if err := reader.Get(w.ctx, namespacedName, backupDeletion); err != nil {
		return pkgerrors.Wrap(err, "backup deletion get")
	}

	backupDeletionStatusLog(backupDeletion, w.log)

	switch backupDeletion.Status.Phase {
	case velero.DeleteBackupRequestPhaseNew:
		fallthrough
	case velero.DeleteBackupRequestPhaseInProgress:
		return errors.New("temporary: backup deletion " + string(backupDeletion.Status.Phase))
	case velero.DeleteBackupRequestPhaseProcessed:
		return w.objectDelete(backupDeletion)
	default:
		return errors.New("temporary: backup deletion status.phase absent")
	}
}

func backupRestore(
	backupLocation *velero.BackupStorageLocation,
	backup *velero.Backup,
	namespacedName types.NamespacedName,
	w objectWriter,
	reader client.Reader,
	sourceNamespaceName string,
	targetNamespaceName string,
	restoreSpec ramendrv1alpha1.ResourceRecoveryGroupSpec,
) error {
	restore := restore(namespacedName, sourceNamespaceName, targetNamespaceName, restoreSpec, backup.Name)
	if err := w.objectCreate(restore); err != nil {
		return err
	}

	restoreObjectNamespacedName := types.NamespacedName{Name: namespacedName.Name, Namespace: VeleroNamespaceNameDefault}
	if err := reader.Get(w.ctx, restoreObjectNamespacedName, restore); err != nil {
		return pkgerrors.Wrap(err, "restore get")
	}

	return restoreStatusProcess(backupLocation, backup, restore, w)
}

func restoreStatusProcess(
	backupLocation *velero.BackupStorageLocation,
	backup *velero.Backup,
	restore *velero.Restore,
	w objectWriter,
) error {
	restoreStatusLog(restore, w.log)

	switch restore.Status.Phase {
	case velero.RestorePhaseNew:
		fallthrough
	case velero.RestorePhaseInProgress:
		return errors.New("temporary: restore" + string(restore.Status.Phase))
	case velero.RestorePhaseFailed:
		backupObjectPathName := backupLocation.Spec.StorageType.ObjectStorage.Prefix +
			"backups/" + backup.Name + "/" + backup.Name + ".tar.gz"
		if strings.HasPrefix(restore.Status.FailureReason,
			"error downloading backup: error copying Backup to temp file: rpc error: "+
				"code = Unknown desc = error getting object "+backupObjectPathName+": "+
				"NoSuchKey: The specified key does not exist.\n\tstatus code: 404, request id: ",
		) {
			w.log.Info("backup absent", "path", backupObjectPathName)

			return w.restoreObjectsDelete(backupLocation, backup, restore)
		}

		fallthrough
	case velero.RestorePhaseFailedValidation:
		fallthrough
	case velero.RestorePhasePartiallyFailed:
		return errors.New("permanent: restore" + string(restore.Status.Phase))
	case velero.RestorePhaseCompleted:
		// return w.restoreObjectsDelete(backupLocation, backup, restore)
		return nil // keep objects around until backup is done (TODO: delete)
	default:
		return errors.New("temporary: restore.status.phase absent")
	}
}

func RestoreResultsGet(
	w objectWriter,
	reader client.Reader,
	namespacedName types.NamespacedName,
	results *string,
) error {
	download := download(namespacedName, velero.DownloadTargetKindRestoreResults)
	if err := w.objectCreate(download); err != nil {
		return err
	}

	if err := reader.Get(w.ctx, namespacedName, download); err != nil {
		return pkgerrors.Wrap(err, "restore results download get")
	}

	downloadStatusLog(download, w.log)

	switch download.Status.Phase {
	case velero.DownloadRequestPhaseNew:
		return errors.New("temporary: download " + string(download.Status.Phase))
	case velero.DownloadRequestPhaseProcessed:
		break
	default:
		return errors.New("temporary: download status.phase absent")
	}

	*results = download.Status.DownloadURL // TODO dereference

	return w.objectDelete(download)
}

func kubeObjectsProtect(
	ctx context.Context,
	writer client.Writer,
	reader client.Reader,
	log logr.Logger,
	s3Url string,
	s3BucketName string,
	s3KeyPrefix string,
	sourceNamespacedName types.NamespacedName,
	spec ramendrv1alpha1.ResourceCaptureGroupSpec,
	captureNumber int64,
) error {
	log.Info("kube objects protect",
		"s3 url", s3Url,
		"s3 bucket", s3BucketName,
		"s3 key prefix", s3KeyPrefix,
		"source namespace", sourceNamespacedName.Namespace,
		"capture number", captureNumber,
	)

	backupNamespacedName := getBackupNamespacedName(sourceNamespacedName.Name,
		sourceNamespacedName.Namespace, spec.Name, captureNumber)

	return backupRealCreate(
		backupNamespacedName,
		objectWriter{ctx: ctx, Writer: writer, log: log},
		reader,
		s3Url,
		s3BucketName,
		s3KeyPrefix,
		sourceNamespacedName.Namespace,
		spec,
		captureNumber,
	)
}

func backupRealCreate(
	namespacedName types.NamespacedName,
	w objectWriter,
	reader client.Reader,
	s3Url string,
	s3BucketName string,
	s3KeyPrefix string,
	sourceNamespaceName string,
	captureInstance ramendrv1alpha1.ResourceCaptureGroupSpec,
	captureNumber int64,
) error {
	backupSpec := getBackupSpecFromCaptureInstance(captureInstance, namespacedName.Namespace)

	backupNamespacedName := types.NamespacedName{Name: namespacedName.Name, Namespace: VeleroNamespaceNameDefault}

	backupLocation, backup, err := w.backupCreate(
		backupNamespacedName, s3Url, s3BucketName, s3KeyPrefix, *backupSpec, sourceNamespaceName, captureNumber,
	)
	if err != nil {
		return err
	}

	if err := reader.Get(w.ctx, backupNamespacedName, backup); err != nil {
		return pkgerrors.Wrap(err, "backup get")
	}

	return backupRealStatusProcess(backupLocation, backup, w)
}

func getBackupSpecFromCaptureInstance(instance ramendrv1alpha1.ResourceCaptureGroupSpec,
	namespace string) *velero.BackupSpec {
	return &velero.BackupSpec{
		IncludedNamespaces:      []string{namespace},
		IncludedResources:       instance.IncludedResources,
		ExcludedResources:       instance.ExcludedResources,
		LabelSelector:           instance.LabelSelector,
		TTL:                     metav1.Duration{}, // TODO: set default here
		IncludeClusterResources: instance.IncludeClusterResources,
		Hooks:                   velero.BackupHooks{},
		// StorageLocation:         "default", // defaults to "default" in standard velero installation
		VolumeSnapshotLocations: []string{},
		DefaultVolumesToRestic:  new(bool),
		OrderedResources:        map[string]string{},
	}
}

func backupRealStatusProcess(
	backupLocation *velero.BackupStorageLocation,
	backup *velero.Backup,
	w objectWriter,
) error {
	backupStatusLog(backup, w.log)

	switch backup.Status.Phase {
	case velero.BackupPhaseCompleted:
		// return w.backupObjectsDelete(backupLocation, backup)
		return nil // leave sub-backup objects around until the entire operation is complete
	case velero.BackupPhaseNew:
		fallthrough
	case velero.BackupPhaseInProgress:
		fallthrough
	case velero.BackupPhaseUploading:
		fallthrough
	case velero.BackupPhaseUploadingPartialFailure:
		fallthrough
	case velero.BackupPhaseDeleting:
		return errors.New("temporary: backup" + string(backup.Status.Phase))
	case velero.BackupPhaseFailedValidation:
		fallthrough
	case velero.BackupPhasePartiallyFailed:
		fallthrough
	case velero.BackupPhaseFailed:
		// return errors.New("permanent: backup" + string(backup.Status.Phase))
		return backupRetry(backupLocation, backup, w)
	}

	return errors.New("temporary: backup.status.phase absent")
}

// complete here could mean success or failure; but processing is over.
func backupIsDone(ctx context.Context, apiReader client.Reader, writer objectWriter,
	namespacedName types.NamespacedName) (bool, error) {
	backup, err := getBackupObject(ctx, apiReader, namespacedName)
	if err != nil {
		return true, err
	}

	backupLocationNamespacedName := types.NamespacedName{
		Name:      backup.Spec.StorageLocation,
		Namespace: namespacedName.Namespace,
	}

	backupStorageLocation, err := getBackupStorageLocationObject(ctx, apiReader, backupLocationNamespacedName)
	if err != nil {
		return false, err
	}

	err = backupRealStatusProcess(backupStorageLocation, backup, writer)
	if err != nil {
		return false, err
	}

	completed := backup.Status.Phase == velero.BackupPhaseCompleted ||
		backup.Status.Phase == velero.BackupPhasePartiallyFailed ||
		backup.Status.Phase == velero.BackupPhaseFailed

	return completed, nil
}

func restoreIsDone(ctx context.Context, apiReader client.Reader, writer objectWriter,
	namespacedName types.NamespacedName) (bool, error) {
	restore, err := getRestoreObject(ctx, apiReader, namespacedName)
	if err != nil {
		return true, err
	}

	// need backupName, backupStorageLocation for process; get BSL from backup object
	backupName := restore.Spec.BackupName

	backup, err := getBackupObject(ctx, apiReader,
		types.NamespacedName{Name: backupName, Namespace: namespacedName.Namespace})
	if err != nil {
		return true, pkgerrors.Wrap(err, "failed to get backup object")
	}

	backupLocationNamespacedName := types.NamespacedName{
		Name:      backup.Spec.StorageLocation,
		Namespace: namespacedName.Namespace,
	}

	backupStorageLocation, err := getBackupStorageLocationObject(ctx, apiReader, backupLocationNamespacedName)
	if err != nil {
		return false, err
	}

	completed := backup.Status.Phase == velero.BackupPhaseCompleted ||
		backup.Status.Phase == velero.BackupPhasePartiallyFailed ||
		backup.Status.Phase == velero.BackupPhaseFailed

	err = restoreStatusProcess(backupStorageLocation, backup, restore, writer)
	if err != nil {
		return false, nil // need better way to handle problematic non-terminal status besides errors
	}

	return completed, nil
}

func getRestoreObject(ctx context.Context, apiReader client.Reader, namespacedName types.NamespacedName) (
	*velero.Restore, error) {
	restore := &velero.Restore{}

	return restore, apiReader.Get(ctx, namespacedName, restore)
}

func getBackupObject(ctx context.Context, apiReader client.Reader, namespacedName types.NamespacedName) (
	*velero.Backup, error) {
	backup := &velero.Backup{}

	return backup, apiReader.Get(ctx, namespacedName, backup)
}

func getBackupStorageLocationObject(ctx context.Context, apiReader client.Reader, namespacedName types.NamespacedName) (
	*velero.BackupStorageLocation, error) {
	backupStorageLocation := &velero.BackupStorageLocation{}

	return backupStorageLocation, apiReader.Get(ctx, namespacedName, backupStorageLocation)
}

func backupRetry(
	backupLocation *velero.BackupStorageLocation,
	backup *velero.Backup,
	w objectWriter,
) error {
	if err := w.backupObjectsDelete(backupLocation, backup); err != nil {
		return err
	}

	return errors.New("backup retry")
}

func namespacedName(namespaceName, s3Url, s3BucketName string,
	captureNumber int64,
) (types.NamespacedName, error) {
	if true {
		return types.NamespacedName{Namespace: namespaceName, Name: strconv.FormatInt(captureNumber, 10)}, nil
	}

	url, err := url.Parse(s3Url)
	if err != nil {
		return types.NamespacedName{}, pkgerrors.Wrap(err, "s3 url parse")
	}

	// https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#dns-label-names
	// https://docs.aws.amazon.com/AmazonS3/latest/userguide/WebsiteEndpoints.html
	// https://docs.aws.amazon.com/AmazonS3/latest/userguide/bucketnamingrules.html
	return types.NamespacedName{
			Namespace: namespaceName,
			Name: strings.ReplaceAll(url.Hostname(), ".", "-") +
				"-" + url.Port() +
				"-" + strings.ReplaceAll(s3BucketName, ".", "-"),
		},
		nil
}

type objectWriter struct {
	client.Writer
	ctx context.Context
	log logr.Logger
}

func (w objectWriter) backupCreate(
	backupNamespacedName types.NamespacedName,
	s3Url string,
	s3BucketName string,
	s3KeyPrefix string,
	backupSpec velero.BackupSpec,
	sourceNamespaceName string,
	captureNumber int64,
) (*velero.BackupStorageLocation, *velero.Backup, error) {
	backupStorageLocationNamespacedName, err := namespacedName(VeleroNamespaceNameDefault, s3Url,
		s3BucketName, captureNumber)
	if err != nil {
		return nil, nil, err
	}

	backupLocation := backupLocation(backupStorageLocationNamespacedName, s3Url, s3BucketName, s3KeyPrefix)
	if err := w.objectCreate(backupLocation); err != nil {
		return backupLocation, nil, err
	}

	backupSpec.IncludedNamespaces = []string{sourceNamespaceName}

	backup := backup(backupNamespacedName, backupSpec)
	if err := w.objectCreate(backup); err != nil {
		return backupLocation, backup, err
	}

	return backupLocation, backup, nil
}

func (w objectWriter) backupObjectsDelete(
	backupLocation *velero.BackupStorageLocation,
	backup *velero.Backup,
) error {
	if err := w.objectDelete(backup); err != nil {
		return err
	}

	return w.objectDelete(backupLocation)
}

func (w objectWriter) restoreObjectsDelete(
	backupLocation *velero.BackupStorageLocation,
	backup *velero.Backup,
	restore *velero.Restore,
) error {
	if err := w.objectDelete(restore); err != nil {
		return err
	}

	return w.backupObjectsDelete(backupLocation, backup)
}

func (w objectWriter) objectCreate(o client.Object) error {
	if err := w.Create(w.ctx, o); err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return pkgerrors.Wrap(err, "object create")
		}

		w.log.Info("object created previously", "type", o.GetObjectKind(), "name", o.GetName())
	} else {
		w.log.Info("object created successfully", "type", o.GetObjectKind(), "name", o.GetName())
	}

	return nil
}

func (w objectWriter) objectDelete(o client.Object) error {
	if err := w.Delete(w.ctx, o); err != nil {
		if !k8serrors.IsNotFound(err) {
			return pkgerrors.Wrap(err, "object delete")
		}

		w.log.Info("object deleted previously", "type", o.GetObjectKind(), "name", o.GetName())
	} else {
		w.log.Info("object deleted successfully", "type", o.GetObjectKind(), "name", o.GetName())
	}

	return nil
}

const (
	secretName    = "s3secret"
	secretKeyName = "aws"
)

func backupLocation(namespacedName types.NamespacedName, s3Url, bucketName, s3KeyPrefix string,
) *velero.BackupStorageLocation {
	return &velero.BackupStorageLocation{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "velero.io/v1",
			Kind:       "BackupStorageLocation",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespacedName.Namespace,
			Name:      namespacedName.Name,
		},
		Spec: velero.BackupStorageLocationSpec{
			Provider: "aws",
			StorageType: velero.StorageType{
				ObjectStorage: &velero.ObjectStorageLocation{
					Bucket: bucketName,
					Prefix: s3KeyPrefix + "velero/",
				},
			},
			Config: map[string]string{
				"region":           "us-east-1", // TODO input
				"s3ForcePathStyle": "true",
				"s3Url":            s3Url,
			},
			Credential: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: secretName,
				},
				Key: secretKeyName,
			},
		},
	}
}

func backup(namespacedName types.NamespacedName, backupSpec velero.BackupSpec) *velero.Backup {
	return &velero.Backup{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "velero.io/v1",
			Kind:       "Backup",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: VeleroNamespaceNameDefault, // must be created in velero/OADP namespace
			Name:      namespacedName.Name,
		},
		Spec: backupSpec,
	}
}

func backupSpecDummy() velero.BackupSpec {
	return velero.BackupSpec{
		IncludedResources: []string{"secrets"},
		LabelSelector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"dummyKey": "dummyValue",
			},
		},
	}
}

func restore(namespacedName types.NamespacedName, sourceNamespaceName, targetNamespaceName string,
	spec ramendrv1alpha1.ResourceRecoveryGroupSpec, backupName string) *velero.Restore {
	restore := &velero.Restore{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "velero.io/v1",
			Kind:       "Restore",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: VeleroNamespaceNameDefault, // can only create backup/restore objects in Velero/OADP namespace
			Name:      namespacedName.Name,
		},
		Spec: velero.RestoreSpec{
			BackupName:              backupName,
			IncludedNamespaces:      []string{namespacedName.Namespace},
			IncludedResources:       spec.IncludedResources,
			ExcludedResources:       spec.ExcludedResources,
			IncludeClusterResources: spec.IncludeClusterResources,
			NamespaceMapping:        map[string]string{sourceNamespaceName: targetNamespaceName},
			// TODO: hooks?
			// TODO: restorePVs?
			// TODO: preserveNodePorts?
			// TODO: labelSelectors? blank label selectors don't match properly; include manually
		},
	}

	return restore
}

func backupDeletion(namespacedName types.NamespacedName) *velero.DeleteBackupRequest {
	return &velero.DeleteBackupRequest{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "velero.io/v1",
			Kind:       "DeleteBackupRequest",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespacedName.Namespace,
			Name:      namespacedName.Name,
		},
		Spec: velero.DeleteBackupRequestSpec{
			BackupName: namespacedName.Name,
		},
	}
}

func download(namespacedName types.NamespacedName, kind velero.DownloadTargetKind) *velero.DownloadRequest {
	return &velero.DownloadRequest{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "velero.io/v1",
			Kind:       "DeleteBackupRequest",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespacedName.Namespace,
			Name:      namespacedName.Name + string(kind),
		},
		Spec: velero.DownloadRequestSpec{
			Target: velero.DownloadTarget{
				Kind: kind,
				Name: namespacedName.Name,
			},
		},
	}
}

func backupStatusLog(backup *velero.Backup, log logr.Logger) {
	log.Info("backup",
		"phase", backup.Status.Phase,
		"warnings", backup.Status.Warnings,
		"errors", backup.Status.Errors,
		// TODO v1.9.0 "failure", backup.Status.FailureReason,
	)

	if backup.Status.StartTimestamp != nil {
		log.Info("backup", "start", backup.Status.StartTimestamp)
	}

	if backup.Status.CompletionTimestamp != nil {
		log.Info("backup", "finish", backup.Status.CompletionTimestamp)
	}

	if backup.Status.Progress != nil {
		log.Info("items",
			"to be backed up", backup.Status.Progress.TotalItems,
			"backed up", backup.Status.Progress.ItemsBackedUp,
		)
	}
}

func restoreStatusLog(restore *velero.Restore, log logr.Logger) {
	log.Info("restore",
		"phase", restore.Status.Phase,
		"warnings", restore.Status.Warnings,
		"errors", restore.Status.Errors,
		"failure", restore.Status.FailureReason,
	)

	if restore.Status.StartTimestamp != nil {
		log.Info("restore", "start", restore.Status.StartTimestamp)
	}

	if restore.Status.CompletionTimestamp != nil {
		log.Info("restore", "finish", restore.Status.CompletionTimestamp)
	}

	if restore.Status.Progress != nil {
		log.Info("items",
			"to be restored", restore.Status.Progress.TotalItems,
			"restored", restore.Status.Progress.ItemsRestored,
		)
	}
}

func backupDeletionStatusLog(backupDeletion *velero.DeleteBackupRequest, log logr.Logger) {
	log.Info("backup deletion",
		"phase", backupDeletion.Status.Phase,
		"errors", len(backupDeletion.Status.Errors),
	)

	for i, message := range backupDeletion.Status.Errors {
		log.Info("backup deletion error", "number", i, "message", message)
	}
}

func downloadStatusLog(download *velero.DownloadRequest, log logr.Logger) {
	log.Info("download",
		"phase", download.Status.Phase,
		"url", download.Status.DownloadURL,
	)

	if download.Status.Expiration != nil {
		log.Info("expiration", download.Status.Expiration)
	}
}
