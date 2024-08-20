// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/go-logr/logr"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	errorswrapper "github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	csiaddonsv1alpha1 "github.com/csi-addons/kubernetes-csi-addons/api/csiaddons/v1alpha1"
	rmn "github.com/ramendr/ramen/api/v1alpha1"
)

const (
	DrClusterManifestWorkName = "ramen-dr-cluster"

	// ManifestWorkNameFormat is a formated a string used to generate the manifest name
	// The format is name-namespace-type-mw where:
	// - name is the DRPC name
	// - namespace is the DRPC namespace
	// - type is "vrg"
	ManifestWorkNameFormat             string = "%s-%s-%s-mw"
	ManifestWorkNameFormatClusterScope string = "%s-%s-mw"
	ManifestWorkNameTypeFormat         string = "%s-mw"

	// ManifestWork Types
	MWTypeVRG       string = "vrg"
	MWTypeNS        string = "ns"
	MWTypeNF        string = "nf"
	MWTypeMMode     string = "mmode"
	MWTypeDRCConfig string = "drcconfig"
)

type MWUtil struct {
	client.Client
	APIReader       client.Reader
	Ctx             context.Context
	Log             logr.Logger
	InstName        string
	TargetNamespace string
}

func ManifestWorkName(name, namespace, mwType string) string {
	return fmt.Sprintf(ManifestWorkNameFormat, name, namespace, mwType)
}

func (mwu *MWUtil) BuildManifestWorkName(mwType string) string {
	if mwType == MWTypeDRCConfig {
		return fmt.Sprintf(ManifestWorkNameTypeFormat, MWTypeDRCConfig)
	}

	return ManifestWorkName(mwu.InstName, mwu.TargetNamespace, mwType)
}

func (mwu *MWUtil) FindManifestWorkByType(mwType, managedCluster string) (*ocmworkv1.ManifestWork, error) {
	mwName := mwu.BuildManifestWorkName(mwType)

	return mwu.FindManifestWork(mwName, managedCluster)
}

func (mwu *MWUtil) FindManifestWork(mwName, managedCluster string) (*ocmworkv1.ManifestWork, error) {
	if managedCluster == "" {
		return nil, fmt.Errorf("invalid cluster for MW %s", mwName)
	}

	mw := &ocmworkv1.ManifestWork{}

	err := mwu.Client.Get(mwu.Ctx, types.NamespacedName{Name: mwName, Namespace: managedCluster}, mw)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, fmt.Errorf("%w", err)
		}

		return nil, fmt.Errorf("failed to retrieve manifestwork (%w)", err)
	}

	return mw, nil
}

func IsManifestInAppliedState(mw *ocmworkv1.ManifestWork) bool {
	applied := false
	available := false
	degraded := false
	conditions := mw.Status.Conditions

	if len(conditions) > 0 {
		for _, condition := range conditions {
			if condition.Status == metav1.ConditionTrue {
				switch {
				case condition.Type == ocmworkv1.WorkApplied:
					applied = true
				case condition.Type == ocmworkv1.WorkAvailable:
					available = true
				case condition.Type == ocmworkv1.WorkDegraded:
					degraded = true
				}
			}
		}
	}

	return applied && available && !degraded
}

func (mwu *MWUtil) CreateOrUpdateVRGManifestWork(
	name, namespace, homeCluster string,
	vrg rmn.VolumeReplicationGroup, annotations map[string]string,
) error {
	manifestWork, err := mwu.generateVRGManifestWork(name, namespace, homeCluster, vrg, annotations)
	if err != nil {
		return err
	}

	return mwu.createOrUpdateManifestWork(manifestWork, homeCluster)
}

func (mwu *MWUtil) generateVRGManifestWork(name, namespace, homeCluster string,
	vrg rmn.VolumeReplicationGroup, annotations map[string]string,
) (*ocmworkv1.ManifestWork, error) {
	vrgClientManifest, err := mwu.generateVRGManifest(vrg)
	if err != nil {
		mwu.Log.Error(err, "failed to generate VolumeReplicationGroup manifest")

		return nil, err
	}

	manifests := []ocmworkv1.Manifest{*vrgClientManifest}

	return mwu.newManifestWork(
		fmt.Sprintf(ManifestWorkNameFormat, name, namespace, MWTypeVRG),
		homeCluster,
		map[string]string{},
		manifests, annotations), nil
}

