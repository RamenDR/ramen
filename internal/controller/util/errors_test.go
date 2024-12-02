// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util_test

import (
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/ramendr/ramen/internal/controller/util"
)

var _ = Describe("OperationInProgress", func() {
	Context("comparing errors", func() {
		err := util.OperationInProgress("this operation")

		It("is not equal", func() {
			Expect(err == util.OperationInProgress("other operation")).To(Equal(false))
		})
		It("match error", func() {
			Expect(util.IsOperationInProgress(err)).To(Equal(true))
		})
		It("match wrapped error", func() {
			wrapped := fmt.Errorf("wrapping operation in progress: %w", err)
			Expect(util.IsOperationInProgress(wrapped)).To(Equal(true))
		})
		It("does not match other errors", func() {
			Expect(util.IsOperationInProgress(errors.New("other error"))).To(Equal(false))
		})
	})
})
