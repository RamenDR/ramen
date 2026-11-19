// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package cephfscg

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	vsv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/ramendr/ramen/internal/controller/util"
)

const (
	// RGSProtectionFinalizer keeps a ReplicationGroupSource until its
	// VolumeGroupSnapshots and member VolumeSnapshots are gone.
	RGSProtectionFinalizer = "replicationgroupsources.ramendr.openshift.io/rgs-protection"

	// VolumeSnapshotInGroupProtectionFinalizer is set by snapshot-controller on a
	// VolumeSnapshot that belongs to a VolumeGroupSnapshot.
	VolumeSnapshotInGroupProtectionFinalizer = "snapshot.storage.kubernetes.io/volumesnapshot-in-group-protection"

	volumeGroupSnapshotBoundFinalizer        = "groupsnapshot.storage.kubernetes.io/volumegroupsnapshot-bound-protection"
	volumeGroupSnapshotBoundFinalizerPrivate = "groupsnapshot.storage.openshift.io/" +
		"volumegroupsnapshot-bound-protection"
	volumeGroupSnapshotContentFinalizer = "groupsnapshot.storage.kubernetes.io/" +
		"volumegroupsnapshotcontent-bound-protection"
	volumeGroupSnapshotContentFinalizerPrivate = "groupsnapshot.storage.openshift.io/" +
		"volumegroupsnapshotcontent-bound-protection"
	volumeSnapshotContentBoundFinalizer = "snapshot.storage.kubernetes.io/volumesnapshotcontent-bound-protection"
)

var (
	// ErrVolumeGroupSnapshotCleanupPending means a VolumeGroupSnapshot or one of
	// its members is still terminating.
	ErrVolumeGroupSnapshotCleanupPending = errors.New(
		"volume group snapshot cleanup is still in progress")

	// ErrReplicationGroupSourceDeleting means one or more ReplicationGroupSource
	// objects still exist after a delete was requested.
	ErrReplicationGroupSourceDeleting = errors.New("replication group source is still deleting")
)

func (h *volumeGroupSourceHandler) cleanupNamedVolumeGroupSnapshot(ctx context.Context, vgsName string) error {
	logger := h.Logger.WithName("cleanupNamedVolumeGroupSnapshot").WithValues("VolumeGroupSnapshot", vgsName)

	vgs, err := h.getVolumeGroupSnapshot(ctx, vgsName)
	if err != nil {
		return err
	}

	pending := false

	if vgs != nil {
		if err := h.deleteRestoredPVCsForGroup(ctx, vgsName, vgs.GetUID()); err != nil {
			if !errors.Is(err, ErrVolumeGroupSnapshotCleanupPending) {
				return err
			}

			pending = true
		}

		if err := h.deleteVolumeGroupSnapshot(ctx, logger, vgs); err != nil {
			return err
		}
	}

	if err := h.deleteGroupSnapshotContents(ctx, logger, vgsName); err != nil {
		if !errors.Is(err, ErrVolumeGroupSnapshotCleanupPending) {
			return err
		}

		pending = true
	}

	if err := h.deleteMemberSnapshots(ctx, logger, vgsName); err != nil {
		return err
	}

	if pending {
		return ErrVolumeGroupSnapshotCleanupPending
	}

	logger.Info("Volume group snapshot cleanup complete")

	return nil
}

func (h *volumeGroupSourceHandler) getVolumeGroupSnapshot(
	ctx context.Context, vgsName string,
) (client.Object, error) {
	vgs, err := util.GetVolumeGroupSnapshot(ctx, h.Client, vgsName, h.VolumeGroupSnapshotNamespace)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, nil
		}

		return nil, err
	}

	return vgs, nil
}

func (h *volumeGroupSourceHandler) deleteRestoredPVCsForGroup(
	ctx context.Context, vgsName string, vgsUID types.UID,
) error {
	members, err := h.memberVolumeSnapshots(ctx, vgsName, vgsUID)
	if err != nil {
		return err
	}

	pending := false

	for idx := range members {
		if err := h.deleteRestoredPVC(ctx, &members[idx]); err != nil {
			if !errors.Is(err, ErrVolumeGroupSnapshotCleanupPending) {
				return err
			}

			pending = true
		}
	}

	if pending {
		return ErrVolumeGroupSnapshotCleanupPending
	}

	return nil
}

