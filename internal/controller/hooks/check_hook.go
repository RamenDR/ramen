// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"fmt"

	"github.com/go-logr/logr"
	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type CheckHook struct {
	Hook *kubeobjects.HookSpec
}

func (c CheckHook) Execute(client client.Client, log logr.Logger) error {
	hookResult, err := EvaluateCheckHook(client, c.Hook, log)
	if err != nil {
		log.Error(err, "error occurred while evaluating check hook")

		return err
	}

	hookName := c.Hook.Name + "/" + c.Hook.Chk.Name
	log.Info("check hook executed successfully", "hook", hookName, "result", hookResult)

	if !hookResult && shouldHookBeFailedOnError(c.Hook) {
		return fmt.Errorf("stopping workflow as hook %s failed", c.Hook.Name)
	}

	return nil
}

func shouldHookBeFailedOnError(hook *kubeobjects.HookSpec) bool {
	// hook.Check.OnError overwrites the feature of hook.OnError -- defaults to fail
	if hook.Chk.OnError != "" && hook.Chk.OnError == "continue" {
		return false
	}

	if hook.OnError != "" && hook.OnError == "continue" {
		return false
	}

	return true
}
