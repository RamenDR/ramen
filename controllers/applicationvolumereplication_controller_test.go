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
	"sigs.k8s.io/controller-runtime/pkg/client"

	spokeClusterV1 "github.com/open-cluster-management/api/cluster/v1"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	plrv1 "github.com/open-cluster-management/multicloud-operators-placementrule/pkg/apis/apps/v1"
	subv1 "github.com/open-cluster-management/multicloud-operators-subscription/pkg/apis/apps/v1"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
)

const (
	ApplicationVolumeReplicationName          = "app-volume-replication-test"
	ApplicationVolumeReplicationNamespaceName = "app-namespace"
	EastManagedCluster                        = "east-cluster"
	WestManagedCluster                        = "west-cluster"

	timeout  = time.Second * 10
	interval = time.Millisecond * 250
)

var (
	localCluster = &spokeClusterV1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: WestManagedCluster,
			Labels: map[string]string{
				"name": WestManagedCluster,
				"key1": "value1",
			},
		},
	}
	remoteCluster = &spokeClusterV1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: EastManagedCluster,
			Labels: map[string]string{
				"name": EastManagedCluster,
				"key1": "value1",
			},
		},
	}

	clusters = []*spokeClusterV1.ManagedCluster{localCluster, remoteCluster}

	eastManagedClusterNamespace = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: EastManagedCluster},
	}

	westManagedClusterNamespace = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: WestManagedCluster},
	}

	appNamespace = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ApplicationVolumeReplicationNamespaceName},
	}
)

type FakeS3StoreWrapper struct{}

func (s *FakeS3StoreWrapper) DownloadPVs(ctx context.Context, r client.Reader,
	s3Endpoint string, s3SecretName types.NamespacedName,
	callerTag string, s3Bucket string) ([]corev1.PersistentVolume, error) {
	pv1 := corev1.PersistentVolume{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PersistentVolume",
			APIVersion: "ramendr.openshift.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "pv0001",
		},
		Spec: corev1.PersistentVolumeSpec{
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					VolumeHandle: "vol-id-1",
				},
			},
			ClaimRef: &corev1.ObjectReference{
				Name: "claim1",
			},
			StorageClassName: "sc-name",
		},
	}

	pv2 := corev1.PersistentVolume{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PersistentVolume",
			APIVersion: "ramendr.openshift.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "pv0002",
		},
		Spec: corev1.PersistentVolumeSpec{
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					VolumeHandle: "vol-id-1",
				},
			},
			ClaimRef: &corev1.ObjectReference{
				Name: "claim2",
			},
			StorageClassName: "sc-name",
		},
	}

	pvList := []corev1.PersistentVolume{}
	pvList = append(pvList, pv1, pv2)

	return pvList, nil
}

// +kubebuilder:docs-gen:collapse=Imports

