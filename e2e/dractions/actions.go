// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

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
	util.Ctx.Log.Info("enter EnableProtection " + w.GetName() + "/" + d.GetName())

	return nil
}

func DisableProtection(w workloads.Workload, d deployers.Deployer) error {
	util.Ctx.Log.Info("enter DisableProtection " + w.GetName() + "/" + d.GetName())

	return nil
}

func Failover(w workloads.Workload, d deployers.Deployer) error {
	util.Ctx.Log.Info("enter Failover " + w.GetName() + "/" + d.GetName())

	return nil
}

func Relocate(w workloads.Workload, d deployers.Deployer) error {
	util.Ctx.Log.Info("enter Relocate " + w.GetName() + "/" + d.GetName())

	return nil
}
