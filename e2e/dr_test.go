// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"testing"
	"time"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/test"
	"github.com/ramendr/ramen/e2e/util"
	"github.com/ramendr/ramen/e2e/validate"
	"github.com/ramendr/ramen/e2e/workloads"
)

func TestDR(dt *testing.T) {
	t := test.WithLog(dt, Ctx.Logger())
	t.Parallel()

	if len(Ctx.Config().Tests) == 0 {
		t.Fatal("No tests found in the configuration file")
	}

	if err := validate.TestConfig(Ctx); err != nil {
		t.Fatal(err.Error())
	}

	if err := util.EnsureChannel(Ctx); err != nil {
		t.Fatalf("Failed to ensure channel: %s", err)
	}

	t.Cleanup(func() {
		timedCtx, cancel := Ctx.WithTimeout(1 * time.Minute)
		defer cancel()

		if err := util.EnsureChannelDeleted(timedCtx); err != nil {
			t.Fatalf("Failed to ensure channel deleted: %s", err)
		}
	})

	pvcSpecs := config.PVCSpecsMap(Ctx.Config())
	deploySpecs := config.DeployersMap(Ctx.Config())

	for _, tc := range Ctx.Config().Tests {
		name := tc.ContextName()
		t.Run(name, func(dt *testing.T) {
			t := test.WithLog(dt, Ctx.Logger().Named(name))
			t.Parallel()

			ctx := setupTest(t, tc, pvcSpecs, deploySpecs)
			runTestFlow(t, ctx)
		})
	}
}

func setupTest(
	t *test.T,
	tc config.Test,
	pvcSpecs map[string]config.PVCSpec,
	deploySpecs map[string]config.Deployer,
) test.Context {
	t.Helper()

	pvcSpec, ok := pvcSpecs[tc.PVCSpec]
	if !ok {
		t.Fatalf("Unknown PVC spec %q", tc.PVCSpec)
	}

	workload, err := workloads.New(Ctx.Context(), tc.Workload, Ctx.Config().Repo, pvcSpec)
	if err != nil {
		t.Fatalf("Failed to create workload %q: %v", tc.Workload, err)
	}

	deployerSpec, ok := deploySpecs[tc.Deployer]
	if !ok {
		t.Fatalf("Unknown deployer %q", tc.Deployer)
	}

	deployer, err := deployers.New(deployerSpec)
	if err != nil {
		t.Fatalf("Failed to create deployer %q: %v", tc.Deployer, err)
	}

	return test.NewContext(Ctx, tc.ContextName(), workload, deployer)
}

func runTestFlow(t *test.T, ctx test.Context) {
	t.Helper()

	if err := ctx.Validate(); err != nil {
		t.Skipf("Skip test: %s", err)
	}

	if !t.Run("Deploy", ctx.Deploy) {
		t.FailNow()
	}

	if !t.Run("Enable", ctx.Enable) {
		t.FailNow()
	}

	if !t.Run("Failover", ctx.Failover) {
		t.FailNow()
	}

	if !t.Run("Relocate", ctx.Relocate) {
		t.FailNow()
	}

	if !t.Run("Disable", ctx.Disable) {
		t.FailNow()
	}

	if !t.Run("Undeploy", ctx.Undeploy) {
		t.FailNow()
	}
}
