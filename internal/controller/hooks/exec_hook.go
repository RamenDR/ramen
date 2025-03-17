// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/go-logr/logr"
	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

type ExecHook struct {
	Hook   *kubeobjects.HookSpec
	Reader client.Reader
	Scheme *runtime.Scheme
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

	execPods := e.getPodsToExecuteCommands(log)

	for _, execPod := range execPods {
		err := executeCommand(&execPod, e.Hook, e.Scheme, log)
		if err != nil && getOpHookOnError(e.Hook) == defaultOnErrorValue {
			return fmt.Errorf("error executing exec hook: %w", err)
		}
	}

	return nil
}

func executeCommand(execPod *ExecPodSpec, hook *kubeobjects.HookSpec, scheme *runtime.Scheme, log logr.Logger) error {
	restCfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("error getting kubeconfig: %w", err)
	}

	coreClient, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return fmt.Errorf("error creating kubernetes client: %w", err)
	}

	buf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	paramCodec := runtime.NewParameterCodec(scheme)
	request := coreClient.CoreV1().RESTClient().Post().
		Namespace(execPod.Namespace).
		Resource("pods").
		Name(execPod.PodName).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command: execPod.Command,
			Stdin:   false,
			Stdout:  true,
			Stderr:  true,
			TTY:     false,
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

func (e ExecHook) getPodsToExecuteCommands(log logr.Logger) []ExecPodSpec {
	var execPods []ExecPodSpec
	/*
		1. If the labelSelector is provided, get the pods using the labelSelector.
		2. If the nameSelector is provided, get the pods using the nameSelector.
		3. If both are provided, OR logic will apply
		For both the conditions, selectResource needs to be considered as Pod.
	*/
	if e.Hook.LabelSelector != nil {
		eps, err := e.getExecPodsUsingLabelSelector(log)
		if err != nil {
			log.Error(err, "error occurred while getting pods using labelSelector")
		}

		execPods = append(execPods, eps...)
	}

	if e.Hook.NameSelector != "" {
		eps, err := e.getExecPodsUsingNameSelector(log)
		if err != nil {
			log.Error(err, "error occurred while getting pods using nameSelector")
		}

		execPods = append(execPods, eps...)
	}

	return execPods
}

func (e ExecHook) getExecPodsUsingLabelSelector(log logr.Logger) ([]ExecPodSpec, error) {
	execPods := make([]ExecPodSpec, 0)
	podList := &corev1.PodList{}

	err := getResourcesUsingLabelSelector(e.Reader, e.Hook, podList)
	if err != nil {
		return execPods, err
	}

	log.Info("pods count obtained using label selector for", "hook", e.Hook.Name,
		"labelSelector", e.Hook.LabelSelector, "podCount", len(podList.Items))

	execPods, err = e.getExecPodSpecsFromObjList(podList, log)
	if err != nil {
		return execPods, fmt.Errorf("error filtering exec pods using labelSelector: %w", err)
	}

	return execPods, nil
}

func (e ExecHook) getExecPodsUsingNameSelector(log logr.Logger) ([]ExecPodSpec, error) {
	var err error

	execPods := make([]ExecPodSpec, 0)
	podList := &corev1.PodList{}

	if isValidK8sName(e.Hook.NameSelector) {
		// use nameSelector for Matching field
		listOps := &client.ListOptions{
			Namespace: e.Hook.Namespace,
			FieldSelector: fields.SelectorFromSet(fields.Set{
				"metadata.name": e.Hook.NameSelector, // needs exact matching with the name
			}),
		}

		err = e.Reader.List(context.Background(), podList, listOps)
		if err != nil {
			log.Error(err, "error listing resources using nameSelector")

			return execPods, fmt.Errorf("error listing resources using nameSelector: %w", err)
		}

		log.Info("pods count obtained using name selector for", "hook", e.Hook.Name,
			"nameSelector", e.Hook.NameSelector, "podCount", len(podList.Items))

		execPods, err = e.getExecPodSpecsFromObjList(podList, log)
		if err != nil {
			return execPods, fmt.Errorf("error getting exec pods using nameSelector: %w", err)
		}

		return execPods, nil
	}

	if isValidRegex(e.Hook.NameSelector) {
		// after listing without the fields selector, match with the regex for filtering
		listOps := &client.ListOptions{
			Namespace: e.Hook.Namespace,
		}

		re, err := regexp.Compile(e.Hook.NameSelector)
		if err != nil {
			return execPods, fmt.Errorf("error during regex compilation using nameSelector: %w", err)
		}

		err = e.Reader.List(context.Background(), podList, listOps)
		if err != nil {
			return execPods, fmt.Errorf("error listing resources using nameSelector: %w", err)
		}

		log.Info("pods count obtained using name selector for", "hook", e.Hook.Name,
			"nameSelector", e.Hook.NameSelector, "podCount", len(podList.Items))

		execPods, err = e.filterExecPodsUsingRegex(podList, re, log)
		if err != nil {
			return execPods, fmt.Errorf("error filtering exec pods using nameSelector regex: %w", err)
		}

		return execPods, nil
	}

	return execPods, nil
}

func (e ExecHook) filterExecPodsUsingRegex(podList *corev1.PodList, re *regexp.Regexp,
	log logr.Logger,
) ([]ExecPodSpec, error) {
	execPods := make([]ExecPodSpec, 0)

	cmd, err := covertCommandToStringArray(e.Hook.Op.Command)
	if err != nil {
		log.Error(err, "error occurred during exec hook execution", "command being converted:", e.Hook.Op.Command)

		return execPods, err
	}

	for _, pod := range podList.Items {
		if re.MatchString(pod.Name) {
			execPods = append(execPods, getExecPodSpec(e.Hook.Op.Container, cmd, &pod))
		}

		// If singlePodOnly, only one should be used.
		if e.Hook.SinglePodOnly {
			break
		}
	}

	return execPods, nil
}

func (e ExecHook) getExecPodSpecsFromObjList(podList *corev1.PodList, log logr.Logger) ([]ExecPodSpec, error) {
	execPods := make([]ExecPodSpec, 0)

	cmd, err := covertCommandToStringArray(e.Hook.Op.Command)
	if err != nil {
		log.Error(err, "error occurred during exec hook execution", "command being converted:", e.Hook.Op.Command)

		return execPods, err
	}

	for _, pod := range podList.Items {
		execPods = append(execPods, getExecPodSpec(e.Hook.Op.Container, cmd, &pod))

		// If singlePodOnly, only one should be used.
		if e.Hook.SinglePodOnly {
			break
		}
	}

	return execPods, nil
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
