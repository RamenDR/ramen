package validate

import (
	"context"
	"fmt"

	storagev1 "k8s.io/api/storage/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ramendr/ramen/e2e/types"
)

// validateStorageClassExists ensures that the specified storageClass exists in managed clusters
func validateStorageClassExists(env *types.Env, pvcSpecs []types.PVCSpecConfig) error {
	clusters := []*types.Cluster{&env.C1, &env.C2}

	for _, spec := range pvcSpecs {
		for _, cluster := range clusters {
			if err := getStorageClass(cluster, spec.StorageClassName); err != nil {
				return fmt.Errorf("failed to find storageClass %q in cluster %q for pvcSpec %q: %w",
					spec.StorageClassName, cluster.Name, spec.Name, err)
			}
		}
	}

	return nil
}

// getStorageClass retrieves a specific StorageClass from the given cluster
func getStorageClass(cluster *types.Cluster, scName string) error {
	var sc storagev1.StorageClass

	key := client.ObjectKey{Name: scName}

	err := cluster.Client.Get(context.Background(), key, &sc)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return fmt.Errorf("storageClass %q not found in cluster %q", scName, cluster.Name)
		}

		return fmt.Errorf("failed to get storageClass %q in cluster %q: %w", scName, cluster.Name, err)
	}

	return nil
}
