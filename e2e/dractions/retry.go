// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	"fmt"
	"time"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/e2e/util"
	"k8s.io/apimachinery/pkg/api/errors"
	"open-cluster-management.io/api/cluster/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// return placement object, placementDecisionName, error
func waitPlacementDecision(client client.Client, namespace string, placementName string,
) (*v1beta1.Placement, string, error) {
	startTime := time.Now()
	placementDecisionName := ""

	for {
		placement, err := getPlacement(client, namespace, placementName)
		if err != nil {
			return nil, "", err
		}

		for _, cond := range placement.Status.Conditions {
			if cond.Type == "PlacementSatisfied" && cond.Status == "True" {
				placementDecisionName = placement.Status.DecisionGroups[0].Decisions[0]
				if placementDecisionName != "" {
					util.Ctx.Log.Info("got placementdecision name " + placementDecisionName)

					return placement, placementDecisionName, nil
				}
			}
		}

		if time.Since(startTime) > time.Second*time.Duration(util.Timeout) {
			return nil, "", fmt.Errorf("could not get placement decision before timeout")
		}

		util.Ctx.Log.Info(fmt.Sprintf("could not get placement decision, retry in %v seconds", util.TimeInterval))
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

		if conditionReady && drpc.Status.LastGroupSyncTime == nil {
			util.Ctx.Log.Info("drpc " + drpcName + " LastGroupSyncTime is nil")
		}

		if time.Since(startTime) > time.Second*time.Duration(util.Timeout) {
			return fmt.Errorf(fmt.Sprintf("drpc %s is not ready yet before timeout of %v", drpcName, util.Timeout))
		}

		util.Ctx.Log.Info(fmt.Sprintf("drpc %s is not ready yet, retry in %v seconds", drpcName, util.TimeInterval))
		time.Sleep(time.Second * time.Duration(util.TimeInterval))
	}
}

func checkDRPCConditions(drpc *ramen.DRPlacementControl) bool {
	available := false
	peerReady := false

	for _, cond := range drpc.Status.Conditions {
		if cond.Type == "Available" {
			if cond.Status != "True" {
				util.Ctx.Log.Info("drpc " + drpc.Name + " condition Available is not True")

				return false
			}

			available = true
		}

		if cond.Type == "PeerReady" {
			if cond.Status != "True" {
				util.Ctx.Log.Info("drpc " + drpc.Name + " condition PeerReady is not True")

				return false
			}

			peerReady = true
		}
	}

	return available && peerReady
}

func waitDRPCPhase(client client.Client, namespace string, name string, phase string) error {
	startTime := time.Now()

	for {
		drpc, err := getDRPC(client, namespace, name)
		if err != nil {
			return err
		}

		currentPhase := string(drpc.Status.Phase)
		if currentPhase == phase {
			util.Ctx.Log.Info("drpc " + name + " phase is " + phase)

			return nil
		}

		if time.Since(startTime) > time.Second*time.Duration(util.Timeout) {
			return fmt.Errorf(fmt.Sprintf("drpc %s status is not %s yet before timeout of %v", name, phase, util.Timeout))
		}

		util.Ctx.Log.Info(fmt.Sprintf("current drpc %s phase is %s, expecting %s, retry in %v seconds",
			name, currentPhase, phase, util.TimeInterval))
		time.Sleep(time.Second * time.Duration(util.TimeInterval))
	}
}

func getCurrentCluster(client client.Client, namespace string, placementName string) (string, error) {
	_, placementDecisionName, err := waitPlacementDecision(client, namespace, placementName)
	if err != nil {
		return "", err
	}

	placementDecision, err := getPlacementDecision(client, namespace, placementDecisionName)
	if err != nil {
		return "", err
	}

	clusterName := placementDecision.Status.Decisions[0].ClusterName
	util.Ctx.Log.Info("placementdecision clusterName: " + clusterName)

	return clusterName, nil
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
func waitDRPC(client client.Client, namespace, name, expectedPhase string) error {
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
			return fmt.Errorf(fmt.Sprintf("drpc %s is not deleted yet before timeout of %v", name, util.Timeout))
		}

		util.Ctx.Log.Info(fmt.Sprintf("drpc %s is not deleted yet, retry in %v seconds", name, util.TimeInterval))
		time.Sleep(time.Second * time.Duration(util.TimeInterval))
	}
}
