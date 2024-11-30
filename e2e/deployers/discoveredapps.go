// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
)

type DiscoveredApps struct{}

func (d DiscoveredApps) GetName() string {
	return "Disapp"
}

func (d DiscoveredApps) Deploy(ctx types.Context) error {
	name := ctx.Name()
	log := ctx.Logger()
	namespace := name

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

	if err = CreateKustomizationFile(ctx, tempDir); err != nil {
		return err
	}

	drpolicy, err := util.GetDRPolicy(util.Ctx.Hub.CtrlClient, util.DefaultDRPolicyName)
	if err != nil {
		return err
	}

	cmd := exec.Command("kubectl", "apply", "-k", tempDir, "-n", namespace,
		"--context", drpolicy.Spec.DRClusters[0], "--timeout=5m")

	if out, err := cmd.Output(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("%w: stdout=%q stderr=%q", err, out, ee.Stderr)
		}

		return err
	}

	if err = WaitWorkloadHealth(ctx, util.Ctx.C1.CtrlClient, namespace); err != nil {
		return err
	}

	log.Info("Workload deployed")

	return nil
}

func (d DiscoveredApps) Undeploy(ctx types.Context) error {
	name := ctx.Name()
	log := ctx.Logger()
	namespace := name // this namespace is in dr clusters

	log.Info("Undeploying workload")

	drpolicy, err := util.GetDRPolicy(util.Ctx.Hub.CtrlClient, util.DefaultDRPolicyName)
	if err != nil {
		return err
	}

	log.Info("Deleting discovered apps on " + drpolicy.Spec.DRClusters[0])

	// delete app on both clusters
	if err := DeleteDiscoveredApps(ctx, namespace, drpolicy.Spec.DRClusters[0]); err != nil {
		return err
	}

	log.Info("Deletting discovered apps on " + drpolicy.Spec.DRClusters[1])

	if err := DeleteDiscoveredApps(ctx, namespace, drpolicy.Spec.DRClusters[1]); err != nil {
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

func (d DiscoveredApps) IsWorkloadSupported(w types.Workload) bool {
	return w.GetName() != "Deploy-cephfs"
}
