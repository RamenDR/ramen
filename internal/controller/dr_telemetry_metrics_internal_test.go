// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

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