func (h *volumeGroupSourceHandler) deleteVolumeGroupSnapshot(
	ctx context.Context, logger logr.Logger, vgs client.Object,
) error {
	if err := ignoreNotFound(h.Client.Delete(ctx, vgs)); err != nil {
		logger.Error(err, "Failed to delete volume group snapshot")

		return err
	}

	current, err := h.getVolumeGroupSnapshot(ctx, vgs.GetName())
	if err != nil {
		return err
	}

	if current == nil {
		return nil
	}

	return h.finishDeletingVolumeGroupSnapshot(ctx, logger, current)
}

func (h *volumeGroupSourceHandler) finishDeletingVolumeGroupSnapshot(
	ctx context.Context, logger logr.Logger, current client.Object,
) error {
	// A ready group snapshot is left for snapshot-controller. A not-ready one
	// never finishes that path, so drop the bound-protection finalizer ourselves.
	if util.VolumeGroupSnapshotIsReady(current) {
		logger.Info("Waiting for ready VolumeGroupSnapshot to finish deleting")

		return ErrVolumeGroupSnapshotCleanupPending
	}

	if err := h.stripVolumeGroupSnapshotFinalizers(ctx, current); err != nil {
		return err
	}

	if err := ignoreNotFound(h.Client.Delete(ctx, current)); err != nil {
		return err
	}

	current, err := h.getVolumeGroupSnapshot(ctx, current.GetName())
	if err != nil {
		return err
	}

	if current != nil {
		logger.Info("Waiting for VolumeGroupSnapshot to be deleted")

		return ErrVolumeGroupSnapshotCleanupPending
	}

	return nil
}

func (h *volumeGroupSourceHandler) stripVolumeGroupSnapshotFinalizers(
	ctx context.Context, current client.Object,
) error {
	if !stripFinalizers(current, volumeGroupSnapshotBoundFinalizer, volumeGroupSnapshotBoundFinalizerPrivate) {
		return nil
	}

	return ignoreNotFound(h.Client.Update(ctx, current))
}

func (h *volumeGroupSourceHandler) deleteMemberSnapshots(
	ctx context.Context, logger logr.Logger, vgsName string,
) error {
	members, err := h.memberVolumeSnapshots(ctx, vgsName, "")
	if err != nil {
		return err
	}

	pending := false

	for idx := range members {
		if err := h.releaseMemberSnapshot(ctx, logger, &members[idx]); err != nil {
			if !errors.Is(err, ErrVolumeGroupSnapshotCleanupPending) {
				return err
			}

			pending = true
		}
	}

	if pending {
		return ErrVolumeGroupSnapshotCleanupPending
	}

	return nil
}

func (h *volumeGroupSourceHandler) releaseMemberSnapshot(
	ctx context.Context, logger logr.Logger, vs *vsv1.VolumeSnapshot,
) error {
	latest := &vsv1.VolumeSnapshot{}
	key := types.NamespacedName{Name: vs.Name, Namespace: vs.Namespace}

	if err := h.Client.Get(ctx, key, latest); err != nil {
		return ignoreNotFound(err)
	}

	pvcPending, err := restoredPVCCleanupPending(h.deleteRestoredPVC(ctx, latest))
	if err != nil {
		return err
	}

	if err := h.deleteMemberSnapshotContent(ctx, latest); err != nil {
		return err
	}

	return h.deleteMemberSnapshotObject(ctx, logger, key, latest, pvcPending)
}

func (h *volumeGroupSourceHandler) deleteMemberSnapshotObject(
	ctx context.Context,
	logger logr.Logger,
	key types.NamespacedName,
	latest *vsv1.VolumeSnapshot,
	pvcPending bool,
) error {
	if ctrlutil.RemoveFinalizer(latest, VolumeSnapshotInGroupProtectionFinalizer) {
		if err := ignoreNotFound(h.Client.Update(ctx, latest)); err != nil {
			return err
		}
	}

	if err := ignoreNotFound(h.Client.Delete(ctx, latest)); err != nil {
		logger.Error(err, "Failed to delete member VolumeSnapshot", "name", latest.Name)

		return err
	}

	err := h.Client.Get(ctx, key, latest)
	if err != nil {
		return memberSnapshotGone(err, pvcPending)
	}

	logger.Info("Waiting for member VolumeSnapshot to be deleted", "name", latest.Name)

	return ErrVolumeGroupSnapshotCleanupPending
}

