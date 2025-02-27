// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package workloads

import (
	"context"
	"slices"
	"strings"

	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
	appsv1 "k8s.io/api/apps/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

type Deployment struct {
	Path     string
	Revision string
	AppName  string
	Name     string
	PVCSpec  util.PVCSpec
}

func (w Deployment) GetAppName() string {
	return w.AppName
}

func (w Deployment) GetName() string {
	return w.Name
}

func (w Deployment) GetPath() string {
	return w.Path
}

func (w Deployment) GetRevision() string {
	return w.Revision
}

func (w Deployment) SupportsDeployer(d types.Deployer) bool {
	return !slices.Contains(w.PVCSpec.UnsupportedDeployers, strings.ToLower(d.GetName()))
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
func (w Deployment) Health(ctx types.Context, cluster util.Cluster, namespace string) error {
	log := ctx.Logger()

	deploy, err := getDeployment(cluster, namespace, w.GetAppName())
	if err != nil {
		return err
	}

	if deploy.Status.Replicas == deploy.Status.ReadyReplicas {
		log.Debugf("Deployment \"%s/%s\" is ready", namespace, w.GetAppName())

		return nil
	}

	return nil
}

func getDeployment(cluster util.Cluster, namespace, name string) (*appsv1.Deployment, error) {
	deploy := &appsv1.Deployment{}
	key := k8stypes.NamespacedName{Name: name, Namespace: namespace}

	err := cluster.Client.Get(context.Background(), key, deploy)
	if err != nil {
		return nil, err
	}

	return deploy, nil
}
