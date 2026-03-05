// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

//nolint:lll
// +kubebuilder:rbac:groups=velero.io,resources=backups,verbs=create;delete;deletecollection;get;list;patch;update;watch
// +kubebuilder:rbac:groups=velero.io,resources=backups/status,verbs=get
// +kubebuilder:rbac:groups=velero.io,resources=backupstoragelocations,verbs=create;delete;deletecollection;get;list;patch;update;watch
// +kubebuilder:rbac:groups=velero.io,resources=restores,verbs=create;delete;deletecollection;get;list;patch;update;watch
// +kubebuilder:rbac:groups=velero.io,resources=restores/status,verbs=get

package velero

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	velero "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	"github.com/ramendr/ramen/internal/controller/util"
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

func (r BackupRequest) Object() client.Object  { return r.backup }
func (r RestoreRequest) Object() client.Object { return r.restore }
func (r BackupRequest) Name() string           { return r.backup.Name }
func (r RestoreRequest) Name() string          { return r.restore.Name }

func (r BackupRequest) StartTime() metav1.Time {
	if r.backup.Status.StartTimestamp != nil {
		return *r.backup.Status.StartTimestamp
	}

	if r.backup.Status.CompletionTimestamp != nil {
		return *r.backup.Status.CompletionTimestamp
	}

	return metav1.Now()
}

func (r RestoreRequest) StartTime() metav1.Time {
	if r.restore.Status.StartTimestamp != nil {
		return *r.restore.Status.StartTimestamp
	}

	if r.restore.Status.CompletionTimestamp != nil {
		return *r.restore.Status.CompletionTimestamp
	}

	return metav1.Now()
}

func (r BackupRequest) EndTime() metav1.Time {
	if r.backup.Status.CompletionTimestamp != nil {
		return *r.backup.Status.CompletionTimestamp
	}

	return metav1.Now()
}

func (r RestoreRequest) EndTime() metav1.Time {
	if r.restore.Status.CompletionTimestamp != nil {
		return *r.restore.Status.CompletionTimestamp
	}

	return metav1.Now()
}

func (r BackupRequest) Status(log logr.Logger) error  { return backupRealStatusProcess(r.backup, log) }
func (r RestoreRequest) Status(log logr.Logger) error { return restoreStatusProcess(r.restore, log) }

type (
	BackupRequests  struct{ backups *velero.BackupList }
	RestoreRequests struct{ restores *velero.RestoreList }
)

func (r BackupRequests) Count() int                     { return len(r.backups.Items) }
func (r RestoreRequests) Count() int                    { return len(r.restores.Items) }
func (r BackupRequests) Get(i int) kubeobjects.Request  { return BackupRequest{&r.backups.Items[i]} }
func (r RestoreRequests) Get(i int) kubeobjects.Request { return RestoreRequest{&r.restores.Items[i]} }

type RequestsManager struct{}

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
		return fmt.Errorf("backup requests delete: %w", err)
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
		return fmt.Errorf("restore requests delete: %w", err)
	}

	return r.ProtectRequestsDelete(ctx, writer, requestNamespaceName, labels)
}

func (RequestsManager) RecoverRequestCreate(
	ctx context.Context,
	k8sclient client.Client,
	log logr.Logger,
	s3Url string,
	s3BucketName string,
	s3RegionName string,
	s3KeyPrefix string,
	secretKeyRef *corev1.SecretKeySelector,
	caCertificates []byte,
	recoverSpec kubeobjects.RecoverSpec,
	requestNamespaceName string,
	captureName string,
	captureRequest kubeobjects.ProtectRequest,
	recoverName string,
	labels map[string]string,
	annotations map[string]string,
) (kubeobjects.RecoverRequest, error) {
	log.Info("Kube objects recover",
		"s3 url", s3Url,
		"s3 bucket", s3BucketName,
		"s3 region", s3RegionName,
		"s3 key prefix", s3KeyPrefix,
		"secret key ref", secretKeyRef,
		"CA certificates", caCertificates,
		"request namespace", requestNamespaceName,
		"capture name", captureName,
		"recover name", recoverName,
		"label set", labels,
		"annotations", annotations,
	)

	restore, err := restoreRealCreate(
		objectWriter{ctx: ctx, Client: k8sclient, log: log},
		s3Url,
		s3BucketName,
		s3RegionName,
		s3KeyPrefix,
		secretKeyRef,
		caCertificates,
		recoverSpec,
		requestNamespaceName,
		captureName,
		captureRequest,
		recoverName,
		labels,
		annotations,
	)

	return RestoreRequest{restore}, err
}

