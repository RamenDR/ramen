// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"

	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
)

var _ = Describe("VRGDryRunSnapshotManagement", Serial, func() {
	var (
		vrg               *ramendrv1alpha1.VolumeReplicationGroup
		vrgNamespacedName types.NamespacedName
		namespace         string
	)

	BeforeEach(func() {
		namespace = "test-dryrun-" + newRandomNamespaceSuffix()

		// Create namespace
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		}
		Expect(k8sClient.Create(context.TODO(), ns)).To(Succeed())

		// Create VRG with minimal configuration
		vrg = &ramendrv1alpha1.VolumeReplicationGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-vrg-dryrun",
				Namespace: namespace,
			},
			Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
				PVCSelector: metav1.LabelSelector{
					MatchLabels: map[string]string{
						"appname": "testapp",
					},
				},
				ReplicationState: ramendrv1alpha1.Primary,
				S3Profiles:       []string{s3Profiles[0].S3ProfileName},
			},
		}
		vrgNamespacedName = types.NamespacedName{Name: vrg.Name, Namespace: vrg.Namespace}
	})

	AfterEach(func() {
		// Cleanup
		if vrg != nil {
			Expect(k8sClient.Delete(context.TODO(), vrg)).To(Or(Succeed(), MatchError(ContainSubstring("not found"))))
		}

		// Clean up any snapshots
		snapshots := &snapv1.VolumeSnapshotList{}
		if err := k8sClient.List(context.TODO(), snapshots, client.InNamespace(namespace)); err == nil {
			for i := range snapshots.Items {
				// Ignore errors during cleanup
				_ = k8sClient.Delete(context.TODO(), &snapshots.Items[i]) //nolint:errcheck
			}
		}

		if namespace != "" {
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			Expect(k8sClient.Delete(context.TODO(), ns)).To(
				Or(Succeed(), MatchError(ContainSubstring("not found"))))
		}
	})

	Describe("DryRun field behavior", func() {
		Context("When DryRun is set to true", func() {
			It("should accept DryRun=true", func() {
				vrg.Spec.DryRun = true
				vrg.Spec.Action = ramendrv1alpha1.VRGActionFailover
				Expect(k8sClient.Create(context.TODO(), vrg)).To(Succeed())

				// Verify VRG was created with DryRun=true
				Eventually(func() bool {
					err := apiReader.Get(context.TODO(), vrgNamespacedName, vrg)

					return err == nil && vrg.Spec.DryRun == true
				}, timeout, interval).Should(BeTrue())
			})
		})

		Context("When DryRun is set to false", func() {
			It("should accept DryRun=false", func() {
				vrg.Spec.DryRun = false
				Expect(k8sClient.Create(context.TODO(), vrg)).To(Succeed())

				// Verify VRG was created with DryRun=false
				Eventually(func() bool {
					err := apiReader.Get(context.TODO(), vrgNamespacedName, vrg)

					return err == nil && vrg.Spec.DryRun == false
				}, timeout, interval).Should(BeTrue())
			})
		})

		Context("When DryRun changes from true to false", func() {
			It("should update successfully", func() {
				vrg.Spec.DryRun = true
				vrg.Spec.Action = ramendrv1alpha1.VRGActionFailover
				Expect(k8sClient.Create(context.TODO(), vrg)).To(Succeed())

				// Update DryRun to false
				Eventually(func() error {
					if err := apiReader.Get(context.TODO(), vrgNamespacedName, vrg); err != nil {
						return err
					}

					vrg.Spec.DryRun = false

					return k8sClient.Update(context.TODO(), vrg)
				}, timeout, interval).Should(Succeed())

				// Verify update
				Eventually(func() bool {
					err := apiReader.Get(context.TODO(), vrgNamespacedName, vrg)

					return err == nil && vrg.Spec.DryRun == false
				}, timeout, interval).Should(BeTrue())
			})
		})
	})

	Describe("Action field with DryRun", func() {
		Context("When Action is Failover with DryRun=true", func() {
			It("should accept the configuration", func() {
				vrg.Spec.DryRun = true
				vrg.Spec.Action = ramendrv1alpha1.VRGActionFailover
				Expect(k8sClient.Create(context.TODO(), vrg)).To(Succeed())

				Eventually(func() bool {
					err := apiReader.Get(context.TODO(), vrgNamespacedName, vrg)

					return err == nil &&
						vrg.Spec.DryRun == true &&
						vrg.Spec.Action == ramendrv1alpha1.VRGActionFailover
				}, timeout, interval).Should(BeTrue())
			})
		})

		Context("When Action is Relocate with DryRun=true", func() {
			It("should accept the configuration", func() {
				vrg.Spec.DryRun = true
				vrg.Spec.Action = ramendrv1alpha1.VRGActionRelocate
				Expect(k8sClient.Create(context.TODO(), vrg)).To(Succeed())

				Eventually(func() bool {
					err := apiReader.Get(context.TODO(), vrgNamespacedName, vrg)

					return err == nil &&
						vrg.Spec.DryRun == true &&
						vrg.Spec.Action == ramendrv1alpha1.VRGActionRelocate
				}, timeout, interval).Should(BeTrue())
			})
		})
	})
})

// Made with Bob
