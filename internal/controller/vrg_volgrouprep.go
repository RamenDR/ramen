// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"fmt"

	"github.com/go-logr/logr"

	volrep "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	rmnutil "github.com/ramendr/ramen/internal/controller/util"
)

//nolint:gocognit,cyclop,funlen
func (v *VRGInstance) reconcileVolGroupRepsAsPrimary(groupPVCs map[types.NamespacedName][]*corev1.PersistentVolumeClaim,
) {
	for vgrNamespacedName, pvcs := range groupPVCs {
		log := v.log.WithValues("vgr", vgrNamespacedName.String())

		requeueResult, _, err := v.processVGRAsPrimary(vgrNamespacedName, pvcs, log)
		if requeueResult {
			v.requeue()
		}

		if err != nil {
			log.Info("Failure in getting or creating VolumeGroupReplication resource",
				"errorValue", err)

			continue
		}

		for idx := range pvcs {
			pvc := pvcs[idx]

			if err := v.uploadPVandPVCtoS3Stores(pvc, log); err != nil {
				log.Error(err, "Requeuing due to failure to upload PV/PVC object to S3 store(s)")

				v.requeue()

				continue
			}
		}
	}
}

//nolint:gocognit,cyclop,funlen
func (v *VRGInstance) reconcileVolGroupRepsAsSecondary(requeue *bool,
	groupPVCs map[types.NamespacedName][]*corev1.PersistentVolumeClaim,
) {
	for vgrNamespacedName, pvcs := range groupPVCs {
		log := v.log.WithValues("vgr", vgrNamespacedName.String())

		vrMissing, requeueResult := v.reconcileMissingVGR(vgrNamespacedName, pvcs, log)
		if vrMissing || requeueResult {
			*requeue = true

			continue
		}

		requeueResult, ready, skip := v.reconcileVGRAsSecondary(vgrNamespacedName, pvcs, log)
		if requeueResult {
			*requeue = true

			continue
		}

		if skip || !ready {
			continue
		}

		if v.undoPVCFinalizersAndPVRetentionForVGR(vgrNamespacedName, pvcs, log) {
			*requeue = true

			continue
		}

		for idx := range pvcs {
			pvc := pvcs[idx]

			v.pvcStatusDeleteIfPresent(pvc.Namespace, pvc.Name, log)
		}
	}
}

func (v *VRGInstance) reconcileVGRAsSecondary(vrNamespacedName types.NamespacedName,
	pvcs []*corev1.PersistentVolumeClaim, log logr.Logger,
) (bool, bool, bool) {
	const (
		requeue bool = true
		skip    bool = true
	)

	for idx := range pvcs {
		if !v.isPVCReadyForSecondary(pvcs[idx], log) {
			return requeue, false, skip
		}
	}

	requeueResult, ready, err := v.processVGRAsSecondary(vrNamespacedName, pvcs, log)
	if err != nil {
		log.Info("Failure in getting or creating VolumeGroupReplication resource",
			"errorValue", err)
	}

	return requeueResult, ready, !skip
}

//nolint:gocognit
func (v *VRGInstance) pvcsUnprotectVolGroupRep(groupPVCs map[types.NamespacedName][]*corev1.PersistentVolumeClaim) {
	for vgrNamespacedName, pvcs := range groupPVCs {
		log := v.log.WithValues("vgr", vgrNamespacedName.String())

		vgrMissing, requeueResult := v.reconcileMissingVGR(vgrNamespacedName, pvcs, log)
		if vgrMissing || requeueResult {
			v.requeue()

			continue
		}

		if v.reconcileVGRForDeletion(vgrNamespacedName, pvcs, log) {
			v.requeue()

			continue
		}

		log.Info("Successfully processed VolumeGroupReplication", "VR instance",
			v.instance.Name)
	}
}

