// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"testing"

	"github.com/ramendr/ramen/e2e/dractions"
	"github.com/ramendr/ramen/e2e/testcontext"
	e2etesting "github.com/ramendr/ramen/e2e/testing"
)

func DeployAction(t *testing.T) {
	testCtx, err := testcontext.GetTestContext(t.Name())
	if err != nil {
		e2etesting.Fatal(t, err, "Failed to get test context")
	}

	if err := testCtx.Deployer.Deploy(testCtx.Workload); err != nil {
		e2etesting.Fatal(t, err, "Failed to deploy workload")
	}
}

func EnableAction(t *testing.T) {
	testCtx, err := testcontext.GetTestContext(t.Name())
	if err != nil {
		e2etesting.Fatal(t, err, "Failed to get test context")
	}

	if err := dractions.EnableProtection(testCtx.Workload, testCtx.Deployer); err != nil {
		e2etesting.Fatal(t, err, "Failed to protect workload")
	}
}

func FailoverAction(t *testing.T) {
	testCtx, err := testcontext.GetTestContext(t.Name())
	if err != nil {
		e2etesting.Fatal(t, err, "Failed to get test context")
	}

	if err := dractions.Failover(testCtx.Workload, testCtx.Deployer); err != nil {
		e2etesting.Fatal(t, err, "Failed to fail over workload")
	}
}

func RelocateAction(t *testing.T) {
	testCtx, err := testcontext.GetTestContext(t.Name())
	if err != nil {
		e2etesting.Fatal(t, err, "Failed to get test context")
	}

	if err := dractions.Relocate(testCtx.Workload, testCtx.Deployer); err != nil {
		e2etesting.Fatal(t, err, "Failed to relocate workload")
	}
}

func DisableAction(t *testing.T) {
	testCtx, err := testcontext.GetTestContext(t.Name())
	if err != nil {
		e2etesting.Fatal(t, err, "Failed to get test context")
	}

	if err := dractions.DisableProtection(testCtx.Workload, testCtx.Deployer); err != nil {
		e2etesting.Fatal(t, err, "Failed to unprotect workload")
	}
}

func UndeployAction(t *testing.T) {
	testCtx, err := testcontext.GetTestContext(t.Name())
	if err != nil {
		e2etesting.Fatal(t, err, "Failed to get test context")
	}

	if err := testCtx.Deployer.Undeploy(testCtx.Workload); err != nil {
		e2etesting.Fatal(t, err, "Failed to undeploy workload")
	}
}
