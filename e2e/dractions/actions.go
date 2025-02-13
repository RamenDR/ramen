// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

	placementDecision, err := waitPlacementDecision(util.Ctx.Hub.Client, managementNamespace, placementName)
	if err != nil {
		return err
	}

	clusterName := placementDecision.Status.Decisions[0].ClusterName
	log.Debugf("Workload running on cluster %q", clusterName)

	err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		placement, err := getPlacement(util.Ctx.Hub.Client, managementNamespace, placementName)
		if err != nil {
			return err
		}

		if placement.Annotations == nil {
			placement.Annotations = make(map[string]string)
		}

		placement.Annotations[OcmSchedulingDisable] = "true"

		if err := updatePlacement(util.Ctx.Hub.Client, placement); err != nil {
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
	if err = createDRPC(ctx, util.Ctx.Hub.Client, drpc); err != nil {
		return err
	}

	// For volsync based replication we must create the cluster namespaces with special annotation.
	if err := util.CreateNamespaceAndAddAnnotation(ctx.AppNamespace()); err != nil {
		return err
	}

	return waitDRPCReady(ctx, util.Ctx.Hub.Client, managementNamespace, drpcName)
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
	client := util.Ctx.Hub.Client

	if err := deleteDRPC(ctx, client, managementNamespace, drpcName); err != nil {
		return err
	}

	return waitDRPCDeleted(ctx, client, managementNamespace, drpcName)
}

func Failover(ctx types.Context) error {
	managementNamespace := ctx.ManagementNamespace()
	log := ctx.Logger()
	name := ctx.Name()

	drpcName := name
	client := util.Ctx.Hub.Client
	drPolicyName := util.DefaultDRPolicyName

	currentCluster, err := getCurrentCluster(client, managementNamespace, name)
	if err != nil {
		return err
	}

	drpolicy, err := util.GetDRPolicy(client, drPolicyName)
	if err != nil {
		return err
	}

	targetCluster, err := getTargetCluster(client, managementNamespace, drpcName, drpolicy)
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

	drpcName := name
	client := util.Ctx.Hub.Client
	drPolicyName := util.DefaultDRPolicyName

	currentCluster, err := getCurrentCluster(client, managementNamespace, name)
	if err != nil {
		return err
	}

	drpolicy, err := util.GetDRPolicy(client, drPolicyName)
	if err != nil {
		return err
	}

	targetCluster, err := getTargetCluster(client, managementNamespace, drpcName, drpolicy)
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
	client := util.Ctx.Hub.Client

	if err := waitAndUpdateDRPC(ctx, client, managementNamespace, drpcName, action, targetCluster); err != nil {
		return err
	}

	if err := waitDRPCPhase(ctx, client, managementNamespace, drpcName, state); err != nil {
		return err
	}

	return waitDRPCReady(ctx, client, managementNamespace, drpcName)
}

func waitAndUpdateDRPC(
	ctx types.Context,
	client client.Client,
	namespace, drpcName string,
	action ramen.DRAction,
	targetCluster string,
) error {
	log := ctx.Logger()

	// here we expect drpc should be ready before action
	if err := waitDRPCReady(ctx, client, namespace, drpcName); err != nil {
		return err
	}

	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		drpc, err := getDRPC(client, namespace, drpcName)
		if err != nil {
			return err
		}

		drpc.Spec.Action = action
		if action == ramen.ActionFailover {
			drpc.Spec.FailoverCluster = targetCluster
		} else {
			drpc.Spec.PreferredCluster = targetCluster
		}

		if err := updateDRPC(client, drpc); err != nil {
			return err
		}

		log.Debugf("Updated drpc \"%s/%s\" with action %q to target cluster %q",
			namespace, drpcName, action, targetCluster)

		return nil
	})
}
