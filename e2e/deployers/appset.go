// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
)

type ApplicationSet struct{}

// Deploy creates an ApplicationSet on the hub cluster, creating the workload on one of the managed clusters.
func (a ApplicationSet) Deploy(ctx types.Context) error {
	name := ctx.Name()
	log := ctx.Logger()
	managementNamespace := ctx.ManagementNamespace()

	drpolicy, err := util.GetDRPolicy(util.Ctx.Hub, config.GetDRPolicyName())
	if err != nil {
		return err
	}

	log.Infof("Deploying applicationset app \"%s/%s\" in cluster %q",
		ctx.AppNamespace(), ctx.Workload().GetAppName(), drpolicy.Spec.DRClusters[0])

	err = CreateManagedClusterSetBinding(ctx, config.GetClusterSetName(), managementNamespace)
	if err != nil {
		return err
	}

	err = CreatePlacement(ctx, name, managementNamespace, drpolicy.Spec.DRClusters[0])
	if err != nil {
		return err
	}

	err = CreatePlacementDecisionConfigMap(ctx, name, managementNamespace)
	if err != nil {
		return err
	}

	err = CreateApplicationSet(ctx, a)
	if err != nil {
		return err
	}

	log.Info("Workload deployed")

	return nil
}

// Undeploy deletes an ApplicationSet from the hub cluster, deleting the workload from the managed clusters.
func (a ApplicationSet) Undeploy(ctx types.Context) error {
	name := ctx.Name()
	log := ctx.Logger()
	managementNamespace := ctx.ManagementNamespace()

	clusterName, err := util.GetCurrentCluster(util.Ctx.Hub, managementNamespace, name)
	if err != nil {
		return err
	}

	log.Infof("Undeploying applicationset app \"%s/%s\" in cluster %q",
		ctx.AppNamespace(), ctx.Workload().GetAppName(), clusterName)

	err = DeleteApplicationSet(ctx, a)
	if err != nil {
		return err
	}

	err = DeleteConfigMap(ctx, name, managementNamespace)
	if err != nil {
		return err
	}

	err = DeletePlacement(ctx, name, managementNamespace)
	if err != nil {
		return err
	}

	// multiple appsets could use the same mcsb in argocd ns.
	// so delete mcsb if only 1 appset is in argocd ns
	lastAppset, err := isLastAppsetInArgocdNs(managementNamespace)
	if err != nil {
		return err
	}

	if lastAppset {
		err = DeleteManagedClusterSetBinding(ctx, config.GetClusterSetName(), managementNamespace)
		if err != nil {
			return err
		}
	}

	log.Info("Workload undeployed")

	return nil
}

func (a ApplicationSet) GetName() string {
	return "appset"
}

func (a ApplicationSet) GetNamespace() string {
	return config.GetNamespaces().ArgocdNamespace
}

func (a ApplicationSet) IsDiscovered() bool {
	return false
}
