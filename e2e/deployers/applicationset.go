// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"github.com/ramendr/ramen/e2e/util"
	"github.com/ramendr/ramen/e2e/workloads"
)

type ApplicationSet struct{}

func (a ApplicationSet) Deploy(w workloads.Workload) error {
	util.Ctx.Log.Info("enter Deploy " + w.GetName() + "/" + a.GetName())

	name := GetCombinedName(a, w)
	namespace := util.ArgocdNamespace

	err := createManagedClusterSetBinding(McsbName, namespace)
	if err != nil {
		return err
	}

	err = createPlacement(name, namespace)
	if err != nil {
		return err
	}

	err = createPlacementDecisionConfigMap(name, namespace)
	if err != nil {
		return err
	}

	err = createApplicationSet(a, w)
	if err != nil {
		return err
	}

	return err
}

func (a ApplicationSet) Undeploy(w workloads.Workload) error {
	util.Ctx.Log.Info("enter Undeploy " + w.GetName() + "/" + a.GetName())

	name := GetCombinedName(a, w)
	namespace := util.ArgocdNamespace

	err := deleteApplicationSet(a, w)
	if err != nil {
		return err
	}

	err = deleteConfigMap(name, namespace)
	if err != nil {
		return err
	}

	err = deletePlacement(name, namespace)
	if err != nil {
		return err
	}

	err = deleteManagedClusterSetBinding(McsbName, namespace)
	if err != nil {
		return err
	}

	return nil
}

func (a ApplicationSet) GetName() string {
	return "Appset"
}
