// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/types"
)

// Context implements types.Context for sharing the log, env, and config with all code.
type Context struct {
	log    *zap.SugaredLogger
	env    *types.Env
	config *config.Config
	ctx    context.Context
}

func NewContext(ctx context.Context, config *config.Config, env *types.Env, log *zap.SugaredLogger) *Context {
	return &Context{
		log:    log,
		env:    env,
		config: config,
		ctx:    ctx,
	}
}

func (c *Context) Logger() *zap.SugaredLogger {
	return c.log
}

func (c *Context) Config() *config.Config {
	return c.config
}

func (c *Context) Env() *types.Env {
	return c.env
}

func (c *Context) Context() context.Context {
	return c.ctx
}

// WithTimeout returns a derived context with a deadline. Call cancel to release resources associated with the context
// as soon as the operation running in the context complete.
func (c Context) WithTimeout(d time.Duration) (*Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(c.ctx, d)
	c.ctx = ctx //nolint:revive

	return &c, cancel
}
