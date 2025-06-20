// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package cephfscg

import (
	"context"
	"fmt"
	"reflect"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	"github.com/go-logr/logr"
	vsv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/util"
	"github.com/ramendr/ramen/internal/controller/volsync"
	vgsv1beta1 "github.com/red-hat-storage/external-snapshotter/client/v8/apis/volumegroupsnapshot/v1beta1"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var (
	RestorePVCinCGNameFormat = "vs-%s"
	SnapshotGroup            = "snapshot.storage.k8s.io"
	SnapshotGroupKind        = "VolumeSnapshot"
)

type VolumeGroupSourceHandler interface {
	CreateOrUpdateVolumeGroupSnapshot(
		ctx context.Context, owner metav1.Object,
	) error

	RestoreVolumesFromVolumeGroupSnapshot(
		ctx context.Context, owner metav1.Object,
	) ([]RestoredPVC, error)

	CreateOrUpdateReplicationSourceForRestoredPVCs(
		ctx context.Context,
		manual string,
		restoredPVCs []RestoredPVC,
		owner metav1.Object,
	) ([]*corev1.ObjectReference, error)

	CheckReplicationSourceForRestoredPVCsCompleted(
		ctx context.Context,
		replicationSources []*corev1.ObjectReference,
	) (bool, error)

	// Only cleanup restored pvc and volumegoupsnapshot
	CleanVolumeGroupSnapshot(
		ctx context.Context,
	) error
}

type RestoredPVC struct {
	// Application PVC Name
	SourcePVCName string
	// Restored PVC Name
	RestoredPVCName string
	// VolumeSnapsht Name of the application PVC in VolumeGroupSnaoshot
	VolumeSnapshotName string
}

type volumeGroupSourceHandler struct {
	client.Client
	VolumeGroupSnapshotName      string
	VolumeGroupSnapshotNamespace string
	VolumeGroupSnapshotClassName string
	VolumeGroupLabel             *metav1.LabelSelector
	VolsyncKeySecretName         string
	DefaultCephFSCSIDriverName   string
	Logger                       logr.Logger
}

func NewVolumeGroupSourceHandler(
	client client.Client,
	rgs *ramendrv1alpha1.ReplicationGroupSource,
	defaultCephFSCSIDriverName string,
	logger logr.Logger,
) VolumeGroupSourceHandler {
	vrgName := rgs.GetLabels()[volsync.VRGOwnerNameLabel]

	vgsName := rgs.Name

	return &volumeGroupSourceHandler{
		Client:                       client,
		VolumeGroupSnapshotName:      vgsName,
		VolumeGroupSnapshotNamespace: rgs.Namespace,
		VolumeGroupSnapshotClassName: rgs.Spec.VolumeGroupSnapshotClassName,
		VolumeGroupLabel:             rgs.Spec.VolumeGroupSnapshotSource,
		VolsyncKeySecretName:         volsync.GetVolSyncPSKSecretNameFromVRGName(vrgName),
		DefaultCephFSCSIDriverName:   defaultCephFSCSIDriverName,
		Logger: logger.WithName("VolumeGroupSourceHandler").
			WithValues("VolumeGroupSnapshotName", vgsName).
			WithValues("VolumeGroupSnapshotNamespace", rgs.Namespace),
	}
}

// CreateOrUpdateVolumeGroupSnapshot create or update a VolumeGroupSnapshot
func (h *volumeGroupSourceHandler) CreateOrUpdateVolumeGroupSnapshot(
	ctx context.Context, owner metav1.Object,
) error {
	logger := h.Logger.WithName("CreateOrUpdateVolumeGroupSnapshot")
	logger.Info("Create or update volume group snapshot")

	volumeGroupSnapshot := &vgsv1beta1.VolumeGroupSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: h.VolumeGroupSnapshotNamespace,
			Name:      h.VolumeGroupSnapshotName,
		},
	}

	op, err := ctrlutil.CreateOrUpdate(ctx, h.Client, volumeGroupSnapshot, func() error {
		if !volumeGroupSnapshot.DeletionTimestamp.IsZero() {
			return fmt.Errorf("the volume group snapshot is being deleted, need to wait")
		}

		if err := ctrl.SetControllerReference(owner, volumeGroupSnapshot, h.Client.Scheme()); err != nil {
			return err
		}

		util.AddLabel(volumeGroupSnapshot, util.CreatedByRamenLabel, "true")
		util.AddLabel(volumeGroupSnapshot, util.RGSOwnerLabel, owner.GetName())
		util.AddAnnotation(volumeGroupSnapshot, volsync.OwnerNameAnnotation, owner.GetName())
		util.AddAnnotation(volumeGroupSnapshot, volsync.OwnerNamespaceAnnotation, owner.GetNamespace())

		volumeGroupSnapshot.Spec.VolumeGroupSnapshotClassName = &h.VolumeGroupSnapshotClassName
		volumeGroupSnapshot.Spec.Source.Selector = h.VolumeGroupLabel

		return nil
	})
	if err != nil {
		logger.Error(err, "Failed to CreateOrUpdate volume group snapshot")

		return err
	}

	logger.Info("VolumeGroupSnapshot successfully created or updated", "operation", op)

	return nil
}

