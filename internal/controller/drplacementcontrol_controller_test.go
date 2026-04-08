// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/format"
	. "github.com/onsi/gomega/gstruct"
	plrv1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	spokeClusterV1 "open-cluster-management.io/api/cluster/v1"
	clrapiv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	ocmworkv1 "open-cluster-management.io/api/work/v1"
	gppv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	viewv1beta1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/view/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	controllers "github.com/ramendr/ramen/internal/controller"
	argocdv1alpha1hack "github.com/ramendr/ramen/internal/controller/argocd"
	rmnutil "github.com/ramendr/ramen/internal/controller/util"
)

const (
	DRPCCommonName        = "drpc-name"
	DefaultDRPCNamespace  = "drpc-namespace"
	ApplicationNamespace  = "vrg-namespace"
	DRPC2Name             = "drpc-name2"
	DRPC2NamespaceName    = "drpc-namespace2"
	UserPlacementRuleName = "user-placement-rule"
	UserPlacementName     = "user-placement"
	East1ManagedCluster   = "east1-cluster"
	East2ManagedCluster   = "east2-cluster"
	West1ManagedCluster   = "west1-cluster"
	AsyncDRPolicyName     = "my-async-dr-peers"
	SyncDRPolicyName      = "my-sync-dr-peers"
	MModeReplicationID    = "storage-replication-id-1"
	MModeCSIProvisioner   = "test.csi.com"
)

var (
	ProtectedPVCCount   = 2 // Count of fake PVCs reported in the VRG status
	RunningVolSyncTests = false
	UseApplicationSet   = false

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
		ObjectMeta: metav1.ObjectMeta{Name: DefaultDRPCNamespace},
	}

	appNamespace2 = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: DRPC2NamespaceName},
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

	syncDRPolicy = &rmn.DRPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: SyncDRPolicyName,
		},
		Spec: rmn.DRPolicySpec{
			DRClusters: []string{East1ManagedCluster, East2ManagedCluster},
		},
	}

	appSet = argocdv1alpha1hack.ApplicationSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "simple-appset",
			Namespace: DefaultDRPCNamespace,
		},
		Spec: argocdv1alpha1hack.ApplicationSetSpec{
			Template: argocdv1alpha1hack.ApplicationSetTemplate{
				ApplicationSetTemplateMeta: argocdv1alpha1hack.ApplicationSetTemplateMeta{},
				Spec: argocdv1alpha1hack.ApplicationSpec{
					Project: "default",
					Destination: argocdv1alpha1hack.ApplicationDestination{
						Namespace: ApplicationNamespace,
					},
				},
			},
			Generators: []argocdv1alpha1hack.ApplicationSetGenerator{
				{
					ClusterDecisionResource: &argocdv1alpha1hack.DuckTypeGenerator{
						LabelSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								clrapiv1beta1.PlacementLabel: UserPlacementName,
							},
						},
					},
				},
			},
		},
	}

	placementDecision = &clrapiv1beta1.PlacementDecision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf(controllers.PlacementDecisionName, UserPlacementName, 1),
			Namespace: DefaultDRPCNamespace,
			Labels: map[string]string{
				"cluster.open-cluster-management.io/decision-group-index": "0",
				"cluster.open-cluster-management.io/decision-group-name":  "",
				"cluster.open-cluster-management.io/placement":            UserPlacementName,
			},
		},
	}
)

func waitForCondition(d, poll time.Duration, desc string, cond func() bool) error {
	deadline := time.Now().Add(d)
	for {
		if cond() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out: %s", desc)
		}
		time.Sleep(poll)
	}
}

func ensureConsistent(d, poll time.Duration, desc string, cond func() bool) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if !cond() {
			return fmt.Errorf("consistency violated: %s", desc)
		}
		time.Sleep(poll)
	}
	return nil
}

func getSyncDRPolicy() *rmn.DRPolicy {
	return &rmn.DRPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: SyncDRPolicyName,
		},
		Spec: rmn.DRPolicySpec{
			DRClusters: []string{East1ManagedCluster, East2ManagedCluster},
		},
	}
}

var drstate string

// FakeProgressCallback of function type
func FakeProgressCallback(namespace string, state string) {
	drstate = state
}

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

func getDefaultVRG(namespace string) *rmn.VolumeReplicationGroup {
	return &rmn.VolumeReplicationGroup{
		TypeMeta:   metav1.TypeMeta{Kind: "VolumeReplicationGroup", APIVersion: "ramendr.openshift.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: DRPCCommonName, Namespace: namespace},
		Spec: rmn.VolumeReplicationGroupSpec{
			Async: &rmn.VRGAsyncSpec{
				SchedulingInterval: schedulingInterval,
			},
			ReplicationState: rmn.Primary,
			PVCSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"appclass": "gold"},
			},
			S3Profiles: []string{s3Profiles[0].S3ProfileName},
		},
	}
}

var restorePVs = true

func setRestorePVsComplete() {
	restorePVs = true
}

func setRestorePVsIncomplete() {
	restorePVs = false
}

func isRestorePVsComplete() bool {
	return restorePVs
}

var ClusterIsDown string

func setClusterDown(clusterName string) {
	ClusterIsDown = clusterName
}

func resetClusterDown() {
	ClusterIsDown = ""
}

var ToggleUIDChecks bool // default false

func setToggleUIDChecks() {
	ToggleUIDChecks = true
}

func resetToggleUIDChecks() {
	ToggleUIDChecks = false
}

var fakeSecondaryFor string

/*func setFakeSecondary(clusterName string) {
	fakeSecondaryFor = clusterName
}*/

//nolint:cyclop
func (f FakeMCVGetter) GetVRGFromManagedCluster(resourceName, resourceNamespace, managedCluster string,
	annnotations map[string]string,
) (*rmn.VolumeReplicationGroup, error) {
	if managedCluster == ClusterIsDown {
		return nil, fmt.Errorf("%s: Faking cluster down %s", getFunctionNameAtIndex(2), managedCluster)
	}

	vrg, err := GetFakeVRGFromMCVUsingMW(managedCluster, resourceNamespace)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return fakeVRGConditionally(resourceNamespace, managedCluster, err)
		}

		return nil, err
	}

	switch getFunctionNameAtIndex(2) {
	case "ensureClusterDataRestored": // TODO: not invoked during tests
		return nil, nil // Returning nil vrg in case this gets invoked to ensure test failure

	case "ensureVRGDeleted":
		return nil, k8serrors.NewNotFound(schema.GroupResource{}, "requested resource not found in ManagedCluster")

	case "checkAccessToVRGOnCluster":
		return nil, nil

	case "isValidFailoverTarget":
		fallthrough
	case "updateResourceCondition":
		fallthrough
	case "ensureVRGIsSecondaryOnCluster":
		fallthrough
	case "ensureDataProtectedOnCluster":
		fallthrough
	case "getVRGsFromManagedClusters":
		return vrg, nil
	}

	return nil, fmt.Errorf("unknown caller %s", getFunctionNameAtIndex(2))
}

func fakeVRGConditionally(resourceNamespace, managedCluster string, err error) (*rmn.VolumeReplicationGroup, error) {
	switch getFunctionNameAtIndex(4) {
	case "getVRGs":
		// Called only from DRCluster reconciler, at present
		return fakeVRGWithMModesProtectedPVC(resourceNamespace), nil

	case "determineDRPCState":
		if ToggleUIDChecks {
			// Fake it, no DRPC UID annotation set for VRG
			return getDefaultVRG(resourceNamespace), nil
		}

		return nil, err
	}

	if getFunctionNameAtIndex(3) == "isValidFailoverTarget" && // TODO: Only this needs handling
		fakeSecondaryFor == managedCluster {
		vrg := getDefaultVRG(resourceNamespace)
		vrg.Spec.ReplicationState = rmn.Secondary

		return vrg, nil
	}

	return nil, err
}

func (f FakeMCVGetter) DeleteVRGManagedClusterView(
	resourceName, resourceNamespace, clusterName, resourceType string,
) error {
	return nil
}

func (f FakeMCVGetter) DeleteNamespaceManagedClusterView(
	resourceName, resourceNamespace, clusterName, resourceType string,
) error {
	return nil
}

func getVRGNamespace(defaultNamespace string) string {
	if UseApplicationSet {
		return ApplicationNamespace
	}

	return defaultNamespace
}

//nolint:funlen
func GetFakeVRGFromMCVUsingMW(managedCluster, resourceNamespace string,
) (*rmn.VolumeReplicationGroup, error) {
	manifestLookupKey := types.NamespacedName{
		Name:      rmnutil.ManifestWorkName(DRPCCommonName, getVRGNamespace(resourceNamespace), "vrg"),
		Namespace: managedCluster,
	}

	mw := &ocmworkv1.ManifestWork{}

	err := k8sClient.Get(context.TODO(), manifestLookupKey, mw)
	if k8serrors.IsNotFound(err) {
		return nil, k8serrors.NewNotFound(schema.GroupResource{},
			fmt.Sprintf("requested resource not found in ManagedCluster %s", managedCluster))
	}

	vrg := &rmn.VolumeReplicationGroup{}

	err = yaml.Unmarshal(mw.Spec.Workload.Manifests[0].Raw, vrg)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal VRG: %w", err)
	}

	vrg.Generation = 1
	vrg.Status.ObservedGeneration = 1

	switch vrg.Spec.ReplicationState {
	case rmn.Primary:
		vrg.Status.State = rmn.PrimaryState
	case rmn.Secondary:
		vrg.Status.State = rmn.SecondaryState
	default:
		vrg.Status.State = rmn.UnknownState
	}

	vrg.Status.PrepareForFinalSyncComplete = true
	vrg.Status.FinalSyncComplete = true
	vrg.Status.ProtectedPVCs = []rmn.ProtectedPVC{}

	if RunningVolSyncTests {
		createFakeProtectedPVCsForVolSync(vrg)
	} else {
		createFakeProtectedPVCsForVolRep(vrg)
	}

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
		Message:            "Data Ready",
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: vrg.Generation,
	})

	reason := controllers.VRGConditionReasonClusterDataRestored
	status := metav1.ConditionTrue

	if !isRestorePVsComplete() {
		reason = controllers.VRGConditionReasonProgressing
		status = metav1.ConditionFalse
	}

	vrg.Status.Conditions = append(vrg.Status.Conditions, metav1.Condition{
		Type:               controllers.VRGConditionTypeClusterDataReady,
		Reason:             reason,
		Status:             status,
		Message:            "Cluster Data Protected",
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: vrg.Generation,
	})

	vrg.Status.Conditions = append(vrg.Status.Conditions, metav1.Condition{
		Type:               controllers.VRGConditionTypeDataProtected,
		Reason:             controllers.VRGConditionReasonDataProtected,
		Status:             metav1.ConditionTrue,
		Message:            "Data Protected",
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: vrg.Generation,
	})

	vrg.Status.Conditions = append(vrg.Status.Conditions, metav1.Condition{
		Type:               controllers.VRGConditionTypeNoClusterDataConflict,
		Reason:             controllers.VRGConditionReasonNoConflictDetected,
		Status:             metav1.ConditionTrue,
		Message:            "No resource conflict",
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: vrg.Generation,
	})

	t := metav1.Now()
	vrg.Status.LastGroupSyncTime = &t

	return vrg, nil
}

func createFakeProtectedPVCsForVolRep(vrg *rmn.VolumeReplicationGroup) {
	for i := 0; i < ProtectedPVCCount; i++ {
		protectedPVC := rmn.ProtectedPVC{}
		protectedPVC.Name = fmt.Sprintf("fakePVC%d", i)
		protectedPVC.StorageIdentifiers.ReplicationID.ID = MModeReplicationID
		protectedPVC.StorageIdentifiers.StorageProvisioner = MModeCSIProvisioner
		protectedPVC.StorageIdentifiers.ReplicationID.Modes = []rmn.MMode{rmn.MModeFailover}

		vrg.Status.ProtectedPVCs = append(vrg.Status.ProtectedPVCs, protectedPVC)
	}
}

func createFakeProtectedPVCsForVolSync(vrg *rmn.VolumeReplicationGroup) {
	for i := 0; i < ProtectedPVCCount; i++ {
		protectedPVC := rmn.ProtectedPVC{}
		protectedPVC.Name = fmt.Sprintf("fakePVC-%d-for-volsync", i)
		protectedPVC.ProtectedByVolSync = true

		vrg.Status.ProtectedPVCs = append(vrg.Status.ProtectedPVCs, protectedPVC)
	}
}

func fakeVRGWithMModesProtectedPVC(vrgNamespace string) *rmn.VolumeReplicationGroup {
	vrg := getDefaultVRG(vrgNamespace).DeepCopy()
	vrg.Status = rmn.VolumeReplicationGroupStatus{
		State:                       rmn.PrimaryState,
		PrepareForFinalSyncComplete: true,
		FinalSyncComplete:           true,
		ProtectedPVCs:               []rmn.ProtectedPVC{},
	}

	protectedPVC := &rmn.ProtectedPVC{}
	protectedPVC.StorageIdentifiers.ReplicationID.ID = MModeReplicationID
	protectedPVC.StorageIdentifiers.StorageProvisioner = MModeCSIProvisioner
	protectedPVC.StorageIdentifiers.ReplicationID.Modes = []rmn.MMode{rmn.MModeFailover}

	vrg.Status.ProtectedPVCs = append(vrg.Status.ProtectedPVCs, *protectedPVC)

	return vrg
}

func createPlacementRule(name, namespace string) (*plrv1.PlacementRule, error) {
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
	if err != nil {
		return nil, err
	}

	return placementRule, nil
}

func createPlacement(name, namespace string) (*clrapiv1beta1.Placement, error) {
	var numberOfClustersToDeployTo int32 = 1

	placement := &clrapiv1beta1.Placement{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				clrapiv1beta1.PlacementDisableAnnotation: "true",
			},
		},
		Spec: clrapiv1beta1.PlacementSpec{
			NumberOfClusters: &numberOfClustersToDeployTo,
			ClusterSets:      []string{East1ManagedCluster, West1ManagedCluster},
		},
	}

	err := k8sClient.Create(context.TODO(), placement)
	if err != nil {
		return nil, err
	}

	return placement, nil
}

func createDRPC(placementName, name, namespace, drPolicyName, preferredCluster string) (*rmn.DRPlacementControl, error) {
	drpc := &rmn.DRPlacementControl{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: rmn.DRPlacementControlSpec{
			PlacementRef: corev1.ObjectReference{
				Name: placementName,
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
			KubeObjectProtection: &rmn.KubeObjectProtectionSpec{},
			PreferredCluster:     preferredCluster,
		},
	}
	if err := k8sClient.Create(context.TODO(), drpc); err != nil {
		return nil, err
	}

	return drpc, nil
}

//nolint:unparam
func deleteUserPlacementRule(name, namespace string) error {
	userPlacementRule, err := getLatestUserPlacementRule(name, namespace)
	if err != nil {
		return err
	}

	if err := k8sClient.Delete(context.TODO(), userPlacementRule); err != nil {
		return err
	}

	return nil
}

func deleteUserPlacement() error {
	userPlacement, err := getLatestUserPlacement(UserPlacementName, DefaultDRPCNamespace)
	if err != nil {
		return err
	}

	if err := k8sClient.Delete(context.TODO(), userPlacement); err != nil {
		return err
	}

	return nil
}

func deleteDRPC() error {
	drpc, err := getLatestDRPC(DefaultDRPCNamespace)
	if err != nil {
		return err
	}

	if err := k8sClient.Delete(context.TODO(), drpc); err != nil {
		return err
	}

	return nil
}

func ensureNamespaceMWsDeletedFromAllClusters(namespace string) {
	foundMW := &ocmworkv1.ManifestWork{}
	mwName := fmt.Sprintf(rmnutil.ManifestWorkNameFormat, DRPCCommonName, namespace, rmnutil.MWTypeNS)

	err := k8sClient.Get(context.TODO(),
		types.NamespacedName{Name: mwName, Namespace: East1ManagedCluster},
		foundMW)
	if err == nil {
		Expect(foundMW).To(BeNil())
	}

	err = k8sClient.Get(context.TODO(),
		types.NamespacedName{Name: mwName, Namespace: West1ManagedCluster},
		foundMW)
	if err == nil {
		Expect(foundMW).To(BeNil())
	}
}

func setDRPCSpecExpectationTo(namespace, preferredCluster, failoverCluster string, action rmn.DRAction) error {
	drpcLookupKey := types.NamespacedName{
		Name:      DRPCCommonName,
		Namespace: namespace,
	}
	latestDRPC := &rmn.DRPlacementControl{}
	retryErr := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		err := k8sClient.Get(context.TODO(), drpcLookupKey, latestDRPC)
		if err != nil {
			return err
		}

		latestDRPC.Spec.Action = action
		latestDRPC.Spec.PreferredCluster = preferredCluster
		latestDRPC.Spec.FailoverCluster = failoverCluster

		return k8sClient.Update(context.TODO(), latestDRPC)
	})

	if retryErr != nil {
		return retryErr
	}

	return waitForCondition(timeout, interval, "failed to update DRPC DR action on time", func() bool {
		var err error
		latestDRPC, err = getLatestDRPC(namespace)
		if err != nil {
			return false
		}

		return latestDRPC.Spec.Action == action
	})
}

