// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/go-logr/logr"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/cephfscg"
	"github.com/ramendr/ramen/internal/controller/util"
	"github.com/ramendr/ramen/internal/controller/volsync"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//nolint:gocognit,funlen,cyclop
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

		var err error
		// Source conditions are not needed and should not be added to vrg.status.ProtectedPVCs,
		// as this would result in incorrect information.
		rdSpec.ProtectedPVC.Conditions = nil

		cg, ok := rdSpec.ProtectedPVC.Labels[ConsistencyGroupLabel]
		if ok && util.IsCGEnabled(v.instance.Annotations) {
			v.log.Info("rdSpec has CG label", "Labels", rdSpec.ProtectedPVC.Labels)
			cephfsCGHandler := cephfscg.NewVSCGHandler(
				v.ctx, v.reconciler.Client, v.instance,
				&metav1.LabelSelector{MatchLabels: map[string]string{ConsistencyGroupLabel: cg}},
				v.volSyncHandler, cg, v.log,
			)
			err = cephfsCGHandler.EnsurePVCfromRGD(rdSpec, failoverAction)
		} else {
			// Create a PVC from snapshot or for direct copy
			err = v.volSyncHandler.EnsurePVCfromRD(rdSpec, failoverAction)
		}

		if err != nil {
			v.log.Info(fmt.Sprintf("Unable to ensure PVC %v -- err: %v", rdSpec, err))

			protectedPVC := v.findProtectedPVC(rdSpec.ProtectedPVC.Namespace, rdSpec.ProtectedPVC.Name)
			if protectedPVC == nil {
				protectedPVC = v.addProtectedPVC(rdSpec.ProtectedPVC.Namespace, rdSpec.ProtectedPVC.Name)
				rdSpec.ProtectedPVC.DeepCopyInto(protectedPVC)
			}

			setVRGConditionTypeVolSyncPVRestoreError(&protectedPVC.Conditions, v.instance.Generation,
				fmt.Sprintf("%v", err))

			continue // Keep trying to ensure PVCs for other rdSpec
		}

		numPVsRestored++

		protectedPVC := v.findProtectedPVC(rdSpec.ProtectedPVC.Namespace, rdSpec.ProtectedPVC.Name)
		if protectedPVC == nil {
			protectedPVC = v.addProtectedPVC(rdSpec.ProtectedPVC.Namespace, rdSpec.ProtectedPVC.Name)
			rdSpec.ProtectedPVC.DeepCopyInto(protectedPVC)
		}

		setVRGConditionTypeVolSyncPVRestoreComplete(&protectedPVC.Conditions, v.instance.Generation, "PVC restored")
	}

	if numPVsRestored != len(v.instance.Spec.VolSync.RDSpec) {
		return numPVsRestored, fmt.Errorf("failed to restore all PVCs. Restored %d PVCs out of %d RDSpecs",
			numPVsRestored, len(v.instance.Spec.VolSync.RDSpec))
	}

	v.log.Info("Success restoring VolSync PVs", "Total", numPVsRestored)

	return numPVsRestored, nil
}

