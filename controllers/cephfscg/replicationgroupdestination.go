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
	"github.com/ramendr/ramen/controllers/volsync"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func (m *rgdMachine) Synchronize(
	ctx context.Context,
) (mover.Result, error) {
	latestImages := make(map[string]*corev1.TypedLocalObjectReference)

	for _, rdSpec := range m.ReplicationGroupDestination.Spec.RDSpecs {
		m.Logger.Info("Create replication destination for PVC", "PVCName", rdSpec.ProtectedPVC.Name)

		rd, err := m.ReconcileRD(rdSpec, m.ReplicationGroupDestination.Status.LastSyncStartTime.String())
		if err != nil {
			return mover.InProgress(), fmt.Errorf("failed to create replication destination: %w", err)
		}

		m.Logger.Info("Check replication destination is completed", "ReplicationDestinationName", rd.Name)

		if rd.Spec.Trigger.Manual != rd.Status.LastManualSync {
			m.Logger.Info("replication destination is not completed", "ReplicationDestinationName", rd.Name)

			return mover.InProgress(), nil
		}

		if rd.Status.LatestImage != nil {
			latestImages[rdSpec.ProtectedPVC.Name] = rd.Status.LatestImage
		}
	}

	m.Logger.Info("Set lastest images to ReplicationGroupDestination", "LenofLastestImages", len(latestImages))

	m.ReplicationGroupDestination.Status.LatestImages = latestImages

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
		m.Logger.Error(err, "Failed to GetVolumeSnapshotClassFromPVCStorageClass", "PVCName", rdSpec.ProtectedPVC.Name)

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

	if _, err := ctrlutil.CreateOrUpdate(context.Background(), m.Client, rd, func() error {
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
		m.Logger.Error(err, "Failed to CreateOrUpdate ReplicationDestination",
			"ReplicationDestinationName", getReplicationDestinationName(rdSpec.ProtectedPVC.Name))

		return nil, fmt.Errorf("%w", err)
	}

	m.ReplicationGroupDestination.Status.ReplicationDestinations = append(
		m.ReplicationGroupDestination.Status.ReplicationDestinations,
		&corev1.ObjectReference{APIVersion: rd.APIVersion, Kind: rd.Kind, Name: rd.GetName()})

	return rd, nil
}
