package deployers

import "github.com/ramendr/ramen/e2e/workloads"

type Imperative struct {
	targetCluster string
}

func (i Imperative) Deploy(w workloads.Workload) error {
	// this needs to deploy to a particular cluster the resources from git, appropriately Kustomized
	// GetResources Kustomized from the Workload and apply to the targetCluster
	w.GetResources()
	return nil
}

func (i Imperative) Undeploy(w workloads.Workload) error {
	// Undeploy resources from Workload on targetCluster
	w.GetResources()
	return nil
}
