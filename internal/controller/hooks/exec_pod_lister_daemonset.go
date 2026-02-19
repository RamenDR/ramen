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

// DaemonSetPodLister handles pod discovery when SelectResource is "daemonset".
// It finds DaemonSets and returns one running pod from each.
type DaemonSetPodLister struct {
	ExecHook
}

// GetPods returns pods from DaemonSets matching the selector criteria.
// When SinglePodOnly is true, it returns one pod per DaemonSet.
// When SinglePodOnly is false, it returns all pods from all DaemonSets.
func (l *DaemonSetPodLister) GetPods(log logr.Logger) ([]ExecPodSpec, error) {
	daemonSets, err := l.getDaemonSets(log)
	if err != nil {
		return nil, err
	}

	if l.Hook.SinglePodOnly {
		return l.getPodsFromDaemonSets(daemonSets, log)
	}

	return l.getAllPodsFromDaemonSets(daemonSets, log)
}

//nolint:dupl // getStatefulSets mirrors this structure; both use label/name selectors.
func (l *DaemonSetPodLister) getDaemonSets(log logr.Logger) ([]appsv1.DaemonSet, error) {
	daemonSetList := &appsv1.DaemonSetList{}
	ds := make([]appsv1.DaemonSet, 0)

	if l.Hook.LabelSelector != nil {
		err := getResourcesUsingLabelSelector(l.Reader, l.Hook, daemonSetList)
		if err != nil {
			return ds, err
		}

		ds = append(ds, daemonSetList.Items...)

		log.Info("daemonsets count obtained using label selector for", "hook", l.Hook.Name, "labelSelector",
			l.Hook.LabelSelector, "selectResource", l.Hook.SelectResource, "daemonSetCount", len(ds))
	}

	if l.Hook.NameSelector != "" {
		selectorType, objs, err := getResourcesUsingNameSelector(l.Reader, l.Hook, daemonSetList)
		if err != nil {
			return ds, err
		}

		for _, obj := range objs {
			d, ok := obj.(*appsv1.DaemonSet)
			if ok {
				ds = append(ds, *d)
			}
		}

		log.Info("daemonsets count obtained using name selector for", "hook", l.Hook.Name,
			"nameSelector", l.Hook.NameSelector, "nameSelectorType", selectorType,
			"selectResource", l.Hook.SelectResource, "daemonSetCount", len(ds))
	}

	return ds, nil
}

func (l *DaemonSetPodLister) getPodsFromDaemonSets(daemonSets []appsv1.DaemonSet,
	log logr.Logger,
) ([]ExecPodSpec, error) {
	execPods := make([]ExecPodSpec, 0)

	for _, daemonSet := range daemonSets {
		if daemonSet.Status.NumberReady > 0 {
			pod, err := l.getFirstRunningPodForDaemonSet(&daemonSet)
			if err != nil {
				return execPods, fmt.Errorf("error getting pod for daemonset %s: %w", daemonSet.Name, err)
			}

			if pod == nil {
				log.Info("no running pod found for daemonset", "daemonset", daemonSet.Name,
					"namespace", daemonSet.Namespace)

				continue
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

func (l *DaemonSetPodLister) getFirstRunningPodForDaemonSet(daemonSet *appsv1.DaemonSet) (*corev1.Pod, error) {
	podList := &corev1.PodList{}

	err := l.Reader.List(context.Background(), podList, client.InNamespace(daemonSet.Namespace))
	if err != nil {
		return nil, fmt.Errorf("error listing pods: %w", err)
	}

	for i := range podList.Items {
		pod := &podList.Items[i]
		if IsPodOwnedByDaemonSet(pod, daemonSet.Name) {
			return pod, nil
		}
	}

	return nil, nil
}

func (l *DaemonSetPodLister) getAllPodsFromDaemonSets(
	daemonSets []appsv1.DaemonSet, log logr.Logger,
) ([]ExecPodSpec, error) {
	execPods := make([]ExecPodSpec, 0)

	cmd, err := covertCommandToStringArray(l.Hook.Op.Command)
	if err != nil {
		return execPods, fmt.Errorf("error converting command to string array: %w", err)
	}

	log.V(1).Info("getAllPodsFromDaemonSets", "daemonSetCount", len(daemonSets))

	for _, daemonSet := range daemonSets {
		if daemonSet.Status.NumberReady > 0 {
			pods, err := listExecPodsInNamespaceForDaemonSet(context.Background(), l.Reader,
				daemonSet.Namespace, daemonSet.Name, cmd, l.Hook.Op.Container)
			if err != nil {
				return execPods, fmt.Errorf("error listing pods for daemonset %s: %w", daemonSet.Name, err)
			}

			execPods = append(execPods, pods...)
		}
	}

	return execPods, nil
}
