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
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	execPods := e.GetPodsToExecuteCommands(log)

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

func (e ExecHook) GetPodsToExecuteCommands(log logr.Logger) []ExecPodSpec {
	var execPods []ExecPodSpec

	if e.Hook.SinglePodOnly {
		eps, err := e.getExecPodsForSinglePodOnly(log)
		if err != nil {
			log.Error(err, "error occurred while getting pods for non singlePodOnly")
		}

		execPods = append(execPods, eps...)
	} else {
		eps := e.getAllPossibleExecPods(log)

		execPods = append(execPods, eps...)
	}

	return execPods
}

func (e ExecHook) getExecPodsForSinglePodOnly(log logr.Logger) ([]ExecPodSpec, error) {
	execPods := make([]ExecPodSpec, 0)

	var err error

	if e.Hook.SelectResource == "" || e.Hook.SelectResource == "pod" {
		// considering the default value for selectResource as Pod
		eps := e.getAllPossibleExecPods(log)
		if len(eps) > 0 {
			execPods = append(execPods, eps[0])
			log.Info("pods details obtained using label selector for", "hook", e.Hook.Name,
				"labelSelector", e.Hook.LabelSelector, "selectResource", e.Hook.SelectResource, "podName", eps[0].PodName)

			return execPods, nil
		}

		return execPods, fmt.Errorf("no pods found using labelSelector or nameSelector when singlePodOnly is true")
	}

	if e.Hook.SelectResource == "deployment" {
		execPods, err = e.getExecPodsFromDepForSinglePodOnly(log)
		if err != nil {
			return execPods, err
		}

		return execPods, nil
	}

	if e.Hook.SelectResource == "statefulset" {
		execPods, err = e.getExecPodsFromStatefulSetForSinglePodOnly(log)
		if err != nil {
			return execPods, err
		}

		return execPods, nil
	}

	return execPods, err
}

func (e ExecHook) getExecPodsFromStatefulSetForSinglePodOnly(log logr.Logger) ([]ExecPodSpec, error) {
	statefulSetList := &appsv1.StatefulSetList{}
	ss := make([]appsv1.StatefulSet, 0)
	execPods := make([]ExecPodSpec, 0)

	if e.Hook.LabelSelector != nil {
		err := getResourcesUsingLabelSelector(e.Reader, e.Hook, statefulSetList)
		if err != nil {
			return execPods, err
		}

		ss = append(ss, statefulSetList.Items...)

		log.Info("statefulsets count obtained using label selector for", "hook", e.Hook.Name, "labelSelector",
			e.Hook.LabelSelector, "selectResource", e.Hook.SelectResource, "statefulSetCount", len(ss))
	}

	if e.Hook.NameSelector != "" {
		objs, err := getResourcesUsingNameSelector(e.Reader, e.Hook, statefulSetList)
		if err != nil {
			return execPods, err
		}

		for _, obj := range objs {
			s, ok := obj.(*appsv1.StatefulSet)
			if ok {
				ss = append(ss, *s)
			}
		}

		log.Info("statefulsets count obtained using name selector for", "hook", e.Hook.Name,
			"nameSelector", e.Hook.NameSelector, "selectResource", e.Hook.SelectResource, "statefulSetCount", len(ss))
	}

	return e.getPodsFromStatefulsets(ss)
}

func (e ExecHook) getPodsFromStatefulsets(ss []appsv1.StatefulSet) ([]ExecPodSpec, error) {
	execPods := make([]ExecPodSpec, 0)

	for _, statefulSet := range ss {
		if statefulSet.Status.ReadyReplicas > 0 {
			pod := &corev1.Pod{}

			err := e.Reader.Get(context.Background(), client.ObjectKey{
				Name:      statefulSet.Name + "-0",
				Namespace: statefulSet.Namespace,
			}, pod)
			if err != nil {
				return execPods, fmt.Errorf("error occurred while getting pod for statefulset: %w", err)
			}

			cmd, err := covertCommandToStringArray(e.Hook.Op.Command)
			if err != nil {
				return execPods, fmt.Errorf("error converting command to string array: %w", err)
			}

			execPods = append(execPods, ExecPodSpec{
				PodName:   pod.Name,
				Namespace: pod.Namespace,
				Command:   cmd,
				Container: getContainerName(e.Hook.Op.Container, pod),
			})
		}
	}

	return execPods, nil
}

