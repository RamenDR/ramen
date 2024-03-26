package dractions

import (
	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/util"
	"github.com/ramendr/ramen/e2e/workloads"
)

type DRActions struct {
	Ctx *util.TestContext
}

func (r DRActions) EnableProtection(w workloads.Workload, d deployers.Deployer) error {
	// If AppSet/Subscription, find Placement
	// Determine DRPolicy
	// Determine preferredCluster
	// Determine PVC label selector
	// Determine KubeObjectProtection requirements if Imperative (?)
	// Create DRPC, in desired namespace
	r.Ctx.Log.Info("enter dractions EnableProtection")
	return nil
}

func (r DRActions) Failover(w workloads.Workload, d deployers.Deployer) error {
	// Determine DRPC
	// Check Placement
	// Failover to alternate in DRPolicy as the failoverCluster
	// Update DRPC
	r.Ctx.Log.Info("enter dractions Failover")
	return nil
}

func (r DRActions) Relocate(w workloads.Workload, d deployers.Deployer) error {
	// Determine DRPC
	// Check Placement
	// Relocate to Primary in DRPolicy as the PrimaryCluster
	// Update DRPC
	r.Ctx.Log.Info("enter dractions Relocate")
	return nil
}
