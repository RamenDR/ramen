package e2e_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/suite"
	"k8s.io/client-go/kubernetes"
)

var configFile = "./config/config.yaml"

func init() {
	viper.SetConfigFile(configFile)
	viper.SetEnvPrefix("RAMENE2E")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()
}

type Config struct {
	Clusters map[string]struct {
		KubeconfigPath string `mapstructure:"kubeconfigpath" required:"true"`
	} `mapstructure:"clusters" required:"true"`
	SetupRegionalDRonOCP bool `mapstructure:"setupregionaldronocp" required:"false"`
}
type TestContext struct {
	Config   *Config
	Clusters map[string]struct {
		k8sClientSet kubernetes.Interface
	}
}

func validateConfig(config *Config) {
	if config.Clusters["hub"].KubeconfigPath == "" {
		fmt.Fprintf(os.Stderr, "Failed to find hub cluster in configuration\n")
		os.Exit(1)
	}

	if config.Clusters["c1"].KubeconfigPath == "" {
		fmt.Fprintf(os.Stderr, "Failed to find c1 cluster in configuration\n")
		os.Exit(1)
	}

	if config.Clusters["c2"].KubeconfigPath == "" {
		fmt.Fprintf(os.Stderr, "Failed to find c2 cluster in configuration\n")
		os.Exit(1)
	}
}

func TestMain(t *testing.T) {
	config := &Config{}

	if err := viper.ReadInConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read configuration file: %v\n", err)
		os.Exit(1)
	}
	if err := viper.UnmarshalExact(config); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse configuration: %v\n", err)
		os.Exit(1)
	}

	validateConfig(config)

	testContext, err := NewTestContext(config)
	if err != nil {
		fmt.Printf("Failed to create TestContext: %v\n", err)
		os.Exit(1)
	}

	defer testContext.Cleanup()

	suite.Run(t, &InfraValidationSuite{testContext: testContext})

	if config.SetupRegionalDRonOCP {
		suite.Run(t, &RegionalDRSuite{testContext: testContext})
	}
}

func NewTestContext(config *Config) (*TestContext, error) {
	testContext := &TestContext{
		Config: config,
		Clusters: make(map[string]struct {
			k8sClientSet kubernetes.Interface
		}),
	}
	for clusterName, cluster := range config.Clusters {
		k8sClientSet, err := getClientSetFromKubeConfigPath(cluster.KubeconfigPath)
		if err != nil {
			return nil, err
		}

		testContext.Clusters[clusterName] = struct {
			k8sClientSet kubernetes.Interface
		}{
			k8sClientSet: k8sClientSet,
		}
	}
	return testContext, nil
}

func (ctx *TestContext) Cleanup() {
	// Add cleanup logic here if needed
}
