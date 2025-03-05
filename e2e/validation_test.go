// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"testing"

	"github.com/ramendr/ramen/e2e/test"
	"github.com/ramendr/ramen/e2e/util"
)

func TestValidation(dt *testing.T) {
	t := test.WithLog(dt, util.Ctx.Log)
	t.Parallel()

	t.Run("hub", func(dt *testing.T) {
		t := test.WithLog(dt, util.Ctx.Log)
		t.Parallel()

		err := util.ValidateRamenHubOperator(util.Ctx.Hub)
		if err != nil {
			t.Fatalf("Failed to validate hub cluster %q: %s", util.Ctx.Hub.Name, err)
		}
	})
	t.Run("c1", func(dt *testing.T) {
		t := test.WithLog(dt, util.Ctx.Log)
		t.Parallel()

		err := util.ValidateRamenDRClusterOperator(util.Ctx.C1)
		if err != nil {
			t.Fatalf("Failed to validate dr cluster %q: %s", util.Ctx.C1.Name, err)
		}
	})
	t.Run("c2", func(dt *testing.T) {
		t := test.WithLog(dt, util.Ctx.Log)
		t.Parallel()

		err := util.ValidateRamenDRClusterOperator(util.Ctx.C2)
		if err != nil {
			t.Fatalf("Failed to validate dr cluster %q: %s", util.Ctx.C2.Name, err)
		}
	})
}
