// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
)

type ApplicationSet struct{}

// Deploy creates an ApplicationSet on the hub cluster, creating the workload on one of the managed clusters.
func (a ApplicationSet) Deploy(ctx types.Context) error {
	name := ctx.Name()
	log := ctx.Logger()
	hubNamespace := ctx.ManagementNamespace()

	log.Info("Deploying workload")

	err := CreateManagedClusterSetBinding(McsbName, hubNamespace)
	if err != nil {
		return err
	}

	err = CreatePlacement(ctx, name, hubNamespace)
	if err != nil {
		return err
	}

	err = CreatePlacementDecisionConfigMap(ctx, name, hubNamespace)
	if err != nil {
		return err
	}

	err = CreateApplicationSet(ctx, a)
	if err != nil {
		return err
	}

	return err
}

// Undeploy deletes an ApplicationSet from the hub cluster, deleting the workload from the managed clusters.
func (a ApplicationSet) Undeploy(ctx types.Context) error {
	name := ctx.Name()
	log := ctx.Logger()
	hubNamespace := ctx.ManagementNamespace()

	log.Info("Undeploying workload")

	err := DeleteApplicationSet(ctx, a)
	if err != nil {
		return err
	}

	err = DeleteConfigMap(ctx, name, hubNamespace)
	if err != nil {
		return err
	}

	err = DeletePlacement(ctx, name, hubNamespace)
	if err != nil {
		return err
	}

	// multiple appsets could use the same mcsb in argocd ns.
	// so delete mcsb if only 1 appset is in argocd ns
	lastAppset, err := isLastAppsetInArgocdNs(hubNamespace)
	if err != nil {
		return err
	}

	if lastAppset {
		err = DeleteManagedClusterSetBinding(ctx, McsbName, hubNamespace)
		if err != nil {
			return err
		}
	}

	return nil
}

func (a ApplicationSet) GetName() string {
	return "Appset"
}

func (a ApplicationSet) GetNamespace() string {
	return util.ArgocdNamespace
}

func (a ApplicationSet) IsWorkloadSupported(w types.Workload) bool {
	return true
}
