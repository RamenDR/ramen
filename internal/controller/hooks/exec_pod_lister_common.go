// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// listExecPodsInNamespaceForDaemonSet lists pods in the namespace that are owned by the named DaemonSet.
func listExecPodsInNamespaceForDaemonSet(
	ctx context.Context,
	reader client.Reader,
	namespace, daemonSetName string,
	cmd []string,
	container string,
) ([]ExecPodSpec, error) {
	podList := &corev1.PodList{}

	err := reader.List(ctx, podList, client.InNamespace(namespace))
	if err != nil {
		return nil, fmt.Errorf("error listing pods: %w", err)
	}

	execPods := make([]ExecPodSpec, 0)

	for i := range podList.Items {
		pod := &podList.Items[i]
		if IsPodOwnedByDaemonSet(pod, daemonSetName) {
			execPods = append(execPods, getExecPodSpec(container, cmd, pod))
		}
	}

	return execPods, nil
}

// listExecPodsInNamespaceForStatefulSet lists pods in the namespace that are owned by the named StatefulSet.
func listExecPodsInNamespaceForStatefulSet(
	ctx context.Context,
	reader client.Reader,
	namespace, statefulSetName string,
	cmd []string,
	container string,
) ([]ExecPodSpec, error) {
	podList := &corev1.PodList{}

	err := reader.List(ctx, podList, client.InNamespace(namespace))
	if err != nil {
		return nil, fmt.Errorf("error listing pods: %w", err)
	}

	execPods := make([]ExecPodSpec, 0)

	for i := range podList.Items {
		pod := &podList.Items[i]
		if IsPodOwnedByStatefulSet(pod, statefulSetName) {
			execPods = append(execPods, getExecPodSpec(container, cmd, pod))
		}
	}

	return execPods, nil
}

// listExecPodsInNamespaceForRS lists pods in the namespace that are owned by the named ReplicaSet.
func listExecPodsInNamespaceForRS(
	ctx context.Context,
	reader client.Reader,
	namespace, rsName string,
	cmd []string,
	container string,
) ([]ExecPodSpec, error) {
	podList := &corev1.PodList{}

	err := reader.List(ctx, podList, client.InNamespace(namespace))
	if err != nil {
		return nil, fmt.Errorf("error listing pods: %w", err)
	}

	execPods := make([]ExecPodSpec, 0)

	for i := range podList.Items {
		pod := &podList.Items[i]
		if IsPodOwnedByRS(pod, rsName) {
			execPods = append(execPods, getExecPodSpec(container, cmd, pod))
		}
	}

	return execPods, nil
}
