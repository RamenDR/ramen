// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	controllers "github.com/ramendr/ramen/internal/controller"
)

// drTelemetryGaugeValue reads a DR telemetry gauge from the controller
// metrics registry; a series that is not present reads as 0
func drTelemetryGaugeValue(metricName, labelName, labelValue string) float64 {
	metricFamilies, err := metrics.Registry.Gather()
	Expect(err).NotTo(HaveOccurred())

	for _, metricFamily := range metricFamilies {
		if metricFamily.GetName() != metricName {
			continue
		}

		for _, metric := range metricFamily.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == labelName && label.GetValue() == labelValue {
					return metric.GetGauge().GetValue()
				}
			}
		}
	}

	return 0
}

var _ = Describe("DRTelemetryWiring", func() {
	const (
		timeout  = time.Second * 10
		interval = time.Millisecond * 100
	)

	Describe("DRPolicy reconciler", func() {
		It("reflects DRPolicy creation and deletion in ramen_dr_policy_type", func() {
			baseline := drTelemetryGaugeValue("ramen_dr_policy_type", controllers.DRTypeLabel, controllers.DRTypeRegional)

			drpolicy := &rmn.DRPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "telemetry-wiring-regional"},
				Spec: rmn.DRPolicySpec{
					SchedulingInterval: "99m",
					DRClusters:         []string{"telemetry-absent-c1", "telemetry-absent-c2"},
				},
			}
			Expect(k8sClient.Create(context.TODO(), drpolicy)).To(Succeed())

			Eventually(func() float64 {
				return drTelemetryGaugeValue("ramen_dr_policy_type", controllers.DRTypeLabel, controllers.DRTypeRegional)
			}, timeout, interval).Should(Equal(baseline + 1))

			Expect(k8sClient.Delete(context.TODO(), drpolicy)).To(Succeed())

			Eventually(func() float64 {
				return drTelemetryGaugeValue("ramen_dr_policy_type", controllers.DRTypeLabel, controllers.DRTypeRegional)
			}, timeout, interval).Should(Equal(baseline))
		})
	})

	Describe("DRPC reconciler", func() {
		It("reflects DRPC creation and deletion in ramen_dr_protected_apps", func() {
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: "telemetry-wiring-ns"},
			}
			Expect(k8sClient.Create(context.TODO(), namespace)).To(Succeed())

			baseline := drTelemetryGaugeValue(
				"ramen_dr_protected_apps", controllers.ManagementMethodLabel, controllers.ManagementMethodManaged)

			drpc := &rmn.DRPlacementControl{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "telemetry-wiring-drpc",
					Namespace: namespace.Name,
				},
				Spec: rmn.DRPlacementControlSpec{
					PlacementRef: corev1.ObjectReference{
						Kind: "Placement", Name: "telemetry-absent-placement", Namespace: namespace.Name,
					},
					DRPolicyRef: corev1.ObjectReference{Name: "telemetry-absent-policy"},
					PVCSelector: metav1.LabelSelector{},
				},
			}
			Expect(k8sClient.Create(context.TODO(), drpc)).To(Succeed())

			Eventually(func() float64 {
				return drTelemetryGaugeValue(
					"ramen_dr_protected_apps", controllers.ManagementMethodLabel, controllers.ManagementMethodManaged)
			}, timeout, interval).Should(Equal(baseline + 1))

			Expect(k8sClient.Delete(context.TODO(), drpc)).To(Succeed())

			Eventually(func() float64 {
				return drTelemetryGaugeValue(
					"ramen_dr_protected_apps", controllers.ManagementMethodLabel, controllers.ManagementMethodManaged)
			}, timeout, interval).Should(Equal(baseline))
		})
	})
})