func restoreRealCreate(
	w objectWriter,
	s3Url string,
	s3BucketName string,
	s3RegionName string,
	s3KeyPrefix string,
	secretKeyRef *corev1.SecretKeySelector,
	caCertificates []byte,
	recoverSpec kubeobjects.RecoverSpec,
	requestNamespaceName string,
	backupName string,
	captureRequest kubeobjects.ProtectRequest,
	restoreName string,
	labels map[string]string,
	annotations map[string]string,
) (*velero.Restore, error) {
	backupRequest, ok := captureRequest.(BackupRequest)
	if !ok {
		_, _, err := backupRequestCreate(
			w, s3Url, s3BucketName, s3RegionName, s3KeyPrefix, secretKeyRef,
			caCertificates,
			getBackupSpecFromObjectsSpec(recoverSpec.Spec),
			requestNamespaceName, backupName,
			labels,
			annotations,
		)
		if err != nil {
			return nil, fmt.Errorf("backup dummy request create: %w", err)
		}

		return nil, err
	}

	return backupDummyStatusProcessAndRestore(
		backupRequest.backup,
		w,
		recoverSpec,
		restoreName,
		labels,
	)
}

func backupDummyStatusProcessAndRestore(
	backup *velero.Backup,
	w objectWriter,
	recoverSpec kubeobjects.RecoverSpec,
	restoreName string,
	labels map[string]string,
) (*velero.Restore, error) {
	backupStatusLog(backup, w.log)

	switch backup.Status.Phase {
	case velero.BackupPhaseCompleted,
		velero.BackupPhasePartiallyFailed,
		velero.BackupPhaseFailed:
		return backupRestore(
			backup, w,
			recoverSpec,
			restoreName,
			labels,
		)
	case velero.BackupPhaseNew,
		velero.BackupPhaseInProgress,
		velero.BackupPhaseWaitingForPluginOperations,
		velero.BackupPhaseWaitingForPluginOperationsPartiallyFailed,
		velero.BackupPhaseDeleting,
		velero.BackupPhaseFinalizing,
		velero.BackupPhaseFinalizingPartiallyFailed:
		return nil, kubeobjects.RequestProcessingErrorCreate("backup" + string(backup.Status.Phase))
	case velero.BackupPhaseFailedValidation:
		return nil, errors.New("backup" + string(backup.Status.Phase))
	default:
		return nil, kubeobjects.RequestProcessingErrorCreate("backup.status.phase absent")
	}
}

func backupRestore(
	backup *velero.Backup,
	w objectWriter,
	recoverSpec kubeobjects.RecoverSpec,
	restoreName string,
	labels map[string]string,
) (*velero.Restore, error) {
	restore := restore(backup.Namespace, restoreName, recoverSpec, backup.Name, labels)
	if err := w.objectCreate(restore); err != nil {
		return nil, err
	}

	return restore, nil
}

func restoreStatusProcess(
	restore *velero.Restore,
	log logr.Logger,
) error {
	restoreStatusLog(restore, log)

	switch restore.Status.Phase {
	case velero.RestorePhaseCompleted:
		return nil
	case velero.RestorePhaseNew,
		velero.RestorePhaseInProgress,
		velero.RestorePhaseWaitingForPluginOperations,
		velero.RestorePhaseWaitingForPluginOperationsPartiallyFailed,
		velero.RestorePhaseFinalizing,
		velero.RestorePhaseFinalizingPartiallyFailed:
		return kubeobjects.RequestProcessingErrorCreate("restore" + string(restore.Status.Phase))
	case velero.RestorePhaseFailed,
		velero.RestorePhaseFailedValidation,
		velero.RestorePhasePartiallyFailed:
		return errors.New("restore" + string(restore.Status.Phase))
	default:
		return kubeobjects.RequestProcessingErrorCreate("restore.status.phase absent")
	}
}

func (RequestsManager) ProtectRequestCreate(
	ctx context.Context,
	k8sclient client.Client,
	log logr.Logger,
	s3Url string,
	s3BucketName string,
	s3RegionName string,
	s3KeyPrefix string,
	secretKeyRef *corev1.SecretKeySelector,
	caCertificates []byte,
	objectsSpec kubeobjects.Spec,
	requestNamespaceName string,
	captureName string,
	labels map[string]string,
	annotations map[string]string,
) (kubeobjects.ProtectRequest, error) {
	log.Info("Kube objects protect",
		"s3 url", s3Url,
		"s3 bucket", s3BucketName,
		"s3 region", s3RegionName,
		"s3 key prefix", s3KeyPrefix,
		"secret key ref", secretKeyRef,
		"CA certificates", caCertificates,
		"source namespaces", objectsSpec.IncludedNamespaces,
		"request namespace", requestNamespaceName,
		"capture name", captureName,
		"label set", labels,
		"annotations", annotations,
	)
	_, backup, err := backupRealCreate(
		objectWriter{ctx: ctx, Client: k8sclient, log: log},
		s3Url,
		s3BucketName,
		s3RegionName,
		s3KeyPrefix,
		secretKeyRef,
		caCertificates,
		objectsSpec,
		requestNamespaceName,
		captureName,
		labels,
		annotations,
	)

	return BackupRequest{backup}, err
}

