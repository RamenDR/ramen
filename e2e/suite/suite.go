package suite

import (
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	"github.com/ramendr/ramen/e2e/utils"
	"k8s.io/client-go/kubernetes"
)

type Config struct {
	Clusters map[string]struct {
		KubeconfigPath string `mapstructure:"kubeconfigpath" required:"true"`
	} `mapstructure:"clusters" required:"true"`
}

type Cluster struct {
	k8sClientSet kubernetes.Interface
}

func (c *Cluster) GetClient() kubernetes.Interface {
	return c.k8sClientSet
}

type Clusters map[string]*Cluster

type TestContext struct {
	Config   *Config
	Clusters Clusters
	Logger   logr.Logger
}

func NewTestContext(config *Config) (*TestContext, error) {
	testContext := &TestContext{
		Config: config,
	}

	testContext.Clusters = make(Clusters)

	for clusterName, cluster := range config.Clusters {
		k8sClientSet, err := utils.GetClientSetFromKubeConfigPath(cluster.KubeconfigPath)
		if err != nil {
			return nil, err
		}

		testContext.Clusters[clusterName] = &Cluster{
			k8sClientSet: k8sClientSet,
		}
	}

	return testContext, nil
}

func (ctx *TestContext) HubClient() kubernetes.Interface {
	return ctx.Clusters["hub"].k8sClientSet
}

func (ctx *TestContext) C1Client() kubernetes.Interface {
	return ctx.Clusters["c1"].k8sClientSet
}

func (ctx *TestContext) C2Client() kubernetes.Interface {
	return ctx.Clusters["c2"].k8sClientSet
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

func (ctx *TestContext) GetManagedClusters() Clusters {
	managedClusters := make(Clusters)

	for clusterName, cluster := range ctx.Clusters {
		if !strings.Contains(clusterName, "hub") {
			managedClusters[clusterName] = cluster
		}
	}

	return managedClusters
}

type TestSuite interface {
	SetContext(ctx *TestContext)
	SetupSuite() error
	TeardownSuite() error
	Tests() []Test
	Description() string
	IsEnabled() bool
	Enable()
	Disable()
	Name() string
}

type Test func() error

func RunSuite(suite TestSuite, ctx *TestContext) error {
	suite.SetContext(ctx)

	if err := suite.SetupSuite(); err != nil {
		return fmt.Errorf("setup suite failed: %w", err)
	}

	defer func() {
		if err := suite.TeardownSuite(); err != nil {
			panic(fmt.Errorf("teardown suite failed: %w", err))
		}
	}()

	for _, test := range suite.Tests() {
		if err := test(); err != nil {
			return fmt.Errorf("test failed: %w", err)
		}
	}

	return nil
}
