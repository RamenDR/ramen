package cephfscg

import (
	"context"
	"fmt"
	"reflect"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	"github.com/go-logr/logr"
	vgsv1alphfa1 "github.com/kubernetes-csi/external-snapshotter/client/v7/apis/volumegroupsnapshot/v1alpha1"
	vsv1 "github.com/kubernetes-csi/external-snapshotter/client/v7/apis/volumesnapshot/v1"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/volsync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var VolumeGroupSnapshotNameFormat = "cephfscg-%s-src"

type VolumeGroupSourceHandler interface {
	CreateOrUpdateVolumeGroupSnapshot(
		ctx context.Context,
	) error

	RestoreVolumesFromVolumeGroupSnapshot(
		ctx context.Context,
	) ([]RestoredPVC, error)

	CreateOrUpdateReplicationSourceForRestoredPVCs(
		ctx context.Context,
		manual string,
		restoredPVCs []RestoredPVC,
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
	return &volumeGroupSourceHandler{
		Client:                       client,
		VolumeGroupSnapshotName:      fmt.Sprintf(VolumeGroupSnapshotNameFormat, rgs.Name),
		VolumeGroupSnapshotNamespace: rgs.Namespace,
		VolumeGroupSnapshotClassName: rgs.Spec.VolumeGroupSnapshotClassName,
		VolumeGroupLabel:             rgs.Spec.VolumeGroupSnapshotSource,
		VolsyncKeySecretName:         volsync.GetVolSyncPSKSecretNameFromVRGName(rgs.Name),
		DefaultCephFSCSIDriverName:   defaultCephFSCSIDriverName,
		Logger: logger.WithName("VolumeGroupSourceHandler").
			WithValues("VolumeGroupSnapshotName", fmt.Sprintf(VolumeGroupSnapshotNameFormat, rgs.Name)).
			WithValues("VolumeGroupSnapshotNamespace", rgs.Namespace),
	}
}

// CreateOrUpdateVolumeGroupSnapshot create or update a VolumeGroupSnapshot
func (h *volumeGroupSourceHandler) CreateOrUpdateVolumeGroupSnapshot(
	ctx context.Context,
) error {
	logger := h.Logger.WithName("CreateOrUpdateVolumeGroupSnapshot")
	logger.Info("Create or update volume group snapshot")

	volumeGroupSnapshot := &vgsv1alphfa1.VolumeGroupSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: h.VolumeGroupSnapshotNamespace,
			Name:      h.VolumeGroupSnapshotName,
		},
	}

	op, err := ctrlutil.CreateOrUpdate(ctx, h.Client, volumeGroupSnapshot, func() error {
		volumeGroupSnapshot.Spec.VolumeGroupSnapshotClassName = &h.VolumeGroupSnapshotClassName
		volumeGroupSnapshot.Spec.Source.Selector = h.VolumeGroupLabel

		return nil
	})
	if err != nil {
		logger.Error(err, "Failed to CreateOrUpdate volume group snapshot")

		return err
	}

	logger.Info("VolumeGroupSnapshot successfully be created or updated", "operation", op)

	return nil
}

