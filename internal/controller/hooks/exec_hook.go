// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	recipev1 "github.com/ramendr/recipe/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	"github.com/ramendr/ramen/internal/controller/util"
)

type ExecHook struct {
	Hook           *kubeobjects.HookSpec
	Reader         client.Reader
	Scheme         *runtime.Scheme
	RecipeElements util.RecipeElements
}

type ExecPodSpec struct {
	PodName   string
	Namespace string
	Command   []string
	Container string
}

// Execute uses exec hook definition provided in the recipe which will have identifiers to
// execute a command on the pod(s) matching the criteria.
func (e ExecHook) Execute(log logr.Logger) error {
	if e.Hook.LabelSelector == nil && e.Hook.NameSelector == "" {
		return fmt.Errorf("either nameSelector or labelSelector should be provided to get resources")
	}

	lister := NewPodLister(e)

	execPods, err := lister.GetPods(log)
	if err != nil {
		log.Error(err, "error occurred while getting pods to execute commands")

		return fmt.Errorf("error getting pods for exec hook: %w", err)
	}

	inverseOp := e.Hook.Op.InverseOp

	failedPod, err := e.executeCommands(execPods, log)
	if shouldInverseOpBeExecuted(inverseOp, e.Hook, err) {
		e.executeInverseOp(inverseOp, log)

		return fmt.Errorf("error executing exec hook on pod %s/%s: %w",
			failedPod.Namespace, failedPod.PodName, err)
	}

	return nil
}

func (e ExecHook) executeInverseOp(inverseOp string, log logr.Logger) {
	var inverseExecPod ExecPodSpec

	hookSpecForInvHook := e.getHookSpecForInverseOp(inverseOp)
	if hookSpecForInvHook == nil {
		log.Error(nil, "inverse operation not found in recipe", "inverseOp", inverseOp)

		return
	}

	log.Info("executing inverse operation", "inverseOp", inverseOp, "namespace", hookSpecForInvHook.Namespace)

	tempE := ExecHook{
		Hook:           hookSpecForInvHook,
		Reader:         e.Reader,
		Scheme:         e.Scheme,
		RecipeElements: e.RecipeElements,
	}

	lister := NewPodLister(tempE)

	execPods, err := lister.GetPods(log)
	if err != nil {
		log.Error(err, "error getting pods for inverse operation", "inverseOp", inverseOp)
	}

	inverseExecPod, err = tempE.executeCommands(execPods, log)
	if err != nil {
		log.Error(err, "error executing inverse operation", "inverseOp", inverseOp, "pod", inverseExecPod.PodName,
			"namespace", inverseExecPod.Namespace, "command", inverseExecPod.Command)

		return
	}

	log.Info("executed inverse operation successfully", "inverseOp", inverseOp, "pod", inverseExecPod.PodName,
		"namespace", inverseExecPod.Namespace, "command", inverseExecPod.Command)
}

func shouldInverseOpBeExecuted(inverseOp string, hookSpec *kubeobjects.HookSpec, err error) bool {
	return err != nil && inverseOp != "" && shouldOpHookBeFailedOnError(hookSpec)
}

func (e ExecHook) getHookSpecForInverseOp(inverseOp string) *kubeobjects.HookSpec {
	invHookParts := make([]string, 0)
	if strings.Contains(inverseOp, "/") {
		invHookParts = strings.Split(inverseOp, "/")
	} else {
		invHookParts = append(invHookParts, e.Hook.Name)
		invHookParts = append(invHookParts, inverseOp)
	}

	hooks := e.RecipeElements.RecipeWithParams.Spec.Hooks

	hook := getMatchingHook(hooks, invHookParts[0])
	if hook != nil {
		return getHookSpec(hook, invHookParts[1])
	}

	return nil
}

func getHookSpec(hook *recipev1.Hook, inverseOp string) *kubeobjects.HookSpec {
	for _, op := range hook.Ops {
		if op.Name == inverseOp {
			return &kubeobjects.HookSpec{
				Name: hook.Name,
				Op: kubeobjects.Operation{
					Name:      op.Name,
					Command:   op.Command,
					Container: op.Container,
					InverseOp: op.InverseOp,
					Timeout:   op.Timeout,
					OnError:   op.OnError,
				},
				SelectResource: hook.SelectResource,
				LabelSelector:  hook.LabelSelector,
				NameSelector:   hook.NameSelector,
				Namespace:      hook.Namespace,
				SinglePodOnly:  hook.SinglePodOnly,
				Timeout:        hook.Timeout,
				Essential:      hook.Essential,
				OnError:        hook.OnError,
			}
		}
	}

	return nil
}

func getMatchingHook(hooks []*recipev1.Hook, hookName string) *recipev1.Hook {
	for _, hook := range hooks {
		if hook.Name == hookName && hook.Type == "exec" {
			return hook
		}
	}

	return nil
}