func (mwu *MWUtil) generateVRGManifest(vrg rmn.VolumeReplicationGroup) (*ocmworkv1.Manifest, error) {
	return mwu.GenerateManifest(vrg)
}

// MaintenanceMode ManifestWork creation
func (mwu *MWUtil) CreateOrUpdateMModeManifestWork(
	name, cluster string,
	mMode rmn.MaintenanceMode, annotations map[string]string,
) error {
	manifestWork, err := mwu.generateMModeManifestWork(name, cluster, mMode, annotations)
	if err != nil {
		return err
	}

	return mwu.createOrUpdateManifestWork(manifestWork, cluster)
}

func (mwu *MWUtil) generateMModeManifestWork(name, cluster string,
	mMode rmn.MaintenanceMode, annotations map[string]string,
) (*ocmworkv1.ManifestWork, error) {
	mModeManifest, err := mwu.generateMModeManifest(mMode)
	if err != nil {
		mwu.Log.Error(err, "failed to generate MaintenanceMode manifest")

		return nil, err
	}

	manifests := []ocmworkv1.Manifest{*mModeManifest}

	return mwu.newManifestWork(
		fmt.Sprintf(ManifestWorkNameFormatClusterScope, name, MWTypeMMode),
		cluster,
		map[string]string{
			MModesLabel: "",
		},
		manifests, annotations), nil
}

func (mwu *MWUtil) generateMModeManifest(mMode rmn.MaintenanceMode) (*ocmworkv1.Manifest, error) {
	return mwu.GenerateManifest(mMode)
}

func (mwu *MWUtil) ListMModeManifests(cluster string) (*ocmworkv1.ManifestWorkList, error) {
	matchLabels := map[string]string{
		MModesLabel: "",
	}
	listOptions := []client.ListOption{
		client.InNamespace(cluster),
		client.MatchingLabels(matchLabels),
	}

	mModeMWs := &ocmworkv1.ManifestWorkList{}
	err := mwu.APIReader.List(context.TODO(), mModeMWs, listOptions...)

	return mModeMWs, err
}

func ExtractMModeFromManifestWork(mw *ocmworkv1.ManifestWork) (*rmn.MaintenanceMode, error) {
	gvk := schema.GroupVersionKind{
		Group:   rmn.GroupVersion.Group,
		Version: rmn.GroupVersion.Version,
		Kind:    "MaintenanceMode",
	}

	rawObject, err := GetRawExtension(mw.Spec.Workload.Manifests, gvk)
	if err != nil {
		return nil, fmt.Errorf("failed fetching MaintenanceMode from manifest %w", err)
	}

	if rawObject == nil {
		return nil, nil
	}

	mMode := &rmn.MaintenanceMode{}

	err = json.Unmarshal(rawObject.Raw, mMode)
	if err != nil {
		return nil, fmt.Errorf("failed unmarshaling MaintenanceMode from manifest %w", err)
	}

	return mMode, nil
}

// NetworkFence MW creation
func (mwu *MWUtil) CreateOrUpdateNFManifestWork(
	name, homeCluster string,
	nf csiaddonsv1alpha1.NetworkFence, annotations map[string]string,
) error {
	manifestWork, err := mwu.generateNFManifestWork(name, homeCluster, nf, annotations)
	if err != nil {
		return err
	}

	return mwu.createOrUpdateManifestWork(manifestWork, homeCluster)
}

func (mwu *MWUtil) generateNFManifestWork(name, homeCluster string,
	nf csiaddonsv1alpha1.NetworkFence, annotations map[string]string,
) (*ocmworkv1.ManifestWork, error) {
	nfClientManifest, err := mwu.generateNFManifest(nf)
	if err != nil {
		mwu.Log.Error(err, "failed to generate NetworkFence manifest")

		return nil, err
	}

	manifests := []ocmworkv1.Manifest{*nfClientManifest}

	// manifest work name for NetworkFence resource is
	// "name-type-mw"
	// name: name of the resource received from higher layer
	//       that wants to create the csiaddonsv1alpha1.NetworkFence resource
	// type: type of the resource for this ManifestWork
	return mwu.newManifestWork(
		fmt.Sprintf(ManifestWorkNameFormat, name, homeCluster, MWTypeNF),
		homeCluster,
		map[string]string{"app": "NF"},
		manifests, annotations), nil
}

