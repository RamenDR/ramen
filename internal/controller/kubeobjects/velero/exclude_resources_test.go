// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package velero_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/ramendr/ramen/internal/controller/kubeobjects/velero"
)

const (
	testNamespace = "test-namespace"
)

// setupFakeClient creates a fake client with corev1 scheme
func setupFakeClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()

	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	require.NoError(t, err)

	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

// createConfigMapWithJSON creates a ConfigMap with JSON-formatted excluded resources
func createConfigMapWithJSON(namespace string, resources []string) *corev1.ConfigMap {
	resourcesJSON, err := json.Marshal(resources)
	if err != nil {
		return nil
	}

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      velero.DefaultExcludedResourcesConfigMapName,
			Namespace: namespace,
		},
		Data: map[string]string{
			velero.ExcludedResourcesKey: string(resourcesJSON),
		},
	}
}

// createConfigMapWithCommaSeparated creates a ConfigMap with comma-separated excluded resources
func createConfigMapWithCommaSeparated(namespace string, resourcesStr string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      velero.DefaultExcludedResourcesConfigMapName,
			Namespace: namespace,
		},
		Data: map[string]string{
			velero.ExcludedResourcesKey: resourcesStr,
		},
	}
}

// TestEnsureConfigMap_CreatesConfigMapWhenNotExists tests that the manager creates
// a ConfigMap with default resources when it doesn't exist
func TestEnsureConfigMap_CreatesConfigMapWhenNotExists(t *testing.T) {
	fakeClient := setupFakeClient(t)
	logger := zap.New(zap.UseDevMode(true))
	manager := velero.NewExcludedResourcesManager(fakeClient, testNamespace, logger)

	ctx := context.Background()
	exclusions, err := manager.EnsureConfigMap(ctx)

	require.NoError(t, err)
	assert.NotEmpty(t, exclusions)

	// Verify ConfigMap was created
	configMap := &corev1.ConfigMap{}
	configMapKey := types.NamespacedName{
		Namespace: testNamespace,
		Name:      velero.DefaultExcludedResourcesConfigMapName,
	}
	err = fakeClient.Get(ctx, configMapKey, configMap)
	require.NoError(t, err)

	// Verify it has the expected labels and annotations
	assert.Equal(t, "ramen-dr-cluster-operator", configMap.Labels["app.kubernetes.io/name"])
	assert.Equal(t, "kubeobjects-protection", configMap.Labels["app.kubernetes.io/component"])
	assert.Contains(t, configMap.Annotations["ramen.openshift.io/description"], "Default list of resources")

	// Verify the data is valid JSON
	var parsedResources []string

	err = json.Unmarshal([]byte(configMap.Data[velero.ExcludedResourcesKey]), &parsedResources)
	require.NoError(t, err)
	assert.NotEmpty(t, parsedResources)

	// Verify it includes critical resources
	assert.Contains(t, parsedResources, "volumereplications.replication.storage.openshift.io")
	assert.Contains(t, parsedResources, "replicationsources.volsync.backube")
}

// TestEnsureConfigMap_ReturnsExistingConfigMapData tests that the manager returns
// existing ConfigMap data when it already exists
func TestEnsureConfigMap_ReturnsExistingConfigMapData(t *testing.T) {
	customResources := []string{
		"volumereplications.replication.storage.openshift.io",
		"customresource.example.com",
	}
	existingConfigMap := createConfigMapWithJSON(testNamespace, customResources)
	fakeClient := setupFakeClient(t, existingConfigMap)
	logger := zap.New(zap.UseDevMode(true))
	manager := velero.NewExcludedResourcesManager(fakeClient, testNamespace, logger)

	ctx := context.Background()
	exclusions, err := manager.EnsureConfigMap(ctx)

	require.NoError(t, err)
	assert.Equal(t, customResources, exclusions)
}

