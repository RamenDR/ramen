// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/ramendr/ramen/controllers/util"
	"golang.org/x/exp/maps" // TODO replace with "maps" in Go 1.21+
)

var _ = Describe("Maps", func() {
	type t map[string]string
	var ax, ay, bx, axBx t
	BeforeEach(func() {
		ax = t{"a": "x"}
		ay = t{"a": "y"}
		bx = t{"b": "x"}
		axBx = t{"a": "x", "b": "x"}
	})
	DescribeTable("MapCopy",
		func(a, b, bExpected t, diffExpected bool) {
			aExpected := maps.Clone(a)
			Expect(util.MapCopy(a, &b)).To(Equal(diffExpected))
			Expect(b).To(ConsistOf(bExpected))
			Expect(a).To(ConsistOf(aExpected))
		},
		Entry(nil, nil, nil, nil, false),
		Entry(nil, ax, nil, ax, true),
		Entry(nil, nil, ax, ax, false),
		Entry(nil, ax, ax, ax, false),
		Entry(nil, ax, ay, ax, true),
		Entry(nil, ax, bx, axBx, true),
	)
})
