// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util_test

import (
	"net/netip"

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

	// Tests for IsDiffSyncEnabled
	Expect(util.IsDiffSyncEnabled(nil)).Should(Equal(false))
	Expect(util.IsDiffSyncEnabled(map[string]string{})).Should(Equal(false))
	Expect(util.IsDiffSyncEnabled(map[string]string{"other-key": "true"})).Should(Equal(false))
	Expect(util.IsDiffSyncEnabled(map[string]string{util.EnableDiffAnnotation: ""})).Should(Equal(false))
	Expect(util.IsDiffSyncEnabled(map[string]string{util.EnableDiffAnnotation: "false"})).Should(Equal(false))
	Expect(util.IsDiffSyncEnabled(map[string]string{util.EnableDiffAnnotation: "True"})).Should(Equal(false))
	Expect(util.IsDiffSyncEnabled(map[string]string{util.EnableDiffAnnotation: "true"})).Should(Equal(true))

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

	shortenedPVCName := util.GetLocalServiceNameForRD("long-pvc-name-50-chars-00000000000000000000000000000000000000")

	Expect(shortenedPVCName).Should(Equal("volsync-rsync-tls-dst-6e0c095d"))

	originalPVCName := util.GetLocalServiceNameForRD("normal-pvc-name")
	Expect(originalPVCName).Should(Equal("volsync-rsync-tls-dst-normal-pvc-name"))
})

var _ = Describe("IsSubnetOf", func() {
	// exact match — detected CIDR is identical to configured CIDR
	Expect(util.IsSubnetOf(
		netip.MustParsePrefix("10.131.0.0/23"),
		netip.MustParsePrefix("10.131.0.0/23"),
	)).Should(BeTrue())

	// subnet containment — detected /32 host within configured /23 supernet
	Expect(util.IsSubnetOf(
		netip.MustParsePrefix("10.131.0.0/23"),
		netip.MustParsePrefix("10.131.0.33/32"),
	)).Should(BeTrue())

	// subnet containment — detected /24 within configured /23 supernet
	Expect(util.IsSubnetOf(
		netip.MustParsePrefix("10.131.0.0/23"),
		netip.MustParsePrefix("10.131.0.0/24"),
	)).Should(BeTrue())

	// detected is broader than configured — /22 is not a subnet of /23
	Expect(util.IsSubnetOf(
		netip.MustParsePrefix("10.131.0.0/23"),
		netip.MustParsePrefix("10.131.0.0/22"),
	)).Should(BeFalse())

	// detected IP is outside the configured supernet entirely
	Expect(util.IsSubnetOf(
		netip.MustParsePrefix("10.131.0.0/23"),
		netip.MustParsePrefix("203.0.113.5/32"),
	)).Should(BeFalse())

	// detected starts at same address but is broader — address matches, bits don't
	Expect(util.IsSubnetOf(
		netip.MustParsePrefix("192.168.1.0/24"),
		netip.MustParsePrefix("192.168.0.0/23"),
	)).Should(BeFalse())
})
