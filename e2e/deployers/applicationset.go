// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
)

const (
	argocdNamespace = "argocd"
)

type ApplicationSet struct{}

// Deploy creates an ApplicationSet on the hub cluster, creating the workload on one of the managed clusters.
func (a ApplicationSet) Deploy(ctx types.Context) error {
	name := ctx.Name()
	log := ctx.Logger()
	managementNamespace := ctx.ManagementNamespace()

	err := CreateManagedClusterSetBinding(ctx, McsbName, managementNamespace)
	if err != nil {
		return err
	}

	err = CreatePlacement(ctx, name, managementNamespace)
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

	clusterName, err := util.GetCurrentCluster(util.Ctx.Hub.Client, managementNamespace, name)
	if err != nil {
		return err
	}

	log.Infof("Deployed applicationset app \"%s/%s\" on cluster %q",
		ctx.AppNamespace(), ctx.Workload().GetAppName(), clusterName)

	return nil
}

// Undeploy deletes an ApplicationSet from the hub cluster, deleting the workload from the managed clusters.
func (a ApplicationSet) Undeploy(ctx types.Context) error {
	name := ctx.Name()
	log := ctx.Logger()
	managementNamespace := ctx.ManagementNamespace()

	err := DeleteApplicationSet(ctx, a)
	if err != nil {
		return err
	}

	clusterName, err := util.GetCurrentCluster(util.Ctx.Hub.Client, managementNamespace, name)
	if err != nil {
		return err
	}

	log.Infof("Undeployed applicationset app \"%s/%s\" on cluster %q",
		ctx.AppNamespace(), ctx.Workload().GetAppName(), clusterName)

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
		err = DeleteManagedClusterSetBinding(ctx, McsbName, managementNamespace)
		if err != nil {
			return err
		}
	}

	return nil
}

func (a ApplicationSet) GetName() string {
	return "appset"
}

func (a ApplicationSet) GetNamespace() string {
	return argocdNamespace
}

func (a ApplicationSet) IsDiscovered() bool {
	return false
}
