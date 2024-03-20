package deployer

import "samples.foo/e2e/workload"

// Deployer interface has methods to deploy a workload to a cluster
type Deployer interface {
	Deploy(workload.Workload) error
	Undeploy(workload.Workload) error
	// Scale(Workload) for adding/removing PVCs; in Deployer even though scaling is a Workload interface
	// as we can Kustomize the Workload and change the deployer to perform the right action
	// Resize(Workload) for changing PVC(s) size
	Health(workload.Workload) error
}

type ApplicationSet struct {
	repoURL  string // From the Workload?
	path     string // From the Workload?
	revision string // From the Workload?
}

func (a ApplicationSet) Deploy(w workload.Workload) error {
	// Generate a Placement for the Workload
	// Generate a Binding for the namespace?
	// Generate an ApplicationSet for the Workload
	// - Kustomize the Workload; call Workload.Kustomize(StorageType)
	// Address namespace/label/suffix as needed for various resources
	w.Kustomize()
	return nil
}

func (a ApplicationSet) Undeploy(w workload.Workload) error {
	// Delete Placement, Binding, ApplicationSet
	return nil
}

type Imperative struct {
	targetCluster string
}

func (i Imperative) Deploy(w workload.Workload) error {
	// this needs to deploy to a particular cluster the resources from git, appropriately Kustomized
	// GetResources Kustomized from the Workload and apply to the targetCluster
	w.GetResources()
	return nil
}

func (i Imperative) Undeploy(w workload.Workload) error {
	// Undeploy resources from Workload on targetCluster
	w.GetResources()
	return nil
}
