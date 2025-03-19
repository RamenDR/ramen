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
	return "disapp"
}

func (d DiscoveredApp) GetNamespace(ctx types.Context) string {
	return ctx.Config().Namespaces.RamenOpsNamespace
}

// Deploy creates a workload on the first managed cluster.
func (d DiscoveredApp) Deploy(ctx types.Context) error {
	log := ctx.Logger()
	appNamespace := ctx.AppNamespace()

	// Deploys the application on the first DR cluster (c1).
	cluster := ctx.Env().C1

	// create namespace in both dr clusters
	if err := util.CreateNamespaceAndAddAnnotation(ctx.Env(), appNamespace, log); err != nil {
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

	log.Infof("Deploying discovered app \"%s/%s\" in cluster %q",
		appNamespace, ctx.Workload().GetAppName(), cluster.Name)

	cmd := exec.Command("kubectl", "apply", "-k", tempDir, "-n", appNamespace,
		"--context", cluster.Name, "--timeout=5m")

	if out, err := cmd.Output(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("%w: stdout=%q stderr=%q", err, out, ee.Stderr)
		}

		return err
	}

	if err = WaitWorkloadHealth(ctx, ctx.Env().C1, appNamespace); err != nil {
		return err
	}

	log.Info("Workload deployed")

	return nil
}

// Undeploy deletes the workload from the managed clusters.
func (d DiscoveredApp) Undeploy(ctx types.Context) error {
	log := ctx.Logger()
	config := ctx.Config()
	appNamespace := ctx.AppNamespace()

	drpolicy, err := util.GetDRPolicy(ctx.Env().Hub, config.DRPolicy)
	if err != nil {
		return err
	}

	log.Infof("Undeploying discovered app \"%s/%s\" in clusters %q and %q",
		appNamespace, ctx.Workload().GetAppName(), drpolicy.Spec.DRClusters[0], drpolicy.Spec.DRClusters[1])

	// delete app on both clusters
	if err := DeleteDiscoveredApps(ctx, appNamespace, drpolicy.Spec.DRClusters[0]); err != nil {
		return err
	}

	if err := DeleteDiscoveredApps(ctx, appNamespace, drpolicy.Spec.DRClusters[1]); err != nil {
		return err
	}

	// delete namespace on both clusters
	if err := util.DeleteNamespace(ctx.Env().C1, appNamespace, log); err != nil {
		return err
	}

	if err := util.DeleteNamespace(ctx.Env().C2, appNamespace, log); err != nil {
		return err
	}

	log.Info("Workload undeployed")

	return nil
}

func (d DiscoveredApp) IsDiscovered() bool {
	return true
}
