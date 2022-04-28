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

	dto "github.com/prometheus/client_model/go"
	rmn "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers"
	rmnutil "github.com/ramendr/ramen/controllers/util"
	plrv1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
)

const (
	DRPCName              = "app-volume-replication-test"
	DRPCNamespaceName     = "app-namespace"
	UserPlacementRuleName = "user-placement-rule"
	East1ManagedCluster   = "east1-cluster"
	East2ManagedCluster   = "east2-cluster"
	West1ManagedCluster   = "west1-cluster"
	AsyncDRPolicyName     = "my-async-dr-peers"
	SyncDRPolicyName      = "my-sync-dr-peers"

	updateRetries = 2 // replace this with 5 when done testing.  It takes a long time for the test to complete
	pvcCount      = 2 // Count of fake PVCs reported in the VRG status
)

var (
	west1Cluster = &spokeClusterV1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: West1ManagedCluster,
			Labels: map[string]string{
				"name": West1ManagedCluster,
				"key1": "west1",
			},
		},
	}
	east1Cluster = &spokeClusterV1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: East1ManagedCluster,
			Labels: map[string]string{
				"name": East1ManagedCluster,
				"key1": "east1",
			},
		},
	}
	east2Cluster = &spokeClusterV1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: East2ManagedCluster,
			Labels: map[string]string{
				"name": East2ManagedCluster,
				"key1": "east2",
			},
		},
	}

	asyncClusters = []*spokeClusterV1.ManagedCluster{west1Cluster, east1Cluster}
	syncClusters  = []*spokeClusterV1.ManagedCluster{east1Cluster, east2Cluster}

	east1ManagedClusterNamespace = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: East1ManagedCluster},
	}

	west1ManagedClusterNamespace = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: West1ManagedCluster},
	}

	appNamespace = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: DRPCNamespaceName},
	}

	east2ManagedClusterNamespace = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: East2ManagedCluster},
	}

	schedulingInterval = "1h"

	drClusters = []rmn.DRCluster{}

	asyncDRPolicy = &rmn.DRPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: AsyncDRPolicyName,
		},
		Spec: rmn.DRPolicySpec{
			DRClusters:         []string{East1ManagedCluster, West1ManagedCluster},
			SchedulingInterval: schedulingInterval,
		},
	}
)

func getSyncDRPolicy() *rmn.DRPolicy {
	return &rmn.DRPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: SyncDRPolicyName,
		},
		Spec: rmn.DRPolicySpec{
			DRClusters:         []string{East1ManagedCluster, East2ManagedCluster},
			SchedulingInterval: schedulingInterval,
		},
	}
}

var drstate string

// FakeProgressCallback of function type
func FakeProgressCallback(drpcName string, state string) {
	drstate = state
}

var restorePVs = true

type FakeMCVGetter struct{}

func getNamespaceObj(namespaceName string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: namespaceName},
	}
}

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

var baseVRG = &rmn.VolumeReplicationGroup{
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
		S3Profiles: []string{s3Profiles[0].S3ProfileName},
	},
}

