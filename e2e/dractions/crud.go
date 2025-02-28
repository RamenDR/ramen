// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	"context"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

func updatePlacement(client client.Client, placement *clusterv1beta1.Placement) error {
	return client.Update(context.Background(), placement)
}

func getDRPC(client client.Client, namespace, name string) (*ramen.DRPlacementControl, error) {
	drpc := &ramen.DRPlacementControl{}
	key := k8stypes.NamespacedName{Namespace: namespace, Name: name}

	err := client.Get(context.Background(), key, drpc)
	if err != nil {
		return nil, err
	}

	return drpc, nil
}

func createDRPC(ctx types.Context, client client.Client, drpc *ramen.DRPlacementControl) error {
	log := ctx.Logger()

	err := client.Create(context.Background(), drpc)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}

		log.Debugf("drpc \"%s/%s\" already exist", drpc.Namespace, drpc.Name)
	}

	spec, err := yaml.Marshal(drpc.Spec)
	if err != nil {
		return err
	}

	log.Debugf("Created drpc \"%s/%s\" with spec:\n%s", drpc.Namespace, drpc.Name, string(spec))

	return nil
}

func updateDRPC(client client.Client, drpc *ramen.DRPlacementControl) error {
	return client.Update(context.Background(), drpc)
}

func deleteDRPC(ctx types.Context, client client.Client, namespace, name string) error {
	log := ctx.Logger()

	objDrpc := &ramen.DRPlacementControl{}
	key := k8stypes.NamespacedName{Namespace: namespace, Name: name}

	err := client.Get(context.Background(), key, objDrpc)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}

		log.Debugf("drpc \"%s/%s\" not found", namespace, name)

		return nil
	}

	if err := client.Delete(context.Background(), objDrpc); err != nil {
		return err
	}

	log.Debugf("Deleted drpc \"%s/%s\"", namespace, name)

	return nil
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

func createPlacementManagedByRamen(ctx types.Context, name, namespace string) error {
	log := ctx.Logger()
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

	err := util.Ctx.Hub.Client.Create(context.Background(), placement)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}

		log.Debugf("Placement \"%s/%s\" already Exists", namespace, name)
	}

	log.Debugf("Created placement \"%s/%s\"", namespace, name)

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
