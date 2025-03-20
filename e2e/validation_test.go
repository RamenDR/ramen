// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"testing"

	"github.com/ramendr/ramen/e2e/test"
	"github.com/ramendr/ramen/e2e/validate"
)

func TestValidation(dt *testing.T) {
	t := test.WithLog(dt, Ctx.log)
	t.Parallel()

	t.Run("hub", func(dt *testing.T) {
		t := test.WithLog(dt, Ctx.log)
		t.Parallel()

		err := validate.RamenHubOperator(Ctx.env.Hub, Ctx.config, Ctx.log)
		if err != nil {
			t.Fatalf("Failed to validate hub cluster %q: %s", Ctx.env.Hub.Name, err)
		}
	})
	t.Run("c1", func(dt *testing.T) {
		t := test.WithLog(dt, Ctx.log)
		t.Parallel()

		err := validate.RamenDRClusterOperator(Ctx.env.C1, Ctx.config, Ctx.log)
		if err != nil {
			t.Fatalf("Failed to validate dr cluster %q: %s", Ctx.env.C1.Name, err)
		}
	})
	t.Run("c2", func(dt *testing.T) {
		t := test.WithLog(dt, Ctx.log)
		t.Parallel()

		err := validate.RamenDRClusterOperator(Ctx.env.C2, Ctx.config, Ctx.log)
		if err != nil {
			t.Fatalf("Failed to validate dr cluster %q: %s", Ctx.env.C2.Name, err)
		}
	})
}
