// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
	"k8s.io/client-go/util/retry"
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
// nolint:funlen
func EnableProtection(ctx types.Context) error {
	d := ctx.Deployer()
	if d.IsDiscovered() {
		return EnableProtectionDiscoveredApps(ctx)
	}

	w := ctx.Workload()
	name := ctx.Name()
	managementNamespace := ctx.ManagementNamespace()
	appNamespace := ctx.AppNamespace()
	log := ctx.Logger()

	log.Infof("Protecting workload in app namespace %q, management namespace: %q", appNamespace, managementNamespace)

	drPolicyName := util.DefaultDRPolicyName
	appname := w.GetAppName()
	placementName := name
	drpcName := name

	clusterName, err := util.GetCurrentCluster(util.Ctx.Hub, managementNamespace, placementName)
	if err != nil {
		return err
	}

	log.Debugf("Workload running on cluster %q", clusterName)

	err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		placement, err := util.GetPlacement(util.Ctx.Hub, managementNamespace, placementName)
		if err != nil {
			return err
		}

		if placement.Annotations == nil {
			placement.Annotations = make(map[string]string)
		}

		placement.Annotations[OcmSchedulingDisable] = "true"

		if err := updatePlacement(util.Ctx.Hub, placement); err != nil {
			return err
		}

		log.Debugf("Annotated placement \"%s/%s\" with \"%s: %s\"",
			managementNamespace, placementName, OcmSchedulingDisable, placement.Annotations[OcmSchedulingDisable])

		return nil
	})
	if err != nil {
		return err
	}

	drpc := generateDRPC(name, managementNamespace, clusterName, drPolicyName, placementName, appname)
	if err = createDRPC(ctx, util.Ctx.Hub, drpc); err != nil {
		return err
	}

	// For volsync based replication we must create the cluster namespaces with special annotation.
	if err := util.CreateNamespaceAndAddAnnotation(ctx.AppNamespace()); err != nil {
		return err
	}

	return waitDRPCReady(ctx, util.Ctx.Hub, managementNamespace, drpcName)
}

// remove DRPC
// update placement annotation
func DisableProtection(ctx types.Context) error {
	d := ctx.Deployer()
	if d.IsDiscovered() {
		return DisableProtectionDiscoveredApps(ctx)
	}

	name := ctx.Name()
	managementNamespace := ctx.ManagementNamespace()
	appNamespace := ctx.AppNamespace()
	log := ctx.Logger()

	log.Infof("Unprotecting workload in app namespace %q, management namespace: %q", appNamespace, managementNamespace)

	drpcName := name

	if err := deleteDRPC(ctx, util.Ctx.Hub, managementNamespace, drpcName); err != nil {
		return err
	}

	return waitDRPCDeleted(ctx, util.Ctx.Hub, managementNamespace, drpcName)
}

func Failover(ctx types.Context) error {
	managementNamespace := ctx.ManagementNamespace()
	log := ctx.Logger()
	name := ctx.Name()

	currentCluster, err := util.GetCurrentCluster(util.Ctx.Hub, managementNamespace, name)
	if err != nil {
		return err
	}

	targetCluster, err := getTargetCluster(util.Ctx.Hub, currentCluster)
	if err != nil {
		return err
	}

	log.Infof("Failing over workload from cluster %q to cluster %q", currentCluster, targetCluster)

	return failoverRelocate(ctx, ramen.ActionFailover, ramen.FailedOver, currentCluster, targetCluster)
}

// Determine DRPC
// Check Placement
// Relocate to Primary in DRPolicy as the PrimaryCluster
// Update DRPC
func Relocate(ctx types.Context) error {
	managementNamespace := ctx.ManagementNamespace()
	log := ctx.Logger()
	name := ctx.Name()

	currentCluster, err := util.GetCurrentCluster(util.Ctx.Hub, managementNamespace, name)
	if err != nil {
		return err
	}

	targetCluster, err := getTargetCluster(util.Ctx.Hub, currentCluster)
	if err != nil {
		return err
	}

	log.Infof("Relocating workload from cluster %q to cluster %q", currentCluster, targetCluster)

	return failoverRelocate(ctx, ramen.ActionRelocate, ramen.Relocated, currentCluster, targetCluster)
}

func failoverRelocate(ctx types.Context,
	action ramen.DRAction,
	state ramen.DRState,
	currentCluster string,
	targetCluster string,
) error {
	d := ctx.Deployer()
	if d.IsDiscovered() {
		return failoverRelocateDiscoveredApps(ctx, action, state, currentCluster, targetCluster)
	}

	drpcName := ctx.Name()
	managementNamespace := ctx.ManagementNamespace()

	if err := waitAndUpdateDRPC(ctx, util.Ctx.Hub, managementNamespace, drpcName, action, targetCluster); err != nil {
		return err
	}

	if err := waitDRPCPhase(ctx, util.Ctx.Hub, managementNamespace, drpcName, state); err != nil {
		return err
	}

	return waitDRPCReady(ctx, util.Ctx.Hub, managementNamespace, drpcName)
}

func waitAndUpdateDRPC(
	ctx types.Context,
	cluster util.Cluster,
	namespace, drpcName string,
	action ramen.DRAction,
	targetCluster string,
) error {
	log := ctx.Logger()

	// here we expect drpc should be ready before action
	if err := waitDRPCReady(ctx, cluster, namespace, drpcName); err != nil {
		return err
	}

	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		drpc, err := getDRPC(cluster, namespace, drpcName)
		if err != nil {
			return err
		}

		drpc.Spec.Action = action
		if action == ramen.ActionFailover {
			drpc.Spec.FailoverCluster = targetCluster
		} else {
			drpc.Spec.PreferredCluster = targetCluster
		}

		if err := updateDRPC(cluster, drpc); err != nil {
			return err
		}

		log.Debugf("Updated drpc \"%s/%s\" with action %q to target cluster %q",
			namespace, drpcName, action, targetCluster)

		return nil
	})
}
