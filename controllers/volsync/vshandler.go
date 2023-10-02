// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package volsync

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/go-logr/logr"
	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v4/apis/volumesnapshot/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/reference"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	errorswrapper "github.com/pkg/errors"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/util"
)

var WaitingForNotInUsePVC = errorswrapper.New("Waiting for PVC to become available for use")

const (
	ServiceExportKind    string = "ServiceExport"
	ServiceExportGroup   string = "multicluster.x-k8s.io"
	ServiceExportVersion string = "v1alpha1"

	VolumeSnapshotKind                     string = "VolumeSnapshot"
	VolumeSnapshotIsDefaultAnnotation      string = "snapshot.storage.kubernetes.io/is-default-class"
	VolumeSnapshotIsDefaultAnnotationValue string = "true"

	PodVolumePVCClaimIndexName    string = "spec.volumes.persistentVolumeClaim.claimName"
	VolumeAttachmentToPVIndexName string = "spec.source.persistentVolumeName"

	VRGOwnerLabel          string = "volumereplicationgroups-owner"
	FinalSyncTriggerString string = "vrg-final-sync"

	VolSyncFinalizerName = "volumereplicationgroups.ramendr.openshift.io/pvc-volsync-protection"

	SchedulingIntervalMinLength int = 2
	CronSpecMaxDayOfMonth       int = 28

	VolSyncDoNotDeleteLabel    = "volsync.backube/do-not-delete" // TODO: point to volsync constant once it is available
	VolSyncDoNotDeleteLabelVal = "true"

	// See: https://issues.redhat.com/browse/ACM-1256
	// https://github.com/stolostron/backlog/issues/21824
	ACMAppSubDoNotDeleteAnnotation    = "apps.open-cluster-management.io/do-not-delete"
	ACMAppSubDoNotDeleteAnnotationVal = "true"

	OwnerNameAnnotation      = "ramendr.openshift.io/owner-name"
	OwnerNamespaceAnnotation = "ramendr.openshift.io/owner-namespace"

	CopyMethodLocalDirect = "LocalDirect"
)

type VSHandler struct {
	ctx                         context.Context
	client                      client.Client
	log                         logr.Logger
	owner                       metav1.Object
	schedulingInterval          string
	volumeSnapshotClassSelector metav1.LabelSelector // volume snapshot classes to be filtered label selector
	defaultCephFSCSIDriverName  string
	destinationCopyMethod       volsyncv1alpha1.CopyMethodType
	volumeSnapshotClassList     *snapv1.VolumeSnapshotClassList
}

func NewVSHandler(ctx context.Context, client client.Client, log logr.Logger, owner metav1.Object,
	asyncSpec *ramendrv1alpha1.VRGAsyncSpec, defaultCephFSCSIDriverName string, copyMethod string,
) *VSHandler {
	vsHandler := &VSHandler{
		ctx:                        ctx,
		client:                     client,
		log:                        log,
		owner:                      owner,
		defaultCephFSCSIDriverName: defaultCephFSCSIDriverName,
		destinationCopyMethod:      volsyncv1alpha1.CopyMethodType(copyMethod),
		volumeSnapshotClassList:    nil, // Do not initialize until we need it
	}

	if asyncSpec != nil {
		vsHandler.schedulingInterval = asyncSpec.SchedulingInterval
		vsHandler.volumeSnapshotClassSelector = asyncSpec.VolumeSnapshotClassSelector
	}

	return vsHandler
}

// returns replication destination only if create/update is successful and the RD is considered available.
// Callers should assume getting a nil replication destination back means they should retry/requeue.
//
//nolint:gocognit,cyclop
func (v *VSHandler) ReconcileRD(
	rdSpec ramendrv1alpha1.VolSyncReplicationDestinationSpec, paused bool,
) (*volsyncv1alpha1.ReplicationDestination, bool, error) {
	l := v.log.WithValues("rdSpec", rdSpec)
	l.Info("Entering ReconcileRD")

	const requeue = true

	if !rdSpec.ProtectedPVC.ProtectedByVolSync {
		return nil, requeue, fmt.Errorf("protectedPVC %s is not VolSync Enabled", rdSpec.ProtectedPVC.Name)
	}

	// Pre-allocated shared secret - DRPC will generate and propagate this secret from hub to clusters
	pskSecretName := GetVolSyncPSKSecretNameFromVRGName(v.owner.GetName())
	// Need to confirm this secret exists on the cluster before proceeding, otherwise volsync will generate it
	secretExists, err := v.validateSecretAndAddVRGOwnerRef(pskSecretName)
	if err != nil || !secretExists {
		return nil, requeue, err
	}

	// Check if a ReplicationSource is still here (Can happen if transitioning from primary to secondary)
	// Before creating a new RD for this PVC, make sure any ReplicationSource for this PVC is cleaned up first
	// This avoids a scenario where we create an RD that immediately syncs with an RS that still exists locally
	err = v.DeleteRS(rdSpec.ProtectedPVC.Name)
	if err != nil {
		return nil, requeue, err
	}

	copyMethod, dstPVC, shouldRequeue, err := v.SelectDestCopyMethod(rdSpec, l)
	if err != nil {
		return nil, requeue, err
	}

	if shouldRequeue {
		return nil, requeue, nil
	}

	var rd *volsyncv1alpha1.ReplicationDestination

	rd, err = v.createOrUpdateRD(rdSpec, pskSecretName, copyMethod, dstPVC, paused)
	if err != nil {
		return nil, requeue, err
	}

	err = v.reconcileServiceExportForRD(rd)
	if err != nil {
		return nil, requeue, err
	}

	if !rdStatusReady(rd, l) {
		return nil, requeue, nil
	}

	l.V(1).Info(fmt.Sprintf("Copy method: %s", v.destinationCopyMethod))

	if v.IsCopyMethodLocalDirect() {
		pvc, err := v.getPVC(*dstPVC)
		if err != nil {
			return nil, requeue, err
		}

		err = v.addOwnerReferenceAndUpdate(pvc, rd)
		if err != nil {
			return nil, requeue, err
		}

		lrd, lrs, err := v.reconcileLocalReplication(rd, rdSpec, pskSecretName, l)
		if lrd == nil || lrs == nil || err != nil {
			return nil, requeue, err
		}
	}

	l.V(1).Info(fmt.Sprintf("ReplicationDestination Reconcile Complete rd=%s", rd.Name))

	return rd, !requeue, nil
}

// For ReplicationDestination - considered ready when a sync has completed
// - rsync address should be filled out in the status
// - latest image should be set properly in the status (at least one sync cycle has completed and we have a snapshot)
func rdStatusReady(rd *volsyncv1alpha1.ReplicationDestination, log logr.Logger) bool {
	if rd.Status == nil {
		return false
	}

	if rd.Status.RsyncTLS == nil || rd.Status.RsyncTLS.Address == nil {
		log.V(1).Info("ReplicationDestination waiting for Address ...")

		return false
	}

	return true
}

func (v *VSHandler) createOrUpdateRD(
	rdSpec ramendrv1alpha1.VolSyncReplicationDestinationSpec, pskSecretName string,
	copyMethod volsyncv1alpha1.CopyMethodType, dstPVC *string, paused bool) (*volsyncv1alpha1.ReplicationDestination, error,
) {
	l := v.log.WithValues("rdSpec", rdSpec)

	volumeSnapshotClassName, err := v.GetVolumeSnapshotClassFromPVCStorageClass(rdSpec.ProtectedPVC.StorageClassName)
	if err != nil {
		return nil, err
	}

	pvcAccessModes := []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce} // Default value
	if len(rdSpec.ProtectedPVC.AccessModes) > 0 {
		pvcAccessModes = rdSpec.ProtectedPVC.AccessModes
	}

	rd := &volsyncv1alpha1.ReplicationDestination{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getReplicationDestinationName(rdSpec.ProtectedPVC.Name),
			Namespace: v.owner.GetNamespace(),
		},
	}

	op, err := ctrlutil.CreateOrUpdate(v.ctx, v.client, rd, func() error {
		if err := ctrl.SetControllerReference(v.owner, rd, v.client.Scheme()); err != nil {
			l.Error(err, "unable to set controller reference")

			return fmt.Errorf("%w", err)
		}

		addVRGOwnerLabel(v.owner, rd)
		addAnnotation(rd, OwnerNameAnnotation, v.owner.GetName())
		addAnnotation(rd, OwnerNamespaceAnnotation, v.owner.GetNamespace())

		rd.Spec.RsyncTLS = &volsyncv1alpha1.ReplicationDestinationRsyncTLSSpec{
			ServiceType: v.getRsyncServiceType(),
			KeySecret:   &pskSecretName,

			ReplicationDestinationVolumeOptions: volsyncv1alpha1.ReplicationDestinationVolumeOptions{
				CopyMethod:              copyMethod,
				Capacity:                rdSpec.ProtectedPVC.Resources.Requests.Storage(),
				StorageClassName:        rdSpec.ProtectedPVC.StorageClassName,
				AccessModes:             pvcAccessModes,
				VolumeSnapshotClassName: &volumeSnapshotClassName,
				DestinationPVC:          dstPVC,
			},
		}
		rd.Spec.Paused = paused

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}

	l.V(1).Info("ReplicationDestination createOrUpdate Complete", "op", op)

	return rd, nil
}

func (v *VSHandler) isPVCInUseByNonRDPod(pvcName string) (bool, error) {
	rd := &volsyncv1alpha1.ReplicationDestination{}

	err := v.client.Get(v.ctx,
		types.NamespacedName{
			Name:      getReplicationDestinationName(pvcName),
			Namespace: v.owner.GetNamespace(),
		}, rd)

	// IF RD is Found, then no more checks are needed. We'll assume that the RD
	// was created when the PVC was Not in use.
	if err == nil {
		return false, nil
	} else if !kerrors.IsNotFound(err) {
		return false, fmt.Errorf("%w", err)
	}

	// PVC must not be in use
	pvcInUse, err := v.pvcExistsAndInUse(pvcName, false)
	if err != nil {
		return false, err
	}

	if pvcInUse {
		return true, nil
	}

	// Not in-use
	return false, nil
}

