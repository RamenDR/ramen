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
	"reflect"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (v *VRGInstance) restorePVsForVolSync() error {
	v.log.Info("VolSync: Restoring VolSync PVs")

	if len(v.instance.Spec.VolSync.RDSpec) == 0 {
		v.log.Info("No RDSpec entries. There are no PVCs to restore")
		// No ReplicationDestinations (i.e. no PVCs) to restore
		return nil
	}

	numPVsRestored := 0
	for _, rdSpec := range v.instance.Spec.VolSync.RDSpec {
		err := v.volSyncHandler.EnsurePVCfromRD(rdSpec)
		if err != nil {
			v.log.Info(fmt.Sprintf("Unable to ensure PVC %v -- err: %v", rdSpec, err))
			protectedPVC := v.findProtectedPVC(rdSpec.ProtectedPVC.Name)
			if protectedPVC == nil {
				protectedPVC = &ramendrv1alpha1.ProtectedPVC{}
				rdSpec.ProtectedPVC.DeepCopyInto(protectedPVC)
				v.instance.Status.ProtectedPVCs = append(v.instance.Status.ProtectedPVCs, *protectedPVC)
			}

			setVRGConditionTypeVolSyncPVRestoreError(&protectedPVC.Conditions, v.instance.Generation,
				fmt.Sprintf("%v", err))

			continue // Keep trying to ensure PVCs for other rdSpec
		}

		numPVsRestored++

		protectedPVC := v.findProtectedPVC(rdSpec.ProtectedPVC.Name)
		if protectedPVC == nil {
			protectedPVC = &ramendrv1alpha1.ProtectedPVC{}
			rdSpec.ProtectedPVC.DeepCopyInto(protectedPVC)
			v.instance.Status.ProtectedPVCs = append(v.instance.Status.ProtectedPVCs, *protectedPVC)
		}

		setVRGConditionTypeVolSyncPVRestoreComplete(&protectedPVC.Conditions, v.instance.Generation, "PVC restored")
	}

	if numPVsRestored != len(v.instance.Spec.VolSync.RDSpec) {
		return fmt.Errorf("failed to restore all PVCs using RDSpec (%v)", v.instance.Spec.VolSync.RDSpec)
	}

	v.log.Info("Success restoring VolSync PVs", "Total", numPVsRestored)

	return nil
}

func (v *VRGInstance) reconcileVolSyncAsPrimary() (requeue bool) {
	v.log.Info("Reconciling VolSync as Primary", "volSync", v.instance.Spec.VolSync)

	requeue = false

	// Cleanup - this VRG is primary, cleanup if necessary
	// remove any ReplicationDestinations (that would have been created when this VRG was secondary) if they
	// are not in the RDSpec list
	if err := v.volSyncHandler.CleanupRDNotInSpecList(v.instance.Spec.VolSync.RDSpec); err != nil {
		v.log.Error(err, "Failed to cleanup the RDSpecs when this VRG instance was secondary")

		requeue = true
		return
	}

	// Extra check - do not continue on and protect PVCs until the spec has been cleared of RDs.  This is to prevent
	// a scenario where we might have a replicationdestination still running locally (with exported service) and
	// then create a replicationsource that then connects to it, instead of connecting to a replicationdestination
	// on the secondary cluster
	if len(v.instance.Spec.VolSync.RDSpec) != 0 {
		v.log.Info("Spec contains RDSpecs (on primary) - will not continue to reconcile ReplicationSources " +
			"until RDSpec is empty")
		requeue = true
		return
	}

	// First time: Add all VolSync PVCs to the protected PVC list and set their ready condition to initializing
	for _, pvc := range v.volSyncPVCs {
		newProtectedPVC := &ramendrv1alpha1.ProtectedPVC{
			Name:               pvc.Name,
			ProtectedByVolSync: true,
			StorageClassName:   pvc.Spec.StorageClassName,
			AccessModes:        pvc.Spec.AccessModes,
			Resources:          pvc.Spec.Resources,
		}

		protectedPVC := v.findProtectedPVC(pvc.Name)
		if protectedPVC == nil {
			protectedPVC = newProtectedPVC
			v.instance.Status.ProtectedPVCs = append(v.instance.Status.ProtectedPVCs, *protectedPVC)
		} else if !reflect.DeepEqual(protectedPVC, newProtectedPVC) {
			newProtectedPVC.DeepCopyInto(protectedPVC)
		}

		// Not much need for VolSyncReplicationSourceSpec anymore - but keeping it around in case we want
		// to add anything to it later to control anything in the ReplicationSource
		rsSpec := ramendrv1alpha1.VolSyncReplicationSourceSpec{
			ProtectedPVC: *protectedPVC,
		}

		_, rs, err := v.volSyncHandler.ReconcileRS(rsSpec, false /* Schedule sync normally */)
		if err != nil {
			v.log.Info(fmt.Sprintf("Failed to reconcile VolSync Replication Source for rsSpec %v. Error %v",
				rsSpec, err))

			setVRGConditionTypeVolSyncRepSourceSetupError(&protectedPVC.Conditions, v.instance.Generation,
				"VolSync setup failed")

			requeue = true
		} else if rs == nil {
			// Replication source is not ready yet //TODO: do we need a condition for this?
			requeue = true
		} else {
			setVRGConditionTypeVolSyncRepSourceSetupComplete(&protectedPVC.Conditions, v.instance.Generation, "Ready")
		}
	}

	if requeue {
		v.log.Info("Not all ReplicationSources completed setup. We'll retry...")
		return
	}

	v.log.Info("Successfully reconciled VolSync as Primary")

	return
}

