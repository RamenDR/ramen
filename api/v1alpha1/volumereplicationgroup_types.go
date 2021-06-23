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
)

// Important: Run "make" to regenerate code after modifying this file

// ReplicationState represents the replication operations to be performed on the volume
type ReplicationState string

const (
	// Promote the protected PVCs to primary
	Primary ReplicationState = "primary"

	// Demote the proteced PVCs to secondary
	Secondary ReplicationState = "secondary"
)

// State captures the latest state of the replication operation
type State string

const (
	// PrimaryState represents the Primary replication state
	PrimaryState State = "Primary"

	// SecondaryState represents the Secondary replication state
	SecondaryState State = "Secondary"

	// UnknownState represents the Unknown replication state
	UnknownState State = "Unknown"
)

// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// VolumeReplicationGroup (VRG) spec declares the desired schedule for data
// replication and replication state of all PVCs identified via the given
// PVC label selector. For each such PVC, the VRG will do the following:
// 	- Create a VolumeReplication (VR) CR to enable storage level replication
// 	  of volume data and set the desired replication state (primary, secondary,
//    etc).
//  - Take the corresponding PV metadata in Kubernetes etcd and deposit it in
//    the S3 store.  The url, access key and access id required to access the
//    S3 store is specified via environment variables of the VRG operator POD,
//    which is obtained from a secret resource.
//  - Manage the lifecycle of VR CR and S3 data according to CUD operations on
//    the PVC and the VRG CR.
type VolumeReplicationGroupSpec struct {
	// Label selector to identify all the PVCs that are in this group
	// that needs to be replicated to the peer cluster.
	PVCSelector metav1.LabelSelector `json:"pvcSelector"`

	// Label selector to identify the VolumeReplicationClass resources
	// that are scanned to select an appropriate VolumeReplicationClass
	// for the VolumeReplication resource.
	//+optional
	ReplicationClassSelector metav1.LabelSelector `json:"replicationClassSelector,omitempty"`

	// scheduling Interval for replicating Persistent Volume
	// data to a peer cluster. Interval is typically in the
	// form <num><m,h,d>. Here <num> is a number, 'm' means
	// minutes, 'h' means hours and 'd' stands for days.
	SchedulingInterval string `json:"schedulingInterval"`

	// Desired state of all volumes [primary or secondary] in this replication group;
	// this value is propagated to children VolumeReplication CRs
	ReplicationState ReplicationState `json:"replicationState"`

	// S3 Endpoint to replicate PV metadata; set this field, along with a secret
	// that contains the access-key-id and secret-access-key to enable VRG to
	// replicate the PV metadata to the given s3 endpoint to a bucket with
	// VRG's name.  Thus the VRG name needs to be unique among other VRGs using
	// the same S3Endpoint and it should also follow AWS bucket naming rules:
	// https://docs.aws.amazon.com/AmazonS3/latest/userguide/bucketnamingrules.html
	// If this field is not set, VRG may be used to simply control the replication
	// state of all PVs in this group, under the expectation that the PV metadata
	// is replicated by a different mechanism; this mode of operation may be
	// referred to as backup-less mode.
	S3Endpoint string `json:"s3Endpoint,omitempty"`
	// S3 Region: https://docs.aws.amazon.com/general/latest/gr/rande.html
	S3Region string `json:"s3Region,omitempty"`

	// Name of k8s secret that contains the credentials to access the S3 endpoint.
	// If S3Endpoint is used, also specify the k8s secret that contains the S3
	// access key id and secret access key set using the keys: AWS_ACCESS_KEY_ID
	// and AWS_SECRET_ACCESS_KEY.  The secret should be present in the same namespace
	// as the VRG
	S3SecretName string `json:"s3SecretName,omitempty"`
}

type ProtectedPVC struct {
	// Name of the VolRep resource
	Name string `json:"name,omitempty"`

	// Conditions for each protected pvc
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type ProtectedPVCMap map[string]*ProtectedPVC

// VolumeReplicationGroupStatus defines the observed state of VolumeReplicationGroup
// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
type VolumeReplicationGroupStatus struct {
	State State `json:"state,omitempty"`

	// All the protected pvcs
	ProtectedPVCs ProtectedPVCMap `json:"protectedPVCs,omitempty"`

	// Conditions are the list of conditions and their status.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// observedGeneration is the last generation change the operator has dealt with
	// +optional
	ObservedGeneration int64       `json:"observedGeneration,omitempty"`
	LastUpdateTime     metav1.Time `json:"lastUpdateTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=vrg

// VolumeReplicationGroup is the Schema for the volumereplicationgroups API
type VolumeReplicationGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VolumeReplicationGroupSpec   `json:"spec,omitempty"`
	Status VolumeReplicationGroupStatus `json:"status,omitempty"`
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
