// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	"github.com/ramendr/ramen/e2e/types"
)

// Namespace annotation for volsync to grant elevated permissions for mover pods
// More info: https://volsync.readthedocs.io/en/stable/usage/permissionmodel.html#controlling-mover-permissions
const volsyncPrivilegedMovers = "volsync.backube/privileged-movers"

func CreateNamespace(ctx types.Context, cluster types.Cluster, namespace string) error {
	log := ctx.Logger()

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}

	err := cluster.Client.Create(ctx.Context(), ns)
	if err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return err
		}

		log.Debugf("Namespace %q already exist in cluster %q", namespace, cluster.Name)
	}

	log.Debugf("Created namespace %q in cluster %q", namespace, cluster.Name)

	return nil
}

func DeleteNamespace(ctx types.Context, cluster types.Cluster, namespace string) error {
	log := ctx.Logger()

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}

	err := cluster.Client.Delete(ctx.Context(), ns)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}

		log.Debugf("Namespace %q not found in cluster %q", namespace, cluster.Name)

		return nil
	}

	log.Debugf("Waiting until namespace %q is deleted in cluster %q", namespace, cluster.Name)

	startTime := time.Now()
	key := k8stypes.NamespacedName{Name: namespace}

	for {
		if err := cluster.Client.Get(ctx.Context(), key, ns); err != nil {
			if !k8serrors.IsNotFound(err) {
				return err
			}

			log.Debugf("Namespace %q deleted in cluster %q", namespace, cluster.Name)

			return nil
		}

		if time.Since(startTime) > 60*time.Second {
			return fmt.Errorf("timeout deleting namespace %q in cluster %q", namespace, cluster.Name)
		}

		if err := Sleep(ctx.Context(), time.Second); err != nil {
			return err
		}
	}
}

// Problem: currently we must manually add an annotation to application’s namespace to make volsync work.
// See this link https://volsync.readthedocs.io/en/stable/usage/permissionmodel.html#controlling-mover-permissions
// Workaround: create ns in both drclusters and add annotation
func CreateNamespaceAndAddAnnotation(ctx types.Context, namespace string) error {
	env := ctx.Env()

	if err := CreateNamespace(ctx, env.C1, namespace); err != nil {
		return err
	}

	if err := addNamespaceAnnotationForVolSync(ctx, env.C1, namespace); err != nil {
		return err
	}

	if err := CreateNamespace(ctx, env.C2, namespace); err != nil {
		return err
	}

	return addNamespaceAnnotationForVolSync(ctx, env.C2, namespace)
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
