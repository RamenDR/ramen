package hooks

import (
	"context"
	"fmt"
	"regexp"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ramendr/ramen/internal/controller/kubeobjects"
)

const (
	defaultTimeoutValue = 300
	defaultOnErrorValue = "fail"
)

type NameSelectorType string

const (
	ValidNameSelector   NameSelectorType = "valid"
	RegexNameSelector   NameSelectorType = "regex"
	InvalidNameSelector NameSelectorType = "invalid"
)

func getResourcesUsingNameSelector(r client.Reader, hook *kubeobjects.HookSpec,
	objList client.ObjectList,
) (NameSelectorType, []client.Object, error) {
	filteredObjs := make([]client.Object, 0)

	var err error
	if isValidK8sName(hook.NameSelector) {
		// use nameSelector for Matching field
		objs, err := getObjectsUsingValidK8sName(r, hook, objList)

		return ValidNameSelector, objs, err
	} else if isValidRegex(hook.NameSelector) {
		// after listing without the fields selector, match with the regex for filtering
		listOps := &client.ListOptions{
			Namespace: hook.Namespace,
		}

		err = r.List(context.Background(), objList, listOps)
		if err != nil {
			return RegexNameSelector, filteredObjs, err
		}

		return RegexNameSelector, getObjectsBasedOnTypeAndRegex(objList, hook.NameSelector), nil
	}

	return InvalidNameSelector, filteredObjs, fmt.Errorf("nameSelector is neither distinct name nor regex")
}

func getObjectsUsingValidK8sName(r client.Reader, hook *kubeobjects.HookSpec,
	objList client.ObjectList,
) ([]client.Object, error) {
	listOps := &client.ListOptions{
		Namespace: hook.Namespace,
	}

	err := r.List(context.Background(), objList, listOps)
	if err != nil {
		return nil, fmt.Errorf("error listing resources using nameSelector: %w", err)
	}

	return getFilteredObjectsBasedOnTypeAndNameSelector(objList, hook.NameSelector), err
}

// Based on the type of resource, slice of objects is returned.
func getFilteredObjectsBasedOnTypeAndNameSelector(objList client.ObjectList, nameSelector string) []client.Object {
	objs := make([]client.Object, 0)

	switch v := objList.(type) {
	case *unstructured.UnstructuredList:
		objs = filterUnstructuredObjects(v.Items, nameSelector)
	case *corev1.PodList:
		objs = filterPods(v.Items, nameSelector)
	case *appsv1.DeploymentList:
		objs = filterDeployments(v.Items, nameSelector)
	case *appsv1.StatefulSetList:
		objs = filterStatefulSets(v.Items, nameSelector)
	case *appsv1.DaemonSetList:
		objs = filterDaemonSets(v.Items, nameSelector)
	}

	return objs
}

func filterDaemonSets(objs []appsv1.DaemonSet, nameSelector string) []client.Object {
	return filterObjectsSameAsNameSelector(toPointerSlice(objs), nameSelector)
}

func filterStatefulSets(objs []appsv1.StatefulSet, nameSelector string) []client.Object {
	return filterObjectsSameAsNameSelector(toPointerSlice(objs), nameSelector)
}

func filterDeployments(objs []appsv1.Deployment, nameSelector string) []client.Object {
	return filterObjectsSameAsNameSelector(toPointerSlice(objs), nameSelector)
}

func filterPods(objs []corev1.Pod, nameSelector string) []client.Object {
	return filterObjectsSameAsNameSelector(toPointerSlice(objs), nameSelector)
}

func filterUnstructuredObjects(objs []unstructured.Unstructured, nameSelector string) []client.Object {
	return filterObjectsSameAsNameSelector(toPointerSlice(objs), nameSelector)
}

func filterObjectsSameAsNameSelector[T client.Object](objs []T, nameSelector string) []client.Object {
	filteredObjs := make([]client.Object, 0, len(objs))

	for _, obj := range objs {
		if obj.GetName() == nameSelector {
			filteredObjs = append(filteredObjs, obj)
		}
	}

	return filteredObjs
}

// Based on the type of resource, slice of objects is returned.
//
//nolint:cyclop
func getObjectsBasedOnType(objList client.ObjectList) []client.Object {
	objs := make([]client.Object, 0)

	switch v := objList.(type) {
	case *unstructured.UnstructuredList:
		for _, uObj := range v.Items {
			objs = append(objs, &uObj)
		}
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
	case *appsv1.DaemonSetList:
		for _, ds := range v.Items {
			objs = append(objs, &ds)
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
	case *unstructured.UnstructuredList:
		objs = getMatchingUnstructedObjs(v, re)
	case *corev1.PodList:
		objs = getMatchingPods(v, re)
	case *appsv1.DeploymentList:
		objs = getMatchingDeployments(v, re)
	case *appsv1.StatefulSetList:
		objs = getMatchingStatefulSets(v, re)
	case *appsv1.DaemonSetList:
		objs = getMatchingDaemonSets(v, re)
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

func getMatchingUnstructedObjs(uList *unstructured.UnstructuredList, re *regexp.Regexp) []client.Object {
	return getRegexMatchingObjects(toPointerSlice(uList.Items), re)
}

func getMatchingPods(pList *corev1.PodList, re *regexp.Regexp) []client.Object {
	return getRegexMatchingObjects(toPointerSlice(pList.Items), re)
}

func getMatchingDeployments(dList *appsv1.DeploymentList, re *regexp.Regexp) []client.Object {
	return getRegexMatchingObjects(toPointerSlice(dList.Items), re)
}

func getMatchingStatefulSets(ssList *appsv1.StatefulSetList, re *regexp.Regexp) []client.Object {
	return getRegexMatchingObjects(toPointerSlice(ssList.Items), re)
}

func getMatchingDaemonSets(dsList *appsv1.DaemonSetList, re *regexp.Regexp) []client.Object {
	return getRegexMatchingObjects(toPointerSlice(dsList.Items), re)
}

func getRegexMatchingObjects[T client.Object](items []T, re *regexp.Regexp) []client.Object {
	objs := make([]client.Object, 0, len(items))

	for _, item := range items {
		if re.MatchString(item.GetName()) {
			obj := item
			objs = append(objs, obj)
		}
	}

	return objs
}

func toPointerSlice[T any](items []T) []*T {
	ptrs := make([]*T, len(items))
	for i := range items {
		ptrs[i] = &items[i]
	}

	return ptrs
}
