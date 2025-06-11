// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package workloads

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/types"
)

const (
	deploymentName    = "deploy"
	deploymentAppName = "busybox"
	deploymentPath    = "workloads/deployment/base"
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

// Check the workload health deployed in a cluster namespace
func (w Deployment) Health(ctx types.TestContext, cluster types.Cluster, namespace string) error {
	deploy, err := getDeployment(ctx, cluster, namespace, w.GetAppName())
	if err != nil {
		return err
	}

	if deploy.Status.Replicas == deploy.Status.ReadyReplicas {
		return nil
	}

	return fmt.Errorf("deployment \"%s/%s\" not ready in cluster %q: %d/%d replicas ready",
		namespace, w.GetAppName(), cluster.Name, deploy.Status.ReadyReplicas, deploy.Status.Replicas)
}

func getDeployment(ctx types.TestContext, cluster types.Cluster, namespace, name string) (*appsv1.Deployment, error) {
	deploy := &appsv1.Deployment{}
	key := k8stypes.NamespacedName{Name: name, Namespace: namespace}

	err := cluster.Client.Get(ctx.Context(), key, deploy)
	if err != nil {
		return nil, err
	}

	return deploy, nil
}
