// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"testing"

	"github.com/ramendr/ramen/e2e/util"
)

func Validate(t *testing.T) {
	t.Helper()

	if !t.Run("CheckRamenHubOperatorStatus", CheckRamenHubOperatorStatus) {
		t.Error("CheckRamenHubOperatorStatus failed")
	}

	if !t.Run("CheckRamenSpokeOperatorStatus", CheckRamenSpokeOperatorStatus) {
		t.Error("CheckRamenHubOperatorStatus failed")
	}
}

func CheckRamenHubOperatorStatus(t *testing.T) {
	util.Ctx.Log.Info("enter CheckRamenHubOperatorStatus")

	isRunning, podName, err := util.CheckRamenHubPodRunningStatus(util.Ctx.Hub.K8sClientSet)
	if err != nil {
		t.Error(err)
	}

	if isRunning {
		util.Ctx.Log.Info("Ramen Hub Operator is running", "pod", podName)
	} else {
		t.Error("no running Ramen Hub Operator pod found")
	}
}

func CheckRamenSpokeOperatorStatus(t *testing.T) {
	util.Ctx.Log.Info("enter CheckRamenSpokeOperatorStatus")

	isRunning, podName, err := util.CheckRamenSpokePodRunningStatus(util.Ctx.C1.K8sClientSet)
	if err != nil {
		t.Error(err)
	}

	if isRunning {
		util.Ctx.Log.Info("Ramen Spoke Operator is running on cluster 1", "pod", podName)
	} else {
		t.Error("no running Ramen Spoke Operator pod on cluster 1")
	}

	isRunning, podName, err = util.CheckRamenSpokePodRunningStatus(util.Ctx.C2.K8sClientSet)
	if err != nil {
		t.Error(err)
	}

	if isRunning {
		util.Ctx.Log.Info("Ramen Spoke Operator is running on cluster 2", "pod", podName)
	} else {
		t.Error("no running Ramen Spoke Operator pod on cluster 2")
	}
}
