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

	// TODO: log cluster namespace for completeness?
	log.Infof("Protecting workload in namespace %q", managementNamespace)

	drPolicyName := util.DefaultDRPolicyName
	appname := w.GetAppName()
	placementName := name
	drpcName := name

	// create mcsb default in ramen-ops ns
	if err := deployers.CreateManagedClusterSetBinding(deployers.McsbName, managementNamespace); err != nil {
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

	log.Info("Creating drpc")

	clusterName := drpolicy.Spec.DRClusters[0]

	drpc := generateDRPCDiscoveredApps(
		name, managementNamespace, clusterName, drPolicyName, placementName, appname, appNamespace)
	if err = createDRPC(util.Ctx.Hub.Client, drpc); err != nil {
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

	// TODO: log cluster namespace for completeness?
	log.Infof("Unprotecting workload in namespace %q", managementNamespace)

	placementName := name
	drpcName := name

	client := util.Ctx.Hub.Client

	log.Info("Deleting drpc")

	if err := deleteDRPC(client, managementNamespace, drpcName); err != nil {
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

func FailoverDiscoveredApps(ctx types.Context) error {
	log := ctx.Logger()
	managementNamespace := ctx.ManagementNamespace()

	log.Infof("Failing over workload in namespace %q", managementNamespace)

	return failoverRelocateDiscoveredApps(ctx, ramen.ActionFailover, ramen.FailedOver)
}

func RelocateDiscoveredApps(ctx types.Context) error {
	log := ctx.Logger()
	managementNamespace := ctx.ManagementNamespace()

	log.Infof("Relocating workload in namespace %q", managementNamespace)

	return failoverRelocateDiscoveredApps(ctx, ramen.ActionRelocate, ramen.Relocated)
}

// nolint:funlen,cyclop
func failoverRelocateDiscoveredApps(ctx types.Context, action ramen.DRAction, state ramen.DRState) error {
	name := ctx.Name()
	log := ctx.Logger()
	managementNamespace := ctx.ManagementNamespace()
	appNamespace := ctx.AppNamespace()

	drpcName := name
	client := util.Ctx.Hub.Client

	currentCluster, err := getCurrentCluster(client, managementNamespace, name)
	if err != nil {
		return err
	}

	drPolicyName := util.DefaultDRPolicyName

	drpolicy, err := util.GetDRPolicy(client, drPolicyName)
	if err != nil {
		return err
	}

	targetCluster, err := getTargetCluster(client, managementNamespace, drpcName, drpolicy)
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
	log.Infof("Cleaning up discovered apps from cluster %q", currentCluster)

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