// CleanVolumeGroupSnapshot delete restored pvc, replicationsource and VolumeGroupSnapshot
func (h *volumeGroupSourceHandler) CleanVolumeGroupSnapshot(
	ctx context.Context,
) error {
	logger := h.Logger.WithName("CleanVolumeGroupSnapshot")
	logger.Info("Get volume group snapshot")

	volumeGroupSnapshot := &vgsv1alphfa1.VolumeGroupSnapshot{}
	if err := h.Client.Get(ctx, types.NamespacedName{
		Name: h.VolumeGroupSnapshotName, Namespace: h.VolumeGroupSnapshotNamespace,
	}, volumeGroupSnapshot); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Volume group snapshot was already deleted")

			return nil
		}

		logger.Error(err, "Failed to get volume group snapshot")

		return err
	}

	for _, vsRef := range volumeGroupSnapshot.Status.VolumeSnapshotRefList {
		logger.Info("Get PVCName from volume snapshot",
			"VolumeSnapshotName", vsRef.Name, "VolumeSnapshotNamespace", vsRef.Namespace)

		pvc, err := GetPVCNameFromVolumeSnapshot(ctx, h.Client, vsRef.Name, vsRef.Namespace, volumeGroupSnapshot)
		if err != nil {
			logger.Error(err, "Failed to get PVC name from volume snapshot",
				"VolumeSnapshotName", vsRef.Name, "VolumeSnapshotNamespace", vsRef.Namespace)

			return err
		}

		restoredPVCName := pvc.Name + "-" + volumeGroupSnapshot.Name
		restoredPVCNamespace := vsRef.Namespace

		logger.Info("Delete restored PVCs", "PVCName", restoredPVCName, "PVCNamespace", restoredPVCNamespace)

		if err := h.Client.Delete(ctx, &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      restoredPVCName,
				Namespace: restoredPVCNamespace,
			},
		}); err != nil && !errors.IsNotFound(err) {
			logger.Error(err, "Failed to delete restored PVC ", "PVCName", restoredPVCName, "PVCNamespace", restoredPVCNamespace)

			return err
		}
	}

	if err := h.Client.Delete(ctx, volumeGroupSnapshot); err != nil && !errors.IsNotFound(err) {
		logger.Error(err, "Failed to delete volume group snapshot")

		return err
	}

	logger.Info("Successfully clean volume group snapshot reh.VolumeGroupSnapshotSource")

	return nil
}

