// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"fmt"
	"reflect"
	"slices"

	volrep "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/util"
)

func isOffloadedByPeerClass(scName, sID string, peerClass *ramendrv1alpha1.PeerClass) bool {
	if scName != peerClass.StorageClassName {
		return false
	}

	if !slices.Contains(peerClass.StorageID, sID) {
		return false
	}

	if !peerClass.Offloaded {
		return false
	}

	return true
}

// pvcsAreOffloaded reports that these PVCs use an offloaded StorageClass.
// The first PVC is enough, because a mix of offloaded and non-offloaded PVCs is rejected.
// A StorageClass lookup failure is treated as not offloaded.
func (v *VRGInstance) pvcsAreOffloaded(pvcs []*corev1.PersistentVolumeClaim) bool {
	if len(pvcs) == 0 {
		return false
	}

	storageClass, err := v.getStorageClass(pvcs[0])
	if err != nil {
		return false
	}

	return util.HasLabel(storageClass, StorageOffloadedLabel)
}

// offloadedSync reports that spec.async is nil and the StorageClass is offloaded.
// The caller checks spec.sync. A lookup failure is not offloaded.
func (v *VRGInstance) offloadedSync(offloaded bool) bool {
	return v.instance.Spec.Async == nil && offloaded
}

func (v *VRGInstance) pvStorageIsOffloaded(pv *corev1.PersistentVolume) bool {
	if pv == nil || pv.Spec.StorageClassName == "" {
		return false
	}

	scName := pv.Spec.StorageClassName

	storageClass, err := v.getStorageClassFromSCName(&scName)
	if err != nil {
		return false
	}

	return util.HasLabel(storageClass, StorageOffloadedLabel)
}

func (v *VRGInstance) isOffloadedByPeerClasses(
	sc *storagev1.StorageClass, peerClasses []ramendrv1alpha1.PeerClass,
) (bool, error) {
	if !util.HasLabel(sc, StorageIDLabel) {
		return false, fmt.Errorf("missing %s label in StorageClass (%s)", StorageIDLabel, sc.GetName())
	}

	sID := sc.GetLabels()[StorageIDLabel]

	for idx := range peerClasses {
		if isOffloadedByPeerClass(sc.GetName(), sID, &peerClasses[idx]) {
			return true, nil
		}
	}

	return false, nil
}

func (v *VRGInstance) isGloballyOffloadedByPeerClasses(
	scName string, peerClasses []ramendrv1alpha1.PeerClass,
) (bool, string) {
	for idx := range peerClasses {
		pc := &peerClasses[idx]

		if scName == pc.StorageClassName && pc.Global {
			return true, pc.GroupReplicationID
		}
	}

	return false, ""
}

func (v *VRGInstance) hasGlobalPeerClass(peerClasses []ramendrv1alpha1.PeerClass) bool {
	for idx := range peerClasses {
		if peerClasses[idx].Global {
			return true
		}
	}

	return false
}

// processOffloadedPVCs ensures either all PVCs for the VRG are offloaded, or none are. IF all are offloaded then
// the VR PVC list is updated with the PVC list
//
//nolint:gocognit,cyclop,funlen
func (v *VRGInstance) processOffloadedPVCs(
	pvcList *corev1.PersistentVolumeClaimList,
	peerClasses []ramendrv1alpha1.PeerClass,
) (bool, error) {
	if len(peerClasses) == 0 {
		return false, nil
	}

	offloaded := false

	for idx := range pvcList.Items {
		pvc := &pvcList.Items[idx]

		storageClass, err := v.getStorageClass(pvc)
		if err != nil {
			return false, err
		}

		pvcOffloadedBySC := util.HasLabel(storageClass, StorageOffloadedLabel)

		pvcOffloadedByPeer, err := v.isOffloadedByPeerClasses(storageClass, peerClasses)
		if err != nil {
			return false, err
		}

		if pvcOffloadedByPeer != pvcOffloadedBySC {
			return false, fmt.Errorf(
				"mismatched peerClass offload support (%t) with current StorageClass "+
					"(%s) offload support (%t) for PVC (%s/%s)",
				pvcOffloadedByPeer,
				*pvc.Spec.StorageClassName,
				pvcOffloadedBySC,
				pvc.GetName(),
				pvc.GetNamespace(),
			)
		}

		if idx == 0 {
			if pvcOffloadedBySC {
				offloaded = true
			}

			continue
		}

		if pvcOffloadedBySC == offloaded {
			continue
		}

		return false, fmt.Errorf("invalid list of PVCs to protect, found a mix of offloaded and non-offloaded PVCs")
	}

	if !offloaded {
		return offloaded, nil
	}

	for idx := range pvcList.Items {
		pvc := &pvcList.Items[idx]

		if err := v.addVolRepConsistencyGroupLabel(pvc); err != nil {
			return offloaded, fmt.Errorf("failed to label offloaded PVC %s/%s for consistency group (%w)",
				pvc.GetNamespace(), pvc.GetName(), err)
		}
	}

	v.volRepPVCs = make([]corev1.PersistentVolumeClaim, len(pvcList.Items))
	copy(v.volRepPVCs, pvcList.Items)

	v.log.Info(fmt.Sprintf("Found %d PVCs targeted for offloaded protection", len(v.volRepPVCs)))

	return offloaded, nil
}

