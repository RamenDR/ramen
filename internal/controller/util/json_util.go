package util

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	"github.com/oliveagle/jsonpath"
	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type expressionResult struct {
	result bool
	err    error
}

func EvaluateJsonPathExpression(client client.Client, captureGroup *kubeobjects.CaptureSpec, log logr.Logger) (bool, error) {
	getTimeoutValue(&captureGroup.Hook)
	nsScopedName := types.NamespacedName{
		Namespace: captureGroup.Hook.Namespace,
		Name:      captureGroup.Hook.Name,
	}
	switch captureGroup.Hook.SelectResource {
	case "pod":
		// handle pod type
		resource := &corev1.Pod{}
		err := client.Get(context.Background(), nsScopedName, resource)
		//if k8serror.NotFound -- we will reexecute after 1s and if timedout, return error
		data, err := json.MarshalIndent(resource, "", "  ")
		if err != nil {
			return false, err
		}
		return evaluateJPExp(captureGroup, data)
	case "deployment":
		// handle deployment type
		log.Info("**** ASN, in case deployment for check hooks")
		resource := &appsv1.Deployment{}
		dep := client.Get(context.Background(), nsScopedName, resource)
		data, err := json.MarshalIndent(dep, "", "  ")
		if err != nil {
			return false, err
		}
		return evaluateJPExp(captureGroup, data)
	case "statefulset":
		// handle statefulset type
		resource := &appsv1.StatefulSet{}
		statefulset := client.Get(context.Background(), nsScopedName, resource)
		data, err := json.MarshalIndent(statefulset, "", "  ")
		if err != nil {
			return false, err
		}
		return evaluateJPExp(captureGroup, data)
	}

	return false, nil
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

func evaluateJPExp(captureGroup *kubeobjects.CaptureSpec, data []byte) (bool, error) {
	var if_data interface{}
	err := json.Unmarshal(data, &if_data)
	if err != nil {
		return false, err
	}
	jsonCondition := captureGroup.Hook.Chk.Condition
	op, paths, err := getJsonPathsAndOp(jsonCondition)
	if err != nil {
		return false, err
	}
	tPaths := trimPaths(paths)
	results := make([]interface{}, len(paths))
	for i, path := range tPaths {
		// This section is using go get github.com/oliveagle/jsonpath -- This seems to work. Enhance based on this, we will see later
		if !strings.HasPrefix(path, "$.") {
			// It might not be a json path and might be a base type
			results[i] = path
			continue
		}
		results[i], err = jsonpath.JsonPathLookup(if_data, path)
		if err != nil {
			return false, err
		}
	}
	fmt.Println("**** ASN, the results are ", results)
	expValue, err := evaluateResults(op, results[0], results[1])
	fmt.Println("**** ASN, expression evaluated value with results ", expValue)
	if err != nil {
		return false, err
	}

	return expValue, nil
}

func evaluateResults(op string, valA, valB interface{}) (bool, error) {
	switch op {
	case "==":
		return valA == valB, nil
	case "!=":
		return valA != valB, nil
	case "<":
		return valA.(string) < valB.(string), nil
	case ">":
		return valA.(string) > valB.(string), nil
	case "<=":
		return valA.(string) <= valB.(string), nil
	case ">=":
		return valA.(string) >= valB.(string), nil
	default:
		return false, fmt.Errorf("unsupported op %s provided for jsonpath evalvation", op)
	}

}

func trimPaths(paths []string) []string {
	tPaths := make([]string, len(paths))
	for i, path := range paths {
		path = strings.TrimSpace(path)
		path = strings.TrimLeft(path, "{")
		path = strings.TrimRight(path, "}")
		tPaths[i] = path
	}
	return tPaths
}

func getJsonPathsAndOp(exp string) (string, []string, error) {
	operators := []string{"==", "!=", ">=", ">", "<=", "<"}
	for _, op := range operators {
		if strings.Contains(exp, op) {
			return op, strings.Split(exp, op), nil
		}
	}
	return "", []string{}, errors.New("unsupported operator used in jsonpath evaluation")
}
