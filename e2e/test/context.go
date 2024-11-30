// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package test

import (
	"fmt"
	"testing"

	"github.com/go-logr/logr"
	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/dractions"
	"github.com/ramendr/ramen/e2e/types"
)

type Context struct {
	workload types.Workload
	deployer types.Deployer
	name     string
	logger   logr.Logger
}

func NewContext(w types.Workload, d types.Deployer, log logr.Logger) Context {
	name := deployers.GetCombinedName(d, w)

	return Context{
		workload: w,
		deployer: d,
		name:     name,
		logger:   log.WithName(name),
	}
}

func (c *Context) Deployer() types.Deployer {
	return c.deployer
}

func (c *Context) Workload() types.Workload {
	return c.workload
}

func (c *Context) Name() string {
	return c.name
}

func (c *Context) Logger() logr.Logger {
	return c.logger
}

// Validated return an error if the combination of deployer and workload is not supported.
// TODO: validate that the workload is compatible with the clusters.
func (c *Context) Validate() error {
	if !c.deployer.IsWorkloadSupported(c.workload) {
		return fmt.Errorf("workload %q not supported by deployer %q", c.workload.GetName(), c.deployer.GetName())
	}

	return nil
}

func (c *Context) Deploy(t *testing.T) {
	t.Helper()

	if err := c.deployer.Deploy(c); err != nil {
		t.Fatal(err)
	}
}

func (c *Context) Undeploy(t *testing.T) {
	t.Helper()

	if err := c.deployer.Undeploy(c); err != nil {
		t.Fatal(err)
	}
}

func (c *Context) Enable(t *testing.T) {
	t.Helper()

	if err := dractions.EnableProtection(c); err != nil {
		t.Fatal(err)
	}
}

func (c *Context) Disable(t *testing.T) {
	t.Helper()

	if err := dractions.DisableProtection(c); err != nil {
		t.Fatal(err)
	}
}

func (c *Context) Failover(t *testing.T) {
	t.Helper()

	if err := dractions.Failover(c); err != nil {
		t.Fatal(err)
	}
}

func (c *Context) Relocate(t *testing.T) {
	t.Helper()

	if err := dractions.Relocate(c); err != nil {
		t.Fatal(err)
	}
}
