// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/oliveagle/jsonpath"
	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
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

func EvaluateCheckHook(k8sClient client.Reader, hook *kubeobjects.HookSpec, log logr.Logger) (bool, error) {
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
			objs, err := getResourcesList(k8sClient, hook, log)
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

func getResourcesList(k8sClient client.Reader, hook *kubeobjects.HookSpec, log logr.Logger) ([]client.Object, error) {
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
		log.Info("getting resources using nameSelector", "nameSelector", hook.NameSelector)

		objsUsingNameSelector, err := getResourcesUsingNameSelector(k8sClient, hook, objList)
		if err != nil {
			return resourceList, fmt.Errorf("error getting resources using nameSelector: %w", err)
		}

		resourceList = append(resourceList, objsUsingNameSelector...)
	}

	if hook.LabelSelector != nil {
		log.Info("getting resources using labelSelector", "labelSelector", hook.LabelSelector)

		objsUsingLabelSelector, err := getResourcesUsingLabelSelector(k8sClient, hook, objList)
		if err != nil {
			return resourceList, fmt.Errorf("error getting resources using labelSelector: %w", err)
		}

		resourceList = append(resourceList, objsUsingLabelSelector...)
	}

	return resourceList, nil
}

func getResourcesUsingLabelSelector(c client.Reader, hook *kubeobjects.HookSpec,
	objList client.ObjectList,
) ([]client.Object, error) {
	filteredObjs := make([]client.Object, 0)

	selector, err := metav1.LabelSelectorAsSelector(hook.LabelSelector)
	if err != nil {
		return filteredObjs, fmt.Errorf("error converting labelSelector to selector")
	}

	listOps := &client.ListOptions{
		Namespace:     hook.Namespace,
		LabelSelector: selector,
	}

	err = c.List(context.Background(), objList, listOps)
	if err != nil {
		return filteredObjs, fmt.Errorf("error listing resources using labelSelector: %w", err)
	}

	return getObjectsBasedOnType(objList), nil
}

func getResourcesUsingNameSelector(c client.Reader, hook *kubeobjects.HookSpec,
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
			return filteredObjs, fmt.Errorf("error listing resources using nameSelector: %w", err)
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
			objs = append(objs, &pod)
		}
	case *appsv1.DeploymentList:
		for _, dep := range v.Items {
			objs = append(objs, &dep)
		}
	case *appsv1.StatefulSetList:
		for _, ss := range v.Items {
			objs = append(objs, &ss)
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
	return evaluateBooleanExpression(booleanExpression, jsonData)
}

func evaluateBooleanExpression(expression string, jsonData interface{}) (bool, error) {
	// handle OR (||)
	orParts := splitOutsideBraces(expression, "||")
	if len(orParts) > 1 {
		for _, part := range orParts {
			result, err := evaluateBooleanExpression(strings.TrimSpace(part), jsonData)
			if err != nil {
				return false, err
			}
			if result {
				return true, nil // Short-circuit: If any part is true
			}
		}
		return false, nil
	}

	// handle AND (&&)
	andParts := splitOutsideBraces(expression, "&&")
	if len(andParts) > 1 {
		for _, part := range andParts {
			result, err := evaluateBooleanExpression(strings.TrimSpace(part), jsonData)
			if err != nil {
				return false, err
			}
			if !result {
				return false, nil // Short-circuit: If any part is false
			}
		}
		return true, nil
	}

	// No logical operator
	op, jsonPaths, err := parseBooleanExpression(expression)
	if err != nil {
		return false, fmt.Errorf("failed to parse boolean expression: %w", err)
	}

	operand := make([]reflect.Value, len(jsonPaths))
	for i, jsonPath := range jsonPaths {
		if strings.HasPrefix(jsonPath, "$") {
			operand[i], err = QueryJSONPath(jsonData, jsonPath)
			if err != nil {
				return false, fmt.Errorf("failed to get value for %v: %w", jsonPath, err)
			}
		} else {
			operand[i] = reflect.ValueOf(jsonPath)
		}
	}

	if !operand[0].IsValid() || !operand[1].IsValid() {
		return false, fmt.Errorf("comparison failed: one of the operands is invalid")
	}

	return compare(operand[0], operand[1], op)
}

