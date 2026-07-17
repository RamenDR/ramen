// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/util"
)

const (
	// resourceModifierDataKey is the key inside the ConfigMap data holding the YAML rules.
	resourceModifierDataKey = "resource-modifiers.yaml"

	// resourceModifierNameSuffix is appended to VRG name to create unique ConfigMap names
	resourceModifierNameSuffix = "-resource-modifier"

	// resourceModifierComponentLabel identifies ConfigMaps managed by VRG for static IP translation
	resourceModifierComponentLabel = "ramendr.openshift.io/resource-modifier"

	// kubevirtAddressAnnotationPath is the JSON-Pointer used in the Velero patch.
	// The "/" in the annotation key is encoded as "~1" per RFC 6901.
	kubevirtAddressAnnotationPath = "/spec/template/metadata/annotations/network.kubevirt.io~1addresses"

	// kubevirtVMGroupResource is the Velero groupResource string for KubeVirt VMs.
	kubevirtVMGroupResource = "virtualmachines.kubevirt.io"

	resourceModifierVersion = "v1"
)

// resourceModifiers mirrors the subset of
// github.com/vmware-tanzu/velero/internal/resourcemodifiers needed to
// produce a valid ResourceModifier ConfigMap. Copied from velero v1.15.0
// resource_modifiers.go — only the fields Ramen writes are included.
type (
	resourceModifiers struct {
		Version               string                 `json:"version"`
		ResourceModifierRules []resourceModifierRule `json:"resourceModifierRules"`
	}

	resourceModifierRule struct {
		Conditions resourceModifierConditions `json:"conditions"`
		Patches    []jsonPatch                `json:"patches,omitempty"`
	}

	resourceModifierConditions struct {
		GroupResource     string   `json:"groupResource"`
		ResourceNameRegex string   `json:"resourceNameRegex,omitempty"`
		Namespaces        []string `json:"namespaces,omitempty"`
	}

	jsonPatch struct {
		Operation string `json:"op"`
		Path      string `json:"path"`
		Value     string `json:"value,omitempty"`
	}
)

// ────────────────────────────────────────────────────────────────────────────
// Helper functions
// ────────────────────────────────────────────────────────────────────────────

// resourceModifierConfigMapName returns the deterministic ConfigMap name for this VRG
func (v *VRGInstance) resourceModifierConfigMapName() string {
	return v.instance.Name + resourceModifierNameSuffix
}

// resourceModifierLabels returns the labels to identify ResourceModifier ConfigMaps
func (v *VRGInstance) resourceModifierLabels() map[string]string {
	labels := util.OwnerLabels(v.instance)
	labels[resourceModifierComponentLabel] = "true"

	return labels
}

// ────────────────────────────────────────────────────────────────────────────
// Primary: populate StaticIPDiscoveryStatus from protected VMs
// ────────────────────────────────────────────────────────────────────────────

// buildDiscoveredResources filters infos to only VMs with HasStaticIP=true and
// converts each into a DiscoveredResource with per-network IP addresses.
func buildDiscoveredResources(infos []util.VMStaticIPInfo) []ramen.DiscoveredResource {
	resources := make([]ramen.DiscoveredResource, 0, len(infos))

	for i := range infos {
		info := &infos[i]
		if !info.HasStaticIP {
			continue
		}

		networks := make([]ramen.DiscoveredNetwork, 0, len(info.PrimaryAddresses))
		for networkName, addrs := range info.PrimaryAddresses {
			networks = append(networks, ramen.DiscoveredNetwork{
				NetworkName: networkName,
				Addresses:   addrs,
			})
		}

		resources = append(resources, ramen.DiscoveredResource{
			ResourceRef: ramen.ResourceReference{
				Namespace: info.Namespace,
				Name:      info.VMName,
			},
			Networks: networks,
		})
	}

	return resources
}

// ────────────────────────────────────────────────────────────────────────────
// Secondary: create the Velero ResourceModifier ConfigMap from spec translations
// ────────────────────────────────────────────────────────────────────────────

// staticIPResourceModifierReconcile is called during secondary reconciliation.
// It creates or updates the Velero ResourceModifier ConfigMap in the velero namespace
// based on spec.staticIPTranslationSpec.
func (v *VRGInstance) staticIPResourceModifierReconcile(result *ctrl.Result) {
	spec := v.instance.Spec.StaticIPTranslationSpec
	if len(spec.IPTranslations) == 0 {
		return
	}

	v.log.Info("Reconciling static IP ResourceModifier ConfigMap")

	yaml, err := buildResourceModifierYAML(spec.IPTranslations)
	if err != nil {
		v.log.Error(err, "Failed to build ResourceModifier YAML")

		result.Requeue = true

		return
	}

	veleroNS := v.veleroNamespaceName()

	if err := v.createOrUpdateResourceModifierCM(veleroNS, yaml); err != nil {
		v.log.Error(err, "Failed to create/update ResourceModifier ConfigMap")

		result.Requeue = true

		return
	}

	v.log.Info("Static IP ResourceModifier ConfigMap reconciled",
		"namespace", veleroNS, "name", v.resourceModifierConfigMapName())
}

