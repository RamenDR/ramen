// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"k8s.io/client-go/util/jsonpath"
)

const (
	expectedNumberOfJSONPaths = 2
	podType                   = "pod"
	deploymentType            = "deployment"
	statefulsetType           = "statefulset"
	pInterval                 = 100
)

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
