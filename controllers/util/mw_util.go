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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
)

const (
	// ManifestWorkNameFormat is a formated a string used to generate the manifest name
	// The format is name-namespace-type-mw where:
	// - name is the DRPC name
	// - namespace is the DRPC namespace
	// - type is either "vrg", "pv", or "roles"
	ManifestWorkNameFormat string = "%s-%s-%s-mw"

	// ManifestWork VRG Type
	MWTypeVRG string = "vrg"

	// ManifestWork PV Type
	MWTypePV string = "pv"

	// ManifestWork Roles Type
	MWTypeRoles string = "roles"

	// Annotations for MW and PlacementRule
	DRPCNameAnnotation      = "drplacementcontrol.ramendr.openshift.io/drpc-name"
	DRPCNamespaceAnnotation = "drplacementcontrol.ramendr.openshift.io/drpc-namespace"
)

type MWUtil struct {
	client.Client
	Ctx           context.Context
	Log           logr.Logger
	InstName      string
	InstNamespace string
}

func ManifestWorkName(name, namespace, mwType string) string {
	return fmt.Sprintf(ManifestWorkNameFormat, name, namespace, mwType)
}

func (mwu *MWUtil) BuildManifestWorkName(mwType string) string {
	return ManifestWorkName(mwu.InstName, mwu.InstNamespace, mwType)
}

func (mwu *MWUtil) ManifestExistAndApplied(mwName, newPrimary string) (bool, error) {
	// Try to find whether we have already created a ManifestWork for this
	mw, err := mwu.FindManifestWork(mwName, newPrimary)
	if err != nil {
		if !errors.IsNotFound(err) {
			mwu.Log.Error(err, "Failed to find 'PV restore' ManifestWork")

			return false, nil
		}

		return false, err
	}

	if mw != nil {
		mwu.Log.Info(fmt.Sprintf("Found an existing manifest work (%v)", mw))
		// Check if the MW is in Applied state
		if IsManifestInAppliedState(mw) {
			mwu.Log.Info(fmt.Sprintf("ManifestWork %s/%s in Applied state", mw.Namespace, mw.Name))

			return true, nil
		}

		mwu.Log.Info("MW is not in applied state", "namespace/name", mw.Namespace+"/"+mw.Name)
	}

	return false, nil
}

func (mwu *MWUtil) FindManifestWork(mwName, managedCluster string) (*ocmworkv1.ManifestWork, error) {
	if managedCluster == "" {
		return nil, fmt.Errorf("invalid cluster for MW %s", mwName)
	}

	mw := &ocmworkv1.ManifestWork{}

	err := mwu.Client.Get(mwu.Ctx, types.NamespacedName{Name: mwName, Namespace: managedCluster}, mw)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, fmt.Errorf("MW not found (%w)", err)
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
	name, namespace, homeCluster, s3Endpoint, s3Region, s3SecretName string, pvcSelector metav1.LabelSelector,
	schedulingInterval string, replClassSelector metav1.LabelSelector) error {
	mwu.Log.Info(fmt.Sprintf("Create or Update manifestwork %s:%s:%s:%s:%s",
		name, namespace, homeCluster, s3Endpoint, s3SecretName))

	manifestWork, err := mwu.generateVRGManifestWork(name, namespace, homeCluster,
		s3Endpoint, s3Region, s3SecretName, pvcSelector, schedulingInterval, replClassSelector)
	if err != nil {
		return err
	}

	return mwu.createOrUpdateManifestWork(manifestWork, homeCluster)
}

func (mwu *MWUtil) generateVRGManifestWork(
	name, namespace, homeCluster, s3Endpoint, s3Region, s3SecretName string,
	pvcSelector metav1.LabelSelector, schedulingInterval string,
	replClassSelector metav1.LabelSelector) (*ocmworkv1.ManifestWork, error) {
	vrgClientManifest, err := mwu.generateVRGManifest(name, namespace, s3Endpoint,
		s3Region, s3SecretName, pvcSelector, schedulingInterval, replClassSelector)
	if err != nil {
		mwu.Log.Error(err, "failed to generate VolumeReplicationGroup manifest")

		return nil, err
	}

	manifests := []ocmworkv1.Manifest{*vrgClientManifest}

	return mwu.newManifestWork(
		fmt.Sprintf(ManifestWorkNameFormat, name, namespace, MWTypeVRG),
		homeCluster,
		map[string]string{"app": "VRG"},
		manifests), nil
}