// CleanVolumeGroupSnapshot delete restored pvc and VolumeGroupSnapshot
func (h *volumeGroupSourceHandler) CleanVolumeGroupSnapshot(
	ctx context.Context,
) error {
	logger := h.Logger.WithName("CleanVolumeGroupSnapshot")
	logger.Info("Get volume group snapshot")

	vgs := &vgsv1beta1.VolumeGroupSnapshot{}
	if err := h.Client.Get(ctx, types.NamespacedName{
		Name: h.VolumeGroupSnapshotName, Namespace: h.VolumeGroupSnapshotNamespace,
	}, vgs); err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("Volume group snapshot was already deleted")

			return nil
		}

		logger.Error(err, "Failed to get volume group snapshot")

		return err
	}

	if vgs.Status != nil {
		volumeSnapshots, err := util.GetVolumeSnapshotsOwnedByVolumeGroupSnapshot(ctx, h.Client, vgs, logger)
		if err != nil {
			return err
		}

		logger.Info("Clean: Found VolumeSnapshots", "len", len(volumeSnapshots), "in group", vgs.Name)

		for idx := range volumeSnapshots {
			vs := &volumeSnapshots[idx]

			err := h.deleteRestoredPVC(ctx, vs)
			if err != nil {
				return err
			}
		}
	}

	if err := h.Client.Delete(ctx, vgs); err != nil && !k8serrors.IsNotFound(err) {
		logger.Error(err, "Failed to delete volume group snapshot")

		return err
	}

	logger.Info("Successfully clean volume group snapshot reh.VolumeGroupSnapshotSource")

	return nil
}

func (h *volumeGroupSourceHandler) deleteRestoredPVC(ctx context.Context, vs *vsv1.VolumeSnapshot) error {
	logger := h.Logger.WithName("deleteRestoredPVC").
		WithValues("VSName", vs.Name).
		WithValues("VSNamespace", vs.Namespace)

	logger.Info("Get PVCName from volume snapshot",
		"vsName", vs.Spec.Source.PersistentVolumeClaimName, "vsNamespace", vs.Namespace)

	pvc, err := util.GetPVC(ctx, h.Client,
		types.NamespacedName{Name: *vs.Spec.Source.PersistentVolumeClaimName, Namespace: vs.Namespace})
	if err != nil {
		logger.Error(err, "Failed to get PVC name from volume snapshot",
			"pvcName", vs.Spec.Source.PersistentVolumeClaimName, "vsNamespace", vs.Namespace)

		return err
	}

	restoredPVCName := fmt.Sprintf(RestorePVCinCGNameFormat, pvc.Name)
	restoredPVCNamespace := pvc.Namespace

	logger.Info("Delete restored PVC", "name", restoredPVCName, "namespace", restoredPVCNamespace)

	if err := h.Client.Delete(ctx, &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      restoredPVCName,
			Namespace: restoredPVCNamespace,
		},
	}); err != nil && !k8serrors.IsNotFound(err) {
		logger.Error(err, "Failed to delete restored PVC ",
			"PVCName", restoredPVCName, "PVCNamespace", restoredPVCNamespace)

		return err
	}

	return nil
}

