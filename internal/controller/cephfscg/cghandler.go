// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package cephfscg

import (
	"context"
	"fmt"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	"github.com/go-logr/logr"
	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/util"
	"github.com/ramendr/ramen/internal/controller/volsync"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func NewVSCGHandler(
	ctx context.Context,
	k8sClient client.Client,
	instance *ramendrv1alpha1.VolumeReplicationGroup,
	volumeGroupSnapshotSource *metav1.LabelSelector,
	vsHandler *volsync.VSHandler,
	cgName string,
	logger logr.Logger,
) VSCGHandler {
	log := logger.WithName("VSCGHandler")

	cgHandler := &cgHandler{
		ctx:                       ctx,
		Client:                    k8sClient,
		instance:                  instance,
		VSHandler:                 vsHandler,
		volumeGroupSnapshotSource: volumeGroupSnapshotSource,
		cgName:                    cgName,
		logger:                    log,
	}

	log.Info("Set Async config")

	cgHandler.volumeSnapshotClassSelector = instance.Spec.Async.VolumeSnapshotClassSelector
	cgHandler.ramenSchedulingInterval = instance.Spec.Async.SchedulingInterval
	cgHandler.volumeGroupSnapshotClassSelector = instance.Spec.Async.VolumeGroupSnapshotClassSelector

	return cgHandler
}

type VSCGHandler interface {
	CreateOrUpdateReplicationGroupDestination(
		replicationGroupDestinationName, replicationGroupDestinationNamespace string,
		rdSpecsInGroup []ramendrv1alpha1.VolSyncReplicationDestinationSpec,
	) (*ramendrv1alpha1.ReplicationGroupDestination, error)

	CreateOrUpdateReplicationGroupSource(
		replicationGroupSourceNamespace string,
		runFinalSync bool,
	) (*ramendrv1alpha1.ReplicationGroupSource, bool, error)

	GetLatestImageFromRGD(
		rgd *ramendrv1alpha1.ReplicationGroupDestination, pvcName string,
	) (*corev1.TypedLocalObjectReference, error)

	EnsurePVCfromRGD(
		rdSpec ramendrv1alpha1.VolSyncReplicationDestinationSpec, failoverAction bool,
	) error

	DeleteLocalRDAndRS(rd *volsyncv1alpha1.ReplicationDestination) error
	GetRDInCG() ([]ramendrv1alpha1.VolSyncReplicationDestinationSpec, error)
	CheckIfPVCMatchLabel(pvcLabels map[string]string) (bool, error)
}

type cgHandler struct {
	ctx context.Context
	client.Client

	instance  *ramendrv1alpha1.VolumeReplicationGroup
	VSHandler *volsync.VSHandler // VSHandler will be used to call the exist funcs

	volumeGroupSnapshotSource        *metav1.LabelSelector
	volumeSnapshotClassSelector      metav1.LabelSelector
	volumeGroupSnapshotClassSelector metav1.LabelSelector
	ramenSchedulingInterval          string

	cgName string

	logger logr.Logger
}

func (c *cgHandler) CreateOrUpdateReplicationGroupDestination(
	replicationGroupDestinationName, replicationGroupDestinationNamespace string,
	rdSpecsInGroup []ramendrv1alpha1.VolSyncReplicationDestinationSpec,
) (*ramendrv1alpha1.ReplicationGroupDestination, error) {
	replicationGroupDestinationName = c.cgName

	log := c.logger.WithName("CreateOrUpdateReplicationGroupDestination").
		WithValues("ReplicationGroupDestinationName", replicationGroupDestinationName,
			"ReplicationGroupDestinationNamespace", replicationGroupDestinationNamespace)

	if err := util.DeleteReplicationGroupSource(c.ctx, c.Client,
		replicationGroupDestinationName, replicationGroupDestinationNamespace); err != nil {
		log.Error(err, "Failed to delete ReplicationGroupSource before creating ReplicationGroupDestination")

		return nil, err
	}

	rgd := &ramendrv1alpha1.ReplicationGroupDestination{
		ObjectMeta: metav1.ObjectMeta{
			Name:      replicationGroupDestinationName,
			Namespace: replicationGroupDestinationNamespace,
		},
	}

	util.AddLabel(rgd, util.CreatedByRamenLabel, "true")
	util.AddLabel(rgd, util.ExcludeFromVeleroBackup, "true")

	_, err := ctrlutil.CreateOrUpdate(c.ctx, c.Client, rgd, func() error {
		if c.VSHandler != nil && !c.VSHandler.IsVRGInAdminNamespace() {
			if err := ctrl.SetControllerReference(c.VSHandler.GetOwner(), rgd, c.Client.Scheme()); err != nil {
				return fmt.Errorf("unable to set controller reference %w", err)
			}
		}

		util.AddLabel(rgd, volsync.VRGOwnerNameLabel, c.instance.GetName())
		util.AddLabel(rgd, volsync.VRGOwnerNamespaceLabel, c.instance.GetNamespace())
		util.AddAnnotation(rgd, volsync.OwnerNameAnnotation, c.instance.GetName())
		util.AddAnnotation(rgd, volsync.OwnerNamespaceAnnotation, c.instance.GetNamespace())

		rgd.Spec.VolumeSnapshotClassSelector = c.volumeSnapshotClassSelector
		rgd.Spec.RDSpecs = rdSpecsInGroup

		return nil
	})
	if err != nil {
		log.Error(err, "Failed to create or update ReplicationGroupDestination")

		return nil, err
	}

	return rgd, nil
}

//nolint:funlen,gocognit,cyclop,gocyclo
func (c *cgHandler) CreateOrUpdateReplicationGroupSource(
	replicationGroupSourceNamespace string,
	runFinalSync bool,
) (*ramendrv1alpha1.ReplicationGroupSource, bool, error) {
	replicationGroupSourceName := c.cgName

	const finalSyncComplete = true

	log := c.logger.WithName("CreateOrUpdateRGS").
		WithValues("RGSName", replicationGroupSourceName,
			"RGSNamespace", replicationGroupSourceNamespace,
			"runFinalSync", runFinalSync)

	log.Info("Get RDs which are owned by RGD", "RGD", replicationGroupSourceName)
	// Get the rd if it exist when change secondary to primary
	rdList := &volsyncv1alpha1.ReplicationDestinationList{}
	if err := c.ListByOwner(rdList,
		map[string]string{util.RGDOwnerLabel: replicationGroupSourceName}, replicationGroupSourceNamespace,
	); err != nil {
		log.Error(err, "Failed to get RDs which are owned by RGD", "RGD", replicationGroupSourceName)

		return nil, !finalSyncComplete, err
	}

	for i := range rdList.Items {
		rd := rdList.Items[i]
		if c.VSHandler.IsCopyMethodDirect() {
			log.Info("Delete local RD and RS if they exists for RD", "RD", rd)

			err := c.DeleteLocalRDAndRS(&rd)
			if err != nil {
				log.Error(err, "Failed to delete local RD and RS for RD", "RD", rd)

				return nil, !finalSyncComplete, err
			}
		}
	}

	if err := util.DeleteReplicationGroupDestination(
		c.ctx, c.Client,
		replicationGroupSourceName, replicationGroupSourceNamespace); err != nil {
		log.Error(err, "Failed to delete ReplicationGroupDestination before creating ReplicationGroupSource")

		return nil, !finalSyncComplete, err
	}

	namespaces := []string{c.instance.Namespace}
	if c.instance.Spec.ProtectedNamespaces != nil && len(*c.instance.Spec.ProtectedNamespaces) != 0 {
		namespaces = *c.instance.Spec.ProtectedNamespaces
	}

	volumeGroupSnapshotClassName, err := util.GetVolumeGroupSnapshotClassFromPVCsStorageClass(c.ctx, c.Client,
		c.volumeGroupSnapshotClassSelector, *c.volumeGroupSnapshotSource, namespaces, c.logger)
	if err != nil {
		log.Error(err, "Failed to get VGSClass name")
		// If final sync is requested, ensure final sync cleanup is run regardless of the error
		if runFinalSync {
			return c.cleanupFinalSyncIfComplete(replicationGroupSourceNamespace)
		}

		return nil, !finalSyncComplete, err
	}

	// If final sync is requested, ensure prerequisites are met
	if runFinalSync {
		log.Info("Running final sync for ReplicationGroupSource")
		// Handle final sync for each PVC by retaining its PV and creating a temporary PVC
		// used exclusively for the final synchronization process.
		requeue, err := c.ensureFinalSyncSetup(replicationGroupSourceName, replicationGroupSourceNamespace, log)
		if err != nil {
			log.Error(err, "Failed to process temporary PVCs for final sync")

			return nil, !finalSyncComplete, err
		}

		if requeue {
			log.Info("Requeuing to allow temporary PVCs for final sync to be setup")

			return nil, !finalSyncComplete, nil
		}
	}

	rgs := &ramendrv1alpha1.ReplicationGroupSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      replicationGroupSourceName,
			Namespace: replicationGroupSourceNamespace,
		},
	}

	util.AddLabel(rgs, util.CreatedByRamenLabel, "true")
	util.AddLabel(rgs, util.ExcludeFromVeleroBackup, "true")

	_, err = ctrlutil.CreateOrUpdate(c.ctx, c.Client, rgs, func() error {
		if c.VSHandler != nil && !c.VSHandler.IsVRGInAdminNamespace() {
			if err := ctrl.SetControllerReference(c.VSHandler.GetOwner(), rgs, c.Client.Scheme()); err != nil {
				return fmt.Errorf("unable to set controller reference %w", err)
			}
		}

		util.AddLabel(rgs, volsync.VRGOwnerNameLabel, c.instance.GetName())
		util.AddLabel(rgs, volsync.VRGOwnerNamespaceLabel, c.instance.GetNamespace())
		util.AddAnnotation(rgs, volsync.OwnerNameAnnotation, c.instance.GetName())
		util.AddAnnotation(rgs, volsync.OwnerNamespaceAnnotation, c.instance.GetNamespace())

		if runFinalSync {
			rgs.Spec.Trigger = &ramendrv1alpha1.ReplicationSourceTriggerSpec{
				Manual: volsync.FinalSyncTriggerString,
			}
		} else {
			scheduleCronSpec := &volsync.DefaultScheduleCronSpec

			if c.ramenSchedulingInterval != "" {
				var err error

				scheduleCronSpec, err = volsync.ConvertSchedulingIntervalToCronSpec(c.ramenSchedulingInterval)
				if err != nil {
					return err
				}
			}

			rgs.Spec.Trigger = &ramendrv1alpha1.ReplicationSourceTriggerSpec{
				Schedule: scheduleCronSpec,
			}
		}

		rgs.Spec.VolumeGroupSnapshotClassName = volumeGroupSnapshotClassName
		rgs.Spec.VolumeGroupSnapshotSource = c.volumeGroupSnapshotSource

		return nil
	})
	if err != nil {
		log.Error(err, "Failed to create or update ReplicationGroupSource")

		return nil, !finalSyncComplete, err
	}
	//
	// For final sync only - check status to make sure the final sync is complete
	// and also run cleanup of temporary resources
	if runFinalSync && isFinalSyncComplete(rgs) {
		log.Info("ReplicationGroupSource complete final sync")

		return rgs, finalSyncComplete, c.ensureFinalSyncCleanup(rgs)
	}

	return rgs, !finalSyncComplete, nil
}

