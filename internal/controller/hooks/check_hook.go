// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/go-logr/logr"
	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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

	if !hookResult && shouldHookBeFailedOnError(c.Hook) {
		return fmt.Errorf("stopping workflow as hook %s failed", c.Hook.Name)
	}

	return nil
}

func shouldHookBeFailedOnError(hook *kubeobjects.HookSpec) bool {
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
			return false, fmt.Errorf("timeout waiting for resource %s to be ready: %w", hook.NameSelector, ctx.Err())
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

			return false, err
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

	objList, err := getObjectListBasedOnResourceType(hook.SelectResource)
	if err != nil {
		return resourceList, fmt.Errorf("error getting object list based on resource type: %w", err)
	}

	if hook.NameSelector != "" {
		log.Info("getting resources using nameSelector", "nameSelector", hook.NameSelector)

		objsUsingNameSelector, err := getResourcesUsingNameSelector(k8sReader, hook, objList)
		if err != nil {
			return resourceList, fmt.Errorf("error getting resources using nameSelector: %w", err)
		}

		resourceList = append(resourceList, objsUsingNameSelector...)
	}

	if hook.LabelSelector != nil {
		log.Info("getting resources using labelSelector", "labelSelector", hook.LabelSelector)

		err := getResourcesUsingLabelSelector(k8sReader, hook, objList)
		if err != nil {
			return resourceList, fmt.Errorf("error getting resources using labelSelector: %w", err)
		}

		objsUsingLabelSelector := getObjectsBasedOnType(objList)
		resourceList = append(resourceList, objsUsingLabelSelector...)
	}

	return resourceList, nil
}

func getObjectListBasedOnResourceType(selectResource string) (client.ObjectList, error) {
	switch selectResource {
	case podType:
		return &corev1.PodList{}, nil
	case deploymentType:
		return &appsv1.DeploymentList{}, nil
	case statefulsetType:
		return &appsv1.StatefulSetList{}, nil
	default:
		return nil, fmt.Errorf("unsupported resource type %s", selectResource)
	}
}

func getMatchingPods(pList *corev1.PodList, re *regexp.Regexp) []client.Object {
	objs := make([]client.Object, 0)

	for _, pod := range pList.Items {
		if re.MatchString(pod.Name) {
			objs = append(objs, &pod)
		}
	}

	return objs
}

func getMatchingDeployments(dList *appsv1.DeploymentList, re *regexp.Regexp) []client.Object {
	objs := make([]client.Object, 0)

	for _, pod := range dList.Items {
		if re.MatchString(pod.Name) {
			objs = append(objs, &pod)
		}
	}

	return objs
}

func getMatchingStatefulSets(ssList *appsv1.StatefulSetList, re *regexp.Regexp) []client.Object {
	objs := make([]client.Object, 0)

	for _, pod := range ssList.Items {
		if re.MatchString(pod.Name) {
			objs = append(objs, &pod)
		}
	}

	return objs
}

func EvaluateCheckHookExp(booleanExpression string, jsonData interface{}) (bool, error) {
	return evaluateBooleanExpression(booleanExpression, jsonData)
}
