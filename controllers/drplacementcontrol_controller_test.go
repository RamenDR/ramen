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
	"encoding/json"
	"fmt"
	"time"

	"github.com/ghodss/yaml"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spokeClusterV1 "github.com/open-cluster-management/api/cluster/v1"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	plrv1 "github.com/open-cluster-management/multicloud-operators-placementrule/pkg/apis/apps/v1"
	rmn "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers"
	rmnutil "github.com/ramendr/ramen/controllers/util"
	fndv2 "github.com/tjanssen3/multicloud-operators-foundation/v2/pkg/apis/view/v1beta1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
)

const (
	DRPCName           = "app-volume-replication-test"
	DRPCNamespaceName  = "app-namespace"
	EastManagedCluster = "east-cluster"
	WestManagedCluster = "west-cluster"
	DRPolicyName       = "my-dr-peers"

	timeout       = time.Second * 10
	interval      = time.Millisecond * 250
	updateRetries = 5
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
		ObjectMeta: metav1.ObjectMeta{Name: DRPCNamespaceName},
	}

	schedulingInterval = "1h"
)

var safeToProceed bool

// FakeProgressCallback of function type
func FakeProgressCallback(drpcName string) {
	safeToProceed = true
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

func createManagedClusterView(namespace string) *fndv2.ManagedClusterView {
	mcvName := controllers.BuildManagedClusterViewName(DRPCName, DRPCNamespaceName, "vrg")
	mcv := &fndv2.ManagedClusterView{

		ObjectMeta: metav1.ObjectMeta{
			Name:      mcvName,
			Namespace: namespace,
		},
		Spec: fndv2.ViewSpec{
			Scope: fndv2.ViewScope{
				// intentionally blank
			},
		},
	}

	err := k8sClient.Create(context.TODO(), mcv)
	if err != nil {
		if errors.IsAlreadyExists(err) {
			err = k8sClient.Get(context.TODO(), types.NamespacedName{Name: mcvName, Namespace: namespace}, mcv)
		}
	}

	Expect(err).NotTo(HaveOccurred())

	return mcv
}

// create a VRG, then fake ManagedClusterView results
func updateManagedClusterViewWithVRG(mcv *fndv2.ManagedClusterView, replicationState rmn.ReplicationState) {
	state := rmn.PrimaryState
	if replicationState != rmn.Primary {
		state = rmn.SecondaryState
	}

	vrg := &rmn.VolumeReplicationGroup{
		TypeMeta:   metav1.TypeMeta{Kind: "VolumeReplicationGroup", APIVersion: "ramendr.openshift.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: DRPCName, Namespace: DRPCNamespaceName},
		Spec: rmn.VolumeReplicationGroupSpec{
			SchedulingInterval: schedulingInterval,
			ReplicationState:   replicationState,
			PVCSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"appclass":    "gold",
					"environment": "dev.AZ1",
				},
			},
			S3Endpoint:   "path/to/s3Endpoint",
			S3SecretName: "SecretName",
		},
		Status: rmn.VolumeReplicationGroupStatus{
			State: state,
		},
	}

	updateManagedClusterView(mcv, vrg, metav1.ConditionTrue)
}

// take an existing ManagedClusterView and apply the given resource to it as though it were "found"
func updateManagedClusterView(mcv *fndv2.ManagedClusterView, resource interface{}, status metav1.ConditionStatus) {
	// get raw bytes
	objJSON, err := json.Marshal(resource)

	Expect(err).NotTo(HaveOccurred())

	// update Status, Result fields
	reason := fndv2.ReasonGetResource
	if status != metav1.ConditionTrue {
		reason = fndv2.ReasonGetResourceFailed
	}

	mcv.Status = fndv2.ViewStatus{
		Conditions: []metav1.Condition{
			{
				Type:               fndv2.ConditionViewProcessing,
				LastTransitionTime: metav1.Time{Time: time.Now().Local()},
				Status:             status,
				Reason:             reason,
			},
		},
		Result: runtime.RawExtension{
			Raw: objJSON,
		},
	}

	err = k8sClient.Status().Update(context.TODO(), mcv)

	Expect(err).NotTo(HaveOccurred())
}