// Returns true only if runFinalSync is true and the final sync is done
// Returns replication source only if create/update is successful
// Callers should assume getting a nil replication source back means they should retry/requeue.
// Returns true/false if final sync is complete, and also returns an RS if one was reconciled.
//
//nolint:cyclop
func (v *VSHandler) ReconcileRS(rsSpec ramendrv1alpha1.VolSyncReplicationSourceSpec,
	runFinalSync bool) (bool /* finalSyncComplete */, *volsyncv1alpha1.ReplicationSource, error,
) {
	l := v.log.WithValues("rsSpec", rsSpec, "runFinalSync", runFinalSync)

	if !rsSpec.ProtectedPVC.ProtectedByVolSync {
		return false, nil, fmt.Errorf("protectedPVC %s is not VolSync Enabled", rsSpec.ProtectedPVC.Name)
	}

	// Pre-allocated shared secret - DRPC will generate and propagate this secret from hub to clusters
	pskSecretName := GetVolSyncPSKSecretNameFromVRGName(v.owner.GetName())

	// Need to confirm this secret exists on the cluster before proceeding, otherwise volsync will generate it
	secretExists, err := v.validateSecretAndAddVRGOwnerRef(pskSecretName)
	if err != nil || !secretExists {
		return false, nil, err
	}

	// Check if a ReplicationDestination is still here (Can happen if transitioning from secondary to primary)
	// Before creating a new RS for this PVC, make sure any ReplicationDestination for this PVC is cleaned up first
	// This avoids a scenario where we create an RS that immediately connects back to an RD that still exists locally
	// Need to be sure ReconcileRS is never called prior to restoring any PVC that need to be restored from RDs first
	err = v.DeleteRD(rsSpec.ProtectedPVC.Name)
	if err != nil {
		return false, nil, err
	}

	pvcOk, err := v.validatePVCBeforeRS(rsSpec, runFinalSync)
	if !pvcOk || err != nil {
		// Return the replicationSource if it already exists
		existingRS, getRSErr := v.getRS(getReplicationSourceName(rsSpec.ProtectedPVC.Name))
		if getRSErr != nil {
			return false, nil, err
		}
		// Return the RS here - allows status updates to understand that prev RS syncs may have completed
		// (i.e. data protected == true), even though we may be indicating that finalSync has not yet completed
		// because the PVC is still in-use
		return false, existingRS, err
	}

	replicationSource, err := v.createOrUpdateRS(rsSpec, pskSecretName, runFinalSync)
	if err != nil {
		return false, replicationSource, err
	}

	if v.IsCopyMethodLocalDirect() {
		err = v.updateFinalizerAndOwnerForLocalDirect(rsSpec.ProtectedPVC.Name, replicationSource)
		if err != nil {
			return false, replicationSource, err
		}
	}

	//
	// For final sync only - check status to make sure the final sync is complete
	// and also run cleanup (removes PVC we just ran the final sync from)
	//
	if runFinalSync && isFinalSyncComplete(replicationSource, l) {
		return true, replicationSource, v.cleanupAfterRSFinalSync(rsSpec)
	}

	l.V(1).Info("ReplicationSource Reconcile Complete")

	return false, replicationSource, err
}

// Need to validate that our PVC is no longer in use before proceeding
// If in final sync and the source PVC no longer exists, this could be from
// a 2nd call to runFinalSync and we may have already cleaned up the PVC - so if pvc does not
// exist, treat the same as not in use - continue on with reconcile of the RS (and therefore
// check status to confirm final sync is complete)
func (v *VSHandler) validatePVCBeforeRS(rsSpec ramendrv1alpha1.VolSyncReplicationSourceSpec,
	runFinalSync bool) (bool, error,
) {
	l := v.log.WithValues("rsSpec", rsSpec, "runFinalSync", runFinalSync)

	if runFinalSync {
		// If runFinalSync, check the PVC and make sure it's not mounted to a pod
		// as we want the app to be quiesced/removed before running final sync
		pvcIsMounted, err := v.pvcExistsAndInUse(rsSpec.ProtectedPVC.Name, false)
		if err != nil {
			return false, err
		}

		if pvcIsMounted {
			return false, nil
		}

		return true, nil // Good to proceed - PVC is not in use, not mounted to node (or does not exist-should not happen)
	}

	// Not running final sync - if we have not yet created an RS for this PVC, then make sure a pod has mounted
	// the PVC and is in "Running" state before attempting to create an RS.
	// This is a best effort to confirm the app that is using the PVC is started before trying to replicate the PVC.
	_, err := v.getRS(getReplicationSourceName(rsSpec.ProtectedPVC.Name))
	if err != nil && kerrors.IsNotFound(err) {
		l.Info("ReplicationSource does not exist yet. " +
			"validating that the PVC to be protected is in use by a ready pod ...")
		// RS does not yet exist - consider PVC is ok if it's mounted and in use by running pod
		inUseByReadyPod, err := v.pvcExistsAndInUse(rsSpec.ProtectedPVC.Name, true /* Check mounting pod is Ready */)
		if err != nil {
			return false, err
		}

		if !inUseByReadyPod {
			l.Info("PVC is not in use by ready pod, not creating RS yet ...")

			return false, nil
		}

		l.Info("PVC is in use by ready pod, proceeding to create RS ...")

		return true, nil
	}

	if err != nil {
		// Err looking up the RS, return it
		return false, err
	}

	// Replication source already exists, no need for any pvc checking
	return true, nil
}

func isFinalSyncComplete(replicationSource *volsyncv1alpha1.ReplicationSource, log logr.Logger) bool {
	if replicationSource.Status == nil || replicationSource.Status.LastManualSync != FinalSyncTriggerString {
		log.V(1).Info("ReplicationSource running final sync - waiting for status ...")

		return false
	}

	log.V(1).Info("ReplicationSource final sync complete")

	return true
}

func (v *VSHandler) cleanupAfterRSFinalSync(rsSpec ramendrv1alpha1.VolSyncReplicationSourceSpec) error {
	// Final sync is done, make sure PVC is cleaned up, Skip if we are using CopyMethodDirect
	if v.IsCopyMethodDirect() {
		v.log.Info("Preserving PVC to use for CopyMethodDirect", "pvcName", rsSpec.ProtectedPVC.Name)

		return nil
	}

	v.log.Info("Cleanup after final sync", "pvcName", rsSpec.ProtectedPVC.Name)

	return util.DeletePVC(v.ctx, v.client, rsSpec.ProtectedPVC.Name, v.owner.GetNamespace(), v.log)
}

//nolint:funlen
func (v *VSHandler) createOrUpdateRS(rsSpec ramendrv1alpha1.VolSyncReplicationSourceSpec,
	pskSecretName string, runFinalSync bool) (*volsyncv1alpha1.ReplicationSource, error,
) {
	l := v.log.WithValues("rsSpec", rsSpec, "runFinalSync", runFinalSync)

	storageClass, err := v.getStorageClass(rsSpec.ProtectedPVC.StorageClassName)
	if err != nil {
		return nil, err
	}

	volumeSnapshotClassName, err := v.getVolumeSnapshotClassFromPVCStorageClass(storageClass)
	if err != nil {
		return nil, err
	}

	// Fix for CephFS (replication source only) - may need different storageclass and access modes
	err = v.ModifyRSSpecForCephFS(&rsSpec, storageClass)
	if err != nil {
		return nil, err
	}

	// Remote service address created for the ReplicationDestination on the secondary
	// The secondary namespace will be the same as primary namespace so use the vrg.Namespace
	remoteAddress := getRemoteServiceNameForRDFromPVCName(rsSpec.ProtectedPVC.Name, v.owner.GetNamespace())

	rs := &volsyncv1alpha1.ReplicationSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getReplicationSourceName(rsSpec.ProtectedPVC.Name),
			Namespace: v.owner.GetNamespace(),
		},
	}

	op, err := ctrlutil.CreateOrUpdate(v.ctx, v.client, rs, func() error {
		if err := ctrl.SetControllerReference(v.owner, rs, v.client.Scheme()); err != nil {
			l.Error(err, "unable to set controller reference")

			return fmt.Errorf("%w", err)
		}

		addVRGOwnerLabel(v.owner, rs)

		rs.Spec.SourcePVC = rsSpec.ProtectedPVC.Name

		if runFinalSync {
			l.V(1).Info("ReplicationSource - final sync")
			// Change the schedule to instead use a keyword trigger - to trigger
			// a final sync to happen
			rs.Spec.Trigger = &volsyncv1alpha1.ReplicationSourceTriggerSpec{
				Manual: FinalSyncTriggerString,
			}
		} else {
			// Set schedule
			scheduleCronSpec, err := v.getScheduleCronSpec()
			if err != nil {
				l.Error(err, "unable to parse schedulingInterval")

				return err
			}
			rs.Spec.Trigger = &volsyncv1alpha1.ReplicationSourceTriggerSpec{
				Schedule: scheduleCronSpec,
			}
		}

		rs.Spec.RsyncTLS = &volsyncv1alpha1.ReplicationSourceRsyncTLSSpec{
			KeySecret: &pskSecretName,
			Address:   &remoteAddress,

			ReplicationSourceVolumeOptions: volsyncv1alpha1.ReplicationSourceVolumeOptions{
				// Always using CopyMethod of snapshot for now - could use 'Clone' CopyMethod for specific
				// storage classes that support it in the future
				CopyMethod:              volsyncv1alpha1.CopyMethodSnapshot,
				VolumeSnapshotClassName: &volumeSnapshotClassName,
				StorageClassName:        rsSpec.ProtectedPVC.StorageClassName,
				AccessModes:             rsSpec.ProtectedPVC.AccessModes,
			},
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}

	l.V(1).Info("ReplicationSource createOrUpdate Complete", "op", op)

	return rs, nil
}

func (v *VSHandler) PreparePVC(pvcName string, prepFinalSync, copyMethodDirectOrLocalDirect bool) error {
	if prepFinalSync || copyMethodDirectOrLocalDirect {
		prepared, err := v.TakePVCOwnership(pvcName)
		if err != nil || !prepared {
			return fmt.Errorf("waiting to take pvc ownership (%w), prepFinalSync: %t, DirectOrLocalDirect: %t",
				err, prepFinalSync, copyMethodDirectOrLocalDirect)
		}
	}

	return nil
}

// This doesn't need to specifically be in VSHandler - could be useful for non-volsync scenarios?
// Will look at annotations on the PVC, make sure the reconcile option from ACM is set to merge (or not exists)
// and then will remove ACM annotations and also add VRG as the owner.  This is to break the connection between
// the appsub and the PVC itself.  This way we can proceed to remove the app without the PVC being removed.
// We need the PVC left behind for running the final sync or for CopyMethod Direct.
func (v *VSHandler) TakePVCOwnership(pvcName string) (bool, error) {
	l := v.log.WithValues("pvcName", pvcName)

	// Confirm PVC exists and add our VRG as ownerRef
	pvc, needUpdate, err := v.preparePVCForOwnershipTakeOver(pvcName)
	if err != nil {
		l.Error(err, "unable to validate PVC or add ownership")

		return false, err
	}

	// Remove acm annotations from the PVC just as a precaution - ACM uses the annotations to track that the
	// pvc is owned by an application.  Removing them should not be necessary now that we are adding
	// the do-not-delete annotation.  With the do-not-delete annotation (see ACMAppSubDoNotDeleteAnnotation), ACM
	// should not delete the pvc when the application is removed.
	updatedAnnotations := map[string]string{}

	for currAnnotationKey, currAnnotationValue := range pvc.Annotations {
		// We want to only preserve annotations not from ACM (i.e. remove all ACM annotations to break ownership)
		if !strings.HasPrefix(currAnnotationKey, "apps.open-cluster-management.io") ||
			currAnnotationKey == ACMAppSubDoNotDeleteAnnotation {
			updatedAnnotations[currAnnotationKey] = currAnnotationValue
		}
	}

	if !reflect.DeepEqual(pvc.Annotations, updatedAnnotations) {
		pvc.Annotations = updatedAnnotations
		needUpdate = true
	}

	if needUpdate {
		l.V(1).Info("Updating PVC metadata", "pvc-md", pvc.GetObjectMeta())

		err = v.client.Update(v.ctx, pvc)
		if err != nil {
			l.Error(err, "Error updating annotations on PVC to break appsub ownership")

			return false, fmt.Errorf("error updating annotations on PVC to break appsub ownership (%w)", err)
		}
	}

	return true, nil
}

// Will return true only if the pvc exists and in use - will not throw error if PVC not found
// If inUsePodMustBeReady is true, will only return true if the pod mounting the PVC is in Ready state
// If inUsePodMustBeReady is false, will run an additional volume attachment check to make sure the PV underlying
// the PVC is really detached (i.e. I/O operations complete) and therefore we can assume, quiesced.
func (v *VSHandler) pvcExistsAndInUse(pvcName string, inUsePodMustBeReady bool) (bool, error) {
	pvc, err := v.getPVC(pvcName)
	if err != nil {
		if kerrors.IsNotFound(err) {
			v.log.Info("PVC not found", "pvcName", pvcName)

			return false, nil // No error just indicate not exists (so also not in use)
		}

		return false, err // error accessing the PVC, return it
	}

	v.log.V(1).Info("pvc found", "pvcName", pvcName)

	inUseByPod, err := util.IsPVCInUseByPod(v.ctx, v.client, v.log, pvcName, pvc.GetNamespace(), inUsePodMustBeReady)
	if err != nil || inUseByPod || inUsePodMustBeReady {
		// Return status immediately
		return inUseByPod, err
	}

	// No pod is mounting the PVC - do additional check to make sure no volume attachment exists
	return util.IsPVAttachedToNode(v.ctx, v.client, v.log, pvc)
}

func (v *VSHandler) getPVC(pvcName string) (*corev1.PersistentVolumeClaim, error) {
	pvc := &corev1.PersistentVolumeClaim{}

	err := v.client.Get(v.ctx,
		types.NamespacedName{
			Name:      pvcName,
			Namespace: v.owner.GetNamespace(),
		}, pvc)
	if err != nil {
		return pvc, fmt.Errorf("%w", err)
	}

	return pvc, nil
}

func (v *VSHandler) getPV(pvName string) (*corev1.PersistentVolume, error) {
	pv := &corev1.PersistentVolume{}

	err := v.client.Get(v.ctx,
		types.NamespacedName{
			Name:      pvName,
			Namespace: v.owner.GetNamespace(),
		}, pv)
	if err != nil {
		return pv, fmt.Errorf("%w", err)
	}

	return pv, nil
}

// Adds ACM "do-not-delete" annotation to indicate that when the appsub is removed, ACM
// should not cleanup this PVC - we want it left behind so we can run a final sync.
// Also adds a finalizer if and only if the copyMethod used is LocalDirect
func (v *VSHandler) preparePVCForOwnershipTakeOver(pvcName string) (*corev1.PersistentVolumeClaim, bool, error) {
	pvc, err := v.getPVC(pvcName)
	if err != nil {
		return nil, false, err
	}

	v.log.V(1).Info("PVC exists", "Name", pvcName)

	// Add annotation to indicate that ACM should not delete/cleanup this pvc.
	annotationUpdated := addAnnotation(pvc, ACMAppSubDoNotDeleteAnnotation, ACMAppSubDoNotDeleteAnnotationVal)

	ownerRefUpdated, err := v.addOwnerReference(pvc, v.owner) // VRG as owner
	if err != nil {
		return nil, false, err
	}

	return pvc, annotationUpdated || ownerRefUpdated, nil
}

func (v *VSHandler) validateSecretAndAddVRGOwnerRef(secretName string) (bool, error) {
	secret := &corev1.Secret{}

	err := v.client.Get(v.ctx,
		types.NamespacedName{
			Name:      secretName,
			Namespace: v.owner.GetNamespace(),
		}, secret)
	if err != nil {
		if !kerrors.IsNotFound(err) {
			v.log.Error(err, "Failed to get secret", "secretName", secretName)

			return false, fmt.Errorf("error getting secret (%w)", err)
		}

		// Secret is not found
		v.log.Info("Secret not found", "secretName", secretName)

		return false, nil
	}

	v.log.Info("Secret exists", "secretName", secretName)

	// Add VRG as owner
	if err := v.addOwnerReferenceAndUpdate(secret, v.owner); err != nil {
		v.log.Error(err, "Unable to update secret", "secretName", secretName)

		return true, err
	}

	v.log.V(1).Info("VolSync secret validated", "secret name", secretName)

	return true, nil
}

func (v *VSHandler) getRS(name string) (*volsyncv1alpha1.ReplicationSource, error) {
	rs := &volsyncv1alpha1.ReplicationSource{}

	err := v.client.Get(v.ctx,
		types.NamespacedName{
			Name:      name,
			Namespace: v.owner.GetNamespace(),
		}, rs)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}

	return rs, nil
}