func (e ExecHook) getExecPodsFromDepForSinglePodOnly(log logr.Logger) ([]ExecPodSpec, error) {
	execPods := make([]ExecPodSpec, 0)

	deps, err := e.getDeploymentsForSinglePodOnly(log)
	if err != nil {
		log.Error(err, "error occurred while getting deployments for singlePodOnly")

		return execPods, fmt.Errorf("error occurred while getting deployments"+
			" for singlePodOnly: %w", err)
	}

	for _, dep := range deps {
		rs, err := e.getReplicaSetFromDepForSinglePodOnly(dep.Name, dep.Namespace)
		if err != nil {
			log.Error(err, "error occurred while getting replicaset for deployment")

			return execPods, fmt.Errorf("error occurred while getting replicaset"+
				" for deployment: %w", err)
		}

		eps, err := e.getPodExecFromReplicaSetForSinglePodOnly(rs.Name, rs.Namespace)
		if err != nil {
			log.Error(err, "error occurred while getting pod exec from replicaset")

			return execPods, fmt.Errorf("error occurred while getting pod exec"+
				" from replicaset: %w", err)
		}

		execPods = append(execPods, eps)
	}

	return execPods, nil
}

func (e ExecHook) getPodExecFromReplicaSetForSinglePodOnly(rsName, rsNS string) (ExecPodSpec, error) {
	podList := &corev1.PodList{}

	err := e.Reader.List(context.Background(), podList, client.InNamespace(rsNS))
	if err != nil {
		return ExecPodSpec{}, fmt.Errorf("error listing pods: %w", err)
	}

	for _, pod := range podList.Items {
		if IsPodOwnedByRS(&pod, rsName) {
			cmd, err := covertCommandToStringArray(e.Hook.Op.Command)
			if err != nil {
				return ExecPodSpec{}, fmt.Errorf("error converting command to string array: %w", err)
			}

			return ExecPodSpec{
				PodName:   pod.Name,
				Namespace: pod.Namespace,
				Command:   cmd,
				Container: getContainerName(e.Hook.Op.Container, &pod),
			}, nil
		}
	}

	return ExecPodSpec{}, nil
}

func (e ExecHook) getReplicaSetFromDepForSinglePodOnly(depName, depNS string) (*appsv1.ReplicaSet, error) {
	// get the replicaset of the deployment
	replicaSet := &appsv1.ReplicaSetList{}

	err := e.Reader.List(context.Background(), replicaSet, client.InNamespace(depNS))
	if err != nil {
		return nil, fmt.Errorf("error listing replicaset: %w", err)
	}

	for _, rs := range replicaSet.Items {
		if IsRSOwnedByDeployment(&rs, depName) {
			return &rs, nil
		}
	}

	return nil, fmt.Errorf("replicaset not found for deployment %s in namespace %s", depName, depNS)
}

func IsPodOwnedByRS(pod *corev1.Pod, rsName string) bool {
	for _, ownerRef := range pod.OwnerReferences {
		if isOwnerCorrect(&ownerRef, rsName, "ReplicaSet") && pod.Status.Phase == corev1.PodRunning {
			return true
		}
	}

	return false
}

func IsRSOwnedByDeployment(rs *appsv1.ReplicaSet, depName string) bool {
	for _, ownerRef := range rs.OwnerReferences {
		if isOwnerCorrect(&ownerRef, depName, "Deployment") && rs.Status.Replicas > 0 && rs.Status.ReadyReplicas > 0 {
			return true
		}
	}

	return false
}

