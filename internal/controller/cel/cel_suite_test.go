// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package cel_test

import (
	// "context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"

	// "github.com/ramendr/ramen/internal/controller/util"
	"github.com/ramendr/ramen/internal/controller/testutils"
	"go.uber.org/zap/zapcore"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const (
	timeout  = time.Second * 10
	interval = time.Millisecond * 10
)

var (
	cfg        *rest.Config
	k8sClient  client.Client
	testEnv    *envtest.Environment
	testLogger logr.Logger
)

func TestUtil(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CEL Suite")
}

var _ = BeforeSuite(func() {
	testutils.ConfigureGinkgo()
	testLogger = zap.New(zap.UseFlagOptions(&zap.Options{
		Development: true,
		DestWriter:  GinkgoWriter,
		TimeEncoder: zapcore.ISO8601TimeEncoder,
	}))
	logf.SetLogger(testLogger)
	testLog := ctrl.Log.WithName("tester")
	testLog.Info("Starting the CEL test suite", "time", time.Now())

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
	err = ramendrv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	By("Creating a k8s client")
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
