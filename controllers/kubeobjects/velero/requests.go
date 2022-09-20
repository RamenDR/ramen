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

// nolint: lll
// +kubebuilder:rbac:groups=velero.io,resources=backups,verbs=create;delete;deletecollection;get;list;patch;update;watch
// +kubebuilder:rbac:groups=velero.io,resources=backups/status,verbs=get
// +kubebuilder:rbac:groups=velero.io,resources=backupstoragelocations,verbs=create;delete;deletecollection;get;patch;update
// +kubebuilder:rbac:groups=velero.io,resources=deletebackuprequests,verbs=create;delete;get;patch;update
// +kubebuilder:rbac:groups=velero.io,resources=downloadrequests,verbs=create;delete;get;patch;update
// +kubebuilder:rbac:groups=velero.io,resources=restores,verbs=create;delete;deletecollection;get;list;patch;update;watch
// +kubebuilder:rbac:groups=velero.io,resources=restores/status,verbs=get

package velero

import (
	"context"
	"errors"

	"github.com/go-logr/logr"
	pkgerrors "github.com/pkg/errors"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/kubeobjects"
	velero "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	path         = "velero/"
	protectsPath = path + "backups/"
	recoversPath = path + "restores/"
)

type (
	BackupRequest  struct{ backup *velero.Backup }
	RestoreRequest struct{ restore *velero.Restore }
)

func (r BackupRequest) Object() client.Object   { return r.backup }
func (r RestoreRequest) Object() client.Object  { return r.restore }
func (r BackupRequest) Name() string            { return r.backup.Name }
func (r RestoreRequest) Name() string           { return r.restore.Name }
func (r BackupRequest) StartTime() metav1.Time  { return *r.backup.Status.StartTimestamp }
func (r RestoreRequest) StartTime() metav1.Time { return *r.restore.Status.StartTimestamp }
func (r BackupRequest) EndTime() metav1.Time    { return *r.backup.Status.CompletionTimestamp }
func (r RestoreRequest) EndTime() metav1.Time   { return *r.restore.Status.CompletionTimestamp }

type (
	BackupRequests  struct{ backups *velero.BackupList }
	RestoreRequests struct{ restores *velero.RestoreList }
)

func (r BackupRequests) Count() int  { return len(r.backups.Items) }
func (r RestoreRequests) Count() int { return len(r.restores.Items) }
func (r BackupRequests) Get(i int) kubeobjects.Request {
	return BackupRequest{&r.backups.Items[i]}
}

func (r RestoreRequests) Get(i int) kubeobjects.Request {
	return RestoreRequest{&r.restores.Items[i]}
}

type RequestsManager struct {}

func (RequestsManager) ProtectsPath() string { return protectsPath }
func (RequestsManager) RecoversPath() string { return recoversPath }

func (RequestsManager) ProtectRequestNew() kubeobjects.ProtectRequest {
	return BackupRequest{&velero.Backup{TypeMeta: backupTypeMeta()}}
}

func (RequestsManager) RecoverRequestNew() kubeobjects.RecoverRequest {
	return RestoreRequest{&velero.Restore{TypeMeta: restoreTypeMeta()}}
}

func (RequestsManager) ProtectRequestsGet(
	ctx context.Context,
	reader client.Reader,
	requestNamespaceName string,
	labels map[string]string,
) (kubeobjects.Requests, error) {
	requests := BackupRequests{&velero.BackupList{}}

	return requests, reader.List(ctx, requests.backups,
		client.InNamespace(requestNamespaceName),
		client.MatchingLabels(labels),
	)
}

func (RequestsManager) RecoverRequestsGet(
	ctx context.Context,
	reader client.Reader,
	requestNamespaceName string,
	labels map[string]string,
) (kubeobjects.Requests, error) {
	requests := RestoreRequests{&velero.RestoreList{}}

	return requests, reader.List(ctx, requests.restores,
		client.InNamespace(requestNamespaceName),
		client.MatchingLabels(labels),
	)
}

