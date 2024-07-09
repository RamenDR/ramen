package cephfscg_test

import (
	"context"
	"errors"
	"time"

	"github.com/backube/volsync/controllers/statemachine"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/hack/fakes"
	controllers "github.com/ramendr/ramen/internal/controller"
	"github.com/ramendr/ramen/internal/controller/cephfscg"
	"github.com/ramendr/ramen/internal/controller/volsync"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var rgsName = "rgs"

var _ = Describe("Replicationgroupsource", func() {
	var replicationGroupSourceMachine statemachine.ReplicationMachine
	var fakeVolumeGroupSourceHandler *fakes.FakeVolumeGroupSourceHandler
	BeforeEach(func() {
		fakeVolumeGroupSourceHandler = &fakes.FakeVolumeGroupSourceHandler{}
		metaTime := metav1.NewTime(time.Now())
		rgs := &ramendrv1alpha1.ReplicationGroupSource{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rgsName,
				Namespace: "default",
				UID:       "123",
				Labels:    map[string]string{volsync.VRGOwnerNameLabel: vrgName},
			},
			Status: ramendrv1alpha1.ReplicationGroupSourceStatus{
				LastSyncStartTime: &metaTime,
			},
		}

		replicationGroupSourceMachine = cephfscg.NewRGSMachine(
			k8sClient, rgs, volsync.NewVSHandler(context.Background(), k8sClient, testLogger, rgs,
				&ramendrv1alpha1.VRGAsyncSpec{}, controllers.DefaultCephFSCSIDriverName,
				controllers.DefaultVolSyncCopyMethod, false,
			), fakeVolumeGroupSourceHandler, testLogger,
		)
	})
	Describe("Synchronize", func() {
		Context("pskSecret not exist", func() {
			It("Should be success", func() {
				result, err := replicationGroupSourceMachine.Synchronize(context.Background())
				Expect(err).To(BeNil())
				Expect(result.Completed).To(BeFalse())
			})
		})
		Context("pskSecret exist", func() {
			It("Should be success", func() {
				fakeVolumeGroupSourceHandler.CheckReplicationSourceForRestoredPVCsCompletedReturns(true, nil)
				pskSecretName := volsync.GetVolSyncPSKSecretNameFromVRGName(vrgName)
				err := k8sClient.Create(
					context.Background(),
					&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: pskSecretName, Namespace: "default"}},
				)
				Expect(client.IgnoreAlreadyExists(err)).To(BeNil())
				result, err := replicationGroupSourceMachine.Synchronize(context.Background())
				Expect(err).To(BeNil())
				Expect(result.Completed).To(BeTrue())
			})
		})
	})
	Describe("Cleanup", func() {
		Context("CleanVolumeGroupSnapshotReturns nil", func() {
			It("Should be success", func() {
				fakeVolumeGroupSourceHandler.CleanVolumeGroupSnapshotReturns(nil)
				result, err := replicationGroupSourceMachine.Cleanup(context.Background())
				Expect(err).To(BeNil())
				Expect(result.Completed).To(BeTrue())
			})
		})
		Context("CleanVolumeGroupSnapshotReturns error", func() {
			It("Should be failed", func() {
				fakeVolumeGroupSourceHandler.CleanVolumeGroupSnapshotReturns(errors.New("an error"))
				_, err := replicationGroupSourceMachine.Cleanup(context.Background())
				Expect(err).NotTo(BeNil())
			})
		})
	})
})
