// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package workloads

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	k8stypes "k8s.io/apimachinery/pkg/types"

	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
)

// lookupPVCInfo returns an entry for each requested PVC name. Its value is true if the claim exists on the cluster.
func lookupPVCInfo(ctx types.TestContext, cluster *types.Cluster, pvcNames []string) (map[string]bool, error) {
	pvcInfo := make(map[string]bool, len(pvcNames))

	for _, name := range pvcNames {
		_, err := getPVC(ctx, cluster, ctx.AppNamespace(), name)
		if err != nil {
			if !k8serrors.IsNotFound(err) {
				return nil, err
			}

			pvcInfo[name] = false
		} else {
			pvcInfo[name] = true
		}
	}

	return pvcInfo, nil
}

func getPVC(
	ctx types.TestContext,
	cluster *types.Cluster,
	namespace, name string,
) (*corev1.PersistentVolumeClaim, error) {
	pvc := &corev1.PersistentVolumeClaim{}
	key := k8stypes.NamespacedName{Name: name, Namespace: namespace}

	err := cluster.Client.Get(ctx.Context(), key, pvc)
	if err != nil {
		return nil, err
	}

	return pvc, nil
}

// checkPVCHealth verifies the application PVC is Bound and not being deleted.
// Bound returns nil. Pending is retryable. NotFound, Lost, or a set
// deletionTimestamp are unrecoverable so wait loops fail immediately.
func checkPVCHealth(
	ctx types.TestContext,
	cluster *types.Cluster,
	namespace, pvcName string,
) error {
	pvc, err := getPVC(ctx, cluster, namespace, pvcName)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}

		return fmt.Errorf("pvc \"%s/%s\" not found in cluster %q: %w",
			namespace, pvcName, cluster.Name, util.ErrUnrecoverable)
	}

	if !pvc.GetDeletionTimestamp().IsZero() {
		return fmt.Errorf("pvc \"%s/%s\" is being deleted in cluster %q: %w",
			namespace, pvcName, cluster.Name, util.ErrUnrecoverable)
	}

	switch pvc.Status.Phase {
	case corev1.ClaimBound:
		return nil
	case corev1.ClaimPending:
		return fmt.Errorf("pvc \"%s/%s\" phase is %q, expected %q in cluster %q",
			namespace, pvcName, pvc.Status.Phase, corev1.ClaimBound, cluster.Name)
	case corev1.ClaimLost:
		return fmt.Errorf("pvc \"%s/%s\" phase is %q in cluster %q: %w",
			namespace, pvcName, pvc.Status.Phase, cluster.Name, util.ErrUnrecoverable)
	default:
		return fmt.Errorf("pvc \"%s/%s\" phase is %q, expected %q in cluster %q",
			namespace, pvcName, pvc.Status.Phase, corev1.ClaimBound, cluster.Name)
	}
}
