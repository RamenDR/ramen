// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"testing"
)

func Validate(t *testing.T) {
	t.Helper()

	e2eContext.Log.Info(t.Name())

	t.Run("RamenHub", RamenHub)
	t.Run("RamenSpokes", RamenSpoke)
	t.Run("Ceph", Ceph)
}

func RamenHub(t *testing.T) {
	e2eContext.Log.Info(t.Name())
}

func RamenSpoke(t *testing.T) {
	e2eContext.Log.Info(t.Name())
}

func Ceph(t *testing.T) {
	e2eContext.Log.Info(t.Name())
}
