// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"testing"
)

func Basic(t *testing.T) {
	t.Helper()

	e2eContext.Log.Info(t.Name())

	t.Run("Deploy", Deploy)
	t.Run("Enable", Enable)
	t.Run("Failover", Failover)
	t.Run("Relocate", Relocate)
	t.Run("Disable", Disable)
	t.Run("Undeploy", Undeploy)
}

func Deploy(t *testing.T) {
	e2eContext.Log.Info(t.Name())
}

func Enable(t *testing.T) {
	e2eContext.Log.Info(t.Name())
}

func Failover(t *testing.T) {
	e2eContext.Log.Info(t.Name())
}

func Relocate(t *testing.T) {
	e2eContext.Log.Info(t.Name())
}

func Disable(t *testing.T) {
	e2eContext.Log.Info(t.Name())
}

func Undeploy(t *testing.T) {
	e2eContext.Log.Info(t.Name())
}
