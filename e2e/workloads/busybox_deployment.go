package workloads

import "github.com/ramendr/ramen/e2e/util"

type BusyboxDeployment struct {
	repoURL  string // Possibly all this is part of Workload than each implementation of the interfaces?
	path     string
	revision string
	Ctx      *util.TestContext
}

func (d BusyboxDeployment) Kustomize() error {
	d.Ctx.Log.Info("enter BusyboxDeployment Kustomize")
	return nil
}

func (d BusyboxDeployment) GetResources() error {
	// this would be a common function given the vars? But we need the resources Kustomized
	d.Ctx.Log.Info("enter BusyboxDeployment GetResources")
	return nil
}

func (d BusyboxDeployment) Health() error {
	// Check the workload health on a targetCluster
	d.Ctx.Log.Info("enter BusyboxDeployment Health")
	return nil
}
