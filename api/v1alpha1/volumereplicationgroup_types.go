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
// are identified via the given application label selector.  The VRG operator
// creates children VolumeReplication (VR) CRs for each PVC of the application,
// with the desired replication class and replication image state (primary or
// secondary).
type VolumeReplicationGroupSpec struct {
	// Important: Run "make" to regenerate code after modifying this file

	// Name of the application to replicate
	ApplicationName string `json:"applicationName,omitempty"`

	// Application label selector that is used to identify all its PVCs
	ApplicationLabelSelector metav1.LabelSelector `json:"applicationLabelSelector"`

	// ReplicationClass of all volumes in this replication group
	VolumeReplicationClass string `json:"volumeReplicationClass"`

	// State of the image of all volumes in this replication group
	ImageState vrv1alpha1.ImageState `json:"imageState"`
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
