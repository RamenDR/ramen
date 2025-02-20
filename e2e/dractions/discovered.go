// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
)

func EnableProtectionDiscoveredApps(ctx types.Context) error {
	w := ctx.Workload()
	name := ctx.Name()
	log := ctx.Logger()
	managementNamespace := ctx.ManagementNamespace()
	appNamespace := ctx.AppNamespace()

	log.Infof("Protecting workload in app namespace %q, management namespace: %q", appNamespace, managementNamespace)

	drPolicyName := util.DefaultDRPolicyName
	appname := w.GetAppName()
	placementName := name
	drpcName := name

	// create mcsb default in ramen-ops ns
	if err := deployers.CreateManagedClusterSetBinding(ctx, deployers.McsbName, managementNamespace); err != nil {
		return err
	}

	// create placement
	if err := createPlacementManagedByRamen(ctx, placementName, managementNamespace); err != nil {
		return err
	}

	// create drpc
	drpolicy, err := util.GetDRPolicy(util.Ctx.Hub.Client, drPolicyName)
	if err != nil {
		return err
	}

	clusterName := drpolicy.Spec.DRClusters[0]

	drpc := generateDRPCDiscoveredApps(
		name, managementNamespace, clusterName, drPolicyName, placementName, appname, appNamespace)
	if err = createDRPC(ctx, util.Ctx.Hub.Client, drpc); err != nil {
		return err
	}

	// wait for drpc ready
	return waitDRPCReady(ctx, util.Ctx.Hub.Client, managementNamespace, drpcName)
}

// remove DRPC
// update placement annotation
func DisableProtectionDiscoveredApps(ctx types.Context) error {
	name := ctx.Name()
	log := ctx.Logger()
	managementNamespace := ctx.ManagementNamespace()
	appNamespace := ctx.AppNamespace()

	log.Infof("Unprotecting workload in app namespace %q, management namespace: %q", appNamespace, managementNamespace)

	placementName := name
	drpcName := name

	client := util.Ctx.Hub.Client

	if err := deleteDRPC(ctx, client, managementNamespace, drpcName); err != nil {
		return err
	}

	if err := waitDRPCDeleted(ctx, client, managementNamespace, drpcName); err != nil {
		return err
	}

	// delete placement
	if err := deployers.DeletePlacement(ctx, placementName, managementNamespace); err != nil {
		return err
	}

	return deployers.DeleteManagedClusterSetBinding(ctx, deployers.McsbName, managementNamespace)
}

// nolint:funlen,cyclop
func failoverRelocateDiscoveredApps(
	ctx types.Context,
	action ramen.DRAction,
	state ramen.DRState,
	currentCluster string,
	targetCluster string,
) error {
	name := ctx.Name()
	managementNamespace := ctx.ManagementNamespace()
	appNamespace := ctx.AppNamespace()

	drpcName := name
	client := util.Ctx.Hub.Client

	drPolicyName := util.DefaultDRPolicyName

	drpolicy, err := util.GetDRPolicy(client, drPolicyName)
	if err != nil {
		return err
	}

	if err := waitAndUpdateDRPC(ctx, client, managementNamespace, drpcName, action, targetCluster); err != nil {
		return err
	}

	if err := waitDRPCProgression(ctx, client, managementNamespace, name,
		ramen.ProgressionWaitOnUserToCleanUp); err != nil {
		return err
	}

	// delete pvc and deployment from dr cluster
	if err = deployers.DeleteDiscoveredApps(ctx, appNamespace, currentCluster); err != nil {
		return err
	}

	if err := waitDRPCPhase(ctx, client, managementNamespace, name, state); err != nil {
		return err
	}

	if err := waitDRPCReady(ctx, client, managementNamespace, name); err != nil {
		return err
	}

	drClient := getDRClusterClient(targetCluster, drpolicy)

	return deployers.WaitWorkloadHealth(ctx, drClient, appNamespace)
}
