// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package cephfscg

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	"github.com/go-logr/logr"
	vgsv1beta1 "github.com/red-hat-storage/external-snapshotter/client/v8/apis/volumegroupsnapshot/v1beta1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/util"
	"github.com/ramendr/ramen/internal/controller/volsync"
)

const (
	// VGS status labels for lifecycle management
	// These labels are used to track VolumeGroupSnapshot state for differential sync
	VGSStatusLabelKey = "ramen.openshift.io/vgs-status"
	VGSStatusCurrent  = "current"  // Active VGS used for current sync cycle
	VGSStatusPrevious = "previous" // Previous VGS used as base for differential sync
)

type diffVolumeGroupSourceHandler struct {
	volumeGroupSourceHandler // embedded
}

func NewDiffVolumeGroupSourceHandler(
	client client.Client,
	rgs *ramendrv1alpha1.ReplicationGroupSource,
	defaultCephFSCSIDriverName string,
	vsHandler *volsync.VSHandler,
	logger logr.Logger,
) VolumeGroupSourceHandler {
	vrgName := rgs.GetLabels()[util.VRGOwnerNameLabel]

	vgsName := rgs.Name

	return &diffVolumeGroupSourceHandler{
		volumeGroupSourceHandler: volumeGroupSourceHandler{
			Client:                       client,
			VolumeGroupSnapshotName:      vgsName,
			VolumeGroupSnapshotNamespace: rgs.Namespace,
			VolumeGroupSnapshotClassName: rgs.Spec.VolumeGroupSnapshotClassName,
			VolumeGroupLabel:             rgs.Spec.VolumeGroupSnapshotSource,
			VolsyncKeySecretName:         volsync.GetVolSyncPSKSecretNameFromVRGName(vrgName),
			DefaultCephFSCSIDriverName:   defaultCephFSCSIDriverName,
			VSHandler:                    vsHandler,
			Logger: logger.WithName("DiffVolumeGroupSourceHandler").
				WithValues("VolumeGroupSnapshotName", vgsName).
				WithValues("VolumeGroupSnapshotNamespace", rgs.Namespace),
		},
	}
}

// resolveRDService overrides the base handler to use the ceph-volsync-plugin service name
// instead of the standard VolSync rsync-tls service
func (h *diffVolumeGroupSourceHandler) resolveRDService(
	originalPVCName, _ string,
	vrg *ramendrv1alpha1.VolumeReplicationGroup,
	rsNS string,
	isSubmarinerEnabled bool,
	logger logr.Logger,
) (string, error) {
	if isSubmarinerEnabled {
		return util.GetRemoteServiceNameForDiffRDFromPVCName(originalPVCName, rsNS), nil
	}

	logger.Info("Non submariner diff sync", "rsspec", vrg.Spec.VolSync.RSSpec)

	for _, rs := range vrg.Spec.VolSync.RSSpec {
		if rs.ProtectedPVC.Name == originalPVCName {
			return rs.RsyncTLS.Address, nil
		}
	}

	return "", fmt.Errorf("no matching RSSpec for diff sync PVC %q", originalPVCName)
}

// findVGSWithStatus finds a VolumeGroupSnapshot with specific status label owned by this handler
func (h *diffVolumeGroupSourceHandler) findVGSWithStatus(
	ctx context.Context, status string,
) (*vgsv1beta1.VolumeGroupSnapshot, error) {
	vgsList, err := h.listVGSWithStatus(ctx, status)
	if err != nil || len(vgsList) == 0 {
		return nil, err
	}

	return &vgsList[0], nil
}

// listVGSWithStatus lists all VolumeGroupSnapshots with specific status label owned by this handler
func (h *diffVolumeGroupSourceHandler) listVGSWithStatus(
	ctx context.Context, status string,
) ([]vgsv1beta1.VolumeGroupSnapshot, error) {
	vgsList := &vgsv1beta1.VolumeGroupSnapshotList{}

	listOptions := []client.ListOption{
		client.InNamespace(h.VolumeGroupSnapshotNamespace),
		client.MatchingLabels{
			VGSStatusLabelKey:  status,
			util.RGSOwnerLabel: h.VolumeGroupSnapshotName,
		},
	}

	if err := h.Client.List(ctx, vgsList, listOptions...); err != nil {
		return nil, err
	}

	return vgsList.Items, nil
}

