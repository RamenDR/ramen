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

// restoreState classifies the relevant ACM Restores by status.phase. The values are ordered so
// that the aggregate state of several Restores is the largest one.
type restoreState int

const (
	// restoreIgnored is a Restore that does not restore managed clusters.
	restoreIgnored restoreState = iota
	// restoreTerminal is a Restore that finished (with or without errors).
	restoreTerminal
	// restoreActive is a Restore that is in progress, or that is enabled for passive sync.
	restoreActive
	// restoreFailed is a Restore that could not run.
	restoreFailed
)

// klusterletAction is what the caller has to do with the global KlusterletConfig.
type klusterletAction int

const (
	actionNone   klusterletAction = iota // nothing to do
	actionEnsure                         // create or update to the grace period
	actionError                          // a Restore is in Error; report and change nothing
)

// restoreStateOf classifies a Restore. Any phase that is not known to be terminal or failed is
// treated as active, so that an unknown phase keeps the protection in place.
func restoreStateOf(restore *restoresv1beta1.Restore) restoreState {
	if restore.Spec.VeleroManagedClustersBackupName == nil {
		return restoreIgnored
	}

	// Same normalization as the Restore webhook.
	backupName := strings.ToLower(strings.TrimSpace(*restore.Spec.VeleroManagedClustersBackupName))
	if backupName == "" || backupName == acmRestoreVeleroManagedClustersBackupSkip {
		return restoreIgnored
	}

	switch string(restore.Status.Phase) {
	case restoresv1beta1.RestorePhaseFinished, restoresv1beta1.RestorePhaseFinishedWithErrors:
		return restoreTerminal
	case restoresv1beta1.RestorePhaseError:
		return restoreFailed
	default:
		return restoreActive
	}
}

// aggregateRestoreState returns Failed if any relevant Restore failed, otherwise Active if any is
// active, otherwise Terminal if any is relevant, otherwise Ignored.
func aggregateRestoreState(restores []restoresv1beta1.Restore) restoreState {
	state := restoreIgnored

	for i := range restores {
		if s := restoreStateOf(&restores[i]); s > state {
			state = s
		}
	}

	return state
}

// decideKlusterletAction decides what to do with the global KlusterletConfig from the Restores.
func decideKlusterletAction(restores []restoresv1beta1.Restore) klusterletAction {
	switch aggregateRestoreState(restores) {
	case restoreFailed:
		return actionError
	case restoreActive:
		return actionEnsure
	case restoreIgnored, restoreTerminal:
	}

	return actionNone
}

// EnsureKlusterletConfigForRestore polls the ACM Restore resources and, while a Restore of managed
// clusters is active, ensures the global KlusterletConfig exists and carries the AppliedManifestWork
// eviction grace period, so that the work agents of the managed clusters do not evict applied
// ManifestWorks while the hub is being recovered. A Restore that is terminal no longer raises the
// grace period, so that it is not asserted again on every reconcile once the Restore is done. The
// grace period is taken from the Ramen configuration
// (hubRecovery.appliedManifestWorkEvictionGracePeriod). A nil ramenConfig means the configuration
// could not be retrieved: the default grace period is then used only to create a missing
// KlusterletConfig, and an existing one is left untouched. It is invoked from the DRPC
// reconciliation so that it is re-run on every reconcile (the DRPC requeues at least once every
// StatusCheckDelay). It returns nil when a Restore of managed clusters was found and nothing
// failed, ErrNoManagedClustersRestore when there was nothing to do, and another error on failure,
// including when a Restore is in the Error phase: the KlusterletConfig is then left unchanged so
// that the protection stays in place.
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

	switch decideKlusterletAction(restoreList.Items) {
	case actionError:
		return fmt.Errorf("ACM Restore %s is in the %s phase, KlusterletConfig %s is left unchanged",
			firstRestoreName(restoreList.Items, restoreFailed), restoresv1beta1.RestorePhaseError,
			klusterletConfigGlobalName)
	case actionEnsure:
		log.Info("Restore of managed clusters is active, ensuring KlusterletConfig",
			"name", klusterletConfigGlobalName)

		// A nil ramenConfig means the grace period is unknown: the default is only used to create
		// a missing KlusterletConfig, and an existing one is left untouched.
		return ensureGlobalKlusterletConfig(ctx, c, gracePeriodOrDefault(ramenConfig), ramenConfig == nil)
	case actionNone:
	}

	if aggregateRestoreState(restoreList.Items) == restoreIgnored {
		return ErrNoManagedClustersRestore
	}

	return nil
}

// gracePeriodOrDefault returns the configured grace period, or the default when the Ramen
// configuration is unknown (nil).
func gracePeriodOrDefault(ramenConfig *rmn.RamenConfig) string {
	if ramenConfig == nil {
		return DefaultAppliedManifestWorkEvictionGracePeriod
	}

	return appliedManifestWorkEvictionGracePeriodOrDefault(ramenConfig)
}

// firstRestoreName returns the name of the first Restore in the given state.
func firstRestoreName(restores []restoresv1beta1.Restore, state restoreState) string {
	for i := range restores {
		if restoreStateOf(&restores[i]) == state {
			return restores[i].GetName()
		}
	}

	return ""
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
