package util

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/jsonpath"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func EvaluateCheckHook(client client.Client, hook *kubeobjects.HookSpec, log logr.Logger) (bool, error) {
	timeout := getTimeoutValue(hook)
	nsScopedName := types.NamespacedName{
		Namespace: hook.Namespace,
		Name:      hook.NameSelector,
	}
	switch hook.SelectResource {
	case "pod":
		// handle pod type
		resource := &corev1.Pod{}
		err := WaitUntilResourceExists(client, resource, nsScopedName, time.Duration(timeout)*time.Second)
		if err != nil {
			return false, err
		}
		podBytes, err := json.Marshal(resource)
		if err != nil {
			return false, err
		}
		jsonData := make(map[string]interface{})
		err = json.Unmarshal(podBytes, &jsonData)
		if err != nil {
			return false, err
		}
		return evaluateCheckHookExp(hook.Chk.Condition, resource)
	case "deployment":
		// handle deployment type
		resource := &appsv1.Deployment{}
		err := WaitUntilResourceExists(client, resource, nsScopedName, time.Duration(timeout)*time.Second)
		if err != nil {
			return false, err
		}
		depBytes, err := json.Marshal(resource)
		if err != nil {
			return false, err
		}
		jsonData := make(map[string]interface{})
		err = json.Unmarshal(depBytes, &jsonData)
		if err != nil {
			return false, err
		}
		return evaluateCheckHookExp(hook.Chk.Condition, resource)
	case "statefulset":
		// handle statefulset type
		resource := &appsv1.StatefulSet{}
		err := WaitUntilResourceExists(client, resource, nsScopedName, time.Duration(timeout)*time.Second)
		if err != nil {
			return false, err
		}
		statefulSetBytes, err := json.Marshal(resource)
		if err != nil {
			return false, err
		}
		jsonData := make(map[string]interface{})
		err = json.Unmarshal(statefulSetBytes, &jsonData)
		if err != nil {
			return false, err
		}
		return evaluateCheckHookExp(hook.Chk.Condition, resource)
	}

	return false, nil
}

func WaitUntilResourceExists(client client.Client, obj client.Object, nsScopedName types.NamespacedName,
	timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond) // Poll every 100 milliseconds
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for resource %s to be ready: %w", nsScopedName.Name, ctx.Err())
		case <-ticker.C:
			err := client.Get(context.Background(), nsScopedName, obj)
			if err != nil {
				if k8serrors.IsNotFound(err) {
					// Resource not found, continue polling
					continue
				}

				return err // Some other error occurred, return it
			}

			return nil // Resource is ready
		}
	}
}

func getTimeoutValue(hook *kubeobjects.HookSpec) int {
	if hook.Chk.Timeout != 0 {
		return hook.Chk.Timeout
	} else if hook.Timeout != 0 {
		return hook.Timeout
	} else {
		// 300s is the default value for timeout
		return 300
	}
}

func evaluateCheckHookExp(booleanExpression string, jsonData interface{}) (bool, error) {
	op, jsonPaths, err := parseBooleanExpression(booleanExpression)
	if err != nil {
		return false, fmt.Errorf("failed to parse boolean expression: %w", err)
	}

	operand := make([]reflect.Value, 2)
	for i, jsonPath := range jsonPaths {
		operand[i], err = QueryJsonPath(jsonData, jsonPath)
		if err != nil {
			return false, fmt.Errorf("failed to get value for %v: %w", jsonPath, err)
		}
	}

	return compare(operand[0], operand[1], op)
}

// compare compares two interfaces using the specified operator
func compare(a, b reflect.Value, operator string) (bool, error) {
	// convert pointer to interface
	if a.Kind() == reflect.Ptr {
		a = a.Elem()
	}

	if b.Kind() == reflect.Ptr {
		b = b.Elem()
	}

	// convert int32 to float64
	if a.Kind() == reflect.Int32 {
		a = reflect.ValueOf(float64(a.Int()))
	}

	if b.Kind() == reflect.Int32 {
		b = reflect.ValueOf(float64(b.Int()))
	}

	// If one is int and another is float64
	// then convert int to float64 for comparison
	if a.Kind() == reflect.Int && b.Kind() == reflect.Float64 {
		a = reflect.ValueOf(float64(a.Int()))
	}
	if a.Kind() == reflect.Float64 && b.Kind() == reflect.Int {
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

	// Safety latch:
	// We will initially support only string, float64 and bool
	// return an error if we encounter any other kind
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

func compareValues(val1, val2 interface{}, operator string) (bool, error) {
	switch v1 := val1.(type) {
	case float64:
		v2, ok := val2.(float64)
		if !ok {
			return false, fmt.Errorf("mismatched types")
		}
		switch operator {
		case "==":
			return v1 == v2, nil
		case "!=":
			return v1 != v2, nil
		case "<":
			return v1 < v2, nil
		case ">":
			return v1 > v2, nil
		case "<=":
			return v1 <= v2, nil
		case ">=":
			return v1 >= v2, nil
		}
	case string:
		v2, ok := val2.(string)
		if !ok {
			return false, fmt.Errorf("mismatched types")
		}
		switch operator {
		case "==":
			return v1 == v2, nil
		case "!=":
			return v1 != v2, nil
		}
	case bool:
		v2, ok := val2.(bool)
		if !ok {
			return false, fmt.Errorf("mismatched types")
		}
		switch operator {
		case "==":
			return v1 == v2, nil
		case "!=":
			return v1 != v2, nil
		}
	}
	return false, fmt.Errorf("unsupported type or operator")
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

		if len(exprs) == 2 &&
			isValidJsonPathExpression(jsonPaths[0]) &&
			isValidJsonPathExpression(jsonPaths[1]) {

			return op, jsonPaths, nil
		}
	}

	return "", []string{}, fmt.Errorf("unable to parse boolean expression %v", booleanExpression)
}

func isValidJsonPathExpression(expr string) bool {
	jp := jsonpath.New("validator").AllowMissingKeys(true)

	err := jp.Parse(expr)
	if err != nil {
		return false
	}

	_, err = QueryJsonPath("{}", expr)

	return err == nil
}

func QueryJsonPath(data interface{}, jsonPath string) (reflect.Value, error) {
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
