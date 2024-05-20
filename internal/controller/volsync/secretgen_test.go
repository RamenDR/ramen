// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package volsync_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	rmnutil "github.com/ramendr/ramen/internal/controller/util"
	"github.com/ramendr/ramen/internal/controller/volsync"
)

var _ = Describe("Secretgen", func() {
	var testNamespace *corev1.Namespace
	var owner metav1.Object

	BeforeEach(func() {
		// Create namespace for test
		testNamespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "sg-test-",
			},
		}
		Expect(k8sClient.Create(ctx, testNamespace)).To(Succeed())
		Expect(testNamespace.GetName()).NotTo(BeEmpty())

		// Create dummy resource to be the "owner" of the generated secret
		// Using a configmap for now - in reality this owner resource will
		// be a DRPC
		ownerCm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "dummycm-owner-",
				Namespace:    testNamespace.GetName(),
			},
		}
		Expect(k8sClient.Create(ctx, ownerCm)).To(Succeed())
		Expect(ownerCm.GetName()).NotTo(BeEmpty())
		owner = ownerCm
	})

	AfterEach(func() {
		// All resources are namespaced, so this should clean it all up
		Expect(k8sClient.Delete(ctx, testNamespace)).To(Succeed())
	})

	Describe("Reconcile volsync rsync secret", func() {
		testSecretName := "test-secret-abc"

		JustBeforeEach(func() {
			testSecret, err := volsync.ReconcileVolSyncReplicationSecret(ctx, k8sClient, owner,
				testSecretName, testNamespace.GetName(), logger)
			Expect(err).NotTo(HaveOccurred())

			Expect(testSecret.GetName()).To(Equal(testSecretName))
			Expect(testSecret.GetNamespace()).To(Equal(testNamespace.GetName()))
		})

		Context("When the secret does not previously exist", func() {
			It("Should create a volsync rsync secret", func() {
				// Re-load secret to make sure it's been created properly
				newSecret := &corev1.Secret{}
				Eventually(func() error {
					return k8sClient.Get(ctx,
						types.NamespacedName{Name: testSecretName, Namespace: testNamespace.GetName()}, newSecret)
				}, maxWait, interval).Should(Succeed())

				// Expect the secret should be owned by owner
				Expect(ownerMatches(newSecret, owner.GetName(), "ConfigMap", true))

				// Expect that the secret includes a label utilized for hub backup.
				Expect(newSecret.GetLabels()[rmnutil.OCMBackupLabelKey]).Should(Equal(rmnutil.OCMBackupLabelValue))

				// Check secret data
				Expect(len(newSecret.Data)).To(Equal(1))

				tlsPskData, ok := newSecret.Data["psk.txt"]
				Expect(ok).To(BeTrue())

				tlsPsk := string(tlsPskData)
				Expect(tlsPsk).To(ContainSubstring("volsyncramen:"))
			})
		})

		Context("When the secret already exists", func() {
			var existingSecret *corev1.Secret
			BeforeEach(func() {
				existingSecret = &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      testSecretName,
						Namespace: testNamespace.GetName(),
					},
					StringData: map[string]string{
						"a":          "b",
						"anotherkey": "anothervalue",
					},
				}
				Expect(k8sClient.Create(ctx, existingSecret)).To(Succeed())

				// Make sure secret has been created, and in client cache
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKeyFromObject(existingSecret), existingSecret)
				}, maxWait, interval).Should(Succeed())
			})

			It("Should leave the existing secret unchanged", func() {
				// Re-load secret to make sure it's been created properly
				secret := &corev1.Secret{}
				Eventually(func() error {
					return k8sClient.Get(ctx,
						types.NamespacedName{Name: testSecretName, Namespace: testNamespace.GetName()}, secret)
				}, maxWait, interval).Should(Succeed())

				Expect(secret.Data).To(Equal(existingSecret.Data))
			})
		})
	})
})
