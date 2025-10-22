// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/validation"

	"github.com/ramendr/ramen/internal/controller/util"
)

var _ = Describe("misc", func() {
	Expect(util.IsPVCMarkedForVolSync(nil)).Should(Equal(false))
	Expect(util.IsPVCMarkedForVolSync(map[string]string{})).Should(Equal(false))
	Expect(util.IsPVCMarkedForVolSync(map[string]string{util.UseVolSyncAnnotation: "true"})).
		Should(Equal(true))

	pvcNamespace1 := "busybox-box-appT"
	pvcNamespace2 := "busybox-box-appppppppXppppT"
	pvcNamespace3 := "busybox-box-appppppppXppppddffghT"
	pvcNamespace4 := "busybox-box-appppppppXppppppppppppppppppppppppppppppppppppppppppppppppT"
	storageID := "496eec7b8b195d78d2265f7550fba723"

	Expect(util.GenerateCombinedName(pvcNamespace1, storageID)).
		Should(Equal("busybox-box-appT-496eec7b8b195d78d2265f7550fba723"))
	Expect(util.GenerateCombinedName(pvcNamespace2, storageID)).
		Should(Equal("busybox-box-appppppppXppppT-496eec7b8b195d78d2265f7550fba723"))
	Expect(util.GenerateCombinedName(pvcNamespace3, storageID)).
		Should(Equal("10011fcc-496eec7b8b195d78d2265f7550fba723"))
	Expect(util.GenerateCombinedName(pvcNamespace4, storageID)).
		Should(Equal("ce2e9aed-496eec7b8b195d78d2265f7550fba723"))

	pvcNamespace5 := "busybox-box-appppppppXppppppppppppppppppppppppppppppppppppppppppppppppT"
	storageID2 := "111111111111111111111111111111111111111496eec7b8b195d78d2265f7550fba723"

	Expect(util.GenerateCombinedName(pvcNamespace5, storageID2)).
		Should(Equal("ce2e9aed-5b7c8892"))

	longResourceName := "54a5f0ff705e7f7d94338c65839890abapp-busybox-rbd-1-cg-placement---------------..--"

	validResourceName := util.TrimToK8sResourceNameLength(longResourceName)
	errs := validation.NameIsDNSSubdomain(validResourceName, false)

	Expect(errs).To(BeEmpty(), "expected a valid DNS subdomain name, got errors: %v", errs)
})
