// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package workloads

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ramendr/ramen/e2e/types"
)

// validatePVCPreserved asserts that a PVC survived Disable DR with do-not-delete-pvc:
// present, Bound, not marked for deletion, and free of DR-managed owner references.
func validatePVCPreserved(
	ctx types.TestContext,
	cluster *types.Cluster,
	namespace, pvcName string,
) error {
	pvc, err := getPVC(ctx, cluster, namespace, pvcName)
	if err != nil {
		return fmt.Errorf("pvc \"%s/%s\" not preserved in cluster %q: %w",
			namespace, pvcName, cluster.Name, err)
	}

	if pvc.DeletionTimestamp != nil {
		return fmt.Errorf("pvc \"%s/%s\" has deletionTimestamp %s in cluster %q",
			namespace, pvcName, pvc.DeletionTimestamp.UTC().Format(metav1.RFC3339Micro), cluster.Name)
	}

	if pvc.Status.Phase != corev1.ClaimBound {
		return fmt.Errorf("pvc \"%s/%s\" phase is %q, expected %q in cluster %q",
			namespace, pvcName, pvc.Status.Phase, corev1.ClaimBound, cluster.Name)
	}

	for _, ref := range pvc.OwnerReferences {
		if isDRManagedOwnerReference(ref) {
			return fmt.Errorf("pvc \"%s/%s\" still owned by %s %q in cluster %q",
				namespace, pvcName, ref.Kind, ref.Name, cluster.Name)
		}
	}

	return nil
}

func isDRManagedOwnerReference(ref metav1.OwnerReference) bool {
	switch ref.Kind {
	case "VolumeReplicationGroup", "ReplicationSource", "ReplicationDestination":
		return true
	default:
		return false
	}
}
