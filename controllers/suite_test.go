/*
Copyright 2021 The RamenDR authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers_test

import (
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	volrep "github.com/csi-addons/volume-replication-operator/api/v1alpha1"
	ocmclv1 "github.com/open-cluster-management/api/cluster/v1"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	viewv1beta1 "github.com/open-cluster-management/multicloud-operators-foundation/pkg/apis/view/v1beta1"
	subv1 "github.com/open-cluster-management/multicloud-operators-subscription/pkg/apis"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	ramencontrollers "github.com/ramendr/ramen/controllers"
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	cfg       *rest.Config
	apiReader client.Reader
	k8sClient client.Client
	testEnv   *envtest.Environment
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Controller Suite",
		[]Reporter{printer.NewlineReporter{}})
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "config", "crd", "bases"),
			filepath.Join("..", "hack", "test"),
		},
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = ocmworkv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = ocmclv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = subv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = ramendrv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = viewv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = volrep.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	// +kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	// test controller behavior
	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).ToNot(HaveOccurred())

	Expect((&ramencontrollers.DRPolicyReconciler{
		Client:            k8sManager.GetClient(),
		APIReader:         k8sManager.GetAPIReader(),
		Scheme:            k8sManager.GetScheme(),
		ObjectStoreGetter: fakeObjectStoreGetter{},
	}).SetupWithManager(k8sManager)).To(Succeed())

	err = (&ramencontrollers.VolumeReplicationGroupReconciler{
		Client:         k8sManager.GetClient(),
		APIReader:      k8sManager.GetAPIReader(),
		Log:            ctrl.Log.WithName("controllers").WithName("VolumeReplicationGroup"),
		ObjStoreGetter: ramencontrollers.S3ObjectStoreGetter(),
		PVDownloader:   FakePVDownloader{},
		PVUploader:     FakePVUploader{},
		PVDeleter:      FakePVDeleter{},
		Scheme:         k8sManager.GetScheme(),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	drpcReconciler := (&ramencontrollers.DRPlacementControlReconciler{
		Client:    k8sManager.GetClient(),
		APIReader: k8sManager.GetAPIReader(),
		Log:       ctrl.Log.WithName("controllers").WithName("DRPlacementControl"),
		MCVGetter: FakeMCVGetter{},
		Scheme:    k8sManager.GetScheme(),
		Callback:  FakeProgressCallback,
	})
	err = drpcReconciler.SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	go func() {
		err = k8sManager.Start(ctrl.SetupSignalHandler())
		Expect(err).ToNot(HaveOccurred())
	}()

	k8sClient = k8sManager.GetClient()
	Expect(k8sClient).ToNot(BeNil())
	apiReader = k8sManager.GetAPIReader()
	Expect(apiReader).ToNot(BeNil())
}, 60)

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
