// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"testing"
)

var Deployers = []string{"Subscription", "AppSet", "Imperative"}

var Workloads = []string{"Deployment", "STS", "DaemonSet"}

var Classes = []string{"rbd", "cephfs"}

func Exhaustive(t *testing.T) {
	t.Helper()

	e2eContext.Log.Info(t.Name())

	for w := range Workloads {
		for d := range Deployers {
			dName := Deployers[d]

			t.Run(Workloads[w], func(t *testing.T) {
				e2eContext.Log.Info(t.Name())

				t.Run(dName, func(t *testing.T) {
					e2eContext.Log.Info(t.Name())

					t.Run("Deploy", Deploy)
					t.Run("Enable", Enable)
					t.Run("Failover", Failover)
					t.Run("Relocate", Relocate)
					t.Run("Disable", Disable)
					t.Run("Undeploy", Undeploy)
				})
			})
		}
	}
}
