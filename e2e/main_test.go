// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"flag"
	"os"
	"testing"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/test"
	"github.com/ramendr/ramen/e2e/util"
	"github.com/ramendr/ramen/e2e/workloads"
)

var (
	configFile string
	logFile    string
)

func init() {
	flag.StringVar(&configFile, "config", "config.yaml", "e2e configuration file")
	flag.StringVar(&logFile, "logfile", "ramen-e2e.log", "e2e log file")
}

func TestMain(m *testing.M) {
	var err error

	flag.Parse()

	log, err := test.CreateLogger(logFile)
	if err != nil {
		panic(err)
	}
	// TODO: Sync the log on exit

	log.Infof("Using config file %q", configFile)
	log.Infof("Using log file %q", logFile)

	options := config.Options{
		Deployers: deployers.AvailableNames(),
		Workloads: workloads.AvailableNames(),
	}
	if err := config.ReadConfig(configFile, options); err != nil {
		log.Fatalf("Failed to read config: %s", err)
	}

	util.Ctx, err = util.NewContext(log)
	if err != nil {
		log.Fatalf("Failed to create testing context: %s", err)
	}

	log.Infof("Using Timeout: %v", util.Timeout)
	log.Infof("Using RetryInterval: %v", util.RetryInterval)

	os.Exit(m.Run())
}

type testDef struct {
	name string
	test func(t *testing.T)
}

var Suites = []testDef{
	{"Exhaustive", Exhaustive},
}

func TestSuites(dt *testing.T) {
	t := test.WithLog(dt, util.Ctx.Log)
	t.Log(t.Name())

	if !t.Run("Validate", Validate) {
		t.FailNow()
	}

	for _, suite := range Suites {
		t.Run(suite.name, suite.test)
	}
}
