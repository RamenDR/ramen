// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	volrep "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v7/apis/volumesnapshot/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/workqueue"
	config "k8s.io/component-base/config/v1alpha1"
	clusterv1alpha1 "open-cluster-management.io/api/cluster/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	ramencontrollers "github.com/ramendr/ramen/internal/controller"
)

func ensureClaimCount(apiReader client.Reader, count int) {
	Eventually(func() bool {
		claims := &clusterv1alpha1.ClusterClaimList{}

		err := apiReader.List(context.TODO(), claims)
		if err != nil {
			return false
		}

		return len(claims.Items) == count
	}, timeout, interval).Should(BeTrue())
}

func ensureClusterClaim(apiReader client.Reader, class, name string) {
	Eventually(func() error {
		ccName := types.NamespacedName{
			Name: class + "." + name,
		}

		cc := &clusterv1alpha1.ClusterClaim{}
		err := apiReader.Get(context.TODO(), ccName, cc)
		if err != nil {
			return err
		}

		if cc.Spec.Value != name {
			return fmt.Errorf("mismatched spec.value in ClusterClaim, expected %s, got %s",
				name, cc.Spec.Value)
		}

		return nil
	}, timeout, interval).Should(BeNil())
}

var _ = Describe("DRClusterConfig-ClusterClaimsTests", Ordered, func() {
	var (
		ctx                 context.Context
		cancel              context.CancelFunc
		cfg                 *rest.Config
		testEnv             *envtest.Environment
		k8sClient           client.Client
		apiReader           client.Reader
		drCConfig           *ramen.DRClusterConfig
		baseSC, sc1, sc2    *storagev1.StorageClass
		baseVSC, vsc1, vsc2 *snapv1.VolumeSnapshotClass
		baseVRC, vrc1, vrc2 *volrep.VolumeReplicationClass
		claimCount          int
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

		By("starting the DRClusterConfig reconciler")

		ramenConfig := &ramen.RamenConfig{
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
		}

		options := manager.Options{Scheme: scheme.Scheme}
		ramencontrollers.LoadControllerOptions(&options, ramenConfig)

		k8sManager, err := ctrl.NewManager(cfg, options)
		Expect(err).ToNot(HaveOccurred())
		apiReader = k8sManager.GetAPIReader()
		Expect(apiReader).ToNot(BeNil())

		rateLimiter := workqueue.NewTypedMaxOfRateLimiter(
			workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](
				10*time.Millisecond,
				100*time.Millisecond),
		)

		Expect((&ramencontrollers.DRClusterConfigReconciler{
			Client:      k8sManager.GetClient(),
			Scheme:      k8sManager.GetScheme(),
			Log:         ctrl.Log.WithName("controllers").WithName("DRClusterConfig"),
			RateLimiter: &rateLimiter,
		}).SetupWithManager(k8sManager)).To(Succeed())

		ctx, cancel = context.WithCancel(context.TODO())
		go func() {
			err = k8sManager.Start(ctx)
			Expect(err).ToNot(HaveOccurred())
		}()

		By("Creating a DClusterConfig")

		drCConfig = &ramen.DRClusterConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "local"},
			Spec:       ramen.DRClusterConfigSpec{},
		}
		Expect(k8sClient.Create(context.TODO(), drCConfig)).To(Succeed())

		By("Defining basic Classes")

		baseSC = &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "baseSC",
				Labels: map[string]string{
					ramencontrollers.StorageIDLabel: "fake",
				},
			},
			Provisioner: "fake.ramen.com",
		}

		baseVSC = &snapv1.VolumeSnapshotClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "baseVSC",
				Labels: map[string]string{
					ramencontrollers.StorageIDLabel: "fake",
				},
			},
			Driver:         "fake.ramen.com",
			DeletionPolicy: snapv1.VolumeSnapshotContentDelete,
		}

		baseVRC = &volrep.VolumeReplicationClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "baseVRC",
				Labels: map[string]string{
					ramencontrollers.VolumeReplicationIDLabel: "fake",
				},
			},
			Spec: volrep.VolumeReplicationClassSpec{
				Provisioner: "fake.ramen.com",
			},
		}
	})

	AfterAll(func() {
		By("deleting the DRClusterConfig")
		Expect(k8sClient.Delete(context.TODO(), drCConfig)).To(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(context.TODO(), types.NamespacedName{
				Name: "local",
			}, drCConfig)

			return errors.IsNotFound(err)
		}, timeout, interval).Should(BeTrue())

		By("ensuring claim count is 0 post deletion")
		ensureClaimCount(apiReader, 0)

		cancel() // Stop the reconciler
		By("tearing down the test environment")
		err := testEnv.Stop()
		Expect(err).NotTo(HaveOccurred())
	})

	Describe("ClusterClaims", Ordered, func() {
		Context("Given DRClusterConfig resource", func() {
			When("there is a StorageClass created with required labels", func() {
				It("creates a ClusterClaim", func() {
					By("creating a StorageClass")

					sc1 = baseSC.DeepCopy()
					sc1.Name = "sc1"
					Expect(k8sClient.Create(context.TODO(), sc1)).To(Succeed())

					claimCount++
					ensureClusterClaim(apiReader, "storage.class", "sc1")
					ensureClaimCount(apiReader, claimCount)
				})
			})
			When("a StorageClass with required labels is deleted", func() {
				It("deletes the associated ClusterClaim", func() {
					By("deleting a StorageClass")

					Expect(k8sClient.Delete(context.TODO(), sc1)).To(Succeed())

					claimCount--
					ensureClaimCount(apiReader, claimCount)
				})
			})
			When("there are multiple StorageClass created with required labels", func() {
				It("creates ClusterClaims", func() {
					By("creating a StorageClass")

					sc1 = baseSC.DeepCopy()
					sc1.Name = "sc1"
					Expect(k8sClient.Create(context.TODO(), sc1)).To(Succeed())

					sc2 = baseSC.DeepCopy()
					sc2.Name = "sc2"
					Expect(k8sClient.Create(context.TODO(), sc2)).To(Succeed())

					claimCount += 2
					ensureClusterClaim(apiReader, "storage.class", "sc1")
					ensureClusterClaim(apiReader, "storage.class", "sc2")
					ensureClaimCount(apiReader, claimCount)
				})
			})
			When("a StorageClass label is deleted", func() {
				It("deletes the associated ClusterClaim", func() {
					By("deleting a StorageClass label")

					sc1.Labels = map[string]string{}
					Expect(k8sClient.Update(context.TODO(), sc1)).To(Succeed())

					claimCount--
					ensureClaimCount(apiReader, claimCount)
					ensureClusterClaim(apiReader, "storage.class", "sc2")
				})
			})
		})
		When("there is a SnapshotCLass created with required labels", func() {
			It("creates a ClusterClaim", func() {
				By("creating a SnapshotClass")

				vsc1 = baseVSC.DeepCopy()
				vsc1.Name = "vsc1"
				Expect(k8sClient.Create(context.TODO(), vsc1)).To(Succeed())

				claimCount++
				ensureClusterClaim(apiReader, "snapshot.class", "vsc1")
				ensureClaimCount(apiReader, claimCount)
			})
		})
		When("a SnapshotClass with required labels is deleted", func() {
			It("deletes the associated ClusterClaim", func() {
				By("deleting a SnapshotClass")

				Expect(k8sClient.Delete(context.TODO(), vsc1)).To(Succeed())

				claimCount--
				ensureClaimCount(apiReader, claimCount)
			})
		})
		When("there are multiple SnapshotClass created with required labels", func() {
			It("creates ClusterClaims", func() {
				By("creating a SnapshotClass")

				vsc1 = baseVSC.DeepCopy()
				vsc1.Name = "vsc1"
				Expect(k8sClient.Create(context.TODO(), vsc1)).To(Succeed())

				vsc2 = baseVSC.DeepCopy()
				vsc2.Name = "vsc2"
				Expect(k8sClient.Create(context.TODO(), vsc2)).To(Succeed())

				claimCount += 2
				ensureClusterClaim(apiReader, "snapshot.class", "vsc1")
				ensureClusterClaim(apiReader, "snapshot.class", "vsc2")
				ensureClaimCount(apiReader, claimCount)
			})
		})
		When("a SnapshotClass label is deleted", func() {
			It("deletes the associated ClusterClaim", func() {
				By("deleting a SnapshotClass label")

				vsc2.Labels = map[string]string{}
				Expect(k8sClient.Update(context.TODO(), vsc2)).To(Succeed())

				claimCount--
				ensureClaimCount(apiReader, claimCount)
				ensureClusterClaim(apiReader, "snapshot.class", "vsc1")
			})
		})
		When("there is a VolumeReplicationCLass created with required labels", func() {
			It("creates a ClusterClaim", func() {
				By("creating a VolumeReplicationClass")

				vrc1 = baseVRC.DeepCopy()
				vrc1.Name = "vrc1"
				Expect(k8sClient.Create(context.TODO(), vrc1)).To(Succeed())

				claimCount++
				ensureClusterClaim(apiReader, "replication.class", "vrc1")
				ensureClaimCount(apiReader, claimCount)
			})
		})
		When("a VolumeReplicationClass with required labels is deleted", func() {
			It("deletes the associated ClusterClaim", func() {
				By("deleting a VolumeReplicationClass")

				Expect(k8sClient.Delete(context.TODO(), vrc1)).To(Succeed())

				claimCount--
				ensureClaimCount(apiReader, claimCount)
			})
		})
		When("there are multiple VolumeReplicationClass created with required labels", func() {
			It("creates ClusterClaims", func() {
				By("creating a VolumeReplicationClass")

				vrc1 = baseVRC.DeepCopy()
				vrc1.Name = "vrc1"
				Expect(k8sClient.Create(context.TODO(), vrc1)).To(Succeed())

				vrc2 = baseVRC.DeepCopy()
				vrc2.Name = "vrc2"
				Expect(k8sClient.Create(context.TODO(), vrc2)).To(Succeed())

				claimCount += 2
				ensureClusterClaim(apiReader, "replication.class", "vrc1")
				ensureClusterClaim(apiReader, "replication.class", "vrc2")
				ensureClaimCount(apiReader, claimCount)
			})
		})
		When("a VolumeReplicationClass label is deleted", func() {
			It("deletes the associated ClusterClaim", func() {
				By("deleting a VolumeReplicationClass label")

				vrc2.Labels = map[string]string{}
				Expect(k8sClient.Update(context.TODO(), vrc2)).To(Succeed())

				claimCount--
				ensureClaimCount(apiReader, claimCount)
				ensureClusterClaim(apiReader, "replication.class", "vrc1")
			})
		})
	})
})
