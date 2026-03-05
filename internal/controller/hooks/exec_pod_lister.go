// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"github.com/go-logr/logr"
)

// PodLister interface abstracts pod discovery based on SelectResource type.
// Each implementation handles a specific resource type (pod, deployment, statefulset, daemonset).
type PodLister interface {
	GetPods(log logr.Logger) ([]ExecPodSpec, error)
}

// NewPodLister returns the appropriate PodLister implementation based on SelectResource.
func NewPodLister(e ExecHook) PodLister {
	switch e.Hook.SelectResource {
	case "deployment":
		return &DeploymentPodLister{ExecHook: e}
	case "statefulset":
		return &StatefulSetPodLister{ExecHook: e}
	case "daemonset":
		return &DaemonSetPodLister{ExecHook: e}
	default: // "pod" or ""
		return &DirectPodLister{ExecHook: e}
	}
}
