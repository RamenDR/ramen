// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package types

import (
	"context"

	"go.uber.org/zap"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ChannelConfig defines the name and namespace for the channel CR.
// This is not user-configurable and always uses default values.
type ChannelConfig struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// NamespacesConfig are determined by distro and are not user-configurable.
type NamespacesConfig struct {
	RamenHubNamespace       string `json:"ramenHubNamespace"`
	RamenDRClusterNamespace string `json:"ramenDRClusterNamespace"`
	RamenOpsNamespace       string `json:"ramenOpsNamespace"`
	ArgocdNamespace         string `json:"argocdNamespace"`
}

// RepoConfig represents the user-configurable git repository settings.
// It includes the repository url and branch to be used for deploying workload.
type RepoConfig struct {
	URL    string `json:"url"`
	Branch string `json:"branch"`
}

type PVCSpecConfig struct {
	Name             string `json:"name"`
	StorageClassName string `json:"storageClassName"`
	AccessModes      string `json:"accessModes"`
}

type ClusterConfig struct {
	Kubeconfig string `json:"kubeconfig"`
}

type TestConfig struct {
	Workload string `json:"workload"`
	Deployer string `json:"deployer"`
	PVCSpec  string `json:"pvcSpec"`
}

type Config struct {
	// User configurable values.
	Distro     string                   `json:"distro"`
	Repo       RepoConfig               `json:"repo"`
	DRPolicy   string                   `json:"drPolicy"`
	ClusterSet string                   `json:"clusterSet"`
	Clusters   map[string]ClusterConfig `json:"clusters"`
	PVCSpecs   []PVCSpecConfig          `json:"pvcSpecs"`
	Tests      []TestConfig             `json:"tests"`

	// Generated values
	Channel    ChannelConfig    `json:"channel"`
	Namespaces NamespacesConfig `json:"namespaces"`
}

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
}

// Context keeps the Logger, Env, Config, and Context shared by all code in the e2e package.
type Context interface {
	Logger() *zap.SugaredLogger
	Env() *Env
	Config() *Config
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
