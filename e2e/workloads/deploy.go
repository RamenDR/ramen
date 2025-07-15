// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package workloads

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	k8stypes "k8s.io/apimachinery/pkg/types"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/types"
)

const (
	deploymentName    = "deploy"
	deploymentAppName = "busybox"
	deploymentPath    = "workloads/deployment/base"
	deploymentPVCName = "busybox-pvc"

	//nolint:lll
	// deploymentMinimumReplicasAvailable is added in a deployment when it has its minimum replicas required available.
	// https://github.com/kubernetes/kubernetes/blob/95bff1b249e048b7e36ae857d7478dd2ac05346a/pkg/controller/deployment/util/deployment_util.go#L97
	deploymentMinimumReplicasAvailable = "MinimumReplicasAvailable"
)

type Deployment struct {
	Name    string
	Branch  string
	PVCSpec config.PVCSpec
}

func NewDeployment(branch string, pvcSpec config.PVCSpec) types.Workload {
	return &Deployment{
		Name:    fmt.Sprintf("%s-%s", deploymentName, pvcSpec.Name),
		Branch:  branch,
		PVCSpec: pvcSpec,
	}
}

func (w Deployment) GetAppName() string {
	return deploymentAppName
}

func (w Deployment) GetName() string {
	return w.Name
}

func (w Deployment) GetPath() string {
	return deploymentPath
}

func (w Deployment) GetBranch() string {
	return w.Branch
}

func (w Deployment) Kustomize() string {
	if w.PVCSpec.StorageClassName == "" && w.PVCSpec.AccessModes == "" {
		return ""
	}

	scName := "rook-ceph-block"
	if w.PVCSpec.StorageClassName != "" {
		scName = w.PVCSpec.StorageClassName
	}

	accessMode := "ReadWriteOnce"
	if w.PVCSpec.AccessModes != "" {
		accessMode = w.PVCSpec.AccessModes
	}

	patch := `{
				"patches": [{
					"target": {
						"kind": "PersistentVolumeClaim",
						"name": "busybox-pvc"
					},
					"patch": "- op: replace\n  path: /spec/storageClassName\n  value: ` + scName +
		`\n- op: add\n  path: /spec/accessModes\n  value: [` + accessMode + `]"
				}]
			}`

	return patch
}

func (w Deployment) GetResources() error {
	// this would be a common function given the vars? But we need the resources Kustomized
	return nil
}

// Check the workload health deployed in a cluster
func (w Deployment) Health(ctx types.TestContext, cluster *types.Cluster) error {
	deploy, err := getDeployment(ctx, cluster, ctx.AppNamespace(), w.GetAppName())
	if err != nil {
		return err
	}

	if deploy.GetGeneration() != deploy.Status.ObservedGeneration {
		return fmt.Errorf("deployment \"%s/%s\" status has stale generation in cluster %q (expected: %d, observed: %d)",
			ctx.AppNamespace(), w.GetAppName(), cluster.Name, deploy.GetGeneration(), deploy.Status.ObservedGeneration)
	}

	condition := findDeploymentCondition(deploy.Status.Conditions, appsv1.DeploymentAvailable)
	if condition == nil {
		return fmt.Errorf("deployment \"%s/%s\" missing %q condition in cluster %q",
			ctx.AppNamespace(), w.GetAppName(), appsv1.DeploymentAvailable, cluster.Name)
	}

	if condition.Status != corev1.ConditionTrue {
		return fmt.Errorf("deployment \"%s/%s\" condition %q is %q in cluster %q: %s",
			ctx.AppNamespace(), w.GetAppName(), appsv1.DeploymentAvailable,
			condition.Status, cluster.Name, condition.Message)
	}

	if condition.Reason != deploymentMinimumReplicasAvailable {
		return fmt.Errorf("deployment \"%s/%s\" condition %q has reason %q but expected %q in cluster %q",
			ctx.AppNamespace(), w.GetAppName(), appsv1.DeploymentAvailable,
			condition.Reason, deploymentMinimumReplicasAvailable, cluster.Name)
	}

	return nil
}

// Status returns the deployment status across managed clusters.
func (w Deployment) Status(ctx types.TestContext) ([]types.WorkloadStatus, error) {
	var statuses []types.WorkloadStatus

	clusters := []*types.Cluster{ctx.Env().C1, ctx.Env().C2}

	for _, cluster := range clusters {
		status, err := w.statusForCluster(ctx, cluster)
		if err != nil {
			return nil, fmt.Errorf("error checking application \"%s/%s\" on cluster %q: %w",
				ctx.AppNamespace(), w.GetAppName(), cluster.Name, err)
		}

		if status.Status != types.ApplicationNotFound {
			statuses = append(statuses, status)
		}
	}

	return statuses, nil
}

func (w Deployment) statusForCluster(ctx types.TestContext, cluster *types.Cluster) (types.WorkloadStatus, error) {
	deploymentExist, err := findDeployment(ctx, cluster, ctx.AppNamespace(), w.GetAppName())
	if err != nil {
		return types.WorkloadStatus{}, err
	}

	pvcExist, err := findPVC(ctx, cluster, ctx.AppNamespace(), deploymentPVCName)
	if err != nil {
		return types.WorkloadStatus{}, err
	}

	var status types.ApplicationStatus

	switch {
	case deploymentExist && pvcExist:
		status = types.ApplicationFound
	case deploymentExist || pvcExist:
		status = types.ApplicationPartial
	default:
		status = types.ApplicationNotFound
	}

	return types.WorkloadStatus{ClusterName: cluster.Name, Status: status}, nil
}

func getDeployment(ctx types.TestContext, cluster *types.Cluster, namespace, name string) (*appsv1.Deployment, error) {
	deploy := &appsv1.Deployment{}
	key := k8stypes.NamespacedName{Name: name, Namespace: namespace}

	err := cluster.Client.Get(ctx.Context(), key, deploy)
	if err != nil {
		return nil, err
	}

	return deploy, nil
}

func findDeployment(ctx types.TestContext, cluster *types.Cluster, namespace, name string) (bool, error) {
	_, err := getDeployment(ctx, cluster, namespace, name)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return false, err
		}

		return false, nil
	}

	return true, nil
}

func getPVC(
	ctx types.TestContext,
	cluster *types.Cluster,
	namespace, name string,
) (*corev1.PersistentVolumeClaim, error) {
	pvc := &corev1.PersistentVolumeClaim{}
	key := k8stypes.NamespacedName{Name: name, Namespace: namespace}

	err := cluster.Client.Get(ctx.Context(), key, pvc)
	if err != nil {
		return nil, err
	}

	return pvc, nil
}

func findPVC(ctx types.TestContext, cluster *types.Cluster, namespace, name string) (bool, error) {
	_, err := getPVC(ctx, cluster, namespace, name)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return false, err
		}

		return false, nil
	}

	return true, nil
}

func findDeploymentCondition(
	conditions []appsv1.DeploymentCondition,
	conditionType appsv1.DeploymentConditionType,
) *appsv1.DeploymentCondition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}

	return nil
}

func init() {
	register(deploymentName, NewDeployment)
}