// TestEnsureConfigMap_ParsesJSONFormat tests that the manager correctly parses
// JSON-formatted excluded resources
func TestEnsureConfigMap_ParsesJSONFormat(t *testing.T) {
	expectedResources := []string{
		"resource1.group.io",
		"resource2.group.io",
		"persistentvolumeclaims",
	}
	existingConfigMap := createConfigMapWithJSON(testNamespace, expectedResources)
	fakeClient := setupFakeClient(t, existingConfigMap)
	logger := zap.New(zap.UseDevMode(true))
	manager := velero.NewExcludedResourcesManager(fakeClient, testNamespace, logger)

	ctx := context.Background()
	exclusions, err := manager.EnsureConfigMap(ctx)

	require.NoError(t, err)
	assert.Equal(t, expectedResources, exclusions)
}

// TestEnsureConfigMap_ParsesCommaSeparatedFormat tests backward compatibility
// with comma-separated format
func TestEnsureConfigMap_ParsesCommaSeparatedFormat(t *testing.T) {
	resourcesStr := "resource1.group.io, resource2.group.io, persistentvolumeclaims"
	expectedResources := []string{
		"resource1.group.io",
		"resource2.group.io",
		"persistentvolumeclaims",
	}
	existingConfigMap := createConfigMapWithCommaSeparated(testNamespace, resourcesStr)
	fakeClient := setupFakeClient(t, existingConfigMap)
	logger := zap.New(zap.UseDevMode(true))
	manager := velero.NewExcludedResourcesManager(fakeClient, testNamespace, logger)

	ctx := context.Background()
	exclusions, err := manager.EnsureConfigMap(ctx)

	require.NoError(t, err)
	assert.Equal(t, expectedResources, exclusions)
}

// TestEnsureConfigMap_HandlesEmptyConfigMap tests that empty ConfigMap returns empty list
func TestEnsureConfigMap_HandlesEmptyConfigMap(t *testing.T) {
	existingConfigMap := createConfigMapWithJSON(testNamespace, []string{})
	fakeClient := setupFakeClient(t, existingConfigMap)
	logger := zap.New(zap.UseDevMode(true))
	manager := velero.NewExcludedResourcesManager(fakeClient, testNamespace, logger)

	ctx := context.Background()
	exclusions, err := manager.EnsureConfigMap(ctx)

	require.NoError(t, err)
	assert.Empty(t, exclusions)
}

// TestEnsureConfigMap_HandlesWhitespaceInCommaSeparated tests that whitespace
// is properly trimmed in comma-separated format
func TestEnsureConfigMap_HandlesWhitespaceInCommaSeparated(t *testing.T) {
	resourcesStr := "  resource1.group.io  ,  resource2.group.io  ,  , persistentvolumeclaims  "
	expectedResources := []string{
		"resource1.group.io",
		"resource2.group.io",
		"persistentvolumeclaims",
	}
	existingConfigMap := createConfigMapWithCommaSeparated(testNamespace, resourcesStr)
	fakeClient := setupFakeClient(t, existingConfigMap)
	logger := zap.New(zap.UseDevMode(true))
	manager := velero.NewExcludedResourcesManager(fakeClient, testNamespace, logger)

	ctx := context.Background()
	exclusions, err := manager.EnsureConfigMap(ctx)

	require.NoError(t, err)
	assert.Equal(t, expectedResources, exclusions)
}

// TestEnsureConfigMap_HandlesMissingResourcesKey tests fallback to defaults
// when ConfigMap exists but missing the resources key
func TestEnsureConfigMap_HandlesMissingResourcesKey(t *testing.T) {
	existingConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      velero.DefaultExcludedResourcesConfigMapName,
			Namespace: testNamespace,
		},
		Data: map[string]string{
			"other-key": "other-value",
		},
	}
	fakeClient := setupFakeClient(t, existingConfigMap)
	logger := zap.New(zap.UseDevMode(true))
	manager := velero.NewExcludedResourcesManager(fakeClient, testNamespace, logger)

	ctx := context.Background()
	exclusions, err := manager.EnsureConfigMap(ctx)

	// Should not error, but fall back to defaults
	require.NoError(t, err)
	assert.NotEmpty(t, exclusions)
	// Should include critical resources from defaults
	assert.Contains(t, exclusions, "volumereplications.replication.storage.openshift.io")
}

