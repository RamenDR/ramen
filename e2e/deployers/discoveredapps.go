// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"os"
	"os/exec"

	"github.com/ramendr/ramen/e2e/util"
	"github.com/ramendr/ramen/e2e/workloads"
)

type DiscoveredApps struct{}

func (d DiscoveredApps) GetName() string {
	return "Disapp"
}

func (d DiscoveredApps) Deploy(w workloads.Workload) error {
	name := GetCombinedName(d, w)
	namespace := name
	log := util.Ctx.Log.WithName(name)

	log.Info("Deploying workload")

	// create namespace in both dr clusters
	if err := util.CreateNamespaceAndAddAnnotation(namespace); err != nil {
		return err
	}

	tempDir, err := os.MkdirTemp("", "ramen-")
	if err != nil {
		return err
	}

	// Clean up by removing the temporary directory when done
	defer os.RemoveAll(tempDir)

	if err = CreateKustomizationFile(w, tempDir); err != nil {
		return err
	}

	drpolicy, err := util.GetDRPolicy(util.Ctx.Hub.CtrlClient, util.DefaultDRPolicyName)
	if err != nil {
		return err
	}

	cmd := exec.Command("kubectl", "apply", "-k", tempDir, "-n", namespace,
		"--context", drpolicy.Spec.DRClusters[0], "--timeout=5m")

	// Run the command and capture the output
	if out, err := cmd.Output(); err != nil {
		log.Info(string(out))

		return err
	}

	if err = WaitWorkloadHealth(util.Ctx.C1.CtrlClient, namespace, w, log); err != nil {
		return err
	}

	log.Info("Workload deployed")

	return nil
}

func (d DiscoveredApps) Undeploy(w workloads.Workload) error {
	name := GetCombinedName(d, w)
	namespace := name // this namespace is in dr clusters
	log := util.Ctx.Log.WithName(name)

	log.Info("Undeploying workload")

	drpolicy, err := util.GetDRPolicy(util.Ctx.Hub.CtrlClient, util.DefaultDRPolicyName)
	if err != nil {
		return err
	}

	log.Info("Deleting discovered apps on " + drpolicy.Spec.DRClusters[0])

	// delete app on both clusters
	if err := DeleteDiscoveredApps(w, namespace, drpolicy.Spec.DRClusters[0], log); err != nil {
		return err
	}

	log.Info("Deletting discovered apps on " + drpolicy.Spec.DRClusters[1])

	if err := DeleteDiscoveredApps(w, namespace, drpolicy.Spec.DRClusters[1], log); err != nil {
		return err
	}

	log.Info("Deleting namespace " + namespace + " on " + drpolicy.Spec.DRClusters[0])

	// delete namespace on both clusters
	if err := util.DeleteNamespace(util.Ctx.C1.CtrlClient, namespace, log); err != nil {
		return err
	}

	log.Info("Deleting namespace " + namespace + " on " + drpolicy.Spec.DRClusters[1])

	if err := util.DeleteNamespace(util.Ctx.C2.CtrlClient, namespace, log); err != nil {
		return err
	}

	log.Info("Workload undeployed")

	return nil
}

func (d DiscoveredApps) IsWorkloadSupported(w workloads.Workload) bool {
	return w.GetName() != "Deploy-cephfs"
}
