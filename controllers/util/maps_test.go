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
	type t map[int]int
	DescribeTable("MapCopy",
		func(a, b, bExpected t, diffExpected bool) {
			aExpected := maps.Clone(a)
			Expect(util.MapCopy(a, &b)).To(Equal(diffExpected))
			Expect(b).To(Equal(bExpected))
			Expect(a).To(Equal(aExpected))
		},
		Entry(nil, nil, nil, nil, false),
		Entry(nil, t{0: 1}, nil, t{0: 1}, true),
		Entry(nil, nil, t{0: 1}, t{0: 1}, false),
		Entry(nil, t{0: 1}, t{0: 1}, t{0: 1}, false),
		Entry(nil, t{0: 1}, t{0: 7}, t{0: 1}, true),
		Entry(nil, t{0: 1}, t{3: 6}, t{0: 1, 3: 6}, true),
	)
})
