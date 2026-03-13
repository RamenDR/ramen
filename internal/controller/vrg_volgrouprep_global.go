// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"fmt"

	volrep "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	rmnutil "github.com/ramendr/ramen/internal/controller/util"
)

// GlobalVGRLabel is the label key used on VRGs and DRPCs to associate them
// with a shared global VolumeGroupReplication resource.
const GlobalVGRLabel = "ramendr.openshift.io/global-vgr"

func (v *VRGInstance) globalVGRLabel() string {
	return v.instance.GetLabels()[GlobalVGRLabel]
}

func (v *VRGInstance) hasGlobalVGRLabel() bool {
	return v.globalVGRLabel() != ""
}

// usesGlobalVGRClass returns true if the offloaded PVCs use a globally scoped VGRClass.
func (v *VRGInstance) usesGlobalVGRClass(pvcList *corev1.PersistentVolumeClaimList) (bool, error) {
	if len(pvcList.Items) == 0 {
		return false, nil
	}

	pvc := &pvcList.Items[0]

	vgrClassObj, err := v.selectVolumeReplicationClass(pvc, true)
	if err != nil {
		return false, fmt.Errorf("failed to find VolumeGroupReplicationClass for PVC %s/%s: %w",
			pvc.GetNamespace(), pvc.GetName(), err)
	}

	vgrClass, ok := vgrClassObj.(*volrep.VolumeGroupReplicationClass)
	if !ok {
		return false, fmt.Errorf("unexpected type for VolumeGroupReplicationClass: %T", vgrClassObj)
	}

	// TODO: Replace with vgrClass.Spec.Global once csi-addons adds the field
	return vgrClass.Spec.Parameters["global"] == "true", nil
}

// addGlobalVGRLabel derives the global VGR name from the PeerClass GroupReplicationID
// and applies it as a label to the VRG, linking it to a shared global VGR resource.
func (v *VRGInstance) addGlobalVGRLabel(pvcList *corev1.PersistentVolumeClaimList) error {
	pvc := &pvcList.Items[0]

	storageClass, err := v.validateAndGetStorageClass(pvc.Spec.StorageClassName, pvc)
	if err != nil {
		return err
	}

	for idx := range v.instance.Spec.Async.PeerClasses {
		pc := &v.instance.Spec.Async.PeerClasses[idx]

		if pc.Offloaded && pc.StorageClassName == storageClass.GetName() && pc.GroupReplicationID != "" {
			// Global VGR name is derived from GroupReplicationID and used as the label value
			vgrLabel := rmnutil.CreateGlobalVGRName(pc.GroupReplicationID)

			if rmnutil.AddLabel(v.instance, GlobalVGRLabel, vgrLabel) {
				if err := v.reconciler.Update(v.ctx, v.instance); err != nil {
					return fmt.Errorf("failed to add label %s to VRG %s/%s: %w",
						GlobalVGRLabel, v.instance.Namespace, v.instance.Name, err)
				}

				v.log.Info("Added global VGR label to VRG", "globalVGR", vgrLabel)
			}

			return nil
		}
	}

	return fmt.Errorf("no offloaded PeerClass with GroupReplicationID for StorageClass %s",
		storageClass.GetName())
}

// isGlobalVGRVRGsInConsensus checks that all VRGs sharing the same global VGR label
// have their spec.replicationState set to the desired state. This prevents the global
// VGR from being updated until all participating VRGs agree.
func (v *VRGInstance) isGlobalVGRVRGsInConsensus(state ramendrv1alpha1.ReplicationState) bool {
	vgrLabel := v.globalVGRLabel()
	log := v.log.WithName("GlobalVGRConsensus").WithValues("globalVGR", vgrLabel, "desiredState", state)

	var vrgs ramendrv1alpha1.VolumeReplicationGroupList

	if err := v.reconciler.List(v.ctx, &vrgs,
		client.MatchingLabels{GlobalVGRLabel: vgrLabel},
	); err != nil {
		log.Error(err, "Failed to list VRGs for consensus check")

		return false
	}

	for idx := range vrgs.Items {
		vrg := &vrgs.Items[idx]
		if vrg.Name == v.instance.Name && vrg.Namespace == v.instance.Namespace {
			continue
		}

		if vrg.Spec.ReplicationState != state {
			log.Info("Consensus not reached, VRG state differs",
				"vrg", vrg.Name, "namespace", vrg.Namespace,
				"currentState", vrg.Spec.ReplicationState)

			return false
		}
	}

	return true
}

// isGlobalVGRDeletionInConsensus checks that all VRGs sharing the same global VGR label
// have a deletion timestamp. The global VGR is only deleted when all VRGs are being removed.
func (v *VRGInstance) isGlobalVGRDeletionInConsensus() bool {
	vgrLabel := v.globalVGRLabel()
	log := v.log.WithName("GlobalVGRDeletionConsensus").WithValues("globalVGR", vgrLabel)

	var vrgs ramendrv1alpha1.VolumeReplicationGroupList

	if err := v.reconciler.List(v.ctx, &vrgs,
		client.MatchingLabels{GlobalVGRLabel: vgrLabel},
	); err != nil {
		log.Error(err, "Failed to list VRGs for deletion consensus")

		return false
	}

	for idx := range vrgs.Items {
		vrg := &vrgs.Items[idx]
		if !rmnutil.ResourceIsDeleted(vrg) {
			log.Info("Consensus not reached, VRG not yet being deleted",
				"vrg", vrg.Name, "namespace", vrg.Namespace)

			return false
		}
	}

	return true
}

func (v *VRGInstance) isGlobalVGRStateMatched(
	status *volrep.VolumeReplicationStatus, desiredState ramendrv1alpha1.ReplicationState,
) bool {
	switch desiredState {
	case ramendrv1alpha1.Primary:
		return status.State == volrep.PrimaryState
	case ramendrv1alpha1.Secondary:
		return status.State == volrep.SecondaryState
	default:
		return false
	}
}
