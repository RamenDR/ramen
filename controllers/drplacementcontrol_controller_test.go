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

	spokeClusterV1 "github.com/open-cluster-management/api/cluster/v1"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	viewv1beta1 "github.com/stolostron/multicloud-operators-foundation/pkg/apis/view/v1beta1"

	argov1alpha1 "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	clrapiv1beta1 "github.com/open-cluster-management-io/api/cluster/v1beta1"
	rmn "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers"
	rmnutil "github.com/ramendr/ramen/controllers/util"
	plrv1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
)

const (
	DRPCName              = "drpc-name"
	DRPCNamespaceName     = "drpc-namespace"
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
		ObjectMeta: metav1.ObjectMeta{Name: DRPCNamespaceName},
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

	appSet = argov1alpha1.ApplicationSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "simple-appset",
			Namespace: DRPCNamespaceName,
		},
		Spec: argov1alpha1.ApplicationSetSpec{
			Template: argov1alpha1.ApplicationSetTemplate{
				ApplicationSetTemplateMeta: argov1alpha1.ApplicationSetTemplateMeta{Name: "{{cluster}}-guestbook"},
				Spec: argov1alpha1.ApplicationSpec{
					Project: "default",
					Source: &argov1alpha1.ApplicationSource{
						RepoURL:        "https://github.com/argoproj/argocd-example-apps.git",
						TargetRevision: "HEAD",
						Path:           "guestbook",
					},
					Destination: argov1alpha1.ApplicationDestination{
						Server:    "{{url}}",
						Namespace: ApplicationNamespace,
					},
				},
			},
			Generators: []argov1alpha1.ApplicationSetGenerator{
				{
					ClusterDecisionResource: &argov1alpha1.DuckTypeGenerator{
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
func FakeProgressCallback(drpcName string, state string) {
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

var baseVRG = &rmn.VolumeReplicationGroup{
	TypeMeta:   metav1.TypeMeta{Kind: "VolumeReplicationGroup", APIVersion: "ramendr.openshift.io/v1alpha1"},
	ObjectMeta: metav1.ObjectMeta{Name: DRPCName, Namespace: DRPCNamespaceName},
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
	vrg := baseVRG.DeepCopy()
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

	case "ensureVRGIsSecondaryOnCluster":
		return moveVRGToSecondary(managedCluster, "vrg", false)

	case "ensureDataProtectedOnCluster":
		return moveVRGToSecondary(managedCluster, "vrg", true)

	case "ensureVRGDeleted":
		return nil, errors.NewNotFound(schema.GroupResource{}, "requested resource not found in ManagedCluster")

	case "getVRGsFromManagedClusters":
		vrgFromMW, err := getVRGFromManifestWork(managedCluster)
		if err != nil {
			if errors.IsNotFound(err) {
				if getFunctionNameAtIndex(3) == "getVRGs" { // Called only from DRCluster reconciler, at present
					return fakeVRGWithMModesProtectedPVC()
				}
			}

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

			protectedPVC := &rmn.ProtectedPVC{}
			protectedPVC.Name = "random name"
			protectedPVC.StorageIdentifiers.ReplicationID.ID = MModeReplicationID
			protectedPVC.StorageIdentifiers.StorageProvisioner = MModeCSIProvisioner
			protectedPVC.StorageIdentifiers.ReplicationID.Modes = []rmn.MMode{rmn.MModeFailover}

			vrgFromMW.Status.ProtectedPVCs = append(vrgFromMW.Status.ProtectedPVCs, *protectedPVC)
		}

		return vrgFromMW, nil
	}

	return nil, fmt.Errorf("unknown caller %s", getFunctionNameAtIndex(2))
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

func getVRGNameSpace() string {
	if UseApplicationSet {
		return ApplicationNamespace
	}

	return DRPCNamespaceName
}

func getVRGFromManifestWork(managedCluster string) (*rmn.VolumeReplicationGroup, error) {
	manifestLookupKey := types.NamespacedName{
		Name:      rmnutil.ManifestWorkName(DRPCName, getVRGNameSpace(), "vrg"),
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

func fakeVRGWithMModesProtectedPVC() (*rmn.VolumeReplicationGroup, error) {
	vrg := baseVRG.DeepCopy()
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

func createDRPC(placementName, name, namespace, drPolicyName string) *rmn.DRPlacementControl {
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
			PreferredCluster:     East1ManagedCluster,
		},
	}
	Expect(k8sClient.Create(context.TODO(), drpc)).Should(Succeed())

	return drpc
}

func deleteUserPlacementRule() {
	userPlacementRule := getLatestUserPlacementRule(UserPlacementRuleName, DRPCNamespaceName)
	Expect(k8sClient.Delete(context.TODO(), userPlacementRule)).Should(Succeed())
}

func deleteUserPlacement() {
	userPlacement := getLatestUserPlacement(UserPlacementName, DRPCNamespaceName)
	Expect(k8sClient.Delete(context.TODO(), userPlacement)).Should(Succeed())
}

func deleteDRPC() {
	drpc := getLatestDRPC()
	Expect(k8sClient.Delete(context.TODO(), drpc)).Should(Succeed())
}

func deleteNamespaceMWsFromAllClusters(namespace string) {
	foundMW := &ocmworkv1.ManifestWork{}
	mwName := fmt.Sprintf(rmnutil.ManifestWorkNameFormat, DRPCName, namespace, rmnutil.MWTypeNS)
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

func setDRPCSpecExpectationTo(action rmn.DRAction, preferredCluster, failoverCluster string) {
	drpcLookupKey := types.NamespacedName{
		Name:      DRPCName,
		Namespace: DRPCNamespaceName,
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
		latestDRPC = getLatestDRPC()

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
	drpcLookupKey := types.NamespacedName{
		Name:      DRPCName,
		Namespace: DRPCNamespaceName,
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

func clearFakeUserPlacementRuleStatus() {
	usrPlRuleLookupKey := types.NamespacedName{
		Name:      UserPlacementRuleName,
		Namespace: DRPCNamespaceName,
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

func moveVRGToSecondary(clusterNamespace, mwType string, protectData bool) (*rmn.VolumeReplicationGroup, error) {
	manifestLookupKey := types.NamespacedName{
		Name:      rmnutil.ManifestWorkName(DRPCName, getVRGNameSpace(), mwType),
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
		Name:      rmnutil.ManifestWorkName(DRPCName, getVRGNameSpace(), mwType),
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

func waitForVRGMWDeletion(clusterNamespace string) {
	manifestLookupKey := types.NamespacedName{
		Name:      rmnutil.ManifestWorkName(DRPCName, getVRGNameSpace(), "vrg"),
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
	createNamespacesAsync(getNamespaceObj(DRPCNamespaceName))

	createManagedClusters(asyncClusters)
	createDRClustersAsync()
	createDRPolicyAsync()

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

	return placementObj, createDRPC(placementName, DRPCName, DRPCNamespaceName, AsyncDRPolicyName)
}

func FollowOnDeploymentAsync(namespace, placementName, homeCluster string) (*plrv1.PlacementRule,
	*rmn.DRPlacementControl,
) {
	createNamespace(appNamespace2)

	placementRule := createPlacementRule(placementName, namespace)
	drpc := createDRPC(placementName, DRPCName, namespace, AsyncDRPolicyName)

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
		Name:      rmnutil.ManifestWorkName(DRPCName, getVRGNameSpace(), "vrg"),
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
	Expect(vrg.Name).Should(Equal(DRPCName))
	Expect(vrg.Spec.PVCSelector.MatchLabels["appclass"]).Should(Equal("gold"))
	Expect(vrg.Spec.ReplicationState).Should(Equal(rmn.Primary))

	// ensure DRPC copied KubeObjectProtection contents to VRG
	drpc := getLatestDRPC()
	Expect(vrg.Spec.KubeObjectProtection).Should(Equal(drpc.Spec.KubeObjectProtection))
}

func getManifestWorkCount(homeClusterNamespace string) int {
	manifestWorkList := &ocmworkv1.ManifestWorkList{}
	listOptions := &client.ListOptions{Namespace: homeClusterNamespace}

	Expect(apiReader.List(context.TODO(), manifestWorkList, listOptions)).NotTo(HaveOccurred())

	return len(manifestWorkList.Items)
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

	Expect(placementObj.GetAnnotations()[controllers.DRPCNameAnnotation]).Should(Equal(DRPCName))
	Expect(placementObj.GetAnnotations()[controllers.DRPCNamespaceAnnotation]).Should(Equal(DRPCNamespaceName))
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

	Consistently(func() bool {
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

	Expect(placementObj.GetAnnotations()[controllers.DRPCNameAnnotation]).Should(Equal(DRPCName))
	Expect(placementObj.GetAnnotations()[controllers.DRPCNamespaceAnnotation]).Should(Equal(DRPCNamespaceName))
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

func waitForUpdateDRPCStatus() {
	Eventually(func() bool {
		drpc := getLatestDRPC()

		for _, condition := range drpc.Status.Conditions {
			if condition.ObservedGeneration != drpc.Generation {
				return false
			}

			if condition.Status != metav1.ConditionTrue {
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
		Expect(getManifestWorkCount(toCluster)).Should(Equal(3))   // MW for VRG+DRCluster+NS
		Expect(getManifestWorkCount(fromCluster)).Should(Equal(2)) // DRCluster + NS MW
	} else {
		Expect(getManifestWorkCount(toCluster)).Should(Equal(4))   // MW for VRG+DRCluster + NS + NF
		Expect(getManifestWorkCount(fromCluster)).Should(Equal(2)) // NS + DRCluster MW
	}

	drpc := getLatestDRPC()
	// At this point expect the DRPC status condition to have 2 types
	// {Available and PeerReady}
	// Final state is 'FailedOver'
	Expect(drpc.Status.Phase).To(Equal(rmn.FailedOver))
	Expect(len(drpc.Status.Conditions)).To(Equal(2))
	_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionAvailable)
	Expect(condition.Reason).To(Equal(string(rmn.FailedOver)))

	decision := getLatestUserPlacementDecision(placementObj.GetName(), placementObj.GetNamespace())
	Expect(decision.ClusterName).To(Equal(toCluster))
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

	decision := getLatestUserPlacementDecision(userPlacementRule.Name, userPlacementRule.Namespace)
	Expect(decision.ClusterName).To(Equal(failoverCluster))
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

	drpc := getLatestDRPC()
	// At this point expect the DRPC status condition to have 2 types
	// {Available and PeerReady}
	// Final state is 'Relocated'
	Expect(drpc.Status.Phase).To(Equal(rmn.Relocated))
	Expect(len(drpc.Status.Conditions)).To(Equal(2))
	_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionAvailable)
	Expect(condition.Reason).To(Equal(string(rmn.Relocated)))

	decision := getLatestUserPlacementDecision(placementObj.GetName(), placementObj.GetNamespace())
	Expect(decision.ClusterName).To(Equal(toCluster1))
	Expect(condition.Reason).To(Equal(string(rmn.Relocated)))
	Expect(drpc.GetAnnotations()[controllers.LastAppDeploymentCluster]).To(Equal(toCluster1))
}

func clearDRActionAfterRelocate(userPlacementRule *plrv1.PlacementRule, preferredCluster, failoverCluster string) {
	setDRPCSpecExpectationTo("", preferredCluster, failoverCluster)
	waitForCompletion(string(rmn.Deployed))

	drpc := getLatestDRPC()
	// At this point expect the DRPC status condition to have 2 types
	// {Available and PeerReady}
	// Final state didn't change and it is 'Relocated' even though we tried to run
	// initial deployment
	Expect(drpc.Status.Phase).To(Equal(rmn.Deployed))
	Expect(len(drpc.Status.Conditions)).To(Equal(2))
	_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionAvailable)
	Expect(condition.Reason).To(Equal(string(rmn.Deployed)))

	decision := getLatestUserPlacementDecision(userPlacementRule.Name, userPlacementRule.Namespace)
	Expect(decision.ClusterName).To(Equal(preferredCluster))
}

func relocateToPreferredCluster(placementObj client.Object, fromCluster string) {
	toCluster1 := "east1-cluster"

	setDRPCSpecExpectationTo(rmn.ActionRelocate, toCluster1, fromCluster)

	updateManifestWorkStatus(toCluster1, "vrg", ocmworkv1.WorkApplied)

	verifyUserPlacementRuleDecision(placementObj.GetName(), placementObj.GetNamespace(), toCluster1)
	verifyDRPCStatusPreferredClusterExpectation(rmn.Relocated)
	verifyVRGManifestWorkCreatedAsPrimary(toCluster1)

	waitForVRGMWDeletion(West1ManagedCluster)

	waitForCompletion(string(rmn.Relocated))
}

func recoverToFailoverCluster(placementObj client.Object, fromCluster, toCluster string) {
	setDRPCSpecExpectationTo(rmn.ActionFailover, fromCluster, toCluster)

	updateManifestWorkStatus(toCluster, "vrg", ocmworkv1.WorkApplied)

	verifyUserPlacementRuleDecision(placementObj.GetName(), placementObj.GetNamespace(), toCluster)
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
	*rmn.DRPlacementControl,
) {
	createNamespacesSync()

	createManagedClusters(syncClusters)
	createDRClustersSync()
	createDRPolicySync()

	placementRule := createPlacementRule(placementName, namespace)
	drpc := createDRPC(UserPlacementRuleName, DRPCName, DRPCNamespaceName, SyncDRPolicyName)

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
	verifyVRGManifestWorkCreatedAsPrimary(preferredCluster)
	updateManifestWorkStatus(preferredCluster, "vrg", ocmworkv1.WorkApplied)
	verifyUserPlacementRuleDecision(userPlacement.GetName(), userPlacement.GetNamespace(), preferredCluster)
	verifyDRPCStatusPreferredClusterExpectation(rmn.Deployed)
	Expect(getManifestWorkCount(preferredCluster)).Should(Equal(3)) // MWs for VRG, 2 namespaces, and DRCluster
	waitForCompletion(string(rmn.Deployed))

	latestDRPC := getLatestDRPC()
	// At this point expect the DRPC status condition to have 2 types
	// {Available and PeerReady}
	// Final state is 'Deployed'
	Expect(latestDRPC.Status.Phase).To(Equal(rmn.Deployed))
	Expect(len(latestDRPC.Status.Conditions)).To(Equal(2))
	_, condition := getDRPCCondition(&latestDRPC.Status, rmn.ConditionAvailable)
	Expect(condition.Reason).To(Equal(string(rmn.Deployed)))
	Expect(latestDRPC.GetAnnotations()[controllers.LastAppDeploymentCluster]).To(Equal(preferredCluster))
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
		Eventually(getManifestWorkCount, timeout, interval).WithArguments(toCluster).Should(Equal(3))
	} else {
		Expect(getManifestWorkCount(toCluster)).Should(Equal(4)) // MW for VRG+NS+DRCluster+NF
	}

	Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(2)) // DRClustern+NS

	drpc := getLatestDRPC()
	// At this point expect the DRPC status condition to have 2 types
	// {Available and PeerReady}
	// Final state is 'FailedOver'
	Expect(drpc.Status.Phase).To(Equal(rmn.FailedOver))
	Expect(len(drpc.Status.Conditions)).To(Equal(2))
	_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionAvailable)
	Expect(condition.Reason).To(Equal(string(rmn.FailedOver)))

	decision := getLatestUserPlacementDecision(placementObj.GetName(), placementObj.GetNamespace())
	Expect(decision.ClusterName).To(Equal(toCluster))
	Expect(drpc.GetAnnotations()[controllers.LastAppDeploymentCluster]).To(Equal(toCluster))
}

func verifyActionResultForPlacement(placement *clrapiv1beta1.Placement, homeCluster string, plType PlacementType) {
	placementDecision := getPlacementDecision(placement.GetName(), placement.GetNamespace())
	Expect(placementDecision).ShouldNot(BeNil())
	Expect(placementDecision.Status.Decisions[0].ClusterName).Should(Equal(homeCluster))
	vrg, err := getVRGFromManifestWork(homeCluster)
	Expect(err).NotTo(HaveOccurred())

	switch plType {
	case UsePlacementWithSubscription:
		Expect(vrg.Namespace).Should(Equal(DRPCNamespaceName))
	case UsePlacementWithAppSet:
		Expect(vrg.Namespace).Should(Equal(appSet.Spec.Template.Spec.Destination.Namespace))
	default:
		Fail("Wrong placement type")
	}
}

func buildVRG(namespaceName, objectName string) rmn.VolumeReplicationGroup {
	return rmn.VolumeReplicationGroup{
		TypeMeta: metav1.TypeMeta{
			APIVersion: rmn.GroupVersion.String(),
			Kind:       "VolumeReplicationGroup",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespaceName,
			Name:      objectName,
		},
		Spec: rmn.VolumeReplicationGroupSpec{
			PVCSelector:      metav1.LabelSelector{},
			ReplicationState: rmn.Primary,
			S3Profiles:       []string{},
			Sync:             &rmn.VRGSyncSpec{},
		},
	}
}

func ensureLatestVRGDownloadedFromS3Stores() {
	orgVRG := buildVRG("vrgNamespace1", "vrgName1")
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

// +kubebuilder:docs-gen:collapse=Imports
//
//nolint:errcheck
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
					DRPCNamespaceName, UserPlacementRuleName, East1ManagedCluster, UsePlacementRule)
				userPlacementRule = placementObj.(*plrv1.PlacementRule)
				Expect(userPlacementRule).NotTo(BeNil())
				verifyInitialDRPCDeployment(userPlacementRule, East1ManagedCluster)
				verifyDRPCOwnedByPlacement(userPlacementRule, getLatestDRPC())
			})
		})
		When("DRAction changes to Failover", func() {
			It("Should not failover to Secondary (West1ManagedCluster) till PV manifest is applied", func() {
				By("\n\n*** Failover - 1\n\n")
				setRestorePVsUncomplete()
				setDRPCSpecExpectationTo(rmn.ActionFailover, East1ManagedCluster, West1ManagedCluster)
				verifyUserPlacementRuleDecisionUnchanged(userPlacementRule.Name, userPlacementRule.Namespace, East1ManagedCluster)
				// MWs for VRG, NS, DRCluster, and MMode
				Eventually(getManifestWorkCount, timeout, interval).WithArguments(West1ManagedCluster).Should(Equal(4))
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
				clearFakeUserPlacementRuleStatus()
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
		When("DRAction is Relocate during hub recovery", func() {
			It("Should reconstructs the DRPC state and points to Primary (East1ManagedCluster)", func() {
				// ----------------------------- RELOCATION TO PRIMARY --------------------------------------
				By("\n\n*** Relocate - 2\n\n")
				clearFakeUserPlacementRuleStatus()
				clearDRPCStatus()
				runRelocateAction(userPlacementRule, West1ManagedCluster, false, false)
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
				runFailoverAction(userPlacementRule, East1ManagedCluster, West1ManagedCluster, false, false)
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
				decision := getLatestUserPlacementDecision(userPlacementRule.Name, userPlacementRule.Namespace)
				Expect(decision.ClusterName).To(Equal(West1ManagedCluster))
				Expect(drpc.GetAnnotations()[controllers.LastAppDeploymentCluster]).To(Equal(West1ManagedCluster))
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
				deleteUserPlacementRule()
			})
		})

		When("Deleting DRPC", func() {
			It("Should delete VRG and NS MWs and MCVs from Primary (East1ManagedCluster)", func() {
				// ----------------------------- DELETE DRPC from PRIMARY --------------------------------------
				By("\n\n*** DELETE DRPC ***\n\n")
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(3)) // DRCluster + VRG MW
				deleteDRPC()
				waitForCompletion("deleted")
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(2))       // DRCluster + NS MW only
				Expect(getManagedClusterViewCount(East1ManagedCluster)).Should(Equal(0)) // NS + VRG MCV
				deleteNamespaceMWsFromAllClusters(DRPCNamespaceName)
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
					DRPCNamespaceName, UserPlacementName, East1ManagedCluster, UsePlacementWithSubscription)
				placement = placementObj.(*clrapiv1beta1.Placement)
				Expect(placement).NotTo(BeNil())
				verifyInitialDRPCDeployment(placement, East1ManagedCluster)
				verifyActionResultForPlacement(placement, East1ManagedCluster, UsePlacementWithSubscription)
				verifyDRPCOwnedByPlacement(placement, getLatestDRPC())
			})
		})
		When("DRAction changes to Failover using Placement with Subscription", func() {
			It("Should not failover to Secondary (West1ManagedCluster) till PV manifest is applied", func() {
				setRestorePVsUncomplete()
				setDRPCSpecExpectationTo(rmn.ActionFailover, East1ManagedCluster, West1ManagedCluster)
				verifyUserPlacementRuleDecisionUnchanged(placement.Name, placement.Namespace, East1ManagedCluster)
				// MWs for VRG, NS, VRG DRCluster, and MMode
				Expect(getManifestWorkCount(West1ManagedCluster)).Should(Equal(4))
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
				deleteUserPlacement()
				drpc := getLatestDRPC()
				_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionPeerReady)
				Expect(condition).NotTo(BeNil())
			})
		})
		When("Deleting DRPC when using Placement", func() {
			It("Should delete VRG and NS MWs and MCVs from Primary (East1ManagedCluster)", func() {
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(3)) // DRCluster + VRG + NS MW
				deleteDRPC()
				waitForCompletion("deleted")
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(2))       // DRCluster + NS MW only
				Expect(getManagedClusterViewCount(East1ManagedCluster)).Should(Equal(0)) // NS + VRG MCV
				deleteNamespaceMWsFromAllClusters(DRPCNamespaceName)
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
				baseVRG.ObjectMeta.Namespace = ApplicationNamespace
				var placementObj client.Object
				placementObj, drpc = InitialDeploymentAsync(
					DRPCNamespaceName, UserPlacementName, East1ManagedCluster, UsePlacementWithAppSet)
				placement = placementObj.(*clrapiv1beta1.Placement)
				Expect(placement).NotTo(BeNil())
				verifyInitialDRPCDeployment(placement, East1ManagedCluster)
				verifyActionResultForPlacement(placement, East1ManagedCluster, UsePlacementWithAppSet)
				verifyDRPCOwnedByPlacement(placement, getLatestDRPC())
			})
		})
		When("DRAction changes to Failover using Placement", func() {
			It("Should not failover to Secondary (West1ManagedCluster) till PV manifest is applied", func() {
				setRestorePVsUncomplete()
				setDRPCSpecExpectationTo(rmn.ActionFailover, East1ManagedCluster, West1ManagedCluster)
				verifyUserPlacementRuleDecisionUnchanged(placement.Name, placement.Namespace, East1ManagedCluster)
				// MWs for VRG, NS, VRG DRCluster, and MMode
				Eventually(getManifestWorkCount, timeout, interval).WithArguments(West1ManagedCluster).Should(Equal(4))
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
				deleteUserPlacement()
				drpc := getLatestDRPC()
				_, condition := getDRPCCondition(&drpc.Status, rmn.ConditionPeerReady)
				Expect(condition).NotTo(BeNil())
			})
		})
		When("Deleting DRPC when using Placement", func() {
			It("Should delete VRG and NS MWs and MCVs from Primary (East1ManagedCluster)", func() {
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(3)) // DRCluster + VRG + NS MW
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
		drpc := &rmn.DRPlacementControl{}
		Specify("DRClusters", func() {
			populateDRClusters()
		})
		When("An Application is deployed for the first time", func() {
			It("Should deploy to East1ManagedCluster", func() {
				By("Initial Deployment")
				userPlacementRule, drpc = InitialDeploymentSync(DRPCNamespaceName, UserPlacementRuleName, East1ManagedCluster)
				verifyInitialDRPCDeployment(userPlacementRule, East1ManagedCluster)
				verifyDRPCOwnedByPlacement(userPlacementRule, getLatestDRPC())
			})
		})
		When("DRAction changes to Failover", func() {
			It("Should not failover to Secondary (East2ManagedCluster) till PV manifest is applied", func() {
				By("\n\n*** Failover - 1\n\n")
				setRestorePVsUncomplete()
				fenceCluster(East1ManagedCluster, false)
				setDRPCSpecExpectationTo(rmn.ActionFailover, East1ManagedCluster, East2ManagedCluster)
				verifyUserPlacementRuleDecisionUnchanged(userPlacementRule.Name, userPlacementRule.Namespace, East1ManagedCluster)
				// MWs for VRG, VRG DRCluster and the MW for NetworkFence CR to fence off
				// East1ManagedCluster
				Expect(getManifestWorkCount(East2ManagedCluster)).Should(Equal(4))
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
				decision := getLatestUserPlacementDecision(userPlacementRule.Name, userPlacementRule.Namespace)
				Expect(decision.ClusterName).To(Equal(East2ManagedCluster))
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
				deleteUserPlacementRule()
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
		drpc = &rmn.DRPlacementControl{}
		Specify("DRClusters", func() {
			populateDRClusters()
		})
		When("An Application is deployed for the first time", func() {
			It("Should deploy to East1ManagedCluster", func() {
				By("Initial Deployment")
				userPlacementRule, drpc = InitialDeploymentSync(DRPCNamespaceName, UserPlacementRuleName, East1ManagedCluster)
				verifyInitialDRPCDeployment(userPlacementRule, East1ManagedCluster)
				verifyDRPCOwnedByPlacement(userPlacementRule, getLatestDRPC())
			})
		})
		When("DRAction changes to Failover", func() {
			It("Should not failover to Secondary (East2ManagedCluster) till PV manifest is applied", func() {
				By("\n\n*** Failover - 1\n\n")
				setRestorePVsUncomplete()
				fenceCluster(East1ManagedCluster, true)
				setDRPCSpecExpectationTo(rmn.ActionFailover, East1ManagedCluster, East2ManagedCluster)
				verifyUserPlacementRuleDecisionUnchanged(userPlacementRule.Name, userPlacementRule.Namespace, East1ManagedCluster)
				// MWs for VRG, VRG DRCluster and the MW for NetworkFence CR to fence off
				// East1ManagedCluster
				Expect(getManifestWorkCount(East2ManagedCluster)).Should(Equal(4))
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
				runFailoverAction(userPlacementRule, East1ManagedCluster, East2ManagedCluster, true, true)
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
				decision := getLatestUserPlacementDecision(userPlacementRule.Name, userPlacementRule.Namespace)
				Expect(decision.ClusterName).To(Equal(East2ManagedCluster))
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
				deleteUserPlacementRule()
			})
		})

		When("Deleting DRPC", func() {
			It("Should delete VRG from Primary (East1ManagedCluster)", func() {
				By("\n\n*** DELETE DRPC ***\n\n")
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(3)) // DRCluster + NS + VRG MW
				deleteDRPC()
				waitForCompletion("deleted")
				Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(2)) // DRCluster + NS MW
				deleteDRPolicySync()
				deleteDRClustersSync()
				deleteNamespaceMWsFromAllClusters(DRPCNamespaceName)
			})
		})
	})
})
