package dractions

import (
	"samples.foo/e2e/deployer"
	"samples.foo/e2e/util"
	"samples.foo/e2e/workload"
)

type DRActions struct {
	Ctx *util.TestContext
}

func (r DRActions) EnableProtection(w workload.Workload, d deployer.Deployer) error {
	// If AppSet/Subscription, find Placement
	// Determine DRPolicy
	// Determine preferredCluster
	// Determine PVC label selector
	// Determine KubeObjectProtection requirements if Imperative (?)
	// Create DRPC, in desired namespace
	r.Ctx.Log.Info("enter dractions EnableProtection")
	return nil
}

func (r DRActions) Failover(w workload.Workload, d deployer.Deployer) error {
	// Determine DRPC
	// Check Placement
	// Failover to alternate in DRPolicy as the failoverCluster
	// Update DRPC
	r.Ctx.Log.Info("enter dractions Failover")
	return nil
}

func (r DRActions) Relocate(w workload.Workload, d deployer.Deployer) error {
	// Determine DRPC
	// Check Placement
	// Relocate to Primary in DRPolicy as the PrimaryCluster
	// Update DRPC
	r.Ctx.Log.Info("enter dractions Relocate")
	return nil
}
