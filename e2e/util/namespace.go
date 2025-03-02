// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Namespace annotation for volsync to grant elevated permissions for mover pods
// More info: https://volsync.readthedocs.io/en/stable/usage/permissionmodel.html#controlling-mover-permissions
const volsyncPrivilegedMovers = "volsync.backube/privileged-movers"

func CreateNamespace(cluster Cluster, namespace string, log *zap.SugaredLogger) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}

	err := cluster.Client.Create(context.Background(), ns)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}

		log.Debugf("Namespace %q already exist in cluster %q", namespace, cluster.Name)
	}

	log.Debugf("Created namespace %q in cluster %q", namespace, cluster.Name)

	return nil
}

func DeleteNamespace(cluster Cluster, namespace string, log *zap.SugaredLogger) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}

	err := cluster.Client.Delete(context.Background(), ns)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}

		log.Debugf("Namespace %q not found in cluster %q", namespace, cluster.Name)

		return nil
	}

	log.Debugf("Waiting until namespace %q is deleted in cluster %q", namespace, cluster.Name)

	startTime := time.Now()
	key := types.NamespacedName{Name: namespace}

	for {
		if err := cluster.Client.Get(context.Background(), key, ns); err != nil {
			if !errors.IsNotFound(err) {
				return err
			}

			log.Debugf("Namespace %q deleted in cluster %q", namespace, cluster.Name)

			return nil
		}

		if time.Since(startTime) > 60*time.Second {
			return fmt.Errorf("timeout deleting namespace %q in cluster %q", namespace, cluster.Name)
		}

		time.Sleep(time.Second)
	}
}

// Problem: currently we must manually add an annotation to application’s namespace to make volsync work.
// See this link https://volsync.readthedocs.io/en/stable/usage/permissionmodel.html#controlling-mover-permissions
// Workaround: create ns in both drclusters and add annotation
func CreateNamespaceAndAddAnnotation(namespace string, log *zap.SugaredLogger) error {
	if err := CreateNamespace(Ctx.C1, namespace, log); err != nil {
		return err
	}

	if err := addNamespaceAnnotationForVolSync(Ctx.C1, namespace, log); err != nil {
		return err
	}

	if err := CreateNamespace(Ctx.C2, namespace, log); err != nil {
		return err
	}

	return addNamespaceAnnotationForVolSync(Ctx.C2, namespace, log)
}

func addNamespaceAnnotationForVolSync(cluster Cluster, namespace string, log *zap.SugaredLogger) error {
	key := types.NamespacedName{Name: namespace}
	objNs := &corev1.Namespace{}

	if err := cluster.Client.Get(context.Background(), key, objNs); err != nil {
		return err
	}

	annotations := objNs.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	annotations[volsyncPrivilegedMovers] = "true"
	objNs.SetAnnotations(annotations)

	if err := cluster.Client.Update(context.Background(), objNs); err != nil {
		return err
	}

	log.Debugf("Annotated namespace %q with \"%s: %s\" in cluster %q",
		namespace, volsyncPrivilegedMovers, annotations[volsyncPrivilegedMovers], cluster.Name)

	return nil
}
