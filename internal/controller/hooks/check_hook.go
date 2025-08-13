// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type CheckHook struct {
	Hook   *kubeobjects.HookSpec
	Reader client.Reader
}

func (c CheckHook) Execute(log logr.Logger) error {
	hookResult, err := EvaluateCheckHook(c.Reader, c.Hook, log)
	if err != nil {
		log.Error(err, "error occurred while evaluating check hook")

		return err
	}

	hookName := c.Hook.Name + "/" + c.Hook.Chk.Name
	log.Info("check hook executed successfully", "hook", hookName, "result", hookResult)

	if !hookResult && shouldChkHookBeFailedOnError(c.Hook) {
		return fmt.Errorf("stopping workflow as hook %s failed", c.Hook.Name)
	}

	return nil
}

func shouldChkHookBeFailedOnError(hook *kubeobjects.HookSpec) bool {
	// hook.Check.OnError overwrites the feature of hook.OnError -- defaults to fail
	if hook.Chk.OnError != "" && hook.Chk.OnError == "continue" {
		return false
	}

	if hook.OnError != "" && hook.OnError == "continue" {
		return false
	}

	return true
}

func EvaluateCheckHook(k8sReader client.Reader, hook *kubeobjects.HookSpec, log logr.Logger) (bool, error) {
	if hook.LabelSelector == nil && hook.NameSelector == "" {
		return false, fmt.Errorf("either nameSelector or labelSelector should be provided to get resources")
	}

	timeout := getChkHookTimeoutValue(hook)

	pollInterval := pInterval * time.Microsecond

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for int(pollInterval.Seconds()) < timeout {
		select {
		case <-ctx.Done():
			return false, fmt.Errorf("no resource found with nameSelector %s and labelSelector %s: %w",
				hook.NameSelector, hook.LabelSelector, ctx.Err())
		case <-ticker.C:
			objs, err := getResourcesList(k8sReader, hook, log)
			if err != nil {
				return false, err // Some other error occurred, return it
			}

			if len(objs) == 0 {
				pollInterval *= 2
				ticker.Reset(pollInterval)

				continue
			}

			return EvaluateCheckHookForObjects(objs, hook, log)
		}
	}

	return false, nil
}

func EvaluateCheckHookForObjects(objs []client.Object, hook *kubeobjects.HookSpec, log logr.Logger) (bool, error) {
	finalRes := true

	var err error

	for _, obj := range objs {
		data, err := ConvertClientObjectToMap(obj)
		if err != nil {
			log.Info("error converting object to map", "for", hook.Name, "with error", err)

			return false, err
		}

		res, err := EvaluateCheckHookExp(hook.Chk.Condition, data)
		finalRes = finalRes && res

		if err != nil {
			log.Info("error executing check hook", "for", hook.Name, "with error", err)

			return false, fmt.Errorf("error executing check hook %s/%s in namespace %s with selectResource %s: %w",
				hook.Name, hook.Chk.Name, hook.Namespace, hook.SelectResource, err)
		}

		log.Info("check hook executed for", "hook", hook.Name, "resource type", hook.SelectResource, "with object name",
			obj.GetName(), "in ns", obj.GetNamespace(), "with execution result", res)
	}

	return finalRes, err
}

func ConvertClientObjectToMap(obj client.Object) (map[string]interface{}, error) {
	var jsonData map[string]interface{}

	jsonBytes, err := json.Marshal(obj)
	if err != nil {
		return jsonData, fmt.Errorf("error marshaling object %w", err)
	}

	err = json.Unmarshal(jsonBytes, &jsonData)
	if err != nil {
		return jsonData, fmt.Errorf("error unmarshalling object %w", err)
	}

	return jsonData, nil
}

