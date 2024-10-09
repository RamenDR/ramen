// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/ramendr/ramen/internal/controller/util"
)

var _ = Describe("Testing Locks", func() {
	nsLock := util.NewNamespaceLock()
	Expect(nsLock.TryToAcquireLock("test")).To(BeTrue())
	Expect(nsLock.TryToAcquireLock("test")).To(BeFalse())
	nsLock.Release("test")
	Expect(nsLock.TryToAcquireLock("test")).To(BeTrue())
})
