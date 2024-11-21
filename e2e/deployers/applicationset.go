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
	log := util.Ctx.Log.WithName(name)

	log.Info("Deploying workload")

	err := CreateManagedClusterSetBinding(McsbName, namespace)
	if err != nil {
		return err
	}

	err = CreatePlacement(name, namespace, log)
	if err != nil {
		return err
	}

	err = CreatePlacementDecisionConfigMap(name, namespace, log)
	if err != nil {
		return err
	}

	err = CreateApplicationSet(a, w, log)
	if err != nil {
		return err
	}

	return err
}

func (a ApplicationSet) Undeploy(w workloads.Workload) error {
	name := GetCombinedName(a, w)
	namespace := util.ArgocdNamespace
	log := util.Ctx.Log.WithName(name)

	log.Info("Undeploying workload")

	err := DeleteApplicationSet(a, w, log)
	if err != nil {
		return err
	}

	err = DeleteConfigMap(name, namespace, log)
	if err != nil {
		return err
	}

	err = DeletePlacement(name, namespace, log)
	if err != nil {
		return err
	}

	// multiple appsets could use the same mcsb in argocd ns.
	// so delete mcsb if only 1 appset is in argocd ns
	lastAppset, err := isLastAppsetInArgocdNs(namespace, log)
	if err != nil {
		return err
	}

	if lastAppset {
		err = DeleteManagedClusterSetBinding(McsbName, namespace, log)
		if err != nil {
			return err
		}
	}

	return nil
}

func (a ApplicationSet) GetName() string {
	return "Appset"
}

func (a ApplicationSet) IsWorkloadSupported(w workloads.Workload) bool {
	return true
}
