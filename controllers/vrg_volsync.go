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
	v.log.Info(fmt.Sprintf("Reconciling VolSync as Primary. VolSyncPVCs %d. VolSyncSpec %+v",
		len(v.volSyncPVCs), v.instance.Spec.VolSync))

	requeue = false

	// Cleanup - this VRG is primary, cleanup if necessary
	// remove any ReplicationDestinations (that would have been created when this VRG was secondary) if they
	// are not in the RDSpec list
	if err := v.volSyncHandler.CleanupRDNotInSpecList(v.instance.Spec.VolSync.RDSpec); err != nil {
		v.log.Error(err, "Failed to cleanup the RDSpecs when this VRG instance was secondary")

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

		// reconcile RS and if run final sync if it is true
		finalSyncComplete, rs, err := v.volSyncHandler.ReconcileRS(rsSpec, v.instance.Spec.RunFinalSync)
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

		if v.instance.Spec.RunFinalSync && !finalSyncComplete {
			requeue = true
		}
	}

	if requeue {
		v.log.Info("Not all ReplicationSources completed setup. We'll retry...")

		return
	}

	if v.instance.Spec.RunFinalSync {
		v.instance.Status.FinalSyncComplete = true
	}

	v.log.Info("Successfully reconciled VolSync as Primary")

	return requeue
}

func (v *VRGInstance) reconcileVolSyncAsSecondary() (requeue bool) {
	v.log.Info("Reconcile VolSync as Secondary", "RDSpec", v.instance.Spec.VolSync.RDSpec)

	requeue = false

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

	// Reset status finalsync flag
	v.instance.Status.FinalSyncComplete = false

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
	}

	if requeue {
		v.log.Info("ReconcileRD - ReplicationDestinations are not all ready. We'll retry...")

		return
	}

	v.log.Info("Successfully reconciled VolSync as Secondary")

	return requeue
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
	if len(v.volSyncPVCs) == 0 {
		return nil
	}

	dataReadyCondition := &v1.Condition{
		Type:               VRGConditionTypeDataReady,
		ObservedGeneration: v.instance.Generation,
		Status:             v1.ConditionTrue,
	}

	if v.instance.Spec.ReplicationState == ramendrv1alpha1.Primary {
		ready := v.isVolSyncReplicationSourceSetupComplete()

		dataReadyCondition.Reason = VRGConditionReasonReady

		if !ready {
			dataReadyCondition.Status = v1.ConditionFalse
			dataReadyCondition.Message = "Not all VolSync PVCs are ready"

			return dataReadyCondition
		}

		dataReadyCondition.Status = v1.ConditionTrue
		dataReadyCondition.Message = "All VolSync PVCs are ready"

		return dataReadyCondition
	}

	// Not primary -- DataReady NOT applicable
	return dataReadyCondition
}

func (v *VRGInstance) aggregateVolSyncDataProtectedCondition() *v1.Condition {
	if len(v.volSyncPVCs) == 0 {
		return nil
	}

	return v.buildDataProtectedCondition()
}

func (v *VRGInstance) aggregateVolSyncClusterDataProtectedCondition() *v1.Condition {
	if len(v.volSyncPVCs) == 0 {
		return nil
	}

	// For ClusterDataReady and ClusterDataProtected condition, will use the same condition as DataProtected.
	// That is; if DataProtected is true, then, ClusterDataRady and ClusterDataProtected should also be true.
	condition := v.buildDataProtectedCondition()
	condition.Type = VRGConditionTypeClusterDataProtected

	return condition
}

func (v *VRGInstance) buildDataProtectedCondition() *v1.Condition {
	ready := true
	
	protectedByVolSyncCount := 0

	if v.instance.Spec.ReplicationState == ramendrv1alpha1.Primary {
		for _, protectedPVC := range v.instance.Status.ProtectedPVCs {
			if protectedPVC.ProtectedByVolSync {
				protectedByVolSyncCount++
				
				condition := findCondition(protectedPVC.Conditions, VRGConditionTypeVolSyncRepSourceSetup)
				if condition == nil || condition.Status != v1.ConditionTrue {
					ready = false
					
					v.log.Info(fmt.Sprintf("VolSync RS hasn't been setup yet for PVC %s", protectedPVC.Name))

					break
				}

				// IFF however, we are running the final sync, then we have to wait
				condition = findCondition(protectedPVC.Conditions, VRGConditionTypeVolSyncFinalSyncInProgress)
				if condition != nil && condition.Status != v1.ConditionTrue {
					ready = false
					
					v.log.Info(fmt.Sprintf("VolSync RS is in progress for PVC %s", protectedPVC.Name))

					break
				}

				// Check now if we have synced up at least once for this PVC
				lastSyncTime, err := v.volSyncHandler.GetRSLastSyncTime(protectedPVC.Name)
				if err != nil {
					ready = false
					
					v.log.Info(fmt.Sprintf("Failed to get lastSyncTime from RS for PVC %s", protectedPVC.Name))

					break
				}

				if lastSyncTime == nil || lastSyncTime.IsZero() {
					ready = false
					
					v.log.Info(fmt.Sprintf("VolSync RS has not synced up yet for PVC %s", protectedPVC.Name))

					break
				}
			}

			if ready && len(v.volSyncPVCs) != protectedByVolSyncCount {
				ready = false
				
				v.log.Info(fmt.Sprintf("VolSync PVCs count does not match with the ready PVCs %d/%d",
					len(v.volSyncPVCs), protectedByVolSyncCount))
			}
		}
	} else {
		for _, rdSpec := range v.instance.Spec.VolSync.RDSpec {
			v.log.Info("Reconcile RD as Secondary", "RDSpec", rdSpec)
			latestImage, err := v.volSyncHandler.GetRDLatestImage(rdSpec.ProtectedPVC.Name)
			if err != nil || latestImage == nil {
				ready = false
				v.log.Info(fmt.Sprintf("Failed to retrieve latestImage for RD %s", rdSpec.ProtectedPVC.Name))

				break
			}
		}
	}

	dataReadyCondition := &v1.Condition{
		Type:               VRGConditionTypeDataProtected,
		Reason:             VRGConditionReasonDataProtected,
		ObservedGeneration: v.instance.Generation,
	}

	if !ready {
		dataReadyCondition.Status = v1.ConditionFalse
		dataReadyCondition.Message = "Not all VolSync PVCs are protected"
	} else {
		dataReadyCondition.Status = v1.ConditionTrue
		dataReadyCondition.Message = "All VolSync PVCs are protected"
	}

	return dataReadyCondition
}

func (v VRGInstance) isVolSyncReplicationSourceSetupComplete() bool {
	ready := len(v.instance.Status.ProtectedPVCs) != 0
	
	for _, protectedPVC := range v.instance.Status.ProtectedPVCs {
		if protectedPVC.ProtectedByVolSync {
			condition := findCondition(protectedPVC.Conditions, VRGConditionTypeVolSyncRepSourceSetup)
			if condition == nil || condition.Status != v1.ConditionTrue {
				ready = false
				
				v.log.Info(fmt.Sprintf("VolSync RS hasn't been setup yet for PVC %s", protectedPVC.Name))
				v.log.Info(fmt.Sprintf("VolSync RS hasn't been setup yet for PVC %+v", protectedPVC))

				break
			}
		}
	}

	return ready
}
