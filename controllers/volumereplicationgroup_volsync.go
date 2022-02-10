/*
Copyright 2021 The RamenDR authors.

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

package controllers

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
)

func (v *VRGInstance) restorePVsForVolSync() error {
	v.log.Info("Restoring VolSync PVs")
	// TODO: refactor this per this comment: https://github.com/RamenDR/ramen/pull/197#discussion_r687246692
	clusterDataReady := findCondition(v.instance.Status.Conditions, VRGConditionTypeClusterDataReady)
	if clusterDataReady != nil && clusterDataReady.Status == metav1.ConditionTrue &&
		clusterDataReady.ObservedGeneration == v.instance.Generation {
		v.log.Info("ClusterDataReady condition found. PVC restore must have already been applied")

		return nil
	}

	if len(v.instance.Spec.VolSync.RDSpec) == 0 {
		v.log.Info("No RDSpec entries. There are no PVCs to restore")
		// No ReplicationDestinations (i.e. no PVCs) to restore
		return nil
	}

	msg := "Restoring PVC cluster data"
	setVRGClusterDataProgressingCondition(&v.instance.Status.Conditions, v.instance.Generation, msg)
	v.log.Info("Restoring PVCs to this managed cluster.", "RDSpec", v.instance.Spec.VolSync.RDSpec)

	success := true

	for _, rdSpec := range v.instance.Spec.VolSync.RDSpec {
		//TODO: Restore volume - if failure, set success=false
		err := v.volSyncHandler.EnsurePVCfromRD(rdSpec)
		if err != nil {
			v.log.Error(err, "Unable to ensure PVC", "rdSpec", rdSpec)
			success = false

			continue // Keep trying to ensure PVCs for other rdSpec
		}

		//TODO: Need any status to indicate which PVCs we've restored? - overall clusterDataReady is set below already
		setVRGClusterDataReadyCondition(&v.instance.Status.Conditions, v.instance.Generation, "PVC restored")
	}

	if !success {
		return fmt.Errorf("failed to restorePVCs using RDSpec (%v)", v.instance.Spec.VolSync.RDSpec)
	}

	msg = "PVC cluster data restored"
	setVRGClusterDataReadyCondition(&v.instance.Status.Conditions, v.instance.Generation, msg)
	v.log.Info(msg, "RDSpec", v.instance.Spec.VolSync.RDSpec)

	return nil
}

func (v *VRGInstance) reconcileVolSyncAsPrimary() bool {
	v.log.Info("Reconciling VolSync as Primary", "volSync", v.instance.Spec.VolSync)

	if v.instance.Spec.VolSync.RDSpec == nil {
		v.instance.Status.VolSyncRepStatus.RDInfo = []ramendrv1alpha1.VolSyncReplicationDestinationInfo{}

		// Reconcile RSSpec
		for _, rsSpec := range v.instance.Spec.VolSync.RSSpec {
			_, err := v.volSyncHandler.ReconcileRS(rsSpec, false /* Schedule sync normally */)
			if err != nil {
				v.log.Error(err, "Failed to reconcile VolSync Replication Source")

				return false
			}

			// We must have a protected PVC
			protectedPVC := v.findProtectedPVC(rsSpec.PVCName)
			if protectedPVC == nil {
				v.log.Error(fmt.Errorf("rsSpecs vs. PVCs mismatch"),
					fmt.Sprintf("Failed to find the protected PVC %s", rsSpec.PVCName))

				return false
			}

			setVolSyncProtectedPVCConditionReady(&protectedPVC.Conditions, v.instance.Generation, "Protecting")
		}

		//TODO: cleanup any RS that is not in rsSpec?

		// Cleanup - this VRG is primary, cleanup if necessary
		// remove ReplicationDestinations that would have been created when this VRG was
		// secondary if they are not in the RDSpec list
		if err := v.volSyncHandler.CleanupRDNotInSpecList(v.instance.Spec.VolSync.RDSpec); err != nil {
			v.log.Error(err, "Failed to cleanup the RDSpecs when this VRG instance was secondary")

			return false
		}
	}

	// First time: Add all VolSync PVCs to the protected PVC list and set their ready condition to initializing
	for _, pvc := range v.volSyncPVCs {
		protectedPVC := v.findProtectedPVC(pvc.Name)
		if protectedPVC == nil {
			protectedPVC = &ramendrv1alpha1.ProtectedPVC{
				Name:               pvc.Name,
				ProtectedByVolSync: true,
				StorageClassName:   pvc.Spec.StorageClassName,
				AccessModes:        pvc.Spec.AccessModes,
				Resources:          pvc.Spec.Resources,
			}

			setVolSyncProtectedPVCConditionInitializing(&protectedPVC.Conditions, v.instance.Generation,
				"Initializing VolSync Replication Source")

			v.instance.Status.ProtectedPVCs = append(v.instance.Status.ProtectedPVCs, *protectedPVC)
		}
	}

	v.log.Info("Successfully reconciled VolSync as Primary")

	return true
}