func getLatestDRPC(namespace string) (*rmn.DRPlacementControl, error) {
	drpcLookupKey := types.NamespacedName{
		Name:      DRPCCommonName,
		Namespace: namespace,
	}
	latestDRPC := &rmn.DRPlacementControl{}
	err := apiReader.Get(context.TODO(), drpcLookupKey, latestDRPC)
	if err != nil {
		return nil, err
	}

	return latestDRPC, nil
}

func clearDRPCStatus() error {
	drpcLookupKey := types.NamespacedName{
		Name:      DRPCCommonName,
		Namespace: DefaultDRPCNamespace,
	}
	latestDRPC := &rmn.DRPlacementControl{}
	retryErr := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		err := k8sClient.Get(context.TODO(), drpcLookupKey, latestDRPC)
		if err != nil {
			return err
		}

		latestDRPC.Status = rmn.DRPlacementControlStatus{}

		return k8sClient.Status().Update(context.TODO(), latestDRPC)
	})

	if retryErr != nil {
		return retryErr
	}

	return nil
}

func clearFakeUserPlacementRuleStatus(name, namespace string) error {
	usrPlRuleLookupKey := types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}

	usrPlRule := &plrv1.PlacementRule{}
	retryErr := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		err := k8sClient.Get(context.TODO(), usrPlRuleLookupKey, usrPlRule)
		if err != nil {
			return err
		}

		usrPlRule.Status = plrv1.PlacementRuleStatus{}

		return k8sClient.Status().Update(context.TODO(), usrPlRule)
	})

	if retryErr != nil {
		return retryErr
	}

	return nil
}

func createNamespace(ns *corev1.Namespace) error {
	nsName := types.NamespacedName{Name: ns.Name}

	err := k8sClient.Get(context.TODO(), nsName, &corev1.Namespace{})
	if err != nil {
		if err := k8sClient.Create(context.TODO(), ns); err != nil {
			return fmt.Errorf("failed to create %v managed cluster namespace: %w", ns.Name, err)
		}
	}

	return nil
}

func createNamespacesAsync(appNamespace *corev1.Namespace) error {
	if err := createNamespace(east1ManagedClusterNamespace); err != nil {
		return err
	}

	if err := createNamespace(west1ManagedClusterNamespace); err != nil {
		return err
	}

	return createNamespace(appNamespace)
}

func createManagedClusters(managedClusters []*spokeClusterV1.ManagedCluster) error {
	for _, cl := range managedClusters {
		mcLookupKey := types.NamespacedName{Name: cl.Name}
		mcObj := &spokeClusterV1.ManagedCluster{}

		err := k8sClient.Get(context.TODO(), mcLookupKey, mcObj)
		if err != nil {
			clinstance := cl.DeepCopy()

			err := k8sClient.Create(context.TODO(), clinstance)
			if err != nil {
				return err
			}

			updateManagedClusterStatus(k8sClient, clinstance)

			continue
		}

		updateManagedClusterStatus(k8sClient, mcObj)
	}

	return nil
}

func populateDRClusters() {
	drClusters = make([]rmn.DRCluster, 0, 3)
	drClusters = append(drClusters,
		rmn.DRCluster{
			ObjectMeta: metav1.ObjectMeta{Name: East1ManagedCluster, Annotations: map[string]string{
				"drcluster.ramendr.openshift.io/storage-secret-name":      "tmp",
				"drcluster.ramendr.openshift.io/storage-secret-namespace": "tmp",
				"drcluster.ramendr.openshift.io/storage-clusterid":        "tmp",
				"drcluster.ramendr.openshift.io/storage-driver":           "tmp.storage.com",
			}},
			Spec: rmn.DRClusterSpec{S3ProfileName: s3Profiles[0].S3ProfileName, Region: "east", CIDRs: cidrs[0]},
		},
		rmn.DRCluster{
			ObjectMeta: metav1.ObjectMeta{Name: West1ManagedCluster, Annotations: map[string]string{
				"drcluster.ramendr.openshift.io/storage-secret-name":      "tmp2",
				"drcluster.ramendr.openshift.io/storage-secret-namespace": "tmp2",
				"drcluster.ramendr.openshift.io/storage-clusterid":        "tmp2",
				"drcluster.ramendr.openshift.io/storage-driver":           "tmp2.storage.com",
			}},
			Spec: rmn.DRClusterSpec{S3ProfileName: s3Profiles[0].S3ProfileName, Region: "west"},
		},
		rmn.DRCluster{
			ObjectMeta: metav1.ObjectMeta{Name: East2ManagedCluster, Annotations: map[string]string{
				"drcluster.ramendr.openshift.io/storage-secret-name":      "tmp",
				"drcluster.ramendr.openshift.io/storage-secret-namespace": "tmp",
				"drcluster.ramendr.openshift.io/storage-clusterid":        "tmp",
				"drcluster.ramendr.openshift.io/storage-driver":           "tmp.storage.com",
			}},
			Spec: rmn.DRClusterSpec{S3ProfileName: s3Profiles[0].S3ProfileName, Region: "east", CIDRs: cidrs[1]},
		},
	)
}

func createDRClusters(inClusters []*spokeClusterV1.ManagedCluster) error {
	for _, managedCluster := range inClusters {
		for idx := range drClusters {
			if managedCluster.Name == drClusters[idx].Name {
				err := k8sClient.Create(context.TODO(), &drClusters[idx])
				if err != nil {
					return err
				}

				updateDRClusterManifestWorkStatus(k8sClient, apiReader, drClusters[idx].Name)
				updateDRClusterConfigMWStatus(k8sClient, apiReader, drClusters[idx].Name)
			}
		}
	}

	return nil
}

func createPlacementDecision() error {
	if err := deletePlacementDecision(); err != nil {
		return err
	}

	plDecision := placementDecision.DeepCopy()

	return k8sClient.Create(context.TODO(), plDecision)
}

func createDRClustersAsync() error {
	return createDRClusters(asyncClusters)
}

func createDRPolicy(inDRPolicy *rmn.DRPolicy) error {
	err := k8sClient.Create(context.TODO(), inDRPolicy)
	if err != nil {
		return err
	}

	return waitForCondition(timeout, interval, "waiting for DRPolicy to be validated", func() bool {
		drpolicy := &rmn.DRPolicy{}
		if err := apiReader.Get(context.TODO(), types.NamespacedName{Name: inDRPolicy.Name}, drpolicy); err != nil {
			return false
		}

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
	})
}

func createDRPolicyAsync() error {
	policy := asyncDRPolicy.DeepCopy()

	return createDRPolicy(policy)
}

func createAppSet() error {
	return k8sClient.Create(context.TODO(), &appSet)
}

func deleteAppSet() error {
	if err := k8sClient.Delete(context.TODO(), &appSet); err != nil {
		return err
	}

	return waitForCondition(timeout, interval, "waiting for AppSet to be deleted", func() bool {
		resource := &argocdv1alpha1hack.ApplicationSet{}

		return k8serrors.IsNotFound(apiReader.Get(context.TODO(), types.NamespacedName{
			Namespace: appSet.Namespace,
			Name:      appSet.Name,
		}, resource))
	})
}

func deleteDRCluster(inDRCluster *rmn.DRCluster) error {
	if err := k8sClient.Delete(context.TODO(), inDRCluster); err != nil {
		return err
	}

	return waitForCondition(timeout, interval, "waiting for DRCluster to be deleted", func() bool {
		drcluster := &rmn.DRCluster{}

		return k8serrors.IsNotFound(apiReader.Get(context.TODO(), types.NamespacedName{
			Namespace: inDRCluster.Namespace,
			Name:      inDRCluster.Name,
		}, drcluster))
	})
}

func deleteDRClusters(inClusters []*spokeClusterV1.ManagedCluster) error {
	for _, managedCluster := range inClusters {
		for idx := range drClusters {
			if managedCluster.Name == drClusters[idx].Name {
				if err := deleteDRCluster(&drClusters[idx]); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func deleteDRClustersAsync() error {
	return deleteDRClusters(asyncClusters)
}

func deleteDRPolicyAsync() error {
	if err := k8sClient.Delete(context.TODO(), asyncDRPolicy); err != nil {
		return err
	}

	return nil
}

// createVRGMW creates a basic (always Primary) ManifestWork for a VRG, used to fake existing VRG MW
// to test upgrade cases for DRPC based UID adoption
func createVRGMW(name, namespace, homeCluster string) error {
	vrg := getDefaultVRG(namespace)
	vrg.Generation = 1

	mwu := rmnutil.MWUtil{
		Client:          k8sClient,
		APIReader:       k8sClient,
		Ctx:             context.TODO(),
		Log:             logr.Logger{},
		InstName:        name,
		TargetNamespace: namespace,
	}

	_, err := mwu.CreateOrUpdateVRGManifestWork(name, namespace, homeCluster, *vrg, nil)

	return err
}

func updateManifestWorkStatus(clusterNamespace, vrgNamespace, mwType, workType string) error {
	manifestLookupKey := types.NamespacedName{
		Name:      rmnutil.ManifestWorkName(DRPCCommonName, getVRGNamespace(vrgNamespace), mwType),
		Namespace: clusterNamespace,
	}
	mw := &ocmworkv1.ManifestWork{}

	if err := waitForCondition(timeout, interval,
		fmt.Sprintf("failed to wait for manifest creation for type %s cluster %s", mwType, clusterNamespace),
		func() bool {
			err := k8sClient.Get(context.TODO(), manifestLookupKey, mw)

			return err == nil
		}); err != nil {
		return err
	}

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

	retryErr := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		err := k8sClient.Get(context.TODO(), manifestLookupKey, mw)
		if err != nil {
			return err
		}

		mw.Status = pvManifestStatus

		return k8sClient.Status().Update(context.TODO(), mw)
	})

	if retryErr != nil {
		return retryErr
	}

	return waitForCondition(timeout, interval,
		"failed to wait for PV manifest condition type to change to 'Applied'", func() bool {
			err := k8sClient.Get(context.TODO(), manifestLookupKey, mw)

			return err == nil && len(mw.Status.Conditions) != 0
		})
}

func ensureDRPolicyIsNotDeleted(drpc *rmn.DRPlacementControl) {
	Consistently(func() bool {
		drpolicy := &rmn.DRPolicy{}
		name := drpc.Spec.DRPolicyRef.Name
		err := apiReader.Get(context.TODO(), types.NamespacedName{Name: name}, drpolicy)
		// TODO: Technically we need to Expect deletion TS is non-zero as well here!
		return err == nil
	}, timeout, interval).Should(BeTrue(), "DRPolicy deleted prematurely, with active DRPC references")
}

func ensureDRPolicyIsDeleted(drpolicyName string) {
	drpolicy := &rmn.DRPolicy{}
	Eventually(func() error {
		return apiReader.Get(context.TODO(), types.NamespacedName{Name: drpolicyName}, drpolicy)
	}, timeout, interval).Should(
		MatchError(
			k8serrors.NewNotFound(
				schema.GroupResource{
					Group:    rmn.GroupVersion.Group,
					Resource: "drpolicies",
				},
				drpolicyName,
			),
		),
		"DRPolicy %s not found\n%s",
		drpolicyName,
		format.Object(*drpolicy, 0),
	)
}

func checkIfDRPCFinalizerNotAdded(drpc *rmn.DRPlacementControl) {
	Consistently(func() bool {
		drpcl := &rmn.DRPlacementControl{}
		err := apiReader.Get(context.TODO(),
			types.NamespacedName{Name: drpc.Name, Namespace: drpc.Namespace},
			drpcl)
		Expect(err).NotTo(HaveOccurred())

		f := drpcl.GetFinalizers()
		for _, e := range f {
			if e == "drpc.ramendr.openshift.io/finalizer" {
				return false
			}
		}

		return true
	}, timeout, interval).Should(BeTrue(), "DRPlacementControl reconciled with a finalizer",
		"when DRPolicy is in a deleted state")
}

type PlacementType int

const (
	UsePlacementRule             = 1
	UsePlacementWithSubscription = 2
	UsePlacementWithAppSet       = 3
)

func InitialDeploymentAsync(namespace, placementName, homeCluster string, plType PlacementType) (
	client.Object, *rmn.DRPlacementControl,
) {
	Expect(createNamespacesAsync(getNamespaceObj(namespace))).To(Succeed())

	Expect(createManagedClusters(asyncClusters)).To(Succeed())
	Expect(createDRClustersAsync()).To(Succeed())
	Expect(createDRPolicyAsync()).To(Succeed())
	Expect(createPlacementDecision()).To(Succeed())

	return CreatePlacementAndDRPC(namespace, placementName, homeCluster, plType)
}

func CreatePlacementAndDRPC(namespace, placementName, homeCluster string, plType PlacementType) (
	client.Object, *rmn.DRPlacementControl,
) {
	var placementObj client.Object

	var err error

	switch plType {
	case UsePlacementRule:
		placementObj, err = createPlacementRule(placementName, namespace)
		Expect(err).NotTo(HaveOccurred())
	case UsePlacementWithSubscription:
		placementObj, err = createPlacement(placementName, namespace)
		Expect(err).NotTo(HaveOccurred())
	case UsePlacementWithAppSet:
		Expect(createAppSet()).To(Succeed())

		placementObj, err = createPlacement(placementName, namespace)
		Expect(err).NotTo(HaveOccurred())
	default:
		Fail("Wrong placement type")
	}

	drpc, err := createDRPC(placementName, DRPCCommonName, namespace, AsyncDRPolicyName, homeCluster)
	Expect(err).NotTo(HaveOccurred())

	return placementObj, drpc
}

func FollowOnDeploymentAsync(namespace, placementName, homeCluster string) (*plrv1.PlacementRule,
	*rmn.DRPlacementControl,
) {
	Expect(createNamespace(appNamespace2)).To(Succeed())

	placementRule, err := createPlacementRule(placementName, namespace)
	Expect(err).NotTo(HaveOccurred())

	drpc, err := createDRPC(placementName, DRPCCommonName, namespace, AsyncDRPolicyName, homeCluster)
	Expect(err).NotTo(HaveOccurred())

	return placementRule, drpc
}

func verifyVRGManifestWorkCreatedAsPrimary(namespace, managedCluster string) {
	vrgManifestLookupKey := types.NamespacedName{
		Name:      rmnutil.DrClusterManifestWorkName,
		Namespace: managedCluster,
	}
	createdVRGRolesManifest := &ocmworkv1.ManifestWork{}

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), vrgManifestLookupKey, createdVRGRolesManifest)

		return err == nil
	}, timeout, interval).Should(BeTrue())

	Expect(len(createdVRGRolesManifest.Spec.Workload.Manifests)).To(Equal(9))

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
		Name:      rmnutil.ManifestWorkName(DRPCCommonName, getVRGNamespace(namespace), "vrg"),
		Namespace: managedCluster,
	}

	mw := &ocmworkv1.ManifestWork{}

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), manifestLookupKey, mw)

		return err == nil
	}, timeout, interval).Should(BeTrue(), fmt.Sprintf("manifestlookup %+v", manifestLookupKey))

	Expect(len(mw.Spec.Workload.Manifests)).To(Equal(1))

	vrgClientManifest := mw.Spec.Workload.Manifests[0]

	Expect(vrgClientManifest).ToNot(BeNil())

	vrg := &rmn.VolumeReplicationGroup{}

	err = yaml.Unmarshal(vrgClientManifest.RawExtension.Raw, &vrg)
	Expect(err).NotTo(HaveOccurred())
	Expect(vrg.Name).Should(Equal(DRPCCommonName))
	Expect(vrg.Spec.PVCSelector.MatchLabels["appclass"]).Should(Equal("gold"))
	Expect(vrg.Spec.ReplicationState).Should(Equal(rmn.Primary))

	// ensure DRPC copied KubeObjectProtection contents to VRG
	drpc, err := getLatestDRPC(namespace)
	Expect(err).NotTo(HaveOccurred())
	Expect(vrg.Spec.KubeObjectProtection).Should(Equal(drpc.Spec.KubeObjectProtection))
}

