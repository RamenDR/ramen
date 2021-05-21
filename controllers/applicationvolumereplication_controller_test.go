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

	volrep "github.com/csi-addons/volume-replication-operator/api/v1alpha1"
	spokeClusterV1 "github.com/open-cluster-management/api/cluster/v1"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	plrv1 "github.com/open-cluster-management/multicloud-operators-placementrule/pkg/apis/apps/v1"
	rmn "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
)

const (
	AVRName            = "app-volume-replication-test"
	AVRNamespaceName   = "app-namespace"
	EastManagedCluster = "east-cluster"
	WestManagedCluster = "west-cluster"
	DRClusterPeersName = "my-dr-peers"

	timeout  = time.Second * 10
	interval = time.Millisecond * 250
)

var (
	westCluster = &spokeClusterV1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: WestManagedCluster,
			Labels: map[string]string{
				"name": WestManagedCluster,
				"key1": "value1",
			},
		},
	}
	eastCluster = &spokeClusterV1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: EastManagedCluster,
			Labels: map[string]string{
				"name": EastManagedCluster,
				"key1": "value2",
			},
		},
	}

	clusters = []*spokeClusterV1.ManagedCluster{westCluster, eastCluster}

	eastManagedClusterNamespace = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: EastManagedCluster},
	}

	westManagedClusterNamespace = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: WestManagedCluster},
	}

	appNamespace = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: AVRNamespaceName},
	}
)

var safeToProceed bool

// FakeProgressCallback of function type
func FakeProgressCallback(avrName string, done bool) {
	safeToProceed = done
}

type FakePVDownloader struct{}

func (s FakePVDownloader) DownloadPVs(ctx context.Context, r client.Reader,
	objStoreGetter controllers.ObjectStoreGetter, s3Endpoint, s3Region string,
	s3SecretName types.NamespacedName, callerTag string,
	s3Bucket string) ([]corev1.PersistentVolume, error) {
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

func createPlacementRule(name, namespace string) *plrv1.PlacementRule {
	namereq := metav1.LabelSelectorRequirement{}
	namereq.Key = "key1"
	namereq.Operator = metav1.LabelSelectorOpIn

	namereq.Values = []string{"value1"}
	labelSelector := &metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{namereq},
	}

	placementRule := &plrv1.PlacementRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: plrv1.PlacementRuleSpec{
			GenericPlacementFields: plrv1.GenericPlacementFields{
				ClusterSelector: labelSelector,
			},
			SchedulerName: "Ramen",
		},
	}

	err := k8sClient.Create(context.TODO(), placementRule)
	Expect(err).NotTo(HaveOccurred())

	return placementRule
}

func updateClonedPlacementRuleStatus(
	userPlRule *plrv1.PlacementRule,
	avr *rmn.ApplicationVolumeReplication,
	clusterName string) {
	decision := plrv1.PlacementDecision{
		ClusterName:      clusterName,
		ClusterNamespace: "test",
	}

	clonedPlRuleLookupKey := types.NamespacedName{
		Name:      fmt.Sprintf("%s-%s", userPlRule.Name, avr.Name),
		Namespace: userPlRule.Namespace,
	}

	clonedPlRule := &plrv1.PlacementRule{}

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), clonedPlRuleLookupKey, clonedPlRule)

		return err == nil
	}, timeout, interval).Should(BeTrue(), "failed to get cloned PlacementRule")

	plDecisions := []plrv1.PlacementDecision{decision}
	clonedPlRule.Status = plrv1.PlacementRuleStatus{
		Decisions: plDecisions,
	}

	err := k8sClient.Status().Update(context.TODO(), clonedPlRule)
	Expect(err).NotTo(HaveOccurred())
}

func createAVR(name, namespace string) *rmn.ApplicationVolumeReplication {
	avr := &rmn.ApplicationVolumeReplication{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: rmn.ApplicationVolumeReplicationSpec{
			Placement: &plrv1.Placement{
				PlacementRef: &corev1.ObjectReference{
					Name: "sub-placement-rule",
					Kind: "PlacementRule",
				},
			},
			DRClusterPeersRef: rmn.DRClusterPeersReference{
				Name:      DRClusterPeersName,
				Namespace: namespace,
			},
			PVCSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"appclass":    "gold",
					"environment": "dev.AZ1",
				},
			},
			S3Endpoint:   "path/to/s3Endpoint",
			S3SecretName: "SecretName",
		},
	}
	Expect(k8sClient.Create(context.TODO(), avr)).Should(Succeed())

	return avr
}

