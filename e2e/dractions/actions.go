// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	"strings"
	"time"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/util"
	"github.com/ramendr/ramen/e2e/workloads"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	OcmSchedulingDisable = "cluster.open-cluster-management.io/experimental-scheduling-disable"

	FiveSecondsDuration = 5 * time.Second
)

// If AppSet/Subscription, find Placement
// Determine DRPolicy
// Determine preferredCluster
// Determine PVC label selector
// Determine KubeObjectProtection requirements if Imperative (?)
// Create DRPC, in desired namespace
// nolint:funlen
func EnableProtection(w workloads.Workload, d deployers.Deployer) error {
	if _, isDiscoveredApps := d.(*deployers.DiscoveredApps); isDiscoveredApps {
		return EnableProtectionDiscoveredApps(w, d)
	}

	name := GetCombinedName(d, w)
	namespace := GetNamespace(d, w)

	util.Ctx.Log.Info("enter EnableProtection " + name)

	drPolicyName := util.DefaultDRPolicyName
	appname := w.GetAppName()
	placementName := name
	drpcName := name

	placementDecisionName, err := waitPlacementDecision(util.Ctx.Hub.CtrlClient, namespace, placementName)
	if err != nil {
		return err
	}

	placementDecision, err := getPlacementDecision(util.Ctx.Hub.CtrlClient, namespace, placementDecisionName)
	if err != nil {
		return err
	}

	clusterName := placementDecision.Status.Decisions[0].ClusterName
	util.Ctx.Log.Info("got clusterName " + clusterName + " from " + placementDecisionName)

	util.Ctx.Log.Info("update placement " + placementName + " annotation")

	err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		placement, err := getPlacement(util.Ctx.Hub.CtrlClient, namespace, placementName)
		if err != nil {
			return err
		}

		if placement.Annotations == nil {
			placement.Annotations = make(map[string]string)
		}
		placement.Annotations[OcmSchedulingDisable] = "true"

		return updatePlacement(util.Ctx.Hub.CtrlClient, placement)
	})
	if err != nil {
		return err
	}

	util.Ctx.Log.Info("create drpc " + drpcName)

	drpc := generateDRPC(name, namespace, clusterName, drPolicyName, placementName, appname)
	if err = createDRPC(util.Ctx.Hub.CtrlClient, drpc); err != nil {
		return err
	}

	// this is the application namespace in drclusters to add the annotation
	nsToAnnonate := name
	if err := util.CreateNamespaceAndAddAnnotation(nsToAnnonate); err != nil {
		return err
	}

	return waitDRPCReady(util.Ctx.Hub.CtrlClient, namespace, drpcName)
}

// remove DRPC
// update placement annotation
func DisableProtection(w workloads.Workload, d deployers.Deployer) error {
	if _, isDiscoveredApps := d.(*deployers.DiscoveredApps); isDiscoveredApps {
		return DisableProtectionDiscoveredApps(w, d)
	}

	name := GetCombinedName(d, w)
	namespace := GetNamespace(d, w)

	util.Ctx.Log.Info("enter DisableProtection " + name)

	drpcName := name
	client := util.Ctx.Hub.CtrlClient

	util.Ctx.Log.Info("delete drpc " + drpcName)

	if err := deleteDRPC(client, namespace, drpcName); err != nil {
		return err
	}

	return waitDRPCDeleted(client, namespace, drpcName)
}

func Failover(w workloads.Workload, d deployers.Deployer) error {
	name := GetCombinedName(d, w)
	util.Ctx.Log.Info("enter Failover " + name)

	if _, isDiscoveredApps := d.(*deployers.DiscoveredApps); isDiscoveredApps {
		return FailoverDiscoveredApps(w, d)
	}

	return failoverRelocate(w, d, ramen.ActionFailover)
}

// Determine DRPC
// Check Placement
// Relocate to Primary in DRPolicy as the PrimaryCluster
// Update DRPC
func Relocate(w workloads.Workload, d deployers.Deployer) error {
	name := GetCombinedName(d, w)
	util.Ctx.Log.Info("enter Relocate " + name)

	if _, isDiscoveredApps := d.(*deployers.DiscoveredApps); isDiscoveredApps {
		return RelocateDiscoveredApps(w, d)
	}

	return failoverRelocate(w, d, ramen.ActionRelocate)
}

func failoverRelocate(w workloads.Workload, d deployers.Deployer, action ramen.DRAction) error {
	name := GetCombinedName(d, w)
	namespace := GetNamespace(d, w)

	drpcName := name
	client := util.Ctx.Hub.CtrlClient

	if err := waitAndUpdateDRPC(client, namespace, drpcName, action); err != nil {
		return err
	}

	if action == ramen.ActionFailover {
		return waitDRPC(client, namespace, name, ramen.FailedOver)
	}

	return waitDRPC(client, namespace, name, ramen.Relocated)
}

func waitAndUpdateDRPC(client client.Client, namespace, drpcName string, action ramen.DRAction) error {
	// here we expect drpc should be ready before action
	if err := waitDRPCReady(client, namespace, drpcName); err != nil {
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

	util.Ctx.Log.Info("update drpc " + drpcName + " " + strings.ToLower(string(action)) + " to " + targetCluster)

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

func GetNamespace(d deployers.Deployer, w workloads.Workload) string {
	return deployers.GetNamespace(d, w)
}

func GetCombinedName(d deployers.Deployer, w workloads.Workload) string {
	return deployers.GetCombinedName(d, w)
}
