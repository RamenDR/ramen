// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"testing"

	"github.com/ramendr/ramen/e2e/app"
	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/env"
	"github.com/ramendr/ramen/e2e/util"
	"github.com/ramendr/ramen/e2e/workloads"
)

// The global test context.
var Ctx *app.Context

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

	log, err := app.CreateLogger(logFile)
	if err != nil {
		panic(err)
	}

	defer func() {
		_ = log.Sync() //nolint:errcheck
	}()

	log.Infof("Using config file %q", configFile)
	log.Infof("Using log file %q", logFile)

	options := config.Options{
		Deployers: deployers.AvailableTypes(),
		Workloads: workloads.AvailableNames(),
	}

	config, err := config.ReadConfig(configFile, options)
	if err != nil {
		log.Errorf("Failed to read config: %s", err)

		return 1
	}

	// The context will be canceled when receiving a signal.
	var stop context.CancelFunc

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	env, err := env.New(ctx, config.Clusters, log)
	if err != nil {
		log.Errorf("Failed to create env: %s", err)

		return 1
	}

	Ctx = app.NewContext(ctx, config, env, log)

	log.Infof("Using DeployTimeout: %v", util.DeployTimeout)
	log.Infof("Using UneployTimeout: %v", util.UndeployTimeout)
	log.Infof("Using EnableTimeout: %v", util.EnableTimeout)
	log.Infof("Using DisableTimeout: %v", util.DisableTimeout)
	log.Infof("Using FailoverTimeout: %v", util.FailoverTimeout)
	log.Infof("Using RelocateTimeout: %v", util.RelocateTimeout)
	log.Infof("Using RetryInterval: %v", util.RetryInterval)

	return m.Run()
}
