// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"github.com/ramendr/ramen/e2e/util"
	"github.com/ramendr/ramen/e2e/workloads"
	subscriptionv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/v1"
)

// mcsb name must be same as the target ManagedClusterSet
const McsbName = ClusterSetName

type Subscription struct{}

func (s Subscription) GetName() string {
	return "Subscr"
}

func (s Subscription) Deploy(w workloads.Workload) error {
	// Generate a Placement for the Workload
	// Use the global Channel
	// Generate a Binding for the namespace (does this need clusters?)
	// Generate a Subscription for the Workload
	// - Kustomize the Workload; call Workload.Kustomize(StorageType)
	// Address namespace/label/suffix as needed for various resources
	name := GetCombinedName(s, w)
	namespace := name

	util.Ctx.Log.Info("enter Deploy " + name)

	// create subscription namespace
	err := util.CreateNamespace(util.Ctx.Hub.CtrlClient, namespace)
	if err != nil {
		return err
	}

	err = CreateManagedClusterSetBinding(McsbName, namespace)
	if err != nil {
		return err
	}

	err = CreatePlacement(name, namespace)
	if err != nil {
		return err
	}

	err = CreateSubscription(s, w)
	if err != nil {
		return err
	}

	err = waitSubscriptionPhase(namespace, name, subscriptionv1.SubscriptionPropagated)
	if err != nil {
		return err
	}

	return nil
}

// Delete Subscription, Placement, Binding
func (s Subscription) Undeploy(w workloads.Workload) error {
	name := GetCombinedName(s, w)
	namespace := name

	util.Ctx.Log.Info("enter Undeploy " + name)

	err := DeleteSubscription(s, w)
	if err != nil {
		return err
	}

	err = DeletePlacement(name, namespace)
	if err != nil {
		return err
	}

	err = DeleteManagedClusterSetBinding(McsbName, namespace)
	if err != nil {
		return err
	}

	err = util.DeleteNamespace(util.Ctx.Hub.CtrlClient, namespace)
	if err != nil {
		return err
	}

	return nil
}

func (s Subscription) IsWorkloadSupported(w workloads.Workload) bool {
	return true
}
