// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
	subscriptionv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/v1"
)

// mcsb name must be same as the target ManagedClusterSet
const McsbName = ClusterSetName

type Subscription struct{}

func (s Subscription) GetName() string {
	return "subscr"
}

func (s Subscription) GetNamespace() string {
	// No special namespaces.
	return ""
}

// Deploy creates a Subscription on the hub cluster, creating the workload on one of the managed clusters.
func (s Subscription) Deploy(ctx types.Context) error {
	// Generate a Placement for the Workload
	// Use the global Channel
	// Generate a Binding for the namespace (does this need clusters?)
	// Generate a Subscription for the Workload
	// - Kustomize the Workload; call Workload.Kustomize(StorageType)
	// Address namespace/label/suffix as needed for various resources
	name := ctx.Name()
	log := ctx.Logger()
	managementNamespace := ctx.ManagementNamespace()

	// create subscription namespace
	err := util.CreateNamespace(util.Ctx.Hub.Client, managementNamespace)
	if err != nil {
		return err
	}

	err = CreateManagedClusterSetBinding(ctx, McsbName, managementNamespace)
	if err != nil {
		return err
	}

	err = CreatePlacement(ctx, name, managementNamespace)
	if err != nil {
		return err
	}

	err = CreateSubscription(ctx, s)
	if err != nil {
		return err
	}

	err = waitSubscriptionPhase(ctx, managementNamespace, name, subscriptionv1.SubscriptionPropagated)
	if err != nil {
		return err
	}

	clusterName, err := util.GetCurrentCluster(util.Ctx.Hub.Client, managementNamespace, name)
	if err != nil {
		return err
	}

	log.Infof("Deployed subscription app \"%s/%s\" on cluster %q",
		ctx.AppNamespace(), ctx.Workload().GetAppName(), clusterName)

	return nil
}

// Undeploy deletes a subscription from the hub cluster, deleting the workload from the managed clusters.
func (s Subscription) Undeploy(ctx types.Context) error {
	name := ctx.Name()
	log := ctx.Logger()
	managementNamespace := ctx.ManagementNamespace()

	err := DeleteSubscription(ctx, s)
	if err != nil {
		return err
	}

	clusterName, err := util.GetCurrentCluster(util.Ctx.Hub.Client, managementNamespace, name)
	if err != nil {
		return err
	}

	log.Infof("Undeployed subscription app \"%s/%s\" on cluster %q",
		ctx.AppNamespace(), ctx.Workload().GetAppName(), clusterName)

	err = DeletePlacement(ctx, name, managementNamespace)
	if err != nil {
		return err
	}

	err = DeleteManagedClusterSetBinding(ctx, McsbName, managementNamespace)
	if err != nil {
		return err
	}

	return util.DeleteNamespace(util.Ctx.Hub.Client, managementNamespace, log)
}

func (s Subscription) IsDiscovered() bool {
	return false
}
