package volsync_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/ramendr/ramen/controllers/volsync"

	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v4/apis/volumesnapshot/v1"
	cfgpolicyv1 "github.com/stolostron/config-policy-controller/api/v1"
	policyv1 "github.com/stolostron/governance-policy-propagator/api/v1"
	plrulev1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	"go.uber.org/zap/zapcore"
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
	logger                         logr.Logger
	k8sClient                      client.Client
	testEnv                        *envtest.Environment
	cancel                         context.CancelFunc
	ctx                            context.Context
	testStorageClassName           = "test.storageclass"
	testStorageClass               *storagev1.StorageClass
	testVolumeSnapshotClassName    = "test.vol.snapclass"
	testDefaultVolumeSnapshotClass *snapv1.VolumeSnapshotClass
	testStorageDriverName          = "test.storage.provisioner"

	storageDriverAandB     = "this-is-driver-a-b"
	totalStorageClassCount = 0
	storageClassAandB      *storagev1.StorageClass
	volumeSnapshotClassA   *snapv1.VolumeSnapshotClass
	volumeSnapshotClassB   *snapv1.VolumeSnapshotClass
)

func TestVolsync(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Volsync Suite")
}

var _ = BeforeSuite(func() {
	logger = zap.New(zap.UseFlagOptions(&zap.Options{
		Development: true,
		DestWriter:  GinkgoWriter,
		TimeEncoder: zapcore.ISO8601TimeEncoder,
	}))
	logf.SetLogger(logger)

	ctx, cancel = context.WithCancel(context.TODO())

	By("Setting up KUBEBUILDER_ASSETS for envtest")
	if _, set := os.LookupEnv("KUBEBUILDER_ASSETS"); !set {
		Expect(os.Setenv("KUBEBUILDER_ASSETS", "../../testbin/bin")).To(Succeed())
	}

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

	err = policyv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = plrulev1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = cfgpolicyv1.AddToScheme(scheme.Scheme)
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
	totalStorageClassCount++

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
	totalStorageClassCount++

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
	totalStorageClassCount++
})

var _ = AfterSuite(func() {
	cancel()
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
