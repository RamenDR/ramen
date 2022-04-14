package volsync_test

import (
	"context"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/ramendr/ramen/controllers/volsync"

	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v4/apis/volumesnapshot/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
)

var (
	k8sClient                   client.Client
	testEnv                     *envtest.Environment
	cancel                      context.CancelFunc
	ctx                         context.Context
	testStorageClassName        = "test.storageclass"
	testStorageClass            *storagev1.StorageClass
	testVolumeSnapshotClassName = "test.vol.snapclass"
	testVolumeSnapshotClass     *snapv1.VolumeSnapshotClass
	testStorageDriverName       = "test.storage.provisioner"
)

func TestVolsync(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Volsync Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "crd", "bases"),
			filepath.Join("..", "..", "hack", "test"),
		},
	}

	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = volsyncv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = snapv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:             scheme.Scheme,
		MetricsBindAddress: "0",
	})
	Expect(err).ToNot(HaveOccurred())

	// Index fields that are required for VSHandler
	err = volsync.IndexFieldsForVSHandler(context.TODO(), k8sManager.GetFieldIndexer())
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
	testVolumeSnapshotClass = &snapv1.VolumeSnapshotClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: testVolumeSnapshotClassName,
		},
		Driver:         testStorageDriverName,
		DeletionPolicy: snapv1.VolumeSnapshotContentDelete,
	}
	Expect(k8sClient.Create(ctx, testVolumeSnapshotClass)).To(Succeed())
})

var _ = AfterSuite(func() {
	cancel()
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
