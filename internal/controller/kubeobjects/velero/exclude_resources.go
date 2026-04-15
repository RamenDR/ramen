// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package velero

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// DefaultExcludedResourcesConfigMapName is the name of the ConfigMap that contains
	// the default list of resources to exclude from Velero backups
	DefaultExcludedResourcesConfigMapName = "default-excluded-resources"

	// ExcludedResourcesKey is the key in the ConfigMap data that contains the excluded resources list
	ExcludedResourcesKey = "resources"
)

// getDefaultExcludedResources returns the hardcoded default list of resources to exclude from Velero backups.
// These defaults are used when creating the ConfigMap for the first time or as a fallback.
func getDefaultExcludedResources() []string {
	return []string{
		// Exclude VRs from Backup so VRG can create them: see https://github.com/RamenDR/ramen/issues/884
		"volumereplications.replication.storage.openshift.io",
		"volumegroupreplications.replication.storage.openshift.io",
		// Exclude VolSync resources as they are managed by VRG
		"replicationsources.volsync.backube",
		"replicationdestinations.volsync.backube",
		// Exclude PVCs and PVs as they are handled separately
		"persistentvolumeclaims",
		"persistentvolumes",
		// Exclude EndpointSlices/Endpoints to prevent Submariner conflicts: see https://github.com/RamenDR/ramen/issues/1889
		"endpointslices.discovery.k8s.io",
		"endpoints",
		// Exclude VolumeSnapshots and VolumeGroupSnapshots from backup
		"volumesnapshots.snapshot.storage.k8s.io",
		"volumegroupsnapshots.groupsnapshot.storage.k8s.io",
	}
}

// getCriticalExcludedResources returns resources that should always be excluded
// to prevent breaking Ramen's internal logic. These will generate warnings if removed from ConfigMap.
func getCriticalExcludedResources() []string {
	return []string{
		"volumereplications.replication.storage.openshift.io",
		"volumegroupreplications.replication.storage.openshift.io",
		"replicationsources.volsync.backube",
		"replicationdestinations.volsync.backube",
	}
}

// ExcludedResourcesManager manages the default excluded resources ConfigMap
type ExcludedResourcesManager struct {
	client    client.Client
	namespace string
	log       logr.Logger
	// Cached excluded resources list
	cachedExclusions []string
}

// NewExcludedResourcesManager creates a new ExcludedResourcesManager
func NewExcludedResourcesManager(client client.Client, namespace string, log logr.Logger) *ExcludedResourcesManager {
	return &ExcludedResourcesManager{
		client:    client,
		namespace: namespace,
		log:       log,
	}
}

// EnsureConfigMap ensures the default excluded resources ConfigMap exists.
// If it doesn't exist, it creates one with the default hardcoded values.
// Returns the list of excluded resources.
func (m *ExcludedResourcesManager) EnsureConfigMap(ctx context.Context) ([]string, error) {
	configMap := &corev1.ConfigMap{}
	configMapKey := types.NamespacedName{
		Namespace: m.namespace,
		Name:      DefaultExcludedResourcesConfigMapName,
	}

	err := m.client.Get(ctx, configMapKey, configMap)
	if err != nil {
		if !errors.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get ConfigMap %s: %w", configMapKey, err)
		}

		// ConfigMap doesn't exist, create it with default values
		m.log.Info("ConfigMap not found, creating with default excluded resources",
			"configMap", DefaultExcludedResourcesConfigMapName,
			"namespace", m.namespace)

		configMap, err = m.createDefaultConfigMap(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to create default ConfigMap: %w", err)
		}
	}

	// Parse and return the excluded resources
	exclusions, err := m.parseExcludedResources(configMap)
	if err != nil {
		m.log.Error(err, "Failed to parse excluded resources from ConfigMap, using hardcoded defaults")

		return getDefaultExcludedResources(), nil
	}

	// Validate and warn about missing critical exclusions
	m.validateCriticalExclusions(exclusions)

	// Cache the exclusions
	m.cachedExclusions = exclusions

	m.log.Info("Loaded excluded resources from ConfigMap",
		"count", len(exclusions),
		"configMap", DefaultExcludedResourcesConfigMapName)

	return exclusions, nil
}

