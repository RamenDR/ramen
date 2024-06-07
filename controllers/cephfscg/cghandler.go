package cephfscg

import (
	"context"
	"fmt"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	"github.com/go-logr/logr"
	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v7/apis/volumesnapshot/v1"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/util"
	"github.com/ramendr/ramen/controllers/volsync"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func NewVSCGHandler(
	ctx context.Context,
	k8sClient client.Client,

	instance *ramendrv1alpha1.VolumeReplicationGroup,
	vsHandler *volsync.VSHandler,

	logger logr.Logger,
) VSCGHandler {
	cgHandler := &cgHandler{
		ctx:                       ctx,
		Client:                    k8sClient,
		instance:                  instance,
		VSHandler:                 vsHandler,
		volumeGroupSnapshotSource: instance.Spec.CephFSConsistencyGroupSelector,
		logger:                    logger.WithName("VSCGHandler"),
	}
	if instance.Spec.Async != nil {
		cgHandler.volumeSnapshotClassSelector = instance.Spec.Async.VolumeSnapshotClassSelector
		cgHandler.ramenSchedulingInterval = instance.Spec.Async.SchedulingInterval
	}

	return cgHandler
}

type VSCGHandler interface {
	CreateOrUpdateReplicationGroupDestination(
		replicationGroupDestinationName, replicationGroupDestinationNamespace string,
		rdSpecs []ramendrv1alpha1.VolSyncReplicationDestinationSpec,
	) (*ramendrv1alpha1.ReplicationGroupDestination, error)

	CreateOrUpdateReplicationGroupSource(
		replicationGroupSourceName, replicationGroupSourceNamespace string,
		runFinalSync bool,
	) (*ramendrv1alpha1.ReplicationGroupSource, bool, error)

	GetLatestImageFromRGD(
		ctx context.Context, pvcName string,
	) (*corev1.TypedLocalObjectReference, error)

	EnsurePVCfromRGD(rdSpec ramendrv1alpha1.VolSyncReplicationDestinationSpec, failoverAction bool) error
	DeleteLocalRDAndRS(rd *volsyncv1alpha1.ReplicationDestination) error
	GetRDInCG() ([]ramendrv1alpha1.VolSyncReplicationDestinationSpec, error)
}

type cgHandler struct {
	ctx context.Context
	client.Client

	instance  *ramendrv1alpha1.VolumeReplicationGroup
	VSHandler *volsync.VSHandler // VSHandler will be used to call the exist funcs

	volumeGroupSnapshotSource   *metav1.LabelSelector
	volumeSnapshotClassSelector metav1.LabelSelector
	ramenSchedulingInterval     string

	logger logr.Logger
}

func (c *cgHandler) CreateOrUpdateReplicationGroupDestination(
	replicationGroupDestinationName, replicationGroupDestinationNamespace string,
	rdSpecs []ramendrv1alpha1.VolSyncReplicationDestinationSpec,
) (*ramendrv1alpha1.ReplicationGroupDestination, error) {
	if err := util.DeleteReplicationGroupSource(c.ctx, c.Client,
		replicationGroupDestinationName, replicationGroupDestinationNamespace); err != nil {
		return nil, err
	}

	rgd := &ramendrv1alpha1.ReplicationGroupDestination{
		ObjectMeta: metav1.ObjectMeta{
			Name:      replicationGroupDestinationName,
			Namespace: replicationGroupDestinationNamespace,
		},
	}

	_, err := ctrlutil.CreateOrUpdate(c.ctx, c.Client, rgd, func() error {
		if err := ctrl.SetControllerReference(c.instance, rgd, c.Client.Scheme()); err != nil {
			return err
		}

		util.AddLabel(rgd, volsync.VRGOwnerNameLabel, c.instance.GetName())
		util.AddLabel(rgd, volsync.VRGOwnerNamespaceLabel, c.instance.GetNamespace())
		util.AddAnnotation(rgd, volsync.OwnerNameAnnotation, c.instance.GetName())
		util.AddAnnotation(rgd, volsync.OwnerNamespaceAnnotation, c.instance.GetNamespace())

		rgd.Spec.VolumeSnapshotClassSelector = c.volumeSnapshotClassSelector
		rgd.Spec.RDSpecs = rdSpecs

		return nil
	})
	if err != nil {
		return nil, err
	}

	return rgd, nil
}

//nolint:funlen,gocognit,cyclop,gocyclo
func (c *cgHandler) CreateOrUpdateReplicationGroupSource(
	replicationGroupSourceName, replicationGroupSourceNamespace string,
	runFinalSync bool,
) (*ramendrv1alpha1.ReplicationGroupSource, bool, error) {
	// Get the rd if it exist when change secondary to primary
	rdList := &volsyncv1alpha1.ReplicationDestinationList{}
	if err := c.ListByOwner(rdList,
		map[string]string{util.RGDOwnerLabel: c.instance.Name}, c.instance.Namespace,
	); err != nil {
		return nil, false, err
	}

	for i := range rdList.Items {
		rd := rdList.Items[i]
		if c.VSHandler.IsCopyMethodDirect() {
			// Before creating a new RGS, make sure any LocalReplicationDestination for this PVC is cleaned up first
			// DeleteRD only delete LRD&LRS here, as only the lrd&lrs have vrg owner and also belongs a CG
			err := c.DeleteLocalRDAndRS(&rd)
			if err != nil {
				return nil, false, err
			}
		}
	}

	if err := util.DeleteReplicationGroupDestination(
		c.ctx, c.Client,
		replicationGroupSourceName, replicationGroupSourceNamespace); err != nil {
		return nil, false, err
	}

	VolumeGroupSnapshotClassSelector := metav1.LabelSelector{}
	if c.instance != nil && c.instance.Spec.Async != nil {
		VolumeGroupSnapshotClassSelector = c.instance.Spec.Async.VolumeGroupSnapshotClassSelector
	}

	volumeGroupSnapshotClassName, err := util.GetVolumeGroupSnapshotClassFromPVCsStorageClass(
		c.ctx, c.Client, VolumeGroupSnapshotClassSelector,
		c.instance.Spec.CephFSConsistencyGroupSelector, c.instance.Namespace, c.logger,
	)
	if err != nil {
		return nil, false, err
	}

	rgs := &ramendrv1alpha1.ReplicationGroupSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      replicationGroupSourceName,
			Namespace: replicationGroupSourceNamespace,
		},
	}

	_, err = ctrlutil.CreateOrUpdate(c.ctx, c.Client, rgs, func() error {
		if err := ctrl.SetControllerReference(c.instance, rgs, c.Client.Scheme()); err != nil {
			return err
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
		return nil, false, err
	}

	//
	// For final sync only - check status to make sure the final sync is complete
	// and also run cleanup (removes PVC we just ran the final sync from)
	//
	if runFinalSync && isFinalSyncComplete(rgs) {
		return rgs, true, nil
	}

	return rgs, false, nil
}

func (c *cgHandler) GetLatestImageFromRGD(
	ctx context.Context, pvcName string,
) (*corev1.TypedLocalObjectReference, error) {
	rgdList := &ramendrv1alpha1.ReplicationGroupDestinationList{}

	if err := c.ListByOwner(rgdList, map[string]string{
		volsync.VRGOwnerNameLabel:      c.instance.GetName(),
		volsync.VRGOwnerNamespaceLabel: c.instance.GetNamespace(),
	}, c.instance.Namespace); err != nil {
		return nil, err
	}

	c.logger.Info("Get rgdList", "rgdList", rgdList)

	var latestImage *corev1.TypedLocalObjectReference

	for _, rgd := range rgdList.Items {
		if util.GetPVCLatestImageRGD(pvcName, rgd) != nil {
			c.logger.Info("Get latest image from RDG for PVC", "PVC", pvcName)
			latestImage = util.GetPVCLatestImageRGD(pvcName, rgd)
		}
	}

	if latestImage != nil {
		c.logger.Info("Get latest image from RDG for PVC", "LatestImage", *latestImage)
	}

	if !isLatestImageReady(latestImage) {
		noSnapErr := fmt.Errorf("unable to find LatestImage from ReplicationDestination %s", pvcName)
		c.logger.Error(noSnapErr, "No latestImage", "rd", pvcName)

		return nil, noSnapErr
	}

	return latestImage, nil
}

func (c *cgHandler) EnsurePVCfromRGD(
	rdSpec ramendrv1alpha1.VolSyncReplicationDestinationSpec, failoverAction bool,
) error {
	latestImage, err := c.GetLatestImageFromRGD(c.ctx, rdSpec.ProtectedPVC.Name)
	if err != nil {
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

// Lists only RS/RD with VRGOwnerLabel that matches the owner
func (c *cgHandler) listByOwner(ctx context.Context, list client.ObjectList) error {
	matchLabels := map[string]string{
		volsync.VRGOwnerNameLabel: c.instance.GetName(),
	}
	listOptions := []client.ListOption{
		client.InNamespace(c.instance.GetNamespace()),
		client.MatchingLabels(matchLabels),
	}

	if err := c.Client.List(ctx, list, listOptions...); err != nil {
		c.logger.Error(err, "Failed to list by label", "matchLabels", matchLabels)

		return fmt.Errorf("error listing by label (%w)", err)
	}

	return nil
}

//nolint:gocognit
func (c *cgHandler) DeleteLocalRDAndRS(rd *volsyncv1alpha1.ReplicationDestination) error {
	latestRDImage, err := c.GetLatestImageFromRGD(c.ctx, rd.Name)
	if err != nil {
		return err
	}

	c.logger.Info("Clean up local resources. Latest Image for main RD", "name", latestRDImage.Name)

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
		if errors.IsNotFound(err) {
			return c.VSHandler.DeleteLocalRD(
				getLocalReplicationName(rd.Name),
				rd.Namespace,
			)
		}

		return err
	}

	// For Local Direct, localRS trigger must point to the latest RD snapshot image. Otherwise,
	// we wait for local final sync to take place first befor cleaning up.
	if lrs.Spec.Trigger != nil && lrs.Spec.Trigger.Manual == latestRDImage.Name {
		// When local final sync is complete, we cleanup all locally created resources except the app PVC
		if lrs.Status != nil && lrs.Status.LastManualSync == lrs.Spec.Trigger.Manual {
			err = c.VSHandler.CleanupLocalResources(lrs)
			if err != nil {
				return err
			}

			c.logger.Info("Cleaned up local resources for RD", "name", rd.Name)

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

func (c *cgHandler) GetRDInCG() ([]ramendrv1alpha1.VolSyncReplicationDestinationSpec, error) {
	rdSpecs := []ramendrv1alpha1.VolSyncReplicationDestinationSpec{}

	if c.instance.Spec.CephFSConsistencyGroupSelector == nil {
		return rdSpecs, nil
	}

	if len(c.instance.Spec.VolSync.RDSpec) == 0 {
		return rdSpecs, nil
	}

	for _, rdSpec := range c.instance.Spec.VolSync.RDSpec {
		pvcInCephfsCg, err := util.CheckIfPVCMatchLabel(
			rdSpec.ProtectedPVC.Labels, c.instance.Spec.CephFSConsistencyGroupSelector)
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
