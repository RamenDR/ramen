// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"fmt"
	"reflect"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	for _, pvc := range v.volSyncPVCs {
		requeuePVC := v.reconcilePVCAsVolSyncPrimary(pvc)
		if requeuePVC {
			requeue = true
		}
	}

	if requeue {
		v.log.Info("Not all ReplicationSources completed setup. We'll retry...")

		return requeue
	}

	if v.instance.Spec.PrepareForFinalSync {
		v.instance.Status.PrepareForFinalSyncComplete = true
	}

	if v.instance.Spec.RunFinalSync {
		v.instance.Status.FinalSyncComplete = true
	}

	v.log.Info("Successfully reconciled VolSync as Primary")

	return requeue
}

func (v *VRGInstance) reconcilePVCAsVolSyncPrimary(pvc corev1.PersistentVolumeClaim) (requeue bool) {
	newProtectedPVC := &ramendrv1alpha1.ProtectedPVC{
		Name:               pvc.Name,
		ProtectedByVolSync: true,
		StorageClassName:   pvc.Spec.StorageClassName,
		Labels:             pvc.Labels,
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

	if v.instance.Spec.PrepareForFinalSync {
		prepared, err := v.volSyncHandler.PreparePVCForFinalSync(pvc.Name)
		if err != nil || !prepared {
			return true
		}
	}

	// reconcile RS and if runFinalSync is true, then one final sync will be run
	finalSyncComplete, rs, err := v.volSyncHandler.ReconcileRS(rsSpec, v.instance.Spec.RunFinalSync)
	if err != nil {
		v.log.Info(fmt.Sprintf("Failed to reconcile VolSync Replication Source for rsSpec %v. Error %v",
			rsSpec, err))

		setVRGConditionTypeVolSyncRepSourceSetupError(&protectedPVC.Conditions, v.instance.Generation,
			"VolSync setup failed")

		return true
	}

	if rs == nil {
		return true
	}

	setVRGConditionTypeVolSyncRepSourceSetupComplete(&protectedPVC.Conditions, v.instance.Generation, "Ready")

	if rs.Status != nil {
		protectedPVC.LastSyncTime = rs.Status.LastSyncTime
	}

	return v.instance.Spec.RunFinalSync && !finalSyncComplete
}

func (v *VRGInstance) reconcileVolSyncAsSecondary() bool {
	v.log.Info("Reconcile VolSync as Secondary", "RDSpec", v.instance.Spec.VolSync.RDSpec)

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

	// Reset status finalsync flags and condition
	v.instance.Status.PrepareForFinalSyncComplete = false
	v.instance.Status.FinalSyncComplete = false

	// We no longer need to have conditions for the secondary VRG
	if len(v.instance.Spec.VolSync.RDSpec) > 0 && len(v.instance.Status.Conditions) > 0 {
		v.instance.Status.Conditions = []metav1.Condition{}
	}

	return v.reconcileRDSpecForDeletionOrReplication()
}

func (v *VRGInstance) reconcileRDSpecForDeletionOrReplication() bool {
	requeue := false

	for _, rdSpec := range v.instance.Spec.VolSync.RDSpec {
		v.log.Info("Reconcile RD as Secondary", "RDSpec", rdSpec)

		rd, err := v.volSyncHandler.ReconcileRD(rdSpec)
		if err != nil {
			v.log.Error(err, "Failed to reconcile VolSync Replication Destination")

			requeue = true

			break
		}

		if rd == nil {
			v.log.Info(fmt.Sprintf("ReconcileRD - ReplicationDestination for %s is not ready. We'll retry...",
				rdSpec.ProtectedPVC.Name))

			requeue = true
		}
	}

	if !requeue {
		v.log.Info("Successfully reconciled VolSync as Secondary")
	}

	return requeue
}

func (v *VRGInstance) aggregateVolSyncDataReadyCondition() *metav1.Condition {
	dataReadyCondition := &metav1.Condition{
		Type:               VRGConditionTypeDataReady,
		Reason:             VRGConditionReasonReady,
		ObservedGeneration: v.instance.Generation,
	}

	if v.instance.Spec.ReplicationState == ramendrv1alpha1.Primary {
		if len(v.volSyncPVCs) == 0 {
			return nil
		}

		ready := v.isVolSyncReplicationSourceSetupComplete()

		if !ready {
			dataReadyCondition.Status = metav1.ConditionFalse
			dataReadyCondition.Message = "Not all VolSync PVCs are ready"

			return dataReadyCondition
		}

		dataReadyCondition.Status = metav1.ConditionTrue
		dataReadyCondition.Message = "All VolSync PVCs are ready"

		return dataReadyCondition
	}

	// Not primary -- DataReady NOT applicable. Return default
	return nil
}

func (v *VRGInstance) aggregateVolSyncDataProtectedConditions() (*metav1.Condition, *metav1.Condition) {
	// For VolSync, clusterDataProtectedCondition is the same as dataProtectedCondition - so copy it
	dataProtectedCondition := v.buildDataProtectedCondition()

	if dataProtectedCondition == nil {
		return nil, nil
	}

	clusterDataProtectedCondition := dataProtectedCondition.DeepCopy()
	clusterDataProtectedCondition.Type = VRGConditionTypeClusterDataProtected

	return dataProtectedCondition, clusterDataProtectedCondition
}

//nolint:gocognit,funlen,cyclop
func (v *VRGInstance) buildDataProtectedCondition() *metav1.Condition {
	if len(v.volSyncPVCs) == 0 && len(v.instance.Spec.VolSync.RDSpec) == 0 {
		v.log.Info("No VolSync PVCs")

		return nil
	}

	ready := true

	protectedByVolSyncCount := 0

	//nolint:nestif
	if v.instance.Spec.ReplicationState == ramendrv1alpha1.Primary {
		for _, protectedPVC := range v.instance.Status.ProtectedPVCs {
			if protectedPVC.ProtectedByVolSync {
				protectedByVolSyncCount++

				condition := findCondition(protectedPVC.Conditions, VRGConditionTypeVolSyncRepSourceSetup)
				if condition == nil || condition.Status != metav1.ConditionTrue {
					ready = false

					v.log.Info(fmt.Sprintf("VolSync RS hasn't been setup yet for PVC %s", protectedPVC.Name))

					break
				}

				// IFF however, we are running the final sync, then we have to wait
				condition = findCondition(protectedPVC.Conditions, VRGConditionTypeVolSyncFinalSyncInProgress)
				if condition != nil && condition.Status != metav1.ConditionTrue {
					ready = false

					v.log.Info(fmt.Sprintf("VolSync RS is in progress for PVC %s", protectedPVC.Name))

					break
				}

				// Check now if we have synced up at least once for this PVC
				rsDataProtected, err := v.volSyncHandler.IsRSDataProtected(protectedPVC.Name)
				if err != nil || !rsDataProtected {
					ready = false

					v.log.Info(fmt.Sprintf("First sync has not yet completed for VolSync RS %s -- Err %v",
						protectedPVC.Name, err))

					break
				}
			}
		}

		if ready && len(v.volSyncPVCs) > protectedByVolSyncCount {
			ready = false

			v.log.Info(fmt.Sprintf("VolSync PVCs count does not match with the ready PVCs %d/%d",
				len(v.volSyncPVCs), protectedByVolSyncCount))
		}
	} else {
		// When secondary, no need to call volSyncHandler.IsRDDataProtected to determine
		// whether the data has been synced or not. We can get that information from
		// VolSync::ReplicationDestination resource. The primary will contain the
		// DataProtected condition.
		return nil
	}

	dataProtectedCondition := &metav1.Condition{
		Type:               VRGConditionTypeDataProtected,
		Reason:             VRGConditionReasonDataProtected,
		ObservedGeneration: v.instance.Generation,
	}

	if !ready {
		dataProtectedCondition.Status = metav1.ConditionFalse
		dataProtectedCondition.Message = "Not all VolSync PVCs are protected"
	} else {
		dataProtectedCondition.Status = metav1.ConditionTrue
		dataProtectedCondition.Message = "All VolSync PVCs are protected"
	}

	return dataProtectedCondition
}

func (v VRGInstance) isVolSyncReplicationSourceSetupComplete() bool {
	ready := len(v.instance.Status.ProtectedPVCs) != 0

	for _, protectedPVC := range v.instance.Status.ProtectedPVCs {
		if protectedPVC.ProtectedByVolSync {
			condition := findCondition(protectedPVC.Conditions, VRGConditionTypeVolSyncRepSourceSetup)
			if condition == nil || condition.Status != metav1.ConditionTrue {
				ready = false

				v.log.Info(fmt.Sprintf("VolSync RS hasn't been setup yet for PVC %s", protectedPVC.Name))

				break
			}
		}
	}

	return ready
}
