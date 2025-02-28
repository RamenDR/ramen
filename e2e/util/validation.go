// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"fmt"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ramenSystemNamespace = "ramen-system"
)

func ValidateRamenHubOperator(cluster Cluster) error {
	labelSelector := "app=ramen-hub"
	podIdentifier := "ramen-hub-operator"

	ramenNameSpace, err := GetRamenNameSpace(cluster)
	if err != nil {
		return err
	}

	pod, err := FindPod(cluster, ramenNameSpace, labelSelector, podIdentifier)
	if err != nil {
		return err
	}

	if pod.Status.Phase != "Running" {
		return fmt.Errorf("ramen hub operator pod %q not running (phase %q)",
			pod.Name, pod.Status.Phase)
	}

	Ctx.Log.Infof("Ramen hub operator pod %q is running", pod.Name)

	return nil
}

func ValidateRamenDRClusterOperator(cluster Cluster, clusterName string) error {
	labelSelector := "app=ramen-dr-cluster"
	podIdentifier := "ramen-dr-cluster-operator"

	ramenNameSpace, err := GetRamenNameSpace(cluster)
	if err != nil {
		return err
	}

	pod, err := FindPod(cluster, ramenNameSpace, labelSelector, podIdentifier)
	if err != nil {
		return err
	}

	if pod.Status.Phase != "Running" {
		return fmt.Errorf("ramen dr cluster operator pod %q not running (phase %q)",
			pod.Name, pod.Status.Phase)
	}

	Ctx.Log.Infof("Ramen dr cluster operator pod %q is running in cluster %q", pod.Name, clusterName)

	return nil
}

func GetRamenNameSpace(cluster Cluster) (string, error) {
	isOpenShift, err := IsOpenShiftCluster(cluster)
	if err != nil {
		return "", err
	}

	if isOpenShift {
		return "openshift-operators", nil
	}

	return ramenSystemNamespace, nil
}

// IsOpenShiftCluster checks if the given Kubernetes cluster is an OpenShift cluster.
// It returns true if the cluster is OpenShift, false otherwise, along with any error encountered.
func IsOpenShiftCluster(cluster Cluster) (bool, error) {
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
func FindPod(cluster Cluster, namespace, labelSelector, podIdentifier string) (
	*v1.Pod, error,
) {
	ls, err := labels.Parse(labelSelector)
	if err != nil {
		return nil, fmt.Errorf("failed to parse label selector %q: %v", labelSelector, err)
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
		return nil, fmt.Errorf("failed to list pods in namespace %s: %v", namespace, err)
	}

	for i := range pods.Items {
		pod := &pods.Items[i]
		if strings.Contains(pod.Name, podIdentifier) {
			return pod, nil
		}
	}

	return nil, fmt.Errorf("no pod with label selector %q and identifier %q in namespace %q",
		labelSelector, podIdentifier, namespace)
}