func (v *VRGInstance) reconcileVolSyncAsSecondary() bool {
	v.log.Info("Reconcile VolSync as Secondary", "RDSpec", v.instance.Spec.VolSync.RDSpec)
	// If RSSpec and RDSpec are not set, then we don't want to have any PVC
	// flagged as a VolSync PVC.
	if v.instance.Spec.VolSync.RSSpec == nil && v.instance.Spec.VolSync.RDSpec == nil {
		for idx := range v.instance.Status.ProtectedPVCs {
			v.instance.Status.ProtectedPVCs[idx].ProtectedByVolSync = false
		}
	}

	shouldWait := false

	// Reconcile RDSpec (deletion or replication)
	for _, rdSpec := range v.instance.Spec.VolSync.RDSpec {
		v.log.Info("Reconcile RD as Secondary", "RDSpec", rdSpec)
		rdInfoForStatus, err := v.volSyncHandler.ReconcileRD(rdSpec)
		if err != nil {
			v.log.Error(err, "Failed to reconcile VolSync Replication Destination")

			return false
		}

		if rdInfoForStatus != nil {
			v.log.Info("rdInfoForStatus as Secondary", "RDSpec", rdInfoForStatus)
			// Update the VSRG status with this rdInfo
			v.updateStatusWithRDInfo(*rdInfoForStatus)
		} else {
			shouldWait = true
		}
	}

	if shouldWait {
		v.log.Info("ReconcileRD didn't succeed. We'll retry...")

		return false
	}

	//TODO: cleanup any RD that is not in rdSpec? may not be necessary?

	// Cleanup - this VRG is secondary, cleanup if necessary
	// remove ReplicationSources that would have been created when this VRG was
	// primary if they are not in the RSSpec list
	if err := v.volSyncHandler.CleanupRSNotInSpecList(v.instance.Spec.VolSync.RSSpec); err != nil {
		v.log.Error(err, "Failed to cleanup the SDSpecs when this VRG instance was primary")

		return false
	}

	// This may be a relocate scenario - in which case we want to run a final sync
	// of the PVCs we've been syncing (via ReplicationDestinations) when we were primary
	// Trigger final sync on any ReplicationDestination in the RSSpec list
	for _, rsSpec := range v.instance.Spec.VolSync.RSSpec {
		finalSyncComplete, err := v.volSyncHandler.ReconcileRS(rsSpec, true /* Run final sync */)
		if err != nil {
			v.log.Error(err, "Failed to reconcile VolSync Replication Destination")

			return false
		}
		if finalSyncComplete {
			//TODO: will need to indicate status back to DRPC controller
		}
	}

	v.log.Info("Successfully reconciled VolSync as Secondary")

	return true
}

func (v *VRGInstance) updateStatusWithRDInfo(rdInfoForStatus ramendrv1alpha1.VolSyncReplicationDestinationInfo) {
	if v.instance.Status.VolSyncRepStatus.RDInfo == nil {
		v.instance.Status.VolSyncRepStatus.RDInfo = []ramendrv1alpha1.VolSyncReplicationDestinationInfo{}
	}

	found := false

	for i := range v.instance.Status.VolSyncRepStatus.RDInfo {
		if v.instance.Status.VolSyncRepStatus.RDInfo[i].PVCName == rdInfoForStatus.PVCName {
			// blindly replace with our updated RDInfo status
			v.instance.Status.VolSyncRepStatus.RDInfo[i] = rdInfoForStatus
			found = true

			break
		}
	}
	if !found {
		// Append the new RDInfo to the status
		v.instance.Status.VolSyncRepStatus.RDInfo = append(v.instance.Status.VolSyncRepStatus.RDInfo, rdInfoForStatus)
	}
}