func getManifestWorkCount(homeClusterNamespace string) (int, error) {
	manifestWorkList := &ocmworkv1.ManifestWorkList{}
	listOptions := &client.ListOptions{Namespace: homeClusterNamespace}

	if err := apiReader.List(context.TODO(), manifestWorkList, listOptions); err != nil {
		return 0, err
	}

	if len(manifestWorkList.Items) == 0 {
		return 0, nil
	}

	// Reduce by one to accommodate for DRClusterConfig ManifestWork
	return len(manifestWorkList.Items) - 1, nil
}

func verifyNSManifestWork(resourceName, namespaceString, managedCluster string) {
	mw := &ocmworkv1.ManifestWork{}
	mwName := fmt.Sprintf(rmnutil.ManifestWorkNameFormat, resourceName, namespaceString, rmnutil.MWTypeNS)
	err := k8sClient.Get(context.TODO(),
		types.NamespacedName{Name: mwName, Namespace: managedCluster},
		mw)

	Expect(err).NotTo(HaveOccurred())

	Expect(mw).ToNot(BeNil())
	Expect(mw.Spec.DeleteOption).ToNot(BeNil())
	Expect(mw.Labels[rmnutil.OCMBackupLabelKey]).To(Equal(""))
}

//nolint:unparam
func getManagedClusterViewCount(homeClusterNamespace string) (int, error) {
	mcvList := &viewv1beta1.ManagedClusterViewList{}
	listOptions := &client.ListOptions{Namespace: homeClusterNamespace}

	if err := k8sClient.List(context.TODO(), mcvList, listOptions); err != nil {
		return 0, err
	}

	return len(mcvList.Items), nil
}

func verifyUserPlacementRuleDecision(name, namespace, homeCluster string) {
	usrPlacementLookupKey := types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}

	usrPlRule := &plrv1.PlacementRule{}

	var placementObj client.Object

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), usrPlacementLookupKey, usrPlRule)
		if k8serrors.IsNotFound(err) {
			usrPlmnt := &clrapiv1beta1.Placement{}

			err = k8sClient.Get(context.TODO(), usrPlacementLookupKey, usrPlmnt)
			if err != nil {
				return false
			}

			placementObj = usrPlmnt
			plDecision := getPlacementDecision(usrPlmnt.GetName(), usrPlmnt.GetNamespace())

			return plDecision != nil && len(plDecision.Status.Decisions) > 0 &&
				plDecision.Status.Decisions[0].ClusterName == homeCluster
		}

		placementObj = usrPlRule

		return err == nil && len(usrPlRule.Status.Decisions) > 0 &&
			usrPlRule.Status.Decisions[0].ClusterName == homeCluster
	}, timeout, interval).Should(BeTrue())

	Expect(placementObj.GetAnnotations()[controllers.DRPCNameAnnotation]).Should(Equal(DRPCCommonName))
	Expect(placementObj.GetAnnotations()[controllers.DRPCNamespaceAnnotation]).Should(Equal(namespace))
}

func waitForDRPCProtected(namespace string) error {
	return waitForCondition(timeout, interval, "DRPC to be protected", func() bool {
		drpc, err := getLatestDRPC(namespace)
		if err != nil {
			return false
		}
		_, cond := getDRPCCondition(&drpc.Status, rmn.ConditionProtected)

		return cond != nil && cond.Status == metav1.ConditionTrue
	})
}

func getPlacementDecision(plName, plNamespace string) *clrapiv1beta1.PlacementDecision {
	plDecision := &clrapiv1beta1.PlacementDecision{}
	plDecisionKey := types.NamespacedName{
		Name:      fmt.Sprintf(controllers.PlacementDecisionName, plName, 1),
		Namespace: plNamespace,
	}

	err := k8sClient.Get(context.TODO(), plDecisionKey, plDecision)
	if err != nil {
		return nil
	}

	return plDecision
}

//nolint:unparam
func verifyUserPlacementRuleDecisionUnchanged(name, namespace, homeCluster string) {
	usrPlacementLookupKey := types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}

	usrPlRule := &plrv1.PlacementRule{}

	var placementObj client.Object

	Consistently(func() bool {
		err := k8sClient.Get(context.TODO(), usrPlacementLookupKey, usrPlRule)
		if k8serrors.IsNotFound(err) {
			usrPlmnt := &clrapiv1beta1.Placement{}

			err = k8sClient.Get(context.TODO(), usrPlacementLookupKey, usrPlmnt)
			if err != nil {
				return false
			}

			placementObj = usrPlmnt
			plDecision := getPlacementDecision(usrPlmnt.GetName(), usrPlmnt.GetNamespace())

			return plDecision != nil && len(plDecision.Status.Decisions) > 0 &&
				plDecision.Status.Decisions[0].ClusterName == homeCluster
		}

		placementObj = usrPlRule

		return err == nil && usrPlRule.Status.Decisions[0].ClusterName == homeCluster
	}, timeout, interval).Should(BeTrue())

	Expect(placementObj.GetAnnotations()[controllers.DRPCNameAnnotation]).Should(Equal(DRPCCommonName))
	Expect(placementObj.GetAnnotations()[controllers.DRPCNamespaceAnnotation]).Should(Equal(namespace))
}

func verifyDRPCStatusPreferredClusterExpectation(namespace string, drState rmn.DRState) {
	drpcLookupKey := types.NamespacedName{
		Name:      DRPCCommonName,
		Namespace: namespace,
	}

	updatedDRPC := &rmn.DRPlacementControl{}

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), drpcLookupKey, updatedDRPC)

		if d := updatedDRPC.Status.PreferredDecision; err == nil && d != (rmn.PlacementDecision{}) {
			idx, condition := getDRPCCondition(&updatedDRPC.Status, rmn.ConditionAvailable)

			return d.ClusterName == East1ManagedCluster &&
				idx != -1 &&
				condition.Reason == string(drState) &&
				len(updatedDRPC.Status.ResourceConditions.ResourceMeta.ProtectedPVCs) == ProtectedPVCCount
		}

		return false
	}, timeout, interval).Should(BeTrue(), fmt.Sprintf("failed waiting for an updated DRPC. State %v", drState))

	Expect(updatedDRPC.Status.PreferredDecision.ClusterName).Should(Equal(East1ManagedCluster))
	_, condition := getDRPCCondition(&updatedDRPC.Status, rmn.ConditionAvailable)
	Expect(condition.Reason).Should(Equal(string(drState)))
}

func getLatestUserPlacementRule(name, namespace string) (*plrv1.PlacementRule, error) {
	usrPlRuleLookupKey := types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}

	usrPlRule := &plrv1.PlacementRule{}

	err := k8sClient.Get(context.TODO(), usrPlRuleLookupKey, usrPlRule)
	if err != nil {
		return nil, err
	}

	return usrPlRule, nil
}

func getLatestUserPlacement(name, namespace string) (*clrapiv1beta1.Placement, error) {
	key := types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}

	plmnt := &clrapiv1beta1.Placement{}

	err := k8sClient.Get(context.TODO(), key, plmnt)
	if err != nil {
		return nil, err
	}

	return plmnt, nil
}

func getLatestUserPlacementDecision(name, namespace string) (*clrapiv1beta1.ClusterDecision, error) {
	key := types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}

	usrPlRule := &plrv1.PlacementRule{}

	err := k8sClient.Get(context.TODO(), key, usrPlRule)
	if err == nil {
		return &clrapiv1beta1.ClusterDecision{
			ClusterName: usrPlRule.Status.Decisions[0].ClusterName,
			Reason:      "PlacementRule Testing",
		}, nil
	}

	if k8serrors.IsNotFound(err) {
		usrPlmnt := &clrapiv1beta1.Placement{}
		err = k8sClient.Get(context.TODO(), key, usrPlmnt)
		if err != nil {
			return nil, err
		}

		plDecision := getPlacementDecision(usrPlmnt.GetName(), usrPlmnt.GetNamespace())
		if plDecision != nil {
			return &clrapiv1beta1.ClusterDecision{
				ClusterName: plDecision.Status.Decisions[0].ClusterName,
				Reason:      "Placement Testing",
			}, nil
		}
	}

	return nil, nil
}

func waitForCompletion(expectedState string) error {
	msg := fmt.Sprintf("failed waiting for state to match. expecting: %s, found %s", expectedState, drstate)

	return waitForCondition(timeout*2, interval, msg, func() bool {
		return drstate == expectedState
	})
}

func waitForDRPCPhaseAndProgression(namespace string, drState rmn.DRState) error {
	msg := fmt.Sprintf("Timed out waiting for Phase to match. Expected %s for drpcNS %s", drState, namespace)

	return waitForCondition(timeout, interval, msg, func() bool {
		drpc, err := getLatestDRPC(namespace)
		if err != nil {
			return false
		}

		return drpc.Status.Phase == drState && drpc.Status.Progression == rmn.ProgressionCompleted
	})
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

//nolint:unparam
func runFailoverAction(placementObj client.Object, fromCluster, toCluster string, isSyncDR bool,
	manualFence bool,
) {
	if isSyncDR {
		fenceCluster(fromCluster, manualFence)
	}

	recoverToFailoverCluster(placementObj, fromCluster, toCluster)
	// TODO: DRCluster as part of Unfence operation, first unfences
	//       the NetworkFence CR and then deletes it. Hence, by the
	//       time this test is made, depending upon whether NetworkFence
	//       resource is cleaned up or not, number of MW may change.
	if !isSyncDR {
		Expect(getManifestWorkCount(toCluster)).Should(BeElementOf(3, 4)) // MW for VRG+DRCluster+NS
	} else {
		if manualFence {
			Expect(getManifestWorkCount(toCluster)).Should(Equal(3)) // MW for VRG+DRCluster + NS
		} else {
			Expect(getManifestWorkCount(toCluster)).Should(Equal(4)) // MW for VRG+DRCluster + NS + NF
		}
	}

	Expect(getManifestWorkCount(fromCluster)).Should(Equal(3)) // DRCluster + NS MW + VRG MW

	drpc, err := getLatestDRPC(placementObj.GetNamespace())
	Expect(err).NotTo(HaveOccurred())
	// At this point expect the DRPC status condition to have 2 types
	// {Available and PeerReady}
	// Final state is 'FailedOver'
	Expect(drpc.Status.Phase).To(Equal(rmn.FailedOver))
	Expect(len(drpc.Status.Conditions)).To(Equal(3))
	_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionAvailable)
	Expect(condition.Reason).To(Equal(string(rmn.FailedOver)))
	Expect(drpc.Status.ActionStartTime).ShouldNot(BeNil())

	decision, err := getLatestUserPlacementDecision(placementObj.GetName(), placementObj.GetNamespace())
	Expect(err).NotTo(HaveOccurred())
	Expect(decision.ClusterName).To(Equal(toCluster))
}

func runRelocateAction(placementObj client.Object, fromCluster string, isSyncDR bool, manualUnfence bool) {
	toCluster1 := "east1-cluster"

	if isSyncDR {
		unfenceCluster(toCluster1, manualUnfence)
	}

	// this is to ensure that drCluster is deleted with
	// empty string as fenceState. Otherwise, in the cleanup
	// section of the test, drPolicy is deleted first and then
	// drCluster is removed. But, drCluster relies on finding
	// a peer cluster through drPolicy for NetworkFence resource
	// create/update/delete operations. Thus, the deletion of the
	// drCluster resource would fail with the error being failure
	// to find a peer cluster. resetdrCluster changes the spec of
	// drCluster fence state to empty string instead of "Unfenced".
	// This ensures that drCluster does not attempt removal of the
	// NetworkFence resource as part of its deletion.
	if !manualUnfence {
		resetdrCluster(toCluster1)
	}

	relocateToPreferredCluster(placementObj, fromCluster)
	// TODO: DRCluster as part of Unfence operation, first unfences
	//       the NetworkFence CR and then deletes it. Hence, by the
	//       time this test is made, depending upon whether NetworkFence
	//       resource is cleaned up or not, number of MW may change.

	// Expect(getManifestWorkCount(toCluster1)).Should(Equal(2)) // MWs for VRG+ROLES
	if !isSyncDR {
		Expect(getManifestWorkCount(fromCluster)).Should(BeElementOf(3, 4)) // DRClusters + NS MW + VRG MW
	} else {
		// By the time this check is made, the NetworkFence CR in the
		// cluster from where the application is migrated might not have
		// been deleted. Hence, the number of MW expectation will be
		// either of 2 or 3.
		Expect(getManifestWorkCount(fromCluster)).Should(BeElementOf(2, 3))
	}

	drpc, err := getLatestDRPC(placementObj.GetNamespace())
	Expect(err).NotTo(HaveOccurred())
	// At this point expect the DRPC status condition to have 2 types
	// {Available and PeerReady}
	// Final state is 'Relocated'
	Expect(drpc.Status.Phase).To(Equal(rmn.Relocated))
	Expect(len(drpc.Status.Conditions)).To(Equal(3))
	_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionAvailable)
	Expect(condition.Reason).To(Equal(string(rmn.Relocated)))

	decision, err := getLatestUserPlacementDecision(placementObj.GetName(), placementObj.GetNamespace())
	Expect(err).NotTo(HaveOccurred())
	Expect(decision.ClusterName).To(Equal(toCluster1))
	Expect(condition.Reason).To(Equal(string(rmn.Relocated)))
	Expect(drpc.GetAnnotations()[controllers.LastAppDeploymentCluster]).To(Equal(toCluster1))
}

func clearDRActionAfterRelocate(userPlacementRule *plrv1.PlacementRule, preferredCluster, failoverCluster string) {
	Expect(setDRPCSpecExpectationTo(userPlacementRule.GetNamespace(), preferredCluster, failoverCluster, "")).To(Succeed())
	Expect(waitForCompletion(string(rmn.Deployed))).To(Succeed())

	drpc, err := getLatestDRPC(userPlacementRule.GetNamespace())
	Expect(err).NotTo(HaveOccurred())
	// At this point expect the DRPC status condition to have 2 types
	// {Available and PeerReady}
	// Final state didn't change and it is 'Relocated' even though we tried to run
	// initial deployment
	Expect(drpc.Status.Phase).To(Equal(rmn.Deployed))
	Expect(len(drpc.Status.Conditions)).To(Equal(3))
	_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionAvailable)
	Expect(condition.Reason).To(Equal(string(rmn.Deployed)))

	decision, err := getLatestUserPlacementDecision(userPlacementRule.Name, userPlacementRule.Namespace)
	Expect(err).NotTo(HaveOccurred())
	Expect(decision.ClusterName).To(Equal(preferredCluster))
}

