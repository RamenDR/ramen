// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"

	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
)

const (
	OcmSchedulingDisable = "cluster.open-cluster-management.io/experimental-scheduling-disable"
)

// If AppSet/Subscription, find Placement
// Determine DRPolicy
// Determine preferredCluster
// Determine PVC label selector
// Determine KubeObjectProtection requirements if Imperative (?)
// Create DRPC, in desired namespace
// nolint:funlen,cyclop
func EnableProtection(ctx types.TestContext) error {
	d := ctx.Deployer()
	if d.IsDiscovered() {
		return EnableProtectionDiscoveredApps(ctx)
	}

	w := ctx.Workload()
	name := ctx.Name()
	managementNamespace := ctx.ManagementNamespace()
	appNamespace := ctx.AppNamespace()
	log := ctx.Logger()
	cfg := ctx.Config()

	drPolicyName := cfg.DRPolicy
	appname := w.GetAppName()
	placementName := name
	drpcName := name

	cluster, err := util.GetCurrentCluster(ctx, managementNamespace, placementName)
	if err != nil {
		return err
	}

	log.Infof("Protecting workload \"%s/%s\" in cluster %q", appNamespace, appname, cluster.Name)

	err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		placement, err := util.GetPlacement(ctx, managementNamespace, placementName)
		if err != nil {
			return err
		}

		if placement.Annotations == nil {
			placement.Annotations = make(map[string]string)
		}

		placement.Annotations[OcmSchedulingDisable] = "true"

		if err := updatePlacement(ctx, placement); err != nil {
			return err
		}

		log.Debugf("Annotated placement \"%s/%s\" with \"%s: %s\" in cluster %q",
			managementNamespace, placementName, OcmSchedulingDisable,
			placement.Annotations[OcmSchedulingDisable], ctx.Env().Hub.Name)

		return nil
	})
	if err != nil {
		return err
	}

	drpc := generateDRPC(name, managementNamespace, cluster.Name, drPolicyName, placementName, appname)
	if err = createDRPC(ctx, drpc); err != nil {
		return err
	}

	if err := util.AddVolsyncAnnontationOnManagedClusters(ctx, appNamespace); err != nil {
		return err
	}

	err = waitDRPCReady(ctx, managementNamespace, drpcName)
	if err != nil {
		return err
	}

	if err = deployers.WaitWorkloadHealth(ctx, cluster); err != nil {
		return err
	}

	log.Info("Workload protected")

	return nil
}

// remove DRPC
// update placement annotation
func DisableProtection(ctx types.TestContext) error {
	name := ctx.Name()
	managementNamespace := ctx.ManagementNamespace()
	appNamespace := ctx.AppNamespace()
	placementName := name
	log := ctx.Logger()

	cluster, err := util.GetCurrentCluster(ctx, managementNamespace, placementName)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}

		log.Debugf("Could not retrieve the cluster name: %s", err)
		log.Infof("Unprotecting workload \"%s/%s\"", appNamespace, ctx.Workload().GetAppName())
	} else {
		log.Infof("Unprotecting workload \"%s/%s\" in cluster %q",
			appNamespace, ctx.Workload().GetAppName(), cluster.Name)
	}

	if err := deleteProtectionResources(ctx); err != nil {
		return err
	}

	if err := waitForProtectionResourcesDelete(ctx); err != nil {
		return err
	}

	// If the cluster is not nil, the workload exists and its health is validated.
	if cluster != nil {
		if err := ctx.Workload().Health(ctx, cluster); err != nil {
			return err
		}

		log.Debugf("Workload \"%s/%s\" is healthy in cluster %q",
			appNamespace, ctx.Workload().GetAppName(), cluster.Name)
	}

	log.Info("Workload unprotected")

	return nil
}

func Failover(ctx types.TestContext) error {
	managementNamespace := ctx.ManagementNamespace()
	log := ctx.Logger()
	name := ctx.Name()
	config := ctx.Config()

	currentCluster, err := util.GetCurrentCluster(ctx, managementNamespace, name)
	if err != nil {
		return err
	}

	targetCluster, err := getTargetCluster(ctx, ctx.Env().Hub, config.DRPolicy, currentCluster.Name)
	if err != nil {
		return err
	}

	log.Infof("Failing over workload \"%s/%s\" from cluster %q to cluster %q",
		ctx.AppNamespace(), ctx.Workload().GetAppName(), currentCluster.Name, targetCluster.Name)

	err = failoverRelocate(ctx, ramen.ActionFailover, ramen.FailedOver, currentCluster, targetCluster)
	if err != nil {
		return err
	}

	log.Info("Workload failed over")

	return nil
}

