// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package testcontext

import (
	"testing"

	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/dractions"
	"github.com/ramendr/ramen/e2e/workloads"
)

type TestContext struct {
	Workload workloads.Workload
	Deployer deployers.Deployer
}

func (c *TestContext) Deploy(t *testing.T) {
	t.Helper()

	if err := c.Deployer.Deploy(c.Workload); err != nil {
		t.Fatal(err)
	}
}

func (c *TestContext) Undeploy(t *testing.T) {
	t.Helper()

	if err := c.Deployer.Undeploy(c.Workload); err != nil {
		t.Fatal(err)
	}
}

func (c *TestContext) Enable(t *testing.T) {
	t.Helper()

	if err := dractions.EnableProtection(c.Workload, c.Deployer); err != nil {
		t.Fatal(err)
	}
}

func (c *TestContext) Disable(t *testing.T) {
	t.Helper()

	if err := dractions.DisableProtection(c.Workload, c.Deployer); err != nil {
		t.Fatal(err)
	}
}

func (c *TestContext) Failover(t *testing.T) {
	t.Helper()

	if err := dractions.Failover(c.Workload, c.Deployer); err != nil {
		t.Fatal(err)
	}
}

func (c *TestContext) Relocate(t *testing.T) {
	t.Helper()

	if err := dractions.Relocate(c.Workload, c.Deployer); err != nil {
		t.Fatal(err)
	}
}
