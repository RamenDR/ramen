// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package volsync_test

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v4/apis/volumesnapshot/v1"
	plrulev1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	"go.uber.org/zap/zapcore"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	cfgpolicyv1 "open-cluster-management.io/config-policy-controller/api/v1"
	policyv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metrics "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	"github.com/ramendr/ramen/controllers"
	"github.com/ramendr/ramen/controllers/util"
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

	skipCleanup bool
	cfg         *rest.Config
)

func TestVolsync(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Volsync Suite")
}

var _ = BeforeSuite(func() {
	var err error

	logger = zap.New(zap.UseFlagOptions(&zap.Options{
		Development: true,
		DestWriter:  GinkgoWriter,
		TimeEncoder: zapcore.ISO8601TimeEncoder,
	}))
	logf.SetLogger(logger)

	ctx, cancel = context.WithCancel(context.TODO())

	By("Setting up KUBEBUILDER_ASSETS for envtest")
	if _, set := os.LookupEnv("KUBEBUILDER_ASSETS"); !set {

		// read content of the file ../../testbin/testassets.txt
		// and set the content as the value of KUBEBUILDER_ASSETS
		// this is to avoid the need to set KUBEBUILDER_ASSETS
		// when running the test suite
		content, err := os.ReadFile("../../testbin/testassets.txt")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Setenv("KUBEBUILDER_ASSETS", string(content))).To(Succeed())
	}

	skipCleanupEnv, set := os.LookupEnv("SKIP_CLEANUP")
	if set && (skipCleanupEnv == "true" || skipCleanupEnv == "1") {
		skipCleanup = true
	}

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "crd", "bases"),
			filepath.Join("..", "..", "hack", "test"),
		},
	}

	cfg, err = testEnv.Start()
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
	if skipCleanup {
		kubeConfigContent, err := controllers.ConvertRestConfigToKubeConfig(cfg)
		if err != nil {
			log.Fatalf("Failed to convert rest.Config to kubeconfig: %v", err)
		}

		filePath := "../../testbin/kubeconfig.yaml"
		if err := controllers.WriteKubeConfigToFile(kubeConfigContent, filePath); err != nil {
			log.Fatalf("Failed to write kubeconfig file: %v", err)
		}

		return
	}
	cancel()
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
