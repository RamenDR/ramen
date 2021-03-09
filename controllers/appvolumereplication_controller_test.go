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
	"time"

	"github.com/ghodss/yaml"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	plrv1 "github.com/open-cluster-management/multicloud-operators-placementrule/pkg/apis/apps/v1"
	subv1 "github.com/open-cluster-management/multicloud-operators-subscription/pkg/apis/apps/v1"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
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

const subPlacementRuleYAML = `apiVersion: apps.open-cluster-management.io/v1
kind: PlacementRule
metadata:
  name: sub-placement-rule
  namespace: app-namespace
spec:
  clusterConditions:
    - status: 'True'
      type: ManagedClusterConditionAvailable
status:
  decisions:
    - clusterName: local-cluster
      clusterNamespace: local-cluster'
    - clusterName: remote-cluster
      clusterNamespace: remote-cluster`

const avrPlacementRuleYAML = `apiVersion: apps.open-cluster-management.io/v1
kind: PlacementRule
metadata:
  name: avr-placement-rule
  namespace: app-namespace
spec:
  clusterConditions:
    - status: 'True'
      type: ManagedClusterConditionAvailable
status:
  decisions:
    - clusterName: local-cluster
      clusterNamespace: local-cluster'
    - clusterName: remote-cluster
      clusterNamespace: remote-cluster`

// +kubebuilder:docs-gen:collapse=Imports

var _ = Describe("AppVolumeReplication controller", func() {
	// Define utility constants for object names and testing timeouts/durations and intervals.
	const (
		AppVolumeReplicationName          = "app-volume-replication-test"
		AppVolumeReplicationNamespaceName = "app-namespace"
		ManagedClusterNamespaceName       = "remote-cluster"

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
			ObjectMeta: metav1.ObjectMeta{Name: AppVolumeReplicationNamespaceName},
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

	Context("When creating AppVolumeReplication", func() {
		It("Should create VolumeReplicationGroup CR embedded in a ManifestWork CR", func() {
			By("By updating AppVolumeReplication status on success")
			ctx := context.Background()

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

			// 2.0 create Placement Rule CR used by the Subscription
			subPlacementRule := &plrv1.PlacementRule{}
			err = yaml.Unmarshal([]byte(subPlacementRuleYAML), &subPlacementRule)
			Expect(err).NotTo(HaveOccurred())
			err = k8sClient.Create(context.TODO(), subPlacementRule)
			Expect(err).NotTo(HaveOccurred())

			// 2.1 update Placement Rule status
			err = k8sClient.Status().Update(ctx, subPlacementRule)
			Expect(err).NotTo(HaveOccurred())

			// 3.0 create Placement Rule used by the AVR CR
			avrPlacementRule := &plrv1.PlacementRule{}
			err = yaml.Unmarshal([]byte(avrPlacementRuleYAML), &avrPlacementRule)
			Expect(err).NotTo(HaveOccurred())
			err = k8sClient.Create(context.TODO(), avrPlacementRule)
			Expect(err).NotTo(HaveOccurred())

			// 3.1 update Placement Rule status
			err = k8sClient.Status().Update(ctx, avrPlacementRule)
			Expect(err).NotTo(HaveOccurred())

			// 4.0 create AVR CR
			avr := &ramendrv1alpha1.AppVolumeReplication{
				ObjectMeta: metav1.ObjectMeta{
					Name:      AppVolumeReplicationName,
					Namespace: AppVolumeReplicationNamespaceName,
				},
				Spec: ramendrv1alpha1.AppVolumeReplicationSpec{
					Placement: &plrv1.Placement{
						PlacementRef: &corev1.ObjectReference{
							Kind: "PlacementRule",
							Name: "avr-placement-rule",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, avr)).Should(Succeed())

			// 5.0 Get the ManifestWork CR. The CR is created in the AVR Reconciler
			manifestLookupKey := types.NamespacedName{
				Name:      "remote-cluster-vrg-manifestwork",
				Namespace: ManagedClusterNamespaceName,
			}
			createdManifest := &ocmworkv1.ManifestWork{}

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
			Expect(vrg.Name).Should(Equal("app-volume-replication-test"))

			// 6.0 retrieve the updated AVR CR. It should have the status updated on success
			avrLookupKey := types.NamespacedName{Name: AppVolumeReplicationName, Namespace: AppVolumeReplicationNamespaceName}
			updatedAVR := &ramendrv1alpha1.AppVolumeReplication{}

			Eventually(func() bool {
				err := k8sClient.Get(ctx, avrLookupKey, updatedAVR)

				return err == nil
			}, timeout, interval).Should(BeTrue())

			// 7.0 check that the home and peer clusters have been selected.
			Expect(updatedAVR.Status.HomeCluster).Should(Equal("remote-cluster"))
			Expect(updatedAVR.Status.PeerCluster).Should(Equal("local-cluster"))
		})
	})
})
