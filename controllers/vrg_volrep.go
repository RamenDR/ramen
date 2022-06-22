/*
Copyright 2022 The RamenDR authors.

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
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/go-logr/logr"

	volrep "github.com/csi-addons/volume-replication-operator/api/v1alpha1"
	volrepController "github.com/csi-addons/volume-replication-operator/controllers"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	rmnutil "github.com/ramendr/ramen/controllers/util"
)

// reconcileVolRepsAsPrimary creates/updates VolumeReplication CR for each pvc
// from pvcList. If it fails (even for one pvc), then requeue is set to true.
func (v *VRGInstance) reconcileVolRepsAsPrimary() bool {
	requeue := false

	for idx := range v.volRepPVCs {
		pvc := &v.volRepPVCs[idx]
		pvcNamespacedName := types.NamespacedName{Name: pvc.Name, Namespace: pvc.Namespace}
		log := v.log.WithValues("pvc", pvcNamespacedName.String())

		requeueResult, skip := v.preparePVCForVRProtection(pvc, log)
		if requeueResult {
			requeue = true

			continue
		}

		if skip {
			continue
		}

		if _, err := v.processVRAsPrimary(pvcNamespacedName, log); err != nil {
			log.Info("Requeuing due to failure in getting or creating VolumeReplication resource for PersistentVolumeClaim",
				"errorValue", err)

			requeue = true

			continue
		}

		// Protect the PVC's PV object stored in etcd by uploading it to S3
		// store(s).  Note that the VRG is responsible only to protect the PV
		// object of each PVC of the subscription.  However, the PVC object
		// itself is assumed to be protected along with other k8s objects in the
		// subscription, such as, the deployment, pods, services, etc., by an
		// entity external to the VRG a la IaC.
		if err := v.uploadPVToS3Stores(pvc, log); err != nil {
			log.Info("Requeuing due to failure to upload PV object to S3 store(s)",
				"errorValue", err)
			// TODO: use requeueAfter time duration.
			requeue = true

			continue
		}

		log.Info("Successfully processed VolumeReplication for PersistentVolumeClaim")
	}
	// We don't need the final sync preparation for VolRep. Just mark it complete
	if v.instance.Spec.PrepareForFinalSync && len(v.volSyncPVCs) == 0 {
		v.instance.Status.PrepareForFinalSyncComplete = true
	}
	// We don't need to run the final sync for VolRep. Just mark it complete
	if v.instance.Spec.RunFinalSync && len(v.volSyncPVCs) == 0 {
		v.instance.Status.FinalSyncComplete = true
	}

	return requeue
}

// reconcileVolRepsAsSecondary reconciles VolumeReplication resources for the VRG as secondary
func (v *VRGInstance) reconcileVolRepsAsSecondary() bool {
	requeue := false

	for idx := range v.volRepPVCs {
		pvc := &v.volRepPVCs[idx]
		pvcNamespacedName := types.NamespacedName{Name: pvc.Name, Namespace: pvc.Namespace}
		log := v.log.WithValues("pvc", pvcNamespacedName.String())

		requeueResult, skip := v.preparePVCForVRProtection(pvc, log)
		if requeueResult {
			requeue = true

			continue
		}

		if skip {
			continue
		}

		requeueResult, skip = v.reconcileVRAsSecondary(pvc, log)
		if requeueResult {
			requeue = true

			continue
		}

		if skip {
			continue
		}

		log.Info("Successfully processed VolumeReplication for PersistentVolumeClaim")
	}

	return requeue
}

func (v *VRGInstance) reconcileVRAsSecondary(pvc *corev1.PersistentVolumeClaim, log logr.Logger) (bool, bool) {
	const (
		requeue bool = true
		skip    bool = true
	)

	if !v.isPVCReadyForSecondary(pvc, log) {
		// 1) Dont requeue as a reconcile would be
		//    triggered when the events that indicate
		//    pvc being ready are caught by predicate.
		// 2) skip as this pvc is not ready for marking
		//    VolRep as secondary. Set the conditions to
		//    VRGConditionReasonProgressing.
		return !requeue, skip
	}

	pvcNamespacedName := types.NamespacedName{Name: pvc.Name, Namespace: pvc.Namespace}
	if _, err := v.processVRAsSecondary(pvcNamespacedName, log); err != nil {
		log.Info("Requeuing due to failure in getting or creating VolumeReplication resource for PersistentVolumeClaim",
			"errorValue", err)

		// Needs a requeue. And further processing of
		// VolRep can be skipped as processVRAsSecondary
		// failed.
		return requeue, skip
	}

	return !requeue, !skip
}

// isPVCReadyForSecondary checks if a PVC is ready to be marked as Secondary
func (v *VRGInstance) isPVCReadyForSecondary(pvc *corev1.PersistentVolumeClaim, log logr.Logger) bool {
	const ready bool = true

	// If PVC is not being deleted, it is not ready for Secondary
	if pvc.GetDeletionTimestamp().IsZero() {
		log.Info("VolumeReplication cannot become Secondary, as its PersistentVolumeClaim is not marked for deletion")

		msg := "PVC not being deleted. Not ready to become Secondary"
		v.updatePVCDataReadyCondition(pvc.Name, VRGConditionReasonProgressing, msg)

		return !ready
	}

	// If PVC is still in use, it is not ready for Secondary
	if containsString(pvc.ObjectMeta.Finalizers, pvcInUse) {
		log.Info("VolumeReplication cannot become Secondary, as its PersistentVolumeClaim is still in use")

		msg := "PVC still in use"
		v.updatePVCDataReadyCondition(pvc.Name, VRGConditionReasonProgressing, msg)

		return !ready
	}

	return ready
}

// preparePVCForVRProtection processes prerequisites of any PVC that needs VR protection. It returns
// a requeue if preparation failed, and returns skip if PVC can be skipped for VR protection
func (v *VRGInstance) preparePVCForVRProtection(pvc *corev1.PersistentVolumeClaim,
	log logr.Logger) (bool, bool) {
	const (
		requeue bool = true
		skip    bool = true
	)

	// if PVC protection is complete, return
	if pvc.Annotations[pvcVRAnnotationProtectedKey] == pvcVRAnnotationProtectedValue {
		return !requeue, !skip
	}

	// Dont requeue. There will be a reconcile request when predicate sees that pvc is ready.
	if skipResult, msg := skipPVC(pvc, log); skipResult {
		// @msg should not be nil as the decision is to skip the pvc.
		// msg should contain info on why that decision was made.
		if msg == "" {
			msg = "PVC not ready"
		}
		// Since pvc is skipped, mark the condition for the PVC as progressing. Even for
		// deletion this applies where if the VR protection finalizer is absent for pvc and
		// it is being deleted.
		v.updatePVCDataReadyCondition(pvc.Name, VRGConditionReasonProgressing, msg)

		return !requeue, skip
	}

	return v.protectPVC(pvc, log)
}

func (v *VRGInstance) protectPVC(pvc *corev1.PersistentVolumeClaim,
	log logr.Logger) (bool, bool) {
	const (
		requeue bool = true
		skip    bool = true
	)
	// Add VR finalizer to PVC for deletion protection
	if err := v.addProtectedFinalizerToPVC(pvc, log); err != nil {
		log.Info("Requeuing, as adding PersistentVolumeClaim finalizer failed", "errorValue", err)

		msg := "Failed to add Protected Finalizer to PVC"
		v.updatePVCDataReadyCondition(pvc.Name, VRGConditionReasonError, msg)

		return requeue, !skip
	}

	if err := v.retainPVForPVC(*pvc, log); err != nil { // Change PV `reclaimPolicy` to "Retain"
		log.Info("Requeuing, as retaining PersistentVolume failed", "errorValue", err)

		msg := "Failed to retain PV for PVC"
		v.updatePVCDataReadyCondition(pvc.Name, VRGConditionReasonError, msg)

		return requeue, !skip
	}

	// Annotate that PVC protection is complete, skip if being deleted
	if pvc.GetDeletionTimestamp().IsZero() {
		if err := v.addProtectedAnnotationForPVC(pvc, log); err != nil {
			log.Info("Requeuing, as annotating PersistentVolumeClaim failed", "errorValue", err)

			msg := "Failed to add protected annotatation to PVC"
			v.updatePVCDataReadyCondition(pvc.Name, VRGConditionReasonError, msg)

			return requeue, !skip
		}
	}

	return !requeue, !skip
}

// This function indicates whether to proceed with the pvc processing
// or not. It mainly checks the following things.
// - Whether pvc is bound or not. If not bound, then no need to
//   process the pvc any further. It can be skipped until it is ready.
// - Whether the pvc is being deleted and VR protection finalizer is
//   not there. If the finalizer is there, then VolumeReplicationGroup
//   need to remove the finalizer for the pvc being deleted. However,
//   if the finalizer is not there, then no need to process the pvc
//   any further and it can be skipped. The pvc will go away eventually.
func skipPVC(pvc *corev1.PersistentVolumeClaim, log logr.Logger) (bool, string) {
	if pvc.Status.Phase != corev1.ClaimBound {
		log.Info("Skipping handling of VR as PersistentVolumeClaim is not bound", "pvcPhase", pvc.Status.Phase)

		msg := "PVC not bound yet"
		// v.updateProtectedPVCCondition(pvc.Name, VRGConditionReasonProgressing, msg)

		return true, msg
	}

	return isPVCDeletedAndNotProtected(pvc, log)
}

func isPVCDeletedAndNotProtected(pvc *corev1.PersistentVolumeClaim, log logr.Logger) (bool, string) {
	// If PVC deleted but not yet protected with a finalizer, skip it!
	if !containsString(pvc.Finalizers, pvcVRFinalizerProtected) && !pvc.GetDeletionTimestamp().IsZero() {
		log.Info("Skipping PersistentVolumeClaim, as it is marked for deletion and not yet protected")

		msg := "Skipping pvc marked for deletion"
		// v.updateProtectedPVCCondition(pvc.Name, VRGConditionReasonProgressing, msg)

		return true, msg
	}

	return false, ""
}

// preparePVCForVRDeletion
func (v *VRGInstance) preparePVCForVRDeletion(pvc *corev1.PersistentVolumeClaim,
	log logr.Logger) error {
	// If PVC does not have the VR finalizer we are done
	if !containsString(pvc.Finalizers, pvcVRFinalizerProtected) {
		return nil
	}

	// For Async mode, we want to change the retention policy back to delete
	// and remove the annotation.
	// For Sync mode, we don't want to set the retention policy to delete as
	// both the primary and the secondary VRG map to the same volume. The only
	// state where a delete retention policy is required for the sync mode is
	// when the VRG is primary.
	if !(v.instance.Spec.ReplicationState == ramendrv1alpha1.Secondary &&
		v.instance.Spec.Sync.Mode == ramendrv1alpha1.SyncModeEnabled) {
		if err := v.undoPVRetentionForPVC(*pvc, log); err != nil {
			return err
		}
	}

	// TODO: Delete the PV from the backing store? But when is it safe to do so?
	// We can delete the PV when VRG (and hence VR) is being deleted as primary, as that is when the
	// application is finally being undeployed, and also the PV would be garbage collected.

	// Remove VR finalizer from PVC and the annotation (PVC maybe left behind, so remove the annotation)
	return v.removeProtectedFinalizerFromPVC(pvc, log)
}

// retainPVForPVC updates the PV reclaim policy to retain for a given PVC
func (v *VRGInstance) retainPVForPVC(pvc corev1.PersistentVolumeClaim, log logr.Logger) error {
	// Get PV bound to PVC
	pv := &corev1.PersistentVolume{}
	pvObjectKey := client.ObjectKey{
		Name: pvc.Spec.VolumeName,
	}

	if err := v.reconciler.Get(v.ctx, pvObjectKey, pv); err != nil {
		log.Error(err, "Failed to get PersistentVolume", "volumeName", pvc.Spec.VolumeName)

		return fmt.Errorf("failed to get PersistentVolume resource (%s) for"+
			" PersistentVolumeClaim resource (%s/%s) belonging to VolumeReplicationGroup (%s/%s), %w",
			pvc.Spec.VolumeName, pvc.Namespace, pvc.Name, v.instance.Namespace, v.instance.Name, err)
	}

	// Check reclaimPolicy of PV, if already set to retain
	if pv.Spec.PersistentVolumeReclaimPolicy == corev1.PersistentVolumeReclaimRetain {
		return nil
	}

	// if not retained, retain PV, and add an annotation to denote this is updated for VR needs
	pv.Spec.PersistentVolumeReclaimPolicy = corev1.PersistentVolumeReclaimRetain
	if pv.ObjectMeta.Annotations == nil {
		pv.ObjectMeta.Annotations = map[string]string{}
	}

	pv.ObjectMeta.Annotations[pvVRAnnotationRetentionKey] = pvVRAnnotationRetentionValue

	if err := v.reconciler.Update(v.ctx, pv); err != nil {
		log.Error(err, "Failed to update PersistentVolume reclaim policy")

		return fmt.Errorf("failed to update PersistentVolume resource (%s) reclaim policy for"+
			" PersistentVolumeClaim resource (%s/%s) belonging to VolumeReplicationGroup (%s/%s), %w",
			pvc.Spec.VolumeName, pvc.Namespace, pvc.Name, v.instance.Namespace, v.instance.Name, err)
	}

	return nil
}

// undoPVRetentionForPVC updates the PV reclaim policy back to its saved state
func (v *VRGInstance) undoPVRetentionForPVC(pvc corev1.PersistentVolumeClaim, log logr.Logger) error {
	// Get PV bound to PVC
	pv := &corev1.PersistentVolume{}
	pvObjectKey := client.ObjectKey{
		Name: pvc.Spec.VolumeName,
	}

	if err := v.reconciler.Get(v.ctx, pvObjectKey, pv); err != nil {
		log.Error(err, "Failed to get PersistentVolume", "volumeName", pvc.Spec.VolumeName)

		return fmt.Errorf("failed to get PersistentVolume resource (%s) for"+
			" PersistentVolumeClaim resource (%s/%s) belonging to VolumeReplicationGroup (%s/%s), %w",
			pvc.Spec.VolumeName, pvc.Namespace, pvc.Name, v.instance.Namespace, v.instance.Name, err)
	}

	if v, ok := pv.ObjectMeta.Annotations[pvVRAnnotationRetentionKey]; !ok || v != pvVRAnnotationRetentionValue {
		return nil
	}

	pv.Spec.PersistentVolumeReclaimPolicy = corev1.PersistentVolumeReclaimDelete
	delete(pv.ObjectMeta.Annotations, pvVRAnnotationRetentionKey)

	if err := v.reconciler.Update(v.ctx, pv); err != nil {
		log.Error(err, "Failed to update PersistentVolume reclaim policy", "volumeName", pvc.Spec.VolumeName)

		return fmt.Errorf("failed to update PersistentVolume resource (%s) reclaim policy for"+
			" PersistentVolumeClaim resource (%s/%s) belonging to VolumeReplicationGroup (%s/%s), %w",
			pvc.Spec.VolumeName, pvc.Namespace, pvc.Name, v.instance.Namespace, v.instance.Name, err)
	}

	return nil
}

// Upload PV to the list of S3 stores in the VRG spec
func (v *VRGInstance) uploadPVToS3Stores(pvc *corev1.PersistentVolumeClaim, log logr.Logger) (err error) {
	// Find the ProtectedPVC of the given PVC in v.instance.Status.ProtectedPVCs[]
	protectedPVC := v.findProtectedPVC(pvc.Name)
	// Find the ClusterDataProtected condition of the given PVC in ProtectedPVC.Conditions
	clusterDataProtected := findCondition(protectedPVC.Conditions, VRGConditionTypeClusterDataProtected)

	// Optimization: skip uploading the PV of this PVC if it was uploaded previously
	if clusterDataProtected != nil && clusterDataProtected.Status == metav1.ConditionTrue &&
		clusterDataProtected.ObservedGeneration == v.instance.Generation {
		// v.log.Info("PV cluster data already protected")
		return nil
	}

	// Error out if VRG has no S3 profiles
	numProfilesToUpload := len(v.instance.Spec.S3Profiles)
	if numProfilesToUpload == 0 {
		msg := "Error uploading PV cluster data because VRG spec has no S3 profiles"
		v.updatePVCClusterDataProtectedCondition(pvc.Name,
			VRGConditionReasonUploadError, msg)
		v.log.Info(msg)

		return fmt.Errorf("error uploading cluster data of PV %s because VRG spec has no S3 profiles",
			pvc.Name)
	}

	s3Profiles, err := v.PVUploadToObjectStores(pvc, log)
	if err != nil {
		return fmt.Errorf("error uploading PV cluster data to the list of s3 profiles")
	}

	numProfilesUploaded := len(s3Profiles)
	// Set ClusterDataProtected condition to true if PV was uploaded to all the profiles
	if numProfilesUploaded == numProfilesToUpload {
		msg := fmt.Sprintf("Done uploading PV cluster data to %d of %d S3 profile(s): %v",
			numProfilesUploaded, numProfilesToUpload, s3Profiles)
		v.log.Info(msg)
		v.updatePVCClusterDataProtectedCondition(pvc.Name,
			VRGConditionReasonUploaded, msg)
	} else {
		// Merely defensive as we don't expect to reach here
		msg := fmt.Sprintf("Uploaded PV cluster data to only  %d of %d S3 profile(s): %v",
			numProfilesUploaded, numProfilesToUpload, s3Profiles)
		v.log.Info(msg)
		v.updatePVCClusterDataProtectedCondition(pvc.Name,
			VRGConditionReasonUploadError, msg)
	}

	return nil
}

func (v *VRGInstance) PVUploadToObjectStore(s3ProfileName string, pvc *corev1.PersistentVolumeClaim) error {
	if s3ProfileName == "" {
		return fmt.Errorf("missing S3 profiles, failed to protect cluster data for PVC %s", pvc.Name)
	}

	objectStore, err := v.getObjectStorer(s3ProfileName)
	if err != nil {
		return fmt.Errorf("error getting object store, failed to protect cluster data for PVC %s, %w", pvc.Name, err)
	}

	pv, err := v.getPVFromPVC(pvc)
	if err != nil {
		return fmt.Errorf("error getting PV for PVC, failed to protect cluster data for PVC %s to s3Profile %s, %w",
			pvc.Name, s3ProfileName, err)
	}

	if err := UploadPV(objectStore, v.s3KeyPrefix(), pv.Name, pv); err != nil {
		var aerr awserr.Error
		if errors.As(err, &aerr) {
			// Treat any aws error as a persistent error
			v.cacheObjectStorer(s3ProfileName, nil,
				fmt.Errorf("persistent error while uploading to s3 profile %s, will retry later", s3ProfileName))
		}

		err := fmt.Errorf("error uploading PV to s3Profile %s, failed to protect cluster data for PVC %s, %w",
			s3ProfileName, pvc.Name, err)

		return err
	}

	return nil
}

func (v *VRGInstance) PVUploadToObjectStores(pvc *corev1.PersistentVolumeClaim,
	log logr.Logger) ([]string, error) {
	succeededProfiles := []string{}
	// Upload the PV to all the S3 profiles in the VRG spec
	for _, s3ProfileName := range v.instance.Spec.S3Profiles {
		err := v.PVUploadToObjectStore(s3ProfileName, pvc)
		if err != nil {
			v.updatePVCClusterDataProtectedCondition(pvc.Name, VRGConditionReasonUploadError, err.Error())
			rmnutil.ReportIfNotPresent(v.reconciler.eventRecorder, v.instance, corev1.EventTypeWarning,
				rmnutil.EventReasonPVUploadFailed, err.Error())

			return succeededProfiles, err
		}

		succeededProfiles = append(succeededProfiles, s3ProfileName)
	}

	return succeededProfiles, nil
}

func (v *VRGInstance) getPVFromPVC(pvc *corev1.PersistentVolumeClaim) (corev1.PersistentVolume, error) {
	pv := corev1.PersistentVolume{}
	volumeName := pvc.Spec.VolumeName
	pvObjectKey := client.ObjectKey{Name: volumeName}

	// Get PV from k8s
	if err := v.reconciler.Get(v.ctx, pvObjectKey, &pv); err != nil {
		return pv, fmt.Errorf("failed to get PV %w", err)
	}

	return pv, nil
}

func (v *VRGInstance) getObjectStorer(s3ProfileName string) (ObjectStorer, error) {
	objectStore, err := v.getCachedObjectStorer(s3ProfileName)
	if objectStore != nil || err != nil {
		return objectStore, err
	}

	objectStore, err = v.reconciler.ObjStoreGetter.ObjectStore(
		v.ctx,
		v.reconciler.APIReader,
		s3ProfileName,
		v.namespacedName,
		v.log)
	if err != nil {
		err = fmt.Errorf("error creating object store for s3Profile %s, %w", s3ProfileName, err)
	}

	v.cacheObjectStorer(s3ProfileName, objectStore, err)

	return objectStore, err
}

func (v *VRGInstance) getCachedObjectStorer(s3ProfileName string) (ObjectStorer, error) {
	if cachedObjectStore, ok := v.objectStorers[s3ProfileName]; ok {
		return cachedObjectStore.storer, cachedObjectStore.err
	}

	return nil, nil
}

func (v *VRGInstance) cacheObjectStorer(s3ProfileName string, objectStore ObjectStorer, err error) {
	v.objectStorers[s3ProfileName] = cachedObjectStorer{
		storer: objectStore,
		err:    err,
	}
}

// reconcileVRsForDeletion cleans up VR resources managed by VRG and also cleans up changes made to PVCs
// TODO: Currently removes VR requests unconditionally, needs to ensure it is managed by VRG
func (v *VRGInstance) reconcileVRsForDeletion() bool {
	requeue := false

	for idx := range v.volRepPVCs {
		pvc := &v.volRepPVCs[idx]
		pvcNamespacedName := types.NamespacedName{Name: pvc.Name, Namespace: pvc.Namespace}
		log := v.log.WithValues("pvc", pvcNamespacedName.String())

		// If the pvc does not have the VR protection finalizer, then one of the
		// 2 possibilities (assuming pvc is not being deleted).
		// 1) This pvc has not yet been processed by VRG before this deletion came on VRG
		// 2) The VolRep resource associated with this pvc has been successfully deleted and
		//    the VR protection finalizer has been successfully removed. No need to process.
		if !containsString(pvc.Finalizers, pvcVRFinalizerProtected) {
			log.Info(fmt.Sprintf("pvc %s does not contain VR protection finalizer. Skipping it",
				pvcNamespacedName))

			continue
		}

		requeueResult, skip := v.preparePVCForVRProtection(pvc, log)
		if requeueResult {
			requeue = true

			continue
		}

		if skip {
			continue
		}

		if v.reconcileVRForDeletion(pvc, log) {
			requeue = true

			continue
		}

		log.Info("Successfully processed VolumeReplication for PersistentVolumeClaim", "VR instance",
			v.instance.Name, "PVC", pvcNamespacedName)
	}

	return requeue
}

func (v *VRGInstance) reconcileVRForDeletion(pvc *corev1.PersistentVolumeClaim, log logr.Logger) bool {
	const requeue bool = true

	var (
		err       error
		available = true
	)

	pvcNamespacedName := types.NamespacedName{Name: pvc.Name, Namespace: pvc.Namespace}

	if v.instance.Spec.ReplicationState == ramendrv1alpha1.Secondary {
		requeueResult, skip := v.reconcileVRAsSecondary(pvc, log)
		if requeueResult {
			log.Info("Requeuing due to failure in reconciling VolumeReplication resource as secondary")

			return requeue
		}

		if skip {
			log.Info("Skipping further processing of VolumeReplication resource as it is not ready")

			return !requeue
		}
	} else if available, err = v.processVRAsPrimary(pvcNamespacedName, log); err != nil {
		log.Info("Requeuing due to failure in getting or creating VolumeReplication resource for PersistentVolumeClaim",
			"errorValue", err)

		return requeue
	}

	// Ensure VR is available at the required state before deletion (do this for Secondary as well?)
	if !available {
		return !requeue
	}

	// Deleting VR first may end-up recreating the VR if reconcile for this PVC is interrupted, but that is better than
	// leaking a VR as that would result in leaking a volume on the storage system
	if err := v.deleteVR(pvcNamespacedName, log); err != nil {
		log.Info("Requeuing due to failure in finalizing VolumeReplication resource for PersistentVolumeClaim",
			"errorValue", err)

		return requeue
	}

	if err := v.preparePVCForVRDeletion(pvc, log); err != nil {
		log.Info("Requeuing due to failure in preparing PersistentVolumeClaim for VolumeReplication deletion",
			"errorValue", err)

		return requeue
	}

	return !requeue
}

func (v *VRGInstance) deleteClusterDataInS3Stores(log logr.Logger) error {
	log.Info("Delete cluster data in", "s3Profiles", v.instance.Spec.S3Profiles)

	for _, s3ProfileName := range v.instance.Spec.S3Profiles {
		if s3ProfileName == NoS3StoreAvailable {
			v.log.Info("NoS3 available to clean")

			continue
		}

		if err := v.DeletePVs(s3ProfileName); err != nil {
			return fmt.Errorf("error deleting PVs using profile %s, err %w", s3ProfileName, err)
		}
	}

	return nil
}

func (v *VRGInstance) DeletePVs(s3ProfileName string) (err error) {
	objectStore, err := v.reconciler.ObjStoreGetter.ObjectStore(
		v.ctx,
		v.reconciler.APIReader,
		s3ProfileName,
		v.namespacedName, // debugTag
		v.log,
	)
	if err != nil {
		return fmt.Errorf("failed to get client for s3Profile %s, err %w",
			s3ProfileName, err)
	}

	s3KeyPrefix := v.s3KeyPrefix()
	msg := fmt.Sprintf("delete PVs with key prefix %s in profile %s",
		s3KeyPrefix, s3ProfileName)
	v.log.Info(msg)

	// Delete all PVs from this VRG's S3 bucket
	if err := objectStore.DeleteObjects(s3KeyPrefix); err != nil {
		return fmt.Errorf("failed to %s, %w", msg, err)
	}

	return nil
}

// processVRAsPrimary processes VR to change its state to primary, with the assumption that the
// related PVC is prepared for VR protection
func (v *VRGInstance) processVRAsPrimary(vrNamespacedName types.NamespacedName, log logr.Logger) (bool, error) {
	if v.instance.Spec.Async.Mode == ramendrv1alpha1.AsyncModeEnabled {
		return v.createOrUpdateVR(vrNamespacedName, volrep.Primary, log)
	}

	// TODO: createOrUpdateVR does two things. It modifies the VR and also
	// updates the PVC Conditions. For the sync mode, we only want the latter.
	// In the future, it would be better to refactor createOrUpdateVR into two
	// functions. For now, we are only updating the conditions for the sync
	// mode below. As there is no VolRep involved in sync mode, the
	// availability is always true. Also, the refactor should work for the
	// condition where both async and sync are enabled at the same time.
	if v.instance.Spec.Sync.Mode == ramendrv1alpha1.SyncModeEnabled {
		msg := "PVC in the VolumeReplicationGroup is ready for use"
		v.updatePVCDataReadyCondition(vrNamespacedName.Name, VRGConditionReasonReady, msg)
		v.updatePVCDataProtectedCondition(vrNamespacedName.Name, VRGConditionReasonReady, msg)

		return true, nil
	}

	return true, nil
}

// processVRAsSecondary processes VR to change its state to secondary, with the assumption that the
// related PVC is prepared for VR as secondary
func (v *VRGInstance) processVRAsSecondary(vrNamespacedName types.NamespacedName, log logr.Logger) (bool, error) {
	if v.instance.Spec.Async.Mode == ramendrv1alpha1.AsyncModeEnabled {
		return v.createOrUpdateVR(vrNamespacedName, volrep.Secondary, log)
	}

	// TODO: createOrUpdateVR does two things. It modifies the VR and also
	// updates the PVC Conditions. For the sync mode, we only want the latter.
	// In the future, it would be better to refactor createOrUpdateVR into two
	// functions. For now, we are only updating the conditions for the sync
	// mode below. As there is no VolRep involved in sync mode, the
	// availability is always true. Also, the refactor should work for the
	// condition where both async and sync are enabled at the same time.
	if v.instance.Spec.Sync.Mode == ramendrv1alpha1.SyncModeEnabled {
		msg := "VolumeReplication resource for the pvc as Secondary is in sync with Primary"
		v.updatePVCDataReadyCondition(vrNamespacedName.Name, VRGConditionReasonReplicated, msg)
		v.updatePVCDataProtectedCondition(vrNamespacedName.Name, VRGConditionReasonDataProtected, msg)

		return true, nil
	}

	return true, nil
}

// createOrUpdateVR updates an existing VR resource if found, or creates it if required
// While both creating and updating the VolumeReplication resource, conditions.status
// for the protected PVC (corresponding to the VolumeReplication resource) is set as
// VRGConditionReasonProgressing. When the VolumeReplication resource changes its state either due to
// successful reaching of the desired state or due to some error, VolumeReplicationGroup
// would get a reconcile. And then the conditions for the appropriate Protected PVC can
// be set as either Replicating or Error.
func (v *VRGInstance) createOrUpdateVR(vrNamespacedName types.NamespacedName,
	state volrep.ReplicationState, log logr.Logger) (bool, error) {
	const available = true

	volRep := &volrep.VolumeReplication{}

	err := v.reconciler.Get(v.ctx, vrNamespacedName, volRep)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			log.Error(err, "Failed to get VolumeReplication resource", "resource", vrNamespacedName)

			// Failed to get VolRep and error is not IsNotFound. It is not
			// clear if the associated VolRep exists or not. If exists, then
			// is it replicating or not. So, mark the protected pvc as error
			// with condition.status as Unknown.
			msg := "Failed to get VolumeReplication resource"
			v.updatePVCDataReadyCondition(vrNamespacedName.Name, VRGConditionReasonErrorUnknown, msg)

			return !available, fmt.Errorf("failed to get VolumeReplication resource"+
				" (%s/%s) belonging to VolumeReplicationGroup (%s/%s), %w",
				vrNamespacedName.Namespace, vrNamespacedName.Name, v.instance.Namespace, v.instance.Name, err)
		}

		// Create VR for PVC
		if err = v.createVR(vrNamespacedName, state); err != nil {
			log.Error(err, "Failed to create VolumeReplication resource", "resource", vrNamespacedName)
			rmnutil.ReportIfNotPresent(v.reconciler.eventRecorder, v.instance, corev1.EventTypeWarning,
				rmnutil.EventReasonVRCreateFailed, err.Error())

			msg := "Failed to create VolumeReplication resource"
			v.updatePVCDataReadyCondition(vrNamespacedName.Name, VRGConditionReasonError, msg)

			return !available, fmt.Errorf("failed to create VolumeReplication resource"+
				" (%s/%s) belonging to VolumeReplicationGroup (%s/%s), %w",
				vrNamespacedName.Namespace, vrNamespacedName.Name, v.instance.Namespace, v.instance.Name, err)
		}

		// Just created VolRep. Mark status.conditions as Progressing.
		msg := "Created VolumeReplication resource for PVC"
		v.updatePVCDataReadyCondition(vrNamespacedName.Name, VRGConditionReasonProgressing, msg)

		return !available, nil
	}

	return v.updateVR(volRep, state, log)
}

func (v *VRGInstance) updateVR(volRep *volrep.VolumeReplication,
	state volrep.ReplicationState, log logr.Logger) (bool, error) {
	const available = true

	// If state is already as desired, check the status
	if volRep.Spec.ReplicationState == state {
		log.Info("VolumeReplication and VolumeReplicationGroup state match. Proceeding to status check")

		return v.checkVRStatus(volRep)
	}

	volRep.Spec.ReplicationState = state
	if err := v.reconciler.Update(v.ctx, volRep); err != nil {
		log.Error(err, "Failed to update VolumeReplication resource",
			"name", volRep.Name, "namespace", volRep.Namespace,
			"state", state)
		rmnutil.ReportIfNotPresent(v.reconciler.eventRecorder, v.instance, corev1.EventTypeWarning,
			rmnutil.EventReasonVRUpdateFailed, err.Error())

		msg := "Failed to update VolumeReplication resource"
		v.updatePVCDataReadyCondition(volRep.Name, VRGConditionReasonError, msg)

		return !available, fmt.Errorf("failed to update VolumeReplication resource"+
			" (%s/%s) as %s, belonging to VolumeReplicationGroup (%s/%s), %w",
			volRep.Namespace, volRep.Name, state,
			v.instance.Namespace, v.instance.Name, err)
	}

	log.Info(fmt.Sprintf("Updated VolumeReplication resource (%s/%s) with state %s",
		volRep.Name, volRep.Namespace, state))
	// Just updated the state of the VolRep. Mark it as progressing.
	msg := "Updated VolumeReplication resource for PVC"
	v.updatePVCDataReadyCondition(volRep.Name, VRGConditionReasonProgressing, msg)

	return !available, nil
}

// createVR creates a VolumeReplication CR with a PVC as its data source.
func (v *VRGInstance) createVR(vrNamespacedName types.NamespacedName, state volrep.ReplicationState) error {
	volumeReplicationClass, err := v.selectVolumeReplicationClass(vrNamespacedName)
	if err != nil {
		return fmt.Errorf("failed to find the appropriate VolumeReplicationClass (%s) %w",
			v.instance.Name, err)
	}

	volRep := &volrep.VolumeReplication{
		ObjectMeta: metav1.ObjectMeta{
			Name:      vrNamespacedName.Name,
			Namespace: vrNamespacedName.Namespace,
		},
		Spec: volrep.VolumeReplicationSpec{
			DataSource: corev1.TypedLocalObjectReference{
				Kind:     "PersistentVolumeClaim",
				Name:     vrNamespacedName.Name,
				APIGroup: new(string),
			},
			ReplicationState:       state,
			VolumeReplicationClass: volumeReplicationClass,
		},
	}

	// Let VRG receive notification for any changes to VolumeReplication CR
	// created by VRG.
	if err := ctrl.SetControllerReference(v.instance, volRep, v.reconciler.Scheme); err != nil {
		return fmt.Errorf("failed to set owner reference to VolumeReplication resource (%s/%s), %w",
			volRep.Name, volRep.Namespace, err)
	}

	v.log.Info("Creating VolumeReplication resource", "resource", volRep)

	if err := v.reconciler.Create(v.ctx, volRep); err != nil {
		return fmt.Errorf("failed to create VolumeReplication resource (%s), %w", vrNamespacedName, err)
	}

	return nil
}

// namespacedName applies to both VolumeReplication resource and pvc as of now.
// This is because, VolumeReplication resource for a pvc that is created by the
// VolumeReplicationGroup has the same name as pvc. But in future if it changes
// functions to be changed would be processVRAsPrimary(), processVRAsSecondary()
// to either receive pvc NamespacedName or pvc itself as an additional argument.
func (v *VRGInstance) selectVolumeReplicationClass(namespacedName types.NamespacedName) (string, error) {
	className := ""

	if !v.vrcUpdated {
		if err := v.updateReplicationClassList(); err != nil {
			v.log.Error(err, "Failed to get VolumeReplicationClass list")

			return className, fmt.Errorf("failed to get VolumeReplicationClass list")
		}

		v.vrcUpdated = true
	}

	if len(v.replClassList.Items) == 0 {
		v.log.Info("No VolumeReplicationClass available")

		return className, fmt.Errorf("no VolumeReplicationClass available")
	}

	storageClass, err := v.getStorageClass(namespacedName)
	if err != nil {
		v.log.Info(fmt.Sprintf("Failed to get the storageclass of pvc %s",
			namespacedName))

		return className, fmt.Errorf("failed to get the storageclass of pvc %s (%w)",
			namespacedName, err)
	}

	for index := range v.replClassList.Items {
		replicationClass := &v.replClassList.Items[index]
		if storageClass.Provisioner != replicationClass.Spec.Provisioner {
			continue
		}

		schedulingInterval, found := replicationClass.Spec.Parameters["schedulingInterval"]
		if !found {
			// schedule not present in parameters of this replicationClass.
			continue
		}

		// ReplicationClass that matches both VRG schedule and pvc provisioner
		if schedulingInterval == v.instance.Spec.Async.SchedulingInterval {
			className = replicationClass.Name

			break
		}
	}

	if className == "" {
		v.log.Info(fmt.Sprintf("No VolumeReplicationClass found to match provisioner and schedule %s/%s",
			storageClass.Provisioner, v.instance.Spec.Async.SchedulingInterval))

		return className, fmt.Errorf("no VolumeReplicationClass found to match provisioner and schedule")
	}

	v.log.Info(fmt.Sprintf("Found VolumeReplicationClass that matches provisioner and schedule %s/%s",
		storageClass.Provisioner, v.instance.Spec.Async.SchedulingInterval))

	return className, nil
}

// if the fetched SCs are stashed, fetching it again for the next PVC can be avoided
// saving a call to the API server
func (v *VRGInstance) getStorageClass(namespacedName types.NamespacedName) (*storagev1.StorageClass, error) {
	var pvc *corev1.PersistentVolumeClaim

	for idx := range v.volRepPVCs {
		pvcItem := &v.volRepPVCs[idx]

		pvcNamespacedName := types.NamespacedName{Name: pvcItem.Name, Namespace: pvcItem.Namespace}
		if pvcNamespacedName == namespacedName {
			pvc = pvcItem

			break
		}
	}

	if pvc == nil {
		v.log.Info(fmt.Sprintf("failed to get the pvc with namespaced name (%s)", namespacedName))

		// Need the storage driver of pvc. If pvc is not found return error.
		return nil, fmt.Errorf("failed to get the pvc with namespaced name %s", namespacedName)
	}

	scName := pvc.Spec.StorageClassName

	storageClass := &storagev1.StorageClass{}
	if err := v.reconciler.Get(v.ctx, types.NamespacedName{Name: *scName}, storageClass); err != nil {
		v.log.Info(fmt.Sprintf("Failed to get the storageclass %s", *scName))

		return nil, fmt.Errorf("failed to get the storageclass with name %s (%w)",
			*scName, err)
	}

	return storageClass, nil
}

func (v *VRGInstance) checkVRStatus(volRep *volrep.VolumeReplication) (bool, error) {
	const available = true

	// When the generation in the status is updated, VRG would get a reconcile
	// as it owns VolumeReplication resource.
	if volRep.Generation != volRep.Status.ObservedGeneration {
		v.log.Info(fmt.Sprintf("Generation mismatch in status for VolumeReplication resource (%s/%s)",
			volRep.Name, volRep.Namespace))

		msg := "VolumeReplication generation not updated in status"
		v.updatePVCDataReadyCondition(volRep.Name, VRGConditionReasonProgressing, msg)

		return !available, nil
	}

	switch {
	case v.instance.Spec.ReplicationState == ramendrv1alpha1.Primary:
		return v.validateVRStatus(volRep, ramendrv1alpha1.Primary), nil
	case v.instance.Spec.ReplicationState == ramendrv1alpha1.Secondary:
		return v.validateVRStatus(volRep, ramendrv1alpha1.Secondary), nil
	default:
		msg := "VolumeReplicationGroup state invalid"
		v.updatePVCDataReadyCondition(volRep.Name, VRGConditionReasonError, msg)

		return !available, fmt.Errorf("invalid Replication State %s for VolumeReplicationGroup (%s:%s)",
			string(v.instance.Spec.ReplicationState), v.instance.Name, v.instance.Namespace)
	}
}

// validateVRStatus validates if the VolumeReplication resource has the desired status for the
// current generation.
// - When replication state is Primary, only Completed condition is checked.
// - When replication state is Secondary, all 3 conditions for Completed/Degraded/Resyncing is
//   checked and ensured healthy.
func (v *VRGInstance) validateVRStatus(volRep *volrep.VolumeReplication, state ramendrv1alpha1.ReplicationState) bool {
	var (
		stateString string
		action      string
	)

	const available = true

	switch state {
	case ramendrv1alpha1.Primary:
		stateString = "primary"
		action = "promoted"
	case ramendrv1alpha1.Secondary:
		stateString = "secondary"
		action = "demoted"
	}

	// it should be completed
	conditionMet, msg := isVRConditionMet(volRep, volrepController.ConditionCompleted, metav1.ConditionTrue)
	if !conditionMet {
		defaultMsg := fmt.Sprintf("VolumeReplication resource for pvc not %s to %s", action, stateString)
		v.updatePVCDataReadyConditionHelper(volRep.Name, VRGConditionReasonError, msg,
			defaultMsg)

		v.updatePVCDataProtectedConditionHelper(volRep.Name, VRGConditionReasonError, msg,
			defaultMsg)

		v.log.Info(fmt.Sprintf("%s (VolRep: %s/%s)", defaultMsg, volRep.Name, volRep.Namespace))

		return !available
	}

	// if primary, all checks are completed
	if state == ramendrv1alpha1.Primary {
		msg = "PVC in the VolumeReplicationGroup is ready for use"
		v.updatePVCDataReadyCondition(volRep.Name, VRGConditionReasonReady, msg)

		v.updatePVCDataProtectedCondition(volRep.Name, VRGConditionReasonReady, msg)

		v.log.Info(fmt.Sprintf("VolumeReplication resource %s/%s is ready for use", volRep.Name,
			volRep.Namespace))

		return available
	}

	return v.validateAdditionalVRStatusForSecondary(volRep)
}

// Return available if resync is happening as secondary or resync is complete as secondary.
// i.e. For VolRep the following conditions should be met
// 1) Data Sync is happening
//    VolRep.Status.Conditions[Degraded].Status = True &&
//    VolRep.Status.Conditions[Resyncing].Status = True
// 2) Data Sync is complete.
//    VolRep.Status.Conditions[Degraded].Status = False &&
//    VolRep.Status.Conditions[Resyncing].Status = False
//
// With 1st condition being met,
// ProtectedPVC.Conditions[DataReady] = True
// ProtectedPVC.Conditions[DataProtected] = False
//
// With 2nd condition being met,
// ProtectedPVC.Conditions[DataReady] = True
// ProtectedPVC.Conditions[DataProtected] = True
func (v *VRGInstance) validateAdditionalVRStatusForSecondary(volRep *volrep.VolumeReplication) bool {
	const available = true

	conditionMet, _ := isVRConditionMet(volRep, volrepController.ConditionResyncing, metav1.ConditionTrue)
	if !conditionMet {
		return v.checkResyncCompletionAsSecondary(volRep)
	}

	conditionMet, msg := isVRConditionMet(volRep, volrepController.ConditionDegraded, metav1.ConditionTrue)
	if !conditionMet {
		v.updatePVCDataProtectedConditionHelper(volRep.Name, VRGConditionReasonError, msg,
			"VolumeReplication resource for pvc is not in Degraded condition while resyncing")

		v.updatePVCDataReadyConditionHelper(volRep.Name, VRGConditionReasonError, msg,
			"VolumeReplication resource for pvc is not in Degraded condition while resyncing")

		v.log.Info(fmt.Sprintf("VolumeReplication resource is not in degraded condition while"+
			" resyncing is true (%s/%s)", volRep.Name, volRep.Namespace))

		return !available
	}

	msg = "VolumeReplication resource for the pvc is syncing as Secondary"
	v.updatePVCDataReadyCondition(volRep.Name, VRGConditionReasonReplicating, msg)
	v.updatePVCDataProtectedCondition(volRep.Name, VRGConditionReasonReplicating, msg)

	v.log.Info(fmt.Sprintf("VolumeReplication resource for the pvc is syncing as Secondary (%s/%s)",
		volRep.Name, volRep.Namespace))

	return available
}

func (v *VRGInstance) checkResyncCompletionAsSecondary(volRep *volrep.VolumeReplication) bool {
	const available = true

	conditionMet, msg := isVRConditionMet(volRep, volrepController.ConditionResyncing, metav1.ConditionFalse)
	if !conditionMet {
		defaultMsg := "VolumeReplication resource for pvc not syncing as Secondary"
		v.updatePVCDataReadyConditionHelper(volRep.Name, VRGConditionReasonError, msg,
			defaultMsg)

		v.updatePVCDataProtectedConditionHelper(volRep.Name, VRGConditionReasonError, msg,
			defaultMsg)

		v.log.Info(fmt.Sprintf("%s (VolRep: %s/%s)", defaultMsg, volRep.Name, volRep.Namespace))

		return !available
	}

	conditionMet, msg = isVRConditionMet(volRep, volrepController.ConditionDegraded, metav1.ConditionFalse)
	if !conditionMet {
		defaultMsg := "VolumeReplication resource for pvc is not syncing and is degraded as Secondary"
		v.updatePVCDataReadyConditionHelper(volRep.Name, VRGConditionReasonError, msg,
			defaultMsg)

		v.updatePVCDataProtectedConditionHelper(volRep.Name, VRGConditionReasonError, msg,
			defaultMsg)

		v.log.Info(fmt.Sprintf("%s (VolRep: %s/%s)", defaultMsg, volRep.Name, volRep.Namespace))

		return !available
	}

	msg = "VolumeReplication resource for the pvc as Secondary is in sync with Primary"
	v.updatePVCDataReadyCondition(volRep.Name, VRGConditionReasonReplicated, msg)
	v.updatePVCDataProtectedCondition(volRep.Name, VRGConditionReasonDataProtected, msg)

	v.log.Info(fmt.Sprintf("data sync completed as both degraded and resyncing are false for"+
		" secondary VolRep (%s/%s)", volRep.Name, volRep.Namespace))

	return available
}

func isVRConditionMet(volRep *volrep.VolumeReplication,
	conditionType string,
	desiredStatus metav1.ConditionStatus) (bool, string) {
	volRepCondition := findCondition(volRep.Status.Conditions, conditionType)
	if volRepCondition == nil {
		msg := fmt.Sprintf("Failed to get the %s condition from status of VolumeReplication resource.", conditionType)

		return false, msg
	}

	if volRep.Generation != volRepCondition.ObservedGeneration {
		msg := fmt.Sprintf("Stale generation for condition %s from status of VolumeReplication resource.", conditionType)

		return false, msg
	}

	if volRepCondition.Status == metav1.ConditionUnknown {
		msg := fmt.Sprintf("Unknown status for condition %s from status of VolumeReplication resource.", conditionType)

		return false, msg
	}

	if volRepCondition.Status != desiredStatus {
		return false, ""
	}

	return true, ""
}

// nolint: unparam
// Disabling unparam linter as currently every invokation of this
// function sends reason as VRGConditionReasonError and the linter
// complains about this function always receiving the same reason.
func (v *VRGInstance) updatePVCDataReadyConditionHelper(name, reason, message, defaultMessage string) {
	if message != "" {
		v.updatePVCDataReadyCondition(name, reason, message)

		return
	}

	v.updatePVCDataReadyCondition(name, reason, defaultMessage)
}

func (v *VRGInstance) updatePVCDataReadyCondition(pvcName, reason, message string) {
	if protectedPVC := v.findProtectedPVC(pvcName); protectedPVC != nil {
		setPVCDataReadyCondition(protectedPVC, reason, message, v.instance.Generation)
		// No need to append it as an already existing entry from the list is being modified.
		return
	}

	protectedPVC := &ramendrv1alpha1.ProtectedPVC{Name: pvcName}
	setPVCDataReadyCondition(protectedPVC, reason, message, v.instance.Generation)

	// created a new instance. Add it to the list
	v.instance.Status.ProtectedPVCs = append(v.instance.Status.ProtectedPVCs, *protectedPVC)
}

// nolint: unparam
// Disabling unparam linter as currently every invokation of this
// function sends reason as VRGConditionReasonError and the linter
// complains about this function always receiving the same reason.
func (v *VRGInstance) updatePVCDataProtectedConditionHelper(name, reason, message, defaultMessage string) {
	if message != "" {
		v.updatePVCDataProtectedCondition(name, reason, message)

		return
	}

	v.updatePVCDataProtectedCondition(name, reason, defaultMessage)
}

func (v *VRGInstance) updatePVCDataProtectedCondition(pvcName, reason, message string) {
	if protectedPVC := v.findProtectedPVC(pvcName); protectedPVC != nil {
		setPVCDataProtectedCondition(protectedPVC, reason, message, v.instance.Generation)
		// No need to append it as an already existing entry from the list is being modified.
		return
	}

	protectedPVC := &ramendrv1alpha1.ProtectedPVC{Name: pvcName}
	setPVCDataProtectedCondition(protectedPVC, reason, message, v.instance.Generation)

	// created a new instance. Add it to the list
	v.instance.Status.ProtectedPVCs = append(v.instance.Status.ProtectedPVCs, *protectedPVC)
}

func setPVCDataReadyCondition(protectedPVC *ramendrv1alpha1.ProtectedPVC, reason, message string,
	observedGeneration int64) {
	switch {
	case reason == VRGConditionReasonError:
		setVRGDataErrorCondition(&protectedPVC.Conditions, observedGeneration, message)
	case reason == VRGConditionReasonReplicating:
		setVRGDataReplicatingCondition(&protectedPVC.Conditions, observedGeneration, message)
	case reason == VRGConditionReasonReplicated:
		setVRGDataReplicatedCondition(&protectedPVC.Conditions, observedGeneration, message)
	case reason == VRGConditionReasonReady:
		setVRGAsPrimaryReadyCondition(&protectedPVC.Conditions, observedGeneration, message)
	case reason == VRGConditionReasonProgressing:
		setVRGDataProgressingCondition(&protectedPVC.Conditions, observedGeneration, message)
	case reason == VRGConditionReasonErrorUnknown:
		setVRGDataErrorUnknownCondition(&protectedPVC.Conditions, observedGeneration, message)
	default:
		// if appropriate reason is not provided, then treat it as an unknown condition.
		message = "Unknown reason: " + reason
		setVRGDataErrorCondition(&protectedPVC.Conditions, observedGeneration, message)
	}
}

func setPVCDataProtectedCondition(protectedPVC *ramendrv1alpha1.ProtectedPVC, reason, message string,
	observedGeneration int64) {
	switch {
	case reason == VRGConditionReasonError:
		setVRGAsDataNotProtectedCondition(&protectedPVC.Conditions, observedGeneration, message)

	// When VRG = Secondary && VolRep's Degraded = True && Resyncing = True
	case reason == VRGConditionReasonReplicating:
		setVRGDataProtectionProgressCondition(&protectedPVC.Conditions, observedGeneration, message)

	// When VRG = Primary
	case reason == VRGConditionReasonReady:
		setVRGDataProtectionProgressCondition(&protectedPVC.Conditions, observedGeneration, message)

	// When VRG = Secondary && VolRep's Degraded = False && Resyncing = False
	case reason == VRGConditionReasonDataProtected:
		setVRGAsDataProtectedCondition(&protectedPVC.Conditions, observedGeneration, message)
	case reason == VRGConditionReasonProgressing:
		setVRGAsDataNotProtectedCondition(&protectedPVC.Conditions, observedGeneration, message)
	case reason == VRGConditionReasonErrorUnknown:
		setVRGDataErrorUnknownCondition(&protectedPVC.Conditions, observedGeneration, message)
	default:
		// if appropriate reason is not provided, then treat it as an unknown condition.
		message = "Unknown reason: " + reason
		setVRGDataErrorUnknownCondition(&protectedPVC.Conditions, observedGeneration, message)
	}
}

func (v *VRGInstance) updatePVCClusterDataProtectedCondition(pvcName, reason, message string) {
	if protectedPVC := v.findProtectedPVC(pvcName); protectedPVC != nil {
		setPVCClusterDataProtectedCondition(protectedPVC, reason, message, v.instance.Generation)
		// No need to append it as an already existing entry from the list is being modified.
		return
	}

	protectedPVC := &ramendrv1alpha1.ProtectedPVC{Name: pvcName}
	setPVCClusterDataProtectedCondition(protectedPVC, reason, message, v.instance.Generation)
	v.instance.Status.ProtectedPVCs = append(v.instance.Status.ProtectedPVCs, *protectedPVC)
}

func setPVCClusterDataProtectedCondition(protectedPVC *ramendrv1alpha1.ProtectedPVC, reason, message string,
	observedGeneration int64) {
	switch {
	case reason == VRGConditionReasonUploaded:
		setVRGClusterDataProtectedCondition(&protectedPVC.Conditions, observedGeneration, message)
	case reason == VRGConditionReasonUploading:
		setVRGClusterDataProtectingCondition(&protectedPVC.Conditions, observedGeneration, message)
	case reason == VRGConditionReasonUploadError:
		setVRGClusterDataUnprotectedCondition(&protectedPVC.Conditions, observedGeneration, message)
	default:
		// if appropriate reason is not provided, then treat it as an unknown condition.
		message = "Unknown reason: " + reason
		setVRGDataErrorUnknownCondition(&protectedPVC.Conditions, observedGeneration, message)
	}
}

// deleteVR deletes a VolumeReplication instance if found
func (v *VRGInstance) deleteVR(vrNamespacedName types.NamespacedName, log logr.Logger) error {
	cr := &volrep.VolumeReplication{
		ObjectMeta: metav1.ObjectMeta{
			Name:      vrNamespacedName.Name,
			Namespace: vrNamespacedName.Namespace,
		},
	}

	err := v.reconciler.Delete(v.ctx, cr)
	if err == nil || k8serrors.IsNotFound(err) {
		return nil
	}

	log.Error(err, "Failed to delete VolumeReplication resource")

	return fmt.Errorf("failed to delete VolumeReplication resource (%s/%s), %w",
		vrNamespacedName.Namespace, vrNamespacedName.Name, err)
}

func (v *VRGInstance) addProtectedAnnotationForPVC(pvc *corev1.PersistentVolumeClaim, log logr.Logger) error {
	if pvc.ObjectMeta.Annotations == nil {
		pvc.ObjectMeta.Annotations = map[string]string{}
	}

	pvc.ObjectMeta.Annotations[pvcVRAnnotationProtectedKey] = pvcVRAnnotationProtectedValue

	if err := v.reconciler.Update(v.ctx, pvc); err != nil {
		// TODO: Should we set the PVC condition to error?
		// msg := "Failed to add protected annotatation to PVC"
		// v.updateProtectedPVCCondition(pvc.Name, PVCError, msg)
		log.Error(err, "Failed to update PersistentVolumeClaim annotation")

		return fmt.Errorf("failed to update PersistentVolumeClaim (%s/%s) annotation (%s) belonging to"+
			"VolumeReplicationGroup (%s/%s), %w",
			pvc.Namespace, pvc.Name, pvcVRAnnotationProtectedKey, v.instance.Namespace, v.instance.Name, err)
	}

	return nil
}

// addFinalizer adds a finalizer to VRG, to act as deletion protection
func (v *VRGInstance) addFinalizer(finalizer string) error {
	if !containsString(v.instance.ObjectMeta.Finalizers, finalizer) {
		v.instance.ObjectMeta.Finalizers = append(v.instance.ObjectMeta.Finalizers, finalizer)
		if err := v.reconciler.Update(v.ctx, v.instance); err != nil {
			v.log.Error(err, "Failed to add finalizer", "finalizer", finalizer)

			return fmt.Errorf("failed to add finalizer to VolumeReplicationGroup resource (%s/%s), %w",
				v.instance.Namespace, v.instance.Name, err)
		}
	}

	return nil
}

func (v *VRGInstance) addProtectedFinalizerToPVC(pvc *corev1.PersistentVolumeClaim,
	log logr.Logger) error {
	if containsString(pvc.Finalizers, pvcVRFinalizerProtected) {
		return nil
	}

	return v.addFinalizerToPVC(pvc, pvcVRFinalizerProtected, log)
}

func (v *VRGInstance) addFinalizerToPVC(pvc *corev1.PersistentVolumeClaim,
	finalizer string,
	log logr.Logger) error {
	if !containsString(pvc.ObjectMeta.Finalizers, finalizer) {
		pvc.ObjectMeta.Finalizers = append(pvc.ObjectMeta.Finalizers, finalizer)
		if err := v.reconciler.Update(v.ctx, pvc); err != nil {
			log.Error(err, "Failed to add finalizer", "finalizer", finalizer)

			return fmt.Errorf("failed to add finalizer (%s) to PersistentVolumeClaim resource"+
				" (%s/%s) belonging to VolumeReplicationGroup (%s/%s), %w",
				finalizer, pvc.Namespace, pvc.Name, v.instance.Namespace, v.instance.Name, err)
		}
	}

	return nil
}

func (v *VRGInstance) removeProtectedFinalizerFromPVC(pvc *corev1.PersistentVolumeClaim,
	log logr.Logger) error {
	return v.removeFinalizerFromPVC(pvc, pvcVRFinalizerProtected, log)
}

// removeFinalizerFromPVC removes the VR finalizer on PVC and also the protected annotation from the PVC
func (v *VRGInstance) removeFinalizerFromPVC(pvc *corev1.PersistentVolumeClaim,
	finalizer string,
	log logr.Logger) error {
	if containsString(pvc.ObjectMeta.Finalizers, finalizer) {
		pvc.ObjectMeta.Finalizers = removeString(pvc.ObjectMeta.Finalizers, finalizer)
		delete(pvc.ObjectMeta.Annotations, pvcVRAnnotationProtectedKey)

		if err := v.reconciler.Update(v.ctx, pvc); err != nil {
			log.Error(err, "Failed to remove finalizer", "finalizer", finalizer)

			return fmt.Errorf("failed to remove finalizer (%s) from PersistentVolumeClaim resource"+
				" (%s/%s) detected as part of VolumeReplicationGroup (%s/%s), %w",
				finalizer, pvc.Namespace, pvc.Name, v.instance.Namespace, v.instance.Name, err)
		}
	}

	return nil
}

// findProtectedPVC returns the &VRG.Status.ProtectedPVC[x] for the given pvcName
func (v *VRGInstance) findProtectedPVC(pvcName string) *ramendrv1alpha1.ProtectedPVC {
	for index := range v.instance.Status.ProtectedPVCs {
		protectedPVC := &v.instance.Status.ProtectedPVCs[index]
		if protectedPVC.Name == pvcName {
			return protectedPVC
		}
	}

	return nil
}

// s3KeyPrefix returns the S3 key prefix of cluster data of this VRG.
func (v *VRGInstance) s3KeyPrefix() string {
	return v.namespacedName + "/"
}

func (v *VRGInstance) restorePVsForVolRep() error {
	v.log.Info("Restoring VolRep PVs")

	if len(v.instance.Spec.S3Profiles) == 0 {
		v.log.Info("No S3 profiles configured")

		return fmt.Errorf("no S3Profiles configured")
	}

	v.log.Info(fmt.Sprintf("Restoring PVs to this managed cluster. ProfileList: %v", v.instance.Spec.S3Profiles))

	success, err := v.fetchAndRestorePV()

	if !success {
		errMsg := fmt.Sprintf("failed to restorePVs using profile list (%v)", v.instance.Spec.S3Profiles)
		v.log.Info(errMsg)

		return fmt.Errorf("%s: %w", errMsg, err)
	}

	return nil
}

func (v *VRGInstance) fetchAndRestorePV() (bool, error) {
	var (
		success = false
		err     error
		NoS3    = false
	)

	for _, s3ProfileName := range v.instance.Spec.S3Profiles {
		var pvList []corev1.PersistentVolume

		if s3ProfileName == NoS3StoreAvailable {
			v.log.Info("NoS3 available to fetch")

			NoS3 = true

			continue
		}

		pvList, err = v.fetchPVClusterDataFromS3Store(s3ProfileName)
		if err != nil {
			v.log.Error(err, fmt.Sprintf("error fetching PV cluster data from S3 profile %s", s3ProfileName))

			continue
		}

		v.log.Info(fmt.Sprintf("Found %d PVs in s3 store whose profile name is %s",
			len(pvList), s3ProfileName))

		err = v.sanityCheckPVClusterData(pvList)
		if err != nil {
			errMsg := fmt.Sprintf("error found during sanity check of PV cluster data in S3 store %s", s3ProfileName)
			v.log.Info(errMsg)
			v.log.Error(err, fmt.Sprintf("Resolve PV conflict in the S3 store %s to deploy the application", s3ProfileName))

			return success, fmt.Errorf("%s: %w", errMsg, err)
		}

		err = v.restorePVClusterData(pvList)
		if err != nil {
			success = false
			// go to the next profile
			continue
		}

		v.log.Info(fmt.Sprintf("Restored %d PVs using profile %s", len(pvList), s3ProfileName))

		success = true

		break
	}

	if NoS3 {
		return true, nil
	}

	return success, err
}

func (v *VRGInstance) fetchPVClusterDataFromS3Store(s3ProfileName string) ([]corev1.PersistentVolume, error) {
	s3KeyPrefix := v.s3KeyPrefix()

	objectStore, err := v.getObjectStorer(s3ProfileName)
	if err != nil {
		return nil, fmt.Errorf("error when downloading PVs, err %w", err)
	}

	return downloadPVs(objectStore, s3KeyPrefix)
}

// sanityCheckPVClusterData returns an error if there are PVs in the input
// pvList that have conflicting claimRefs that point to the same PVC name but
// different PVC UID.
//
// Under normal circumstances, each PV in the S3 store will point to a unique
// PVC and the sanity check will succeed.  In the case of failover related
// split-brain error scenarios, there can be multiple clusters that concurrently
// have the same VRG in primary state.  During the split-brain scenario, if the
// VRG is configured to use the same S3 store for both download and upload of
// cluster data and, if the application added a new PVC to the application on
// each cluster after failover, the S3 store could end up with multiple PVs for
// the same PVC because each of the clusters uploaded its unique PV to the S3
// store, thus resulting in ambiguous PVs for the same PVC.  If the S3 store
// ends up in such a situation, Ramen cannot determine with certainty which PV
// among the conflicting PVs should be restored to the cluster, and thus fails
// the sanity check.
func (v *VRGInstance) sanityCheckPVClusterData(pvList []corev1.PersistentVolume) error {
	pvMap := map[string]corev1.PersistentVolume{}
	// Scan the PVs and create a map of PVs that have conflicting claimRefs
	for _, thisPV := range pvList {
		claimRef := thisPV.Spec.ClaimRef
		claimKey := fmt.Sprintf("%s/%s", claimRef.Namespace, claimRef.Name)

		prevPV, found := pvMap[claimKey]
		if !found {
			pvMap[claimKey] = thisPV

			continue
		}

		msg := fmt.Sprintf("when restoring PV cluster data, detected conflicting claimKey %s in PVs %s and %s",
			claimKey, prevPV.Name, thisPV.Name)
		v.log.Info(msg)

		return fmt.Errorf(msg)
	}

	return nil
}

func (v *VRGInstance) restorePVClusterData(pvList []corev1.PersistentVolume) error {
	numRestored := 0

	for idx := range pvList {
		pv := &pvList[idx]
		v.cleanupPVForRestore(pv)
		v.addPVRestoreAnnotation(pv)

		if err := v.reconciler.Create(v.ctx, pv); err != nil {
			if k8serrors.IsAlreadyExists(err) {
				err := v.validatePVExistence(pv)
				if err != nil {
					v.log.Info("PV exists. Ignoring and moving to next PV", "error", err.Error())
					// ignoring any errors
					continue
				}

				// Valid PV exists and it is managed by Ramen
				numRestored++

				continue
			}

			v.log.Info("Failed to restore PV", "name", pv.Name, "Error", err)

			continue
		}

		numRestored++
	}

	if numRestored != len(pvList) {
		return fmt.Errorf("failed to restore all PVs. Total %d. Restored %d", len(pvList), numRestored)
	}

	v.log.Info("Success restoring VolRep PVs", "Total", numRestored)

	return nil
}

func (v *VRGInstance) updateExistingPVForSync(pv *corev1.PersistentVolume) error {
	// In case of sync mode, the pv is never deleted as part of the
	// failover/relocate process. Hence, the restore may not be
	// required and the annotation for restore can be missing for
	// the sync mode.
	v.cleanupPVForRestore(pv)
	v.addPVRestoreAnnotation(pv)

	if err := v.reconciler.Update(v.ctx, pv); err != nil {
		return fmt.Errorf("failed to cleanup existing PV for sync DR PV: %v, err: %w", pv.Name, err)
	}

	return nil
}

func (v *VRGInstance) validatePVExistence(pv *corev1.PersistentVolume) error {
	existingPV := &corev1.PersistentVolume{}

	err := v.reconciler.Get(v.ctx, types.NamespacedName{Name: pv.Name}, existingPV)
	if err != nil {
		return fmt.Errorf("failed to get existing PV (%w)", err)
	}

	if existingPV.ObjectMeta.Annotations != nil &&
		existingPV.ObjectMeta.Annotations[PVRestoreAnnotation] == "True" {
		// Should we check and see if PV in being deleted? Should we just treat it as exists
		// and then we don't care if deletion takes place later, which is what we do now?
		v.log.Info("PV exists and managed by Ramen", "PV", existingPV)

		return nil
	}

	// Right now, we can only have either Sync mode or Async mode. In case of
	// sync mode, the pv is never deleted as part of the failover/relocate
	// process. Hence, the restore is not required and the annotation for
	// restore can be missing for the sync mode. Skip the check for the
	// annotation in this case.
	if v.instance.Spec.Sync.Mode == ramendrv1alpha1.SyncModeEnabled {
		v.log.Info("PV exists, will update for sync", "PV", existingPV)

		return v.updateExistingPVForSync(existingPV)
	}

	return fmt.Errorf("found PV object not restored by Ramen for PV %s", existingPV.Name)
}

// cleanupPVForRestore cleans up required PV fields, to ensure restore succeeds to a new cluster, and
// rebinding the PV to a newly created PVC with the same claimRef succeeds
func (v *VRGInstance) cleanupPVForRestore(pv *corev1.PersistentVolume) {
	pv.ResourceVersion = ""
	if pv.Spec.ClaimRef != nil {
		pv.Spec.ClaimRef.UID = ""
		pv.Spec.ClaimRef.ResourceVersion = ""
		pv.Spec.ClaimRef.APIVersion = ""
	}
}

// addPVRestoreAnnotation adds annotation to the PV indicating that the PV is restored by Ramen
func (v *VRGInstance) addPVRestoreAnnotation(pv *corev1.PersistentVolume) {
	if pv.ObjectMeta.Annotations == nil {
		pv.ObjectMeta.Annotations = map[string]string{}
	}

	pv.ObjectMeta.Annotations[PVRestoreAnnotation] = "True"
}

//
// Follow this logic to update VRG (and also ProtectedPVC) conditions for VolRep
// while reconciling VolumeReplicationGroup resource.
//
// For both Primary and Secondary:
// if getting VolRep fails and volrep does not exist:
//    ProtectedPVC.conditions.Available.Status = False
//    ProtectedPVC.conditions.Available.Reason = Progressing
//    return
// if getting VolRep fails and some other error:
//    ProtectedPVC.conditions.Available.Status = Unknown
//    ProtectedPVC.conditions.Available.Reason = Error
//
// This below if condition check helps in undersanding whether
// promotion/demotion has been successfully completed or not.
// if VolRep.Status.Conditions[Completed].Status == True
//    ProtectedPVC.conditions.Available.Status = True
//    ProtectedPVC.conditions.Available.Reason = Replicating
// else
//    ProtectedPVC.conditions.Available.Status = False
//    ProtectedPVC.conditions.Available.Reason = Error
//
// if all ProtectedPVCs are Replicating, then
//    VRG.conditions.Available.Status = true
//    VRG.conditions.Available.Reason = Replicating
// if atleast one ProtectedPVC.conditions[Available].Reason == Error
//    VRG.conditions.Available.Status = false
//    VRG.conditions.Available.Reason = Error
// if no ProtectedPVCs is in error and atleast one is progressing, then
//    VRG.conditions.Available.Status = false
//    VRG.conditions.Available.Reason = Progressing
//
//nolint:funlen
func (v *VRGInstance) aggregateVolRepDataReadyCondition() {
	vrgReady := len(v.instance.Status.ProtectedPVCs) != 0
	vrgProgressing := false

	for _, protectedPVC := range v.instance.Status.ProtectedPVCs {
		if protectedPVC.ProtectedByVolSync {
			continue
		}

		condition := findCondition(protectedPVC.Conditions, VRGConditionTypeDataReady)

		v.log.Info("Condition for DataReady", "cond", condition, "protectedPVC", protectedPVC)

		if condition == nil {
			vrgReady = false
			// When will we hit this condition? If it is due to a race condition,
			// why treat it as an error instead of progressing?
			v.log.Info(fmt.Sprintf("Failed to find condition %s for vrg %s/%s", VRGConditionTypeDataReady,
				v.instance.Name, v.instance.Namespace))

			break
		}

		if condition.Reason == VRGConditionReasonProgressing {
			vrgReady = false
			vrgProgressing = true
			// Breaking out in this case may be incorrect, as another PVC could
			// have a more serious `error` condition, isn't it?
			break
		}

		if condition.Reason == VRGConditionReasonError ||
			condition.Reason == VRGConditionReasonErrorUnknown {
			vrgReady = false
			// If there is even a single protected pvc that saw an error,
			// then entire VRG should mark its condition as error. Set
			// vrgPogressing to false.
			vrgProgressing = false

			v.log.Info(fmt.Sprintf("Condition %s has error reason %s for vrg %s/%s", VRGConditionTypeDataReady,
				condition.Reason, v.instance.Name, v.instance.Namespace))

			break
		}
	}

	if vrgReady {
		v.vrgReadyStatus()

		return
	}

	if vrgProgressing {
		v.log.Info("Marking VRG not DataReady with progressing reason")

		msg := "VolumeReplicationGroup is progressing"
		setVRGDataProgressingCondition(&v.instance.Status.Conditions, v.instance.Generation, msg)

		return
	}

	// None of the VRG Ready and VRG Progressing conditions are met.
	// Set Error condition for VRG.
	v.log.Info("Marking VRG not DataReady with error. All PVCs are not ready")

	msg := "All PVCs of the VolumeReplicationGroup are not ready"
	setVRGDataErrorCondition(&v.instance.Status.Conditions, v.instance.Generation, msg)
}

//nolint:funlen,gocognit
func (v *VRGInstance) aggregateVolRepDataProtectedCondition() {
	vrgProtected := true
	vrgReplicating := false

	for _, protectedPVC := range v.instance.Status.ProtectedPVCs {
		if protectedPVC.ProtectedByVolSync {
			continue
		}

		condition := findCondition(protectedPVC.Conditions, VRGConditionTypeDataProtected)

		if condition == nil {
			vrgProtected = false
			vrgReplicating = false

			v.log.Info(fmt.Sprintf("Failed to find condition %s for vrg", VRGConditionTypeDataProtected))

			break
		}

		// VRGConditionReasonReplicating => VRG secondary, VRGConditionReasonReady => VRG Primary
		if condition.Reason == VRGConditionReasonReplicating ||
			condition.Reason == VRGConditionReasonReady {
			vrgProtected = false
			vrgReplicating = true

			continue
		}

		if condition.Reason == VRGConditionReasonError ||
			condition.Reason == VRGConditionReasonErrorUnknown {
			vrgProtected = false
			// Even a single pvc seeing error means, entire VRG marks this
			// condition as error. Set vrgReplicating to false
			vrgReplicating = false

			v.log.Info(fmt.Sprintf("Condition %s has error reason %s for vrg",
				VRGConditionTypeDataProtected, condition.Reason))

			break
		}
	}

	if vrgProtected {
		v.log.Info("Marking VRG data protected after completing replication")

		msg := "PVCs in the VolumeReplicationGroup are data protected "
		setVRGAsDataProtectedCondition(&v.instance.Status.Conditions, v.instance.Generation, msg)

		return
	}

	if vrgReplicating {
		v.log.Info("Marking VRG data protection false with replicating reason")

		msg := "VolumeReplicationGroup is replicating"
		setVRGDataProtectionProgressCondition(&v.instance.Status.Conditions, v.instance.Generation, msg)

		return
	}

	// VRG is neither Data Protected nor Replicating
	v.log.Info("Marking VRG data not protected with error. All PVCs are not ready")

	msg := "All PVCs of the VolumeReplicationGroup are not ready"
	setVRGAsDataNotProtectedCondition(&v.instance.Status.Conditions, v.instance.Generation, msg)
}

// updateVRGClusterDataProtectedCondition updates the VRG summary level
// cluster data protected condition based on individual PVC's cluster data
// protected condition.  If at least one PVC is experiencing an error condition,
// set the VRG level condition to error.  If not, if at least one PVC is in a
// protecting condition, set the VRG level condition to protecting.  If not, set
// the VRG level condition to true.
func (v *VRGInstance) aggregateVolRepClusterDataProtectedCondition() {
	atleastOneProtecting := false
	atleastOneError := false

	for _, protectedPVC := range v.instance.Status.ProtectedPVCs {
		if protectedPVC.ProtectedByVolSync {
			continue
		}

		condition := findCondition(protectedPVC.Conditions, VRGConditionTypeClusterDataProtected)

		if condition == nil ||
			condition.Reason == VRGConditionReasonUploading {
			atleastOneProtecting = true
			// Continue to check if there are other PVCs that have an error
			// condition.
			continue
		}

		if condition.Reason != VRGConditionReasonUploaded {
			atleastOneError = true
			// A single PVC with an error condition is sufficient to affect the
			// entire VRG; no need to check other PVCs.
			break
		}
	}

	if atleastOneError {
		msg := "Cluster data of one or more PVs are unprotected"
		setVRGClusterDataUnprotectedCondition(&v.instance.Status.Conditions, v.instance.Generation, msg)
		v.log.Info(msg)

		return
	}

	if atleastOneProtecting {
		msg := "Cluster data of one or more PVs are in the process of being protected"
		setVRGClusterDataProtectingCondition(&v.instance.Status.Conditions, v.instance.Generation, msg)
		v.log.Info(msg)

		return
	}

	// All PVCs in the VRG are in protected state because not a single PVC is in
	// error condition and not a single PVC is in protecting condition.  Hence,
	// the VRG's cluster data protection condition is met.
	msg := "Cluster data of all PVs are protected"
	setVRGClusterDataProtectedCondition(&v.instance.Status.Conditions, v.instance.Generation, msg)
	v.log.Info(msg)
}