func splitOutsideBraces(expression, delimiter string) []string {
	var result []string
	braces := 0
	lastIndex := 0

	for i := 0; i <= len(expression)-len(delimiter); i++ {
		if expression[i] == '{' {
			braces++
		} else if expression[i] == '}' {
			braces--
		}

		if braces == 0 && expression[i:i+len(delimiter)] == delimiter {
			result = append(result, expression[lastIndex:i])
			lastIndex = i + len(delimiter)
		}
	}

	result = append(result, expression[lastIndex:])
	return result
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

	// Convert interface{} to actual type instead of just converting to string
	if a.Kind() == reflect.Interface {
		a = reflect.ValueOf(a.Interface())
	}
	if b.Kind() == reflect.Interface {
		b = reflect.ValueOf(b.Interface())
	}

	// Convert all integer types to float64 for consistency
	if isKindInt(a.Kind()) {
		a = reflect.ValueOf(float64(a.Int()))
	}
	if isKindInt(b.Kind()) {
		b = reflect.ValueOf(float64(b.Int()))
	}

	// Convert numeric strings to float64 for valid comparison
	if a.Kind() == reflect.String && isNumericString(a.String()) {
		num, _ := strconv.ParseFloat(a.String(), 64)
		a = reflect.ValueOf(num)
	}
	if b.Kind() == reflect.String && isNumericString(b.String()) {
		num, _ := strconv.ParseFloat(b.String(), 64)
		b = reflect.ValueOf(num)
	}

	// Convert string "True"/"False" to boolean before comparison
	a = convertToBoolean(a)
	b = convertToBoolean(b)

	// Ensure operands are of the same type before comparison
	if a.Kind() != b.Kind() {
		return false, fmt.Errorf("operands of different kinds can't be compared: %v, %v", a.Kind(), b.Kind())
	}

	// Validate the operator for the given types
	if operator != "==" && operator != "!=" {
		if !isKindStringOrFloat64(a.Kind()) {
			return false, fmt.Errorf("unsupported operands for operator: %v, %v, %s", a.Kind(), b.Kind(), operator)
		}
	}

	// Here, we have two scenarios:
	// 1. operands are either string or float64 and operator is any of the 6
	// 2. operands are of any kind and operator is either == or !=
	// Safety latch: return an error if we encounter any other kind
	if isUnsupportedKind(a.Kind()) || isUnsupportedKind(b.Kind()) {
		return false, fmt.Errorf("unsupported kind for comparison: %v, %v", a.Kind(), b.Kind())
	}

	if isKindBool(a.Kind()) {
		return compareBool(a.Bool(), b.Bool(), operator)
	}

	return compareValues(a.Interface(), b.Interface(), operator)
}

func isKindInt(kind reflect.Kind) bool {
	return kind >= reflect.Int && kind <= reflect.Int64
}

