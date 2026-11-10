// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package volsync_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	rmnutil "github.com/ramendr/ramen/internal/controller/util"
	"github.com/ramendr/ramen/internal/controller/volsync"
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

	Describe("Reconcile volsync rsync secret when the secret is not visible in the cache", func() {
		const hiddenSecretName = "test-secret-hidden"

		var (
			existingSecret *corev1.Secret
			filteredClient client.Client
			cancelCache    context.CancelFunc
		)

		BeforeEach(func() {
			// Client using a cache filtered like the ramen operator cache: only secrets with the created-by-ramen
			// label are cached. The suite client is not filtered since other tests use unlabelled secrets.
			filteredCache, err := cache.New(testEnv.Config, cache.Options{
				Scheme: scheme.Scheme,
				ByObject: map[client.Object]cache.ByObject{
					&corev1.Secret{}: {Label: labels.SelectorFromSet(labels.Set{rmnutil.CreatedByRamenLabel: "true"})},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			var cacheCtx context.Context

			cacheCtx, cancelCache = context.WithCancel(ctx)

			go func() {
				defer GinkgoRecover()

				Expect(filteredCache.Start(cacheCtx)).To(Succeed())
			}()

			Expect(filteredCache.WaitForCacheSync(cacheCtx)).To(BeTrue())

			filteredClient, err = client.New(testEnv.Config, client.Options{
				Scheme: scheme.Scheme,
				Cache:  &client.CacheOptions{Reader: filteredCache},
			})
			Expect(err).NotTo(HaveOccurred())

			// Old owner of the existing secret
			oldOwner := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "dummycm-old-owner-",
					Namespace:    testNamespace.GetName(),
				},
			}
			Expect(k8sClient.Create(ctx, oldOwner)).To(Succeed())

			// Existing secret without ramen labels, as if filtered out of the cache
			existingSecret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      hiddenSecretName,
					Namespace: testNamespace.GetName(),
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
			Expect(k8sClient.Create(ctx, existingSecret)).To(Succeed())
		})

		AfterEach(func() {
			cancelCache()
		})

		It("Should label the existing secret and return its data", func() {
			// The secret is not labeled, so it is hidden from the filtered cache
			hidden := &corev1.Secret{}
			err := filteredClient.Get(ctx, client.ObjectKeyFromObject(existingSecret), hidden)
			Expect(kerrors.IsNotFound(err)).To(BeTrue())

			secret, err := volsync.ReconcileVolSyncReplicationSecret(ctx, filteredClient, owner,
				hiddenSecretName, testNamespace.GetName(), logger)
			Expect(err).NotTo(HaveOccurred())

			// Existing PSK must be preserved and returned
			Expect(string(secret.Data["psk.txt"])).To(Equal("volsyncramen:existing"))

			// Labels must be added so the secret enters the filtered cache
			Expect(secret.GetLabels()).To(HaveKeyWithValue(rmnutil.CreatedByRamenLabel, "true"))
			Expect(secret.GetLabels()).To(HaveKeyWithValue(rmnutil.OCMBackupLabelKey, rmnutil.OCMBackupLabelValue))

			// The labeled secret must now be visible in the filtered cache
			stored := &corev1.Secret{}

			Eventually(func() error {
				return filteredClient.Get(ctx, client.ObjectKeyFromObject(existingSecret), stored)
			}, maxWait, interval).Should(Succeed())

			Expect(string(stored.Data["psk.txt"])).To(Equal("volsyncramen:existing"))
			Expect(stored.GetLabels()).To(HaveKeyWithValue(rmnutil.OCMBackupLabelKey, rmnutil.OCMBackupLabelValue))

			// The old owner must be replaced by the current owner as the controller of the existing secret
			Expect(stored.GetOwnerReferences()).To(HaveLen(1))
			Expect(stored.GetOwnerReferences()[0].Name).To(Equal(owner.GetName()))
			Expect(stored.GetOwnerReferences()[0].Controller).To(HaveValue(BeTrue()))
		})
	})
})
