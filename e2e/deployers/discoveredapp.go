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

const (
	ramenOpsNamespace = "ramen-ops"
)

type DiscoveredApp struct{}

func (d DiscoveredApp) GetName() string {
	return "disapp"
}

func (d DiscoveredApp) GetNamespace() string {
	return ramenOpsNamespace
}

// Deploy creates a workload on the first managed cluster.
func (d DiscoveredApp) Deploy(ctx types.Context) error {
	log := ctx.Logger()
	appNamespace := ctx.AppNamespace()

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

	drpolicy, err := util.GetDRPolicy(util.Ctx.Hub, util.DefaultDRPolicyName)
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

	log.Infof("Deployed discovered app \"%s/%s\" on cluster %q",
		appNamespace, ctx.Workload().GetAppName(), drpolicy.Spec.DRClusters[0])

	return nil
}

// Undeploy deletes the workload from the managed clusters.
func (d DiscoveredApp) Undeploy(ctx types.Context) error {
	log := ctx.Logger()
	appNamespace := ctx.AppNamespace()

	drpolicy, err := util.GetDRPolicy(util.Ctx.Hub, util.DefaultDRPolicyName)
	if err != nil {
		return err
	}

	// delete app on both clusters
	if err := DeleteDiscoveredApps(ctx, appNamespace, drpolicy.Spec.DRClusters[0]); err != nil {
		return err
	}

	if err := DeleteDiscoveredApps(ctx, appNamespace, drpolicy.Spec.DRClusters[1]); err != nil {
		return err
	}

	// delete namespace on both clusters
	if err := util.DeleteNamespace(util.Ctx.C1, appNamespace, log); err != nil {
		return err
	}

	if err := util.DeleteNamespace(util.Ctx.C2, appNamespace, log); err != nil {
		return err
	}

	log.Infof("Undeployed discovered app \"%s/%s\"", appNamespace, ctx.Workload().GetAppName())

	return nil
}

func (d DiscoveredApp) IsDiscovered() bool {
	return true
}
