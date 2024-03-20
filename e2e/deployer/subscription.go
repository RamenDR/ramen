package deployer

import (
	"samples.foo/e2e/util"
	"samples.foo/e2e/workload"
)

type Subscription struct {
	branch  string
	path    string
	channel string
	Ctx     *util.TestContext
}

func (s Subscription) Deploy(w workload.Workload) error {
	// Generate a Placement for the Workload
	// Use the global Channel
	// Generate a Binding for the namespace (does this need clusters?)
	// Generate a Subscription for the Workload
	// - Kustomize the Workload; call Workload.Kustomize(StorageType)
	// Address namespace/label/suffix as needed for various resources
	s.Ctx.Log.Info("enter Subscription Deploy")
	w.Kustomize()
	return nil
}

func (s Subscription) Undeploy(w workload.Workload) error {
	// Delete Subscription, Placement, Binding
	s.Ctx.Log.Info("enter Subscription Undeploy")
	return nil
}

func (s Subscription) Health(w workload.Workload) error {
	s.Ctx.Log.Info("enter Subscription Health")
	w.GetResources()
	// Check health using reflection to known types of the workload on the targetCluster
	// Again if using reflection can be a common function outside of deployer as such
	return nil
}
