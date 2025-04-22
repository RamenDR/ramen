// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"testing"

	"github.com/ramendr/ramen/e2e/test"
	"github.com/ramendr/ramen/e2e/validate"
)

func TestValidation(dt *testing.T) {
	t := test.WithLog(dt, Ctx.Logger())
	t.Parallel()

	if err := validate.TestConfig(&Ctx); err != nil {
		t.Fatal(err.Error())
	}

	env := Ctx.Env()

	t.Run("hub", func(dt *testing.T) {
		t := test.WithLog(dt, Ctx.Logger())
		t.Parallel()

		err := validate.RamenHubOperator(&Ctx, env.Hub)
		if err != nil {
			t.Fatalf("Failed to validate hub cluster %q: %s", env.Hub.Name, err)
		}
	})
	t.Run("c1", func(dt *testing.T) {
		t := test.WithLog(dt, Ctx.Logger())
		t.Parallel()

		err := validate.RamenDRClusterOperator(&Ctx, env.C1)
		if err != nil {
			t.Fatalf("Failed to validate dr cluster %q: %s", env.C1.Name, err)
		}
	})
	t.Run("c2", func(dt *testing.T) {
		t := test.WithLog(dt, Ctx.Logger())
		t.Parallel()

		err := validate.RamenDRClusterOperator(&Ctx, env.C2)
		if err != nil {
			t.Fatalf("Failed to validate dr cluster %q: %s", env.C2.Name, err)
		}
	})
}
