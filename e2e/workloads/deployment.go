// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package workloads

import (
	"context"

	"github.com/go-logr/logr"
	"github.com/ramendr/ramen/e2e/util"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
func (w Deployment) Health(client client.Client, namespace string, log logr.Logger) error {
	deploy, err := getDeployment(client, namespace, w.GetAppName())
	if err != nil {
		return err
	}

	if deploy.Status.Replicas == deploy.Status.ReadyReplicas {
		log.Info("Deployment is ready")

		return nil
	}

	return nil
}

func getDeployment(client client.Client, namespace, name string) (*appsv1.Deployment, error) {
	deploy := &appsv1.Deployment{}
	key := types.NamespacedName{Name: name, Namespace: namespace}

	err := client.Get(context.Background(), key, deploy)
	if err != nil {
		return nil, err
	}

	return deploy, nil
}
