// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
)

func EnableProtectionDiscoveredApps(ctx types.Context) error {
	w := ctx.Workload()
	name := ctx.Name()
	log := ctx.Logger()
	managementNamespace := ctx.ManagementNamespace()
	appNamespace := ctx.AppNamespace()

	drPolicyName := config.GetDRPolicyName()
	appname := w.GetAppName()
	placementName := name
	drpcName := name

	// create mcsb default in ramen-ops ns
	if err := deployers.CreateManagedClusterSetBinding(ctx, config.GetClusterSetName(), managementNamespace); err != nil {
		return err
	}

	// create placement
	if err := createPlacementManagedByRamen(ctx, placementName, managementNamespace); err != nil {
		return err
	}

	// create drpc
	drpolicy, err := util.GetDRPolicy(util.Ctx.Hub, drPolicyName)
	if err != nil {
		return err
	}

	clusterName := drpolicy.Spec.DRClusters[0]

	log.Infof("Protecting workload \"%s/%s\" in cluster %q", appNamespace, appname, clusterName)

	drpc := generateDRPCDiscoveredApps(
		name, managementNamespace, clusterName, drPolicyName, placementName, appname, appNamespace)
	if err = createDRPC(ctx, util.Ctx.Hub, drpc); err != nil {
		return err
	}

	// wait for drpc ready
	err = waitDRPCReady(ctx, util.Ctx.Hub, managementNamespace, drpcName)
	if err != nil {
		return err
	}

	log.Info("Workload protected")

	return nil
}

// remove DRPC
// update placement annotation
func DisableProtectionDiscoveredApps(ctx types.Context) error {
	name := ctx.Name()
	log := ctx.Logger()
	managementNamespace := ctx.ManagementNamespace()
	appNamespace := ctx.AppNamespace()

	placementName := name
	drpcName := name

	clusterName, err := util.GetCurrentCluster(util.Ctx.Hub, managementNamespace, placementName)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}

		log.Debugf("Could not retrieve the cluster name: %s", err)
		log.Infof("Unprotecting workload \"%s/%s\"", appNamespace, ctx.Workload().GetAppName())
	} else {
		log.Infof("Unprotecting workload \"%s/%s\" in cluster %q",
			appNamespace, ctx.Workload().GetAppName(), clusterName)
	}

	if err := deleteDRPC(ctx, util.Ctx.Hub, managementNamespace, drpcName); err != nil {
		return err
	}

	if err := waitDRPCDeleted(ctx, util.Ctx.Hub, managementNamespace, drpcName); err != nil {
		return err
	}

	// delete placement
	if err := deployers.DeletePlacement(ctx, placementName, managementNamespace); err != nil {
		return err
	}

	err = deployers.DeleteManagedClusterSetBinding(ctx, config.GetClusterSetName(), managementNamespace)
	if err != nil {
		return err
	}

	log.Info("Workload unprotected")

	return nil
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

	drPolicyName := config.GetDRPolicyName()

	drpolicy, err := util.GetDRPolicy(util.Ctx.Hub, drPolicyName)
	if err != nil {
		return err
	}

	if err := waitAndUpdateDRPC(ctx, util.Ctx.Hub, managementNamespace, drpcName, action, targetCluster); err != nil {
		return err
	}

	if err := waitDRPCProgression(ctx, util.Ctx.Hub, managementNamespace, name,
		ramen.ProgressionWaitOnUserToCleanUp); err != nil {
		return err
	}

	// delete pvc and deployment from dr cluster
	if err = deployers.DeleteDiscoveredApps(ctx, appNamespace, currentCluster); err != nil {
		return err
	}

	if err := waitDRPCPhase(ctx, util.Ctx.Hub, managementNamespace, name, state); err != nil {
		return err
	}

	if err := waitDRPCReady(ctx, util.Ctx.Hub, managementNamespace, name); err != nil {
		return err
	}

	drCluster := getDRCluster(targetCluster, drpolicy)

	return deployers.WaitWorkloadHealth(ctx, drCluster, appNamespace)
}