func isOwnerCorrect(ownerRef *metav1.OwnerReference, ownerName, ownerKind string) bool {
	return ownerRef.Kind == ownerKind && ownerRef.Name == ownerName && ownerRef.Controller != nil && *ownerRef.Controller
}

func (e ExecHook) getDeploymentsForSinglePodOnly(log logr.Logger) ([]appsv1.Deployment, error) {
	deps := make([]appsv1.Deployment, 0)
	deploymentList := &appsv1.DeploymentList{}

	var err error

	if e.Hook.LabelSelector != nil {
		err = getResourcesUsingLabelSelector(e.Reader, e.Hook, deploymentList)
		if err != nil {
			return nil, err
		}

		deps = append(deps, deploymentList.Items...)

		log.Info("deployments count obtained using label selector for", "hook", e.Hook.Name, "labelSelector",
			e.Hook.LabelSelector, "selectResource", e.Hook.SelectResource, "deploymentCount", len(deps))
	}

	if e.Hook.NameSelector != "" {
		objs, err := getResourcesUsingNameSelector(e.Reader, e.Hook, deploymentList)
		if err != nil {
			return deps, err
		}

		for _, dep := range objs {
			d, ok := dep.(*appsv1.Deployment)

			if ok {
				deps = append(deps, *d)
			}
		}

		log.Info("deployments count obtained using name selector for", "hook", e.Hook.Name,
			"nameSelector", e.Hook.NameSelector, "selectResource", e.Hook.SelectResource, "deploymentCount", len(deps))
	}

	return deps, err
}

func (e ExecHook) getAllPossibleExecPods(log logr.Logger) []ExecPodSpec {
	execPods := make([]ExecPodSpec, 0)

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

		log.Info("all pods count obtained using label selector for", "hook", e.Hook.Name,
			"labelSelector", e.Hook.LabelSelector, "selectResource", e.Hook.SelectResource, "podCount", len(execPods))
	}

	if e.Hook.NameSelector != "" {
		eps, err := e.getExecPodsUsingNameSelector(log)
		if err != nil {
			log.Error(err, "error occurred while getting pods using nameSelector")
		}

		execPods = append(execPods, eps...)

		log.Info("all pods count obtained using name selector for", "hook", e.Hook.Name,
			"nameSelector", e.Hook.NameSelector, "selectResource", e.Hook.SelectResource, "podCount", len(execPods))
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

	// For selectResource other than pod, if the given name is valid k8s name, then .* needs to be appended.
	nameSelector := e.Hook.NameSelector

	if e.Hook.SelectResource == "deployment" || e.Hook.SelectResource == "statefulset" {
		nameSelector = fmt.Sprintf("%s.*", nameSelector)
	}

	if isValidK8sName(nameSelector) {
		listOps := &client.ListOptions{
			Namespace:     e.Hook.Namespace,
			FieldSelector: fields.SelectorFromSet(fields.Set{"metadata.name": e.Hook.NameSelector}),
		}

		err = e.Reader.List(context.Background(), podList, listOps)
		if err != nil {
			return execPods, fmt.Errorf("error listing resources using nameSelector: %w", err)
		}

		execPods, err = e.getExecPodSpecsFromObjList(podList, log)
		if err != nil {
			return execPods, fmt.Errorf("error getting exec pods using nameSelector: %w", err)
		}

		return execPods, nil
	}

	if isValidRegex(nameSelector) {
		listOps := &client.ListOptions{Namespace: e.Hook.Namespace}

		re, err := regexp.Compile(e.Hook.NameSelector)
		if err != nil {
			return execPods, fmt.Errorf("error during regex compilation using nameSelector: %w", err)
		}

		err = e.Reader.List(context.Background(), podList, listOps)
		if err != nil {
			return execPods, fmt.Errorf("error listing resources using nameSelector: %w", err)
		}

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
