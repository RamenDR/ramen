// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/ramendr/ramen/e2e/types"
	"gopkg.in/yaml.v2"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/api/resmap"
	"sigs.k8s.io/kustomize/kyaml/filesys"
)

const fMode = 0o600

type CombinedData map[string]interface{}

// ApplyKustomization builds and applies a kustomization
func ApplyKustomization(ctx types.TestContext, client client.Client, kustomizationPath, namespace string) error {
	resMap, err := buildKustomization(kustomizationPath)
	if err != nil {
		return fmt.Errorf("failed to build kustomization: %w", err)
	}

	return applyResources(ctx, client, resMap, namespace)
}

// CreateKustomizationFile creates a kustomization file
func CreateKustomizationFile(ctx types.TestContext, dir string) error {
	w := ctx.Workload()
	config := ctx.Config()
	yamlData := `resources:
- ` + config.Repo.URL + `/` + w.GetPath() + `?ref=` + w.GetBranch()

	var yamlContent CombinedData

	err := yaml.Unmarshal([]byte(yamlData), &yamlContent)
	if err != nil {
		return err
	}

	patch := w.Kustomize()

	var jsonContent CombinedData

	err = json.Unmarshal([]byte(patch), &jsonContent)
	if err != nil {
		return err
	}

	for key, value := range jsonContent {
		yamlContent[key] = value
	}

	combinedYAML, err := yaml.Marshal(&yamlContent)
	if err != nil {
		return err
	}

	outputFile := dir + "/kustomization.yaml"

	return os.WriteFile(outputFile, combinedYAML, fMode)
}

// DeleteKustomization builds and deletes resources from a kustomization
func DeleteKustomization(ctx types.TestContext, client client.Client, kustomizationPath, namespace string, wait bool) error {
	resMap, err := buildKustomization(kustomizationPath)
	if err != nil {
		return fmt.Errorf("failed to build kustomization: %w", err)
	}

	return deleteResources(ctx, client, resMap, namespace)
}

// applyResources applies resources from a Resource Map to kubernetes objects
func applyResources(ctx types.TestContext, client client.Client, resMap resmap.ResMap, namespace string) error {
	for _, resource := range resMap.Resources() {
		object, err := resource.Map()
		if err != nil {
			return fmt.Errorf("failed to get resource map: %w", err)
		}
		obj := &unstructured.Unstructured{}
		obj.SetUnstructuredContent(object)

		if obj.GetNamespace() == "" && namespace != "" {
			obj.SetNamespace(namespace)
		}

		if err := client.Create(ctx.Context(), obj); err != nil {
			return fmt.Errorf("failed to apply resource %s/%s: %w", obj.GetKind(), obj.GetName(), err)
		}
	}

	ctx.Logger().Info("Applied resources successfully")
	return nil
}

// buildKustomization builds the kustomization and returns a Resource Map
func buildKustomization(kustomizationPath string) (resmap.ResMap, error) {
	fSys := filesys.MakeFsOnDisk()

	options := krusty.MakeDefaultOptions()
	k := krusty.MakeKustomizer(options)

	return k.Run(fSys, kustomizationPath)
}

// deleteResources deletes the kubernetes resources
func deleteResources(ctx types.TestContext, k8sClient client.Client, resMap resmap.ResMap, namespace string) error {
	timeOut := 2 * time.Minute
	pollInterval := 2 * time.Second

	for _, resource := range resMap.Resources() {

		object, err := resource.Map()
		if err != nil {
			return fmt.Errorf("failed to get resource map: %w", err)
		}

		obj := &unstructured.Unstructured{}
		obj.SetUnstructuredContent(object)

		if obj.GetNamespace() == "" && namespace != "" {
			obj.SetNamespace(namespace)
		}

		if err := k8sClient.Delete(ctx.Context(), obj); err != nil {
			if !errors.IsNotFound(err) {
				return fmt.Errorf("failed to delete resource %s/%s: %w", obj.GetKind(), obj.GetName(), err)
			}
			ctx.Logger().Info("Resource already deleted", "kind", obj.GetKind(), "name", obj.GetName())
			continue
		}

		key := client.ObjectKeyFromObject(obj)

		pollErr := wait.PollUntilContextTimeout(ctx.Context(), pollInterval, timeOut, true, func(ctx context.Context) (bool, error) {
			err := k8sClient.Get(ctx, key, obj)
			if errors.IsNotFound(err) {
				return true, nil
			}
			if err != nil {
				return false, fmt.Errorf("error waiting for deletion of %s/%s: %w", obj.GetKind(), obj.GetName(), err)
			}
			return false, nil
		})

		if pollErr != nil {
			return err
		}
	}

	ctx.Logger().Info("Deleted resources successfully")
	return nil
}
