// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
)

type ApplicationSet struct{}

func (a ApplicationSet) Deploy(ctx types.Context) error {
	name := ctx.Name()
	log := ctx.Logger()
	namespace := util.ArgocdNamespace

	log.Info("Deploying workload")

	err := CreateManagedClusterSetBinding(McsbName, namespace)
	if err != nil {
		return err
	}

	err = CreatePlacement(ctx, name, namespace)
	if err != nil {
		return err
	}

	err = CreatePlacementDecisionConfigMap(ctx, name, namespace)
	if err != nil {
		return err
	}

	err = CreateApplicationSet(ctx, a)
	if err != nil {
		return err
	}

	return err
}

func (a ApplicationSet) Undeploy(ctx types.Context) error {
	name := ctx.Name()
	log := ctx.Logger()
	namespace := util.ArgocdNamespace

	log.Info("Undeploying workload")

	err := DeleteApplicationSet(ctx, a)
	if err != nil {
		return err
	}

	err = DeleteConfigMap(ctx, name, namespace)
	if err != nil {
		return err
	}

	err = DeletePlacement(ctx, name, namespace)
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
		err = DeleteManagedClusterSetBinding(ctx, McsbName, namespace)
		if err != nil {
			return err
		}
	}

	return nil
}

func (a ApplicationSet) GetName() string {
	return "Appset"
}

func (a ApplicationSet) IsWorkloadSupported(w types.Workload) bool {
	return true
}
