// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"testing"
)

func Validate(t *testing.T) {
	t.Helper()

	if !t.Run("CheckRamenHubOperatorStatus", CheckRamenHubOperatorStatus) {
		t.Error()
	}
}

func CheckRamenHubOperatorStatus(t *testing.T) {
}
