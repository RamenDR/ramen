package e2e_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
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
}
type TestContext struct {
	hubClient kubernetes.Interface
	c1Client  kubernetes.Interface
	c2Client  kubernetes.Interface
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

	testContext, err := NewTestContext(config.Clusters["hub"].KubeconfigPath, config.Clusters["c1"].KubeconfigPath, config.Clusters["c2"].KubeconfigPath)

	if err != nil {
		fmt.Printf("Failed to create TestContext: %v\n", err)
		os.Exit(1)
	}

	defer testContext.Cleanup()

}

func NewTestContext(hubKubeconfigPath, c1KubeconfigPath, c2KubeconfigPath string) (*TestContext, error) {
	hubConfig, err := clientcmd.BuildConfigFromFlags("", hubKubeconfigPath)
	if err != nil {
		return nil, err
	}

	c1Config, err := clientcmd.BuildConfigFromFlags("", c1KubeconfigPath)
	if err != nil {
		return nil, err
	}

	c2Config, err := clientcmd.BuildConfigFromFlags("", c2KubeconfigPath)
	if err != nil {
		return nil, err
	}

	hubClient, err := kubernetes.NewForConfig(hubConfig)
	if err != nil {
		return nil, err
	}

	c1Client, err := kubernetes.NewForConfig(c1Config)
	if err != nil {
		return nil, err
	}

	c2Client, err := kubernetes.NewForConfig(c2Config)
	if err != nil {
		return nil, err
	}

	return &TestContext{
		hubClient: hubClient,
		c1Client:  c1Client,
		c2Client:  c2Client,
	}, nil
}

func (ctx *TestContext) HubClient() kubernetes.Interface {
	return ctx.hubClient
}

func (ctx *TestContext) C1Client() kubernetes.Interface {
	return ctx.c1Client
}

func (ctx *TestContext) C2Client() kubernetes.Interface {
	return ctx.c2Client
}

func (ctx *TestContext) Cleanup() {
	// Add cleanup logic here if needed
}
