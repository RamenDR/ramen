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
		replicationGroupSourceName, replicationGroupSourceNamespace string,
		runFinalSync bool,
	) (*ramendrv1alpha1.ReplicationGroupSource, bool, error)

	GetLatestImageFromRGD(
		ctx context.Context, pvcName, pvcNamespace string,
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

	_, err := ctrlutil.CreateOrUpdate(c.ctx, c.Client, rgd, func() error {
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
	replicationGroupSourceName, replicationGroupSourceNamespace string,
	runFinalSync bool,
) (*ramendrv1alpha1.ReplicationGroupSource, bool, error) {
	replicationGroupSourceName = c.cgName

	log := c.logger.WithName("CreateOrUpdateReplicationGroupSource").
		WithValues("ReplicationGroupSourceName", replicationGroupSourceName,
			"ReplicationGroupSourceNamespace", replicationGroupSourceNamespace,
			"runFinalSync", runFinalSync)

	log.Info("Get RDs which are owned by RGD", "RGD", replicationGroupSourceName)
	// Get the rd if it exist when change secondary to primary
	rdList := &volsyncv1alpha1.ReplicationDestinationList{}
	if err := c.ListByOwner(rdList,
		map[string]string{util.RGDOwnerLabel: replicationGroupSourceName}, replicationGroupSourceNamespace,
	); err != nil {
		log.Error(err, "Failed to get RDs which are owned by RGD", "RGD", replicationGroupSourceName)

		return nil, false, err
	}

	for i := range rdList.Items {
		rd := rdList.Items[i]
		if c.VSHandler.IsCopyMethodDirect() {
			log.Info("Delete local RD and RS if they exists for RD", "RD", rd)

			err := c.DeleteLocalRDAndRS(&rd)
			if err != nil {
				log.Error(err, "Failed to delete local RD and RS for RD", "RD", rd)

				return nil, false, err
			}
		}
	}

	if err := util.DeleteReplicationGroupDestination(
		c.ctx, c.Client,
		replicationGroupSourceName, replicationGroupSourceNamespace); err != nil {
		log.Error(err, "Failed to delete ReplicationGroupDestination before creating ReplicationGroupSource")

		return nil, false, err
	}

	namespaces := []string{c.instance.Namespace}
	if c.instance.Spec.ProtectedNamespaces != nil && len(*c.instance.Spec.ProtectedNamespaces) != 0 {
		namespaces = *c.instance.Spec.ProtectedNamespaces
	}

	volumeGroupSnapshotClassName, err := util.GetVolumeGroupSnapshotClassFromPVCsStorageClass(
		c.ctx, c.Client, c.volumeGroupSnapshotClassSelector,
		*c.volumeGroupSnapshotSource, namespaces, c.logger,
	)
	if err != nil {
		log.Error(err, "Failed to get volume group snapshot class name")

		return nil, false, err
	}

	rgs := &ramendrv1alpha1.ReplicationGroupSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      replicationGroupSourceName,
			Namespace: replicationGroupSourceNamespace,
		},
	}

	util.AddLabel(rgs, util.CreatedByRamenLabel, "true")

	_, err = ctrlutil.CreateOrUpdate(c.ctx, c.Client, rgs, func() error {
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

		return nil, false, err
	}
	//
	// For final sync only - check status to make sure the final sync is complete
	// and also run cleanup (removes PVC we just ran the final sync from)
	//
	if runFinalSync && isFinalSyncComplete(rgs) {
		log.Info("ReplicationGroupSource complete final sync")

		return rgs, true, nil
	}

	return rgs, false, nil
}

func (c *cgHandler) GetLatestImageFromRGD(
	ctx context.Context, pvcName, pvcNamespace string,
) (*corev1.TypedLocalObjectReference, error) {
	log := c.logger.WithName("GetLatestImageFromRGD").
		WithValues("PVCName", pvcName, "PVCNamespace", pvcNamespace)

	log.Info("Get ReplicationDestination for the pvc")

	rd := &volsyncv1alpha1.ReplicationDestination{}
	if err := c.Client.Get(ctx,
		types.NamespacedName{Name: getReplicationDestinationName(pvcName), Namespace: pvcNamespace},
		rd); err != nil {
		log.Error(err, "Failed to get ReplicationDestination")

		return nil, err
	}

	log.Info("Get ReplicationGroupDestination which manages the ReplicationDestination")

	rgd := &ramendrv1alpha1.ReplicationGroupDestination{}
	if err := c.Client.Get(ctx,
		types.NamespacedName{Name: rd.Labels[util.RGDOwnerLabel], Namespace: rd.Namespace},
		rgd); err != nil {
		log.Error(err, "Failed to get ReplicationGroupDestination")

		return nil, err
	}

	latestImage := rgd.Status.LatestImages[pvcName]
	if latestImage != nil {
		c.logger.Info("Get latest image from RGD for PVC", "LatestImage", *latestImage)
	}

	if !isLatestImageReady(latestImage) {
		noSnapErr := fmt.Errorf("unable to find LatestImage from ReplicationGroupDestination %s",
			rd.Labels[util.RGDOwnerLabel])
		c.logger.Error(noSnapErr, "No latestImage",
			"ReplicationDestination", pvcName, "ReplicationGroupDestination", rd.Labels[util.RGDOwnerLabel])

		return nil, noSnapErr
	}

	return latestImage, nil
}

func (c *cgHandler) EnsurePVCfromRGD(
	rdSpec ramendrv1alpha1.VolSyncReplicationDestinationSpec, failoverAction bool,
) error {
	log := c.logger.WithName("EnsurePVCfromRGD")

	latestImage, err := c.GetLatestImageFromRGD(c.ctx, rdSpec.ProtectedPVC.Name, rdSpec.ProtectedPVC.Namespace)
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

	return c.VSHandler.ValidateSnapshotAndEnsurePVC(rdSpec, *vsImageRef, failoverAction)
}

//nolint:gocognit
func (c *cgHandler) DeleteLocalRDAndRS(rd *volsyncv1alpha1.ReplicationDestination) error {
	latestRDImage, err := c.GetLatestImageFromRGD(c.ctx, rd.Name, rd.Namespace)
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
