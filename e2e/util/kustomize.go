// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"fmt"
	"io"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/kyaml/filesys"
)

// buildKustomization builds the kustomization and returns YAML bytes
func buildKustomization(kustomizationPath string) ([]byte, error) {
	fSys := filesys.MakeFsOnDisk()

	options := krusty.MakeDefaultOptions()
	k := krusty.MakeKustomizer(options)

	resMap, err := k.Run(fSys, kustomizationPath)
	if err != nil {
		return nil, fmt.Errorf("kustomize build failed: %w", err)
	}

	yaml, err := resMap.AsYaml()
	if err != nil {
		return nil, fmt.Errorf("failed to convert to YAML: %w", err)
	}

	return yaml, nil
}

// createK8sClient creates a dynamic Kubernetes client and REST mapper
func createK8sClient(kubeconfig string) (dynamic.Interface, meta.RESTMapper, error) {
	config, err := clientcmd.LoadFromFile(kubeconfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	restConfig, err := clientcmd.NewDefaultClientConfig(*config, nil).ClientConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create REST config: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(restConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create discovery client: %w", err)
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(discoveryClient))

	return dynamicClient, mapper, nil
}

// ApplyKustomization builds and applies a kustomization using Kubernetes API
func ApplyKustomization(kustomizationPath, namespace, kubeconfig string) error {
	ctx := context.Background()

	yamlData, err := buildKustomization(kustomizationPath)
	if err != nil {
		return fmt.Errorf("failed to build kustomization: %w", err)
	}

	client, mapper, err := createK8sClient(kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	return applyYAML(ctx, client, mapper, yamlData, namespace)
}

// applyYAML parses YAML and applies resources using Kubernetes API
func applyYAML(ctx context.Context, client dynamic.Interface, mapper meta.RESTMapper, yamlData []byte, namespace string) error {
	resources, err := parseYAMLResources(yamlData)
	if err != nil {
		return fmt.Errorf("failed to parse YAML resources: %w", err)
	}

	for _, resource := range resources {
		if err := applyResource(ctx, client, mapper, resource, namespace); err != nil {
			return fmt.Errorf("failed to apply resource %s/%s: %w", resource.GetKind(), resource.GetName(), err)
		}
	}

	fmt.Println("Applied resources successfully")

	return nil
}

// applyResource applies a single resource using the Kubernetes API
func applyResource(ctx context.Context, client dynamic.Interface, mapper meta.RESTMapper, obj *unstructured.Unstructured, namespace string) error {
	if obj.GetNamespace() == "" && namespace != "" {
		obj.SetNamespace(namespace)
	}

	gvk := obj.GroupVersionKind()
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return fmt.Errorf("failed to get REST mapping for %s: %w", gvk, err)
	}

	var resourceInterface dynamic.ResourceInterface
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		resourceInterface = client.Resource(mapping.Resource).Namespace(obj.GetNamespace())
	} else {
		resourceInterface = client.Resource(mapping.Resource)
	}

	existing, err := resourceInterface.Get(ctx, obj.GetName(), metav1.GetOptions{})
	if err != nil {
		_, err = resourceInterface.Create(ctx, obj, metav1.CreateOptions{})
		return err
	}

	obj.SetResourceVersion(existing.GetResourceVersion())
	_, err = resourceInterface.Update(ctx, obj, metav1.UpdateOptions{})
	return err
}

// DeleteKustomization builds and deletes resources from a kustomization using Kubernetes API
func DeleteKustomization(kustomizationPath, namespace, kubeconfig string, wait bool) error {
	ctx := context.Background()

	yamlData, err := buildKustomization(kustomizationPath)
	if err != nil {
		return fmt.Errorf("failed to build kustomization: %w", err)
	}

	client, mapper, err := createK8sClient(kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	return deleteYAML(ctx, client, mapper, yamlData, namespace)
}

// deleteYAML parses YAML and deletes resources using Kubernetes API
func deleteYAML(ctx context.Context, client dynamic.Interface, mapper meta.RESTMapper, yamlData []byte, namespace string) error {
	resources, err := parseYAMLResources(yamlData)
	if err != nil {
		return fmt.Errorf("failed to parse YAML resources: %w", err)
	}

	for _, resource := range resources {
		if err := deleteResource(ctx, client, mapper, resource, namespace); err != nil {
			return fmt.Errorf("failed to delete resource %s/%s: %w", resource.GetKind(), resource.GetName(), err)
		}
	}

	fmt.Println("Deleted resources successfully")

	return nil
}

func deleteResource(ctx context.Context, client dynamic.Interface, mapper meta.RESTMapper, obj *unstructured.Unstructured, namespace string) error {
	if obj.GetNamespace() == "" && namespace != "" {
		obj.SetNamespace(namespace)
	}

	gvk := obj.GroupVersionKind()
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return fmt.Errorf("failed to get REST mapping for %s: %w", gvk, err)
	}

	var resourceInterface dynamic.ResourceInterface
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		resourceInterface = client.Resource(mapping.Resource).Namespace(obj.GetNamespace())
	} else {
		resourceInterface = client.Resource(mapping.Resource)
	}

	return resourceInterface.Delete(ctx, obj.GetName(), metav1.DeleteOptions{})
}

// parseYAMLResources parses multi-document YAML into unstructured objects
func parseYAMLResources(yamlData []byte) ([]*unstructured.Unstructured, error) {
	var resources []*unstructured.Unstructured

	decoder := yaml.NewYAMLToJSONDecoder(strings.NewReader(string(yamlData)))
	for {
		var rawObj runtime.RawExtension
		if err := decoder.Decode(&rawObj); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("failed to decode YAML: %w", err)
		}

		if len(rawObj.Raw) == 0 {
			continue
		}

		obj := &unstructured.Unstructured{}
		if err := obj.UnmarshalJSON(rawObj.Raw); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
		}

		if obj.GetKind() == "" {
			continue
		}

		resources = append(resources, obj)
	}

	return resources, nil
}