func (v *VSHandler) DeleteRS(pvcName string) error {
	// Remove a ReplicationSource by name that is owned (by parent vrg owner)
	currentRSListByOwner, err := v.listRSByOwner()
	if err != nil {
		return err
	}

	for i := range currentRSListByOwner.Items {
		rs := currentRSListByOwner.Items[i]

		if rs.GetName() == getReplicationSourceName(pvcName) {
			// Delete the ReplicationSource, log errors with cleanup but continue on
			if err := v.client.Delete(v.ctx, &rs); err != nil {
				v.log.Error(err, "Error cleaning up ReplicationSource", "name", rs.GetName())
			} else {
				v.log.Info("Deleted ReplicationSource", "name", rs.GetName())
			}
		}
	}

	return nil
}

//nolint:nestif
func (v *VSHandler) DeleteRD(pvcName string) error {
	// Remove a ReplicationDestination by name that is owned (by parent vrg owner)
	currentRDListByOwner, err := v.listRDByOwner()
	if err != nil {
		return err
	}
	v.log.Info("Deleting ReplicationDestinations")
	for i := range currentRDListByOwner.Items {
		rd := currentRDListByOwner.Items[i]
		v.log.Info("Deleting ReplicationDestination", "name", rd.GetName())
		if rd.GetName() == getReplicationDestinationName(pvcName) {
			if v.IsCopyMethodLocalDirect() {
				latestImageRef, err := v.getRDLatestImage(pvcName)
				if err != nil {
					return err
				}

				err = v.deleteLocalRDAndRS(rd.GetName(), *latestImageRef)
				if err != nil {
					return err
				}
			}
			// Delete the ReplicationDestination, log errors with cleanup but continue on
			if err := v.client.Delete(v.ctx, &rd); err != nil {
				v.log.Error(err, "Error cleaning up ReplicationDestination", "name", rd.GetName())
			} else {
				v.log.Info("DeleteRD deleted ReplicationDestination", "name", rd.GetName())
			}
		}
	}

	return nil
}

//nolint:gocognit
func (v *VSHandler) deleteLocalRDAndRS(rdName string, snapshotRef corev1.TypedLocalObjectReference) error {
	lrs := &volsyncv1alpha1.ReplicationSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getLocalReplicationName(rdName),
			Namespace: v.owner.GetNamespace(),
		},
	}

	err := v.client.Get(v.ctx, types.NamespacedName{
		Name:      lrs.GetName(),
		Namespace: lrs.GetNamespace(),
	}, lrs)
	if err != nil {
		if kerrors.IsNotFound(err) {
			return v.deleteLocalRD(getLocalReplicationName(rdName))
		}

		return err
	}

	// For LocalDirect, localRS trigger must point to the latest RD snapshot image. Otherwise,
	// we wait for local final sync to take place first befor cleaning up.
	if lrs.Spec.Trigger != nil && lrs.Spec.Trigger.Manual == snapshotRef.Name {
		// When local final sync is complete, we cleanup all locally created resources except the app PVC
		if lrs.Status != nil && lrs.Status.LastManualSync == lrs.Spec.Trigger.Manual {
			err = v.cleanupLocalResources(lrs)
			if err != nil {
				return err
			}

			v.log.V(1).Info("Cleaned up local resources for RD", "name", rdName)

			return nil
		}
	}

	return fmt.Errorf("waiting for local final sync to complete")
}

//nolint:gocognit
func (v *VSHandler) CleanupRDNotInSpecList(rdSpecList []ramendrv1alpha1.VolSyncReplicationDestinationSpec) error {
	// Remove any ReplicationDestination owned (by parent vrg owner) that is not in the provided rdSpecList
	currentRDListByOwner, err := v.listRDByOwner()
	if err != nil {
		return err
	}

	for i := range currentRDListByOwner.Items {
		rd := currentRDListByOwner.Items[i]

		foundInSpecList := false

		for _, rdSpec := range rdSpecList {
			if rd.GetName() == getReplicationDestinationName(rdSpec.ProtectedPVC.Name) {
				foundInSpecList = true

				break
			}
		}

		if !foundInSpecList {
			// If it is localRD, there will be no RDSpec. We shoul NOT clean it up yet.
			if rd.GetLabels()[VolSyncDoNotDeleteLabel] == VolSyncDoNotDeleteLabelVal {
				continue
			}

			if v.IsCopyMethodLocalDirect() {
				latestImageRef, err := v.getRDLatestImage(rd.GetName())
				if err != nil {
					return err
				}

				err = v.deleteLocalRDAndRS(rd.GetName(), *latestImageRef)
				if err != nil {
					return err
				}
			}

			// Delete the ReplicationDestination, log errors with cleanup but continue on
			if err := v.client.Delete(v.ctx, &rd); err != nil {
				v.log.Error(err, "Error cleaning up ReplicationDestination", "name", rd.GetName())
			} else {
				v.log.Info("CleanupRDNotInSpecList deleted ReplicationDestination", "name", rd.GetName())
			}
		}
	}

	return nil
}

// Make sure a ServiceExport exists to export the service for this RD to remote clusters
// See: https://access.redhat.com/documentation/en-us/red_hat_advanced_cluster_management_for_kubernetes/
// 2.4/html/services/services-overview#enable-service-discovery-submariner
func (v *VSHandler) reconcileServiceExportForRD(rd *volsyncv1alpha1.ReplicationDestination) error {
	// Using unstructured to avoid needing to require serviceexport in client scheme
	svcExport := &unstructured.Unstructured{}
	svcExport.Object = map[string]interface{}{
		"metadata": map[string]interface{}{
			"name":      getLocalServiceNameForRD(rd.GetName()), // Get name of the local service (this needs to be exported)
			"namespace": rd.GetNamespace(),
		},
	}
	svcExport.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   ServiceExportGroup,
		Kind:    ServiceExportKind,
		Version: ServiceExportVersion,
	})

	op, err := ctrlutil.CreateOrUpdate(v.ctx, v.client, svcExport, func() error {
		// Make this ServiceExport owned by the replication destination itself rather than the VRG
		// This way on relocate scenarios or failover/failback, when the RD is cleaned up the associated
		// ServiceExport will get cleaned up with it.
		if err := ctrlutil.SetOwnerReference(rd, svcExport, v.client.Scheme()); err != nil {
			v.log.Error(err, "unable to set controller reference", "resource", svcExport)

			return fmt.Errorf("%w", err)
		}

		return nil
	})

	v.log.V(1).Info("ServiceExport createOrUpdate Complete", "op", op)

	if err != nil {
		v.log.Error(err, "error creating or updating ServiceExport", "replication destination name", rd.GetName(),
			"namespace", rd.GetNamespace())

		return fmt.Errorf("error creating or updating ServiceExport (%w)", err)
	}

	v.log.V(1).Info("ServiceExport Reconcile Complete")

	return nil
}

func (v *VSHandler) listRSByOwner() (volsyncv1alpha1.ReplicationSourceList, error) {
	rsList := volsyncv1alpha1.ReplicationSourceList{}
	if err := v.listByOwner(&rsList); err != nil {
		v.log.Error(err, "Failed to list ReplicationSources for VRG", "vrg name", v.owner.GetName())

		return rsList, err
	}

	return rsList, nil
}

