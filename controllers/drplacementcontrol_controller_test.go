// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/format"
	. "github.com/onsi/gomega/gstruct"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/yaml"

	machineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	spokeClusterV1 "github.com/open-cluster-management/api/cluster/v1"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	viewv1beta1 "github.com/stolostron/multicloud-operators-foundation/pkg/apis/view/v1beta1"

	clrapiv1beta1 "github.com/open-cluster-management-io/api/cluster/v1beta1"
	rmn "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers"
	argocdv1alpha1hack "github.com/ramendr/ramen/controllers/argocd"
	rmnutil "github.com/ramendr/ramen/controllers/util"
	plrv1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	gppv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
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

	pvcCount = 2 // Count of fake PVCs reported in the VRG status
)

var (
	NumberOfVrgsToReturnWhenRebuildingState = 0

	UseApplicationSet = false

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

	cidrs = [][]string{
		{"198.51.100.17/24", "198.51.100.18/24", "198.51.100.19/24"}, // valid CIDR
		{"198.51.100.20/24", "198.51.100.21/24", "198.51.100.22/24"}, // valid CIDR
	}

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
)

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

type FakeMCVGetter struct {
	client.Client
	apiReader client.Reader
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

// GetMModeFromManagedCluster: MMode code uses GetMModeFromManagedCluster to create a MCV and not fetch it, that
// is done using ListMCV. As a result this fake function creates an MCV for record keeping purposes and returns
// a nil mcv back in case of success
func (f FakeMCVGetter) GetMModeFromManagedCluster(
	resourceName, managedCluster string,
	annotations map[string]string,
) (*rmn.MaintenanceMode, error) {
	mModeMCV := &viewv1beta1.ManagedClusterView{}

	mcvName := rmnutil.BuildManagedClusterViewName(resourceName, "", rmnutil.MWTypeMMode)

	err := f.Get(context.TODO(), types.NamespacedName{Name: mcvName, Namespace: managedCluster}, mModeMCV)
	if err == nil {
		return nil, nil
	}

	if !errors.IsNotFound(err) {
		return nil, err
	}

	mModeMCV = &viewv1beta1.ManagedClusterView{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mcvName,
			Namespace: managedCluster,
			Labels: map[string]string{
				rmnutil.MModesLabel: "",
			},
		},
		Spec: viewv1beta1.ViewSpec{},
	}

	err = f.Create(context.TODO(), mModeMCV)

	return nil, err
}

// TODO: The implementation is the same as the one in ManagedClusterViewGetterImpl
func (f FakeMCVGetter) ListMModesMCVs(managedCluster string) (*viewv1beta1.ManagedClusterViewList, error) {
	matchLabels := map[string]string{
		rmnutil.MModesLabel: "",
	}
	listOptions := []client.ListOption{
		client.InNamespace(managedCluster),
		client.MatchingLabels(matchLabels),
	}

	mModeMCVs := &viewv1beta1.ManagedClusterViewList{}
	if err := f.apiReader.List(context.TODO(), mModeMCVs, listOptions...); err != nil {
		return nil, err
	}

	return mModeMCVs, nil
}

// GetResource looks up the ManifestWork for the view resource, and if found returns the manifest resource
// with an appropriately faked status
// NOTE: Currently as only the MMode operations are directly using the GetResource function, this implementation
// assumes the same.
func (f FakeMCVGetter) GetResource(mcv *viewv1beta1.ManagedClusterView, resource interface{}) error {
	foundMW := &ocmworkv1.ManifestWork{}
	mwName := fmt.Sprintf(
		rmnutil.ManifestWorkNameFormatClusterScope,
		rmnutil.ClusterScopedResourceNameFromMCVName(mcv.GetName()),
		rmnutil.MWTypeMMode,
	)

	err := f.Get(context.TODO(),
		types.NamespacedName{Name: mwName, Namespace: mcv.GetNamespace()},
		foundMW)
	if err != nil {
		// Potentially NotFound error that we want to return anyway
		return err
	}

	// Found the MW for the MCV, return a fake resource status
	mModeFromMW, err := rmnutil.ExtractMModeFromManifestWork(foundMW)
	if err != nil {
		return err
	}

	mModeFromMW.Status = rmn.MaintenanceModeStatus{
		State:              rmn.MModeStateCompleted,
		ObservedGeneration: mModeFromMW.Generation,
		Conditions: []metav1.Condition{
			{
				Type:               string(rmn.MModeConditionFailoverActivated),
				Status:             metav1.ConditionTrue,
				LastTransitionTime: metav1.NewTime(time.Now()),
				Reason:             "testing",
				Message:            "testing",
			},
		},
	}

	// TODO: Is this required, i.e unmarshal and then marshal again?
	marJ, err := json.Marshal(mModeFromMW)
	if err != nil {
		return err
	}

	return json.Unmarshal(marJ, resource)
}

// DeleteManagedClusterView: This fake function would eventually delete the MMode MCV that is created
// by the call to GetMModeFromManagedCluster. It is generic enough to delete any MCV that was created as well
// TODO: Implementation is mostly the same as the one in ManagedClusterViewGetterImpl
func (f FakeMCVGetter) DeleteManagedClusterView(clusterName, mcvName string, logger logr.Logger) error {
	mcv := &viewv1beta1.ManagedClusterView{}

	err := f.Get(context.TODO(), types.NamespacedName{Name: mcvName, Namespace: clusterName}, mcv)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}

		return err
	}

	return f.Delete(context.TODO(), mcv)
}