func (RequestsManager) ProtectRequestsDelete(
	ctx context.Context,
	writer client.Writer,
	requestNamespaceName string,
	labels map[string]string,
) error {
	options := []client.DeleteAllOfOption{
		client.InNamespace(requestNamespaceName),
		client.MatchingLabels(labels),
	}

	if err := writer.DeleteAllOf(ctx, &velero.Backup{}, options...); err != nil {
		return pkgerrors.Wrap(err, "backup requests delete")
	}

	return writer.DeleteAllOf(ctx, &velero.BackupStorageLocation{}, options...)
}

func (r RequestsManager) RecoverRequestsDelete(
	ctx context.Context,
	writer client.Writer,
	requestNamespaceName string,
	labels map[string]string,
) error {
	if err := writer.DeleteAllOf(ctx, &velero.Restore{},
		client.InNamespace(requestNamespaceName),
		client.MatchingLabels(labels),
	); err != nil {
		return pkgerrors.Wrap(err, "restore requests delete")
	}

	return r.ProtectRequestsDelete(ctx, writer, requestNamespaceName, labels)
}

func (RequestsManager) RecoverRequestCreate(
	ctx context.Context,
	writer client.Writer,
	reader client.Reader,
	log logr.Logger,
	s3Url string,
	s3BucketName string,
	s3RegionName string,
	s3KeyPrefix string,
	secretKeyRef *corev1.SecretKeySelector,
	sourceNamespaceName string,
	targetNamespaceName string,
	objectsSpec ramendrv1alpha1.KubeObjectsSpec,
	requestNamespaceName string,
	captureName string,
	recoverName string,
	labels map[string]string,
) (kubeobjects.RecoverRequest, error) {
	log.Info("Kube objects recover",
		"s3 url", s3Url,
		"s3 bucket", s3BucketName,
		"s3 region", s3RegionName,
		"s3 key prefix", s3KeyPrefix,
		"secret key ref", secretKeyRef,
		"source namespace", sourceNamespaceName,
		"target namespace", targetNamespaceName,
		"request namespace", requestNamespaceName,
		"capture name", captureName,
		"recover name", recoverName,
		"label set", labels,
	)

	restore, err := backupDummyCreateAndRestore(
		objectWriter{ctx: ctx, Writer: writer, log: log},
		reader,
		s3Url,
		s3BucketName,
		s3RegionName,
		s3KeyPrefix,
		secretKeyRef,
		sourceNamespaceName,
		targetNamespaceName,
		objectsSpec,
		requestNamespaceName,
		captureName,
		recoverName,
		labels,
	)

	return RestoreRequest{restore}, err
}

func backupDummyCreateAndRestore(
	w objectWriter,
	reader client.Reader,
	s3Url string,
	s3BucketName string,
	s3RegionName string,
	s3KeyPrefix string,
	secretKeyRef *corev1.SecretKeySelector,
	sourceNamespaceName string,
	targetNamespaceName string,
	objectsSpec ramendrv1alpha1.KubeObjectsSpec,
	requestNamespaceName string,
	backupName string,
	restoreName string,
	labels map[string]string,
) (*velero.Restore, error) {
	backupLocation, backup, err := backupCreate(
		types.NamespacedName{Namespace: requestNamespaceName, Name: backupName},
		w, reader, s3Url, s3BucketName, s3RegionName, s3KeyPrefix, secretKeyRef,
		backupSpecDummy(), sourceNamespaceName,
		labels,
	)
	if err != nil {
		return nil, err
	}

	return backupDummyStatusProcessAndRestore(
		backupLocation, backup, w, reader,
		sourceNamespaceName,
		targetNamespaceName,
		objectsSpec,
		restoreName,
		labels,
	)
}

func backupDummyStatusProcessAndRestore(
	backupLocation *velero.BackupStorageLocation,
	backup *velero.Backup,
	w objectWriter,
	reader client.Reader,
	sourceNamespaceName string,
	targetNamespaceName string,
	objectsSpec ramendrv1alpha1.KubeObjectsSpec,
	restoreName string,
	labels map[string]string,
) (*velero.Restore, error) {
	backupStatusLog(backup, w.log)

	switch backup.Status.Phase {
	case velero.BackupPhaseCompleted:
		fallthrough
	case velero.BackupPhasePartiallyFailed:
		fallthrough
	case velero.BackupPhaseFailed:
		return backupRestore(backupLocation, backup, w, reader, sourceNamespaceName, targetNamespaceName,
			objectsSpec,
			restoreName,
			labels,
		)
	case velero.BackupPhaseNew:
		fallthrough
	case velero.BackupPhaseInProgress:
		fallthrough
	case velero.BackupPhaseUploading:
		fallthrough
	case velero.BackupPhaseUploadingPartialFailure:
		fallthrough
	case velero.BackupPhaseDeleting:
		return nil, kubeobjects.RequestProcessingErrorCreate("backup" + string(backup.Status.Phase))
	case velero.BackupPhaseFailedValidation:
		return nil, backupRequestFailedDelete(backupLocation, backup, w)
	}

	return nil, kubeobjects.RequestProcessingErrorCreate("backup.status.phase absent")
}

