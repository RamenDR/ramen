// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	"context"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func getPlacement(ctrlClient client.Client, namespace, name string) (*clusterv1beta1.Placement, error) {
	placement := &clusterv1beta1.Placement{}
	key := types.NamespacedName{Namespace: namespace, Name: name}

	err := ctrlClient.Get(context.Background(), key, placement)
	if err != nil {
		return nil, err
	}

	return placement, nil
}

func updatePlacement(ctrlClient client.Client, placement *clusterv1beta1.Placement) error {
	err := ctrlClient.Update(context.Background(), placement)
	if err != nil {
		return err
	}

	return nil
}

func getPlacementDecision(ctrlClient client.Client, namespace, name string) (*clusterv1beta1.PlacementDecision, error) {
	placementDecision := &clusterv1beta1.PlacementDecision{}
	key := types.NamespacedName{Namespace: namespace, Name: name}

	err := ctrlClient.Get(context.Background(), key, placementDecision)
	if err != nil {
		return nil, err
	}

	return placementDecision, nil
}

func getDRPC(ctrlClient client.Client, namespace, name string) (*ramen.DRPlacementControl, error) {
	drpc := &ramen.DRPlacementControl{}
	key := types.NamespacedName{Namespace: namespace, Name: name}

	err := ctrlClient.Get(context.Background(), key, drpc)
	if err != nil {
		return nil, err
	}

	return drpc, nil
}

func createDRPC(ctrlClient client.Client, drpc *ramen.DRPlacementControl) error {
	err := ctrlClient.Create(context.Background(), drpc)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}
		// ctx.Log.Info("drpc " + drpc.Name + " already Exists")
	}

	return nil
}

func updateDRPC(ctrlClient client.Client, drpc *ramen.DRPlacementControl) error {
	err := ctrlClient.Update(context.Background(), drpc)
	if err != nil {
		return err
	}

	return nil
}

func deleteDRPC(ctrlClient client.Client, namespace, name string) error {
	objDrpc := &ramen.DRPlacementControl{}
	key := types.NamespacedName{Namespace: namespace, Name: name}

	err := ctrlClient.Get(context.Background(), key, objDrpc)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}

		return nil
	}

	err = ctrlClient.Delete(context.Background(), objDrpc)
	if err != nil {
		return err
	}

	return nil
}

func getDRPolicy(ctrlClient client.Client, name string) (*ramen.DRPolicy, error) {
	drpolicy := &ramen.DRPolicy{}
	key := types.NamespacedName{Name: name}

	err := ctrlClient.Get(context.Background(), key, drpolicy)
	if err != nil {
		return nil, err
	}

	return drpolicy, nil
}

func generateDRPC(name, namespace, clusterName, drPolicyName, placementName, appname string) *ramen.DRPlacementControl {
	drpc := &ramen.DRPlacementControl{
		TypeMeta: metav1.TypeMeta{
			Kind:       "DRPlacementControl",
			APIVersion: "ramendr.openshift.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"app": name},
		},
		Spec: ramen.DRPlacementControlSpec{
			PreferredCluster: clusterName,
			DRPolicyRef: v1.ObjectReference{
				Name: drPolicyName,
			},
			PlacementRef: v1.ObjectReference{
				Kind: "placement",
				Name: placementName,
			},
			PVCSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"appname": appname},
			},
		},
	}

	return drpc
}
