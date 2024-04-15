// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-logr/logr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/kubectl/pkg/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	uberzap "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	kubeconfigHub string
	kubeconfigC1  string
	kubeconfigC2  string
)

func init() {
	flag.StringVar(&kubeconfigHub, "kubeconfig-hub", "", "Path to the kubeconfig file for the hub cluster")
	flag.StringVar(&kubeconfigC1, "kubeconfig-c1", "", "Path to the kubeconfig file for the C1 managed cluster")
	flag.StringVar(&kubeconfigC2, "kubeconfig-c2", "", "Path to the kubeconfig file for the C2 managed cluster")
}

type Cluster struct {
	K8sClientSet *kubernetes.Clientset
	CtrlClient   client.Client
}

type Context struct {
	Log *logr.Logger
	Hub Cluster
	C1  Cluster
	C2  Cluster
}

var e2eContext *Context

func setupClient(kubeconfigPath string) (*kubernetes.Clientset, client.Client, error) {
	var err error

	if kubeconfigPath == "" {
		return nil, nil, fmt.Errorf("kubeconfigPath is empty")
	}

	kubeconfigPath, err = filepath.Abs(kubeconfigPath)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to determine absolute path to file (%s): %w", kubeconfigPath, err)
	}

	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build config from kubeconfig (%s): %w", kubeconfigPath, err)
	}

	k8sClientSet, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build k8s client set from kubeconfig (%s): %w", kubeconfigPath, err)
	}

	ctrlClient, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build controller client from kubeconfig (%s): %w", kubeconfigPath, err)
	}

	return k8sClientSet, ctrlClient, nil
}

func newContext(log *logr.Logger, hub, c1, c2 string) (*Context, error) {
	var err error

	ctx := new(Context)
	ctx.Log = log

	ctx.Hub.K8sClientSet, ctx.Hub.CtrlClient, err = setupClient(hub)
	if err != nil {
		return nil, fmt.Errorf("failed to create clients for hub cluster: %w", err)
	}

	ctx.C1.K8sClientSet, ctx.C1.CtrlClient, err = setupClient(c1)
	if err != nil {
		return nil, fmt.Errorf("failed to create clients for c1 cluster: %w", err)
	}

	ctx.C2.K8sClientSet, ctx.C2.CtrlClient, err = setupClient(c2)
	if err != nil {
		return nil, fmt.Errorf("failed to create clients for c2 cluster: %w", err)
	}

	return ctx, nil
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

	e2eContext, err = newContext(&log, kubeconfigHub, kubeconfigC1, kubeconfigC2)
	if err != nil {
		log.Error(err, "unable to create new testing context")

		panic(err)
	}

	os.Exit(m.Run())
}

type testDef struct {
	name string
	test func(t *testing.T)
}

var Suites = []testDef{
	{"Basic", Basic},
	{"Validate", Validate},
	{"Exhaustive", Exhaustive},
}

func TestSuites(t *testing.T) {
	e2eContext.Log.Info(t.Name())

	for idx := range Suites {
		t.Run(Suites[idx].name, Suites[idx].test)
	}
}
