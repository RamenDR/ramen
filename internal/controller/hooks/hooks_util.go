package hooks

import (
	"context"
	"fmt"
	"regexp"

	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultTimeoutValue = 300
	defaultOnErrorValue = "fail"
)

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