// take an existing ManagedClusterView and update status as NotFound
func updateManagedClusterViewStatusAsNotFound(mcv *fndv2.ManagedClusterView) {
	// update Status, Result fields
	mcv.Status = fndv2.ViewStatus{
		Conditions: []metav1.Condition{
			{
				Type:               fndv2.ConditionViewProcessing,
				LastTransitionTime: metav1.Time{Time: time.Now().Local()},
				Status:             metav1.ConditionFalse,
				Reason:             fndv2.ReasonGetResourceFailed,
			},
		},
	}

	err := k8sClient.Status().Update(context.TODO(), mcv)
	Expect(err).NotTo(HaveOccurred())

	mcvLatest := &fndv2.ManagedClusterView{}
	mcvLookupKey := types.NamespacedName{
		Name:      mcv.Name,
		Namespace: mcv.Namespace,
	}

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), mcvLookupKey, mcvLatest)
		if errors.IsNotFound(err) {
			return true
		}

		return err == nil && len(mcvLatest.Status.Conditions) > 0 &&
			mcvLatest.Status.Conditions[0].Reason == fndv2.ReasonGetResourceFailed
	}, timeout, interval).Should(BeTrue(),
		fmt.Sprintf("failed to wait for MCV.status.condition to change (%v)", mcvLatest))
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
			SchedulerName: "ramen",
		},
	}

	err := k8sClient.Create(context.TODO(), placementRule)
	Expect(err).NotTo(HaveOccurred())

	return placementRule
}

