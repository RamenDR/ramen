// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/env"
	"github.com/ramendr/ramen/e2e/test"
	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
	"github.com/ramendr/ramen/e2e/workloads"
)

// Context implements types.Context for sharing the log, env, and config with all code.
type Context struct {
	log     *zap.SugaredLogger
	env     *types.Env
	config  *types.Config
	context context.Context
}

func (c *Context) Logger() *zap.SugaredLogger {
	return c.log
}

func (c *Context) Config() *types.Config {
	return c.config
}

func (c *Context) Env() *types.Env {
	return c.env
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

// The global test context.
var Ctx Context

func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

func testMain(m *testing.M) int {
	var (
		err        error
		configFile string
		logFile    string
	)

	flag.StringVar(&configFile, "config", "config.yaml", "e2e configuration file")
	flag.StringVar(&logFile, "logfile", "ramen-e2e.log", "e2e log file")
	flag.Parse()

	Ctx.log, err = test.CreateLogger(logFile)
	if err != nil {
		panic(err)
	}

	defer func() {
		_ = Ctx.log.Sync() //nolint:errcheck
	}()

	log := Ctx.log

	log.Infof("Using config file %q", configFile)
	log.Infof("Using log file %q", logFile)

	options := config.Options{
		Deployers: deployers.AvailableNames(),
		Workloads: workloads.AvailableNames(),
	}

	Ctx.config, err = config.ReadConfig(configFile, options)
	if err != nil {
		log.Errorf("Failed to read config: %s", err)

		return 1
	}

	// The context will be canceled when receiving a signal.
	var stop context.CancelFunc
	Ctx.context, stop = signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	Ctx.env, err = env.New(Ctx.Context(), Ctx.config, Ctx.log)
	if err != nil {
		log.Errorf("Failed to create testing context: %s", err)

		return 1
	}

	log.Infof("Using Timeout: %v", util.Timeout)
	log.Infof("Using RetryInterval: %v", util.RetryInterval)

	return m.Run()
}
