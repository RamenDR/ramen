package main

import (
	"fmt"

	"github.com/ramendr/ramen/e2e/util"
	"github.com/spf13/viper"
)

func validateConfig(config *util.Config) error {
	if config.Clusters["hub"].KubeconfigPath == "" {
		return fmt.Errorf("failed to find hub cluster in configuration")
	}

	if config.Clusters["c1"].KubeconfigPath == "" {
		return fmt.Errorf("failed to find c1 cluster in configuration")
	}

	if config.Clusters["c2"].KubeconfigPath == "" {
		return fmt.Errorf("failed to find c2 cluster in configuration")
	}

	return nil
}

func readConfig() (*util.Config, error) {
	config := &util.Config{}

	viper.SetConfigFile("config.yaml")

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			return nil, fmt.Errorf("failed to find configuration file: %v", err)
		}

		return nil, fmt.Errorf("failed to read configuration file: %v", err)
	}

	if err := viper.UnmarshalExact(config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal configuration: %v", err)
	}

	if err := validateConfig(config); err != nil {
		return nil, fmt.Errorf("failed to validate configuration: %v", err)
	}

	return config, nil
}

func configContext(ctx *util.TestContext, config *util.Config) error {

	ctx.Config = config
	ctx.Clusters = make(util.Clusters)

	for clusterName, cluster := range config.Clusters {
		k8sClientSet, dynamicClient, err := util.GetClientSetFromKubeConfigPath(cluster.KubeconfigPath)
		if err != nil {
			return err
		}

		ctx.Clusters[clusterName] = &util.Cluster{
			K8sClientSet:  k8sClientSet,
			DynamicClient: dynamicClient,
		}
	}

	return nil
}