func (f FakeMCVGetter) GetNamespaceFromManagedCluster(
	resourceName, managedCluster, namespaceString string, annotations map[string]string,
) (*corev1.Namespace, error) {
	appNamespaceObj := &corev1.Namespace{}

	// err := k8sClient.Get(context.TODO(), appNamespaceLookupKey, appNamespaceObj)
	foundMW := &ocmworkv1.ManifestWork{}
	mwName := fmt.Sprintf(rmnutil.ManifestWorkNameFormat, resourceName, namespaceString, rmnutil.MWTypeNS)
	err := k8sClient.Get(context.TODO(),
		types.NamespacedName{Name: mwName, Namespace: managedCluster},
		foundMW)

	return appNamespaceObj, err
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

func setRestorePVsUncomplete() {
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

//nolint:funlen,cyclop,gocognit
func (f FakeMCVGetter) GetVRGFromManagedCluster(resourceName, resourceNamespace, managedCluster string,
	annnotations map[string]string,
) (*rmn.VolumeReplicationGroup, error) {
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
	vrg := getDefaultVRG(resourceNamespace).DeepCopy()
	vrg.Status = vrgStatus

	vrg.Generation = 1

	switch getFunctionNameAtIndex(2) {
	case "updateResourceCondition":
		for i := 0; i < pvcCount; i++ {
			vrg.Status.ProtectedPVCs = append(vrg.Status.ProtectedPVCs, rmn.ProtectedPVC{Name: fmt.Sprintf("fakePVC%d", i)})
		}

		return vrg, nil
	case "ensureClusterDataRestored":
		if isRestorePVsComplete() {
			vrg.Status.Conditions[0].Type = controllers.VRGConditionTypeClusterDataReady
			vrg.Status.Conditions[0].Reason = controllers.VRGConditionReasonClusterDataRestored
		}

		return vrg, nil

	case "checkAccessToVRGOnCluster":
		return checkResource(managedCluster)

	case "ensureVRGIsSecondaryOnCluster":
		return moveVRGToSecondary(managedCluster, resourceNamespace, "vrg", false)

	case "ensureDataProtectedOnCluster":
		return moveVRGToSecondary(managedCluster, resourceNamespace, "vrg", true)

	case "ensureVRGDeleted":
		return nil, errors.NewNotFound(schema.GroupResource{}, "requested resource not found in ManagedCluster")

	case "getVRGsFromManagedClusters":
		return doGetFakeVRGsFromManagedClusters(managedCluster, resourceNamespace, vrgStatus)
	}

	return nil, fmt.Errorf("unknown caller %s", getFunctionNameAtIndex(2))
}

func checkResource(managedCluster string) (*rmn.VolumeReplicationGroup, error) {
	if managedCluster == ClusterIsDown {
		return nil, fmt.Errorf("Faking cluster down %s", managedCluster)
	}

	return nil, nil
}

func doGetFakeVRGsFromManagedClusters(managedCluster string, vrgNamespace string,
	vrgStatus rmn.VolumeReplicationGroupStatus,
) (*rmn.VolumeReplicationGroup, error) {
	if managedCluster == ClusterIsDown {
		return nil, fmt.Errorf("Faking cluster down %s", managedCluster)
	}

	vrgFromMW, err := getVRGFromManifestWork(managedCluster, vrgNamespace)
	if err != nil && !errors.IsNotFound(err) {
		return nil, err
	}

	if errors.IsNotFound(err) {
		if getFunctionNameAtIndex(4) == "getVRGs" { // Called only from DRCluster reconciler, at present
			return fakeVRGWithMModesProtectedPVC(vrgNamespace)
		}

		if getFunctionNameAtIndex(4) == "determineDRPCState" && ToggleUIDChecks {
			// Fake it, no UID
			return getDefaultVRG(vrgNamespace), nil
		}

		return nil, err
	}

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

	protectedPVC := &rmn.ProtectedPVC{}
	protectedPVC.Name = "random name"
	protectedPVC.StorageIdentifiers.ReplicationID.ID = MModeReplicationID
	protectedPVC.StorageIdentifiers.StorageProvisioner = MModeCSIProvisioner
	protectedPVC.StorageIdentifiers.ReplicationID.Modes = []rmn.MMode{rmn.MModeFailover}

	vrgFromMW.Status.ProtectedPVCs = append(vrgFromMW.Status.ProtectedPVCs, *protectedPVC)

	return vrgFromMW, nil
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

func getVRGFromManifestWork(managedCluster, resourceNamespace string) (*rmn.VolumeReplicationGroup, error) {
	manifestLookupKey := types.NamespacedName{
		Name:      rmnutil.ManifestWorkName(DRPCCommonName, getVRGNamespace(resourceNamespace), "vrg"),
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

func fakeVRGWithMModesProtectedPVC(vrgNamespace string) (*rmn.VolumeReplicationGroup, error) {
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

func createPlacement(name, namespace string) *clrapiv1beta1.Placement {
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
	Expect(err).NotTo(HaveOccurred())

	return placement
}

func createDRPC(placementName, name, namespace, drPolicyName, preferredCluster string) *rmn.DRPlacementControl {
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
	Expect(k8sClient.Create(context.TODO(), drpc)).Should(Succeed())

	return drpc
}

//nolint:unparam
func deleteUserPlacementRule(name, namespace string) {
	userPlacementRule := getLatestUserPlacementRule(name, namespace)
	Expect(k8sClient.Delete(context.TODO(), userPlacementRule)).Should(Succeed())
}

func deleteUserPlacement(name, namespace string) {
	userPlacement := getLatestUserPlacement(name, namespace)
	Expect(k8sClient.Delete(context.TODO(), userPlacement)).Should(Succeed())
}

func deleteDRPC() {
	drpc := getLatestDRPC(DefaultDRPCNamespace)
	Expect(k8sClient.Delete(context.TODO(), drpc)).Should(Succeed())
}

func deleteNamespaceMWsFromAllClusters(namespace string) {
	foundMW := &ocmworkv1.ManifestWork{}
	mwName := fmt.Sprintf(rmnutil.ManifestWorkNameFormat, DRPCCommonName, namespace, rmnutil.MWTypeNS)
	err := k8sClient.Get(context.TODO(),
		types.NamespacedName{Name: mwName, Namespace: East1ManagedCluster},
		foundMW)

	if err == nil {
		Expect(k8sClient.Delete(context.TODO(), foundMW)).Should(Succeed())
	}

	err = k8sClient.Get(context.TODO(),
		types.NamespacedName{Name: mwName, Namespace: West1ManagedCluster},
		foundMW)
	if err == nil {
		Expect(k8sClient.Delete(context.TODO(), foundMW)).Should(Succeed())
	}
}

func setDRPCSpecExpectationTo(namespace, preferredCluster, failoverCluster string, action rmn.DRAction) {
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

	Expect(retryErr).NotTo(HaveOccurred())

	Eventually(func() bool {
		latestDRPC = getLatestDRPC(namespace)

		return latestDRPC.Spec.Action == action
	}, timeout, interval).Should(BeTrue(), "failed to update DRPC DR action on time")
}

func getLatestDRPC(namespace string) *rmn.DRPlacementControl {
	drpcLookupKey := types.NamespacedName{
		Name:      DRPCCommonName,
		Namespace: namespace,
	}
	latestDRPC := &rmn.DRPlacementControl{}
	err := apiReader.Get(context.TODO(), drpcLookupKey, latestDRPC)
	Expect(err).NotTo(HaveOccurred())

	return latestDRPC
}

func clearDRPCStatus() {
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
	Expect(retryErr).NotTo(HaveOccurred())
}

//nolint:unparam
func clearFakeUserPlacementRuleStatus(name, namespace string) {
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

	Expect(retryErr).NotTo(HaveOccurred())
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

func createManagedClusters(managedClusters []*spokeClusterV1.ManagedCluster) {
	for _, cl := range managedClusters {
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

func createDRClusters(inClusters []*spokeClusterV1.ManagedCluster) {
	for _, managedCluster := range inClusters {
		for idx := range drClusters {
			if managedCluster.Name == drClusters[idx].Name {
				err := k8sClient.Create(context.TODO(), &drClusters[idx])
				Expect(err).NotTo(HaveOccurred())
				updateDRClusterManifestWorkStatus(drClusters[idx].Name)
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
	policy := asyncDRPolicy.DeepCopy()
	createDRPolicy(policy)
}

func createAppSet() {
	err := k8sClient.Create(context.TODO(), &appSet)
	Expect(err).NotTo(HaveOccurred())
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

func moveVRGToSecondary(clusterNamespace, resourceNamespace, mwType string, protectData bool,
) (*rmn.VolumeReplicationGroup, error) {
	if clusterNamespace == ClusterIsDown {
		return nil, fmt.Errorf("moveVRGToSecondary: Faking cluster down %s", clusterNamespace)
	}

	manifestLookupKey := types.NamespacedName{
		Name:      rmnutil.ManifestWorkName(DRPCCommonName, getVRGNamespace(resourceNamespace), mwType),
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

// createVRGMW creates a basic (always Primary) ManifestWork for a VRG, used to fake existing VRG MW
// to test upgrade cases for DRPC based UID adoption
func createVRGMW(name, namespace, homeCluster string) {
	vrg := getDefaultVRG(namespace)

	mwu := rmnutil.MWUtil{
		Client:          k8sClient,
		APIReader:       k8sClient,
		Ctx:             context.TODO(),
		Log:             logr.Logger{},
		InstName:        name,
		TargetNamespace: namespace,
	}

	Expect(mwu.CreateOrUpdateVRGManifestWork(name, namespace, homeCluster, *vrg, nil)).To(Succeed())
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

func updateManifestWorkStatus(clusterNamespace, vrgNamespace, mwType, workType string) {
	manifestLookupKey := types.NamespacedName{
		Name:      rmnutil.ManifestWorkName(DRPCCommonName, getVRGNamespace(vrgNamespace), mwType),
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

	retryErr := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		err := k8sClient.Get(context.TODO(), manifestLookupKey, mw)
		if err != nil {
			return err
		}

		mw.Status = pvManifestStatus

		return k8sClient.Status().Update(context.TODO(), mw)
	})

	Expect(retryErr).NotTo(HaveOccurred())

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), manifestLookupKey, mw)

		return err == nil && len(mw.Status.Conditions) != 0
	}, timeout, interval).Should(BeTrue(), "failed to wait for PV manifest condition type to change to 'Applied'")
}

func waitForVRGMWDeletion(clusterNamespace, vrgNamespace string) {
	manifestLookupKey := types.NamespacedName{
		Name:      rmnutil.ManifestWorkName(DRPCCommonName, getVRGNamespace(vrgNamespace), "vrg"),
		Namespace: clusterNamespace,
	}
	createdManifest := &ocmworkv1.ManifestWork{}

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), manifestLookupKey, createdManifest)

		return errors.IsNotFound(err)
	}, timeout, interval).Should(BeTrue(), "failed to wait for manifest deletion for type vrg")
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
			errors.NewNotFound(
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
	createNamespacesAsync(getNamespaceObj(namespace))

	createManagedClusters(asyncClusters)
	createDRClustersAsync()
	createDRPolicyAsync()

	return CreatePlacementAndDRPC(namespace, placementName, homeCluster, plType)
}

func CreatePlacementAndDRPC(namespace, placementName, homeCluster string, plType PlacementType) (
	client.Object, *rmn.DRPlacementControl,
) {
	var placementObj client.Object

	switch plType {
	case UsePlacementRule:
		placementObj = createPlacementRule(placementName, namespace)
	case UsePlacementWithSubscription:
		placementObj = createPlacement(placementName, namespace)
	case UsePlacementWithAppSet:
		createAppSet()

		placementObj = createPlacement(placementName, namespace)
	default:
		Fail("Wrong placement type")
	}

	return placementObj, createDRPC(placementName, DRPCCommonName, namespace, AsyncDRPolicyName, homeCluster)
}

func FollowOnDeploymentAsync(namespace, placementName, homeCluster string) (*plrv1.PlacementRule,
	*rmn.DRPlacementControl,
) {
	createNamespace(appNamespace2)

	placementRule := createPlacementRule(placementName, namespace)
	drpc := createDRPC(placementName, DRPCCommonName, namespace, AsyncDRPolicyName, homeCluster)

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

	Expect(len(createdVRGRolesManifest.Spec.Workload.Manifests)).To(Equal(10))

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
	drpc := getLatestDRPC(namespace)
	Expect(vrg.Spec.KubeObjectProtection).Should(Equal(drpc.Spec.KubeObjectProtection))
}

func getManifestWorkCount(homeClusterNamespace string) int {
	manifestWorkList := &ocmworkv1.ManifestWorkList{}
	listOptions := &client.ListOptions{Namespace: homeClusterNamespace}

	Expect(apiReader.List(context.TODO(), manifestWorkList, listOptions)).NotTo(HaveOccurred())

	return len(manifestWorkList.Items)
}

func verifyNSManifestWorkBackupLabelNotExist(resourceName, namespaceString, managedCluster string) {
	mw := &ocmworkv1.ManifestWork{}
	mwName := fmt.Sprintf(rmnutil.ManifestWorkNameFormat, resourceName, namespaceString, rmnutil.MWTypeNS)
	err := k8sClient.Get(context.TODO(),
		types.NamespacedName{Name: mwName, Namespace: managedCluster},
		mw)

	Expect(err).NotTo(HaveOccurred())

	Expect(mw).ToNot(BeNil())
	Expect(mw.Labels[rmnutil.OCMBackupLabelKey]).To(Equal(""))
}

func getManagedClusterViewCount(homeClusterNamespace string) int {
	mcvList := &viewv1beta1.ManagedClusterViewList{}
	listOptions := &client.ListOptions{Namespace: homeClusterNamespace}

	Expect(k8sClient.List(context.TODO(), mcvList, listOptions)).NotTo(HaveOccurred())

	return len(mcvList.Items)
}

func verifyUserPlacementRuleDecision(name, namespace, homeCluster string) {
	usrPlcementLookupKey := types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}

	usrPlRule := &plrv1.PlacementRule{}

	var placementObj client.Object

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), usrPlcementLookupKey, usrPlRule)
		if errors.IsNotFound(err) {
			usrPlmnt := &clrapiv1beta1.Placement{}
			err = k8sClient.Get(context.TODO(), usrPlcementLookupKey, usrPlmnt)
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
	usrPlcementLookupKey := types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}

	usrPlRule := &plrv1.PlacementRule{}

	var placementObj client.Object

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), usrPlcementLookupKey, usrPlRule)
		if errors.IsNotFound(err) {
			usrPlmnt := &clrapiv1beta1.Placement{}
			err = k8sClient.Get(context.TODO(), usrPlcementLookupKey, usrPlmnt)
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

func getLatestUserPlacement(name, namespace string) *clrapiv1beta1.Placement {
	key := types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}

	plmnt := &clrapiv1beta1.Placement{}

	err := k8sClient.Get(context.TODO(), key, plmnt)
	Expect(err).NotTo(HaveOccurred())

	return plmnt
}

func getLatestUserPlacementDecision(name, namespace string) *clrapiv1beta1.ClusterDecision {
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
		}
	}

	if errors.IsNotFound(err) {
		usrPlmnt := &clrapiv1beta1.Placement{}
		err = k8sClient.Get(context.TODO(), key, usrPlmnt)
		Expect(err).NotTo(HaveOccurred())

		plDecision := getPlacementDecision(usrPlmnt.GetName(), usrPlmnt.GetNamespace())
		if plDecision != nil {
			return &clrapiv1beta1.ClusterDecision{
				ClusterName: plDecision.Status.Decisions[0].ClusterName,
				Reason:      "Placement Testing",
			}
		}
	}

	return nil
}

func waitForCompletion(expectedState string) {
	Eventually(func() bool {
		return drstate == expectedState
	}, timeout*2, interval).Should(BeTrue(),
		fmt.Sprintf("failed waiting for state to match. expecting: %s, found %s", expectedState, drstate))
}

//nolint:unparam
func waitForDRPCPhaseAndProgression(namespace string, drState rmn.DRState) {
	Eventually(func() bool {
		drpc := getLatestDRPC(namespace)

		return drpc.Status.Phase == drState && drpc.Status.Progression == rmn.ProgressionCompleted
	}, timeout, interval).Should(BeTrue(), fmt.Sprintf("Timed out waiting for Phase to match. Expected %s for drpcNS %s",
		drState, namespace))
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
		Expect(getManifestWorkCount(fromCluster)).Should(Equal(2))        // DRCluster + NS MW
	} else {
		Expect(getManifestWorkCount(toCluster)).Should(Equal(4))   // MW for VRG+DRCluster + NS + NF
		Expect(getManifestWorkCount(fromCluster)).Should(Equal(2)) // NS + DRCluster MW
	}

	drpc := getLatestDRPC(placementObj.GetNamespace())
	// At this point expect the DRPC status condition to have 2 types
	// {Available and PeerReady}
	// Final state is 'FailedOver'
	Expect(drpc.Status.Phase).To(Equal(rmn.FailedOver))
	Expect(len(drpc.Status.Conditions)).To(Equal(3))
	_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionAvailable)
	Expect(condition.Reason).To(Equal(string(rmn.FailedOver)))
	Expect(drpc.Status.ActionStartTime).ShouldNot(BeNil())

	decision := getLatestUserPlacementDecision(placementObj.GetName(), placementObj.GetNamespace())
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
		Expect(getManifestWorkCount(fromCluster)).Should(Equal(2)) // DRClusters + NS MW
	} else {
		// By the time this check is made, the NetworkFence CR in the
		// cluster from where the application is migrated might not have
		// been deleted. Hence, the number of MW expectation will be
		// either of 2 or 3.
		Expect(getManifestWorkCount(fromCluster)).Should(BeElementOf(2, 3))
	}

	drpc := getLatestDRPC(placementObj.GetNamespace())
	// At this point expect the DRPC status condition to have 2 types
	// {Available and PeerReady}
	// Final state is 'Relocated'
	Expect(drpc.Status.Phase).To(Equal(rmn.Relocated))
	Expect(len(drpc.Status.Conditions)).To(Equal(3))
	_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionAvailable)
	Expect(condition.Reason).To(Equal(string(rmn.Relocated)))

	decision := getLatestUserPlacementDecision(placementObj.GetName(), placementObj.GetNamespace())
	Expect(decision.ClusterName).To(Equal(toCluster1))
	Expect(condition.Reason).To(Equal(string(rmn.Relocated)))
	Expect(drpc.GetAnnotations()[controllers.LastAppDeploymentCluster]).To(Equal(toCluster1))
}

