// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package kubeobjects_test

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ramendr/ramen/internal/controller/kubeobjects"
)

var _ = Describe("kubeobjects", func() {
	Context("comparing errors", func() {
		err := kubeobjects.RequestProcessingErrorCreate("error")
		other := kubeobjects.RequestProcessingErrorCreate("other")

		It("is not equal", func() {
			Expect(other == err).To(Equal(false))
		})
		It("is same error", func() {
			Expect(errors.Is(other, err)).To(Equal(true))
		})
		It("is not same error", func() {
			Expect(errors.Is(err, errors.New("error"))).To(Equal(false))
		})
	})
})
