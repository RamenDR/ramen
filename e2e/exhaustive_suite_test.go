// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"testing"

	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/testcontext"
	"github.com/ramendr/ramen/e2e/workloads"
)

// Deployers = {"Subscription", "AppSet", "Imperative"}
// Workloads = {"Deployment", "STS", "DaemonSet"}
// Classes   = {"rbd", "cephfs"}

var deployment = &workloads.Deployment{
	Path:     "workloads/deployment/k8s-regional-rbd",
	Revision: "main",
	AppName:  "busybox",
	Name:     "Deployment",
}

var Workloads = []workloads.Workload{deployment}

var subscription = &deployers.Subscription{}

// appset := &deployers.ApplicationSet{}
// Deployers := []deployers.Deployer{subscription, appset}

var Deployers = []deployers.Deployer{subscription}

func Exhaustive(t *testing.T) {
	t.Helper()
	t.Parallel()

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