func relocateToPreferredCluster(placementObj client.Object, fromCluster string) {
	toCluster1 := "east1-cluster"

	Expect(setDRPCSpecExpectationTo(placementObj.GetNamespace(), toCluster1, fromCluster, rmn.ActionRelocate)).To(Succeed())

	Expect(updateManifestWorkStatus(toCluster1, placementObj.GetNamespace(), "vrg", ocmworkv1.WorkApplied)).To(Succeed())

	Expect(waitForDRPCProtected(placementObj.GetNamespace())).To(Succeed())

	verifyUserPlacementRuleDecision(placementObj.GetName(), placementObj.GetNamespace(), toCluster1)
	verifyDRPCStatusPreferredClusterExpectation(placementObj.GetNamespace(), rmn.Relocated)
	verifyVRGManifestWorkCreatedAsPrimary(placementObj.GetNamespace(), toCluster1)

	Expect(waitForCompletion(string(rmn.Relocated))).To(Succeed())
}

func recoverToFailoverCluster(placementObj client.Object, fromCluster, toCluster string) {
	Expect(setDRPCSpecExpectationTo(placementObj.GetNamespace(), fromCluster, toCluster, rmn.ActionFailover)).To(Succeed())

	Expect(updateManifestWorkStatus(toCluster, placementObj.GetNamespace(), "vrg", ocmworkv1.WorkApplied)).To(Succeed())

	Expect(waitForDRPCProtected(placementObj.GetNamespace())).To(Succeed())

	verifyUserPlacementRuleDecision(placementObj.GetName(), placementObj.GetNamespace(), toCluster)
	verifyDRPCStatusPreferredClusterExpectation(placementObj.GetNamespace(), rmn.FailedOver)
	verifyVRGManifestWorkCreatedAsPrimary(placementObj.GetNamespace(), toCluster)

	Expect(waitForCompletion(string(rmn.FailedOver))).To(Succeed())
}

func createNamespacesSync() error {
	if err := createNamespace(east1ManagedClusterNamespace); err != nil {
		return err
	}

	if err := createNamespace(east2ManagedClusterNamespace); err != nil {
		return err
	}

	return createNamespace(appNamespace)
}

func InitialDeploymentSync(namespace, placementName, homeCluster string) (*plrv1.PlacementRule,
	*rmn.DRPlacementControl,
) {
	Expect(createNamespacesSync()).To(Succeed())

	Expect(createManagedClusters(syncClusters)).To(Succeed())
	Expect(createDRClustersSync()).To(Succeed())
	Expect(createDRPolicySync()).To(Succeed())

	placementRule, err := createPlacementRule(placementName, namespace)
	Expect(err).NotTo(HaveOccurred())

	drpc, err := createDRPC(UserPlacementRuleName, DRPCCommonName, DefaultDRPCNamespace, SyncDRPolicyName, homeCluster)
	Expect(err).NotTo(HaveOccurred())

	return placementRule, drpc
}

func createDRClustersSync() error {
	return createDRClusters(syncClusters)
}

func createDRPolicySync() error {
	policy := syncDRPolicy.DeepCopy()

	return createDRPolicy(policy)
}

func deleteDRClustersSync() error {
	return deleteDRClusters(syncClusters)
}

func deleteDRPolicySync() error {
	if err := k8sClient.Delete(context.TODO(), getSyncDRPolicy()); err != nil {
		return err
	}

	return nil
}

func deletePlacementDecision() error {
	if err := client.IgnoreNotFound(k8sClient.Delete(context.TODO(), placementDecision)); err != nil {
		return err
	}

	return waitForCondition(timeout, interval, "waiting for PlacementDecision to be deleted", func() bool {
		resource := &clrapiv1beta1.PlacementDecision{}

		return k8serrors.IsNotFound(apiReader.Get(context.TODO(), types.NamespacedName{
			Namespace: placementDecision.Namespace,
			Name:      placementDecision.Name,
		}, resource))
	})
}

func fenceCluster(cluster string, manual bool) {
	latestDRCluster := getLatestDRCluster(cluster)
	if manual {
		latestDRCluster.Spec.ClusterFence = rmn.ClusterFenceStateManuallyFenced
	} else {
		latestDRCluster.Spec.ClusterFence = rmn.ClusterFenceStateFenced
	}

	latestDRCluster = updateDRClusterParameters(latestDRCluster)
	objectConditionExpectEventually(
		apiReader,
		latestDRCluster,
		metav1.ConditionTrue,
		Equal(controllers.DRClusterConditionReasonFenced),
		Ignore(),
		rmn.DRClusterConditionTypeFenced,
		false)
}

func unfenceCluster(cluster string, manual bool) {
	latestDRCluster := getLatestDRCluster(cluster)
	if manual {
		latestDRCluster.Spec.ClusterFence = rmn.ClusterFenceStateManuallyUnfenced
	} else {
		latestDRCluster.Spec.ClusterFence = rmn.ClusterFenceStateUnfenced
	}

	latestDRCluster = updateDRClusterParameters(latestDRCluster)
	objectConditionExpectEventually(
		apiReader,
		latestDRCluster,
		metav1.ConditionFalse,
		BeElementOf(controllers.DRClusterConditionReasonUnfenced, controllers.DRClusterConditionReasonCleaning,
			controllers.DRClusterConditionReasonClean),
		Ignore(),
		rmn.DRClusterConditionTypeFenced,
		false)
}

func resetdrCluster(cluster string) {
	latestDRCluster := getLatestDRCluster(cluster)
	latestDRCluster.Spec.ClusterFence = ""
	updateDRClusterParameters(latestDRCluster)
}

//nolint:unparam
func verifyInitialDRPCDeployment(userPlacement client.Object, preferredCluster string) {
	verifyVRGManifestWorkCreatedAsPrimary(userPlacement.GetNamespace(), preferredCluster)
	Expect(updateManifestWorkStatus(preferredCluster, userPlacement.GetNamespace(), "vrg", ocmworkv1.WorkApplied)).To(Succeed())
	verifyUserPlacementRuleDecision(userPlacement.GetName(), userPlacement.GetNamespace(), preferredCluster)
	verifyDRPCStatusPreferredClusterExpectation(userPlacement.GetNamespace(), rmn.Deployed)
	Expect(getManifestWorkCount(preferredCluster)).Should(BeElementOf(3, 4)) // MWs for VRG, 2 namespaces, and DRCluster
	Expect(waitForCompletion(string(rmn.Deployed))).To(Succeed())

	latestDRPC, err := getLatestDRPC(userPlacement.GetNamespace())
	Expect(err).NotTo(HaveOccurred())
	// At this point expect the DRPC status condition to have 2 types
	// {Available and PeerReady}
	// Final state is 'Deployed'
	Expect(latestDRPC.Status.Phase).To(Equal(rmn.Deployed))
	Expect(len(latestDRPC.Status.Conditions)).To(Equal(3))
	_, condition := getDRPCCondition(&latestDRPC.Status, rmn.ConditionAvailable)
	Expect(condition.Reason).To(Equal(string(rmn.Deployed)))
	Expect(latestDRPC.GetAnnotations()[controllers.LastAppDeploymentCluster]).To(Equal(preferredCluster))
	Expect(latestDRPC.GetAnnotations()[controllers.DRPCAppNamespace]).
		To(Equal(getVRGNamespace(userPlacement.GetNamespace())))

	verifyNSManifestWork(latestDRPC.Name, getVRGNamespace(latestDRPC.Namespace),
		East1ManagedCluster)
}

func verifyFailoverToSecondary(placementObj client.Object, toCluster string,
	isSyncDR bool,
) {
	recoverToFailoverCluster(placementObj, East1ManagedCluster, toCluster)

	// TODO: DRCluster as part of Unfence operation, first unfences
	//       the NetworkFence CR and then deletes it. Hence, by the
	//       time this test is made, depending upon whether NetworkFence
	//       resource is cleaned up or not, number of MW may change.
	if !isSyncDR {
		// MW for VRG+NS+DRCluster
		Eventually(getManifestWorkCount, timeout, interval).WithArguments(toCluster).Should(BeElementOf(3, 4))
	} else {
		Expect(getManifestWorkCount(toCluster)).Should(BeElementOf(3, 4)) // MW for VRG+NS+DRCluster+NF
	}

	Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(3)) // DRClustern + NS + VRG-MW

	drpc, err := getLatestDRPC(placementObj.GetNamespace())
	Expect(err).NotTo(HaveOccurred())
	// At this point expect the DRPC status condition to have 2 types
	// {Available and PeerReady}
	// Final state is 'FailedOver'
	Expect(drpc.Status.Phase).To(Equal(rmn.FailedOver))
	Expect(len(drpc.Status.Conditions)).To(Equal(3))
	_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionAvailable)
	Expect(condition.Reason).To(Equal(string(rmn.FailedOver)))

	decision, err := getLatestUserPlacementDecision(placementObj.GetName(), placementObj.GetNamespace())
	Expect(err).NotTo(HaveOccurred())
	Expect(decision.ClusterName).To(Equal(toCluster))
	Expect(drpc.GetAnnotations()[controllers.LastAppDeploymentCluster]).To(Equal(toCluster))
}

func verifyActionResultForPlacement(placement *clrapiv1beta1.Placement, homeCluster string, plType PlacementType) {
	placementDecision := getPlacementDecision(placement.GetName(), placement.GetNamespace())
	Expect(placementDecision).ShouldNot(BeNil())
	Expect(placementDecision.GetLabels()[rmnutil.ExcludeFromVeleroBackup]).Should(Equal("true"))
	Expect(placementDecision.Status.Decisions[0].ClusterName).Should(Equal(homeCluster))
	vrg, err := GetFakeVRGFromMCVUsingMW(homeCluster, placement.GetNamespace())
	Expect(err).NotTo(HaveOccurred())

	switch plType {
	case UsePlacementWithSubscription:
		Expect(vrg.Namespace).Should(Equal(placement.GetNamespace()))
	case UsePlacementWithAppSet:
		Expect(vrg.Namespace).Should(Equal(appSet.Spec.Template.Spec.Destination.Namespace))
	default:
		Fail("Wrong placement type")
	}
}

func buildVRG(objectName, namespaceName, dstCluster string, action rmn.VRGAction) rmn.VolumeReplicationGroup {
	return rmn.VolumeReplicationGroup{
		TypeMeta: metav1.TypeMeta{
			APIVersion: rmn.GroupVersion.String(),
			Kind:       "VolumeReplicationGroup",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespaceName,
			Name:      objectName,
			Annotations: map[string]string{
				controllers.DestinationClusterAnnotationKey: dstCluster,
			},
		},
		Spec: rmn.VolumeReplicationGroupSpec{
			Action:           action,
			PVCSelector:      metav1.LabelSelector{},
			ReplicationState: rmn.Primary,
			S3Profiles:       []string{},
			Sync:             &rmn.VRGSyncSpec{},
		},
	}
}

func ensureLatestVRGDownloadedFromS3Stores() {
	orgVRG := buildVRG("vrgName1", "vrgNamespace1", East1ManagedCluster, rmn.VRGAction(""))
	s3ProfileNames := []string{s3Profiles[0].S3ProfileName, s3Profiles[1].S3ProfileName}

	objectStorer1, _, err := drpcReconciler.ObjStoreGetter.ObjectStore(
		ctx, apiReader, s3ProfileNames[0], "drpolicy validation", testLogger)

	Expect(err).ToNot(HaveOccurred())
	Expect(controllers.VrgObjectProtect(objectStorer1, orgVRG)).To(Succeed())

	objectStorer2, _, err := drpcReconciler.ObjStoreGetter.ObjectStore(
		ctx, apiReader, s3ProfileNames[1], "drpolicy validation", testLogger)
	Expect(err).ToNot(HaveOccurred())

	Expect(controllers.VrgObjectProtect(objectStorer2, orgVRG)).To(Succeed())

	vrg := controllers.GetLastKnownVRGPrimaryFromS3(context.TODO(),
		apiReader, s3ProfileNames,
		"vrgName1", "vrgNamespace1", drpcReconciler.ObjStoreGetter, testLogger)

	Expect(err).ToNot(HaveOccurred())
	Expect(vrg.Name).To(Equal("vrgName1"))

	t1 := metav1.Now()
	orgVRG.Status.LastUpdateTime = t1
	Expect(controllers.VrgObjectProtect(objectStorer2, orgVRG)).To(Succeed())

	vrg2 := controllers.GetLastKnownVRGPrimaryFromS3(context.TODO(),
		apiReader, s3ProfileNames,
		"vrgName1", "vrgNamespace1", drpcReconciler.ObjStoreGetter, testLogger)

	Expect(err).ToNot(HaveOccurred())
	Expect(vrg2.Status.LastUpdateTime).To(Equal(t1))

	vrg3 := controllers.GetLastKnownVRGPrimaryFromS3(context.TODO(),
		apiReader, s3ProfileNames,
		"vrgName1", "vrgNamespace1", drpcReconciler.ObjStoreGetter, testLogger)

	Expect(err).ToNot(HaveOccurred())
	Expect(vrg3.Status.LastUpdateTime).To(Equal(t1))

	Expect(controllers.VrgObjectUnprotect(objectStorer2, orgVRG)).To(Succeed())
}

func verifyDRPCOwnedByPlacement(placementObj client.Object, drpc *rmn.DRPlacementControl) {
	for _, ownerReference := range drpc.GetOwnerReferences() {
		if ownerReference.Name == placementObj.GetName() {
			return
		}
	}

	Fail(fmt.Sprintf("DRPC %s not owned by Placement %s", drpc.GetName(), placementObj.GetName()))
}

var _ = Describe("DRPlacementControl - Ensure do-not-delete PVC Annotation", func() {
	BeforeEach(func() {
		populateDRClusters()
	})

	AfterEach(func() {
		err := forceCleanupClusterAfterAErrorTest()
		Expect(err).ToNot(HaveOccurred())
	})

	var mwu rmnutil.MWUtil

	BeforeEach(func() {
		mwu = rmnutil.MWUtil{
			Client:          k8sClient,
			APIReader:       k8sClient,
			Ctx:             context.TODO(),
			Log:             testLogger,
			InstName:        DRPCCommonName,
			TargetNamespace: DefaultDRPCNamespace,
		}
	})

	testEnsureAnnotation := func(plType PlacementType) {
		resourceType := "UserPlacement"
		placementName := UserPlacementName

		if plType == UsePlacementRule {
			resourceType = "UserPlacementRule"
			placementName = UserPlacementRuleName
		}

		It(fmt.Sprintf("Should handle annotation propagation for %s", resourceType), func(ctx SpecContext) {
			placementObj, drpc := InitialDeploymentAsync(
				DefaultDRPCNamespace,
				placementName,
				East1ManagedCluster,
				plType, // UsePlacementRule or UsePlacement
			)

			vrg := getDefaultVRG(DefaultDRPCNamespace)

			// Set scheduler based on placement type
			if plType == UsePlacementRule {
				// PlacementRule changes scheduler in Spec
				pr, err := placementObj.(*plrv1.PlacementRule)
				Expect(err).NotTo(BeNil())
				Expect(pr).NotTo(BeNil())
				pr.Spec.SchedulerName = "ramen"
			} else {
				// Placement changes scheduling via annotation instead
				pl, err := placementObj.(*clrapiv1beta1.Placement)
				Expect(err).NotTo(BeNil())
				Expect(pl).NotTo(BeNil())

				annotations := pl.GetAnnotations()
				if annotations == nil {
					annotations = make(map[string]string)
				}

				annotations[controllers.DoNotDeletePVCAnnotation] = controllers.DoNotDeletePVCAnnotationVal
				pl.SetAnnotations(annotations)
			}

			// Positive base case: Ramen is the scheduler - do nothing, no error
			err := controllers.EnsureDoNotDeletePVCAnnotation(mwu, drpc, placementObj, vrg, East1ManagedCluster, mwu.Log)
			Expect(err).To(BeNil())

			// Default scheduler
			if plType == UsePlacementRule {
				// PlacementRule changes scheduler in Spec
				pr, err := placementObj.(*plrv1.PlacementRule)
				Expect(err).NotTo(BeNil())
				Expect(pr).NotTo(BeNil())
				pr.Spec.SchedulerName = "default-scheduler"
			} else {
				placementObj.SetAnnotations(make(map[string]string))
			}

			// With default scheduler, missing annotation on DRPC causes error
			err = controllers.EnsureDoNotDeletePVCAnnotation(mwu, drpc, placementObj, vrg, East1ManagedCluster, mwu.Log)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("do-not-delete-pvc annotation not yet applied to the DRPC"))

			// Add annotation to DRPC to fix first error
			annotations := drpc.GetAnnotations()
			if annotations == nil {
				annotations = make(map[string]string)
			}

			annotations[controllers.DoNotDeletePVCAnnotation] = "true"
			drpc.SetAnnotations(annotations)

			// Now missing annotation on VRG causes error
			err = controllers.EnsureDoNotDeletePVCAnnotation(mwu, drpc, placementObj, vrg, East1ManagedCluster, mwu.Log)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("annotation hasn't been propagated to cluster"))

			// Add annotation to VRG to fix propagation issue
			annotations = vrg.GetAnnotations()
			if annotations == nil {
				annotations = make(map[string]string)
			}

			annotations[controllers.DoNotDeletePVCAnnotation] = "true"
			vrg.SetAnnotations(annotations)

			// Final success case
			err = controllers.EnsureDoNotDeletePVCAnnotation(mwu, drpc, placementObj, vrg, East1ManagedCluster, mwu.Log)
			Expect(err).To(BeNil())

			if plType == UsePlacementRule {
				Expect(deleteUserPlacementRule(UserPlacementRuleName, DefaultDRPCNamespace)).To(Succeed())
			} else {
				Expect(deleteUserPlacement()).To(Succeed())
			}

			Expect(deleteDRPC()).To(Succeed())
		})
	}

	When("EnsureDoNotDeletePVCAnnotation is called with a PlacementRule", func() {
		testEnsureAnnotation(UsePlacementRule)
	})

	When("EnsureDoNotDeletePVCAnnotation is called with a Placement", func() {
		testEnsureAnnotation(UsePlacementWithSubscription)
	})
})