func updateClonedPlacementRuleStatus(
	userPlRule *plrv1.PlacementRule,
	drpc *rmn.DRPlacementControl,
	clusterName string) {
	decision := plrv1.PlacementDecision{
		ClusterName:      clusterName,
		ClusterNamespace: clusterName,
	}

	clonedPlRuleLookupKey := types.NamespacedName{
		Name:      fmt.Sprintf(controllers.ClonedPlacementRuleNameFormat, drpc.Name, drpc.Namespace),
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

func createDRPC(name, namespace string) *rmn.DRPlacementControl {
	drpc := &rmn.DRPlacementControl{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: rmn.DRPlacementControlSpec{
			PlacementRef: corev1.ObjectReference{
				Name: "sub-placement-rule",
				Kind: "PlacementRule",
			},
			DRPolicyRef: corev1.ObjectReference{
				Name: DRPolicyName,
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
	Expect(k8sClient.Create(context.TODO(), drpc)).Should(Succeed())

	return drpc
}

func deleteDRPC() {
	drpc := getLatestDRPC(DRPCName, DRPCNamespaceName)
	Expect(k8sClient.Delete(context.TODO(), drpc)).Should(Succeed())
}

func setDRPCSpecExpectationTo(drpc *rmn.DRPlacementControl,
	s3Endpoint string, action rmn.DRAction, preferredCluster string) {
	localRetries := 0
	for localRetries < updateRetries {
		latestDRPC := getLatestDRPC(drpc.Name, drpc.Namespace)
		if s3Endpoint != "" {
			latestDRPC.Spec.S3Endpoint = s3Endpoint
		}

		latestDRPC.Spec.Action = action
		latestDRPC.Spec.PreferredCluster = preferredCluster
		latestDRPC.Spec.FailoverCluster = WestManagedCluster
		err := k8sClient.Update(context.TODO(), latestDRPC)

		if errors.IsConflict(err) {
			localRetries++

			continue
		}

		Expect(err).NotTo(HaveOccurred())

		break
	}

	Eventually(func() bool {
		latestDRPC := getLatestDRPC(drpc.Name, drpc.Namespace)
		if latestDRPC.Spec.Action != "" {
			return latestDRPC.Spec.Action == action
		}

		return false
	}, timeout, interval).Should(BeTrue(), "failed to update DRPC DR action on time")
}

func getLatestDRPC(name, namespace string) *rmn.DRPlacementControl {
	drpcLookupKey := types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}
	latestDRPC := &rmn.DRPlacementControl{}
	err := k8sClient.Get(context.TODO(), drpcLookupKey, latestDRPC)
	Expect(err).NotTo(HaveOccurred())

	return latestDRPC
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

func createDRPolicy(name, namespace string, clusters []string) {
	clusterPeers := &rmn.DRPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: rmn.DRPolicySpec{
			ClusterNames:       clusters,
			SchedulingInterval: schedulingInterval,
		},
	}

	err := k8sClient.Create(context.TODO(), clusterPeers)
	Expect(err).NotTo(HaveOccurred())
}

func updateManifestWorkStatus(clusterNamespace, mwType, workType string) {
	manifestLookupKey := types.NamespacedName{
		Name:      rmnutil.ManifestWorkName(DRPCName, DRPCNamespaceName, mwType),
		Namespace: clusterNamespace,
	}
	createdManifest := &ocmworkv1.ManifestWork{}

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), manifestLookupKey, createdManifest)

		return err == nil
	}, timeout, interval).Should(BeTrue(),
		fmt.Sprintf("failed to wait for manifest creation for type %s cluster %s", mwType, clusterNamespace))

	timeOld := time.Now().Local()
	timeMostRecent := timeOld.Add(time.Second)
	pvManifestStatus := ocmworkv1.ManifestWorkStatus{
		Conditions: []metav1.Condition{
			{
				Type:               workType,
				LastTransitionTime: metav1.Time{Time: timeMostRecent},
				Status:             metav1.ConditionTrue,
				Reason:             "test",
			},
		},
	}
	// If work requires `applied`, add `available` as well to ensure a successful check
	if workType == ocmworkv1.WorkApplied {
		pvManifestStatus.Conditions = append(pvManifestStatus.Conditions, metav1.Condition{
			Type:               ocmworkv1.WorkAvailable,
			LastTransitionTime: metav1.Time{Time: timeMostRecent},
			Status:             metav1.ConditionTrue,
			Reason:             "test",
		})
	}

	createdManifest.Status = pvManifestStatus

	err := k8sClient.Status().Update(context.TODO(), createdManifest)
	if err != nil {
		// try again
		Expect(k8sClient.Get(context.TODO(), manifestLookupKey, createdManifest)).NotTo(HaveOccurred())
		createdManifest.Status = pvManifestStatus
		err = k8sClient.Status().Update(context.TODO(), createdManifest)
	}

	Expect(err).NotTo(HaveOccurred())

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), manifestLookupKey, createdManifest)

		return err == nil && len(createdManifest.Status.Conditions) != 0
	}, timeout, interval).Should(BeTrue(), "failed to wait for PV manifest condition type to change to 'Applied'")
}

func waitForVRGMWDeletion(clusterNamespace string) {
	manifestLookupKey := types.NamespacedName{
		Name:      rmnutil.ManifestWorkName(DRPCName, DRPCNamespaceName, "vrg"),
		Namespace: clusterNamespace,
	}
	createdManifest := &ocmworkv1.ManifestWork{}

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), manifestLookupKey, createdManifest)

		return errors.IsNotFound(err)
	}, timeout, interval).Should(BeTrue(), "failed to wait for manifest deletion for type vrg")
}

func InitialDeployment(namespace, placementName, homeCluster string) (*plrv1.PlacementRule,
	*rmn.DRPlacementControl) {
	createNamespaces()

	createManagedClusters()
	createDRPolicy(DRPolicyName, DRPCNamespaceName,
		[]string{EastManagedCluster, WestManagedCluster})

	placementRule := createPlacementRule(placementName, namespace)
	drpc := createDRPC(DRPCName, DRPCNamespaceName)

	return placementRule, drpc
}