func clearDRActionAfterRelocate(userPlacementRule *plrv1.PlacementRule, preferredCluster, failoverCluster string) {
	setDRPCSpecExpectationTo(userPlacementRule.GetNamespace(), preferredCluster, failoverCluster, "")
	waitForCompletion(string(rmn.Deployed))

	drpc := getLatestDRPC(userPlacementRule.GetNamespace())
	// At this point expect the DRPC status condition to have 2 types
	// {Available and PeerReady}
	// Final state didn't change and it is 'Relocated' even though we tried to run
	// initial deployment
	Expect(drpc.Status.Phase).To(Equal(rmn.Deployed))
	Expect(len(drpc.Status.Conditions)).To(Equal(3))
	_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionAvailable)
	Expect(condition.Reason).To(Equal(string(rmn.Deployed)))

	decision := getLatestUserPlacementDecision(userPlacementRule.Name, userPlacementRule.Namespace)
	Expect(decision.ClusterName).To(Equal(preferredCluster))
}

func relocateToPreferredCluster(placementObj client.Object, fromCluster string) {
	toCluster1 := "east1-cluster"

	setDRPCSpecExpectationTo(placementObj.GetNamespace(), toCluster1, fromCluster, rmn.ActionRelocate)

	updateManifestWorkStatus(toCluster1, placementObj.GetNamespace(), "vrg", ocmworkv1.WorkApplied)

	verifyUserPlacementRuleDecision(placementObj.GetName(), placementObj.GetNamespace(), toCluster1)
	verifyDRPCStatusPreferredClusterExpectation(placementObj.GetNamespace(), rmn.Relocated)
	verifyVRGManifestWorkCreatedAsPrimary(placementObj.GetNamespace(), toCluster1)

	waitForVRGMWDeletion(West1ManagedCluster, placementObj.GetNamespace())

	waitForCompletion(string(rmn.Relocated))
}