var _ = Describe("DRPlacementControl Reconciler Errors", func() {
	BeforeEach(func() {
		populateDRClusters()
	})

	When("a DRPC is deleted and drclusters don't exist", func() {
		AfterEach(func() {
			err := forceCleanupClusterAfterAErrorTest()
			Expect(err).ToNot(HaveOccurred())
		})
		It("Should return an error", func(ctx SpecContext) {
			_, _ = InitialDeploymentAsync(DefaultDRPCNamespace, UserPlacementRuleName, East1ManagedCluster,
				UsePlacementRule)
			Expect(waitForCompletion(string(rmn.Deployed))).To(Succeed())

			err := retry.RetryOnConflict(retry.DefaultBackoff, deleteAllDRClusters)
			Expect(err).ToNot(HaveOccurred())

			Expect(deleteDRPC()).To(Succeed())

			errCount := 0

			for {
				_, err = drpcReconcile(DRPCCommonName, DefaultDRPCNamespace)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to get drclusters"))

				errCount++
				if errCount > 2 {
					// Found the required error message for more than a second
					break
				}

				time.Sleep(time.Second)
			}
		}, SpecTimeout(time.Second*10))
	})
})

// +kubebuilder:docs-gen:collapse=Imports
//
//nolint:errcheck
var _ = Describe("DRPlacementControl Reconciler", func() {
	Specify("DRClusters", func() {
		populateDRClusters()
	})
	Context("DRPlacementControl Reconciler Async DR using PlacementRule (Subscription)", func() {
		var (
			userPlacementRule *plrv1.PlacementRule
			drpc              *rmn.DRPlacementControl
		)

		When("An Application is deployed for the first time", func() {
			It("Should deploy to East1ManagedCluster", func() {
				By("Initial Deployment")

				var placementObj client.Object

				placementObj, drpc = InitialDeploymentAsync(
					DefaultDRPCNamespace, UserPlacementRuleName, East1ManagedCluster, UsePlacementRule)
				userPlacementRule = placementObj.(*plrv1.PlacementRule)
				Expect(userPlacementRule).NotTo(BeNil())
				verifyInitialDRPCDeployment(userPlacementRule, East1ManagedCluster)
				latestDRPC, err := getLatestDRPC(DefaultDRPCNamespace)
				Expect(err).NotTo(HaveOccurred())
				verifyDRPCOwnedByPlacement(userPlacementRule, latestDRPC)
			})
		})
		When("DRAction changes to Failover", func() {
			It("Should not failover to Secondary (West1ManagedCluster) till PV manifest is applied", func() {
				By("\n\n*** Failover - 1\n\n")
				setRestorePVsIncomplete()
				Expect(setDRPCSpecExpectationTo(DefaultDRPCNamespace, East1ManagedCluster, West1ManagedCluster, rmn.ActionFailover)).To(Succeed())
				verifyUserPlacementRuleDecisionUnchanged(userPlacementRule.Name, userPlacementRule.Namespace, East1ManagedCluster)
				// MWs for VRG, NS, DRCluster, and MMode
				Expect(getManifestWorkCount(West1ManagedCluster)).Should(BeElementOf(3, 4))
				setRestorePVsComplete()
			})
			It("Should failover to Secondary (West1ManagedCluster)", func() {
				// ----------------------------- FAILOVER TO SECONDARY (West1ManagedCluster) --------------------------------------
				By("\n\n*** Failover - 1\n\n")
				verifyFailoverToSecondary(userPlacementRule, West1ManagedCluster, false)
			})
		})
		When("DRAction is Failover during hub recovery", func() {
			It("Should reconstructs the DRPC state and points to Secondary (West1ManagedCluster)", func() {
				By("\n\n*** Failover after \n\n")
				Expect(clearFakeUserPlacementRuleStatus(UserPlacementRuleName, DefaultDRPCNamespace)).To(Succeed())
				Expect(clearDRPCStatus()).To(Succeed())
				verifyFailoverToSecondary(userPlacementRule, West1ManagedCluster, false)
			})
		})
		When("DRAction is set to Relocate", func() {
			It("Should relocate to Primary (East1ManagedCluster)", func() {
				// ----------------------------- RELOCATION TO PRIMARY --------------------------------------
				By("\n\n*** Relocate - 1\n\n")
				runRelocateAction(userPlacementRule, West1ManagedCluster, false, false)
			})
		})
		When("DRAction is changed to Failover after relocation", func() {
			It("Should failover again to Secondary (West1ManagedCluster)", func() {
				// ----------------------------- FAILOVER TO SECONDARY --------------------------------------
				By("\n\n*** Failover - 3\n\n")
				runFailoverAction(userPlacementRule, East1ManagedCluster, West1ManagedCluster, false, false)
			})
		})
		When("DRAction is set to Relocate", func() {
			It("Should relocate to Primary (East1ManagedCluster)", func() {
				// ----------------------------- RELOCATION TO PRIMARY --------------------------------------
				By("\n\n*** relocate 2\n\n")
				runRelocateAction(userPlacementRule, West1ManagedCluster, false, false)
			})
		})
		When("Get VRG from s3 store", func() {
			It("Should get the latest primary VRG from s3 stores", func() {
				ensureLatestVRGDownloadedFromS3Stores()
			})
		})
		When("Deleting DRPolicy with DRPC references", func() {
			It("Should retain the deleted DRPolicy in the API server", func() {
				// ----------------------------- DELETE DRPolicy  --------------------------------------
				By("\n\n*** DELETE drpolicy ***\n\n")
				Expect(deleteDRPolicyAsync()).To(Succeed())
				ensureDRPolicyIsNotDeleted(drpc)
			})
		})
		When("A DRPC is created referring to a deleted DRPolicy", func() {
			It("Should fail DRPC reconciliaiton and not add a finalizer", func() {
				_, drpc2 := FollowOnDeploymentAsync(DRPC2NamespaceName, UserPlacementRuleName, East1ManagedCluster)
				checkIfDRPCFinalizerNotAdded(drpc2)
				Expect(k8sClient.Delete(context.TODO(), drpc2)).Should(Succeed())
			})
		})
		When("Deleting user PlacementRule", func() {
			It("Should cleanup DRPC", func() {
				// ----------------------------- DELETE DRPC from PRIMARY --------------------------------------
				By("\n\n*** DELETE User PlacementRule ***\n\n")
				Expect(deleteUserPlacementRule(UserPlacementRuleName, DefaultDRPCNamespace)).To(Succeed())
			})
		})

		When("Deleting DRPC", func() {
			It("Should delete VRG and NS MWs and MCVs from Primary (East1ManagedCluster)", func() {
				// ----------------------------- DELETE DRPC from PRIMARY --------------------------------------
				By("\n\n*** DELETE DRPC ***\n\n")
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(BeElementOf(3, 4)) // DRCluster + VRG MW
				Expect(deleteDRPC()).To(Succeed())
				Expect(waitForCompletion("deleted")).To(Succeed())
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(1))       // DRCluster
				Expect(getManagedClusterViewCount(East1ManagedCluster)).Should(Equal(0)) // NS + VRG MCV
				ensureNamespaceMWsDeletedFromAllClusters(DefaultDRPCNamespace)
			})
			It("should delete the DRPC causing its referenced drpolicy to be deleted"+
				" by drpolicy controller since no DRPCs reference it anymore", func() {
				ensureDRPolicyIsDeleted(drpc.Spec.DRPolicyRef.Name)
			})
		})
		Specify("delete drclusters", func() {
			Expect(deleteDRClustersAsync()).To(Succeed())
		})
	})
	// TEST WITH Placement AND Subscription
	Context("DRPlacementControl Reconciler Async DR using Placement (Subscription)", func() {
		var (
			placement *clrapiv1beta1.Placement
			drpc      *rmn.DRPlacementControl
		)

		Specify("DRClusters", func() {
			populateDRClusters()
		})
		When("An Application is deployed for the first time using Placement", func() {
			It("Should deploy to East1ManagedCluster", func() {
				By("Initial Deployment")

				var placementObj client.Object

				placementObj, drpc = InitialDeploymentAsync(
					DefaultDRPCNamespace, UserPlacementName, East1ManagedCluster, UsePlacementWithSubscription)
				placement = placementObj.(*clrapiv1beta1.Placement)
				Expect(placement).NotTo(BeNil())
				verifyInitialDRPCDeployment(placement, East1ManagedCluster)
				verifyActionResultForPlacement(placement, East1ManagedCluster, UsePlacementWithSubscription)
				latestDRPC, err := getLatestDRPC(DefaultDRPCNamespace)
				Expect(err).NotTo(HaveOccurred())
				verifyDRPCOwnedByPlacement(placement, latestDRPC)
			})
		})
		When("DRAction changes to Failover using Placement with Subscription", func() {
			It("Should not failover to Secondary (West1ManagedCluster) till PV manifest is applied", func() {
				setRestorePVsIncomplete()
				Expect(setDRPCSpecExpectationTo(DefaultDRPCNamespace, East1ManagedCluster, West1ManagedCluster, rmn.ActionFailover)).To(Succeed())
				verifyUserPlacementRuleDecisionUnchanged(placement.Name, placement.Namespace, East1ManagedCluster)
				// MWs for VRG, NS, VRG DRCluster, and MMode
				Expect(getManifestWorkCount(West1ManagedCluster)).Should(BeElementOf(3, 4))
				Expect(len(getPlacementDecision(placement.GetName(), placement.GetNamespace()).
					Status.Decisions)).Should(Equal(1))
				setRestorePVsComplete()
			})
			It("Should failover to Secondary (West1ManagedCluster) when using Subscription", func() {
				runFailoverAction(placement, East1ManagedCluster, West1ManagedCluster, false, false)
				verifyActionResultForPlacement(placement, West1ManagedCluster, UsePlacementWithSubscription)
			})
		})
		When("DRAction is set to Relocate using Placement with Subscriptioin", func() {
			It("Should relocate to Primary (East1ManagedCluster)", func() {
				runRelocateAction(placement, West1ManagedCluster, false, false)
				verifyActionResultForPlacement(placement, East1ManagedCluster, UsePlacementWithSubscription)
			})
		})
		When("Deleting DRPolicy with DRPC references when using Placement", func() {
			It("Should retain the deleted DRPolicy in the API server", func() {
				Expect(deleteDRPolicyAsync()).To(Succeed())
				ensureDRPolicyIsNotDeleted(drpc)
			})
		})
		When("Deleting user Placement", func() {
			It("Should cleanup DRPC", func() {
				Expect(deleteUserPlacement()).To(Succeed())
				latestDRPC, err := getLatestDRPC(DefaultDRPCNamespace)
				Expect(err).NotTo(HaveOccurred())
				_, condition := getDRPCCondition(&latestDRPC.Status, rmn.ConditionPeerReady)
				Expect(condition).NotTo(BeNil())
			})
		})
		When("Deleting DRPC when using Placement", func() {
			It("Should delete VRG and NS MWs and MCVs from Primary (East1ManagedCluster)", func() {
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(BeElementOf(3, 4)) // DRCluster + VRG + NS MW
				Expect(deleteDRPC()).To(Succeed())
				Expect(waitForCompletion("deleted")).To(Succeed())
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(1))       // DRCluster
				Expect(getManagedClusterViewCount(East1ManagedCluster)).Should(Equal(0)) // NS + VRG MCV
				ensureNamespaceMWsDeletedFromAllClusters(DefaultDRPCNamespace)
			})
			It("should delete the DRPC causing its referenced drpolicy to be deleted"+
				" by drpolicy controller since no DRPCs reference it anymore", func() {
				ensureDRPolicyIsDeleted(drpc.Spec.DRPolicyRef.Name)
			})
		})
		Specify("delete drclusters when using Placement", func() {
			Expect(deleteDRClustersAsync()).To(Succeed())
		})
	})
	// TEST WITH Placement AND ApplicationSet
	Context("DRPlacementControl Reconciler Async DR using Placement (ApplicationSet)", func() {
		var (
			placement *clrapiv1beta1.Placement
			drpc      *rmn.DRPlacementControl
		)

		Specify("DRClusters", func() {
			populateDRClusters()
		})
		When("An Application is deployed for the first time using Placement", func() {
			It("Should deploy to East1ManagedCluster", func() {
				By("Initial Deployment")

				UseApplicationSet = true
				getBaseVRG(DefaultDRPCNamespace).ObjectMeta.Namespace = ApplicationNamespace

				var placementObj client.Object

				placementObj, drpc = InitialDeploymentAsync(
					DefaultDRPCNamespace, UserPlacementName, East1ManagedCluster, UsePlacementWithAppSet)
				placement = placementObj.(*clrapiv1beta1.Placement)
				Expect(placement).NotTo(BeNil())
				verifyInitialDRPCDeployment(placement, East1ManagedCluster)
				verifyActionResultForPlacement(placement, East1ManagedCluster, UsePlacementWithAppSet)
				latestDRPC, err := getLatestDRPC(DefaultDRPCNamespace)
				Expect(err).NotTo(HaveOccurred())
				verifyDRPCOwnedByPlacement(placement, latestDRPC)
			})
		})
		When("DRAction changes to Failover using Placement", func() {
			It("Should not failover to Secondary (West1ManagedCluster) till PV manifest is applied", func() {
				setRestorePVsIncomplete()
				Expect(setDRPCSpecExpectationTo(DefaultDRPCNamespace, East1ManagedCluster, West1ManagedCluster, rmn.ActionFailover)).To(Succeed())
				verifyUserPlacementRuleDecisionUnchanged(placement.Name, placement.Namespace, East1ManagedCluster)
				// MWs for VRG, NS, VRG DRCluster, and MMode
				Expect(getManifestWorkCount(West1ManagedCluster)).Should(BeElementOf(3, 4))
				Expect(len(getPlacementDecision(placement.GetName(), placement.GetNamespace()).
					Status.Decisions)).Should(Equal(1))
				setRestorePVsComplete()
			})
			It("Should failover to Secondary (West1ManagedCluster)", func() {
				runFailoverAction(placement, East1ManagedCluster, West1ManagedCluster, false, false)
				verifyActionResultForPlacement(placement, West1ManagedCluster, UsePlacementWithAppSet)
			})
		})
		When("DRAction is set to Relocate using Placement", func() {
			It("Should relocate to Primary (East1ManagedCluster)", func() {
				runRelocateAction(placement, West1ManagedCluster, false, false)
				verifyActionResultForPlacement(placement, East1ManagedCluster, UsePlacementWithAppSet)
			})
		})
		When("DRAction is changed to Failover after relocation using Placement", func() {
			It("Should failover again to Secondary (West1ManagedCluster)", func() {
				runFailoverAction(placement, East1ManagedCluster, West1ManagedCluster, false, false)
				verifyActionResultForPlacement(placement, West1ManagedCluster, UsePlacementWithAppSet)
			})
		})
		When("DRAction is set to Relocate again using Placement", func() {
			It("Should relocate again to Primary (East1ManagedCluster)", func() {
				runRelocateAction(placement, West1ManagedCluster, false, false)
				verifyActionResultForPlacement(placement, East1ManagedCluster, UsePlacementWithAppSet)
			})
		})
		When("Deleting DRPolicy with DRPC references when using Placement", func() {
			It("Should retain the deleted DRPolicy in the API server", func() {
				Expect(deleteDRPolicyAsync()).To(Succeed())
				ensureDRPolicyIsNotDeleted(drpc)
			})
		})
		When("Deleting user Placement", func() {
			It("Should cleanup DRPC", func() {
				Expect(deleteUserPlacement()).To(Succeed())
				latestDRPC, err := getLatestDRPC(DefaultDRPCNamespace)
				Expect(err).NotTo(HaveOccurred())
				_, condition := getDRPCCondition(&latestDRPC.Status, rmn.ConditionPeerReady)
				Expect(condition).NotTo(BeNil())
			})
		})
		When("Deleting DRPC when using Placement", func() {
			It("Should delete VRG and NS MWs and MCVs from Primary (East1ManagedCluster)", func() {
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(BeElementOf(3, 4)) // DRCluster + VRG + NS MW
				Expect(deleteDRPC()).To(Succeed())
				Expect(waitForCompletion("deleted")).To(Succeed())
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(1))       // DRCluster
				Expect(getManagedClusterViewCount(East1ManagedCluster)).Should(Equal(0)) // NS + VRG MCV
				ensureNamespaceMWsDeletedFromAllClusters(ApplicationNamespace)
				Expect(deleteAppSet()).To(Succeed())
				UseApplicationSet = false
			})
			It("should delete the DRPC causing its referenced drpolicy to be deleted"+
				" by drpolicy controller since no DRPCs reference it anymore", func() {
				ensureDRPolicyIsDeleted(drpc.Spec.DRPolicyRef.Name)
			})
		})
		Specify("delete drclusters when using Placement", func() {
			Expect(deleteDRClustersAsync()).To(Succeed())
		})
	})
	Context("DRPlacementControl Reconciler Sync DR", func() {
		userPlacementRule := &plrv1.PlacementRule{}
		// drpc := &rmn.DRPlacementControl{}
		Specify("DRClusters", func() {
			populateDRClusters()
		})
		When("An Application is deployed for the first time", func() {
			It("Should deploy to East1ManagedCluster", func() {
				By("Initial Deployment")

				userPlacementRule, _ = InitialDeploymentSync(DefaultDRPCNamespace, UserPlacementRuleName, East1ManagedCluster)
				verifyInitialDRPCDeployment(userPlacementRule, East1ManagedCluster)
				latestDRPC, err := getLatestDRPC(DefaultDRPCNamespace)
				Expect(err).NotTo(HaveOccurred())
				verifyDRPCOwnedByPlacement(userPlacementRule, latestDRPC)
			})
		})
		When("DRAction changes to Failover", func() {
			It("Should not failover to Secondary (East2ManagedCluster) till PV manifest is applied", func() {
				By("\n\n*** Failover - 1\n\n")
				setRestorePVsIncomplete()
				fenceCluster(East1ManagedCluster, false)
				Expect(setDRPCSpecExpectationTo(DefaultDRPCNamespace, East1ManagedCluster, East2ManagedCluster, rmn.ActionFailover)).To(Succeed())
				verifyUserPlacementRuleDecisionUnchanged(userPlacementRule.Name, userPlacementRule.Namespace, East1ManagedCluster)
				// MWs for VRG, VRG DRCluster and the MW for NetworkFence CR to fence off
				Expect(getManifestWorkCount(East2ManagedCluster)).Should(BeElementOf(3, 4))
				Expect(len(userPlacementRule.Status.Decisions)).Should(Equal(0))
				setRestorePVsComplete()
			})
			It("Should failover to Secondary (East2ManagedCluster)", func() {
				By("\n\n*** Failover - 1\n\n")
				verifyFailoverToSecondary(userPlacementRule, East2ManagedCluster, true)
			})
		})
		When("DRAction is set to Relocate", func() {
			It("Should relocate to Primary (East1ManagedCluster)", func() {
				// ----------------------------- RELOCATION TO PRIMARY FOR SYNC DR -------------------
				By("\n\n*** relocate 2\n\n")
				runRelocateAction(userPlacementRule, East2ManagedCluster, true, false)
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
				runFailoverAction(userPlacementRule, East1ManagedCluster, East2ManagedCluster, true, false)
			})
		})
		When("DRAction is set to Relocate", func() {
			It("Should relocate to Primary (East1ManagedCluster)", func() {
				// ----------------------------- RELOCATION TO PRIMARY FOR SYNC DR------------------------
				By("\n\n*** relocate 2\n\n")
				runRelocateAction(userPlacementRule, East2ManagedCluster, true, false)
			})
		})
		When("Deleting user PlacementRule", func() {
			It("Should cleanup DRPC", func() {
				By("\n\n*** DELETE User PlacementRule ***\n\n")
				Expect(deleteUserPlacementRule(UserPlacementRuleName, DefaultDRPCNamespace)).To(Succeed())
			})
		})
		When("Deleting DRPC", func() {
			It("Should delete VRG from Primary (East1ManagedCluster)", func() {
				By("\n\n*** DELETE DRPC ***\n\n")
				Expect(deleteDRPC()).To(Succeed())
				Expect(waitForCompletion("deleted")).To(Succeed())
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(1)) // DRCluster
				Expect(deleteDRPolicySync()).To(Succeed())
				Expect(deleteDRClustersSync()).To(Succeed())
			})
		})

		// manual fencing and manual unfencing
		userPlacementRule = &plrv1.PlacementRule{}
		// drpc = &rmn.DRPlacementControl{}
		Specify("DRClusters", func() {
			populateDRClusters()
		})
		When("An Application is deployed for the first time", func() {
			It("Should deploy to East1ManagedCluster", func() {
				By("Initial Deployment")

				userPlacementRule, _ = InitialDeploymentSync(DefaultDRPCNamespace, UserPlacementRuleName, East1ManagedCluster)
				verifyInitialDRPCDeployment(userPlacementRule, East1ManagedCluster)
				latestDRPC, err := getLatestDRPC(DefaultDRPCNamespace)
				Expect(err).NotTo(HaveOccurred())
				verifyDRPCOwnedByPlacement(userPlacementRule, latestDRPC)
			})
		})
		When("DRAction changes to Failover", func() {
			It("Should not failover to Secondary (East2ManagedCluster) till PV manifest is applied", func() {
				By("\n\n*** Failover - 1\n\n")
				setRestorePVsIncomplete()
				fenceCluster(East1ManagedCluster, true)
				Expect(setDRPCSpecExpectationTo(DefaultDRPCNamespace, East1ManagedCluster, East2ManagedCluster, rmn.ActionFailover)).To(Succeed())
				verifyUserPlacementRuleDecisionUnchanged(userPlacementRule.Name, userPlacementRule.Namespace, East1ManagedCluster)
				// MWs for VRG, VRG DRCluster and the MW for NetworkFence CR to fence off
				Expect(getManifestWorkCount(East2ManagedCluster)).Should(BeElementOf(3, 4))
				Expect(len(userPlacementRule.Status.Decisions)).Should(Equal(0))
				setRestorePVsComplete()
			})
			It("Should failover to Secondary (East2ManagedCluster)", func() {
				By("\n\n*** Failover - 1\n\n")
				verifyFailoverToSecondary(userPlacementRule, East2ManagedCluster, true)
			})
		})
		When("DRAction is set to Relocate", func() {
			It("Should relocate to Primary (East1ManagedCluster)", func() {
				// ----------------------------- RELOCATION TO PRIMARY FOR SYNC DR -------------------
				By("\n\n*** relocate 2\n\n")
				runRelocateAction(userPlacementRule, East2ManagedCluster, true, true)
			})
		})
		When("DRAction is changed to Failover after relocation", func() {
			It("Should failover again to Secondary (East2ManagedCluster)", func() {
				// ----------------------------- FAILOVER TO SECONDARY FOR SYNC DR--------------------
				By("\n\n*** Failover - 3\n\n")
				runFailoverAction(userPlacementRule, East1ManagedCluster, East2ManagedCluster, true, true)
			})
		})
		When("DRAction is set to Relocate", func() {
			It("Should relocate to Primary (East1ManagedCluster)", func() {
				// ----------------------------- RELOCATION TO PRIMARY FOR SYNC DR------------------------
				By("\n\n*** relocate 2\n\n")
				runRelocateAction(userPlacementRule, East2ManagedCluster, true, false)
			})
		})
		When("Deleting user PlacementRule", func() {
			It("Should cleanup DRPC", func() {
				By("\n\n*** DELETE User PlacementRule ***\n\n")
				Expect(deleteUserPlacementRule(UserPlacementRuleName, DefaultDRPCNamespace)).To(Succeed())
			})
		})

		When("Deleting DRPC", func() {
			It("Should delete VRG from Primary (East1ManagedCluster)", func() {
				By("\n\n*** DELETE DRPC ***\n\n")
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(BeElementOf(3, 4)) // DRCluster + NS + VRG MW
				Expect(deleteDRPC()).To(Succeed())
				Expect(waitForCompletion("deleted")).To(Succeed())
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(1)) // DRCluster
				Expect(deleteDRPolicySync()).To(Succeed())
				Expect(deleteDRClustersSync()).To(Succeed())
				ensureNamespaceMWsDeletedFromAllClusters(DefaultDRPCNamespace)
			})
		})
	})

	Context("DRPlacementControl Reconciler HubRecovery (Subscription)", func() {
		var userPlacementRule1 *plrv1.PlacementRule

		Specify("DRClusters", func() {
			populateDRClusters()
		})
		When("Application deployed for the first time", func() {
			It("Should deploy drpc", func() {
				Expect(createNamespacesAsync(getNamespaceObj(DefaultDRPCNamespace))).To(Succeed())
				Expect(createManagedClusters(asyncClusters)).To(Succeed())
				Expect(createDRClustersAsync()).To(Succeed())
				Expect(createDRPolicyAsync()).To(Succeed())

				var placementObj client.Object

				placementObj, _ = CreatePlacementAndDRPC(
					DefaultDRPCNamespace, UserPlacementRuleName, East1ManagedCluster, UsePlacementRule)
				userPlacementRule1 = placementObj.(*plrv1.PlacementRule)
				Expect(userPlacementRule1).NotTo(BeNil())
				Expect(waitForDRPCPhaseAndProgression(DefaultDRPCNamespace, rmn.Deployed)).To(Succeed())
				Expect(uploadVRGtoS3Store(DRPCCommonName, DefaultDRPCNamespace, East1ManagedCluster, rmn.VRGAction(""))).To(Succeed())
			})
		})
		//nolint:lll
		// -------- Before Hub Recovery ---------
		// NAMESPACE           NAME           AGE     PREFERREDCLUSTER   FAILOVERCLUSTER   DESIREDSTATE   CURRENTSTATE   PROGRESSION            START TIME             DURATION          PEER READY
		// busybox-samples-1   busybox-drpc   28m     East1ManagedClus                                    Deployed       Completed              2023-12-20T01:24:01Z   21.044521711s     True
		// -------- After Hub Recovery ---------
		// NAMESPACE           NAME           AGE     PREFERREDCLUSTER   FAILOVERCLUSTER   DESIREDSTATE   CURRENTSTATE   PROGRESSION      START TIME             DURATION   PEER READY
		// busybox-samples-1   busybox-drpc   149m    East1ManagedClus                                    Deployed       Completed                                          True
		// -------- After Secondary is back online ---------
		// NAMESPACE           NAME           AGE     PREFERREDCLUSTER   FAILOVERCLUSTER   DESIREDSTATE   CURRENTSTATE   PROGRESSION      START TIME             DURATION   PEER READY
		// busybox-samples-1   busybox-drpc   12h     East1ManagedClus                                      Deployed       Completed                                              True
		When("HubRecovery: DRAction is Initial deploy -> Secondary Down", func() {
			It("Should reconstructs the DRPC state to completion. Primary is East1ManagedCluster", func() {
				setClusterDown(West1ManagedCluster)
				Expect(clearFakeUserPlacementRuleStatus(UserPlacementRuleName, DefaultDRPCNamespace)).To(Succeed())
				Expect(clearDRPCStatus()).To(Succeed())
				expectedAction := rmn.DRAction("")
				expectedPhase := rmn.Deployed
				expectedPorgression := rmn.ProgressionCompleted
				verifyDRPCStateAndProgression(expectedAction, expectedPhase, expectedPorgression)
				resetClusterDown()

				expectedCompleted := rmn.ProgressionCompleted
				verifyDRPCStateAndProgression(expectedAction, expectedPhase, expectedCompleted)
			})
		})
		//nolint:lll
		// -------- Before Hub Recovery ---------
		// NAMESPACE           NAME           AGE     PREFERREDCLUSTER   FAILOVERCLUSTER   DESIREDSTATE   CURRENTSTATE   PROGRESSION            START TIME             DURATION          PEER READY
		// busybox-samples-5   busybox-drpc   9m11s   East1ManagedClus                                    Deployed       Completed              2023-12-20T01:43:04Z   15.060661732s     True
		// -------- After Hub Recovery ---------
		// NAMESPACE           NAME           AGE     PREFERREDCLUSTER   FAILOVERCLUSTER   DESIREDSTATE   CURRENTSTATE   PROGRESSION      START TIME             DURATION   PEER READY
		// busybox-samples-5   busybox-drpc   130m    East1ManagedClus                                    Deployed       UpdatingPlRule   2023-12-20T03:52:09Z              True
		// -------- After Primary is back online ---------
		// NAMESPACE           NAME           AGE     PREFERREDCLUSTER   FAILOVERCLUSTER   DESIREDSTATE   CURRENTSTATE   PROGRESSION      START TIME             DURATION   PEER READY
		// busybox-samples-5   busybox-drpc   11h     East1ManagedClus                                    Deployed       Completed     2023-12-20T12:52:20Z   5m32.467527356s   True
		When("HubRecovery: DRAction is Initial deploy -> Primary Down", func() {
			It("Should pause and wait for user to trigger a failover. Primary East1ManagedCluster", func() {
				setClusterDown(East1ManagedCluster)
				Expect(clearDRPCStatus()).To(Succeed())
				expectedAction := rmn.DRAction("")
				expectedPhase := rmn.WaitForUser
				expectedPorgression := rmn.ProgressionActionPaused
				verifyDRPCStateAndProgression(expectedAction, expectedPhase, expectedPorgression)
			})
		})

		// Failover
		When("HubRecovery: DRAction is set to Failover -> primary cluster down", func() {
			It("Should failover to West1ManagedCluster", func() {
				from := East1ManagedCluster
				to := West1ManagedCluster

				resetClusterDown()
				runFailoverAction(userPlacementRule1, from, to, false, false)
				Expect(waitForDRPCPhaseAndProgression(DefaultDRPCNamespace, rmn.FailedOver)).To(Succeed())
				Expect(uploadVRGtoS3Store(DRPCCommonName, DefaultDRPCNamespace, West1ManagedCluster, rmn.VRGActionFailover)).To(Succeed())
				resetClusterDown()
			})
		})

		//nolint:lll
		// -------- Before Hub Recovery Action FailedOver ---
		// NAMESPACE           NAME           AGE     PREFERREDCLUSTER   FAILOVERCLUSTER   DESIREDSTATE   CURRENTSTATE   PROGRESSION            START TIME             DURATION          PEER READY
		// busybox-samples-2   busybox-drpc   18m     East1ManagedClus   West1ManagedClu   Failover       FailedOver     Completed              2023-12-20T01:45:03Z   3m4.604746186s    True
		// -------- After Hub Recovery ----------------------
		// NAMESPACE           NAME           AGE     PREFERREDCLUSTER   FAILOVERCLUSTER   DESIREDSTATE   CURRENTSTATE   PROGRESSION      START TIME             DURATION   PEER READY
		// busybox-samples-2   busybox-drpc   140m    East1ManagedClus   West1ManagedClu   Failover                      Paused                                             True
		// -------- After Primary is back online ------------
		// NAMESPACE           NAME           AGE     PREFERREDCLUSTER   FAILOVERCLUSTER   DESIREDSTATE   CURRENTSTATE   PROGRESSION      START TIME             DURATION   PEER READY
		// busybox-samples-2   busybox-drpc   11h     East1ManagedClus   West1ManagedClu   Failover       FailedOver     Completed                                              True
		When("HubRecovery: DRAction is Failover -> Primary Down", func() {
			It("Should Pause, but allows failover. Primary West1ManagedCluster", func() {
				setClusterDown(West1ManagedCluster)
				Expect(clearFakeUserPlacementRuleStatus(UserPlacementRuleName, DefaultDRPCNamespace)).To(Succeed())
				Expect(clearDRPCStatus()).To(Succeed())
				// TODO: Why did we shift the failover to deploy action here? It fails as VRG exists as Secondary now
				// on the cluster to deploy to
				Expect(setDRPCSpecExpectationTo(DefaultDRPCNamespace, East1ManagedCluster, West1ManagedCluster, rmn.ActionFailover)).To(Succeed())
				expectedAction := rmn.ActionFailover
				expectedPhase := rmn.WaitForUser
				expectedPorgression := rmn.ProgressionActionPaused
				verifyDRPCStateAndProgression(expectedAction, expectedPhase, expectedPorgression)
				checkConditionAllowFailover(DefaultDRPCNamespace)

				// User intervention is required (simulate user intervention)
				resetClusterDown()
				Expect(setDRPCSpecExpectationTo(DefaultDRPCNamespace, East1ManagedCluster, West1ManagedCluster, rmn.ActionFailover)).To(Succeed())
				expectedAction = rmn.ActionFailover
				expectedPhase = rmn.FailedOver
				expectedPorgression = rmn.ProgressionCompleted
				verifyDRPCStateAndProgression(expectedAction, expectedPhase, expectedPorgression)
				Expect(waitForCompletion(string(rmn.FailedOver))).To(Succeed())
			})
		})

		// Relocate
		When("HubRecovery: DRAction is set to Relocate", func() {
			It("Should relocate to Primary (East1ManagedCluster)", func() {
				// ----------------------------- RELOCATION TO PRIMARY --------------------------------------
				from := West1ManagedCluster
				runRelocateAction(userPlacementRule1, from, false, false)
				Expect(uploadVRGtoS3Store(DRPCCommonName, DefaultDRPCNamespace, East1ManagedCluster, rmn.VRGAction(rmn.ActionRelocate))).To(Succeed())
				Expect(waitForDRPCPhaseAndProgression(DefaultDRPCNamespace, rmn.Relocated)).To(Succeed())
			})
		})
		//nolint:lll
		// -------- Before Hub Recovery Action Relocated ---
		// NAMESPACE           NAME           AGE     PREFERREDCLUSTER   FAILOVERCLUSTER   DESIREDSTATE   CURRENTSTATE   PROGRESSION            START TIME             DURATION          PEER READY
		// busybox-sample      busybox-drpc   4h32m   East1ManagedClus   West1ManagedClu   Relocate       Relocated      Completed              2023-12-19T21:36:06Z   2m5.608275449s    True
		// -------- After Hub Recovery MUST PAUSE -----------
		// NAMESPACE           NAME           AGE     PREFERREDCLUSTER   FAILOVERCLUSTER   DESIREDSTATE   CURRENTSTATE   PROGRESSION      START TIME             DURATION   PEER READY
		// busybox-sample      busybox-drpc   6h33m   East1ManagedClus   West1ManagedClu   Relocate       Relocated      Cleaning Up                                        False
		// -------- After Primary is back online ------------
		// NAMESPACE           NAME           AGE     PREFERREDCLUSTER   FAILOVERCLUSTER   DESIREDSTATE   CURRENTSTATE   PROGRESSION      START TIME             DURATION   PEER READY
		// busybox-sample      busybox-drpc   16h     East1ManagedClus   West1ManagedClu   Relocate       Relocated      Completed                                              True
		When("HubRecovery: DRAction is Relocate -> Secondary Down", func() {
			It("Should Continue given the primary East1ManagedCluster is up", func() {
				setClusterDown(West1ManagedCluster)
				Expect(clearFakeUserPlacementRuleStatus(UserPlacementRuleName, DefaultDRPCNamespace)).To(Succeed())
				Expect(clearDRPCStatus()).To(Succeed())
				expectedAction := rmn.ActionRelocate
				expectedPhase := rmn.DRState("")
				expectedPorgression := rmn.ProgressionStatus("")
				verifyDRPCStateAndProgression(expectedAction, expectedPhase, expectedPorgression)

				// User intervention is required (simulate user intervention)
				resetClusterDown()
				Expect(setDRPCSpecExpectationTo(DefaultDRPCNamespace, East1ManagedCluster, West1ManagedCluster, rmn.ActionRelocate)).To(Succeed())
				expectedAction = rmn.ActionRelocate
				expectedPhase = rmn.Relocated
				expectedPorgression = rmn.ProgressionCompleted
				verifyDRPCStateAndProgression(expectedAction, expectedPhase, expectedPorgression)
				Expect(waitForCompletion(string(rmn.Relocated))).To(Succeed())
			})
		})
		//nolint:lll
		// -------- Before Hub Recovery Action Relocated ---
		// NAMESPACE           NAME           AGE     PREFERREDCLUSTER   FAILOVERCLUSTER   DESIREDSTATE   CURRENTSTATE   PROGRESSION            START TIME             DURATION          PEER READY
		// busybox-samples-3   busybox-drpc   16m     East1ManagedClus                     Relocate       Relocated      Completed              2023-12-20T01:46:26Z   2m19.553160011s   True
		// -------- After Hub Recovery MUST PAUSE -----------
		// NAMESPACE           NAME           AGE     PREFERREDCLUSTER   FAILOVERCLUSTER   DESIREDSTATE   CURRENTSTATE   PROGRESSION      START TIME             DURATION   PEER READY
		// busybox-samples-3   busybox-drpc   137m    East1ManagedClus                                                   Paused                                             True
		// -------- After Primary is back online ------------
		// NAMESPACE           NAME           AGE     PREFERREDCLUSTER   FAILOVERCLUSTER   DESIREDSTATE   CURRENTSTATE   PROGRESSION      START TIME             DURATION   PEER READY
		// busybox-samples-3   busybox-drpc   11h     East1ManagedClus                     Relocate       Relocated      Completed                                              True
		When("HubRecovery: DRAction is supposed to be Relocate -> Primary Down -> Action Cleared", func() {
			It("Should Pause given the primary East1ManagedCluster is down, but allow failover", func() {
				setClusterDown(East1ManagedCluster)
				Expect(clearFakeUserPlacementRuleStatus(UserPlacementRuleName, DefaultDRPCNamespace)).To(Succeed())
				Expect(clearDRPCStatus()).To(Succeed())
				Expect(setDRPCSpecExpectationTo(DefaultDRPCNamespace, East1ManagedCluster, West1ManagedCluster, "")).To(Succeed())
				expectedAction := rmn.DRAction("")
				expectedPhase := rmn.WaitForUser
				expectedPorgression := rmn.ProgressionActionPaused
				verifyDRPCStateAndProgression(expectedAction, expectedPhase, expectedPorgression)
				checkConditionAllowFailover(DefaultDRPCNamespace)

				// User intervention is required (simulate user intervention)
				resetClusterDown()
				Expect(setDRPCSpecExpectationTo(DefaultDRPCNamespace, East1ManagedCluster, West1ManagedCluster, rmn.ActionRelocate)).To(Succeed())
				expectedAction = rmn.ActionRelocate
				expectedPhase = rmn.Relocated
				expectedPorgression = rmn.ProgressionCompleted
				verifyDRPCStateAndProgression(expectedAction, expectedPhase, expectedPorgression)
				Expect(waitForCompletion(string(rmn.Relocated))).To(Succeed())
			})
		})

		When("Deleting DRPolicy with DRPC references", func() {
			It("Should retain the deleted DRPolicy in the API server", func() {
				// ----------------------------- DELETE DRPolicy  --------------------------------------
				By("\n\n*** DELETE drpolicy ***\n\n")
				Expect(deleteDRPolicyAsync()).To(Succeed())
			})
		})
		When("Deleting user PlacementRule", func() {
			It("Should cleanup DRPC", func() {
				// ----------------------------- DELETE DRPC from PRIMARY --------------------------------------
				By("\n\n*** DELETE User PlacementRule ***\n\n")
				Expect(deleteUserPlacementRule(UserPlacementRuleName, DefaultDRPCNamespace)).To(Succeed())
			})
		})

		When("Deleting DRPC", func() {
			It("Should delete all VRGs", func() {
				Expect(deleteDRPC()).To(Succeed())
				Expect(waitForCompletion("deleted")).To(Succeed())
				ensureNamespaceMWsDeletedFromAllClusters(DefaultDRPCNamespace)
			})
		})
		Specify("delete drclusters", func() {
			Expect(deleteDRClustersAsync()).To(Succeed())
		})
	})

	Context("DRPlacementControl Reconciler HubRecovery VRG Adoption (Subscription)", func() {
		var userPlacementRule1 *plrv1.PlacementRule

		Specify("DRClusters", func() {
			populateDRClusters()
		})

		When("Application deployed for the first time", func() {
			It("Should deploy drpc", func() {
				Expect(createNamespacesAsync(getNamespaceObj(DefaultDRPCNamespace))).To(Succeed())
				Expect(createManagedClusters(asyncClusters)).To(Succeed())
				Expect(createDRClustersAsync()).To(Succeed())
				Expect(createDRPolicyAsync()).To(Succeed())
				setToggleUIDChecks()

				// Create an existing VRG MW on East, to simulate upgrade cases (West1 will report an
				// orphan VRG for orphan cases)
				Expect(createVRGMW(DRPCCommonName, DefaultDRPCNamespace, East1ManagedCluster)).To(Succeed())

				var placementObj client.Object

				placementObj, _ = CreatePlacementAndDRPC(
					DefaultDRPCNamespace, UserPlacementRuleName, East1ManagedCluster, UsePlacementRule)
				userPlacementRule1 = placementObj.(*plrv1.PlacementRule)
				Expect(userPlacementRule1).NotTo(BeNil())
				Expect(waitForDRPCPhaseAndProgression(DefaultDRPCNamespace, rmn.Deployed)).To(Succeed())
				Expect(uploadVRGtoS3Store(DRPCCommonName, DefaultDRPCNamespace, East1ManagedCluster, rmn.VRGAction(""))).To(Succeed())
				resetToggleUIDChecks()
			})
		})

		When("Deleting DRPolicy with DRPC references", func() {
			It("Should retain the deleted DRPolicy in the API server", func() {
				By("\n\n*** DELETE drpolicy ***\n\n")
				Expect(deleteDRPolicyAsync()).To(Succeed())
			})
		})

		When("Deleting user PlacementRule", func() {
			It("Should cleanup DRPC", func() {
				By("\n\n*** DELETE User PlacementRule ***\n\n")
				Expect(deleteUserPlacementRule(UserPlacementRuleName, DefaultDRPCNamespace)).To(Succeed())
			})
		})

		When("Deleting DRPC", func() {
			It("Should delete all VRGs", func() {
				Expect(deleteDRPC()).To(Succeed())
				Expect(waitForCompletion("deleted")).To(Succeed())
				ensureNamespaceMWsDeletedFromAllClusters(DefaultDRPCNamespace)
			})
		})

		Specify("delete drclusters", func() {
			Expect(deleteDRClustersAsync()).To(Succeed())
		})
	})

	/* TODO: This was added to prevent failover in case there is a Secondary VRG still in the cluster, needs to adapt
	to changes in isValidFailoverTarget function now
	Context("Test DRPlacementControl Failover stalls if peer has a Secondary (Placement/Subscription)", func() {
		var placement *clrapiv1beta1.Placement
		var drpc *rmn.DRPlacementControl
		Specify("DRClusters", func() {
			populateDRClusters()
		})
		When("An Application is deployed for the first time using Placement", func() {
			It("Should deploy to East1ManagedCluster", func() {
				By("Initial Deployment")
				var placementObj client.Object
				placementObj, drpc = InitialDeploymentAsync(
					DefaultDRPCNamespace, UserPlacementName, East1ManagedCluster, UsePlacementWithSubscription)
				placement = placementObj.(*clrapiv1beta1.Placement)
				Expect(placement).NotTo(BeNil())
				verifyInitialDRPCDeployment(placement, East1ManagedCluster)
				verifyActionResultForPlacement(placement, East1ManagedCluster, UsePlacementWithSubscription)
				verifyDRPCOwnedByPlacement(placement, getLatestDRPC(DefaultDRPCNamespace))
			})
		})
		When("DRAction changes to Failover", func() {
			It("Should not start failover if there is a secondary VRG on the failoverCluster", func() { // TODO
				setFakeSecondary(West1ManagedCluster)
				setDRPCSpecExpectationTo(DefaultDRPCNamespace, East1ManagedCluster, West1ManagedCluster, rmn.ActionFailover)
				verifyUserPlacementRuleDecisionUnchanged(placement.Name, placement.Namespace, East1ManagedCluster)
				// Check MW for primary on West1ManagedCluster is not created
				Expect(getManifestWorkCount(West1ManagedCluster)).Should(Equal(1)) // DRCluster
				setFakeSecondary("")
			})
		})
		Specify("Cleanup after tests", func() {
			deleteUserPlacement()
			deleteDRPC()
			Expect(waitForCompletion("deleted")).To(Succeed())
			Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(1))       // DRCluster
			Expect(getManagedClusterViewCount(East1ManagedCluster)).Should(Equal(0)) // NS + VRG MCV
			ensureNamespaceMWsDeletedFromAllClusters(DefaultDRPCNamespace)
			deleteDRPolicyAsync()
			ensureDRPolicyIsDeleted(drpc.Spec.DRPolicyRef.Name)
			deleteDRClustersAsync()
			Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(0))
		})
	})*/

	Context("Test DRPlacementControl With VolSync Setup", func() {
		var (
			userPlacementRule *plrv1.PlacementRule
			drpc              *rmn.DRPlacementControl
		)

		Specify("DRClusters", func() {
			RunningVolSyncTests = true

			populateDRClusters()
		})
		When("The Application is deployed for VolSync", func() {
			It("Should deploy to East1ManagedCluster", func() {
				var placementObj client.Object

				placementObj, drpc = InitialDeploymentAsync(
					DefaultDRPCNamespace, UserPlacementRuleName, East1ManagedCluster, UsePlacementRule)
				userPlacementRule = placementObj.(*plrv1.PlacementRule)
				Expect(userPlacementRule).NotTo(BeNil())
				verifyInitialDRPCDeployment(userPlacementRule, East1ManagedCluster)
				latestDRPC, err := getLatestDRPC(DefaultDRPCNamespace)
				Expect(err).NotTo(HaveOccurred())
				verifyDRPCOwnedByPlacement(userPlacementRule, latestDRPC)
			})
		})
		When("DRAction is changed to Failover", func() {
			It("Should failover to Secondary (West1ManagedCluster)", func() {
				recoverToFailoverCluster(userPlacementRule, East1ManagedCluster, West1ManagedCluster)
				Expect(getVRGManifestWorkCount()).Should(Equal(2))
				verifyRDSpecAfterActionSwitch(West1ManagedCluster, East1ManagedCluster, 2)
			})
		})
		When("DRAction is set to Relocate", func() {
			It("Should relocate to Primary (East1ManagedCluster)", func() {
				relocateToPreferredCluster(userPlacementRule, West1ManagedCluster)
				Expect(getVRGManifestWorkCount()).Should(Equal(2))
				verifyRDSpecAfterActionSwitch(East1ManagedCluster, West1ManagedCluster, 2)
			})
		})
		When("DRAction is changed back to Failover using only 1 protectedPVC", func() {
			It("Should failover to secondary (West1ManagedCluster)", func() {
				ProtectedPVCCount = 1

				recoverToFailoverCluster(userPlacementRule, East1ManagedCluster, West1ManagedCluster)
				Expect(getVRGManifestWorkCount()).Should(Equal(2))
				verifyRDSpecAfterActionSwitch(West1ManagedCluster, East1ManagedCluster, 1)

				ProtectedPVCCount = 2
			})
		})
		When("DRAction is set back to Relocate using only 1 protectedPVC", func() {
			It("Should relocate to Primary (East1ManagedCluster)", func() {
				ProtectedPVCCount = 1

				relocateToPreferredCluster(userPlacementRule, West1ManagedCluster)
				Expect(getVRGManifestWorkCount()).Should(Equal(2))
				verifyRDSpecAfterActionSwitch(East1ManagedCluster, West1ManagedCluster, 1)

				ProtectedPVCCount = 2
			})
		})
		When("DRAction is changed back to Failover using only 10 protectedPVC", func() {
			It("Should failover to secondary (West1ManagedCluster)", func() {
				ProtectedPVCCount = 10

				recoverToFailoverCluster(userPlacementRule, East1ManagedCluster, West1ManagedCluster)
				Expect(getVRGManifestWorkCount()).Should(Equal(2))
				verifyRDSpecAfterActionSwitch(West1ManagedCluster, East1ManagedCluster, 10)

				ProtectedPVCCount = 2
			})
		})
		When("DRAction is set back to Relocate using only 10 protectedPVC", func() {
			It("Should relocate to Primary (East1ManagedCluster)", func() {
				ProtectedPVCCount = 10

				relocateToPreferredCluster(userPlacementRule, West1ManagedCluster)
				Expect(getVRGManifestWorkCount()).Should(Equal(2))
				verifyRDSpecAfterActionSwitch(East1ManagedCluster, West1ManagedCluster, 10)

				ProtectedPVCCount = 2
			})
		})
		When("Deleting DRPolicy with DRPC references", func() {
			It("Should retain the deleted DRPolicy in the API server", func() {
				Expect(deleteDRPolicyAsync()).To(Succeed())
				ensureDRPolicyIsNotDeleted(drpc)
			})
		})
		When("Deleting user PlacementRule", func() {
			It("Should cleanup DRPC", func() {
				Expect(deleteUserPlacementRule(UserPlacementRuleName, DefaultDRPCNamespace)).To(Succeed())
			})
		})

		When("Deleting DRPC", func() {
			It("Should delete VRG and NS MWs and MCVs from Primary (East1ManagedCluster)", func() {
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(BeElementOf(3, 4)) // DRCluster + VRG MW
				Expect(deleteDRPC()).To(Succeed())
				Expect(waitForCompletion("deleted")).To(Succeed())
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(1))       // DRCluster
				Expect(getManagedClusterViewCount(East1ManagedCluster)).Should(Equal(0)) // NS + VRG MCV
				ensureNamespaceMWsDeletedFromAllClusters(DefaultDRPCNamespace)
			})
			It("should delete the DRPC causing its referenced drpolicy to be deleted"+
				" by drpolicy controller since no DRPCs reference it anymore", func() {
				ensureDRPolicyIsDeleted(drpc.Spec.DRPolicyRef.Name)
			})
		})
		Specify("delete drclusters", func() {
			RunningVolSyncTests = false
			Expect(deleteDRClustersAsync()).To(Succeed())
		})
	})
})