func backupRealCreate(
	w objectWriter,
	s3Url string,
	s3BucketName string,
	s3RegionName string,
	s3KeyPrefix string,
	secretKeyRef *corev1.SecretKeySelector,
	caCertificates []byte,
	objectsSpec kubeobjects.Spec,
	requestNamespaceName string,
	captureName string,
	labels map[string]string,
	annotations map[string]string,
) (*velero.BackupStorageLocation, *velero.Backup, error) {
	return backupRequestCreate(
		w, s3Url, s3BucketName, s3RegionName, s3KeyPrefix, secretKeyRef,
		caCertificates,
		getBackupSpecFromObjectsSpec(objectsSpec),
		requestNamespaceName, captureName,
		labels,
		annotations,
	)
}

func getBackupSpecFromObjectsSpec(objectsSpec kubeobjects.Spec) velero.BackupSpec {
	if objectsSpec.LabelSelector == nil {
		objectsSpec.LabelSelector = &metav1.LabelSelector{}
	}

	newLabelSelector := objectsSpec.LabelSelector
	newLabelSelector.MatchExpressions = append(
		newLabelSelector.MatchExpressions,
		metav1.LabelSelectorRequirement{
			Key:      util.CreatedByRamenLabel,
			Operator: metav1.LabelSelectorOpNotIn,
			Values:   []string{"true"},
		},
	)

	return velero.BackupSpec{
		IncludedNamespaces: objectsSpec.IncludedNamespaces,
		IncludedResources:  objectsSpec.IncludedResources,
		// exclude VRs from Backup so VRG can create them: see https://github.com/RamenDR/ramen/issues/884
		// exclude EndpointSlices/Endpoints to prevent Submariner conflicts: see https://github.com/RamenDR/ramen/issues/1889
		// exclude VolumeSnapshots and VolumeGroupSnapshots from backup
		ExcludedResources: append(objectsSpec.ExcludedResources, "volumereplications.replication.storage.openshift.io",
			"volumegroupreplications.replication.storage.openshift.io", "replicationsources.volsync.backube",
			"replicationdestinations.volsync.backube", "PersistentVolumeClaims", "PersistentVolumes",
			"endpointslices.discovery.k8s.io", "endpoints", "volumesnapshots.snapshot.storage.k8s.io",
			"volumegroupsnapshots.groupsnapshot.storage.k8s.io"),
		LabelSelector:           newLabelSelector,
		OrLabelSelectors:        objectsSpec.OrLabelSelectors,
		TTL:                     metav1.Duration{}, // TODO: set default here
		IncludeClusterResources: objectsSpec.IncludeClusterResources,
		// TODO: Hooks should be handled by ramen code.
		// Hooks:                   getBackupHooks(objectsSpec.KubeResourcesSpec.Hooks)
		VolumeSnapshotLocations: []string{},
		DefaultVolumesToRestic:  new(bool),
		OrderedResources:        map[string]string{},
	}
}

func backupRealStatusProcess(
	backup *velero.Backup,
	log logr.Logger,
) error {
	backupStatusLog(backup, log)

	switch backup.Status.Phase {
	case velero.BackupPhaseCompleted:
		return nil
	case velero.BackupPhaseNew,
		velero.BackupPhaseInProgress,
		velero.BackupPhaseWaitingForPluginOperations,
		velero.BackupPhaseWaitingForPluginOperationsPartiallyFailed,
		velero.BackupPhaseDeleting,
		velero.BackupPhaseFinalizing,
		velero.BackupPhaseFinalizingPartiallyFailed:
		return kubeobjects.RequestProcessingErrorCreate("backup" + string(backup.Status.Phase))
	case velero.BackupPhaseFailedValidation,
		velero.BackupPhasePartiallyFailed,
		velero.BackupPhaseFailed:
		return errors.New("backup" + string(backup.Status.Phase))
	default:
		return kubeobjects.RequestProcessingErrorCreate("backup.status.phase absent")
	}
}

func (r BackupRequest) Deallocate(
	ctx context.Context,
	k8sclient client.Client,
	log logr.Logger,
) error {
	return objectWriter{ctx: ctx, Client: k8sclient, log: log}.backupObjectsDelete(
		&velero.BackupStorageLocation{ObjectMeta: metav1.ObjectMeta{Namespace: r.backup.Namespace, Name: r.backup.Name}},
		r.backup,
	)
}

