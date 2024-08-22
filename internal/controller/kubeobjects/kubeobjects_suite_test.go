// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package kubeobjects_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestKubeobjects(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Kubeobjects Suite")
}
