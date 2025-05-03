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

	pvcNamespace1 := "busybox-box-appT"
	pvcNamespace2 := "busybox-box-appppppppXppppT"
	pvcNamespace3 := "busybox-box-appppppppXppppddffghT"
	pvcNamespace4 := "busybox-box-appppppppXppppppppppppppppppppppppppppppppppppppppppppppppT"
	storageID := "496eec7b8b195d78d2265f7550fba723"

	Expect(util.GenerateCombinedLabel(pvcNamespace1, storageID)).
		Should(Equal("busybox-box-appT.496eec7b8b195d78d2265f7550fba723"))
	Expect(util.GenerateCombinedLabel(pvcNamespace2, storageID)).
		Should(Equal("busybox-box-appppppppXppppT.496eec7b8b195d78d2265f7550fba723"))
	Expect(util.GenerateCombinedLabel(pvcNamespace3, storageID)).
		Should(Equal("busybox-box-appppppppX10011fcc.496eec7b8b195d78d2265f7550fba723"))
	Expect(util.GenerateCombinedLabel(pvcNamespace4, storageID)).
		Should(Equal("busybox-box-appppppppXce2e9aed.496eec7b8b195d78d2265f7550fba723"))
})
