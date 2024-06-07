package cephfscg

import (
	"context"
	"fmt"
	"time"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	"github.com/backube/volsync/controllers/mover"
	"github.com/backube/volsync/controllers/statemachine"
	"github.com/go-logr/logr"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/util"
	"github.com/ramendr/ramen/controllers/volsync"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type rgdMachine struct {
	client.Client
	ReplicationGroupDestination *ramendrv1alpha1.ReplicationGroupDestination
	VSHandler                   *volsync.VSHandler // VSHandler will be used to call the exist funcs
	Logger                      logr.Logger
}

func NewRGDMachine(
	client client.Client,
	replicationGroupDestination *ramendrv1alpha1.ReplicationGroupDestination,
	vsHandler *volsync.VSHandler,
	logger logr.Logger,
) statemachine.ReplicationMachine {
	return &rgdMachine{
		Client:                      client,
		ReplicationGroupDestination: replicationGroupDestination,
		VSHandler:                   vsHandler,
		Logger:                      logger.WithName("ReplicationGroupDestinationMachine"),
	}
}

func (m *rgdMachine) Cronspec() string            { return "" } // noTrigger
func (m *rgdMachine) ManualTag() string           { return "" } // noTrigger
func (m *rgdMachine) LastManualTag() string       { return "" }
func (m *rgdMachine) SetLastManualTag(tag string) {}

func (m *rgdMachine) NextSyncTime() *metav1.Time {
	return m.ReplicationGroupDestination.Status.NextSyncTime
}

func (m *rgdMachine) SetNextSyncTime(next *metav1.Time) {
	m.ReplicationGroupDestination.Status.NextSyncTime = next
}

func (m *rgdMachine) LastSyncStartTime() *metav1.Time {
	return m.ReplicationGroupDestination.Status.LastSyncStartTime
}

func (m *rgdMachine) SetLastSyncStartTime(last *metav1.Time) {
	m.ReplicationGroupDestination.Status.LastSyncStartTime = last
}

func (m *rgdMachine) LastSyncTime() *metav1.Time {
	return m.ReplicationGroupDestination.Status.LastSyncTime
}

func (m *rgdMachine) SetLastSyncTime(last *metav1.Time) {
	m.ReplicationGroupDestination.Status.LastSyncTime = last
}

func (m *rgdMachine) LastSyncDuration() *metav1.Duration {
	return m.ReplicationGroupDestination.Status.LastSyncDuration
}

func (m *rgdMachine) SetLastSyncDuration(duration *metav1.Duration) {
	m.ReplicationGroupDestination.Status.LastSyncDuration = duration
}

func (m *rgdMachine) Conditions() *[]metav1.Condition {
	return &m.ReplicationGroupDestination.Status.Conditions
}

func (m *rgdMachine) Synchronize(ctx context.Context) (mover.Result, error) {
	createdRDs := []*volsyncv1alpha1.ReplicationDestination{}
	rds := []*corev1.ObjectReference{}

	for _, rdSpec := range m.ReplicationGroupDestination.Spec.RDSpecs {
		m.Logger.Info("Create replication destination for PVC", "PVCName", rdSpec.ProtectedPVC.Name)

		rd, err := m.ReconcileRD(rdSpec, m.ReplicationGroupDestination.Status.LastSyncStartTime.String())
		if err != nil {
			return mover.InProgress(), fmt.Errorf("failed to create replication destination: %w", err)
		}

		createdRDs = append(createdRDs, rd)
		rds = append(
			rds,
			&corev1.ObjectReference{APIVersion: rd.APIVersion, Kind: rd.Kind, Name: rd.GetName(), Namespace: rd.GetNamespace()},
		)
	}

	m.ReplicationGroupDestination.Status.ReplicationDestinations = rds
	latestImages := make(map[string]*corev1.TypedLocalObjectReference)

	for _, rd := range createdRDs {
		m.Logger.Info("Check replication destination is completed", "ReplicationDestinationName", rd.Name)

		if rd.Spec.Trigger.Manual != rd.Status.LastManualSync {
			m.Logger.Info("replication destination is not completed", "ReplicationDestinationName", rd.Name)

			return mover.InProgress(), nil
		}

		if rd.Status.LatestImage != nil && rd.Spec.RsyncTLS != nil && rd.Spec.RsyncTLS.DestinationPVC != nil {
			latestImages[*rd.Spec.RsyncTLS.DestinationPVC] = rd.Status.LatestImage
		}

		if err := util.DeferDeleteImage(
			ctx, m.Client, rd.Status.LatestImage.Name, rd.Namespace, rd.Spec.Trigger.Manual, rd.Name,
		); err != nil {
			return mover.InProgress(), err
		}
	}

	m.Logger.Info("Set lastest images to ReplicationGroupDestination", "LenofLastestImages", len(latestImages))

	m.ReplicationGroupDestination.Status.LatestImages = latestImages

	if err := util.CleanExpiredRDImages(ctx, m.Client, m.ReplicationGroupDestination); err != nil {
		return mover.InProgress(), err
	}

	return mover.Complete(), nil
}

func (m *rgdMachine) Cleanup(ctx context.Context) (mover.Result, error) {
	// No temp resources created by ReplicationGroupDestination
	return mover.Complete(), nil
}

func (m *rgdMachine) SetOutOfSync(bool)                     {}
func (m *rgdMachine) IncMissedIntervals()                   {}
func (m *rgdMachine) ObserveSyncDuration(dur time.Duration) {}

//nolint:cyclop
func (m *rgdMachine) ReconcileRD(
	rdSpec ramendrv1alpha1.VolSyncReplicationDestinationSpec, manual string,
) (*volsyncv1alpha1.ReplicationDestination, error,
) {
	if !rdSpec.ProtectedPVC.ProtectedByVolSync {
		return nil, fmt.Errorf("protectedPVC %s is not VolSync Enabled", rdSpec.ProtectedPVC.Name)
	}

	// Pre-allocated shared secret - DRPC will generate and propagate this secret from hub to clusters
	pskSecretName := volsync.GetVolSyncPSKSecretNameFromVRGName(m.ReplicationGroupDestination.Name)
	// Need to confirm this secret exists on the cluster before proceeding, otherwise volsync will generate it
	secretExists, err := m.VSHandler.ValidateSecretAndAddVRGOwnerRef(pskSecretName)
	if err != nil || !secretExists {
		return nil, err
	}

	dstPVC, err := m.VSHandler.PrecreateDestPVCIfEnabled(rdSpec)
	if err != nil {
		return nil, err
	}

	var rd *volsyncv1alpha1.ReplicationDestination

	rd, err = m.CreateReplicationDestinations(rdSpec, pskSecretName, dstPVC, manual)
	if err != nil {
		return nil, err
	}

	err = m.VSHandler.ReconcileServiceExportForRD(rd)
	if err != nil {
		return nil, err
	}

	if !volsync.RDStatusReady(rd, m.Logger) {
		return nil, nil
	}

	return rd, nil
}

func (m *rgdMachine) CreateReplicationDestinations(
	rdSpec ramendrv1alpha1.VolSyncReplicationDestinationSpec,
	pskSecretName string, dstPVC *string, manual string,
) (*volsyncv1alpha1.ReplicationDestination, error) {
	volumeSnapshotClassName, err := m.VSHandler.GetVolumeSnapshotClassFromPVCStorageClass(
		rdSpec.ProtectedPVC.StorageClassName)
	if err != nil {
		m.Logger.Error(err, "Failed to get VolumeSnapshotClass from PVC StorageClass", "PVCName", rdSpec.ProtectedPVC.Name)

		return nil, err
	}

	pvcAccessModes := []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce} // Default value
	if len(rdSpec.ProtectedPVC.AccessModes) > 0 {
		pvcAccessModes = rdSpec.ProtectedPVC.AccessModes
	}

	rd := &volsyncv1alpha1.ReplicationDestination{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getReplicationDestinationName(rdSpec.ProtectedPVC.Name),
			Namespace: m.ReplicationGroupDestination.Namespace,
		},
	}

	if _, err := ctrlutil.CreateOrUpdate(
		context.Background(), m.Client, rd,
		func() error {
			if err := ctrl.SetControllerReference(m.ReplicationGroupDestination, rd, m.Client.Scheme()); err != nil {
				return err
			}

			util.AddLabel(rd, util.RGDOwnerLabel, m.ReplicationGroupDestination.Name)
			util.AddAnnotation(rd, volsync.OwnerNameAnnotation, m.ReplicationGroupDestination.Name)
			util.AddAnnotation(rd, volsync.OwnerNamespaceAnnotation, m.ReplicationGroupDestination.Namespace)

			rd.Spec.Trigger = &volsyncv1alpha1.ReplicationDestinationTriggerSpec{
				Manual: manual,
			}
			rd.Spec.RsyncTLS = &volsyncv1alpha1.ReplicationDestinationRsyncTLSSpec{
				ServiceType: &volsync.DefaultRsyncServiceType,
				KeySecret:   &pskSecretName,
				ReplicationDestinationVolumeOptions: volsyncv1alpha1.ReplicationDestinationVolumeOptions{
					CopyMethod:              volsyncv1alpha1.CopyMethodSnapshot,
					Capacity:                rdSpec.ProtectedPVC.Resources.Requests.Storage(),
					StorageClassName:        rdSpec.ProtectedPVC.StorageClassName,
					AccessModes:             pvcAccessModes,
					VolumeSnapshotClassName: &volumeSnapshotClassName,
					DestinationPVC:          dstPVC,
				},
			}

			return nil
		}); err != nil {
		m.Logger.Error(err, "Failed to create or update ReplicationDestination",
			"ReplicationDestinationName", getReplicationDestinationName(rdSpec.ProtectedPVC.Name))

		return nil, fmt.Errorf("%w", err)
	}

	return rd, nil
}

