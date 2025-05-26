// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"fmt"
	"reflect"
	"time"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	argocdv1alpha1hack "github.com/ramendr/ramen/e2e/argocd"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ocmv1b1 "open-cluster-management.io/api/cluster/v1beta1"
	ocmv1b2 "open-cluster-management.io/api/cluster/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ramendr/ramen/e2e/types"
)

func WaitForApplicationSetDelete(ctx types.Context, cluster types.Cluster, name, namespace string) error {
	obj := &argocdv1alpha1hack.ApplicationSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	return waitForResourceDelete(ctx, cluster, obj)
}

func WaitForConfigMapDelete(ctx types.Context, cluster types.Cluster, name, namespace string) error {
	obj := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	return waitForResourceDelete(ctx, cluster, obj)
}

func WaitForPlacementDelete(ctx types.Context, cluster types.Cluster, name, namespace string) error {
	obj := &ocmv1b1.Placement{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	return waitForResourceDelete(ctx, cluster, obj)
}

func WaitForManagedClusterSetBindingDelete(ctx types.Context, cluster types.Cluster, name, namespace string) error {
	obj := &ocmv1b2.ManagedClusterSetBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	return waitForResourceDelete(ctx, cluster, obj)
}

func WaitForDRPCDelete(ctx types.Context, cluster types.Cluster, name, namespace string) error {
	obj := &ramen.DRPlacementControl{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	return waitForResourceDelete(ctx, cluster, obj)
}

func WaitForNamespaceDelete(ctx types.Context, cluster types.Cluster, name string) error {
	log := ctx.Logger()
	obj := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}

	managed, err := isManagedByRamenE2e(ctx, cluster, obj)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}

		log.Debugf("Namespace %q not found in cluster %q", name, cluster.Name)

		return nil
	}

	if !managed {
		log.Warnf("Skipping wait for deletion of namespace %q in cluster %q: "+
			"not managed by ramen-e2e (missing label %s=%s)", name, cluster.Name, managedByLabel, ramenE2e)

		return nil
	}

	return waitForResourceDelete(ctx, cluster, obj)
}

// WaitForNamespaceDeleteOnManagedClusters waits for namespaces on both drclusters with ramen-e2e label.
func WaitForNamespaceDeleteOnManagedClusters(ctx types.Context, name string) error {
	if err := WaitForNamespaceDelete(ctx, ctx.Env().C1, name); err != nil {
		return err
	}

	return WaitForNamespaceDelete(ctx, ctx.Env().C2, name)
}

// waitForResourceDelete waits until a resource is deleted or deadline is reached
func waitForResourceDelete(ctx types.Context, cluster types.Cluster, obj client.Object) error {
	log := ctx.Logger()
	kind := getKind(obj)
	resourceName := logName(obj)

	log.Debugf("Waiting until %s %q is deleted in cluster %q", kind, resourceName, cluster.Name)

	for {
		if err := cluster.Client.Get(ctx.Context(), client.ObjectKeyFromObject(obj), obj); err != nil {
			if !k8serrors.IsNotFound(err) {
				return err
			}

			log.Debugf("%s %q deleted in cluster %q", kind, resourceName, cluster.Name)

			return nil
		}

		if err := Sleep(ctx.Context(), time.Second); err != nil {
			return fmt.Errorf("%s %q not deleted in cluster %q: %w", kind, resourceName, cluster.Name, err)
		}
	}
}

// getKind extracts resource type name from the object
func getKind(obj client.Object) string {
	t := reflect.TypeOf(obj)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	return t.Name()
}

// logName returns the resource name for logging (namespace/name or just name)
func logName(obj client.Object) string {
	if obj.GetNamespace() != "" {
		return obj.GetNamespace() + "/" + obj.GetName()
	}

	return obj.GetName()
}
