// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	"context"
	"fmt"
	"time"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/e2e/util"
	"k8s.io/apimachinery/pkg/api/errors"
	"open-cluster-management.io/api/cluster/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// nolint:gocognit
// return placementDecisionName, error
func waitPlacementDecision(client client.Client, namespace string, placementName string,
) (string, error) {
	startTime := time.Now()
	placementDecisionName := ""

	for {
		placement, err := getPlacement(client, namespace, placementName)
		if err != nil {
			return "", err
		}

		for _, cond := range placement.Status.Conditions {
			if cond.Type == "PlacementSatisfied" && cond.Status == "True" {
				placementDecisionName = placement.Status.DecisionGroups[0].Decisions[0]
				if placementDecisionName != "" {
					return placementDecisionName, nil
				}
			}
		}

		// if placement is controlled by ramen, it will not have placementdecision name in its status
		// so need query placementdecision by label
		placementDecision, err := getPlacementDecisionFromPlacement(client, placement)
		if err == nil && placementDecision != nil {
			return placementDecision.Name, nil
		}

		if time.Since(startTime) > time.Second*time.Duration(util.Timeout) {
			return "", fmt.Errorf(
				"could not get placement decision for " + placementName + " before timeout, fail")
		}

		time.Sleep(time.Second * time.Duration(util.TimeInterval))
	}
}

func waitDRPCReady(client client.Client, namespace string, drpcName string) error {
	startTime := time.Now()

	for {
		drpc, err := getDRPC(client, namespace, drpcName)
		if err != nil {
			return err
		}

		conditionReady := checkDRPCConditions(drpc)
		if conditionReady && drpc.Status.LastGroupSyncTime != nil {
			util.Ctx.Log.Info("drpc " + drpcName + " is ready")

			return nil
		}

		if time.Since(startTime) > time.Second*time.Duration(util.Timeout) {
			if !conditionReady {
				util.Ctx.Log.Info("drpc " + drpcName + " condition 'Available' or 'PeerReady' is not True")
			}

			if conditionReady && drpc.Status.LastGroupSyncTime == nil {
				util.Ctx.Log.Info("drpc " + drpcName + " LastGroupSyncTime is nil")
			}

			return fmt.Errorf("drpc " + drpcName + " is not ready yet before timeout, fail")
		}

		time.Sleep(time.Second * time.Duration(util.TimeInterval))
	}
}

func checkDRPCConditions(drpc *ramen.DRPlacementControl) bool {
	available := false
	peerReady := false

	for _, cond := range drpc.Status.Conditions {
		if cond.Type == "Available" {
			if cond.Status != "True" {
				return false
			}

			available = true
		}

		if cond.Type == "PeerReady" {
			if cond.Status != "True" {
				return false
			}

			peerReady = true
		}
	}

	return available && peerReady
}

func waitDRPCPhase(client client.Client, namespace, name string, phase ramen.DRState) error {
	startTime := time.Now()

	for {
		drpc, err := getDRPC(client, namespace, name)
		if err != nil {
			return err
		}

		currentPhase := drpc.Status.Phase
		if currentPhase == phase {
			util.Ctx.Log.Info(fmt.Sprintf("drpc %s phase is %s", name, phase))

			return nil
		}

		if time.Since(startTime) > time.Second*time.Duration(util.Timeout) {
			return fmt.Errorf(fmt.Sprintf(
				"drpc %s status is not %s yet before timeout, fail", name, phase))
		}

		time.Sleep(time.Second * time.Duration(util.TimeInterval))
	}
}

func getCurrentCluster(client client.Client, namespace string, placementName string) (string, error) {
	placementDecisionName, err := waitPlacementDecision(client, namespace, placementName)
	if err != nil {
		return "", err
	}

	placementDecision, err := getPlacementDecision(client, namespace, placementDecisionName)
	if err != nil {
		return "", err
	}

	clusterName := placementDecision.Status.Decisions[0].ClusterName

	return clusterName, nil
}

// return dr cluster client
func getDRClusterClient(clusterName string, drpolicy *ramen.DRPolicy) client.Client {
	if clusterName == drpolicy.Spec.DRClusters[0] {
		return util.Ctx.C1.CtrlClient
	}

	return util.Ctx.C2.CtrlClient
}

