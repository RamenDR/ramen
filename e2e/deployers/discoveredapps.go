// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"fmt"

	"github.com/ramendr/ramen/e2e/util"
	"github.com/ramendr/ramen/e2e/workloads"
)

type DiscoveredApps struct{}

func (s DiscoveredApps) GetName() string {
	return "Disapp"
}

func (s DiscoveredApps) Deploy(w workloads.Workload) error {
	name := GetCombinedName(s, w)
	namespace := name

	util.Ctx.Log.Info("enter Deploy " + name)

	// create namespace in both dr clusters
	if err := util.CreateNamespaceAndAddAnnotation(namespace); err != nil {
		return err
	}

	// create pvc and deployment on dr1
	pvc, err := GetPVCFromFile()
	if err != nil {
		return err
	}

	// TODO: kustomize pvc with workload pvcspec
	// consider to add getPVCSpec() in workload interface to change pvc sc name and access mode
	// right now discoveredapps only supports rbd, so do it later

	err = createPVC(util.Ctx.C1.CtrlClient, pvc, namespace)
	if err != nil {
		err = fmt.Errorf("unable to create pvc %s: %w",
			pvc.Name, err)

		return err
	}

	deploy, err := GetDeploymentFromFile()
	if err != nil {
		return err
	}

	err = createDeployment(util.Ctx.C1.CtrlClient, deploy, namespace)
	if err != nil {
		err = fmt.Errorf("unable to create deployment %s: %w",
			deploy.Name, err)

		return err
	}

	return waitDeploymentReady(util.Ctx.C1.CtrlClient, namespace, deploy.Name)
}

func (s DiscoveredApps) Undeploy(w workloads.Workload) error {
	name := GetCombinedName(s, w)
	namespace := name

	util.Ctx.Log.Info("enter Undeploy " + name)

	// delete discovered apps on both clusters
	if err := DeleteDiscoveredApps(util.Ctx.C1.CtrlClient, namespace); err != nil {
		return err
	}

	if err := DeleteDiscoveredApps(util.Ctx.C2.CtrlClient, namespace); err != nil {
		return err
	}

	// delete namespace on both clusters
	if err := util.DeleteNamespace(util.Ctx.C1.CtrlClient, namespace); err != nil {
		return err
	}

	return util.DeleteNamespace(util.Ctx.C2.CtrlClient, namespace)
}

func (s DiscoveredApps) IsWorkloadSupported(w workloads.Workload) bool {
	return w.GetName() != "Deploy-cephfs"
}