// setVGSStatus updates the VolumeGroupSnapshot status label
func (h *diffVolumeGroupSourceHandler) setVGSStatus(
	ctx context.Context, vgs *vgsv1beta1.VolumeGroupSnapshot, status string,
) error {
	if vgs == nil {
		return nil
	}

	util.AddLabel(vgs, VGSStatusLabelKey, status)

	return h.Client.Update(ctx, vgs)
}

func (h *diffVolumeGroupSourceHandler) getPreviousSnapshotMap(
	ctx context.Context,
) (map[string]string, error) {
	vgs, err := h.findVGSWithStatus(ctx, VGSStatusPrevious)
	if err != nil {
		return nil, err
	}

	previousSnapshotMap := make(map[string]string)
	if vgs == nil {
		return previousSnapshotMap, nil
	}

	volumeSnapshots, err := util.GetVolumeSnapshotsOwnedByVolumeGroupSnapshot(ctx, h.Client, vgs, h.Logger)
	if err != nil {
		return nil, err
	}

	for _, vs := range volumeSnapshots {
		if vs.Status == nil || vs.Status.BoundVolumeSnapshotContentName == nil {
			continue
		}

		previousSnapshotMap[*vs.Spec.Source.PersistentVolumeClaimName] = vs.Name
	}

	return previousSnapshotMap, nil
}

// deleteRestoredPVCsForVGS deletes all restored PVCs for a given VolumeGroupSnapshot
func (h *diffVolumeGroupSourceHandler) deleteRestoredPVCsForVGS(
	ctx context.Context, vgs *vgsv1beta1.VolumeGroupSnapshot,
) error {
	logger := h.Logger.WithName("deleteRestoredPVCsForVGS").
		WithValues("VGSName", vgs.Name)

	if vgs.Status == nil {
		return nil
	}

	volumeSnapshots, err := util.GetVolumeSnapshotsOwnedByVolumeGroupSnapshot(ctx, h.Client, vgs, logger)
	if err != nil {
		return err
	}

	for idx := range volumeSnapshots {
		vs := &volumeSnapshots[idx]
		if err := h.deleteRestoredPVC(ctx, vs); err != nil {
			return err
		}
	}

	return nil
}

// populateVolumeGroupSnapshot sets labels, annotations, and spec on a VGS for diff sync.
func (h *diffVolumeGroupSourceHandler) populateVolumeGroupSnapshot(
	owner metav1.Object, vgs *vgsv1beta1.VolumeGroupSnapshot,
) error {
	if !vgs.DeletionTimestamp.IsZero() {
		return fmt.Errorf("the volume group snapshot is being deleted, need to wait")
	}

	if err := ctrl.SetControllerReference(owner, vgs, h.Client.Scheme()); err != nil {
		return err
	}

	util.AddLabel(vgs, util.CreatedByRamenLabel, "true")
	util.AddLabel(vgs, util.RGSOwnerLabel, owner.GetName())
	// Add status=current label for lifecycle tracking
	util.AddLabel(vgs, VGSStatusLabelKey, VGSStatusCurrent)
	util.AddAnnotation(vgs, volsync.OwnerNameAnnotation, owner.GetName())
	util.AddAnnotation(vgs, volsync.OwnerNamespaceAnnotation, owner.GetNamespace())

	vgs.Spec.VolumeGroupSnapshotClassName = &h.VolumeGroupSnapshotClassName
	vgs.Spec.Source.Selector = h.VolumeGroupLabel

	return nil
}

