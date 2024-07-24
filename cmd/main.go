// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	volrep "github.com/csi-addons/kubernetes-csi-addons/apis/replication.storage/v1alpha1"
	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v4/apis/volumesnapshot/v1"
	clrapiv1beta1 "github.com/open-cluster-management-io/api/cluster/v1beta1"
	ocmv1 "github.com/open-cluster-management/api/cluster/v1"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	viewv1beta1 "github.com/stolostron/multicloud-operators-foundation/pkg/apis/view/v1beta1"
	plrv1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	velero "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	uberzap "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	cpcv1 "open-cluster-management.io/config-policy-controller/api/v1"
	gppv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"

	controllers "github.com/ramendr/ramen/internal/controller"
	argocdv1alpha1hack "github.com/ramendr/ramen/internal/controller/argocd"
	rmnutil "github.com/ramendr/ramen/internal/controller/util"
	recipe "github.com/ramendr/recipe/api/v1alpha1"
	// +kubebuilder:scaffold:imports
)

var (
	scheme     = runtime.NewScheme()
	setupLog   = ctrl.Log.WithName("setup")
	configFile string
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(ramendrv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func configureLogOptions() *zap.Options {
	opts := zap.Options{
		Development: true,
		ZapOpts: []uberzap.Option{
			uberzap.AddCaller(),
		},
		TimeEncoder: zapcore.ISO8601TimeEncoder,
	}

	return &opts
}

// bindFlags takes a list of functions that bind flags to variables.
// In addition, any ramen specific flags are bound here.
func bindFlags(bindfuncs ...func(*flag.FlagSet)) {
	flag.StringVar(&configFile, "config", "",
		"The controller will load its initial configuration from this file. "+
			"Omit this flag to use the default configuration values. "+
			"Command-line flags override configuration from this file.")

	for _, f := range bindfuncs {
		f(flag.CommandLine)
	}
}

func buildOptions() (*ctrl.Options, *ramendrv1alpha1.RamenConfig) {
	ctrlOptions := ctrl.Options{Scheme: scheme}

	ramenConfig := controllers.LoadControllerConfig(configFile, setupLog)
	controllers.LoadControllerOptions(&ctrlOptions, ramenConfig)

	return &ctrlOptions, ramenConfig
}

func configureController(ramenConfig *ramendrv1alpha1.RamenConfig) error {
	controllers.ControllerType = ramenConfig.RamenControllerType

	if !(controllers.ControllerType == ramendrv1alpha1.DRClusterType ||
		controllers.ControllerType == ramendrv1alpha1.DRHubType) {
		return fmt.Errorf("invalid controller type specified (%s), should be one of [%s|%s]",
			controllers.ControllerType, ramendrv1alpha1.DRHubType, ramendrv1alpha1.DRClusterType)
	}

	setupLog.Info("controller type", "type", controllers.ControllerType)

	if controllers.ControllerType == ramendrv1alpha1.DRHubType {
		utilruntime.Must(plrv1.AddToScheme(scheme))
		utilruntime.Must(ocmworkv1.AddToScheme(scheme))
		utilruntime.Must(viewv1beta1.AddToScheme(scheme))
		utilruntime.Must(cpcv1.AddToScheme(scheme))
		utilruntime.Must(gppv1.AddToScheme(scheme))
		utilruntime.Must(argocdv1alpha1hack.AddToScheme(scheme))
		utilruntime.Must(clrapiv1beta1.AddToScheme(scheme))
		utilruntime.Must(recipe.AddToScheme(scheme))
		utilruntime.Must(ocmv1.AddToScheme(scheme))
	} else {
		utilruntime.Must(velero.AddToScheme(scheme))
		utilruntime.Must(volrep.AddToScheme(scheme))
		utilruntime.Must(volsyncv1alpha1.AddToScheme(scheme))
		utilruntime.Must(snapv1.AddToScheme(scheme))
		utilruntime.Must(recipe.AddToScheme(scheme))
		utilruntime.Must(apiextensions.AddToScheme(scheme))
	}

	return nil
}

func newManager(options *ctrl.Options) (ctrl.Manager, error) {
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), *options)
	if err != nil {
		return mgr, fmt.Errorf("starting new manager failed %w", err)
	}

	return mgr, nil
}

