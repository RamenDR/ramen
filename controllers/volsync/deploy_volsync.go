// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package volsync

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	ManagedClusterAddOnKind    string = "ManagedClusterAddOn"
	ManagedClusterAddOnGroup   string = "addon.open-cluster-management.io"
	ManagedClusterAddOnVersion string = "v1alpha1"

	VolsyncManagedClusterAddOnName string = "volsync" // Needs to have this name
)

// Function to deploy Volsync from ACM to managed cluster via a ManagedClusterAddOn
//
// Calling this function requires a clusterrole that can create/update ManagedClusterAddOns
//
// Should be called from the Hub
func DeployVolSyncToCluster(ctx context.Context, k8sClient client.Client,
	managedClusterName string, log logr.Logger,
) error {
	err := reconcileVolSyncManagedClusterAddOn(ctx, k8sClient, managedClusterName,
		log.WithValues("managedClusterName", managedClusterName))
	if err != nil {
		return err
	}

	return nil
}

func reconcileVolSyncManagedClusterAddOn(ctx context.Context, k8sClient client.Client,
	managedClusterName string, log logr.Logger,
) error {
	log.Info("Reconciling VolSync ManagedClusterAddOn")

	// Using unstructured to avoid needing to require ManagedClusterAddOn in client scheme
	vsMCAO := &unstructured.Unstructured{}
	vsMCAO.Object = map[string]interface{}{
		"metadata": map[string]interface{}{
			"name":      VolsyncManagedClusterAddOnName,
			"namespace": managedClusterName, // Needs to be deployed to managedcluster ns on hub
		},
	}
	vsMCAO.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   ManagedClusterAddOnGroup,
		Version: ManagedClusterAddOnVersion,
		Kind:    ManagedClusterAddOnKind,
	})

	op, err := ctrlutil.CreateOrUpdate(ctx, k8sClient, vsMCAO, func() error {
		// Do not update the ManagedClusterAddOn if it already exists - let users update settings if required
		creationTimeStamp := vsMCAO.GetCreationTimestamp()
		if creationTimeStamp.IsZero() {
			// Create with empty spec - no spec settings required
			vsMCAO.Object["spec"] = map[string]interface{}{}
		}

		return nil
	})
	if err != nil {
		log.Error(err, "error creating or updating VolSync ManagedClusterAddOn")

		return fmt.Errorf("error creating or updating VolSync ManagedClusterAddOn (%w)", err)
	}

	log.Info("VolSync ManagedClusterAddOn createOrUpdate Complete", "op", op)

	return nil
}
