// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"fmt"
	"reflect"
	"regexp"
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

// nolint:gocognit,cyclop
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
//nolint:funlen,gocognit,gocyclo,cyclop
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
		num, err := strconv.ParseFloat(a.String(), 64)
		if err != nil {
			return false, fmt.Errorf("failed to convert string to float64: %w", err)
		}

		a = reflect.ValueOf(num)
	}

	if b.Kind() == reflect.String && isNumericString(b.String()) {
		num, err := strconv.ParseFloat(b.String(), 64)
		if err != nil {
			return false, fmt.Errorf("failed to convert string to float64: %w", err)
		}

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

// nolint:cyclop
func parseBooleanExpression(booleanExpression string) (op string, jsonPaths []string, err error) {
	// List of valid operators
	operators := []string{"==", "!=", ">=", ">", "<=", "<"}

	// Find the first occurrence of an operator that is outside curly braces
	operatorIndex := -1

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

// IsValidJSONPathExpression checks if a given expression is a valid JSONPath expression
//
//nolint:cyclop
func IsValidJSONPathExpression(expr string) bool {
	expr = strings.TrimSpace(expr)

	if !(strings.HasPrefix(expr, "{") && strings.HasSuffix(expr, "}")) {
		return false
	}

	innerExpr := strings.Trim(expr, "{}")

	if innerExpr == "true" || innerExpr == "false" || innerExpr == "True" || innerExpr == "False" {
		return true
	}

	if isLiteralValue(innerExpr) {
		return true
	}

	if !strings.HasPrefix(innerExpr, "$") {
		return false
	}

	// JSONPath expressions and comparisons
	jsonPathRegex := regexp.MustCompile(`^\$([\.\w\[\]\"'\(\)\@\?\=\-]+)$`)
	jsonPathComparisonRegex := regexp.MustCompile(`^\$([\.\w\[\]\"'\(\)\@\?\=\-]+)` +
		`(\s*(==|!=|>=|<=|>|<)\s*(\$\S+|\d+|".*"|'.*'|\{.*\}))$`)
	jsonPathFilterRegex := regexp.MustCompile(`^\$.*\[\?\(@.*\)\].*$`)

	if jsonPathRegex.MatchString(innerExpr) || jsonPathComparisonRegex.MatchString(innerExpr) ||
		jsonPathFilterRegex.MatchString(innerExpr) {
		return true
	}

	return false
}

// Checks if a value is a valid literal (number or string)
func isLiteralValue(expr string) bool {
	// Check if it's a quoted string (single or double quotes)
	if (strings.HasPrefix(expr, "\"") && strings.HasSuffix(expr, "\"")) ||
		(strings.HasPrefix(expr, "'") && strings.HasSuffix(expr, "'")) {
		return true
	}

	// Check if it's a valid number
	if _, err := strconv.ParseFloat(expr, 64); err == nil {
		return true
	}

	return false
}

func QueryJSONPath(data interface{}, jsonPath string) (reflect.Value, error) {
	jp := jsonpath.New("extractor").AllowMissingKeys(true)

	if err := jp.Parse(jsonPath); err != nil {
		return reflect.Value{}, fmt.Errorf("failed to get value invalid jsonpath %w", err)
	}

	result, err := jp.FindResults(data)
	if err != nil {
		return reflect.Value{}, fmt.Errorf("failed to get value from data using jsonpath %w", err)
	}

	if len(result) == 0 || len(result[0]) == 0 {
		return reflect.Value{}, nil
	}

	return result[0][0], nil
}