func getVRGManifestWorkCount() int {
	count := 0

	for _, drCluster := range drClusters {
		mwName := rmnutil.ManifestWorkName(DRPCCommonName, DefaultDRPCNamespace, rmnutil.MWTypeVRG)
		mw := &ocmworkv1.ManifestWork{}

		err := k8sClient.Get(context.TODO(), types.NamespacedName{Name: mwName, Namespace: drCluster.Name}, mw)
		if err == nil {
			count++
		}
	}

	return count
}

func getVRGFromManifestWork(clusterNamespace string) (*rmn.VolumeReplicationGroup, error) {
	mwName := rmnutil.ManifestWorkName(DRPCCommonName, DefaultDRPCNamespace, rmnutil.MWTypeVRG)
	mw := &ocmworkv1.ManifestWork{}

	err := k8sClient.Get(context.TODO(), types.NamespacedName{Name: mwName, Namespace: clusterNamespace}, mw)
	if err != nil {
		return nil, err
	}

	return rmnutil.ExtractVRGFromManifestWork(mw)
}

func verifyRDSpecAfterActionSwitch(primaryCluster, secondaryCluster string, numOfRDSpecs int) {
	// For Primary Cluster
	vrg, err := getVRGFromManifestWork(primaryCluster)
	Expect(err).NotTo(HaveOccurred())
	Expect(len(vrg.Spec.VolSync.RDSpec)).Should(Equal(0))
	// For Secondary Cluster
	vrg, err = getVRGFromManifestWork(secondaryCluster)
	Expect(err).NotTo(HaveOccurred())
	Expect(len(vrg.Spec.VolSync.RDSpec)).Should(Equal(numOfRDSpecs))
}

