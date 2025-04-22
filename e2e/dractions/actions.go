// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"

	"github.com/ramendr/ramen/e2e/config"
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
// nolint:funlen
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

	clusterName, err := util.GetCurrentCluster(ctx, managementNamespace, placementName)
	if err != nil {
		return err
	}

	log.Infof("Protecting workload \"%s/%s\" in cluster %q", appNamespace, appname, clusterName)

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

	drpc := generateDRPC(name, managementNamespace, clusterName, drPolicyName, placementName, appname)
	if err = createDRPC(ctx, drpc); err != nil {
		return err
	}

	if err := createNamespaces(ctx, appNamespace); err != nil {
		return err
	}

	err = waitDRPCReady(ctx, managementNamespace, drpcName)
	if err != nil {
		return err
	}

	log.Info("Workload protected")

	return nil
}

// remove DRPC
// update placement annotation
func DisableProtection(ctx types.TestContext) error {
	d := ctx.Deployer()
	if d.IsDiscovered() {
		return DisableProtectionDiscoveredApps(ctx)
	}

	name := ctx.Name()
	managementNamespace := ctx.ManagementNamespace()
	appNamespace := ctx.AppNamespace()
	placementName := name
	log := ctx.Logger()

	clusterName, err := util.GetCurrentCluster(ctx, managementNamespace, placementName)
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

	drpcName := name

	if err := deleteDRPC(ctx, managementNamespace, drpcName); err != nil {
		return err
	}

	err = waitDRPCDeleted(ctx, managementNamespace, drpcName)
	if err != nil {
		return err
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

	targetCluster, err := getTargetCluster(ctx, ctx.Env().Hub, config.DRPolicy, currentCluster)
	if err != nil {
		return err
	}

	log.Infof("Failing over workload \"%s/%s\" from cluster %q to cluster %q",
		ctx.AppNamespace(), ctx.Workload().GetAppName(), currentCluster, targetCluster)

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

	targetCluster, err := getTargetCluster(ctx, ctx.Env().Hub, config.DRPolicy, currentCluster)
	if err != nil {
		return err
	}

	log.Infof("Relocating workload \"%s/%s\" from cluster %q to cluster %q",
		ctx.AppNamespace(), ctx.Workload().GetAppName(), currentCluster, targetCluster)

	err = failoverRelocate(ctx, ramen.ActionRelocate, ramen.Relocated, currentCluster, targetCluster)
	if err != nil {
		return err
	}

	log.Info("Workload relocated")

	return nil
}

func failoverRelocate(
	ctx types.TestContext,
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

	if err := waitAndUpdateDRPC(ctx, managementNamespace, drpcName, action, targetCluster); err != nil {
		return err
	}

	if err := waitDRPCPhase(ctx, managementNamespace, drpcName, state); err != nil {
		return err
	}

	return waitDRPCReady(ctx, managementNamespace, drpcName)
}

func waitAndUpdateDRPC(
	ctx types.TestContext,
	namespace, drpcName string,
	action ramen.DRAction,
	targetCluster string,
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
			drpc.Spec.FailoverCluster = targetCluster
		} else {
			drpc.Spec.PreferredCluster = targetCluster
		}

		if err := updateDRPC(ctx, drpc); err != nil {
			return err
		}

		log.Debugf("Updated drpc \"%s/%s\" with action %q to target cluster %q",
			namespace, drpcName, action, targetCluster)

		return nil
	})
}

// createNamespaces creates namespaces and annotations for managed app protection on
// both DR clusters for Volsync based replication if the distribution is Kubernetes.
// Returns an error if namespace creation or annotation fails.
func createNamespaces(ctx types.TestContext, appNamespace string) error {
	if ctx.Config().Distro == config.DistroK8s {
		return util.CreateNamespaceAndAddAnnotation(ctx, appNamespace)
	}

	return nil
}
