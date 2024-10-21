// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"errors"
	"testing"

	"github.com/ramendr/ramen/e2e/util"
	"k8s.io/client-go/kubernetes"
)

func Validate(t *testing.T) {
	t.Helper()

	// TODO: Use real cluster names from config

	t.Run("hub", func(t *testing.T) {
		if err := validateHub(util.Ctx.Hub.K8sClientSet, "hub"); err != nil {
			util.Fatal(t, "Validating cluster hub failed", err)
		}
	})
	t.Run("c1", func(t *testing.T) {
		if err := validateCluster(util.Ctx.C1.K8sClientSet, "c1"); err != nil {
			util.Fatal(t, "Validating cluster c2 failed", err)
		}
	})
	t.Run("c2", func(t *testing.T) {
		if err := validateCluster(util.Ctx.C1.K8sClientSet, "c2"); err != nil {
			util.Fatal(t, "Validating cluster c2 failed", err)
		}
	})
}

func validateHub(client *kubernetes.Clientset, name string) error {
	util.Ctx.Log.Info("Validating hub cluster", "name", name)

	isRunning, podName, err := util.CheckRamenHubPodRunningStatus(client)
	if err != nil {
		return err
	}

	if !isRunning {
		return errors.New("no running ramen-hub-operator pod found")
	}

	util.Ctx.Log.Info("Ramen hub operator is running", "pod", podName)

	return nil
}

func validateCluster(client *kubernetes.Clientset, name string) error {
	util.Ctx.Log.Info("Validating managed cluster", "name", name)

	isRunning, podName, err := util.CheckRamenSpokePodRunningStatus(client)
	if err != nil {
		return err
	}

	if !isRunning {
		return errors.New("no running ramen-dr-cluster-operator pod found")
	}

	util.Ctx.Log.Info("Ramen DR cluster operator is running", "pod", podName)

	return nil
}
