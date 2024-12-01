// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	"context"
	"fmt"
	"time"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func waitDRPCReady(ctx types.Context, client client.Client, namespace string, drpcName string) error {
	log := ctx.Logger()
	startTime := time.Now()

	log.Info("Waiting until drpc is ready")

	for {
		drpc, err := getDRPC(client, namespace, drpcName)
		if err != nil {
			return err
		}

		available := conditionMet(drpc.Status.Conditions, ramen.ConditionAvailable)
		peerReady := conditionMet(drpc.Status.Conditions, ramen.ConditionPeerReady)

		if available && peerReady && drpc.Status.LastGroupSyncTime != nil {
			log.Info("drpc is ready")

			return nil
		}

		if time.Since(startTime) > util.Timeout {
			return fmt.Errorf("timeout waiting for drpc to become ready (Available: %v, PeerReady: %v, lastGroupSyncTime: %v)",
				available, peerReady, drpc.Status.LastGroupSyncTime)
		}

		time.Sleep(util.RetryInterval)
	}
}

func conditionMet(conditions []metav1.Condition, conditionType string) bool {
	condition := meta.FindStatusCondition(conditions, conditionType)

	return condition != nil && condition.Status == "True"
}

func waitDRPCPhase(ctx types.Context, client client.Client, namespace, name string, phase ramen.DRState) error {
	log := ctx.Logger()
	startTime := time.Now()

	for {
		drpc, err := getDRPC(client, namespace, name)
		if err != nil {
			return err
		}

		currentPhase := drpc.Status.Phase
		if currentPhase == phase {
			log.Infof("drpc phase is %q", phase)

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
		return util.Ctx.C1.Client
	}

	return util.Ctx.C2.Client
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

func waitDRPCDeleted(ctx types.Context, client client.Client, namespace string, name string) error {
	log := ctx.Logger()
	startTime := time.Now()

	for {
		_, err := getDRPC(client, namespace, name)
		if err != nil {
			if errors.IsNotFound(err) {
				log.Info("drpc is deleted")

				return nil
			}

			log.Infof("Failed to get drpc: %s", err)
		}

		if time.Since(startTime) > util.Timeout {
			return fmt.Errorf("drpc %q is not deleted yet before timeout, fail", name)
		}

		time.Sleep(util.RetryInterval)
	}
}

// nolint:unparam
func waitDRPCProgression(
	ctx types.Context,
	client client.Client,
	namespace, name string,
	progression ramen.ProgressionStatus,
) error {
	log := ctx.Logger()
	startTime := time.Now()

	for {
		drpc, err := getDRPC(client, namespace, name)
		if err != nil {
			return err
		}

		currentProgression := drpc.Status.Progression
		if currentProgression == progression {
			log.Infof("drpc progression is %q", progression)

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
