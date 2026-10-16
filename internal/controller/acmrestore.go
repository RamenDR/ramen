// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"fmt"
	"strings"

	restoresv1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	klusterletconfigv1alpha1 "github.com/stolostron/cluster-lifecycle-api/klusterletconfig/v1alpha1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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

// EnsureKlusterletConfigForRestore polls the ACM Restore resources and, when one restores managed
// clusters, ensures the global KlusterletConfig exists and carries the AppliedManifestWork
// eviction grace period. It is invoked from the DRPC reconciliation so that it is re-run on every
// reconcile (the DRPC requeues at least once every StatusCheckDelay).
func EnsureKlusterletConfigForRestore(ctx context.Context, c client.Client) error {
	log := ctrl.Log.WithName("acmrestore")

	restoreList := &restoresv1beta1.RestoreList{}

	if err := c.List(ctx, restoreList); err != nil {
		if meta.IsNoMatchError(err) {
			log.Info("Restore CRD is not installed", "groupVersion", restoresv1beta1.GroupVersion.String())

			return nil
		}

		return fmt.Errorf("failed to list Restores: %w", err)
	}

	relevant := false

	for i := range restoreList.Items {
		item := &restoreList.Items[i]

		if item.Spec.VeleroManagedClustersBackupName == nil {
			continue
		}

		// Same normalization as the Restore webhook.
		backupName := strings.ToLower(strings.TrimSpace(*item.Spec.VeleroManagedClustersBackupName))
		if backupName == "" || backupName == acmRestoreVeleroManagedClustersBackupSkip {
			continue
		}

		relevant = true

		break
	}

	if !relevant {
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
