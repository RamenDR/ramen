package dractions

import (
	"time"

	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/util"
	"github.com/ramendr/ramen/e2e/workloads"
)

const (
	OcmSchedulingDisable = "cluster.open-cluster-management.io/experimental-scheduling-disable"
	DefaultDRPolicyName  = "dr-policy"
	FiveSecondsDuration  = 5 * time.Second
)

func EnableProtection(w workloads.Workload, d deployers.Deployer) error {
	util.Ctx.Log.Info("enter EnableProtection " + w.GetID() + "/" + d.GetID())

	return nil
}

func DisableProtection(w workloads.Workload, d deployers.Deployer) error {
	util.Ctx.Log.Info("enter DisableProtection " + w.GetID() + "/" + d.GetID())

	return nil
}

func Failover(w workloads.Workload, d deployers.Deployer) error {
	util.Ctx.Log.Info("enter Failover " + w.GetID() + "/" + d.GetID())

	return nil
}

func Relocate(w workloads.Workload, d deployers.Deployer) error {
	util.Ctx.Log.Info("enter Relocate " + w.GetID() + "/" + d.GetID())

	return nil
}
