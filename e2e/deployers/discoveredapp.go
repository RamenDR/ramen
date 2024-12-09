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

type DiscoveredApp struct{}

func (d DiscoveredApp) GetName() string {
	return "Disapp"
}

func (d DiscoveredApp) GetNamespace() string {
	return util.RamenOpsNamespace
}

// Deploy creates a workload on the first managed cluster.
func (d DiscoveredApp) Deploy(ctx types.Context) error {
	log := ctx.Logger()
	appNamespace := ctx.AppNamespace()

	log.Info("Deploying workload")

	// create namespace in both dr clusters
	if err := util.CreateNamespaceAndAddAnnotation(appNamespace); err != nil {
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

	drpolicy, err := util.GetDRPolicy(util.Ctx.Hub.Client, util.DefaultDRPolicyName)
	if err != nil {
		return err
	}

	cmd := exec.Command("kubectl", "apply", "-k", tempDir, "-n", appNamespace,
		"--context", drpolicy.Spec.DRClusters[0], "--timeout=5m")

	if out, err := cmd.Output(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("%w: stdout=%q stderr=%q", err, out, ee.Stderr)
		}

		return err
	}

	if err = WaitWorkloadHealth(ctx, util.Ctx.C1.Client, appNamespace); err != nil {
		return err
	}

	log.Info("Workload deployed")

	return nil
}

// Undeploy deletes the workload from the managed clusters.
func (d DiscoveredApp) Undeploy(ctx types.Context) error {
	log := ctx.Logger()
	appNamespace := ctx.AppNamespace()

	log.Info("Undeploying workload")

	drpolicy, err := util.GetDRPolicy(util.Ctx.Hub.Client, util.DefaultDRPolicyName)
	if err != nil {
		return err
	}

	log.Infof("Deleting discovered apps on cluster %q", drpolicy.Spec.DRClusters[0])

	// delete app on both clusters
	if err := DeleteDiscoveredApps(ctx, appNamespace, drpolicy.Spec.DRClusters[0]); err != nil {
		return err
	}

	log.Infof("Deletting discovered apps on cluster %q", drpolicy.Spec.DRClusters[1])

	if err := DeleteDiscoveredApps(ctx, appNamespace, drpolicy.Spec.DRClusters[1]); err != nil {
		return err
	}

	log.Infof("Deleting namespace %q on cluster %q", appNamespace, drpolicy.Spec.DRClusters[0])

	// delete namespace on both clusters
	if err := util.DeleteNamespace(util.Ctx.C1.Client, appNamespace, log); err != nil {
		return err
	}

	log.Infof("Deleting namespace %q on cluster %q", appNamespace, drpolicy.Spec.DRClusters[1])

	if err := util.DeleteNamespace(util.Ctx.C2.Client, appNamespace, log); err != nil {
		return err
	}

	log.Info("Workload undeployed")

	return nil
}

func (d DiscoveredApp) IsWorkloadSupported(w types.Workload) bool {
	return w.GetName() != "Deploy-cephfs"
}

func (d DiscoveredApp) IsDiscovered() bool {
	return true
}
