/*
Copyright 2022 The RamenDR authors.

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

package util

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	errorswrapper "github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	csiaddonsv1alpha1 "github.com/csi-addons/kubernetes-csi-addons/api/v1alpha1"
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

	GetNamespaceFromManagedCluster(resourceName, resourceNamespace, managedCluster string,
		annotations map[string]string) (*corev1.Namespace, error)

	DeleteVRGManagedClusterView(resourceName, resourceNamespace, clusterName, resourceType string) error

	DeleteNamespaceManagedClusterView(resourceName, resourceNamespace, clusterName, resourceType string) error

	DeleteNFManagedClusterView(resourceName, resourceNamespace, clusterName, resourceType string) error
}

type ManagedClusterViewGetterImpl struct {
	client.Client
}

func (m ManagedClusterViewGetterImpl) GetVRGFromManagedCluster(resourceName, resourceNamespace, managedCluster string,
	annotations map[string]string) (*rmn.VolumeReplicationGroup, error) {
	logger := ctrl.Log.WithName("MCV").WithValues("resouceName", resourceName)
	// get VRG and verify status through ManagedClusterView
	mcvMeta := metav1.ObjectMeta{
		Name:        BuildManagedClusterViewName(resourceName, resourceNamespace, "vrg"),
		Namespace:   managedCluster,
		Annotations: annotations,
	}

	mcvViewscope := viewv1beta1.ViewScope{
		Resource:  "VolumeReplicationGroup",
		Name:      resourceName,
		Namespace: resourceNamespace,
	}

	vrg := &rmn.VolumeReplicationGroup{}

	err := m.getManagedClusterResource(mcvMeta, mcvViewscope, vrg, logger)

	return vrg, err
}

func (m ManagedClusterViewGetterImpl) GetNFFromManagedCluster(resourceName, resourceNamespace, managedCluster string,
	annotations map[string]string) (*csiaddonsv1alpha1.NetworkFence, error) {
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
		Resource:  "NetworkFence",
		Name:      resourceName,
		Namespace: resourceNamespace,
	}

	nf := &csiaddonsv1alpha1.NetworkFence{}

	err := m.getManagedClusterResource(mcvMeta, mcvViewscope, nf, logger)

	return nf, err
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

func (m ManagedClusterViewGetterImpl) GetNamespaceFromManagedCluster(
	resourceName, managedCluster, namespaceString string, annotations map[string]string) (*corev1.Namespace, error) {
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
		Resource: "Namespace",
		Name:     namespaceString,
	}

	namespace := &corev1.Namespace{}

	err := m.getManagedClusterResource(mcvMeta, mcvViewscope, namespace, logger)

	return namespace, err
}

/*
Description: queries a managed cluster for a resource type, and populates a variable with the results.
Requires:
	1) meta: information of the new/existing resource; defines which cluster(s) to search
	2) viewscope: query information for managed cluster resource. Example: resource, name.
	3) interface: empty variable to populate results into
Returns: error if encountered (nil if no error occurred). See results on interface object.
*/
func (m ManagedClusterViewGetterImpl) getManagedClusterResource(
	meta metav1.ObjectMeta, viewscope viewv1beta1.ViewScope, resource interface{}, logger logr.Logger) error {
	// create MCV first
	mcv, err := m.getOrCreateManagedClusterView(meta, viewscope, logger)
	if err != nil {
		return errorswrapper.Wrap(err, "getManagedClusterResource failed")
	}

	logger.Info(fmt.Sprintf("MCV Conditions: %v", mcv.Status.Conditions))

	// want single recent Condition with correct Type; otherwise: bad path
	switch len(mcv.Status.Conditions) {
	case 0:
		err = fmt.Errorf("missing ManagedClusterView conditions")
	case 1:
		switch {
		case mcv.Status.Conditions[0].Type != viewv1beta1.ConditionViewProcessing:
			err = fmt.Errorf("found invalid condition (%s) in ManagedClusterView", mcv.Status.Conditions[0].Type)
		case mcv.Status.Conditions[0].Reason == viewv1beta1.ReasonGetResourceFailed:
			err = errors.NewNotFound(schema.GroupResource{}, "requested resource not found in ManagedCluster")
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
	1) meta: specifies MangedClusterView name and managed cluster search information
	2) viewscope: once the managed cluster is found, use this information to find the resource.
		Optional params: Namespace, Resource, Group, Version, Kind. Resource can be used by itself, Kind requires Version
Returns: ManagedClusterView, error
*/
func (m ManagedClusterViewGetterImpl) getOrCreateManagedClusterView(
	meta metav1.ObjectMeta, viewscope viewv1beta1.ViewScope, logger logr.Logger) (*viewv1beta1.ManagedClusterView, error) {
	mcv := &viewv1beta1.ManagedClusterView{
		ObjectMeta: meta,
		Spec: viewv1beta1.ViewSpec{
			Scope: viewscope,
		},
	}

	err := m.Get(context.TODO(), types.NamespacedName{Name: meta.Name, Namespace: meta.Namespace}, mcv)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info(fmt.Sprintf("Creating ManagedClusterView %v", mcv))
			err = m.Create(context.TODO(), mcv)
		}

		if err != nil {
			return nil, errorswrapper.Wrap(err, "failed to getOrCreateManagedClusterView")
		}
	}

	if mcv.Spec.Scope != viewscope {
		logger.Info("WARNING: existing ManagedClusterView has different ViewScope than desired one")
	}

	return mcv, nil
}

func (m ManagedClusterViewGetterImpl) DeleteVRGManagedClusterView(
	resourceName, resourceNamespace, clusterName, resourceType string) error {
	logger := ctrl.Log.WithName("MCV").WithValues("resouceName", resourceName)
	mcvNameVRG := BuildManagedClusterViewName(resourceName, resourceNamespace, MWTypeVRG)

	return m.DeleteManagedClusterView(clusterName, mcvNameVRG, logger)
}

func (m ManagedClusterViewGetterImpl) DeleteNamespaceManagedClusterView(
	resourceName, resourceNamespace, clusterName, resourceType string) error {
	logger := ctrl.Log.WithName("MCV").WithValues("resouceName", resourceName)
	mcvNameNS := BuildManagedClusterViewName(resourceName, resourceNamespace, MWTypeNS)

	return m.DeleteManagedClusterView(clusterName, mcvNameNS, logger)
}

func (m ManagedClusterViewGetterImpl) DeleteNFManagedClusterView(
	resourceName, resourceNamespace, clusterName, resourceType string) error {
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