func (c *cgHandler) GetLatestImageFromRGD(rgd *ramendrv1alpha1.ReplicationGroupDestination, pvcName string,
) (*corev1.TypedLocalObjectReference, error) {
	if rgd == nil {
		return nil, fmt.Errorf("rgd is nil")
	}

	latestImage := rgd.Status.LatestImages[pvcName]
	if latestImage != nil {
		c.logger.Info("Get latest image from RGD for PVC", "LatestImage", *latestImage)
	}

	if !isLatestImageReady(latestImage) {
		noSnapErr := fmt.Errorf("unable to find LatestImage from ReplicationGroupDestination %s",
			rgd.Name)
		c.logger.Error(noSnapErr, "No latestImage",
			"ReplicationDestination", pvcName, "ReplicationGroupDestination", rgd.Name)

		return nil, noSnapErr
	}

	return latestImage, nil
}

func (c *cgHandler) EnsurePVCfromRGD(
	rdSpec ramendrv1alpha1.VolSyncReplicationDestinationSpec, failoverAction bool,
) error {
	log := c.logger.WithName("EnsurePVCfromRGD")

	rd, err := volsync.GetRD(c.ctx, c.Client,
		rdSpec.ProtectedPVC.Name, rdSpec.ProtectedPVC.Namespace, log)
	if err != nil {
		return err
	}

	rgd, err := GetRGD(c.ctx, c.Client, rd.Labels[util.RGDOwnerLabel], rd.Namespace, log)
	if err != nil {
		return err
	}

	latestImage, err := c.GetLatestImageFromRGD(rgd, rdSpec.ProtectedPVC.Name)
	if err != nil {
		log.Error(err, "Failed to get latest image from RGD")

		return err
	}

	// Make copy of the ref and make sure API group is filled out correctly (shouldn't really need this part)
	vsImageRef := latestImage.DeepCopy()
	if vsImageRef.APIGroup == nil || *vsImageRef.APIGroup == "" {
		vsGroup := snapv1.GroupName
		vsImageRef.APIGroup = &vsGroup
	}

	c.logger.Info("Latest Image for ReplicationDestination", "latestImage", vsImageRef.Name)

	// time to pause RD
	err = PauseRGD(c.ctx, c.Client, rgd, log)
	if err != nil {
		log.Error(err, "Failed to pause RGD")

		return err
	}

	return c.VSHandler.ValidateSnapshotAndEnsurePVC(rdSpec, *vsImageRef, failoverAction)
}