func recoverToFailoverCluster(placementObj client.Object, fromCluster, toCluster string) {
	setDRPCSpecExpectationTo(placementObj.GetNamespace(), fromCluster, toCluster, rmn.ActionFailover)

	updateManifestWorkStatus(toCluster, placementObj.GetNamespace(), "vrg", ocmworkv1.WorkApplied)

	verifyUserPlacementRuleDecision(placementObj.GetName(), placementObj.GetNamespace(), toCluster)
	verifyDRPCStatusPreferredClusterExpectation(placementObj.GetNamespace(), rmn.FailedOver)
	verifyVRGManifestWorkCreatedAsPrimary(placementObj.GetNamespace(), toCluster)

	waitForVRGMWDeletion(fromCluster, placementObj.GetNamespace())

	waitForCompletion(string(rmn.FailedOver))
}

func createNamespacesSync() {
	createNamespace(east1ManagedClusterNamespace)
	createNamespace(east2ManagedClusterNamespace)
	createNamespace(appNamespace)
}

func InitialDeploymentSync(namespace, placementName, homeCluster string) (*plrv1.PlacementRule,
	*rmn.DRPlacementControl,
) {
	createNamespacesSync()

	createManagedClusters(syncClusters)
	createDRClustersSync()
	createDRPolicySync()

	placementRule := createPlacementRule(placementName, namespace)
	drpc := createDRPC(UserPlacementRuleName, DRPCCommonName, DefaultDRPCNamespace, SyncDRPolicyName, homeCluster)

	return placementRule, drpc
}

