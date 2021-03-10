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
	plrv1 "github.com/open-cluster-management/multicloud-operators-placementrule/pkg/apis/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// AppVolumeReplicationSpec defines the desired state of AppVolumeReplication
type AppVolumeReplicationSpec struct {
	Placement     *plrv1.Placement `json:"placement"`
	FailedCluster string           `json:"failedCluster"`
}

// AppVolumeReplicationStatus defines the observed state of AppVolumeReplication
type AppVolumeReplicationStatus struct {
	HomeCluster string `json:"homeCluster,omitempty"`
	PeerCluster string `json:"peerCluster,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// AppVolumeReplication is the Schema for the appvolumereplications API
type AppVolumeReplication struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AppVolumeReplicationSpec   `json:"spec,omitempty"`
	Status AppVolumeReplicationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AppVolumeReplicationList contains a list of AppVolumeReplication
type AppVolumeReplicationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AppVolumeReplication `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AppVolumeReplication{}, &AppVolumeReplicationList{})
}
