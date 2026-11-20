// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	restoresv1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	klusterletconfigv1alpha1 "github.com/stolostron/cluster-lifecycle-api/klusterletconfig/v1alpha1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
)

const (
	// acmRestoreVeleroManagedClustersBackupSkip is the value of the ACM Restore
	// spec.veleroManagedClustersBackupName that tells ACM not to restore managed clusters.
	acmRestoreVeleroManagedClustersBackupSkip = "skip"

	// klusterletConfigGlobalName is the ACM wide default KlusterletConfig.
	klusterletConfigGlobalName = "global"
)

// ErrNoManagedClustersRestore is returned by EnsureKlusterletConfigForRestore when there is no ACM
// Restore of managed clusters (or the Restore CRD is not installed), so there was nothing to do.
var ErrNoManagedClustersRestore = errors.New("no ACM Restore of managed clusters")

// EnsureKlusterletConfigForRestore polls the ACM Restore resources and, when one restores managed
// clusters, ensures the global KlusterletConfig exists and carries the AppliedManifestWork
// eviction grace period, so that the work agents of the managed clusters do not evict applied
// ManifestWorks while the hub is being recovered. The grace period is taken from the Ramen
// configuration (hubRecovery.appliedManifestWorkEvictionGracePeriod). A nil ramenConfig means the
// configuration could not be retrieved: the default grace period is then used only to create a
// missing KlusterletConfig, and an existing one is left untouched. It is invoked from the DRPC
// reconciliation so that it is re-run on every reconcile (the DRPC requeues at least once every
// StatusCheckDelay). It returns nil when a Restore of managed clusters was found and the
// KlusterletConfig was ensured, ErrNoManagedClustersRestore when there was nothing to do, and
// another error on failure.
func EnsureKlusterletConfigForRestore(ctx context.Context, c client.Client, ramenConfig *rmn.RamenConfig) error {
	log := ctrl.Log.WithName("acmrestore")

	restoreList := &restoresv1beta1.RestoreList{}

	if err := c.List(ctx, restoreList); err != nil {
		if meta.IsNoMatchError(err) {
			log.Info("Restore CRD is not installed", "groupVersion", restoresv1beta1.GroupVersion.String())

			return ErrNoManagedClustersRestore
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
		return ErrNoManagedClustersRestore
	}

	log.Info("Restore of managed clusters detected, ensuring KlusterletConfig", "name", klusterletConfigGlobalName)

	// A nil ramenConfig means the grace period is unknown: the default is only used to create a
	// missing KlusterletConfig, and an existing one is left untouched.
	gracePeriod := DefaultAppliedManifestWorkEvictionGracePeriod
	if ramenConfig != nil {
		gracePeriod = appliedManifestWorkEvictionGracePeriodOrDefault(ramenConfig)
	}

	return ensureGlobalKlusterletConfig(ctx, c, gracePeriod, ramenConfig == nil)
}

// ensureGlobalKlusterletConfig creates the global KlusterletConfig if it is missing, with
// spec.appliedManifestWorkEvictionGracePeriod set to gracePeriod. If it already exists, it is
// updated to gracePeriod unless createOnly is set, in which case it is left untouched (used when
// gracePeriod is only a fallback and must not override an existing value).
func ensureGlobalKlusterletConfig(
	ctx context.Context, c client.Client, gracePeriod string, createOnly bool,
) error {
	kc := &klusterletconfigv1alpha1.KlusterletConfig{}
	log := ctrl.Log.WithName("acmrestore")

	err := c.Get(ctx, types.NamespacedName{Name: klusterletConfigGlobalName}, kc)
	if k8serrors.IsNotFound(err) {
		kc.SetName(klusterletConfigGlobalName)
		kc.Spec.AppliedManifestWorkEvictionGracePeriod = gracePeriod

		log.Info("Creating KlusterletConfig",
			"name", klusterletConfigGlobalName, "evictionGracePeriod", gracePeriod)

		if err := c.Create(ctx, kc); err != nil {
			return fmt.Errorf("failed to create KlusterletConfig %s: %w", klusterletConfigGlobalName, err)
		}

		return nil
	}

	if err != nil {
		return fmt.Errorf("failed to get KlusterletConfig %s: %w", klusterletConfigGlobalName, err)
	}

	if createOnly {
		return nil
	}

	if kc.Spec.AppliedManifestWorkEvictionGracePeriod == gracePeriod {
		return nil
	}

	kc.Spec.AppliedManifestWorkEvictionGracePeriod = gracePeriod

	log.Info("Updating KlusterletConfig eviction grace period",
		"name", klusterletConfigGlobalName, "evictionGracePeriod", gracePeriod)

	if err := c.Update(ctx, kc); err != nil {
		return fmt.Errorf("failed to update KlusterletConfig %s: %w", klusterletConfigGlobalName, err)
	}

	return nil
}