func verifyVRGManifestWorkCreatedAsPrimary(managedCluster string) {
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
		Name:      rmnutil.ManifestWorkName(DRPCName, DRPCNamespaceName, "vrg"),
		Namespace: managedCluster,
	}
	mw := &ocmworkv1.ManifestWork{}

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), manifestLookupKey, mw)

		return err == nil
	}, timeout, interval).Should(BeTrue())

	Expect(len(mw.Spec.Workload.Manifests)).To(Equal(1))

	vrgClientManifest := mw.Spec.Workload.Manifests[0]

	Expect(vrgClientManifest).ToNot(BeNil())

	vrg := &rmn.VolumeReplicationGroup{}

	err = yaml.Unmarshal(vrgClientManifest.RawExtension.Raw, &vrg)
	Expect(err).NotTo(HaveOccurred())
	Expect(vrg.Name).Should(Equal(DRPCName))
	Expect(vrg.Spec.PVCSelector.MatchLabels["appclass"]).Should(Equal("gold"))
	Expect(vrg.Spec.ReplicationState).Should(Equal(rmn.Primary))
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

		return err == nil && len(usrPlRule.Status.Decisions) > 0 &&
			usrPlRule.Status.Decisions[0].ClusterName == homeCluster
	}, timeout, interval).Should(BeTrue())

	Expect(usrPlRule.ObjectMeta.Annotations[rmnutil.DRPCNameAnnotation]).Should(Equal(DRPCName))
	Expect(usrPlRule.ObjectMeta.Annotations[rmnutil.DRPCNamespaceAnnotation]).Should(Equal(DRPCNamespaceName))
}

func verifyUserPlacementRuleDecisionUnchanged(name, namespace, homeCluster string) {
	usrPlRuleLookupKey := types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}

	usrPlRule := &plrv1.PlacementRule{}

	Consistently(func() bool {
		err := k8sClient.Get(context.TODO(), usrPlRuleLookupKey, usrPlRule)

		return err == nil && usrPlRule.Status.Decisions[0].ClusterName == homeCluster
	}, timeout, interval).Should(BeTrue())

	Expect(usrPlRule.ObjectMeta.Annotations[rmnutil.DRPCNameAnnotation]).Should(Equal(DRPCName))
	Expect(usrPlRule.ObjectMeta.Annotations[rmnutil.DRPCNamespaceAnnotation]).Should(Equal(DRPCNamespaceName))
}

func verifyDRPCStatusPreferredClusterExpectation(drState rmn.DRState) {
	drpcLookupKey := types.NamespacedName{
		Name:      DRPCName,
		Namespace: DRPCNamespaceName,
	}

	updatedDRPC := &rmn.DRPlacementControl{}

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), drpcLookupKey, updatedDRPC)

		if d := updatedDRPC.Status.PreferredDecision; err == nil && d != (plrv1.PlacementDecision{}) {
			return d.ClusterName == EastManagedCluster && updatedDRPC.Status.LastKnownDRState == drState
		}

		return false
	}, timeout, interval).Should(BeTrue(), fmt.Sprintf("failed waiting for an updated DRPC. State %v", drState))

	Expect(updatedDRPC.Status.PreferredDecision.ClusterName).Should(Equal(EastManagedCluster))
	Expect(updatedDRPC.Status.LastKnownDRState).Should(Equal(drState))
}

func waitForCompletion() {
	Eventually(func() bool {
		return safeToProceed
	}, timeout*2, interval).Should(BeTrue(), "failed to wait for hook to be called")
}

// +kubebuilder:docs-gen:collapse=Imports