func (h *volumeGroupSourceHandler) deleteMemberSnapshotContent(
	ctx context.Context, vs *vsv1.VolumeSnapshot,
) error {
	name, ok := boundVolumeSnapshotContentName(vs)
	if !ok {
		return nil
	}

	content := &vsv1.VolumeSnapshotContent{}

	if err := h.Client.Get(ctx, types.NamespacedName{Name: name}, content); err != nil {
		return ignoreMissing(err)
	}

	if err := h.stripUnreadySnapshotContentFinalizer(ctx, vs, content); err != nil {
		return err
	}

	if err := h.Client.Delete(ctx, content); err != nil {
		return ignoreMissing(err)
	}

	if err := h.Client.Get(ctx, types.NamespacedName{Name: name}, content); err != nil {
		return ignoreMissing(err)
	}

	return ErrVolumeGroupSnapshotCleanupPending
}

func (h *volumeGroupSourceHandler) stripUnreadySnapshotContentFinalizer(
	ctx context.Context, vs *vsv1.VolumeSnapshot, content *vsv1.VolumeSnapshotContent,
) error {
	if volumeSnapshotIsReady(vs) || !stripFinalizers(content, volumeSnapshotContentBoundFinalizer) {
		return nil
	}

	return ignoreMissing(h.Client.Update(ctx, content))
}

func (h *volumeGroupSourceHandler) deleteGroupSnapshotContents(
	ctx context.Context, logger logr.Logger, vgsName string,
) error {
	refs, err := util.ListVolumeGroupSnapshotContentRefs(ctx, h.Client)
	if err != nil {
		return ignoreMissing(err)
	}

	pending := false

	for _, ref := range refs {
		if ref.VolumeGroupSnapshotName != vgsName || ref.VolumeGroupSnapshotNamespace != h.VolumeGroupSnapshotNamespace {
			continue
		}

		if err := h.deleteBoundGroupContent(ctx, ref.Name); err != nil {
			if !errors.Is(err, ErrVolumeGroupSnapshotCleanupPending) {
				logger.Error(err, "Failed to delete VolumeGroupSnapshotContent", "name", ref.Name)

				return err
			}

			pending = true
		}
	}

	if pending {
		return ErrVolumeGroupSnapshotCleanupPending
	}

	return nil
}

func (h *volumeGroupSourceHandler) deleteBoundGroupContent(ctx context.Context, name string) error {
	if name == "" {
		return nil
	}

	content := util.NewVolumeGroupSnapshotContent(name)

	if err := h.Client.Get(ctx, types.NamespacedName{Name: name}, content); err != nil {
		return ignoreMissing(err)
	}

	if stripFinalizers(content, volumeGroupSnapshotContentFinalizer, volumeGroupSnapshotContentFinalizerPrivate) {
		if err := h.Client.Update(ctx, content); err != nil {
			return ignoreMissing(err)
		}
	}

	if err := h.Client.Delete(ctx, content); err != nil {
		return ignoreMissing(err)
	}

	if err := h.Client.Get(ctx, types.NamespacedName{Name: name}, content); err != nil {
		return ignoreMissing(err)
	}

	return ErrVolumeGroupSnapshotCleanupPending
}

func (h *volumeGroupSourceHandler) memberVolumeSnapshots(
	ctx context.Context, vgsName string, vgsUID types.UID,
) ([]vsv1.VolumeSnapshot, error) {
	list := &vsv1.VolumeSnapshotList{}
	if err := h.Client.List(ctx, list, client.InNamespace(h.VolumeGroupSnapshotNamespace)); err != nil {
		return nil, err
	}

	members := make([]vsv1.VolumeSnapshot, 0, len(list.Items))

	for i := range list.Items {
		if volumeSnapshotBelongsToGroup(&list.Items[i], vgsName, vgsUID) {
			members = append(members, list.Items[i])
		}
	}

	return members, nil
}

