// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package validate

import (
	"fmt"
	"slices"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
)

func RamenHubOperator(ctx types.Context, cluster *types.Cluster) error {
	config := ctx.Config()
	log := ctx.Logger()

	labelSelector := "app=ramen-hub"
	podIdentifier := "ramen-hub-operator"

	pod, err := FindPod(ctx, cluster, config.Namespaces.RamenHubNamespace, labelSelector, podIdentifier)
	if err != nil {
		return err
	}

	if pod.Status.Phase != "Running" {
		return fmt.Errorf("ramen hub operator pod %q not running (phase %q) in cluster %q",
			pod.Name, pod.Status.Phase, cluster.Name)
	}

	log.Infof("Ramen hub operator pod %q is running in cluster %q", pod.Name, cluster.Name)

	return nil
}

func RamenDRClusterOperator(ctx types.Context, cluster *types.Cluster) error {
	config := ctx.Config()
	log := ctx.Logger()

	labelSelector := "app=ramen-dr-cluster"
	podIdentifier := "ramen-dr-cluster-operator"

	pod, err := FindPod(ctx, cluster, config.Namespaces.RamenDRClusterNamespace, labelSelector, podIdentifier)
	if err != nil {
		return err
	}

	if pod.Status.Phase != "Running" {
		return fmt.Errorf("ramen dr cluster operator pod %q not running (phase %q) in cluster %q",
			pod.Name, pod.Status.Phase, cluster.Name)
	}

	log.Infof("Ramen dr cluster operator pod %q is running in cluster %q", pod.Name, cluster.Name)

	return nil
}

// FindPod returns the first pod matching the label selector including the pod identifier in the namespace.
func FindPod(ctx types.Context, cluster *types.Cluster, namespace, labelSelector, podIdentifier string) (
	*v1.Pod, error,
) {
	ls, err := labels.Parse(labelSelector)
	if err != nil {
		return nil, fmt.Errorf("failed to parse label selector %q in cluster %q: %v", labelSelector, cluster.Name, err)
	}

	pods := &v1.PodList{}
	listOptions := []client.ListOption{
		client.InNamespace(namespace),
		client.MatchingLabelsSelector{
			Selector: ls,
		},
	}

	err = cluster.Client.List(ctx.Context(), pods, listOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to list pods in namespace %s in cluster %q: %v", namespace, cluster.Name, err)
	}

	for i := range pods.Items {
		pod := &pods.Items[i]
		if strings.Contains(pod.Name, podIdentifier) {
			return pod, nil
		}
	}

	return nil, fmt.Errorf("no pod with label selector %q and identifier %q in namespace %q in cluster %q",
		labelSelector, podIdentifier, namespace, cluster.Name)
}

// TestConfig is a wrapper function which performs validation checks on
// the test environment configurations with the DR resources on the clusters.
// Returns an error if any validation fails.
func TestConfig(ctx types.Context) error {
	if err := detectDistro(ctx); err != nil {
		return fmt.Errorf("failed to validate test config: %w", err)
	}

	if err := validateStorageClasses(ctx); err != nil {
		return fmt.Errorf("failed to validate test config: %w", err)
	}

	if err := clustersInDRPolicy(ctx); err != nil {
		return fmt.Errorf("failed to validate test config: %w", err)
	}

	if err := clustersInClusterSet(ctx); err != nil {
		return fmt.Errorf("failed to validate test config: %w", err)
	}

	return nil
}

// clustersInDRPolicy checks if configured clusters match the configured
// drpolicy. Returns an error if cluster names are not the same as drpolicy
// drclusters. The reason for a failure may be wrong cluster name or wrong
// drpolicy.
func clustersInDRPolicy(ctx types.Context) error {
	env := ctx.Env()
	config := ctx.Config()
	log := ctx.Logger()

	drpolicy, err := util.GetDRPolicy(ctx, env.Hub, config.DRPolicy)
	if err != nil {
		return fmt.Errorf("failed to get DRPolicy %q: %w", config.DRPolicy, err)
	}

	clusters := []*types.Cluster{env.C1, env.C2}
	for _, cluster := range clusters {
		if !slices.Contains(drpolicy.Spec.DRClusters, cluster.Name) {
			return fmt.Errorf("cluster %q is not defined in drpolicy %q, clusters in drpolicy: %q",
				cluster.Name, config.DRPolicy, drpolicy.Spec.DRClusters)
		}
	}

	log.Infof("Validated clusters [%q, %q] in DRPolicy %q", clusters[0].Name, clusters[1].Name, config.DRPolicy)

	return nil
}

