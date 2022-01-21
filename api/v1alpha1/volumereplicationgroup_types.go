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

// AsyncMode which will be either Enabled or Disabled
// +kubebuilder:validation:Enum=Enabled;Disabled
type AsyncMode string

// SyncMode which will be either Enabled or Disabled
// +kubebuilder:validation:Enum=Enabled;Disabled
type SyncMode string

// These are the valid values for AsyncMode
const (
	// AsyncMode enabled for DR
	AsyncModeEnabled = AsyncMode("Enabled")

	// AsyncMode disabled for DR
	AsyncModeDisabled = AsyncMode("Disabled")
)

// These are the valid values for SyncMode
const (
	// AsyncMode enabled for DR
	SyncModeEnabled = SyncMode("Enabled")

	// AsyncMode disabled for DR
	SyncModeDisabled = SyncMode("Disabled")
)

// VRGAsyncSpec has the parameters associated with RegionalDR
type VRGAsyncSpec struct {
	// Label selector to identify the VolumeReplicationClass resources
	// that are scanned to select an appropriate VolumeReplicationClass
	// for the VolumeReplication resource.
	//+optional
	ReplicationClassSelector metav1.LabelSelector `json:"replicationClassSelector,omitempty"`

	// scheduling Interval for replicating Persistent Volume
	// data to a peer cluster. Interval is typically in the
	// form <num><m,h,d>. Here <num> is a number, 'm' means
	// minutes, 'h' means hours and 'd' stands for days.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^\d+[mhd]$`
	SchedulingInterval string `json:"schedulingInterval"`

	// Mode determines if AsyncDR is enabled or not
	Mode AsyncMode `json:"mode"`
}

// VRGSyncSpec has the parameters associated with MetroDR
type VRGSyncSpec struct {
	// Mode determines if SyncDR is enabled or not
	Mode SyncMode `json:"mode"`
}

// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// VolumeReplicationGroup (VRG) spec declares the desired schedule for data
// replication and replication state of all PVCs identified via the given
// PVC label selector. For each such PVC, the VRG will do the following:
// 	- Create a VolumeReplication (VR) CR to enable storage level replication
// 	  of volume data and set the desired replication state (primary, secondary,
//    etc).
//  - Take the corresponding PV cluster data in Kubernetes etcd and deposit it in
//    the S3 store.  The url, access key and access id required to access the
//    S3 store is specified via environment variables of the VRG operator POD,
//    which is obtained from a secret resource.
//  - Manage the lifecycle of VR CR and S3 data according to CUD operations on
//    the PVC and the VRG CR.
type VolumeReplicationGroupSpec struct {
	// Label selector to identify all the PVCs that are in this group
	// that needs to be replicated to the peer cluster.
	PVCSelector metav1.LabelSelector `json:"pvcSelector"`

	// Desired state of all volumes [primary or secondary] in this replication group;
	// this value is propagated to children VolumeReplication CRs
	ReplicationState ReplicationState `json:"replicationState"`

	// List of unique S3 profiles in RamenConfig that should be used to store
	// and forward PV related cluster state to peer DR clusters.
	S3Profiles []string `json:"s3Profiles"`

	Async VRGAsyncSpec `json:"async,omitempty"`
	Sync  VRGSyncSpec  `json:"sync,omitempty"`
}

type ProtectedPVC struct {
	// Name of the VolRep resource
	Name string `json:"name,omitempty"`

	// Conditions for each protected pvc
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// VolumeReplicationGroupStatus defines the observed state of VolumeReplicationGroup
// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
type VolumeReplicationGroupStatus struct {
	State State `json:"state,omitempty"`

	// All the protected pvcs
	ProtectedPVCs []ProtectedPVC `json:"protectedPVCs,omitempty"`

	// Conditions are the list of VRG's summary conditions and their status.
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
