package utils

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func GetClientSetFromKubeConfigPath(kubeconfigPath string) (kubernetes.Interface, error) {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, err
	}

	k8sClientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return k8sClientSet, nil
}

// IsOpenShiftCluster checks if the given Kubernetes cluster is an OpenShift cluster.
// It returns true if the cluster is OpenShift, false otherwise, along with any error encountered.
func IsOpenShiftCluster(k8sClient kubernetes.Interface) (bool, error) {
	discoveryClient := k8sClient.Discovery()

	apiGroups, err := discoveryClient.ServerGroups()
	if err != nil {
		return false, err
	}

	for _, group := range apiGroups.Groups {
		if group.Name == "route.openshift.io" {
			return true, nil
		}
	}

	return false, nil
}

// CheckPodRunningStatus checks if there is at least one pod matching the labelSelector
// in the given namespace that is in the "Running" phase and contains the podIdentifier in its name.
func CheckPodRunningStatus(client kubernetes.Interface, namespace, labelSelector, podIdentifier string) (
	bool, string, error,
) {
	pods, err := client.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return false, "", fmt.Errorf("failed to list pods in namespace %s: %v", namespace, err)
	}

	for _, pod := range pods.Items {
		if strings.Contains(pod.Name, podIdentifier) && pod.Status.Phase == "Running" {
			return true, pod.Name, nil
		}
	}

	return false, "", nil
}
