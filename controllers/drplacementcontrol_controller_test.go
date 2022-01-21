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
	"runtime"
	"strings"
	"time"

	"github.com/ghodss/yaml"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	errorswrapper "github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	machineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spokeClusterV1 "github.com/open-cluster-management/api/cluster/v1"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	plrv1 "github.com/open-cluster-management/multicloud-operators-placementrule/pkg/apis/apps/v1"
	dto "github.com/prometheus/client_model/go"
	rmn "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers"
	rmnutil "github.com/ramendr/ramen/controllers/util"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
)

const (
	DRPCName              = "app-volume-replication-test"
	DRPCNamespaceName     = "app-namespace"
	UserPlacementRuleName = "user-placement-rule"
	EastManagedCluster    = "east-cluster"
	WestManagedCluster    = "west-cluster"
	DRPolicyName          = "my-dr-peers"

	timeout       = time.Second * 10
	interval      = time.Millisecond * 10
	updateRetries = 2 // replace this with 5 when done testing.  It takes a long time for the test to complete
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

	drPolicy = &rmn.DRPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: DRPolicyName,
		},
		Spec: rmn.DRPolicySpec{
			DRClusterSet: []rmn.ManagedCluster{
				{
					Name:          EastManagedCluster,
					S3ProfileName: "fakeS3Profile",
					Region:        "east",
				},
				{
					Name:          WestManagedCluster,
					S3ProfileName: "fakeS3Profile",
					Region:        "west",
				},
			},
			SchedulingInterval: schedulingInterval,
		},
	}
)

var drstate string

// FakeProgressCallback of function type
func FakeProgressCallback(drpcName string, state string) {
	drstate = state
}

var restorePVs = true

type FakeMCVGetter struct{}

//nolint:dogsled
func getFunctionNameAtIndex(idx int) string {
	pc, _, _, _ := runtime.Caller(idx)
	data := runtime.FuncForPC(pc).Name()
	result := strings.Split(data, ".")

	return result[len(result)-1]
}

func (f FakeMCVGetter) GetNamespaceFromManagedCluster(
	resourceName, managedCluster, namespaceString string) (*corev1.Namespace, error) {
	appNamespaceLookupKey := types.NamespacedName{Name: namespaceString}
	appNamespaceObj := &corev1.Namespace{}

	err := k8sClient.Get(context.TODO(), appNamespaceLookupKey, appNamespaceObj)

	return appNamespaceObj, errorswrapper.Wrap(err, "failed to get Namespace from managedcluster")
}

func (f FakeMCVGetter) GetVRGFromManagedCluster(
	resourceName, resourceNamespace, managedCluster string) (*rmn.VolumeReplicationGroup, error) {
	conType := controllers.VRGConditionTypeDataReady
	reason := controllers.VRGConditionReasonReplicating
	vrgStatus := rmn.VolumeReplicationGroupStatus{
		State: rmn.PrimaryState,
		Conditions: []metav1.Condition{
			{
				Type:               conType,
				Reason:             reason,
				Status:             metav1.ConditionTrue,
				Message:            "Testing VRG",
				LastTransitionTime: metav1.Now(),
			},
		},
	}
	vrg := &rmn.VolumeReplicationGroup{
		TypeMeta:   metav1.TypeMeta{Kind: "VolumeReplicationGroup", APIVersion: "ramendr.openshift.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: DRPCName, Namespace: DRPCNamespaceName},
		Spec: rmn.VolumeReplicationGroupSpec{
			Async: rmn.VRGAsyncSpec{
				Mode:               rmn.AsyncModeEnabled,
				SchedulingInterval: schedulingInterval,
			},
			ReplicationState: rmn.Primary,
			PVCSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"appclass": "gold"},
			},
			S3Profiles: []string{"fakeS3Profile"},
		},
		Status: vrgStatus,
	}

	switch getFunctionNameAtIndex(2) {
	case "updateDRPCStatus":
		return vrg, nil
	case "checkPVsHaveBeenRestored":
		if restorePVs {
			vrg.Status.Conditions[0].Type = controllers.VRGConditionTypeClusterDataReady
			vrg.Status.Conditions[0].Reason = controllers.VRGConditionReasonClusterDataRestored
		}

		return vrg, nil

	case "ensureVRGIsSecondaryOnCluster":
		return moveVRGToSecondary(managedCluster, "vrg", false)

	case "ensureDataProtectedOnCluster":
		return moveVRGToSecondary(managedCluster, "vrg", true)

	case "ensureVRGDeleted":
		return nil, errors.NewNotFound(schema.GroupResource{}, "requested resource not found in ManagedCluster")

	case "getVRGsFromManagedClusters":
		return getVRGFromManifestWork(managedCluster)
	}

	return nil, fmt.Errorf("unknonw caller %s", getFunctionNameAtIndex(2))
}