func verifyDRPCStateAndProgression(expectedAction rmn.DRAction, expectedPhase rmn.DRState,
	expectedPorgression rmn.ProgressionStatus,
) {
	var phase rmn.DRState

	var progression rmn.ProgressionStatus

	Eventually(func() bool {
		drpc, err := getLatestDRPC(DefaultDRPCNamespace)
		if err != nil {
			return false
		}
		phase = drpc.Status.Phase
		progression = drpc.Status.Progression

		return phase == expectedPhase && progression == expectedPorgression
	}, timeout, interval).Should(BeTrue(),
		fmt.Sprintf("Phase has not been updated yet! Phase:%s Expected:%s - progression:%s expected:%s",
			phase, expectedPhase, progression, expectedPorgression))

	drpc, err := getLatestDRPC(DefaultDRPCNamespace)
	Expect(err).NotTo(HaveOccurred())
	Expect(drpc.Spec.Action).Should(Equal(expectedAction))
	Expect(drpc.Status.Phase).Should(Equal(expectedPhase))
	Expect(drpc.Status.Progression).Should(Equal(expectedPorgression))
}

func checkConditionAllowFailover(namespace string) {
	var drpc *rmn.DRPlacementControl

	var availableCondition metav1.Condition

	Eventually(func() bool {
		var err error
		drpc, err = getLatestDRPC(namespace)
		if err != nil {
			return false
		}
		for _, availableCondition = range drpc.Status.Conditions {
			if availableCondition.Type != rmn.ConditionPeerReady {
				if availableCondition.Status == metav1.ConditionTrue {
					return true
				}
			}
		}

		return false
	}, timeout, interval).Should(BeTrue(), fmt.Sprintf("Condition '%+v'", availableCondition))

	Expect(drpc.Status.Phase).To(Equal(rmn.WaitForUser))
}