//nolint:funlen
func (f FakeMCVGetter) GetVRGFromManagedCluster(
	resourceName, resourceNamespace, managedCluster string) (*rmn.VolumeReplicationGroup, error) {
	conType := controllers.VRGConditionTypeDataReady
	reason := controllers.VRGConditionReasonReplicating
	vrgStatus := rmn.VolumeReplicationGroupStatus{
		State:                       rmn.PrimaryState,
		PrepareForFinalSyncComplete: true,
		FinalSyncComplete:           true,
		Conditions: []metav1.Condition{
			{
				Type:               conType,
				Reason:             reason,
				Status:             metav1.ConditionTrue,
				Message:            "Testing VRG",
				LastTransitionTime: metav1.Now(),
				ObservedGeneration: 1,
			},
		},
		ProtectedPVCs: []rmn.ProtectedPVC{},
	}
	vrg := baseVRG.DeepCopy()
	vrg.Status = vrgStatus

	vrg.Generation = 1

	switch getFunctionNameAtIndex(2) {
	case "updateDRPCStatus":
		for i := 0; i < pvcCount; i++ {
			vrg.Status.ProtectedPVCs = append(vrg.Status.ProtectedPVCs, rmn.ProtectedPVC{Name: fmt.Sprintf("fakePVC%d", i)})
		}

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
		vrgFromMW, err := getVRGFromManifestWork(managedCluster)
		if err != nil {
			return nil, err
		}

		if vrgFromMW != nil {
			vrgFromMW.Generation = 1
			vrgFromMW.Status = vrgStatus
			vrgFromMW.Status.Conditions = append(vrgFromMW.Status.Conditions, metav1.Condition{
				Type:               controllers.VRGConditionTypeClusterDataReady,
				Reason:             controllers.VRGConditionReasonClusterDataRestored,
				Status:             metav1.ConditionTrue,
				Message:            "Cluster Data Ready",
				LastTransitionTime: metav1.Now(),
				ObservedGeneration: vrgFromMW.Generation,
			})
			vrgFromMW.Status.Conditions = append(vrgFromMW.Status.Conditions, metav1.Condition{
				Type:               controllers.VRGConditionTypeClusterDataProtected,
				Reason:             controllers.VRGConditionReasonClusterDataRestored,
				Status:             metav1.ConditionTrue,
				Message:            "Cluster Data Protected",
				LastTransitionTime: metav1.Now(),
				ObservedGeneration: vrgFromMW.Generation,
			})
			vrgFromMW.Status.Conditions = append(vrgFromMW.Status.Conditions, metav1.Condition{
				Type:               controllers.VRGConditionTypeDataProtected,
				Reason:             controllers.VRGConditionReasonDataProtected,
				Status:             metav1.ConditionTrue,
				Message:            "Data Protected",
				LastTransitionTime: metav1.Now(),
				ObservedGeneration: vrgFromMW.Generation,
			})

			newProtectedPVC := &rmn.ProtectedPVC{
				Name: "random name",
			}

			vrgFromMW.Status.ProtectedPVCs = append(vrgFromMW.Status.ProtectedPVCs, *newProtectedPVC)
		}

		return vrgFromMW, nil
	}

	return nil, fmt.Errorf("unknown caller %s", getFunctionNameAtIndex(2))
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

	// Fake generation:
	vrg.Generation = 1

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
		Message:            "Data Read",
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: vrg.Generation,
	})

	vrg.Status.Conditions = append(vrg.Status.Conditions, metav1.Condition{
		Type:               controllers.VRGConditionTypeClusterDataReady,
		Reason:             controllers.VRGConditionReasonClusterDataRestored,
		Status:             metav1.ConditionTrue,
		Message:            "Cluster Data Protected",
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: vrg.Generation,
	})

	return vrg, nil
}