// TestEnsureConfigMap_HandlesInvalidJSON tests fallback to comma-separated
// when JSON parsing fails
func TestEnsureConfigMap_HandlesInvalidJSON(t *testing.T) {
	existingConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      velero.DefaultExcludedResourcesConfigMapName,
			Namespace: testNamespace,
		},
		Data: map[string]string{
			velero.ExcludedResourcesKey: "resource1, resource2", // Not JSON
		},
	}
	fakeClient := setupFakeClient(t, existingConfigMap)
	logger := zap.New(zap.UseDevMode(true))
	manager := velero.NewExcludedResourcesManager(fakeClient, testNamespace, logger)

	ctx := context.Background()
	exclusions, err := manager.EnsureConfigMap(ctx)

	require.NoError(t, err)
	assert.Equal(t, []string{"resource1", "resource2"}, exclusions)
}

// TestGetExcludedResources_UsesCachedValue tests that GetExcludedResources
// returns cached value without hitting the API
func TestGetExcludedResources_UsesCachedValue(t *testing.T) {
	customResources := []string{"cached-resource1", "cached-resource2"}
	existingConfigMap := createConfigMapWithJSON(testNamespace, customResources)
	fakeClient := setupFakeClient(t, existingConfigMap)
	logger := zap.New(zap.UseDevMode(true))
	manager := velero.NewExcludedResourcesManager(fakeClient, testNamespace, logger)

	ctx := context.Background()

	// First call to populate cache
	exclusions1, err := manager.EnsureConfigMap(ctx)
	require.NoError(t, err)
	assert.Equal(t, customResources, exclusions1)

	// Delete the ConfigMap to ensure we're using cache
	err = fakeClient.Delete(ctx, existingConfigMap)
	require.NoError(t, err)

	// GetExcludedResources should return cached value
	exclusions2, err := manager.GetExcludedResources(ctx)
	require.NoError(t, err)
	assert.Equal(t, customResources, exclusions2)
}

// TestGetExcludedResources_LoadsWhenNotCached tests that GetExcludedResources
// loads from ConfigMap when cache is empty
func TestGetExcludedResources_LoadsWhenNotCached(t *testing.T) {
	customResources := []string{"resource1", "resource2"}
	existingConfigMap := createConfigMapWithJSON(testNamespace, customResources)
	fakeClient := setupFakeClient(t, existingConfigMap)
	logger := zap.New(zap.UseDevMode(true))
	manager := velero.NewExcludedResourcesManager(fakeClient, testNamespace, logger)

	ctx := context.Background()

	// Call GetExcludedResources without EnsureConfigMap first
	exclusions, err := manager.GetExcludedResources(ctx)
	require.NoError(t, err)
	assert.Equal(t, customResources, exclusions)
}