func (r RestoreRequest) Deallocate(
	ctx context.Context,
	k8sclient client.Client,
	log logr.Logger,
) error {
	backupObjectMeta := metav1.ObjectMeta{Namespace: r.restore.Namespace, Name: r.restore.Spec.BackupName}

	return objectWriter{ctx: ctx, Client: k8sclient, log: log}.restoreObjectsDelete(
		&velero.BackupStorageLocation{ObjectMeta: backupObjectMeta},
		&velero.Backup{ObjectMeta: backupObjectMeta},
		r.restore,
	)
}

type objectWriter struct {
	ctx context.Context
	client.Client
	log logr.Logger
}

func backupRequestCreate(
	w objectWriter,
	s3Url string,
	s3BucketName string,
	s3RegionName string,
	s3KeyPrefix string,
	secretKeyRef *corev1.SecretKeySelector,
	caCertificates []byte,
	backupSpec velero.BackupSpec,
	requestsNamespaceName string,
	requestName string,
	labels map[string]string,
	annotations map[string]string,
) (*velero.BackupStorageLocation, *velero.Backup, error) {
	backupLocation := &velero.BackupStorageLocation{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: requestsNamespaceName,
			Name:      requestName,
		},
	}

	err := w.bslCreateOrUpdate(s3Url,
		s3BucketName,
		s3RegionName,
		s3KeyPrefix,
		secretKeyRef,
		caCertificates,
		labels,
		backupLocation)
	if err != nil {
		w.log.Error(err, "")

		return backupLocation, nil, err
	}
	// Check BSL availability before creating backup
	if err := w.checkBSLAvailability(backupLocation); err != nil {
		return backupLocation, nil, fmt.Errorf("BSL %s/%s not available for backup creation: %w",
			backupLocation.Namespace, backupLocation.Name, err)
	}

	backupSpec.StorageLocation = requestName
	backupSpec.SnapshotVolumes = new(bool)
	backupRequest := backupRequest(requestsNamespaceName, requestName, backupSpec, labels, annotations)

	return backupLocation, backupRequest, w.Create(w.ctx, backupRequest)
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
			return fmt.Errorf("object create: %w", err)
		}

		w.log.Info("Object created previously", "type", o.GetObjectKind(), "name", o.GetName())
	} else {
		w.log.Info("Object created successfully", "type", o.GetObjectKind(), "name", o.GetName())
	}

	return nil
}

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

func (w objectWriter) objectDelete(o client.Object) error {
	if err := w.Delete(w.ctx, o); err != nil {
		if !k8serrors.IsNotFound(err) {
			return fmt.Errorf("object delete: %w", err)
		}

		w.log.Info("Object deleted previously", "type", o.GetObjectKind(), "name", o.GetName())
	} else {
		w.log.Info("Object deleted successfully", "type", o.GetObjectKind(), "name", o.GetName())
	}

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

func veleroTypeMeta(kind string) metav1.TypeMeta {
	return metav1.TypeMeta{
		APIVersion: velero.SchemeGroupVersion.String(),
		Kind:       kind,
	}
}

func backupTypeMeta() metav1.TypeMeta  { return veleroTypeMeta("Backup") }
func restoreTypeMeta() metav1.TypeMeta { return veleroTypeMeta("Restore") }

func backupRequest(namespaceName, name string, spec velero.BackupSpec,
	labels map[string]string,
	annotations map[string]string,
) *velero.Backup {
	return &velero.Backup{
		TypeMeta: backupTypeMeta(),
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   namespaceName,
			Name:        name,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: spec,
	}
}

func restore(
	requestNamespaceName string,
	restoreName string,
	recoverSpec kubeobjects.RecoverSpec,
	backupName string,
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
			IncludedResources:       recoverSpec.IncludedResources,
			ExcludedResources:       recoverSpec.ExcludedResources,
			NamespaceMapping:        recoverSpec.NamespaceMapping,
			LabelSelector:           recoverSpec.LabelSelector,
			OrLabelSelectors:        recoverSpec.OrLabelSelectors,
			RestoreStatus:           recoverSpec.RestoreStatus,
			IncludeClusterResources: recoverSpec.IncludeClusterResources,
			ExistingResourcePolicy:  recoverSpec.ExistingResourcePolicy,
			// TODO: hooks?
			// TODO: restorePVs?
			// TODO: preserveNodePorts?
		},
	}
}

func backupStatusLog(backup *velero.Backup, log logr.Logger) {
	log.Info("Backup",
		"phase", backup.Status.Phase,
		"warnings", backup.Status.Warnings,
		"errors", backup.Status.Errors,
		"failure", backup.Status.FailureReason,
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
