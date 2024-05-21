// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package workloads

import "github.com/ramendr/ramen/e2e/util"

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

func (w Deployment) Health() error {
	// Check the workload health on a targetCluster
	return nil
}