func (v *VRGInstance) reconcileVolSyncAsSecondary() (requeue bool) {
	v.log.Info("Reconcile VolSync as Secondary", "RDSpec", v.instance.Spec.VolSync.RDSpec)

	requeue = false

	if v.instance.Spec.VolSync.RunFinalSync {
		for idx, protectedPVC := range v.instance.Status.ProtectedPVCs {
			if protectedPVC.ProtectedByVolSync {
				rsSpec := ramendrv1alpha1.VolSyncReplicationSourceSpec{
					ProtectedPVC: protectedPVC,
				}

				finalSyncComplete, _, err := v.volSyncHandler.ReconcileRS(rsSpec, true /* run final sync */)
				if err != nil {
					v.log.Info(fmt.Sprintf("Failed to run final sync for rsSpec %v. Error %v",
						rsSpec, err))

					requeue = false
					setVRGConditionTypeVolSyncFinalSyncError(&v.instance.Status.ProtectedPVCs[idx].Conditions,
						v.instance.Generation, "Final sync error")

					continue
				}

				if !finalSyncComplete {
					requeue = false
					setVRGConditionTypeVolSyncFinalSyncInProgress(&v.instance.Status.ProtectedPVCs[idx].Conditions,
						v.instance.Generation, "Final sync in-progress")

					continue
				}

				setVRGConditionTypeVolSyncFinalSyncComplete(&v.instance.Status.ProtectedPVCs[idx].Conditions,
					v.instance.Generation, "Final sync complete")
			}
		}

		if requeue {
			v.log.Info("Waiting for final sync of ReplicationSources to complete ...")
			return
		}

		v.log.Info("Final sync of ReplicationSources is complete")
	}

	// If we are secondary, and RDSpec is not set, then we don't want to have any PVC
	// flagged as a VolSync PVC.
	if v.instance.Spec.VolSync.RDSpec == nil {
		idx := 0
		for _, protectedPVC := range v.instance.Status.ProtectedPVCs {
			if !protectedPVC.ProtectedByVolSync {
				v.instance.Status.ProtectedPVCs[idx] = protectedPVC
				idx++
			}
		}

		v.instance.Status.ProtectedPVCs = v.instance.Status.ProtectedPVCs[:idx]
		v.log.Info("Protected PVCs left", "ProtectedPVCs", v.instance.Status.ProtectedPVCs)
	}

	// Reconcile RDSpec (deletion or replication)
	for _, rdSpec := range v.instance.Spec.VolSync.RDSpec {
		v.log.Info("Reconcile RD as Secondary", "RDSpec", rdSpec)
		rd, err := v.volSyncHandler.ReconcileRD(rdSpec)
		if err != nil {
			v.log.Error(err, "Failed to reconcile VolSync Replication Destination")

			requeue = true
			return
		}

		if rd == nil {
			// Replication destination is not ready yet, indicate we should requeue after the for loop is complete
			requeue = true
		}

		// Cleanup - this VRG is secondary, cleanup if necessary
		// remove ReplicationSources that would have been created when this VRG was primary
		if err := v.volSyncHandler.DeleteRS(rdSpec.ProtectedPVC.Name); err != nil {
			v.log.Error(err, "Failed to delete RS from when this VRG instance was primary")

			requeue = true
			return
		}

		// setVRGConditionTypeVolSyncRepDestSetupComplete(&protectedPVC.Conditions, v.instance.Generation, "Ready")
	}

	if requeue {
		v.log.Info("ReconcileRD - ReplicationDestinations are not all ready. We'll retry...")
		return
	}

	v.log.Info("Successfully reconciled VolSync as Secondary")

	return
}

func (v *VRGInstance) isFinalSyncInProgress() bool {
	status := false
	for _, protectedPVC := range v.instance.Status.ProtectedPVCs {
		if protectedPVC.ProtectedByVolSync {
			condition := findCondition(protectedPVC.Conditions, VRGConditionTypeVolSyncFinalSyncInProgress)
			if condition != nil && condition.Status == v1.ConditionTrue {
				status = true

				break
			}
		}
	}

	v.log.Info(fmt.Sprintf("Is final sync in-progress? %v", status))

	return status
}

