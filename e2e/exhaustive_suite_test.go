// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/test"
	"github.com/ramendr/ramen/e2e/util"
	"github.com/ramendr/ramen/e2e/workloads"
)

// Deployers = {"Subscription", "AppSet", "Imperative"}
// Workloads = {"Deployment", "STS", "DaemonSet"}
// Classes   = {"rbd", "cephfs"}

const (
	GITPATH     = "workloads/deployment/base"
	GITREVISION = "main"
	APPNAME     = "busybox"
)

var (
	Workloads      = []workloads.Workload{}
	subscription   = &deployers.Subscription{}
	appset         = &deployers.ApplicationSet{}
	discoveredApps = &deployers.DiscoveredApps{}
	Deployers      = []deployers.Deployer{subscription, appset, discoveredApps}
)

func generateSuffix(storageClassName string) string {
	suffix := storageClassName

	if strings.ToLower(storageClassName) == "rook-ceph-block" {
		suffix = "rbd"
	}

	if strings.ToLower(storageClassName) == "rook-cephfs" {
		suffix = "cephfs"
	}

	return suffix
}

func generateWorkloads([]workloads.Workload) {
	pvcSpecs := util.GetPVCSpecs()
	for _, pvcSpec := range pvcSpecs {
		// add storageclass name to deployment name
		suffix := generateSuffix(pvcSpec.StorageClassName)
		deployment := &workloads.Deployment{
			Path:     GITPATH,
			Revision: GITREVISION,
			AppName:  APPNAME,
			Name:     fmt.Sprintf("Deploy-%s", suffix),
			PVCSpec:  pvcSpec,
		}
		Workloads = append(Workloads, deployment)
	}
}

func Exhaustive(t *testing.T) {
	t.Helper()
	t.Parallel()

	if err := util.EnsureChannel(); err != nil {
		t.Fatalf("failed to ensure channel: %v", err)
	}

	t.Cleanup(func() {
		if err := util.EnsureChannelDeleted(); err != nil {
			t.Fatalf("failed to ensure channel deleted: %v", err)
		}
	})

	generateWorkloads(Workloads)

	for _, deployer := range Deployers {
		for _, workload := range Workloads {
			ctx := test.Context{Workload: workload, Deployer: deployer}
			t.Run(deployers.GetCombinedName(deployer, workload), func(t *testing.T) {
				t.Parallel()
				runTestFlow(t, ctx)
			})
		}
	}
}

func runTestFlow(t *testing.T, ctx test.Context) {
	t.Helper()

	if !ctx.Deployer.IsWorkloadSupported(ctx.Workload) {
		t.Skipf("Workload %s not supported by deployer %s, skip test", ctx.Workload.GetName(), ctx.Deployer.GetName())
	}

	if !t.Run("Deploy", ctx.Deploy) {
		t.Fatal("Deploy failed")
	}

	if !t.Run("Enable", ctx.Enable) {
		t.Fatal("Enable failed")
	}

	if !t.Run("Failover", ctx.Failover) {
		t.Fatal("Failover failed")
	}

	if !t.Run("Relocate", ctx.Relocate) {
		t.Fatal("Relocate failed")
	}

	if !t.Run("Disable", ctx.Disable) {
		t.Fatal("Disable failed")
	}

	if !t.Run("Undeploy", ctx.Undeploy) {
		t.Fatal("Undeploy failed")
	}
}