func isNumericString(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

// Convert a string reflect.Value to boolean if it contains "true"/"false"
func convertToBoolean(v reflect.Value) reflect.Value {
	if v.Kind() == reflect.String {
		strVal := strings.TrimSpace(v.String())

		// Remove surrounding double quotes, if present
		if strings.HasPrefix(strVal, "\"") && strings.HasSuffix(strVal, "\"") {
			strVal = strVal[1 : len(strVal)-1]
		}

		// Convert "true"/"false" strings to booleans
		switch strings.ToLower(strVal) {
		case "true":
			return reflect.ValueOf(true)
		case "false":
			return reflect.ValueOf(false)
		}
	}
	return v
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
	// List of valid operators
	operators := []string{"==", "!=", ">=", ">", "<=", "<"}

	// Find the first occurrence of an operator that is outside curly braces
	var operatorIndex int = -1
	var foundOperator string

	for _, operator := range operators {
		index := findOperatorOutsideBraces(booleanExpression, operator)
		if index != -1 && (operatorIndex == -1 || index < operatorIndex) {
			operatorIndex = index
			foundOperator = operator
		}
	}

	if operatorIndex == -1 {
		return "", nil, fmt.Errorf("unable to find a valid boolean operator in expression: %v", booleanExpression)
	}

	// Split using the found operator
	operand1 := strings.TrimSpace(booleanExpression[:operatorIndex])
	operand2 := strings.TrimSpace(booleanExpression[operatorIndex+len(foundOperator):])

	// Remove {}
	if strings.HasPrefix(operand1, "{") && strings.HasSuffix(operand1, "}") {
		operand1 = strings.TrimPrefix(operand1, "{")
		operand1 = strings.TrimSuffix(operand1, "}")
	}

	if strings.HasPrefix(operand2, "{") && strings.HasSuffix(operand2, "}") {
		operand2 = strings.TrimPrefix(operand2, "{")
		operand2 = strings.TrimSuffix(operand2, "}")
	}

	fmt.Printf("Operands in parseBooleanExpression: %s : %s\n", operand1, operand2)

	// Validate JSONPath and literals correctly
	if !IsValidJSONPathExpression(operand1) || !IsValidJSONPathExpression(operand2) {
		return "", nil, fmt.Errorf("invalid JSONPath or literal in boolean expression: %v", booleanExpression)
	}

	return foundOperator, []string{operand1, operand2}, nil
}

func findOperatorOutsideBraces(expression string, operator string) int {
	braces := 0

	for i := 0; i <= len(expression)-len(operator); i++ {
		// Track opening `{` and closing `}`
		if expression[i] == '{' {
			braces++
		} else if expression[i] == '}' {
			braces--
		}

		// Found operator outside `{}`?
		if braces == 0 && expression[i:i+len(operator)] == operator {
			return i
		}
	}

	return -1 // Not found
}

func isLiteralValue(expr string) bool {
	expr = strings.TrimSpace(expr)

	// Allow unwrapped True/False, numbers, quoted strings, and lists
	literalRegex := regexp.MustCompile(`^(True|False|[0-9]+|"[^"]*"|\[.*\])$`)

	// Ensure valid lists (basic check for square brackets inside {})
	if strings.HasPrefix(expr, "[") && strings.HasSuffix(expr, "]") {
		return true
	}

	return literalRegex.MatchString(expr)
}

func IsValidJSONPathExpression(expr string) bool {
	expr = strings.TrimSpace(expr)

	jsonPathRegex := regexp.MustCompile(`^\$[.\w\[\]\(\)\@\=\?\-"']+$`)
	if jsonPathRegex.MatchString(expr) {
		return true
	}

	if isLiteralValue(expr) {
		return true
	}

	return false
}

func QueryJSONPath(data interface{}, jsonPath string) (reflect.Value, error) {
	// Fix `==` issue inside JSONPath library used
	jsonPath = strings.Replace(jsonPath, "==", "=", -1)

	expr, err := jsonpath.Compile(jsonPath)
	if err != nil {
		return reflect.Value{}, fmt.Errorf("failed to compile JSONPath: %w", err)
	}

	result, err := expr.Lookup(data)
	if err != nil {
		return reflect.Value{}, fmt.Errorf("failed to get value using JSONPath: %w", err)
	}

	fmt.Printf("The value QueryJSONPath returned is: %v\n", result)
	val := reflect.ValueOf(result)

	// Extract first element if JSONPath returns a slice
	if val.Kind() == reflect.Slice && val.Len() > 0 {
		val = val.Index(0)
	}

	// Check if value is nil or invalid
	if !val.IsValid() || (val.Kind() == reflect.Ptr && val.IsNil()) {
		return reflect.Value{}, fmt.Errorf("JSONPath %v returned nil or empty", jsonPath)
	}

	return val, nil
}