func (e ExecHook) executeCommands(execPods []ExecPodSpec, log logr.Logger) (ExecPodSpec, error) {
	restCfg, err := config.GetConfig()
	if err != nil {
		return ExecPodSpec{}, fmt.Errorf("error getting kubeconfig: %w", err)
	}

	coreClient, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return ExecPodSpec{}, fmt.Errorf("error creating kubernetes client: %w", err)
	}

	for _, execPod := range execPods {
		err := executeCommand(coreClient, restCfg, &execPod, e.Hook, e.Scheme, log)
		if err != nil && getOpHookOnError(e.Hook) == defaultOnErrorValue {
			log.Error(err, "error executing command on pod", "pod", execPod.PodName,
				"namespace", execPod.Namespace, "command", execPod.Command)

			return execPod, fmt.Errorf("error executing exec hook: %w", err)
		}
	}

	return ExecPodSpec{}, nil
}

func executeCommand(coreClient *kubernetes.Clientset, restCfg *rest.Config, execPod *ExecPodSpec,
	hook *kubeobjects.HookSpec, scheme *runtime.Scheme, log logr.Logger,
) error {
	buf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	paramCodec := runtime.NewParameterCodec(scheme)
	request := coreClient.CoreV1().RESTClient().Post().
		Namespace(execPod.Namespace).
		Resource("pods").
		Name(execPod.PodName).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command:   execPod.Command,
			Container: execPod.Container,
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, paramCodec)

	exec, err := remotecommand.NewSPDYExecutor(restCfg, "POST", request.URL())
	if err != nil {
		return fmt.Errorf("error creating executor: %w", err)
	}

	// This time duration should be used from hook definition
	ctx, cancelFunc := context.WithTimeout(context.Background(), time.Duration(getOpHookTimeoutValue(hook))*time.Second)
	defer cancelFunc()

	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: buf,
		Stderr: errBuf,
	})
	if err != nil {
		log.Error(err, "error executing command on pod")

		return fmt.Errorf("error executing command on pod: command %s, error %s", execPod.Command, errBuf.String())
	}

	log.Info("executed exec command successfully", "pod", execPod.PodName, "namespace", execPod.Namespace,
		"command", execPod.Command, "output", buf.String())

	return nil
}

// Helper functions used by multiple listers

// IsPodOwnedByRS checks if a pod is owned by the specified ReplicaSet and is running.
func IsPodOwnedByRS(pod *corev1.Pod, rsName string) bool {
	for _, ownerRef := range pod.OwnerReferences {
		if isOwnerCorrect(&ownerRef, rsName, "ReplicaSet") && pod.Status.Phase == corev1.PodRunning {
			return true
		}
	}

	return false
}

// IsRSOwnedByDeployment checks if a ReplicaSet is owned by the specified Deployment and has ready replicas.
func IsRSOwnedByDeployment(rs *appsv1.ReplicaSet, depName string) bool {
	for _, ownerRef := range rs.OwnerReferences {
		if isOwnerCorrect(&ownerRef, depName, "Deployment") && rs.Status.Replicas > 0 && rs.Status.ReadyReplicas > 0 {
			return true
		}
	}

	return false
}

// IsPodOwnedByStatefulSet checks if a pod is owned by the specified StatefulSet and is running.
func IsPodOwnedByStatefulSet(pod *corev1.Pod, ssName string) bool {
	for _, ownerRef := range pod.OwnerReferences {
		if isOwnerCorrect(&ownerRef, ssName, "StatefulSet") && pod.Status.Phase == corev1.PodRunning {
			return true
		}
	}

	return false
}

// IsPodOwnedByDaemonSet checks if a pod is owned by the specified DaemonSet and is running.
func IsPodOwnedByDaemonSet(pod *corev1.Pod, dsName string) bool {
	for _, ownerRef := range pod.OwnerReferences {
		if isOwnerCorrect(&ownerRef, dsName, "DaemonSet") && pod.Status.Phase == corev1.PodRunning {
			return true
		}
	}

	return false
}

// isOwnerCorrect verifies the owner reference matches the expected name and kind.
func isOwnerCorrect(ownerRef *metav1.OwnerReference, ownerName, ownerKind string) bool {
	return ownerRef.Kind == ownerKind && ownerRef.Name == ownerName && ownerRef.Controller != nil &&
		*ownerRef.Controller
}

func getExecPodSpec(container string, cmd []string, pod *corev1.Pod) ExecPodSpec {
	execContainer := getContainerName(container, pod)

	return ExecPodSpec{
		PodName:   pod.Name,
		Namespace: pod.Namespace,
		Command:   cmd,
		Container: execContainer,
	}
}

func getContainerName(containerName string, pod *corev1.Pod) string {
	if containerName != "" {
		return containerName
	}

	return pod.Spec.Containers[0].Name
}

func covertCommandToStringArray(command string) ([]string, error) {
	var cmd []string
	if isJSONArray(command) {
		err := json.Unmarshal([]byte(command), &cmd)
		if err != nil {
			return []string{}, err
		}
	} else {
		cmd = strings.Split(command, " ")
	}

	return cmd, nil
}

func shouldOpHookBeFailedOnError(hook *kubeobjects.HookSpec) bool {
	// hook.Check.OnError overwrites the feature of hook.OnError -- defaults to fail
	if hook.Op.OnError != "" && hook.Op.OnError == "continue" {
		return false
	}

	if hook.OnError != "" && hook.OnError == "continue" {
		return false
	}

	return true
}
