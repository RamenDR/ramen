// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"flag"
	"os"
	"testing"

	"go.uber.org/zap"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/env"
	"github.com/ramendr/ramen/e2e/test"
	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
	"github.com/ramendr/ramen/e2e/workloads"
)

type Context struct {
	log    *zap.SugaredLogger
	env    *types.Env
	config *types.Config
}

// The global test context
var Ctx Context

func TestMain(m *testing.M) {
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
	// TODO: Sync the log on exit

	log := Ctx.log

	log.Infof("Using config file %q", configFile)
	log.Infof("Using log file %q", logFile)

	options := config.Options{
		Deployers: deployers.AvailableNames(),
		Workloads: workloads.AvailableNames(),
	}

	Ctx.config, err = config.ReadConfig(configFile, options)
	if err != nil {
		log.Fatalf("Failed to read config: %s", err)
	}

	Ctx.env, err = env.New(Ctx.config, Ctx.log)
	if err != nil {
		log.Fatalf("Failed to create testing context: %s", err)
	}

	log.Infof("Using Timeout: %v", util.Timeout)
	log.Infof("Using RetryInterval: %v", util.RetryInterval)

	os.Exit(m.Run())
}
