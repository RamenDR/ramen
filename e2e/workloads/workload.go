// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package workloads

type Workload interface {
	Kustomize() string // Can differ based on the workload, hence part of the Workload interface
	// GetResources() error // Get the actual workload resources

	GetName() string
	GetAppName() string

	// GetRepoURL() string // Possibly all this is part of Workload than each implementation of the interfaces?
	GetPath() string
	GetRevision() string
}
