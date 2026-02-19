// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package cephfscg

import (
	"context"
	"time"

	"github.com/backube/volsync/controllers/mover"
	"github.com/backube/volsync/controllers/statemachine"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/util"
	"github.com/ramendr/ramen/internal/controller/volsync"
)

type replicationGroupSourceMachine struct {
	client.Client
	ReplicationGroupSource *ramendrv1alpha1.ReplicationGroupSource
	Vrg                    *ramendrv1alpha1.VolumeReplicationGroup
	VSHandler              *volsync.VSHandler // VSHandler will be used to call the exist funcs
	VolumeGroupHandler     VolumeGroupSourceHandler
	Logger                 logr.Logger
}

func NewRGSMachine(
	client client.Client,
	replicationGroupSource *ramendrv1alpha1.ReplicationGroupSource,
	vrg *ramendrv1alpha1.VolumeReplicationGroup,
	vsHandler *volsync.VSHandler,
	volumeGroupHandler VolumeGroupSourceHandler,
	logger logr.Logger,
) statemachine.ReplicationMachine {
	return &replicationGroupSourceMachine{
		Client:                 client,
		ReplicationGroupSource: replicationGroupSource,
		Vrg:                    vrg,
		VSHandler:              vsHandler,
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

//nolint:funlen,cyclop
func (m *replicationGroupSourceMachine) Synchronize(ctx context.Context) (mover.Result, error) {
	m.Logger.Info("Create volume group snapshot")

	wait, err := m.VolumeGroupHandler.WaitIfPVCTooNew(ctx)
	if err != nil {
		m.Logger.Error(err, "Failed to check if PVCs are in use before creating volume group snapshot")

		return mover.InProgress(), err
	}
	// If any PVC is too new, wait and requeue
	// Note: this is only a mitigation, not a full solution, to the problem of ROX PVCs
	//	not being ready (with correct SELinux labels) in time
	//	--- see note on VolumeGroupSourceHandler.WaitIfPVCTooNew() for details.
	//  We could eliminate this by waiting for all PVCs to be used (mounted) before
	//	creating the snapshot, but that would mean, we will not be able to protect unused
	//	PVCs (e.g. those created but only mounted sometime in the future).
	//
	if wait {
		m.Logger.Error(err, "Some PVCs are not old enough, cannot create volume group snapshot now")

		return mover.InProgress(), nil
	}

	// Ensure application PVCs are mounted before first snapshot (e.g. for SELinux labels).
	ready, err := m.VolumeGroupHandler.EnsureApplicationPVCsMounted(ctx)
	if err != nil {
		m.Logger.Error(err, "Failed to ensure application PVCs are mounted")

		return mover.InProgress(), err
	}

	if !ready {
		m.Logger.Info("Waiting for application PVCs to be mounted before first snapshot")

		return mover.InProgress(), nil
	}

	createdOrUpdatedVGS, err := m.VolumeGroupHandler.CreateOrUpdateVolumeGroupSnapshot(
		ctx, m.ReplicationGroupSource,
	)
	if err != nil {
		m.Logger.Error(err, "Failed to create volume group snapshot")

		return mover.InProgress(), err
	}

	if createdOrUpdatedVGS {
		m.Logger.Info("VolumeGroupSnapshot was created or updated, need to wait until it's ready to be used")

		return mover.InProgress(), nil
	}

	m.Logger.Info("Create ReplicationSource for each Restored PVC")
	vrgName := m.ReplicationGroupSource.GetLabels()[util.VRGOwnerNameLabel]
	// Pre-allocated shared secret - DRPC will generate and propagate this secret from hub to clusters
	pskSecretName := volsync.GetVolSyncPSKSecretNameFromVRGName(vrgName)

	// Need to confirm this secret exists on the cluster before proceeding, otherwise volsync will generate it
	secretExists, err := m.VSHandler.ValidateSecretAndAddVRGOwnerRef(pskSecretName)
	if err != nil || !secretExists {
		m.Logger.Error(err, "Failed to validate secret and add VRGOwnerRef")

		return mover.InProgress(), err
	}

	if m.VSHandler.IsVRGInAdminNamespace() {
		// copy the secret to the namespace where the PVC is
		err = m.VSHandler.CopySecretToPVCNamespace(pskSecretName, m.ReplicationGroupSource.Namespace)
		if err != nil {
			m.Logger.Error(err, "Failed to CopySecretToPVCNamespace", "PSKSecretName", pskSecretName)

			return mover.InProgress(), err
		}
	}

	if m.ReplicationGroupSource.Status.LastSyncStartTime == nil {
		m.Logger.Info("LastSyncStartTime in ReplicationGroupSource is not updated yet.")

		return mover.InProgress(), nil
	}

	m.Logger.Info("Restore PVCs from volume group snapshot")

	restoredPVCs, err := m.VolumeGroupHandler.RestoreVolumesFromVolumeGroupSnapshot(ctx, m.ReplicationGroupSource)
	if err != nil {
		m.Logger.Error(err, "Failed to restore volume group snapshot")

		return mover.InProgress(), err
	}

	replicationSources, srcCreatedOrUpdated, err := m.VolumeGroupHandler.CreateOrUpdateReplicationSourceForRestoredPVCs(
		ctx, m.ReplicationGroupSource.Status.LastSyncStartTime.String(),
		restoredPVCs, m.ReplicationGroupSource, m.Vrg, m.VSHandler.IsSubmarinerEnabled())
	if err != nil {
		m.Logger.Error(err, "Failed to create replication source")

		return mover.InProgress(), err
	}

	if srcCreatedOrUpdated {
		m.Logger.Info("Some replication sources were created or updated, need to wait until they are ready to be used")

		return mover.InProgress(), nil
	}

	m.ReplicationGroupSource.Status.ReplicationSources = replicationSources

	m.Logger.Info("Check if all ReplicationSources are completed")

	return m.handlerRSCompletionForRestoredPVCs(ctx)
}

func (m *replicationGroupSourceMachine) handlerRSCompletionForRestoredPVCs(ctx context.Context) (mover.Result, error) {
	completed, err := m.VolumeGroupHandler.CheckReplicationSourceForRestoredPVCsCompleted(ctx,
		m.ReplicationGroupSource.Status.ReplicationSources)
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
