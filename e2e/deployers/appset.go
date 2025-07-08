// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
)

type ApplicationSet struct{}

// Deploy creates an ApplicationSet on the hub cluster, creating the workload on one of the managed clusters.
func (a ApplicationSet) Deploy(ctx types.TestContext) error {
	name := ctx.Name()
	log := ctx.Logger()
	managementNamespace := ctx.ManagementNamespace()

	// Deploys the application on the first DR cluster (c1).
	cluster := ctx.Env().C1

	log.Infof("Deploying applicationset app \"%s/%s\" in cluster %q",
		ctx.AppNamespace(), ctx.Workload().GetAppName(), cluster.Name)

	if err := util.CreateNamespaceOnMangedClusters(ctx, ctx.AppNamespace()); err != nil {
		return err
	}

	err := CreatePlacement(ctx, name, managementNamespace, cluster.Name)
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

	if err = WaitWorkloadHealth(ctx, cluster); err != nil {
		return err
	}

	log.Info("Workload deployed")

	return nil
}

// Undeploy deletes an ApplicationSet from the hub cluster, deleting the workload from the managed clusters.
func (a ApplicationSet) Undeploy(ctx types.TestContext) error {
	name := ctx.Name()
	log := ctx.Logger()
	managementNamespace := ctx.ManagementNamespace()

	cluster, err := util.GetCurrentCluster(ctx, managementNamespace, name)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}

		log.Debugf("Could not retrieve the cluster name: %s", err)
		log.Infof("Undeploying applicationset app \"%s/%s\"", ctx.AppNamespace(), ctx.Workload().GetAppName())
	} else {
		log.Infof("Undeploying applicationset app \"%s/%s\" in cluster %q",
			ctx.AppNamespace(), ctx.Workload().GetAppName(), cluster.Name)
	}

	if err := a.DeleteResources(ctx); err != nil {
		return err
	}

	if err := a.WaitForResourcesDelete(ctx); err != nil {
		return err
	}

	log.Info("Workload undeployed")

	return nil
}

func (a ApplicationSet) DeleteResources(ctx types.TestContext) error {
	if err := DeleteApplicationSet(ctx, a); err != nil {
		return err
	}

	if err := DeleteConfigMap(ctx, ctx.Name(), ctx.ManagementNamespace()); err != nil {
		return err
	}

	if err := DeletePlacement(ctx, ctx.Name(), ctx.ManagementNamespace()); err != nil {
		return err
	}

	return util.DeleteNamespaceOnManagedClusters(ctx, ctx.AppNamespace())
}

func (a ApplicationSet) WaitForResourcesDelete(ctx types.TestContext) error {
	if err := util.WaitForApplicationSetDelete(ctx, ctx.Env().Hub, ctx.Name(), ctx.ManagementNamespace()); err != nil {
		return err
	}

	if err := util.WaitForConfigMapDelete(ctx, ctx.Env().Hub, ctx.Name(), ctx.ManagementNamespace()); err != nil {
		return err
	}

	if err := util.WaitForPlacementDelete(ctx, ctx.Env().Hub, ctx.Name(), ctx.ManagementNamespace()); err != nil {
		return err
	}

	return util.WaitForNamespaceDeleteOnManagedClusters(ctx, ctx.AppNamespace())
}

func (a ApplicationSet) GetName() string {
	return "appset"
}

func (a ApplicationSet) GetNamespace(ctx types.TestContext) string {
	return ctx.Config().Namespaces.ArgocdNamespace
}

func (a ApplicationSet) IsDiscovered() bool {
	return false
}
