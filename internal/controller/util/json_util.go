// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/util/jsonpath"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultTimeoutValue       = 300
	expectedNumberOfJSONPaths = 2
	podType                   = "pod"
	deploymentType            = "deployment"
	statefulsetType           = "statefulset"
	pInterval                 = 100
)

func EvaluateCheckHook(k8sClient client.Client, hook *kubeobjects.HookSpec, log logr.Logger) (bool, error) {
	if hook.LabelSelector == nil && hook.NameSelector == "" {
		return false, fmt.Errorf("either nameSelector or labelSelector should be provided to get resources")
	}

	timeout := getTimeoutValue(hook)

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
			objs, err := getResourcesList(k8sClient, hook)
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
		res, err := EvaluateCheckHookExp(hook.Chk.Condition, obj)
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

func getResourcesList(k8sClient client.Client, hook *kubeobjects.HookSpec) ([]client.Object, error) {
	resourceList := make([]client.Object, 0)

	var objList client.ObjectList

	switch hook.SelectResource {
	case podType:
		objList = &corev1.PodList{}
	case deploymentType:
		objList = &appsv1.DeploymentList{}
	case statefulsetType:
		objList = &appsv1.StatefulSetList{}
	default:
		return resourceList, fmt.Errorf("unsupported resource type %s", hook.SelectResource)
	}

	if hook.NameSelector != "" {
		objsUsingNameSelector, err := getResourcesUsingNameSelector(k8sClient, hook, objList)
		if err != nil {
			return resourceList, err
		}

		resourceList = append(resourceList, objsUsingNameSelector...)
	}

	if hook.LabelSelector != nil {
		objsUsingLabelSelector, err := getResourcesUsingLabelSelector(k8sClient, hook, objList)
		if err != nil {
			return resourceList, err
		}

		resourceList = append(resourceList, objsUsingLabelSelector...)
	}

	return resourceList, nil
}

func getResourcesUsingLabelSelector(c client.Client, hook *kubeobjects.HookSpec,
	objList client.ObjectList,
) ([]client.Object, error) {
	filteredObjs := make([]client.Object, 0)

	selector, err := metav1.LabelSelectorAsSelector(hook.LabelSelector)
	if err != nil {
		return filteredObjs, fmt.Errorf("error during labelSelector to selector conversion")
	}

	listOps := &client.ListOptions{
		Namespace:     hook.Namespace,
		LabelSelector: selector,
	}

	err = c.List(context.Background(), objList, listOps)
	if err != nil {
		return filteredObjs, err
	}

	return getObjectsBasedOnType(objList), nil
}

func getResourcesUsingNameSelector(c client.Client, hook *kubeobjects.HookSpec,
	objList client.ObjectList,
) ([]client.Object, error) {
	filteredObjs := make([]client.Object, 0)

	var err error

	if isValidK8sName(hook.NameSelector) {
		// use nameSelector for Matching field
		listOps := &client.ListOptions{
			Namespace: hook.Namespace,
			FieldSelector: fields.SelectorFromSet(fields.Set{
				"metadata.name": hook.NameSelector, // needs exact matching with the name
			}),
		}

		err = c.List(context.Background(), objList, listOps)
		if err != nil {
			return filteredObjs, err
		}

		return getObjectsBasedOnType(objList), nil
	} else if isValidRegex(hook.NameSelector) {
		// after listing without the fields selector, match with the regex for filtering
		listOps := &client.ListOptions{
			Namespace: hook.Namespace,
		}

		re, err := regexp.Compile(hook.NameSelector)
		if err != nil {
			return filteredObjs, err
		}

		err = c.List(context.Background(), objList, listOps)
		if err != nil {
			return filteredObjs, err
		}

		return getObjectsBasedOnTypeAndRegex(objList, re), nil
	}

	return filteredObjs, fmt.Errorf("nameSelector is neither distinct name nor regex")
}

// Based on the type of resource, slice of objects is returned.
func getObjectsBasedOnType(objList client.ObjectList) []client.Object {
	objs := make([]client.Object, 0)

	switch v := objList.(type) {
	case *corev1.PodList:
		for _, pod := range v.Items {
			podCopy := pod
			objs = append(objs, &podCopy)
		}
	case *appsv1.DeploymentList:
		for _, dep := range v.Items {
			depCopy := dep
			objs = append(objs, &depCopy)
		}
	case *appsv1.StatefulSetList:
		for _, ss := range v.Items {
			ssCopy := ss
			objs = append(objs, &ssCopy)
		}
	}

	return objs
}

// Based on the type of resource and regex match, slice of objects is returned.
func getObjectsBasedOnTypeAndRegex(objList client.ObjectList, re *regexp.Regexp) []client.Object {
	objs := make([]client.Object, 0)

	switch v := objList.(type) {
	case *corev1.PodList:
		objs = getMatchingPods(v, re)
	case *appsv1.DeploymentList:
		objs = getMatchingDeployments(v, re)
	case *appsv1.StatefulSetList:
		objs = getMatchingStatefulSets(v, re)
	}

	return objs
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

func isValidK8sName(nameSelector string) bool {
	regex := `^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`
	re := regexp.MustCompile(regex)

	return re.MatchString(nameSelector)
}

func isValidRegex(nameSelector string) bool {
	_, err := regexp.Compile(nameSelector)

	return err == nil
}

func getTimeoutValue(hook *kubeobjects.HookSpec) int {
	if hook.Chk.Timeout != 0 {
		return hook.Chk.Timeout
	} else if hook.Timeout != 0 {
		return hook.Timeout
	}
	// 300s is the default value for timeout
	return defaultTimeoutValue
}

func EvaluateCheckHookExp(booleanExpression string, jsonData interface{}) (bool, error) {
	op, jsonPaths, err := parseBooleanExpression(booleanExpression)
	if err != nil {
		return false, fmt.Errorf("failed to parse boolean expression: %w", err)
	}

	operand := make([]reflect.Value, len(jsonPaths))
	for i, jsonPath := range jsonPaths {
		operand[i], err = QueryJSONPath(jsonData, jsonPath)
		if err != nil {
			return false, fmt.Errorf("failed to get value for %v: %w", jsonPath, err)
		}
	}

	return compare(operand[0], operand[1], op)
}

// compare compares two interfaces using the specified operator
//
//nolint:gocognit,gocyclo,cyclop
func compare(a, b reflect.Value, operator string) (bool, error) {
	// convert pointer to interface
	if a.Kind() == reflect.Ptr {
		a = a.Elem()
	}

	if b.Kind() == reflect.Ptr {
		b = b.Elem()
	}

	if a.Kind() == reflect.Int ||
		a.Kind() == reflect.Int8 ||
		a.Kind() == reflect.Int16 ||
		a.Kind() == reflect.Int32 ||
		a.Kind() == reflect.Int64 {
		a = reflect.ValueOf(float64(a.Int()))
	}

	if b.Kind() == reflect.Int ||
		b.Kind() == reflect.Int8 ||
		b.Kind() == reflect.Int16 ||
		b.Kind() == reflect.Int32 ||
		b.Kind() == reflect.Int64 {
		b = reflect.ValueOf(float64(b.Int()))
	}

	// if they are just an interface then we convert them to string
	if a.Kind() == reflect.Interface {
		a = reflect.ValueOf(fmt.Sprintf("%v", a.Interface()))
	}

	if b.Kind() == reflect.Interface {
		b = reflect.ValueOf(fmt.Sprintf("%v", b.Interface()))
	}

	if a.Kind() != b.Kind() {
		return false, fmt.Errorf("operands of different kinds can't be compared %v, %v", a.Kind(), b.Kind())
	}

	// At this point, both a and b should be of the same kind
	if operator != "==" && operator != "!=" {
		if !isKindStringOrFloat64(a.Kind()) || !isKindStringOrFloat64(b.Kind()) {
			return false, fmt.Errorf("operands not supported for operator: %v, %v, %s",
				a.Kind(), b.Kind(), operator)
		}
	}

	// Here, we have two scenarios:
	// 1. operands are either string or float64 and operator is any of the 6
	// 2. operands are of any kind and operator is either == or !=
	// Safety latch: return an error if we encounter any other kind
	if isUnsupportedKind(a.Kind()) || isUnsupportedKind(b.Kind()) {
		return false, fmt.Errorf("unsupported kind for comparison: %v, %v", a.Kind(), b.Kind())
	}

	if isKindBool(a.Kind()) && isKindBool(b.Kind()) {
		return compareBool(a.Bool(), b.Bool(), operator)
	}

	return compareValues(a.Interface(), b.Interface(), operator)
}

func compareBool(a, b bool, operator string) (bool, error) {
	switch operator {
	case "==":
		return a == b, nil
	case "!=":
		return a != b, nil
	default:
		return false, fmt.Errorf("unknown operator: %s", operator)
	}
}

func compareString(a, b, operator string) (bool, error) {
	switch operator {
	case "==":
		return a == b, nil
	case "!=":
		return a != b, nil
	default:
		return false, fmt.Errorf("unknown operator: %s", operator)
	}
}

func compareFloat(a, b float64, operator string) (bool, error) {
	switch operator {
	case "==":
		return a == b, nil
	case "!=":
		return a != b, nil
	case "<":
		return a < b, nil
	case ">":
		return a > b, nil
	case "<=":
		return a <= b, nil
	case ">=":
		return a >= b, nil
	default:
		return false, fmt.Errorf("unknown operator: %s", operator)
	}
}

func compareValues(val1, val2 interface{}, operator string) (bool, error) {
	switch v1 := val1.(type) {
	case float64:
		v2, ok := val2.(float64)
		if !ok {
			return false, fmt.Errorf("types mismatch: expected %T, actual: %T", val1, val2)
		}

		return compareFloat(v1, v2, operator)
	case string:
		v2, ok := val2.(string)
		if !ok {
			return false, fmt.Errorf("types mismatch: expected %T, actual: %T", val1, val2)
		}

		return compareString(v1, v2, operator)
	case bool:
		v2, ok := val2.(bool)
		if !ok {
			return false, fmt.Errorf("types mismatch: expected %T, actual: %T", val1, val2)
		}

		return compareBool(v1, v2, operator)
	}

	return false, fmt.Errorf("unsupported type or operator, types are %T and %T, operator is %s",
		val1, val2, operator)
}

func isKindString(kind reflect.Kind) bool {
	return kind == reflect.String
}

func isKindFloat64(kind reflect.Kind) bool {
	return kind == reflect.Float64
}

func isKindBool(kind reflect.Kind) bool {
	return kind == reflect.Bool
}

func isUnsupportedKind(kind reflect.Kind) bool {
	return kind != reflect.String && kind != reflect.Float64 && kind != reflect.Bool
}

func isKindStringOrFloat64(kind reflect.Kind) bool {
	return isKindString(kind) || isKindFloat64(kind)
}

func parseBooleanExpression(booleanExpression string) (op string, jsonPaths []string, err error) {
	operators := []string{"==", "!=", ">=", ">", "<=", "<"}

	// TODO
	// If one of the jsonpaths have the operator that we are looking for,
	// then strings.Split with split it at that point and not in the middle.

	for _, op := range operators {
		exprs := strings.Split(booleanExpression, op)

		jsonPaths = trimLeadingTrailingWhiteSpace(exprs)

		if len(exprs) == expectedNumberOfJSONPaths &&
			IsValidJSONPathExpression(jsonPaths[0]) &&
			IsValidJSONPathExpression(jsonPaths[1]) {
			return op, jsonPaths, nil
		}
	}

	return "", []string{}, fmt.Errorf("unable to parse boolean expression %v", booleanExpression)
}

func IsValidJSONPathExpression(expr string) bool {
	jp := jsonpath.New("validator").AllowMissingKeys(true)

	err := jp.Parse(expr)
	if err != nil {
		return false
	}

	_, err = QueryJSONPath("{}", expr)

	return err == nil
}

func QueryJSONPath(data interface{}, jsonPath string) (reflect.Value, error) {
	jp := jsonpath.New("extractor").AllowMissingKeys(true)

	if err := jp.Parse(jsonPath); err != nil {
		return reflect.Value{}, fmt.Errorf("failed to get value invalid jsonpath %w", err)
	}

	results, err := jp.FindResults(data)
	if err != nil {
		return reflect.Value{}, fmt.Errorf("failed to get value from data using jsonpath %w", err)
	}

	if len(results) == 0 || len(results[0]) == 0 {
		return reflect.Value{}, nil
	}

	return results[0][0], nil
}

func trimLeadingTrailingWhiteSpace(paths []string) []string {
	tPaths := make([]string, len(paths))

	for i, path := range paths {
		tpath := strings.TrimSpace(path)
		tPaths[i] = tpath
	}

	return tPaths
}
