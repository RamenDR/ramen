// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"github.com/ramendr/ramen/e2e/util"
	"github.com/ramendr/ramen/e2e/workloads"
)

type Subscription struct {
	// NamePrefix helps when the same workload needs to run in parallel with different deployers.
	// In the future we potentially would add a resource suffix that is either randomly generated
	// or a hash of the full test name, to handle cases where we want to run the "same" combination
	// of deployer+workload for various reasons.
	NamePrefix string
	McsbName   string
}

func (s *Subscription) Init() {
	s.NamePrefix = "sub-"
	s.McsbName = "default"
}

func (s Subscription) GetName() string {
	return "Subscription"
}

func (s Subscription) Deploy(w workloads.Workload) error {
	// Generate a Placement for the Workload
	// Use the global Channel
	// Generate a Binding for the namespace (does this need clusters?)
	// Generate a Subscription for the Workload
	// - Kustomize the Workload; call Workload.Kustomize(StorageType)
	// Address namespace/label/suffix as needed for various resources
	util.Ctx.Log.Info("enter Deploy " + w.GetName() + "/Subscription")

	// w.Kustomize()
	err := createNamespace()
	if err != nil {
		return err
	}

	err = createManagedClusterSetBinding()
	if err != nil {
		return err
	}

	err = createPlacement()
	if err != nil {
		return err
	}

	err = createSubscription()
	if err != nil {
		return err
	}

	return nil
}

func (s Subscription) Undeploy(w workloads.Workload) error {
	// Delete Subscription, Placement, Binding
	util.Ctx.Log.Info("enter Undeploy " + w.GetName() + "/Subscription")

	err := deleteSubscription()
	if err != nil {
		return err
	}

	err = deletePlacement()
	if err != nil {
		return err
	}

	err = deleteManagedClusterSetBinding()
	if err != nil {
		return err
	}

	err = deleteNamespace()
	if err != nil {
		return err
	}

	return nil
}