// TestReloadExcludedResources_UpdatesCache tests that ReloadExcludedResources
// refreshes the cache from the ConfigMap
func TestReloadExcludedResources_UpdatesCache(t *testing.T) {
	initialResources := []string{"resource1", "resource2"}
	existingConfigMap := createConfigMapWithJSON(testNamespace, initialResources)
	fakeClient := setupFakeClient(t, existingConfigMap)
	logger := zap.New(zap.UseDevMode(true))
	manager := velero.NewExcludedResourcesManager(fakeClient, testNamespace, logger)

	ctx := context.Background()

	// First load
	exclusions1, err := manager.EnsureConfigMap(ctx)
	require.NoError(t, err)
	assert.Equal(t, initialResources, exclusions1)

	// Update ConfigMap
	updatedResources := []string{"updated-resource1", "updated-resource2", "updated-resource3"}

	updatedJSON, err := json.Marshal(updatedResources)
	if err != nil {
		require.NoError(t, err)
	}

	existingConfigMap.Data[velero.ExcludedResourcesKey] = string(updatedJSON)
	err = fakeClient.Update(ctx, existingConfigMap)
	require.NoError(t, err)

	// Reload and verify updated resources
	exclusions2, err := manager.ReloadExcludedResources(ctx)
	require.NoError(t, err)
	assert.Equal(t, updatedResources, exclusions2)

	// Verify cache was updated
	exclusions3, err := manager.GetExcludedResources(ctx)
	require.NoError(t, err)
	assert.Equal(t, updatedResources, exclusions3)
}

// TestReloadExcludedResources_RecreatesDeletedConfigMap tests that
// ReloadExcludedResources recreates ConfigMap if it was deleted
func TestReloadExcludedResources_RecreatesDeletedConfigMap(t *testing.T) {
	initialResources := []string{"resource1", "resource2"}
	existingConfigMap := createConfigMapWithJSON(testNamespace, initialResources)
	fakeClient := setupFakeClient(t, existingConfigMap)
	logger := zap.New(zap.UseDevMode(true))
	manager := velero.NewExcludedResourcesManager(fakeClient, testNamespace, logger)

	ctx := context.Background()

	// First load
	exclusions1, err := manager.EnsureConfigMap(ctx)
	require.NoError(t, err)
	assert.Equal(t, initialResources, exclusions1)

	// Delete ConfigMap
	err = fakeClient.Delete(ctx, existingConfigMap)
	require.NoError(t, err)

	// Verify ConfigMap is deleted
	configMapKey := types.NamespacedName{
		Namespace: testNamespace,
		Name:      velero.DefaultExcludedResourcesConfigMapName,
	}
	err = fakeClient.Get(ctx, configMapKey, &corev1.ConfigMap{})
	assert.True(t, errors.IsNotFound(err))

	// Reload should recreate with defaults
	exclusions2, err := manager.ReloadExcludedResources(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, exclusions2)

	// Verify ConfigMap was recreated
	recreatedConfigMap := &corev1.ConfigMap{}
	err = fakeClient.Get(ctx, configMapKey, recreatedConfigMap)
	require.NoError(t, err)
	assert.NotNil(t, recreatedConfigMap)
}

// TestReloadExcludedResources_FallbackOnParseError tests that ReloadExcludedResources
// falls back to cached value when parse error occurs
func TestReloadExcludedResources_FallbackOnParseError(t *testing.T) {
	initialResources := []string{"resource1", "resource2"}
	existingConfigMap := createConfigMapWithJSON(testNamespace, initialResources)
	fakeClient := setupFakeClient(t, existingConfigMap)
	logger := zap.New(zap.UseDevMode(true))
	manager := velero.NewExcludedResourcesManager(fakeClient, testNamespace, logger)

	ctx := context.Background()

	// First load to populate cache
	exclusions1, err := manager.EnsureConfigMap(ctx)
	require.NoError(t, err)
	assert.Equal(t, initialResources, exclusions1)

	// Update ConfigMap to remove the resources key (will cause parse error)
	existingConfigMap.Data = map[string]string{
		"wrong-key": "wrong-value",
	}
	err = fakeClient.Update(ctx, existingConfigMap)
	require.NoError(t, err)

	// Reload should return cached value on parse error
	exclusions2, err := manager.ReloadExcludedResources(ctx)
	require.NoError(t, err)
	assert.Equal(t, initialResources, exclusions2)
}