func setupReconcilers(mgr ctrl.Manager, ramenConfig *ramendrv1alpha1.RamenConfig) {
	if controllers.ControllerType == ramendrv1alpha1.DRHubType {
		setupReconcilersHub(mgr)
	}

	if controllers.ControllerType == ramendrv1alpha1.DRClusterType {
		setupReconcilersCluster(mgr, ramenConfig)
	}
}

func setupReconcilersCluster(mgr ctrl.Manager, ramenConfig *ramendrv1alpha1.RamenConfig) {
	if err := (&controllers.ProtectedVolumeReplicationGroupListReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		APIReader:      mgr.GetAPIReader(),
		ObjStoreGetter: controllers.S3ObjectStoreGetter(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ProtectedVolumeReplicationGroupList")
		os.Exit(1)
	}

	// Index fields that are required for VSHandler
	if err := rmnutil.IndexFieldsForVSHandler(context.Background(), mgr.GetFieldIndexer()); err != nil {
		setupLog.Error(err, "unable to index fields for controller", "controller", "VolumeReplicationGroup")
		os.Exit(1)
	}

	if err := (&controllers.VolumeReplicationGroupReconciler{
		Client:         mgr.GetClient(),
		APIReader:      mgr.GetAPIReader(),
		Log:            ctrl.Log.WithName("controllers").WithName("VolumeReplicationGroup"),
		ObjStoreGetter: controllers.S3ObjectStoreGetter(),
		Scheme:         mgr.GetScheme(),
	}).SetupWithManager(mgr, ramenConfig); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "VolumeReplicationGroup")
		os.Exit(1)
	}

	if err := (&controllers.DRClusterConfigReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "DRClusterConfig")
		os.Exit(1)
	}
}

func setupReconcilersHub(mgr ctrl.Manager) {
	if err := (&controllers.DRPolicyReconciler{
		Client:            mgr.GetClient(),
		APIReader:         mgr.GetAPIReader(),
		Log:               ctrl.Log.WithName("controllers").WithName("DRPolicy"),
		Scheme:            mgr.GetScheme(),
		ObjectStoreGetter: controllers.S3ObjectStoreGetter(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "DRPolicy")
		os.Exit(1)
	}

	if err := (&controllers.DRClusterReconciler{
		Client:    mgr.GetClient(),
		APIReader: mgr.GetAPIReader(),
		Log:       ctrl.Log.WithName("controllers").WithName("DRCluster"),
		Scheme:    mgr.GetScheme(),
		MCVGetter: rmnutil.ManagedClusterViewGetterImpl{
			Client:    mgr.GetClient(),
			APIReader: mgr.GetAPIReader(),
		},
		ObjectStoreGetter: controllers.S3ObjectStoreGetter(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "DRCluster")
		os.Exit(1)
	}

	if err := (&controllers.DRPlacementControlReconciler{
		Client:    mgr.GetClient(),
		APIReader: mgr.GetAPIReader(),
		Log:       ctrl.Log.WithName("controllers").WithName("DRPlacementControl"),
		MCVGetter: rmnutil.ManagedClusterViewGetterImpl{
			Client:    mgr.GetClient(),
			APIReader: mgr.GetAPIReader(),
		},
		Scheme:         mgr.GetScheme(),
		Callback:       func(string, string) {},
		ObjStoreGetter: controllers.S3ObjectStoreGetter(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "DRPlacementControl")
		os.Exit(1)
	}
}

func main() {
	logOpts := configureLogOptions()
	bindFlags(logOpts.BindFlags)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(logOpts)))

	ctrlOptions, ramenConfig := buildOptions()

	if err := configureController(ramenConfig); err != nil {
		setupLog.Error(err, "unable to configure controller")
		os.Exit(1)
	}

	mgr, err := newManager(ctrlOptions)
	if err != nil {
		setupLog.Error(err, "unable to Get new manager")
		os.Exit(1)
	}

	setupReconcilers(mgr, ramenConfig)

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
