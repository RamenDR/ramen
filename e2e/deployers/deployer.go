package deployers

import "github.com/ramendr/ramen/e2e/workloads"

// Deployer interface has methods to deploy a workload to a cluster
type Deployer interface {
	Deploy(workloads.Workload) error
	Undeploy(workloads.Workload) error
	// Scale(Workload) for adding/removing PVCs; in Deployer even though scaling is a Workload interface
	// as we can Kustomize the Workload and change the deployer to perform the right action
	// Resize(Workload) for changing PVC(s) size
	Health(workloads.Workload) error
}
