package hooks

import (
	"context"
	"fmt"
	"regexp"

	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultTimeoutValue = 300
	defaultOnErrorValue = "fail"
)

func getResourcesUsingNameSelector(r client.Reader, hook *kubeobjects.HookSpec,
	objList client.ObjectList,
) ([]client.Object, error) {
	filteredObjs := make([]client.Object, 0)

	var err error

	if isValidK8sName(hook.NameSelector) {
		// use nameSelector for Matching field
		return getObjectsUsingValidK8sName(r, hook, objList)
	} else if isValidRegex(hook.NameSelector) {
		// after listing without the fields selector, match with the regex for filtering
		listOps := &client.ListOptions{
			Namespace: hook.Namespace,
		}

		err = r.List(context.Background(), objList, listOps)
		if err != nil {
			return filteredObjs, err
		}

		return getObjectsBasedOnTypeAndRegex(objList, hook.NameSelector), nil
	}

	return filteredObjs, fmt.Errorf("nameSelector is neither distinct name nor regex")
}

func getObjectsUsingValidK8sName(r client.Reader, hook *kubeobjects.HookSpec,
	objList client.ObjectList,
) ([]client.Object, error) {
	listOps := &client.ListOptions{
		Namespace: hook.Namespace,
		FieldSelector: fields.SelectorFromSet(fields.Set{
			"metadata.name": hook.NameSelector, // needs exact matching with the name
		}),
	}

	err := r.List(context.Background(), objList, listOps)
	if err != nil {
		return nil, fmt.Errorf("error listing resources using nameSelector: %w", err)
	}

	return getObjectsBasedOnType(objList), err
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
func getObjectsBasedOnTypeAndRegex(objList client.ObjectList, nameSelector string) []client.Object {
	objs := make([]client.Object, 0)

	re, err := regexp.Compile(nameSelector)
	if err != nil {
		return objs
	}

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

func getResourcesUsingLabelSelector(r client.Reader, hook *kubeobjects.HookSpec,
	objList client.ObjectList,
) error {
	selector, err := metav1.LabelSelectorAsSelector(hook.LabelSelector)
	if err != nil {
		return fmt.Errorf("error converting labelSelector to selector")
	}

	listOps := &client.ListOptions{
		Namespace:     hook.Namespace,
		LabelSelector: selector,
	}

	err = r.List(context.Background(), objList, listOps)
	if err != nil {
		return fmt.Errorf("error listing resources using labelSelector: %w", err)
	}

	return nil
}

func isJSONArray(input string) bool {
	// Regular expression to check if the input is a JSON array
	re := regexp.MustCompile(`^\s*\[.*\]\s*$`)

	return re.MatchString(input)
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

func getOpHookOnError(hook *kubeobjects.HookSpec) string {
	if hook.Op.OnError != "" {
		return hook.Op.OnError
	} else if hook.OnError != "" {
		return hook.OnError
	}

	// Default to fail if not specified
	return defaultOnErrorValue
}

func getChkHookTimeoutValue(hook *kubeobjects.HookSpec) int {
	if hook.Chk.Timeout != 0 {
		return hook.Chk.Timeout
	} else if hook.Timeout != 0 {
		return hook.Timeout
	}
	// 300s is the default value for timeout
	return defaultTimeoutValue
}

func getOpHookTimeoutValue(hook *kubeobjects.HookSpec) int {
	if hook.Op.Timeout != 0 {
		return hook.Op.Timeout
	} else if hook.Timeout != 0 {
		return hook.Timeout
	}
	// 300s is the default value for timeout
	return defaultTimeoutValue
}
