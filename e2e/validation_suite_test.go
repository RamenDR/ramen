// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"testing"

	"github.com/ramendr/ramen/e2e/util"
)

func Validate(t *testing.T) {
	t.Helper()
	t.Run("hub", func(t *testing.T) {
		err := util.ValidateRamenHubOperator(util.Ctx.Hub.K8sClientSet)
		if err != nil {
			t.Fatal(err)
		}
	})
	t.Run("c1", func(t *testing.T) {
		err := util.ValidateRamenDRClusterOperator(util.Ctx.C1.K8sClientSet, "c1")
		if err != nil {
			t.Fatal(err)
		}
	})
	t.Run("c2", func(t *testing.T) {
		err := util.ValidateRamenDRClusterOperator(util.Ctx.C2.K8sClientSet, "c2")
		if err != nil {
			t.Fatal(err)
		}
	})
}
