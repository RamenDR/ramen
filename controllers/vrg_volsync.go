// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/go-logr/logr"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/util"
	"github.com/ramendr/ramen/controllers/volsync"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	FinalSyncPVCNameSuffix = "-for-final-sync"
)

func (v *VRGInstance) restorePVsAndPVCsForVolSync() (int, error) {
	v.log.Info("VolSync: Restoring VolSync PVs")

	if len(v.instance.Spec.VolSync.RDSpec) == 0 {
		v.log.Info("No RDSpec entries. There are no PVCs to restore")
		// No ReplicationDestinations (i.e. no PVCs) to restore
		return 0, nil
	}

	numPVsRestored := 0

	for _, rdSpec := range v.instance.Spec.VolSync.RDSpec {
		failoverAction := v.instance.Spec.Action == ramendrv1alpha1.VRGActionFailover
		// Create a PVC from snapshot or for direct copy
		err := v.volSyncHandler.EnsurePVCfromRD(rdSpec, failoverAction)
		if err != nil {
			v.log.Info(fmt.Sprintf("Unable to ensure PVC %v -- err: %v", rdSpec, err))

			protectedPVC := v.findFirstProtectedPVCWithName(rdSpec.ProtectedPVC.Name)
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

		protectedPVC := v.findFirstProtectedPVCWithName(rdSpec.ProtectedPVC.Name)
		if protectedPVC == nil {
			protectedPVC = &ramendrv1alpha1.ProtectedPVC{}
			rdSpec.ProtectedPVC.DeepCopyInto(protectedPVC)
			v.instance.Status.ProtectedPVCs = append(v.instance.Status.ProtectedPVCs, *protectedPVC)
		}

		setVRGConditionTypeVolSyncPVRestoreComplete(&protectedPVC.Conditions, v.instance.Generation, "PVC restored")
	}

	if numPVsRestored != len(v.instance.Spec.VolSync.RDSpec) {
		return numPVsRestored, fmt.Errorf("failed to restore all PVCs using RDSpec (%v)", v.instance.Spec.VolSync.RDSpec)
	}

	v.log.Info("Success restoring VolSync PVs", "Total", numPVsRestored)

	return numPVsRestored, nil
}

func (v *VRGInstance) reconcileVolSyncAsPrimary() (requeue bool) {
	if len(v.volSyncPVCs) == 0 {
		return
	}

	v.log.Info(fmt.Sprintf("Reconciling VolSync as Primary. %d VolSyncPVCs", len(v.volSyncPVCs)))

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

	v.log.Info("Successfully reconciled VolSync as Primary")

	return requeue
}

// reconcilePVCAsVolSyncPrimary reconciles a PVC as the primary source for VolSync.
// It protects the PVC from deletion and retains the PV.
// It reconciles the ReplicationSource.
func (v *VRGInstance) reconcilePVCAsVolSyncPrimary(pvc corev1.PersistentVolumeClaim) bool {
	newProtectedPVC := &ramendrv1alpha1.ProtectedPVC{
		Name:               pvc.Name,
		Namespace:          pvc.Namespace,
		ProtectedByVolSync: true,
		StorageClassName:   pvc.Spec.StorageClassName,
		Annotations:        protectedPVCAnnotations(pvc),
		Labels:             pvc.Labels,
		AccessModes:        pvc.Spec.AccessModes,
		Resources:          pvc.Spec.Resources,
	}

	protectedPVC := v.findFirstProtectedPVCWithName(pvc.Name)
	if protectedPVC == nil {
		protectedPVC = newProtectedPVC
		v.instance.Status.ProtectedPVCs = append(v.instance.Status.ProtectedPVCs, *protectedPVC)
	} else if !reflect.DeepEqual(protectedPVC, newProtectedPVC) {
		newProtectedPVC.Conditions = protectedPVC.Conditions
		newProtectedPVC.DeepCopyInto(protectedPVC)
	}

	rsSpec := ramendrv1alpha1.VolSyncReplicationSourceSpec{
		ProtectedPVC: *protectedPVC,
	}

	const requeue = true

	err := v.protectPVCAndRetainPV(&pvc)
	if err != nil {
		return requeue
	}

	// reconcile RS and if runFinalSync is true, then one final sync will be run
	_, rs, err := v.volSyncHandler.ReconcileRS(rsSpec, false)
	if err != nil {
		v.log.Info(fmt.Sprintf("Failed to reconcile VolSync Replication Source for rsSpec %v. Error %v",
			rsSpec, err))

		setVRGConditionTypeVolSyncRepSourceSetupError(&protectedPVC.Conditions, v.instance.Generation,
			"VolSync setup failed")

		return requeue
	}

	if rs == nil {
		return requeue
	}

	setVRGConditionTypeVolSyncRepSourceSetupComplete(&protectedPVC.Conditions, v.instance.Generation, "Ready")

	return !requeue
}

func (v *VRGInstance) protectPVCAndRetainPV(pvc *corev1.PersistentVolumeClaim) error {
	// Add VolSync finalizer to PVC for deletion protection
	err := util.NewResourceUpdater(pvc).
		AddFinalizer(volsync.PvcVSFinalizerProtected).
		Update(v.ctx, v.reconciler.Client)
	if err != nil {
		return err
	}

	// Update PV reclaim policy but do not clean the claimRef
	return util.UpdatePVReclaimPolicy(
		v.ctx,
		v.reconciler.Client,
		util.PVAnnotationRetainedForVolSync,
		corev1.PersistentVolumeReclaimRetain,
		pvc,
		false,
		v.log)
}

func (v *VRGInstance) reconcileVolSyncAsSecondary() bool {
	v.log.Info("Reconcile VolSync as Secondary", "RDSpec", v.instance.Spec.VolSync.RDSpec)

	// Ensure final sync when switching to secondary due to a relocate request and RDSpec has not been set yet.
	if v.instance.Spec.VolSync.RDSpec == nil {
		if v.instance.Spec.Action == ramendrv1alpha1.VRGActionRelocate {
			if requeue := v.prepareAndReconcileFinalSync(); requeue {
				return requeue
			}
		}

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
		Status:             metav1.ConditionTrue,
		Type:               VRGConditionTypeDataReady,
		Reason:             VRGConditionReasonReady,
		ObservedGeneration: v.instance.Generation,
		Message:            "All VolSync PVCs are ready",
	}

	if v.instance.Spec.ReplicationState == ramendrv1alpha1.Secondary {
		if v.instance.Spec.Action != ramendrv1alpha1.VRGActionRelocate ||
			v.instance.Status.State == ramendrv1alpha1.SecondaryState {
			dataReadyCondition.Reason = VRGConditionReasonUnused
			dataReadyCondition.Message = "Volsync based PVC protection does not report DataReady condition as Secondary"

			return dataReadyCondition
		}
	}

	if len(v.volSyncPVCs) == 0 {
		dataReadyCondition.Reason = VRGConditionReasonUnused
		dataReadyCondition.Message = "No PVCs are protected using Volsync scheme"

		return dataReadyCondition
	}

	// On Failover/Relocation, we depend on PVs to be restored. For initial deployment,
	// we depend on ReplicationSourceSetup to determine Data readiness.
	ready := v.isVolSyncProtectedPVCConditionReady(VRGConditionTypeVolSyncPVsRestored) ||
		v.isVolSyncProtectedPVCConditionReady(VRGConditionTypeVolSyncRepSourceSetup)

	if !ready {
		dataReadyCondition.Status = metav1.ConditionFalse
		dataReadyCondition.Message = "Not all VolSync PVCs are ready"
		dataReadyCondition.Reason = VRGConditionReasonProgressing

		return dataReadyCondition
	}

	return dataReadyCondition
}

func (v *VRGInstance) aggregateVolSyncDataProtectedConditions() (*metav1.Condition, *metav1.Condition) {
	// For VolSync, clusterDataProtectedCondition is used, however, we use it here
	// to check whether we replicated at least once
	return v.buildProtectionCondition(VRGConditionTypeDataProtected),
		v.buildProtectionCondition(VRGConditionTypeClusterDataProtected)
}

//nolint:gocognit,funlen,cyclop,gocyclo
func (v *VRGInstance) buildProtectionCondition(conditionType string) *metav1.Condition {
	if len(v.volSyncPVCs) == 0 && len(v.instance.Spec.VolSync.RDSpec) == 0 {
		return newVRGAsDataProtectedUnusedCondition(v.instance.Generation,
			"No PVCs are protected using Volsync scheme")
	}

	if v.instance.Spec.ReplicationState == ramendrv1alpha1.Secondary &&
		v.instance.Status.State == ramendrv1alpha1.SecondaryState {
		// The primary will contain the DataProtected condition.
		return newVRGAsDataProtectedUnusedCondition(v.instance.Generation,
			"Volsync based PVC protection does not report DataProtected/ClusterDataProtected conditions as Secondary")
	}

	ready := true

	protectedByVolSyncCount := 0

	//nolint:nestif
	for _, protectedPVC := range v.instance.Status.ProtectedPVCs {
		if protectedPVC.ProtectedByVolSync {
			protectedByVolSyncCount++

			condition := findCondition(protectedPVC.Conditions, VRGConditionTypeVolSyncRepSourceSetup)
			if condition == nil || condition.Status != metav1.ConditionTrue {
				ready = false

				v.log.Info(fmt.Sprintf("VolSync RS hasn't been setup yet for PVC %s", protectedPVC.Name))

				break
			}

			if conditionType == VRGConditionTypeClusterDataProtected {
				// Check if we have synced up at least once for this PVC
				rsDataProtected, err := v.volSyncHandler.IsRSDataProtected(protectedPVC.Name)
				if err != nil || !rsDataProtected {
					ready = false

					v.log.Info(fmt.Sprintf("First sync has not yet completed for VolSync RS %s -- Err %v",
						protectedPVC.Name, err))

					break
				}
			} else if conditionType == VRGConditionTypeDataProtected {
				condition = findCondition(protectedPVC.Conditions, VRGConditionTypeVolSyncFinalSync)
				if condition != nil {
					if condition.Status != metav1.ConditionTrue {
						ready = false

						v.log.Info(fmt.Sprintf("VolSync RS is in progress for PVC %s", protectedPVC.Name))

						break
					}
				} else {
					ready = false
				}
			}
		}
	}

	if ready && len(v.volSyncPVCs) > protectedByVolSyncCount {
		ready = false

		v.log.Info(fmt.Sprintf("VolSync PVCs count does not match with the ready PVCs %d/%d",
			len(v.volSyncPVCs), protectedByVolSyncCount))
	}

	condition := &metav1.Condition{
		Type:               conditionType,
		ObservedGeneration: v.instance.Generation,
	}

	if !ready {
		condition.Status = metav1.ConditionFalse
		condition.Reason = VRGConditionReasonProgressing
		condition.Message = "Not all VolSync PVCs are protected"
	} else {
		condition.Status = metav1.ConditionTrue
		condition.Reason = VRGConditionReasonDataProtected
		condition.Message = "All VolSync PVCs are protected"
	}

	return condition
}

func (v VRGInstance) isVolSyncProtectedPVCConditionReady(conType string) bool {
	ready := len(v.instance.Status.ProtectedPVCs) != 0

	for _, protectedPVC := range v.instance.Status.ProtectedPVCs {
		if protectedPVC.ProtectedByVolSync {
			condition := findCondition(protectedPVC.Conditions, conType)
			if condition == nil || condition.Status != metav1.ConditionTrue {
				ready = false

				v.log.Info(fmt.Sprintf("VolSync: %s is not complete yet for PVC %s", conType, protectedPVC.Name))

				break
			}

			v.log.Info(fmt.Sprintf("VolSync: %s is complete for PVC %s", conType, protectedPVC.Name))
		}
	}

	return ready
}

// protectedPVCAnnotations return the annotations that we must propagate to the
// destination cluster:
//   - apps.open-cluster-management.io/* - required to make the protected PVC
//     owned by OCM when DR is disabled. Copy all annnotations except the
//     special "do-not-delete" annotation, used only on the source cluster
//     during relocate.
func protectedPVCAnnotations(pvc corev1.PersistentVolumeClaim) map[string]string {
	res := map[string]string{}

	for key, value := range pvc.Annotations {
		if strings.HasPrefix(key, "apps.open-cluster-management.io/") &&
			key != volsync.ACMAppSubDoNotDeleteAnnotation {
			res[key] = value
		}
	}

	return res
}

func (v *VRGInstance) pvcUnprotectVolSync(pvc corev1.PersistentVolumeClaim, log logr.Logger) {
	if !VolumeUnprotectionEnabledForAsyncVolSync {
		log.Info("Volume unprotection disabled for VolSync")

		return
	}
	// TODO Delete ReplicationSource, ReplicationDestination, etc.
	v.pvcStatusDeleteIfPresent(pvc.Namespace, pvc.Name, log)
}

// disownPVCs this function is disassociating all PVCs (targeted for VolSync replication) from its owner (VRG)
func (v *VRGInstance) disownPVCs() error {
	if v.instance.Spec.ReplicationState == ramendrv1alpha1.Secondary &&
		v.instance.Status.State == ramendrv1alpha1.SecondaryState {
		v.log.Info("Keeping the PVCs as is")

		return nil
	}

	for idx := range v.volSyncPVCs {
		pvc := &v.volSyncPVCs[idx]

		err := v.volSyncHandler.DisownVolSyncManagedPVC(pvc)
		if err != nil {
			return err
		}

		// Update PV reclaim policy but do not clean the claimRef
		err = util.UpdatePVReclaimPolicy(
			v.ctx,
			v.reconciler.Client,
			util.PVAnnotationRetainedForVolSync,
			corev1.PersistentVolumeReclaimDelete,
			pvc,
			false,
			v.log)

		if err != nil {
			return err
		}

		err = util.NewResourceUpdater(pvc).
			RemoveFinalizer(volsync.PvcVSFinalizerProtected).
			Update(v.ctx, v.reconciler.Client)

		if err != nil {
			return err
		}
	}

	return nil
}

// prepareAndReconcileFinalSync prepares and reconciles the final sync as a secondary source.
func (v *VRGInstance) prepareAndReconcileFinalSync() bool {
	v.log.Info("Reconcile VolSync as Secondary for final sync", "RDSpec", v.instance.Spec.VolSync.RDSpec)

	requeue := false

	for idx := range v.volSyncPVCs {
		pvc := &v.volSyncPVCs[idx]
		pvcName := pvc.GetName()

		finalSyncPVC, created, err := v.prepareFinalSync(pvc, v.log)
		if err != nil {
			v.log.Info("Failed to prepare for final sync.", "Error", err)

			requeue = true

			continue
		}

		if created {
			v.log.Info("Final Sync PVC was just created. Skipping RS reconciliation for it.", "pvc", pvcName)

			requeue = true

			continue
		}

		// Run finalsync
		err = v.reconcileFinalSync(finalSyncPVC)
		if err != nil {
			v.log.Info("Reconciled final sync", "Error", err)

			requeue = true

			continue
		}

		// Prepared and ran final sync successfully. Remove APP PVC finalizer.
		err = v.cleanupAfterFinalSync(finalSyncPVC)
		if err != nil {
			v.log.Info("Final sync cleanup", "Error", err)

			requeue = true

			continue
		}
	}

	return requeue
}

// prepareFinalSync will do the following:
// 1. Retain the PV claimed by the PVC
// 2. Updates the ClaimRef to point to a new temporary PVC
// 3. Create the temporary PVC
func (v *VRGInstance) prepareFinalSync(pvc *corev1.PersistentVolumeClaim,
	log logr.Logger,
) (*corev1.PersistentVolumeClaim, bool, error) {
	const created = true

	v.log.Info("Prepare final sync")

	if strings.HasSuffix(pvc.GetName(), FinalSyncPVCNameSuffix) {
		return pvc, !created, nil
	}

	finalSyncPVCName := pvc.GetName() + FinalSyncPVCNameSuffix

	if err := v.updatePVForFinalSync(pvc, finalSyncPVCName, log); err != nil {
		return nil, !created, err
	}

	return v.createAndPreparePVCForFinalSync(pvc, finalSyncPVCName, log)
}

// updatePVForFinalSync will retain the PV and changes the claimRef to point to a new PVC
func (v *VRGInstance) updatePVForFinalSync(pvc *corev1.PersistentVolumeClaim, finalSyncPVCName string, log logr.Logger,
) error {
	return v.setPVReclaimPolicy(pvc, finalSyncPVCName, corev1.PersistentVolumeReclaimRetain, log)
}

// cleanupAfterFinalSync performs cleanup after the final sync is complete.
// It removes the App PVC finalizer but retains the PV to avoid syncing the entire PV to the secondary.
func (v *VRGInstance) cleanupAfterFinalSync(finalSyncPVC *corev1.PersistentVolumeClaim) error {
	v.log.Info("Reset after final sync is complete")
	// Prepared and ran final sync successfully.
	// Remove APP PVC finalizer, but ensure the PV stays retained in order to avoid syncing the entire pv to the secondary
	claimName := finalSyncPVC.GetName()
	if appPVCName, ok := finalSyncPVC.GetAnnotations()[util.AppPVCNameAnnotation]; ok && appPVCName != "" {
		claimName = appPVCName
	}

	err := v.setPVReclaimPolicy(finalSyncPVC, claimName, corev1.PersistentVolumeReclaimDelete, v.log)
	if err != nil {
		return err
	}

	err = util.NewResourceUpdater(finalSyncPVC).
		RemoveFinalizer(volsync.PvcVSFinalizerProtected).
		Update(v.ctx, v.reconciler.Client)
	if err != nil {
		return err // requeue
	}

	return v.reconciler.Client.Delete(v.ctx, finalSyncPVC)
}

// setPVReclaimPolicy sets the reclaim policy and the claimRef for the PV associated with
// the passed in PVC. If the PV object is updated, the fuction also updates the apiserver.
func (v *VRGInstance) setPVReclaimPolicy(pvc *corev1.PersistentVolumeClaim, claimName string,
	reclaimPolicy corev1.PersistentVolumeReclaimPolicy, log logr.Logger,
) error {
	pv, err := v.getPVFromPVC(pvc)
	if err != nil {
		log.Error(err, "Failed to get PV")

		return err
	}

	v.log.Info("Updating PV", "Name", pv.GetName(), "ReclaimPolicy", reclaimPolicy, "ClaimRef", claimName)

	updated := false
	if pv.Spec.PersistentVolumeReclaimPolicy != reclaimPolicy {
		// Change reclaim policy if it has not been set earlier (on becoming primary)
		updated = ChangeReclaimPolicy(pv, reclaimPolicy, util.PVAnnotationRetainedForVolSync)
	}

	if pv.Spec.ClaimRef.Name != claimName || pv.Spec.ClaimRef.Namespace != pvc.GetNamespace() {
		// Update the claimRef to the new tmp PVC on the same namespace as the original PVC.
		updated = ChangeClaimRef(pv, claimName, pvc.GetNamespace())
	}

	if updated {
		if err := v.reconciler.Client.Update(v.ctx, pv); err != nil {
			log.Error(err, "Failed to update PersistentVolume")

			return fmt.Errorf("failed to update PersistentVolume resource (%s) reclaim policy for"+
				" PersistentVolumeClaim resource (%s/%s), %w",
				pvc.Spec.VolumeName, pvc.Namespace, pvc.Name, err)
		}
	}

	return nil
}

// createAndPreparePVCForFinalSync creates and prepares the App PVC for final sync. It clones the provided
// application PVC to create a temporary finalSyncPVC.
//
// The finalSyncPVC retains a reference to the application PVC through the AppPVCNameAnnotation annotation.
// After creating the finalSyncPVC, it deletes the RS associated with the application PVC, allowing a new RS
// to be created using the finalSyncPVC.
//
// Returns the finalSyncPVC, a boolean indicating whether it was created, and an error if encountered during
// the process.
func (v *VRGInstance) createAndPreparePVCForFinalSync(pvc *corev1.PersistentVolumeClaim, finalSyncPVCName string,
	log logr.Logger,
) (*corev1.PersistentVolumeClaim, bool, error) {
	const created = true

	finalSyncPVC := pvc.DeepCopy()
	finalSyncPVC.ObjectMeta.Name = finalSyncPVCName
	finalSyncPVC.ObjectMeta.Annotations = map[string]string{util.AppPVCNameAnnotation: pvc.Name}
	finalSyncPVC.ObjectMeta.Finalizers = []string{}
	finalSyncPVC.ObjectMeta.Labels = pvc.Labels
	finalSyncPVC.ObjectMeta.ResourceVersion = ""
	finalSyncPVC.ObjectMeta.OwnerReferences = nil

	op, err := ctrlutil.CreateOrUpdate(v.ctx, v.reconciler.Client, finalSyncPVC, func() error {
		if err := ctrl.SetControllerReference(v.instance, finalSyncPVC, v.reconciler.Client.Scheme()); err != nil {
			return fmt.Errorf("failed to set controller reference %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, !created, err
	}

	log.V(1).Info("Final sync PVC", "operation", op)

	log.V(1).Info("Cleaning app PVC and its corresponding RS", "app PVC", pvc.GetName())

	if err := v.volSyncHandler.DeleteRS(pvc.GetName()); err != nil {
		return nil, false, err
	}

	if err := util.NewResourceUpdater(pvc).
		RemoveFinalizer(volsync.PvcVSFinalizerProtected).
		Update(v.ctx, v.reconciler.Client); err != nil {
		return nil, false, err // requeue
	}

	return finalSyncPVC, created, nil
}

func (v *VRGInstance) reconcileFinalSync(finalSyncPVC *corev1.PersistentVolumeClaim) error {
	v.log.Info("Reconcile final sync")

	appPVCName := finalSyncPVC.GetAnnotations()[util.AppPVCNameAnnotation]
	newProtectedPVC := &ramendrv1alpha1.ProtectedPVC{
		Name:               appPVCName,
		Namespace:          finalSyncPVC.GetNamespace(),
		ProtectedByVolSync: true,
		StorageClassName:   finalSyncPVC.Spec.StorageClassName,
		Annotations:        map[string]string{util.SourcePVCNameAnnotation: finalSyncPVC.GetName()},
		Labels:             finalSyncPVC.Labels,
		AccessModes:        finalSyncPVC.Spec.AccessModes,
		Resources:          finalSyncPVC.Spec.Resources,
	}

	protectedPVC := v.findFirstProtectedPVCWithName(appPVCName)
	if protectedPVC == nil {
		protectedPVC = newProtectedPVC
		v.instance.Status.ProtectedPVCs = append(v.instance.Status.ProtectedPVCs, *protectedPVC)
	} else if !reflect.DeepEqual(protectedPVC, newProtectedPVC) {
		newProtectedPVC.Conditions = protectedPVC.Conditions
		newProtectedPVC.DeepCopyInto(protectedPVC)
	}

	rsSpec := ramendrv1alpha1.VolSyncReplicationSourceSpec{
		ProtectedPVC: *protectedPVC,
	}

	runFinalSync := true
	// reconcile RS for FinalSync
	finalSyncComplete, _, err := v.volSyncHandler.ReconcileRS(rsSpec, runFinalSync)
	if err != nil {
		v.log.Info(fmt.Sprintf("Failed to reconcile VolSync Replication Source for rsSpec %v. Error %v",
			rsSpec, err))

		return err
	}

	if !finalSyncComplete {
		setVRGConditionTypeVolSyncFinalSyncInProgress(&protectedPVC.Conditions, v.instance.Generation,
			"Final sync in progress")

		return fmt.Errorf("waiting for finalSync to complete")
	}

	v.log.Info("Final sysnc complete")
	setVRGConditionTypeVolSyncFinalSyncComplete(&protectedPVC.Conditions, v.instance.Generation, "Final sync complete")

	return nil
}

// ChangeReclaimPolicy modifies the reclaim policy of a PV object and adds annotations to denote
// the reclaimer (VolRep/VolSync) but it does not update the apiserver.
func ChangeReclaimPolicy(pv *corev1.PersistentVolume,
	newReclaimPolicy corev1.PersistentVolumeReclaimPolicy,
	reclaimer string,
) bool {
	const updated = true
	// Check reclaimPolicy of PV, if already set to the new reclaim policy
	if pv.Spec.PersistentVolumeReclaimPolicy == newReclaimPolicy {
		return !updated
	}

	pv.Spec.PersistentVolumeReclaimPolicy = newReclaimPolicy
	if pv.ObjectMeta.Annotations == nil {
		pv.ObjectMeta.Annotations = map[string]string{}
	}

	// Save the reclaimer (VolRep/VolSync)
	pv.ObjectMeta.Annotations[util.PVAnnotationRetainKey] = reclaimer

	return updated
}

// ChangeClaimRef updates the claim reference of a PV to a new PVC. It also prepares the PV
// for reclamation by cleaning up its resource version and claim reference if present.
func ChangeClaimRef(pv *corev1.PersistentVolume, pvcName, pvcNamespace string) bool {
	// first clean the PV from the old reclaimer
	preparePVForReclaim(pv)
	// switch to the new claimRef
	pv.Spec.ClaimRef.Name = pvcName
	pv.Spec.ClaimRef.Namespace = pvcNamespace

	return true
}