// RestoreVolumesFromVolumeGroupSnapshot restores VolumeGroupSnapshot to PVCs
//
//nolint:funlen,cyclop
func (h *volumeGroupSourceHandler) RestoreVolumesFromVolumeGroupSnapshot(
	ctx context.Context, owner metav1.Object,
) ([]RestoredPVC, error) {
	logger := h.Logger.WithName("RestoreFromVolumeGroupSnapshot")
	logger.Info("Get volume group snapshot")

	vgs := &vgsv1beta1.VolumeGroupSnapshot{}
	if err := h.Client.Get(ctx,
		types.NamespacedName{Name: h.VolumeGroupSnapshotName, Namespace: h.VolumeGroupSnapshotNamespace},
		vgs); err != nil {
		return nil, fmt.Errorf("failed to get volume group snapshot: %w", err)
	}

	if vgs.Status == nil || vgs.Status.ReadyToUse == nil ||
		(vgs.Status.ReadyToUse != nil && !*vgs.Status.ReadyToUse) {
		return nil, fmt.Errorf("can't restore volume group snapshot: volume group snapshot is not ready to be used")
	}

	restoredPVCs := []RestoredPVC{}

	volumeSnapshots, err := util.GetVolumeSnapshotsOwnedByVolumeGroupSnapshot(ctx, h.Client, vgs, logger)
	if err != nil {
		return nil, err
	}

	logger.Info("Restore: Found VolumeSnapshots", "len", len(volumeSnapshots), "in group", vgs.Name)

	for _, vs := range volumeSnapshots {
		logger.Info("Get PVCName from volume snapshot",
			"PVCName", vs.Spec.Source.PersistentVolumeClaimName, "VolumeSnapshotName", vs.Name)

		pvc, err := util.GetPVC(ctx, h.Client,
			types.NamespacedName{Name: *vs.Spec.Source.PersistentVolumeClaimName, Namespace: vgs.Namespace})
		if err != nil {
			return nil, fmt.Errorf("failed to get PVC from VGS %s: %w",
				vgs.Namespace+"/"+*vs.Spec.Source.PersistentVolumeClaimName, err)
		}

		storageClass, err := GetStorageClass(ctx, h.Client, pvc.Spec.StorageClassName)
		if err != nil {
			return nil, err
		}

		restoreAccessModes := []corev1.PersistentVolumeAccessMode{corev1.ReadOnlyMany}
		if storageClass.Provisioner != h.DefaultCephFSCSIDriverName {
			restoreAccessModes = pvc.Spec.AccessModes
		}

		RestoredPVCNamespacedName := types.NamespacedName{
			Namespace: pvc.Namespace,
			Name:      fmt.Sprintf(RestorePVCinCGNameFormat, pvc.Name),
		}
		if err := h.RestoreVolumesFromSnapshot(
			ctx, vs.Name, pvc, RestoredPVCNamespacedName,
			restoreAccessModes, owner); err != nil {
			return nil, fmt.Errorf("failed to restore volumes from snapshot %s: %w",
				vs.Name+"/"+pvc.Namespace, err)
		}

		logger.Info("Successfully restore volumes from snapshot",
			"RestoredPVCName", RestoredPVCNamespacedName.Name, "RestoredPVCNamespace", RestoredPVCNamespacedName.Namespace)

		restoredPVCs = append(restoredPVCs, RestoredPVC{
			SourcePVCName:      pvc.Name,
			RestoredPVCName:    RestoredPVCNamespacedName.Name,
			VolumeSnapshotName: vs.Name,
		})
	}

	logger.Info("All volume snapshot volume group are successfully restored")

	return restoredPVCs, nil
}