//nolint:gocognit
func (c *cgHandler) DeleteLocalRDAndRS(rd *volsyncv1alpha1.ReplicationDestination) error {
	rgd, err := GetRGD(c.ctx, c.Client, rd.Labels[util.RGDOwnerLabel], rd.Namespace, c.logger)
	if err != nil {
		return err
	}

	latestRDImage, err := c.GetLatestImageFromRGD(rgd, rd.Name)
	if err != nil {
		return err
	}

	c.logger.Info("Clean up local resources. Latest Image for RD", "name", latestRDImage.Name)

	lrs := &volsyncv1alpha1.ReplicationSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getLocalReplicationName(rd.Name),
			Namespace: rd.Namespace,
		},
	}

	err = c.Client.Get(c.ctx, types.NamespacedName{
		Name:      lrs.GetName(),
		Namespace: lrs.GetNamespace(),
	}, lrs)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return c.VSHandler.DeleteLocalRD(
				getLocalReplicationName(rd.Name),
				rd.Namespace,
			)
		}

		c.logger.Error(err, "Failed to get local ReplicationSource")

		return err
	}

	// For Local Direct, localRS trigger must point to the latest RD snapshot image. Otherwise,
	// we wait for local final sync to take place first befor cleaning up.
	if lrs.Spec.Trigger != nil && lrs.Spec.Trigger.Manual == latestRDImage.Name {
		// When local final sync is complete, we cleanup all locally created resources except the app PVC
		if lrs.Status != nil && lrs.Status.LastManualSync == lrs.Spec.Trigger.Manual {
			c.logger.Info("Clean up local resources for RD", "name", rd.Name)

			err = c.VSHandler.CleanupLocalResources(lrs)
			if err != nil {
				c.logger.Info("Failed to cleaned up local resources for RD", "name", rd.Name)

				return err
			}

			return nil
		}
	}

	return fmt.Errorf("waiting for local final sync to complete")
}

