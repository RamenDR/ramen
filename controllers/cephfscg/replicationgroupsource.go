package cephfscg

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/backube/volsync/controllers/mover"
	"github.com/backube/volsync/controllers/statemachine"
	"github.com/go-logr/logr"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type replicationGroupSourceMachine struct {
	client.Client
	ReplicationGroupSource *ramendrv1alpha1.ReplicationGroupSource
	VolumeGroupHandler     VolumeGroupSourceHandler
	Logger                 logr.Logger
}

func NewRGSMachine(
	client client.Client,
	replicationGroupSource *ramendrv1alpha1.ReplicationGroupSource,
	volumeGroupHandler VolumeGroupSourceHandler,
	logger logr.Logger,
) statemachine.ReplicationMachine {
	return &replicationGroupSourceMachine{
		Client:                 client,
		ReplicationGroupSource: replicationGroupSource,
		VolumeGroupHandler:     volumeGroupHandler,
		Logger:                 logger.WithName("ReplicationGroupSourceMachine"),
	}
}

func (m *replicationGroupSourceMachine) Cronspec() string {
	if m.ReplicationGroupSource.Spec.Trigger != nil && m.ReplicationGroupSource.Spec.Trigger.Schedule != nil {
		return *m.ReplicationGroupSource.Spec.Trigger.Schedule
	}

	return ""
}

func (m *replicationGroupSourceMachine) NextSyncTime() *metav1.Time {
	return m.ReplicationGroupSource.Status.NextSyncTime
}

func (m *replicationGroupSourceMachine) SetNextSyncTime(next *metav1.Time) {
	m.ReplicationGroupSource.Status.NextSyncTime = next
}

func (m *replicationGroupSourceMachine) ManualTag() string {
	if m.ReplicationGroupSource.Spec.Trigger != nil {
		return m.ReplicationGroupSource.Spec.Trigger.Manual
	}

	return ""
}

func (m *replicationGroupSourceMachine) LastManualTag() string {
	return m.ReplicationGroupSource.Status.LastManualSync
}

func (m *replicationGroupSourceMachine) SetLastManualTag(tag string) {
	m.ReplicationGroupSource.Status.LastManualSync = tag
}

func (m *replicationGroupSourceMachine) LastSyncStartTime() *metav1.Time {
	return m.ReplicationGroupSource.Status.LastSyncStartTime
}

func (m *replicationGroupSourceMachine) SetLastSyncStartTime(last *metav1.Time) {
	m.ReplicationGroupSource.Status.LastSyncStartTime = last
}

func (m *replicationGroupSourceMachine) LastSyncTime() *metav1.Time {
	return m.ReplicationGroupSource.Status.LastSyncTime
}

func (m *replicationGroupSourceMachine) SetLastSyncTime(last *metav1.Time) {
	m.ReplicationGroupSource.Status.LastSyncTime = last
}

func (m *replicationGroupSourceMachine) LastSyncDuration() *metav1.Duration {
	return m.ReplicationGroupSource.Status.LastSyncDuration
}

func (m *replicationGroupSourceMachine) SetLastSyncDuration(duration *metav1.Duration) {
	m.ReplicationGroupSource.Status.LastSyncDuration = duration
}

func (m *replicationGroupSourceMachine) Conditions() *[]metav1.Condition {
	return &m.ReplicationGroupSource.Status.Conditions
}

func (m *replicationGroupSourceMachine) Synchronize(ctx context.Context) (mover.Result, error) {
	m.Logger.Info("Create volume group snapshot")

	if err := m.VolumeGroupHandler.CreateOrUpdateVolumeGroupSnapshot(
		ctx,
	); err != nil {
		m.Logger.Error(err, "Failed to create volume group snapshot")

		return mover.InProgress(), err
	}

	m.Logger.Info("Restore PVCs from volume group snapshot")

	restoredPVCs, err := m.VolumeGroupHandler.RestoreVolumesFromVolumeGroupSnapshot(ctx)
	if err != nil {
		m.Logger.Error(err, "Failed to restore volume group snapshot")

		return mover.InProgress(), err
	}

	m.Logger.Info("Create ReplicationSource for each Restored PVC")

	replicationSources, err := m.VolumeGroupHandler.CreateOrUpdateReplicationSourceForRestoredPVCs(
		ctx, m.ReplicationGroupSource.Status.LastSyncStartTime.String(), restoredPVCs)
	if err != nil {
		m.Logger.Error(err, "Failed to create replication source")

		return mover.InProgress(), err
	}

	m.ReplicationGroupSource.Status.ReplicationSources = replicationSources

	m.Logger.Info("Check if all ReplicationSources are completed")

	completed, err := m.VolumeGroupHandler.CheckReplicationSourceForRestoredPVCsCompleted(ctx, replicationSources)
	if err != nil {
		m.Logger.Error(err, "Failed to check replication sources")

		return mover.InProgress(), err
	}

	if !completed {
		m.Logger.Info("Not all ReplicationSources are completed")

		return mover.InProgress(), nil
	}

	m.Logger.Info("All ReplicationSources are completed")

	return mover.Complete(), nil
}

func (m *replicationGroupSourceMachine) Cleanup(ctx context.Context) (mover.Result, error) {
	m.Logger.Info("Clean Replication Group Source")

	err := m.VolumeGroupHandler.CleanVolumeGroupSnapshot(ctx)
	if err != nil {
		m.Logger.Error(err, "Failed to clean Replication Group Source")

		return mover.InProgress(), err
	}

	return mover.Complete(), nil
}

func (m *replicationGroupSourceMachine) SetOutOfSync(bool)                     {}
func (m *replicationGroupSourceMachine) IncMissedIntervals()                   {}
func (m *replicationGroupSourceMachine) ObserveSyncDuration(dur time.Duration) {}
