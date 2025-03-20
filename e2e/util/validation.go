// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ramendr/ramen/e2e/types"
)

func ValidateRamenHubOperator(cluster types.Cluster, config *types.Config, log *zap.SugaredLogger) error {
	labelSelector := "app=ramen-hub"
	podIdentifier := "ramen-hub-operator"

	pod, err := FindPod(cluster, config.Namespaces.RamenHubNamespace, labelSelector, podIdentifier)
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

func ValidateRamenDRClusterOperator(cluster types.Cluster, config *types.Config, log *zap.SugaredLogger) error {
	labelSelector := "app=ramen-dr-cluster"
	podIdentifier := "ramen-dr-cluster-operator"

	pod, err := FindPod(cluster, config.Namespaces.RamenDRClusterNamespace, labelSelector, podIdentifier)
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

// IsOpenShiftCluster checks if the given Kubernetes cluster is an OpenShift cluster.
// It returns true if the cluster is OpenShift, false otherwise, along with any error encountered.
func IsOpenShiftCluster(cluster types.Cluster) (bool, error) {
	configList := &unstructured.Unstructured{}
	configList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "config.openshift.io",
		Version: "v1",
		Kind:    "ClusterVersion",
	})

	err := cluster.Client.List(context.TODO(), configList)
	if err == nil {
		// found OpenShift only resource type, it is OpenShift
		return true, nil
	}

	if meta.IsNoMatchError(err) {
		// api server says no match for OpenShift only resource type,
		// it is not OpenShift
		return false, nil
	}

	// unexpected error
	return false, err
}

// FindPod returns the first pod matching the label selector including the pod identifier in the namespace.
func FindPod(cluster types.Cluster, namespace, labelSelector, podIdentifier string) (
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

	err = cluster.Client.List(context.Background(), pods, listOptions...)
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

// ValidateClustersInDRPolicy checks if configured clusters match the configured
// drpolicy. Returns an error if cluster names are not the same as drpolicy
// drclusters. The reason for a failure may be wrong cluster name or wrong
// drpolicy.
func ValidateClustersInDRPolicy(env *types.Env, config *types.Config, log *zap.SugaredLogger) error {
	drpolicy, err := GetDRPolicy(env.Hub, config.DRPolicy)
	if err != nil {
		return err
	}

	clusters := []types.Cluster{env.C1, env.C2}
	for _, cluster := range clusters {
		if !slices.Contains(drpolicy.Spec.DRClusters, cluster.Name) {
			return fmt.Errorf("cluster %q is not defined in drpolicy %q, clusters in drpolicy: %q",
				cluster.Name, config.DRPolicy, drpolicy.Spec.DRClusters)
		}
	}

	log.Infof("Validated clusters [%q, %q] in DRPolicy %q", clusters[0].Name, clusters[1].Name, config.DRPolicy)

	return nil
}
