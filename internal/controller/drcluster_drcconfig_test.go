// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	ocmv1 "open-cluster-management.io/api/cluster/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/workqueue"
	config "k8s.io/component-base/config/v1alpha1"
	"k8s.io/utils/ptr"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	ramencontrollers "github.com/ramendr/ramen/internal/controller"
)

var _ = Describe("DRCluster-DRClusterConfigTests", Ordered, func() {
	var (
		ctx            context.Context
		cancel         context.CancelFunc
		cfg            *rest.Config
		testEnv        *envtest.Environment
		k8sClient      client.Client
		apiReader      client.Reader
		drCluster1     *ramen.DRCluster
		drCluster1Name = "drcluster1"
		ramenConfig    *ramen.RamenConfig
		mc             *ocmv1.ManagedCluster
	)

	BeforeAll(func() {
		By("bootstrapping test environment")

		Expect(os.Setenv("POD_NAMESPACE", ramenNamespace)).To(Succeed())

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
		Expect(err).NotTo(HaveOccurred())

		By("Creating namespaces")

		Expect(k8sClient.Create(context.TODO(),
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ramenNamespace}})).To(Succeed())

		Expect(k8sClient.Create(
			context.TODO(),
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: drCluster1Name}},
		)).To(Succeed())

		By("Defining a ramen configuration")

		ramenConfig = &ramen.RamenConfig{
			TypeMeta: metav1.TypeMeta{
				Kind:       "RamenConfig",
				APIVersion: ramen.GroupVersion.String(),
			},
			LeaderElection: &config.LeaderElectionConfiguration{
				LeaderElect:  new(bool),
				ResourceName: ramencontrollers.HubLeaderElectionResourceName,
			},
			Metrics: ramen.ControllerMetrics{
				BindAddress: "0", // Disable metrics
			},
			RamenControllerType: ramen.DRHubType,
		}
		ramenConfig.DrClusterOperator.DeploymentAutomationEnabled = true
		ramenConfig.DrClusterOperator.S3SecretDistributionEnabled = true
		configMap, err := ramencontrollers.ConfigMapNew(
			ramenNamespace,
			ramencontrollers.HubOperatorConfigMapName,
			ramenConfig,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Create(context.TODO(), configMap)).To(Succeed())

		By("starting the DRCluster reconciler")

		options := manager.Options{Scheme: scheme.Scheme}
		options.Controller.SkipNameValidation = ptr.To(true)
		ramencontrollers.LoadControllerOptions(&options, ramenConfig)

		k8sManager, err := ctrl.NewManager(cfg, options)
		Expect(err).ToNot(HaveOccurred())

		apiReader = k8sManager.GetAPIReader()

		rateLimiter := workqueue.NewTypedMaxOfRateLimiter(
			workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](10*time.Millisecond, 100*time.Millisecond),
		)

		Expect((&ramencontrollers.DRClusterReconciler{
			Client:    k8sManager.GetClient(),
			APIReader: k8sManager.GetAPIReader(),
			Scheme:    k8sManager.GetScheme(),
			Log:       ctrl.Log.WithName("controllers").WithName("DRCluster"),
			MCVGetter: FakeMCVGetter{
				Client:    k8sClient,
				apiReader: k8sManager.GetAPIReader(),
			},
			ObjectStoreGetter: fakeObjectStoreGetter{},
			RateLimiter:       &rateLimiter,
		}).SetupWithManager(k8sManager)).To(Succeed())

		ctx, cancel = context.WithCancel(context.TODO())
		go func() {
			err = k8sManager.Start(ctx)
			Expect(err).ToNot(HaveOccurred())
		}()

		By("creating the initial API resources")

		// Initialize --- DRCluster
		drCluster1 = &ramen.DRCluster{
			ObjectMeta: metav1.ObjectMeta{Name: drCluster1Name},
			Spec:       ramen.DRClusterSpec{S3ProfileName: "NoS3", Region: "east"},
		}

		Expect(k8sClient.Create(context.TODO(), drCluster1)).To(Succeed())
		updateDRClusterManifestWorkStatus(k8sClient, apiReader, drCluster1Name)
	})

	AfterAll(func() {
		cancel() // Stop the reconciler
		By("tearing down the test environment")
		err := testEnv.Stop()
		Expect(err).NotTo(HaveOccurred())
	})

	Describe("DRClusterConfig", Ordered, func() {
		Context("Given ManagedCluster resource status", func() {
			When("There is no ManagedCluster resource", func() {
				It("reports DRCluster validated as false", func() {
					drclusterConditionExpectEventually(
						apiReader,
						drCluster1,
						false,
						metav1.ConditionFalse,
						Equal("DRClusterConfigInProgress"),
						Ignore(),
						ramen.DRClusterValidated,
					)
				})
				It("fails to create the DRClusterConfig manifest", func() {
					ensureDRClusterConfigMWNotFound(k8sClient, drCluster1Name, true)
				})
			})
			When("ManagedCluster resource has no status", func() {
				It("reports DRCluster validated as false", func() {
					By("creating a ManagedCluster resource without status")
					mc = createManagedCluster(k8sClient, drCluster1Name)
					Expect(mc != nil)
					drclusterConditionExpectEventually(
						apiReader,
						drCluster1,
						false,
						metav1.ConditionFalse,
						Equal("DRClusterConfigInProgress"),
						Ignore(),
						ramen.DRClusterValidated,
					)
				})
				It("fails to create the DRClusterConfig manifest", func() {
					ensureDRClusterConfigMWNotFound(k8sClient, drCluster1Name, true)
				})
			})
			When("ManagedCluster resource has no claims", func() {
				It("reports DRCluster validated as false", func() {
					By("updating a ManagedCluster resource status with conditions")
					mc.Status = ocmv1.ManagedClusterStatus{
						Conditions: []metav1.Condition{
							{
								Type:               ocmv1.ManagedClusterConditionHubAccepted,
								LastTransitionTime: metav1.Time{Time: time.Now()},
								Status:             metav1.ConditionTrue,
								Reason:             ocmv1.ManagedClusterConditionHubAccepted,
								Message:            "Faked status",
							},
							{
								Type:               ocmv1.ManagedClusterConditionJoined,
								LastTransitionTime: metav1.Time{Time: time.Now()},
								Status:             metav1.ConditionTrue,
								Reason:             ocmv1.ManagedClusterConditionJoined,
								Message:            "Faked status",
							},
						},
					}
					Expect(k8sClient.Status().Update(context.TODO(), mc)).To(Succeed())

					drclusterConditionExpectEventually(
						apiReader,
						drCluster1,
						false,
						metav1.ConditionFalse,
						Equal("DRClusterConfigInProgress"),
						Ignore(),
						ramen.DRClusterValidated,
					)
				})
				It("fails to create the DRClusterConfig manifest", func() {
					ensureDRClusterConfigMWNotFound(k8sClient, drCluster1Name, true)
				})
			})
			When("ManagedCluster resource is missing cluster ID claim", func() {
				It("reports DRCluster validated as false", func() {
					By("updating a ManagedCluster resource status with other claims")
					mc.Status = ocmv1.ManagedClusterStatus{
						Conditions: []metav1.Condition{
							{
								Type:               ocmv1.ManagedClusterConditionHubAccepted,
								LastTransitionTime: metav1.Time{Time: time.Now()},
								Status:             metav1.ConditionTrue,
								Reason:             ocmv1.ManagedClusterConditionHubAccepted,
								Message:            "Faked status",
							},
							{
								Type:               ocmv1.ManagedClusterConditionJoined,
								LastTransitionTime: metav1.Time{Time: time.Now()},
								Status:             metav1.ConditionTrue,
								Reason:             ocmv1.ManagedClusterConditionJoined,
								Message:            "Faked status",
							},
						},
						ClusterClaims: []ocmv1.ManagedClusterClaim{
							{
								Name:  "fake.claim.1",
								Value: "fake",
							},
							{
								Name:  "fake.claim.2",
								Value: "fake",
							},
						},
					}
					Expect(k8sClient.Status().Update(context.TODO(), mc)).To(Succeed())

					drclusterConditionExpectEventually(
						apiReader,
						drCluster1,
						false,
						metav1.ConditionFalse,
						Equal("DRClusterConfigInProgress"),
						Ignore(),
						ramen.DRClusterValidated,
					)
				})
				It("fails to create the DRClusterConfig manifest", func() {
					ensureDRClusterConfigMWNotFound(k8sClient, drCluster1Name, true)
				})
			})
			When("ManagedCluster resource is missing value for cluster ID claim", func() {
				It("reports DRCluster validated as false", func() {
					By("updating a ManagedCluster resource status with other claims")
					mc.Status = ocmv1.ManagedClusterStatus{
						Conditions: []metav1.Condition{
							{
								Type:               ocmv1.ManagedClusterConditionHubAccepted,
								LastTransitionTime: metav1.Time{Time: time.Now()},
								Status:             metav1.ConditionTrue,
								Reason:             ocmv1.ManagedClusterConditionHubAccepted,
								Message:            "Faked status",
							},
							{
								Type:               ocmv1.ManagedClusterConditionJoined,
								LastTransitionTime: metav1.Time{Time: time.Now()},
								Status:             metav1.ConditionTrue,
								Reason:             ocmv1.ManagedClusterConditionJoined,
								Message:            "Faked status",
							},
						},
						ClusterClaims: []ocmv1.ManagedClusterClaim{
							{
								Name:  "fake.claim.1",
								Value: "fake",
							},
							{
								Name:  "fake.claim.2",
								Value: "fake",
							},
							{
								Name:  "id.k8s.io",
								Value: "",
							},
						},
					}
					Expect(k8sClient.Status().Update(context.TODO(), mc)).To(Succeed())

					drclusterConditionExpectEventually(
						apiReader,
						drCluster1,
						false,
						metav1.ConditionFalse,
						Equal("DRClusterConfigInProgress"),
						Ignore(),
						ramen.DRClusterValidated,
					)
				})
				It("fails to create the DRClusterConfig manifest", func() {
					ensureDRClusterConfigMWNotFound(k8sClient, drCluster1Name, true)
				})
			})
			When("ManagedCluster resource has all required status", func() {
				It("reports DRCluster validated as true", func() {
					By("updating a ManagedCluster resource status with correct claims")
					mc.Status = ocmv1.ManagedClusterStatus{
						Conditions: []metav1.Condition{
							{
								Type:               ocmv1.ManagedClusterConditionHubAccepted,
								LastTransitionTime: metav1.Time{Time: time.Now()},
								Status:             metav1.ConditionTrue,
								Reason:             ocmv1.ManagedClusterConditionHubAccepted,
								Message:            "Faked status",
							},
							{
								Type:               ocmv1.ManagedClusterConditionJoined,
								LastTransitionTime: metav1.Time{Time: time.Now()},
								Status:             metav1.ConditionTrue,
								Reason:             ocmv1.ManagedClusterConditionJoined,
								Message:            "Faked status",
							},
						},
						ClusterClaims: []ocmv1.ManagedClusterClaim{
							{
								Name:  "fake.claim.1",
								Value: "fake",
							},
							{
								Name:  "fake.claim.2",
								Value: "fake",
							},
							{
								Name:  "id.k8s.io",
								Value: "cluster",
							},
						},
					}
					Expect(k8sClient.Status().Update(context.TODO(), mc)).To(Succeed())

					By("marking DRClusterConfig manifest as applied")
					updateDRClusterConfigMWStatus(k8sClient, apiReader, drCluster1Name)

					drclusterConditionExpectEventually(
						apiReader,
						drCluster1,
						false,
						metav1.ConditionTrue,
						Equal(ramencontrollers.DRClusterConditionReasonValidated),
						Ignore(),
						ramen.DRClusterValidated,
					)
				})
				It("creates the DRClusterConfig manifest", func() {
					verifyDRClusterConfigMW(k8sClient, drCluster1Name, "cluster", []string{}, false)
				})
			})
		})
		Context("Given various DRPolicy resources", func() {
			When("There is a Sync DRPolicy", func() {
				It("DRClusterConfig manifest has empty schedules", func() {
					By("Creating a Sync DRPolicy")
					syncDRPolicy := ramen.DRPolicy{
						ObjectMeta: metav1.ObjectMeta{
							Name: "drpolicy-sync",
						},
						Spec: ramen.DRPolicySpec{
							SchedulingInterval: "",
							DRClusters: []string{
								drCluster1Name,
								"fake",
							},
						},
					}
					Expect(k8sClient.Create(context.TODO(), &syncDRPolicy)).To(Succeed())

					verifyDRClusterConfigMW(k8sClient, drCluster1Name, "cluster", []string{}, true)
				})
			})
			When("There is an Async DRPolicy", func() {
				It("DRClusterConfig manifest has required schedules", func() {
					By("Creating an Async DRPolicy")
					asyncDRPolicy := ramen.DRPolicy{
						ObjectMeta: metav1.ObjectMeta{
							Name: "drpolicy-async1",
						},
						Spec: ramen.DRPolicySpec{
							SchedulingInterval: "1m",
							DRClusters: []string{
								drCluster1Name,
								"fake",
							},
						},
					}
					Expect(k8sClient.Create(context.TODO(), &asyncDRPolicy)).To(Succeed())

					verifyDRClusterConfigMW(k8sClient, drCluster1Name, "cluster", []string{"1m"}, false)
				})
			})
			When("There is another Async DRPolicy with the same schedule", func() {
				It("DRClusterConfig manifest has required schedules", func() {
					By("Creating an Async DRPolicy")
					asyncDRPolicy := ramen.DRPolicy{
						ObjectMeta: metav1.ObjectMeta{
							Name: "drpolicy-async2",
						},
						Spec: ramen.DRPolicySpec{
							SchedulingInterval: "1m",
							DRClusters: []string{
								drCluster1Name,
								"fake",
							},
						},
					}
					Expect(k8sClient.Create(context.TODO(), &asyncDRPolicy)).To(Succeed())

					verifyDRClusterConfigMW(k8sClient, drCluster1Name, "cluster", []string{"1m"}, true)
				})
			})
			When("There is another Async DRPolicy with a different schedule", func() {
				It("DRClusterConfig manifest has required schedules", func() {
					By("Creating an Async DRPolicy")
					asyncDRPolicy := ramen.DRPolicy{
						ObjectMeta: metav1.ObjectMeta{
							Name: "drpolicy-async3",
						},
						Spec: ramen.DRPolicySpec{
							SchedulingInterval: "5m",
							DRClusters: []string{
								drCluster1Name,
								"fake",
							},
						},
					}
					Expect(k8sClient.Create(context.TODO(), &asyncDRPolicy)).To(Succeed())

					verifyDRClusterConfigMW(k8sClient, drCluster1Name, "cluster", []string{"1m", "5m"}, false)
				})
			})
			When("An Async DRPolicy with a common schedule is deleted", func() {
				It("DRClusterConfig manifest has required schedules", func() {
					By("Creating an Async DRPolicy")
					asyncDRPolicy := ramen.DRPolicy{
						ObjectMeta: metav1.ObjectMeta{
							Name: "drpolicy-async1",
						},
					}
					Expect(k8sClient.Delete(context.TODO(), &asyncDRPolicy)).To(Succeed())

					verifyDRClusterConfigMW(k8sClient, drCluster1Name, "cluster", []string{"1m", "5m"}, true)
				})
			})
			When("An Async DRPolicy with a unique schedule is deleted", func() {
				It("DRClusterConfig manifest has required schedules", func() {
					By("Creating an Async DRPolicy")
					asyncDRPolicy := ramen.DRPolicy{
						ObjectMeta: metav1.ObjectMeta{
							Name: "drpolicy-async3",
						},
					}
					Expect(k8sClient.Delete(context.TODO(), &asyncDRPolicy)).To(Succeed())

					verifyDRClusterConfigMW(k8sClient, drCluster1Name, "cluster", []string{"1m"}, false)
				})
			})
			When("A last Async DRPolicy with a unique schedule is deleted", func() {
				It("DRClusterConfig manifest has required schedules", func() {
					By("Creating an Async DRPolicy")
					asyncDRPolicy := ramen.DRPolicy{
						ObjectMeta: metav1.ObjectMeta{
							Name: "drpolicy-async2",
						},
					}
					Expect(k8sClient.Delete(context.TODO(), &asyncDRPolicy)).To(Succeed())

					verifyDRClusterConfigMW(k8sClient, drCluster1Name, "cluster", []string{}, false)
				})
			})
			When("An Async DRPolicy does not contain the DRCluster", func() {
				It("DRClusterConfig manifest has required schedules", func() {
					By("Creating an Async DRPolicy")
					asyncDRPolicy := ramen.DRPolicy{
						ObjectMeta: metav1.ObjectMeta{
							Name: "drpolicy-async3",
						},
						Spec: ramen.DRPolicySpec{
							SchedulingInterval: "5m",
							DRClusters: []string{
								"fake1",
								"fake2",
							},
						},
					}
					Expect(k8sClient.Create(context.TODO(), &asyncDRPolicy)).To(Succeed())

					verifyDRClusterConfigMW(k8sClient, drCluster1Name, "cluster", []string{}, true)
				})
			})
			When("There are no DRPolicy resources containing the DRCluster", func() {
				It("DRClusterConfig manifest has required schedules", func() {
					By("Deleting all DRPolicy resources referencing the DRCluster")
					syncDRPolicy := ramen.DRPolicy{
						ObjectMeta: metav1.ObjectMeta{
							Name: "drpolicy-sync",
						},
					}
					Expect(k8sClient.Delete(context.TODO(), &syncDRPolicy)).To(Succeed())

					verifyDRClusterConfigMW(k8sClient, drCluster1Name, "cluster", []string{}, true)
				})
			})
			When("There are no DRPolicy resources", func() {
				It("DRClusterConfig manifest has required schedules", func() {
					By("Deleting all DRPolicy resources")
					asyncDRPolicy := ramen.DRPolicy{
						ObjectMeta: metav1.ObjectMeta{
							Name: "drpolicy-async3",
						},
					}
					Expect(k8sClient.Delete(context.TODO(), &asyncDRPolicy)).To(Succeed())

					verifyDRClusterConfigMW(k8sClient, drCluster1Name, "cluster", []string{}, true)
				})
			})
		})
		Context("When a DRCluster is deleted", func() {
			It("Cleans up the DRClusterConfig ManifestWork", func() {
				By("Deleting the DRCluster resource")
				drc := ramen.DRCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: drCluster1Name,
					},
				}
				Expect(k8sClient.Delete(context.TODO(), &drc)).To(Succeed())

				By("Ensuring the ManifestWork is not found")
				ensureDRClusterConfigMWNotFound(k8sClient, drCluster1Name, false)
			})
		})
	})
})
