// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DeploymentPodLister handles pod discovery when SelectResource is "deployment".
// It finds deployments, then their ReplicaSets, then the pods owned by those ReplicaSets.
type DeploymentPodLister struct {
	ExecHook
}

// GetPods returns pods from deployments matching the selector criteria.
// When SinglePodOnly is true, it returns one pod per deployment.
// When SinglePodOnly is false, it returns all pods from all deployments' ReplicaSets.
func (l *DeploymentPodLister) GetPods(log logr.Logger) ([]ExecPodSpec, error) {
	execPods := make([]ExecPodSpec, 0)

	deps, err := l.getDeployments(log)
	if err != nil {
		log.Error(err, "error occurred while getting deployments")

		return execPods, fmt.Errorf("error occurred while getting deployments: %w", err)
	}

	if l.Hook.SinglePodOnly {
		for _, dep := range deps {
			rs, err := l.getActiveReplicaSet(dep.Name, dep.Namespace)
			if err != nil {
				log.Error(err, "error occurred while getting replicaset for deployment")

				return execPods, fmt.Errorf("error occurred while getting replicaset for deployment: %w", err)
			}

			pod, err := l.getPodFromReplicaSet(rs.Name, rs.Namespace)
			if err != nil {
				log.Error(err, "error occurred while getting pod from replicaset")

				return execPods, fmt.Errorf("error occurred while getting pod from replicaset: %w", err)
			}

			if pod != nil {
				execPods = append(execPods, *pod)
			}
		}

		return execPods, nil
	}

	// SinglePodOnly=false: Get all pods from all ReplicaSets of all deployments
	return l.getAllPodsFromDeployments(deps, log)
}

func (l *DeploymentPodLister) getDeployments(log logr.Logger) ([]appsv1.Deployment, error) {
	deps := make([]appsv1.Deployment, 0)
	deploymentList := &appsv1.DeploymentList{}

	if l.Hook.LabelSelector != nil {
		err := getResourcesUsingLabelSelector(l.Reader, l.Hook, deploymentList)
		if err != nil {
			return nil, err
		}

		deps = append(deps, deploymentList.Items...)

		log.Info("deployments count obtained using label selector for", "hook", l.Hook.Name, "labelSelector",
			l.Hook.LabelSelector, "selectResource", l.Hook.SelectResource, "deploymentCount", len(deps))
	}

	if l.Hook.NameSelector != "" {
		selectorType, objs, err := getResourcesUsingNameSelector(l.Reader, l.Hook, deploymentList)
		if err != nil {
			return deps, err
		}

		for _, dep := range objs {
			d, ok := dep.(*appsv1.Deployment)

			if ok {
				deps = append(deps, *d)
			}
		}

		log.Info("deployments count obtained using name selector for", "hook", l.Hook.Name,
			"nameSelector", l.Hook.NameSelector, "selectorType", selectorType, "selectResource", l.Hook.SelectResource,
			"deploymentCount", len(deps))
	}

	return deps, nil
}

func (l *DeploymentPodLister) getActiveReplicaSet(depName, depNS string) (*appsv1.ReplicaSet, error) {
	replicaSetList := &appsv1.ReplicaSetList{}

	err := l.Reader.List(context.Background(), replicaSetList, client.InNamespace(depNS))
	if err != nil {
		return nil, fmt.Errorf("error listing replicaset: %w", err)
	}

	for i := range replicaSetList.Items {
		rs := &replicaSetList.Items[i]
		if IsRSOwnedByDeployment(rs, depName) {
			return rs, nil
		}
	}

	return nil, fmt.Errorf("replicaset not found for deployment %s in namespace %s", depName, depNS)
}

func (l *DeploymentPodLister) getPodFromReplicaSet(rsName, rsNS string) (*ExecPodSpec, error) {
	podList := &corev1.PodList{}

	err := l.Reader.List(context.Background(), podList, client.InNamespace(rsNS))
	if err != nil {
		return nil, fmt.Errorf("error listing pods: %w", err)
	}

	for i := range podList.Items {
		pod := &podList.Items[i]
		if IsPodOwnedByRS(pod, rsName) {
			cmd, err := covertCommandToStringArray(l.Hook.Op.Command)
			if err != nil {
				return nil, fmt.Errorf("error converting command to string array: %w", err)
			}

			execPod := ExecPodSpec{
				PodName:   pod.Name,
				Namespace: pod.Namespace,
				Command:   cmd,
				Container: getContainerName(l.Hook.Op.Container, pod),
			}

			return &execPod, nil
		}
	}

	return nil, nil
}

func (l *DeploymentPodLister) getAllPodsFromDeployments(deps []appsv1.Deployment, log logr.Logger) ([]ExecPodSpec, error) {
	execPods := make([]ExecPodSpec, 0)

	cmd, err := covertCommandToStringArray(l.Hook.Op.Command)
	if err != nil {
		return execPods, fmt.Errorf("error converting command to string array: %w", err)
	}

	for _, dep := range deps {
		// Get all ReplicaSets owned by this deployment
		replicaSetList := &appsv1.ReplicaSetList{}
		err := l.Reader.List(context.Background(), replicaSetList, client.InNamespace(dep.Namespace))
		if err != nil {
			log.Error(err, "error listing replicasets for deployment", "deployment", dep.Name)

			continue
		}

		// Get all pods from all ReplicaSets of this deployment
		for i := range replicaSetList.Items {
			rs := &replicaSetList.Items[i]
			if IsRSOwnedByDeployment(rs, dep.Name) {
				podList := &corev1.PodList{}
				err := l.Reader.List(context.Background(), podList, client.InNamespace(dep.Namespace))
				if err != nil {
					log.Error(err, "error listing pods for replicaset", "replicaset", rs.Name)

					continue
				}

				for j := range podList.Items {
					pod := &podList.Items[j]
					if IsPodOwnedByRS(pod, rs.Name) {
						execPods = append(execPods, ExecPodSpec{
							PodName:   pod.Name,
							Namespace: pod.Namespace,
							Command:   cmd,
							Container: getContainerName(l.Hook.Op.Container, pod),
						})
					}
				}
			}
		}
	}

	return execPods, nil
}

