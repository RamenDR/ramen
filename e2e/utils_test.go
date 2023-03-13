package e2e_test

import (
	"fmt"

	"k8s.io/client-go/kubernetes"
)

func runOnAllClusters(testContext *TestContext, f func(kubernetes.Interface) error) error {
	var err error

	err = f(testContext.HubClient())
	if err != nil {
		fmt.Printf("Failed to run function on hub cluster: %v\n", err)
	}

	err = f(testContext.C1Client())
	if err != nil {
		fmt.Printf("Failed to run function on cluster 1: %v\n", err)
	}

	err = f(testContext.C2Client())
	if err != nil {
		fmt.Printf("Failed to run function on cluster 2: %v\n", err)
	}

	return err
}

func runOnAllOCPClusters(testContext *TestContext, f func(string) error) error {
	var err error

	for _, cluster := range testContext.Config.Clusters {
		err = f(cluster.KubeconfigPath)
		if err != nil {
			fmt.Printf("Failed to run function on cluster: %v\n", err)
		}
	}

	return err
}
