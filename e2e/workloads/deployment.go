// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package workloads

type Deployment struct {
	RepoURL  string
	Path     string
	Revision string
	AppName  string
}

func (w *Deployment) Init() {
	w.RepoURL = "https://github.com/ramendr/ocm-ramen-samples.git"
	w.Path = "workloads/deployment/k8s-regional-rbd"
	w.Revision = "main"
	w.AppName = "busybox"
}

func (w Deployment) GetAppName() string {
	return w.AppName
}

func (w Deployment) GetID() string {
	return "Deployment"
}

func (w Deployment) GetRepoURL() string {
	return w.RepoURL
}

func (w Deployment) GetPath() string {
	return w.Path
}

func (w Deployment) GetRevision() string {
	return w.Revision
}

func (w Deployment) Kustomize() error {
	return nil
}

func (w Deployment) GetResources() error {
	// this would be a common function given the vars? But we need the resources Kustomized
	return nil
}

func (w Deployment) Health() error {
	// Check the workload health on a targetCluster
	return nil
}
