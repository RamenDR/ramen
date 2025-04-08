// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package env

import (
	"context"
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
	// ClusterClaim
	ocmv1a1 "open-cluster-management.io/api/cluster/v1alpha1"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	argocdv1alpha1hack "github.com/ramendr/ramen/e2e/argocd"
	subscription "open-cluster-management.io/multicloud-operators-subscription/pkg/apis"
	placementrule "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"

	"github.com/ramendr/ramen/e2e/types"
)

func addToScheme(scheme *runtime.Scheme) error {
	if err := ocmv1b1.AddToScheme(scheme); err != nil {
		return err
	}

	if err := ocmv1b2.AddToScheme(scheme); err != nil {
		return err
	}

	if err := ocmv1a1.AddToScheme(scheme); err != nil {
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

func setupCluster(
	cluster *types.Cluster,
	key string,
	clusterConfig types.ClusterConfig,
	log *zap.SugaredLogger,
) error {
	client, err := setupClient(clusterConfig.Kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to setup cluster %q: %w", key, err)
	}

	cluster.Client = client
	cluster.Kubeconfig = clusterConfig.Kubeconfig

	switch key {
	case "hub":
		// For hub cluster, use generic name "hub"
		cluster.Name = "hub"
		log.Infof("Using %q cluster name: %q", key, cluster.Name)
	case "c1", "c2":
		// For c1 and c2 clusters, get the cluster name from ClusterClaim
		cluster.Name, err = getClusterClaimName(cluster)
		if err != nil {
			return fmt.Errorf("failed to get ClusterClaim name for the %q managed cluster: %w", key, err)
		}

		log.Infof("Detected %q managed cluster name: %q", key, cluster.Name)
	default:
		// Panic for unexpected keys
		panic(fmt.Sprintf("Unexpected cluster key: %q, expected \"hub\", \"c1\", or \"c2\"", key))
	}

	return nil
}

// getClusterClaimName gets the cluster name using clusterclaims/name resource.
// Returns an error if the resource is not found or retrieval fails.
func getClusterClaimName(cluster *types.Cluster) (string, error) {
	clusterClaim := &ocmv1a1.ClusterClaim{}
	key := client.ObjectKey{Name: "name"}

	err := cluster.Client.Get(context.TODO(), key, clusterClaim)
	if err != nil {
		return "", fmt.Errorf("failed to get ClusterClaim name: %w", err)
	}

	return clusterClaim.Spec.Value, nil
}

func New(config *types.Config, log *zap.SugaredLogger) (*types.Env, error) {
	env := &types.Env{}

	if err := setupCluster(&env.Hub, "hub", config.Clusters["hub"], log); err != nil {
		return nil, fmt.Errorf("failed to create env: %w", err)
	}

	if err := setupCluster(&env.C1, "c1", config.Clusters["c1"], log); err != nil {
		return nil, fmt.Errorf("failed to create env: %w", err)
	}

	if err := setupCluster(&env.C2, "c2", config.Clusters["c2"], log); err != nil {
		return nil, fmt.Errorf("failed to create env: %w", err)
	}

	return env, nil
}

// GetCluster returns the cluster from the env that matches clusterName.
// If not found, it returns an empty Cluster and an error.
func GetCluster(env *types.Env, clusterName string) (types.Cluster, error) {
	switch clusterName {
	case env.C1.Name:
		return env.C1, nil
	case env.C2.Name:
		return env.C2, nil
	case env.Hub.Name:
		return env.Hub, nil
	default:
		return types.Cluster{}, fmt.Errorf("cluster %q not found in environment", clusterName)
	}
}