func (v *VRGInstance) aggregateVolSyncDataReadyCondition() *v1.Condition {
	notReady := false
	dataReadyCondition := &v1.Condition{
		Type:               VRGConditionTypeDataReady,
		Reason:             "Inapplicable",
		ObservedGeneration: v.instance.Generation,
		Status:             v1.ConditionTrue,
		Message:            "Inapplicable",
	}

	if len(v.volSyncPVCs) == 0 {
		return dataReadyCondition
	}

	if v.instance.Spec.ReplicationState == ramendrv1alpha1.Primary {
		for _, protectedPVC := range v.instance.Status.ProtectedPVCs {
			if protectedPVC.ProtectedByVolSync {
				condition := findCondition(protectedPVC.Conditions, VRGConditionTypeVolSyncRepSourceSetup)
				if condition == nil || condition.Status != v1.ConditionTrue {
					notReady = true

					break
				}
			}
		}

		if notReady {
			dataReadyCondition.Reason = VRGConditionReasonReady
			dataReadyCondition.Status = v1.ConditionFalse
			dataReadyCondition.Message = "Not all VolSync PVCs are ready"

			return dataReadyCondition
		}

		dataReadyCondition.Reason = VRGConditionReasonReady
		dataReadyCondition.Status = v1.ConditionTrue
		dataReadyCondition.Message = "All VolSync PVCs are ready"

		return dataReadyCondition
	}

	// else
	// TODO: Determine If we have an RD status for each PVC.  For now assume DataReady for all
	dataReadyCondition.Reason = VRGConditionReasonReady
	dataReadyCondition.Status = v1.ConditionTrue
	dataReadyCondition.Message = "Assuming Data readiness"

	return dataReadyCondition
}

func (v *VRGInstance) aggregateVolSyncDataProtectedCondition() *v1.Condition {
	notReady := false
	dataReadyCondition := &v1.Condition{
		Type:               VRGConditionTypeDataProtected,
		Reason:             "Inapplicable",
		ObservedGeneration: v.instance.Generation,
		Status:             v1.ConditionTrue,
		Message:            "Inapplicable",
	}

	if len(v.volSyncPVCs) == 0 {
		return dataReadyCondition
	}

	if v.instance.Spec.ReplicationState == ramendrv1alpha1.Primary {
		for _, protectedPVC := range v.instance.Status.ProtectedPVCs {
			if protectedPVC.ProtectedByVolSync {
				condition := findCondition(protectedPVC.Conditions, VRGConditionTypeVolSyncFinalSyncInProgress)
				if condition != nil && condition.Status != v1.ConditionTrue {
					notReady = true

					break
				}
			}
			// TODO: Determine from RS if we have synced up at least once, otherwise, set notReady to true
		}

		if notReady {
			dataReadyCondition.Reason = VRGConditionReasonDataProtected
			dataReadyCondition.Status = v1.ConditionFalse
			dataReadyCondition.Message = "Not all VolSync PVCs are protected"

			return dataReadyCondition
		}

		dataReadyCondition.Reason = VRGConditionReasonDataProtected
		dataReadyCondition.Status = v1.ConditionTrue
		dataReadyCondition.Message = "All VolSync PVCs are protected"

		return dataReadyCondition
	}

	// else
	// TODO: Determine from RD status if we have the latestImage for each PVC. For now assuming DataProtected for all
	dataReadyCondition.Reason = VRGConditionReasonDataProtected
	dataReadyCondition.Status = v1.ConditionTrue
	dataReadyCondition.Message = "Assuming Data protection"

	return dataReadyCondition
}

func (v *VRGInstance) aggregateVolSyncClusterDataProtectedCondition() *v1.Condition {
	notReady := false
	dataReadyCondition := &v1.Condition{
		Type:               VRGConditionTypeClusterDataProtected,
		Reason:             "Inapplicable",
		ObservedGeneration: v.instance.Generation,
		Status:             v1.ConditionTrue,
		Message:            "Inapplicable",
	}

	if len(v.volSyncPVCs) == 0 {
		return dataReadyCondition
	}

	// TODO: For ClusterDataRady and ClusterDataProtected condition, should probably use the same
	// condition as DataProtected.  That is; if DataProtected is true, then, ClusterDataRady and ClusterDataProtected
	// should also be true.  For now, treat them separate with hardcodeded values. 
	if v.instance.Spec.ReplicationState == ramendrv1alpha1.Primary {
		for _, protectedPVC := range v.instance.Status.ProtectedPVCs {
			if protectedPVC.ProtectedByVolSync {
				condition := findCondition(protectedPVC.Conditions, VRGConditionTypeVolSyncRepSourceSetup)
				if condition == nil || condition.Status != v1.ConditionTrue {
					notReady = true
				}
			}
		}

		if notReady {
			dataReadyCondition.Reason = "NotSaved"
			dataReadyCondition.Status = v1.ConditionFalse
			dataReadyCondition.Message = "Not all VolSync PVCs have thier ClusterData protected"

			return dataReadyCondition
		}

		dataReadyCondition.Reason = "Saved"
		dataReadyCondition.Status = v1.ConditionTrue
		dataReadyCondition.Message = "All VolSync PVCs have thier ClusterData protected"

		return dataReadyCondition
	}

	// else
	dataReadyCondition.Reason = "Saved"
	dataReadyCondition.Status = v1.ConditionTrue
	dataReadyCondition.Message = "Assuming ClusterData protection"

	return dataReadyCondition
}
