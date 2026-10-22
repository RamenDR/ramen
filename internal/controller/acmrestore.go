// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"fmt"

	klusterletconfigv1alpha1 "github.com/stolostron/cluster-lifecycle-api/klusterletconfig/v1alpha1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NOTE: the typed cluster.open-cluster-management.io/v1beta1 Restore API lives in the
// cluster-backup-operator module. That package does not compile in this repo because its
// restore_webhook.go calls the pre-generic ctrl.NewWebhookManagedBy(mgr), while this repo
// pins controller-runtime v0.23.3 where that function is generic. The Restore is therefore
// handled as an unstructured object by GVK.

const (
	// acmRestoreVeleroManagedClustersBackupSkip is the value of the ACM Restore
	// spec.veleroManagedClustersBackupName that tells ACM not to restore managed clusters.
	acmRestoreVeleroManagedClustersBackupSkip = "skip"

	// klusterletConfigGlobalName is the ACM wide default KlusterletConfig.
	klusterletConfigGlobalName = "global"

	// klusterletConfigEvictionGracePeriod is the AppliedManifestWork eviction grace period set on
	// the global KlusterletConfig, so that the work agents of the managed clusters do not evict
	// applied ManifestWorks while the hub is being recovered.
	klusterletConfigEvictionGracePeriod = "24h"
)

var restoreGVK = schema.GroupVersionKind{
	Group:   "cluster.open-cluster-management.io",
	Version: "v1beta1",
	Kind:    "RestoreList",
}

// EnsureKlusterletConfigForRestore polls the ACM Restore resources and, when one restores managed
// clusters, ensures the global KlusterletConfig exists and carries the AppliedManifestWork
// eviction grace period. It is invoked from the DRPC reconciliation so that it is re-run on every
// reconcile (the DRPC requeues at least once every StatusCheckDelay).
func EnsureKlusterletConfigForRestore(ctx context.Context, c client.Client) error {
	log := ctrl.Log.WithName("acmrestore")

	restoreList := &unstructured.UnstructuredList{}
	restoreList.SetGroupVersionKind(restoreGVK)

	if err := c.List(ctx, restoreList); err != nil {
		return fmt.Errorf("failed to list Restores: %w", err)
	}

	relevant := false

	for i := range restoreList.Items {
		item := &restoreList.Items[i]

		backupName, found, err := unstructured.NestedString(item.Object, "spec", "veleroManagedClustersBackupName")
		if err != nil {
			continue
		}

		if !found || backupName == "" || backupName == acmRestoreVeleroManagedClustersBackupSkip {
			continue
		}

		relevant = true

		break
	}

	if !relevant {
		log.Info("No Restore of managed clusters detected, skipping KlusterletConfig update")

		return nil
	}

	log.Info("Restore of managed clusters detected, ensuring KlusterletConfig", "name", klusterletConfigGlobalName)

	return ensureGlobalKlusterletConfig(ctx, c)
}

// ensureGlobalKlusterletConfig creates or updates the global KlusterletConfig so that its
// spec.appliedManifestWorkEvictionGracePeriod equals klusterletConfigEvictionGracePeriod.
func ensureGlobalKlusterletConfig(ctx context.Context, c client.Client) error {
	kc := &klusterletconfigv1alpha1.KlusterletConfig{}
	log := ctrl.Log.WithName("acmrestore")

	err := c.Get(ctx, types.NamespacedName{Name: klusterletConfigGlobalName}, kc)
	if k8serrors.IsNotFound(err) {
		kc.SetName(klusterletConfigGlobalName)
		kc.Spec.AppliedManifestWorkEvictionGracePeriod = klusterletConfigEvictionGracePeriod

		log.Info("Creating KlusterletConfig",
			"name", klusterletConfigGlobalName, "evictionGracePeriod", klusterletConfigEvictionGracePeriod)

		if err := c.Create(ctx, kc); err != nil {
			return fmt.Errorf("failed to create KlusterletConfig %s: %w", klusterletConfigGlobalName, err)
		}

		return nil
	}

	if err != nil {
		return fmt.Errorf("failed to get KlusterletConfig %s: %w", klusterletConfigGlobalName, err)
	}

	if kc.Spec.AppliedManifestWorkEvictionGracePeriod == klusterletConfigEvictionGracePeriod {
		return nil
	}

	kc.Spec.AppliedManifestWorkEvictionGracePeriod = klusterletConfigEvictionGracePeriod

	log.Info("Updating KlusterletConfig eviction grace period",
		"name", klusterletConfigGlobalName, "evictionGracePeriod", klusterletConfigEvictionGracePeriod)

	if err := c.Update(ctx, kc); err != nil {
		return fmt.Errorf("failed to update KlusterletConfig %s: %w", klusterletConfigGlobalName, err)
	}

	return nil
}
