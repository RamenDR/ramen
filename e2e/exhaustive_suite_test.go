// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/testcontext"
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
			// assign workload and deployer to a local variable to avoid parallel test issue
			// see https://go.dev/wiki/CommonMistakes
			d := deployer
			w := workload

			t.Run(deployers.GetCombinedName(d, w), func(t *testing.T) {
				t.Parallel()
				testcontext.AddTestContext(t.Name(), w, d)
				runTestFlow(t)
				testcontext.DeleteTestContext(t.Name())
			})
		}
	}
}

func runTestFlow(t *testing.T) {
	t.Helper()

	testCtx, err := testcontext.GetTestContext(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	if !testCtx.Deployer.IsWorkloadSupported(testCtx.Workload) {
		t.Skipf("Workload %s not supported by deployer %s, skip test", testCtx.Workload.GetName(), testCtx.Deployer.GetName())
	}

	if !t.Run("Deploy", DeployAction) {
		t.Fatal("Deploy failed")
	}

	if !t.Run("Enable", EnableAction) {
		t.Fatal("Enable failed")
	}

	if !t.Run("Failover", FailoverAction) {
		t.Fatal("Failover failed")
	}

	if !t.Run("Relocate", RelocateAction) {
		t.Fatal("Relocate failed")
	}

	if !t.Run("Disable", DisableAction) {
		t.Fatal("Disable failed")
	}

	if !t.Run("Undeploy", UndeployAction) {
		t.Fatal("Undeploy failed")
	}
}
