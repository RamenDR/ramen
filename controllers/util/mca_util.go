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
	"fmt"

	"github.com/go-logr/logr"
	actionv1beta1 "github.com/open-cluster-management/multicloud-operators-foundation/pkg/apis/action/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type MCAUtil struct {
	client.Client
	Ctx             context.Context
	Log             logr.Logger
	ParentName      string
	ParentNamespace string
}

type MCAStatus string

const (
	MCAProgressing MCAStatus = "Progressing"
	MCACompleted   MCAStatus = "Completed"
	MCAError       MCAStatus = "Error"

	DRPCLabel        = "drplacementcontrol.ramendr.openshift.io"
	drpcMCAFinalizer = "drplacementcontrol.ramendr.openshift.io/mca-protection"
)

// Create helps create required ManagedClusterAction(MCA) CRs for each PV, with the following properties:
//   - Each PV is a separate MCA CR, and is created if not already existing
//	 - Each MCA CR is labeled with the MCAUtil.InstName-MCAUtil.InstNamespace, to list efficiently
//   - Each MCA CR is added with the annotations DRPCNameAnnotation and DRPCNamespaceAnnotation for reconcile filtering
//   - Each MCA CR is protected by a finalizer, to prevent OCM garbage collection from deleting the the CRs
//     once their Status.Conditons[].Type == Completed is True
//	   - This hence requires the caller to garbage collect the same using the GarbageCollect interface
func (mca *MCAUtil) Create(namespace string, pvList []corev1.PersistentVolume) error {
	for _, pv := range pvList {
		pvKubeWork := mca.generatePVKubeWork(pv)

		err := mca.createManagedClusterAction(namespace, pv.Name, pvKubeWork)
		if err != nil {
			mca.Log.Error(err, "Failed to create ManagedClusterAction for PV", "pvName", pv.Name)

			return fmt.Errorf("failed to create ManagedClusterAction for PV (%s), %w", pv.Name, err)
		}
	}

	return nil
}

func (mca *MCAUtil) generatePVKubeWork(pv corev1.PersistentVolume) *actionv1beta1.KubeWorkSpec {
	return &actionv1beta1.KubeWorkSpec{
		Resource: "PersistentVolume",
		ObjectTemplate: runtime.RawExtension{
			Object: &pv,
		},
	}
}

func (mca *MCAUtil) createManagedClusterAction(namespace, pvName string, pvKubeWork *actionv1beta1.KubeWorkSpec) error {
	actionName := mca.ParentNamespace + "-" + mca.ParentName + "-" + pvName

	pvAction := mca.newPVAction(actionv1beta1.CreateActionType, namespace, actionName, pvKubeWork)

	existingAction := &actionv1beta1.ManagedClusterAction{}

	err := mca.Client.Get(mca.Ctx,
		types.NamespacedName{Name: actionName, Namespace: namespace},
		existingAction)
	if err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("failed to check if ManagedClusterAction exists (%s), %w", actionName, err)
		}

		mca.Log.Info("Creating ManagedClusterAction for", "cluster", namespace, "actionName", actionName)

		return mca.Client.Create(mca.Ctx, pvAction)
	}

	// TODO: There is a failfast oppertunity here based on status that we get
	return nil
}

func (mca *MCAUtil) newPVAction(
	actiontype actionv1beta1.ActionType,
	namespace, name string,
	kubework *actionv1beta1.KubeWorkSpec) *actionv1beta1.ManagedClusterAction {
	return &actionv1beta1.ManagedClusterAction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				DRPCNameAnnotation:      mca.ParentName,
				DRPCNamespaceAnnotation: mca.ParentNamespace,
			},
			Labels: map[string]string{
				DRPCLabel: mca.ParentNamespace + "-" + mca.ParentName,
			},
			Finalizers: []string{
				drpcMCAFinalizer,
			},
		},
		Spec: actionv1beta1.ActionSpec{
			ActionType: actiontype,
			KubeWork:   kubework,
		},
	}
}