// Lists only RS/RD with VRGOwnerNameLabel that matches the owner
func (c *cgHandler) ListByOwner(list client.ObjectList, matchLabels map[string]string, objNamespace string) error {
	listOptions := []client.ListOption{
		client.InNamespace(objNamespace),
		client.MatchingLabels(matchLabels),
	}

	if err := c.Client.List(c.ctx, list, listOptions...); err != nil {
		c.logger.Error(err, "Failed to list by label", "matchLabels", matchLabels)

		return fmt.Errorf("error listing by label (%w)", err)
	}

	return nil
}

func (c *cgHandler) CheckIfPVCMatchLabel(pvcLabels map[string]string) (bool, error) {
	if c.volumeGroupSnapshotSource == nil {
		return false, nil
	}

	selector, err := metav1.LabelSelectorAsSelector(c.volumeGroupSnapshotSource)
	if err != nil {
		return false, err
	}

	return selector.Matches(labels.Set(pvcLabels)), nil
}

func (c *cgHandler) GetRDInCG() ([]ramendrv1alpha1.VolSyncReplicationDestinationSpec, error) {
	rdSpecs := []ramendrv1alpha1.VolSyncReplicationDestinationSpec{}

	if c.volumeGroupSnapshotSource == nil {
		c.logger.Info("volumeGroupSnapshotSource is nil, return an empty RD list")

		return rdSpecs, nil
	}

	if len(c.instance.Spec.VolSync.RDSpec) == 0 {
		c.logger.Info("There is no RDSpec in the VRG")

		return rdSpecs, nil
	}

	for _, rdSpec := range c.instance.Spec.VolSync.RDSpec {
		pvcInCephfsCg, err := c.CheckIfPVCMatchLabel(rdSpec.ProtectedPVC.Labels)
		if err != nil {
			c.logger.Error(err, "Failed to check if pvc label match consistency group selector")

			return nil, err
		}

		if pvcInCephfsCg {
			rdSpecs = append(rdSpecs, rdSpec)
		}
	}

	return rdSpecs, nil
}

