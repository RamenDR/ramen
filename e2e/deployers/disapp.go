// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
)

type DiscoveredApp struct{}

func (d DiscoveredApp) GetName() string {
	return "disapp"
}

func (d DiscoveredApp) GetNamespace(ctx types.TestContext) string {
	return ctx.Config().Namespaces.RamenOpsNamespace
}

// Deploy creates a workload on the first managed cluster.
func (d DiscoveredApp) Deploy(ctx types.TestContext) error {
	log := ctx.Logger()
	appNamespace := ctx.AppNamespace()

	// Deploys the application on the first DR cluster (c1).
	cluster := ctx.Env().C1

	// Create namespace on the first DR cluster (c1)
	err := util.CreateNamespace(ctx, cluster, appNamespace)
	if err != nil {
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

	if err := runCommand(
		ctx.Context(),
		"kubectl",
		"apply",
		"--kustomize", tempDir,
		"--namespace", appNamespace,
		"--kubeconfig", cluster.Kubeconfig,
		"--timeout=5m",
	); err != nil {
		return err
	}

	if err = WaitWorkloadHealth(ctx, ctx.Env().C1, appNamespace); err != nil {
		return err
	}

	log.Info("Workload deployed")

	return nil
}

// Undeploy deletes the workload from the managed clusters.
func (d DiscoveredApp) Undeploy(ctx types.TestContext) error {
	log := ctx.Logger()
	appNamespace := ctx.AppNamespace()

	log.Infof("Undeploying discovered app \"%s/%s\" in clusters %q and %q",
		appNamespace, ctx.Workload().GetAppName(), ctx.Env().C1.Name, ctx.Env().C2.Name)

	// delete app on both clusters

	if err := DeleteDiscoveredApps(ctx, ctx.Env().C1, appNamespace); err != nil {
		return err
	}

	if err := DeleteDiscoveredApps(ctx, ctx.Env().C2, appNamespace); err != nil {
		return err
	}

	// delete namespace on both clusters

	if err := util.DeleteNamespace(ctx, ctx.Env().C1, appNamespace); err != nil {
		return err
	}

	if err := util.DeleteNamespace(ctx, ctx.Env().C2, appNamespace); err != nil {
		return err
	}

	// wait for namespace to be deleted on both clusters

	if err := util.WaitForNamespaceDelete(ctx, ctx.Env().C1, appNamespace); err != nil {
		return err
	}

	if err := util.WaitForNamespaceDelete(ctx, ctx.Env().C2, appNamespace); err != nil {
		return err
	}

	log.Info("Workload undeployed")

	return nil
}

func (d DiscoveredApp) IsDiscovered() bool {
	return true
}

func DeleteDiscoveredApps(ctx types.TestContext, cluster types.Cluster, namespace string) error {
	log := ctx.Logger()

	tempDir, err := os.MkdirTemp("", "ramen-")
	if err != nil {
		return err
	}

	// Clean up by removing the temporary directory when done
	defer os.RemoveAll(tempDir)

	if err = CreateKustomizationFile(ctx, tempDir); err != nil {
		return err
	}

	if err := runCommand(
		ctx.Context(),
		"kubectl",
		"delete",
		"--kustomize", tempDir,
		"--namespace", namespace,
		"--kubeconfig", cluster.Kubeconfig,
		"--wait=false",
		"--ignore-not-found",
	); err != nil {
		return err
	}

	log.Debugf("Deleted discovered app \"%s/%s\" in cluster %q",
		namespace, ctx.Workload().GetAppName(), cluster.Name)

	return nil
}

// runCommand runs a command and return the error. The command will be killed when the context is canceled or the
// deadline is exceeded.
func runCommand(ctx context.Context, command string, args ...string) error {
	cmd := exec.CommandContext(ctx, command, args...)

	// Run the command in a new process group so it is not terminated by the shell when the user interrupt go test.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if out, err := cmd.Output(); err != nil {
		// If the context was canceled or the deadline exceeded, ignore the unhelpful "killed" error from the command.
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if ee, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("%w: stdout=%q stderr=%q", err, out, ee.Stderr)
		}

		return err
	}

	return nil
}