func (v *VSHandler) listRDByOwner() (volsyncv1alpha1.ReplicationDestinationList, error) {
	rdList := volsyncv1alpha1.ReplicationDestinationList{}
	if err := v.listByOwner(&rdList); err != nil {
		v.log.Error(err, "Failed to list ReplicationDestinations for VRG", "vrg name", v.owner.GetName())

		return rdList, err
	}

	return rdList, nil
}

// Lists only RS/RD with VRGOwnerLabel that matches the owner
func (v *VSHandler) listByOwner(list client.ObjectList) error {
	matchLabels := map[string]string{
		VRGOwnerLabel: v.owner.GetName(),
	}
	listOptions := []client.ListOption{
		client.InNamespace(v.owner.GetNamespace()),
		client.MatchingLabels(matchLabels),
	}

	if err := v.client.List(v.ctx, list, listOptions...); err != nil {
		v.log.Error(err, "Failed to list by label", "matchLabels", matchLabels)

		return fmt.Errorf("error listing by label (%w)", err)
	}

	return nil
}

func (v *VSHandler) EnsurePVCfromRD(rdSpec ramendrv1alpha1.VolSyncReplicationDestinationSpec) error {
	l := v.log.WithValues("rdSpec", rdSpec)

	latestImage, err := v.getRDLatestImage(rdSpec.ProtectedPVC.Name)
	if err != nil {
		return err
	}

	if !isLatestImageReady(latestImage) {
		noSnapErr := fmt.Errorf("unable to find LatestImage from ReplicationDestination %s", rdSpec.ProtectedPVC.Name)
		l.Error(noSnapErr, "No latestImage")

		return noSnapErr
	}

	// Make copy of the ref and make sure API group is filled out correctly (shouldn't really need this part)
	vsImageRef := latestImage.DeepCopy()
	if vsImageRef.APIGroup == nil || *vsImageRef.APIGroup == "" {
		vsGroup := snapv1.GroupName
		vsImageRef.APIGroup = &vsGroup
	}

	l.V(1).Info("Latest Image for main ReplicationDestination", "latestImage	", vsImageRef)

	return v.validateSnapshotAndEnsurePVC(rdSpec, *vsImageRef)
}

func (v *VSHandler) EnsurePVCforDirectCopy(ctx context.Context, log logr.Logger,
	rdSpec ramendrv1alpha1.VolSyncReplicationDestinationSpec,
) error {
	logger := log.WithValues("PVC", rdSpec.ProtectedPVC.Name)

	if len(rdSpec.ProtectedPVC.AccessModes) == 0 {
		return fmt.Errorf("accessModes must be provided for PVC %v", rdSpec.ProtectedPVC)
	}

	if rdSpec.ProtectedPVC.Resources.Requests.Storage() == nil {
		return fmt.Errorf("capacity must be provided %v", rdSpec.ProtectedPVC)
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rdSpec.ProtectedPVC.Name,
			Namespace: v.owner.GetNamespace(),
		},
	}

	op, err := ctrlutil.CreateOrUpdate(ctx, v.client, pvc, func() error {
		if err := ctrl.SetControllerReference(v.owner, pvc, v.client.Scheme()); err != nil {
			return fmt.Errorf("failed to set controller reference %w", err)
		}

		if pvc.CreationTimestamp.IsZero() {
			pvc.Spec.AccessModes = rdSpec.ProtectedPVC.AccessModes
			pvc.Spec.StorageClassName = rdSpec.ProtectedPVC.StorageClassName
			volumeMode := corev1.PersistentVolumeFilesystem
			pvc.Spec.VolumeMode = &volumeMode
		}

		pvc.Spec.Resources.Requests = rdSpec.ProtectedPVC.Resources.Requests

		if pvc.Labels == nil {
			pvc.Labels = rdSpec.ProtectedPVC.Labels
		} else {
			for key, val := range rdSpec.ProtectedPVC.Labels {
				pvc.Labels[key] = val
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	logger.V(1).Info("PVC created", "operation", op, "pvc", pvc)

	return nil
}

// EnsurePVCforLocalDirectCopy takes control of the PV before the application PVC is fully deleted to optimize
// synchronization from source to destination cluster without syncing the entire data set. This is done every time
// a new ReplicationDestination is to be created. If this is a fresh initial deployment, then this function simply
// creates the destinationPVC, otherwise, it releases the application PVC, the PV bound to it becomes "Available"
// with a ClaimRef of the new destinationPVC, which gets created right after.
func (v *VSHandler) EnsurePVCforLocalDirectCopy(ctx context.Context, log logr.Logger,
	rdSpec ramendrv1alpha1.VolSyncReplicationDestinationSpec,
) (bool, error) {
	rdInst := &volsyncv1alpha1.ReplicationDestination{}
	rdName := getReplicationDestinationName(rdSpec.ProtectedPVC.Name)

	const requeue = true
	// Check if RD exists. If RD exists, it means we are either handling an upgrade or we have already detached,...
	err := v.client.Get(v.ctx, types.NamespacedName{Name: rdName, Namespace: v.owner.GetNamespace()}, rdInst)
	if err != nil {
		if !kerrors.IsNotFound(err) {
			log.Error(err, "Failed to get ReplicationDestination")

			return requeue, fmt.Errorf("error getting replicationdestination (%w)", err)
		}

		log.Info("ReplicationDestination not found", "rd", rdName)
	} else {
		log.Info("ReplicationDestination found", "rd", rdName)
		// We found an RD, PVC orchstration must have already done.
		return !requeue, nil
	}

	// ReplicationDestination not found
	// Assuming the Primary was running on this cluster. Try to get the app PVC.
	appPVC, err := v.getPVC(rdSpec.ProtectedPVC.Name)
	if err != nil {
		if kerrors.IsNotFound(err) {
			// Didn't find the app PVC. It is possible we are setting up the RD due to an initial deployment
			return !requeue, v.createPVCforLocalDirect(ctx, rdSpec, log)
		}

		return requeue, fmt.Errorf("failed to get PVC for LocalDirect")
	}

	// We have an app PVC, we expect it to be in the deleting state
	if appPVC.GetDeletionTimestamp().IsZero() {
		return requeue, fmt.Errorf("app PVC %s not in deleted state", appPVC.GetName())
	}

	log.Info("Detaching appPVC from PV", "appPVC", rdSpec.ProtectedPVC.Name)
	// Reset claimRef, and use (instead of appPVC), a newly to be created PVC.
	shouldRequeue, err := v.detachPVfromDeletedAppPVC(ctx, rdSpec, appPVC, log)
	if err != nil {
		return requeue, err
	}

	if shouldRequeue {
		return requeue, nil
	}

	log.Info("Creating a New PVC to use instead of appPVC", "newPVC", BuildNameForMainDstPVC(rdSpec.ProtectedPVC.Name))
	// Create a new PVC to be used by the RD for localDirect only.
	err = v.createPVCforLocalDirect(ctx, rdSpec, log)
	if err != nil {
		log.Info(err.Error())

		return requeue, err
	}

	return !requeue, RemoveFinalizer(ctx, v.client, appPVC, VolSyncFinalizerName)
}

// detachPVfromDeletedAppPVC accepts a deleted application's PVC as an input argument and detaches it from its
// associated PersistentVolume. This operation ensures that the PVC is disassociated from its underlying storage,
// allowing for the safe removal of the PVC and the safe reuse of the PV.
// This function will stall waiting for the pv.Status.Phase to become "Available"
func (v *VSHandler) detachPVfromDeletedAppPVC(ctx context.Context,
	rdSpec ramendrv1alpha1.VolSyncReplicationDestinationSpec,
	appPVC *corev1.PersistentVolumeClaim,
	log logr.Logger,
) (bool, error) {
	const requeue = true
	// Get the PV that is bound to this app PVC
	pv, err := v.getPV(appPVC.Spec.VolumeName)
	if err != nil {
		if !kerrors.IsNotFound(err) {
			return requeue, fmt.Errorf("failed to get PV for LocalDirect")
		}
	} else {
		// PV found. Replace its ClaimRef with the new one for the new PVC that will be created.
		if pv.Spec.ClaimRef == nil {
			pv.Spec.ClaimRef = &corev1.ObjectReference{}
		}

		// Build a new PVC name. This name will match the soon to be created PVC
		newPVCName := BuildNameForMainDstPVC(appPVC.GetName())

		if pv.Spec.ClaimRef.Name != newPVCName {
			pv.Spec.ClaimRef.Name = newPVCName
			pv.Spec.ClaimRef.Namespace = appPVC.GetNamespace()
			pv.Spec.ClaimRef.UID = ""
			pv.Spec.ClaimRef.ResourceVersion = ""
			pv.Spec.ClaimRef.APIVersion = ""

			if err := v.client.Update(ctx, pv); err != nil {
				return requeue, fmt.Errorf("failed to update PV %s. Err %w", pv.GetName(), err)
			}

			log.Info(fmt.Sprintf("Removed claimRef.Name %s from pv %s, and added %s",
				appPVC.GetName(), pv.GetName(), newPVCName))

			return requeue, nil // Just updated the PV. Need to requeue
		}

		if pv.Status.Phase != corev1.VolumeAvailable {
			log.Info("PV phase is not Available yet. Waiting...", "phase", pv.Status.Phase)
			return requeue, nil // Wait for the PV phase to change to "Available". Need to requeue
		}
	}

	return !requeue, nil
}

// createPVCforLocalDirect creates a new PVC to be attached to an available PV for local direct usage.
func (v *VSHandler) createPVCforLocalDirect(ctx context.Context,
	rdSpec ramendrv1alpha1.VolSyncReplicationDestinationSpec,
	log logr.Logger,
) error {
	if len(rdSpec.ProtectedPVC.AccessModes) == 0 {
		return fmt.Errorf("accessModes must be provided for PVC %v", rdSpec.ProtectedPVC)
	}

	if rdSpec.ProtectedPVC.Resources.Requests.Storage() == nil {
		return fmt.Errorf("capacity must be provided %v", rdSpec.ProtectedPVC)
	}

	rdPVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      BuildNameForMainDstPVC(rdSpec.ProtectedPVC.Name),
			Namespace: v.owner.GetNamespace(),
		},
	}

	op, err := ctrlutil.CreateOrUpdate(ctx, v.client, rdPVC, func() error {
		// Here we want to set owner as the RD, but we don't have an RD yet. So delay setting the owner
		if rdPVC.CreationTimestamp.IsZero() {
			rdPVC.Spec.AccessModes = rdSpec.ProtectedPVC.AccessModes
			rdPVC.Spec.StorageClassName = rdSpec.ProtectedPVC.StorageClassName
			volumeMode := corev1.PersistentVolumeFilesystem
			rdPVC.Spec.VolumeMode = &volumeMode
		}

		rdPVC.Spec.Resources.Requests = rdSpec.ProtectedPVC.Resources.Requests

		return nil
	})
	if err != nil {
		return err
	}

	log.V(1).Info("PVC created for main RD to be used for localDirect", "operation", op, "pvc", rdPVC)

	return nil
}

func (v *VSHandler) validateSnapshotAndEnsurePVC(rdSpec ramendrv1alpha1.VolSyncReplicationDestinationSpec,
	snapshotRef corev1.TypedLocalObjectReference,
) error {
	snap, err := v.validateSnapshotAndAddDoNotDeleteLabel(snapshotRef)
	if err != nil {
		return err
	}

	if v.IsCopyMethodDirectOrLocalDirect() {
		v.log.V(1).Info(fmt.Sprintf("Using copyMethod %s. pvcName %s",
			v.destinationCopyMethod, rdSpec.ProtectedPVC.Name))

		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rdSpec.ProtectedPVC.Name,
				Namespace: v.owner.GetNamespace(),
			},
		}

		err = ValidateObjectExists(v.ctx, v.client, pvc)
		if err != nil {
			return err
		}

		err = v.ensureLastSnapSyncedLocally(rdSpec.ProtectedPVC.Name, rdSpec, snapshotRef)
		if err != nil {
			return err
		}
	} else {
		// Restore pvc from snapshot
		var restoreSize *resource.Quantity

		if snap.Status != nil {
			restoreSize = snap.Status.RestoreSize
		}

		_, err := v.ensurePVCFromSnapshot(rdSpec, snapshotRef, restoreSize)
		if err != nil {
			return err
		}
	}

	// Add ownerRef on snapshot pointing to the vrg - if/when the VRG gets cleaned up, then GC can cleanup the snap
	return v.addOwnerReferenceAndUpdate(snap, v.owner)
}

