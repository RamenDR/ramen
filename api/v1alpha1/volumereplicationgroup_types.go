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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vrv1alpha1 "github.com/kube-storage/volume-replication-operator/api/v1alpha1"
)

// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// VolumeReplicationGroup (VRG) spec declares the desired replication class
// and replication state of all the PVCs owned by a given application, which
// are identified via the given PVC label selector.  For every PVC created
// by the application, the VRG operator does the following:
// 	- Create a VolumeReplication (VR) CR to enable storage level replication
// 	  of volume data and set the volume state to primary, secondary, etc.
//  - Take the corresponding PV metadata in Kubernetes etcd and deposit it in
//    the S3 store.  The url, access key and access id required to access the
//    S3 store is specified via environment variables of the VRG operator POD,
//    which is obtained from a secret resource.
//  - Manage the lifecycle of VR CR and S3 data to match the CUD operations on
//    VRG CR.
type VolumeReplicationGroupSpec struct {
	// Important: Run "make" to regenerate code after modifying this file

	// Label selector to identify all the PVCs that are in this group
	// This operator will create and needs to be replicated
	PVCLabelSelector metav1.LabelSelector `json:"pvcLabelSelector"`

	// ReplicationClass of all volumes in this replication group that is
	// passed to VolumeReplication CR
	VolumeReplicationClass string `json:"volumeReplicationClass"`

	// Desired state of all volumes [primary or secondary] in this replication group that is
	// passed to VolumeReplication CR
	VolumeState vrv1alpha1.ImageState `json:"volumeState"`
}

// VolumeReplicationGroupStatus defines the observed state of VolumeReplicationGroup
// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
// Important: Run "make" to regenerate code after modifying this file
type VolumeReplicationGroupStatus struct{}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=vrg

// VolumeReplicationGroup is the Schema for the volumereplicationgroups API
type VolumeReplicationGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VolumeReplicationGroupSpec    `json:"spec,omitempty"`
	Status *VolumeReplicationGroupStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// VolumeReplicationGroupList contains a list of VolumeReplicationGroup
type VolumeReplicationGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VolumeReplicationGroup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&VolumeReplicationGroup{}, &VolumeReplicationGroupList{})
}
