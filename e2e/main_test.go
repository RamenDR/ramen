// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"flag"
	"os"
	"testing"

	"github.com/ramendr/ramen/e2e/util"
	uberzap "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func init() {
	flag.StringVar(&util.ConfigFile, "config", "", "Path to the config file")
}

func TestMain(m *testing.M) {
	var err error

	flag.Parse()

	log := zap.New(zap.UseFlagOptions(&zap.Options{
		Development: true,
		ZapOpts: []uberzap.Option{
			uberzap.AddCaller(),
		},
		TimeEncoder: zapcore.ISO8601TimeEncoder,
	}))

	util.Ctx, err = util.NewContext(log, util.ConfigFile)
	if err != nil {
		log.Error(err, "unable to create new testing context")

		panic(err)
	}

	log.Info("Global setting", "Timeout", util.Timeout)
	log.Info("Global setting", "Retry Interval", util.RetryInterval)

	os.Exit(m.Run())
}

type testDef struct {
	name string
	test func(t *testing.T)
}

var Suites = []testDef{
	{"Exhaustive", Exhaustive},
}

func TestSuites(t *testing.T) {
	util.Ctx.Log.Info(t.Name())

	if !t.Run("Validate", Validate) {
		t.Fatal("failed to validate the test suite")
	}

	for _, suite := range Suites {
		t.Run(suite.name, suite.test)
	}
}
