// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package volsync_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metrics "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	rmnutil "github.com/ramendr/ramen/internal/controller/util"
	"github.com/ramendr/ramen/internal/controller/volsync"
	ramenmanager "github.com/ramendr/ramen/internal/manager"
)

var _ = Describe("Secretgen", func() {
	var (
		testNamespace *corev1.Namespace
		owner         metav1.Object
	)

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

	Describe("Repair existing volsync rsync secret metadata", func() {
		const existingSecretName = "test-secret-existing"

		var (
			existingSecret *corev1.Secret
			hubClient      client.Client
			cancelManager  context.CancelFunc
		)

		BeforeEach(func() {
			hubManager, err := ramenmanager.NewManager(testEnv.Config, ctrl.Options{
				Scheme:  scheme.Scheme,
				Metrics: metrics.Options{BindAddress: "0"},
			}, ramendrv1alpha1.DRHubType)
			Expect(err).NotTo(HaveOccurred())

			var managerCtx context.Context

			managerCtx, cancelManager = context.WithCancel(ctx)

			go func() {
				defer GinkgoRecover()

				Expect(hubManager.Start(managerCtx)).To(Succeed())
			}()

			Expect(hubManager.GetCache().WaitForCacheSync(managerCtx)).To(BeTrue())
			hubClient = hubManager.GetClient()

			// Old owner of the existing secret
			oldOwner := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "dummycm-old-owner-",
					Namespace:    testNamespace.GetName(),
				},
			}
			Expect(hubClient.Create(ctx, oldOwner)).To(Succeed())

			// Existing secret without Ramen labels and with an old owner reference.
			// It has a non-Ramen label, which must be preserved by the repair.
			existingSecret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      existingSecretName,
					Namespace: testNamespace.GetName(),
					Labels:    map[string]string{"app": "existing"},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "v1",
							Kind:       "ConfigMap",
							Name:       oldOwner.GetName(),
							UID:        oldOwner.GetUID(),
							Controller: ptr.To(true),
						},
					},
				},
				StringData: map[string]string{
					"psk.txt": "volsyncramen:existing",
				},
			}
			Expect(hubClient.Create(ctx, existingSecret)).To(Succeed())
		})

		AfterEach(func() {
			cancelManager()
		})

		It("Should add the Ramen labels and set the current owner reference", func() {
			secret, err := volsync.ReconcileVolSyncReplicationSecret(ctx, hubClient, owner,
				existingSecretName, testNamespace.GetName(), logger)
			Expect(err).NotTo(HaveOccurred())

			// Existing PSK must be preserved and returned
			Expect(string(secret.Data["psk.txt"])).To(Equal("volsyncramen:existing"))

			// Ramen labels must be added to the existing secret.
			Expect(secret.GetLabels()).To(HaveKeyWithValue(rmnutil.CreatedByRamenLabel, "true"))
			Expect(secret.GetLabels()).To(HaveKeyWithValue(rmnutil.OCMBackupLabelKey, rmnutil.OCMBackupLabelValue))
			Expect(secret.GetLabels()).To(HaveKeyWithValue("app", "existing"))

			stored := &corev1.Secret{}

			// Use hubClient, not k8sClient. k8sClient reads the old secret from the API server
			// immediately. hubClient reads from the filtered cache, which returns NotFound until
			// it receives the ADD event for the relabeled secret. Eventually waits for that event.
			Eventually(func() error {
				return hubClient.Get(ctx, client.ObjectKeyFromObject(existingSecret), stored)
			}, maxWait, interval).Should(Succeed())

			Expect(string(stored.Data["psk.txt"])).To(Equal("volsyncramen:existing"))
			Expect(stored.GetLabels()).To(HaveKeyWithValue(rmnutil.CreatedByRamenLabel, "true"))
			Expect(stored.GetLabels()).To(HaveKeyWithValue(rmnutil.OCMBackupLabelKey, rmnutil.OCMBackupLabelValue))
			Expect(stored.GetLabels()).To(HaveKeyWithValue("app", "existing"))

			// The stale owner must be replaced by the current controller owner.
			Expect(stored.GetOwnerReferences()).To(HaveLen(1))
			Expect(stored.GetOwnerReferences()[0].Name).To(Equal(owner.GetName()))
			Expect(stored.GetOwnerReferences()[0].Controller).To(HaveValue(BeTrue()))
		})
	})
})