func (mwu *MWUtil) generateVRGManifest(
	name, namespace, s3Endpoint, s3Region, s3SecretName string,
	pvcSelector metav1.LabelSelector, schedulingInterval string,
	replClassSelector metav1.LabelSelector) (*ocmworkv1.Manifest, error) {
	return mwu.GenerateManifest(&rmn.VolumeReplicationGroup{
		TypeMeta:   metav1.TypeMeta{Kind: "VolumeReplicationGroup", APIVersion: "ramendr.openshift.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: rmn.VolumeReplicationGroupSpec{
			PVCSelector:              pvcSelector,
			SchedulingInterval:       schedulingInterval,
			ReplicationState:         rmn.Primary,
			S3Endpoint:               s3Endpoint,
			S3Region:                 s3Region,
			S3SecretName:             s3SecretName,
			ReplicationClassSelector: replClassSelector,
		},
	})
}

func (mwu *MWUtil) CreateOrUpdateVRGRolesManifestWork(namespace string) error {
	manifestWork, err := mwu.generateVRGRolesManifestWork(namespace)
	if err != nil {
		return err
	}

	return mwu.createOrUpdateManifestWork(manifestWork, namespace)
}

func (mwu *MWUtil) generateVRGRolesManifestWork(namespace string) (*ocmworkv1.ManifestWork, error) {
	vrgClusterRole, err := mwu.generateVRGClusterRoleManifest()
	if err != nil {
		mwu.Log.Error(err, "failed to generate VolumeReplicationGroup ClusterRole manifest")

		return nil, err
	}

	vrgClusterRoleBinding, err := mwu.generateVRGClusterRoleBindingManifest()
	if err != nil {
		mwu.Log.Error(err, "failed to generate VolumeReplicationGroup ClusterRoleBinding manifest")

		return nil, err
	}

	manifests := []ocmworkv1.Manifest{*vrgClusterRole, *vrgClusterRoleBinding}

	return mwu.newManifestWork(
		"ramendr-vrg-roles",
		namespace,
		map[string]string{},
		manifests), nil
}

func (mwu *MWUtil) generateVRGClusterRoleManifest() (*ocmworkv1.Manifest, error) {
	return mwu.GenerateManifest(&rbacv1.ClusterRole{
		TypeMeta:   metav1.TypeMeta{Kind: "ClusterRole", APIVersion: "rbac.authorization.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "open-cluster-management:klusterlet-work-sa:agent:volrepgroup-edit"},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"ramendr.openshift.io"},
				Resources: []string{"volumereplicationgroups"},
				Verbs:     []string{"create", "get", "list", "update", "delete"},
			},
		},
	})
}

func (mwu *MWUtil) generateVRGClusterRoleBindingManifest() (*ocmworkv1.Manifest, error) {
	return mwu.GenerateManifest(&rbacv1.ClusterRoleBinding{
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
	})
}

func (mwu *MWUtil) CreateOrUpdatePVRolesManifestWork(namespace string) error {
	manifestWork, err := mwu.generatePVRolesManifestWork(namespace)
	if err != nil {
		return err
	}

	return mwu.createOrUpdateManifestWork(manifestWork, namespace)
}

func (mwu *MWUtil) generatePVRolesManifestWork(namespace string) (*ocmworkv1.ManifestWork, error) {
	pvClusterRole, err := mwu.generatePVClusterRoleManifest()
	if err != nil {
		mwu.Log.Error(err, "failed to generate PersistentVolume ClusterRole manifest")

		return nil, err
	}

	pvClusterRoleBinding, err := mwu.generatePVClusterRoleBindingManifest()
	if err != nil {
		mwu.Log.Error(err, "failed to generate PersistentVolume ClusterRoleBinding manifest")

		return nil, err
	}

	manifests := []ocmworkv1.Manifest{*pvClusterRole, *pvClusterRoleBinding}

	return mwu.newManifestWork(
		"ramendr-pv-roles",
		namespace,
		map[string]string{},
		manifests), nil
}

func (mwu *MWUtil) generatePVClusterRoleManifest() (*ocmworkv1.Manifest, error) {
	return mwu.GenerateManifest(&rbacv1.ClusterRole{
		TypeMeta:   metav1.TypeMeta{Kind: "ClusterRole", APIVersion: "rbac.authorization.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "open-cluster-management:klusterlet-work-sa:agent:pv-edit"},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"persistentvolumes"},
				Verbs:     []string{"create", "get", "list", "update", "delete"},
			},
		},
	})
}

func (mwu *MWUtil) generatePVClusterRoleBindingManifest() (*ocmworkv1.Manifest, error) {
	return mwu.GenerateManifest(&rbacv1.ClusterRoleBinding{
		TypeMeta:   metav1.TypeMeta{Kind: "ClusterRoleBinding", APIVersion: "rbac.authorization.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "open-cluster-management:klusterlet-work-sa:agent:pv-edit"},
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
			Name:     "open-cluster-management:klusterlet-work-sa:agent:pv-edit",
		},
	})
}

