package util

import (
	"strings"

	"github.com/go-logr/logr"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

type Config struct {
	Clusters map[string]struct {
		KubeconfigPath string `mapstructure:"kubeconfigpath" required:"true"`
	} `mapstructure:"clusters" required:"true"`
}

type Cluster struct {
	K8sClientSet  *kubernetes.Clientset
	DynamicClient *dynamic.DynamicClient
}

type Clusters map[string]*Cluster

type TestContext struct {
	Config   *Config
	Clusters Clusters
	Log      logr.Logger
}

func GetClientSetFromKubeConfigPath(kubeconfigPath string) (*kubernetes.Clientset, *dynamic.DynamicClient, error) {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, nil, err
	}

	k8sClientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, nil, err
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, nil, err
	}

	return k8sClientSet, dynamicClient, nil
}

func (ctx *TestContext) HubClient() *kubernetes.Clientset {
	return ctx.Clusters["hub"].K8sClientSet
}

func (ctx *TestContext) HubDynamicClient() *dynamic.DynamicClient {
	return ctx.Clusters["hub"].DynamicClient
}

func (ctx *TestContext) C1Client() *kubernetes.Clientset {
	return ctx.Clusters["c1"].K8sClientSet
}

func (ctx *TestContext) C1DynamicClient() *dynamic.DynamicClient {
	return ctx.Clusters["c1"].DynamicClient
}

func (ctx *TestContext) C2Client() *kubernetes.Clientset {
	return ctx.Clusters["c2"].K8sClientSet
}

func (ctx *TestContext) C2DynamicClient() *dynamic.DynamicClient {
	return ctx.Clusters["c2"].DynamicClient
}

func (ctx *TestContext) GetClusters() Clusters {
	return ctx.Clusters
}

func (ctx *TestContext) GetHubClusters() Clusters {
	hubClusters := make(Clusters)

	for clusterName, cluster := range ctx.Clusters {
		if strings.Contains(clusterName, "hub") {
			hubClusters[clusterName] = cluster
		}
	}

	return hubClusters
}