func createDRClustersSync() {
	createDRClusters(syncClusters)
}

func createDRPolicySync() {
	policy := syncDRPolicy.DeepCopy()
	createDRPolicy(policy)
}

func deleteDRClustersSync() {
	deleteDRClusters(syncClusters)
}

func deleteDRPolicySync() {
	Expect(k8sClient.Delete(context.TODO(), getSyncDRPolicy())).To(Succeed())
}

func fenceCluster(cluster string, manual bool) {
	latestDRCluster := getLatestDRCluster(cluster)
	if manual {
		latestDRCluster.Spec.ClusterFence = rmn.ClusterFenceStateManuallyFenced
	} else {
		latestDRCluster.Spec.ClusterFence = rmn.ClusterFenceStateFenced
	}

	latestDRCluster = updateDRClusterParameters(latestDRCluster)
	drclusterConditionExpectEventually(latestDRCluster, false, metav1.ConditionTrue,
		Equal(controllers.DRClusterConditionReasonFenced), Ignore(),
		rmn.DRClusterConditionTypeFenced)
}

func unfenceCluster(cluster string, manual bool) {
	latestDRCluster := getLatestDRCluster(cluster)
	if manual {
		latestDRCluster.Spec.ClusterFence = rmn.ClusterFenceStateManuallyUnfenced
	} else {
		latestDRCluster.Spec.ClusterFence = rmn.ClusterFenceStateUnfenced
	}

	latestDRCluster = updateDRClusterParameters(latestDRCluster)
	drclusterConditionExpectEventually(latestDRCluster, false, metav1.ConditionFalse,
		BeElementOf(controllers.DRClusterConditionReasonUnfenced, controllers.DRClusterConditionReasonCleaning,
			controllers.DRClusterConditionReasonClean),
		Ignore(), rmn.DRClusterConditionTypeFenced)
}

func resetdrCluster(cluster string) {
	latestDRCluster := getLatestDRCluster(cluster)
	latestDRCluster.Spec.ClusterFence = ""
	updateDRClusterParameters(latestDRCluster)
}

//nolint:unparam
func verifyInitialDRPCDeployment(userPlacement client.Object, preferredCluster string) {
	verifyVRGManifestWorkCreatedAsPrimary(userPlacement.GetNamespace(), preferredCluster)
	updateManifestWorkStatus(preferredCluster, userPlacement.GetNamespace(), "vrg", ocmworkv1.WorkApplied)
	verifyUserPlacementRuleDecision(userPlacement.GetName(), userPlacement.GetNamespace(), preferredCluster)
	verifyDRPCStatusPreferredClusterExpectation(userPlacement.GetNamespace(), rmn.Deployed)
	Expect(getManifestWorkCount(preferredCluster)).Should(BeElementOf(3, 4)) // MWs for VRG, 2 namespaces, and DRCluster
	waitForCompletion(string(rmn.Deployed))

	latestDRPC := getLatestDRPC(userPlacement.GetNamespace())
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

	verifyNSManifestWorkBackupLabelNotExist(latestDRPC.Name, getVRGNamespace(latestDRPC.Namespace),
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
		Expect(getManifestWorkCount(toCluster)).Should(Equal(4)) // MW for VRG+NS+DRCluster+NF
	}

	Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(2)) // DRClustern+NS

	drpc := getLatestDRPC(placementObj.GetNamespace())
	// At this point expect the DRPC status condition to have 2 types
	// {Available and PeerReady}
	// Final state is 'FailedOver'
	Expect(drpc.Status.Phase).To(Equal(rmn.FailedOver))
	Expect(len(drpc.Status.Conditions)).To(Equal(3))
	_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionAvailable)
	Expect(condition.Reason).To(Equal(string(rmn.FailedOver)))

	decision := getLatestUserPlacementDecision(placementObj.GetName(), placementObj.GetNamespace())
	Expect(decision.ClusterName).To(Equal(toCluster))
	Expect(drpc.GetAnnotations()[controllers.LastAppDeploymentCluster]).To(Equal(toCluster))
}

