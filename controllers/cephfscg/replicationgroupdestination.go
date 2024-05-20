package cephfscg

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	"github.com/backube/volsync/controllers/mover"
	"github.com/backube/volsync/controllers/statemachine"
	"github.com/go-logr/logr"
	rmn "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/volsync"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type rgdMachine struct {
	client.Client
	ReplicationGroupDestination *rmn.ReplicationGroupDestination
	VSHandler                   *volsync.VSHandler // VSHandler will be used to call the exist funcs
	Logger                      logr.Logger
}

func NewRGDMachine(
	client client.Client,
	replicationGroupDestination *rmn.ReplicationGroupDestination,
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
	createdRDs := []*volsyncv1alpha1.ReplicationDestination{}

	for _, rdSpec := range m.ReplicationGroupDestination.Spec.RDSpecs {
		m.Logger.Info("Create replication destination for PVC", "PVCName", rdSpec.ProtectedPVC.Name)

		_, err := m.VSHandler.PrecreateDestPVCIfEnabled(rdSpec)
		if err != nil {
			return mover.InProgress(), fmt.Errorf("failed to pre-create application pvc: %w", err)
		}

		rd, err := m.VSHandler.ReconcileRD(rdSpec)
		if err != nil {
			return mover.InProgress(), fmt.Errorf("failed to create replication destination: %w", err)
		}

		createdRDs = append(createdRDs, rd)
	}

	lastestImages := []*corev1.TypedLocalObjectReference{}

	for _, rd := range createdRDs {
		m.Logger.Info("Check replication destination is completed", "ReplicationDestinationName", rd.Name)

		replicationDestination := &volsyncv1alpha1.ReplicationDestination{}

		err := m.Client.Get(ctx,
			types.NamespacedName{Name: rd.Name, Namespace: rd.Namespace}, replicationDestination)
		if err != nil {
			return mover.InProgress(), fmt.Errorf("failed to get replication destination %s: %w", rd.Name, err)
		}

		if replicationDestination.Spec.Trigger.Manual != replicationDestination.Status.LastManualSync {
			m.Logger.Info("replication destination is not completed", "ReplicationDestinationName", rd.Name)

			return mover.InProgress(), nil
		}

		if rd.Status.LatestImage != nil {
			lastestImages = append(lastestImages, rd.Status.LatestImage)
		}
	}

	m.Logger.Info("Set lastest images to ReplicationGroupDestination", "LenofLastestImages", len(lastestImages))

	m.ReplicationGroupDestination.Status.LatestImages = lastestImages

	return mover.Complete(), nil
}

func (m *rgdMachine) Cleanup(ctx context.Context) (mover.Result, error) {
	// No temp resources created by ReplicationGroupDestination
	return mover.Complete(), nil
}

func (m *rgdMachine) SetOutOfSync(bool)                     {}
func (m *rgdMachine) IncMissedIntervals()                   {}
func (m *rgdMachine) ObserveSyncDuration(dur time.Duration) {}
