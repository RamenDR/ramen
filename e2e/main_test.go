// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"flag"
	"os"
	"testing"

	"github.com/ramendr/ramen/e2e/test"
	"github.com/ramendr/ramen/e2e/util"
)

func init() {
	flag.StringVar(&util.ConfigFile, "config", "", "Path to the config file")
}

func TestMain(m *testing.M) {
	var err error

	flag.Parse()

	log, err := test.CreateLogger()
	if err != nil {
		panic(err)
	}
	// TODO: Sync the log on exit

	util.Ctx, err = util.NewContext(log, util.ConfigFile)
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