func createPlacementRule(name, namespace string) *plrv1.PlacementRule {
	namereq := metav1.LabelSelectorRequirement{}
	namereq.Key = "key1"
	namereq.Operator = metav1.LabelSelectorOpIn

	namereq.Values = []string{"west1"}
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

func createDRPC(name, namespace, drPolicyName string) *rmn.DRPlacementControl {
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
				Name: drPolicyName,
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

func setDRPCSpecExpectationTo(action rmn.DRAction, preferredCluster, failoverCluster string) {
	localRetries := 0
	for localRetries < updateRetries {
		latestDRPC := getLatestDRPC()

		latestDRPC.Spec.Action = action
		latestDRPC.Spec.PreferredCluster = preferredCluster
		latestDRPC.Spec.FailoverCluster = failoverCluster
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

func createNamespace(ns *corev1.Namespace) {
	nsName := types.NamespacedName{Name: ns.Name}

	err := k8sClient.Get(context.TODO(), nsName, &corev1.Namespace{})
	if err != nil {
		Expect(k8sClient.Create(context.TODO(), ns)).NotTo(HaveOccurred(),
			"failed to create %v managed cluster namespace", ns.Name)
	}
}

func createNamespacesAsync(appNamespace *corev1.Namespace) {
	createNamespace(east1ManagedClusterNamespace)
	createNamespace(west1ManagedClusterNamespace)
	createNamespace(appNamespace)
}

func createManagedClustersAsync() {
	for _, cl := range asyncClusters {
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

func populateDRClusters() {
	drClusters = nil
	drClusters = append(drClusters,
		rmn.DRCluster{
			ObjectMeta: metav1.ObjectMeta{Name: East1ManagedCluster},
			Spec:       rmn.DRClusterSpec{S3ProfileName: s3Profiles[0].S3ProfileName, Region: "east"},
		},
		rmn.DRCluster{
			ObjectMeta: metav1.ObjectMeta{Name: West1ManagedCluster},
			Spec:       rmn.DRClusterSpec{S3ProfileName: s3Profiles[0].S3ProfileName, Region: "west"},
		},
		rmn.DRCluster{
			ObjectMeta: metav1.ObjectMeta{Name: East2ManagedCluster},
			Spec:       rmn.DRClusterSpec{S3ProfileName: s3Profiles[0].S3ProfileName, Region: "east"},
		},
	)
}

func createDRClusters(inClusters []*spokeClusterV1.ManagedCluster) {
	for _, managedCluster := range inClusters {
		for idx := range drClusters {
			if managedCluster.Name == drClusters[idx].Name {
				err := k8sClient.Create(context.TODO(), &drClusters[idx])
				Expect(err).NotTo(HaveOccurred())
			}
		}
	}
}

func createDRClustersAsync() {
	createDRClusters(asyncClusters)
}

func createDRPolicy(inDRPolicy *rmn.DRPolicy) {
	err := k8sClient.Create(context.TODO(), inDRPolicy)
	Expect(err).NotTo(HaveOccurred())
	Eventually(func() bool {
		drpolicy := &rmn.DRPolicy{}
		Expect(apiReader.Get(context.TODO(), types.NamespacedName{Name: inDRPolicy.Name}, drpolicy)).To(Succeed())

		for _, condition := range drpolicy.Status.Conditions {
			if condition.Type != rmn.DRPolicyValidated {
				continue
			}

			if condition.ObservedGeneration != drpolicy.Generation {
				return false
			}

			if condition.Status == metav1.ConditionTrue {
				return true
			}

			return false
		}

		return false
	}, timeout, interval).Should(BeTrue())
}

func createDRPolicyAsync() {
	createDRPolicy(asyncDRPolicy)
}

func deleteDRCluster(inDRCluster *rmn.DRCluster) {
	Expect(k8sClient.Delete(context.TODO(), inDRCluster)).To(Succeed())
	Eventually(func() bool {
		drcluster := &rmn.DRCluster{}

		return errors.IsNotFound(apiReader.Get(context.TODO(), types.NamespacedName{
			Namespace: inDRCluster.Namespace,
			Name:      inDRCluster.Name,
		}, drcluster))
	}, timeout, interval).Should(BeTrue())
}

func deleteDRClusters(inClusters []*spokeClusterV1.ManagedCluster) {
	for _, managedCluster := range inClusters {
		for idx := range drClusters {
			if managedCluster.Name == drClusters[idx].Name {
				deleteDRCluster(&drClusters[idx])
			}
		}
	}
}

func deleteDRClustersAsync() {
	deleteDRClusters(asyncClusters)
}

func deleteDRPolicyAsync() {
	Expect(k8sClient.Delete(context.TODO(), asyncDRPolicy)).To(Succeed())
}

func moveVRGToSecondary(clusterNamespace, mwType string, protectData bool) (*rmn.VolumeReplicationGroup, error) {
	manifestLookupKey := types.NamespacedName{
		Name:      rmnutil.ManifestWorkName(DRPCName, DRPCNamespaceName, mwType),
		Namespace: clusterNamespace,
	}

	var vrg *rmn.VolumeReplicationGroup

	var err error

	Eventually(func() bool {
		vrg, err = updateVRGMW(manifestLookupKey, protectData)

		return err == nil || errors.IsNotFound(err)
	}, timeout, interval).Should(BeTrue(),
		fmt.Sprintf("failed to wait for manifestwork update %s cluster %s", mwType, clusterNamespace))

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

func InitialDeploymentAsync(namespace, placementName, homeCluster string) (*plrv1.PlacementRule,
	*rmn.DRPlacementControl) {
	createNamespacesAsync(getNamespaceObj(DRPCNamespaceName))

	createManagedClustersAsync()
	createDRClustersAsync()
	createDRPolicyAsync()

	placementRule := createPlacementRule(placementName, namespace)
	drpc := createDRPC(DRPCName, DRPCNamespaceName, AsyncDRPolicyName)

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

	Expect(len(createdVRGRolesManifest.Spec.Workload.Manifests)).To(Equal(8))

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

			return d.ClusterName == East1ManagedCluster &&
				idx != -1 &&
				condition.Reason == string(drState) &&
				len(updatedDRPC.Status.ResourceConditions.ResourceMeta.ProtectedPVCs) == pvcCount
		}

		return false
	}, timeout, interval).Should(BeTrue(), fmt.Sprintf("failed waiting for an updated DRPC. State %v", drState))

	Expect(updatedDRPC.Status.PreferredDecision.ClusterName).Should(Equal(East1ManagedCluster))
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
		fmt.Sprintf("failed waiting for state to match. expecting: %s, found %s", expectedState, drstate))
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

func getDRClusterCondition(status *rmn.DRClusterStatus, conditionType string) *metav1.Condition {
	if len(status.Conditions) == 0 {
		return nil
	}

	for i := range status.Conditions {
		if status.Conditions[i].Type == conditionType {
			return &status.Conditions[i]
		}
	}

	return nil
}

func runFailoverAction(userPlacementRule *plrv1.PlacementRule, fromCluster, toCluster string, isSyncDR bool) {
	if isSyncDR {
		fenceCluster(fromCluster)
	}

	recoverToFailoverCluster(userPlacementRule, fromCluster, toCluster)
	// TODO: DRCluster as part of Unfence operation, first unfences
	//       the NetworkFence CR and then deletes it. Hence, by the
	//       time this test is made, depending upon whether NetworkFence
	//       resource is cleaned up or not, number of MW may change.
	if !isSyncDR {
		Expect(getManifestWorkCount(toCluster)).Should(Equal(2))   // MW for VRG+ROLES
		Expect(getManifestWorkCount(fromCluster)).Should(Equal(1)) // Roles MW
	} else {
		Expect(getManifestWorkCount(toCluster)).Should(Equal(3))   // MW for VRG+ROLES + NF
		Expect(getManifestWorkCount(fromCluster)).Should(Equal(1)) // Roles MW
	}

	drpc := getLatestDRPC()
	// At this point expect the DRPC status condition to have 2 types
	// {Available and PeerReady}
	// Final state is 'FailedOver'
	Expect(drpc.Status.Phase).To(Equal(rmn.FailedOver))
	Expect(len(drpc.Status.Conditions)).To(Equal(2))
	_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionAvailable)
	Expect(condition.Reason).To(Equal(string(rmn.FailedOver)))

	userPlacementRule = getLatestUserPlacementRule(userPlacementRule.Name, userPlacementRule.Namespace)
	Expect(userPlacementRule.Status.Decisions[0].ClusterName).To(Equal(toCluster))
}

func clearDRActionAfterFailover(userPlacementRule *plrv1.PlacementRule, preferredCluster, failoverCluster string) {
	drstate = "none"

	setDRPCSpecExpectationTo("", preferredCluster, failoverCluster)
	waitForCompletion(string(rmn.FailedOver))
	waitForUpdateDRPCStatus()

	drpc := getLatestDRPC()
	// At this point expect the DRPC status condition to have 2 types
	// {Available and PeerReady}
	// Final state didn't change and it is 'FailedOver' even though we tried to run
	// initial deployment
	Expect(drpc.Status.Phase).To(Equal(rmn.FailedOver))
	Expect(len(drpc.Status.Conditions)).To(Equal(2))
	_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionAvailable)
	Expect(condition.Reason).To(Equal(string(rmn.FailedOver)))

	userPlacementRule = getLatestUserPlacementRule(userPlacementRule.Name, userPlacementRule.Namespace)
	Expect(userPlacementRule.Status.Decisions[0].ClusterName).To(Equal(failoverCluster))
}

func runRelocateAction(userPlacementRule *plrv1.PlacementRule, fromCluster string, isSyncDR bool) {
	toCluster1 := "east1-cluster"

	if isSyncDR {
		unfenceCluster(toCluster1)
	}

	relocateToPreferredCluster(userPlacementRule, fromCluster)
	// TODO: DRCluster as part of Unfence operation, first unfences
	//       the NetworkFence CR and then deletes it. Hence, by the
	//       time this test is made, depending upon whether NetworkFence
	//       resource is cleaned up or not, number of MW may change.

	// Expect(getManifestWorkCount(toCluster1)).Should(Equal(2)) // MWs for VRG+ROLES
	if !isSyncDR {
		Expect(getManifestWorkCount(fromCluster)).Should(Equal(1))
	} else {
		// By the time this check is made, the NetworkFence CR in the
		// cluster from where the application is migrated might not have
		// been deleted. Hence, the number of MW expectation will be
		// either of 1 or 2.
		Expect(getManifestWorkCount(fromCluster)).Should(BeElementOf(1, 2))
	}

	drpc := getLatestDRPC()
	// At this point expect the DRPC status condition to have 2 types
	// {Available and PeerReady}
	// Final state is 'Relocated'
	Expect(drpc.Status.Phase).To(Equal(rmn.Relocated))
	Expect(len(drpc.Status.Conditions)).To(Equal(2))
	_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionAvailable)
	Expect(condition.Reason).To(Equal(string(rmn.Relocated)))

	userPlacementRule = getLatestUserPlacementRule(userPlacementRule.Name, userPlacementRule.Namespace)
	Expect(userPlacementRule.Status.Decisions[0].ClusterName).To(Equal(toCluster1))
	Expect(condition.Reason).To(Equal(string(rmn.Relocated)))

	val, err := rmnutil.GetMetricValueSingle("ramen_relocate_time", dto.MetricType_GAUGE)
	Expect(err).NotTo(HaveOccurred())
	Expect(val).NotTo(Equal(0.0)) // failover time should be non-zero
}

func clearDRActionAfterRelocate(userPlacementRule *plrv1.PlacementRule, preferredCluster, failoverCluster string) {
	setDRPCSpecExpectationTo("", preferredCluster, failoverCluster)
	waitForCompletion(string(rmn.Relocated))

	drpc := getLatestDRPC()
	// At this point expect the DRPC status condition to have 2 types
	// {Available and PeerReady}
	// Final state didn't change and it is 'Relocated' even though we tried to run
	// initial deployment
	Expect(drpc.Status.Phase).To(Equal(rmn.Relocated))
	Expect(len(drpc.Status.Conditions)).To(Equal(2))
	_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionAvailable)
	Expect(condition.Reason).To(Equal(string(rmn.Relocated)))

	userPlacementRule = getLatestUserPlacementRule(userPlacementRule.Name, userPlacementRule.Namespace)
	Expect(userPlacementRule.Status.Decisions[0].ClusterName).To(Equal(preferredCluster))
}

