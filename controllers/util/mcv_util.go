// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	errorswrapper "github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	csiaddonsv1alpha1 "github.com/csi-addons/kubernetes-csi-addons/apis/csiaddons/v1alpha1"
	rmn "github.com/ramendr/ramen/api/v1alpha1"
	viewv1beta1 "github.com/stolostron/multicloud-operators-foundation/pkg/apis/view/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
)

// begin MCV code
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

	GetResource(mcv *viewv1beta1.ManagedClusterView, resource interface{}) error

	DeleteManagedClusterView(clusterName, mcvName string, logger logr.Logger) error

	GetNamespaceFromManagedCluster(resourceName, resourceNamespace, managedCluster string,
		annotations map[string]string) (*corev1.Namespace, error)

	DeleteVRGManagedClusterView(resourceName, resourceNamespace, clusterName, resourceType string) error

	DeleteNamespaceManagedClusterView(resourceName, resourceNamespace, clusterName, resourceType string) error

	DeleteNFManagedClusterView(resourceName, resourceNamespace, clusterName, resourceType string) error
}

type ManagedClusterViewGetterImpl struct {
	client.Client
	APIReader client.Reader
}

func (m ManagedClusterViewGetterImpl) GetVRGFromManagedCluster(resourceName, resourceNamespace, managedCluster string,
	annotations map[string]string,
) (*rmn.VolumeReplicationGroup, error) {
	logger := ctrl.Log.WithName("MCV").WithValues("resourceName", resourceName, "cluster", managedCluster)
	// get VRG and verify status through ManagedClusterView
	mcvMeta := metav1.ObjectMeta{
		Name:        BuildManagedClusterViewName(resourceName, resourceNamespace, "vrg"),
		Namespace:   managedCluster,
		Annotations: annotations,
	}

	mcvViewscope := viewv1beta1.ViewScope{
		Kind:      "VolumeReplicationGroup",
		Group:     rmn.GroupVersion.Group,
		Version:   rmn.GroupVersion.Version,
		Name:      resourceName,
		Namespace: resourceNamespace,
	}

	vrg := &rmn.VolumeReplicationGroup{}

	err := m.getManagedClusterResource(mcvMeta, mcvViewscope, vrg, logger)

	return vrg, err
}

func (m ManagedClusterViewGetterImpl) GetNFFromManagedCluster(resourceName, resourceNamespace, managedCluster string,
	annotations map[string]string,
) (*csiaddonsv1alpha1.NetworkFence, error) {
	logger := ctrl.Log.WithName("MCV").WithValues("resouceName", resourceName)
	// get NetworkFence and verify status through ManagedClusterView
	mcvMeta := metav1.ObjectMeta{
		Name:      BuildManagedClusterViewName(resourceName, resourceNamespace, "nf"),
		Namespace: managedCluster,
	}

	if annotations != nil {
		mcvMeta.Annotations = annotations
	}

	mcvViewscope := viewv1beta1.ViewScope{
		Kind:      "NetworkFence",
		Group:     csiaddonsv1alpha1.GroupVersion.Group,
		Version:   csiaddonsv1alpha1.GroupVersion.Version,
		Name:      "network-fence-" + resourceName,
		Namespace: resourceNamespace,
	}

	nf := &csiaddonsv1alpha1.NetworkFence{}

	err := m.getManagedClusterResource(mcvMeta, mcvViewscope, nf, logger)

	return nf, err
}

func (m ManagedClusterViewGetterImpl) GetMModeFromManagedCluster(resourceName, managedCluster string,
	annotations map[string]string,
) (*rmn.MaintenanceMode, error) {
	logger := ctrl.Log.WithName("MCV").WithValues("resouceName", resourceName)
	// get MaintenanceMode and verify status through ManagedClusterView
	mcvMeta := metav1.ObjectMeta{
		Name:      BuildManagedClusterViewName(resourceName, "", MWTypeMMode),
		Namespace: managedCluster,
		Labels: map[string]string{
			MModesLabel: "",
		},
	}

	if annotations != nil {
		mcvMeta.Annotations = annotations
	}

	mcvViewscope := viewv1beta1.ViewScope{
		Kind:    "MaintenanceMode",
		Group:   rmn.GroupVersion.Group,
		Version: rmn.GroupVersion.Version,
		Name:    resourceName,
	}

	mMode := &rmn.MaintenanceMode{}

	err := m.getManagedClusterResource(mcvMeta, mcvViewscope, mMode, logger)

	return mMode, err
}