func (v *VRGInstance) reconcileVGRForDeletion(vrNamespacedName types.NamespacedName,
	pvcs []*corev1.PersistentVolumeClaim, log logr.Logger,
) bool {
	const requeue = true

	if v.instance.Spec.ReplicationState == ramendrv1alpha1.Secondary {
		requeueResult, ready, skip := v.reconcileVGRAsSecondary(vrNamespacedName, pvcs, log)
		if requeueResult {
			log.Info("Requeuing due to failure in reconciling VolumeGroupReplication resource as secondary")

			return requeue
		}

		if skip || !ready {
			log.Info("Skipping further processing of VolumeGroupReplication resource as it is not ready",
				"skip", skip, "ready", ready)

			return !requeue
		}
	} else {
		requeueResult, ready, err := v.processVGRAsPrimary(vrNamespacedName, pvcs, log)

		switch {
		case err != nil:
			log.Info("Requeuing due to failure in getting or creating VolumeGroupReplication resource",
				"errorValue", err)

			fallthrough
		case requeueResult:
			return requeue
		case !ready:
			return requeue
		}
	}

	return v.undoPVCFinalizersAndPVRetentionForVGR(vrNamespacedName, pvcs, log)
}

func (v *VRGInstance) undoPVCFinalizersAndPVRetentionForVGR(vrNamespacedName types.NamespacedName,
	pvcs []*corev1.PersistentVolumeClaim, log logr.Logger,
) bool {
	const requeue = true

	if err := v.deleteVGR(vrNamespacedName, log); err != nil {
		log.Info("Requeuing due to failure in finalizing VolumeGroupReplication resource",
			"errorValue", err)

		return requeue
	}

	for idx := range pvcs {
		pvc := pvcs[idx]

		if err := v.preparePVCForVRDeletion(pvc, log); err != nil {
			log.Info("Requeuing due to failure in preparing PersistentVolumeClaim for VolumeGroupReplication deletion",
				"errorValue", err)

			return requeue
		}
	}

	return !requeue
}

func (v *VRGInstance) reconcileMissingVGR(vrNamespacedName types.NamespacedName,
	pvcs []*corev1.PersistentVolumeClaim, log logr.Logger,
) (bool, bool) {
	const (
		requeue   = true
		vrMissing = true
	)

	if v.instance.Spec.Async == nil {
		return !vrMissing, !requeue
	}

	volRep := &volrep.VolumeGroupReplication{}

	err := v.reconciler.Get(v.ctx, vrNamespacedName, volRep)
	if err == nil {
		if rmnutil.ResourceIsDeleted(volRep) {
			log.Info("Requeuing due to processing a deleted VGR")

			return !vrMissing, requeue
		}

		return !vrMissing, !requeue
	}

	if !k8serrors.IsNotFound(err) {
		log.Info("Requeuing due to failure in getting VGR resource", "errorValue", err)

		return !vrMissing, requeue
	}

	log.Info("Unprotecting PVCs as VGR is missing")

	for idx := range pvcs {
		pvc := pvcs[idx]

		if err := v.preparePVCForVRDeletion(pvc, log); err != nil {
			log.Info("Requeuing due to failure in preparing PersistentVolumeClaim for deletion",
				"errorValue", err)

			return vrMissing, requeue
		}
	}

	return vrMissing, !requeue
}

func (v *VRGInstance) isCGEnabled(pvc *corev1.PersistentVolumeClaim) (string, bool) {
	cg, ok := pvc.GetLabels()[ConsistencyGroupLabel]

	return cg, ok && rmnutil.IsCGEnabled(v.instance.GetAnnotations())
}

func (v *VRGInstance) processVGRAsPrimary(vrNamespacedName types.NamespacedName,
	pvcs []*corev1.PersistentVolumeClaim, log logr.Logger,
) (bool, bool, error) {
	return v.createOrUpdateVGR(vrNamespacedName, pvcs, volrep.Primary, log)
}

func (v *VRGInstance) processVGRAsSecondary(vrNamespacedName types.NamespacedName,
	pvcs []*corev1.PersistentVolumeClaim, log logr.Logger,
) (bool, bool, error) {
	return v.createOrUpdateVGR(vrNamespacedName, pvcs, volrep.Secondary, log)
}

