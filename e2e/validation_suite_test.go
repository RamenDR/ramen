// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"testing"

	"github.com/ramendr/ramen/e2e/test"
	"github.com/ramendr/ramen/e2e/util"
)

func Validate(dt *testing.T) {
	t := test.WithLog(dt, util.Ctx.Log)
	t.Helper()
	t.Run("hub", func(dt *testing.T) {
		t := test.WithLog(dt, util.Ctx.Log)

		err := util.ValidateRamenHubOperator(util.Ctx.Hub)
		if err != nil {
			t.Fatalf("Failed to validated hub cluster: %s", err)
		}
	})
	t.Run("c1", func(dt *testing.T) {
		t := test.WithLog(dt, util.Ctx.Log)

		err := util.ValidateRamenDRClusterOperator(util.Ctx.C1, "c1")
		if err != nil {
			t.Fatalf("Failed to validated dr cluster c1: %s", err)
		}
	})
	t.Run("c2", func(dt *testing.T) {
		t := test.WithLog(dt, util.Ctx.Log)

		err := util.ValidateRamenDRClusterOperator(util.Ctx.C2, "c2")
		if err != nil {
			t.Fatalf("Failed to validated dr cluster c2: %s", err)
		}
	})
}