// TestEnsureConfigMap_ValidatesCriticalResources tests that missing critical
// resources are logged (warning behavior)
func TestEnsureConfigMap_ValidatesCriticalResources(t *testing.T) {
	// ConfigMap without critical resources
	resourcesWithoutCritical := []string{
		"persistentvolumeclaims",
		"persistentvolumes",
	}
	existingConfigMap := createConfigMapWithJSON(testNamespace, resourcesWithoutCritical)
	fakeClient := setupFakeClient(t, existingConfigMap)
	logger := zap.New(zap.UseDevMode(true))
	manager := velero.NewExcludedResourcesManager(fakeClient, testNamespace, logger)

	ctx := context.Background()

	// This should succeed but log warnings
	exclusions, err := manager.EnsureConfigMap(ctx)
	require.NoError(t, err)
	assert.Equal(t, resourcesWithoutCritical, exclusions)
	// Note: We can't easily test log output without a custom logger,
	// but at least verify it doesn't error out
}

// TestEnsureConfigMap_ConfigMapWithCriticalResources tests that all critical
// resources are properly recognized
func TestEnsureConfigMap_ConfigMapWithCriticalResources(t *testing.T) {
	resourcesWithCritical := []string{
		"volumereplications.replication.storage.openshift.io",
		"volumegroupreplications.replication.storage.openshift.io",
		"replicationsources.volsync.backube",
		"replicationdestinations.volsync.backube",
		"persistentvolumeclaims",
	}
	existingConfigMap := createConfigMapWithJSON(testNamespace, resourcesWithCritical)
	fakeClient := setupFakeClient(t, existingConfigMap)
	logger := zap.New(zap.UseDevMode(true))
	manager := velero.NewExcludedResourcesManager(fakeClient, testNamespace, logger)

	ctx := context.Background()

	exclusions, err := manager.EnsureConfigMap(ctx)
	require.NoError(t, err)
	assert.Equal(t, resourcesWithCritical, exclusions)
	// All critical resources are present, so no warnings
}

// TestEnsureConfigMap_MultipleNamespaces tests that managers with different
// namespaces operate independently
func TestEnsureConfigMap_MultipleNamespaces(t *testing.T) {
	namespace1 := "namespace1"
	namespace2 := "namespace2"

	resources1 := []string{"resource1"}
	resources2 := []string{"resource2"}

	configMap1 := createConfigMapWithJSON(namespace1, resources1)
	configMap2 := createConfigMapWithJSON(namespace2, resources2)

	fakeClient := setupFakeClient(t, configMap1, configMap2)
	logger := zap.New(zap.UseDevMode(true))

	manager1 := velero.NewExcludedResourcesManager(fakeClient, namespace1, logger)
	manager2 := velero.NewExcludedResourcesManager(fakeClient, namespace2, logger)

	ctx := context.Background()

	exclusions1, err := manager1.EnsureConfigMap(ctx)
	require.NoError(t, err)
	assert.Equal(t, resources1, exclusions1)

	exclusions2, err := manager2.EnsureConfigMap(ctx)
	require.NoError(t, err)
	assert.Equal(t, resources2, exclusions2)
}

// TestParseExcludedResources_FiltersEmptyEntries tests that empty entries
// from JSON arrays are filtered out
func TestParseExcludedResources_FiltersEmptyEntries(t *testing.T) {
	// JSON with empty strings
	resourcesJSON := `["resource1", "", "resource2", "  ", "resource3"]`
	existingConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      velero.DefaultExcludedResourcesConfigMapName,
			Namespace: testNamespace,
		},
		Data: map[string]string{
			velero.ExcludedResourcesKey: resourcesJSON,
		},
	}
	fakeClient := setupFakeClient(t, existingConfigMap)
	logger := zap.New(zap.UseDevMode(true))
	manager := velero.NewExcludedResourcesManager(fakeClient, testNamespace, logger)

	ctx := context.Background()
	exclusions, err := manager.EnsureConfigMap(ctx)

	require.NoError(t, err)
	assert.Equal(t, []string{"resource1", "resource2", "resource3"}, exclusions)
}
