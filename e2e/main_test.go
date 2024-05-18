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
	flag.StringVar(&util.KubeconfigHub, "kubeconfig-hub", "", "Path to the kubeconfig file for the hub cluster")
	flag.StringVar(&util.KubeconfigC1, "kubeconfig-c1", "", "Path to the kubeconfig file for the C1 managed cluster")
	flag.StringVar(&util.KubeconfigC2, "kubeconfig-c2", "", "Path to the kubeconfig file for the C2 managed cluster")
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

	util.Ctx, err = util.NewContext(&log, util.KubeconfigHub, util.KubeconfigC1, util.KubeconfigC2)
	if err != nil {
		log.Error(err, "unable to create new testing context")

		panic(err)
	}

	if err := util.ReadConfig(); err != nil {
		panic(err)
	}

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

	if err := util.EnsureChannel(); err != nil {
		t.Fatalf("failed to ensure channel: %v", err)
	}

	if !t.Run("Validate", Validate) {
		t.Fatal("failed to validate the test suite")
	}

	for _, suite := range Suites {
		t.Run(suite.name, suite.test)
	}

	t.Cleanup(func() {
		if err := util.EnsureChannelDeleted(); err != nil {
			t.Fatalf("failed to ensure channel deleted: %v", err)
		}
	})
}
