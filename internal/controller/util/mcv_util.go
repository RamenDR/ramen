// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	volrep "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	"github.com/go-logr/logr"
	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	storagev1 "k8s.io/api/storage/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	csiaddonsv1alpha1 "github.com/csi-addons/kubernetes-csi-addons/api/csiaddons/v1alpha1"
	rmn "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/core"
	viewv1beta1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/view/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
)

//nolint:interfacebloat
type ManagedClusterViewGetter interface {
	GetVRGFromManagedCluster(
		resourceName, resourceNamespace, managedCluster string,
		annotations map[string]string) (*rmn.VolumeReplicationGroup, error)

	GetNFFromManagedCluster(
		resourceName, resourceNamespace, managedCluster string,
		annotations map[string]string) (*csiaddonsv1alpha1.NetworkFence, error)

	GetMModeFromManagedCluster(
		resourceName, managedCluster string,
		annotations map[string]string) (*rmn.MaintenanceMode, error)

	ListMModesMCVs(managedCluster string) (*viewv1beta1.ManagedClusterViewList, error)

	GetDRClusterConfigFromManagedCluster(
		resourceName string,
		annotations map[string]string) (*rmn.DRClusterConfig, error)

	DeleteDRClusterConfigManagedClusterView(clusterName string) error

	GetSClassFromManagedCluster(
		resourceName, managedCluster string,
		annotations map[string]string) (*storagev1.StorageClass, error)

	ListSClassMCVs(managedCluster string) (*viewv1beta1.ManagedClusterViewList, error)

	GetVSClassFromManagedCluster(
		resourceName, managedCluster string,
		annotations map[string]string) (*snapv1.VolumeSnapshotClass, error)

	ListVSClassMCVs(managedCluster string) (*viewv1beta1.ManagedClusterViewList, error)

	GetVRClassFromManagedCluster(
		resourceName, managedCluster string,
		annotations map[string]string) (*volrep.VolumeReplicationClass, error)

	ListVRClassMCVs(managedCluster string) (*viewv1beta1.ManagedClusterViewList, error)

	GetResource(mcv *viewv1beta1.ManagedClusterView, resource interface{}) error

	DeleteManagedClusterView(clusterName, mcvName string, logger logr.Logger) error

	DeleteVRGManagedClusterView(resourceName, resourceNamespace, clusterName, resourceType string) error

	DeleteNamespaceManagedClusterView(resourceName, resourceNamespace, clusterName, resourceType string) error

	DeleteNFManagedClusterView(resourceName, resourceNamespace, clusterName, resourceType string) error
}

type ManagedClusterViewGetterImpl struct {
	client.Client
	APIReader client.Reader
}

// getResourceFromManagedCluster gets the resource named resourceName in the resourceNamespace (empty if cluster scoped)
// with the passed in group, version, and kind on the managedCluster. The created ManagedClusterView(MCV) has the
// passed in annotations and labels added to it. The MCV is named mcvname, and fetched into the passed in "resource"
// interface
func (m ManagedClusterViewGetterImpl) getResourceFromManagedCluster(
	resourceName, resourceNamespace, managedCluster string,
	annotations map[string]string, labels map[string]string,
	mcvName, kind, group, version string,
	resource interface{},
) error {
	logger := ctrl.Log.WithName("MCV").WithValues("resouceName", resourceName, "cluster", managedCluster)

	mcvMeta := metav1.ObjectMeta{
		Name:      mcvName,
		Namespace: managedCluster,
	}

	mcvMeta.Labels = labels
	mcvMeta.Annotations = annotations

	mcvViewscope := viewv1beta1.ViewScope{
		Kind:    kind,
		Group:   group,
		Version: version,
		Name:    resourceName,
	}

	if resourceNamespace != "" {
		mcvViewscope.Namespace = resourceNamespace
	}

	return m.getManagedClusterResource(mcvMeta, mcvViewscope, resource, logger)
}

func (m ManagedClusterViewGetterImpl) GetVRGFromManagedCluster(resourceName, resourceNamespace, managedCluster string,
	annotations map[string]string,
) (*rmn.VolumeReplicationGroup, error) {
	vrg := &rmn.VolumeReplicationGroup{}

	err := m.getResourceFromManagedCluster(
		resourceName,
		resourceNamespace,
		managedCluster,
		annotations,
		nil,
		BuildManagedClusterViewName(resourceName, resourceNamespace, "vrg"),
		"VolumeReplicationGroup",
		rmn.GroupVersion.Group,
		rmn.GroupVersion.Version,
		vrg,
	)

	return vrg, err
}