// GarbageCollect helps remove the finalizer on required ManagedClusterAction(MCA) CRs for each PV,
// with the following properties:
//	 - Each MCA CR that is labeled with the MCAUtil.InstName/MCAUtil.InstNamespace is processed
//   - Removes the finalizer added by Create to let OCM or k8s garbage collect the MCA CRs
//     - IOW, post a GarbageCollect call it is possible that the MCA object may linger till the other garbage
//       collectors kick in and delete the objects
func (mca *MCAUtil) GarbageCollect(namespace string) error {
	pvActions, err := mca.pvActions(namespace)
	if err != nil {
		return err
	}

	for idx := range pvActions.Items {
		pvAction := &pvActions.Items[idx]
		if ContainsString(pvAction.ObjectMeta.Finalizers, drpcMCAFinalizer) {
			pvAction.ObjectMeta.Finalizers = RemoveString(pvAction.ObjectMeta.Finalizers, drpcMCAFinalizer)

			if err := mca.Client.Update(mca.Ctx, pvAction); err != nil {
				mca.Log.Error(err, "Failed to remove finalizer on ManagedClusterAction",
					"cluster", namespace, "actionName", pvAction.Name)

				return fmt.Errorf("failed to remove finalizer on ManagedClusterAction resource"+
					" (%s/%s), %w", namespace, pvAction.Name, err)
			}
		}
	}

	return nil
}

// Status returns a single MCAStatus as,
//	- MCACompleted, if all MCA PVs have their Completed status set to true
//  - MCAProgressing, if some MCA PVs are yet to report status
//  - MCAError, if ANY MCA PV has reported their Completed status as false
func (mca *MCAUtil) Status(namespace string) (MCAStatus, error) {
	pvActions, err := mca.pvActions(namespace)
	if err != nil {
		return MCAProgressing, err
	}

	for _, pvAction := range pvActions.Items {
		actionCondition := FindCondition(pvAction.Status.Conditions, actionv1beta1.ConditionActionCompleted)

		switch {
		case actionCondition == nil:
			mca.Log.Info("ManagedClusterAction progressing", "cluster", namespace, "actionName", pvAction.Name)

			return MCAProgressing, nil
		case actionCondition.Status == metav1.ConditionUnknown:
			mca.Log.Info("ManagedClusterAction progressing", "cluster", namespace, "actionName", pvAction.Name)

			return MCAProgressing, nil
		case actionCondition.Status == metav1.ConditionFalse:
			err = fmt.Errorf("%s", actionCondition.Message)
			mca.Log.Error(err, "ManagedClusterAction error", "cluster", namespace, "actionName", pvAction.Name)

			return MCAError, fmt.Errorf("failed to apply ManagedClusterAction resource (%s/%s), %w",
				namespace, pvAction.Name, err)
		}
	}

	return MCACompleted, nil
}

func (mca *MCAUtil) pvActions(namespace string) (*actionv1beta1.ManagedClusterActionList, error) {
	pvActionsList := &actionv1beta1.ManagedClusterActionList{}

	actionLabels := client.MatchingLabels{
		DRPCLabel: mca.ParentNamespace + "-" + mca.ParentName,
	}

	listOptions := []client.ListOption{
		client.InNamespace(namespace),
		actionLabels,
	}

	if err := mca.Client.List(mca.Ctx, pvActionsList, listOptions...); err != nil {
		mca.Log.Error(err, "Failed to list ManagedClusterAction", "labeled", labels.Set(actionLabels))

		return nil, fmt.Errorf("failed to list ManagedClusterAction labeled %v, %w", labels.Set(actionLabels), err)
	}

	mca.Log.Info("Found ManagedClusterActions", "count", len(pvActionsList.Items))

	return pvActionsList, nil
}

// NOTE: Delete and Update MCA interfaces are not provided, till required
