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
	return "Subscr"
}

func (s Subscription) Deploy(ctx types.Context) error {
	// Generate a Placement for the Workload
	// Use the global Channel
	// Generate a Binding for the namespace (does this need clusters?)
	// Generate a Subscription for the Workload
	// - Kustomize the Workload; call Workload.Kustomize(StorageType)
	// Address namespace/label/suffix as needed for various resources
	name := ctx.Name()
	log := ctx.Logger()
	namespace := name

	log.Info("Deploying workload")

	// create subscription namespace
	err := util.CreateNamespace(util.Ctx.Hub.CtrlClient, namespace)
	if err != nil {
		return err
	}

	err = CreateManagedClusterSetBinding(McsbName, namespace)
	if err != nil {
		return err
	}

	err = CreatePlacement(ctx, name, namespace)
	if err != nil {
		return err
	}

	err = CreateSubscription(ctx, s)
	if err != nil {
		return err
	}

	return waitSubscriptionPhase(ctx, namespace, name, subscriptionv1.SubscriptionPropagated)
}

// Delete Subscription, Placement, Binding
func (s Subscription) Undeploy(ctx types.Context) error {
	name := ctx.Name()
	log := ctx.Logger()
	namespace := name

	log.Info("Undeploying workload")

	err := DeleteSubscription(ctx, s)
	if err != nil {
		return err
	}

	err = DeletePlacement(ctx, name, namespace)
	if err != nil {
		return err
	}

	err = DeleteManagedClusterSetBinding(ctx, McsbName, namespace)
	if err != nil {
		return err
	}

	return util.DeleteNamespace(util.Ctx.Hub.CtrlClient, namespace, log)
}

func (s Subscription) IsWorkloadSupported(w types.Workload) bool {
	return true
}