func setAVRSpecExpectationTo(avr *rmn.ApplicationVolumeReplication,
	s3Endpoint string, action rmn.DRAction, preferredCluster, failoverCluster string) {
	latestAVR := getLatestAVR(avr.Name, avr.Namespace)
	if s3Endpoint != "" {
		latestAVR.Spec.S3Endpoint = s3Endpoint
	}

	latestAVR.Spec.Action = action
	latestAVR.Spec.PreferredCluster = preferredCluster
	latestAVR.Spec.FailoverCluster = failoverCluster
	err := k8sClient.Update(context.TODO(), latestAVR)
	Expect(err).NotTo(HaveOccurred())

	Eventually(func() bool {
		latestAVR := getLatestAVR(avr.Name, avr.Namespace)
		if latestAVR.Spec.Action != "" {
			return latestAVR.Spec.Action == action
		}

		return false
	}, timeout, interval).Should(BeTrue(), "failed to update AVR DR action on time")
}

func getLatestAVR(name, namespace string) *rmn.ApplicationVolumeReplication {
	avrLookupKey := types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}
	latestAVR := &rmn.ApplicationVolumeReplication{}
	err := k8sClient.Get(context.TODO(), avrLookupKey, latestAVR)
	Expect(err).NotTo(HaveOccurred())

	return latestAVR
}

func createNamespaces() {
	eastNamespaceLookupKey := types.NamespacedName{Name: eastManagedClusterNamespace.Name}
	eastNamespaceObj := &corev1.Namespace{}

	err := k8sClient.Get(context.TODO(), eastNamespaceLookupKey, eastNamespaceObj)
	if err != nil {
		Expect(k8sClient.Create(context.TODO(), eastManagedClusterNamespace)).NotTo(HaveOccurred(),
			"failed to create east managed cluster namespace")
	}

	westNamespaceLookupKey := types.NamespacedName{Name: westManagedClusterNamespace.Name}
	westNamespaceObj := &corev1.Namespace{}

	err = k8sClient.Get(context.TODO(), westNamespaceLookupKey, westNamespaceObj)
	if err != nil {
		Expect(k8sClient.Create(context.TODO(), westManagedClusterNamespace)).NotTo(HaveOccurred(),
			"failed to create west managed cluster namespace")
	}

	appNamespaceLookupKey := types.NamespacedName{Name: appNamespace.Name}
	appNamespaceObj := &corev1.Namespace{}

	err = k8sClient.Get(context.TODO(), appNamespaceLookupKey, appNamespaceObj)
	if err != nil {
		Expect(k8sClient.Create(context.TODO(), appNamespace)).NotTo(HaveOccurred(), "failed to create app namespace")
	}
}

func createManagedClusters() {
	for _, cl := range clusters {
		mcLookupKey := types.NamespacedName{Name: cl.Name}
		mcObj := &spokeClusterV1.ManagedCluster{}

		err := k8sClient.Get(context.TODO(), mcLookupKey, mcObj)
		if err != nil {
			clinstance := cl.DeepCopy()

			err := k8sClient.Create(context.TODO(), clinstance)
			Expect(err).NotTo(HaveOccurred())
		}
	}
}

func createDRClusterPeers(name, namespace string, clusters []string) {
	clusterPeers := &rmn.DRClusterPeers{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: rmn.DRClusterPeersSpec{
			ClusterNames:      clusters,
			ReplicationPolicy: "async",
		},
	}

	err := k8sClient.Create(context.TODO(), clusterPeers)
	Expect(err).NotTo(HaveOccurred())
}

