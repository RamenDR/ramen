// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/ramendr/ramen/internal/controller/util"
)

var _ = Describe("misc", func() {
	Expect(util.IsCGEnabled(nil)).Should(Equal(false))
	Expect(util.IsCGEnabled(map[string]string{})).Should(Equal(false))
	Expect(util.IsCGEnabled(map[string]string{util.IsCGEnabledAnnotation: "true"})).Should(Equal(true))

	Expect(util.IsPVCMarkedForVolSync(nil)).Should(Equal(false))
	Expect(util.IsPVCMarkedForVolSync(map[string]string{})).Should(Equal(false))
	Expect(util.IsPVCMarkedForVolSync(map[string]string{util.UseVolSyncAnnotation: "true"})).
		Should(Equal(true))
})
