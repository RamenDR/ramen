// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package volsync

import (
	"fmt"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	"github.com/go-logr/logr"
	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/util"
)

// CreateCurrentStateSnapshot creates a VolumeSnapshot of the app PVC's current state.
// Used before diff sync rollback to capture the (possibly corrupted) state for diff calculation.
func (v *VSHandler) CreateCurrentStateSnapshot(
	rdSpec ramendrv1alpha1.VolSyncReplicationDestinationSpec,
) (string, error) {
	pvcName := rdSpec.ProtectedPVC.Name
	namespace := rdSpec.ProtectedPVC.Namespace
	snapshotName := fmt.Sprintf("current-state-%s", pvcName)

	storageClass, err := v.getStorageClass(rdSpec.ProtectedPVC.StorageClassName)
	if err != nil {
		return "", err
	}

	volumeSnapshotClassName, err := v.getVolumeSnapshotClassFromPVCStorageClass(storageClass)
	if err != nil {
		return "", err
	}

	snap := &snapv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      snapshotName,
			Namespace: namespace,
		},
	}

	_, err = ctrlutil.CreateOrUpdate(v.ctx, v.client, snap, func() error {
		util.AddLabel(snap, util.CreatedByRamenLabel, "true")
		util.AddLabel(snap, util.VRGOwnerNameLabel, v.owner.GetName())
		util.AddLabel(snap, util.VRGOwnerNamespaceLabel, v.owner.GetNamespace())

		snap.Spec.Source.PersistentVolumeClaimName = &pvcName
		snap.Spec.VolumeSnapshotClassName = &volumeSnapshotClassName

		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to create current state snapshot for %s: %w", pvcName, err)
	}

	v.log.Info("Current state snapshot created", "snapshotName", snapshotName, "pvcName", pvcName)

	return snapshotName, nil
}

// IsSnapshotReady checks if a VolumeSnapshot has ReadyToUse=true.
func (v *VSHandler) IsSnapshotReady(name, namespace string) (bool, error) {
	snap := &snapv1.VolumeSnapshot{}

	err := v.client.Get(v.ctx, types.NamespacedName{Name: name, Namespace: namespace}, snap)
	if err != nil {
		return false, err
	}

	if snap.Status == nil || snap.Status.ReadyToUse == nil || !*snap.Status.ReadyToUse {
		return false, nil
	}

	return true, nil
}

// reconcileDiffLocalReplication orchestrates diff-based local rollback using External spec.
// Instead of full rsync copy, uses the ceph-volsync-plugin to transfer only changed blocks.
//
//nolint:funlen
func (v *VSHandler) reconcileDiffLocalReplication(
	rd *volsyncv1alpha1.ReplicationDestination,
	rdSpec ramendrv1alpha1.VolSyncReplicationDestinationSpec,
	snapshotRef *corev1.TypedLocalObjectReference,
	pskSecretName string,
	l logr.Logger,
) (*volsyncv1alpha1.ReplicationDestination, *volsyncv1alpha1.ReplicationSource, error) {
	// Step 1: Create current state snapshot of the app PVC
	currentSnapName, err := v.CreateCurrentStateSnapshot(rdSpec)
	if err != nil {
		return nil, nil, err
	}

	// Step 2: Wait for snapshot ready (return-and-retry)
	ready, err := v.IsSnapshotReady(currentSnapName, rdSpec.ProtectedPVC.Namespace)
	if err != nil {
		return nil, nil, fmt.Errorf("error checking current state snapshot readiness: %w", err)
	}

	if !ready {
		return nil, nil, fmt.Errorf("waiting for current state snapshot %s to be ready", currentSnapName)
	}

	// Step 3: Create diff local RD with External spec
	lrd, err := v.ReconcileDiffLocalRD(rdSpec, pskSecretName)
	if lrd == nil || err != nil {
		return nil, nil, fmt.Errorf("failed to reconcile diff localRD (%w)", err)
	}

	// Step 4: Create shallow PVC from LatestImage + diff local RS with External spec
	lrs, err := v.ReconcileDiffLocalRS(rd, &rdSpec, snapshotRef, currentSnapName,
		pskSecretName, *lrd.Status.RsyncTLS.Address)
	if err != nil {
		return lrd, nil, fmt.Errorf("failed to reconcile diff localRS (%w)", err)
	}

	l.V(1).Info(fmt.Sprintf("Diff local replication reconcile complete lrd=%s, lrs=%s", lrd.Name, lrs.Name))

	return lrd, lrs, nil
}

