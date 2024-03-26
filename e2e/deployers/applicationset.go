package deployers

import "github.com/ramendr/ramen/e2e/workloads"

type ApplicationSet struct {
	repoURL  string // From the Workload?
	path     string // From the Workload?
	revision string // From the Workload?
}

func (a ApplicationSet) Deploy(w workloads.Workload) error {
	// Generate a Placement for the Workload
	// Generate a Binding for the namespace?
	// Generate an ApplicationSet for the Workload
	// - Kustomize the Workload; call Workload.Kustomize(StorageType)
	// Address namespace/label/suffix as needed for various resources
	w.Kustomize()
	return nil
}

func (a ApplicationSet) Undeploy(w workloads.Workload) error {
	// Delete Placement, Binding, ApplicationSet
	return nil
}
