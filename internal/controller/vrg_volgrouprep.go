// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/go-logr/logr"

	volrep "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

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

		if err := v.uploadVGRandVGRCtoS3Stores(vgrNamespacedName, log); err != nil {
			log.Error(err, "Requeuing due to failure to upload VGR object to S3 store(s)")

			v.requeue()

			continue
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

func (v *VRGInstance) addAnnotationForResource(resource client.Object, resourceType, annotationKey,
	annotationValue string, log logr.Logger,
) error {
	annotations := resource.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	// Check if the annotation is already set
	if currentValue, exists := annotations[annotationKey]; exists && currentValue == annotationValue {
		log.Info(fmt.Sprintf("%s annotation already set to the desired value", resourceType), "resource", resource.GetName())

		return nil
	}

	annotations[annotationKey] = annotationValue
	resource.SetAnnotations(annotations)

	if err := v.reconciler.Update(v.ctx, resource); err != nil {
		return fmt.Errorf("failed to update %s (%s/%s) annotation (%s) belonging to VolumeReplicationGroup (%s/%s), %w",
			resourceType, resource.GetNamespace(), resource.GetName(), annotationKey,
			v.instance.Namespace, v.instance.Name, err)
	}

	log.Info(fmt.Sprintf("%s (%s/%s) annotation (%s) successfully updated to %s",
		resourceType, resource.GetNamespace(), resource.GetName(), annotationKey, annotationValue))

	return nil
}

func (v *VRGInstance) isVGRandVGRCArchivedAlready(vgr *volrep.VolumeGroupReplication, log logr.Logger) bool {
	vgrc, err := v.getVGRCFromVGR(vgr)
	if err != nil {
		log.Error(err, "Failed to get VGRC to check if archived")

		return false
	}

	if vgr.Annotations[pvcVRAnnotationArchivedKey] != v.generateArchiveAnnotation(vgr.Generation) {
		return false
	}

	if vgrc.Annotations[pvcVRAnnotationArchivedKey] != v.generateArchiveAnnotation(vgrc.Generation) {
		return false
	}

	return true
}

// Upload VGRCs and VGRs to the list of S3 stores in the VRG spec
func (v *VRGInstance) uploadVGRandVGRCtoS3Stores(vrNamespacedName types.NamespacedName, log logr.Logger) error {
	vgr := &volrep.VolumeGroupReplication{}

	err := v.reconciler.Get(v.ctx, vrNamespacedName, vgr)
	if err != nil {
		return fmt.Errorf("failed to get VGR (%w)", err)
	}

	if v.isVGRandVGRCArchivedAlready(vgr, log) {
		msg := fmt.Sprintf("VGR %s cluster data already protected", vgr.Name)
		v.log.Info(msg)

		return nil
	}

	// Error out if VRG has no S3 profiles
	numProfilesToUpload := len(v.instance.Spec.S3Profiles)
	if numProfilesToUpload == 0 {
		msg := "Error uploading VGR and VGRC cluster data because VRG spec has no S3 profiles"
		v.log.Info(msg)

		return fmt.Errorf("error uploading cluster data of VGR and VGRC %s because VRG spec has no S3 profiles",
			vgr.Name)
	}

	s3Profiles, err := v.UploadVGRandVGRCtoS3Stores(vgr, log)
	if err != nil {
		return fmt.Errorf("failed to upload VGR/VGRC with error (%w). Uploaded to %v S3 profile(s)", err, s3Profiles)
	}

	numProfilesUploaded := len(s3Profiles)

	if numProfilesUploaded != numProfilesToUpload {
		// Merely defensive as we don't expect to reach here
		msg := fmt.Sprintf("uploaded VGR/VGRC cluster data to only  %d of %d S3 profile(s): %v",
			numProfilesUploaded, numProfilesToUpload, s3Profiles)
		v.log.Info(msg)

		return errors.New(msg)
	}

	if err := v.addArchivedAnnotationForVGRandVGRC(vgr, log); err != nil {
		return err
	}

	msg := fmt.Sprintf("Done uploading VGR/VGRC cluster data to %d of %d S3 profile(s): %v",
		numProfilesUploaded, numProfilesToUpload, s3Profiles)
	v.log.Info(msg)

	return nil
}

func (v *VRGInstance) UploadVGRandVGRCtoS3Store(s3ProfileName string, vgr *volrep.VolumeGroupReplication) error {
	if s3ProfileName == "" {
		return fmt.Errorf("missing S3 profiles, failed to protect cluster data for VGR %s", vgr.Name)
	}

	objectStore, err := v.getObjectStorer(s3ProfileName)
	if err != nil {
		return fmt.Errorf("error getting object store, failed to protect cluster data for VGR %s, %w", vgr.Name, err)
	}

	vgrc, err := v.getVGRCFromVGR(vgr)
	if err != nil {
		return fmt.Errorf("error getting VGRC for VGR, failed to protect cluster data for VGRC %s to s3Profile %s, %w",
			vgrc.Name, s3ProfileName, err)
	}

	return v.UploadVGRAndVGRCtoS3(s3ProfileName, objectStore, vgr, &vgrc)
}

func (v *VRGInstance) UploadVGRAndVGRCtoS3(s3ProfileName string, objectStore ObjectStorer,
	vgr *volrep.VolumeGroupReplication, vgrc *volrep.VolumeGroupReplicationContent,
) error {
	if err := UploadVGRC(objectStore, v.s3KeyPrefix(), vgrc.Name, *vgrc); err != nil {
		var aerr awserr.Error
		if errors.As(err, &aerr) {
			// Treat any aws error as a persistent error
			v.cacheObjectStorer(s3ProfileName, nil,
				fmt.Errorf("persistent error while uploading to s3 profile %s, will retry later", s3ProfileName))
		}

		err := fmt.Errorf("error uploading VGRC to s3Profile %s, failed to protect cluster data for VGRC %s, %w",
			s3ProfileName, vgrc.Name, err)

		return err
	}

	vgrNamespacedName := types.NamespacedName{Namespace: vgr.Namespace, Name: vgr.Name}
	vgrNamespacedNameString := vgrNamespacedName.String()

	if err := UploadVGR(objectStore, v.s3KeyPrefix(), vgrNamespacedNameString, *vgr); err != nil {
		err := fmt.Errorf("error uploading VGR to s3Profile %s, failed to protect cluster data for VGR %s, %w",
			s3ProfileName, vgrNamespacedNameString, err)

		return err
	}

	return nil
}

func (v *VRGInstance) UploadVGRandVGRCtoS3Stores(vgr *volrep.VolumeGroupReplication,
	log logr.Logger,
) ([]string, error) {
	succeededProfiles := []string{}
	// Upload the VGR and VGRC to all the S3 profiles in the VRG spec
	for _, s3ProfileName := range v.instance.Spec.S3Profiles {
		err := v.UploadVGRandVGRCtoS3Store(s3ProfileName, vgr)
		if err != nil {
			rmnutil.ReportIfNotPresent(v.reconciler.eventRecorder, v.instance, corev1.EventTypeWarning,
				rmnutil.EventReasonUploadFailed, err.Error())

			return succeededProfiles, err
		}

		succeededProfiles = append(succeededProfiles, s3ProfileName)
	}

	return succeededProfiles, nil
}

func (v *VRGInstance) getVGRCFromVGR(vgr *volrep.VolumeGroupReplication) (volrep.VolumeGroupReplicationContent, error) {
	vgrc := volrep.VolumeGroupReplicationContent{}

	vgrcName := vgr.Spec.VolumeGroupReplicationContentName
	vgrcObjectKey := client.ObjectKey{
		Name: vgrcName,
	}

	if err := v.reconciler.Get(v.ctx, vgrcObjectKey, &vgrc); err != nil {
		return vgrc, fmt.Errorf("failed to get VGRC %v from VGR %v, %w",
			vgrcObjectKey, client.ObjectKeyFromObject(vgr), err)
	}

	return vgrc, nil
}

// pvcUnprotectVolGroupRep removes protection for a PVC when VRG is Primary and is protected by VGR in a CG.
// The unprotection works as follows:
// - Sets the CG label value to an empty string, ensuring the PVC is deselected from the CG that it belongs to
// - Ensures the PVC is no longer reported as part of the VGR status as protected
// - Deletes the VGR if it was protecting only this PVC
// - Removes PV/PVC protection metadata
// As the order ensures that the last action for the PVC is to remove its CG label, the entire workflow is idempotent
// across multiple reconcile loops.
// Further, all errors/progress is reported into the PVC DataReady condition.
func (v *VRGInstance) pvcUnprotectVolGroupRep(pvc *corev1.PersistentVolumeClaim) {
	if reset, err := v.resetCGLabelValue(pvc); err != nil || !reset {
		if err != nil {
			v.updatePVCDataReadyCondition(pvc.Namespace, pvc.Name, VRGConditionReasonError,
				fmt.Sprintf("Retrying, on error (%s) processing PVC for deletion or deselection from protection", err))
		} else {
			v.updatePVCDataReadyCondition(pvc.Namespace, pvc.Name, VRGConditionReasonError,
				"PVC is deleted or deselected from protection, and is not protected as part of a consistency group")
		}

		v.requeue()

		return
	}

	vgr, err := v.getVGRUsingSCLabel(pvc)
	if err != nil && !k8serrors.IsNotFound(err) {
		v.updatePVCDataReadyCondition(pvc.Namespace, pvc.Name, VRGConditionReasonError,
			fmt.Sprintf("Retrying, on error (%s) processing PVC for deletion or deselection from protection", err))

		v.requeue()

		return
	}

	if err == nil {
		if !v.ensurePVCUnprotected(pvc, vgr) {
			v.updatePVCDataReadyCondition(pvc.Namespace, pvc.Name, VRGConditionReasonProgressing,
				"PVC is being processed for deletion or deselection from protection")

			v.requeue()

			return
		}

		if err := v.deleteVGRIfUnused(vgr); err != nil {
			v.updatePVCDataReadyCondition(pvc.Namespace, pvc.Name, VRGConditionReasonError,
				fmt.Sprintf("Retrying, on error (%s) processing PVC for deletion or deselection from protection", err),
			)

			v.requeue()

			return
		}
	}

	// VGR not found or ensured that PVC is not part of VRG status
	if err := v.preparePVCForVRDeletion(pvc, v.log); err != nil {
		v.updatePVCDataReadyCondition(pvc.Namespace, pvc.Name, VRGConditionReasonError,
			fmt.Sprintf("Retrying due to error (%s) processing PVC for deletion or deselection from protection", err),
		)

		v.requeue()

		return
	}
}

// resetCGLabelValue resets the CG label value to an empty string, and returns if reset was detected as a success
func (v *VRGInstance) resetCGLabelValue(pvc *corev1.PersistentVolumeClaim) (bool, error) {
	const reset = true

	cg, ok := v.isCGEnabled(pvc)
	if !ok {
		v.log.Info("PVC is not protected by a CG", "VR instance", v.instance.Name)

		return !reset, fmt.Errorf("PVC is not protected as part of a consistency group")
	}

	if cg == "" {
		return reset, nil
	}

	err := rmnutil.NewResourceUpdater(pvc).AddLabel(ConsistencyGroupLabel, "").Update(v.ctx, v.reconciler.Client)
	if err != nil {
		return !reset, fmt.Errorf("error (%s) updating PVC labels", err)
	}

	return !reset, nil
}

// getVGRUsingSCLabel fetches the VGR that is protecting the PVC using the SC and VGRC labels, it is useful when the CG
// label is present without a correlating value to construuct the VGR name
func (v *VRGInstance) getVGRUsingSCLabel(pvc *corev1.PersistentVolumeClaim) (*volrep.VolumeGroupReplication, error) {
	rID, err := v.getVGRClassReplicationID(pvc)
	if err != nil {
		// Error is masked here, as caller expects k8serrors regarding vgr resource
		return nil, fmt.Errorf("error determining replicationID")
	}

	vgrNamespacedName := types.NamespacedName{
		Name:      rmnutil.TrimToK8sResourceNameLength(rID + v.instance.Name),
		Namespace: pvc.Namespace,
	}

	vgr := &volrep.VolumeGroupReplication{}
	err = v.reconciler.Get(v.ctx, vgrNamespacedName, vgr)

	return vgr, err
}

// getVGRClassReplicationID is a utility function that fetches the replicationID for the PVC looking at the class labels
func (v *VRGInstance) getVGRClassReplicationID(pvc *corev1.PersistentVolumeClaim) (string, error) {
	vgrClass, err := v.selectVolumeReplicationClass(pvc, true)
	if err != nil {
		return "", err
	}

	replicationID, ok := vgrClass.GetLabels()[ReplicationIDLabel]
	if !ok {
		v.log.Info(fmt.Sprintf("VolumeGroupReplicationClass %s is missing replicationID for PVC %s/%s",
			vgrClass.GetName(), pvc.GetNamespace(), pvc.GetName()))

		return "", fmt.Errorf("volumeGroupReplicationClass %s is missing replicationID for PVC %s/%s",
			vgrClass.GetName(), pvc.GetNamespace(), pvc.GetName())
	}

	return replicationID, nil
}

// ensurePVCUnprotected returns true if the passed in PVC is not protected by the vgr
func (v *VRGInstance) ensurePVCUnprotected(
	pvc *corev1.PersistentVolumeClaim,
	vgr *volrep.VolumeGroupReplication,
) bool {
	const unprotected = true

	if rmnutil.ResourceIsDeleted(vgr) {
		return !unprotected
	}

	for i := range vgr.Status.PersistentVolumeClaimsRefList {
		if vgr.Status.PersistentVolumeClaimsRefList[i].Name == pvc.GetName() {
			return !unprotected
		}
	}

	return unprotected
}

// deleteVGRIfUnused garbage collects a VGR that is not protecting any PVCs
func (v *VRGInstance) deleteVGRIfUnused(vgr *volrep.VolumeGroupReplication) error {
	if len(vgr.Status.PersistentVolumeClaimsRefList) != 0 {
		return nil
	}

	err := v.reconciler.Delete(v.ctx, vgr)
	if err != nil && !k8serrors.IsNotFound(err) {
		return err
	}
	// TODO: Delete VGR from S3 store

	return nil
}

//nolint:gocognit
func (v *VRGInstance) pvcsUnprotectVolGroupRep(groupPVCs map[types.NamespacedName][]*corev1.PersistentVolumeClaim) {
	// Single PVC that is deselected/deleted will not come here, in a circutious way!
	// VRG deletion will invoke this routine
	for vgrNamespacedName, pvcs := range groupPVCs {
		log := v.log.WithValues("vgr", vgrNamespacedName.String())

		vgrMissing, requeueResult := v.reconcileMissingVGR(vgrNamespacedName, pvcs, log)
		if vgrMissing || requeueResult {
			if requeueResult {
				v.requeue()
			}

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
	volumeReplicationClass, err := v.selectVolumeReplicationClass(pvcs[0], false)
	if err != nil {
		return fmt.Errorf("failed to find the appropriate VolumeReplicationClass (%s) %w",
			v.instance.Name, err)
	}

	volumeGroupReplicationClass, err := v.selectVolumeReplicationClass(pvcs[0], true)
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

func (v *VRGInstance) addArchivedAnnotationForVGRandVGRC(vgr *volrep.VolumeGroupReplication, log logr.Logger) error {
	value := v.generateArchiveAnnotation(vgr.Generation)

	err := v.addAnnotationForResource(vgr, "VolumeGroupReplication", pvcVRAnnotationArchivedKey, value, log)
	if err != nil {
		return err
	}

	vgrc, err := v.getVGRCFromVGR(vgr)
	if err != nil {
		log.Error(err, "Failed to get VGRC to add archived annotation")

		return fmt.Errorf("failed to update VGRC (%s) annotation (%s) belonging to"+
			"VolumeReplicationGroup (%s/%s), %w",
			vgrc.Name, pvcVRAnnotationArchivedKey, v.instance.Namespace, v.instance.Name, err)
	}

	value = v.generateArchiveAnnotation(vgrc.Generation)

	return v.addAnnotationForResource(&vgrc, "VolumeGroupReplicationContent", pvcVRAnnotationArchivedKey, value, log)
}

func (v *VRGInstance) restoreVGRsAndVGRCsForVolRep(result *ctrl.Result) error {
	if !rmnutil.IsCGEnabled(v.instance.GetAnnotations()) {
		return nil
	}

	v.log.Info("Restoring VolRep VGRs and VGRCs")

	if len(v.instance.Spec.S3Profiles) == 0 {
		v.log.Info("No S3 profiles configured")

		result.Requeue = true

		return fmt.Errorf("no S3Profiles configured")
	}

	v.log.Info(fmt.Sprintf("Restoring VGRs and VGRCs to this managed cluster. ProfileList: %v",
		v.instance.Spec.S3Profiles))

	err := v.restoreVGRsAndVGRCsFromS3(result)
	if err != nil {
		errMsg := fmt.Sprintf("failed to restore VGRs and VGRCs using profile list (%v)", v.instance.Spec.S3Profiles)
		v.log.Info(errMsg)

		return fmt.Errorf("%s: %w", errMsg, err)
	}

	return nil
}

func (v *VRGInstance) restoreVGRsAndVGRCsFromS3(result *ctrl.Result) error {
	err := errors.New("s3Profiles empty")
	NoS3 := false

	for _, s3ProfileName := range v.instance.Spec.S3Profiles {
		if s3ProfileName == NoS3StoreAvailable {
			v.log.Info("NoS3 available to fetch")

			NoS3 = true

			continue
		}

		var objectStore ObjectStorer

		objectStore, _, err = v.reconciler.ObjStoreGetter.ObjectStore(
			v.ctx, v.reconciler.APIReader, s3ProfileName, v.namespacedName, v.log)
		if err != nil {
			v.log.Error(err, "Kube objects recovery object store inaccessible", "profile", s3ProfileName)

			continue
		}

		var vgrcCount, vgrCount int

		// Restore all VGRCs found in the s3 store. If any failure, the next profile will be retried
		vgrcCount, err = v.restoreVGRCsFromObjectStore(objectStore, s3ProfileName)
		if err != nil {
			continue
		}

		vgrCount, err = v.restoreVGRsFromObjectStore(objectStore, s3ProfileName)
		if err != nil || vgrcCount != vgrCount {
			v.log.Info(fmt.Sprintf("Warning: Mismatch in VGRC/VGR count %d/%d (%v)",
				vgrcCount, vgrCount, err))

			continue
		}

		v.log.Info(fmt.Sprintf("Restored %d VGRCs and %d VGRs using profile %s", vgrcCount, vgrCount, s3ProfileName))

		return nil
	}

	if NoS3 {
		return nil
	}

	result.Requeue = true

	return err
}

func (v *VRGInstance) restoreVGRCsFromObjectStore(objectStore ObjectStorer, s3ProfileName string) (int, error) {
	vgrcList, err := downloadVGRCs(objectStore, v.s3KeyPrefix())
	if err != nil {
		v.log.Error(err, fmt.Sprintf("error fetching VGRC cluster data from S3 profile %s", s3ProfileName))

		return 0, err
	}

	v.log.Info(fmt.Sprintf("Found %d VGRCs in s3 store using profile %s", len(vgrcList), s3ProfileName))

	if err = v.checkVGRCClusterData(vgrcList); err != nil {
		errMsg := fmt.Sprintf("Error found in VGRC cluster data in S3 store %s", s3ProfileName)
		v.log.Info(errMsg)
		v.log.Error(err, fmt.Sprintf("Resolve VGRC conflict in the S3 store %s to deploy the application", s3ProfileName))

		return 0, fmt.Errorf("%s: %w", errMsg, err)
	}

	return restoreClusterDataObjects(v, vgrcList, "VGRC", cleanupVGRCForRestore, v.validateExistingVGRC)
}

func (v *VRGInstance) restoreVGRsFromObjectStore(objectStore ObjectStorer, s3ProfileName string) (int, error) {
	vgrList, err := downloadVGRs(objectStore, v.s3KeyPrefix())
	if err != nil {
		v.log.Error(err, fmt.Sprintf("error fetching VGR cluster data from S3 profile %s", s3ProfileName))

		return 0, err
	}

	v.log.Info(fmt.Sprintf("Found %d VGRs in s3 store using profile %s", len(vgrList), s3ProfileName))

	return restoreClusterDataObjects(v, vgrList, "VGR", v.cleanupVGRForRestore, v.validateExistingVGR)
}

func (v *VRGInstance) checkVGRCClusterData(vgrcList []volrep.VolumeGroupReplicationContent) error {
	vgrcMap := map[string]volrep.VolumeGroupReplicationContent{}
	// Scan the VGRCs and create a map of VGRCs that have conflicting volumeGroupReplicationRef
	for _, thisVGRC := range vgrcList {
		vgrRef := thisVGRC.Spec.VolumeGroupReplicationRef
		vgrKey := fmt.Sprintf("%s/%s", vgrRef.Namespace, vgrRef.Name)

		prevVGRC, found := vgrcMap[vgrKey]
		if !found {
			vgrcMap[vgrKey] = thisVGRC

			continue
		}

		msg := fmt.Sprintf("when restoring VGRC cluster data, detected conflicting vgrKey %s in VGRCs %s and %s",
			vgrKey, prevVGRC.Name, thisVGRC.Name)
		v.log.Info(msg)

		return errors.New(msg)
	}

	return nil
}

func (v *VRGInstance) validateExistingVGRC(vgrc *volrep.VolumeGroupReplicationContent) error {
	existingVGRC := &volrep.VolumeGroupReplicationContent{}
	if err := v.reconciler.Get(v.ctx, types.NamespacedName{Name: vgrc.Name}, existingVGRC); err != nil {
		return fmt.Errorf("failed to get existing VGRC %s (%w)", vgrc.Name, err)
	}

	if rmnutil.ResourceIsDeleted(existingVGRC) {
		return fmt.Errorf("existing VGRC %s is being deleted", existingVGRC.Name)
	}

	return nil
}

func (v *VRGInstance) validateExistingVGR(vgr *volrep.VolumeGroupReplication) error {
	existingVGR := &volrep.VolumeGroupReplication{}
	vgrNSName := types.NamespacedName{Name: vgr.Name, Namespace: vgr.Namespace}

	err := v.reconciler.Get(v.ctx, vgrNSName, existingVGR)
	if err != nil {
		return fmt.Errorf("failed to get existing VGR %s (%w)", vgrNSName.String(), err)
	}

	if rmnutil.ResourceIsDeleted(existingVGR) {
		return fmt.Errorf("existing VGR %s is being deleted", vgrNSName.String())
	}

	if existingVGR.Spec.VolumeGroupReplicationContentName != vgr.Spec.VolumeGroupReplicationContentName {
		return fmt.Errorf("VGR %s exists and refers to a different VGRC %s than VGRC %s desired",
			vgrNSName.String(), existingVGR.Spec.VolumeGroupReplicationContentName,
			vgr.Spec.VolumeGroupReplicationContentName)
	}

	v.log.Info(fmt.Sprintf("VGR %s exists and refers to desired VGRC %s", vgrNSName.String(),
		existingVGR.Spec.VolumeGroupReplicationContentName))

	return nil
}

func cleanupVGRCForRestore(vgrc *volrep.VolumeGroupReplicationContent) error {
	vgrc.ResourceVersion = ""
	vgrc.Spec.VolumeGroupReplicationRef = nil

	return nil
}

func (v *VRGInstance) cleanupVGRForRestore(vgr *volrep.VolumeGroupReplication) error {
	vgr.ObjectMeta.Annotations = PruneAnnotations(vgr.GetAnnotations())
	vgr.ObjectMeta.Finalizers = []string{}
	vgr.ObjectMeta.ResourceVersion = ""
	vgr.ObjectMeta.OwnerReferences = nil

	if !vrgInAdminNamespace(v.instance, v.ramenConfig) {
		if err := ctrl.SetControllerReference(v.instance, vgr, v.reconciler.Scheme); err != nil {
			return fmt.Errorf("failed to set owner reference to VolumeGroupReplication resource (%s/%s), %w",
				vgr.GetName(), vgr.GetNamespace(), err)
		}
	}

	return nil
}
