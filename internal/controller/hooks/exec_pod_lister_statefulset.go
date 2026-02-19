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

// StatefulSetPodLister handles pod discovery when SelectResource is "statefulset".
// It finds StatefulSets and returns the first pod (index 0) from each.
type StatefulSetPodLister struct {
	ExecHook
}

// GetPods returns pods from StatefulSets matching the selector criteria.
// When SinglePodOnly is true, it returns one pod (index 0) per StatefulSet.
// When SinglePodOnly is false, it returns all pods from all StatefulSets.
func (l *StatefulSetPodLister) GetPods(log logr.Logger) ([]ExecPodSpec, error) {
	statefulSets, err := l.getStatefulSets(log)
	if err != nil {
		return nil, err
	}

	if l.Hook.SinglePodOnly {
		return l.getPodsFromStatefulSets(statefulSets)
	}

	return l.getAllPodsFromStatefulSets(statefulSets, log)
}

//nolint:dupl // getDaemonSets mirrors this structure; both use label/name selectors.
func (l *StatefulSetPodLister) getStatefulSets(log logr.Logger) ([]appsv1.StatefulSet, error) {
	statefulSetList := &appsv1.StatefulSetList{}
	ss := make([]appsv1.StatefulSet, 0)

	if l.Hook.LabelSelector != nil {
		err := getResourcesUsingLabelSelector(l.Reader, l.Hook, statefulSetList)
		if err != nil {
			return ss, err
		}

		ss = append(ss, statefulSetList.Items...)

		log.Info("statefulsets count obtained using label selector for", "hook", l.Hook.Name, "labelSelector",
			l.Hook.LabelSelector, "selectResource", l.Hook.SelectResource, "statefulSetCount", len(ss))
	}

	if l.Hook.NameSelector != "" {
		selectorType, objs, err := getResourcesUsingNameSelector(l.Reader, l.Hook, statefulSetList)
		if err != nil {
			return ss, err
		}

		for _, obj := range objs {
			s, ok := obj.(*appsv1.StatefulSet)
			if ok {
				ss = append(ss, *s)
			}
		}

		log.Info("statefulsets count obtained using name selector for", "hook", l.Hook.Name,
			"nameSelector", l.Hook.NameSelector, "nameSelectorType", selectorType,
			"selectResource", l.Hook.SelectResource, "statefulSetCount", len(ss))
	}

	return ss, nil
}

func (l *StatefulSetPodLister) getPodsFromStatefulSets(statefulSets []appsv1.StatefulSet) ([]ExecPodSpec, error) {
	execPods := make([]ExecPodSpec, 0)

	for _, statefulSet := range statefulSets {
		if statefulSet.Status.ReadyReplicas > 0 {
			pod := &corev1.Pod{}

			// StatefulSet pods follow the naming convention: <statefulset-name>-<ordinal>
			// We get the first pod (ordinal 0)
			err := l.Reader.Get(context.Background(), client.ObjectKey{
				Name:      statefulSet.Name + "-0",
				Namespace: statefulSet.Namespace,
			}, pod)
			if err != nil {
				return execPods, fmt.Errorf("error occurred while getting pod for statefulset: %w", err)
			}

			cmd, err := covertCommandToStringArray(l.Hook.Op.Command)
			if err != nil {
				return execPods, fmt.Errorf("error converting command to string array: %w", err)
			}

			execPods = append(execPods, getExecPodSpec(l.Hook.Op.Container, cmd, pod))
		}
	}

	return execPods, nil
}

func (l *StatefulSetPodLister) getAllPodsFromStatefulSets(
	statefulSets []appsv1.StatefulSet, log logr.Logger,
) ([]ExecPodSpec, error) {
	execPods := make([]ExecPodSpec, 0)

	cmd, err := covertCommandToStringArray(l.Hook.Op.Command)
	if err != nil {
		return execPods, fmt.Errorf("error converting command to string array: %w", err)
	}

	log.V(1).Info("getAllPodsFromStatefulSets", "statefulSetCount", len(statefulSets))

	for _, statefulSet := range statefulSets {
		if statefulSet.Status.ReadyReplicas > 0 {
			pods, err := listExecPodsInNamespaceForStatefulSet(context.Background(), l.Reader,
				statefulSet.Namespace, statefulSet.Name, cmd, l.Hook.Op.Container)
			if err != nil {
				return execPods, fmt.Errorf("error listing pods for statefulset %s: %w", statefulSet.Name, err)
			}

			execPods = append(execPods, pods...)
		}
	}

	return execPods, nil
}
