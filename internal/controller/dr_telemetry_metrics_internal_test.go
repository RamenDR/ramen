// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus/testutil"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
)

func drPolicyWithPeerClasses(syncPeers, asyncPeers []rmn.PeerClass) *rmn.DRPolicy {
	return &rmn.DRPolicy{
		Status: rmn.DRPolicyStatus{
			Sync:  rmn.Sync{PeerClasses: syncPeers},
			Async: rmn.Async{PeerClasses: asyncPeers},
		},
	}
}

func drPolicyWithInterval(schedulingInterval string) *rmn.DRPolicy {
	return &rmn.DRPolicy{
		Spec: rmn.DRPolicySpec{SchedulingInterval: schedulingInterval},
	}
}

var _ = Describe("DRTelemetryMetrics drPolicyDRType", func() {
	peers := []rmn.PeerClass{{StorageID: []string{"storage-id-1"}}}

	DescribeTable("classifies a DRPolicy",
		func(drpolicy *rmn.DRPolicy, expected string) {
			Expect(drPolicyDRType(drpolicy)).To(Equal(expected))
		},
		Entry("sync peerClasses populated", drPolicyWithPeerClasses(peers, nil), DRTypeMetro),
		Entry("async peerClasses populated", drPolicyWithPeerClasses(nil, peers), DRTypeRegional),
		Entry("both sync and async peerClasses populated", drPolicyWithPeerClasses(peers, peers), DRTypeMetro),
		Entry("no peerClasses, no scheduling interval", drPolicyWithInterval(""), DRTypeMetro),
		Entry("no peerClasses, zero scheduling interval", drPolicyWithInterval("0m"), DRTypeMetro),
		Entry("no peerClasses, non-zero scheduling interval", drPolicyWithInterval("5m"), DRTypeRegional),
		Entry("no peerClasses, malformed scheduling interval", drPolicyWithInterval("5x"), DRTypeUnknown),
	)
})

var _ = Describe("DRTelemetryMetrics syncDRActionCountAnnotation", func() {
	drpcWithPhase := func(phase rmn.DRState, annotations map[string]string) *rmn.DRPlacementControl {
		drpc := &rmn.DRPlacementControl{}
		drpc.SetAnnotations(annotations)
		drpc.Status.Phase = phase

		return drpc
	}

	syncThroughPhases := func(drpc *rmn.DRPlacementControl, phases ...rmn.DRState) {
		for _, phase := range phases {
			drpc.Status.Phase = phase
			syncDRActionCountAnnotation(drpc)
		}
	}

	It("does nothing for a DRPC that has not initiated an action", func() {
		drpc := drpcWithPhase("", nil)
		Expect(syncDRActionCountAnnotation(drpc)).To(BeFalse())
		Expect(drpc.GetAnnotations()).To(BeEmpty())

		drpc = drpcWithPhase(rmn.Deployed, nil)
		Expect(syncDRActionCountAnnotation(drpc)).To(BeFalse())
		Expect(drpc.GetAnnotations()).To(BeEmpty())
	})

	It("counts a failover when Initiating transitions into FailingOver", func() {
		drpc := drpcWithPhase(rmn.Initiating, nil)
		Expect(syncDRActionCountAnnotation(drpc)).To(BeTrue())

		failover, relocate := drpcActionCounts(drpc)
		Expect(failover).To(BeZero())
		Expect(relocate).To(BeZero())

		drpc.Status.Phase = rmn.FailingOver
		Expect(syncDRActionCountAnnotation(drpc)).To(BeTrue())

		failover, relocate = drpcActionCounts(drpc)
		Expect(failover).To(Equal(1.0))
		Expect(relocate).To(BeZero())
	})

	It("counts a relocate when Initiating transitions into Relocating", func() {
		drpc := drpcWithPhase("", nil)
		syncThroughPhases(drpc, rmn.Initiating, rmn.Relocating)

		failover, relocate := drpcActionCounts(drpc)
		Expect(failover).To(BeZero())
		Expect(relocate).To(Equal(1.0))
	})

	It("does not double count while the phase is unchanged", func() {
		drpc := drpcWithPhase("", nil)
		syncThroughPhases(drpc, rmn.Initiating, rmn.FailingOver)

		Expect(syncDRActionCountAnnotation(drpc)).To(BeFalse())

		failover, _ := drpcActionCounts(drpc)
		Expect(failover).To(Equal(1.0))
	})

	It("does not count action phase re-entry without a new initiation", func() {
		drpc := drpcWithPhase("", nil)
		// Post-action cleanup can flap between the action phase and its
		// completed phase; such re-entries are not new actions
		syncThroughPhases(drpc, rmn.Initiating, rmn.Relocating, rmn.Relocated, rmn.Relocating, rmn.Relocated)

		_, relocate := drpcActionCounts(drpc)
		Expect(relocate).To(Equal(1.0))
	})

	It("counts each newly initiated action", func() {
		drpc := drpcWithPhase("", nil)
		syncThroughPhases(drpc, rmn.Initiating, rmn.FailingOver, rmn.FailedOver,
			rmn.Initiating, rmn.Relocating, rmn.Relocated,
			rmn.Initiating, rmn.FailingOver)

		failover, relocate := drpcActionCounts(drpc)
		Expect(failover).To(Equal(2.0))
		Expect(relocate).To(Equal(1.0))
	})

	It("disarms when Initiating leads to a non-action phase and rearms on the next initiation", func() {
		drpc := drpcWithPhase("", nil)
		syncThroughPhases(drpc, rmn.Initiating, rmn.WaitForUser, rmn.Initiating, rmn.Relocating)

		failover, relocate := drpcActionCounts(drpc)
		Expect(failover).To(BeZero())
		Expect(relocate).To(Equal(1.0))
	})

	It("recovers from a malformed annotation", func() {
		drpc := drpcWithPhase("", map[string]string{
			DRActionCountAnnotation: "not-json",
		})
		syncThroughPhases(drpc, rmn.Initiating, rmn.FailingOver)

		failover, relocate := drpcActionCounts(drpc)
		Expect(failover).To(Equal(1.0))
		Expect(relocate).To(BeZero())
	})

	It("reports zero counts for a DRPC without the annotation", func() {
		failover, relocate := drpcActionCounts(drpcWithPhase(rmn.Deployed, nil))
		Expect(failover).To(BeZero())
		Expect(relocate).To(BeZero())
	})
})

