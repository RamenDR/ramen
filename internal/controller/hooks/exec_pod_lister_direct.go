// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"context"
	"fmt"
	"regexp"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DirectPodLister handles pod discovery when SelectResource is "pod" or empty.
// It directly queries pods using label or name selectors.
type DirectPodLister struct {
	ExecHook
}

// GetPods returns pods matching the selector criteria.
// If SinglePodOnly is true, returns only the first matching pod.
func (l *DirectPodLister) GetPods(log logr.Logger) ([]ExecPodSpec, error) {
	execPods := l.getAllPossibleExecPods(log)

	if l.Hook.SinglePodOnly && len(execPods) > 0 {
		log.Info("pods details obtained for singlePodOnly", "hook", l.Hook.Name,
			"labelSelector", l.Hook.LabelSelector, "selectResource", l.Hook.SelectResource,
			"podName", execPods[0].PodName)

		return []ExecPodSpec{execPods[0]}, nil
	}

	if l.Hook.SinglePodOnly && len(execPods) == 0 {
		return nil, fmt.Errorf("no pods found using labelSelector or nameSelector when singlePodOnly is true")
	}

	return execPods, nil
}

func (l *DirectPodLister) getAllPossibleExecPods(log logr.Logger) []ExecPodSpec {
	execPods := make([]ExecPodSpec, 0)

	/*
		1. If the labelSelector is provided, get the pods using the labelSelector.
		2. If the nameSelector is provided, get the pods using the nameSelector.
		3. If both are provided, OR logic will apply
	*/
	if l.Hook.LabelSelector != nil {
		eps, err := l.getExecPodsUsingLabelSelector(log)
		if err != nil {
			log.Error(err, "error occurred while getting pods using labelSelector")
		}

		execPods = append(execPods, eps...)

		log.Info("all pods count obtained using label selector for", "hook", l.Hook.Name,
			"labelSelector", l.Hook.LabelSelector, "selectResource", l.Hook.SelectResource, "podCount", len(execPods))
	}

	if l.Hook.NameSelector != "" {
		eps, err := l.getExecPodsUsingNameSelector(log)
		if err != nil {
			log.Error(err, "error occurred while getting pods using nameSelector")
		}

		execPods = append(execPods, eps...)

		log.Info("all pods count obtained using name selector for", "hook", l.Hook.Name,
			"nameSelector", l.Hook.NameSelector, "selectResource", l.Hook.SelectResource, "podCount", len(execPods))
	}

	return execPods
}

func (l *DirectPodLister) getExecPodsUsingLabelSelector(log logr.Logger) ([]ExecPodSpec, error) {
	execPods := make([]ExecPodSpec, 0)
	podList := &corev1.PodList{}

	err := getResourcesUsingLabelSelector(l.Reader, l.Hook, podList)
	if err != nil {
		return execPods, err
	}

	execPods, err = l.getExecPodSpecsFromPodList(podList, log)
	if err != nil {
		return execPods, fmt.Errorf("error filtering exec pods using labelSelector: %w", err)
	}

	return execPods, nil
}

func (l *DirectPodLister) getExecPodsUsingNameSelector(log logr.Logger) ([]ExecPodSpec, error) {
	execPods := make([]ExecPodSpec, 0)
	podList := &corev1.PodList{}

	nameSelector := l.Hook.NameSelector

	if isValidK8sName(nameSelector) {
		listOps := &client.ListOptions{
			Namespace:     l.Hook.Namespace,
			FieldSelector: fields.SelectorFromSet(fields.Set{"metadata.name": l.Hook.NameSelector}),
		}

		err := l.Reader.List(context.Background(), podList, listOps)
		if err != nil {
			return execPods, fmt.Errorf("error listing resources using nameSelector: %w", err)
		}

		execPods, err = l.getExecPodSpecsFromPodList(podList, log)
		if err != nil {
			return execPods, fmt.Errorf("error getting exec pods using nameSelector: %w", err)
		}

		return execPods, nil
	}

	if isValidRegex(nameSelector) {
		listOps := &client.ListOptions{Namespace: l.Hook.Namespace}

		re, err := regexp.Compile(l.Hook.NameSelector)
		if err != nil {
			return execPods, fmt.Errorf("error during regex compilation using nameSelector: %w", err)
		}

		err = l.Reader.List(context.Background(), podList, listOps)
		if err != nil {
			return execPods, fmt.Errorf("error listing resources using nameSelector: %w", err)
		}

		execPods, err = l.filterExecPodsUsingRegex(podList, re, log)
		if err != nil {
			return execPods, fmt.Errorf("error filtering exec pods using nameSelector regex: %w", err)
		}

		return execPods, nil
	}

	return execPods, nil
}

func (l *DirectPodLister) filterExecPodsUsingRegex(podList *corev1.PodList, re *regexp.Regexp,
	log logr.Logger,
) ([]ExecPodSpec, error) {
	execPods := make([]ExecPodSpec, 0)

	cmd, err := covertCommandToStringArray(l.Hook.Op.Command)
	if err != nil {
		log.Error(err, "error occurred during exec hook execution", "command being converted:", l.Hook.Op.Command)

		return execPods, err
	}

	for i := range podList.Items {
		pod := &podList.Items[i]
		if re.MatchString(pod.Name) {
			execPods = append(execPods, getExecPodSpec(l.Hook.Op.Container, cmd, pod))
		}
	}

	return execPods, nil
}

func (l *DirectPodLister) getExecPodSpecsFromPodList(podList *corev1.PodList, log logr.Logger) ([]ExecPodSpec, error) {
	execPods := make([]ExecPodSpec, 0)

	cmd, err := covertCommandToStringArray(l.Hook.Op.Command)
	if err != nil {
		log.Error(err, "error occurred during exec hook execution", "command being converted:", l.Hook.Op.Command)

		return execPods, err
	}

	for i := range podList.Items {
		pod := &podList.Items[i]
		execPods = append(execPods, getExecPodSpec(l.Hook.Op.Container, cmd, pod))
	}

	return execPods, nil
}
