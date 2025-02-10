// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	"strings"

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
	log := ctx.Logger()

	log.Infof("Protecting workload in namespace %q", managementNamespace)

	drPolicyName := util.DefaultDRPolicyName
	appname := w.GetAppName()
	placementName := name
	drpcName := name

	placementDecision, err := waitPlacementDecision(util.Ctx.Hub.Client, managementNamespace, placementName)
	if err != nil {
		return err
	}

	clusterName := placementDecision.Status.Decisions[0].ClusterName
	log.Infof("Workload running on cluster %q", clusterName)

	log.Info("Annotating placement")

	err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		placement, err := getPlacement(util.Ctx.Hub.Client, managementNamespace, placementName)
		if err != nil {
			return err
		}

		if placement.Annotations == nil {
			placement.Annotations = make(map[string]string)
		}

		placement.Annotations[OcmSchedulingDisable] = "true"

		return updatePlacement(util.Ctx.Hub.Client, placement)
	})
	if err != nil {
		return err
	}

	log.Info("Creating drpc")

	drpc := generateDRPC(name, managementNamespace, clusterName, drPolicyName, placementName, appname)
	if err = createDRPC(util.Ctx.Hub.Client, drpc); err != nil {
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
	log := ctx.Logger()

	log.Infof("Unprotecting workload in namespace %q", managementNamespace)

	drpcName := name
	client := util.Ctx.Hub.Client

	log.Info("Deleting drpc")

	if err := deleteDRPC(client, managementNamespace, drpcName); err != nil {
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

	log.Info("Updating drpc " + strings.ToLower(string(action)) + " to " + targetCluster)

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

		return updateDRPC(client, drpc)
	})
}