var _ = Describe("DRTelemetryMetrics", func() {
	BeforeEach(func() {
		InitDRTelemetryMetrics()
	})

	Describe("InitDRTelemetryMetrics", func() {
		It("initializes every label combination to zero", func() {
			Expect(testutil.CollectAndCount(drPolicyType)).To(Equal(3))
			Expect(testutil.CollectAndCount(drProtectedApps)).To(Equal(2))
			Expect(testutil.CollectAndCount(drActions)).To(Equal(2))

			for _, drType := range []string{DRTypeMetro, DRTypeRegional, DRTypeUnknown} {
				Expect(testutil.ToFloat64(drPolicyType.WithLabelValues(drType))).To(BeZero())
			}

			for _, method := range []string{ManagementMethodDiscovered, ManagementMethodManaged} {
				Expect(testutil.ToFloat64(drProtectedApps.WithLabelValues(method))).To(BeZero())
			}

			for _, action := range []string{ActionFailover, ActionRelocate} {
				Expect(testutil.ToFloat64(drActions.WithLabelValues(action))).To(BeZero())
			}
		})

		It("resets previously set values to zero", func() {
			SetDRPolicyTypeMetric(DRTypeMetro, 4)
			InitDRTelemetryMetrics()

			Expect(testutil.ToFloat64(drPolicyType.WithLabelValues(DRTypeMetro))).To(BeZero())
		})
	})

	Describe("Set helpers", func() {
		It("sets the policy type gauge for the given dr_type", func() {
			SetDRPolicyTypeMetric(DRTypeMetro, 2)
			SetDRPolicyTypeMetric(DRTypeRegional, 3)

			Expect(testutil.ToFloat64(drPolicyType.WithLabelValues(DRTypeMetro))).To(Equal(2.0))
			Expect(testutil.ToFloat64(drPolicyType.WithLabelValues(DRTypeRegional))).To(Equal(3.0))
			Expect(testutil.ToFloat64(drPolicyType.WithLabelValues(DRTypeUnknown))).To(BeZero())
		})

		It("sets the protected apps gauge for the given management_method", func() {
			SetDRProtectedAppsMetric(ManagementMethodDiscovered, 5)

			Expect(testutil.ToFloat64(drProtectedApps.WithLabelValues(ManagementMethodDiscovered))).To(Equal(5.0))
			Expect(testutil.ToFloat64(drProtectedApps.WithLabelValues(ManagementMethodManaged))).To(BeZero())
		})

		It("sets the actions gauge for the given action", func() {
			SetDRActionsMetric(ActionFailover, 7)
			SetDRActionsMetric(ActionRelocate, 1)

			Expect(testutil.ToFloat64(drActions.WithLabelValues(ActionFailover))).To(Equal(7.0))
			Expect(testutil.ToFloat64(drActions.WithLabelValues(ActionRelocate))).To(Equal(1.0))
		})
	})
})