//nolint:funlen,gocognit,cyclop
func (v *VSHandler) ensurePVCFromSnapshot(rdSpec ramendrv1alpha1.VolSyncReplicationDestinationSpec,
	snapshotRef corev1.TypedLocalObjectReference, snapRestoreSize *resource.Quantity,
) (*corev1.PersistentVolumeClaim, error) {
	l := v.log.WithValues("pvcName", rdSpec.ProtectedPVC.Name, "snapshotRef", snapshotRef,
		"snapRestoreSize", snapRestoreSize)

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rdSpec.ProtectedPVC.Name,
			Namespace: v.owner.GetNamespace(),
		},
	}

	pvcRequestedCapacity := rdSpec.ProtectedPVC.Resources.Requests.Storage()
	if snapRestoreSize != nil {
		if pvcRequestedCapacity == nil || snapRestoreSize.Cmp(*pvcRequestedCapacity) > 0 {
			pvcRequestedCapacity = snapRestoreSize
		}
	}

	pvcNeedsRecreation := false

	op, err := ctrlutil.CreateOrUpdate(v.ctx, v.client, pvc, func() error {
		if !pvc.CreationTimestamp.IsZero() && !objectRefMatches(pvc.Spec.DataSource, &snapshotRef) {
			// If this pvc already exists and not pointing to our desired snapshot, we will need to
			// delete it and re-create as we cannot update the datasource
			pvcNeedsRecreation = true

			return nil
		}
		if pvc.Status.Phase == corev1.ClaimBound {
			// PVC already bound at this point
			l.V(1).Info("PVC already bound")

			return nil
		}

		util.UpdateStringMap(&pvc.Labels, rdSpec.ProtectedPVC.Labels)
		util.UpdateStringMap(&pvc.Annotations, rdSpec.ProtectedPVC.Annotations)

		accessModes := []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce} // Default value
		if len(rdSpec.ProtectedPVC.AccessModes) > 0 {
			accessModes = rdSpec.ProtectedPVC.AccessModes
		}

		if pvc.CreationTimestamp.IsZero() { // set immutable fields
			pvc.Spec.AccessModes = accessModes
			pvc.Spec.StorageClassName = rdSpec.ProtectedPVC.StorageClassName

			// Only set when initially creating
			pvc.Spec.DataSource = &snapshotRef
		}

		pvc.Spec.Resources.Requests = corev1.ResourceList{
			corev1.ResourceStorage: *pvcRequestedCapacity,
		}

		return nil
	})
	if err != nil {
		l.Error(err, "Unable to createOrUpdate PVC from snapshot")

		return nil, fmt.Errorf("error creating or updating PVC from snapshot (%w)", err)
	}

	if pvcNeedsRecreation {
		needsRecreateErr := fmt.Errorf("pvc has incorrect datasource, will need to delete and recreate, pvc: %s",
			pvc.GetName())
		v.log.Error(needsRecreateErr, "Need to delete pvc (pvc restored from snapshot)")

		delErr := v.client.Delete(v.ctx, pvc)
		if delErr != nil {
			v.log.Error(delErr, "Error deleting pvc", "pvc name", pvc.GetName())
		}

		// Return error to indicate the ensurePVC should be attempted again
		return nil, needsRecreateErr
	}

	l.V(1).Info("PVC createOrUpdate Complete", "op", op)

	return pvc, nil
}

// Validates snapshot exists and adds VolSync "do-not-delete" label to indicate volsync should not cleanup this snapshot
func (v *VSHandler) validateSnapshotAndAddDoNotDeleteLabel(
	volumeSnapshotRef corev1.TypedLocalObjectReference,
) (*snapv1.VolumeSnapshot, error) {
	volSnap := &snapv1.VolumeSnapshot{}

	err := v.client.Get(v.ctx, types.NamespacedName{
		Name:      volumeSnapshotRef.Name,
		Namespace: v.owner.GetNamespace(),
	}, volSnap)
	if err != nil {
		v.log.Error(err, "Unable to get VolumeSnapshot", "volumeSnapshotRef", volumeSnapshotRef)

		return nil, fmt.Errorf("error getting volumesnapshot (%w)", err)
	}

	// Add label to indicate that VolSync should not delete/cleanup this snapshot
	labelsUpdated := addLabel(volSnap, VolSyncDoNotDeleteLabel, VolSyncDoNotDeleteLabelVal)
	if labelsUpdated {
		if err := v.client.Update(v.ctx, volSnap); err != nil {
			v.log.Error(err, "Failed to add label to snapshot",
				"snapshot name", volSnap.GetName(), "labelName", VolSyncDoNotDeleteLabel)

			return nil, fmt.Errorf("failed to add %s label to snapshot %s (%w)",
				VolSyncDoNotDeleteLabel, volSnap.GetName(), err)
		}

		v.log.Info("label added to snapshot", "snapshot name", volSnap.GetName(), "labelName", VolSyncDoNotDeleteLabel)
	}

	v.log.V(1).Info("VolumeSnapshot validated", "volumesnapshot name", volSnap.GetName())

	return volSnap, nil
}

func addLabel(obj client.Object, labelName, labelValue string) bool {
	return addLabelOrAnnotation(obj, labelName, labelValue, true)
}

func addAnnotation(obj client.Object, annotationName, annotationValue string) bool {
	return addLabelOrAnnotation(obj, annotationName, annotationValue, false)
}

func addLabelOrAnnotation(obj client.Object, name, value string, isLabel bool) bool {
	updated := false

	// Get current labels or annotations
	var mapToUpdate map[string]string
	if isLabel {
		mapToUpdate = obj.GetLabels()
	} else {
		mapToUpdate = obj.GetAnnotations()
	}

	if mapToUpdate == nil {
		mapToUpdate = map[string]string{}
	}

	val, ok := mapToUpdate[name]
	if !ok || val != value {
		mapToUpdate[name] = value

		updated = true

		if isLabel {
			obj.SetLabels(mapToUpdate)
		} else {
			obj.SetAnnotations(mapToUpdate)
		}
	}

	return updated
}

func (v *VSHandler) addOwnerReference(obj, owner metav1.Object) (bool, error) {
	currentOwnerRefs := obj.GetOwnerReferences()

	err := ctrlutil.SetOwnerReference(owner, obj, v.client.Scheme())
	if err != nil {
		return false, fmt.Errorf("%w", err)
	}

	needsUpdate := !reflect.DeepEqual(obj.GetOwnerReferences(), currentOwnerRefs)

	return needsUpdate, nil
}

func (v *VSHandler) addOwnerReferenceAndUpdate(obj client.Object, owner metav1.Object) error {
	needsUpdate, err := v.addOwnerReference(obj, owner)
	if err != nil {
		return err
	}

	if needsUpdate {
		v.updateResource(obj)
	}

	return nil
}

func (v *VSHandler) updateResource(obj client.Object) error {
	objKindAndName := getKindAndName(v.client.Scheme(), obj)

	if err := v.client.Update(v.ctx, obj); err != nil {
		v.log.Error(err, "Failed to update object", "obj", objKindAndName)

		return fmt.Errorf("failed to update object %s (%w)", objKindAndName, err)
	}

	v.log.Info("Updated object", "obj", objKindAndName)

	return nil
}

func (v *VSHandler) getRsyncServiceType() *corev1.ServiceType {
	// Use default right now - in future we may use a volsyncProfile
	return &DefaultRsyncServiceType
}

// Workaround for cephfs issue: FIXME:
// For CephFS only, there is a problem where restoring a PVC from snapshot can be very slow when there are a lot of
// files - on every replication cycle we need to create a PVC from snapshot in order to get a point-in-time copy of
// the source PVC to sync with the replicationdestination.
// This workaround follows the instructions here:
// https://github.com/ceph/ceph-csi/blob/devel/docs/cephfs-snapshot-backed-volumes.md
//
// Steps:
// 1. If the storageclass detected is cephfs, create a new storageclass with backingSnapshot: "true" parameter
// (or reuse if it already exists).  If not cephfs, return and do not modify rsSpec.
// 2. Modify rsSpec to use the new storageclass and also update AccessModes to 'ReadOnlyMany' as per the instructions
// above.
func (v *VSHandler) ModifyRSSpecForCephFS(rsSpec *ramendrv1alpha1.VolSyncReplicationSourceSpec,
	storageClass *storagev1.StorageClass,
) error {
	if storageClass.Provisioner != v.defaultCephFSCSIDriverName {
		return nil // No workaround required
	}

	v.log.Info("CephFS storageclass detected on source PVC, creating replicationsource with read-only "+
		" PVC from snapshot", "storageClassName", storageClass.GetName())

	// Create/update readOnlyPVCStorageClass
	readOnlyPVCStorageClass := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: storageClass.GetName() + "-vrg",
		},
	}

	op, err := ctrlutil.CreateOrUpdate(v.ctx, v.client, readOnlyPVCStorageClass, func() error {
		// Do not update the storageclass if it already exists - Provisioner and Parameters are immutable anyway
		if readOnlyPVCStorageClass.CreationTimestamp.IsZero() {
			readOnlyPVCStorageClass.Provisioner = storageClass.Provisioner

			// Copy other parameters from the original storage class
			readOnlyPVCStorageClass.Parameters = map[string]string{}
			for k, v := range storageClass.Parameters {
				readOnlyPVCStorageClass.Parameters[k] = v
			}

			// Set backingSnapshot parameter to true
			readOnlyPVCStorageClass.Parameters["backingSnapshot"] = "true"

			// Note - not copying volumebindingmode or reclaim policy from the source storageclass will leave defaults
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("%w", err)
	}

	v.log.Info("StorageClass for readonly cephfs PVC createOrUpdate Complete", "op", op)

	// Update the rsSpec with access modes and the special storageclass
	rsSpec.ProtectedPVC.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadOnlyMany}
	rsSpec.ProtectedPVC.StorageClassName = &readOnlyPVCStorageClass.Name

	return nil
}

func (v *VSHandler) GetVolumeSnapshotClassFromPVCStorageClass(storageClassName *string) (string, error) {
	storageClass, err := v.getStorageClass(storageClassName)
	if err != nil {
		return "", err
	}

	return v.getVolumeSnapshotClassFromPVCStorageClass(storageClass)
}

