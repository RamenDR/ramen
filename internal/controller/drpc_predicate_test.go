// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	controllers "github.com/ramendr/ramen/internal/controller"
)

var _ = Describe("DRPCPredicateDRCluster", func() {
	var drClusterOld, drClusterNew *rmn.DRCluster
	var baseTime time.Time

	BeforeEach(func() {
		// Initialize the DRClusters to their defaults
		baseTime = time.Now()
		drClusterOld = &rmn.DRCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "drcluster1"},
			Spec:       rmn.DRClusterSpec{S3ProfileName: "unknown"},
			Status: rmn.DRClusterStatus{
				MaintenanceModes: []rmn.ClusterMaintenanceMode{
					{
						StorageProvisioner: "provisioner1",
						TargetID:           "id1",
						Conditions: []metav1.Condition{
							{
								Type:               string(rmn.MModeConditionFailoverActivated),
								Status:             metav1.ConditionTrue,
								LastTransitionTime: metav1.NewTime(baseTime),
								Reason:             string(rmn.MModeStateCompleted),
								Message:            "testing",
							},
						},
					},
					{
						StorageProvisioner: "provisioner1",
						TargetID:           "id2",
						Conditions: []metav1.Condition{
							{
								Type:               string(rmn.MModeConditionFailoverActivated),
								Status:             metav1.ConditionTrue,
								LastTransitionTime: metav1.NewTime(baseTime),
								Reason:             string(rmn.MModeStateCompleted),
								Message:            "testing",
							},
						},
					},
					{
						StorageProvisioner: "provisioner1",
						TargetID:           "id1",
						Conditions: []metav1.Condition{
							{
								Type:               string(rmn.MModeConditionFailoverActivated),
								Status:             metav1.ConditionTrue,
								LastTransitionTime: metav1.NewTime(baseTime),
								Reason:             string(rmn.MModeStateCompleted),
								Message:            "testing",
							},
						},
					},
				},
			},
		}

		drClusterNew = drClusterOld.DeepCopy()
	})

	Describe("Test DRCluster update predicate", func() {
		When("old and new have no maintenance mode changes", func() {
			It("returns DRCluster update of interest as false", func() {
				Expect(controllers.DRClusterUpdateOfInterest(drClusterOld, drClusterNew)).To(BeFalse())
			})
		})

		When("new has unrelated changes", func() {
			It("returns DRCluster update of interest as false", func() {
				drClusterNew.Spec.Region = "testing"
				Expect(controllers.DRClusterUpdateOfInterest(drClusterOld, drClusterNew)).To(BeFalse())
			})
		})

		When("new has no maintenance modes", func() {
			It("returns DRCluster update of interest as false", func() {
				drClusterNew.Status = rmn.DRClusterStatus{}
				Expect(controllers.DRClusterUpdateOfInterest(drClusterOld, drClusterNew)).To(BeFalse())
			})
		})

		When("new has no maintenance mode conditions", func() {
			It("returns DRCluster update of interest as false", func() {
				drClusterNew.Status = rmn.DRClusterStatus{
					MaintenanceModes: []rmn.ClusterMaintenanceMode{
						{
							StorageProvisioner: "provisioner1",
							TargetID:           "id1",
						},
					},
				}
				Expect(controllers.DRClusterUpdateOfInterest(drClusterOld, drClusterNew)).To(BeFalse())
			})
		})

		When("new has maintenance modes activated as false", func() {
			It("returns DRCluster update of interest as false", func() {
				drClusterNew.Status = rmn.DRClusterStatus{
					MaintenanceModes: []rmn.ClusterMaintenanceMode{
						{
							StorageProvisioner: "provisioner1",
							TargetID:           "id1",
							Conditions: []metav1.Condition{
								{
									Type:               string(rmn.MModeConditionFailoverActivated),
									Status:             metav1.ConditionFalse,
									LastTransitionTime: metav1.NewTime(baseTime),
									Reason:             string(rmn.MModeStateCompleted),
									Message:            "testing",
								},
							},
						},
					},
				}
				Expect(controllers.DRClusterUpdateOfInterest(drClusterOld, drClusterNew)).To(BeFalse())
			})
		})

		When("new has maintenance modes activated as unknown", func() {
			It("returns DRCluster update of interest as false", func() {
				drClusterNew.Status = rmn.DRClusterStatus{
					MaintenanceModes: []rmn.ClusterMaintenanceMode{
						{
							StorageProvisioner: "provisioner1",
							TargetID:           "id1",
							Conditions: []metav1.Condition{
								{
									Type:               string(rmn.MModeConditionFailoverActivated),
									Status:             metav1.ConditionUnknown,
									LastTransitionTime: metav1.NewTime(baseTime),
									Reason:             string(rmn.MModeStateCompleted),
									Message:            "testing",
								},
							},
						},
					},
				}
				Expect(controllers.DRClusterUpdateOfInterest(drClusterOld, drClusterNew)).To(BeFalse())
			})
		})

		When("new has unrelated maintenance mode condition update", func() {
			It("returns DRCluster update of interest as false", func() {
				for idx := range drClusterNew.Status.MaintenanceModes {
					// Prepend the new condition for the test
					drClusterNew.Status.MaintenanceModes[idx].Conditions = append([]metav1.Condition{
						{
							Type:               "Unrelated",
							Status:             metav1.ConditionTrue,
							LastTransitionTime: metav1.NewTime(baseTime),
							Reason:             string(rmn.MModeStateCompleted),
							Message:            "testing",
						},
					}, drClusterNew.Status.MaintenanceModes[idx].Conditions...)
				}
				Expect(controllers.DRClusterUpdateOfInterest(drClusterOld, drClusterNew)).To(BeFalse())
			})
		})

		When("new has all maintenance modes activated, and old has all deactivated", func() {
			It("returns DRCluster update of interest as true", func() {
				for mModeidx := range drClusterOld.Status.MaintenanceModes {
					for condIdx, condition := range drClusterOld.Status.MaintenanceModes[mModeidx].Conditions {
						if condition.Type != string(rmn.MModeConditionFailoverActivated) {
							continue
						}

						drClusterOld.Status.MaintenanceModes[mModeidx].Conditions[condIdx].Status = metav1.ConditionFalse
					}
				}
				Expect(controllers.DRClusterUpdateOfInterest(drClusterOld, drClusterNew)).To(BeTrue())
			})
		})

		When("new has a maintenance mode activated, that was missing conditions in old", func() {
			It("returns DRCluster update of interest as true", func() {
				for mModeidx := range drClusterOld.Status.MaintenanceModes {
					drClusterOld.Status.MaintenanceModes[mModeidx].Conditions = nil
				}
				Expect(controllers.DRClusterUpdateOfInterest(drClusterOld, drClusterNew)).To(BeTrue())
			})
		})

		When("new has maintenance mode activated, and all maintenance modes were deactivated in old", func() {
			It("returns DRCluster update of interest as true", func() {
				drClusterOld.Status.MaintenanceModes = nil
				Expect(controllers.DRClusterUpdateOfInterest(drClusterOld, drClusterNew)).To(BeTrue())
			})
		})
	})

	Describe("Test DRCluster triggered DRPC reconcile filter", Ordered, func() {
		var (
			cfg                       *rest.Config
			testEnv                   *envtest.Environment
			k8sClient                 client.Client
			r                         *controllers.DRPlacementControlReconciler
			drClusterUnreferenced     *rmn.DRCluster
			drCluster1, drCluster2    *rmn.DRCluster
			drCluster3, drCluster4    *rmn.DRCluster
			drCluster5, drCluster6    *rmn.DRCluster
			drpolicies                [3]rmn.DRPolicy
			baseDRPC                  *rmn.DRPlacementControl
			deployedDRPC              *rmn.DRPlacementControl
			failoverDRPCFailedOver    *rmn.DRPlacementControl
			toFailoverDRPCGenMismatch *rmn.DRPlacementControl
			toFailoverDRPCCondMissing *rmn.DRPlacementControl
			toFailoverDRPCCondFalse   *rmn.DRPlacementControl
		)

		BeforeAll(func() {
			By("bootstrapping test environment")
			testEnv = &envtest.Environment{
				CRDDirectoryPaths: []string{
					filepath.Join("..", "..", "config", "crd", "bases"),
					filepath.Join("..", "..", "hack", "test"),
				},
			}

			if testEnv.UseExistingCluster != nil && *testEnv.UseExistingCluster == true {
				namespaceDeletionSupported = true
			}

			var err error
			done := make(chan interface{})
			go func() {
				defer GinkgoRecover()
				cfg, err = testEnv.Start()
				close(done)
			}()
			Eventually(done).WithTimeout(time.Minute).Should(BeClosed())
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg).NotTo(BeNil())

			k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})

			r = &controllers.DRPlacementControlReconciler{
				Client: k8sClient,
			}

			// Initialize --- DRClusters
			// Unreferenced in any policy
			drClusterUnreferenced = drClusterNew.DeepCopy()
			drClusterUnreferenced.ObjectMeta.Name = "drcluster-unreferenced"

			// Referenced in policies
			drCluster1 = drClusterNew.DeepCopy()
			drCluster1.ObjectMeta.Name = "drcluster1"
			drCluster1.Spec.Region = "east1"

			drCluster2 = drClusterNew.DeepCopy()
			drCluster2.ObjectMeta.Name = "drcluster2"
			drCluster2.Spec.Region = "east2"

			drCluster3 = drClusterNew.DeepCopy()
			drCluster3.ObjectMeta.Name = "drcluster3"
			drCluster3.Spec.Region = "east3"

			drCluster4 = drClusterNew.DeepCopy()
			drCluster4.ObjectMeta.Name = "drcluster4"
			drCluster4.Spec.Region = "east4"

			drCluster5 = drClusterNew.DeepCopy()
			drCluster5.ObjectMeta.Name = "drcluster5"
			drCluster5.Spec.Region = "east5"

			drCluster6 = drClusterNew.DeepCopy()
			drCluster6.ObjectMeta.Name = "drcluster6"
			drCluster5.Spec.Region = "east6"

			Expect(k8sClient.Create(context.TODO(), drCluster1)).To(Succeed())
			Expect(k8sClient.Create(context.TODO(), drCluster2)).To(Succeed())
			Expect(k8sClient.Create(context.TODO(), drCluster3)).To(Succeed())
			Expect(k8sClient.Create(context.TODO(), drCluster4)).To(Succeed())
			Expect(k8sClient.Create(context.TODO(), drCluster5)).To(Succeed())
			Expect(k8sClient.Create(context.TODO(), drCluster6)).To(Succeed())

			// Initialize --- DRPolicies
			drpolicies = [3]rmn.DRPolicy{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "drpolicy0"},
					Spec: rmn.DRPolicySpec{
						DRClusters:         []string{"drcluster1", "drcluster2"},
						SchedulingInterval: "1m",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "drpolicy1"},
					Spec: rmn.DRPolicySpec{
						DRClusters:         []string{"drcluster3", "drcluster4"},
						SchedulingInterval: "1m",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "drpolicy-unreferenced"},
					Spec: rmn.DRPolicySpec{
						DRClusters:         []string{"drcluster5", "drcluster6"},
						SchedulingInterval: "1m",
					},
				},
			}

			for idx := range drpolicies {
				Expect(k8sClient.Create(context.TODO(), &drpolicies[idx])).To(Succeed())
			}

			// Initialize -- DRPCs
			// Define a base to copy from
			baseDRPC = &rmn.DRPlacementControl{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "drpc-base",
					Namespace: "default",
				},
				Spec: rmn.DRPlacementControlSpec{
					PlacementRef: corev1.ObjectReference{
						Name: "placementName",
					},
					DRPolicyRef: corev1.ObjectReference{
						Name: "drPolicyName",
					},
					PVCSelector:     metav1.LabelSelector{},
					FailoverCluster: "failoverCluster",
				},
			}

			// Create a namespace for all DRPCs
			localDRPCNameSpace := "ns1"
			Expect(k8sClient.Create(
				context.TODO(),
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: localDRPCNameSpace}},
			)).To(Succeed())

			// Define a DRPC that is in deployed state
			deployedDRPC = baseDRPC.DeepCopy()
			deployedDRPC.ObjectMeta.Namespace = localDRPCNameSpace
			deployedDRPC.ObjectMeta.Name = "deployed-drpc"
			deployedDRPC.Spec.DRPolicyRef.Name = "drpolicy0"

			// Define a DRPC that has failed over to drcluster3
			failoverDRPCFailedOver = baseDRPC.DeepCopy()
			failoverDRPCFailedOver.ObjectMeta.Namespace = localDRPCNameSpace
			failoverDRPCFailedOver.ObjectMeta.Name = "failingover-drcluster3-drpc"
			failoverDRPCFailedOver.Spec.DRPolicyRef.Name = "drpolicy1"
			failoverDRPCFailedOver.Spec.Action = rmn.ActionFailover
			failoverDRPCFailedOver.Spec.FailoverCluster = "drcluster3"

			// Define a DRPCs that is faiing over to drcluster4 (various conditions)
			toFailoverDRPCGenMismatch = baseDRPC.DeepCopy()
			toFailoverDRPCGenMismatch.ObjectMeta.Namespace = localDRPCNameSpace
			toFailoverDRPCGenMismatch.ObjectMeta.Name = "failingover-drcluster4-drpcgenmismatch"
			toFailoverDRPCGenMismatch.Spec.DRPolicyRef.Name = "drpolicy1"
			toFailoverDRPCGenMismatch.Spec.Action = rmn.ActionFailover
			toFailoverDRPCGenMismatch.Spec.FailoverCluster = "drcluster4"

			toFailoverDRPCCondMissing = toFailoverDRPCGenMismatch.DeepCopy()
			toFailoverDRPCCondMissing.ObjectMeta.Name = "failingover-drcluster4-drpccondmissing"

			toFailoverDRPCCondFalse = toFailoverDRPCGenMismatch.DeepCopy()
			toFailoverDRPCCondFalse.ObjectMeta.Name = "failingover-drcluster4-drpccondfalse"

			// Start creating the DRPCs (post all required DeepCopy operations)
			Expect(k8sClient.Create(context.TODO(), baseDRPC)).To(Succeed())
			Expect(k8sClient.Create(context.TODO(), deployedDRPC)).To(Succeed())

			Expect(k8sClient.Create(context.TODO(), failoverDRPCFailedOver)).To(Succeed())
			failoverDRPCFailedOver.Status = rmn.DRPlacementControlStatus{
				Conditions: []metav1.Condition{
					{
						Type:               rmn.ConditionAvailable,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(baseTime),
						Reason:             "Testing",
						Message:            "testing",
						ObservedGeneration: 1,
					},
				},
			}
			Expect(k8sClient.Status().Update(context.TODO(), failoverDRPCFailedOver)).To(Succeed())

			Expect(k8sClient.Create(context.TODO(), toFailoverDRPCGenMismatch)).To(Succeed())
			toFailoverDRPCGenMismatch.Status = rmn.DRPlacementControlStatus{
				Conditions: []metav1.Condition{
					{
						Type:               rmn.ConditionAvailable,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(baseTime),
						Reason:             "Testing",
						Message:            "testing",
						ObservedGeneration: 5,
					},
				},
			}
			Expect(k8sClient.Status().Update(context.TODO(), toFailoverDRPCGenMismatch)).To(Succeed())

			Expect(k8sClient.Create(context.TODO(), toFailoverDRPCCondMissing)).To(Succeed())
			toFailoverDRPCCondMissing.Status = rmn.DRPlacementControlStatus{
				Conditions: []metav1.Condition{},
			}
			Expect(k8sClient.Status().Update(context.TODO(), toFailoverDRPCCondMissing)).To(Succeed())

			Expect(k8sClient.Create(context.TODO(), toFailoverDRPCCondFalse)).To(Succeed())
			toFailoverDRPCCondFalse.Status = rmn.DRPlacementControlStatus{
				Conditions: []metav1.Condition{
					{
						Type:               rmn.ConditionAvailable,
						Status:             metav1.ConditionFalse,
						LastTransitionTime: metav1.NewTime(baseTime),
						Reason:             "Testing",
						Message:            "testing",
						ObservedGeneration: 5,
					},
				},
			}
			Expect(k8sClient.Status().Update(context.TODO(), toFailoverDRPCCondFalse)).To(Succeed())
		})

		When("No DRPolicy refers to the DRCluster", func() {
			It("Should return an empty list of DRPCs to reconcile", func() {
				Expect(len(r.FilterDRCluster(drClusterUnreferenced))).To(HaveValue(Equal(0)))
			})
		})

		When("No DRPC refers to the DRPolicy", func() {
			It("Should return an empty list of DRPCs to reconcile", func() {
				Expect(len(r.FilterDRCluster(drCluster6))).To(HaveValue(Equal(0)))
			})
		})

		When("DRPC refers to the DRPolicy, but not failing over", func() {
			It("Should return an empty list of DRPCs to reconcile", func() {
				Expect(len(r.FilterDRCluster(drCluster1))).To(HaveValue(Equal(0)))
			})
		})

		When("DRPC refers to the DRPolicy, but has already failed over", func() {
			It("Should return an empty list of DRPCs to reconcile", func() {
				Expect(len(r.FilterDRCluster(drCluster3))).To(HaveValue(Equal(0)))
			})
		})

		When("DRPC refers to the DRPolicy, but failing over to a different DRCluster", func() {
			It("Should return an empty list of DRPCs to reconcile", func() {
				Expect(len(r.FilterDRCluster(drCluster4))).To(HaveValue(Equal(3)))
			})
		})

		// TODO: Exact same test as above, fold into one
		When("DRPC refers to the DRPolicy, and is failing over", func() {
			It("Should return a DRPC to reconcile", func() {
				// Generation mismatch; Available missing/false/unknown;
				Expect(len(r.FilterDRCluster(drCluster4))).To(HaveValue(Equal(3)))
			})
		})

		AfterAll(func() {
			By("tearing down the test environment")
			err := testEnv.Stop()
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