func (m ManagedClusterViewGetterImpl) GetNFFromManagedCluster(resourceName, resourceNamespace, managedCluster string,
	annotations map[string]string,
) (*csiaddonsv1alpha1.NetworkFence, error) {
	nf := &csiaddonsv1alpha1.NetworkFence{}

	err := m.getResourceFromManagedCluster(
		"network-fence-"+resourceName,
		resourceNamespace,
		managedCluster,
		annotations,
		nil,
		BuildManagedClusterViewName(resourceName, resourceNamespace, "nf"),
		"NetworkFence",
		csiaddonsv1alpha1.GroupVersion.Group,
		csiaddonsv1alpha1.GroupVersion.Version,
		nf,
	)

	return nf, err
}

func (m ManagedClusterViewGetterImpl) GetMModeFromManagedCluster(resourceName, managedCluster string,
	annotations map[string]string,
) (*rmn.MaintenanceMode, error) {
	mMode := &rmn.MaintenanceMode{}

	err := m.getResourceFromManagedCluster(
		resourceName,
		"",
		managedCluster,
		annotations,
		map[string]string{MModesLabel: ""},
		BuildManagedClusterViewName(resourceName, "", MWTypeMMode),
		"MaintenanceMode",
		rmn.GroupVersion.Group,
		rmn.GroupVersion.Version,
		mMode,
	)

	return mMode, err
}

func (m ManagedClusterViewGetterImpl) listMCVsWithLabel(cluster string, matchLabels map[string]string) (
	*viewv1beta1.ManagedClusterViewList,
	error,
) {
	listOptions := []client.ListOption{
		client.InNamespace(cluster),
		client.MatchingLabels(matchLabels),
	}

	mcvs := &viewv1beta1.ManagedClusterViewList{}
	if err := m.APIReader.List(context.TODO(), mcvs, listOptions...); err != nil {
		return nil, err
	}

	return mcvs, nil
}

func (m ManagedClusterViewGetterImpl) ListMModesMCVs(cluster string) (*viewv1beta1.ManagedClusterViewList, error) {
	return m.listMCVsWithLabel(cluster, map[string]string{MModesLabel: ""})
}

func (m ManagedClusterViewGetterImpl) GetDRClusterConfigFromManagedCluster(
	clusterName string,
	annotations map[string]string,
) (*rmn.DRClusterConfig, error) {
	drcConfig := &rmn.DRClusterConfig{}

	err := m.getResourceFromManagedCluster(
		clusterName,
		"",
		clusterName,
		annotations,
		nil,
		BuildManagedClusterViewName(clusterName, "", MWTypeDRCConfig),
		"DRClusterConfig",
		rmn.GroupVersion.Group,
		rmn.GroupVersion.Version,
		drcConfig,
	)

	return drcConfig, err
}

func (m ManagedClusterViewGetterImpl) GetSClassFromManagedCluster(resourceName, managedCluster string,
	annotations map[string]string,
) (*storagev1.StorageClass, error) {
	sc := &storagev1.StorageClass{}

	err := m.getResourceFromManagedCluster(
		resourceName,
		"",
		managedCluster,
		annotations,
		map[string]string{SClassLabel: ""},
		BuildManagedClusterViewName(resourceName, "", MWTypeSClass),
		"StorageClass",
		storagev1.SchemeGroupVersion.Group,
		storagev1.SchemeGroupVersion.Version,
		sc,
	)

	return sc, err
}

func (m ManagedClusterViewGetterImpl) ListSClassMCVs(cluster string) (*viewv1beta1.ManagedClusterViewList, error) {
	return m.listMCVsWithLabel(cluster, map[string]string{SClassLabel: ""})
}

func (m ManagedClusterViewGetterImpl) GetVSClassFromManagedCluster(resourceName, managedCluster string,
	annotations map[string]string,
) (*snapv1.VolumeSnapshotClass, error) {
	vsc := &snapv1.VolumeSnapshotClass{}

	err := m.getResourceFromManagedCluster(
		resourceName,
		"",
		managedCluster,
		annotations,
		map[string]string{VSClassLabel: ""},
		BuildManagedClusterViewName(resourceName, "", MWTypeVSClass),
		"VolumeSnapshotClass",
		snapv1.SchemeGroupVersion.Group,
		snapv1.SchemeGroupVersion.Version,
		vsc,
	)

	return vsc, err
}

func (m ManagedClusterViewGetterImpl) ListVSClassMCVs(cluster string) (*viewv1beta1.ManagedClusterViewList, error) {
	return m.listMCVsWithLabel(cluster, map[string]string{VSClassLabel: ""})
}

