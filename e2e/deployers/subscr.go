// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	subscriptionv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/v1"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
)

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

	drpolicy, err := util.GetDRPolicy(ctx.Env().Hub, config.GetDRPolicyName())
	if err != nil {
		return err
	}

	log.Infof("Deploying subscription app \"%s/%s\" in cluster %q",
		ctx.AppNamespace(), ctx.Workload().GetAppName(), drpolicy.Spec.DRClusters[0])

	// create subscription namespace
	err = util.CreateNamespace(ctx.Env().Hub, managementNamespace, log)
	if err != nil {
		return err
	}

	err = CreateManagedClusterSetBinding(ctx, config.GetClusterSetName(), managementNamespace)
	if err != nil {
		return err
	}

	err = CreatePlacement(ctx, name, managementNamespace, drpolicy.Spec.DRClusters[0])
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

	log.Info("Workload deployed")

	return nil
}

// Undeploy deletes a subscription from the hub cluster, deleting the workload from the managed clusters.
func (s Subscription) Undeploy(ctx types.Context) error {
	name := ctx.Name()
	log := ctx.Logger()
	managementNamespace := ctx.ManagementNamespace()

	clusterName, err := util.GetCurrentCluster(ctx.Env().Hub, managementNamespace, name)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}

		log.Debugf("Could not retrieve the cluster name: %s", err)
		log.Infof("Undeploying subscription app \"%s/%s\"", ctx.AppNamespace(), ctx.Workload().GetAppName())
	} else {
		log.Infof("Undeploying subscription app \"%s/%s\" in cluster %q",
			ctx.AppNamespace(), ctx.Workload().GetAppName(), clusterName)
	}

	err = DeleteSubscription(ctx, s)
	if err != nil {
		return err
	}

	err = DeletePlacement(ctx, name, managementNamespace)
	if err != nil {
		return err
	}

	err = DeleteManagedClusterSetBinding(ctx, config.GetClusterSetName(), managementNamespace)
	if err != nil {
		return err
	}

	err = util.DeleteNamespace(ctx.Env().Hub, managementNamespace, log)
	if err != nil {
		return err
	}

	log.Info("Workload undeployed")

	return nil
}

func (s Subscription) IsDiscovered() bool {
	return false
}