func DeleteRGS(ctx context.Context, k8sClient client.Client, ownerName, ownerNamespace string, logger logr.Logger,
) error {
	rgsList := &ramendrv1alpha1.ReplicationGroupSourceList{}

	err := ListReplicationGroupByOwner(ctx, k8sClient, rgsList, ownerName, ownerNamespace, logger)
	if err != nil {
		return err
	}

	return DeleteTypedObjectList(ctx, k8sClient, ToPointerSlice(rgsList.Items), logger)
}

func DeleteRGD(ctx context.Context, k8sClient client.Client, ownerName, ownerNamespace string, logger logr.Logger,
) error {
	rgdList := &ramendrv1alpha1.ReplicationGroupDestinationList{}

	err := ListReplicationGroupByOwner(ctx, k8sClient, rgdList, ownerName, ownerNamespace, logger)
	if err != nil {
		return err
	}

	return DeleteTypedObjectList(ctx, k8sClient, ToPointerSlice(rgdList.Items), logger)
}

func ListReplicationGroupByOwner(ctx context.Context, k8sClient client.Client, objList client.ObjectList, ownerName,
	ownerNamespace string, logger logr.Logger,
) error {
	matchLabels := map[string]string{
		volsync.VRGOwnerNameLabel:      ownerName,
		volsync.VRGOwnerNamespaceLabel: ownerNamespace,
	}

	listOptions := []client.ListOption{
		client.MatchingLabels(matchLabels),
	}

	if err := k8sClient.List(ctx, objList, listOptions...); err != nil {
		return fmt.Errorf("error listing Object by label %v - (%w)", matchLabels, err)
	}

	return nil
}

func DeleteTypedObjectList[T client.Object](ctx context.Context, k8sClient client.Client, items []T, logger logr.Logger,
) error {
	for _, obj := range items {
		if err := k8sClient.Delete(ctx, obj); err != nil {
			logger.Error(err, "Error cleaning up object", "name", obj.GetName())
		}
	}

	return nil
}

func ToPointerSlice[T any](items []T) []*T {
	out := make([]*T, len(items))
	for i := range items {
		out[i] = &items[i]
	}

	return out
}

// ensureFinalSyncSetup ensures that all prerequisites for a final sync are met.
// It verifies that each application PVC belonging to the given ReplicationGroupSource
// is in Terminating state, and if the ReplicationGroupSource is triggered for final sync,
// it creates (or ensures readiness of) a temporary PVC used exclusively for the final sync.
//
// The function returns (true, err) when reconciliation should be requeued
// It returns (false, nil) when final sync setup is complete and no requeue is needed.
func (c *cgHandler) ensureFinalSyncSetup(rgsName, rgsNamespace string, log logr.Logger) (bool, error) {
	const requeue = true

	rgs, err := GetRGS(c.ctx, c.Client, rgsName, rgsNamespace, log)
	if err != nil {
		return true, err
	}

	requeueResult := false

	for _, rs := range rgs.Status.ReplicationSources {
		srcPVC, err := util.GetPVC(c.ctx, c.Client, types.NamespacedName{Namespace: rs.Namespace, Name: rs.Name})
		if err != nil {
			if k8serrors.IsNotFound(err) {
				log.Info("Application PVC not found, checking the next one",
					"namespace", rs.Namespace, "name", rs.Name)

				continue
			}

			log.Error(err, "Failed to retrieve application PVC", "pvcName", rs.Name)

			return requeue, err
		}

		if !util.ResourceIsDeleted(srcPVC) {
			log.Info("Final sync will not run until application PVC is deleted",
				"namespace", srcPVC.Namespace, "name", srcPVC.Name)

			return requeue, nil
		}

		if IsPrepareForFinalSyncTriggered(rgs) {
			result, err := c.ensureTmpPVCForFinalSync(srcPVC, log)
			if err != nil {
				return true, err
			}

			requeueResult = requeueResult || result
		}
	}

	// All replication sources are ready, no need to requeue
	return requeueResult, nil
}

