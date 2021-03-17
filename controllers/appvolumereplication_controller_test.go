/*
Copyright 2021 The RamenDR authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers_test

import (
	"context"
	"fmt"
	"time"

	"github.com/ghodss/yaml"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	spokeClusterV1 "github.com/open-cluster-management/api/cluster/v1"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	plrv1 "github.com/open-cluster-management/multicloud-operators-placementrule/pkg/apis/apps/v1"
	subv1 "github.com/open-cluster-management/multicloud-operators-subscription/pkg/apis/apps/v1"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	// "k8s.io/apimachinery/pkg/api/errors"
)

// const subscriptionYAML = `apiVersion: apps.open-cluster-management.io/v1
// kind: Subscription
// metadata:
//   name: my-subscription
//   namespace: app-namespace
// spec:
//   channel: test/test-github-channel
//   placement:
//     placementRef:
//       kind: PlacementRule
//       name: sub-placement-rule
// status:
//   lastUpdateTime: '2021-02-17T17:28:14Z'
//   message: 'remote-cluster:Active'
//   phase: Propagated
//   statuses:
//     remote-cluster:
//       packages:
//         ggithubcom-ramendr-internal-ConfigMap-test-configmap:
//           lastUpdateTime: '2021-02-16T23:09:03Z'
//           phase: Subscribed`

// const subscriptionPausedNoStatusYAML = `apiVersion: apps.open-cluster-management.io/v1
// kind: Subscription
// metadata:
//   name: my-paused-subscription
//   namespace: sub-paused-namespace
//   labels:
//     ramendr: protected
//     subscription-pause: true
// spec:
//   channel: test/test-github-channel
//   placement:
//     placementRef:
//       kind: PlacementRule
//       name: sub-placement-rule`

// const subscriptionPausedWithStatusYAML = `apiVersion: apps.open-cluster-management.io/v1
// kind: Subscription
// metadata:
//   name: my-subscription
//   namespace: app-namespace
//   labels:
// 	ramendr: protected
// 	subscription-pause: true
// spec:
//   channel: test/test-github-channel
//   placement:
//     placementRef:
//       kind: PlacementRule
//       name: sub-placement-rule
// status:
//   lastUpdateTime: '2021-02-17T17:28:14Z'
//   message: 'remote-cluster:Active'
//   phase: Propagated
//   statuses:
//     remote-cluster:
//       packages:
//         ggithubcom-ramendr-internal-ConfigMap-test-configmap:
//           lastUpdateTime: '2021-02-16T23:09:03Z'
//           phase: Subscribed`

const (
	AppVolumeReplicationName          = "app-volume-replication-test"
	AppVolumeReplicationNamespaceName = "app-namespace"
	ManagedClusterNamespaceName       = "remote-cluster"

	timeout  = time.Second * 10
	interval = time.Millisecond * 250
)

var (
	localCluster = &spokeClusterV1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: "local-cluster",
			Labels: map[string]string{
				"name": "local-cluster",
				"key1": "value1",
			},
		},
	}
	remoteCluster = &spokeClusterV1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: "remote-cluster",
			Labels: map[string]string{
				"name": "remote-cluster",
				"key1": "value1",
			},
		},
	}

	clusters = []*spokeClusterV1.ManagedCluster{localCluster, remoteCluster}

	managedClusterNamespace = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "remote-cluster"},
	}

	appNamespace = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: AppVolumeReplicationNamespaceName},
	}
)

// +kubebuilder:docs-gen:collapse=Imports

var _ = Describe("AppVolumeReplication Reconciler", func() {
	// Define utility constants for object names and testing timeouts/durations and intervals.
	Context("AppVolumeReplication CR", func() {
		When("Creating AppVolumeReplication CR for the first time", func() {
			It("The reconciler creates VolumeReplicationGroup CR embedded within a ManifestWork CR", func() {
				ctx := context.Background()

				Expect(k8sClient.Create(ctx, managedClusterNamespace)).NotTo(HaveOccurred(),
					"failed to create managed cluster namespace")
				Expect(k8sClient.Create(ctx, appNamespace)).NotTo(HaveOccurred(), "failed to create app namespace")

				By("Creating a Subscription")
				subscription := &subv1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "subscription-1",
						Namespace: "app-namespace",
					},
					Spec: subv1.SubscriptionSpec{
						Channel: "test/test-github-channel",
						Placement: &plrv1.Placement{
							PlacementRef: &corev1.ObjectReference{
								Name: "sub-placement-rule",
								Kind: "PlacementRule",
							},
						},
					},
				}

				err := k8sClient.Create(context.TODO(), subscription)
				Expect(err).NotTo(HaveOccurred())
				defer func() {
					Expect(k8sClient.Delete(context.TODO(), subscription)).NotTo(HaveOccurred())
				}()

				subStatus := subv1.SubscriptionStatus{
					Phase:          "Propagated",
					Reason:         "",
					LastUpdateTime: metav1.Now(),
					Statuses: subv1.SubscriptionClusterStatusMap{
						"remote-cluster": &subv1.SubscriptionPerClusterStatus{
							SubscriptionPackageStatus: map[string]*subv1.SubscriptionUnitStatus{
								"packages": {
									Phase: subv1.SubscriptionSubscribed,
								},
							},
						},
					},
				}

				subscription.Status = subStatus
				err = k8sClient.Status().Update(ctx, subscription)
				Expect(err).NotTo(HaveOccurred())

				subLookupKey := types.NamespacedName{Name: subscription.Name, Namespace: subscription.Namespace}
				createdSubscription := &subv1.Subscription{}
				err = k8sClient.Get(ctx, subLookupKey, createdSubscription)
				Expect(err).NotTo(HaveOccurred())
				Expect(createdSubscription.Status.Phase).Should(Equal(subv1.SubscriptionPhase("Propagated")))

				By("Creating Managed Clusters")
				for _, cl := range clusters {
					clinstance := cl.DeepCopy()

					err = k8sClient.Create(context.TODO(), clinstance)
					Expect(err).NotTo(HaveOccurred())

					defer func() {
						Expect(k8sClient.Delete(context.TODO(), clinstance)).NotTo(HaveOccurred())
					}()
				}

				By("Creating PlacementRule")
				namereq := metav1.LabelSelectorRequirement{}
				namereq.Key = "key1"
				namereq.Operator = metav1.LabelSelectorOpIn

				namereq.Values = []string{"value1"}
				labelSelector := &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{namereq},
				}

				placementRule := &plrv1.PlacementRule{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub-placement-rule",
						Namespace: "app-namespace",
					},
					Spec: plrv1.PlacementRuleSpec{
						GenericPlacementFields: plrv1.GenericPlacementFields{
							ClusterSelector: labelSelector,
						},
					},
				}

				err = k8sClient.Create(context.TODO(), placementRule)
				Expect(err).NotTo(HaveOccurred())
				defer func() {
					Expect(k8sClient.Delete(context.TODO(), placementRule)).NotTo(HaveOccurred())
				}()

				By("Creating AVR CR")
				avr := &ramendrv1alpha1.AppVolumeReplication{
					ObjectMeta: metav1.ObjectMeta{
						Name:      AppVolumeReplicationName,
						Namespace: AppVolumeReplicationNamespaceName,
					},
					Spec: ramendrv1alpha1.AppVolumeReplicationSpec{
						FailoverClusters: make(map[string]string),
					},
				}
				Expect(k8sClient.Create(ctx, avr)).Should(Succeed())
				defer func() {
					Expect(k8sClient.Delete(context.TODO(), avr)).NotTo(HaveOccurred())
				}()

				By("AVR Creating ManifestWork")
				manifestLookupKey := types.NamespacedName{
					Name:      "remote-cluster-vrg-manifestwork",
					Namespace: ManagedClusterNamespaceName,
				}
				createdManifest := &ocmworkv1.ManifestWork{}

				Eventually(func() bool {
					err := k8sClient.Get(ctx, manifestLookupKey, createdManifest)

					return err == nil
				}, timeout, interval).Should(BeTrue())

				defer func() {
					Expect(k8sClient.Delete(context.TODO(), createdManifest)).NotTo(HaveOccurred())
				}()

				// 5.1 verify that VRG CR has been created and added to the ManifestWork
				Expect(len(createdManifest.Spec.Workload.Manifests)).To(Equal(1))
				vrgClientManifest := createdManifest.Spec.Workload.Manifests[0]
				Expect(vrgClientManifest).ToNot(BeNil())
				vrg := &ramendrv1alpha1.VolumeReplicationGroup{}
				err = yaml.Unmarshal(vrgClientManifest.RawExtension.Raw, &vrg)
				Expect(err).NotTo(HaveOccurred())
				Expect(vrg.Name).Should(Equal(subscription.Name))

				By("Retrieving the updated AVR CR. It should have the status updated on success")
				avrLookupKey := types.NamespacedName{Name: AppVolumeReplicationName, Namespace: AppVolumeReplicationNamespaceName}
				updatedAVR := &ramendrv1alpha1.AppVolumeReplication{}
				Eventually(func() bool {
					err := k8sClient.Get(ctx, avrLookupKey, updatedAVR)

					return err == nil
				}, timeout, interval).Should(BeTrue())

				// 7.0 check that the home and peer clusters have been selected.
				Expect(updatedAVR.Status.Decisions["subscription-1"].HomeCluster).Should(Equal("remote-cluster"))
				Expect(updatedAVR.Status.Decisions["subscription-1"].PeerCluster).Should(Equal("local-cluster"))
			})
		})
	})

	Context("AppVolumeReplication Reconciler", func() {
		When("Subscription is paused", func() {
			It("Should Unpause the subscription", func() {
				ctx := context.Background()
				By("Creating subscription")
				subscription := &subv1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "subscription-2",
						Namespace: "app-namespace",
						Labels: map[string]string{
							"ramendr":                    "protected",
							subv1.LabelSubscriptionPause: "true",
						},
					},
					Spec: subv1.SubscriptionSpec{
						Channel: "test/test-github-channel",
						Placement: &plrv1.Placement{
							PlacementRef: &corev1.ObjectReference{
								Name: "sub-placement-rule",
								Kind: "PlacementRule",
							},
						},
					},
				}

				err := k8sClient.Create(context.TODO(), subscription)
				Expect(err).NotTo(HaveOccurred())

				By("Updating subscription status")
				subStatus := subv1.SubscriptionStatus{
					Phase:          "Propagated",
					Reason:         "",
					LastUpdateTime: metav1.Now(),
					Statuses: subv1.SubscriptionClusterStatusMap{
						"remote-cluster": &subv1.SubscriptionPerClusterStatus{
							SubscriptionPackageStatus: map[string]*subv1.SubscriptionUnitStatus{
								"packages": {
									Phase: subv1.SubscriptionSubscribed,
								},
							},
						},
					},
				}

				subscription.Status = subStatus
				err = k8sClient.Status().Update(ctx, subscription)
				Expect(err).NotTo(HaveOccurred())

				By("Creating 2 managed clusters")
				for _, cl := range clusters {
					clinstance := cl.DeepCopy()

					err = k8sClient.Create(context.TODO(), clinstance)
					Expect(err).NotTo(HaveOccurred())

					defer func() {
						Expect(k8sClient.Delete(context.TODO(), clinstance)).NotTo(HaveOccurred())
					}()
				}

				By("Creating PlacementRule")
				namereq := metav1.LabelSelectorRequirement{}
				namereq.Key = "key1"
				namereq.Operator = metav1.LabelSelectorOpIn

				namereq.Values = []string{"value1"}
				labelSelector := &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{namereq},
				}

				placementRule := &plrv1.PlacementRule{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub-placement-rule",
						Namespace: "app-namespace",
					},
					Spec: plrv1.PlacementRuleSpec{
						GenericPlacementFields: plrv1.GenericPlacementFields{
							ClusterSelector: labelSelector,
						},
					},
				}

				err = k8sClient.Create(context.TODO(), placementRule)
				Expect(err).NotTo(HaveOccurred())
				defer func() {
					Expect(k8sClient.Delete(context.TODO(), placementRule)).NotTo(HaveOccurred())
				}()

				avr := &ramendrv1alpha1.AppVolumeReplication{
					ObjectMeta: metav1.ObjectMeta{
						Name:      AppVolumeReplicationName,
						Namespace: AppVolumeReplicationNamespaceName,
					},
					Spec: ramendrv1alpha1.AppVolumeReplicationSpec{
						FailoverClusters: make(map[string]string),
					},
				}
				Expect(k8sClient.Create(ctx, avr)).Should(Succeed())
				defer func() {
					Expect(k8sClient.Delete(context.TODO(), avr)).NotTo(HaveOccurred())
				}()

				manifestLookupKey := types.NamespacedName{
					Name:      "remote-cluster-vrg-manifestwork",
					Namespace: ManagedClusterNamespaceName,
				}
				createdManifest := &ocmworkv1.ManifestWork{}

				Eventually(func() bool {
					err := k8sClient.Get(ctx, manifestLookupKey, createdManifest)

					return err == nil
				}, timeout, interval).Should(BeTrue())

				subLookupKey := types.NamespacedName{
					Name:      "subscription-2",
					Namespace: "app-namespace",
				}
				updatedSub := &subv1.Subscription{}
				Eventually(func() bool {
					err := k8sClient.Get(ctx, subLookupKey, updatedSub)

					return err == nil
				}, timeout, interval).Should(BeTrue())

				labels := updatedSub.GetLabels()
				str := fmt.Sprintf("now the label is %v", updatedSub)
				By(str)
				Expect(labels["ramendr"]).To(Equal("protected"))
				Expect(labels[subv1.LabelSubscriptionPause]).To(Equal("false"))
			})
		})
	})
})
