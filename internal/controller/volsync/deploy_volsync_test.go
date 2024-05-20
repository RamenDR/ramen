// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package volsync_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ramendr/ramen/internal/controller/volsync"
)

var _ = Describe("Deploy VolSync via ManagedClusterAddOn to managed clusters", func() {
	var testManagedClusterNamespace1 *corev1.Namespace
	var testManagedClusterName1 string

	var testManagedClusterNamespace2 *corev1.Namespace
	var testManagedClusterName2 string

	BeforeEach(func() {
		// Create namespaces for test - will be for the "managedclusters"
		testManagedClusterNamespace1 = createManagedClusterNamespace()
		testManagedClusterName1 = testManagedClusterNamespace1.GetName()

		testManagedClusterNamespace2 = createManagedClusterNamespace()
		testManagedClusterName2 = testManagedClusterNamespace2.GetName()
	})

	AfterEach(func() {
		// All resources are namespaced, so this should clean it all up
		Expect(k8sClient.Delete(ctx, testManagedClusterNamespace1)).To(Succeed())
		Expect(k8sClient.Delete(ctx, testManagedClusterNamespace2)).To(Succeed())
	})

	Context("When calling DeployVolSyncToClusters", func() {
		var cluster1ManagedClusterAddOn *unstructured.Unstructured
		var cluster2ManagedClusterAddOn *unstructured.Unstructured

		BeforeEach(func() {
			Expect(volsync.DeployVolSyncToCluster(ctx, k8sClient, testManagedClusterName1, logger)).To(Succeed())
			Expect(volsync.DeployVolSyncToCluster(ctx, k8sClient, testManagedClusterName2, logger)).To(Succeed())

			cluster1ManagedClusterAddOn = getVolSyncManagedClusterAddon(testManagedClusterName1)
			cluster2ManagedClusterAddOn = getVolSyncManagedClusterAddon(testManagedClusterName2)
		})

		It("Should create a volsync ManagedClusterAddOn for each managed cluster", func() {
			// Should be an addon for each in the managedcluster namespace with name "volsync"
			Expect(cluster1ManagedClusterAddOn.GetName()).To(Equal(volsync.VolsyncManagedClusterAddOnName))
			Expect(cluster1ManagedClusterAddOn.Object["spec"]).To(Equal(map[string]interface{}{}))

			Expect(cluster2ManagedClusterAddOn.GetName()).To(Equal(volsync.VolsyncManagedClusterAddOnName))
			Expect(cluster2ManagedClusterAddOn.Object["spec"]).To(Equal(map[string]interface{}{}))
		})

		Context("When a managedclusteraddon for volsync already exists", func() {
			// Outer beforeEach will have created a volsync managedcluster addon for each cluster
			// Now modify them and run DeployVolSyncToClusters() again
			BeforeEach(func() {
				cluster1ManagedClusterAddOn.SetAnnotations(map[string]string{
					"favorite-icecream": "chocolate",
				})
				Expect(k8sClient.Update(ctx, cluster1ManagedClusterAddOn)).To(Succeed())

				cluster2ManagedClusterAddOn.SetAnnotations(map[string]string{
					"favorite-icecream": "mango",
					"favorite-color":    "yellow",
				})
				Expect(k8sClient.Update(ctx, cluster2ManagedClusterAddOn)).To(Succeed())

				// Make sure the cache picks up the annotation updates
				Eventually(func() bool {
					err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster1ManagedClusterAddOn),
						cluster1ManagedClusterAddOn)
					if err != nil {
						return false
					}

					err2 := k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster2ManagedClusterAddOn),
						cluster2ManagedClusterAddOn)
					if err2 != nil {
						return false
					}

					return len(cluster1ManagedClusterAddOn.GetAnnotations()) == 1 &&
						len(cluster1ManagedClusterAddOn.GetAnnotations()) == 2
				})
			})

			It("Should not modify the volsync ManagedClusterAddOns", func() {
				Expect(volsync.DeployVolSyncToCluster(ctx, k8sClient, testManagedClusterName1, logger)).To(Succeed())
				Expect(volsync.DeployVolSyncToCluster(ctx, k8sClient, testManagedClusterName2, logger)).To(Succeed())

				cluster1McaoReloaded := getVolSyncManagedClusterAddon(testManagedClusterName1)
				Expect(cluster1McaoReloaded.Object["spec"]).To(Equal(map[string]interface{}{}))
				Expect(len(cluster1McaoReloaded.GetAnnotations())).To(Equal(1))

				cluster2McaoReloaded := getVolSyncManagedClusterAddon(testManagedClusterName2)
				Expect(cluster2McaoReloaded.Object["spec"]).To(Equal(map[string]interface{}{}))
				Expect(len(cluster2McaoReloaded.GetAnnotations())).To(Equal(2))
			})
		})
	})
})

func getVolSyncManagedClusterAddon(managedClusterName string) *unstructured.Unstructured {
	mcAddon := &unstructured.Unstructured{}

	mcAddon.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   volsync.ManagedClusterAddOnGroup,
		Version: volsync.ManagedClusterAddOnVersion,
		Kind:    volsync.ManagedClusterAddOnKind,
	})

	Eventually(func() error {
		return k8sClient.Get(ctx, client.ObjectKey{
			Namespace: managedClusterName,
			Name:      volsync.VolsyncManagedClusterAddOnName,
		}, mcAddon)
	}, maxWait, interval).Should(Succeed())

	return mcAddon
}

func createManagedClusterNamespace() *corev1.Namespace {
	// Create namespace for test - ns will be for the "managedcluster"
	testNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-mgd-cluster-ns-",
		},
	}
	Expect(k8sClient.Create(ctx, testNamespace)).To(Succeed())
	Expect(testNamespace.GetName()).NotTo(BeEmpty())

	Eventually(func() error {
		return k8sClient.Get(ctx, client.ObjectKeyFromObject(testNamespace), testNamespace)
	}, maxWait, interval).Should(Succeed())

	return testNamespace
}
