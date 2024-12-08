// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	"strings"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/e2e/deployers"
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
	if _, isDiscoveredApps := d.(*deployers.DiscoveredApps); isDiscoveredApps {
		return EnableProtectionDiscoveredApps(ctx)
	}

	w := ctx.Workload()
	name := ctx.Name()
	namespace := ctx.Namespace()
	log := ctx.Logger()

	log.Info("Protecting workload")

	drPolicyName := util.DefaultDRPolicyName
	appname := w.GetAppName()
	placementName := name
	drpcName := name

	placementDecision, err := waitPlacementDecision(util.Ctx.Hub.Client, namespace, placementName)
	if err != nil {
		return err
	}

	clusterName := placementDecision.Status.Decisions[0].ClusterName
	log.Infof("Workload running on cluster %q", clusterName)

	log.Info("Annotating placement")

	err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		placement, err := getPlacement(util.Ctx.Hub.Client, namespace, placementName)
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

	drpc := generateDRPC(name, namespace, clusterName, drPolicyName, placementName, appname)
	if err = createDRPC(util.Ctx.Hub.Client, drpc); err != nil {
		return err
	}

	// this is the application namespace in drclusters to add the annotation
	nsToAnnotate := name
	if err := util.CreateNamespaceAndAddAnnotation(nsToAnnotate); err != nil {
		return err
	}

	return waitDRPCReady(ctx, util.Ctx.Hub.Client, namespace, drpcName)
}

// remove DRPC
// update placement annotation
func DisableProtection(ctx types.Context) error {
	d := ctx.Deployer()
	log := ctx.Logger()

	if _, isDiscoveredApps := d.(*deployers.DiscoveredApps); isDiscoveredApps {
		return DisableProtectionDiscoveredApps(ctx)
	}

	log.Info("Unprotecting workload")

	name := ctx.Name()
	namespace := ctx.Namespace()
	drpcName := name
	client := util.Ctx.Hub.Client

	log.Info("Deleting drpc")

	if err := deleteDRPC(client, namespace, drpcName); err != nil {
		return err
	}

	return waitDRPCDeleted(ctx, client, namespace, drpcName)
}

func Failover(ctx types.Context) error {
	d := ctx.Deployer()
	log := ctx.Logger()

	if _, isDiscoveredApps := d.(*deployers.DiscoveredApps); isDiscoveredApps {
		return FailoverDiscoveredApps(ctx)
	}

	log.Info("Failing over workload")

	return failoverRelocate(ctx, ramen.ActionFailover, ramen.FailedOver)
}

// Determine DRPC
// Check Placement
// Relocate to Primary in DRPolicy as the PrimaryCluster
// Update DRPC
func Relocate(ctx types.Context) error {
	d := ctx.Deployer()
	log := ctx.Logger()

	if _, isDiscoveredApps := d.(*deployers.DiscoveredApps); isDiscoveredApps {
		return RelocateDiscoveredApps(ctx)
	}

	log.Info("Relocating workload")

	return failoverRelocate(ctx, ramen.ActionRelocate, ramen.Relocated)
}

func failoverRelocate(ctx types.Context, action ramen.DRAction, state ramen.DRState) error {
	drpcName := ctx.Name()
	namespace := ctx.Namespace()
	client := util.Ctx.Hub.Client

	if err := waitAndUpdateDRPC(ctx, client, namespace, drpcName, action); err != nil {
		return err
	}

	if err := waitDRPCPhase(ctx, client, namespace, drpcName, state); err != nil {
		return err
	}

	return waitDRPCReady(ctx, client, namespace, drpcName)
}

func waitAndUpdateDRPC(
	ctx types.Context,
	client client.Client,
	namespace, drpcName string,
	action ramen.DRAction,
) error {
	log := ctx.Logger()

	// here we expect drpc should be ready before action
	if err := waitDRPCReady(ctx, client, namespace, drpcName); err != nil {
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