func getResourcesList(k8sReader client.Reader, hook *kubeobjects.HookSpec, log logr.Logger) ([]client.Object, error) {
	resourceList := make([]client.Object, 0)

	uList, err := validateAndGetUnstructedListBasedOnType(hook.SelectResource)
	if err != nil {
		return resourceList, fmt.Errorf("error getting object list based on resource type: %w", err)
	}

	if hook.NameSelector != "" {
		log.Info("getting resources using nameSelector", "nameSelector", hook.NameSelector)

		selectorType, objsUsingNameSelector, err := getResourcesUsingNameSelector(k8sReader, hook, uList)
		if err != nil {
			return resourceList, fmt.Errorf("error getting resources using nameSelector: %w", err)
		}

		log.Info("resources found using nameSelector", "selectorType", selectorType, "count", len(objsUsingNameSelector))
		resourceList = append(resourceList, objsUsingNameSelector...)
	}

	if hook.LabelSelector != nil {
		log.Info("getting resources using labelSelector", "labelSelector", hook.LabelSelector)

		err := getResourcesUsingLabelSelector(k8sReader, hook, uList)
		if err != nil {
			return resourceList, fmt.Errorf("error getting resources using labelSelector: %w", err)
		}

		objsUsingLabelSelector := getObjectsBasedOnType(uList)
		resourceList = append(resourceList, objsUsingLabelSelector...)
	}

	return resourceList, nil
}

func validateAndGetUnstructedListBasedOnType(resourceType string) (*unstructured.UnstructuredList, error) {
	const three = 3

	resourceParts := strings.Split(resourceType, "/")
	if len(resourceParts) != 1 && len(resourceParts) != three {
		return nil, fmt.Errorf("invalid resource type, supported resource types are pod/deployment/statefulset," +
			"plural resource names for core or custom resource in the format <apiGroup>/<apiVersion>/<resourceName>")
	}

	list := &unstructured.UnstructuredList{}

	// process resource given as one of the predefined types pod, deployment or statefulset
	gvkMap := prepareMapForDefinedTypes()
	if gvk, ok := gvkMap[resourceType]; ok {
		list.SetGroupVersionKind(gvk)

		return list, nil
	}

	mapper, err := getRestMapper()
	if err != nil {
		return list, err
	}

	// process resource given in the format <apiGroup>/<apiVersion>/<resource>
	if len(resourceParts) == three {
		gvr := schema.GroupVersionResource{
			Group:    resourceParts[0],
			Version:  resourceParts[1],
			Resource: resourceParts[2],
		}

		gvk, err := convertGVRToGVK(mapper, gvr)
		if err != nil {
			return list, err
		}

		list.SetGroupVersionKind(*gvk)

		return list, nil
	}

	// process resources given as serviceaccounts etc which belong to core apis
	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: resourceType,
	}

	gvk, err := convertGVRToGVK(mapper, gvr)
	if err != nil {
		return list, fmt.Errorf("unrecognized core resource or invalid format: %w", err)
	}

	list.SetGroupVersionKind(*gvk)

	return list, nil
}

func prepareMapForDefinedTypes() map[string]schema.GroupVersionKind {
	gvkMap := make(map[string]schema.GroupVersionKind)
	gvkMap["pod"] = schema.GroupVersionKind{
		Group:   "",
		Version: "v1",
		Kind:    "Pod",
	}

	gvkMap["deployment"] = schema.GroupVersionKind{
		Group:   "apps",
		Version: "v1",
		Kind:    "Deployment",
	}

	gvkMap["statefulset"] = schema.GroupVersionKind{
		Group:   "apps",
		Version: "v1",
		Kind:    "StatefulSet",
	}

	return gvkMap
}

func getRestMapper() (meta.RESTMapper, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	dc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, err
	}

	apiGroupResources, err := restmapper.GetAPIGroupResources(dc)
	if err != nil {
		return nil, err
	}

	return restmapper.NewDiscoveryRESTMapper(apiGroupResources), nil
}

func convertGVRToGVK(mapper meta.RESTMapper, gvr schema.GroupVersionResource) (*schema.GroupVersionKind, error) {
	gvk, err := mapper.KindFor(gvr)
	if err != nil {
		return nil, err
	}

	return &gvk, nil
}

func EvaluateCheckHookExp(booleanExpression string, jsonData interface{}) (bool, error) {
	return evaluateBooleanExpression(booleanExpression, jsonData)
}
