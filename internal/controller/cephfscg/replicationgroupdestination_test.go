package cephfscg_test

import (
	"context"
	"time"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	"github.com/backube/volsync/controllers/statemachine"
	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v7/apis/volumesnapshot/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	internalController "github.com/ramendr/ramen/internal/controller"
	"github.com/ramendr/ramen/internal/controller/cephfscg"
	"github.com/ramendr/ramen/internal/controller/volsync"
)

var _ = Describe("Replicationgroupdestination", func() {
	var replicationGroupDestinationMachine statemachine.ReplicationMachine
	BeforeEach(func() {
		metaTime := metav1.NewTime(time.Now())
		rgd := &ramendrv1alpha1.ReplicationGroupDestination{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rgdName,
				Namespace: "default",
				UID:       "123",
			},
			Status: ramendrv1alpha1.ReplicationGroupDestinationStatus{
				LastSyncStartTime: &metaTime,
			},
		}

		replicationGroupDestinationMachine = cephfscg.NewRGDMachine(
			k8sClient, rgd, volsync.NewVSHandler(context.Background(), k8sClient, testLogger, rgd,
				&ramendrv1alpha1.VRGAsyncSpec{}, internalController.DefaultCephFSCSIDriverName,
				"Direct", false,
			), testLogger,
		)
	})
	Describe("Cleanup", func() {
		It("Should be success", func() {
			result, err := replicationGroupDestinationMachine.Cleanup(context.Background())
			Expect(err).To(BeNil())
			Expect(result.Completed).To(BeTrue())
		})
	})
	Describe("Synchronize", func() {
		It("Should be failed", func() {
			result, err := replicationGroupDestinationMachine.Synchronize(context.Background())
			Expect(err).To(BeNil())
			Expect(result.Completed).To(BeFalse())
		})
		Describe("There is RD Specs in the replicaiton group destination", func() {
			BeforeEach(func() {
				metaTime := metav1.NewTime(time.Now())
				rgd := &ramendrv1alpha1.ReplicationGroupDestination{
					ObjectMeta: metav1.ObjectMeta{
						Name:      rgdName,
						Namespace: "default",
						UID:       "123",
						Labels:    map[string]string{volsync.VRGOwnerNameLabel: vrgName},
					},
					Spec: ramendrv1alpha1.ReplicationGroupDestinationSpec{
						RDSpecs: []ramendrv1alpha1.VolSyncReplicationDestinationSpec{{
							ProtectedPVC: ramendrv1alpha1.ProtectedPVC{
								Name:               protectedPVCName,
								Namespace:          "default",
								ProtectedByVolSync: true,
								StorageClassName:   &scName,
								AccessModes:        []corev1.PersistentVolumeAccessMode{corev1.ReadOnlyMany},
								Resources: corev1.VolumeResourceRequirements{
									Limits: map[corev1.ResourceName]resource.Quantity{
										corev1.ResourceStorage: *resource.NewQuantity(1, resource.BinarySI),
									},
									Requests: map[corev1.ResourceName]resource.Quantity{
										corev1.ResourceStorage: *resource.NewQuantity(1, resource.BinarySI),
									},
								},
							},
						}},
					},
					Status: ramendrv1alpha1.ReplicationGroupDestinationStatus{
						LastSyncStartTime: &metaTime,
					},
				}

				replicationGroupDestinationMachine = cephfscg.NewRGDMachine(
					mgrClient, rgd, volsync.NewVSHandler(context.Background(), mgrClient, testLogger, rgd,
						&ramendrv1alpha1.VRGAsyncSpec{}, internalController.DefaultCephFSCSIDriverName,
						"Direct", false,
					), testLogger,
				)

				pskSecretName := volsync.GetVolSyncPSKSecretNameFromVRGName(vrgName)
				err := k8sClient.Create(
					context.Background(),
					&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: pskSecretName, Namespace: "default"}},
				)
				Expect(client.IgnoreAlreadyExists(err)).To(BeNil())
				CreateStorageClass()
				CreateVolumeSnapshotClass()
				CreateVS(vsName)
				UpdateVS(vsName)
			})
			It("Should be failed", func() {
				Eventually(func() (bool, error) {
					result, err := replicationGroupDestinationMachine.Synchronize(context.Background())
					if err != nil {
						return false, err
					}

					// fake func to act as the volsync
					err = UpdateRplicationDestination(protectedPVCName)
					if err != nil {
						return false, err
					}

					return result.Completed, nil
				}, timeout, interval).Should(BeElementOf(true, nil))
			})
		})
	})
})

var (
	rgdName          = "rgd"
	vscName          = "vsc"
	protectedPVCName = "vs"
)

func CreateVolumeSnapshotClass() {
	volumeSnapshotClass := &snapv1.VolumeSnapshotClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: vscName,
		},
		DeletionPolicy: snapv1.VolumeSnapshotContentDelete,
		Driver:         internalController.DefaultCephFSCSIDriverName,
	}

	Eventually(func() error {
		err := k8sClient.Create(context.TODO(), volumeSnapshotClass)

		return client.IgnoreAlreadyExists(err)
	}, timeout, interval).Should(BeNil())
}

func UpdateRplicationDestination(pvcName string) error {
	address := "127.0.0.1"
	rd := &volsyncv1alpha1.ReplicationDestination{}

	err := k8sClient.Get(context.Background(),
		types.NamespacedName{Name: pvcName, Namespace: "default"}, rd)
	if err != nil {
		testLogger.Error(err, "failed to get rd")

		return err
	}

	rd.Status = &volsyncv1alpha1.ReplicationDestinationStatus{
		RsyncTLS: &volsyncv1alpha1.ReplicationDestinationRsyncTLSStatus{
			Address: &address,
		},
		LastManualSync: rd.Spec.Trigger.Manual,
		LatestImage: &corev1.TypedLocalObjectReference{
			Name: vsName,
		},
	}

	err = k8sClient.Status().Update(context.Background(), rd)
	if err != nil {
		testLogger.Error(err, "failed to update")

		return err
	}

	testLogger.Info("rd is successfully to update")

	return nil
}