func (mwu *MWUtil) CreateOrUpdatePVsManifestWork(
	name string, namespace string, homeClusterName string, pvList []corev1.PersistentVolume) error {
	mwu.Log.Info("Create manifest work for PVs", "DRPC",
		name, "cluster", homeClusterName, "PV count", len(pvList))

	mwName := mwu.BuildManifestWorkName(MWTypePV)

	manifestWork, err := mwu.generatePVManifestWork(mwName, homeClusterName, pvList)
	if err != nil {
		return err
	}

	return mwu.createOrUpdateManifestWork(manifestWork, homeClusterName)
}

func (mwu *MWUtil) generatePVManifestWork(
	mwName, homeClusterName string, pvList []corev1.PersistentVolume) (*ocmworkv1.ManifestWork, error) {
	manifests, err := mwu.generatePVManifest(pvList)
	if err != nil {
		return nil, err
	}

	return mwu.newManifestWork(
		mwName,
		homeClusterName,
		map[string]string{"app": "PV"},
		manifests), nil
}

// This function follow a slightly different pattern than the rest, simply because the pvList that come
// from the S3 store will contain PV objects already converted to a string.
func (mwu *MWUtil) generatePVManifest(
	pvList []corev1.PersistentVolume) ([]ocmworkv1.Manifest, error) {
	manifests := []ocmworkv1.Manifest{}

	for _, pv := range pvList {
		pvClientManifest, err := mwu.GenerateManifest(pv)
		// Either all succeed or none
		if err != nil {
			mwu.Log.Error(err, "failed to generate manifest for PV")

			return nil, err
		}

		manifests = append(manifests, *pvClientManifest)
	}

	return manifests, nil
}

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
	labels map[string]string, manifests []ocmworkv1.Manifest) *ocmworkv1.ManifestWork {
	return &ocmworkv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: mcNamespace,
			Labels:    labels,
			Annotations: map[string]string{
				DRPCNameAnnotation:      mwu.InstName,
				DRPCNamespaceAnnotation: mwu.InstNamespace,
			},
		},
		Spec: ocmworkv1.ManifestWorkSpec{
			Workload: ocmworkv1.ManifestsTemplate{
				Manifests: manifests,
			},
		},
	}
}

func (mwu *MWUtil) createOrUpdateManifestWork(
	mw *ocmworkv1.ManifestWork,
	managedClusternamespace string) error {
	foundMW := &ocmworkv1.ManifestWork{}

	err := mwu.Client.Get(mwu.Ctx,
		types.NamespacedName{Name: mw.Name, Namespace: managedClusternamespace},
		foundMW)
	if err != nil {
		if !errors.IsNotFound(err) {
			return errorswrapper.Wrap(err, fmt.Sprintf("failed to fetch ManifestWork %s", mw.Name))
		}

		// Let DRPC receive notification for any changes to ManifestWork CR created by it.
		// if err := ctrl.SetControllerReference(d.instance, mw, d.reconciler.Scheme); err != nil {
		//	return fmt.Errorf("failed to set owner reference to ManifestWork resource (%s/%s) (%v)",
		//		mw.Name, mw.Namespace, err)
		// }

		mwu.Log.Info("Creating ManifestWork for", "cluster", managedClusternamespace, "MW", mw)

		return mwu.Client.Create(mwu.Ctx, mw)
	}

	if !reflect.DeepEqual(foundMW.Spec, mw.Spec) {
		mw.Spec.DeepCopyInto(&foundMW.Spec)

		mwu.Log.Info("ManifestWork exists.", "MW", mw)

		return mwu.Client.Update(mwu.Ctx, foundMW)
	}

	return nil
}

func (mwu *MWUtil) DeleteManifestWorksForCluster(clusterName string) error {
	err := mwu.deleteVRGManifestWork(clusterName)
	if err != nil {
		mwu.Log.Error(err, "failed to delete MW for VRG")

		return fmt.Errorf("failed to delete ManifestWork for VRG in namespace %s (%w)", clusterName, err)
	}

	err = mwu.deletePVManifestWork(clusterName)
	if err != nil {
		mwu.Log.Error(err, "failed to delete MW for PVs")

		return fmt.Errorf("failed to delete ManifestWork for PVs in namespace %s (%w)", clusterName, err)
	}

	return nil
}

func (mwu *MWUtil) deletePVManifestWork(fromCluster string) error {
	pvMWName := mwu.BuildManifestWorkName(MWTypePV)
	pvMWNamespace := fromCluster

	return mwu.deleteManifestWork(pvMWName, pvMWNamespace)
}

func (mwu *MWUtil) deleteVRGManifestWork(fromCluster string) error {
	vrgMWName := mwu.BuildManifestWorkName(MWTypeVRG)
	vrgMWNamespace := fromCluster

	return mwu.deleteManifestWork(vrgMWName, vrgMWNamespace)
}

func (mwu *MWUtil) deleteManifestWork(mwName, mwNamespace string) error {
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

	return mwu.Client.Delete(mwu.Ctx, mw)
}
