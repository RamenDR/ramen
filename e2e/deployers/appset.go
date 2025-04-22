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

	log.Info("Workload deployed")

	return nil
}

// Undeploy deletes an ApplicationSet from the hub cluster, deleting the workload from the managed clusters.
func (a ApplicationSet) Undeploy(ctx types.TestContext) error {
	name := ctx.Name()
	log := ctx.Logger()
	managementNamespace := ctx.ManagementNamespace()

	clusterName, err := util.GetCurrentCluster(ctx, managementNamespace, name)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}

		log.Debugf("Could not retrieve the cluster name: %s", err)
		log.Infof("Undeploying applicationset app \"%s/%s\"", ctx.AppNamespace(), ctx.Workload().GetAppName())
	} else {
		log.Infof("Undeploying applicationset app \"%s/%s\" in cluster %q",
			ctx.AppNamespace(), ctx.Workload().GetAppName(), clusterName)
	}

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

	log.Info("Workload undeployed")

	return nil
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