func (m ManagedClusterViewGetterImpl) GetVRClassFromManagedCluster(resourceName, managedCluster string,
	annotations map[string]string,
) (*volrep.VolumeReplicationClass, error) {
	vrc := &volrep.VolumeReplicationClass{}

	err := m.getResourceFromManagedCluster(
		resourceName,
		"",
		managedCluster,
		annotations,
		map[string]string{VRClassLabel: ""},
		BuildManagedClusterViewName(resourceName, "", MWTypeVRClass),
		"VolumeReplicationClass",
		volrep.GroupVersion.Group,
		volrep.GroupVersion.Version,
		vrc,
	)

	return vrc, err
}

func (m ManagedClusterViewGetterImpl) ListVRClassMCVs(cluster string) (*viewv1beta1.ManagedClusterViewList, error) {
	return m.listMCVsWithLabel(cluster, map[string]string{VRClassLabel: ""})
}

// outputs a string for use in creating a ManagedClusterView name
// example: when looking for a vrg with name 'demo' in the namespace 'ramen', input: ("demo", "ramen", "vrg")
// this will give output "demo-ramen-vrg-mcv"
func BuildManagedClusterViewName(resourceName, resourceNamespace, resource string) string {
	// for cluster scoped resources such as NetworkFence resource
	if resourceNamespace == "" {
		return fmt.Sprintf("%s-%s-mcv", resourceName, resource)
	}

	return fmt.Sprintf("%s-%s-%s-mcv", resourceName, resourceNamespace, resource)
}

func ClusterScopedResourceNameFromMCVName(mcvName string) string {
	splitName := strings.Split(mcvName, "-")

	// Length will be at least 3, remove last 2 elements and return the resource name
	return strings.Join(splitName[:len(splitName)-2], "-")
}

/*
Description: queries a managed cluster for a resource type, and populates a variable with the results.
Requires:
 1. meta: information of the new/existing resource; defines which cluster(s) to search
 2. viewscope: query information for managed cluster resource. Example: resource, name.
 3. interface: empty variable to populate results into

Returns: error if encountered (nil if no error occurred). See results on interface object.
*/
func (m ManagedClusterViewGetterImpl) getManagedClusterResource(
	meta metav1.ObjectMeta, viewscope viewv1beta1.ViewScope, resource interface{}, logger logr.Logger,
) error {
	// create MCV first
	mcv, err := m.getOrCreateManagedClusterView(meta, viewscope, logger)
	if err != nil {
		return fmt.Errorf("getManagedClusterResource failed: %w", err)
	}

	logger.Info(fmt.Sprintf("Get managedClusterResource Returned the following MCV Conditions: %v",
		mcv.Status.Conditions))

	return m.GetResource(mcv, resource)
}

// This function is temporarily used to parse the MCV.Status.Conditions[0].Messagefield for known error strings,
// including "not found" and "the server could not find the requested resource". While this approach is effective
// for identifying errors in the short term, it is not a sustainable solution.

// As a next step, an issue should be opened against the ACM View Controller to fix and improve the MCV status,
// potentially by adding the last error to the MCV.status field. This would allow for more comprehensive and accurate
// error reporting, and reduce the need for temporary workarounds like this function.
func parseErrorMessage(message string) error {
	checkNotFound := func(str string) bool {
		return strings.HasSuffix(str, "not found") ||
			strings.HasSuffix(str, "the server could not find the requested resource")
	}

	extractLastError := func(str string) string {
		index := strings.LastIndex(str, "err:")
		if index == -1 {
			return ""
		}

		return str[index+len("err:"):]
	}

	if checkNotFound(message) {
		return k8serrors.NewNotFound(schema.GroupResource{}, "requested resource not found in ManagedCluster")
	}

	return fmt.Errorf("err: %s", extractLastError(message))
}

func (m ManagedClusterViewGetterImpl) GetResource(mcv *viewv1beta1.ManagedClusterView, resource interface{}) error {
	var err error

	// want single recent Condition with correct Type; otherwise: bad path
	switch len(mcv.Status.Conditions) {
	case 0:
		err = fmt.Errorf("missing ManagedClusterView conditions")
	case 1:
		switch {
		case mcv.Status.Conditions[0].Type != viewv1beta1.ConditionViewProcessing:
			err = fmt.Errorf("found invalid condition (%s) in ManagedClusterView", mcv.Status.Conditions[0].Type)
		case mcv.Status.Conditions[0].Reason == viewv1beta1.ReasonGetResourceFailed:
			err = parseErrorMessage(mcv.Status.Conditions[0].Message)
		case mcv.Status.Conditions[0].Status != metav1.ConditionTrue:
			err = fmt.Errorf("ManagedClusterView is not ready (reason: %s)", mcv.Status.Conditions[0].Reason)
		}
	default:
		err = fmt.Errorf("found multiple status conditions with ManagedClusterView")
	}

	if err != nil {
		return fmt.Errorf("getManagedClusterResource results: %w", err)
	}

	// good path: convert raw data to usable object
	err = json.Unmarshal(mcv.Status.Result.Raw, resource)
	if err != nil {
		return fmt.Errorf("failed to Unmarshal data from ManagedClusterView to resource: %w", err)
	}

	return nil // success
}

