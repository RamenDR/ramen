// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"testing"

	"github.com/ramendr/ramen/e2e/test"
	"github.com/ramendr/ramen/e2e/util"
)

func TestValidation(dt *testing.T) {
	t := test.WithLog(dt, Ctx.log)
	t.Parallel()

	t.Run("hub", func(dt *testing.T) {
		t := test.WithLog(dt, Ctx.log)
		t.Parallel()

		err := util.ValidateRamenHubOperator(util.Ctx.Hub, Ctx.log)
		if err != nil {
			t.Fatalf("Failed to validate hub cluster %q: %s", util.Ctx.Hub.Name, err)
		}
	})
	t.Run("c1", func(dt *testing.T) {
		t := test.WithLog(dt, Ctx.log)
		t.Parallel()

		err := util.ValidateRamenDRClusterOperator(util.Ctx.C1, Ctx.log)
		if err != nil {
			t.Fatalf("Failed to validate dr cluster %q: %s", util.Ctx.C1.Name, err)
		}
	})
	t.Run("c2", func(dt *testing.T) {
		t := test.WithLog(dt, Ctx.log)
		t.Parallel()

		err := util.ValidateRamenDRClusterOperator(util.Ctx.C2, Ctx.log)
		if err != nil {
			t.Fatalf("Failed to validate dr cluster %q: %s", util.Ctx.C2.Name, err)
		}
	})
}
