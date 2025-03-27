// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package types

import (
	"go.uber.org/zap"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ChannelConfig defines the name and namespace for the channel CR.
// This is not user-configurable and always uses default values.
type ChannelConfig struct {
	Name      string
	Namespace string
}

// NamespacesConfig are determined by distro and are not user-configurable.
type NamespacesConfig struct {
	RamenHubNamespace       string
	RamenDRClusterNamespace string
	RamenOpsNamespace       string
	ArgocdNamespace         string
}

// RepoConfig represents the user-configurable git repository settings.
// It includes the repository url and branch to be used for deploying workload.
type RepoConfig struct {
	URL    string
	Branch string
}

type PVCSpecConfig struct {
	Name                 string
	StorageClassName     string
	AccessModes          string
	UnsupportedDeployers []string
}

type ClusterConfig struct {
	Kubeconfig string
}

type TestConfig struct {
	Workload string
	Deployer string
	PVCSpec  string
}

type Config struct {
	// User configurable values.
	Distro     string
	Repo       RepoConfig
	DRPolicy   string
	ClusterSet string
	Clusters   map[string]ClusterConfig
	PVCSpecs   []PVCSpecConfig
	Tests      []TestConfig

	// Generated values
	Channel    ChannelConfig
	Namespaces NamespacesConfig
}

// Clsuter can be a hub cluster or a managed cluster.
type Cluster struct {
	Name       string
	Client     client.Client
	Kubeconfig string
}

type Env struct {
	Hub Cluster
	C1  Cluster
	C2  Cluster
}

// Deployer interface has methods to deploy a workload to a cluster
type Deployer interface {
	Deploy(Context) error
	Undeploy(Context) error
	GetName() string
	// GetNamespace return the namespace for the ramen resources, or empty string if not using a special namespace.
	GetNamespace(Context) string
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

	// SupportsDeployer returns tue if this workload is compatible with deployer.
	SupportsDeployer(Deployer) bool

	Health(ctx Context, cluster Cluster, namespace string) error
}

// Context combines workload, deployer and logger used in the content of one test.
// The context name is used for logging and resource names.
type Context interface {
	Deployer() Deployer
	Workload() Workload
	Name() string

	// Namespace for OCM and Ramen resources (Subscription, ApplicationSet, DRPC, VRG) on the hub and managed clusters.
	// Depending on the deployer, it may be the same as AppNamespace().
	ManagementNamespace() string

	// Namespace for application resources on the managed clusters.
	AppNamespace() string

	Logger() *zap.SugaredLogger
	Env() *Env
	Config() *Config
}