func (v *VSHandler) getVolumeSnapshotClassFromPVCStorageClass(storageClass *storagev1.StorageClass) (string, error) {
	volumeSnapshotClasses, err := v.GetVolumeSnapshotClasses()
	if err != nil {
		return "", err
	}

	var matchedVolumeSnapshotClassName string

	for _, volumeSnapshotClass := range volumeSnapshotClasses {
		if volumeSnapshotClass.Driver == storageClass.Provisioner {
			// Match the first one where driver/provisioner == the storage class provisioner
			// But keep looping - if we find the default storageVolumeClass, use it instead
			if matchedVolumeSnapshotClassName == "" || isDefaultVolumeSnapshotClass(volumeSnapshotClass) {
				matchedVolumeSnapshotClassName = volumeSnapshotClass.GetName()
			}
		}
	}

	if matchedVolumeSnapshotClassName == "" {
		noVSCFoundErr := fmt.Errorf("unable to find matching volumesnapshotclass for storage provisioner %s",
			storageClass.Provisioner)
		v.log.Error(noVSCFoundErr, "No VolumeSnapshotClass found")

		return "", noVSCFoundErr
	}

	return matchedVolumeSnapshotClassName, nil
}

func (v *VSHandler) getStorageClass(storageClassName *string) (*storagev1.StorageClass, error) {
	if storageClassName == nil || *storageClassName == "" {
		err := fmt.Errorf("no storageClassName given, cannot proceed")
		v.log.Error(err, "Failed to get StorageClass")

		return nil, err
	}

	storageClass := &storagev1.StorageClass{}
	if err := v.client.Get(v.ctx, types.NamespacedName{Name: *storageClassName}, storageClass); err != nil {
		v.log.Error(err, "Failed to get StorageClass", "name", storageClassName)

		return nil, fmt.Errorf("error getting storage class (%w)", err)
	}

	return storageClass, nil
}

func isDefaultVolumeSnapshotClass(volumeSnapshotClass snapv1.VolumeSnapshotClass) bool {
	isDefaultAnnotation, ok := volumeSnapshotClass.Annotations[VolumeSnapshotIsDefaultAnnotation]

	return ok && isDefaultAnnotation == VolumeSnapshotIsDefaultAnnotationValue
}

func (v *VSHandler) GetVolumeSnapshotClasses() ([]snapv1.VolumeSnapshotClass, error) {
	if v.volumeSnapshotClassList == nil {
		// Load the list if it hasn't been initialized yet
		v.log.Info("Fetching VolumeSnapshotClass", "labelSelector", v.volumeSnapshotClassSelector)

		selector, err := metav1.LabelSelectorAsSelector(&v.volumeSnapshotClassSelector)
		if err != nil {
			v.log.Error(err, "Unable to use volume snapshot label selector", "labelSelector",
				v.volumeSnapshotClassSelector)

			return nil, fmt.Errorf("unable to use volume snapshot label selector (%w)", err)
		}

		listOptions := []client.ListOption{
			client.MatchingLabelsSelector{
				Selector: selector,
			},
		}

		vscList := &snapv1.VolumeSnapshotClassList{}
		if err := v.client.List(v.ctx, vscList, listOptions...); err != nil {
			v.log.Error(err, "Failed to list VolumeSnapshotClasses", "labelSelector", v.volumeSnapshotClassSelector)

			return nil, fmt.Errorf("error listing volumesnapshotclasses (%w)", err)
		}

		v.volumeSnapshotClassList = vscList
	}

	return v.volumeSnapshotClassList.Items, nil
}

func (v *VSHandler) getScheduleCronSpec() (*string, error) {
	if v.schedulingInterval != "" {
		return ConvertSchedulingIntervalToCronSpec(v.schedulingInterval)
	}

	// Use default value if not specified
	v.log.Info("Warning - scheduling interval is empty, using default Schedule for volsync",
		"DefaultScheduleCronSpec", DefaultScheduleCronSpec)

	return &DefaultScheduleCronSpec, nil
}

// Convert from schedulingInterval which is in the format of <num><m,h,d>
// to the format VolSync expects, which is cronspec: https://en.wikipedia.org/wiki/Cron#Overview
func ConvertSchedulingIntervalToCronSpec(schedulingInterval string) (*string, error) {
	// format needs to have at least 1 number and end with m or h or d
	if len(schedulingInterval) < SchedulingIntervalMinLength {
		return nil, fmt.Errorf("scheduling interval %s is invalid", schedulingInterval)
	}

	mhd := schedulingInterval[len(schedulingInterval)-1:]
	mhd = strings.ToLower(mhd) // Make sure we get lowercase m, h or d

	num := schedulingInterval[:len(schedulingInterval)-1]

	numInt, err := strconv.Atoi(num)
	if err != nil {
		return nil, fmt.Errorf("scheduling interval prefix %s cannot be convered to an int value", num)
	}

	var cronSpec string

	switch mhd {
	case "m":
		cronSpec = fmt.Sprintf("*/%s * * * *", num)
	case "h":
		// TODO: cronspec has a max here of 23 hours - do we try to convert into days?
		cronSpec = fmt.Sprintf("0 */%s * * *", num)
	case "d":
		if numInt > CronSpecMaxDayOfMonth {
			// Max # of days in interval we'll allow is 28 - otherwise there are issues converting to a cronspec
			// which is expected to be a day of the month (1-31).  I.e. if we tried to set to */31 we'd get
			// every 31st day of the month
			num = "28"
		}

		cronSpec = fmt.Sprintf("0 0 */%s * *", num)
	}

	if cronSpec == "" {
		return nil, fmt.Errorf("scheduling interval %s is invalid. Unable to parse m/h/d", schedulingInterval)
	}

	return &cronSpec, nil
}

func (v *VSHandler) IsRSDataProtected(pvcName string) (bool, error) {
	l := v.log.WithValues("pvcName", pvcName)

	// Get RD instance
	rs := &volsyncv1alpha1.ReplicationSource{}

	err := v.client.Get(v.ctx,
		types.NamespacedName{
			Name:      getReplicationSourceName(pvcName),
			Namespace: v.owner.GetNamespace(),
		}, rs)
	if err != nil {
		if !kerrors.IsNotFound(err) {
			l.Error(err, "Failed to get ReplicationSource")

			return false, fmt.Errorf("%w", err)
		}

		l.Info("No ReplicationSource found", "pvcName", pvcName)

		return false, nil
	}

	return isRSLastSyncTimeReady(rs.Status), nil
}

func isRSLastSyncTimeReady(rsStatus *volsyncv1alpha1.ReplicationSourceStatus) bool {
	if rsStatus != nil && rsStatus.LastSyncTime != nil && !rsStatus.LastSyncTime.IsZero() {
		return true
	}

	return false
}

func (v *VSHandler) getRD(pvcName string) (*volsyncv1alpha1.ReplicationDestination, error) {
	l := v.log.WithValues("pvcName", pvcName)

	// Get RD instance
	rdInst := &volsyncv1alpha1.ReplicationDestination{}

	err := v.client.Get(v.ctx,
		types.NamespacedName{
			Name:      getReplicationDestinationName(pvcName),
			Namespace: v.owner.GetNamespace(),
		}, rdInst)
	if err != nil {
		if !kerrors.IsNotFound(err) {
			l.Error(err, "Failed to get ReplicationDestination")

			return nil, fmt.Errorf("error getting replicationdestination (%w)", err)
		}

		l.Info("No ReplicationDestination found")

		return nil, nil
	}

	return rdInst, nil
}

func (v *VSHandler) getRDLatestImage(pvcName string) (*corev1.TypedLocalObjectReference, error) {
	rdInst, err := v.getRD(pvcName)
	if err != nil {
		return nil, err
	} else if rdInst == nil {
		return nil, nil
	}

	var latestImage *corev1.TypedLocalObjectReference
	if rdInst.Status != nil {
		latestImage = rdInst.Status.LatestImage
	}

	return latestImage, nil
}

// Returns true if at least one sync has completed (we'll consider this "data protected")
func (v *VSHandler) IsRDDataProtected(pvcName string) (bool, error) {
	latestImage, err := v.getRDLatestImage(pvcName)
	if err != nil {
		return false, err
	}

	return isLatestImageReady(latestImage), nil
}

func (v *VSHandler) SelectDestCopyMethod(rdSpec ramendrv1alpha1.VolSyncReplicationDestinationSpec, log logr.Logger,
) (volsyncv1alpha1.CopyMethodType, *string, bool, error) {
	const requeue = true

	if v.IsCopyMethodSnapshot() {
		v.log.Info("Using copyMethod of Snapshot")

		return volsyncv1alpha1.CopyMethodSnapshot, nil, !requeue, nil // use default copyMethod
	} else if v.IsCopyMethodDirect() {
		v.log.Info("Using copyMethod of Direct")
		// IF using CopyMethodDirect, then ensure that the PVC exists, otherwise, create it.
		err := v.EnsurePVCforDirectCopy(v.ctx, log, rdSpec)
		if err != nil {
			return "", nil, requeue, err
		}

		// PVC must not be in-use before creating the RD
		inUse, err := v.isPVCInUseByNonRDPod(rdSpec.ProtectedPVC.Name)
		if err != nil {
			return "", nil, requeue, err
		}

		// It is possible that the PVC becomes in-use at this point (if an app using this PVC is also deployed
		// on this cluster). That race condition will be ignored. That would be a user error to deploy the
		// same app in the same namespace and on the destination cluster...
		if inUse {
			return "", nil, requeue, fmt.Errorf("pvc %v is mounted by others. Checking later", rdSpec.ProtectedPVC.Name)
		}

		v.log.Info(fmt.Sprintf("Using copyMethod of Direct with App PVC %s", rdSpec.ProtectedPVC.Name))
		// Using the application PVC for syncing from source to destination and save a snapshot
		// everytime a sync is successful
		return volsyncv1alpha1.CopyMethodSnapshot, &rdSpec.ProtectedPVC.Name, !requeue, nil
	} else if v.IsCopyMethodLocalDirect() {
		v.log.Info("Using copyMethod of LocalDirect")

		shouldRequeue, err := v.EnsurePVCforLocalDirectCopy(v.ctx, log, rdSpec)
		if err != nil {
			return "", nil, requeue, err
		}

		if shouldRequeue {
			return "", nil, requeue, nil
		}

		pvcName := BuildNameForMainDstPVC(rdSpec.ProtectedPVC.Name)

		return volsyncv1alpha1.CopyMethodSnapshot, &pvcName, !requeue, nil
	}

	return volsyncv1alpha1.CopyMethodSnapshot, nil, !requeue, nil // use default copyMethod
}

func (v *VSHandler) IsCopyMethodSnapshot() bool {
	return v.destinationCopyMethod == volsyncv1alpha1.CopyMethodSnapshot
}

func (v *VSHandler) IsCopyMethodDirect() bool {
	return v.destinationCopyMethod == volsyncv1alpha1.CopyMethodDirect
}

func (v *VSHandler) IsCopyMethodLocalDirect() bool {
	return v.destinationCopyMethod == CopyMethodLocalDirect
}

func (v *VSHandler) IsCopyMethodDirectOrLocalDirect() bool {
	return v.IsCopyMethodDirect() || v.IsCopyMethodLocalDirect()
}

func isLatestImageReady(latestImage *corev1.TypedLocalObjectReference) bool {
	if latestImage == nil || latestImage.Name == "" || latestImage.Kind != VolumeSnapshotKind {
		return false
	}

	return true
}

func addVRGOwnerLabel(owner, obj metav1.Object) {
	// Set vrg label to owner name - enables lookups by owner label
	labels := obj.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}

	labels[VRGOwnerLabel] = owner.GetName()
	obj.SetLabels(labels)
}

