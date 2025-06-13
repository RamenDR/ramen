// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"fmt"
	"os"

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
// nolint:funlen
func (d DiscoveredApp) Deploy(ctx types.TestContext) error {
	log := ctx.Logger()
	appNamespace := ctx.AppNamespace()

	var cluster types.Cluster

	workloadStatus, err := ctx.Workload().Status(ctx)
	if err != nil {
		return err
	}

	switch len(workloadStatus) {
	case 0:
		cluster = ctx.Env().C1
		log.Debugf("Application \"%s/%s\" not found on any dr clusters, defaulting to deploy on %q",
			appNamespace, ctx.Workload().GetAppName(), cluster.Name)

	case 1:
		cluster = workloadStatus[0].Cluster
		log.Debugf("Application \"%s/%s\" found in dr cluster %q with status %q",
			appNamespace, ctx.Workload().GetAppName(), workloadStatus[0].Cluster.Name, workloadStatus[0].Status)

	default:
		return fmt.Errorf("application \"%s/%s\" found on multiple dr clusters [%q, %q] with status [%q, %q], "+
			"aborting deploy", appNamespace, ctx.Workload().GetAppName(), workloadStatus[0].Cluster.Name,
			workloadStatus[1].Cluster.Name, workloadStatus[0].Status, workloadStatus[1].Status)
	}

	if err := util.CreateNamespaceOnMangedClusters(ctx, appNamespace); err != nil {
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

	if err := util.RunCommand(
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

	if err = WaitWorkloadHealth(ctx, cluster, appNamespace); err != nil {
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
	if err := util.DeleteNamespaceOnManagedClusters(ctx, appNamespace); err != nil {
		return err
	}

	// wait for namespace to be deleted on both clusters
	if err := util.WaitForNamespaceDeleteOnManagedClusters(ctx, appNamespace); err != nil {
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

	if err := util.RunCommand(
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