func CreateOrUpdateReplicationGroupDestination(
	ctx context.Context, k8sClient client.Client,
	replicationGroupDestinationName, replicationGroupDestinationNamespace string,
	volumeSnapshotClassSelector metav1.LabelSelector,
	rdSpecs []ramendrv1alpha1.VolSyncReplicationDestinationSpec,
	owner metav1.Object,
) (*ramendrv1alpha1.ReplicationGroupDestination, error) {
	if err := util.DeleteReplicationGroupSource(ctx, k8sClient,
		replicationGroupDestinationName, replicationGroupDestinationNamespace); err != nil {
		return nil, err
	}

	rgd := &ramendrv1alpha1.ReplicationGroupDestination{
		ObjectMeta: metav1.ObjectMeta{
			Name:      replicationGroupDestinationName,
			Namespace: replicationGroupDestinationNamespace,
		},
	}

	_, err := ctrlutil.CreateOrUpdate(ctx, k8sClient, rgd, func() error {
		if err := ctrl.SetControllerReference(owner, rgd, k8sClient.Scheme()); err != nil {
			return err
		}

		util.AddLabel(rgd, volsync.VRGOwnerLabel, owner.GetName())
		util.AddAnnotation(rgd, volsync.OwnerNameAnnotation, owner.GetName())
		util.AddAnnotation(rgd, volsync.OwnerNamespaceAnnotation, owner.GetNamespace())

		rgd.Spec.VolumeSnapshotClassSelector = volumeSnapshotClassSelector
		rgd.Spec.RDSpecs = rdSpecs

		return nil
	})
	if err != nil {
		return nil, err
	}

	return rgd, nil
}
