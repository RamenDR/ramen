// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package workloads

import (
	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Workload interface {
	Kustomize() string // Can differ based on the workload, hence part of the Workload interface
	// GetResources() error // Get the actual workload resources

	GetName() string
	GetAppName() string

	// GetRepoURL() string // Possibly all this is part of Workload than each implementation of the interfaces?
	GetPath() string
	GetRevision() string

	Health(client client.Client, namespace string, log logr.Logger) error
}
