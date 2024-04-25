// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"github.com/ramendr/ramen/e2e/util"
	"github.com/ramendr/ramen/e2e/workloads"
)

type Subscription struct {
	NamePrefix string
	McsbName   string

	ChannelName      string
	ChannelNamespace string
}

func (s *Subscription) Init() {
	s.NamePrefix = "sub-"
	s.McsbName = "default"
	s.ChannelName = "ramen-gitops"
	s.ChannelNamespace = "ramen-samples"
}

func (s Subscription) GetID() string {
	return "Subscription"
}

func (s Subscription) Deploy(w workloads.Workload) error {
	// Generate a Placement for the Workload
	// Use the global Channel
	// Generate a Binding for the namespace (does this need clusters?)
	// Generate a Subscription for the Workload
	// - Kustomize the Workload; call Workload.Kustomize(StorageType)
	// Address namespace/label/suffix as needed for various resources
	util.Ctx.Log.Info("enter Deploy " + w.GetID() + "/Subscription")

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
	util.Ctx.Log.Info("enter Undeploy " + w.GetID() + "/Subscription")

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
