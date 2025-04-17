// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-logr/logr"

	volrep "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"

	storagev1 "k8s.io/api/storage/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ocmworkv1 "open-cluster-management.io/api/work/v1"
	viewv1beta1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/view/v1beta1"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	rmnutil "github.com/ramendr/ramen/internal/controller/util"
)

type FakeMCVGetter struct {
	client.Client
	apiReader client.Reader
}

func (f FakeMCVGetter) GetDRClusterConfigFromManagedCluster(
	resourceName string,
	annotations map[string]string,
) (*rmn.DRClusterConfig, error) {
	return nil, nil
}

func (f FakeMCVGetter) DeleteDRClusterConfigManagedClusterView(clusterName string) error {
	return nil
}

func (f FakeMCVGetter) GetSClassFromManagedCluster(resourceName, managedCluster string, annotations map[string]string,
) (*storagev1.StorageClass, error) {
	return nil, nil
}

func (f FakeMCVGetter) ListSClassMCVs(managedCluster string) (*viewv1beta1.ManagedClusterViewList, error) {
	return &viewv1beta1.ManagedClusterViewList{}, nil
}

func (f FakeMCVGetter) GetVSClassFromManagedCluster(resourceName, managedCluster string, annotations map[string]string,
) (*snapv1.VolumeSnapshotClass, error) {
	return nil, nil
}

func (f FakeMCVGetter) ListVSClassMCVs(managedCluster string) (*viewv1beta1.ManagedClusterViewList, error) {
	return &viewv1beta1.ManagedClusterViewList{}, nil
}

func (f FakeMCVGetter) GetVRClassFromManagedCluster(resourceName, managedCluster string, annotations map[string]string,
) (*volrep.VolumeReplicationClass, error) {
	return nil, nil
}

func (f FakeMCVGetter) ListVRClassMCVs(managedCluster string) (*viewv1beta1.ManagedClusterViewList, error) {
	return &viewv1beta1.ManagedClusterViewList{}, nil
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

	if !k8serrors.IsNotFound(err) {
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
		if k8serrors.IsNotFound(err) {
			return nil
		}

		return err
	}

	return f.Delete(context.TODO(), mcv)
}
