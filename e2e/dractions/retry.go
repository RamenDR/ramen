// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/e2e/util"
	"k8s.io/apimachinery/pkg/api/errors"
	"open-cluster-management.io/api/cluster/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// waitPlacementDecision waits until we have a placement decision and returns the placement decision object.
func waitPlacementDecision(client client.Client, namespace string, placementName string,
) (*v1beta1.PlacementDecision, error) {
	startTime := time.Now()

	for {
		placement, err := getPlacement(client, namespace, placementName)
		if err != nil {
			return nil, err
		}

		placementDecision, err := getPlacementDecisionFromPlacement(client, placement)
		if err != nil {
			return nil, err
		}

		if placementDecision != nil && len(placementDecision.Status.Decisions) > 0 {
			return placementDecision, nil
		}

		if time.Since(startTime) > util.Timeout {
			return nil, fmt.Errorf("timeout waiting for placement decisions for %q ", placementName)
		}

		time.Sleep(util.RetryInterval)
	}
}

func waitDRPCReady(client client.Client, namespace string, drpcName string, log logr.Logger) error {
	startTime := time.Now()

	for {
		drpc, err := getDRPC(client, namespace, drpcName)
		if err != nil {
			return err
		}

		conditionReady := checkDRPCConditions(drpc)
		if conditionReady && drpc.Status.LastGroupSyncTime != nil {
			log.Info("drpc is ready")

			return nil
		}

		if time.Since(startTime) > util.Timeout {
			if !conditionReady {
				log.Info("drpc condition 'Available' or 'PeerReady' is not True")
			}

			if conditionReady && drpc.Status.LastGroupSyncTime == nil {
				log.Info("drpc LastGroupSyncTime is nil")
			}

			return fmt.Errorf("drpc %q is not ready yet before timeout, fail", drpcName)
		}

		time.Sleep(util.RetryInterval)
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

func waitDRPCPhase(client client.Client, namespace, name string, phase ramen.DRState, log logr.Logger) error {
	startTime := time.Now()

	for {
		drpc, err := getDRPC(client, namespace, name)
		if err != nil {
			return err
		}

		currentPhase := drpc.Status.Phase
		if currentPhase == phase {
			log.Info(fmt.Sprintf("drpc phase is %s", phase))

			return nil
		}

		if time.Since(startTime) > util.Timeout {
			return fmt.Errorf("drpc %q status is not %q yet before timeout, fail", name, phase)
		}

		time.Sleep(util.RetryInterval)
	}
}

func getCurrentCluster(client client.Client, namespace string, placementName string) (string, error) {
	placementDecision, err := waitPlacementDecision(client, namespace, placementName)
	if err != nil {
		return "", err
	}

	return placementDecision.Status.Decisions[0].ClusterName, nil
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

	var targetCluster string
	if currentCluster == drpolicy.Spec.DRClusters[0] {
		targetCluster = drpolicy.Spec.DRClusters[1]
	} else {
		targetCluster = drpolicy.Spec.DRClusters[0]
	}

	return targetCluster, nil
}

// first wait DRPC to have the expected phase, then check DRPC conditions
func waitDRPC(client client.Client, namespace, name string, expectedPhase ramen.DRState, log logr.Logger) error {
	// check Phase
	if err := waitDRPCPhase(client, namespace, name, expectedPhase, log); err != nil {
		return err
	}
	// then check Conditions
	return waitDRPCReady(client, namespace, name, log)
}

func waitDRPCDeleted(client client.Client, namespace string, name string, log logr.Logger) error {
	startTime := time.Now()

	for {
		_, err := getDRPC(client, namespace, name)
		if err != nil {
			if errors.IsNotFound(err) {
				log.Info("drpc is deleted")

				return nil
			}

			log.Info(fmt.Sprintf("failed to get drpc: %v", err))
		}

		if time.Since(startTime) > util.Timeout {
			return fmt.Errorf("drpc %q is not deleted yet before timeout, fail", name)
		}

		time.Sleep(util.RetryInterval)
	}
}

// nolint:unparam
func waitDRPCProgression(client client.Client, namespace, name string, progression ramen.ProgressionStatus,
	log logr.Logger,
) error {
	startTime := time.Now()

	for {
		drpc, err := getDRPC(client, namespace, name)
		if err != nil {
			return err
		}

		currentProgression := drpc.Status.Progression
		if currentProgression == progression {
			log.Info(fmt.Sprintf("drpc progression is %s", progression))

			return nil
		}

		if time.Since(startTime) > util.Timeout {
			return fmt.Errorf("drpc %q progression is not %q yet before timeout of %v",
				name, progression, util.Timeout)
		}

		time.Sleep(util.RetryInterval)
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
