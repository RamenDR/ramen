// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/util"
	"github.com/ramendr/ramen/e2e/workloads"
)

func EnableProtectionDiscoveredApps(w workloads.Workload, d deployers.Deployer) error {
	name := GetCombinedName(d, w)
	namespace := GetNamespace(d, w) // this namespace is in hub
	namespaceInDrCluster := name    // this namespace is in dr clusters

	util.Ctx.Log.Info("enter EnableProtectionDiscoveredApps " + name)

	drPolicyName := DefaultDRPolicyName
	appname := w.GetAppName()
	placementName := name
	drpcName := name

	// create mcsb default in ramen-ops ns
	if err := deployers.CreateManagedClusterSetBinding(deployers.McsbName, namespace); err != nil {
		return err
	}

	// create placement
	if err := createPlacementManagedByRamen(placementName, namespace); err != nil {
		return err
	}

	// create drpc
	drpolicy, err := getDRPolicy(util.Ctx.Hub.CtrlClient, drPolicyName)
	if err != nil {
		return err
	}

	util.Ctx.Log.Info("create drpc " + drpcName)

	clusterName := drpolicy.Spec.DRClusters[0]

	drpc := generateDRPCDiscoveredApps(
		name, namespace, clusterName, drPolicyName, placementName, appname, namespaceInDrCluster)
	if err = createDRPC(util.Ctx.Hub.CtrlClient, drpc); err != nil {
		return err
	}

	// wait for drpc ready
	return waitDRPCReady(util.Ctx.Hub.CtrlClient, namespace, drpcName)
}

// remove DRPC
// update placement annotation
func DisableProtectionDiscoveredApps(w workloads.Workload, d deployers.Deployer) error {
	name := GetCombinedName(d, w)
	namespace := GetNamespace(d, w) // this namespace is in hub

	util.Ctx.Log.Info("enter DisableProtectionDiscoveredApps " + name)

	placementName := name
	drpcName := name

	client := util.Ctx.Hub.CtrlClient

	util.Ctx.Log.Info("delete drpc " + drpcName)

	if err := deleteDRPC(client, namespace, drpcName); err != nil {
		return err
	}

	if err := waitDRPCDeleted(client, namespace, drpcName); err != nil {
		return err
	}

	// delete placement
	if err := deployers.DeletePlacement(placementName, namespace); err != nil {
		return err
	}

	return deployers.DeleteManagedClusterSetBinding(deployers.McsbName, namespace)
}

func FailoverDiscoveredApps(w workloads.Workload, d deployers.Deployer) error {
	util.Ctx.Log.Info("enter DRActions FailoverDiscoveredApps")

	return failoverRelocateDiscoveredApps(w, d, ramen.ActionFailover)
}

func RelocateDiscoveredApps(w workloads.Workload, d deployers.Deployer) error {
	util.Ctx.Log.Info("enter DRActions RelocateDiscoveredApps")

	return failoverRelocateDiscoveredApps(w, d, ramen.ActionRelocate)
}

// nolint:funlen,cyclop
func failoverRelocateDiscoveredApps(w workloads.Workload, d deployers.Deployer, action ramen.DRAction) error {
	name := GetCombinedName(d, w)
	namespace := GetNamespace(d, w) // this namespace is in hub
	namespaceInDrCluster := name    // this namespace is in dr clusters

	drPolicyName := DefaultDRPolicyName
	drpcName := name
	client := util.Ctx.Hub.CtrlClient

	if err := waitAndUpdateDRPC(client, namespace, drpcName, action); err != nil {
		return err
	}

	currentCluster, err := getCurrentCluster(client, namespace, name)
	if err != nil {
		return err
	}

	drpolicy, err := getDRPolicy(client, drPolicyName)
	if err != nil {
		return err
	}

	drClient := getDRClusterClient(currentCluster, drpolicy)

	if err = waitDRPCProgression(client, namespace, name, ramen.ProgressionWaitOnUserToCleanUp); err != nil {
		return err
	}

	// delete pvc and deployment from dr cluster
	util.Ctx.Log.Info("start to clean up discovered apps from " + currentCluster)

	if err = deployers.DeleteDiscoveredApps(drClient, namespaceInDrCluster); err != nil {
		return err
	}

	if err = waitDRPCProgression(client, namespace, name, ramen.ProgressionCompleted); err != nil {
		return err
	}

	return waitDRPCReady(client, namespace, name)
}