func relocateToPreferredCluster(userPlacementRule *plrv1.PlacementRule, fromCluster string) {
	toCluster1 := "east1-cluster"

	setDRPCSpecExpectationTo(rmn.ActionRelocate, toCluster1, fromCluster)

	updateManifestWorkStatus(toCluster1, "vrg", ocmworkv1.WorkApplied)

	verifyUserPlacementRuleDecision(userPlacementRule.Name, userPlacementRule.Namespace, toCluster1)
	verifyDRPCStatusPreferredClusterExpectation(rmn.Relocated)
	verifyVRGManifestWorkCreatedAsPrimary(toCluster1)

	waitForVRGMWDeletion(West1ManagedCluster)

	waitForCompletion(string(rmn.Relocated))
}

func recoverToFailoverCluster(userPlacementRule *plrv1.PlacementRule, fromCluster, toCluster string) {
	setDRPCSpecExpectationTo(rmn.ActionFailover, fromCluster, toCluster)

	updateManifestWorkStatus(toCluster, "vrg", ocmworkv1.WorkApplied)

	verifyUserPlacementRuleDecision(userPlacementRule.Name, userPlacementRule.Namespace, toCluster)
	verifyDRPCStatusPreferredClusterExpectation(rmn.FailedOver)
	verifyVRGManifestWorkCreatedAsPrimary(toCluster)

	waitForVRGMWDeletion(fromCluster)

	waitForCompletion(string(rmn.FailedOver))
}