func verifyActionResultForPlacement(placement *clrapiv1beta1.Placement, homeCluster string, plType PlacementType) {
	placementDecision := getPlacementDecision(placement.GetName(), placement.GetNamespace())
	Expect(placementDecision).ShouldNot(BeNil())
	Expect(placementDecision.GetLabels()["velero.io/exclude-from-backup"]).Should(Equal("true"))
	Expect(placementDecision.Status.Decisions[0].ClusterName).Should(Equal(homeCluster))
	vrg, err := getVRGFromManifestWork(homeCluster, placement.GetNamespace())
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
			waitForCompletion(string(rmn.Deployed))

			err := retry.RetryOnConflict(retry.DefaultBackoff, deleteAllDRClusters)
			Expect(err).ToNot(HaveOccurred())

			deleteDRPC()

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
//nolint:errcheck,scopelint
var _ = Describe("DRPlacementControl Reconciler", func() {
	Specify("DRClusters", func() {
		populateDRClusters()
	})
	Context("DRPlacementControl Reconciler Async DR using PlacementRule (Subscription)", func() {
		var userPlacementRule *plrv1.PlacementRule
		var drpc *rmn.DRPlacementControl
		When("An Application is deployed for the first time", func() {
			It("Should deploy to East1ManagedCluster", func() {
				By("Initial Deployment")
				var placementObj client.Object
				placementObj, drpc = InitialDeploymentAsync(
					DefaultDRPCNamespace, UserPlacementRuleName, East1ManagedCluster, UsePlacementRule)
				userPlacementRule = placementObj.(*plrv1.PlacementRule)
				Expect(userPlacementRule).NotTo(BeNil())
				verifyInitialDRPCDeployment(userPlacementRule, East1ManagedCluster)
				verifyDRPCOwnedByPlacement(userPlacementRule, getLatestDRPC(DefaultDRPCNamespace))
			})
		})
		When("DRAction changes to Failover", func() {
			It("Should not failover to Secondary (West1ManagedCluster) till PV manifest is applied", func() {
				By("\n\n*** Failover - 1\n\n")
				setRestorePVsUncomplete()
				setDRPCSpecExpectationTo(DefaultDRPCNamespace, East1ManagedCluster, West1ManagedCluster, rmn.ActionFailover)
				verifyUserPlacementRuleDecisionUnchanged(userPlacementRule.Name, userPlacementRule.Namespace, West1ManagedCluster)
				// MWs for VRG, NS, DRCluster, and MMode
				Eventually(getManifestWorkCount, timeout, interval).WithArguments(West1ManagedCluster).Should(BeElementOf(3, 4))
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
				clearFakeUserPlacementRuleStatus(UserPlacementRuleName, DefaultDRPCNamespace)
				clearDRPCStatus()
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
				deleteDRPolicyAsync()
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
				deleteUserPlacementRule(UserPlacementRuleName, DefaultDRPCNamespace)
			})
		})

		When("Deleting DRPC", func() {
			It("Should delete VRG and NS MWs and MCVs from Primary (East1ManagedCluster)", func() {
				// ----------------------------- DELETE DRPC from PRIMARY --------------------------------------
				By("\n\n*** DELETE DRPC ***\n\n")
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(BeElementOf(3, 4)) // DRCluster + VRG MW
				deleteDRPC()
				waitForCompletion("deleted")
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(2))       // DRCluster + NS MW only
				Expect(getManagedClusterViewCount(East1ManagedCluster)).Should(Equal(0)) // NS + VRG MCV
				deleteNamespaceMWsFromAllClusters(DefaultDRPCNamespace)
			})
			It("should delete the DRPC causing its referenced drpolicy to be deleted"+
				" by drpolicy controller since no DRPCs reference it anymore", func() {
				ensureDRPolicyIsDeleted(drpc.Spec.DRPolicyRef.Name)
			})
		})
		Specify("delete drclusters", func() {
			deleteDRClustersAsync()
		})
	})
	// TEST WITH Placement AND Subscription
	Context("DRPlacementControl Reconciler Async DR using Placement (Subscription)", func() {
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
		When("DRAction changes to Failover using Placement with Subscription", func() {
			It("Should not failover to Secondary (West1ManagedCluster) till PV manifest is applied", func() {
				setRestorePVsUncomplete()
				setDRPCSpecExpectationTo(DefaultDRPCNamespace, East1ManagedCluster, West1ManagedCluster, rmn.ActionFailover)
				verifyUserPlacementRuleDecisionUnchanged(placement.Name, placement.Namespace, West1ManagedCluster)
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
				deleteDRPolicyAsync()
				ensureDRPolicyIsNotDeleted(drpc)
			})
		})
		When("Deleting user Placement", func() {
			It("Should cleanup DRPC", func() {
				deleteUserPlacement(UserPlacementName, DefaultDRPCNamespace)
				drpc := getLatestDRPC(DefaultDRPCNamespace)
				_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionPeerReady)
				Expect(condition).NotTo(BeNil())
			})
		})
		When("Deleting DRPC when using Placement", func() {
			It("Should delete VRG and NS MWs and MCVs from Primary (East1ManagedCluster)", func() {
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(BeElementOf(3, 4)) // DRCluster + VRG + NS MW
				deleteDRPC()
				waitForCompletion("deleted")
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(2))       // DRCluster + NS MW only
				Expect(getManagedClusterViewCount(East1ManagedCluster)).Should(Equal(0)) // NS + VRG MCV
				deleteNamespaceMWsFromAllClusters(DefaultDRPCNamespace)
			})
			It("should delete the DRPC causing its referenced drpolicy to be deleted"+
				" by drpolicy controller since no DRPCs reference it anymore", func() {
				ensureDRPolicyIsDeleted(drpc.Spec.DRPolicyRef.Name)
			})
		})
		Specify("delete drclusters when using Placement", func() {
			deleteDRClustersAsync()
		})
	})
	// TEST WITH Placement AND ApplicationSet
	Context("DRPlacementControl Reconciler Async DR using Placement (ApplicationSet)", func() {
		var placement *clrapiv1beta1.Placement
		var drpc *rmn.DRPlacementControl
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
				verifyDRPCOwnedByPlacement(placement, getLatestDRPC(DefaultDRPCNamespace))
			})
		})
		When("DRAction changes to Failover using Placement", func() {
			It("Should not failover to Secondary (West1ManagedCluster) till PV manifest is applied", func() {
				setRestorePVsUncomplete()
				setDRPCSpecExpectationTo(DefaultDRPCNamespace, East1ManagedCluster, West1ManagedCluster, rmn.ActionFailover)
				verifyUserPlacementRuleDecisionUnchanged(placement.Name, placement.Namespace, East1ManagedCluster)
				// MWs for VRG, NS, VRG DRCluster, and MMode
				Eventually(getManifestWorkCount, timeout, interval).WithArguments(West1ManagedCluster).Should(BeElementOf(3, 4))
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
				deleteDRPolicyAsync()
				ensureDRPolicyIsNotDeleted(drpc)
			})
		})
		When("Deleting user Placement", func() {
			It("Should cleanup DRPC", func() {
				deleteUserPlacement(UserPlacementName, DefaultDRPCNamespace)
				drpc := getLatestDRPC(DefaultDRPCNamespace)
				_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionPeerReady)
				Expect(condition).NotTo(BeNil())
			})
		})
		When("Deleting DRPC when using Placement", func() {
			It("Should delete VRG and NS MWs and MCVs from Primary (East1ManagedCluster)", func() {
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(BeElementOf(3, 4)) // DRCluster + VRG + NS MW
				deleteDRPC()
				waitForCompletion("deleted")
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(2))       // DRCluster + NS MW only
				Expect(getManagedClusterViewCount(East1ManagedCluster)).Should(Equal(0)) // NS + VRG MCV
				deleteNamespaceMWsFromAllClusters(ApplicationNamespace)
				UseApplicationSet = false
			})
			It("should delete the DRPC causing its referenced drpolicy to be deleted"+
				" by drpolicy controller since no DRPCs reference it anymore", func() {
				ensureDRPolicyIsDeleted(drpc.Spec.DRPolicyRef.Name)
			})
		})
		Specify("delete drclusters when using Placement", func() {
			deleteDRClustersAsync()
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
				verifyDRPCOwnedByPlacement(userPlacementRule, getLatestDRPC(DefaultDRPCNamespace))
			})
		})
		When("DRAction changes to Failover", func() {
			It("Should not failover to Secondary (East2ManagedCluster) till PV manifest is applied", func() {
				By("\n\n*** Failover - 1\n\n")
				setRestorePVsUncomplete()
				fenceCluster(East1ManagedCluster, false)
				setDRPCSpecExpectationTo(DefaultDRPCNamespace, East1ManagedCluster, East2ManagedCluster, rmn.ActionFailover)
				verifyUserPlacementRuleDecisionUnchanged(userPlacementRule.Name, userPlacementRule.Namespace, East2ManagedCluster)
				// MWs for VRG, VRG DRCluster and the MW for NetworkFence CR to fence off
				// East1ManagedCluster
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
				deleteUserPlacementRule(UserPlacementRuleName, DefaultDRPCNamespace)
			})
		})
		When("Deleting DRPC", func() {
			It("Should delete VRG from Primary (East1ManagedCluster)", func() {
				By("\n\n*** DELETE DRPC ***\n\n")
				deleteDRPC()
				waitForCompletion("deleted")
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(2)) // DRCluster+NS MW
				deleteDRPolicySync()
				deleteDRClustersSync()
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
				verifyDRPCOwnedByPlacement(userPlacementRule, getLatestDRPC(DefaultDRPCNamespace))
			})
		})
		When("DRAction changes to Failover", func() {
			It("Should not failover to Secondary (East2ManagedCluster) till PV manifest is applied", func() {
				By("\n\n*** Failover - 1\n\n")
				setRestorePVsUncomplete()
				fenceCluster(East1ManagedCluster, true)
				setDRPCSpecExpectationTo(DefaultDRPCNamespace, East1ManagedCluster, East2ManagedCluster, rmn.ActionFailover)
				verifyUserPlacementRuleDecisionUnchanged(userPlacementRule.Name, userPlacementRule.Namespace, East2ManagedCluster)
				// MWs for VRG, VRG DRCluster and the MW for NetworkFence CR to fence off
				// East1ManagedCluster
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
				deleteUserPlacementRule(UserPlacementRuleName, DefaultDRPCNamespace)
			})
		})

		When("Deleting DRPC", func() {
			It("Should delete VRG from Primary (East1ManagedCluster)", func() {
				By("\n\n*** DELETE DRPC ***\n\n")
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(BeElementOf(3, 4)) // DRCluster + NS + VRG MW
				deleteDRPC()
				waitForCompletion("deleted")
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(2)) // DRCluster + NS MW
				deleteDRPolicySync()
				deleteDRClustersSync()
				deleteNamespaceMWsFromAllClusters(DefaultDRPCNamespace)
			})
		})
	})

	Context("DRPlacementControl Reconciler HubRecovery (Subscription)", func() {
		var userPlacementRule1 *plrv1.PlacementRule
		var drpc1 *rmn.DRPlacementControl

		Specify("DRClusters", func() {
			populateDRClusters()
		})
		When("Application deployed for the first time", func() {
			It("Should deploy drpc", func() {
				createNamespacesAsync(getNamespaceObj(DefaultDRPCNamespace))
				createManagedClusters(asyncClusters)
				createDRClustersAsync()
				createDRPolicyAsync()

				var placementObj client.Object
				placementObj, drpc1 = CreatePlacementAndDRPC(
					DefaultDRPCNamespace, UserPlacementRuleName, East1ManagedCluster, UsePlacementRule)
				userPlacementRule1 = placementObj.(*plrv1.PlacementRule)
				Expect(userPlacementRule1).NotTo(BeNil())
				waitForDRPCPhaseAndProgression(DefaultDRPCNamespace, rmn.Deployed)
				uploadVRGtoS3Store(DRPCCommonName, DefaultDRPCNamespace, East1ManagedCluster, rmn.VRGAction(""))
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
				clearFakeUserPlacementRuleStatus(UserPlacementRuleName, DefaultDRPCNamespace)
				clearDRPCStatus()
				expectedAction := rmn.DRAction("")
				expectedPhase := rmn.Deployed
				exptectedPorgression := rmn.ProgressionCompleted
				verifyDRPCStateAndProgression(expectedAction, expectedPhase, exptectedPorgression)
				resetClusterDown()
				exptectedCompleted := rmn.ProgressionCompleted
				verifyDRPCStateAndProgression(expectedAction, expectedPhase, exptectedCompleted)
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
				clearFakeUserPlacementRuleStatus(UserPlacementRuleName, DefaultDRPCNamespace)
				clearDRPCStatus()
				expectedAction := rmn.DRAction("")
				expectedPhase := rmn.WaitForUser
				exptectedPorgression := rmn.ProgressionActionPaused
				verifyDRPCStateAndProgression(expectedAction, expectedPhase, exptectedPorgression)
			})
		})

		// Failover
		When("HubRecovery: DRAction is set to Failover -> primary cluster down", func() {
			It("Should failover to West1ManagedCluster", func() {
				from := East1ManagedCluster
				to := West1ManagedCluster
				resetClusterDown()
				runFailoverAction(userPlacementRule1, from, to, false, false)
				waitForDRPCPhaseAndProgression(DefaultDRPCNamespace, rmn.FailedOver)
				uploadVRGtoS3Store(DRPCCommonName, DefaultDRPCNamespace, West1ManagedCluster, rmn.VRGActionFailover)
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
				clearFakeUserPlacementRuleStatus(UserPlacementRuleName, DefaultDRPCNamespace)
				clearDRPCStatus()
				setDRPCSpecExpectationTo(DefaultDRPCNamespace, East1ManagedCluster, West1ManagedCluster, "")
				expectedAction := rmn.DRAction("")
				expectedPhase := rmn.WaitForUser
				exptectedPorgression := rmn.ProgressionActionPaused
				verifyDRPCStateAndProgression(expectedAction, expectedPhase, exptectedPorgression)
				checkConditionAllowFailover(DefaultDRPCNamespace)

				// User intervention is required (simulate user intervention)
				resetClusterDown()
				setDRPCSpecExpectationTo(DefaultDRPCNamespace, East1ManagedCluster, West1ManagedCluster, rmn.ActionFailover)
				expectedAction = rmn.ActionFailover
				expectedPhase = rmn.FailedOver
				exptectedPorgression = rmn.ProgressionCompleted
				verifyDRPCStateAndProgression(expectedAction, expectedPhase, exptectedPorgression)
				waitForCompletion(string(rmn.FailedOver))
			})
		})

		// Relocate
		When("HubRecovery: DRAction is set to Relocate", func() {
			It("Should relocate to Primary (East1ManagedCluster)", func() {
				// ----------------------------- RELOCATION TO PRIMARY --------------------------------------
				from := West1ManagedCluster
				runRelocateAction(userPlacementRule1, from, false, false)
				uploadVRGtoS3Store(DRPCCommonName, DefaultDRPCNamespace, East1ManagedCluster, rmn.VRGAction(rmn.ActionRelocate))
				waitForDRPCPhaseAndProgression(DefaultDRPCNamespace, rmn.Relocated)
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
				clearFakeUserPlacementRuleStatus(UserPlacementRuleName, DefaultDRPCNamespace)
				clearDRPCStatus()
				expectedAction := rmn.ActionRelocate
				expectedPhase := rmn.Relocated
				exptectedPorgression := rmn.ProgressionCleaningUp
				verifyDRPCStateAndProgression(expectedAction, expectedPhase, exptectedPorgression)

				// User intervention is required (simulate user intervention)
				resetClusterDown()
				setDRPCSpecExpectationTo(DefaultDRPCNamespace, East1ManagedCluster, West1ManagedCluster, rmn.ActionRelocate)
				expectedAction = rmn.ActionRelocate
				expectedPhase = rmn.Relocated
				exptectedPorgression = rmn.ProgressionCompleted
				verifyDRPCStateAndProgression(expectedAction, expectedPhase, exptectedPorgression)
				waitForCompletion(string(rmn.Relocated))
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
				clearFakeUserPlacementRuleStatus(UserPlacementRuleName, DefaultDRPCNamespace)
				clearDRPCStatus()
				setDRPCSpecExpectationTo(DefaultDRPCNamespace, East1ManagedCluster, West1ManagedCluster, "")
				expectedAction := rmn.DRAction("")
				expectedPhase := rmn.WaitForUser
				exptectedPorgression := rmn.ProgressionActionPaused
				verifyDRPCStateAndProgression(expectedAction, expectedPhase, exptectedPorgression)
				checkConditionAllowFailover(DefaultDRPCNamespace)

				// User intervention is required (simulate user intervention)
				resetClusterDown()
				setDRPCSpecExpectationTo(DefaultDRPCNamespace, East1ManagedCluster, West1ManagedCluster, rmn.ActionRelocate)
				expectedAction = rmn.ActionRelocate
				expectedPhase = rmn.Relocated
				exptectedPorgression = rmn.ProgressionCompleted
				verifyDRPCStateAndProgression(expectedAction, expectedPhase, exptectedPorgression)
				waitForCompletion(string(rmn.Relocated))
			})
		})

		When("Deleting DRPolicy with DRPC references", func() {
			It("Should retain the deleted DRPolicy in the API server", func() {
				// ----------------------------- DELETE DRPolicy  --------------------------------------
				By("\n\n*** DELETE drpolicy ***\n\n")
				deleteDRPolicyAsync()
			})
		})
		When("Deleting user PlacementRule", func() {
			It("Should cleanup DRPC", func() {
				// ----------------------------- DELETE DRPC from PRIMARY --------------------------------------
				By("\n\n*** DELETE User PlacementRule ***\n\n")
				deleteUserPlacementRule(UserPlacementRuleName, DefaultDRPCNamespace)
			})
		})

		When("Deleting DRPC", func() {
			It("Should delete all VRGs", func() {
				Expect(k8sClient.Delete(context.TODO(), drpc1)).Should(Succeed())
				deleteNamespaceMWsFromAllClusters(DefaultDRPCNamespace)
			})
		})
		Specify("delete drclusters", func() {
			deleteDRClustersAsync()
		})
	})

	Context("DRPlacementControl Reconciler HubRecovery VRG Adoption (Subscription)", func() {
		var userPlacementRule1 *plrv1.PlacementRule
		var drpc1 *rmn.DRPlacementControl

		Specify("DRClusters", func() {
			populateDRClusters()
		})

		When("Application deployed for the first time", func() {
			It("Should deploy drpc", func() {
				createNamespacesAsync(getNamespaceObj(DefaultDRPCNamespace))
				createManagedClusters(asyncClusters)
				createDRClustersAsync()
				createDRPolicyAsync()
				setToggleUIDChecks()

				// Create an existing VRG MW on East, to simulate upgrade cases (West1 will report an
				// orphan VRG for orphan cases)
				createVRGMW(DRPCCommonName, DefaultDRPCNamespace, East1ManagedCluster)

				var placementObj client.Object
				placementObj, drpc1 = CreatePlacementAndDRPC(
					DefaultDRPCNamespace, UserPlacementRuleName, East1ManagedCluster, UsePlacementRule)
				userPlacementRule1 = placementObj.(*plrv1.PlacementRule)
				Expect(userPlacementRule1).NotTo(BeNil())
				waitForDRPCPhaseAndProgression(DefaultDRPCNamespace, rmn.Deployed)
				uploadVRGtoS3Store(DRPCCommonName, DefaultDRPCNamespace, East1ManagedCluster, rmn.VRGAction(""))
				resetToggleUIDChecks()
			})
		})

		When("Deleting DRPolicy with DRPC references", func() {
			It("Should retain the deleted DRPolicy in the API server", func() {
				By("\n\n*** DELETE drpolicy ***\n\n")
				deleteDRPolicyAsync()
			})
		})

		When("Deleting user PlacementRule", func() {
			It("Should cleanup DRPC", func() {
				By("\n\n*** DELETE User PlacementRule ***\n\n")
				deleteUserPlacementRule(UserPlacementRuleName, DefaultDRPCNamespace)
			})
		})

		When("Deleting DRPC", func() {
			It("Should delete all VRGs", func() {
				Expect(k8sClient.Delete(context.TODO(), drpc1)).Should(Succeed())
				deleteNamespaceMWsFromAllClusters(DefaultDRPCNamespace)
			})
		})

		Specify("delete drclusters", func() {
			deleteDRClustersAsync()
		})
	})
})