// clustersInClusterSet checks if configured clusters exists in configured clusterset.
// Returns an error if provided cluster names are not the same as managedclusters in clusterset.
// The reason for a failure may be wrong cluster name or wrong clusterset.
func clustersInClusterSet(ctx types.Context) error {
	env := ctx.Env()
	config := ctx.Config()
	log := ctx.Logger()

	if _, err := getClusterSet(ctx, config.ClusterSet); err != nil {
		return err
	}

	clusterNames, err := getManagedClustersFromClusterSet(ctx, config.ClusterSet)
	if err != nil {
		return err
	}

	clusters := []*types.Cluster{env.C1, env.C2}
	for _, cluster := range clusters {
		if !slices.Contains(clusterNames, cluster.Name) {
			return fmt.Errorf("cluster %q is not defined in ClusterSet %q, clusters in ClusterSet: %q",
				cluster.Name, config.ClusterSet, clusterNames)
		}
	}

	log.Infof("Validated clusters [%q, %q] in ClusterSet %q", clusters[0].Name, clusters[1].Name, config.ClusterSet)

	return nil
}

// detectDistro detects the cluster distribution. If distro is set in config, it uses the user
// provided distro. Otherwise, it determines the distro, sets config.Distro and config.Namespaces.
// Returns an error if distro detection fails or if clusters have inconsistent distributions.
func detectDistro(ctx types.Context) error {
	env := ctx.Env()
	cfg := ctx.Config()
	log := ctx.Logger()

	if cfg.Distro != "" {
		log.Infof("Using user selected kubernetes distribution: %q", cfg.Distro)

		return nil
	}

	var detectedDistro string

	clusters := []*types.Cluster{env.Hub, env.C1, env.C2}
	for _, cluster := range clusters {
		distro, err := probeDistro(ctx, cluster)
		if err != nil {
			return fmt.Errorf("failed to determine distro for cluster %q: %w", cluster.Name, err)
		}

		if detectedDistro == "" {
			detectedDistro = distro
		} else if detectedDistro != distro {
			return fmt.Errorf("clusters have inconsistent distributions, cluster %q has distro %q, expected %q",
				cluster.Name, distro, detectedDistro)
		}
	}

	cfg.Distro = detectedDistro

	switch cfg.Distro {
	case config.DistroOcp:
		cfg.Namespaces = config.OcpNamespaces
	case config.DistroK8s:
		cfg.Namespaces = config.K8sNamespaces
	default:
		panic(fmt.Sprintf("unexpected distro: %q", cfg.Distro))
	}

	log.Infof("Detected kubernetes distribution: %q", cfg.Distro)
	log.Infof("Using namespaces: %+v", cfg.Namespaces)

	return nil
}

// probeDistro determines the distribution of the given Kubernetes cluster.
// Returns the detected distribution name ("ocp" or "k8s") or an error if detection fails.
func probeDistro(ctx types.Context, cluster *types.Cluster) (string, error) {
	configList := &unstructured.Unstructured{}
	configList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "config.openshift.io",
		Version: "v1",
		Kind:    "ClusterVersion",
	})

	err := cluster.Client.List(ctx.Context(), configList)
	if err == nil {
		// found OpenShift only resource type, it is OpenShift
		return config.DistroOcp, nil
	}

	if meta.IsNoMatchError(err) {
		// api server says no match for OpenShift only resource type,
		// it is not OpenShift
		return config.DistroK8s, nil
	}

	// unexpected error
	return "", err
}