func getReplicationDestinationName(pvcName string) string {
	return pvcName // Use PVC name as name of ReplicationDestination
}

func getReplicationSourceName(pvcName string) string {
	return pvcName // Use PVC name as name of ReplicationSource
}

func getLocalReplicationName(pvcName string) string {
	return pvcName + "-local" // Use PVC name as name plus -local for local RD and RS
}

// Service name that VolSync will create locally in the same namespace as the ReplicationDestination
func getLocalServiceNameForRDFromPVCName(pvcName string) string {
	return getLocalServiceNameForRD(getReplicationDestinationName(pvcName))
}

func getLocalServiceNameForRD(rdName string) string {
	// This is the name VolSync will use for the service
	return fmt.Sprintf("volsync-rsync-tls-dst-%s", rdName)
}

// This is the remote service name that can be accessed from another cluster.  This assumes submariner and that
// a ServiceExport is created for the service on the cluster that has the ReplicationDestination
func getRemoteServiceNameForRDFromPVCName(pvcName, rdNamespace string) string {
	return fmt.Sprintf("%s.%s.svc.clusterset.local", getLocalServiceNameForRDFromPVCName(pvcName), rdNamespace)
}

// BuildNameForMainDstPVC transforms a given PVC name into a new name following a specific format. Its primary purpose is
// to create a distinct name for a new PVC. This new PVC is main destinationPVC for the ReplicationDestination.
func BuildNameForMainDstPVC(pvcName string) string {
	return fmt.Sprintf("vs-%s-main-dst-ld", pvcName)
}

func getKindAndName(scheme *runtime.Scheme, obj client.Object) string {
	ref, err := reference.GetReference(scheme, obj)
	if err != nil {
		return obj.GetName()
	}

	return ref.Kind + "/" + ref.Name
}

func objectRefMatches(a, b *corev1.TypedLocalObjectReference) bool {
	if a == nil {
		return b == nil
	}

	if b == nil {
		return false
	}

	return a.Kind == b.Kind && a.Name == b.Name
}

// ValidateObjectExists indicates whether a kubernetes resource exists in APIServer
func ValidateObjectExists(ctx context.Context, c client.Client, obj client.Object) error {
	key := client.ObjectKeyFromObject(obj)
	if err := c.Get(ctx, key, obj); err != nil {
		if kerrors.IsNotFound(err) {
			// PVC not found. Should we restore automatically from snapshot?
			return fmt.Errorf("PVC %s not found", key.Name)
		}

		return fmt.Errorf("failed to fetch application PVC %s - (%w)", key.Name, err)
	}

	return nil
}

func (v *VSHandler) reconcileLocalReplication(rd *volsyncv1alpha1.ReplicationDestination,
	rdSpec ramendrv1alpha1.VolSyncReplicationDestinationSpec,
	pskSecretName string, l logr.Logger) (*volsyncv1alpha1.ReplicationDestination,
	*volsyncv1alpha1.ReplicationSource, error,
) {
	lrd, err := v.reconcileLocalRD(rdSpec, pskSecretName)
	if lrd == nil || err != nil {
		return nil, nil, err
	}

	rsSpec := &ramendrv1alpha1.VolSyncReplicationSourceSpec{
		ProtectedPVC: rdSpec.ProtectedPVC,
	}

	lrs, err := v.reconcileLocalRS(rd, rsSpec, pskSecretName, *lrd.Status.RsyncTLS.Address)
	if err != nil || lrs == nil {
		return lrd, nil, err
	}

	l.V(1).Info(fmt.Sprintf("Local ReplicationDestination Reconcile Complete lrd=%s,lrs=%s", lrd.Name, lrs.Name))

	return lrd, lrs, nil
}

func (v *VSHandler) reconcileLocalRD(rdSpec ramendrv1alpha1.VolSyncReplicationDestinationSpec,
	pskSecretName string) (*volsyncv1alpha1.ReplicationDestination, error,
) {
	l := v.log.WithValues("rdSpec", rdSpec)

	lrd := &volsyncv1alpha1.ReplicationDestination{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getLocalReplicationName(rdSpec.ProtectedPVC.Name),
			Namespace: v.owner.GetNamespace(),
		},
	}

	err := v.EnsurePVCforDirectCopy(v.ctx, l, rdSpec)
	if err != nil {
		return nil, err
	}

	op, err := ctrlutil.CreateOrUpdate(v.ctx, v.client, lrd, func() error {
		if err := ctrl.SetControllerReference(v.owner, lrd, v.client.Scheme()); err != nil {
			l.Error(err, "unable to set controller reference")

			return err
		}

		addVRGOwnerLabel(v.owner, lrd)
		addLabel(lrd, VolSyncDoNotDeleteLabel, VolSyncDoNotDeleteLabelVal)

		pvcAccessModes := []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce} // Default value
		if len(rdSpec.ProtectedPVC.AccessModes) > 0 {
			pvcAccessModes = rdSpec.ProtectedPVC.AccessModes
		}

		lrd.Spec.RsyncTLS = &volsyncv1alpha1.ReplicationDestinationRsyncTLSSpec{
			ServiceType: v.getRsyncServiceType(),
			KeySecret:   &pskSecretName,

			ReplicationDestinationVolumeOptions: volsyncv1alpha1.ReplicationDestinationVolumeOptions{
				CopyMethod:       volsyncv1alpha1.CopyMethodDirect,
				Capacity:         rdSpec.ProtectedPVC.Resources.Requests.Storage(),
				StorageClassName: rdSpec.ProtectedPVC.StorageClassName,
				AccessModes:      pvcAccessModes,
				DestinationPVC:   &rdSpec.ProtectedPVC.Name,
			},
		}

		return nil
	})
	if err != nil {
		return nil, err
	}
	// Now check status - only return an RD if we have an address filled out in the ReplicationDestination Status
	if lrd.Status == nil || lrd.Status.RsyncTLS == nil || lrd.Status.RsyncTLS.Address == nil {
		l.V(1).Info("Local ReplicationDestination waiting for Address ...")

		return nil, nil
	}

	l.V(1).Info("Local ReplicationDestination Reconcile Complete", "op", op)

	return lrd, nil
}

//nolint:funlen
func (v *VSHandler) reconcileLocalRS(rd *volsyncv1alpha1.ReplicationDestination,
	rsSpec *ramendrv1alpha1.VolSyncReplicationSourceSpec,
	pskSecretName, address string,
) (*volsyncv1alpha1.ReplicationSource, error,
) {
	storageClass, err := v.getStorageClass(rsSpec.ProtectedPVC.StorageClassName)
	if err != nil {
		return nil, err
	}

	volumeSnapshotClassName, err := v.getVolumeSnapshotClassFromPVCStorageClass(storageClass)
	if err != nil {
		return nil, err
	}

	// Fix for CephFS (replication source only) - may need different storageclass and access modes
	err = v.ModifyRSSpecForCephFS(rsSpec, storageClass)
	if err != nil {
		return nil, err
	}

	pvc, err := v.setupLocalRS(rd)
	if err != nil || pvc == nil {
		return nil, err
	}

	lrs := &volsyncv1alpha1.ReplicationSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getLocalReplicationName(rsSpec.ProtectedPVC.Name),
			Namespace: v.owner.GetNamespace(),
		},
	}

	op, err := ctrlutil.CreateOrUpdate(v.ctx, v.client, lrs, func() error {
		if err := ctrl.SetControllerReference(v.owner, lrs, v.client.Scheme()); err != nil {
			v.log.Error(err, "unable to set controller reference")

			return err
		}

		addVRGOwnerLabel(v.owner, lrs)

		lrs.Spec.Trigger = &volsyncv1alpha1.ReplicationSourceTriggerSpec{
			Manual: pvc.GetName(),
		}

		lrs.Spec.SourcePVC = pvc.GetName()
		lrs.Spec.RsyncTLS = &volsyncv1alpha1.ReplicationSourceRsyncTLSSpec{
			KeySecret: &pskSecretName,
			Address:   &address,

			ReplicationSourceVolumeOptions: volsyncv1alpha1.ReplicationSourceVolumeOptions{
				CopyMethod:              volsyncv1alpha1.CopyMethodDirect,
				VolumeSnapshotClassName: &volumeSnapshotClassName,
				StorageClassName:        rsSpec.ProtectedPVC.StorageClassName,
				AccessModes:             rsSpec.ProtectedPVC.AccessModes,
			},
		}

		return nil
	})

	v.log.V(1).Info("Local ReplicationSource createOrUpdate Complete", "op", op, "error", err)

	if err != nil {
		return nil, err
	}

	// Add lrs as owner to the RO PVC
	if err := v.addOwnerReferenceAndUpdate(pvc, lrs); err != nil {
		v.log.Error(err, "Unable to update owner", "pvcName", pvc.GetName())

		return lrs, err
	}

	return lrs, nil
}

func (v *VSHandler) cleanupLocalResources(lrs *volsyncv1alpha1.ReplicationSource) error {
	// delete RO PVC created for localRS
	err := util.DeletePVC(v.ctx, v.client, lrs.Spec.SourcePVC, v.owner.GetNamespace(), v.log)
	if err != nil {
		return err
	}

	// delete localRS
	if err := v.client.Delete(v.ctx, lrs); err != nil {
		return err
	}

	// Delete the localRD. The name of the localRD is the same as the name of the localRS
	return v.deleteLocalRD(lrs.GetName())
}

func (v *VSHandler) deleteLocalRD(lrdName string) error {
	lrd := &volsyncv1alpha1.ReplicationDestination{
		ObjectMeta: metav1.ObjectMeta{
			Name:      lrdName,
			Namespace: v.owner.GetNamespace(),
		},
	}

	err := v.client.Get(v.ctx, types.NamespacedName{
		Name:      lrd.Name,
		Namespace: lrd.Namespace,
	}, lrd)
	if err != nil {
		if kerrors.IsNotFound(err) {
			return nil
		}

		return err
	}

	return v.client.Delete(v.ctx, lrd)
}

func (v *VSHandler) setupLocalRS(rd *volsyncv1alpha1.ReplicationDestination,
) (*corev1.PersistentVolumeClaim, error) {
	latestImage, err := v.getRDLatestImage(rd.GetName())
	if err != nil {
		return nil, err
	}

	if !isLatestImageReady(latestImage) {
		v.log.V(1).Info(fmt.Sprintf("unable to find LatestImage from ReplicationDestination %s", rd.GetName()))

		return nil, nil
	}

	// Make copy of the ref and make sure API group is filled out correctly (shouldn't really need this part)
	vsImageRef := latestImage.DeepCopy()
	if vsImageRef.APIGroup == nil || *vsImageRef.APIGroup == "" {
		vsGroup := snapv1.GroupName
		vsImageRef.APIGroup = &vsGroup
	}

	v.log.V(1).Info("Latest Image for ReplicationDestination to be used by LocalRS", "latestImage	", vsImageRef)

	lrs := &volsyncv1alpha1.ReplicationSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getLocalReplicationName(rd.GetName()),
			Namespace: v.owner.GetNamespace(),
		},
	}

	err = v.client.Get(v.ctx, types.NamespacedName{
		Name:      lrs.Name,
		Namespace: lrs.Namespace,
	}, lrs)
	if err != nil {
		if !kerrors.IsNotFound(err) {
			v.log.Error(err, "Unable to get Local ReplicationSource", "LocalRS", lrs)

			return nil, fmt.Errorf("error getting Local ReplicationSource (%w)", err)
		}
	}

	err = v.cleanupPreviousTransferResources(lrs, vsImageRef.Name)
	if err != nil {
		return nil, err
	}

	snap, err := v.validateSnapshotAndAddDoNotDeleteLabel(*vsImageRef)
	if err != nil {
		return nil, err
	}

	var restoreSize *resource.Quantity

	if snap.Status != nil {
		restoreSize = snap.Status.RestoreSize
	}

	// In all other cases, we have to create a RO PVC.
	return v.createReadOnlyPVCFromSnapshot(rd, *latestImage, restoreSize)
}