// RestoreVolumesFromSnapshot restore a snapshot to a read-only pvc
//
//nolint:funlen,gocognit,cyclop
func (h *volumeGroupSourceHandler) RestoreVolumesFromSnapshot(
	ctx context.Context,
	vsName string,
	pvc *corev1.PersistentVolumeClaim,
	restoredPVCNamespacedname types.NamespacedName,
	restoreAccessModes []corev1.PersistentVolumeAccessMode,
	owner metav1.Object,
) error {
	logger := h.Logger.WithName("RestoreVolumesFromSnapshot").
		WithValues("RestoredPVCName", restoredPVCNamespacedname.Name).
		WithValues("RestoredPVCNamespace", restoredPVCNamespacedname.Namespace)

	volumeSnapshot := &vsv1.VolumeSnapshot{}
	if err := h.Client.Get(ctx,
		types.NamespacedName{Name: vsName, Namespace: pvc.Namespace},
		volumeSnapshot,
	); err != nil {
		return fmt.Errorf("failed to get volume snapshot: %w", err)
	}

	snapshotRef := corev1.TypedLocalObjectReference{Name: vsName, APIGroup: &SnapshotGroup, Kind: SnapshotGroupKind}
	restoredPVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      restoredPVCNamespacedname.Name,
			Namespace: restoredPVCNamespacedname.Namespace,
		},
	}

	logger.Info("Create or update PVC with snapshot as data h.VolumeGroupSnapshotSource",
		"snapshotRef", snapshotRef)

	if _, err := ctrlutil.CreateOrUpdate(ctx, h.Client, restoredPVC, func() error {
		if !restoredPVC.DeletionTimestamp.IsZero() {
			return fmt.Errorf("the restored pvc is being deleted, need to wait")
		}

		if err := ctrl.SetControllerReference(owner, restoredPVC, h.Client.Scheme()); err != nil {
			return err
		}

		util.AddLabel(restoredPVC, util.CreatedByRamenLabel, "true")
		util.AddLabel(restoredPVC, util.RGSOwnerLabel, owner.GetName())
		util.AddAnnotation(restoredPVC, volsync.OwnerNameAnnotation, owner.GetName())
		util.AddAnnotation(restoredPVC, volsync.OwnerNamespaceAnnotation, owner.GetNamespace())

		if !restoredPVC.CreationTimestamp.IsZero() &&
			restoredPVC.Spec.DataSource != nil &&
			!reflect.DeepEqual(*restoredPVC.Spec.DataSource, snapshotRef) {
			logger.Info("PVC already exist but with wrong data source, "+
				"need to delete this PVC and re-create",
				"WrongDataSource", restoredPVC.Spec.DataSource,
				"CorrentDataSource", snapshotRef,
			)
			// If this pvc already exists and not pointing to our desired snapshot, we will need to
			// delete it and re-create as we cannot update the datah.VolumeGroupSnapshotSource
			if err := h.Client.Delete(ctx, restoredPVC); err != nil && !k8serrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete PVC: %w", err)
			}

			logger.Info("PVC was successfully deleted")

			return fmt.Errorf("wrong data pvc was deleted, requeue")
		}
		if restoredPVC.Status.Phase == corev1.ClaimBound {
			// PVC already bound at this point
			logger.Info("PVC already restore the snapshot")

			return nil
		}

		if restoredPVC.CreationTimestamp.IsZero() { // set immutable fields
			restoredPVC.Spec.AccessModes = restoreAccessModes
			restoredPVC.Spec.StorageClassName = pvc.Spec.StorageClassName
			restoredPVC.Spec.DataSource = &snapshotRef
		}

		restoreSize := pvc.Spec.Resources.Requests.Storage()
		if volumeSnapshot.Status != nil && volumeSnapshot.Status.RestoreSize != nil {
			restoreSize = volumeSnapshot.Status.RestoreSize
		}

		if restoreSize != nil {
			restoredPVC.Spec.Resources = corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: *restoreSize,
				},
			}
		}

		logger.Info("PVC will be restored", "PVCSpec", restoredPVC.Spec)

		return nil
	}); err != nil {
		return fmt.Errorf("failed to create or update PVC: %w", err)
	}

	logger.Info("Successfully to create or update PVC with snapshot as data source")

	return nil
}

// CreateOrUpdateReplicationSourceForRestoredPVCs create or update replication source for each restored pvc
//
//nolint:funlen
func (h *volumeGroupSourceHandler) CreateOrUpdateReplicationSourceForRestoredPVCs(
	ctx context.Context,
	manual string,
	restoredPVCs []RestoredPVC,
	owner metav1.Object,
) ([]*corev1.ObjectReference, error) {
	logger := h.Logger.WithName("CreateReplicationSourceForRestoredPVCs").
		WithValues("NumberOfRestoredPVCs", len(restoredPVCs))
	logger.Info("Start to create replication source for restored PVCs")

	replicationSources := []*corev1.ObjectReference{}

	for _, tmpRestoredPVC := range restoredPVCs {
		restoredPVC := tmpRestoredPVC
		logger.Info("Create replication source for restored PVC", "RestoredPVC", restoredPVC.RestoredPVCName)

		replicationSourceNamepspace := h.VolumeGroupSnapshotNamespace
		replicationSource := &volsyncv1alpha1.ReplicationSource{
			ObjectMeta: metav1.ObjectMeta{
				Name:      restoredPVC.SourcePVCName,
				Namespace: replicationSourceNamepspace,
			},
		}

		rdService := getRemoteServiceNameForRDFromPVCName(restoredPVC.SourcePVCName, replicationSourceNamepspace)

		op, err := ctrlutil.CreateOrUpdate(ctx, h.Client, replicationSource, func() error {
			if err := ctrl.SetControllerReference(owner, replicationSource, h.Client.Scheme()); err != nil {
				return err
			}

			util.AddLabel(replicationSource, util.CreatedByRamenLabel, "true")
			util.AddLabel(replicationSource, util.RGSOwnerLabel, owner.GetName())
			util.AddAnnotation(replicationSource, volsync.OwnerNameAnnotation, owner.GetName())
			util.AddAnnotation(replicationSource, volsync.OwnerNamespaceAnnotation, owner.GetNamespace())

			replicationSource.Spec.SourcePVC = restoredPVC.RestoredPVCName
			replicationSource.Spec.Trigger = &volsyncv1alpha1.ReplicationSourceTriggerSpec{
				Manual: manual,
			}
			replicationSource.Spec.RsyncTLS = &volsyncv1alpha1.ReplicationSourceRsyncTLSSpec{
				ReplicationSourceVolumeOptions: volsyncv1alpha1.ReplicationSourceVolumeOptions{
					CopyMethod: volsyncv1alpha1.CopyMethodDirect,
				},

				KeySecret: &h.VolsyncKeySecretName,
				Address:   &rdService,
			}

			return nil
		})
		if err != nil {
			logger.Error(err, "Failed to CreateOrUpdate replication source", "RestoredPVC", restoredPVC.RestoredPVCName)

			return nil, err
		}

		replicationSources = append(replicationSources, &corev1.ObjectReference{
			APIVersion: replicationSource.APIVersion,
			Kind:       replicationSource.Kind,
			Namespace:  replicationSource.Namespace,
			Name:       replicationSource.Name,
		})

		logger.Info("replication source successfully reconciled", "operation", op, "RestoredPVC", restoredPVC.RestoredPVCName)
	}

	logger.Info("Replication sources are successfully created for all restored PVCs")

	return replicationSources, nil
}