// createOrUpdateResourceModifierCM creates or updates the ResourceModifier ConfigMap
// in the given namespace using deterministic naming based on VRG name.
func (v *VRGInstance) createOrUpdateResourceModifierCM(namespace, yamlData string) error {
	cmName := v.resourceModifierConfigMapName()
	cm := &corev1.ConfigMap{}
	key := types.NamespacedName{Namespace: namespace, Name: cmName}

	err := v.reconciler.Get(v.ctx, key, cm)
	if err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("get ResourceModifier ConfigMap: %w", err)
	}

	if k8serrors.IsNotFound(err) {
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      cmName,
				Labels:    v.resourceModifierLabels(),
			},
			Data: map[string]string{
				resourceModifierDataKey: yamlData,
			},
		}

		// if err := controllerutil.SetControllerReference(v.instance, cm, v.reconciler.Scheme); err != nil {
		// 		return fmt.Errorf("set controller reference on ResourceModifier ConfigMap: %w", err)
		// }

		if err := v.reconciler.Create(v.ctx, cm); err != nil {
			return fmt.Errorf("create ResourceModifier ConfigMap: %w", err)
		}

		return nil
	}

	// Update existing
	patch := client.MergeFrom(cm.DeepCopy())
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}

	cm.Data[resourceModifierDataKey] = yamlData

	if err := v.reconciler.Patch(v.ctx, cm, patch); err != nil {
		return fmt.Errorf("patch ResourceModifier ConfigMap: %w", err)
	}

	return nil
}

func buildResourceModifierYAML(translations []ramen.IPTranslationSpec) (string, error) {
	rules := make([]resourceModifierRule, 0, len(translations))

	for i := range translations {
		t := &translations[i]

		for j := range t.Networks {
			net := &t.Networks[j]

			for k := range net.Addresses {
				addr := &net.Addresses[k]

				patchValue, err := buildAnnotationPatchValue(net.NetworkName, addr.TargetIP)
				if err != nil {
					return "", fmt.Errorf(
						"build patch value for %s/%s network %s: %w",
						t.ResourceRef.Namespace, t.ResourceRef.Name, net.NetworkName, err,
					)
				}

				rules = append(rules, resourceModifierRule{
					Conditions: resourceModifierConditions{
						GroupResource:     kubevirtVMGroupResource,
						ResourceNameRegex: "^" + t.ResourceRef.Name + "$",
						Namespaces:        namespaces(t.ResourceRef.Namespace),
					},
					Patches: []jsonPatch{
						{
							Operation: "replace",
							Path:      kubevirtAddressAnnotationPath,
							Value:     patchValue,
						},
					},
				})
			}
		}
	}

	rm := resourceModifiers{
		Version:               resourceModifierVersion,
		ResourceModifierRules: rules,
	}

	out, err := yaml.Marshal(rm)
	if err != nil {
		return "", fmt.Errorf("marshal ResourceModifiers to YAML: %w", err)
	}

	return string(out), nil
}

// buildAnnotationPatchValue builds the Velero patch value string for the
// network.kubevirt.io/addresses annotation.
//
// The KubeVirt VM template carries the annotation:
//
//	network.kubevirt.io/addresses: {"primary-udn":["192.168.200.25"]}
//
// In the Velero backup YAML the annotation value is a YAML double-quoted string.
// The ResourceModifier patch value must reproduce the exact YAML encoding:
//
//	value: "\"{\\\"primary-udn\\\":[\\\"192.168.200.25\\\"]}\""
//
// Construction:
//  1. Serialize the {network:[ip]} map to a JSON string, e.g.
//     {"primary-udn":["192.168.200.25"]}
//  2. Escape every " in that JSON string as \\\"  (YAML double-quote encoding)
//  3. Wrap the result in \"…\" and then in outer "…" to produce the YAML token
func buildAnnotationPatchValue(networkName, targetIP string) (string, error) {
	// Step 1: JSON-encode the network→ip map
	innerMap := map[string][]string{
		networkName: {targetIP},
	}

	innerJSON, err := json.Marshal(innerMap)
	if err != nil {
		return "", err
	}

	// Step 2: escape each " as \\\" so YAML double-quote decoding yields \".
	// This is the standard Velero ResourceModifier value encoding for JSON annotations.
	escaped := strings.ReplaceAll(string(innerJSON), `"`, `\\\"`)

	// Step 3: wrap: "\"{…}\""
	return fmt.Sprintf(`"\"%s\""`, escaped), nil
}