func updateManifestWorkStatus(clusterNamespace, mwType string) {
	manifestLookupKey := types.NamespacedName{
		Name:      controllers.BuildManifestWorkName(AVRName, AVRNamespaceName, mwType),
		Namespace: clusterNamespace,
	}
	createdManifest := &ocmworkv1.ManifestWork{}

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), manifestLookupKey, createdManifest)

		return err == nil
	}, timeout, interval).Should(BeTrue(), fmt.Sprintf("failed to wait for manifest creation for type %s", mwType))

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

	err := k8sClient.Status().Update(context.TODO(), createdManifest)
	Expect(err).NotTo(HaveOccurred())

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), manifestLookupKey, createdManifest)

		return err == nil && len(createdManifest.Status.Conditions) != 0
	}, timeout, interval).Should(BeTrue(), "failed to wait for PV manifest condition type to change to 'Applied'")
}

func InitialDeployment(namespace, placementName, homeCluster string) (*plrv1.PlacementRule,
	*rmn.ApplicationVolumeReplication) {
	createNamespaces()

	createManagedClusters()
	createDRClusterPeers(DRClusterPeersName, AVRNamespaceName,
		[]string{EastManagedCluster, WestManagedCluster})

	placementRule := createPlacementRule(placementName, namespace)
	avr := createAVR(AVRName, AVRNamespaceName)

	return placementRule, avr
}

func verifyVRGManifestWorkCreatedAsExpected(managedCluster string, state volrep.ReplicationState) {
	// 4.0 Get the VRG Roles ManifestWork. The work is created per managed cluster in the AVR reconciler
	vrgManifestLookupKey := types.NamespacedName{
		Name:      "ramendr-vrg-roles",
		Namespace: managedCluster,
	}
	createdVRGRolesManifest := &ocmworkv1.ManifestWork{}

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), vrgManifestLookupKey, createdVRGRolesManifest)

		return err == nil
	}, timeout, interval).Should(BeTrue())

	Expect(len(createdVRGRolesManifest.Spec.Workload.Manifests)).To(Equal(2))

	vrgClusterRoleManifest := createdVRGRolesManifest.Spec.Workload.Manifests[0]
	Expect(vrgClusterRoleManifest).ToNot(BeNil())

	vrgClusterRole := &rbacv1.ClusterRole{}
	err := yaml.Unmarshal(vrgClusterRoleManifest.RawExtension.Raw, &vrgClusterRole)
	Expect(err).NotTo(HaveOccurred())

	vrgClusterRoleBindingManifest := createdVRGRolesManifest.Spec.Workload.Manifests[1]
	Expect(vrgClusterRoleBindingManifest).ToNot(BeNil())

	vrgClusterRoleBinding := &rbacv1.ClusterRoleBinding{}

	err = yaml.Unmarshal(vrgClusterRoleManifest.RawExtension.Raw, &vrgClusterRoleBinding)
	Expect(err).NotTo(HaveOccurred())

	manifestLookupKey := types.NamespacedName{
		Name:      controllers.BuildManifestWorkName(AVRName, AVRNamespaceName, "vrg"),
		Namespace: managedCluster,
	}
	createdManifest := &ocmworkv1.ManifestWork{}

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), manifestLookupKey, createdManifest)

		return err == nil
	}, timeout, interval).Should(BeTrue())

	// 5.1 verify that VRG CR has been created and added to the ManifestWork
	Expect(len(createdManifest.Spec.Workload.Manifests)).To(Equal(1))
	vrgClientManifest := createdManifest.Spec.Workload.Manifests[0]

	Expect(vrgClientManifest).ToNot(BeNil())

	vrg := &rmn.VolumeReplicationGroup{}

	err = yaml.Unmarshal(vrgClientManifest.RawExtension.Raw, &vrg)
	Expect(err).NotTo(HaveOccurred())
	Expect(vrg.Name).Should(Equal(AVRName))
	Expect(vrg.Spec.PVCSelector.MatchLabels["appclass"]).Should(Equal("gold"))
	Expect(vrg.Spec.ReplicationState).Should(Equal(state))
}

func getManifestWorkCount(homeClusterNamespace string) int {
	manifestWorkList := &ocmworkv1.ManifestWorkList{}
	listOptions := &client.ListOptions{Namespace: homeClusterNamespace}

	Expect(k8sClient.List(context.TODO(), manifestWorkList, listOptions)).NotTo(HaveOccurred())

	return len(manifestWorkList.Items)
}

func verifyUserPlacementRuleDecision(name, namespace, homeCluster string) {
	usrPlRuleLookupKey := types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}

	usrPlRule := &plrv1.PlacementRule{}

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), usrPlRuleLookupKey, usrPlRule)

		return err == nil && usrPlRule.Status.Decisions[0].ClusterName == homeCluster
	}, timeout, interval).Should(BeTrue())
}