// processGloballyOffloadedPVCs validates that all offloaded PVCs in the list are
// globally offloaded, and that all PVCs share the same GroupReplicationID. If so,
// the VRG is labeled with the global VGR label.
func (v *VRGInstance) processGloballyOffloadedPVCs(
	pvcList *corev1.PersistentVolumeClaimList, peerClasses []ramendrv1alpha1.PeerClass,
) error {
	if !v.hasGlobalPeerClass(peerClasses) {
		return nil
	}

	var grID string

	for idx := range pvcList.Items {
		pvc := &pvcList.Items[idx]

		pvcGlobal, pvcGRID := v.isGloballyOffloadedByPeerClasses(*pvc.Spec.StorageClassName, peerClasses)

		if !pvcGlobal {
			return fmt.Errorf("peerClass for StorageClass (%s) is not globally offloaded for PVC (%s/%s)",
				*pvc.Spec.StorageClassName, pvc.GetNamespace(), pvc.GetName())
		}

		if idx == 0 {
			grID = pvcGRID

			continue
		}

		if pvcGRID != grID {
			return fmt.Errorf("not all PVCs share the same GroupReplicationID, cannot use global VGR")
		}
	}

	return v.addGlobalVGRLabel(grID)
}

// --- Offloaded VolumeGroupReplicationClass selection ---

func offloadedVGRClassMatches(
	vgrClass *volrep.VolumeGroupReplicationClass,
	provisioner, storageID, groupReplicationID string,
) bool {
	if vgrClass.Spec.Provisioner != provisioner {
		return false
	}

	if vgrClass.GetLabels()[StorageIDLabel] != storageID {
		return false
	}

	return vgrClass.GetLabels()[GroupReplicationIDLabel] == groupReplicationID
}

// selectOffloadedVGRClass selects a VolumeGroupReplicationClass for offloaded storage.
// Match is provisioner, storageID, and groupReplicationID. Scheduling interval is not
// compared: the storage provider owns replication timing. Classes are listed here so
// selection does not depend on Spec.Async.
func (v *VRGInstance) selectOffloadedVGRClass(pvc *corev1.PersistentVolumeClaim) (client.Object, error) {
	storageClass, err := v.getStorageClass(pvc)
	if err != nil {
		return nil, err
	}

	storageID, ok := storageClass.GetLabels()[StorageIDLabel]
	if !ok {
		return nil, fmt.Errorf("missing %s label in StorageClass (%s)", StorageIDLabel, storageClass.GetName())
	}

	groupReplicationID, ok := storageClass.GetLabels()[GroupReplicationIDLabel]
	if !ok {
		return nil, fmt.Errorf("missing %s label in StorageClass (%s)", GroupReplicationIDLabel, storageClass.GetName())
	}

	vgrClassList := &volrep.VolumeGroupReplicationClassList{}
	if err := v.reconciler.List(v.ctx, vgrClassList); err != nil {
		return nil, fmt.Errorf("failed to list VolumeGroupReplicationClasses (%w)", err)
	}

	matches := []client.Object{}

	for idx := range vgrClassList.Items {
		vgrClass := &vgrClassList.Items[idx]
		if !offloadedVGRClassMatches(vgrClass, storageClass.Provisioner, storageID, groupReplicationID) {
			continue
		}

		matches = append(matches, vgrClass)
	}

	switch len(matches) {
	case 0:
		v.log.Info("No VolumeGroupReplicationClass found for offloaded storage",
			"provisioner", storageClass.Provisioner,
			"storageID", storageID,
			"groupReplicationID", groupReplicationID)

		return nil, fmt.Errorf("no VolumeGroupReplicationClass found to match provisioner, storageID and groupReplicationID")
	case 1:
		return matches[0], nil
	}

	return v.filterDefaultVRC(matches, "VolumeGroupReplicationClass")
}

func (r *VolumeReplicationGroupReconciler) filterVRGForOffloadedVGR(
	vgr *volrep.VolumeGroupReplication, log logr.Logger,
) []reconcile.Request {
	req := []reconcile.Request{}

	// Get VRG owner information from VGR labels
	ownerNamespace, ownerName, ok := util.OwnerNamespaceNameAndName(vgr.GetLabels())
	if !ok {
		log.V(1).Info("VGR does not have owner labels, skipping")

		return req
	}

	// Fetch the VRG using the owner labels
	vrg := &ramendrv1alpha1.VolumeReplicationGroup{}
	vrgNamespacedName := types.NamespacedName{
		Namespace: ownerNamespace,
		Name:      ownerName,
	}

	if err := r.Client.Get(context.TODO(), vrgNamespacedName, vrg); err != nil {
		if k8serrors.IsNotFound(err) {
			log.V(1).Info("VRG not found for VGR", "vrg", vrgNamespacedName)
		} else {
			log.Error(err, "Failed to get VRG for VGR", "vrg", vrgNamespacedName)
		}

		return req
	}

	// Compare VGR's LastSyncTime with VRG's LastGroupSyncTime
	// If they differ, enqueue the VRG for reconciliation
	if !reflect.DeepEqual(vgr.Status.LastSyncTime, vrg.Status.LastGroupSyncTime) {
		req = append(req, reconcile.Request{
			NamespacedName: vrgNamespacedName,
		})

		log.Info("Enqueuing VRG for offloaded VGR sync time change",
			"vrg", vrgNamespacedName,
			"vgrLastSyncTime", util.TimeString(vgr.Status.LastSyncTime),
			"vrgLastGroupSyncTime", util.TimeString(vrg.Status.LastGroupSyncTime))
	}

	return req
}