func getTargetCluster(client client.Client, namespace, placementName string, drpolicy *ramen.DRPolicy) (string, error) {
	currentCluster, err := getCurrentCluster(client, namespace, placementName)
	if err != nil {
		return "", err
	}

	targetCluster := ""
	if currentCluster == drpolicy.Spec.DRClusters[0] {
		targetCluster = drpolicy.Spec.DRClusters[1]
	} else {
		targetCluster = drpolicy.Spec.DRClusters[0]
	}

	return targetCluster, nil
}

// first wait DRPC to have the expected phase, then check DRPC conditions
func waitDRPC(client client.Client, namespace, name string, expectedPhase ramen.DRState) error {
	// sleep to wait for DRPC is processed
	time.Sleep(FiveSecondsDuration)
	// check Phase
	if err := waitDRPCPhase(client, namespace, name, expectedPhase); err != nil {
		return err
	}
	// then check Conditions
	return waitDRPCReady(client, namespace, name)
}

func waitDRPCDeleted(client client.Client, namespace string, name string) error {
	startTime := time.Now()
	// sleep to wait for DRPC is deleted
	time.Sleep(FiveSecondsDuration)

	for {
		_, err := getDRPC(client, namespace, name)
		if err != nil {
			if errors.IsNotFound(err) {
				util.Ctx.Log.Info("drpc " + name + " is deleted")

				return nil
			}

			util.Ctx.Log.Info(fmt.Sprintf("error to get drpc %s: %v", name, err))
		}

		if time.Since(startTime) > time.Second*time.Duration(util.Timeout) {
			return fmt.Errorf(fmt.Sprintf("drpc %s is not deleted yet before timeout, fail", name))
		}

		time.Sleep(time.Second * time.Duration(util.TimeInterval))
	}
}

// nolint:unparam
func waitDRPCProgression(client client.Client, namespace, name string, progression ramen.ProgressionStatus) error {
	startTime := time.Now()

	for {
		drpc, err := getDRPC(client, namespace, name)
		if err != nil {
			return err
		}

		currentProgression := drpc.Status.Progression
		if currentProgression == progression {
			util.Ctx.Log.Info(fmt.Sprintf("drpc %s progression is %s", name, progression))

			return nil
		}

		if time.Since(startTime) > time.Second*time.Duration(util.Timeout) {
			return fmt.Errorf(fmt.Sprintf("drpc %s progression is not %s yet before timeout of %v",
				name, progression, util.Timeout))
		}

		time.Sleep(time.Second * time.Duration(util.TimeInterval))
	}
}

func getPlacementDecisionFromPlacement(ctrlClient client.Client, placement *v1beta1.Placement,
) (*v1beta1.PlacementDecision, error) {
	matchLabels := map[string]string{
		v1beta1.PlacementLabel: placement.GetName(),
	}

	listOptions := []client.ListOption{
		client.InNamespace(placement.GetNamespace()),
		client.MatchingLabels(matchLabels),
	}

	plDecisions := &v1beta1.PlacementDecisionList{}
	if err := ctrlClient.List(context.Background(), plDecisions, listOptions...); err != nil {
		return nil, fmt.Errorf("failed to list PlacementDecisions (placement: %s)",
			placement.GetNamespace()+"/"+placement.GetName())
	}

	if len(plDecisions.Items) == 0 {
		return nil, nil
	}

	if len(plDecisions.Items) > 1 {
		return nil, fmt.Errorf("multiple PlacementDecisions found for Placement (count: %d, placement: %s)",
			len(plDecisions.Items), placement.GetNamespace()+"/"+placement.GetName())
	}

	plDecision := plDecisions.Items[0]
	// r.Log.Info("Found ClusterDecision", "ClsDedicision", plDecision.Status.Decisions)

	if len(plDecision.Status.Decisions) > 1 {
		return nil, fmt.Errorf("multiple placements found in PlacementDecision"+
			" (count: %d, Placement: %s, PlacementDecision: %s)",
			len(plDecision.Status.Decisions),
			placement.GetNamespace()+"/"+placement.GetName(),
			plDecision.GetName()+"/"+plDecision.GetNamespace())
	}

	return &plDecision, nil
}