func verifyAVRStatusExpectation(homeCluster, peerCluster, preferredHomeCluster string,
	drState rmn.DRState) {
	avrLookupKey := types.NamespacedName{
		Name:      AVRName,
		Namespace: AVRNamespaceName,
	}

	updatedAVR := &rmn.ApplicationVolumeReplication{}

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), avrLookupKey, updatedAVR)

		if d := updatedAVR.Status.Decision; err == nil && d != (rmn.PlacementDecision{}) {
			return d.HomeCluster == homeCluster
		}

		return false
	}, timeout, interval).Should(BeTrue(), "failed waiting for an updated AVR")

	// 7.0 check that the home and peer clusters have been selected.
	Expect(updatedAVR.Status.Decision.HomeCluster).Should(Equal(homeCluster))
	Expect(updatedAVR.Status.Decision.PeerCluster).Should(Equal(peerCluster))
	Expect(updatedAVR.Status.Decision.PreferredHomeCluster).Should(Equal(preferredHomeCluster))
	Expect(updatedAVR.Status.LastKnownDRState).Should(Equal(drState))
}

func waitForCompletion() {
	Eventually(func() bool {
		return safeToProceed
	}, timeout*2, interval).Should(BeTrue(), "failed to wait for hook to be called")
}

// +kubebuilder:docs-gen:collapse=Imports

