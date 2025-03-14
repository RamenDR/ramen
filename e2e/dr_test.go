// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"testing"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/test"
	"github.com/ramendr/ramen/e2e/util"
	"github.com/ramendr/ramen/e2e/workloads"
)

func TestDR(dt *testing.T) {
	t := test.WithLog(dt, Ctx.log)
	t.Parallel()

	if len(Ctx.config.Tests) == 0 {
		t.Fatal("No tests found in the configuration file")
	}

	if err := util.ValidateClustersInDRPolicy(Ctx.env, Ctx.config, Ctx.log); err != nil {
		t.Fatalf("Failed to validate clusters with DRPolicy: %s", err)
	}

	if err := util.EnsureChannel(Ctx.env.Hub, Ctx.config, Ctx.log); err != nil {
		t.Fatalf("Failed to ensure channel: %s", err)
	}

	t.Cleanup(func() {
		if err := util.EnsureChannelDeleted(Ctx.env.Hub, Ctx.config, Ctx.log); err != nil {
			t.Fatalf("Failed to ensure channel deleted: %s", err)
		}
	})

	pvcSpecs := config.PVCSpecsMap(Ctx.config)

	for _, tc := range Ctx.config.Tests {
		pvcSpec, ok := pvcSpecs[tc.PVCSpec]
		if !ok {
			panic("unknown pvcSpec")
		}

		workload, err := workloads.New(tc.Workload, Ctx.config.Repo.Branch, pvcSpec)
		if err != nil {
			panic(err)
		}

		deployer, err := deployers.New(tc.Deployer)
		if err != nil {
			panic(err)
		}

		ctx := test.NewContext(workload, deployer, Ctx.env, Ctx.config, Ctx.log)
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
