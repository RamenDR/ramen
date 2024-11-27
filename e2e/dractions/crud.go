// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	"context"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/util"
	recipe "github.com/ramendr/recipe/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func getPlacement(client client.Client, namespace, name string) (*clusterv1beta1.Placement, error) {
	placement := &clusterv1beta1.Placement{}
	key := types.NamespacedName{Namespace: namespace, Name: name}

	err := client.Get(context.Background(), key, placement)
	if err != nil {
		return nil, err
	}

	return placement, nil
}

func updatePlacement(client client.Client, placement *clusterv1beta1.Placement) error {
	return client.Update(context.Background(), placement)
}

func getDRPC(client client.Client, namespace, name string) (*ramen.DRPlacementControl, error) {
	drpc := &ramen.DRPlacementControl{}
	key := types.NamespacedName{Namespace: namespace, Name: name}

	err := client.Get(context.Background(), key, drpc)
	if err != nil {
		return nil, err
	}

	return drpc, nil
}

func createDRPC(client client.Client, drpc *ramen.DRPlacementControl) error {
	err := client.Create(context.Background(), drpc)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}
		// ctx.Log.Info("drpc " + drpc.Name + " already Exists")
	}

	return nil
}

func updateDRPC(client client.Client, drpc *ramen.DRPlacementControl) error {
	return client.Update(context.Background(), drpc)
}

func deleteDRPC(client client.Client, namespace, name string) error {
	objDrpc := &ramen.DRPlacementControl{}
	key := types.NamespacedName{Namespace: namespace, Name: name}

	err := client.Get(context.Background(), key, objDrpc)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}

		return nil
	}

	return client.Delete(context.Background(), objDrpc)
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

func createPlacementManagedByRamen(name, namespace string) error {
	labels := make(map[string]string)
	labels[deployers.AppLabelKey] = name
	clusterSet := []string{"default"}
	annotations := make(map[string]string)
	annotations[OcmSchedulingDisable] = "true"

	var numClusters int32 = 1
	placement := &clusterv1beta1.Placement{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: clusterv1beta1.PlacementSpec{
			ClusterSets:      clusterSet,
			NumberOfClusters: &numClusters,
		},
	}

	err := util.Ctx.Hub.CtrlClient.Create(context.Background(), placement)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}

		util.Ctx.Log.Info("placement " + placement.Name + " already Exists")
	}

	return nil
}

func generateDRPCDiscoveredApps(name, namespace, clusterName, drPolicyName, placementName,
	appname, protectedNamespace string,
) *ramen.DRPlacementControl {
	kubeObjectProtectionSpec := &ramen.KubeObjectProtectionSpec{
		KubeObjectSelector: &metav1.LabelSelector{
			MatchExpressions: []metav1.LabelSelectorRequirement{
				{
					Key:      "appname",
					Operator: metav1.LabelSelectorOpIn,
					Values:   []string{appname},
				},
			},
		},
	}
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
			ProtectedNamespaces:  &[]string{protectedNamespace},
			KubeObjectProtection: kubeObjectProtectionSpec,
		},
	}

	return drpc
}

func createRecipe(name, namespace string) error {
	err := util.Ctx.C1.CtrlClient.Create(context.Background(), getRecipe(name, namespace))
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}

		util.Ctx.Log.Info("recipe " + name + " already exists" + " in the cluster " + "C1")
	}

	err = util.Ctx.C2.CtrlClient.Create(context.Background(), getRecipe(name, namespace))
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}

		util.Ctx.Log.Info("recipe " + name + " already exists" + " in the cluster " + "C2")
	}

	return nil
}

func getRecipe(name, namespace string) *recipe.Recipe {
	return &recipe.Recipe{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Recipe",
			APIVersion: "ramendr.openshift.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: recipe.RecipeSpec{
			AppType: "busybox",
			Groups: []*recipe.Group{
				{
					Name: "rg1",
					Type: "resource",
					IncludedNamespaces: []string{
						namespace,
					},
				},
				{
					Name: "rg2",
					Type: "resource",
					IncludedNamespaces: []string{
						namespace,
					},
					// select resource for pod
				},
			},
			Hooks: []*recipe.Hook{
				{
					Name:           "backup",
					Type:           "check",
					Namespace:      name,
					NameSelector:   "busybox",
					SelectResource: "deployment",
					Timeout:        300,
					Chks: []*recipe.Check{
						{
							Name:      "check-replicas",
							Condition: "{$.spec.replicas} == {$.status.readyReplicas}",
						},
					},
				},
				{
					Name:           "restore",
					Type:           "check",
					Namespace:      name,
					NameSelector:   "busybox-recipe",
					SelectResource: "deployment",
					Timeout:        300,
					Chks: []*recipe.Check{
						{
							Name:      "check-replicas",
							Condition: "{$.spec.replicas} == {$.status.readyReplicas}",
						},
					},
				},
			},
			Workflows: []*recipe.Workflow{
				{
					Name: "backup",
					Sequence: []map[string]string{
						{
							"group": "rg2",
						},
						{
							"hook": "backup/check-replicas",
						},
						{
							"group": "rg1",
						},
					},
				},
				{
					Name: "restore",
					Sequence: []map[string]string{
						{
							"group": "rg1",
						},
						{
							"hook": "restore/check-replicas",
						},
					},
				},
			},
		},
	}
}

func deleteRecipe(client client.Client, name, namespace string) error {
	r := &recipe.Recipe{}
	key := types.NamespacedName{Namespace: namespace, Name: name}

	err := client.Get(context.Background(), key, r)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}

		return nil
	}

	return client.Delete(context.Background(), r)
}