func createNamespacesSync() {
	createNamespace(east1ManagedClusterNamespace)
	createNamespace(east2ManagedClusterNamespace)
	createNamespace(appNamespace)
}

func InitialDeploymentSync(namespace, placementName, homeCluster string) (*plrv1.PlacementRule,
	*rmn.DRPlacementControl) {
	createNamespacesSync()

	createManagedClustersSync()
	createDRClustersSync()
	createDRPolicySync()

	placementRule := createPlacementRule(placementName, namespace)
	drpc := createDRPC(DRPCName, DRPCNamespaceName, SyncDRPolicyName)

	return placementRule, drpc
}

func createManagedClustersSync() {
	for _, cl := range syncClusters {
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

func createDRClustersSync() {
	createDRClusters(syncClusters)
}

func createDRPolicySync() {
	createDRPolicy(syncDRPolicy)
}

func deleteDRClustersSync() {
	deleteDRClusters(syncClusters)
}

func deleteDRPolicySync() {
	Expect(k8sClient.Delete(context.TODO(), getSyncDRPolicy())).To(Succeed())
}

func getLatestDRCluster(cluster string) *rmn.DRCluster {
	drclusterLookupKey := types.NamespacedName{
		Name: cluster,
	}
	latestDRCluster := &rmn.DRCluster{}
	err := apiReader.Get(context.TODO(), drclusterLookupKey, latestDRCluster)
	Expect(err).NotTo(HaveOccurred())

	return latestDRCluster
}

func fenceCluster(cluster string) {
	localRetries := 0
	for localRetries < updateRetries {
		latestDRCluster := getLatestDRCluster(cluster)
		latestDRCluster.Spec.ClusterFence = rmn.ClusterFenceStateFenced

		err := k8sClient.Update(context.TODO(), latestDRCluster)
		if errors.IsConflict(err) {
			localRetries++

			continue
		}

		Expect(err).NotTo(HaveOccurred())

		break
	}

	Eventually(func() bool {
		latestDRCluster := getLatestDRCluster(cluster)

		drClusterFencedCondition := getDRClusterCondition(&latestDRCluster.Status,
			rmn.DRClusterConditionTypeFenced)
		if drClusterFencedCondition == nil {
			return false
		}

		if drClusterFencedCondition.ObservedGeneration != latestDRCluster.Generation {
			return false
		}

		return drClusterFencedCondition.Status == metav1.ConditionTrue
	}, timeout, interval).Should(BeTrue(), "failed to update DRCluster on time")
}

func unfenceCluster(cluster string) {
	localRetries := 0
	for localRetries < updateRetries {
		latestDRCluster := getLatestDRCluster(cluster)
		latestDRCluster.Spec.ClusterFence = rmn.ClusterFenceStateUnfenced

		err := k8sClient.Update(context.TODO(), latestDRCluster)
		if errors.IsConflict(err) {
			localRetries++

			continue
		}

		Expect(err).NotTo(HaveOccurred())

		break
	}

	Eventually(func() bool {
		latestDRCluster := getLatestDRCluster(cluster)

		drClusterFencedCondition := getDRClusterCondition(&latestDRCluster.Status,
			rmn.DRClusterConditionTypeFenced)
		if drClusterFencedCondition == nil {
			return false
		}

		if drClusterFencedCondition.ObservedGeneration != latestDRCluster.Generation {
			return false
		}

		return drClusterFencedCondition.Status == metav1.ConditionFalse
	}, timeout, interval).Should(BeTrue(), "failed to update DRCluster on time")
}

func verifyInitialDRPCDeployment(userPlacementRule *plrv1.PlacementRule, drpc *rmn.DRPlacementControl,
	preferredCluster string) {
	updateClonedPlacementRuleStatus(userPlacementRule, drpc, preferredCluster)
	verifyVRGManifestWorkCreatedAsPrimary(preferredCluster)
	updateManifestWorkStatus(preferredCluster, "vrg", ocmworkv1.WorkApplied)
	verifyUserPlacementRuleDecision(userPlacementRule.Name, userPlacementRule.Namespace, preferredCluster)
	verifyDRPCStatusPreferredClusterExpectation(rmn.Deployed)
	Expect(getManifestWorkCount(preferredCluster)).Should(Equal(2)) // MWs for VRG and ROLES
	waitForCompletion(string(rmn.Deployed))

	latestDRPC := getLatestDRPC()
	// At this point expect the DRPC status condition to have 2 types
	// {Available and PeerReady}
	// Final state is 'Deployed'
	Expect(latestDRPC.Status.Phase).To(Equal(rmn.Deployed))
	Expect(len(latestDRPC.Status.Conditions)).To(Equal(2))
	_, condition := getDRPCCondition(&latestDRPC.Status, rmn.ConditionAvailable)
	Expect(condition.Reason).To(Equal(string(rmn.Deployed)))

	val, err := rmnutil.GetMetricValueSingle("ramen_initial_deploy_time", dto.MetricType_GAUGE)
	Expect(err).NotTo(HaveOccurred())
	Expect(val).NotTo(Equal(0.0)) // failover time should be non-zero
}

func verifyFailoverToSecondary(userPlacementRule *plrv1.PlacementRule, fromCluster, toCluster string,
	isSyncDR bool) {
	recoverToFailoverCluster(userPlacementRule, fromCluster, toCluster)

	// TODO: DRCluster as part of Unfence operation, first unfences
	//       the NetworkFence CR and then deletes it. Hence, by the
	//       time this test is made, depending upon whether NetworkFence
	//       resource is cleaned up or not, number of MW may change.
	if !isSyncDR {
		Expect(getManifestWorkCount(toCluster)).Should(Equal(2)) // MW for VRG+ROLES
	} else {
		Expect(getManifestWorkCount(toCluster)).Should(Equal(3)) // MW for VRG+ROLES+NF
	}

	Expect(getManifestWorkCount(fromCluster)).Should(Equal(1)) // Roles MW

	val, err := rmnutil.GetMetricValueSingle("ramen_failover_time", dto.MetricType_GAUGE)
	Expect(err).NotTo(HaveOccurred())
	Expect(val).NotTo(Equal(0.0)) // failover time should be non-zero

	drpc := getLatestDRPC()
	// At this point expect the DRPC status condition to have 2 types
	// {Available and PeerReady}
	// Final state is 'FailedOver'
	Expect(drpc.Status.Phase).To(Equal(rmn.FailedOver))
	Expect(len(drpc.Status.Conditions)).To(Equal(2))
	_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionAvailable)
	Expect(condition.Reason).To(Equal(string(rmn.FailedOver)))

	userPlacementRule = getLatestUserPlacementRule(userPlacementRule.Name, userPlacementRule.Namespace)
	Expect(userPlacementRule.Status.Decisions[0].ClusterName).To(Equal(toCluster))
}

// +kubebuilder:docs-gen:collapse=Imports
var _ = Describe("DRPlacementControl Reconciler", func() {
	Specify("DRClusters", func() {
		populateDRClusters()
	})
	Context("DRPlacementControl Reconciler Async DR", func() {
		userPlacementRule := &plrv1.PlacementRule{}
		drpc := &rmn.DRPlacementControl{}
		When("An Application is deployed for the first time", func() {
			It("Should deploy to East1ManagedCluster", func() {
				By("Initial Deployment")
				userPlacementRule, drpc = InitialDeploymentAsync(DRPCNamespaceName, UserPlacementRuleName, East1ManagedCluster)
				verifyInitialDRPCDeployment(userPlacementRule, drpc, East1ManagedCluster)
			})
		})
		When("DRAction changes to Failover", func() {
			It("Should not failover to Secondary (West1ManagedCluster) till PV manifest is applied", func() {
				By("\n\n*** Failover - 1\n\n")
				restorePVs = false
				setDRPCSpecExpectationTo(rmn.ActionFailover, East1ManagedCluster, West1ManagedCluster)
				verifyUserPlacementRuleDecisionUnchanged(userPlacementRule.Name, userPlacementRule.Namespace, East1ManagedCluster)
				Expect(getManifestWorkCount(West1ManagedCluster)).Should(Equal(2)) // MWs for VRG and VRG ROLES
				Expect(len(userPlacementRule.Status.Decisions)).Should(Equal(0))
				restorePVs = true
			})
			It("Should failover to Secondary (West1ManagedCluster)", func() {
				// ----------------------------- FAILOVER TO SECONDARY (West1ManagedCluster) --------------------------------------
				By("\n\n*** Failover - 1\n\n")
				verifyFailoverToSecondary(userPlacementRule, East1ManagedCluster, West1ManagedCluster, false)
			})
		})
		When("DRAction is set to Relocate", func() {
			It("Should relocate to Primary (East1ManagedCluster)", func() {
				// ----------------------------- RELOCATION TO PRIMARY --------------------------------------
				By("\n\n*** Relocate - 1\n\n")
				runRelocateAction(userPlacementRule, West1ManagedCluster, false)
			})
		})
		When("DRAction is cleared after relocation", func() {
			It("Should not do anything", func() {
				// ----------------------------- Clear DRAction --------------------------------------
				clearDRActionAfterRelocate(userPlacementRule, East1ManagedCluster, West1ManagedCluster)
			})
		})
		When("DRAction is changed to Failover after relocation", func() {
			It("Should failover again to Secondary (West1ManagedCluster)", func() {
				// ----------------------------- FAILOVER TO SECONDARY --------------------------------------
				By("\n\n*** Failover - 3\n\n")
				runFailoverAction(userPlacementRule, East1ManagedCluster, West1ManagedCluster, false)
			})
		})
		When("DRAction is cleared after failover", func() {
			It("Should not do anything", func() {
				// ----------------------------- Clear DRAction --------------------------------------
				By("\n\n>>> clearing DRAction")
				clearDRActionAfterFailover(userPlacementRule, East1ManagedCluster, West1ManagedCluster)
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
				Expect(userPlacementRule.Status.Decisions[0].ClusterName).To(Equal(West1ManagedCluster))
			})
		})
		When("DRAction is set to Relocate", func() {
			It("Should relocate to Primary (East1ManagedCluster)", func() {
				// ----------------------------- RELOCATION TO PRIMARY --------------------------------------
				By("\n\n*** relocate 2\n\n")
				runRelocateAction(userPlacementRule, West1ManagedCluster, false)
			})
		})

		When("Deleting user PlacementRule", func() {
			It("Should cleanup DRPC", func() {
				// ----------------------------- DELETE DRPC from PRIMARY --------------------------------------
				By("\n\n*** DELETE User PlacementRule ***\n\n")
				deleteUserPlacementRule()
				drpc := getLatestDRPC()
				_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionPeerReady)
				Expect(condition).NotTo(BeNil())
				// waitForCompletion("deleted")
				// Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(1)) // Roles MW
			})
		})

		When("Deleting DRPC", func() {
			It("Should delete VRG from Primary (East1ManagedCluster)", func() {
				// ----------------------------- DELETE DRPC from PRIMARY --------------------------------------
				By("\n\n*** DELETE DRPC ***\n\n")
				deleteDRPC()
				waitForCompletion("deleted")
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(1)) // Roles MW
				deleteDRPolicyAsync()
				deleteDRClustersAsync()
			})
		})
	})
	Context("DRPlacementControl Reconciler Sync DR", func() {
		userPlacementRule := &plrv1.PlacementRule{}
		drpc := &rmn.DRPlacementControl{}
		Specify("DRClusters", func() {
			populateDRClusters()
		})
		When("An Application is deployed for the first time", func() {
			It("Should deploy to East1ManagedCluster", func() {
				By("Initial Deployment")
				userPlacementRule, drpc = InitialDeploymentSync(DRPCNamespaceName, UserPlacementRuleName, East1ManagedCluster)
				verifyInitialDRPCDeployment(userPlacementRule, drpc, East1ManagedCluster)
			})
		})
		When("DRAction changes to Failover", func() {
			It("Should not failover to Secondary (East2ManagedCluster) till PV manifest is applied", func() {
				By("\n\n*** Failover - 1\n\n")
				restorePVs = false
				fenceCluster(East1ManagedCluster)
				setDRPCSpecExpectationTo(rmn.ActionFailover, East1ManagedCluster, East2ManagedCluster)
				verifyUserPlacementRuleDecisionUnchanged(userPlacementRule.Name, userPlacementRule.Namespace, East1ManagedCluster)
				// MWs for VRG, VRG ROLES and the MW for NetworkFence CR to fence off
				// East1ManagedCluster
				Expect(getManifestWorkCount(East2ManagedCluster)).Should(Equal(3))
				Expect(len(userPlacementRule.Status.Decisions)).Should(Equal(0))
				restorePVs = true
			})
			It("Should failover to Secondary (East2ManagedCluster)", func() {
				By("\n\n*** Failover - 1\n\n")
				verifyFailoverToSecondary(userPlacementRule, East1ManagedCluster, East2ManagedCluster, true)
			})
		})
		When("DRAction is set to Relocate", func() {
			It("Should relocate to Primary (East1ManagedCluster)", func() {
				// ----------------------------- RELOCATION TO PRIMARY FOR SYNC DR -------------------
				By("\n\n*** relocate 2\n\n")
				runRelocateAction(userPlacementRule, East2ManagedCluster, true)
			})
		})
		When("DRAction is cleared after relocation", func() {
			It("Should not do anything", func() {
				// ----------------------------- Clear DRAction --------------------------------------
				clearDRActionAfterRelocate(userPlacementRule, East1ManagedCluster, East2ManagedCluster)
			})
		})
		When("DRAction is changed to Failover after relocation", func() {
			It("Should failover again to Secondary (East2ManagedCluster)", func() {
				// ----------------------------- FAILOVER TO SECONDARY FOR SYNC DR--------------------
				By("\n\n*** Failover - 3\n\n")
				runFailoverAction(userPlacementRule, East1ManagedCluster, East2ManagedCluster, true)
			})
		})
		When("DRAction is cleared after failover", func() {
			It("Should not do anything", func() {
				// ----------------------------- Clear DRAction --------------------------------------
				By("\n\n>>> clearing DRAction")
				clearDRActionAfterFailover(userPlacementRule, East1ManagedCluster, East2ManagedCluster)
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
				Expect(userPlacementRule.Status.Decisions[0].ClusterName).To(Equal(East2ManagedCluster))
			})
		})
		When("DRAction is set to Relocate", func() {
			It("Should relocate to Primary (East1ManagedCluster)", func() {
				// ----------------------------- RELOCATION TO PRIMARY FOR SYNC DR------------------------
				By("\n\n*** relocate 2\n\n")
				runRelocateAction(userPlacementRule, East2ManagedCluster, true)
			})
		})
		When("Deleting user PlacementRule", func() {
			It("Should cleanup DRPC", func() {
				By("\n\n*** DELETE User PlacementRule ***\n\n")
				deleteUserPlacementRule()
				drpc := getLatestDRPC()
				_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionPeerReady)
				Expect(condition).NotTo(BeNil())
				// waitForCompletion("deleted")
				// Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(1)) // Roles MW
			})
		})

		When("Deleting DRPC", func() {
			It("Should delete VRG from Primary (East1ManagedCluster)", func() {
				By("\n\n*** DELETE DRPC ***\n\n")
				deleteDRPC()
				waitForCompletion("deleted")
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(1)) // Roles MW
				deleteDRPolicySync()
				deleteDRClustersSync()
			})
		})
	})
<<<<<<< HEAD
<<<<<<< HEAD
=======
<<<<<<< HEAD
=======
>>>>>>> Fix VRG/VolSync unit tests
	Specify("delete s3 profiles and secret", func() {
		s3ProfilesDelete()
	})
>>>>>>> simplify ramen volsync orchestration
})
