package validate

import (
	"fmt"

	storagev1 "k8s.io/api/storage/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ramendr/ramen/e2e/types"
)

// validateStorageClasses verifies that each storageClass referenced in PVC specs
// exists in both managed clusters.
func validateStorageClasses(ctx types.Context) error {
	env := ctx.Env()
	config := ctx.Config()

	clusters := []*types.Cluster{&env.C1, &env.C2}

	for _, spec := range config.PVCSpecs {
		for _, cluster := range clusters {
			_, err := getStorageClass(ctx, cluster, spec.StorageClassName)
			if err != nil {
				return fmt.Errorf("failed to validate pvcSpec %q storage class: %w", spec.Name, err)
			}
		}
	}

	return nil
}

// getStorageClass retrieves a specific StorageClass from the given cluster
func getStorageClass(ctx types.Context, cluster *types.Cluster, scName string) (*storagev1.StorageClass, error) {
	var sc storagev1.StorageClass

	key := client.ObjectKey{Name: scName}

	err := cluster.Client.Get(ctx.Context(), key, &sc)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, fmt.Errorf("storageClass %q not found in cluster %q", scName, cluster.Name)
		}

		return nil, fmt.Errorf("failed to get storageClass %q in cluster %q: %w", scName, cluster.Name, err)
	}

	return &sc, nil
}