//nolint:unparam
func uploadVRGtoS3Store(name, namespace, dstCluster string, action rmn.VRGAction) error {
	vrg := buildVRG(name, namespace, dstCluster, action)
	s3ProfileNames := []string{s3Profiles[0].S3ProfileName, s3Profiles[1].S3ProfileName}

	objectStorer, _, err := drpcReconciler.ObjStoreGetter.ObjectStore(
		ctx, apiReader, s3ProfileNames[0], "Hub Recovery", testLogger)
	if err != nil {
		return err
	}

	return controllers.VrgObjectProtect(objectStorer, vrg)
}

// drpcRenconcile calls the drpc reconciler with the given drpc name and
// namespace as the reconcile.Request. This call will be in parallel to the call
// that would be made by the controller. Only use this function when you are
// checking for settled states of the reconciler; for example when you are
// expecting an error that the controller can't correct unless you change the
// state of the cluster or the drpc.
func drpcReconcile(drpcname string, drpcnamespace string) (reconcile.Result, error) {
	res, err := drpcReconciler.Reconcile(context.TODO(), reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      drpcname,
			Namespace: drpcnamespace,
		},
	})

	return res, err
}

func deleteAllManagedClusters() error {
	managedClusters := &spokeClusterV1.ManagedClusterList{}

	err := k8sClient.List(context.TODO(), managedClusters)
	if err != nil {
		return err
	}

	for _, mc := range managedClusters.Items {
		mcToDelete := mc

		mcToDelete.SetFinalizers([]string{})

		err := k8sClient.Update(context.TODO(), &mcToDelete)
		if err != nil {
			return err
		}

		err = k8sClient.Delete(context.TODO(), &mcToDelete)
		if err != nil && !k8serrors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

func deleteAllManifestWorks() error {
	manifestWorks := &ocmworkv1.ManifestWorkList{}

	err := k8sClient.List(context.TODO(), manifestWorks)
	if err != nil {
		return err
	}

	for _, mw := range manifestWorks.Items {
		mwToDelete := mw

		mwToDelete.SetFinalizers([]string{})

		err := k8sClient.Update(context.TODO(), &mwToDelete)
		if err != nil {
			return err
		}

		err = k8sClient.Delete(context.TODO(), &mwToDelete)
		if err != nil && !k8serrors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

func deleteAllManagedClusterViews() error {
	managedClusterViews := &viewv1beta1.ManagedClusterViewList{}

	err := k8sClient.List(context.TODO(), managedClusterViews)
	if err != nil {
		return err
	}

	for _, mcv := range managedClusterViews.Items {
		mcvToDelete := mcv

		mcvToDelete.SetFinalizers([]string{})

		err := k8sClient.Update(context.TODO(), &mcvToDelete)
		if err != nil {
			return err
		}

		err = k8sClient.Delete(context.TODO(), &mcvToDelete)
		if err != nil && !k8serrors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

func deleteAllDRPCs() error {
	drpcs := &rmn.DRPlacementControlList{}

	err := k8sClient.List(context.TODO(), drpcs)
	if err != nil {
		return err
	}

	for _, drpc := range drpcs.Items {
		drpcToDelete := drpc

		drpcToDelete.SetFinalizers([]string{})

		err := k8sClient.Update(context.TODO(), &drpcToDelete)
		if err != nil {
			return err
		}

		err = k8sClient.Delete(context.TODO(), &drpcToDelete)
		if err != nil && !k8serrors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

func deleteAllDRPolicies() error {
	drPolicies := &rmn.DRPolicyList{}

	err := k8sClient.List(context.TODO(), drPolicies)
	if err != nil {
		return err
	}

	for _, drPolicy := range drPolicies.Items {
		drPolicyToDelete := drPolicy

		drPolicyToDelete.SetFinalizers([]string{})

		err := k8sClient.Update(context.TODO(), &drPolicyToDelete)
		if err != nil {
			return err
		}

		err = k8sClient.Delete(context.TODO(), &drPolicyToDelete)
		if err != nil && !k8serrors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

func deleteAllDRClusters() error {
	drClusters := &rmn.DRClusterList{}

	err := k8sClient.List(context.TODO(), drClusters)
	if err != nil {
		return err
	}

	for _, drCluster := range drClusters.Items {
		drClusterToDelete := drCluster

		drClusterToDelete.SetFinalizers([]string{})

		err := k8sClient.Update(context.TODO(), &drClusterToDelete)
		if err != nil {
			return err
		}

		err = k8sClient.Delete(context.TODO(), &drClusterToDelete)
		if err != nil && !k8serrors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

func removeRamenFinalizersFromObject(obj client.Object) {
	ramenFinalizers := []string{
		controllers.DRPCFinalizer,
		rmnutil.SecretPolicyFinalizer,
		rmnutil.SecretPolicyFinalizer + "-" + string(rmnutil.SecretFormatVelero),
	}

	objFinalizers := obj.GetFinalizers()
	newObjFinalizers := []string{}

	for _, finalizer := range objFinalizers {
		for _, ramenFinalizer := range ramenFinalizers {
			if !strings.Contains(finalizer, ramenFinalizer) {
				newObjFinalizers = append(newObjFinalizers, finalizer)
			}
		}
	}

	obj.SetFinalizers(objFinalizers)
}

func removeRamenFinalisersFromSecrets() error {
	secrets := &corev1.SecretList{}

	err := k8sClient.List(context.TODO(), secrets)
	if err != nil {
		return err
	}

	for _, secret := range secrets.Items {
		secretToUpdate := secret

		removeRamenFinalizersFromObject(&secretToUpdate)
		secretToUpdate.SetFinalizers([]string{})

		err := k8sClient.Update(context.TODO(), &secretToUpdate)
		if err != nil && !k8serrors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

func deleteAllUserPlacementRules() error {
	userPlacementRules := &plrv1.PlacementRuleList{}

	err := k8sClient.List(context.TODO(), userPlacementRules)
	if err != nil {
		return err
	}

	for _, upr := range userPlacementRules.Items {
		uprToDelete := upr

		uprToDelete.SetFinalizers([]string{})

		err := k8sClient.Update(context.TODO(), &uprToDelete)
		if err != nil {
			return err
		}

		err = k8sClient.Delete(context.TODO(), &uprToDelete)
		if err != nil && !k8serrors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

func deleteAllPlacementBindings() error {
	placementBindings := &gppv1.PlacementBindingList{}

	err := k8sClient.List(context.TODO(), placementBindings)
	if err != nil {
		return err
	}

	for _, pb := range placementBindings.Items {
		pbToDelete := pb

		pbToDelete.SetFinalizers([]string{})

		err := k8sClient.Update(context.TODO(), &pbToDelete)
		if err != nil {
			return err
		}

		err = k8sClient.Delete(context.TODO(), &pbToDelete)
		if err != nil && !k8serrors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

func forceCleanupClusterAfterAErrorTest() error {
	err := retry.RetryOnConflict(retry.DefaultBackoff, deleteAllDRPCs)
	if err != nil {
		return err
	}

	err = retry.RetryOnConflict(retry.DefaultBackoff, deleteAllDRPolicies)
	if err != nil {
		return err
	}

	err = retry.RetryOnConflict(retry.DefaultBackoff, deleteAllDRClusters)
	if err != nil {
		return err
	}

	err = retry.RetryOnConflict(retry.DefaultBackoff, deleteAllUserPlacementRules)
	if err != nil {
		return err
	}

	err = retry.RetryOnConflict(retry.DefaultBackoff, deleteAllManifestWorks)
	if err != nil {
		return err
	}

	err = retry.RetryOnConflict(retry.DefaultBackoff, deleteAllManagedClusters)
	if err != nil {
		return err
	}

	err = retry.RetryOnConflict(retry.DefaultBackoff, deleteAllManagedClusterViews)
	if err != nil {
		return err
	}

	err = retry.RetryOnConflict(retry.DefaultBackoff, removeRamenFinalisersFromSecrets)
	if err != nil {
		return err
	}

	err = retry.RetryOnConflict(retry.DefaultBackoff, deleteAllPlacementBindings)
	if err != nil {
		return err
	}

	return nil
}
