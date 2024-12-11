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
	namespace := ctx.Namespace() // this namespace is in hub
	namespaceInDrCluster := name // this namespace is in dr clusters

	log.Info("Protecting workload")

	drPolicyName := util.DefaultDRPolicyName
	appname := w.GetAppName()
	placementName := name
	drpcName := name

	// create mcsb default in ramen-ops ns
	if err := deployers.CreateManagedClusterSetBinding(deployers.McsbName, namespace); err != nil {
		return err
	}

	// create placement
	if err := createPlacementManagedByRamen(ctx, placementName, namespace); err != nil {
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
		name, namespace, clusterName, drPolicyName, placementName, appname, namespaceInDrCluster)
	if err = createDRPC(util.Ctx.Hub.Client, drpc); err != nil {
		return err
	}

	// wait for drpc ready
	return waitDRPCReady(ctx, util.Ctx.Hub.Client, namespace, drpcName)
}

// remove DRPC
// update placement annotation
func DisableProtectionDiscoveredApps(ctx types.Context) error {
	name := ctx.Name()
	log := ctx.Logger()
	namespace := ctx.Namespace() // this namespace is in hub

	log.Info("Unprotecting workload")

	placementName := name
	drpcName := name

	client := util.Ctx.Hub.Client

	log.Info("Deleting drpc")

	if err := deleteDRPC(client, namespace, drpcName); err != nil {
		return err
	}

	if err := waitDRPCDeleted(ctx, client, namespace, drpcName); err != nil {
		return err
	}

	// delete placement
	if err := deployers.DeletePlacement(ctx, placementName, namespace); err != nil {
		return err
	}

	return deployers.DeleteManagedClusterSetBinding(ctx, deployers.McsbName, namespace)
}

func FailoverDiscoveredApps(ctx types.Context) error {
	log := ctx.Logger()
	log.Info("Failing over workload")

	return failoverRelocateDiscoveredApps(ctx, ramen.ActionFailover, ramen.FailedOver)
}

func RelocateDiscoveredApps(ctx types.Context) error {
	log := ctx.Logger()
	log.Info("Relocating workload")

	return failoverRelocateDiscoveredApps(ctx, ramen.ActionRelocate, ramen.Relocated)
}

// nolint:funlen,cyclop
func failoverRelocateDiscoveredApps(ctx types.Context, action ramen.DRAction, state ramen.DRState) error {
	name := ctx.Name()
	log := ctx.Logger()
	namespace := ctx.Namespace() // this namespace is in hub
	namespaceInDrCluster := name // this namespace is in dr clusters

	drpcName := name
	client := util.Ctx.Hub.Client

	currentCluster, err := getCurrentCluster(client, namespace, name)
	if err != nil {
		return err
	}

	drPolicyName := util.DefaultDRPolicyName

	drpolicy, err := util.GetDRPolicy(client, drPolicyName)
	if err != nil {
		return err
	}

	targetCluster, err := getTargetCluster(client, namespace, drpcName, drpolicy)
	if err != nil {
		return err
	}

	if err := waitAndUpdateDRPC(ctx, client, namespace, drpcName, action); err != nil {
		return err
	}

	if err := waitDRPCProgression(ctx, client, namespace, name, ramen.ProgressionWaitOnUserToCleanUp); err != nil {
		return err
	}

	// delete pvc and deployment from dr cluster
	log.Infof("Cleaning up discovered apps from cluster %q", currentCluster)

	if err = deployers.DeleteDiscoveredApps(ctx, namespaceInDrCluster, currentCluster); err != nil {
		return err
	}

	if err := waitDRPCPhase(ctx, client, namespace, name, state); err != nil {
		return err
	}

	if err := waitDRPCReady(ctx, client, namespace, name); err != nil {
		return err
	}

	drClient := getDRClusterClient(targetCluster, drpolicy)

	return deployers.WaitWorkloadHealth(ctx, drClient, namespaceInDrCluster)
}