var _ = Describe("ApplicationVolumeReplication Reconciler", func() {
	// Define utility constants for object names and testing timeouts/durations and intervals.
	Context("ApplicationVolumeReplication CR", func() {
		When("Creating ApplicationVolumeReplication CR for the first time", func() {
			It("The reconciler creates VolumeReplicationGroup CR embedded within a ManifestWork CR", func() {
				ctx := context.Background()
				By("Creating east cluster namespace and app namespace")
				Expect(k8sClient.Create(ctx, eastManagedClusterNamespace)).NotTo(HaveOccurred(),
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
						EastManagedCluster: &subv1.SubscriptionPerClusterStatus{
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
				avr := &ramendrv1alpha1.ApplicationVolumeReplication{
					ObjectMeta: metav1.ObjectMeta{
						Name:      ApplicationVolumeReplicationName,
						Namespace: ApplicationVolumeReplicationNamespaceName,
					},
					Spec: ramendrv1alpha1.ApplicationVolumeReplicationSpec{
						FailoverClusters: ramendrv1alpha1.FailoverClusterMap{},
						S3Endpoint:       "path/to/s3Endpoint",
						S3SecretName:     "SecretName",
					},
				}
				Expect(k8sClient.Create(ctx, avr)).Should(Succeed())
				defer func() {
					Expect(k8sClient.Delete(context.TODO(), avr)).NotTo(HaveOccurred())
				}()

				// 4.0 Get the VRG Roles ManifestWork. The work is created per managed cluster in the AVR reconciler
				vrgManifestLookupKey := types.NamespacedName{
					Name:      "ramendr-vrg-roles",
					Namespace: EastManagedCluster,
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

				By("Get ManifestWork")
				manifestLookupKey := types.NamespacedName{
					Name:      fmt.Sprintf("%s-%s-%s-mw", subscription.Name, subscription.Namespace, "vrg"),
					Namespace: EastManagedCluster,
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
				Expect(updatedAVR.Status.Decisions["subscription-1"].HomeCluster).Should(Equal(EastManagedCluster))
				Expect(updatedAVR.Status.Decisions["subscription-1"].PeerCluster).Should(Equal(WestManagedCluster))
			})
		})
	})

	Context("ApplicationVolumeReplication Reconciler", func() {
		When("Subscription is paused", func() {
			It("Should failover", func() {
				ctx := context.Background()
				By("Creating cluster namespace")
				Expect(k8sClient.Create(ctx, westManagedClusterNamespace)).NotTo(HaveOccurred(),
					"failed to create managed cluster namespace")

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
						WestManagedCluster: &subv1.SubscriptionPerClusterStatus{
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

				By("Creating AVR")
				avr := &ramendrv1alpha1.ApplicationVolumeReplication{
					ObjectMeta: metav1.ObjectMeta{
						Name:      ApplicationVolumeReplicationName,
						Namespace: ApplicationVolumeReplicationNamespaceName,
					},
					Spec: ramendrv1alpha1.ApplicationVolumeReplicationSpec{
						FailoverClusters: ramendrv1alpha1.FailoverClusterMap{"subscription-2": WestManagedCluster},
						S3Endpoint:       "path/to/s3Endpoint",
						S3SecretName:     "SecretName",
					},
				}
				Expect(k8sClient.Create(ctx, avr)).Should(Succeed())
				defer func() {
					Expect(k8sClient.Delete(context.TODO(), avr)).NotTo(HaveOccurred())
				}()

				By("Creating ManifestWork")
				manifestLookupKey := types.NamespacedName{
					Name:      fmt.Sprintf("%s-%s-%s-mw", subscription.Name, subscription.Namespace, "pv"),
					Namespace: WestManagedCluster,
				}
				createdManifest := &ocmworkv1.ManifestWork{}

				Eventually(func() bool {
					err := k8sClient.Get(ctx, manifestLookupKey, createdManifest)

					return err == nil
				}, timeout, interval).Should(BeTrue(), "failed to wait for manifest creation")

				defer func() {
					Expect(k8sClient.Delete(context.TODO(), createdManifest)).NotTo(HaveOccurred())
				}()

				// 5.1 verify that PVs have been created and added to the ManifestWork
				Expect(len(createdManifest.Spec.Workload.Manifests)).To(Equal(2))
				pvClientManifest1 := createdManifest.Spec.Workload.Manifests[0]
				Expect(pvClientManifest1).ToNot(BeNil())
				pv1 := &corev1.PersistentVolume{}
				err = yaml.Unmarshal(pvClientManifest1.RawExtension.Raw, &pv1)
				Expect(err).NotTo(HaveOccurred())
				Expect(pv1.Name).Should(Equal("pv0001"))

				pvClientManifest2 := createdManifest.Spec.Workload.Manifests[1]
				Expect(pvClientManifest2).ToNot(BeNil())
				pv2 := &corev1.PersistentVolume{}
				err = yaml.Unmarshal(pvClientManifest2.RawExtension.Raw, &pv2)
				Expect(err).NotTo(HaveOccurred())
				Expect(pv2.Name).Should(Equal("pv0002"))

				// 5.2 update the status to Applied
				timeOld := time.Now().Local()
				timeMostRecent := timeOld.Add(time.Second)
				pvManifestStatus := ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:               ocmworkv1.WorkApplied,
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
							Status:             metav1.ConditionTrue,
							Reason:             "test",
						},
					},
				}
				createdManifest.Status = pvManifestStatus
				err = k8sClient.Status().Update(ctx, createdManifest)
				Expect(err).NotTo(HaveOccurred())

				Eventually(func() bool {
					err := k8sClient.Get(ctx, manifestLookupKey, createdManifest)

					return err == nil && len(createdManifest.Status.Conditions) != 0
				}, timeout, interval).Should(BeTrue(), "failed to wait for manifest creation")

				subLookupKey := types.NamespacedName{
					Name:      "subscription-2",
					Namespace: "app-namespace",
				}
				updatedSub := &subv1.Subscription{}
				Eventually(func() bool {
					err := k8sClient.Get(ctx, subLookupKey, updatedSub)

					return err == nil &&
						updatedSub.GetLabels()[subv1.LabelSubscriptionPause] == "false"
				}, timeout, interval).Should(BeTrue(), "failed to wait for subscription update")

				labels := updatedSub.GetLabels()
				str := fmt.Sprintf("now the label is %v", updatedSub)
				By(str)
				Expect(labels["ramendr"]).To(Equal("protected"))
				Expect(labels[subv1.LabelSubscriptionPause]).To(Equal("false"))
				By("Updating AVR")
				avrLookupKey := types.NamespacedName{
					Name:      ApplicationVolumeReplicationName,
					Namespace: ApplicationVolumeReplicationNamespaceName,
				}
				updatedAVR := &ramendrv1alpha1.ApplicationVolumeReplication{}

				Eventually(func() bool {
					err := k8sClient.Get(ctx, avrLookupKey, updatedAVR)

					return err == nil && updatedAVR.Status.Decisions != nil &&
						updatedAVR.Status.Decisions["subscription-2"].HomeCluster == WestManagedCluster
				}, timeout, interval).Should(BeTrue(), "failed to wait for updated AVR")

				// updatedAVR.Spec.FailoverClusters["test"] = "fake"
				// err = k8sClient.Update(ctx, updatedAVR)
				// Expect(err).NotTo(HaveOccurred())

				// Eventually(func() bool {
				// 	err := k8sClient.Get(ctx, avrLookupKey, updatedAVR)

				// 	return err == nil
				// }, timeout, interval).Should(BeTrue(), "failed to wait for updated AVR")

				Expect(updatedAVR.Status.Decisions["subscription-2"].HomeCluster).Should(Equal(WestManagedCluster))
				Expect(updatedAVR.Status.Decisions["subscription-2"].PeerCluster).Should(Equal(EastManagedCluster))
			})
		})
	})
})

// TODO: roll into single AVR block. Requires some refactoring to work with existing BeforeEach check.
var _ = Describe("IsManifestInAppliedState checks ManifestWork", func() {
	Context("with single timestamp", func() {
		timeOld := time.Now().Local()
		timeMostRecent := timeOld.Add(time.Second)

		It("'Applied' present, Status False", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:               ocmworkv1.WorkApplied,
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
							Status:             metav1.ConditionFalse,
						},
					},
				},
			}

			Expect(controllers.IsManifestInAppliedState(mw)).To(Equal(false))
		})

		It("'Applied' present, Status true", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:               ocmworkv1.WorkApplied,
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
							Status:             metav1.ConditionTrue,
						},
					},
				},
			}

			Expect(controllers.IsManifestInAppliedState(mw)).To(Equal(true))
		})

		It("'Applied' not present", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:               ocmworkv1.WorkProgressing,
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
						},
					},
				},
			}

			Expect(controllers.IsManifestInAppliedState(mw)).To(Equal(false))
		})
	})

	Context("with multiple timestamps", func() {
		timeOld := time.Now().Local()
		timeMostRecent := timeOld.Add(time.Second)

		It("no duplicate timestamps, 'Applied' is most recent", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:               ocmworkv1.WorkApplied,
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
							Status:             metav1.ConditionTrue,
						},
						{
							Type:               ocmworkv1.WorkProgressing,
							LastTransitionTime: metav1.Time{Time: timeOld},
						},
					},
				},
			}

			Expect(controllers.IsManifestInAppliedState(mw)).To(Equal(true))
		})

		It("no duplicates timestamps, 'Applied' is not most recent", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:               ocmworkv1.WorkProgressing,
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
						},
						{
							Type:               ocmworkv1.WorkApplied,
							LastTransitionTime: metav1.Time{Time: timeOld},
						},
					},
				},
			}

			Expect(controllers.IsManifestInAppliedState(mw)).To(Equal(false))
		})

		It("with duplicate timestamps, 'Applied' is most recent", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:               ocmworkv1.WorkApplied,
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
							Status:             metav1.ConditionTrue,
						},
						{
							Type:               ocmworkv1.WorkProgressing,
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
							Status:             metav1.ConditionUnknown,
						},
						{
							Type:               ocmworkv1.WorkProgressing,
							LastTransitionTime: metav1.Time{Time: timeOld},
						},
					},
				},
			}

			Expect(controllers.IsManifestInAppliedState(mw)).To(Equal(true))
		})

		It("with duplicate timestamps, 'Applied' is not most recent", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:               ocmworkv1.WorkAvailable,
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
						},
						{
							Type:               ocmworkv1.WorkProgressing,
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
						},
						{
							Type:               ocmworkv1.WorkApplied,
							LastTransitionTime: metav1.Time{Time: timeOld},
						},
					},
				},
			}

			Expect(controllers.IsManifestInAppliedState(mw)).To(Equal(false))
		})

		It("duplicate timestamps with Degraded and Applied status", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:               ocmworkv1.WorkApplied,
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
						},
						{
							Type:               ocmworkv1.WorkDegraded,
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
						},
					},
				},
			}

			Expect(controllers.IsManifestInAppliedState(mw)).To(Equal(false))
		})
	})

	Context("with no timestamps", func() {
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
