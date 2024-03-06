package utils

import "k8s.io/client-go/kubernetes"

const ramenSystemNamespace = "ramen-system"

func GetRamenNameSpace(k8sClient kubernetes.Interface) (string, error) {
	isOpenShift, err := IsOpenShiftCluster(k8sClient)
	if err != nil {
		return "", err
	}

	if isOpenShift {
		return "openshift-operators", nil
	}

	return ramenSystemNamespace, nil
}

// CheckRamenHubPodRunningStatus checks if the Ramen Hub Operator pod is running.
func CheckRamenHubPodRunningStatus(k8sClient kubernetes.Interface) (bool, string, error) {
	labelSelector := "app=ramen-hub"
	podIdentifier := "ramen-hub-operator"

	ramenNameSpace, err := GetRamenNameSpace(k8sClient)
	if err != nil {
		return false, "", err
	}

	return CheckPodRunningStatus(k8sClient, ramenNameSpace, labelSelector, podIdentifier)
}

// CheckRamenDROperatorPodRunningStatus checks if the Ramen DR Operator pod is running.
func CheckRamenDROperatorPodRunningStatus(k8sClient kubernetes.Interface) (bool, string, error) {
	labelSelector := "app=ramen-dr-cluster"
	podIdentifier := "ramen-dr-cluster-operator"

	ramenNameSpace, err := GetRamenNameSpace(k8sClient)
	if err != nil {
		return false, "", err
	}

	return CheckPodRunningStatus(k8sClient, ramenNameSpace, labelSelector, podIdentifier)
}
