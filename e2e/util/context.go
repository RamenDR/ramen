// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"fmt"
	"path/filepath"

	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/kubectl/pkg/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	// Placement
	ocmv1b1 "open-cluster-management.io/api/cluster/v1beta1"
	// ManagedClusterSetBinding
	ocmv1b2 "open-cluster-management.io/api/cluster/v1beta2"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	argocdv1alpha1hack "github.com/ramendr/ramen/e2e/argocd"
	subscription "open-cluster-management.io/multicloud-operators-subscription/pkg/apis"
	placementrule "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
)

type Cluster struct {
	Name   string
	Client client.Client
}

type Context struct {
	Log *zap.SugaredLogger
	Hub Cluster
	C1  Cluster
	C2  Cluster
}

var Ctx *Context

const (
	defaultHubClusterName = "hub"
	defaultC1ClusterName  = "c1"
	defaultC2ClusterName  = "c2"
)

func addToScheme(scheme *runtime.Scheme) error {
	if err := ocmv1b1.AddToScheme(scheme); err != nil {
		return err
	}

	if err := ocmv1b2.AddToScheme(scheme); err != nil {
		return err
	}

	if err := placementrule.AddToScheme(scheme); err != nil {
		return err
	}

	if err := subscription.AddToScheme(scheme); err != nil {
		return err
	}

	if err := argocdv1alpha1hack.AddToScheme(scheme); err != nil {
		return err
	}

	return ramen.AddToScheme(scheme)
}

func setupClient(kubeconfigPath string) (client.Client, error) {
	var err error

	if kubeconfigPath == "" {
		return nil, fmt.Errorf("kubeconfigPath is empty")
	}

	kubeconfigPath, err = filepath.Abs(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("unable to determine absolute path to file (%s): %w", kubeconfigPath, err)
	}

	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to build config from kubeconfig (%s): %w", kubeconfigPath, err)
	}

	if err := addToScheme(scheme.Scheme); err != nil {
		return nil, err
	}

	client, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to build controller client from kubeconfig (%s): %w", kubeconfigPath, err)
	}

	return client, nil
}

func NewContext(log *zap.SugaredLogger, configFile string) (*Context, error) {
	var err error

	ctx := new(Context)
	ctx.Log = log

	if err := ReadConfig(configFile); err != nil {
		panic(err)
	}

	ctx.Hub.Name = config.Clusters["hub"].Name
	if ctx.Hub.Name == "" {
		ctx.Hub.Name = defaultHubClusterName
		log.Infof("Cluster \"hub\" name not set, using default name %q", defaultHubClusterName)
	}

	ctx.Hub.Client, err = setupClient(config.Clusters["hub"].KubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create clients for hub cluster: %w", err)
	}

	ctx.C1.Name = config.Clusters["c1"].Name
	if ctx.C1.Name == "" {
		ctx.C1.Name = defaultC1ClusterName
		log.Infof("Cluster \"c1\" name not set, using default name %q", defaultC1ClusterName)
	}

	ctx.C1.Client, err = setupClient(config.Clusters["c1"].KubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create clients for c1 cluster: %w", err)
	}

	ctx.C2.Name = config.Clusters["c2"].Name
	if ctx.C2.Name == "" {
		ctx.C2.Name = defaultC2ClusterName
		log.Infof("Cluster \"c2\" name not set, using default name %q", defaultC2ClusterName)
	}

	ctx.C2.Client, err = setupClient(config.Clusters["c2"].KubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create clients for c2 cluster: %w", err)
	}

	return ctx, nil
}