// RestoreVolumesFromVolumeGroupSnapshot restore VolumeGroupSnapshot to PVCs
func (h *volumeGroupSourceHandler) RestoreVolumesFromVolumeGroupSnapshot(
	ctx context.Context,
) ([]RestoredPVC, error) {
	logger := h.Logger.WithName("RestoreVolumesFromVolumeGroupSnapshot")
	logger.Info("Get volume group snapshot")

	volumeGroupSnapshot := &vgsv1alphfa1.VolumeGroupSnapshot{}
	if err := h.Client.Get(ctx,
		types.NamespacedName{Name: h.VolumeGroupSnapshotName, Namespace: h.VolumeGroupSnapshotNamespace},
		volumeGroupSnapshot); err != nil {
		return nil, fmt.Errorf("failed to get volume group snapshot: %w", err)
	}

	if volumeGroupSnapshot.Status.ReadyToUse == nil ||
		(volumeGroupSnapshot.Status.ReadyToUse != nil && !*volumeGroupSnapshot.Status.ReadyToUse) {
		return nil, fmt.Errorf("can't restore volume group snapshot: volume group snapshot is not ready to be used")
	}

	restoredPVCs := []RestoredPVC{}

	for _, vsRef := range volumeGroupSnapshot.Status.VolumeSnapshotRefList {
		logger.Info("Get PVCName from volume snapshot",
			"VolumeSnapshotName", vsRef.Name, "VolumeSnapshotNamespace", vsRef.Namespace)

		pvc, err := GetPVCNameFromVolumeSnapshot(ctx, h.Client, vsRef.Name, vsRef.Namespace, volumeGroupSnapshot)
		if err != nil {
			return nil, fmt.Errorf("failed to get PVC name from volume snapshot %s: %w", vsRef.Namespace+"/"+vsRef.Name, err)
		}

		restoreStorageClass, err := GetRestoreStorageClass(ctx, h.Client,
			*pvc.Spec.StorageClassName, h.DefaultCephFSCSIDriverName)
		if err != nil {
			return nil, fmt.Errorf("failed to get Restore Storage Class from PVC %s: %w", pvc.Name+"/"+vsRef.Namespace, err)
		}

		RestoredPVCNamespacedName := types.NamespacedName{
			Namespace: vsRef.Namespace,
			Name:      pvc.Name + "-" + volumeGroupSnapshot.Name,
		}
		if err := h.RestoreVolumesFromSnapshot(
			ctx, vsRef, RestoredPVCNamespacedName, restoreStorageClass.GetName()); err != nil {
			return nil, fmt.Errorf("failed to restore volumes from snapshot %s: %w", vsRef.Name+"/"+vsRef.Namespace, err)
		}

		logger.Info("Successfully restore volumes from snapshot",
			"RestoredPVCName", RestoredPVCNamespacedName.Name, "RestoredPVCNamespace", RestoredPVCNamespacedName.Namespace)

		restoredPVCs = append(restoredPVCs, RestoredPVC{
			SourcePVCName:      pvc.Name,
			RestoredPVCName:    RestoredPVCNamespacedName.Name,
			VolumeSnapshotName: vsRef.Name,
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
	vsRef corev1.ObjectReference,
	restoredPVCNamespacedname types.NamespacedName,
	restoreStorageClassName string,
) error {
	logger := h.Logger.WithName("RestoreVolumesFromSnapshot").
		WithValues("RestoredPVCName", restoredPVCNamespacedname.Name).
		WithValues("RestoredPVCNamespace", restoredPVCNamespacedname.Namespace)

	volumeSnapshot := &vsv1.VolumeSnapshot{}
	if err := h.Client.Get(ctx,
		types.NamespacedName{Name: vsRef.Name, Namespace: vsRef.Namespace},
		volumeSnapshot,
	); err != nil {
		return fmt.Errorf("failed to get volume snapshot: %w", err)
	}

	group := vsRef.GroupVersionKind().Group
	snapshotRef := corev1.TypedLocalObjectReference{Name: vsRef.Name, APIGroup: &group, Kind: vsRef.Kind}
	restoredPVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      restoredPVCNamespacedname.Name,
			Namespace: restoredPVCNamespacedname.Namespace,
		},
	}

	for {
		pvcNeedsRecreation := false
		logger.Info("Create or update PVC with snapshot as data h.VolumeGroupSnapshotSource",
			"PVCNeedsRecreation", pvcNeedsRecreation)

		if _, err := ctrlutil.CreateOrUpdate(ctx, h.Client, restoredPVC, func() error {
			if !restoredPVC.CreationTimestamp.IsZero() &&
				restoredPVC.Spec.DataSource != nil &&
				!reflect.DeepEqual(*restoredPVC.Spec.DataSource, snapshotRef) {
				logger.Info("PVC already exist but with wrong data h.VolumeGroupSnapshotSource, "+
					"need to delete this PVC and re-create",
					"WrongDataSourceName", restoredPVC.Spec.DataSource.Name)
				// If this pvc already exists and not pointing to our desired snapshot, we will need to
				// delete it and re-create as we cannot update the datah.VolumeGroupSnapshotSource
				pvcNeedsRecreation = true

				return nil
			}
			if restoredPVC.Status.Phase == corev1.ClaimBound {
				// PVC already bound at this point
				logger.Info("PVC already restore the snapshot")

				return nil
			}
			restoredPVC.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadOnlyMany}
			restoredPVC.Spec.DataSource = &snapshotRef
			restoredPVC.Spec.Resources = corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: *volumeSnapshot.Status.RestoreSize,
				},
			}
			restoredPVC.Spec.StorageClassName = &restoreStorageClassName

			return nil
		}); err != nil {
			return fmt.Errorf("failed to create or update PVC: %w", err)
		}

		if pvcNeedsRecreation {
			logger.Info("Delete PVC", "PVCNeedsRecreation", pvcNeedsRecreation)

			if err := h.Client.Delete(ctx, restoredPVC); err != nil && !errors.IsNotFound(err) {
				return fmt.Errorf("failed to delete PVC: %w", err)
			}

			logger.Info("PVC was successfully deleted", "PVCNeedsRecreation", pvcNeedsRecreation)
		} else {
			logger.Info("No need to delete PVC", "PVCNeedsRecreation", pvcNeedsRecreation)

			break
		}
	}

	logger.Info("Successfully to create or update PVC with snapshot as data h.VolumeGroupSnapshotSource")

	return nil
}

