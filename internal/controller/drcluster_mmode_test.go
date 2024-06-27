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
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	plrv1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/workqueue"
	config "k8s.io/component-base/config/v1alpha1"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	ramencontrollers "github.com/ramendr/ramen/internal/controller"
	"github.com/ramendr/ramen/internal/controller/util"
)

var _ = Describe("DRClusterMModeTests", Ordered, func() {
	var (
		ctx                    context.Context
		cancel                 context.CancelFunc
		cfg                    *rest.Config
		testEnv                *envtest.Environment
		k8sClient              client.Client
		drCluster1, drCluster2 *rmn.DRCluster
		drpolicy               rmn.DRPolicy
		drPolicyName           = "drpolicy0"
		baseDRPC               *rmn.DRPlacementControl
		deployedDRPC           *rmn.DRPlacementControl
		failoverDRPC1          *rmn.DRPlacementControl
		failoverDRPC2          *rmn.DRPlacementControl
		timeout                = time.Second * 5
		interval               = time.Millisecond * 100
		ramenConfig            *rmn.RamenConfig
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
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "drcluster1"}},
		)).To(Succeed())

		ensureManagedCluster(k8sClient, "drcluster1")

		Expect(k8sClient.Create(
			context.TODO(),
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "drcluster2"}},
		)).To(Succeed())

		ensureManagedCluster(k8sClient, "drcluster2")

		By("Defining a ramen configuration")

		ramenConfig = &rmn.RamenConfig{
			TypeMeta: metav1.TypeMeta{
				Kind:       "RamenConfig",
				APIVersion: rmn.GroupVersion.String(),
			},
			LeaderElection: &config.LeaderElectionConfiguration{
				LeaderElect:  new(bool),
				ResourceName: ramencontrollers.HubLeaderElectionResourceName,
			},
			Metrics: rmn.ControllerMetrics{
				BindAddress: "0", // Disable metrics
			},
			RamenControllerType: rmn.DRHubType,
			S3StoreProfiles: []rmn.S3StoreProfile{
				{
					S3ProfileName:        "fake",
					S3Bucket:             bucketNameSucc,
					S3CompatibleEndpoint: "http://192.168.39.223:30000",
					S3Region:             "us-east-1",
					S3SecretRef:          corev1.SecretReference{Name: "fakes3secret"},
				},
			},
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

		s3Secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: configMap.Namespace, Name: "fakes3secret"},
			StringData: map[string]string{
				"AWS_ACCESS_KEY_ID":     awsAccessKeyIDSucc,
				"AWS_SECRET_ACCESS_KEY": "",
			},
		}
		Expect(k8sClient.Create(context.TODO(), s3Secret)).To(Succeed())

		By("starting the DRCluster reconciler")

		options := manager.Options{Scheme: scheme.Scheme}
		ramencontrollers.LoadControllerOptions(&options, ramenConfig)

		Expect(err).NotTo(HaveOccurred())

		k8sManager, err := ctrl.NewManager(cfg, options)
		Expect(err).ToNot(HaveOccurred())

		rateLimiter := workqueue.NewMaxOfRateLimiter(
			workqueue.NewItemExponentialFailureRateLimiter(10*time.Millisecond, 100*time.Millisecond),
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

		// Initialize --- DRPolicy
		drpolicy = rmn.DRPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: drPolicyName},
			Spec: rmn.DRPolicySpec{
				DRClusters:         []string{"drcluster1", "drcluster2"},
				SchedulingInterval: "1m",
			},
		}

		Expect(k8sClient.Create(context.TODO(), &drpolicy)).To(Succeed())

		// Create a namespace for all DRPCs
		localDRPCNameSpace := "ns1"
		Expect(k8sClient.Create(
			context.TODO(),
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: localDRPCNameSpace}},
		)).To(Succeed())

		// Create PlacementRule for all DRPCs
		// TODO: Future proof, ue Placement instead (without AppSet Placement would work as well)
		placementRule := &plrv1.PlacementRule{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "plrule",
				Namespace: localDRPCNameSpace,
			},
			Spec: plrv1.PlacementRuleSpec{
				GenericPlacementFields: plrv1.GenericPlacementFields{},
				SchedulerName:          "ramen",
			},
		}
		Expect(k8sClient.Create(context.TODO(), placementRule)).To(Succeed())

		// Initialize -- DRPCs
		// Define a base to copy from
		baseDRPC = &rmn.DRPlacementControl{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "drpc-base",
				Namespace: localDRPCNameSpace,
			},
			Spec: rmn.DRPlacementControlSpec{
				PlacementRef: corev1.ObjectReference{
					Name: "plrule",
				},
				DRPolicyRef: corev1.ObjectReference{
					Name: drPolicyName,
				},
				PVCSelector:      metav1.LabelSelector{},
				FailoverCluster:  "drcluster1",
				PreferredCluster: "drcluster2",
			},
			Status: rmn.DRPlacementControlStatus{
				Conditions: []metav1.Condition{
					{
						Type:               rmn.ConditionAvailable,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(time.Now()),
						Reason:             "Testing",
						Message:            "testing",
						ObservedGeneration: 1,
					},
				},
			},
		}

		// Define a DRPC that is in deployed state
		deployedDRPC = baseDRPC.DeepCopy()
		deployedDRPC.ObjectMeta.Name = "drpc3"
		deployedDRPC.Spec.DRPolicyRef.Name = drPolicyName

		// Define a DRPCs that is faiing over to drcluster1
		failoverDRPC1 = baseDRPC.DeepCopy()
		failoverDRPC1.ObjectMeta.Name = "drpc1"
		failoverDRPC1.Spec.DRPolicyRef.Name = drPolicyName
		failoverDRPC1.Spec.Action = rmn.ActionFailover

		failoverDRPC2 = failoverDRPC1.DeepCopy()
		failoverDRPC2.ObjectMeta.Name = "drpc2"

		// Create the DRPCs
		Expect(k8sClient.Create(context.TODO(), deployedDRPC)).To(Succeed())
		Expect(k8sClient.Create(context.TODO(), failoverDRPC1)).To(Succeed())
		Expect(k8sClient.Create(context.TODO(), failoverDRPC2)).To(Succeed())

		// Update DRPC status
		deployedDRPC.Status = baseDRPC.Status
		Expect(k8sClient.Status().Update(context.TODO(), deployedDRPC)).To(Succeed())
		failoverDRPC1.Status = baseDRPC.Status
		Expect(k8sClient.Status().Update(context.TODO(), failoverDRPC1)).To(Succeed())
		failoverDRPC2.Status = baseDRPC.Status
		Expect(k8sClient.Status().Update(context.TODO(), failoverDRPC2)).To(Succeed())

		// Initialize --- DRCluster
		drCluster1 = &rmn.DRCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "drcluster1"},
			Spec:       rmn.DRClusterSpec{S3ProfileName: "fake", Region: "east"},
		}
		drCluster2 = drCluster1.DeepCopy()
		drCluster2.ObjectMeta.Name = "drcluster2"
		drCluster2.Spec.Region = "west"

		Expect(k8sClient.Create(context.TODO(), drCluster1)).To(Succeed())
		updateDRClusterManifestWorkStatus(k8sClient, k8sManager.GetAPIReader(), drCluster1.GetName())
		updateDRClusterConfigMWStatus(k8sClient, k8sManager.GetAPIReader(), drCluster1.GetName())
		Expect(k8sClient.Create(context.TODO(), drCluster2)).To(Succeed())
		updateDRClusterManifestWorkStatus(k8sClient, k8sManager.GetAPIReader(), drCluster2.GetName())
		updateDRClusterConfigMWStatus(k8sClient, k8sManager.GetAPIReader(), drCluster2.GetName())
	})

	AfterAll(func() {
		cancel() // Stop the reconciler
		By("tearing down the test environment")
		err := testEnv.Stop()
		Expect(err).NotTo(HaveOccurred())
	})

	Describe("Test DRPC triggered DRCluster reconcile", Ordered, func() {
		mModesActive := func() bool {
			drcluster := &rmn.DRCluster{}
			Expect(k8sClient.Get(context.TODO(), types.NamespacedName{Name: drCluster1.GetName()}, drcluster)).To(Succeed())

			for _, maintenanceMode := range drcluster.Status.MaintenanceModes {
				if !(maintenanceMode.State == rmn.MModeStateCompleted &&
					maintenanceMode.StorageProvisioner == "test.csi.com" &&
					maintenanceMode.TargetID == "storage-replication-id-1") {
					continue
				}

				for _, condition := range maintenanceMode.Conditions {
					if condition.Type != string(rmn.MModeConditionFailoverActivated) {
						continue
					}

					return condition.Status == metav1.ConditionTrue
				}
			}

			return false
		}

		emptyMModeMWList := func() bool {
			matchLabels := map[string]string{
				util.MModesLabel: "",
			}
			listOptions := []client.ListOption{
				client.InNamespace("drcluster1"),
				client.MatchingLabels(matchLabels),
			}

			mModeMWs := &ocmworkv1.ManifestWorkList{}
			Expect(k8sClient.List(context.TODO(), mModeMWs, listOptions...)).To(Succeed())

			return len(mModeMWs.Items) == 0
		}

		When("No DRPCs are failing over", func() {
			It("MaintenanceMode should not be activated", func() {
				Consistently(mModesActive, timeout, interval).Should(BeFalse(), "MaintenanceModes active when not needed")
				Expect(emptyMModeMWList()).To(BeTrue(), "Found MMode manifests when not needed")
			})
		})

		When("DRPCs are failing over", func() {
			It("MaintenanceMode should remain activated", func() {
				By("Updating DRPCs to reflect that they are failing over")

				failoverDRPC1.Status = rmn.DRPlacementControlStatus{
					Conditions: []metav1.Condition{
						{
							Type:               rmn.ConditionAvailable,
							Status:             metav1.ConditionFalse,
							LastTransitionTime: metav1.NewTime(time.Now()),
							Reason:             "Testing",
							Message:            "testing",
							ObservedGeneration: 1,
						},
					},
				}
				failoverDRPC1.Status.DeepCopyInto(&failoverDRPC2.Status)

				Expect(k8sClient.Status().Update(context.TODO(), failoverDRPC1)).To(Succeed())
				Eventually(mModesActive, timeout, interval).Should(BeTrue(), "MaintenanceModes still inactive")
				Expect(emptyMModeMWList()).To(BeFalse(), "MMode manifests missing")

				Expect(k8sClient.Status().Update(context.TODO(), failoverDRPC2)).To(Succeed())
				Consistently(mModesActive, timeout, interval).Should(BeTrue(), "MaintenanceModes activated when not needed")

				By("Updating a DRPC as failed over, but others are still failing over")

				failoverDRPC1.Status = rmn.DRPlacementControlStatus{
					Conditions: []metav1.Condition{
						{
							Type:               rmn.ConditionAvailable,
							Status:             metav1.ConditionTrue,
							LastTransitionTime: metav1.NewTime(time.Now()),
							Reason:             "Testing",
							Message:            "testing",
							ObservedGeneration: 1,
						},
					},
				}
				Expect(k8sClient.Status().Update(context.TODO(), failoverDRPC1)).To(Succeed())

				Consistently(mModesActive, timeout, interval).Should(BeTrue(), "MaintenanceModes activated when not needed")
				Expect(emptyMModeMWList()).To(BeFalse(), "MMode manifests missing")
			})
		})

		When("All DRPCs have failed over", func() {
			It("MaintenanceMode should be deactivated", func() {
				By("Updating the last DRPC as failed over")

				failoverDRPC2.Status = rmn.DRPlacementControlStatus{
					Conditions: []metav1.Condition{
						{
							Type:               rmn.ConditionAvailable,
							Status:             metav1.ConditionTrue,
							LastTransitionTime: metav1.NewTime(time.Now()),
							Reason:             "Testing",
							Message:            "testing",
							ObservedGeneration: 1,
						},
					},
				}
				Expect(k8sClient.Status().Update(context.TODO(), failoverDRPC2)).To(Succeed())
				Eventually(mModesActive, timeout, interval).Should(BeFalse(), "MaintenanceModes still active")
				Consistently(mModesActive, timeout, interval).Should(BeFalse(), "MaintenanceModes activated when not needed")
				Expect(emptyMModeMWList()).To(BeTrue(), "Found MMode manifests when not needed")
			})
		})

		When("DRCluster is deleted", func() {
			It("Garbage collects all MaintenanceMode resources", func() {
				Expect(k8sClient.Delete(context.TODO(), &drpolicy)).To(Succeed())
				Expect(k8sClient.Delete(context.TODO(), drCluster1)).To(Succeed())
				Eventually(emptyMModeMWList, timeout, interval).Should(BeTrue(), "Found MMode manifests when not needed")
			})
		})
	})
})
