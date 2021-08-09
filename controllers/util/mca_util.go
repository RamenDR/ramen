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

	"github.com/go-logr/logr"
	actionv1beta1 "github.com/open-cluster-management/multicloud-operators-foundation/pkg/apis/action/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type MCAUtil struct {
	client.Client
	Ctx           context.Context
	Log           logr.Logger
	InstName      string
	InstNamespace string
}

type MCAStatus string

const (
	Progressing MCAStatus = "Progressing"
	Completed   MCAStatus = "Completed"
	Error       MCAStatus = "Error"
)

// Create helps create required ManagedClusterAction(MCA) CRs for each PV, with the following properties:
//   - Each PV is a separate MCA CR, and is created if not already existing
//	 - Each MCA CR is labelled with the MCAUtil.InstName/MCAUtil.InstNamespace, to list efficiently
//   - Each MCA CR is added with the annotations DRPCNameAnnotation and DRPCNamespaceAnnotation for reconcile filtering
//   - Each MCA CR is protected by a finalizer, to prevent OCM garbage collection from deleting the the CRs
//     once their Status.Conditons[].Type == Completed is True
//	   - This hence requires the caller to garbage collect the same using the GarbageCollect interface
func (mca *MCAUtil) Create(namespace string, pvList []corev1.PersistentVolume) error {

}

// GarbageCollect helps remove the finalizer on required ManagedClusterAction(MCA) CRs for each PV,
// with the following properties:
//	 - Each MCA CR that is labelled with the MCAUtil.InstName/MCAUtil.InstNamespace is processed
//   - Removes the finalizer added by Create to let OCM or k8s garbage collect the MCA CRs
//     - IOW, post a GarbageCollect call it is possible that the MCA object may linger till the other garbage
//       collectors kick in and delete the objects
func (mca *MCAUtil) GarbageCollect(namespace string, pvList []corev1.PersistentVolume) error {

}

// Status returns a single MCAStatus as,
//	- MCAUtil.Completed, if all MCA PVs have their Completed status set to true
//  - MCAUtil.Progressing, if some MCA PVs are yet to report status
//  - MCAUtil.Error, if ANY MCA PV has reported their Completed status as false
func (mca *MCAUtil) Status(namespace string, pvList []corev1.PersistentVolume) (MCAStatus, error) {

}

// NOTE: Delete and Update MCA interfaces are not provided, till required

// NewAction is a dummy for now to bring in the import
func NewAction(name, namespace string, actiontype actionv1beta1.ActionType, kubework *actionv1beta1.KubeWorkSpec) *actionv1beta1.ManagedClusterAction {
	return &actionv1beta1.ManagedClusterAction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: actionv1beta1.ActionSpec{
			ActionType: actiontype,
			KubeWork:   kubework,
		},
	}
}
