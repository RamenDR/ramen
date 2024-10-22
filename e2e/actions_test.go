// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"testing"

	"github.com/ramendr/ramen/e2e/dractions"
	"github.com/ramendr/ramen/e2e/testcontext"
	"github.com/ramendr/ramen/e2e/util"
)

func DeployAction(t *testing.T) {
	testCtx, err := testcontext.GetTestContext(t.Name())
	if err != nil {
		util.Fatal(t, "Deploy failed", err)
	}

	if err := testCtx.Deployer.Deploy(testCtx.Workload); err != nil {
		util.Fatal(t, "Deploy failed", err)
	}
}

func EnableAction(t *testing.T) {
	testCtx, err := testcontext.GetTestContext(t.Name())
	if err != nil {
		util.Fatal(t, "Enable failed", err)
	}

	if err := dractions.EnableProtection(testCtx.Workload, testCtx.Deployer); err != nil {
		util.Fatal(t, "Enable failed", err)
	}
}

func FailoverAction(t *testing.T) {
	testCtx, err := testcontext.GetTestContext(t.Name())
	if err != nil {
		util.Fatal(t, "Failover failed", err)
	}

	if err := dractions.Failover(testCtx.Workload, testCtx.Deployer); err != nil {
		util.Fatal(t, "Failover failed", err)
	}
}

func RelocateAction(t *testing.T) {
	testCtx, err := testcontext.GetTestContext(t.Name())
	if err != nil {
		util.Fatal(t, "Relocate failed", err)
	}

	if err := dractions.Relocate(testCtx.Workload, testCtx.Deployer); err != nil {
		util.Fatal(t, "Relocate failed", err)
	}
}

func DisableAction(t *testing.T) {
	testCtx, err := testcontext.GetTestContext(t.Name())
	if err != nil {
		util.Fatal(t, "Disable failed", err)
	}

	if err := dractions.DisableProtection(testCtx.Workload, testCtx.Deployer); err != nil {
		util.Fatal(t, "Disable failed", err)
	}
}

func UndeployAction(t *testing.T) {
	testCtx, err := testcontext.GetTestContext(t.Name())
	if err != nil {
		util.Fatal(t, "Undeploy failed", err)
	}

	if err := testCtx.Deployer.Undeploy(testCtx.Workload); err != nil {
		util.Fatal(t, "Undeploy failed", err)
	}
}