// CreateOrUpdateVolumeGroupSnapshot creates or reuses a VolumeGroupSnapshot with status label tracking.
// It follows the snapshot preservation pattern:
// 1. Look for existing VGS with status=current
// 2. If found and ready, reuse it
// 3. If not found, create new VGS with timestamp suffix and status=current label
func (h *diffVolumeGroupSourceHandler) CreateOrUpdateVolumeGroupSnapshot(
	ctx context.Context, owner metav1.Object,
) (bool, error) {
	logger := h.Logger.WithName("CreateOrUpdateVolumeGroupSnapshot")
	logger.Info("Create or update volume group snapshot with status label tracking")

	// Step 1: Look for existing VGS with status=current
	currentVGS, err := h.findVGSWithStatus(ctx, VGSStatusCurrent)
	if err != nil {
		logger.Error(err, "Failed to find current VGS")

		return false, err
	}

	if currentVGS != nil {
		// Check if it's being deleted
		if !currentVGS.DeletionTimestamp.IsZero() {
			logger.Info("Current VGS is being deleted, need to wait", "name", currentVGS.Name)

			return true, nil
		}

		// Check if ready
		if IsVGSReady(currentVGS) {
			logger.Info("Reusing existing ready current VGS", "name", currentVGS.Name)

			return false, nil
		}

		// Wait for it to become ready
		logger.Info("Current VGS exists but not ready yet, waiting", "name", currentVGS.Name)

		return true, nil
	}

	return h.createNewVGS(ctx, owner, logger)
}

// createNewVGS creates a new VolumeGroupSnapshot with a timestamp suffix and status=current label.
func (h *diffVolumeGroupSourceHandler) createNewVGS(
	ctx context.Context, owner metav1.Object, logger logr.Logger,
) (bool, error) {
	suffix := strconv.FormatInt(time.Now().Unix(), 10)
	vgsName := fmt.Sprintf("%s-%s", h.VolumeGroupSnapshotName, suffix)

	logger.Info("Creating new VGS with status=current", "name", vgsName)

	volumeGroupSnapshot := &vgsv1beta1.VolumeGroupSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: h.VolumeGroupSnapshotNamespace,
			Name:      vgsName,
		},
	}

	op, err := ctrlutil.CreateOrUpdate(ctx, h.Client, volumeGroupSnapshot, func() error {
		return h.populateVolumeGroupSnapshot(owner, volumeGroupSnapshot)
	})
	if err != nil {
		logger.Error(err, "Failed to CreateOrUpdate volume group snapshot")

		return false, err
	}

	logger.Info("VolumeGroupSnapshot successfully created or updated", "operation", op,
		"name", vgsName, "VGSUid", volumeGroupSnapshot.UID)

	return op == ctrlutil.OperationResultCreated || op == ctrlutil.OperationResultUpdated, nil
}

