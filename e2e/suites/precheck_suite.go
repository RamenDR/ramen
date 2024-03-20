package suites

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ramendr/ramen/e2e/util"
	rookv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

const ramenSystemNamespace = "ramen-system"

type PrecheckSuite struct {
	ctx *util.TestContext
}

func (s *PrecheckSuite) SetContext(ctx *util.TestContext) {
	ctx.Log.Info("enter SetContext")
	s.ctx = ctx
}

func (s *PrecheckSuite) SetupSuite() error {
	s.ctx.Log.Info("enter SetupSuite")
	return nil
}

func (s *PrecheckSuite) TeardownSuite() error {
	s.ctx.Log.Info("enter TeardownSuite")
	return nil
}

func (s *PrecheckSuite) Tests() []Test {
	s.ctx.Log.Info("enter Tests")
	return []Test{
		s.TestRamenHubOperatorStatus,
		s.TestRamenSpokeOperatorStatus,
		s.TestCephClusterStatus,
		//check other operator like cert manager, csi, ocm, submariner, rook, velero, minio, volsync
	}
}

func (s *PrecheckSuite) TestRamenHubOperatorStatus() error {
	s.ctx.Log.Info("enter Running TestRamenHubOperatorStatus")

	isRunning, podName, err := CheckRamenHubPodRunningStatus(s.ctx.HubClient())
	if err != nil {
		return err
	}

	if isRunning {
		s.ctx.Log.Info("Ramen Hub Operator is running", "pod", podName)
		return nil
	}

	return fmt.Errorf("no running Ramen Hub Operator pod")
}

func (s *PrecheckSuite) TestRamenSpokeOperatorStatus() error {
	s.ctx.Log.Info("enter Running TestRamenSpokeOperatorStatus")

	isRunning, podName, err := CheckRamenSpokePodRunningStatus(s.ctx.C1Client())
	if err != nil {
		return err
	}

	if isRunning {
		s.ctx.Log.Info("Ramen Spoke Operator is running on cluster 1", "pod", podName)
	} else {
		return fmt.Errorf("no running Ramen Spoke Operator pod on cluster 1")
	}

	isRunning, podName, err = CheckRamenSpokePodRunningStatus(s.ctx.C2Client())
	if err != nil {
		return err
	}

	if isRunning {
		s.ctx.Log.Info("Ramen Spoke Operator is running on cluster 2", "pod", podName)
	} else {
		return fmt.Errorf("no running Ramen Spoke Operator pod on cluster 2")
	}

	return nil

}

// CheckPodRunningStatus checks if there is at least one pod matching the labelSelector
// in the given namespace that is in the "Running" phase and contains the podIdentifier in its name.
func CheckPodRunningStatus(client *kubernetes.Clientset, namespace, labelSelector, podIdentifier string) (
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

func GetRamenNameSpace(k8sClient *kubernetes.Clientset) (string, error) {
	isOpenShift, err := IsOpenShiftCluster(k8sClient)
	if err != nil {
		return "", err
	}

	if isOpenShift {
		return "openshift-operators", nil
	}

	return ramenSystemNamespace, nil
}

// IsOpenShiftCluster checks if the given Kubernetes cluster is an OpenShift cluster.
// It returns true if the cluster is OpenShift, false otherwise, along with any error encountered.
func IsOpenShiftCluster(k8sClient *kubernetes.Clientset) (bool, error) {
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

func CheckRamenHubPodRunningStatus(k8sClient *kubernetes.Clientset) (bool, string, error) {
	labelSelector := "app=ramen-hub"
	podIdentifier := "ramen-hub-operator"

	ramenNameSpace, err := GetRamenNameSpace(k8sClient)
	if err != nil {
		return false, "", err
	}

	return CheckPodRunningStatus(k8sClient, ramenNameSpace, labelSelector, podIdentifier)
}

func CheckRamenSpokePodRunningStatus(k8sClient *kubernetes.Clientset) (bool, string, error) {
	labelSelector := "app=ramen-dr-cluster"
	podIdentifier := "ramen-dr-cluster-operator"

	ramenNameSpace, err := GetRamenNameSpace(k8sClient)
	if err != nil {
		return false, "", err
	}

	return CheckPodRunningStatus(k8sClient, ramenNameSpace, labelSelector, podIdentifier)
}

func (s *PrecheckSuite) TestCephClusterStatus() error {
	s.ctx.Log.Info("enter Running TestCephClusterStatus")

	c1DynamicClient := s.ctx.C1DynamicClient()

	err := CheckCephClusterRunningStatus(c1DynamicClient)
	if err != nil {
		return err
	}

	c2DynamicClient := s.ctx.C2DynamicClient()
	err = CheckCephClusterRunningStatus(c2DynamicClient)
	if err != nil {
		return err
	}

	return nil

}

func CheckCephClusterRunningStatus(client *dynamic.DynamicClient) error {
	rookNamespace := "rook-ceph"

	resource := schema.GroupVersionResource{Group: "ceph.rook.io", Version: "v1", Resource: "cephclusters"}
	resp, err := client.Resource(resource).Namespace(rookNamespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("could not list cephcluster")

	}
	respJson, _ := resp.MarshalJSON()
	cephclusterList := rookv1.CephClusterList{}
	json.Unmarshal(respJson, &cephclusterList)
	if len(cephclusterList.Items) == 0 {
		return fmt.Errorf("found 0 cephcluster")
	}
	if len(cephclusterList.Items) > 1 {
		return fmt.Errorf("found more than 1 cephcluster")
	}
	phase := fmt.Sprint(cephclusterList.Items[0].Status.Phase)
	if phase != "Ready" {
		return fmt.Errorf("cephcluster is not Ready")
	}

	return nil
}