func (mwu *MWUtil) generateNFManifest(nf csiaddonsv1alpha1.NetworkFence) (*ocmworkv1.Manifest, error) {
	return mwu.GenerateManifest(nf)
}

// DRClusterConfig ManifestWork creation
func (mwu *MWUtil) CreateOrUpdateDRCConfigManifestWork(cluster string, cConfig rmn.DRClusterConfig) error {
	manifestWork, err := mwu.generateDRCConfigManifestWork(cluster, cConfig)
	if err != nil {
		return err
	}

	return mwu.createOrUpdateManifestWork(manifestWork, cluster)
}

func (mwu *MWUtil) generateDRCConfigManifestWork(
	cluster string,
	cConfig rmn.DRClusterConfig,
) (*ocmworkv1.ManifestWork, error) {
	cConfigManifest, err := mwu.generateDRCConfigManifest(cConfig)
	if err != nil {
		mwu.Log.Error(err, "failed to generate DRClusterConfig manifest")

		return nil, err
	}

	manifests := []ocmworkv1.Manifest{*cConfigManifest}

	return mwu.newManifestWork(
		mwu.BuildManifestWorkName(MWTypeDRCConfig),
		cluster,
		map[string]string{},
		manifests, nil), nil
}

func (mwu *MWUtil) generateDRCConfigManifest(cConfig rmn.DRClusterConfig) (*ocmworkv1.Manifest, error) {
	return mwu.GenerateManifest(cConfig)
}

func ExtractDRCConfigFromManifestWork(mw *ocmworkv1.ManifestWork) (*rmn.DRClusterConfig, error) {
	gvk := schema.GroupVersionKind{
		Group:   rmn.GroupVersion.Group,
		Version: rmn.GroupVersion.Version,
		Kind:    "DRClusterConfig",
	}

	drcConfig := &rmn.DRClusterConfig{}

	err := ExtractResourceFromManifestWork(mw, drcConfig, gvk)

	return drcConfig, err
}

func (mwu *MWUtil) IsManifestApplied(cluster, mwType string) bool {
	mw, err := mwu.FindManifestWork(mwu.BuildManifestWorkName(mwType), cluster)
	if err != nil {
		return false
	}

	deployed := IsManifestInAppliedState(mw)

	return deployed
}

// Namespace MW creation
func (mwu *MWUtil) CreateOrUpdateNamespaceManifest(
	name string, namespaceName string, managedClusterNamespace string,
	annotations map[string]string,
) error {
	manifest, err := mwu.GenerateManifest(Namespace(namespaceName))
	if err != nil {
		return err
	}

	manifests := []ocmworkv1.Manifest{
		*manifest,
	}

	mwName := fmt.Sprintf(ManifestWorkNameFormat, name, namespaceName, MWTypeNS)
	manifestWork := mwu.newManifestWork(
		mwName,
		managedClusterNamespace,
		map[string]string{},
		manifests,
		annotations)

	return mwu.createOrUpdateManifestWork(manifestWork, managedClusterNamespace)
}

func Namespace(name string) *corev1.Namespace {
	return &corev1.Namespace{
		TypeMeta:   metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
}

func ExtractResourceFromManifestWork(
	mw *ocmworkv1.ManifestWork,
	object client.Object,
	gvk schema.GroupVersionKind,
) error {
	rawObject, err := GetRawExtension(mw.Spec.Workload.Manifests, gvk)
	if err != nil {
		return fmt.Errorf("failed fetching %s Kind from manifest %w", gvk.Kind, err)
	}

	if rawObject == nil {
		return nil
	}

	err = json.Unmarshal(rawObject.Raw, object)
	if err != nil {
		return fmt.Errorf("failed to unmarshal %s Kind from manifest %w", gvk.Kind, err)
	}

	return nil
}

func GetRawExtension(
	manifests []ocmworkv1.Manifest,
	gvk schema.GroupVersionKind,
) (*runtime.RawExtension, error) {
	for _, manifest := range manifests {
		obj := &unstructured.Unstructured{}

		err := json.Unmarshal(manifest.Raw, obj)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSON. Error %w", err)
		}

		objgvk := obj.GroupVersionKind()
		if objgvk == gvk {
			return &manifest.RawExtension, nil
		}
	}

	return nil, nil
}