// ────────────────────────────────────────────────────────────────────────────
// ResourceModifierConfigMapRef returns a TypedLocalObjectReference pointing at
// the ResourceModifier ConfigMap, for use in Velero RestoreSpec.
// ────────────────────────────────────────────────────────────────────────────

func (v *VRGInstance) resourceModifierRef() *corev1.TypedLocalObjectReference {
	return &corev1.TypedLocalObjectReference{
		APIGroup: nil, // core group — same as Velero expects
		Kind:     "ConfigMap",
		Name:     v.resourceModifierConfigMapName(),
	}
}

// hasStaticIPTranslation returns true when the VRG spec includes IP translation rules.
func hasStaticIPTranslation(vrg *ramen.VolumeReplicationGroup) bool {
	return len(vrg.Spec.StaticIPTranslationSpec.IPTranslations) > 0
}

// ensureResourceModifierCM guarantees the Velero ResourceModifier ConfigMap
// exists in the velero namespace before a restore is attempted.
//
// If the ConfigMap is absent it is created from the current spec and result.Requeue
// is set to defer the restore to the next reconcile — giving the API server time
// to persist the object before Velero reads it.
// If the ConfigMap already exists, nothing is changed and the restore can proceed.
func (v *VRGInstance) ensureResourceModifierCM(result *ctrl.Result) error {
	veleroNS := v.veleroNamespaceName()
	cmName := v.resourceModifierConfigMapName()
	key := types.NamespacedName{Namespace: veleroNS, Name: cmName}

	existing := &corev1.ConfigMap{}
	err := v.reconciler.Get(v.ctx, key, existing)

	switch {
	case err == nil:
		// ConfigMap already exists — restore can proceed.
		v.log.Info("ResourceModifier ConfigMap present, proceeding with restore",
			"namespace", veleroNS, "name", cmName)

		return nil

	case !k8serrors.IsNotFound(err):
		// Unexpected API error.
		v.log.Error(err, "Failed to check ResourceModifier ConfigMap existence")

		result.Requeue = true

		return fmt.Errorf("check ResourceModifier ConfigMap: %w", err)
	}

	// ConfigMap is missing — build it from the current spec and requeue so the
	// restore runs in the next cycle after the object is fully persisted.
	v.log.Info("ResourceModifier ConfigMap not found, creating before restore",
		"namespace", veleroNS, "name", cmName)

	v.staticIPResourceModifierReconcile(result)

	// Whether creation succeeded or failed, requeue so the restore path is
	// not attempted in this cycle.
	result.Requeue = true

	return nil
}

// deleteResourceModifierCM removes the ResourceModifier ConfigMap from the velero
// namespace. Called during VRG deletion to clean up owned resources.
// Uses deterministic naming (<vrg-name>-resource-modifier) and verifies ownership labels.
func (v *VRGInstance) deleteResourceModifierCM(ctx context.Context, namespace string) error {
	cmName := v.resourceModifierConfigMapName()
	cm := &corev1.ConfigMap{}
	key := types.NamespacedName{Namespace: namespace, Name: cmName}

	// Get the ConfigMap first to verify ownership
	err := v.reconciler.Get(ctx, key, cm)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			v.log.V(1).Info("ResourceModifier ConfigMap already deleted",
				"namespace", namespace, "name", cmName)

			return nil
		}

		return fmt.Errorf("get ResourceModifier ConfigMap for deletion: %w", err)
	}

	// Verify the ConfigMap has our owner labels before deletion
	expectedLabels := v.resourceModifierLabels()
	for key, expectedValue := range expectedLabels {
		if actualValue, exists := cm.Labels[key]; !exists || actualValue != expectedValue {
			v.log.Info("Skipping deletion of ConfigMap without matching owner labels",
				"namespace", namespace, "name", cmName,
				"expectedLabel", key, "expectedValue", expectedValue, "actualValue", actualValue)

			return nil
		}
	}

	if err := v.reconciler.Delete(ctx, cm); err != nil {
		return fmt.Errorf("delete ResourceModifier ConfigMap: %w", err)
	}

	v.log.Info("ResourceModifier ConfigMap deleted", "namespace", namespace, "name", cmName)

	return nil
}

func namespaces(ns string) []string {
	if ns == "" {
		return nil
	}

	return []string{ns}
}