func verifyDRPCStateAndProgression(expectedAction rmn.DRAction, expectedPhase rmn.DRState,
	exptectedPorgression rmn.ProgressionStatus,
) {
	var phase rmn.DRState

	var progression rmn.ProgressionStatus

	Eventually(func() bool {
		drpc := getLatestDRPC(DefaultDRPCNamespace)
		phase = drpc.Status.Phase
		progression = drpc.Status.Progression

		return phase == expectedPhase && progression == exptectedPorgression
	}, timeout, interval).Should(BeTrue(),
		fmt.Sprintf("Phase has not been updated yet! Phase:%s Expected:%s - progression:%s exptected:%s",
			phase, expectedPhase, progression, exptectedPorgression))

	drpc := getLatestDRPC(DefaultDRPCNamespace)
	Expect(drpc.Spec.Action).Should(Equal(expectedAction))
	Expect(drpc.Status.Phase).Should(Equal(expectedPhase))
	Expect(drpc.Status.Progression).Should(Equal(exptectedPorgression))
}

func checkConditionAllowFailover(namespace string) {
	var drpc *rmn.DRPlacementControl

	var availableCondition metav1.Condition

	Eventually(func() bool {
		drpc = getLatestDRPC(namespace)
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
func uploadVRGtoS3Store(name, namespace, dstCluster string, action rmn.VRGAction) {
	vrg := buildVRG(name, namespace, dstCluster, action)
	s3ProfileNames := []string{s3Profiles[0].S3ProfileName, s3Profiles[1].S3ProfileName}

	objectStorer, _, err := drpcReconciler.ObjStoreGetter.ObjectStore(
		ctx, apiReader, s3ProfileNames[0], "Hub Recovery", testLogger)

	Expect(err).ToNot(HaveOccurred())
	Expect(controllers.VrgObjectProtect(objectStorer, vrg)).To(Succeed())
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
		if err != nil && !errors.IsNotFound(err) {
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
		if err != nil && !errors.IsNotFound(err) {
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
		if err != nil && !errors.IsNotFound(err) {
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
		if err != nil && !errors.IsNotFound(err) {
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
		if err != nil && !errors.IsNotFound(err) {
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
		if err != nil && !errors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

func removeRamenFinalizersFromObject(obj client.Object) {
	ramenFinalizers := []string{
		controllers.DRPCFinalizer,
		rmnutil.SecretPolicyFinalizer,
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
		if err != nil && !errors.IsNotFound(err) {
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
		if err != nil && !errors.IsNotFound(err) {
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
		if err != nil && !errors.IsNotFound(err) {
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