func (v *VRGInstance) createOrUpdateVGR(vrNamespacedName types.NamespacedName,
	pvcs []*corev1.PersistentVolumeClaim, state volrep.ReplicationState, log logr.Logger,
) (bool, bool, error) {
	const requeue = true

	volRep := &volrep.VolumeGroupReplication{}

	err := v.reconciler.Get(v.ctx, vrNamespacedName, volRep)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			log.Error(err, "Failed to get VolumeGroupReplication resource", "resource", vrNamespacedName)

			// Failed to get VolRep and error is not IsNotFound. It is not
			// clear if the associated VolRep exists or not. If exists, then
			// is it replicating or not. So, mark the protected pvc as error
			// with condition.status as Unknown.
			msg := "Failed to get VolumeGroupReplication resource"

			for idx := range pvcs {
				pvc := pvcs[idx]

				v.updatePVCDataReadyCondition(pvc.Namespace, pvc.Name, VRGConditionReasonErrorUnknown, msg)
			}

			return requeue, false, fmt.Errorf("failed to get VolumeGroupReplication resource"+
				" (%s/%s) belonging to VolumeReplicationGroup (%s/%s), %w",
				vrNamespacedName.Namespace, vrNamespacedName.Name, v.instance.Namespace, v.instance.Name, err)
		}

		// Create VGR
		if err = v.createVGR(vrNamespacedName, pvcs, state); err != nil {
			log.Error(err, "Failed to create VolumeGroupReplication resource", "resource", vrNamespacedName)
			rmnutil.ReportIfNotPresent(v.reconciler.eventRecorder, v.instance, corev1.EventTypeWarning,
				rmnutil.EventReasonVRCreateFailed, err.Error())

			msg := "Failed to create VolumeGroupReplication resource"

			for idx := range pvcs {
				pvc := pvcs[idx]

				v.updatePVCDataReadyCondition(pvc.Namespace, pvc.Name, VRGConditionReasonError, msg)
			}

			return requeue, false, fmt.Errorf("failed to create VolumeGroupReplication resource"+
				" (%s/%s) belonging to VolumeReplicationGroup (%s/%s), %w",
				vrNamespacedName.Namespace, vrNamespacedName.Name, v.instance.Namespace, v.instance.Name, err)
		}

		// Just created VolRep. Mark status.conditions as Progressing.
		msg := "Created VolumeGroupReplication resource"

		for idx := range pvcs {
			pvc := pvcs[idx]

			v.updatePVCDataReadyCondition(pvc.Namespace, pvc.Name, VRGConditionReasonProgressing, msg)
		}

		return !requeue, false, nil
	}

	return v.updateVGR(pvcs, volRep, state, log)
}

func (v *VRGInstance) updateVGR(pvcs []*corev1.PersistentVolumeClaim,
	volRep *volrep.VolumeGroupReplication, state volrep.ReplicationState, log logr.Logger,
) (bool, bool, error) {
	const requeue = true

	log.Info(fmt.Sprintf("Update VolumeGroupReplication %s/%s", volRep.Namespace, volRep.Name))

	if volRep.Spec.ReplicationState == state && volRep.Spec.AutoResync == v.autoResync(state) {
		log.Info("VolumeGroupReplication and VolumeReplicationGroup state match. Proceeding to status check")

		return !requeue, v.checkVRStatus(pvcs, volRep, &volRep.Status.VolumeReplicationStatus), nil
	}

	volRep.Spec.ReplicationState = state
	volRep.Spec.AutoResync = v.autoResync(state)

	if err := v.reconciler.Update(v.ctx, volRep); err != nil {
		log.Error(err, "Failed to update VolumeGroupReplication resource",
			"name", volRep.GetName(), "namespace", volRep.GetNamespace(),
			"state", state)
		rmnutil.ReportIfNotPresent(v.reconciler.eventRecorder, v.instance, corev1.EventTypeWarning,
			rmnutil.EventReasonVRUpdateFailed, err.Error())

		msg := "Failed to update VolumeGroupReplication resource"

		for idx := range pvcs {
			pvc := pvcs[idx]

			v.updatePVCDataReadyCondition(pvc.Namespace, pvc.Name, VRGConditionReasonError, msg)
		}

		return requeue, false, fmt.Errorf("failed to update VolumeGroupReplication resource"+
			" (%s/%s) as %s, belonging to VolumeReplicationGroup (%s/%s), %w",
			volRep.GetNamespace(), volRep.GetName(), state,
			v.instance.Namespace, v.instance.Name, err)
	}

	log.Info(fmt.Sprintf("Updated VolumeGroupReplication resource (%s/%s) with state %s",
		volRep.GetName(), volRep.GetNamespace(), state))
	// Just updated the state of the VolRep. Mark it as progressing.
	msg := "Updated VolumeGroupReplication resource"

	for idx := range pvcs {
		pvc := pvcs[idx]

		v.updatePVCDataReadyCondition(pvc.Namespace, pvc.Name, VRGConditionReasonProgressing, msg)
	}

	return !requeue, false, nil
}

