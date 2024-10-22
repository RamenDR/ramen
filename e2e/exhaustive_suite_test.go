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
		util.Fatal(t, "Failed to ensure channel", err)
	}

	t.Cleanup(func() {
		if err := util.EnsureChannelDeleted(); err != nil {
			util.Fatal(t, "Failed to ensure channel deleted", err)
		}
	})

	generateWorkloads(Workloads)

	for _, workload := range Workloads {
		for _, deployer := range Deployers {
			// assign workload and deployer to a local variable to avoid parallel test issue
			// see https://go.dev/wiki/CommonMistakes
			w := workload
			d := deployer

			t.Run(w.GetName(), func(t *testing.T) {
				t.Parallel()
				t.Run(d.GetName(), func(t *testing.T) {
					t.Parallel()
					testcontext.AddTestContext(t.Name(), w, d)
					runTestFlow(t)
					testcontext.DeleteTestContext(t.Name(), w, d)
				})
			})
		}
	}
}

func runTestFlow(t *testing.T) {
	t.Helper()

	testCtx, err := testcontext.GetTestContext(t.Name())
	if err != nil {
		util.Fatal(t, "Failed to get test context", err)
	}

	if !testCtx.Deployer.IsWorkloadSupported(testCtx.Workload) {
		util.Skipf(t, "Workload %s not supported by deployer %s", testCtx.Workload.GetName(), testCtx.Deployer.GetName())
	}

	if !t.Run("Deploy", DeployAction) {
		util.FailNow(t)
	}

	if !t.Run("Enable", EnableAction) {
		util.FailNow(t)
	}

	if !t.Run("Failover", FailoverAction) {
		util.FailNow(t)
	}

	if !t.Run("Relocate", RelocateAction) {
		util.FailNow(t)
	}

	if !t.Run("Disable", DisableAction) {
		util.FailNow(t)
	}

	if !t.Run("Undeploy", UndeployAction) {
		util.FailNow(t)
	}
}
