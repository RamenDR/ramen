// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package types

import (
	"context"

	recipe "github.com/ramendr/recipe/api/v1alpha1"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	Hub        *Cluster `json:"hub"`
	PassiveHub *Cluster `json:"passive-hub,omitempty"`
	C1         *Cluster `json:"c1"`
	C2         *Cluster `json:"c2"`
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
	// IsDiscovered returns true for OCM discovered application, false for OCM managed applications.
	IsDiscovered() bool

	// DeleteResources removes all resources that were deployed as part of the workload
	DeleteResources(TestContext) error
	// WaitForResourcesDelete waits for all workload resources to be deleted
	WaitForResourcesDelete(TestContext) error

	// GetRecipe returns the recipe if the deployer is configured to use one.
	// Returns nil if the deployer is not configured to use a recipe.
	GetRecipe(TestContext) (*recipe.Recipe, error)

	// GetConfig returns the deployer configuration used by the deployer
	GetConfig() *config.Deployer
}

// nolint:interfacebloat
type Workload interface {
	// Can differ based on the workload, hence part of the Workload interface
	Kustomize() string

	GetName() string
	GetAppName() string
	GetPath() string
	GetBranch() string
	// GetSelectResource returns the hook.selectResource of workload used. It can be either "deployment"
	//  or "pod" or "statefulset" or any resource with the format "<apigroup>/<apiversion>/<kindplural>"
	GetSelectResource() string
	// GetLabelSelector returns the labelSelector to used for selecting the resources.
	// This value is made use during preparation of hooks used in recipe.
	GetLabelSelector() *metav1.LabelSelector
	// GetChecks returns the check hook to be executed based on workload and given namespace.
	GetChecks(string) []*recipe.Check
	// GetOperations returns the exec hook to be executed based on workload and given namespace.
	GetOperations(string) []*recipe.Operation
	Health(ctx TestContext, cluster *Cluster) error
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