var _ = Describe("DRPlacementControl Reconciler", func() {
	Context("DRPlacementControl Reconciler", func() {
		userPlacementRule := &plrv1.PlacementRule{}
		drpc := &rmn.DRPlacementControl{}

		When("An Application is deployed for the first time", func() {
			It("Should deploy to EastManagedCluster", func() {
				By("Initial Deployment")
				safeToProceed = false
				userPlacementRule, drpc = InitialDeployment(DRPCNamespaceName, "sub-placement-rule", EastManagedCluster)
				mcv := createManagedClusterView(EastManagedCluster)
				updateManagedClusterViewWithVRG(mcv, rmn.Primary)

				updateClonedPlacementRuleStatus(userPlacementRule, drpc, EastManagedCluster)
				verifyVRGManifestWorkCreatedAsPrimary(EastManagedCluster)
				updateManifestWorkStatus(EastManagedCluster, "vrg", ocmworkv1.WorkApplied)
				verifyUserPlacementRuleDecision(userPlacementRule.Name, userPlacementRule.Namespace, EastManagedCluster)
				verifyDRPCStatusPreferredClusterExpectation(rmn.Initial)
				Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(2)) // MWs for VRG and ROLES
				waitForCompletion()
			})
		})
		When("DRAction changes to failover", func() {
			It("Should not failover to Secondary (WestManagedCluster) till PV manifest is applied", func() {
				By("\n\n*** Failover - 1\n\n")
				updateClonedPlacementRuleStatus(userPlacementRule, drpc, WestManagedCluster)
				setDRPCSpecExpectationTo(drpc, "", rmn.ActionFailover, "")
				verifyUserPlacementRuleDecisionUnchanged(userPlacementRule.Name, userPlacementRule.Namespace, EastManagedCluster)
				Expect(getManifestWorkCount(WestManagedCluster)).Should(Equal(2)) // MWs for PV and PV ROLE
			})
			It("Should failover to Secondary (WestManagedCluster)", func() {
				// ----------------------------- FAILOVER TO SECONDARY (WestManagedCluster) --------------------------------------
				By("\n\n*** Failover - 1\n\n")
				safeToProceed = false

				mcvWest := createManagedClusterView(WestManagedCluster)
				updateManagedClusterViewWithVRG(mcvWest, rmn.Primary)
				mcvEast := createManagedClusterView(EastManagedCluster)
				updateManagedClusterViewWithVRG(mcvEast, rmn.Secondary)

				updateManifestWorkStatus(WestManagedCluster, "pv", ocmworkv1.WorkApplied)
				updateManifestWorkStatus(WestManagedCluster, "vrg", ocmworkv1.WorkApplied)
				verifyUserPlacementRuleDecision(userPlacementRule.Name, userPlacementRule.Namespace, WestManagedCluster)
				verifyDRPCStatusPreferredClusterExpectation(rmn.FailedOver)
				verifyVRGManifestWorkCreatedAsPrimary(WestManagedCluster)
				Expect(getManifestWorkCount(WestManagedCluster)).Should(Equal(4)) // MWs for VRG+ROLES+PVs
				waitForVRGMWDeletion(EastManagedCluster)
				updateManagedClusterViewStatusAsNotFound(mcvEast)
				// tickle the DRPC reconciler, should be removed once we watch for MCV resource updates
				setDRPCSpecExpectationTo(drpc, "newS3Endpoint-1", rmn.ActionFailover, "")
				Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(1)) // MW for VRG ROLE only
				waitForCompletion()
			})
		})
		When("DRPC Reconciler is called to failover the second time to the same cluster", func() {
			It("Should NOT do anything", func() {
				By("\n\n*** Failover - 2: NOOP\n\n")
				safeToProceed = false

				mcvWest := createManagedClusterView(WestManagedCluster)
				updateManagedClusterViewWithVRG(mcvWest, rmn.Primary)
				mcvEast := createManagedClusterView(EastManagedCluster)

				updateClonedPlacementRuleStatus(userPlacementRule, drpc, WestManagedCluster)
				// Force the reconciler to execute by changing one of the drpc.Spec fields. We chose s3Endpoint
				setDRPCSpecExpectationTo(drpc, "newS3Endpoint-2", rmn.ActionFailover, "")
				verifyUserPlacementRuleDecision(userPlacementRule.Name, userPlacementRule.Namespace, WestManagedCluster)
				verifyDRPCStatusPreferredClusterExpectation(rmn.FailedOver)
				Expect(getManifestWorkCount(WestManagedCluster)).Should(Equal(4)) // MWs for VRG+ROLES+PVs
				waitForVRGMWDeletion(EastManagedCluster)
				updateManagedClusterViewStatusAsNotFound(mcvEast)
				Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(1)) // MWs for VRG ROLE only
				waitForCompletion()
			})
		})
		When("DRAction is set to failback", func() {
			It("Should failback to Primary (EastManagedCluster)", func() {
				// ----------------------------- FAILBACK TO PRIMARY --------------------------------------
				By("\n\n*** Failback - 1\n\n")
				safeToProceed = false

				mcvEast := createManagedClusterView(EastManagedCluster)
				mcvWest := createManagedClusterView(WestManagedCluster)

				updateManagedClusterViewWithVRG(mcvEast, rmn.Secondary)
				updateManagedClusterViewWithVRG(mcvWest, rmn.Secondary)

				updateManifestWorkStatus(WestManagedCluster, "vrg", ocmworkv1.WorkProgressing)
				updateClonedPlacementRuleStatus(userPlacementRule, drpc, EastManagedCluster)
				setDRPCSpecExpectationTo(drpc, "", rmn.ActionFailback, "")

				updateManagedClusterViewWithVRG(mcvEast, rmn.Secondary)
				updateManagedClusterViewWithVRG(mcvWest, rmn.Secondary)

				updateManifestWorkStatus(EastManagedCluster, "pv", ocmworkv1.WorkApplied)
				updateManifestWorkStatus(EastManagedCluster, "vrg", ocmworkv1.WorkApplied)

				verifyUserPlacementRuleDecision(userPlacementRule.Name, userPlacementRule.Namespace, EastManagedCluster)
				verifyDRPCStatusPreferredClusterExpectation(rmn.FailedBack)
				verifyVRGManifestWorkCreatedAsPrimary(EastManagedCluster)

				Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(4)) // MWs for VRG+ROLES+PVs
				waitForVRGMWDeletion(WestManagedCluster)

				updateManagedClusterViewStatusAsNotFound(mcvWest)
				Expect(getManifestWorkCount(WestManagedCluster)).Should(Equal(2)) // Roles MWs
				waitForCompletion()
			})
		})
		When("DRPC Reconciler is called to failback for the second time to the same cluster", func() {
			It("Should NOT do anything", func() {
				By("\n\n*** Failback - 2: NOOP\n\n")
				safeToProceed = false

				mcvEast := createManagedClusterView(EastManagedCluster)
				mcvWest := createManagedClusterView(WestManagedCluster)

				updateClonedPlacementRuleStatus(userPlacementRule, drpc, EastManagedCluster)
				// Force the reconciler to execute by changing one of the drpc.Spec fields. It is easier to change s3Endpoint
				setDRPCSpecExpectationTo(drpc, "path/to/s3Endpoint", rmn.ActionFailback, "")
				updateManagedClusterViewWithVRG(mcvEast, rmn.Secondary)
				updateManagedClusterViewWithVRG(mcvWest, rmn.Secondary)
				verifyUserPlacementRuleDecision(userPlacementRule.Name, userPlacementRule.Namespace, EastManagedCluster)
				verifyDRPCStatusPreferredClusterExpectation(rmn.FailedBack)
				waitForVRGMWDeletion(WestManagedCluster)
				updateManagedClusterViewStatusAsNotFound(mcvWest)
				Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(4)) // MWs for VRG+ROLES+PVs
				Expect(getManifestWorkCount(WestManagedCluster)).Should(Equal(2)) // Roles MWs
				waitForCompletion()
			})
		})
		When("DRAction is changed to failover after failback", func() {
			It("Should failover again to Secondary (WestManagedCluster)", func() {
				// ----------------------------- FAILOVER TO SECONDARY --------------------------------------
				By("\n\n*** Failover - 3\n\n")
				safeToProceed = false

				mcvEast := createManagedClusterView(EastManagedCluster)
				mcvWest := createManagedClusterView(WestManagedCluster)

				updateClonedPlacementRuleStatus(userPlacementRule, drpc, WestManagedCluster)
				setDRPCSpecExpectationTo(drpc, "", rmn.ActionFailover, EastManagedCluster)

				updateManagedClusterViewWithVRG(mcvEast, rmn.Secondary)
				updateManagedClusterViewWithVRG(mcvWest, rmn.Secondary)

				updateManifestWorkStatus(WestManagedCluster, "pv", ocmworkv1.WorkApplied)
				updateManifestWorkStatus(WestManagedCluster, "vrg", ocmworkv1.WorkApplied)
				verifyUserPlacementRuleDecision(userPlacementRule.Name, userPlacementRule.Namespace, WestManagedCluster)
				verifyDRPCStatusPreferredClusterExpectation(rmn.FailedOver)
				verifyVRGManifestWorkCreatedAsPrimary(WestManagedCluster)

				Expect(getManifestWorkCount(WestManagedCluster)).Should(Equal(4)) // MW for VRG+ROLES+PVs
				waitForVRGMWDeletion(EastManagedCluster)
				updateManagedClusterViewStatusAsNotFound(mcvEast)
				Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(2)) // Roles MWs
				waitForCompletion()
			})
		})
		When("DRAction is set to relocation", func() {
			It("Should relocate to Primary (EastManagedCluster)", func() {
				// ----------------------------- FAILBACK TO PRIMARY --------------------------------------
				By("\n\n*** relocate\n\n")
				safeToProceed = false

				mcvEast := createManagedClusterView(EastManagedCluster)
				mcvWest := createManagedClusterView(WestManagedCluster)

				updateManifestWorkStatus(WestManagedCluster, "vrg", ocmworkv1.WorkProgressing)
				updateClonedPlacementRuleStatus(userPlacementRule, drpc, EastManagedCluster)
				setDRPCSpecExpectationTo(drpc, "", rmn.ActionRelocate, "")

				updateManagedClusterViewWithVRG(mcvEast, rmn.Secondary)
				updateManagedClusterViewWithVRG(mcvWest, rmn.Secondary)

				updateManifestWorkStatus(EastManagedCluster, "pv", ocmworkv1.WorkApplied)
				updateManifestWorkStatus(EastManagedCluster, "vrg", ocmworkv1.WorkApplied)

				verifyUserPlacementRuleDecision(userPlacementRule.Name, userPlacementRule.Namespace, EastManagedCluster)
				verifyDRPCStatusPreferredClusterExpectation(rmn.Relocated)
				verifyVRGManifestWorkCreatedAsPrimary(EastManagedCluster)

				Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(4)) // MWs for VRG+ROLES+PVs
				waitForVRGMWDeletion(WestManagedCluster)
				updateManagedClusterViewStatusAsNotFound(mcvWest)
				Expect(getManifestWorkCount(WestManagedCluster)).Should(Equal(2)) // Roles MWs
				waitForCompletion()
			})
			When("Deleting DRPC", func() {
				It("Should delete VRG from Primary (EastManagedCluster)", func() {
					// ----------------------------- DELETE DRPC from PRIMARY --------------------------------------
					By("\n\n*** DELETE DRPC ***\n\n")
					safeToProceed = false
					deleteDRPC()
					waitForCompletion()
					Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(2)) // Roles MWs
				})
			})
		})
	})
})
