// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"fmt"
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

var Workloads = []workloads.Workload{}

var subscription = &deployers.Subscription{}

// appset := &deployers.ApplicationSet{}
// Deployers := []deployers.Deployer{subscription, appset}

var Deployers = []deployers.Deployer{subscription}

func generateWorkloads([]workloads.Workload) {
	pvcSpecs := util.GetPVCSpecs()
	for i, pvcSpec := range pvcSpecs {
		deployment := &workloads.Deployment{
			Path:     GITPATH,
			Revision: GITREVISION,
			AppName:  APPNAME,
			Name:     fmt.Sprintf("Deployment-%d", i),
			PVCSpec:  pvcSpec,
		}
		Workloads = append(Workloads, deployment)
	}
}

func Exhaustive(t *testing.T) {
	t.Helper()
	t.Parallel()

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
