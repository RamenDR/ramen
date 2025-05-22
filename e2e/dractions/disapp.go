// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	"fmt"

	ramen "github.com/ramendr/ramen/api/v1alpha1"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/env"
	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
)

func EnableProtectionDiscoveredApps(ctx types.TestContext) error {
	w := ctx.Workload()
	name := ctx.Name()
	log := ctx.Logger()
	config := ctx.Config()
	managementNamespace := ctx.ManagementNamespace()
	appNamespace := ctx.AppNamespace()

	drPolicyName := config.DRPolicy
	appname := w.GetAppName()
	placementName := name
	drpcName := name

	// We assume that the application is on the first cluster (c1).
	// This is correct only for the current test flow.
	// TODO: must discover where the application is located instead.
	cluster := ctx.Env().C1

	// create mcsb default in ramen-ops ns
	if err := deployers.CreateManagedClusterSetBinding(ctx, config.ClusterSet, managementNamespace); err != nil {
		return err
	}

	// create placement
	if err := createPlacementManagedByRamen(ctx, placementName, managementNamespace); err != nil {
		return err
	}

	log.Infof("Protecting workload \"%s/%s\" in cluster %q", appNamespace, appname, cluster.Name)

	drpc := generateDRPCDiscoveredApps(
		name, managementNamespace, cluster.Name, drPolicyName, placementName, appname, appNamespace)
	if err := createDRPC(ctx, drpc); err != nil {
		return err
	}

	if err := createNamespacesDiscoveredApps(ctx, appNamespace); err != nil {
		return err
	}

	// wait for drpc ready
	err := waitDRPCReady(ctx, managementNamespace, drpcName)
	if err != nil {
		return err
	}

	log.Info("Workload protected")

	return nil
}

// remove DRPC
// update placement annotation
func DisableProtectionDiscoveredApps(ctx types.TestContext) error {
	name := ctx.Name()
	log := ctx.Logger()
	config := ctx.Config()
	managementNamespace := ctx.ManagementNamespace()
	appNamespace := ctx.AppNamespace()

	placementName := name
	drpcName := name

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

	if err := deleteDRPC(ctx, managementNamespace, drpcName); err != nil {
		return err
	}

	// delete placement
	if err := deployers.DeletePlacement(ctx, placementName, managementNamespace); err != nil {
		return err
	}

	err = deployers.DeleteManagedClusterSetBinding(ctx, config.ClusterSet, managementNamespace)
	if err != nil {
		return err
	}

	if err := util.WaitForDRPCDelete(ctx, ctx.Env().Hub, drpcName, managementNamespace); err != nil {
		return err
	}

	if err := util.WaitForPlacementDelete(ctx, ctx.Env().Hub, placementName, managementNamespace); err != nil {
		return err
	}

	if err := util.WaitForManagedClusterSetBindingDelete(ctx, ctx.Env().Hub, config.ClusterSet,
		managementNamespace); err != nil {
		return err
	}

	log.Info("Workload unprotected")

	return nil
}

// nolint:funlen,cyclop
func failoverRelocateDiscoveredApps(
	ctx types.TestContext,
	action ramen.DRAction,
	state ramen.DRState,
	currentClusterName string,
	targetClusterName string,
) error {
	name := ctx.Name()
	managementNamespace := ctx.ManagementNamespace()
	appNamespace := ctx.AppNamespace()

	drpcName := name

	if err := waitAndUpdateDRPC(ctx, managementNamespace, drpcName, action, targetClusterName); err != nil {
		return err
	}

	if err := waitDRPCProgression(ctx, managementNamespace, name,
		ramen.ProgressionWaitOnUserToCleanUp); err != nil {
		return err
	}

	currentCluster, err := env.GetCluster(ctx.Env(), currentClusterName)
	if err != nil {
		return err
	}

	// delete pvc and deployment from dr cluster
	if err := deployers.DeleteDiscoveredApps(ctx, currentCluster, appNamespace); err != nil {
		return err
	}

	if err := waitDRPCPhase(ctx, managementNamespace, name, state); err != nil {
		return err
	}

	if err := waitDRPCReady(ctx, managementNamespace, name); err != nil {
		return err
	}

	targetCluster, err := env.GetCluster(ctx.Env(), targetClusterName)
	if err != nil {
		return err
	}

	return deployers.WaitWorkloadHealth(ctx, targetCluster, appNamespace)
}

// createNamespacesDiscoveredApps creates namespaces and adds annotations for discovered app protection
// based on the Kubernetes distribution.
// For Kubernetes, creates namespaces and adds annotation on both DR clusters for volsync based replication.
// For OpenShift, creates namespace only on the target DR cluster (c2) before failover.
// Returns an error if the distribution is unknown, and if namespace creation or annotation fails.
func createNamespacesDiscoveredApps(ctx types.TestContext, namespace string) error {
	switch ctx.Config().Distro {
	case config.DistroK8s:
		return util.CreateNamespaceAndAddAnnotation(ctx, namespace)
	case config.DistroOcp:
		return util.CreateNamespace(ctx, ctx.Env().C2, namespace)
	default:
		return fmt.Errorf("unknown distro: %s", ctx.Config().Distro)
	}
}
