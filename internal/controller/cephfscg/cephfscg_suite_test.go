// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package cephfscg_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	"github.com/go-logr/logr"
	groupsnapv1beta1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumegroupsnapshot/v1beta1"
	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/testutils"
	"github.com/ramendr/ramen/internal/controller/util"
	plrv1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	"go.uber.org/zap/zapcore"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	cpcv1 "open-cluster-management.io/config-policy-controller/api/v1"
	gppv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

const (
	timeout  = time.Second * 10
	interval = time.Millisecond * 10
)

var (
	cfg         *rest.Config
	k8sClient   client.Client
	mgrClient   client.Client
	testEnv     *envtest.Environment
	secretsUtil util.SecretsUtil
	testLogger  logr.Logger
	CtxCancel   context.CancelFunc
	Ctx         context.Context
)

func TestCephfscg(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Cephfscg Suite")
}

var _ = BeforeSuite(func() {
	Ctx, CtxCancel = context.WithCancel(context.TODO())
	testutils.ConfigureGinkgo()
	testLogger = zap.New(zap.UseFlagOptions(&zap.Options{
		Development: true,
		DestWriter:  GinkgoWriter,
		TimeEncoder: zapcore.ISO8601TimeEncoder,
	}))
	logf.SetLogger(testLogger)
	testLog := ctrl.Log.WithName("tester")
	testLog.Info("Starting the utils test suite", "time", time.Now())

	By("Setting up KUBEBUILDER_ASSETS for envtest")
	if _, set := os.LookupEnv("KUBEBUILDER_ASSETS"); !set {
		testLog.Info("Setting up KUBEBUILDER_ASSETS for envtest")

		// read content of the file ../../../testbin/testassets.txt
		// and set the content as the value of KUBEBUILDER_ASSETS
		// this is to avoid the need to set KUBEBUILDER_ASSETS
		// when running the test suite
		content, err := os.ReadFile("../../../testbin/testassets.txt")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Setenv("KUBEBUILDER_ASSETS", string(content))).To(Succeed())
	}

	By("Bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "..", "config", "crd", "bases"),
			filepath.Join("..", "..", "..", "hack", "test"),
		},
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	By("Setting up required schemes in envtest")
	err = plrv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = cpcv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = gppv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = volsyncv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = ramendrv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = groupsnapv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = snapv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0", // Disable metrics
		},
	})
	Expect(err).ToNot(HaveOccurred())

	// Index fields that are required for VSHandler
	err = util.IndexFieldsForVSHandler(context.TODO(), k8sManager.GetFieldIndexer())
	Expect(err).ToNot(HaveOccurred())

	By("Creating a k8s client")
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())
	mgrClient = k8sManager.GetClient()

	secretsUtil = util.SecretsUtil{
		Client:    k8sClient,
		APIReader: k8sClient,
		Ctx:       context.TODO(),
		Log:       ctrl.Log.WithName("secrets_util"),
	}
	go func() {
		defer GinkgoRecover()
		err = k8sManager.Start(Ctx)
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	CtxCancel()
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
