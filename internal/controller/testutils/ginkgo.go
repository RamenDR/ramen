// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package testutils

import "github.com/onsi/gomega/format"

func ConfigureGinkgo() {
	// onsi.github.io/gomega/#adjusting-output
	format.MaxLength = 0
}