func (c *cgHandler) ensureTmpPVCForFinalSync(srcPVC *corev1.PersistentVolumeClaim, log logr.Logger) (bool, error) {
	const requeue = true

	result, err := c.VSHandler.SetupTmpPVCForFinalSync(srcPVC)
	if err != nil {
		log.Error(err, "Failed to set up temporary PVC for final sync")

		return requeue, err
	}

	if result == requeue {
		log.Info("Waiting for temporary PVC readiness before final sync")

		return requeue, nil
	}

	// Ensure the util.ConsistencyGroupLabel is removed from the main PVC ans saved as an annotation
	// This prevents the main PVC from being selected for replication again
	// and also saves the CG name for reference.
	if err := util.NewResourceUpdater(srcPVC).
		DeleteLabel(util.ConsistencyGroupLabel).
		AddAnnotation(util.ConsistencyGroupLabel, c.cgName).
		Update(c.ctx, c.Client); err != nil {
		log.Error(err, "Failed to remove util.ConsistencyGroupLabel from main PVC",
			"pvcName", srcPVC.Name)

		return requeue, err
	}

	return !requeue, nil
}

func (c *cgHandler) cleanupFinalSyncIfComplete(namespace string,
) (*ramendrv1alpha1.ReplicationGroupSource, bool, error) {
	const finalSyncComplete = true

	rgs, err := GetRGS(c.ctx, c.Client, c.cgName, namespace, c.logger)
	if err != nil {
		return nil, !finalSyncComplete, err
	}

	if isFinalSyncComplete(rgs) {
		c.logger.Info("RGS completed final sync")

		return rgs, finalSyncComplete, c.ensureFinalSyncCleanup(rgs)
	}

	return rgs, !finalSyncComplete, nil
}

func (c *cgHandler) ensureFinalSyncCleanup(rgs *ramendrv1alpha1.ReplicationGroupSource) error {
	for _, rs := range rgs.Status.ReplicationSources {
		err := c.VSHandler.UndoAfterFinalSync(rs.Name, rs.Namespace)
		if err != nil {
			return err
		}

		err = c.VSHandler.CleanupAfterRSFinalSync(rs.Name, rs.Namespace)
		if err != nil {
			return err
		}
	}

	return nil
}

func GetRGS(ctx context.Context,
	k8sClient client.Client,
	name, namespace string,
	log logr.Logger,
) (*ramendrv1alpha1.ReplicationGroupSource, error) {
	rgs := &ramendrv1alpha1.ReplicationGroupSource{}
	if err := k8sClient.Get(ctx,
		types.NamespacedName{Name: name, Namespace: namespace},
		rgs); err != nil {
		log.Error(err, "Failed to retrieve RGS", "name", name, "namespace", namespace)

		return nil, err
	}

	return rgs, nil
}

func GetRGD(ctx context.Context,
	k8sClient client.Client,
	name, namespace string,
	log logr.Logger,
) (*ramendrv1alpha1.ReplicationGroupDestination, error) {
	rgd := &ramendrv1alpha1.ReplicationGroupDestination{}
	if err := k8sClient.Get(ctx,
		types.NamespacedName{Name: name, Namespace: namespace},
		rgd); err != nil {
		log.Error(err, "Failed to get RGD", "name", name, "namespace", namespace)

		return nil, err
	}

	return rgd, nil
}

func PauseRGD(ctx context.Context,
	k8sClient client.Client,
	rgd *ramendrv1alpha1.ReplicationGroupDestination,
	log logr.Logger,
) error {
	if rgd.Spec.Paused {
		log.Info("RGD is already paused", "RGD", rgd.Name)

		return nil
	}

	log.Info("Pausing RGD", "RGD", rgd.Name)

	rgd.Spec.Paused = true

	if err := k8sClient.Update(ctx, rgd); err != nil {
		return fmt.Errorf("failed to update RGD %s (%w)", rgd.Name, err)
	}

	return nil
}

func IsPrepareForFinalSyncTriggered(rgs *ramendrv1alpha1.ReplicationGroupSource) bool {
	return rgs.Spec.Trigger != nil &&
		rgs.Spec.Trigger.Manual == volsync.PrepareForFinalSyncTriggerString
}

func (c *cgHandler) CleanupFinalSyncIfNeeded(rgs *ramendrv1alpha1.ReplicationGroupSource, runFinalSync bool) error {
	if !runFinalSync {
		return nil
	}

	return c.ensureFinalSyncCleanup(rgs)
}
