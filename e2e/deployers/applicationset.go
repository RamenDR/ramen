// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"github.com/ramendr/ramen/e2e/util"
	"github.com/ramendr/ramen/e2e/workloads"
)

type ApplicationSet struct{}

func (a ApplicationSet) Deploy(w workloads.Workload) error {
	name := GetCombinedName(a, w)
	namespace := util.ArgocdNamespace

	util.Ctx.Log.Info("enter Deploy " + name)

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
	name := GetCombinedName(a, w)
	namespace := util.ArgocdNamespace

	util.Ctx.Log.Info("enter Undeploy " + name)

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

	// multiple appsets could use the same mcsb in argocd ns.
	// so delete mcsb if only 1 appset is in argocd ns
	lastAppset, err := isLastAppsetInArgocdNs(namespace)
	if err != nil {
		return err
	}

	if lastAppset {
		err = deleteManagedClusterSetBinding(McsbName, namespace)
		if err != nil {
			return err
		}
	}

	return nil
}

func (a ApplicationSet) GetName() string {
	return "Appset"
}
