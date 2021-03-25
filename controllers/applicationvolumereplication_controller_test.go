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
	"github.com/ramendr/ramen/controllers"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
)

const subscriptionYAML = `apiVersion: apps.open-cluster-management.io/v1
kind: Subscription
metadata:
  name: my-subscription
  namespace: app-namespace
spec:
  channel: test/test-github-channel
  placement:
    placementRef:
      kind: PlacementRule
      name: sub-placement-rule
status:
  lastUpdateTime: '2021-02-17T17:28:14Z'
  message: 'remote-cluster:Active'
  phase: Propagated
  statuses:
    remote-cluster:
      packages:
        ggithubcom-ramendr-internal-ConfigMap-test-configmap:
          lastUpdateTime: '2021-02-16T23:09:03Z'
          phase: Subscribed`

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
)

// +kubebuilder:docs-gen:collapse=Imports

var _ = Describe("ApplicationVolumeReplication controller", func() {
	// Define utility constants for object names and testing timeouts/durations and intervals.
	const (
		ApplicationVolumeReplicationName          = "app-volume-replication-test"
		ApplicationVolumeReplicationNamespaceName = "app-namespace"
		ManagedClusterNamespaceName               = "remote-cluster"

		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	var (
		ManagedClusterNamespace *corev1.Namespace
		AppNamespace            *corev1.Namespace
	)

	BeforeEach(func() {
		ManagedClusterNamespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "remote-cluster"},
		}
		ctx := context.Background()
		err := k8sClient.Create(ctx, ManagedClusterNamespace)
		Expect(err).NotTo(HaveOccurred())

		AppNamespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: ApplicationVolumeReplicationNamespaceName},
		}
		err = k8sClient.Create(ctx, AppNamespace)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		ctx := context.Background()
		if ManagedClusterNamespace != nil {
			err := k8sClient.Delete(ctx, ManagedClusterNamespace)
			Expect(err).NotTo(HaveOccurred(), "failed to delete managed cluster namespace")
		}

		if AppNamespace != nil {
			err := k8sClient.Delete(ctx, AppNamespace)
			Expect(err).NotTo(HaveOccurred(), "failed to delete App namespace")
		}
	})

	Context("When creating ApplicationVolumeReplication", func() {
		It("Should create VolumeReplicationGroup CR embedded in a ManifestWork CR", func() {
			ctx := context.Background()

			By("Creating a fake subscriptioin....")
			// 1.0 create a fake Subscription CR
			subscription := &subv1.Subscription{}
			err := yaml.Unmarshal([]byte(subscriptionYAML), &subscription)
			Expect(err).NotTo(HaveOccurred())
			err = k8sClient.Create(context.TODO(), subscription)
			Expect(err).NotTo(HaveOccurred())

			// 1.1 update the Subscription status
			err = k8sClient.Status().Update(ctx, subscription)
			Expect(err).NotTo(HaveOccurred())
			subLookupKey := types.NamespacedName{Name: subscription.Name, Namespace: subscription.Namespace}
			createdSubscription := &subv1.Subscription{}
			err = k8sClient.Get(ctx, subLookupKey, createdSubscription)
			Expect(err).NotTo(HaveOccurred())
			Expect(createdSubscription.Status.Phase).Should(Equal(subv1.SubscriptionPhase("Propagated")))

			By("Creating Managed Clusters and Placement Rule....")
			// 3.0 create ManagedClusters
			for _, cl := range clusters {
				clinstance := cl.DeepCopy()

				err = k8sClient.Create(context.TODO(), clinstance)
				Expect(err).NotTo(HaveOccurred())

				// defer Expect(k8sClient.Delete(context.TODO(), clinstance)).NotTo(HaveOccurred())
			}

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
			// defer Expect(k8sClient.Delete(context.TODO(), placementRule)).NotTo(HaveOccurred())
			Expect(err).NotTo(HaveOccurred())

			By("Creating an AVR....")
			// 3.0 create AVR CR
			avr := &ramendrv1alpha1.ApplicationVolumeReplication{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ApplicationVolumeReplicationName,
					Namespace: ApplicationVolumeReplicationNamespaceName,
				},
				Spec: ramendrv1alpha1.ApplicationVolumeReplicationSpec{
					FailedCluster: "",
				},
			}
			Expect(k8sClient.Create(ctx, avr)).Should(Succeed())

			// 4.0 Get the VRG Roles ManifestWork. The work is created per managed cluster in the AVR reconciler
			vrgManifestLookupKey := types.NamespacedName{
				Name:      "ramendr-vrg-roles",
				Namespace: ManagedClusterNamespaceName,
			}
			createdVRGRolesManifest := &ocmworkv1.ManifestWork{}

			By("Waiting for VRG roles ManifestWork creation....")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, vrgManifestLookupKey, createdVRGRolesManifest)

				return err == nil
			}, timeout, interval).Should(BeTrue())

			Expect(len(createdVRGRolesManifest.Spec.Workload.Manifests)).To(Equal(2))

			vrgClusterRoleManifest := createdVRGRolesManifest.Spec.Workload.Manifests[0]
			Expect(vrgClusterRoleManifest).ToNot(BeNil())
			vrgClusterRole := &rbacv1.ClusterRole{}
			err = yaml.Unmarshal(vrgClusterRoleManifest.RawExtension.Raw, &vrgClusterRole)
			Expect(err).NotTo(HaveOccurred())

			vrgClusterRoleBindingManifest := createdVRGRolesManifest.Spec.Workload.Manifests[1]
			Expect(vrgClusterRoleBindingManifest).ToNot(BeNil())
			vrgClusterRoleBinding := &rbacv1.ClusterRoleBinding{}
			err = yaml.Unmarshal(vrgClusterRoleManifest.RawExtension.Raw, &vrgClusterRoleBinding)
			Expect(err).NotTo(HaveOccurred())

			// 5.0 Get the ManifestWork CR. The CR is created in the AVR Reconciler
			manifestLookupKey := types.NamespacedName{
				Name:      fmt.Sprintf(controllers.ManifestWorkNameFormat, subscription.Name, subscription.Namespace),
				Namespace: ManagedClusterNamespaceName,
			}
			createdManifest := &ocmworkv1.ManifestWork{}

			By("Waiting for ManifestWork creation....")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, manifestLookupKey, createdManifest)

				return err == nil
			}, timeout, interval).Should(BeTrue())

			// 5.1 verify that VRG CR has been created and added to the ManifestWork
			Expect(len(createdManifest.Spec.Workload.Manifests)).To(Equal(1))
			vrgClientManifest := createdManifest.Spec.Workload.Manifests[0]
			Expect(vrgClientManifest).ToNot(BeNil())
			vrg := &ramendrv1alpha1.VolumeReplicationGroup{}
			err = yaml.Unmarshal(vrgClientManifest.RawExtension.Raw, &vrg)
			Expect(err).NotTo(HaveOccurred())
			Expect(vrg.Name).Should(Equal(subscription.Name))

			By("Waiting for AVR status to be updated....")
			// 6.0 retrieve the updated AVR CR. It should have the status updated on success
			avrLookupKey := types.NamespacedName{
				Name:      ApplicationVolumeReplicationName,
				Namespace: ApplicationVolumeReplicationNamespaceName,
			}
			updatedAVR := &ramendrv1alpha1.ApplicationVolumeReplication{}

			Eventually(func() bool {
				err := k8sClient.Get(ctx, avrLookupKey, updatedAVR)

				return err == nil
			}, timeout, interval).Should(BeTrue())

			// 7.0 check that the home and peer clusters have been selected.
			Expect(updatedAVR.Status.Decisions["my-subscription"].HomeCluster).Should(Equal("remote-cluster"))
			Expect(updatedAVR.Status.Decisions["my-subscription"].PeerCluster).Should(Equal("local-cluster"))
		})
	})
})

