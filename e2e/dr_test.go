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
		pvcSpec, ok := pvcSpecs[tc.PVCSpec]
		if !ok {
			panic("unknown pvcSpec")
		}

		workload, err := workloads.New(tc.Workload, Ctx.Config().Repo.Branch, pvcSpec)
		if err != nil {
			panic(err)
		}

		deployerSpec, ok := deploySpecs[tc.Deployer]
		if !ok {
			panic("unknown deployer")
		}

		deployer, err := deployers.New(deployerSpec)
		if err != nil {
			panic(err)
		}

		ctx := test.NewContext(Ctx, workload, deployer)
		t.Run(ctx.Name(), func(dt *testing.T) {
			t := test.WithLog(dt, ctx.Logger())
			t.Parallel()
			runTestFlow(t, ctx)
		})
	}
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
