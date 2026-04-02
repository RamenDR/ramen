// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
)

var _ = Describe("DRPCDryRunTestFailover", func() {
	var (
		drpc               *rmn.DRPlacementControl
		drpcNamespacedName types.NamespacedName
		namespace          string
		drPolicy           *rmn.DRPolicy
		userPlacement      client.Object
	)

	BeforeEach(func() {
		namespace = "test-drpc-dryrun-" + newRandomNamespaceSuffix()

		// Create namespace
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		}
		Expect(k8sClient.Create(context.TODO(), ns)).To(Succeed())

		// Create DRPolicy
		drPolicy = &rmn.DRPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-drpolicy-" + newRandomNamespaceSuffix(),
			},
			Spec: rmn.DRPolicySpec{
				DRClusters:         []string{East1ManagedCluster, West1ManagedCluster},
				SchedulingInterval: "1h",
			},
		}
		Expect(k8sClient.Create(context.TODO(), drPolicy)).To(Succeed())

		// Create user placement
		userPlacement = createUserPlacement(namespace)

		// Create DRPC
		drpc = &rmn.DRPlacementControl{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-drpc-dryrun",
				Namespace: namespace,
			},
			Spec: rmn.DRPlacementControlSpec{
				PlacementRef: corev1.ObjectReference{
					Name: userPlacement.GetName(),
					Kind: userPlacement.GetObjectKind().GroupVersionKind().Kind,
				},
				DRPolicyRef: corev1.ObjectReference{
					Name: drPolicy.Name,
				},
				PVCSelector: metav1.LabelSelector{
					MatchLabels: map[string]string{
						"appname": "testapp",
					},
				},
			},
		}
		drpcNamespacedName = types.NamespacedName{Name: drpc.Name, Namespace: drpc.Namespace}
	})

	AfterEach(func() {
		// Cleanup - errors are ignored as resources may not exist
		if drpc != nil {
			Expect(k8sClient.Delete(context.TODO(), drpc)).To(Or(Succeed(), MatchError(ContainSubstring("not found"))))
		}

		if userPlacement != nil {
			Expect(k8sClient.Delete(context.TODO(), userPlacement)).To(Or(Succeed(), MatchError(ContainSubstring("not found"))))
		}

		if drPolicy != nil {
			Expect(k8sClient.Delete(context.TODO(), drPolicy)).To(Or(Succeed(), MatchError(ContainSubstring("not found"))))
		}

		if namespace != "" {
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			Expect(k8sClient.Delete(context.TODO(), ns)).To(Or(Succeed(), MatchError(ContainSubstring("not found"))))
		}
	})

	Describe("DryRun field behavior", func() {
		Context("When DryRun is not set (defaults to false)", func() {
			It("should have DryRun defaulting to false", func() {
				drpc.Spec.Action = rmn.ActionFailover
				drpc.Spec.FailoverCluster = East1ManagedCluster
				Expect(k8sClient.Create(context.TODO(), drpc)).To(Succeed())

				// Verify DRPC was created
				Eventually(func() error {
					return apiReader.Get(context.TODO(), drpcNamespacedName, drpc)
				}, timeout, interval).Should(Succeed())

				// Verify DryRun defaults to false (boolean zero value)
				Expect(drpc.Spec.DryRun).To(BeFalse())
			})
		})

		Context("When DryRun is explicitly set to true", func() {
			It("should preserve DryRun=true", func() {
				drpc.Spec.Action = rmn.ActionFailover
				drpc.Spec.FailoverCluster = East1ManagedCluster
				drpc.Spec.DryRun = true
				Expect(k8sClient.Create(context.TODO(), drpc)).To(Succeed())

				// Verify DRPC was created with DryRun=true
				Eventually(func() bool {
					if err := apiReader.Get(context.TODO(), drpcNamespacedName, drpc); err != nil {
						return false
					}

					return drpc.Spec.DryRun
				}, timeout, interval).Should(BeTrue())
			})
		})

		Context("When DryRun transitions from true to false", func() {
			It("should update DryRun field successfully", func() {
				// Create with DryRun=true
				drpc.Spec.Action = rmn.ActionFailover
				drpc.Spec.FailoverCluster = East1ManagedCluster
				drpc.Spec.DryRun = true
				Expect(k8sClient.Create(context.TODO(), drpc)).To(Succeed())

				// Verify initial state
				Eventually(func() bool {
					if err := apiReader.Get(context.TODO(), drpcNamespacedName, drpc); err != nil {
						return false
					}

					return drpc.Spec.DryRun
				}, timeout, interval).Should(BeTrue())

				// Update to DryRun=false
				Eventually(func() error {
					if err := apiReader.Get(context.TODO(), drpcNamespacedName, drpc); err != nil {
						return err
					}

					drpc.Spec.DryRun = false

					return k8sClient.Update(context.TODO(), drpc)
				}, timeout, interval).Should(Succeed())

				// Verify DryRun is now false
				Eventually(func() bool {
					if err := apiReader.Get(context.TODO(), drpcNamespacedName, drpc); err != nil {
						return true // return opposite to fail the check
					}

					return drpc.Spec.DryRun
				}, timeout, interval).Should(BeFalse())
			})
		})
	})

	Describe("DryRun with different actions", func() {
		Context("When DryRun=true with Failover action", func() {
			It("should accept the configuration", func() {
				drpc.Spec.Action = rmn.ActionFailover
				drpc.Spec.FailoverCluster = East1ManagedCluster
				drpc.Spec.DryRun = true
				Expect(k8sClient.Create(context.TODO(), drpc)).To(Succeed())

				Eventually(func() error {
					return apiReader.Get(context.TODO(), drpcNamespacedName, drpc)
				}, timeout, interval).Should(Succeed())

				Expect(drpc.Spec.DryRun).To(BeTrue())
				Expect(drpc.Spec.Action).To(Equal(rmn.ActionFailover))
			})
		})

		Context("When DryRun=true with Relocate action", func() {
			It("should accept the configuration (though dry-run only applies to Failover)", func() {
				drpc.Spec.Action = rmn.ActionRelocate
				drpc.Spec.FailoverCluster = East1ManagedCluster
				drpc.Spec.DryRun = true
				Expect(k8sClient.Create(context.TODO(), drpc)).To(Succeed())

				Eventually(func() error {
					return apiReader.Get(context.TODO(), drpcNamespacedName, drpc)
				}, timeout, interval).Should(Succeed())

				Expect(drpc.Spec.DryRun).To(BeTrue())
				Expect(drpc.Spec.Action).To(Equal(rmn.ActionRelocate))
			})
		})

		Context("When DryRun=false with Failover action", func() {
			It("should accept normal failover configuration", func() {
				drpc.Spec.Action = rmn.ActionFailover
				drpc.Spec.FailoverCluster = East1ManagedCluster
				drpc.Spec.DryRun = false
				Expect(k8sClient.Create(context.TODO(), drpc)).To(Succeed())

				Eventually(func() error {
					return apiReader.Get(context.TODO(), drpcNamespacedName, drpc)
				}, timeout, interval).Should(Succeed())

				Expect(drpc.Spec.DryRun).To(BeFalse())
				Expect(drpc.Spec.Action).To(Equal(rmn.ActionFailover))
			})
		})
	})
})

func createUserPlacement(namespace string) client.Object {
	placement := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-placement-" + newRandomNamespaceSuffix(),
			Namespace: namespace,
		},
		Data: map[string]string{
			"placement": "test",
		},
	}
	Expect(k8sClient.Create(context.TODO(), placement)).To(Succeed())

	return placement
}