func (mwu *MWUtil) GetDrClusterManifestWork(clusterName string) (*ocmworkv1.ManifestWork, error) {
	mw, err := mwu.FindManifestWork(DrClusterManifestWorkName, clusterName)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, nil
		}

		return nil, err
	}

	return mw, nil
}

func (mwu *MWUtil) CreateOrUpdateDrClusterManifestWork(
	clusterName string,
	objectsToAppend []interface{}, annotations map[string]string,
) error {
	objects := append(
		[]interface{}{
			vrgClusterRole,
			vrgClusterRoleBinding,
			mModeClusterRole,
			mModeClusterRoleBinding,
			drClusterConfigRole,
			drClusterConfigRoleBinding,
		},
		objectsToAppend...,
	)

	manifests := make([]ocmworkv1.Manifest, len(objects))

	for i, object := range objects {
		manifest, err := mwu.GenerateManifest(object)
		if err != nil {
			mwu.Log.Error(err, "failed to generate manifest", "object", object)

			return err
		}

		manifests[i] = *manifest
	}

	return mwu.createOrUpdateManifestWork(
		mwu.newManifestWork(
			DrClusterManifestWorkName,
			clusterName,
			map[string]string{},
			manifests, annotations,
		),
		clusterName,
	)
}

var (
	vrgClusterRole = &rbacv1.ClusterRole{
		TypeMeta:   metav1.TypeMeta{Kind: "ClusterRole", APIVersion: "rbac.authorization.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "open-cluster-management:klusterlet-work-sa:agent:volrepgroup-edit"},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"ramendr.openshift.io"},
				Resources: []string{"volumereplicationgroups"},
				Verbs:     []string{"create", "get", "list", "update", "delete"},
			},
		},
	}

	vrgClusterRoleBinding = &rbacv1.ClusterRoleBinding{
		TypeMeta:   metav1.TypeMeta{Kind: "ClusterRoleBinding", APIVersion: "rbac.authorization.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "open-cluster-management:klusterlet-work-sa:agent:volrepgroup-edit"},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "klusterlet-work-sa",
				Namespace: "open-cluster-management-agent",
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "open-cluster-management:klusterlet-work-sa:agent:volrepgroup-edit",
		},
	}

	mModeClusterRole = &rbacv1.ClusterRole{
		TypeMeta:   metav1.TypeMeta{Kind: "ClusterRole", APIVersion: "rbac.authorization.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "open-cluster-management:klusterlet-work-sa:agent:mmode-edit"},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"ramendr.openshift.io"},
				Resources: []string{"maintenancemodes"},
				Verbs:     []string{"create", "get", "list", "update", "delete"},
			},
		},
	}

	mModeClusterRoleBinding = &rbacv1.ClusterRoleBinding{
		TypeMeta:   metav1.TypeMeta{Kind: "ClusterRoleBinding", APIVersion: "rbac.authorization.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "open-cluster-management:klusterlet-work-sa:agent:mmode-edit"},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "klusterlet-work-sa",
				Namespace: "open-cluster-management-agent",
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "open-cluster-management:klusterlet-work-sa:agent:mmode-edit",
		},
	}

	drClusterConfigRole = &rbacv1.ClusterRole{
		TypeMeta:   metav1.TypeMeta{Kind: "ClusterRole", APIVersion: "rbac.authorization.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "open-cluster-management:klusterlet-work-sa:agent:drclusterconfig-edit"},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"ramendr.openshift.io"},
				Resources: []string{"drclusterconfigs"},
				Verbs:     []string{"create", "get", "list", "update", "delete"},
			},
		},
	}

	drClusterConfigRoleBinding = &rbacv1.ClusterRoleBinding{
		TypeMeta:   metav1.TypeMeta{Kind: "ClusterRoleBinding", APIVersion: "rbac.authorization.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "open-cluster-management:klusterlet-work-sa:agent:drclusterconfig-edit"},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "klusterlet-work-sa",
				Namespace: "open-cluster-management-agent",
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "open-cluster-management:klusterlet-work-sa:agent:drclusterconfig-edit",
		},
	}
)

func (mwu *MWUtil) GenerateManifest(obj interface{}) (*ocmworkv1.Manifest, error) {
	objJSON, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal %v to JSON, error %w", obj, err)
	}

	manifest := &ocmworkv1.Manifest{}
	manifest.RawExtension = runtime.RawExtension{Raw: objJSON}

	return manifest, nil
}