func getVRGFromManifestWork(managedCluster string) (*rmn.VolumeReplicationGroup, error) {
	manifestLookupKey := types.NamespacedName{
		Name:      rmnutil.ManifestWorkName(DRPCName, DRPCNamespaceName, "vrg"),
		Namespace: managedCluster,
	}

	mw := &ocmworkv1.ManifestWork{}

	err := k8sClient.Get(context.TODO(), manifestLookupKey, mw)
	if errors.IsNotFound(err) {
		return nil, errors.NewNotFound(schema.GroupResource{},
			fmt.Sprintf("requested resource not found in ManagedCluster %s", managedCluster))
	}

	vrg := &rmn.VolumeReplicationGroup{}
	err = yaml.Unmarshal(mw.Spec.Workload.Manifests[0].Raw, vrg)
	Expect(err).NotTo(HaveOccurred())

	// Always report conditions as a success?
	vrg.Status.Conditions = append(vrg.Status.Conditions, metav1.Condition{
		Type:               controllers.VRGConditionTypeClusterDataProtected,
		Reason:             controllers.VRGConditionReasonUploaded,
		Status:             metav1.ConditionTrue,
		Message:            "Cluster data protected",
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: vrg.Generation,
	})

	vrg.Status.Conditions = append(vrg.Status.Conditions, metav1.Condition{
		Type:               controllers.VRGConditionTypeDataReady,
		Reason:             controllers.VRGConditionReasonReplicating,
		Status:             metav1.ConditionTrue,
		Message:            "Testing VRG",
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: vrg.Generation,
	})

	return vrg, nil
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
				Name: UserPlacementRuleName,
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
		},
	}
	Expect(k8sClient.Create(context.TODO(), drpc)).Should(Succeed())

	return drpc
}

func deleteUserPlacementRule() {
	userPlacementRule := getLatestUserPlacementRule(UserPlacementRuleName, DRPCNamespaceName)
	Expect(k8sClient.Delete(context.TODO(), userPlacementRule)).Should(Succeed())
}

func deleteDRPC() {
	drpc := getLatestDRPC()
	Expect(k8sClient.Delete(context.TODO(), drpc)).Should(Succeed())
}

func setDRPCSpecExpectationTo(action rmn.DRAction) {
	localRetries := 0
	for localRetries < updateRetries {
		latestDRPC := getLatestDRPC()

		latestDRPC.Spec.Action = action
		latestDRPC.Spec.PreferredCluster = EastManagedCluster
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
		latestDRPC := getLatestDRPC()

		return latestDRPC.Spec.Action == action
	}, timeout, interval).Should(BeTrue(), "failed to update DRPC DR action on time")
}

func getLatestDRPC() *rmn.DRPlacementControl {
	drpcLookupKey := types.NamespacedName{
		Name:      DRPCName,
		Namespace: DRPCNamespaceName,
	}
	latestDRPC := &rmn.DRPlacementControl{}
	err := apiReader.Get(context.TODO(), drpcLookupKey, latestDRPC)
	Expect(err).NotTo(HaveOccurred())

	return latestDRPC
}

