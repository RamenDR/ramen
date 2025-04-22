// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	"sigs.k8s.io/yaml"

	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/types"
)

func updatePlacement(ctx types.TestContext, placement *clusterv1beta1.Placement) error {
	hub := ctx.Env().Hub

	return hub.Client.Update(ctx.Context(), placement)
}

func getDRPC(ctx types.TestContext, namespace, name string) (*ramen.DRPlacementControl, error) {
	hub := ctx.Env().Hub

	drpc := &ramen.DRPlacementControl{}
	key := k8stypes.NamespacedName{Namespace: namespace, Name: name}

	err := hub.Client.Get(ctx.Context(), key, drpc)
	if err != nil {
		return nil, err
	}

	return drpc, nil
}

func createDRPC(ctx types.TestContext, drpc *ramen.DRPlacementControl) error {
	log := ctx.Logger()
	hub := ctx.Env().Hub

	err := hub.Client.Create(ctx.Context(), drpc)
	if err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return err
		}

		log.Debugf("drpc \"%s/%s\" already exist in cluster %q", drpc.Namespace, drpc.Name, hub.Name)
	}

	spec, err := yaml.Marshal(drpc.Spec)
	if err != nil {
		return err
	}

	log.Debugf("Created drpc \"%s/%s\" in cluster %q with spec:\n%s",
		drpc.Namespace, drpc.Name, hub.Name, string(spec))

	return nil
}

func updateDRPC(ctx types.TestContext, drpc *ramen.DRPlacementControl) error {
	hub := ctx.Env().Hub

	return hub.Client.Update(ctx.Context(), drpc)
}

func deleteDRPC(ctx types.TestContext, namespace, name string) error {
	log := ctx.Logger()
	hub := ctx.Env().Hub

	objDrpc := &ramen.DRPlacementControl{}
	key := k8stypes.NamespacedName{Namespace: namespace, Name: name}

	err := hub.Client.Get(ctx.Context(), key, objDrpc)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}

		log.Debugf("drpc \"%s/%s\" not found in cluster %q", namespace, name, hub.Name)

		return nil
	}

	if err := hub.Client.Delete(ctx.Context(), objDrpc); err != nil {
		return err
	}

	log.Debugf("Deleted drpc \"%s/%s\" in cluster %q", namespace, name, hub.Name)

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
				Kind:      "Placement",
				Name:      placementName,
				Namespace: namespace,
			},
			PVCSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"appname": appname},
			},
		},
	}

	return drpc
}

func createPlacementManagedByRamen(ctx types.TestContext, name, namespace string) error {
	log := ctx.Logger()
	labels := make(map[string]string)
	labels[deployers.AppLabelKey] = name
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
		// Predicate is not required since OCM is not managing this app.
		Spec: clusterv1beta1.PlacementSpec{
			ClusterSets:      []string{ctx.Config().ClusterSet},
			NumberOfClusters: &numClusters,
		},
	}

	err := ctx.Env().Hub.Client.Create(ctx.Context(), placement)
	if err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return err
		}

		log.Debugf("Placement \"%s/%s\" already Exists in cluster %q", namespace, name, ctx.Env().Hub.Name)
	}

	log.Debugf("Created placement \"%s/%s\" in cluster %q", namespace, name, ctx.Env().Hub.Name)

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
				Kind:      "Placement",
				Name:      placementName,
				Namespace: namespace,
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
