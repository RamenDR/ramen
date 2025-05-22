// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/types"
)

const (
	// Namespace annotation for volsync to grant elevated permissions for mover pods
	// More info: https://volsync.readthedocs.io/en/stable/usage/permissionmodel.html#controlling-mover-permissions
	volsyncPrivilegedMovers = "volsync.backube/privileged-movers"

	// Label to identify namespaces created by ramen e2e
	ramenE2EManagedLabel = "app.kubernetes.io/managed-by"
	ramenE2EManagedValue = "ramen-e2e"
)

func CreateNamespace(ctx types.Context, cluster types.Cluster, namespace string) error {
	log := ctx.Logger()
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
			Labels: map[string]string{
				ramenE2EManagedLabel: ramenE2EManagedValue,
			},
		},
	}

	err := cluster.Client.Create(ctx.Context(), ns)
	if err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return err
		}

		log.Debugf("Namespace %q already exists in cluster %q", namespace, cluster.Name)
	} else {
		log.Debugf("Created namespace %q in cluster %q with label %s=%s",
			namespace, cluster.Name, ramenE2EManagedLabel, ramenE2EManagedValue)
	}

	return nil
}

func DeleteNamespace(ctx types.Context, cluster types.Cluster, namespace string) error {
	log := ctx.Logger()
	key := k8stypes.NamespacedName{Name: namespace}
	objNs := &corev1.Namespace{}

	err := cluster.Client.Get(ctx.Context(), key, objNs)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			log.Debugf("Namespace %q not found in cluster %q", namespace, cluster.Name)

			return nil
		}

		return err
	}

	labels := objNs.GetLabels()
	if labels == nil || labels[ramenE2EManagedLabel] != ramenE2EManagedValue {
		log.Debugf("Namespace %q in cluster %q is not managed by ramen-e2e, skipping deletion",
			namespace, cluster.Name)

		return nil
	}

	err = cluster.Client.Delete(ctx.Context(), objNs)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}

		log.Debugf("Namespace %q not found in cluster %q", namespace, cluster.Name)
	}

	return nil
}

// CreateAppNamespaces creates a namespace on both drclusters with ramen-e2e label.
// The label ensure safe deletion by allowing only ramen-e2e managed namespaces to be removed.
// This prevents accidental deletion of namespaces not created by the e2e test framework.
func CreateAppNamespaces(ctx types.Context, namespace string) error {
	if err := CreateNamespace(ctx, ctx.Env().C1, namespace); err != nil {
		return err
	}

	return CreateNamespace(ctx, ctx.Env().C2, namespace)
}

// DeleteAppNamespaces safely deletes a namespace from both drclusters.
// Only deletes namespaces that were created by ramen-e2e.
// This provides protection against accidentally deleting user or system namespaces.
func DeleteAppNamespaces(ctx types.Context, namespace string) error {
	if err := DeleteNamespace(ctx, ctx.Env().C1, namespace); err != nil {
		return err
	}

	return DeleteNamespace(ctx, ctx.Env().C2, namespace)
}

// Problem: currently we must manually add an annotation to application’s namespace to make volsync work.
// See this link https://volsync.readthedocs.io/en/stable/usage/permissionmodel.html#controlling-mover-permissions
// Workaround: add volsync annotation on app namespaces on both drclusters
func AddVolsyncAnnotation(ctx types.Context, namespace string) error {
	if ctx.Config().Distro == config.DistroK8s {
		if err := addNamespaceAnnotationForVolSync(ctx, ctx.Env().C1, namespace); err != nil {
			return err
		}

		return addNamespaceAnnotationForVolSync(ctx, ctx.Env().C2, namespace)
	}

	return nil
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

		annotations[volsyncPrivilegedMovers] = "true"
		objNs.SetAnnotations(annotations)

		if err := cluster.Client.Update(ctx.Context(), objNs); err != nil {
			return err
		}

		log.Debugf("Annotated namespace %q with \"%s: %s\" in cluster %q",
			namespace, volsyncPrivilegedMovers, annotations[volsyncPrivilegedMovers], cluster.Name)

		return nil
	})
}