// cleanOlderPreviousVGS keeps only the newest previous VGS, deleting older ones and their restored PVCs.
func (h *diffVolumeGroupSourceHandler) cleanOlderPreviousVGS(
	ctx context.Context, logger logr.Logger,
) error {
	previousVGSList, err := h.listVGSWithStatus(ctx, VGSStatusPrevious)
	if err != nil {
		logger.Error(err, "Failed to list previous VGS")

		return err
	}

	if len(previousVGSList) <= 1 {
		return nil
	}

	// Find the newest previous VGS by CreationTimestamp
	latestIdx := 0
	latestTime := previousVGSList[0].CreationTimestamp

	for i := 1; i < len(previousVGSList); i++ {
		if previousVGSList[i].CreationTimestamp.After(latestTime.Time) {
			latestIdx = i
			latestTime = previousVGSList[i].CreationTimestamp
		}
	}

	for i := range previousVGSList {
		if i == latestIdx {
			continue
		}

		vgs := &previousVGSList[i]
		logger.Info("Deleting older previous VGS", "name", vgs.Name)

		if err := h.deleteRestoredPVCsForVGS(ctx, vgs); err != nil {
			return err
		}

		if err := h.Client.Delete(ctx, vgs); err != nil && !k8serrors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

// CleanVolumeGroupSnapshot implements the snapshot preservation pattern during cleanup:
// 1. Keep only the most recent previous VGS, delete older ones
// 2. Delete restored PVCs for current VGS (they're temporary)
// 3. Transition current VGS to previous status
// After cleanup, up to 2 previous VGS may exist (the kept one + the just-transitioned one).
func (h *diffVolumeGroupSourceHandler) CleanVolumeGroupSnapshot(
	ctx context.Context,
) error {
	logger := h.Logger.WithName("CleanVolumeGroupSnapshot")
	logger.Info("Starting VGS cleanup with snapshot preservation")

	// Step 1: Handle previous VGS - keep only the newest, delete rest
	if err := h.cleanOlderPreviousVGS(ctx, logger); err != nil {
		return err
	}

	// Step 2: Find current VGS and transition it to previous
	currentVGS, err := h.findVGSWithStatus(ctx, VGSStatusCurrent)
	if err != nil {
		logger.Error(err, "Failed to find current VGS")

		return err
	}

	if currentVGS == nil {
		logger.Info("No current VGS found to transition")

		return nil
	}

	logger.Info("Processing current VGS", "name", currentVGS.Name)

	// Delete restored PVCs for current VGS (they're temporary for sync)
	if err := h.deleteRestoredPVCsForVGS(ctx, currentVGS); err != nil {
		return err
	}

	// Transition status: current -> previous
	if err := h.setVGSStatus(ctx, currentVGS, VGSStatusPrevious); err != nil {
		return err
	}

	logger.Info("Successfully transitioned current VGS to previous", "name", currentVGS.Name)

	return nil
}

// RestoreVolumesFromVolumeGroupSnapshot restores VolumeGroupSnapshot to PVCs
// It finds the current VGS by status label instead of fixed name.
//
//nolint:dupl // intentional override — different VGS lookup, same post-processing
func (h *diffVolumeGroupSourceHandler) RestoreVolumesFromVolumeGroupSnapshot(
	ctx context.Context, owner metav1.Object,
) ([]RestoredPVC, error) {
	logger := h.Logger.WithName("RestoreFromVolumeGroupSnapshot")
	logger.Info("Finding current VGS by status label")

	// Find VGS with status=current instead of using fixed name
	vgs, err := h.findVGSWithStatus(ctx, VGSStatusCurrent)
	if err != nil {
		return nil, fmt.Errorf("failed to find current volume group snapshot: %w", err)
	}

	if vgs == nil {
		return nil, fmt.Errorf("no current VolumeGroupSnapshot found")
	}

	if !IsVGSReady(vgs) {
		return nil, fmt.Errorf("can't restore volume group snapshot: volume group snapshot is not ready to be used")
	}

	logger.Info("Found current VGS", "name", vgs.Name)

	volumeSnapshots, err := util.GetVolumeSnapshotsOwnedByVolumeGroupSnapshot(ctx, h.Client, vgs, logger)
	if err != nil {
		return nil, err
	}

	logger.Info("Restore: Found VolumeSnapshots", "len", len(volumeSnapshots), "in group", vgs.Name)

	return h.restorePVCsFromSnapshots(ctx, vgs, volumeSnapshots, owner, logger)
}

// CreateOrUpdateReplicationSourceForRestoredPVCs create or update replication source for each restored pvc
func (h *diffVolumeGroupSourceHandler) CreateOrUpdateReplicationSourceForRestoredPVCs(
	ctx context.Context,
	manual string,
	restoredPVCs []RestoredPVC,
	owner metav1.Object,
	vrg *ramendrv1alpha1.VolumeReplicationGroup,
	isSubmarinerEnabled bool,
) ([]*corev1.ObjectReference, bool, error) {
	logger := h.Logger.WithName("CreateReplicationSourceForRestoredPVCs").
		WithValues("NumberOfRestoredPVCs", len(restoredPVCs))
	logger.Info("Start to create replication source for restored PVCs")

	createdOrUpdated := false

	previousSnapshotMap, err := h.getPreviousSnapshotMap(ctx)
	if err != nil {
		return nil, false, err
	}

	replicationSources := []*corev1.ObjectReference{}

	for _, tmpRestoredPVC := range restoredPVCs {
		restoredPVC := tmpRestoredPVC

		ref, op, err := h.createOrUpdateDiffRS(
			ctx, manual, restoredPVC, previousSnapshotMap, owner, vrg, isSubmarinerEnabled, logger)
		if err != nil {
			return nil, createdOrUpdated, err
		}

		replicationSources = append(replicationSources, ref)

		createdOrUpdated = createdOrUpdated ||
			(op == ctrlutil.OperationResultCreated || op == ctrlutil.OperationResultUpdated)
	}

	logger.Info("Replication sources are successfully created for all restored PVCs")

	return replicationSources, createdOrUpdated, nil
}

//nolint:funlen
func (h *diffVolumeGroupSourceHandler) createOrUpdateDiffRS(
	ctx context.Context,
	manual string,
	restoredPVC RestoredPVC,
	previousSnapshotMap map[string]string,
	owner metav1.Object,
	vrg *ramendrv1alpha1.VolumeReplicationGroup,
	isSubmarinerEnabled bool,
	logger logr.Logger,
) (*corev1.ObjectReference, ctrlutil.OperationResult, error) {
	logger.Info("Create replication source for restored PVC", "RestoredPVC", restoredPVC.RestoredPVCName)

	originalPVCName := strings.TrimSuffix(restoredPVC.SourcePVCName, util.SuffixForFinalsyncPVC)
	replicationSourceNamespace := h.VolumeGroupSnapshotNamespace

	replicationSource := &volsyncv1alpha1.ReplicationSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      originalPVCName,
			Namespace: replicationSourceNamespace,
		},
	}

	rdService, err := h.resolveRDService(originalPVCName, restoredPVC.RestoredPVCName,
		vrg, replicationSourceNamespace, isSubmarinerEnabled, logger)
	if err != nil {
		return nil, "", err
	}

	provisioner, err := h.resolveProvisioner(ctx, restoredPVC.RestoredPVCName, replicationSourceNamespace, logger)
	if err != nil {
		return nil, "", err
	}

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
		replicationSource.Spec.RsyncTLS = nil
		replicationSource.Spec.External = &volsyncv1alpha1.ReplicationSourceExternalSpec{
			Provider: provisioner,
			Parameters: map[string]string{
				"keySecret":          h.VolsyncKeySecretName,
				"address":            rdService,
				"copyMethod":         string(volsyncv1alpha1.CopyMethodDirect),
				"volumeName":         restoredPVC.SourcePVCName,
				"baseSnapshotName":   previousSnapshotMap[restoredPVC.SourcePVCName],
				"targetSnapshotName": restoredPVC.VolumeSnapshotName,
			},
		}

		return nil
	})
	if err != nil {
		logger.Error(err, "Failed to CreateOrUpdate replication source", "RestoredPVC", restoredPVC.RestoredPVCName)

		return nil, op, err
	}

	logger.Info("replication source successfully reconciled", "operation", op, "RestoredPVC", restoredPVC.RestoredPVCName)

	return &corev1.ObjectReference{
		APIVersion: replicationSource.APIVersion,
		Kind:       replicationSource.Kind,
		Namespace:  replicationSource.Namespace,
		Name:       replicationSource.Name,
	}, op, nil
}

// resolveProvisioner gets the StorageClass provisioner for a PVC.
func (h *diffVolumeGroupSourceHandler) resolveProvisioner(
	ctx context.Context, pvcName, namespace string, logger logr.Logger,
) (string, error) {
	pvc, err := util.GetPVC(ctx, h.Client, types.NamespacedName{
		Name:      pvcName,
		Namespace: namespace,
	})
	if err != nil {
		logger.Error(err, "Failed to get restored PVC for replication source", "RestoredPVC", pvcName)

		return "", err
	}

	if pvc.Spec.StorageClassName != nil {
		storageClass, err := GetStorageClass(ctx, h.Client, pvc.Spec.StorageClassName)
		if err != nil {
			logger.Error(err, "Failed to get storage class for restored PVC", "RestoredPVC", pvcName)

			return "", err
		}

		return storageClass.Provisioner, nil
	}

	return h.DefaultCephFSCSIDriverName, nil
}