func KubeObjectsCaptureRequestDelete(
	ctx context.Context,
	writer client.Writer,
	log logr.Logger,
	requestNamespaceName string,
	captureName string,
) error {
	objectMeta := metav1.ObjectMeta{Namespace: requestNamespaceName, Name: captureName}
	w := objectWriter{ctx: ctx, Writer: writer, log: log}

	if err := w.objectDelete(&velero.BackupStorageLocation{ObjectMeta: objectMeta}); err != nil {
		return err
	}

	return w.objectDelete(&velero.Backup{ObjectMeta: objectMeta})
}

func KubeObjectsCaptureDelete(
	ctx context.Context,
	writer client.Writer,
	reader client.Reader,
	log logr.Logger,
	s3Url string,
	s3BucketName string,
	s3RegionName string,
	s3KeyPrefix string,
	secretKeyRef *corev1.SecretKeySelector,
	sourceNamespaceName string,
	requestNamespaceName string,
	captureName string,
	labels map[string]string,
) error {
	w := objectWriter{ctx: ctx, Writer: writer, log: log}
	namespacedName := types.NamespacedName{Namespace: requestNamespaceName, Name: captureName}

	backupLocation, backup, err := backupCreate(
		namespacedName, w, reader, s3Url, s3BucketName, s3RegionName, s3KeyPrefix, secretKeyRef,
		backupSpecDummy(), sourceNamespaceName,
		labels,
	)
	if err != nil {
		return err
	}

	return backupAndBackupObjectsDelete(backupLocation, backup, namespacedName, w, reader)
}

