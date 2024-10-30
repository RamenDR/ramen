// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	"github.com/go-logr/logr"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/util"
	"github.com/ramendr/ramen/e2e/workloads"
)

func EnableProtectionDiscoveredApps(w workloads.Workload, d deployers.Deployer) error {
	name := GetCombinedName(d, w)
	namespace := GetNamespace(d, w) // this namespace is in hub
	namespaceInDrCluster := name    // this namespace is in dr clusters
	log := util.Ctx.Log.WithName(name)

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
	if err := createPlacementManagedByRamen(placementName, namespace, log); err != nil {
		return err
	}

	// create drpc
	drpolicy, err := util.GetDRPolicy(util.Ctx.Hub.CtrlClient, drPolicyName)
	if err != nil {
		return err
	}

	log.Info("Creating drpc")

	clusterName := drpolicy.Spec.DRClusters[0]

	drpc := generateDRPCDiscoveredApps(
		name, namespace, clusterName, drPolicyName, placementName, appname, namespaceInDrCluster)
	if err = createDRPC(util.Ctx.Hub.CtrlClient, drpc); err != nil {
		return err
	}

	// wait for drpc ready
	return waitDRPCReady(util.Ctx.Hub.CtrlClient, namespace, drpcName, log)
}

// remove DRPC
// update placement annotation
func DisableProtectionDiscoveredApps(w workloads.Workload, d deployers.Deployer) error {
	name := GetCombinedName(d, w)
	namespace := GetNamespace(d, w) // this namespace is in hub
	log := util.Ctx.Log.WithName(name)

	log.Info("Unprotecting workload")

	placementName := name
	drpcName := name

	client := util.Ctx.Hub.CtrlClient

	log.Info("Deleting drpc")

	if err := deleteDRPC(client, namespace, drpcName); err != nil {
		return err
	}

	if err := waitDRPCDeleted(client, namespace, drpcName, log); err != nil {
		return err
	}

	// delete placement
	if err := deployers.DeletePlacement(placementName, namespace, log); err != nil {
		return err
	}

	return deployers.DeleteManagedClusterSetBinding(deployers.McsbName, namespace, log)
}

func FailoverDiscoveredApps(w workloads.Workload, d deployers.Deployer, log logr.Logger) error {
	log.Info("Failing over workload")

	return failoverRelocateDiscoveredApps(w, d, ramen.ActionFailover, log)
}

func RelocateDiscoveredApps(w workloads.Workload, d deployers.Deployer, log logr.Logger) error {
	log.Info("Relocating workload")

	return failoverRelocateDiscoveredApps(w, d, ramen.ActionRelocate, log)
}

// nolint:funlen,cyclop
func failoverRelocateDiscoveredApps(w workloads.Workload, d deployers.Deployer, action ramen.DRAction,
	log logr.Logger,
) error {
	name := GetCombinedName(d, w)
	namespace := GetNamespace(d, w) // this namespace is in hub
	namespaceInDrCluster := name    // this namespace is in dr clusters

	drpcName := name
	client := util.Ctx.Hub.CtrlClient

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

	if err := waitAndUpdateDRPC(client, namespace, drpcName, action, log); err != nil {
		return err
	}

	if err := waitDRPCProgression(client, namespace, name, ramen.ProgressionWaitOnUserToCleanUp, log); err != nil {
		return err
	}

	// delete pvc and deployment from dr cluster
	log.Info("Cleaning up discovered apps from " + currentCluster)

	if err = deployers.DeleteDiscoveredApps(w, namespaceInDrCluster, currentCluster, log); err != nil {
		return err
	}

	if err = waitDRPCProgression(client, namespace, name, ramen.ProgressionCompleted, log); err != nil {
		return err
	}

	if err = waitDRPCReady(client, namespace, name, log); err != nil {
		return err
	}

	drClient := getDRClusterClient(targetCluster, drpolicy)

	return deployers.WaitWorkloadHealth(drClient, namespaceInDrCluster, w, log)
}
