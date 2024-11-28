// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package types

import (
	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Deployer interface has methods to deploy a workload to a cluster
type Deployer interface {
	Deploy(Workload) error
	Undeploy(Workload) error
	IsWorkloadSupported(Workload) bool
	GetName() string
}

type Workload interface {
	// Can differ based on the workload, hence part of the Workload interface
	Kustomize() string

	GetName() string
	GetAppName() string
	GetPath() string
	GetRevision() string

	// TODO: client and log with context, or use internal workload state.
	Health(client client.Client, namespace string, log logr.Logger) error
}

// Context combines workload, deployer and logger used in the content of one test.
// The context name is used for logging and resource names.
type Context interface {
	Deployer() Deployer
	Workload() Workload
	Name() string
	Logger() logr.Logger
}