// CheckReplicationSourceForRestoredPVCsCompleted check if all replication source are completed
func (h *volumeGroupSourceHandler) CheckReplicationSourceForRestoredPVCsCompleted(
	ctx context.Context,
	replicationSources []*corev1.ObjectReference,
) (bool, error) {
	logger := h.Logger.WithName("CheckReplicationSourceForRestoredPVCsCompleted").
		WithValues("NumberOfReplicationSource", len(replicationSources)).
		WithValues("VolumeGroupSnapshotName", h.VolumeGroupSnapshotName)
	logger.Info("Start to check replication source")

	for _, replicationSource := range replicationSources {
		logger.Info("Check replication source",
			"ReplicationSourceName", replicationSource.Name,
			"ReplicationSourceNamespace", replicationSource.Namespace,
		)

		replicationSourceInCluster := &volsyncv1alpha1.ReplicationSource{}

		err := h.Client.Get(ctx,
			types.NamespacedName{Name: replicationSource.Name, Namespace: replicationSource.Namespace},
			replicationSourceInCluster)
		if err != nil {
			logger.Error(err, "Failed to get replication source", "ReplicationSource", replicationSource.Name)

			return false, err
		}

		if replicationSourceInCluster.Spec.Trigger == nil || replicationSourceInCluster.Spec.Trigger.Manual == "" {
			logger.Info("There is no manual trigger in the replication source",
				"ReplicationSourceName", replicationSource.Name,
				"ReplicationSourceNamespace", replicationSource.Namespace,
			)

			return false, fmt.Errorf("manual trigger not found in the replicationsource spec")
		}

		if replicationSourceInCluster.Status == nil ||
			replicationSourceInCluster.Spec.Trigger.Manual != replicationSourceInCluster.Status.LastManualSync {
			logger.Info("replication source is not completed",
				"ReplicationSourceName", replicationSource.Name,
				"ReplicationSourceNamespace", replicationSource.Namespace,
			)

			return false, nil
		}
	}

	logger.Info("All replication sources are successfully completed")

	return true, nil
}

func GetPVCfromStorageHandle(
	ctx context.Context,
	k8sClient client.Client,
	storageHandle string,
) (*corev1.PersistentVolumeClaim, error) {
	// get pv from storageHandle, then get pvc from pv
	pvList := &corev1.PersistentVolumeList{}

	if err := k8sClient.List(ctx, pvList); err != nil {
		return nil, err
	}

	for _, pv := range pvList.Items {
		if pv.Spec.CSI != nil && pv.Spec.CSI.VolumeHandle == storageHandle {
			pvc := &corev1.PersistentVolumeClaim{}

			err := k8sClient.Get(ctx,
				types.NamespacedName{
					Name:      pv.Spec.ClaimRef.Name,
					Namespace: pv.Spec.ClaimRef.Namespace,
				}, pvc)
			if err != nil {
				return nil, err
			}

			return pvc, nil
		}
	}

	return nil, fmt.Errorf("PVC is not found")
}