func backupAndBackupObjectsDelete(
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
	if err := objectCreateAndGet(w, reader, backupDeletion); err != nil {
		// DeleteBackupRequest controller deletes DeleteBackupRequest on success
		if k8serrors.IsNotFound(err) {
			w.log.Info("Backup deletion request not found")

			return nil
		}

		return err
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
	w objectWriter,
	reader client.Reader,
	sourceNamespaceName string,
	targetNamespaceName string,
	objectsSpec ramendrv1alpha1.KubeObjectsSpec,
	restoreName string,
	labels map[string]string,
) (*velero.Restore, error) {
	restore := restore(backup.Namespace, restoreName, objectsSpec, backup.Name,
		sourceNamespaceName, targetNamespaceName, labels)
	if err := objectCreateAndGet(w, reader, restore); err != nil {
		return nil, err
	}

	return restoreStatusProcess(backupLocation, backup, restore, w)
}

func restoreStatusProcess(
	backupLocation *velero.BackupStorageLocation,
	backup *velero.Backup,
	restore *velero.Restore,
	w objectWriter,
) (*velero.Restore, error) {
	restoreStatusLog(restore, w.log)

	switch restore.Status.Phase {
	case velero.RestorePhaseCompleted:
		return restore, nil
	case velero.RestorePhaseNew:
		fallthrough
	case velero.RestorePhaseInProgress:
		return nil, kubeobjects.RequestProcessingErrorCreate("restore" + string(restore.Status.Phase))
	case velero.RestorePhaseFailed:
		fallthrough
	case velero.RestorePhaseFailedValidation:
		fallthrough
	case velero.RestorePhasePartiallyFailed:
		return nil, restoreRequestFailedDelete(backupLocation, backup, restore, w)
	default:
		return nil, kubeobjects.RequestProcessingErrorCreate("restore.status.phase absent")
	}
}

func restoreRequestFailedDelete(
	backupLocation *velero.BackupStorageLocation,
	backup *velero.Backup,
	restore *velero.Restore,
	w objectWriter,
) error {
	if err := w.restoreObjectsDelete(backupLocation, backup, restore); err != nil {
		return err
	}

	return errors.New("restore" + string(restore.Status.Phase) + "; request deleted")
}

func RestoreResultsGet(
	w objectWriter,
	reader client.Reader,
	namespacedName types.NamespacedName,
	results *string,
) error {
	download := download(namespacedName, velero.DownloadTargetKindRestoreResults)
	if err := objectCreateAndGet(w, reader, download); err != nil {
		return err
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

func (RequestsManager) ProtectRequestCreate(
	ctx context.Context,
	writer client.Writer,
	reader client.Reader,
	log logr.Logger,
	s3Url string,
	s3BucketName string,
	s3RegionName string,
	s3KeyPrefix string,
	secretKeyRef *corev1.SecretKeySelector,
	sourceNamespaceName string,
	objectsSpec ramendrv1alpha1.KubeObjectsSpec,
	requestNamespaceName string,
	captureName string,
	labels map[string]string,
) (kubeobjects.ProtectRequest, error) {
	log.Info("Kube objects protect",
		"s3 url", s3Url,
		"s3 bucket", s3BucketName,
		"s3 region", s3RegionName,
		"s3 key prefix", s3KeyPrefix,
		"secret key ref", secretKeyRef,
		"source namespace", sourceNamespaceName,
		"request namespace", requestNamespaceName,
		"capture name", captureName,
		"label set", labels,
	)

	backup, err := backupRealCreate(
		objectWriter{ctx: ctx, Writer: writer, log: log},
		reader,
		s3Url,
		s3BucketName,
		s3RegionName,
		s3KeyPrefix,
		secretKeyRef,
		sourceNamespaceName,
		objectsSpec,
		requestNamespaceName,
		captureName,
		labels,
	)

	return BackupRequest{backup}, err
}

func backupRealCreate(
	w objectWriter,
	reader client.Reader,
	s3Url string,
	s3BucketName string,
	s3RegionName string,
	s3KeyPrefix string,
	secretKeyRef *corev1.SecretKeySelector,
	sourceNamespaceName string,
	objectsSpec ramendrv1alpha1.KubeObjectsSpec,
	requestNamespaceName string,
	captureName string,
	labels map[string]string,
) (*velero.Backup, error) {
	backupLocation, backup, err := backupCreate(
		types.NamespacedName{Namespace: requestNamespaceName, Name: captureName},
		w, reader, s3Url, s3BucketName, s3RegionName, s3KeyPrefix, secretKeyRef,
		getBackupSpecFromObjectsSpec(objectsSpec),
		sourceNamespaceName,
		labels,
	)
	if err != nil {
		return nil, err
	}

	return backupRealStatusProcess(backupLocation, backup, w)
}

func getBackupSpecFromObjectsSpec(objectsSpec ramendrv1alpha1.KubeObjectsSpec) velero.BackupSpec {
	return velero.BackupSpec{
		IncludedResources:       objectsSpec.IncludedResources,
		ExcludedResources:       objectsSpec.ExcludedResources,
		LabelSelector:           objectsSpec.LabelSelector,
		TTL:                     metav1.Duration{}, // TODO: set default here
		IncludeClusterResources: objectsSpec.IncludeClusterResources,
		Hooks:                   velero.BackupHooks{},
		VolumeSnapshotLocations: []string{},
		DefaultVolumesToRestic:  new(bool),
		OrderedResources:        map[string]string{},
	}
}

func backupRealStatusProcess(
	backupLocation *velero.BackupStorageLocation,
	backup *velero.Backup,
	w objectWriter,
) (*velero.Backup, error) {
	backupStatusLog(backup, w.log)

	switch backup.Status.Phase {
	case velero.BackupPhaseCompleted:
		return backup, nil
	case velero.BackupPhaseNew:
		fallthrough
	case velero.BackupPhaseInProgress:
		fallthrough
	case velero.BackupPhaseUploading:
		fallthrough
	case velero.BackupPhaseUploadingPartialFailure:
		fallthrough
	case velero.BackupPhaseDeleting:
		return nil, kubeobjects.RequestProcessingErrorCreate("backup" + string(backup.Status.Phase))
	case velero.BackupPhaseFailedValidation:
		fallthrough
	case velero.BackupPhasePartiallyFailed:
		fallthrough
	case velero.BackupPhaseFailed:
		return nil, backupRequestFailedDelete(backupLocation, backup, w)
	}

	return nil, kubeobjects.RequestProcessingErrorCreate("backup.status.phase absent")
}

func backupRequestFailedDelete(
	backupLocation *velero.BackupStorageLocation,
	backup *velero.Backup,
	w objectWriter,
) error {
	if err := w.backupObjectsDelete(backupLocation, backup); err != nil {
		return err
	}

	return errors.New("backup" + string(backup.Status.Phase) + "; request deleted")
}

func (r BackupRequest) Deallocate(
	ctx context.Context,
	writer client.Writer,
	log logr.Logger,
) error {
	return objectWriter{ctx: ctx, Writer: writer, log: log}.objectDelete(r.backup)
}

func (r RestoreRequest) Deallocate(
	ctx context.Context,
	writer client.Writer,
	log logr.Logger,
) error {
	return objectWriter{ctx: ctx, Writer: writer, log: log}.objectDelete(r.restore)
}

type objectWriter struct {
	ctx context.Context
	client.Writer
	log logr.Logger
}

func backupCreate(
	backupNamespacedName types.NamespacedName,
	w objectWriter,
	reader client.Reader,
	s3Url string,
	s3BucketName string,
	s3RegionName string,
	s3KeyPrefix string,
	secretKeyRef *corev1.SecretKeySelector,
	backupSpec velero.BackupSpec,
	sourceNamespaceName string,
	labels map[string]string,
) (*velero.BackupStorageLocation, *velero.Backup, error) {
	backupLocation := backupLocation(backupNamespacedName,
		s3Url, s3BucketName, s3RegionName, s3KeyPrefix, secretKeyRef,
		labels,
	)
	if err := w.objectCreate(backupLocation); err != nil {
		return backupLocation, nil, err
	}

	backupSpec.StorageLocation = backupNamespacedName.Name
	backupSpec.IncludedNamespaces = []string{sourceNamespaceName}
	backupSpec.SnapshotVolumes = new(bool)
	backup := backup(backupNamespacedName, backupSpec, labels)

	return backupLocation, backup, objectCreateAndGet(w, reader, backup)
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

		w.log.Info("Object created previously", "type", o.GetObjectKind(), "name", o.GetName())
	} else {
		w.log.Info("Object created successfully", "type", o.GetObjectKind(), "name", o.GetName())
	}

	return nil
}

func (w objectWriter) objectDelete(o client.Object) error {
	if err := w.Delete(w.ctx, o); err != nil {
		if !k8serrors.IsNotFound(err) {
			return pkgerrors.Wrap(err, "object delete")
		}

		w.log.Info("Object deleted previously", "type", o.GetObjectKind(), "name", o.GetName())
	} else {
		w.log.Info("Object deleted successfully", "type", o.GetObjectKind(), "name", o.GetName())
	}

	return nil
}

func objectCreateAndGet(
	w objectWriter,
	reader client.Reader,
	o client.Object,
) error {
	if err := w.objectCreate(o); err != nil {
		return err
	}

	return reader.Get(w.ctx, types.NamespacedName{Namespace: o.GetNamespace(), Name: o.GetName()}, o)
}

func veleroTypeMeta(kind string) metav1.TypeMeta {
	return metav1.TypeMeta{
		APIVersion: velero.SchemeGroupVersion.String(),
		Kind:       kind,
	}
}

func backupTypeMeta() metav1.TypeMeta  { return veleroTypeMeta("Backup") }
func restoreTypeMeta() metav1.TypeMeta { return veleroTypeMeta("Restore") }

func backupLocation(namespacedName types.NamespacedName,
	s3Url, s3BucketName, s3RegionName, s3KeyPrefix string,
	secretKeyRef *corev1.SecretKeySelector,
	labels map[string]string,
) *velero.BackupStorageLocation {
	return &velero.BackupStorageLocation{
		TypeMeta: veleroTypeMeta("BackupStorageLocation"),
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespacedName.Namespace,
			Name:      namespacedName.Name,
			Labels:    labels,
		},
		Spec: velero.BackupStorageLocationSpec{
			Provider: "aws",
			StorageType: velero.StorageType{
				ObjectStorage: &velero.ObjectStorageLocation{
					Bucket: s3BucketName,
					Prefix: s3KeyPrefix + path,
				},
			},
			Config: map[string]string{
				"region":           s3RegionName,
				"s3ForcePathStyle": "true",
				"s3Url":            s3Url,
			},
			Credential: secretKeyRef,
		},
	}
}

func backup(namespacedName types.NamespacedName, backupSpec velero.BackupSpec,
	labels map[string]string,
) *velero.Backup {
	return &velero.Backup{
		TypeMeta: backupTypeMeta(),
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespacedName.Namespace,
			Name:      namespacedName.Name,
			Labels:    labels,
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

func restore(
	requestNamespaceName string,
	restoreName string,
	objectsSpec ramendrv1alpha1.KubeObjectsSpec,
	backupName string,
	sourceNamespaceName, targetNamespaceName string,
	labels map[string]string,
) *velero.Restore {
	return &velero.Restore{
		TypeMeta: restoreTypeMeta(),
		ObjectMeta: metav1.ObjectMeta{
			Namespace: requestNamespaceName,
			Name:      restoreName,
			Labels:    labels,
		},
		Spec: velero.RestoreSpec{
			BackupName:              backupName,
			IncludedNamespaces:      []string{sourceNamespaceName},
			IncludedResources:       objectsSpec.IncludedResources,
			ExcludedResources:       objectsSpec.ExcludedResources,
			LabelSelector:           objectsSpec.LabelSelector,
			IncludeClusterResources: objectsSpec.IncludeClusterResources,
			NamespaceMapping:        map[string]string{sourceNamespaceName: targetNamespaceName},
			// TODO: hooks?
			// TODO: restorePVs?
			// TODO: preserveNodePorts?
		},
	}
}

func backupDeletion(namespacedName types.NamespacedName) *velero.DeleteBackupRequest {
	return &velero.DeleteBackupRequest{
		TypeMeta: veleroTypeMeta("DeleteBackupRequest"),
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
		TypeMeta: veleroTypeMeta("DownloadRequest"),
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
	log.Info("Backup",
		"phase", backup.Status.Phase,
		"warnings", backup.Status.Warnings,
		"errors", backup.Status.Errors,
		// TODO v1.9.0 "failure", backup.Status.FailureReason,
		"validation errors", backup.Status.ValidationErrors,
	)

	if backup.Status.StartTimestamp != nil {
		log.Info("Backup", "start", backup.Status.StartTimestamp)
	}

	if backup.Status.CompletionTimestamp != nil {
		log.Info("Backup", "finish", backup.Status.CompletionTimestamp)
	}

	if backup.Status.Progress != nil {
		log.Info("Items",
			"to be backed up", backup.Status.Progress.TotalItems,
			"backed up", backup.Status.Progress.ItemsBackedUp,
		)
	}
}

func restoreStatusLog(restore *velero.Restore, log logr.Logger) {
	log.Info("Restore",
		"phase", restore.Status.Phase,
		"warnings", restore.Status.Warnings,
		"errors", restore.Status.Errors,
		"failure", restore.Status.FailureReason,
		"validation errors", restore.Status.ValidationErrors,
	)

	if restore.Status.StartTimestamp != nil {
		log.Info("Restore", "start", restore.Status.StartTimestamp)
	}

	if restore.Status.CompletionTimestamp != nil {
		log.Info("Restore", "finish", restore.Status.CompletionTimestamp)
	}

	if restore.Status.Progress != nil {
		log.Info("Items",
			"to be restored", restore.Status.Progress.TotalItems,
			"restored", restore.Status.Progress.ItemsRestored,
		)
	}
}

func backupDeletionStatusLog(backupDeletion *velero.DeleteBackupRequest, log logr.Logger) {
	log.Info("Backup deletion",
		"phase", backupDeletion.Status.Phase,
		"errors", backupDeletion.Status.Errors,
	)
}

func downloadStatusLog(download *velero.DownloadRequest, log logr.Logger) {
	log.Info("Download",
		"phase", download.Status.Phase,
		"url", download.Status.DownloadURL,
	)

	if download.Status.Expiration != nil {
		log.Info("Download", "expiration", download.Status.Expiration)
	}
}