// CreateOrUpdateReplicationSourceForRestoredPVCs create or update replication source for each restored pvc
func (h *volumeGroupSourceHandler) CreateOrUpdateReplicationSourceForRestoredPVCs(
	ctx context.Context,
	manual string,
	restoredPVCs []RestoredPVC,
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
				Name:      restoredPVC.RestoredPVCName,
				Namespace: replicationSourceNamepspace,
			},
		}
		rdService := getRemoteServiceNameForRDFromPVCName(restoredPVC.SourcePVCName, replicationSourceNamepspace)

		op, err := ctrlutil.CreateOrUpdate(ctx, h.Client, replicationSource, func() error {
			replicationSource.Spec.SourcePVC = restoredPVC.RestoredPVCName
			replicationSource.Spec.Trigger = &volsyncv1alpha1.ReplicationSourceTriggerSpec{
				Manual: manual,
			}
			replicationSource.Spec.RsyncTLS = &volsyncv1alpha1.ReplicationSourceRsyncTLSSpec{
				ReplicationSourceVolumeOptions: volsyncv1alpha1.ReplicationSourceVolumeOptions{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadOnlyMany},
					CopyMethod:  volsyncv1alpha1.CopyMethodDirect,
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

		replicationSource := &volsyncv1alpha1.ReplicationSource{}

		err := h.Client.Get(ctx,
			types.NamespacedName{Name: replicationSource.Name, Namespace: replicationSource.Namespace},
			replicationSource)
		if err != nil {
			logger.Error(err, "Failed to get replication source", "ReplicationSource", replicationSource.Name)

			return false, err
		}

		if replicationSource.Spec.Trigger.Manual != replicationSource.Status.LastManualSync {
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

// TODO(wangyouhang): https://github.com/kubernetes-csi/external-snapshotter/issues/969
// Fake func, need to be changed
func GetPVCNameFromVolumeSnapshot(
	ctx context.Context, k8sClient client.Client, vsName string,
	vsNamespace string, vgs *vgsv1alphfa1.VolumeGroupSnapshot,
) (*corev1.PersistentVolumeClaim, error) {
	if vgs.Status.BoundVolumeGroupSnapshotContentName == nil {
		return nil, fmt.Errorf("BoundVolumeGroupSnapshotContentName is nil")
	}

	// get vs index in vgs
	var index int

	for i, VolumeSnapshotRef := range vgs.Status.VolumeSnapshotRefList {
		if VolumeSnapshotRef.Name == vsName && VolumeSnapshotRef.Namespace == vsNamespace {
			index = i
		}
	}

	// get storageHandle based on index
	vgsc := &vgsv1alphfa1.VolumeGroupSnapshotContent{}

	err := k8sClient.Get(ctx,
		types.NamespacedName{
			Name:      *vgs.Status.BoundVolumeGroupSnapshotContentName,
			Namespace: vgs.GetNamespace(),
		},
		vgsc)
	if err != nil {
		return nil, err
	}

	if len(vgs.Status.VolumeSnapshotRefList) != len(vgsc.Spec.Source.VolumeHandles) {
		return nil, fmt.Errorf("len of vgs.Status.VolumeSnapshotRefList != len of vgsc.Spec.Source.VolumeHandles")
	}

	storageHandle := vgsc.Spec.Source.VolumeHandles[index]

	pvc, err := GetPVCfromStorageHandle(ctx, k8sClient, storageHandle, vgs.Namespace)
	if err != nil {
		return nil, fmt.Errorf("PVC is not found")
	}

	return pvc, nil
}

func GetPVCfromStorageHandle(
	ctx context.Context,
	k8sClient client.Client,
	storageHandle string,
	namespace string,
) (*corev1.PersistentVolumeClaim, error) {
	// get pv from storageHandle, then get pvc from pv
	pvList := &corev1.PersistentVolumeList{}

	if err := k8sClient.List(ctx, pvList, client.InNamespace(namespace)); err != nil {
		return nil, err
	}

	for _, pv := range pvList.Items {
		if pv.Spec.CSI.VolumeHandle == storageHandle {
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
