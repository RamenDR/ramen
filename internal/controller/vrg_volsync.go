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
	"k8s.io/apimachinery/pkg/types"
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

		cgLabelVal, ok := rdSpec.ProtectedPVC.Labels[ConsistencyGroupLabel]
		if ok && util.IsCGEnabledForVolSync(v.ctx, v.reconciler.APIReader, v.instance.Annotations) {
			v.log.Info("The CG label from the primary cluster found in RDSpec", "Label", cgLabelVal)
			// Get the CG label value for this cluster
			cgLabelVal, err = v.getCGLabelValue(rdSpec.ProtectedPVC.StorageClassName,
				rdSpec.ProtectedPVC.Name, rdSpec.ProtectedPVC.Namespace)
			if err == nil {
				cephfsCGHandler := cephfscg.NewVSCGHandler(
					v.ctx, v.reconciler.Client, v.instance,
					&metav1.LabelSelector{MatchLabels: map[string]string{ConsistencyGroupLabel: cgLabelVal}},
					v.volSyncHandler, cgLabelVal, v.log,
				)
				err = cephfsCGHandler.EnsurePVCfromRGD(rdSpec, failoverAction)
			}
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
	err := v.volSyncHandler.CleanupRDNotInSpecList(v.instance.Spec.VolSync.RDSpec, v.instance.Spec.ReplicationState)
	if err != nil {
		v.log.Error(err, "Failed to cleanup the RDSpecs when this VRG instance was secondary")

		requeue = true

		return
	}

	*finalSyncPrepared = true

	for _, pvc := range v.volSyncPVCs {
		var finalSyncForPVCPrepared bool

		// TODO: Add deleted PVC handling here?
		requeuePVC := v.reconcilePVCAsVolSyncPrimary(pvc, &finalSyncForPVCPrepared)
		if requeuePVC {
			requeue = true
		}

		if *finalSyncPrepared {
			*finalSyncPrepared = finalSyncForPVCPrepared
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
func (v *VRGInstance) reconcilePVCAsVolSyncPrimary(pvc corev1.PersistentVolumeClaim, finalSyncPrepared *bool,
) (requeue bool) {
	newProtectedPVC := &ramendrv1alpha1.ProtectedPVC{
		Name:               pvc.Name,
		Namespace:          pvc.Namespace,
		ProtectedByVolSync: true,
		StorageClassName:   pvc.Spec.StorageClassName,
		Annotations:        protectedPVCAnnotations(pvc),
		Labels:             pvc.Labels,
		AccessModes:        pvc.Spec.AccessModes,
		Resources:          pvc.Spec.Resources,
		VolumeMode:         pvc.Spec.VolumeMode,
	}

	if v.pvcUnprotectVolSyncIfDeleted(pvc, v.log) {
		return false
	}

	err := util.NewResourceUpdater(&pvc).
		AddFinalizer(volsync.PVCFinalizerProtected).
		AddLabel(util.LabelOwnerNamespaceName, v.instance.Namespace).
		AddLabel(util.LabelOwnerName, v.instance.Name).
		Update(v.ctx, v.reconciler.Client)
	if err != nil {
		v.log.Info(fmt.Sprintf("Unable to add finalizer for PVC. We'll retry later. %v", err))

		return true // requeue
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

	err = v.volSyncHandler.PreparePVC(util.ProtectedPVCNamespacedName(*protectedPVC),
		v.volSyncHandler.IsCopyMethodDirect(),
		v.instance.Spec.PrepareForFinalSync,
		v.instance.Spec.RunFinalSync,
	)
	if err != nil {
		v.log.Info(fmt.Sprintf("Unable to Prepare PVC. We'll retry later. %v", err))

		return true
	}

	*finalSyncPrepared = true

	cg, ok := pvc.Labels[ConsistencyGroupLabel]
	if ok && util.IsCGEnabledForVolSync(v.ctx, v.reconciler.APIReader, v.instance.Annotations) {
		v.log.Info("PVC has CG label", "Labels", pvc.Labels)
		cephfsCGHandler := cephfscg.NewVSCGHandler(
			v.ctx, v.reconciler.Client, v.instance,
			&metav1.LabelSelector{MatchLabels: map[string]string{ConsistencyGroupLabel: cg}},
			v.volSyncHandler, cg, v.log,
		)

		rgs, finalSyncComplete, err := cephfsCGHandler.CreateOrUpdateReplicationGroupSource(
			v.instance.Name, pvc.Namespace, v.instance.Spec.RunFinalSync,
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
		// This might be a case where we lose the RDSpec temporarily,
		// so we don't know if workload status is truly inactive.
		idx := 0

		for _, protectedPVC := range v.instance.Status.ProtectedPVCs {
			if !protectedPVC.ProtectedByVolSync {
				v.instance.Status.ProtectedPVCs[idx] = protectedPVC
				idx++
			}
		}

		v.instance.Status.ProtectedPVCs = v.instance.Status.ProtectedPVCs[:idx]
		v.log.Info("Protected PVCs left", "ProtectedPVCs", v.instance.Status.ProtectedPVCs)

		if requeue := v.updateWorkloadActivityAsSecondary(); requeue {
			v.log.Info("Workload is still active, requeueing for VolSync reconciliation as Secondary")

			return true // requeue
		}
	}

	// Reset status finalsync flags and condition
	v.instance.Status.PrepareForFinalSyncComplete = false
	v.instance.Status.FinalSyncComplete = false

	return v.reconcileRDSpecForDeletionOrReplication()
}

// updateWorkloadActivityAsSecondary updates workload status of volsync PVCs if still in use by the workload. This is
// useful to set DataReady on VRG as false if VRG is being reconciled as Secondary.
func (v *VRGInstance) updateWorkloadActivityAsSecondary() bool {
	for idx := range v.volSyncPVCs {
		pvcNSName := types.NamespacedName{
			Namespace: v.volSyncPVCs[idx].GetNamespace(),
			Name:      v.volSyncPVCs[idx].GetName(),
		}

		inUse, err := v.volSyncHandler.IsPVCInUseByNonRDPod(pvcNSName)
		if err != nil {
			// As errors are ignored and do not update DataReady using the same, even if a false positive set workload
			// as active unconditionally
			v.volSyncHandler.SetWorkloadStatus("active")

			v.log.Info("Failed to determine if pvc is in use", "error", err, "pvc", pvcNSName)

			return true // requeue
		}

		if inUse {
			v.volSyncHandler.SetWorkloadStatus("active")

			v.log.Info("One or more pvcs are in use as secondary, waiting for workload to be inactive",
				"pvc", pvcNSName)

			return true // requeue
		}
	}

	return false // no requeue
}

func (v *VRGInstance) reconcileRDSpecForDeletionOrReplication() bool {
	err := v.volSyncHandler.CleanupRDNotInSpecList(v.instance.Spec.VolSync.RDSpec, v.instance.Spec.ReplicationState)
	if err != nil {
		v.log.Error(err, "Failed to cleanup the RDSpecs when this VRG instance was secondary")

		return true // requeue
	}

	rdSpecsUsingCG, requeue, err := v.reconcileCGMembership()
	if err != nil {
		v.log.Error(err, "Failed to reconcile CG for deletion or replication")

		return requeue
	}

	for _, rdSpec := range v.instance.Spec.VolSync.RDSpec {
		v.log.Info("Reconcile RD as Secondary", "RDSpec", rdSpec.ProtectedPVC.Name)

		key := fmt.Sprintf("%s-%s", rdSpec.ProtectedPVC.Namespace, rdSpec.ProtectedPVC.Name)

		_, ok := rdSpecsUsingCG[key]
		if ok {
			v.log.Info("Skip Reconcile RD as Secondary as it's in a consistency group", "RDSpec", rdSpec.ProtectedPVC.Name)

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

func (v *VRGInstance) reconcileCGMembership() (map[string]struct{}, bool, error) {
	groups := map[string][]ramendrv1alpha1.VolSyncReplicationDestinationSpec{}

	rdSpecsUsingCG := make(map[string]struct{})

	for _, rdSpec := range v.instance.Spec.VolSync.RDSpec {
		cgLabelVal, ok := rdSpec.ProtectedPVC.Labels[ConsistencyGroupLabel]
		if ok && util.IsCGEnabledForVolSync(v.ctx, v.reconciler.APIReader, v.instance.Annotations) {
			v.log.Info("RDSpec contains the CG label from the primary cluster", "Label", cgLabelVal)
			// Get the CG label value for this cluster
			cgLabelVal, err := v.getCGLabelValue(rdSpec.ProtectedPVC.StorageClassName,
				rdSpec.ProtectedPVC.Name, rdSpec.ProtectedPVC.Namespace)
			if err != nil {
				v.log.Error(err, "Failed to get cgLabelVal")

				return rdSpecsUsingCG, true, err
			}

			key := fmt.Sprintf("%s-%s", rdSpec.ProtectedPVC.Namespace, rdSpec.ProtectedPVC.Name)
			rdSpecsUsingCG[key] = struct{}{}

			groups[cgLabelVal] = append(groups[cgLabelVal], rdSpec)
		}
	}

	requeue, err := v.createOrUpdateReplicationDestinations(groups)

	return rdSpecsUsingCG, requeue, err
}

func (v *VRGInstance) createOrUpdateReplicationDestinations(
	groups map[string][]ramendrv1alpha1.VolSyncReplicationDestinationSpec,
) (bool, error) {
	requeue := false

	for groupKey, groupVal := range groups {
		cephfsCGHandler := cephfscg.NewVSCGHandler(
			v.ctx, v.reconciler.Client, v.instance,
			&metav1.LabelSelector{MatchLabels: map[string]string{ConsistencyGroupLabel: groupKey}},
			v.volSyncHandler, groupKey, v.log,
		)

		v.log.Info("Create ReplicationGroupDestination with RDSpecs", "RDSpecs", v.getRDSpecGroupName(groups[groupKey]))

		namespace := v.commonProtectedPVCNamespace(groupVal)
		if namespace == "" {
			v.log.Error(fmt.Errorf("RDSpecs in the group %s have different namespaces", groupKey),
				"Failed to create ReplicationGroupDestination")

			requeue = true

			return requeue, fmt.Errorf("RDSpecs in the group %s have different namespaces", groupKey)
		}

		replicationGroupDestination, err := cephfsCGHandler.CreateOrUpdateReplicationGroupDestination(
			v.instance.Name, namespace, groups[groupKey],
		)
		if err != nil {
			v.log.Error(err, "Failed to create ReplicationGroupDestination")

			requeue = true

			return requeue, err
		}

		ready, err := util.IsReplicationGroupDestinationReady(v.ctx, v.reconciler.Client, replicationGroupDestination)
		if err != nil {
			v.log.Error(err, "Failed to check if ReplicationGroupDestination if ready")

			requeue = true

			return requeue, err
		}

		if !ready {
			v.log.Info(fmt.Sprintf("ReplicationGroupDestination for %s is not ready. We'll retry...",
				replicationGroupDestination.Name))

			requeue = true
		}
	}

	return requeue, nil
}

// commonProtectedPVCNamespace returns the shared namespace of a group of VolSyncReplicationDestinationSpec.
// If all items in the group have the same ProtectedPVC namespace, it returns that namespace; otherwise,
// it returns an empty string.
func (v *VRGInstance) commonProtectedPVCNamespace(destinationSpecs []ramendrv1alpha1.VolSyncReplicationDestinationSpec,
) string {
	if len(destinationSpecs) == 0 {
		return ""
	}

	namespace := destinationSpecs[0].ProtectedPVC.Namespace
	for _, spec := range destinationSpecs {
		if spec.ProtectedPVC.Namespace != namespace {
			return ""
		}
	}

	return namespace
}

func (v *VRGInstance) getRDSpecGroupName(rdSpecs []ramendrv1alpha1.VolSyncReplicationDestinationSpec) string {
	names := make([]string, 0, len(rdSpecs))

	for _, rdSpec := range rdSpecs {
		names = append(names, rdSpec.ProtectedPVC.Name)
	}

	return strings.Join(names, ",")
}

func (v *VRGInstance) aggregateVolSyncDataReadyCondition() *metav1.Condition {
	dataReadyCondition := &metav1.Condition{
		Status:             metav1.ConditionTrue,
		Type:               VRGConditionTypeDataReady,
		Reason:             VRGConditionReasonReady,
		ObservedGeneration: v.instance.Generation,
		Message:            "All VolSync PVCs are ready",
	}

	if len(v.volSyncPVCs) == 0 {
		dataReadyCondition.Reason = VRGConditionReasonUnused
		dataReadyCondition.Message = "No PVCs are protected using Volsync scheme"

		return dataReadyCondition
	}

	if v.instance.Spec.ReplicationState == ramendrv1alpha1.Secondary {
		if v.volSyncHandler.GetWorkloadStatus() == "active" {
			dataReadyCondition.Status = metav1.ConditionFalse
			dataReadyCondition.Reason = VRGConditionReasonProgressing
			dataReadyCondition.Message = "Some of the VolSync PVCs are active"

			return dataReadyCondition
		}

		dataReadyCondition.Reason = VRGConditionReasonUnused
		dataReadyCondition.Message = "Volsync based PVC protection does not report DataReady condition as Secondary"

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

//nolint:gocognit,funlen,cyclop,gocyclo
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

			condition := util.FindCondition(protectedPVC.Conditions, VRGConditionTypeVolSyncRepSourceSetup)
			if condition == nil || condition.Status != metav1.ConditionTrue {
				ready = false

				v.log.Info(fmt.Sprintf("VolSync RS hasn't been setup yet for PVC %s", protectedPVC.Name))

				break
			}

			// IFF however, we are running the final sync, then we have to wait
			condition = util.FindCondition(protectedPVC.Conditions, VRGConditionTypeVolSyncFinalSyncInProgress)
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

	actualVolSyncPVCs := 0

	for _, pvc := range v.volSyncPVCs {
		if util.ResourceIsDeleted(&pvc) {
			// If the PVC is deleted, we need to skip counting it.
			continue
		}

		actualVolSyncPVCs++
	}

	if ready && actualVolSyncPVCs > protectedByVolSyncCount {
		ready = false

		v.log.Info(fmt.Sprintf("VolSync PVCs count does not match with the ready PVCs %d/%d",
			actualVolSyncPVCs, protectedByVolSyncCount))
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
			condition := util.FindCondition(protectedPVC.Conditions, conType)
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

func (v *VRGInstance) isPVCDeletedForUnprotection(pvc *corev1.PersistentVolumeClaim) bool {
	pvcDeleted := util.ResourceIsDeleted(pvc)
	if !pvcDeleted {
		return false
	}

	if v.instance.Spec.ReplicationState != ramendrv1alpha1.Primary ||
		v.instance.Spec.PrepareForFinalSync ||
		v.instance.Spec.RunFinalSync {
		v.log.Info(
			"PVC deletion handling skipped",
			"replicationstate",
			v.instance.Spec.ReplicationState,
			"finalsync",
			v.instance.Spec.PrepareForFinalSync || v.instance.Spec.RunFinalSync,
		)

		return false
	}

	return true
}

func (v *VRGInstance) pvcUnprotectVolSyncIfDeleted(
	pvc corev1.PersistentVolumeClaim, log logr.Logger,
) (pvcDeleted bool) {
	if !v.isPVCDeletedForUnprotection(&pvc) {
		log.Info("PVC is not valid for unprotection", "PVC", pvc.Name)

		return false
	}

	log.Info("PVC unprotect VolSync", "deletion time", pvc.GetDeletionTimestamp())
	v.pvcUnprotectVolSync(pvc, log)

	return true
}

func (v *VRGInstance) pvcUnprotectVolSync(pvc corev1.PersistentVolumeClaim, log logr.Logger) {
	if !v.ramenConfig.VolumeUnprotectionEnabled {
		log.Info("Volume unprotection disabled")

		return
	}

	if util.IsCGEnabledForVolSync(v.ctx, v.reconciler.APIReader, v.instance.Annotations) {
		// At this moment, we don't support unprotecting CG PVCs.
		log.Info("Unprotecting CG PVCs is not supported", "PVC", pvc.Name)

		return
	}

	log.Info("Unprotecting VolSync PVC", "PVC", pvc.Name)
	// This call is only from Primary cluster. delete ReplicationSource and related resources.
	if err := v.volSyncHandler.UnprotectVolSyncPVC(&pvc); err != nil {
		log.Error(err, "Failed to unprotect VolSync PVC", "PVC", pvc.Name)

		return
	}
	// Remove the PVC from VRG status
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

// cleanupResources this function deleted all RS, RD, RGS, RGD and VolumeSnapshots from its owner
func (v *VRGInstance) cleanupResources() error {
	for idx := range v.volSyncPVCs {
		pvc := &v.volSyncPVCs[idx]

		if err := v.doCleanupResources(pvc.Name, pvc.Namespace); err != nil {
			return err
		}

		err := util.NewResourceUpdater(pvc).
			RemoveFinalizer(volsync.PVCFinalizerProtected).
			Update(v.ctx, v.reconciler.Client)
		if err != nil {
			v.log.Info("Failed to update PVC", "pvcName", pvc.GetName(), "error", err)

			return err
		}
	}

	for idx := range v.instance.Spec.VolSync.RDSpec {
		protectedPVC := v.instance.Spec.VolSync.RDSpec[idx].ProtectedPVC

		if err := v.doCleanupResources(protectedPVC.Name, protectedPVC.Namespace); err != nil {
			return err
		}
	}

	return nil
}

func (v *VRGInstance) doCleanupResources(name, namespace string) error {
	if err := v.volSyncHandler.DeleteRS(name, namespace); err != nil {
		return err
	}

	if err := v.volSyncHandler.DeleteRD(name, namespace); err != nil {
		return err
	}

	if err := v.volSyncHandler.DeleteSnapshots(namespace); err != nil {
		return err
	}

	if err := cephfscg.DeleteRGS(v.ctx, v.reconciler.Client, v.instance.Name, v.instance.Namespace, v.log); err != nil {
		return err
	}

	if err := cephfscg.DeleteRGD(v.ctx, v.reconciler.Client, v.instance.Name, v.instance.Namespace, v.log); err != nil {
		return err
	}

	return nil
}