// ReconcileDiffLocalRD creates a local ReplicationDestination with External spec for diff rollback.
//
//nolint:funlen
func (v *VSHandler) ReconcileDiffLocalRD(
	rdSpec ramendrv1alpha1.VolSyncReplicationDestinationSpec,
	pskSecretName string,
) (*volsyncv1alpha1.ReplicationDestination, error) {
	v.log.Info("Reconciling diff localRD", "rdSpec name", rdSpec.ProtectedPVC.Name)

	lrd := &volsyncv1alpha1.ReplicationDestination{
		ObjectMeta: metav1.ObjectMeta{
			Name:      util.GetLocalReplicationName(rdSpec.ProtectedPVC.Name),
			Namespace: rdSpec.ProtectedPVC.Namespace,
		},
	}

	err := v.EnsurePVCforDirectCopy(v.ctx, rdSpec)
	if err != nil {
		return nil, err
	}

	storageClass, err := v.getStorageClass(rdSpec.ProtectedPVC.StorageClassName)
	if err != nil {
		return nil, err
	}

	volumeSnapshotClassName, err := v.getVolumeSnapshotClassFromPVCStorageClass(storageClass)
	if err != nil {
		return nil, err
	}

	pvcAccessModes := []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
	if len(rdSpec.ProtectedPVC.AccessModes) > 0 {
		pvcAccessModes = rdSpec.ProtectedPVC.AccessModes
	}

	op, err := ctrlutil.CreateOrUpdate(v.ctx, v.client, lrd, func() error {
		util.AddLabel(lrd, util.CreatedByRamenLabel, "true")
		util.AddLabel(lrd, util.VRGOwnerNameLabel, v.owner.GetName())
		util.AddLabel(lrd, util.VRGOwnerNamespaceLabel, v.owner.GetNamespace())
		util.AddLabel(lrd, VolSyncDoNotDeleteLabel, VolSyncDoNotDeleteLabelVal)

		lrd.Spec.RsyncTLS = nil
		lrd.Spec.External = &volsyncv1alpha1.ReplicationDestinationExternalSpec{
			Provider: storageClass.Provisioner,
			Parameters: map[string]string{
				"copyMethod":              string(volsyncv1alpha1.CopyMethodDirect),
				"destinationPVC":          rdSpec.ProtectedPVC.Name,
				"keySecret":               pskSecretName,
				"capacity":                rdSpec.ProtectedPVC.Resources.Requests.Storage().String(),
				"storageClassName":        *rdSpec.ProtectedPVC.StorageClassName,
				"accessModes":             string(pvcAccessModes[0]),
				"volumeSnapshotClassName": volumeSnapshotClassName,
			},
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Wait for address — plugin populates Status.RsyncTLS.Address even for External spec
	if lrd.Status == nil || lrd.Status.RsyncTLS == nil || lrd.Status.RsyncTLS.Address == nil {
		v.log.V(1).Info("Diff local ReplicationDestination waiting for Address...")

		return nil, fmt.Errorf("waiting for address")
	}

	v.log.V(1).Info("Diff local ReplicationDestination Reconcile Complete", "op", op)

	return lrd, nil
}

// ReconcileDiffLocalRS creates a local ReplicationSource with External spec for diff rollback.
//
//nolint:funlen
func (v *VSHandler) ReconcileDiffLocalRS(
	rd *volsyncv1alpha1.ReplicationDestination,
	rdSpec *ramendrv1alpha1.VolSyncReplicationDestinationSpec,
	snapshotRef *corev1.TypedLocalObjectReference,
	currentStateSnapshotName string,
	pskSecretName, address string,
) (*volsyncv1alpha1.ReplicationSource, error) {
	v.log.Info("Reconciling diff localRS", "RD", rd.GetName())

	// Reuse setupLocalRS to validate LatestImage + create shallow PVC from snapshot
	pvc, err := v.setupLocalRS(rd, rdSpec, snapshotRef)
	if err != nil {
		return nil, err
	}

	storageClass, err := v.getStorageClass(rdSpec.ProtectedPVC.StorageClassName)
	if err != nil {
		return nil, err
	}

	lrs := &volsyncv1alpha1.ReplicationSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      util.GetLocalReplicationName(rdSpec.ProtectedPVC.Name),
			Namespace: rdSpec.ProtectedPVC.Namespace,
		},
	}

	op, err := ctrlutil.CreateOrUpdate(v.ctx, v.client, lrs, func() error {
		util.AddLabel(lrs, util.CreatedByRamenLabel, "true")
		util.AddLabel(lrs, util.VRGOwnerNameLabel, v.owner.GetName())
		util.AddLabel(lrs, util.VRGOwnerNamespaceLabel, v.owner.GetNamespace())

		lrs.Spec.SourcePVC = pvc.GetName()
		lrs.Spec.Trigger = &volsyncv1alpha1.ReplicationSourceTriggerSpec{
			Manual: pvc.GetName(),
		}

		lrs.Spec.RsyncTLS = nil
		lrs.Spec.External = &volsyncv1alpha1.ReplicationSourceExternalSpec{
			Provider: storageClass.Provisioner,
			Parameters: map[string]string{
				"copyMethod":         string(volsyncv1alpha1.CopyMethodDirect),
				"volumeName":         rdSpec.ProtectedPVC.Name,
				"baseSnapshotName":   currentStateSnapshotName,
				"targetSnapshotName": snapshotRef.Name,
				"address":            address,
				"keySecret":          pskSecretName,
			},
		}

		return nil
	})

	v.log.V(1).Info("Diff local ReplicationSource createOrUpdate Complete", "op", op, "error", err)

	if err != nil {
		return nil, err
	}

	return lrs, nil
}
