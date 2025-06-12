// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

//nolint:thelper // for using dt *testing.T and keeping test code idiomatic.
package test

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/dractions"
	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
)

// Make it easier to manage namespaces created by the tests.
const appNamespacePrefix = "e2e-"

type Context struct {
	parent   types.Context
	context  context.Context
	workload types.Workload
	deployer types.Deployer
	name     string
	logger   *zap.SugaredLogger
}

func NewContext(
	parent types.Context,
	w types.Workload,
	d types.Deployer,
) Context {
	name := strings.ToLower(d.GetName() + "-" + w.GetName() + "-" + w.GetAppName())

	return Context{
		parent:   parent,
		context:  parent.Context(),
		workload: w,
		deployer: d,
		name:     name,
		logger:   parent.Logger().Named(name),
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

func (c *Context) ManagementNamespace() string {
	if ns := c.deployer.GetNamespace(c); ns != "" {
		return ns
	}

	return c.AppNamespace()
}

func (c *Context) AppNamespace() string {
	return appNamespacePrefix + c.name
}

func (c *Context) Logger() *zap.SugaredLogger {
	return c.logger
}

func (c *Context) Env() *types.Env {
	return c.parent.Env()
}

func (c *Context) Config() *config.Config {
	return c.parent.Config()
}

func (c *Context) Context() context.Context {
	return c.context
}

// WithTimeout returns a derived context with a deadline. Call cancel to release resources associated with the context
// as soon as the operation running in the context complete.
func (c Context) WithTimeout(d time.Duration) (*Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(c.context, d)
	c.context = ctx //nolint:revive

	return &c, cancel
}

// Validated return an error if the combination of deployer and workload is not supported.
// TODO: validate that the deployer/workload is compatible with the clusters.
func (c *Context) Validate() error {
	return nil
}

func (c *Context) Deploy(dt *testing.T) {
	t := WithLog(dt, c.logger)
	t.Helper()

	timedCtx, cancel := c.WithTimeout(util.DeployTimeout)
	defer cancel()

	if err := timedCtx.deployer.Deploy(timedCtx); err != nil {
		t.Fatalf("Failed to deploy workload: %s", err)
	}
}

func (c *Context) Undeploy(dt *testing.T) {
	t := WithLog(dt, c.logger)
	t.Helper()

	timedCtx, cancel := c.WithTimeout(util.UndeployTimeout)
	defer cancel()

	if err := timedCtx.deployer.Undeploy(timedCtx); err != nil {
		t.Fatalf("Failed to undeploy workload: %s", err)
	}
}

func (c *Context) Enable(dt *testing.T) {
	t := WithLog(dt, c.logger)
	t.Helper()

	timedCtx, cancel := c.WithTimeout(util.EnableTimeout)
	defer cancel()

	if err := dractions.EnableProtection(timedCtx); err != nil {
		t.Fatalf("Failed to enable protection for workload: %s", err)
	}
}

func (c *Context) Disable(dt *testing.T) {
	t := WithLog(dt, c.logger)
	t.Helper()

	timedCtx, cancel := c.WithTimeout(util.DisableTimeout)
	defer cancel()

	if err := dractions.DisableProtection(timedCtx); err != nil {
		t.Fatalf("Failed to disable protection for workload: %s", err)
	}
}

func (c *Context) Failover(dt *testing.T) {
	t := WithLog(dt, c.logger)
	t.Helper()

	timedCtx, cancel := c.WithTimeout(util.FailoverTimeout)
	defer cancel()

	if err := dractions.Failover(timedCtx); err != nil {
		t.Fatalf("Failed to failover workload: %s", err)
	}
}

func (c *Context) Relocate(dt *testing.T) {
	t := WithLog(dt, c.logger)
	t.Helper()

	timedCtx, cancel := c.WithTimeout(util.RelocateTimeout)
	defer cancel()

	if err := dractions.Relocate(timedCtx); err != nil {
		t.Fatalf("Failed to relocate workload: %s", err)
	}
}