// Determine DRPC
// Check Placement
// Relocate to Primary in DRPolicy as the PrimaryCluster
// Update DRPC
func Relocate(ctx types.TestContext) error {
	managementNamespace := ctx.ManagementNamespace()
	log := ctx.Logger()
	config := ctx.Config()
	name := ctx.Name()

	currentCluster, err := util.GetCurrentCluster(ctx, managementNamespace, name)
	if err != nil {
		return err
	}

	targetCluster, err := getTargetCluster(ctx, ctx.Env().Hub, config.DRPolicy, currentCluster.Name)
	if err != nil {
		return err
	}

	log.Infof("Relocating workload \"%s/%s\" from cluster %q to cluster %q",
		ctx.AppNamespace(), ctx.Workload().GetAppName(), currentCluster.Name, targetCluster.Name)

	err = failoverRelocate(ctx, ramen.ActionRelocate, ramen.Relocated, currentCluster, targetCluster)
	if err != nil {
		return err
	}

	log.Info("Workload relocated")

	return nil
}

// Purge deletes the workload's protection resources, then the workload,
// and waits for all related resources to be completely deleted.
func Purge(ctx types.TestContext) error {
	log := ctx.Logger()

	cluster, err := util.GetCurrentCluster(ctx, ctx.ManagementNamespace(), ctx.Name())
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}

		log.Infof("Purging workload \"%s/%s\"", ctx.AppNamespace(), ctx.Workload().GetAppName())
	} else {
		log.Infof("Purging workload \"%s/%s\" in cluster %q",
			ctx.AppNamespace(), ctx.Workload().GetAppName(), cluster.Name)
	}

	if err := deleteProtectionResources(ctx); err != nil {
		return err
	}

	if err := ctx.Deployer().DeleteResources(ctx); err != nil {
		return err
	}

	if err := waitForProtectionResourcesDelete(ctx); err != nil {
		return err
	}

	if err := ctx.Deployer().WaitForResourcesDelete(ctx); err != nil {
		return err
	}

	log.Info("Workload purged")

	return nil
}

func failoverRelocate(
	ctx types.TestContext,
	action ramen.DRAction,
	state ramen.DRState,
	currentCluster, targetCluster *types.Cluster,
) error {
	d := ctx.Deployer()
	if d.IsDiscovered() {
		return failoverRelocateDiscoveredApps(ctx, action, state, currentCluster, targetCluster)
	}

	drpcName := ctx.Name()
	managementNamespace := ctx.ManagementNamespace()

	if err := waitAndUpdateDRPC(ctx, managementNamespace, drpcName, action, targetCluster); err != nil {
		return err
	}

	if err := waitDRPCPhase(ctx, managementNamespace, drpcName, state); err != nil {
		return err
	}

	if err := waitDRPCReady(ctx, managementNamespace, drpcName); err != nil {
		return err
	}

	return deployers.WaitWorkloadHealth(ctx, targetCluster)
}

func waitAndUpdateDRPC(
	ctx types.TestContext,
	namespace, drpcName string,
	action ramen.DRAction,
	targetCluster *types.Cluster,
) error {
	log := ctx.Logger()

	// here we expect drpc should be ready before action
	if err := waitDRPCReady(ctx, namespace, drpcName); err != nil {
		return err
	}

	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		drpc, err := getDRPC(ctx, namespace, drpcName)
		if err != nil {
			return err
		}

		drpc.Spec.Action = action
		if action == ramen.ActionFailover {
			drpc.Spec.FailoverCluster = targetCluster.Name
		} else {
			drpc.Spec.PreferredCluster = targetCluster.Name
		}

		if err := updateDRPC(ctx, drpc); err != nil {
			return err
		}

		log.Debugf("Updated drpc \"%s/%s\" with action %q to target cluster %q",
			namespace, drpcName, action, targetCluster.Name)

		return nil
	})
}

// deleteProtectionResources deletes protection-related resources like the DRPC.
// For discovered deployers, it also deletes the associated Placement and ManagedClusterSetBinding.
func deleteProtectionResources(ctx types.TestContext) error {
	if err := deleteDRPC(ctx, ctx.ManagementNamespace(), ctx.Name()); err != nil {
		return err
	}

	if ctx.Deployer().IsDiscovered() {
		if err := deployers.DeletePlacement(ctx, ctx.Name(), ctx.ManagementNamespace()); err != nil {
			return err
		}
	}

	return nil
}

// waitForProtectionResourcesDelete waits for protection-related resources to be deleted.
// This includes the DRPC, and for discovered deployers, the Placement and ManagedClusterSetBinding.
func waitForProtectionResourcesDelete(ctx types.TestContext) error {
	if err := util.WaitForDRPCDelete(ctx, ctx.Env().Hub, ctx.Name(), ctx.ManagementNamespace()); err != nil {
		return err
	}

	if ctx.Deployer().IsDiscovered() {
		if err := util.WaitForPlacementDelete(ctx, ctx.Env().Hub, ctx.Name(), ctx.ManagementNamespace()); err != nil {
			return err
		}
	}

	return nil
}
