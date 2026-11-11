// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package manager

import (
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/rest"
	ocmworkv1 "open-cluster-management.io/api/work/v1"
	viewv1beta1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/view/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	rmnutil "github.com/ramendr/ramen/internal/controller/util"
)

// NewManager creates the manager for the selected Ramen controller type.
func NewManager(config *rest.Config, options ctrl.Options,
	controllerType ramendrv1alpha1.ControllerType,
) (ctrl.Manager, error) {
	switch controllerType {
	case ramendrv1alpha1.DRHubType:
		options.Cache = hubCacheOptions()
	case ramendrv1alpha1.DRClusterType:
	default:
		return nil, fmt.Errorf("invalid controller type %q", controllerType)
	}

	return ctrl.NewManager(config, options)
}

func hubCacheOptions() cache.Options {
	ramenLabelSelector := labels.SelectorFromSet(labels.Set{rmnutil.CreatedByRamenLabel: "true"})

	return cache.Options{
		ByObject: map[ctrlclient.Object]cache.ByObject{
			&corev1.Secret{}: {
				Namespaces: map[string]cache.Config{
					os.Getenv("POD_NAMESPACE"): {},
					cache.AllNamespaces:        {LabelSelector: ramenLabelSelector},
				},
			},
			// Every ManagedClusterView ramen creates is labeled CreatedByRamenLabel; MCVs
			// created by other ACM components are never read by ramen.
			&viewv1beta1.ManagedClusterView{}: {Label: ramenLabelSelector},
			// Every ManifestWork ramen creates is labeled CreatedByRamenLabel; this avoids
			// caching the (often large) ManifestWorks other ACM components create per
			// managed cluster (klusterlet, addons, policies, etc).
			&ocmworkv1.ManifestWork{}: {Label: ramenLabelSelector},
		},
	}
}