func (m ManagedClusterViewGetterImpl) ListMModesMCVs(cluster string) (*viewv1beta1.ManagedClusterViewList, error) {
	matchLabels := map[string]string{
		MModesLabel: "",
	}
	listOptions := []client.ListOption{
		client.InNamespace(cluster),
		client.MatchingLabels(matchLabels),
	}

	mModeMCVs := &viewv1beta1.ManagedClusterViewList{}
	if err := m.APIReader.List(context.TODO(), mModeMCVs, listOptions...); err != nil {
		return nil, err
	}

	return mModeMCVs, nil
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

func (m ManagedClusterViewGetterImpl) GetNamespaceFromManagedCluster(
	resourceName, managedCluster, namespaceString string, annotations map[string]string,
) (*corev1.Namespace, error) {
	logger := ctrl.Log.WithName("MCV").WithValues("resouceName", resourceName)

	// get Namespace and verify status through ManagedClusterView
	mcvMeta := metav1.ObjectMeta{
		Name:      BuildManagedClusterViewName(resourceName, namespaceString, MWTypeNS),
		Namespace: managedCluster,
	}

	if annotations != nil {
		mcvMeta.Annotations = annotations
	}

	mcvViewscope := viewv1beta1.ViewScope{
		Kind:    "Namespace",
		Group:   corev1.SchemeGroupVersion.Group,
		Version: corev1.SchemeGroupVersion.Version,
		Name:    namespaceString,
	}

	namespace := &corev1.Namespace{}

	err := m.getManagedClusterResource(mcvMeta, mcvViewscope, namespace, logger)

	return namespace, err
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
		return errorswrapper.Wrap(err, "getManagedClusterResource failed")
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
		return errors.NewNotFound(schema.GroupResource{}, "requested resource not found in ManagedCluster")
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
		return errorswrapper.Wrap(err, "getManagedClusterResource results")
	}

	// good path: convert raw data to usable object
	err = json.Unmarshal(mcv.Status.Result.Raw, resource)
	if err != nil {
		return errorswrapper.Wrap(err, "failed to Unmarshal data from ManagedClusterView to resource")
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

	err := m.Get(context.TODO(), key, mcv)
	if err != nil {
		if !errors.IsNotFound(err) {
			return nil, errorswrapper.Wrap(err, "failed to get ManagedClusterView")
		}

		logger.Info(fmt.Sprintf("Creating ManagedClusterView %s with scope %+v",
			key, viewscope))

		if err := m.Create(context.TODO(), mcv); err != nil {
			return nil, errorswrapper.Wrap(err, "failed to create ManagedClusterView")
		}
	}

	if mcv.Spec.Scope != viewscope {
		// Expected once when uprading ramen if scope format or details have changed.
		logger.Info(fmt.Sprintf("Updating ManagedClusterView %s scope %+v to %+v",
			key, mcv.Spec.Scope, viewscope))

		mcv.Spec.Scope = viewscope
		if err := m.Update(context.TODO(), mcv); err != nil {
			return nil, errorswrapper.Wrap(err, "failed to update ManagedClusterView")
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

func (m ManagedClusterViewGetterImpl) DeleteManagedClusterView(clusterName, mcvName string, logger logr.Logger) error {
	logger.Info("Delete ManagedClusterView from", "namespace", clusterName, "name", mcvName)

	mcv := &viewv1beta1.ManagedClusterView{}

	err := m.Get(context.TODO(), types.NamespacedName{Name: mcvName, Namespace: clusterName}, mcv)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}

		return fmt.Errorf("failed to retrieve ManagedClusterView for type: %s. Error: %w", mcvName, err)
	}

	logger.Info("Deleting ManagedClusterView", "name", mcv.Name, "namespace", mcv.Namespace)

	return m.Delete(context.TODO(), mcv)
}