func clearDRPCStatus() {
	latestDRPC := getLatestDRPC()
	latestDRPC.Status = rmn.DRPlacementControlStatus{}
	latestDRPC.Status.LastUpdateTime = metav1.Now()
	err := k8sClient.Status().Update(context.TODO(), latestDRPC)
	Expect(err).NotTo(HaveOccurred())
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

func createDRPolicy() {
	err := k8sClient.Create(context.TODO(), drPolicy)
	Expect(err).NotTo(HaveOccurred())
}

func deleteDRPolicy() {
	Expect(k8sClient.Delete(context.TODO(), drPolicy)).To(Succeed())
}

func moveVRGToSecondary(clusterNamespace, mwType string, protectData bool) (*rmn.VolumeReplicationGroup, error) {
	manifestLookupKey := types.NamespacedName{
		Name:      rmnutil.ManifestWorkName(DRPCName, DRPCNamespaceName, mwType),
		Namespace: clusterNamespace,
	}

	vrg, err := updateVRGMW(manifestLookupKey, protectData)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, err
		}

		// If the resource is changed when MW is being
		// updated, then it can fail. Try again.
		vrg, err = updateVRGMW(manifestLookupKey, protectData)
		if err != nil && errors.IsNotFound(err) {
			return nil, err
		}
	}

	Expect(err).NotTo(HaveOccurred(), "erros %w in updating MW", err)

	return vrg, err
}

func updateVRGMW(manifestLookupKey types.NamespacedName, dataProtected bool) (*rmn.VolumeReplicationGroup, error) {
	mw := &ocmworkv1.ManifestWork{}

	err := k8sClient.Get(context.TODO(), manifestLookupKey, mw)
	if errors.IsNotFound(err) {
		return nil, errors.NewNotFound(schema.GroupResource{}, "requested resource not found in ManagedCluster")
	}

	Expect(err).NotTo(HaveOccurred())

	vrgClientManifest := &mw.Spec.Workload.Manifests[0]
	vrg := &rmn.VolumeReplicationGroup{}

	err = yaml.Unmarshal(vrgClientManifest.RawExtension.Raw, &vrg)
	Expect(err).NotTo(HaveOccurred())

	if vrg.Spec.ReplicationState == rmn.Secondary {
		vrg.Status.State = rmn.SecondaryState

		updateDataProtectedCondition(dataProtected, vrg)

		objJSON, err := json.Marshal(vrg)
		Expect(err).NotTo(HaveOccurred())

		manifest := &ocmworkv1.Manifest{}
		manifest.RawExtension = machineryruntime.RawExtension{Raw: objJSON}

		mw.Spec.Workload.Manifests[0] = *manifest

		err = k8sClient.Update(context.TODO(), mw)
		// Expect(err).NotTo(HaveOccurred(), "erros %w in updating MW", err)
		if err != nil {
			return nil, fmt.Errorf("failed to update VRG ManifestWork %w", err)
		}
	}

	return vrg, nil
}

func updateDataProtectedCondition(dataProtected bool, vrg *rmn.VolumeReplicationGroup) {
	if dataProtected {
		if len(vrg.Status.Conditions) == 0 {
			vrg.Status.Conditions = append(vrg.Status.Conditions, metav1.Condition{
				Type:               controllers.VRGConditionTypeDataProtected,
				Reason:             controllers.VRGConditionReasonDataProtected,
				Status:             metav1.ConditionTrue,
				Message:            "Data Protected",
				LastTransitionTime: metav1.Now(),
				ObservedGeneration: vrg.Generation,
			})
		} else {
			vrg.Status.Conditions[0].Type = controllers.VRGConditionTypeDataProtected
			vrg.Status.Conditions[0].Reason = controllers.VRGConditionReasonDataProtected
			vrg.Status.Conditions[0].Status = metav1.ConditionTrue
			vrg.Status.Conditions[0].ObservedGeneration = vrg.Generation
			vrg.Status.Conditions[0].LastTransitionTime = metav1.Now()
		}
	}
}