var _ = Describe("ApplicationVolumeReplication Reconciler", func() {
	Context("ApplicationVolumeReplication Reconciler", func() {
		userPlacementRule := &plrv1.PlacementRule{}
		avr := &rmn.ApplicationVolumeReplication{}

		When("An Application is deployed for the first time", func() {
			It("Should deploy to EastManagedCluster", func() {
				By("Initial Deployment")
				safeToProceed = false
				userPlacementRule, avr = InitialDeployment(AVRNamespaceName, "sub-placement-rule", EastManagedCluster)
				updateClonedPlacementRuleStatus(userPlacementRule, avr, EastManagedCluster)
				verifyVRGManifestWorkCreatedAsExpected(EastManagedCluster, volrep.Primary)
				updateManifestWorkStatus(EastManagedCluster, "vrg")
				verifyUserPlacementRuleDecision(userPlacementRule.Name, userPlacementRule.Namespace, EastManagedCluster)
				verifyAVRStatusExpectation(EastManagedCluster, WestManagedCluster, "", rmn.Initial)
				waitForCompletion()
			})
		})
		When("DRAction changes to failover", func() {
			It("Should failover to Secondary (WestManagedCluster)", func() {
				// ----------------------------- FAILOVER TO SECONDARY --------------------------------------
				By("\n\n*** Failover - 1\n\n")
				safeToProceed = false
				updateClonedPlacementRuleStatus(userPlacementRule, avr, WestManagedCluster)
				setAVRSpecExpectationTo(avr, "", rmn.ActionFailover, "", "")
				updateManifestWorkStatus(WestManagedCluster, "pv")
				updateManifestWorkStatus(WestManagedCluster, "vrg")
				verifyUserPlacementRuleDecision(userPlacementRule.Name, userPlacementRule.Namespace, WestManagedCluster)
				verifyAVRStatusExpectation(WestManagedCluster, EastManagedCluster, EastManagedCluster, rmn.FailedOver)
				verifyVRGManifestWorkCreatedAsExpected(WestManagedCluster, volrep.Primary)
				verifyVRGManifestWorkCreatedAsExpected(EastManagedCluster, volrep.Secondary)
				Expect(getManifestWorkCount(WestManagedCluster)).Should(Equal(3)) // MW for VRG+ROLES+PV
				Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(2)) // MW for ROLES
				waitForCompletion()
			})
		})
		When("AVR Reconciler is called to failover the second time to the same cluster", func() {
			It("Should NOT do anything", func() {
				By("\n\n*** Failover - 2: NOOP\n\n")
				safeToProceed = false
				updateClonedPlacementRuleStatus(userPlacementRule, avr, WestManagedCluster)
				// Force the reconciler to execute by changing one of the avr.Spec fields. We chose s3Endpoint
				setAVRSpecExpectationTo(avr, "newS3Endpoint", rmn.ActionFailover, "", "")
				verifyUserPlacementRuleDecision(userPlacementRule.Name, userPlacementRule.Namespace, WestManagedCluster)
				verifyAVRStatusExpectation(WestManagedCluster, EastManagedCluster, EastManagedCluster, rmn.FailedOver)
				Expect(getManifestWorkCount(WestManagedCluster)).Should(Equal(3)) // MW for VRG+ROLES+PV
				Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(2)) // MW for ROLES
				waitForCompletion()
			})
		})
		When("DRAction is set to failback", func() {
			It("Should failback to Primary (EastManagedCluster)", func() {
				// ----------------------------- FAILBACK TO PRIMARY --------------------------------------
				By("\n\n*** Failback - 1\n\n")
				safeToProceed = false
				updateClonedPlacementRuleStatus(userPlacementRule, avr, EastManagedCluster)
				setAVRSpecExpectationTo(avr, "", rmn.ActionFailback, "", "")
				updateManifestWorkStatus(EastManagedCluster, "pv")
				updateManifestWorkStatus(EastManagedCluster, "vrg")
				verifyUserPlacementRuleDecision(userPlacementRule.Name, userPlacementRule.Namespace, EastManagedCluster)
				verifyAVRStatusExpectation(EastManagedCluster, WestManagedCluster, "", rmn.FailedBack)
				verifyVRGManifestWorkCreatedAsExpected(EastManagedCluster, volrep.Primary)
				verifyVRGManifestWorkCreatedAsExpected(WestManagedCluster, volrep.Secondary)
				Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(3)) // MW for VRG+ROLES+PV
				Expect(getManifestWorkCount(WestManagedCluster)).Should(Equal(2)) // MW for ROLES
				waitForCompletion()
			})
		})
		When("AVR Reconciler is called to failback for the second time to the same cluster", func() {
			It("Should NOT do anything", func() {
				By("\n\n*** Failback - 2: NOOP\n\n")
				safeToProceed = false
				updateClonedPlacementRuleStatus(userPlacementRule, avr, EastManagedCluster)
				// Force the reconciler to execute by changing one of the avr.Spec fields. It is easier to change s3Endpoint
				setAVRSpecExpectationTo(avr, "path/to/s3Endpoint", rmn.ActionFailback, "", "")
				verifyUserPlacementRuleDecision(userPlacementRule.Name, userPlacementRule.Namespace, EastManagedCluster)
				verifyAVRStatusExpectation(EastManagedCluster, WestManagedCluster, "", rmn.FailedBack)
				Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(3)) // MW for VRG+ROLES+PV
				Expect(getManifestWorkCount(WestManagedCluster)).Should(Equal(2)) // MW for ROLES
				waitForCompletion()
			})
		})
		When("DRAction is changed to failover after failback", func() {
			It("Should failover again to Secondary (WestManagedCluster)", func() {
				// ----------------------------- FAILOVER TO SECONDARY --------------------------------------
				By("\n\n*** Failover - 3\n\n")
				safeToProceed = false
				updateClonedPlacementRuleStatus(userPlacementRule, avr, WestManagedCluster)
				setAVRSpecExpectationTo(avr, "", rmn.ActionFailover, EastManagedCluster, WestManagedCluster)
				updateManifestWorkStatus(WestManagedCluster, "pv")
				updateManifestWorkStatus(WestManagedCluster, "vrg")
				verifyUserPlacementRuleDecision(userPlacementRule.Name, userPlacementRule.Namespace, WestManagedCluster)
				verifyAVRStatusExpectation(WestManagedCluster, EastManagedCluster, EastManagedCluster, rmn.FailedOver)
				verifyVRGManifestWorkCreatedAsExpected(WestManagedCluster, volrep.Primary)
				verifyVRGManifestWorkCreatedAsExpected(EastManagedCluster, volrep.Secondary)
				Expect(getManifestWorkCount(WestManagedCluster)).Should(Equal(3)) // MW for VRG+ROLES+PV
				Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(2)) // MW for ROLES
				waitForCompletion()
			})
		})
		When("DRAction is changed to failover after a failover", func() {
			It("Should failover to Primary (EastManagedCluster)", func() {
				// ----------------------------- FAILOVER TO PREFERREDCLUSTER --------------------------------------
				By("\n\n*** Failover - 3\n\n")
				safeToProceed = false
				updateClonedPlacementRuleStatus(userPlacementRule, avr, EastManagedCluster)
				// Force the reconciler to execute by changing one of the avr.Spec fields. It is easier to change s3Endpoint
				setAVRSpecExpectationTo(avr, "path/to/s3Endpoint1", rmn.ActionFailover, WestManagedCluster, EastManagedCluster)
				updateManifestWorkStatus(EastManagedCluster, "pv")
				updateManifestWorkStatus(EastManagedCluster, "vrg")
				verifyUserPlacementRuleDecision(userPlacementRule.Name, userPlacementRule.Namespace, EastManagedCluster)
				verifyAVRStatusExpectation(EastManagedCluster, WestManagedCluster, "", rmn.FailedOver)
				verifyVRGManifestWorkCreatedAsExpected(EastManagedCluster, volrep.Primary)
				verifyVRGManifestWorkCreatedAsExpected(WestManagedCluster, volrep.Secondary)
				Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(3)) // MW for VRG+ROLES+PV
				Expect(getManifestWorkCount(WestManagedCluster)).Should(Equal(2)) // MW for ROLES
				waitForCompletion()
			})
		})
		When("DRAction is changed to failover after failover", func() {
			It("Should failover to secondary (WestManagedCluster)", func() {
				// ----------------------------- FAILOVER TO SECONDARY --------------------------------------
				By("\n\n*** Failover - 4\n\n")
				safeToProceed = false
				updateClonedPlacementRuleStatus(userPlacementRule, avr, WestManagedCluster)
				setAVRSpecExpectationTo(avr, "", rmn.ActionFailover, EastManagedCluster, WestManagedCluster)
				updateManifestWorkStatus(WestManagedCluster, "pv")
				updateManifestWorkStatus(WestManagedCluster, "vrg")
				verifyUserPlacementRuleDecision(userPlacementRule.Name, userPlacementRule.Namespace, WestManagedCluster)
				verifyAVRStatusExpectation(WestManagedCluster, EastManagedCluster, EastManagedCluster, rmn.FailedOver)
				verifyVRGManifestWorkCreatedAsExpected(WestManagedCluster, volrep.Primary)
				verifyVRGManifestWorkCreatedAsExpected(EastManagedCluster, volrep.Secondary)
				Expect(getManifestWorkCount(WestManagedCluster)).Should(Equal(3)) // MW for VRG+ROLES+PV
				Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(2)) // MW for ROLES
				waitForCompletion()
			})
		})
		When("DRAction is changed to failback after a sequence of failovers", func() {
			It("Should failback to Primary (EastManagedCluster)", func() {
				// ----------------------------- FAILBACK TO PREFERREDCLUSTER --------------------------------------
				By("\n\n*** Failback - 4\n\n")
				safeToProceed = false
				updateClonedPlacementRuleStatus(userPlacementRule, avr, EastManagedCluster)
				setAVRSpecExpectationTo(avr, "", rmn.ActionFailback, EastManagedCluster, WestManagedCluster)
				updateManifestWorkStatus(EastManagedCluster, "pv")
				updateManifestWorkStatus(EastManagedCluster, "vrg")
				verifyUserPlacementRuleDecision(userPlacementRule.Name, userPlacementRule.Namespace, EastManagedCluster)
				verifyAVRStatusExpectation(EastManagedCluster, WestManagedCluster, "", rmn.FailedBack)
				verifyVRGManifestWorkCreatedAsExpected(EastManagedCluster, volrep.Primary)
				verifyVRGManifestWorkCreatedAsExpected(WestManagedCluster, volrep.Secondary)
				Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(3)) // MW for VRG+ROLES+PV
				Expect(getManifestWorkCount(WestManagedCluster)).Should(Equal(2)) // MW for ROLES
				waitForCompletion()
			})
		})
	})
	Context("IsManifestInAppliedState checks ManifestWork with single timestamp", func() {
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

	Context("IsManifestInAppliedState checks ManifestWork with multiple timestamps", func() {
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

	Context("IsManifestInAppliedState checks ManifestWork with no timestamps", func() {
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