func (v *VRGInstance) reconcileVolSyncAsPrimary(finalSyncPrepared *bool) (requeue bool) {
	finalSyncComplete := func() {
		*finalSyncPrepared = true
		v.instance.Status.FinalSyncComplete = v.instance.Spec.RunFinalSync
	}

	if len(v.volSyncPVCs) == 0 {
		finalSyncComplete()

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

	finalSyncComplete()
	v.log.Info("Successfully reconciled VolSync as Primary")

	return requeue
}

//nolint:gocognit,funlen,cyclop,gocyclo,nestif
func (v *VRGInstance) reconcilePVCAsVolSyncPrimary(pvc corev1.PersistentVolumeClaim) (requeue bool) {
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

	protectedPVC := v.findProtectedPVC(pvc.Namespace, pvc.Name)
	if protectedPVC == nil {
		v.instance.Status.ProtectedPVCs = append(v.instance.Status.ProtectedPVCs, *newProtectedPVC)
		protectedPVC = &v.instance.Status.ProtectedPVCs[len(v.instance.Status.ProtectedPVCs)-1]
	} else if !reflect.DeepEqual(protectedPVC, newProtectedPVC) {
		newProtectedPVC.Conditions = protectedPVC.Conditions
		newProtectedPVC.DeepCopyInto(protectedPVC)
	}

	// Not much need for VolSyncReplicationSourceSpec anymore - but keeping it around in case we want
	// to add anything to it later to control anything in the ReplicationSource
	rsSpec := ramendrv1alpha1.VolSyncReplicationSourceSpec{
		ProtectedPVC: *protectedPVC,
	}

	err := v.volSyncHandler.PreparePVC(util.ProtectedPVCNamespacedName(*protectedPVC),
		v.instance.Spec.PrepareForFinalSync,
		v.volSyncHandler.IsCopyMethodDirect())
	if err != nil {
		return true
	}

	cg, ok := pvc.Labels[ConsistencyGroupLabel]
	if ok && util.IsCGEnabled(v.instance.Annotations) {
		v.log.Info("PVC has CG label", "Labels", pvc.Labels)
		cephfsCGHandler := cephfscg.NewVSCGHandler(
			v.ctx, v.reconciler.Client, v.instance,
			&metav1.LabelSelector{MatchLabels: map[string]string{ConsistencyGroupLabel: cg}},
			v.volSyncHandler, cg, v.log,
		)

		rgs, finalSyncComplete, err := cephfsCGHandler.CreateOrUpdateReplicationGroupSource(
			v.instance.Name, v.instance.Namespace, v.instance.Spec.RunFinalSync,
		)
		if err != nil {
			setVRGConditionTypeVolSyncRepSourceSetupError(&protectedPVC.Conditions, v.instance.Generation,
				"VolSync setup failed")

			return true
		}

		setVRGConditionTypeVolSyncRepSourceSetupComplete(&protectedPVC.Conditions, v.instance.Generation, "Ready")

		protectedPVC.LastSyncTime = rgs.Status.LastSyncTime
		protectedPVC.LastSyncDuration = rgs.Status.LastSyncDuration

		return v.instance.Spec.RunFinalSync && !finalSyncComplete
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
		protectedPVC.LastSyncDuration = rs.Status.LastSyncDuration
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

	return v.reconcileRDSpecForDeletionOrReplication()
}

//nolint:gocognit,funlen,cyclop,nestif
func (v *VRGInstance) reconcileRDSpecForDeletionOrReplication() bool {
	requeue := false
	rdinCGs := []ramendrv1alpha1.VolSyncReplicationDestinationSpec{}

	for _, rdSpec := range v.instance.Spec.VolSync.RDSpec {
		cg, ok := rdSpec.ProtectedPVC.Labels[ConsistencyGroupLabel]
		if ok && util.IsCGEnabled(v.instance.Annotations) {
			v.log.Info("rdSpec has CG label", "Labels", rdSpec.ProtectedPVC.Labels)
			cephfsCGHandler := cephfscg.NewVSCGHandler(
				v.ctx, v.reconciler.Client, v.instance,
				&metav1.LabelSelector{MatchLabels: map[string]string{ConsistencyGroupLabel: cg}},
				v.volSyncHandler, cg, v.log,
			)

			rdinCG, err := cephfsCGHandler.GetRDInCG()
			if err != nil {
				v.log.Error(err, "Failed to get RD in CG")

				requeue = true

				return requeue
			}

			if len(rdinCG) > 0 {
				v.log.Info("Create ReplicationGroupDestination with RDSpecs", "RDSpecs", rdinCG)

				replicationGroupDestination, err := cephfsCGHandler.CreateOrUpdateReplicationGroupDestination(
					v.instance.Name, v.instance.Namespace, rdinCG,
				)
				if err != nil {
					v.log.Error(err, "Failed to create ReplicationGroupDestination")

					requeue = true

					return requeue
				}

				ready, err := util.IsReplicationGroupDestinationReady(v.ctx, v.reconciler.Client, replicationGroupDestination)
				if err != nil {
					v.log.Error(err, "Failed to check if ReplicationGroupDestination if ready")

					requeue = true

					return requeue
				}

				if !ready {
					v.log.Info(fmt.Sprintf("ReplicationGroupDestination for %s is not ready. We'll retry...",
						replicationGroupDestination.Name))

					requeue = true
				}

				rdinCGs = append(rdinCGs, rdinCG...)
			}
		}
	}

	for _, rdSpec := range v.instance.Spec.VolSync.RDSpec {
		v.log.Info("Reconcile RD as Secondary", "RDSpec", rdSpec)

		if util.IsRDExist(rdSpec, rdinCGs) {
			v.log.Info("Skip Reconcile RD as Secondary as it's in a consistency group",
				"RDSpec", rdSpec, "RDInCGs", rdinCGs)

			continue
		}

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
		dataReadyCondition.Reason = VRGConditionReasonUnused
		dataReadyCondition.Message = "Volsync based PVC protection does not report DataReady condition as Secondary"

		return dataReadyCondition
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
		return newVRGAsDataProtectedUnusedCondition(v.instance.Generation,
			"No PVCs are protected using Volsync scheme")
	}

	if v.instance.Spec.ReplicationState == ramendrv1alpha1.Secondary {
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

			// IFF however, we are running the final sync, then we have to wait
			condition = findCondition(protectedPVC.Conditions, VRGConditionTypeVolSyncFinalSyncInProgress)
			if condition != nil && condition.Status != metav1.ConditionTrue {
				ready = false

				v.log.Info(fmt.Sprintf("VolSync RS is in progress for PVC %s", protectedPVC.Name))

				break
			}

			// Check now if we have synced up at least once for this PVC
			rsDataProtected, err := v.volSyncHandler.IsRSDataProtected(protectedPVC.Name, protectedPVC.Namespace)
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

	dataProtectedCondition := &metav1.Condition{
		Type:               VRGConditionTypeDataProtected,
		ObservedGeneration: v.instance.Generation,
	}

	if !ready {
		dataProtectedCondition.Status = metav1.ConditionFalse
		dataProtectedCondition.Reason = VRGConditionReasonProgressing
		dataProtectedCondition.Message = "Not all VolSync PVCs are protected"
	} else {
		dataProtectedCondition.Status = metav1.ConditionTrue
		dataProtectedCondition.Reason = VRGConditionReasonDataProtected
		dataProtectedCondition.Message = "All VolSync PVCs are protected"
	}

	return dataProtectedCondition
}

func (v VRGInstance) isVolSyncProtectedPVCConditionReady(conType string) bool {
	ready := len(v.instance.Status.ProtectedPVCs) != 0

	for _, protectedPVC := range v.instance.Status.ProtectedPVCs {
		if protectedPVC.ProtectedByVolSync {
			condition := findCondition(protectedPVC.Conditions, conType)
			if condition == nil || condition.Status != metav1.ConditionTrue {
				ready = false

				v.log.Info(fmt.Sprintf("VolSync %s is not complete yet for PVC %s", conType, protectedPVC.Name))

				break
			}
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
	if v.instance.GetAnnotations()[DoNotDeletePVCAnnotation] != DoNotDeletePVCAnnotationVal {
		return nil
	}

	for idx := range v.volSyncPVCs {
		pvc := &v.volSyncPVCs[idx]

		err := v.volSyncHandler.DisownVolSyncManagedPVC(pvc)
		if err != nil {
			return err
		}
	}

	return nil
}

// cleanupResources this function deleted all PS, PD and VolumeSnapshots from its owner (VRG)
func (v *VRGInstance) cleanupResources() error {
	for idx := range v.volSyncPVCs {
		pvc := &v.volSyncPVCs[idx]

		if err := v.volSyncHandler.DeleteRS(pvc.Name, pvc.Namespace); err != nil {
			return err
		}

		if err := v.volSyncHandler.DeleteRD(pvc.Name, pvc.Namespace); err != nil {
			return err
		}

		if err := v.volSyncHandler.DeleteSnapshots(pvc.Namespace); err != nil {
			return err
		}
	}

	return nil
}