var _ = Describe("isManifestInAppliedState", func() {
	timeOld := time.Now().Local()
	timeMostRecent := timeOld.Add(time.Second)

	Describe("single timestamp", func() {
		// test 1: only condition contains 'Applied'
		It("'Applied' present", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:               "Applied",
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
						},
					},
				},
			}

			Expect(controllers.IsManifestInAppliedState(mw)).To(Equal(true))
		})

		// test 2: only condition does not contain 'Applied'
		It("'Applied' not present", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:               "Processing",
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
						},
					},
				},
			}

			Expect(controllers.IsManifestInAppliedState(mw)).To(Equal(false))
		})
	})

	Describe("multiple timestamps", func() {
		// test 1: multiple timestamps, "Applied" is most recent, no equal timestamps
		It("no duplicates, 'Applied' is most recent", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:               "Applied",
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
						},
						{
							Type:               "Processing",
							LastTransitionTime: metav1.Time{Time: timeOld},
						},
					},
				},
			}

			Expect(controllers.IsManifestInAppliedState(mw)).To(Equal(true))
		})

		// test 2: multiple timestamps, "Applied" is NOT most recent
		It("no duplicates, 'Applied' is not most recent", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:               "Processing",
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
						},
						{
							Type:               "Applied",
							LastTransitionTime: metav1.Time{Time: timeOld},
						},
					},
				},
			}

			Expect(controllers.IsManifestInAppliedState(mw)).To(Equal(false))
		})

		// test 3: "Applied" is in most recent, includes equal timestamps
		It("with duplicates, 'Applied' is most recent", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:               "Applied",
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
						},
						{
							Type:               "Processing",
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
						},
						{
							Type:               "Processing",
							LastTransitionTime: metav1.Time{Time: timeOld},
						},
					},
				},
			}

			Expect(controllers.IsManifestInAppliedState(mw)).To(Equal(true))
		})

		// test 4: "Applied" is not most recent, but includes duplicate timestamps
		It("with duplicates, 'Applied' is not most recent", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:               "Available",
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
						},
						{
							Type:               "Processing",
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
						},
						{
							Type:               "Applied",
							LastTransitionTime: metav1.Time{Time: timeOld},
						},
					},
				},
			}

			Expect(controllers.IsManifestInAppliedState(mw)).To(Equal(false))
		})

		// test 5: error check - valid manifest with missing conditions
		It("manifest missing conditions", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						// empty
					},
				},
			}

			Expect(controllers.IsManifestInAppliedState(mw)).To(Equal(false))
		})
	})
})
