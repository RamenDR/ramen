// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	"time"

	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/util"
	"github.com/ramendr/ramen/e2e/workloads"
)

const (
	OcmSchedulingDisable = "cluster.open-cluster-management.io/experimental-scheduling-disable"
	DefaultDRPolicyName  = "dr-policy"
	FiveSecondsDuration  = 5 * time.Second
)

// If AppSet/Subscription, find Placement
// Determine DRPolicy
// Determine preferredCluster
// Determine PVC label selector
// Determine KubeObjectProtection requirements if Imperative (?)
// Create DRPC, in desired namespace
// nolint:funlen
func EnableProtection(w workloads.Workload, d deployers.Deployer) error {
	name := GetCombinedName(d, w)
	namespace := GetNamespace(d, w)

	util.Ctx.Log.Info("enter EnableProtection " + name)

	drPolicyName := DefaultDRPolicyName
	appname := w.GetAppName()
	placementName := name
	drpcName := name

	placement, placementDecisionName, err := waitPlacementDecision(util.Ctx.Hub.CtrlClient, namespace, placementName)
	if err != nil {
		return err
	}

	placementDecision, err := getPlacementDecision(util.Ctx.Hub.CtrlClient, namespace, placementDecisionName)
	if err != nil {
		return err
	}

	clusterName := placementDecision.Status.Decisions[0].ClusterName
	util.Ctx.Log.Info("got clusterName " + clusterName + " from " + placementDecisionName)

	// move update placement annotation after placement has been handled
	// otherwise if we first add ocm disable annotation then it might not
	// yet be handled by ocm and thus PlacementSatisfied=false
	if placement.Annotations == nil {
		placement.Annotations = make(map[string]string)
	}

	placement.Annotations[OcmSchedulingDisable] = "true"

	util.Ctx.Log.Info("update placement " + placementName + " annotation")

	if err = updatePlacement(util.Ctx.Hub.CtrlClient, placement); err != nil {
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
	namespace := GetNamespace(d, w)

	util.Ctx.Log.Info("enter Failover " + name)

	drPolicyName := DefaultDRPolicyName
	drpcName := name
	client := util.Ctx.Hub.CtrlClient

	// here we expect drpc should be ready before failover
	if err := waitDRPCReady(client, namespace, drpcName); err != nil {
		return err
	}

	util.Ctx.Log.Info("get drpc " + drpcName)

	drpc, err := getDRPC(client, namespace, drpcName)
	if err != nil {
		return err
	}

	util.Ctx.Log.Info("get drpolicy " + drPolicyName + " for " + drpcName)

	drpolicy, err := getDRPolicy(client, drPolicyName)
	if err != nil {
		return err
	}

	targetCluster, err := getTargetCluster(client, namespace, name, drpolicy)
	if err != nil {
		return err
	}

	drpc.Spec.Action = "Failover"
	drpc.Spec.FailoverCluster = targetCluster

	util.Ctx.Log.Info("update drpc " + drpcName + " failover to " + targetCluster)

	if err = updateDRPC(client, drpc); err != nil {
		return err
	}

	return waitDRPC(client, namespace, name, "FailedOver")
}

// Determine DRPC
// Check Placement
// Relocate to Primary in DRPolicy as the PrimaryCluster
// Update DRPC
func Relocate(w workloads.Workload, d deployers.Deployer) error {
	name := GetCombinedName(d, w)
	namespace := GetNamespace(d, w)

	util.Ctx.Log.Info("enter Relocate " + name)

	drPolicyName := DefaultDRPolicyName
	drpcName := name
	client := util.Ctx.Hub.CtrlClient

	// here we expect drpc should be ready before relocate
	err := waitDRPCReady(client, namespace, drpcName)
	if err != nil {
		return err
	}

	util.Ctx.Log.Info("get drpc " + drpcName)

	drpc, err := getDRPC(client, namespace, drpcName)
	if err != nil {
		return err
	}

	util.Ctx.Log.Info("get drpolicy " + drPolicyName + " for " + drpcName)

	drpolicy, err := getDRPolicy(client, drPolicyName)
	if err != nil {
		return err
	}

	targetCluster, err := getTargetCluster(client, namespace, name, drpolicy)
	if err != nil {
		return err
	}

	drpc.Spec.Action = "Relocate"
	drpc.Spec.PreferredCluster = targetCluster

	util.Ctx.Log.Info("update drpc " + drpcName + " relocate to " + targetCluster)

	err = updateDRPC(client, drpc)
	if err != nil {
		return err
	}

	return waitDRPC(client, namespace, name, "Relocated")
}

func GetCombinedName(d deployers.Deployer, w workloads.Workload) string {
	return deployers.GetCombinedName(d, w)
}

func GetNamespace(d deployers.Deployer, w workloads.Workload) string {
	_, isAppSet := d.(*deployers.ApplicationSet)
	if isAppSet {
		// appset need be deployed in argocd ns
		return util.ArgocdNamespace
	}

	return GetCombinedName(d, w)
}
