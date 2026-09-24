// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/test"
	"github.com/ramendr/ramen/e2e/util"
	"github.com/ramendr/ramen/e2e/validate"
	"github.com/ramendr/ramen/e2e/workloads"
)

// TestSingleS3Store tests disaster recovery scenarios with a single S3 store
// located on the hub cluster. This validates that RamenDR correctly handles
// centralized S3 storage for both discovered apps and RBD workloads.
//
// This test runs tests marked with group "single-s3-store" from the configuration.
// It can be used with any configuration file that includes tests with the
// "single-s3-store" group (e.g., test.yaml, test-single-s3-store.yaml).
func TestSingleS3Store(dt *testing.T) {
	t := test.WithLog(dt, Ctx.Logger())
	t.Parallel()

	if err := setupTestEnvironment(); err != nil {
		t.Fatalf("Failed to setup test environment: %s", err)
	}

	t.Cleanup(func() {
		timedCtx, cancel := Ctx.WithTimeout(1 * time.Minute)
		defer cancel()

		if err := util.EnsureChannelDeleted(timedCtx); err != nil {
			t.Fatalf("Failed to ensure channel deleted: %s", err)
		}
	})

	singleS3Tests := filterSingleS3StoreTests(Ctx.Config().Tests)
	if len(singleS3Tests) == 0 {
		t.Skip("No single S3 store tests found in configuration")
	}

	runSingleS3StoreTests(t, singleS3Tests)
}

// setupTestEnvironment initializes the test environment with configuration validation
// and channel setup.
func setupTestEnvironment() error {
	if len(Ctx.Config().Tests) == 0 {
		return fmt.Errorf("no tests found in the configuration file")
	}

	if err := validate.TestConfig(Ctx); err != nil {
		return err
	}

	return util.EnsureChannel(Ctx)
}

// runSingleS3StoreTests executes all single S3 store test cases.
func runSingleS3StoreTests(t *test.T, singleS3Tests []config.Test) {
	pvcSpecs := config.PVCSpecsMap(Ctx.Config())
	deploySpecs := config.DeployersMap(Ctx.Config())

	for _, tc := range singleS3Tests {
		executeTestCase(t, tc, pvcSpecs, deploySpecs)
	}
}

// executeTestCase runs a single test case with the given configuration.
func executeTestCase(
	t *test.T,
	tc config.Test,
	pvcSpecs map[string]config.PVCSpec,
	deploySpecs map[string]config.Deployer,
) {
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
		runSingleS3StoreTestFlow(t, ctx)
	})
}

// runSingleS3StoreTestFlow executes the standard DR workflow for single S3 store tests.
// This validates:
// - Deploy: Application deployment to primary cluster
// - Enable: DR protection enablement using centralized S3 store
// - Failover: Disaster failover with S3 backup restoration
// - Relocate: Relocation back to primary cluster
// - Disable: DR protection disablement
// - Undeploy: Application cleanup
func runSingleS3StoreTestFlow(t *test.T, ctx test.Context) {
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

// filterSingleS3StoreTests filters test cases to include only those with
// the "single-s3-store" group designation. This allows selective execution
// of single S3 store tests separately from the main test suite to avoid
// resource contention.
func filterSingleS3StoreTests(tests []config.Test) []config.Test {
	var filtered []config.Test

	for _, test := range tests {
		// Include tests marked with single-s3-store group
		if test.Group == "single-s3-store" {
			filtered = append(filtered, test)
		}
	}

	return filtered
}
