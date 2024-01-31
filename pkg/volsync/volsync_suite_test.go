// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package volsync_test

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v4/apis/volumesnapshot/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metrics "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	util "github.com/ramendr/ramen/pkg/utils"
	"github.com/ramendr/ramen/test/integration"
)

var (
	logger                         logr.Logger
	k8sClient                      client.Client
	testEnv                        *envtest.Environment
	cfg                            *rest.Config
	cancel                         context.CancelFunc
	ctx                            context.Context
	testStorageClassName           = "test.storageclass"
	testStorageClass               *storagev1.StorageClass
	testVolumeSnapshotClassName    = "test.vol.snapclass"
	testDefaultVolumeSnapshotClass *snapv1.VolumeSnapshotClass
	testStorageDriverName          = "test.storage.provisioner"

	testCephFSStorageClassName        = "test.cephfs.storageclass"
	testCephFSStorageClass            *storagev1.StorageClass
	testCephFSVolumeSnapshotClassName = "test.cephfs.vol.snapclass"
	testCephFSVolumeSnapshotClass     *snapv1.VolumeSnapshotClass
	testCephFSStorageDriverName       = "openshift-storage.cephfs.csi.ceph.com" // This is the real name

	storageDriverAandB   = "this-is-driver-a-b"
	storageClassAandB    *storagev1.StorageClass
	volumeSnapshotClassA *snapv1.VolumeSnapshotClass
	volumeSnapshotClassB *snapv1.VolumeSnapshotClass

	totalVolumeSnapshotClassCount = 0
)

func TestVolsync(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Volsync Suite")
}

var _ = BeforeSuite(func() {
	var err error
	logger = integration.ConfigureTestLogger()

	ctx, cancel = context.WithCancel(context.TODO())

	testEnv, cfg, _, err = integration.ConfigureSetupEnvTest()
	Expect(err).NotTo(HaveOccurred())

	err = integration.AddSchemes()
	Expect(err).NotTo(HaveOccurred())

	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
		Metrics: metrics.Options{
			BindAddress: "0",
		},
	})
	Expect(err).ToNot(HaveOccurred())

	// Index fields that are required for VSHandler
	err = util.IndexFieldsForVSHandler(context.TODO(), k8sManager.GetFieldIndexer())
	Expect(err).ToNot(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = k8sManager.Start(ctx)
		Expect(err).ToNot(HaveOccurred())
	}()

	k8sClient = k8sManager.GetClient()

	// Create dummy storageClass resource to use in tests
	testStorageClass = &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: testStorageClassName,
		},
		Provisioner: testStorageDriverName,
	}
	Expect(k8sClient.Create(ctx, testStorageClass)).To(Succeed())

	// Create dummy volumeSnapshotClass resource to use in tests
	testDefaultVolumeSnapshotClass = &snapv1.VolumeSnapshotClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: testVolumeSnapshotClassName,
			Annotations: map[string]string{
				"snapshot.storage.kubernetes.io/is-default-class": "true",
			},
		},
		Driver:         testStorageDriverName,
		DeletionPolicy: snapv1.VolumeSnapshotContentDelete,
	}
	Expect(k8sClient.Create(ctx, testDefaultVolumeSnapshotClass)).To(Succeed())
	totalVolumeSnapshotClassCount++

	// Create dummy storageClass resource to use in tests
	storageClassAandB = &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testing-storage-class-a-b",
		},
		Provisioner: storageDriverAandB,
	}
	Expect(k8sClient.Create(ctx, storageClassAandB)).To(Succeed())

	volumeSnapshotClassA = &snapv1.VolumeSnapshotClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "thisis-testvsclassa",
			Labels: map[string]string{
				"i-like-ramen": "true",
				"abc":          "a",
			},
		},
		Driver:         storageDriverAandB,
		DeletionPolicy: snapv1.VolumeSnapshotContentDelete,
	}
	Expect(k8sClient.Create(ctx, volumeSnapshotClassA)).To(Succeed())
	totalVolumeSnapshotClassCount++

	volumeSnapshotClassB = &snapv1.VolumeSnapshotClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "thisis-testvsclassb",
			Labels: map[string]string{
				"i-like-ramen": "true",
				"abc":          "b",
			},
		},
		Driver:         storageDriverAandB, // Same storagedriver as volsnapclassA
		DeletionPolicy: snapv1.VolumeSnapshotContentDelete,
	}
	Expect(k8sClient.Create(ctx, volumeSnapshotClassB)).To(Succeed())
	totalVolumeSnapshotClassCount++

	// Create fake cephfs storageclass and volumesnapshotclass
	testCephFSStorageClass = &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: testCephFSStorageClassName,
		},
		Provisioner: testCephFSStorageDriverName,
		Parameters: map[string]string{
			"parameter-a":     "avaluehere",
			"testing-testing": "yes",
		},
	}
	Expect(k8sClient.Create(ctx, testCephFSStorageClass)).To(Succeed())

	testCephFSVolumeSnapshotClass = &snapv1.VolumeSnapshotClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: testCephFSVolumeSnapshotClassName,
		},
		Driver:         testCephFSStorageDriverName,
		DeletionPolicy: snapv1.VolumeSnapshotContentDelete,
	}
	Expect(k8sClient.Create(ctx, testCephFSVolumeSnapshotClass)).To(Succeed())
	totalVolumeSnapshotClassCount++
})

var _ = AfterSuite(func() {
	cancel()
	By("tearing down the test environment")
	err := integration.CleanupSetupEnvTest(testEnv)
	Expect(err).NotTo(HaveOccurred())
})
