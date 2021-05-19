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

package main

import (
	"flag"
	"fmt"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	volrep "github.com/csi-addons/volume-replication-operator/api/v1alpha1"
	spokeClusterV1 "github.com/open-cluster-management/api/cluster/v1"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	plrv1 "github.com/open-cluster-management/multicloud-operators-placementrule/pkg/apis/apps/v1"
	subv1 "github.com/open-cluster-management/multicloud-operators-subscription/pkg/apis/apps/v1"
	fndv2 "github.com/tjanssen3/multicloud-operators-foundation/v2/pkg/apis/view/v1beta1"

	uberzap "go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(ramendrv1alpha1.AddToScheme(scheme))
	utilruntime.Must(volrep.AddToScheme(scheme))
	utilruntime.Must(subv1.SchemeBuilder.AddToScheme(scheme))
	utilruntime.Must(plrv1.AddToScheme(scheme))
	utilruntime.Must(ocmworkv1.AddToScheme(scheme))
	utilruntime.Must(spokeClusterV1.AddToScheme(scheme))
	utilruntime.Must(fndv2.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func newManager() (ctrl.Manager, error) {
	var (
		metricsAddr          string
		enableLeaderElection bool
		probeAddr            string
		portAddr             = 9443
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")

	opts := zap.Options{
		Development: true,
		ZapOpts: []uberzap.Option{
			uberzap.AddCaller(),
		},
	}

	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   portAddr,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "ae40cfcb.openshift.io",
	})
	if err != nil {
		return mgr, fmt.Errorf("starting new manager failed %w", err)
	}

	return mgr, nil
}

func setupReconcilers(mgr ctrl.Manager) {
	if err := (&controllers.VolumeReplicationGroupReconciler{
		Client:         mgr.GetClient(),
		Log:            ctrl.Log.WithName("controllers").WithName("VolumeReplicationGroup"),
		ObjStoreGetter: controllers.S3ObjectStoreGetter(),
		Scheme:         mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "VolumeReplicationGroup")
		os.Exit(1)
	}

	avrReconciler := (&controllers.ApplicationVolumeReplicationReconciler{
		Client:         mgr.GetClient(),
		Log:            ctrl.Log.WithName("controllers").WithName("ApplicationVolumeReplication"),
		ObjStoreGetter: controllers.S3ObjectStoreGetter(),
		PVDownloader:   controllers.ObjectStorePVDownloader{},
		Scheme:         mgr.GetScheme(),
		Callback:       func(string, bool) {},
	})
	if err := avrReconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ApplicationVolumeReplication")
		os.Exit(1)
	}
}

func main() {
	mgr, err := newManager()
	if err != nil {
		setupLog.Error(err, "unable to Get new manager")
		os.Exit(1)
	}

	setupReconcilers(mgr)

	// +kubebuilder:scaffold:builder
	if err := mgr.AddHealthzCheck("health", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}

	if err := mgr.AddReadyzCheck("check", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
