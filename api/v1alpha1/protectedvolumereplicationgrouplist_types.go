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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProtectedVolumeReplicationGroupListSpec defines the desired state of ProtectedVolumeReplicationGroupList
type ProtectedVolumeReplicationGroupListSpec struct {
	// ProfileName is the name of the S3 profile in the Ramen operator config map
	// specifying the store to be queried
	S3ProfileName string `json:"s3ProfileName"`
}

// ProtectedVolumeReplicationGroupListStatus defines the observed state of ProtectedVolumeReplicationGroupList
type ProtectedVolumeReplicationGroupListStatus struct {
	// SampleTime is a timestamp representing the node time when the specified
	// store was last queried. It is represented in RFC3339 form and is in UTC.
	SampleTime metav1.Time `json:"sampleTime,omitempty"`

	// Items is a list of VolumeReplicationGroup objects represented in
	// the specified store when it was last queried.
	Items []VolumeReplicationGroup `json:"items,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Cluster

// ProtectedVolumeReplicationGroupList is the Schema for the protectedvolumereplicationgrouplists API
type ProtectedVolumeReplicationGroupList struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec ProtectedVolumeReplicationGroupListSpec `json:"spec,omitempty"`
	// +optional
	Status *ProtectedVolumeReplicationGroupListStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ProtectedVolumeReplicationGroupListList contains a list of ProtectedVolumeReplicationGroupList
type ProtectedVolumeReplicationGroupListList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProtectedVolumeReplicationGroupList `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ProtectedVolumeReplicationGroupList{}, &ProtectedVolumeReplicationGroupListList{})
}