func volumeSnapshotBelongsToGroup(vs *vsv1.VolumeSnapshot, vgsName string, vgsUID types.UID) bool {
	for _, owner := range vs.OwnerReferences {
		if owner.Kind != "VolumeGroupSnapshot" || owner.Name != vgsName {
			continue
		}

		if vgsUID == "" || owner.UID == vgsUID {
			return true
		}
	}

	if vs.Status != nil && vs.Status.VolumeGroupSnapshotName != nil &&
		*vs.Status.VolumeGroupSnapshotName == vgsName {
		return true
	}

	return false
}

func (h *volumeGroupSourceHandler) groupNamesReferencedByMembers(
	ctx context.Context, match func(string) bool,
) ([]string, error) {
	list := &vsv1.VolumeSnapshotList{}
	if err := h.Client.List(ctx, list, client.InNamespace(h.VolumeGroupSnapshotNamespace)); err != nil {
		return nil, err
	}

	names := map[string]struct{}{}

	for i := range list.Items {
		vs := &list.Items[i]
		for _, owner := range vs.OwnerReferences {
			if owner.Kind == "VolumeGroupSnapshot" && match(owner.Name) {
				names[owner.Name] = struct{}{}
			}
		}

		if vs.Status != nil && vs.Status.VolumeGroupSnapshotName != nil && match(*vs.Status.VolumeGroupSnapshotName) {
			names[*vs.Status.VolumeGroupSnapshotName] = struct{}{}
		}
	}

	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}

	return out, nil
}

func restoredPVCCleanupPending(err error) (bool, error) {
	if errors.Is(err, ErrVolumeGroupSnapshotCleanupPending) {
		return true, nil
	}

	return false, err
}

func memberSnapshotGone(err error, pvcPending bool) error {
	if !k8serrors.IsNotFound(err) {
		return err
	}

	if pvcPending {
		return ErrVolumeGroupSnapshotCleanupPending
	}

	return nil
}

func boundVolumeSnapshotContentName(vs *vsv1.VolumeSnapshot) (string, bool) {
	if vs.Status == nil || vs.Status.BoundVolumeSnapshotContentName == nil {
		return "", false
	}

	name := *vs.Status.BoundVolumeSnapshotContentName
	if name == "" {
		return "", false
	}

	return name, true
}

func volumeSnapshotIsReady(vs *vsv1.VolumeSnapshot) bool {
	return vs.Status != nil && vs.Status.ReadyToUse != nil && *vs.Status.ReadyToUse
}

func ignoreNotFound(err error) error {
	if err == nil || k8serrors.IsNotFound(err) {
		return nil
	}

	return err
}

func stripFinalizers(obj client.Object, finalizers ...string) bool {
	changed := false

	for _, finalizer := range finalizers {
		if ctrlutil.RemoveFinalizer(obj, finalizer) {
			changed = true
		}
	}

	return changed
}

func ignoreMissing(err error) error {
	if err == nil || k8serrors.IsNotFound(err) || meta.IsNoMatchError(err) {
		return nil
	}

	return err
}

// ownedGroupSnapshotNames returns VolumeGroupSnapshot names for this RGS, including
// groups whose object is already gone but whose member snapshots remain.
func (h *volumeGroupSourceHandler) ownedGroupSnapshotNames(
	ctx context.Context, match func(string) bool,
) ([]string, error) {
	listed, err := util.ListVolumeGroupSnapshots(ctx, h.Client,
		client.InNamespace(h.VolumeGroupSnapshotNamespace),
		client.MatchingLabels{util.RGSOwnerLabel: h.VolumeGroupSnapshotName},
	)
	if err != nil {
		return nil, fmt.Errorf("list volume group snapshots: %w", err)
	}

	names := map[string]struct{}{}

	for _, vgs := range listed {
		if match(vgs.GetName()) {
			names[vgs.GetName()] = struct{}{}
		}
	}

	referenced, err := h.groupNamesReferencedByMembers(ctx, match)
	if err != nil {
		return nil, err
	}

	for _, name := range referenced {
		names[name] = struct{}{}
	}

	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}

	return out, nil
}