// createVGR creates a VolumeGroupReplication CR
func (v *VRGInstance) createVGR(vrNamespacedName types.NamespacedName,
	pvcs []*corev1.PersistentVolumeClaim, state volrep.ReplicationState,
) error {
	pvcNamespacedName := types.NamespacedName{Name: pvcs[0].Name, Namespace: pvcs[0].Namespace}

	volumeReplicationClass, err := v.selectVolumeReplicationClass(pvcNamespacedName, false)
	if err != nil {
		return fmt.Errorf("failed to find the appropriate VolumeReplicationClass (%s) %w",
			v.instance.Name, err)
	}

	volumeGroupReplicationClass, err := v.selectVolumeReplicationClass(pvcNamespacedName, true)
	if err != nil {
		return fmt.Errorf("failed to find the appropriate VolumeGroupReplicationClass (%s) %w",
			v.instance.Name, err)
	}

	cg, ok := pvcs[0].GetLabels()[ConsistencyGroupLabel]
	if !ok {
		return fmt.Errorf("failed to create VolumeGroupReplication (%s/%s) %w",
			vrNamespacedName.Namespace, vrNamespacedName.Name, err)
	}

	selector := metav1.AddLabelToSelector(&v.recipeElements.PvcSelector.LabelSelector, ConsistencyGroupLabel, cg)

	volRep := &volrep.VolumeGroupReplication{
		ObjectMeta: metav1.ObjectMeta{
			Name:      vrNamespacedName.Name,
			Namespace: vrNamespacedName.Namespace,
			Labels:    rmnutil.OwnerLabels(v.instance),
		},
		Spec: volrep.VolumeGroupReplicationSpec{
			ReplicationState:                state,
			VolumeReplicationClassName:      volumeReplicationClass.GetName(),
			VolumeGroupReplicationClassName: volumeGroupReplicationClass.GetName(),
			Source: volrep.VolumeGroupReplicationSource{
				Selector: selector,
			},
		},
	}

	if !vrgInAdminNamespace(v.instance, v.ramenConfig) {
		// This is to keep existing behavior of ramen.
		// Set the owner reference only for the VRs which are in the same namespace as the VRG and
		// when VRG is not in the admin namespace.
		if err := ctrl.SetControllerReference(v.instance, volRep, v.reconciler.Scheme); err != nil {
			return fmt.Errorf("failed to set owner reference to VolumeGroupReplication resource (%s/%s), %w",
				volRep.GetName(), volRep.GetNamespace(), err)
		}
	}

	if err := v.reconciler.Create(v.ctx, volRep); err != nil {
		return fmt.Errorf("failed to create VolumeGroupReplication resource (%s/%s), %w",
			volRep.GetName(), volRep.GetNamespace(), err)
	}

	v.log.Info(fmt.Sprintf("Created VolumeGroupReplication resource (%s/%s) with state %s",
		volRep.GetName(), volRep.GetNamespace(), state))

	return nil
}

func (v *VRGInstance) deleteVGR(vrNamespacedName types.NamespacedName, log logr.Logger) error {
	cr := &volrep.VolumeGroupReplication{
		ObjectMeta: metav1.ObjectMeta{
			Name:      vrNamespacedName.Name,
			Namespace: vrNamespacedName.Namespace,
		},
	}

	err := v.reconciler.Delete(v.ctx, cr)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			log.Error(err, "Failed to delete VolumeGroupReplication resource")

			return fmt.Errorf("failed to delete VolumeGroupReplication resource (%s/%s), %w",
				vrNamespacedName.Namespace, vrNamespacedName.Name, err)
		}

		return nil
	}

	v.log.Info("Deleted VolumeGroupReplication resource %s/%s", vrNamespacedName.Namespace, vrNamespacedName.Name)

	return v.ensureVRDeletedFromAPIServer(vrNamespacedName, cr, log)
}
