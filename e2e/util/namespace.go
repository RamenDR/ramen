// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/types"
)

const (
	// Namespace annotation for volsync to grant elevated permissions for mover pods
	// More info: https://volsync.readthedocs.io/en/stable/usage/permissionmodel.html#controlling-mover-permissions
	volsyncPrivilegedMoversAnnotation = "volsync.backube/privileged-movers"

	// Label to identify namespaces created by ramen e2e
	managedByLabel = "app.kubernetes.io/managed-by"
	ramenE2e       = "ramen-e2e"
)

// CreateNamespace creates a namespace in the specified cluster with the ramen-e2e managed label.
// If the namespace already exists, it checks whether it's managed by ramen-e2e and logs a warning if not.
func CreateNamespace(ctx types.Context, cluster types.Cluster, name string) error {
	log := ctx.Logger()

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				managedByLabel: ramenE2e,
			},
		},
	}

	err := cluster.Client.Create(ctx.Context(), ns)
	if err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return err
		}

		log.Debugf("Namespace %q already exist in cluster %q", name, cluster.Name)

		managed, err := isManagedByRamenE2e(ctx, cluster, ns)
		if err != nil {
			return err
		}

		if !managed {
			log.Warnf("Namespace %q in cluster %q will not be cleaned up after the test: "+
				"not managed by ramen-e2e (missing lebel %s=%s)", name, cluster.Name, managedByLabel, ramenE2e)
		}

		return nil
	}

	log.Debugf("Created namespace %q in cluster %q with label %s=%s", name, cluster.Name, managedByLabel, ramenE2e)

	return nil
}

// DeleteNamespace safely deletes a namespace from the specified cluster.
// Only deletes namespaces that are managed by ramen-e2e.
func DeleteNamespace(ctx types.Context, cluster types.Cluster, name string) error {
	log := ctx.Logger()

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}

	managed, err := isManagedByRamenE2e(ctx, cluster, ns)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}

		log.Debugf("Namespace %q not found in cluster %q", name, cluster.Name)

		return nil
	}

	if !managed {
		log.Warnf("Skipping deletion of namespace %q in cluster %q: not managed by ramen-e2e (missing label %s=%s)",
			name, cluster.Name, managedByLabel, ramenE2e)

		return nil
	}

	err = cluster.Client.Delete(ctx.Context(), ns)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}

		log.Debugf("Namespace %q not found in cluster %q", name, cluster.Name)

		return nil
	}

	log.Debugf("Deleted namespace %q in cluster %q", name, cluster.Name)

	return nil
}

// CreateNamespaceOnMangedClusters creates a namespace on both drclusters with ramen-e2e label.
func CreateNamespaceOnMangedClusters(ctx types.Context, namespace string) error {
	if err := CreateNamespace(ctx, ctx.Env().C1, namespace); err != nil {
		return err
	}

	return CreateNamespace(ctx, ctx.Env().C2, namespace)
}

// DeleteNamespaceOnManagedClusters safely deletes a namespace from both drclusters.
// Only deletes namespaces that were created by ramen-e2e (have the management label).
// This provides protection against accidentally deleting user or system namespaces.
func DeleteNamespaceOnManagedClusters(ctx types.Context, namespace string) error {
	if err := DeleteNamespace(ctx, ctx.Env().C1, namespace); err != nil {
		return err
	}

	return DeleteNamespace(ctx, ctx.Env().C2, namespace)
}

// Problem: currently we must manually add an annotation to application’s namespace to make volsync work.
// See this link https://volsync.readthedocs.io/en/stable/usage/permissionmodel.html#controlling-mover-permissions
// Workaround: add volsync annotation on app namespaces on both drclusters
func AddVolsyncAnnontationOnManagedClusters(ctx types.Context, namespace string) error {
	if ctx.Config().Distro != config.DistroK8s {
		return nil
	}

	if err := addNamespaceAnnotationForVolSync(ctx, ctx.Env().C1, namespace); err != nil {
		return err
	}

	return addNamespaceAnnotationForVolSync(ctx, ctx.Env().C2, namespace)
}

func addNamespaceAnnotationForVolSync(ctx types.Context, cluster types.Cluster, namespace string) error {
	log := ctx.Logger()

	key := k8stypes.NamespacedName{Name: namespace}

	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		objNs := &corev1.Namespace{}

		if err := cluster.Client.Get(ctx.Context(), key, objNs); err != nil {
			return err
		}

		annotations := objNs.GetAnnotations()
		if annotations == nil {
			annotations = make(map[string]string)
		}

		annotations[volsyncPrivilegedMoversAnnotation] = "true"
		objNs.SetAnnotations(annotations)

		if err := cluster.Client.Update(ctx.Context(), objNs); err != nil {
			return err
		}

		log.Debugf("Annotated namespace %q with \"%s: %s\" in cluster %q",
			namespace, volsyncPrivilegedMoversAnnotation, annotations[volsyncPrivilegedMoversAnnotation], cluster.Name)

		return nil
	})
}

// isManagedByRamenE2e checks if a Kubernetes object is managed by the ramen-e2e.
// Returns true if the object exists and has the required label, false if it exists but lacks the label.
// Returns an error if the object cannot be retrieved (e.g., not found).
func isManagedByRamenE2e(ctx types.Context, cluster types.Cluster, obj client.Object) (bool, error) {
	err := cluster.Client.Get(ctx.Context(), client.ObjectKeyFromObject(obj), obj)
	if err != nil {
		return false, err
	}

	labels := obj.GetLabels()

	return labels[managedByLabel] == ramenE2e, nil
}