func updateManifestWorkStatus(clusterNamespace, mwType, workType string) {
	manifestLookupKey := types.NamespacedName{
		Name:      rmnutil.ManifestWorkName(DRPCName, DRPCNamespaceName, mwType),
		Namespace: clusterNamespace,
	}
	mw := &ocmworkv1.ManifestWork{}

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), manifestLookupKey, mw)

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

	mw.Status = pvManifestStatus

	err := k8sClient.Status().Update(context.TODO(), mw)
	if err != nil {
		// try again
		Expect(k8sClient.Get(context.TODO(), manifestLookupKey, mw)).NotTo(HaveOccurred())
		mw.Status = pvManifestStatus
		err = k8sClient.Status().Update(context.TODO(), mw)
	}

	Expect(err).NotTo(HaveOccurred())

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), manifestLookupKey, mw)

		return err == nil && len(mw.Status.Conditions) != 0
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
	createDRPolicy()

	placementRule := createPlacementRule(placementName, namespace)
	drpc := createDRPC(DRPCName, DRPCNamespaceName)

	return placementRule, drpc
}

func verifyVRGManifestWorkCreatedAsPrimary(managedCluster string) {
	vrgManifestLookupKey := types.NamespacedName{
		Name:      rmnutil.DrClusterManifestWorkName,
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
			idx, condition := getDRPCCondition(&updatedDRPC.Status, rmn.ConditionAvailable)

			return d.ClusterName == EastManagedCluster && idx != -1 && condition.Reason == string(drState)
		}

		return false
	}, timeout, interval).Should(BeTrue(), fmt.Sprintf("failed waiting for an updated DRPC. State %v", drState))

	Expect(updatedDRPC.Status.PreferredDecision.ClusterName).Should(Equal(EastManagedCluster))
	_, condition := getDRPCCondition(&updatedDRPC.Status, rmn.ConditionAvailable)
	Expect(condition.Reason).Should(Equal(string(drState)))
}

func getLatestUserPlacementRule(name, namespace string) *plrv1.PlacementRule {
	usrPlRuleLookupKey := types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}

	usrPlRule := &plrv1.PlacementRule{}

	err := k8sClient.Get(context.TODO(), usrPlRuleLookupKey, usrPlRule)
	Expect(err).NotTo(HaveOccurred())

	return usrPlRule
}

func waitForCompletion(expectedState string) {
	Eventually(func() bool {
		return drstate == expectedState
	}, timeout*2, interval).Should(BeTrue(),
		fmt.Sprintf("failed to waiting for state to match. expecting: %s, found %s", expectedState, drstate))
}

func waitForUpdateDRPCStatus() {
	Eventually(func() bool {
		drpc := getLatestDRPC()

		for _, condition := range drpc.Status.Conditions {
			if condition.ObservedGeneration != drpc.Generation {
				return false
			}
		}

		return true
	}, timeout, interval).Should(BeTrue(), "failed to see an updated DRPC status")
}

func getDRPCCondition(status *rmn.DRPlacementControlStatus, conditionType string) (int, *metav1.Condition) {
	if len(status.Conditions) == 0 {
		return -1, nil
	}

	for i := range status.Conditions {
		if status.Conditions[i].Type == conditionType {
			return i, &status.Conditions[i]
		}
	}

	return -1, nil
}

func relocateToPreferredCluster(userPlacementRule *plrv1.PlacementRule) {
	setDRPCSpecExpectationTo(rmn.ActionRelocate)

	updateManifestWorkStatus(EastManagedCluster, "vrg", ocmworkv1.WorkApplied)

	verifyUserPlacementRuleDecision(userPlacementRule.Name, userPlacementRule.Namespace, EastManagedCluster)
	verifyDRPCStatusPreferredClusterExpectation(rmn.Relocated)
	verifyVRGManifestWorkCreatedAsPrimary(EastManagedCluster)

	waitForVRGMWDeletion(WestManagedCluster)

	waitForCompletion(string(rmn.Relocated))
}