/*
Description: create a new ManagedClusterView object, or update the existing one with the same name.
Requires:
 1. meta: specifies MangedClusterView name and managed cluster search information
 2. viewscope: once the managed cluster is found, use this information to find the resource.
    Optional params: Namespace, Resource, Group, Version, Kind. Resource can be used by itself, Kind requires Version

Returns: ManagedClusterView, error
*/
func (m ManagedClusterViewGetterImpl) getOrCreateManagedClusterView(
	meta metav1.ObjectMeta, viewscope viewv1beta1.ViewScope, logger logr.Logger,
) (*viewv1beta1.ManagedClusterView, error) {
	key := types.NamespacedName{Name: meta.Name, Namespace: meta.Namespace}

	mcv := &viewv1beta1.ManagedClusterView{
		ObjectMeta: meta,
		Spec: viewv1beta1.ViewSpec{
			Scope: viewscope,
		},
	}

	core.ObjectCreatedByRamenSetLabel(mcv)

	err := m.Get(context.TODO(), key, mcv)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get ManagedClusterView: %w", err)
		}

		logger.Info(fmt.Sprintf("Creating ManagedClusterView %s with scope %s",
			key, viewscope.Name))

		if err := m.Create(context.TODO(), mcv); err != nil {
			return nil, fmt.Errorf("failed to create ManagedClusterView: %w", err)
		}
	}

	if mcv.Spec.Scope != viewscope {
		// Expected once when uprading ramen if scope format or details have changed.
		logger.Info(fmt.Sprintf("Updating ManagedClusterView %s scope %s to %s",
			key, mcv.Spec.Scope.Name, viewscope.Name))

		mcv.Spec.Scope = viewscope
		if err := m.Update(context.TODO(), mcv); err != nil {
			return nil, fmt.Errorf("failed to update ManagedClusterView: %w", err)
		}
	}

	return mcv, nil
}

func (m ManagedClusterViewGetterImpl) DeleteVRGManagedClusterView(
	resourceName, resourceNamespace, clusterName, resourceType string,
) error {
	logger := ctrl.Log.WithName("MCV").WithValues("resouceName", resourceName)
	mcvNameVRG := BuildManagedClusterViewName(resourceName, resourceNamespace, MWTypeVRG)

	return m.DeleteManagedClusterView(clusterName, mcvNameVRG, logger)
}

func (m ManagedClusterViewGetterImpl) DeleteNamespaceManagedClusterView(
	resourceName, resourceNamespace, clusterName, resourceType string,
) error {
	logger := ctrl.Log.WithName("MCV").WithValues("resouceName", resourceName)
	mcvNameNS := BuildManagedClusterViewName(resourceName, resourceNamespace, MWTypeNS)

	return m.DeleteManagedClusterView(clusterName, mcvNameNS, logger)
}

func (m ManagedClusterViewGetterImpl) DeleteNFManagedClusterView(
	resourceName, resourceNamespace, clusterName, resourceType string,
) error {
	logger := ctrl.Log.WithName("MCV").WithValues("resouceName", resourceName)
	mcvNameNF := BuildManagedClusterViewName(resourceName, resourceNamespace, MWTypeNF)

	return m.DeleteManagedClusterView(clusterName, mcvNameNF, logger)
}

func (m ManagedClusterViewGetterImpl) DeleteDRClusterConfigManagedClusterView(clusterName string) error {
	logger := ctrl.Log.WithName("MCV").WithValues("resouceName", clusterName)
	mcvNameDRCConfig := BuildManagedClusterViewName(clusterName, "", MWTypeDRCConfig)

	return m.DeleteManagedClusterView(clusterName, mcvNameDRCConfig, logger)
}

func (m ManagedClusterViewGetterImpl) DeleteManagedClusterView(clusterName, mcvName string, logger logr.Logger) error {
	logger.Info("Delete ManagedClusterView from", "namespace", clusterName, "name", mcvName)

	mcv := &viewv1beta1.ManagedClusterView{}

	err := m.Get(context.TODO(), types.NamespacedName{Name: mcvName, Namespace: clusterName}, mcv)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}

		return fmt.Errorf("failed to retrieve ManagedClusterView for type: %s. Error: %w", mcvName, err)
	}

	logger.Info("Deleting ManagedClusterView", "name", mcv.Name, "namespace", mcv.Namespace)

	return m.Delete(context.TODO(), mcv)
}
