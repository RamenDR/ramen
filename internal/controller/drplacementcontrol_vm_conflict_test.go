// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	core "github.com/ramendr/ramen/internal/controller/core"
)

const testRamenOpsNamespace = "ramen-ops"

func makeRamenConfig() *rmn.RamenConfig {
	return &rmn.RamenConfig{
		RamenOpsNamespace: testRamenOpsNamespace,
	}
}

func makeDRPC(recipeRef *rmn.RecipeRef, params map[string][]string, observedGen int64) *rmn.DRPlacementControl {
	drpc := &rmn.DRPlacementControl{}
	drpc.Status.ObservedGeneration = observedGen

	if recipeRef != nil {
		drpc.Spec.KubeObjectProtection = &rmn.KubeObjectProtectionSpec{
			RecipeRef:        recipeRef,
			RecipeParameters: params,
		}
	}

	return drpc
}

func vmRecipeRef() *rmn.RecipeRef {
	return &rmn.RecipeRef{
		Name:      core.VMRecipeName,
		Namespace: testRamenOpsNamespace,
	}
}

func nonVMRecipeRef() *rmn.RecipeRef {
	return &rmn.RecipeRef{
		Name:      "some-other-recipe",
		Namespace: testRamenOpsNamespace,
	}
}

var _ = Describe("bothDRPCsUseVMRecipe", func() {
	cfg := makeRamenConfig()

	It("returns false when first DRPC has no KubeObjectProtection", func() {
		drpc := makeDRPC(nil, nil, 1)
		other := makeDRPC(vmRecipeRef(), nil, 1)
		Expect(bothDRPCsUseVMRecipe(drpc, other, cfg)).To(BeFalse())
	})

	It("returns false when second DRPC has no KubeObjectProtection", func() {
		drpc := makeDRPC(vmRecipeRef(), nil, 1)
		other := makeDRPC(nil, nil, 1)
		Expect(bothDRPCsUseVMRecipe(drpc, other, cfg)).To(BeFalse())
	})

	It("returns false when first DRPC uses a non-VM recipe", func() {
		drpc := makeDRPC(nonVMRecipeRef(), nil, 1)
		other := makeDRPC(vmRecipeRef(), nil, 1)
		Expect(bothDRPCsUseVMRecipe(drpc, other, cfg)).To(BeFalse())
	})

	It("returns false when recipe namespace does not match ramen ops", func() {
		wrongNS := &rmn.RecipeRef{Name: core.VMRecipeName, Namespace: "wrong-ns"}
		drpc := makeDRPC(wrongNS, nil, 1)
		other := makeDRPC(vmRecipeRef(), nil, 1)
		Expect(bothDRPCsUseVMRecipe(drpc, other, cfg)).To(BeFalse())
	})

	It("returns true when both DRPCs use vm-recipe in ramen-ops namespace", func() {
		drpc := makeDRPC(vmRecipeRef(), nil, 1)
		other := makeDRPC(vmRecipeRef(), nil, 1)
		Expect(bothDRPCsUseVMRecipe(drpc, other, cfg)).To(BeTrue())
	})
})