// createDefaultConfigMap creates a ConfigMap with the default excluded resources
func (m *ExcludedResourcesManager) createDefaultConfigMap(ctx context.Context) (*corev1.ConfigMap, error) {
	defaults := getDefaultExcludedResources()

	// Convert to JSON for better structure and validation
	resourcesJSON, err := json.Marshal(defaults)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal default excluded resources: %w", err)
	}

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      DefaultExcludedResourcesConfigMapName,
			Namespace: m.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "ramen-dr-cluster-operator",
				"app.kubernetes.io/component":  "kubeobjects-protection",
				"app.kubernetes.io/managed-by": "ramen-dr-cluster-operator",
			},
			Annotations: map[string]string{
				"ramen.openshift.io/description": "Default list of resources to exclude from Velero backups. " +
					"You can modify this ConfigMap to customize the exclusions. " +
					"Changes will be picked up on the next reconciliation.",
				"ramen.openshift.io/version": "v1",
			},
		},
		Data: map[string]string{
			ExcludedResourcesKey: string(resourcesJSON),
		},
	}

	if err := m.client.Create(ctx, configMap); err != nil {
		return nil, fmt.Errorf("failed to create ConfigMap: %w", err)
	}

	m.log.Info("Created default excluded resources ConfigMap",
		"configMap", DefaultExcludedResourcesConfigMapName,
		"namespace", m.namespace,
		"resources", len(defaults))

	return configMap, nil
}

// parseExcludedResources parses the excluded resources from the ConfigMap
func (m *ExcludedResourcesManager) parseExcludedResources(configMap *corev1.ConfigMap) ([]string, error) {
	resourcesData, ok := configMap.Data[ExcludedResourcesKey]
	if !ok {
		return nil, fmt.Errorf("ConfigMap missing key %s", ExcludedResourcesKey)
	}

	if resourcesData == "" {
		m.log.Info("ConfigMap has empty excluded resources list")

		return []string{}, nil
	}

	var exclusions []string

	// Try to parse as JSON first
	err := json.Unmarshal([]byte(resourcesData), &exclusions)
	if err != nil {
		// Fallback to comma-separated format for backwards compatibility
		m.log.Info("ConfigMap data is not JSON, trying comma-separated format")
		exclusions = m.parseCommaSeparated(resourcesData)
	}

	// Trim whitespace from all entries
	for i := range exclusions {
		exclusions[i] = strings.TrimSpace(exclusions[i])
	}

	// Remove empty entries
	filtered := make([]string, 0, len(exclusions))
	for _, resource := range exclusions {
		if resource != "" {
			filtered = append(filtered, resource)
		}
	}

	return filtered, nil
}

// parseCommaSeparated parses a comma-separated list of resources
func (m *ExcludedResourcesManager) parseCommaSeparated(data string) []string {
	parts := strings.Split(data, ",")
	result := make([]string, 0, len(parts))

	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}

	return result
}

// validateCriticalExclusions checks if critical resources are in the exclusion list
// and logs warnings if they're missing
func (m *ExcludedResourcesManager) validateCriticalExclusions(exclusions []string) {
	criticalResources := getCriticalExcludedResources()

	for _, critical := range criticalResources {
		found := false

		for _, excluded := range exclusions {
			if excluded == critical {
				found = true

				break
			}
		}

		if !found {
			m.log.Info("WARNING: Critical resource not in exclusions list, this may cause issues",
				"resource", critical,
				"reason", "Required by Ramen for proper VRG operation")
		}
	}
}

// GetExcludedResources returns the cached excluded resources.
// If not cached, it loads from ConfigMap.
func (m *ExcludedResourcesManager) GetExcludedResources(ctx context.Context) ([]string, error) {
	if m.cachedExclusions != nil {
		return m.cachedExclusions, nil
	}

	return m.EnsureConfigMap(ctx)
}

// ReloadExcludedResources reloads the excluded resources from the ConfigMap.
// This should be called when the ConfigMap is updated.
func (m *ExcludedResourcesManager) ReloadExcludedResources(ctx context.Context) ([]string, error) {
	m.log.Info("Reloading excluded resources from ConfigMap")

	configMap := &corev1.ConfigMap{}
	configMapKey := types.NamespacedName{
		Namespace: m.namespace,
		Name:      DefaultExcludedResourcesConfigMapName,
	}

	err := m.client.Get(ctx, configMapKey, configMap)
	if err != nil {
		if errors.IsNotFound(err) {
			// ConfigMap was deleted, recreate with defaults
			m.log.Info("ConfigMap was deleted, recreating with defaults")

			return m.EnsureConfigMap(ctx)
		}

		return nil, fmt.Errorf("failed to reload ConfigMap: %w", err)
	}

	exclusions, err := m.parseExcludedResources(configMap)
	if err != nil {
		m.log.Error(err, "Failed to parse excluded resources after reload, using previous cache")

		return m.cachedExclusions, nil
	}

	m.validateCriticalExclusions(exclusions)
	m.cachedExclusions = exclusions

	m.log.Info("Reloaded excluded resources from ConfigMap",
		"count", len(exclusions))

	return exclusions, nil
}