func (mwu *MWUtil) newManifestWork(name string, mcNamespace string,
	labels map[string]string, manifests []ocmworkv1.Manifest, annotations map[string]string,
) *ocmworkv1.ManifestWork {
	mw := &ocmworkv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: mcNamespace,
			Labels:    labels,
		},
		Spec: ocmworkv1.ManifestWorkSpec{
			Workload: ocmworkv1.ManifestsTemplate{
				Manifests: manifests,
			},
		},
	}

	if annotations != nil {
		mw.ObjectMeta.Annotations = annotations
	}

	return mw
}

func (mwu *MWUtil) createOrUpdateManifestWork(
	mw *ocmworkv1.ManifestWork,
	managedClusternamespace string,
) error {
	key := types.NamespacedName{Name: mw.Name, Namespace: managedClusternamespace}
	foundMW := &ocmworkv1.ManifestWork{}

	err := mwu.Client.Get(mwu.Ctx, key, foundMW)
	if err != nil {
		if !errors.IsNotFound(err) {
			return errorswrapper.Wrap(err, fmt.Sprintf("failed to fetch ManifestWork %s", key))
		}

		mwu.Log.Info("Creating ManifestWork", "cluster", managedClusternamespace, "MW", mw)

		return mwu.Client.Create(mwu.Ctx, mw)
	}

	if !reflect.DeepEqual(foundMW.Spec, mw.Spec) {
		mwu.Log.Info("Updating ManifestWork", "name", mw.Name, "namespace", foundMW.Namespace)

		return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			if err := mwu.Client.Get(mwu.Ctx, key, foundMW); err != nil {
				return err
			}

			mw.Spec.DeepCopyInto(&foundMW.Spec)

			return mwu.Client.Update(mwu.Ctx, foundMW)
		})
	}

	return nil
}

func (mwu *MWUtil) GetVRGManifestWorkCount(drClusters []string) int {
	count := 0

	for _, clusterName := range drClusters {
		_, err := mwu.FindManifestWorkByType(MWTypeVRG, clusterName)
		if err != nil {
			continue
		}

		count++
	}

	return count
}

func (mwu *MWUtil) DeleteManifestWorksForCluster(clusterName string) error {
	// VRG
	err := mwu.deleteManifestWorkWrapper(clusterName, MWTypeVRG)
	if err != nil {
		mwu.Log.Error(err, "failed to delete MW for VRG")

		return fmt.Errorf("failed to delete ManifestWork for VRG in namespace %s (%w)", clusterName, err)
	}

	// The ManifestWork that created a Namespace is intentionally left on the server

	return nil
}

func (mwu *MWUtil) deleteManifestWorkWrapper(fromCluster string, mwType string) error {
	mwName := mwu.BuildManifestWorkName(mwType)
	mwNamespace := fromCluster

	return mwu.DeleteManifestWork(mwName, mwNamespace)
}

func (mwu *MWUtil) DeleteManifestWork(mwName, mwNamespace string) error {
	mwu.Log.Info("Delete ManifestWork from", "namespace", mwNamespace, "name", mwName)

	mw := &ocmworkv1.ManifestWork{}

	err := mwu.Client.Get(mwu.Ctx, types.NamespacedName{Name: mwName, Namespace: mwNamespace}, mw)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}

		return fmt.Errorf("failed to retrieve manifestwork for type: %s. Error: %w", mwName, err)
	}

	mwu.Log.Info("Deleting ManifestWork", "name", mw.Name, "namespace", mwNamespace)

	err = mwu.Client.Delete(mwu.Ctx, mw)
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete MW. Error %w", err)
	}

	return nil
}

func ExtractVRGFromManifestWork(mw *ocmworkv1.ManifestWork) (*rmn.VolumeReplicationGroup, error) {
	if len(mw.Spec.Workload.Manifests) == 0 {
		return nil, fmt.Errorf("invalid VRG ManifestWork for type: %s", mw.Name)
	}

	vrgClientManifest := &mw.Spec.Workload.Manifests[0]
	vrg := &rmn.VolumeReplicationGroup{}

	err := yaml.Unmarshal(vrgClientManifest.RawExtension.Raw, &vrg)
	if err != nil {
		return nil, fmt.Errorf("unable to unmarshal VRG object (%w)", err)
	}

	return vrg, nil
}
