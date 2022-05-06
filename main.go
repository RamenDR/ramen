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
	"context"
	"flag"
	"fmt"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	volrep "github.com/csi-addons/kubernetes-csi-addons/apis/replication.storage/v1alpha1"
	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v4/apis/volumesnapshot/v1"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	cpcv1 "github.com/stolostron/config-policy-controller/api/v1"
	gppv1 "github.com/stolostron/governance-policy-propagator/api/v1"
	viewv1beta1 "github.com/stolostron/multicloud-operators-foundation/pkg/apis/view/v1beta1"
	plrv1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	velero "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	uberzap "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"

	"github.com/ramendr/ramen/controllers"
	rmnutil "github.com/ramendr/ramen/controllers/util"
	"github.com/ramendr/ramen/controllers/volsync"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(ramendrv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func newManager() (ctrl.Manager, error) {
	var configFile string

	flag.StringVar(&configFile, "config", "",
		"The controller will load its initial configuration from this file. "+
			"Omit this flag to use the default configuration values. "+
			"Command-line flags override configuration from this file.")

	opts := zap.Options{
		Development: true,
		ZapOpts: []uberzap.Option{
			uberzap.AddCaller(),
		},
		TimeEncoder: zapcore.ISO8601TimeEncoder,
	}

	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	options := ctrl.Options{Scheme: scheme}
	ramenConfig := ramendrv1alpha1.RamenConfig{}

	if configFile != "" {
		options, ramenConfig = controllers.LoadControllerConfig(configFile, scheme, setupLog)
	}

	controllers.ControllerType = ramenConfig.RamenControllerType
	if !(controllers.ControllerType == ramendrv1alpha1.DRClusterType ||
		controllers.ControllerType == ramendrv1alpha1.DRHubType) {
		return nil, fmt.Errorf("invalid controller type specified (%s), should be one of [%s|%s]",
			controllers.ControllerType, ramendrv1alpha1.DRHubType, ramendrv1alpha1.DRClusterType)
	}

	if controllers.ControllerType == ramendrv1alpha1.DRHubType {
		utilruntime.Must(plrv1.AddToScheme(scheme))
		utilruntime.Must(ocmworkv1.AddToScheme(scheme))
		utilruntime.Must(viewv1beta1.AddToScheme(scheme))
		utilruntime.Must(cpcv1.AddToScheme(scheme))
		utilruntime.Must(gppv1.AddToScheme(scheme))
	} else {
		utilruntime.Must(velero.AddToScheme(scheme))
		utilruntime.Must(volrep.AddToScheme(scheme))
		utilruntime.Must(volsyncv1alpha1.AddToScheme(scheme))
		utilruntime.Must(snapv1.AddToScheme(scheme))
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), options)
	if err != nil {
		return mgr, fmt.Errorf("starting new manager failed %w", err)
	}

	return mgr, nil
}

func setupReconcilers(mgr ctrl.Manager) {
	if controllers.ControllerType == ramendrv1alpha1.DRHubType {
		setupReconcilersHub(mgr)

		return
	}

	if err := (&controllers.S3BucketViewReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "S3BucketView")
		os.Exit(1)
	}

	// Index fields that are required for VSHandler
	if err := volsync.IndexFieldsForVSHandler(context.Background(), mgr.GetFieldIndexer()); err != nil {
		setupLog.Error(err, "unable to index fields for controller", "controller", "VolumeReplicationGroup")
		os.Exit(1)
	}

	if err := (&controllers.VolumeReplicationGroupReconciler{
		Client:         mgr.GetClient(),
		APIReader:      mgr.GetAPIReader(),
		Log:            ctrl.Log.WithName("controllers").WithName("VolumeReplicationGroup"),
		ObjStoreGetter: controllers.S3ObjectStoreGetter(),
		Scheme:         mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "VolumeReplicationGroup")
		os.Exit(1)
	}
}

func setupReconcilersHub(mgr ctrl.Manager) {
	if err := (&controllers.DRPolicyReconciler{
		Client:            mgr.GetClient(),
		APIReader:         mgr.GetAPIReader(),
		Scheme:            mgr.GetScheme(),
		ObjectStoreGetter: controllers.S3ObjectStoreGetter(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "DRPolicy")
		os.Exit(1)
	}

	if err := (&controllers.DRClusterReconciler{
		Client:            mgr.GetClient(),
		APIReader:         mgr.GetAPIReader(),
		Scheme:            mgr.GetScheme(),
		MCVGetter:         rmnutil.ManagedClusterViewGetterImpl{Client: mgr.GetClient()},
		ObjectStoreGetter: controllers.S3ObjectStoreGetter(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "DRCluster")
		os.Exit(1)
	}

	if err := (&controllers.DRPlacementControlReconciler{
		Client:    mgr.GetClient(),
		APIReader: mgr.GetAPIReader(),
		Log:       ctrl.Log.WithName("controllers").WithName("DRPlacementControl"),
		MCVGetter: rmnutil.ManagedClusterViewGetterImpl{Client: mgr.GetClient()},
		Scheme:    mgr.GetScheme(),
		Callback:  func(string, string) {},
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "DRPlacementControl")
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
