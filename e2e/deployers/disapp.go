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
func (d DiscoveredApp) Deploy(ctx types.TestContext) error {
	log := ctx.Logger()
	appNamespace := ctx.AppNamespace()

	cluster, err := chooseDeployCluster(ctx)
	if err != nil {
		return err
	}

	log.Infof("Deploying discovered app \"%s/%s\" in cluster %q", appNamespace, ctx.Workload().GetAppName(), cluster.Name)

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

// chooseDeployCluster determines which cluster to deploy the discovered
// application on based on the status of the workload across clusters.
func chooseDeployCluster(ctx types.TestContext) (types.Cluster, error) {
	log := ctx.Logger()

	statuses, err := ctx.Workload().Status(ctx)
	if err != nil {
		return types.Cluster{}, err
	}

	switch len(statuses) {
	case 0:
		cluster := ctx.Env().C1
		log.Debugf("Application \"%s/%s\" not found, deploying on cluster %q",
			ctx.AppNamespace(), ctx.Workload().GetAppName(), cluster.Name)

		return cluster, nil
	case 1:
		log.Debugf("Application \"%s/%s\" found in cluster %q with status %q",
			ctx.AppNamespace(), ctx.Workload().GetAppName(), statuses[0].ClusterName, statuses[0].Status)

		return ctx.Env().GetCluster(statuses[0].ClusterName)
	default:
		return types.Cluster{}, fmt.Errorf("application \"%s/%s\" found on multiple clusters: %+v",
			ctx.AppNamespace(), ctx.Workload().GetAppName(), statuses)
	}
}
