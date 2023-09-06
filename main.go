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

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	volrep "github.com/csi-addons/kubernetes-csi-addons/apis/replication.storage/v1alpha1"
	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v4/apis/volumesnapshot/v1"
	clrapiv1beta1 "github.com/open-cluster-management-io/api/cluster/v1beta1"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	viewv1beta1 "github.com/stolostron/multicloud-operators-foundation/pkg/apis/view/v1beta1"
	plrv1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	velero "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	uberzap "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	cpcv1 "open-cluster-management.io/config-policy-controller/api/v1"
	gppv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"

	argov1alpha1 "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/ramendr/ramen/controllers"
	rmnutil "github.com/ramendr/ramen/controllers/util"
	recipe "github.com/ramendr/recipe/api/v1alpha1"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(ramendrv1alpha1.AddToScheme(scheme))
	utilruntime.Must(recipe.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func newManager() (ctrl.Manager, *ramendrv1alpha1.RamenConfig, error) {
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
	ramenConfig := &ramendrv1alpha1.RamenConfig{}

	controllers.LoadControllerConfig(configFile, setupLog, &options, ramenConfig)

	controllers.ControllerType = ramenConfig.RamenControllerType
	if !(controllers.ControllerType == ramendrv1alpha1.DRClusterType ||
		controllers.ControllerType == ramendrv1alpha1.DRHubType) {
		return nil, nil, fmt.Errorf("invalid controller type specified (%s), should be one of [%s|%s]",
			controllers.ControllerType, ramendrv1alpha1.DRHubType, ramendrv1alpha1.DRClusterType)
	}

	if controllers.ControllerType == ramendrv1alpha1.DRHubType {
		utilruntime.Must(plrv1.AddToScheme(scheme))
		utilruntime.Must(ocmworkv1.AddToScheme(scheme))
		utilruntime.Must(viewv1beta1.AddToScheme(scheme))
		utilruntime.Must(cpcv1.AddToScheme(scheme))
		utilruntime.Must(gppv1.AddToScheme(scheme))
		utilruntime.Must(argov1alpha1.AddToScheme(scheme))
		utilruntime.Must(clrapiv1beta1.AddToScheme(scheme))
	} else {
		utilruntime.Must(velero.AddToScheme(scheme))
		utilruntime.Must(volrep.AddToScheme(scheme))
		utilruntime.Must(volsyncv1alpha1.AddToScheme(scheme))
		utilruntime.Must(snapv1.AddToScheme(scheme))
		utilruntime.Must(recipe.AddToScheme(scheme))
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), options)
	if err != nil {
		return mgr, nil, fmt.Errorf("starting new manager failed %w", err)
	}

	return mgr, ramenConfig, nil
}

func setupReconcilers(mgr ctrl.Manager, ramenConfig *ramendrv1alpha1.RamenConfig) {
	if controllers.ControllerType == ramendrv1alpha1.DRHubType {
		setupReconcilersHub(mgr)

		return
	}

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

	// setup webhook for DRCluster
	if err := (&ramendrv1alpha1.DRCluster{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "DRCluster")
		os.Exit(1)
	}

	// setup webhook for DRPolicy
	if err := (&ramendrv1alpha1.DRPolicy{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "DRPolicy")
		os.Exit(1)
	}

	// setup webhook for drpc
	if err := (&ramendrv1alpha1.DRPlacementControl{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "DRPlacementControl")
		os.Exit(1)
	}
}

func main() {
	mgr, ramenConfig, err := newManager()
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