var _ = Describe("vmRecipeParametersOverlap", func() {
	DescribeTable("overlap detection",
		func(drpcParams, otherParams map[string][]string, drpcGen, otherGen int64, expected bool) {
			drpc := makeDRPC(vmRecipeRef(), drpcParams, drpcGen)
			other := makeDRPC(vmRecipeRef(), otherParams, otherGen)
			Expect(vmRecipeParametersOverlap(drpc, other)).To(Equal(expected))
		},
		Entry("no overlap in any key — returns false",
			map[string][]string{core.VMList: {"vm-a"}, core.K8SLabelSelector: {"label-a"}},
			map[string][]string{core.VMList: {"vm-b"}, core.K8SLabelSelector: {"label-b"}},
			int64(1), int64(1),
			false,
		),
		Entry("VM list overlaps, both observed — returns true",
			map[string][]string{core.VMList: {"vm-a", "vm-b"}},
			map[string][]string{core.VMList: {"vm-b", "vm-c"}},
			int64(1), int64(1),
			true,
		),
		Entry("K8S label selector overlaps, both observed — returns true",
			map[string][]string{core.K8SLabelSelector: {"label-x"}},
			map[string][]string{core.K8SLabelSelector: {"label-x", "label-y"}},
			int64(1), int64(1),
			true,
		),
		Entry("PVC label selector overlaps, both observed — returns true",
			map[string][]string{core.PVCLabelSelector: {"pvc-a"}},
			map[string][]string{core.PVCLabelSelector: {"pvc-a"}},
			int64(1), int64(1),
			true,
		),
		Entry("overlap exists but drpc is brand new (gen=0) — returns true (new DRPC should be rejected)",
			map[string][]string{core.VMList: {"vm-a"}},
			map[string][]string{core.VMList: {"vm-a"}},
			int64(0), int64(1),
			true,
		),
		Entry("overlap exists, drpc established but other is brand new (gen=0) — returns false (other will detect on its turn)",
			map[string][]string{core.VMList: {"vm-a"}},
			map[string][]string{core.VMList: {"vm-a"}},
			int64(1), int64(0),
			false,
		),
		Entry("overlap exists, both brand new (gen=0) — returns true",
			map[string][]string{core.VMList: {"vm-a"}},
			map[string][]string{core.VMList: {"vm-a"}},
			int64(0), int64(0),
			true,
		),
		Entry("empty parameters — returns false",
			map[string][]string{},
			map[string][]string{},
			int64(1), int64(1),
			false,
		),
		Entry("nil parameters — returns false",
			nil,
			nil,
			int64(1), int64(1),
			false,
		),
	)
})

var _ = Describe("vmDRPCsProtectIndependentResources", func() {
	cfg := makeRamenConfig()

	It("returns false when DRPCs don't both use VM recipe", func() {
		drpc := makeDRPC(nil, nil, 1)
		other := makeDRPC(vmRecipeRef(), nil, 1)
		Expect(vmDRPCsProtectIndependentResources(drpc, other, cfg)).To(BeFalse())
	})

	It("returns true when both use VM recipe with non-overlapping VMs", func() {
		drpc := makeDRPC(vmRecipeRef(), map[string][]string{core.VMList: {"vm-a"}}, 1)
		other := makeDRPC(vmRecipeRef(), map[string][]string{core.VMList: {"vm-b"}}, 1)
		Expect(vmDRPCsProtectIndependentResources(drpc, other, cfg)).To(BeTrue())
	})

	It("returns false when both use VM recipe with overlapping VMs (both established)", func() {
		drpc := makeDRPC(vmRecipeRef(), map[string][]string{core.VMList: {"vm-a"}}, 1)
		other := makeDRPC(vmRecipeRef(), map[string][]string{core.VMList: {"vm-a"}}, 1)
		Expect(vmDRPCsProtectIndependentResources(drpc, other, cfg)).To(BeFalse())
	})

	It("returns false when drpc is new and VMs overlap — new DRPC conflicts", func() {
		drpc := makeDRPC(vmRecipeRef(), map[string][]string{core.VMList: {"vm-a"}}, 0)
		other := makeDRPC(vmRecipeRef(), map[string][]string{core.VMList: {"vm-a"}}, 1)
		Expect(vmDRPCsProtectIndependentResources(drpc, other, cfg)).To(BeFalse())
	})

	It("returns true when drpc is established and other is new with overlapping VMs — let other detect on its turn", func() {
		drpc := makeDRPC(vmRecipeRef(), map[string][]string{core.VMList: {"vm-a"}}, 1)
		other := makeDRPC(vmRecipeRef(), map[string][]string{core.VMList: {"vm-a"}}, 0)
		Expect(vmDRPCsProtectIndependentResources(drpc, other, cfg)).To(BeTrue())
	})

	It("returns true when both use VM recipe with empty parameters", func() {
		drpc := makeDRPC(vmRecipeRef(), map[string][]string{}, 1)
		other := makeDRPC(vmRecipeRef(), map[string][]string{}, 1)
		Expect(vmDRPCsProtectIndependentResources(drpc, other, cfg)).To(BeTrue())
	})
})