//nolint:gocognit,nestif
func (v *VSHandler) cleanupPreviousTransferResources(lrs *volsyncv1alpha1.ReplicationSource,
	snapImageName string,
) error {
	if lrs.Spec.Trigger != nil && lrs.Spec.Trigger.Manual != snapImageName {
		// Only clean up and create new PVC if the previous transfer has completed. We don't want to abort it.
		if lrs.Spec.Trigger.Manual != "" {
			if lrs.Status != nil && lrs.Status.LastManualSync == lrs.Spec.Trigger.Manual {
				err := v.deleteSnapshot(v.ctx, v.client, lrs.Spec.Trigger.Manual, v.owner.GetNamespace(), v.log)
				if err != nil {
					return err
				}

				err = util.DeletePVC(v.ctx, v.client, lrs.Spec.SourcePVC, v.owner.GetNamespace(), v.log)
				if err != nil {
					return err
				}
			} else { // don't process any further. We have to wait for the previous transfer to complete
				return fmt.Errorf("wait for previous localRS tranfter to complete - RS/PVC %s/%s",
					lrs.GetName(), lrs.Spec.SourcePVC)
			}
		}
	}

	return nil
}

func (v *VSHandler) createReadOnlyPVCFromSnapshot(rd *volsyncv1alpha1.ReplicationDestination,
	snapshotRef corev1.TypedLocalObjectReference, snapRestoreSize *resource.Quantity,
) (*corev1.PersistentVolumeClaim, error) {
	l := v.log.WithValues("pvcName", rd.GetName(), "snapshotRef", snapshotRef,
		"snapRestoreSize", snapRestoreSize)

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      snapshotRef.Name, // Use the snapshot name as RO name for the PVC
			Namespace: v.owner.GetNamespace(),
		},
	}

	pvcRequestedCapacity := rd.Spec.RsyncTLS.Capacity
	if snapRestoreSize != nil {
		if pvcRequestedCapacity == nil || snapRestoreSize.Cmp(*pvcRequestedCapacity) > 0 {
			pvcRequestedCapacity = snapRestoreSize
		}
	}

	op, err := ctrlutil.CreateOrUpdate(v.ctx, v.client, pvc, func() error {
		if pvc.Status.Phase == corev1.ClaimBound {
			// PVC already bound at this point
			l.V(1).Info("PVC already bound")

			return nil
		}

		accessModes := []corev1.PersistentVolumeAccessMode{corev1.ReadOnlyMany}

		if pvc.CreationTimestamp.IsZero() { // set immutable fields
			pvc.Spec.AccessModes = accessModes
			pvc.Spec.StorageClassName = rd.Spec.RsyncTLS.StorageClassName

			// Only set when initially creating
			pvc.Spec.DataSource = &snapshotRef
		}

		pvc.Spec.Resources.Requests = corev1.ResourceList{
			corev1.ResourceStorage: *pvcRequestedCapacity,
		}

		return nil
	})
	if err != nil {
		l.Error(err, "Unable to createOrUpdate PVC from snapshot for localRS")

		return nil, fmt.Errorf("error creating or updating PVC from snapshot for localRS (%w)", err)
	}

	l.V(1).Info("PVC for localRS createOrUpdate Complete", "op", op)

	return pvc, nil
}

func (v *VSHandler) deleteSnapshot(ctx context.Context,
	k8sClient client.Client,
	snapshotName, namespace string,
	log logr.Logger,
) error {
	volSnap := &snapv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      snapshotName,
			Namespace: namespace,
		},
	}

	err := k8sClient.Delete(ctx, volSnap)
	if err != nil {
		if !kerrors.IsNotFound(err) {
			log.Error(err, "error deleting snapshot", "snapshotName", snapshotName)

			return fmt.Errorf("error deleting pvc (%w)", err)
		}
	} else {
		log.Info("deleted snapshot", "snapshotName", snapshotName)
	}

	return nil
}

// RemoveFinalizer removes object finalizer from the resource and persists the change
func RemoveFinalizer(ctx context.Context, client client.Client, o client.Object, finalizer string) error {
	finalizerUpdated := controllerutil.RemoveFinalizer(o, finalizer)

	if !finalizerUpdated {
		return nil
	}

	if err := client.Update(ctx, o); err != nil {
		return fmt.Errorf("failed to remove finalizer from object (%s/%s), %w",
			o.GetNamespace(), o.GetName(), err)
	}

	return nil
}

// ensureLastSnapSyncedLocally ensures that the last snap is synced locally, the main RD is paused,
// and the app PVC is not in use
func (v *VSHandler) ensureLastSnapSyncedLocally(pvcName string,
	rdSpec ramendrv1alpha1.VolSyncReplicationDestinationSpec,
	snapshotRef corev1.TypedLocalObjectReference,
) error {
	if v.IsCopyMethodLocalDirect() {
		rd, err := v.getRD(pvcName)
		if err != nil {
			return err
		}

		if err := v.checkSyncStatusAndPauseSyncFromPeerSrc(rd, rdSpec, snapshotRef); err != nil {
			return err
		}

		if err := v.handleLastSnapshotSyncAndPVCUsage(pvcName, v.owner.GetNamespace(), rd.GetName(), snapshotRef); err != nil {
			return err
		}
	}

	return nil
}

// checkSyncStatusAndPauseSyncFromPeerSrc checks the sync status of the last complete snapshot and pauses
// any further syncs beyond the last complete snapshot, ensuring no additional snaps are generated.
func (v *VSHandler) checkSyncStatusAndPauseSyncFromPeerSrc(rd *volsyncv1alpha1.ReplicationDestination,
	rdSpec ramendrv1alpha1.VolSyncReplicationDestinationSpec,
	snapshotRef corev1.TypedLocalObjectReference,
) error {
	if rd == nil {
		return fmt.Errorf("RD %s object is nil. Waiting for PVC to not be in use...", rdSpec.ProtectedPVC.Name)
	}

	v.log.V(1).Info(fmt.Sprintf("Check if last snap needs to be synced - rd: %s, snap %s", rd.GetName(), snapshotRef.Name))

	started, _, err := v.checkLastSnapshotSyncStatus(rd.GetName(), snapshotRef)
	if err != nil {
		return err
	}

	if !started || !rd.Spec.Paused {
		v.log.V(1).Info(fmt.Sprintf("Last snap %s for rd %s not started/inprogress sync locally yet. Forcing RD to reconcile...",
			snapshotRef.Name, rd.GetName()))
		_, _, err := v.ReconcileRD(rdSpec, true)
		if err != nil {
			return err
		}
		return WaitingForNotInUsePVC
	}

	return nil
}

// handleLastSnapshotSyncAndPVCUsage function is responsible for checking the sync status of the last snap,
// checking whether the PVC is currently in use by a replication object, and, if in use, initiating the
// deletion of the local replication objects.
func (v *VSHandler) handleLastSnapshotSyncAndPVCUsage(pvcName, pvcNamespace, rdName string,
	snapshotRef corev1.TypedLocalObjectReference,
) error {
	v.log.V(1).Info(fmt.Sprintf("Check if manual sync is complete - rd: %s", rdName))
	_, completed, err := v.checkLastSnapshotSyncStatus(rdName, snapshotRef)
	if err != nil {
		return err
	}

	if !completed {
		v.log.V(1).Info(fmt.Sprintf("local sync is still in progress for pvc %s. Waiting...", pvcName))
		return WaitingForNotInUsePVC
	}

	// Make sure the PVC is not used by any pod, otherwise, force the local resources to clean up
	inUseByPod, err := util.IsPVCInUseByPod(v.ctx, v.client, v.log, pvcName, pvcNamespace, false)
	if err != nil {
		return err
	}

	// If the localRD is still starting up, the inUseByPod might not be accurate.
	// Need to check if the localRS has completed sync as well
	if inUseByPod {
		// If the PVC is in use, we need to delete local resources. We need the PVC to let go. We no longer need local resources
		v.log.V(1).Info(fmt.Sprintf("Delete local resources - rd: %s", rdName))
		err = v.deleteLocalRDAndRS(rdName, snapshotRef)
		if err != nil {
			return err
		}

		v.log.V(1).Info(fmt.Sprintf("Done with sync and cleanup for pvc %s. Rechecking...", pvcName))
		return WaitingForNotInUsePVC
	}

	return nil
}

// checkLastSnapshotSyncStatus checks the sync status of the last snapshot and returns two boolean values:
// one indicating whether the sync has started, and the other indicating whether the sync has completed successfully.
func (v *VSHandler) checkLastSnapshotSyncStatus(rdName string, snapshotRef corev1.TypedLocalObjectReference,
) (bool, bool, error) {
	lrs := &volsyncv1alpha1.ReplicationSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getLocalReplicationName(rdName), // rdName is also the pvc name
			Namespace: v.owner.GetNamespace(),
		},
	}

	err := v.client.Get(v.ctx, types.NamespacedName{
		Name:      lrs.GetName(),
		Namespace: lrs.GetNamespace(),
	}, lrs)
	if err != nil {
		if kerrors.IsNotFound(err) {
			return true, true, nil
		}

		return false, false, err
	}

	const started = true
	const completed = true

	v.log.V(1).Info("Local RS trigger", "trigger", lrs.Spec.Trigger, "snapName", snapshotRef.Name)
	// For LocalDirect, localRS trigger must point to the latest RD snapshot image. Otherwise,
	// we wait for local final sync to take place first befor cleaning up.
	if lrs.Spec.Trigger != nil && lrs.Spec.Trigger.Manual == snapshotRef.Name {
		// When local final sync is complete, we cleanup all locally created resources except the app PVC
		if lrs.Status != nil && lrs.Status.LastManualSync == lrs.Spec.Trigger.Manual {
			return started, completed, nil
		}

		return started, !completed, nil
	}

	return !started, !completed, nil
}

func (v *VSHandler) updateFinalizerAndOwnerForLocalDirect(pvcName string, rs *volsyncv1alpha1.ReplicationSource,
) error {
	pvc, err := v.getPVC(pvcName)
	if err != nil {
		return err
	}

	// Add Finalizer. For now, add finalizer only if dest copyMethod is LocaDirect
	finalizerUpdated := util.AddFinalizer(pvc, VolSyncFinalizerName)

	ownerUpdated := false
	ownerFound := false
	// find current owner
	for _, ownerRef := range pvc.ObjectMeta.OwnerReferences {
		if ownerRef.UID == rs.GetObjectMeta().GetUID() {
			ownerFound = true

			break
		}
	}

	if !ownerFound || len(pvc.ObjectMeta.OwnerReferences) > 1 {
		// Reset Owners before adding the RS as the owner
		pvc.SetOwnerReferences([]metav1.OwnerReference{})
		ownerUpdated, err = v.addOwnerReference(pvc, rs)
		if err != nil {
			return err
		}
	}

	if finalizerUpdated || ownerUpdated {
		err = v.updateResource(pvc)
		if err != nil {
			return err
		}
	}

	return nil
}