func recoverToFailoverCluster(userPlacementRule *plrv1.PlacementRule) {
	setDRPCSpecExpectationTo(rmn.ActionFailover)

	updateManifestWorkStatus(WestManagedCluster, "vrg", ocmworkv1.WorkApplied)

	verifyUserPlacementRuleDecision(userPlacementRule.Name, userPlacementRule.Namespace, WestManagedCluster)
	verifyDRPCStatusPreferredClusterExpectation(rmn.FailedOver)
	verifyVRGManifestWorkCreatedAsPrimary(WestManagedCluster)

	waitForVRGMWDeletion(EastManagedCluster)

	waitForCompletion(string(rmn.FailedOver))
}

// +kubebuilder:docs-gen:collapse=Imports
var _ = Describe("DRPlacementControl Reconciler", func() {
	Context("DRPlacementControl Reconciler", func() {
		userPlacementRule := &plrv1.PlacementRule{}
		drpc := &rmn.DRPlacementControl{}
		When("An Application is deployed for the first time", func() {
			It("Should deploy to EastManagedCluster", func() {
				By("Initial Deployment")
				userPlacementRule, drpc = InitialDeployment(DRPCNamespaceName, UserPlacementRuleName, EastManagedCluster)
				updateClonedPlacementRuleStatus(userPlacementRule, drpc, EastManagedCluster)
				verifyVRGManifestWorkCreatedAsPrimary(EastManagedCluster)
				updateManifestWorkStatus(EastManagedCluster, "vrg", ocmworkv1.WorkApplied)
				verifyUserPlacementRuleDecision(userPlacementRule.Name, userPlacementRule.Namespace, EastManagedCluster)
				verifyDRPCStatusPreferredClusterExpectation(rmn.Deployed)
				Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(2)) // MWs for VRG and ROLES
				waitForCompletion(string(rmn.Deployed))

				drpc := getLatestDRPC()
				// At this point expect the DRPC status condition to have 2 types
				// {Available and PeerReady}
				// Final state is 'Deployed'
				Expect(drpc.Status.Phase).To(Equal(rmn.Deployed))
				Expect(len(drpc.Status.Conditions)).To(Equal(1))
				_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionAvailable)
				Expect(condition.Reason).To(Equal(string(rmn.Deployed)))

				val, err := rmnutil.GetMetricValueSingle("ramen_initial_deploy_time", dto.MetricType_GAUGE)
				Expect(err).NotTo(HaveOccurred())
				Expect(val).NotTo(Equal(0.0)) // failover time should be non-zero
			})
		})
		When("DRAction changes to Failover", func() {
			It("Should not failover to Secondary (WestManagedCluster) till PV manifest is applied", func() {
				By("\n\n*** Failover - 1\n\n")
				restorePVs = false
				setDRPCSpecExpectationTo(rmn.ActionFailover)
				verifyUserPlacementRuleDecisionUnchanged(userPlacementRule.Name, userPlacementRule.Namespace, EastManagedCluster)
				Expect(getManifestWorkCount(WestManagedCluster)).Should(Equal(2)) // MWs for VRG and VRG ROLES
				Expect(len(userPlacementRule.Status.Decisions)).Should(Equal(0))
				restorePVs = true
			})
			It("Should failover to Secondary (WestManagedCluster)", func() {
				// ----------------------------- FAILOVER TO SECONDARY (WestManagedCluster) --------------------------------------
				By("\n\n*** Failover - 1\n\n")
				recoverToFailoverCluster(userPlacementRule)
				Expect(getManifestWorkCount(WestManagedCluster)).Should(Equal(2)) // MW for VRG+ROLES
				Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(1)) // Roles MW

				val, err := rmnutil.GetMetricValueSingle("ramen_failover_time", dto.MetricType_GAUGE)
				Expect(err).NotTo(HaveOccurred())
				Expect(val).NotTo(Equal(0.0)) // failover time should be non-zero

				drpc = getLatestDRPC()
				// At this point expect the DRPC status condition to have 2 types
				// {Available and PeerReady}
				// Final state is 'FailedOver'
				Expect(drpc.Status.Phase).To(Equal(rmn.FailedOver))
				Expect(len(drpc.Status.Conditions)).To(Equal(2))
				_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionAvailable)
				Expect(condition.Reason).To(Equal(string(rmn.FailedOver)))
				userPlacementRule = getLatestUserPlacementRule(userPlacementRule.Name, userPlacementRule.Namespace)
				Expect(userPlacementRule.Status.Decisions[0].ClusterName).To(Equal(WestManagedCluster))
			})
		})
		When("DRAction is set to Relocate", func() {
			It("Should relocate to Primary (EastManagedCluster)", func() {
				// ----------------------------- RELOCATION TO PRIMARY --------------------------------------
				By("\n\n*** Relocate - 1\n\n")
				relocateToPreferredCluster(userPlacementRule)
				Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(2)) // MWs for VRG+ROLES

				drpc = getLatestDRPC()
				// At this point expect the DRPC status condition to have 2 types
				// {Available and PeerReady}
				// Final state is 'Relocated'
				Expect(drpc.Status.Phase).To(Equal(rmn.Relocated))
				Expect(len(drpc.Status.Conditions)).To(Equal(2))
				_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionAvailable)
				Expect(condition.Reason).To(Equal(string(rmn.Relocated)))
				userPlacementRule = getLatestUserPlacementRule(userPlacementRule.Name, userPlacementRule.Namespace)
				Expect(userPlacementRule.Status.Decisions[0].ClusterName).To(Equal(EastManagedCluster))
				Expect(condition.Reason).To(Equal(string(rmn.Relocated)))

				val, err := rmnutil.GetMetricValueSingle("ramen_relocate_time", dto.MetricType_GAUGE)
				Expect(err).NotTo(HaveOccurred())
				Expect(val).NotTo(Equal(0.0)) // failover time should be non-zero
			})
		})
		When("DRAction is cleared after relocation", func() {
			It("Should not do anything", func() {
				// ----------------------------- Clear DRAction --------------------------------------
				setDRPCSpecExpectationTo("")
				waitForCompletion(string(rmn.Relocated))
				drpc = getLatestDRPC()
				// At this point expect the DRPC status condition to have 2 types
				// {Available and PeerReady}
				// Final state didn't change and it is 'Relocated' even though we tried to run
				// initial deployment
				Expect(drpc.Status.Phase).To(Equal(rmn.Relocated))
				Expect(len(drpc.Status.Conditions)).To(Equal(2))
				_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionAvailable)
				Expect(condition.Reason).To(Equal(string(rmn.Relocated)))
				userPlacementRule = getLatestUserPlacementRule(userPlacementRule.Name, userPlacementRule.Namespace)
				Expect(userPlacementRule.Status.Decisions[0].ClusterName).To(Equal(EastManagedCluster))
			})
		})
		When("DRAction is changed to Failover after relocation", func() {
			It("Should failover again to Secondary (WestManagedCluster)", func() {
				// ----------------------------- FAILOVER TO SECONDARY --------------------------------------
				By("\n\n*** Failover - 3\n\n")
				recoverToFailoverCluster(userPlacementRule)
				Expect(getManifestWorkCount(WestManagedCluster)).Should(Equal(2)) // MW for VRG+ROLES
				Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(1)) // Roles MW

				drpc := getLatestDRPC()
				// At this point expect the DRPC status condition to have 2 types
				// {Available and PeerReady}
				// Final state is 'FailedOver'
				Expect(drpc.Status.Phase).To(Equal(rmn.FailedOver))
				Expect(len(drpc.Status.Conditions)).To(Equal(2))
				_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionAvailable)
				Expect(condition.Reason).To(Equal(string(rmn.FailedOver)))
				userPlacementRule = getLatestUserPlacementRule(userPlacementRule.Name, userPlacementRule.Namespace)
				Expect(userPlacementRule.Status.Decisions[0].ClusterName).To(Equal(WestManagedCluster))
			})
		})
		When("DRAction is cleared after failover", func() {
			It("Should not do anything", func() {
				// ----------------------------- Clear DRAction --------------------------------------
				By("\n\n>>> clearing DRAction")
				drstate = "none"
				setDRPCSpecExpectationTo("")
				waitForCompletion(string(rmn.FailedOver))
				waitForUpdateDRPCStatus()
				drpc = getLatestDRPC()
				// At this point expect the DRPC status condition to have 2 types
				// {Available and PeerReady}
				// Final state didn't change and it is 'FailedOver' even though we tried to run
				// initial deployment
				Expect(drpc.Status.Phase).To(Equal(rmn.FailedOver))
				Expect(len(drpc.Status.Conditions)).To(Equal(2))
				_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionAvailable)
				Expect(condition.Reason).To(Equal(string(rmn.FailedOver)))
				userPlacementRule = getLatestUserPlacementRule(userPlacementRule.Name, userPlacementRule.Namespace)
				Expect(userPlacementRule.Status.Decisions[0].ClusterName).To(Equal(WestManagedCluster))
			})
		})
		When("DRAction is cleared but DRPC status is empty", func() {
			It("Should do nothing for placement as it is deployed somewhere else", func() {
				// ----------------------------- Clear DRAction --------------------------------------
				By("\n\n>>> Update DRPC status")
				clearDRPCStatus()
				waitForCompletion(string(""))
				drpc = getLatestDRPC()
				// At this point expect the DRPC status condition to have 2 types
				// {Available and PeerReady}
				// Final state didn't change and it is 'FailedOver' even though we tried to run
				// initial deployment. But the status remains cleared.
				Expect(drpc.Status.Phase).To(Equal(rmn.DRState("")))
				userPlacementRule = getLatestUserPlacementRule(userPlacementRule.Name, userPlacementRule.Namespace)
				Expect(userPlacementRule.Status.Decisions[0].ClusterName).To(Equal(WestManagedCluster))
			})
		})
		When("DRAction is set to Relocate", func() {
			It("Should relocate to Primary (EastManagedCluster)", func() {
				// ----------------------------- RELOCATION TO PRIMARY --------------------------------------
				By("\n\n*** relocate 2\n\n")
				relocateToPreferredCluster(userPlacementRule)
				Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(2)) // MWs for VRG+ROLES
				Expect(getManifestWorkCount(WestManagedCluster)).Should(Equal(1)) // Roles MW

				drpc = getLatestDRPC()
				// At this point expect the DRPC status condition to have 2 types
				// {Available and PeerReady}
				// Final state is 'Relocated'
				Expect(drpc.Status.Phase).To(Equal(rmn.Relocated))
				Expect(len(drpc.Status.Conditions)).To(Equal(2))
				_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionAvailable)
				Expect(condition.Reason).To(Equal(string(rmn.Relocated)))
				userPlacementRule = getLatestUserPlacementRule(userPlacementRule.Name, userPlacementRule.Namespace)
				Expect(userPlacementRule.Status.Decisions[0].ClusterName).To(Equal(EastManagedCluster))
			})
		})

		When("Deleting user PlacementRule", func() {
			It("Should cleanup DRPC", func() {
				// ----------------------------- DELETE DRPC from PRIMARY --------------------------------------
				By("\n\n*** DELETE User PlacementRule ***\n\n")
				deleteUserPlacementRule()
				_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionPeerReady)
				Expect(condition).NotTo(BeNil())
				// waitForCompletion("deleted")
				// Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(1)) // Roles MW
			})
		})

		When("Deleting DRPC", func() {
			It("Should delete VRG from Primary (EastManagedCluster)", func() {
				// ----------------------------- DELETE DRPC from PRIMARY --------------------------------------
				By("\n\n*** DELETE DRPC ***\n\n")
				deleteDRPC()
				waitForCompletion("deleted")
				Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(1)) // Roles MW
				deleteDRPolicy()
			})
		})
	})
})
