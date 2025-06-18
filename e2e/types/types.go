// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package types

import (
	"context"

	"go.uber.org/zap"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ramendr/ramen/e2e/config"
)

type ApplicationStatus string

const (
	ApplicationFound    = ApplicationStatus("found")
	ApplicationPartial  = ApplicationStatus("partial")
	ApplicationNotFound = ApplicationStatus("not-found")
)

// Clsuter can be a hub cluster or a managed cluster.
type Cluster struct {
	Name       string        `json:"name"`
	Client     client.Client `json:"-"`
	Kubeconfig string        `json:"kubeconfig"`
}

type Env struct {
	Hub Cluster `json:"hub"`
	C1  Cluster `json:"c1"`
	C2  Cluster `json:"c2"`
}

// WorkloadStatus holds the status of an application on a specific cluster
type WorkloadStatus struct {
	ClusterName string
	Status      ApplicationStatus
}

// Deployer interface has methods to deploy a workload to a cluster
type Deployer interface {
	Deploy(TestContext) error
	Undeploy(TestContext) error
	GetName() string
	// GetNamespace return the namespace for the ramen resources, or empty string if not using a special namespace.
	GetNamespace(TestContext) string
	// Return true for OCM discovered application, false for OCM managed applications.
	IsDiscovered() bool
}

type Workload interface {
	// Can differ based on the workload, hence part of the Workload interface
	Kustomize() string

	GetName() string
	GetAppName() string
	GetPath() string
	GetBranch() string
	Health(ctx TestContext, cluster Cluster, namespace string) error
	Status(ctx TestContext) ([]WorkloadStatus, error)
}

// Context keeps the Logger, Env, Config, and Context shared by all code in the e2e package.
type Context interface {
	Logger() *zap.SugaredLogger
	Env() *Env
	Config() *config.Config
	Context() context.Context
}

// TestContext is a more specific Context for a single test; a combination of Deployer, Workload, and namespaces. A test
// has a unique Name and Logger, and it shares the global Env, Config and Context.
type TestContext interface {
	Context
	Deployer() Deployer
	Workload() Workload
	Name() string

	// Namespace for OCM and Ramen resources (Subscription, ApplicationSet, DRPC, VRG) on the hub and managed clusters.
	// Depending on the deployer, it may be the same as AppNamespace().
	ManagementNamespace() string

	// Namespace for application resources on the managed clusters.
	AppNamespace() string
}
