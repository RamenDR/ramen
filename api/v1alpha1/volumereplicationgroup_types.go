// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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

// VRGAsyncSpec has the parameters associated with RegionalDR
type VRGAsyncSpec struct {
	// Label selector to identify the VolumeReplicationClass resources
	// that are scanned to select an appropriate VolumeReplicationClass
	// for the VolumeReplication resource.
	//+optional
	ReplicationClassSelector metav1.LabelSelector `json:"replicationClassSelector,omitempty"`

	// Label selector to identify the VolumeSnapshotClass resources
	// that are scanned to select an appropriate VolumeSnapshotClass
	// for the VolumeReplication resource when using VolSync.
	//+optional
	VolumeSnapshotClassSelector metav1.LabelSelector `json:"volumeSnapshotClassSelector,omitempty"`

	// scheduling Interval for replicating Persistent Volume
	// data to a peer cluster. Interval is typically in the
	// form <num><m,h,d>. Here <num> is a number, 'm' means
	// minutes, 'h' means hours and 'd' stands for days.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^\d+[mhd]$`
	SchedulingInterval string `json:"schedulingInterval"`
}

// VRGSyncSpec has the parameters associated with MetroDR
type VRGSyncSpec struct{}

// VolSyncReplicationDestinationSpec defines the configuration for the VolSync
// protected PVC to be used by the destination cluster (Secondary)
type VolSyncReplicationDestinationSpec struct {
	// protectedPVC contains the information about the PVC to be protected by VolSync
	//+optional
	ProtectedPVC ProtectedPVC `json:"protectedPVC,omitempty"`
}

// VolSyncReplicationSourceSpec defines the configuration for the VolSync
// protected PVC to be used by the source cluster (Primary)
type VolSyncReplicationSourceSpec struct {
	// protectedPVC contains the information about the PVC to be protected by VolSync
	//+optional
	ProtectedPVC ProtectedPVC `json:"protectedPVC,omitempty"`
}

// VolSynccSpec defines the ReplicationDestination specs for the Secondary VRG, or
// the ReplicationSource specs for the Primary VRG
type VolSyncSpec struct {
	// rdSpec array contains the PVCs information that will/are be/being protected by VolSync
	//+optional
	RDSpec []VolSyncReplicationDestinationSpec `json:"rdSpec,omitempty"`

	// disabled when set, all the VolSync code is bypassed. Default is 'false'
	Disabled bool `json:"disabled,omitempty"`
}

// VRGAction which will be either a Failover or Relocate
// +kubebuilder:validation:Enum=Failover;Relocate
type VRGAction string

// These are the valid values for VRGAction
const (
	// Failover, VRG was failed over to/from this cluster,
	// the to/from is determined by VRG spec.ReplicationState values of Primary/Secondary respectively
	VRGActionFailover = VRGAction("Failover")

	// Relocate, VRG was relocated to/from this cluster,
	// the to/from is determined by VRG spec.ReplicationState values of Primary/Secondary respectively
	VRGActionRelocate = VRGAction("Relocate")
)

const ReservedBackupName = "use-backup-not-restore"

type KubeObjectProtectionSpec struct {
	// Preferred time between captures
	//+optional
	//+kubebuilder:validation:Format=duration
	CaptureInterval *metav1.Duration `json:"captureInterval,omitempty"`

	// Name of the Recipe to reference for capture and recovery workflows and volume selection.
	//+optional
	RecipeRef *RecipeRef `json:"recipeRef,omitempty"`

	// Recipe parameter definitions
	//+optional
	RecipeParameters map[string][]string `json:"recipeParameters,omitempty"`

	// Label selector to identify all the kube objects that need DR protection.
	// +optional
	KubeObjectSelector *metav1.LabelSelector `json:"kubeObjectSelector,omitempty"`
}

type RecipeRef struct {
	// Name of namespace recipe is in
	//+optional
	Namespace string `json:"namespace,omitempty"`

	// Name of recipe
	//+optional
	Name string `json:"name,omitempty"`
}

const KubeObjectProtectionCaptureIntervalDefault = 5 * time.Minute

// VolumeReplicationGroup (VRG) spec declares the desired schedule for data
// replication and replication state of all PVCs identified via the given
// PVC label selector. For each such PVC, the VRG will do the following:
//   - Create a VolumeReplication (VR) CR to enable storage level replication
//     of volume data and set the desired replication state (primary, secondary,
//     etc).
//   - Take the corresponding PV cluster data in Kubernetes etcd and deposit it in
//     the S3 store.  The url, access key and access id required to access the
//     S3 store is specified via environment variables of the VRG operator POD,
//     which is obtained from a secret resource.
//   - Manage the lifecycle of VR CR and S3 data according to CUD operations on
//     the PVC and the VRG CR.
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

	//+optional
	Async *VRGAsyncSpec `json:"async,omitempty"`
	//+optional
	Sync *VRGSyncSpec `json:"sync,omitempty"`

	// volsync defines the configuration when using VolSync plugin for replication.
	//+optional
	VolSync VolSyncSpec `json:"volSync,omitempty"`

	// PrepareForFinalSync when set, it tells VRG to prepare for the final sync from source to destination
	// cluster. Final sync is needed for relocation only, and for VolSync only
	//+optional
	PrepareForFinalSync bool `json:"prepareForFinalSync,omitempty"`

	// runFinalSync used to indicate whether final sync is needed. Final sync is needed for
	// relocation only, and for VolSync only
	//+optional
	RunFinalSync bool `json:"runFinalSync,omitempty"`

	// Action is either Failover or Relocate
	//+optional
	Action VRGAction `json:"action,omitempty"`
	//+optional
	KubeObjectProtection *KubeObjectProtectionSpec `json:"kubeObjectProtection,omitempty"`

	// ProtectedNamespaces is a list of namespaces that are considered for protection by the VRG.
	// Omitting this field means resources are only protected in the namespace where VRG is.
	// If this field is set, the VRG must be in the Ramen Ops Namespace as configured in the Ramen Config.
	// If this field is set, the protected namespace resources are treated as unmanaged.
	// You can use a recipe to filter and coordinate the order of the resources that are protected.
	//+optional
	ProtectedNamespaces *[]string `json:"protectedNamespaces,omitempty"`
}

type Identifier struct {
	// ID contains the globally unique storage identifier that identifies
	// the storage or replication backend
	ID string `json:"id"`

	// Modes is a list of maintenance modes that need to be activated on the storage
	// backend, prior to various Ramen related orchestration. This is read from the label
	// "ramendr.openshift.io/maintenancemodes" on the StorageClass or VolumeReplicationClass,
	// the value for which is a comma separated list of maintenance modes.
	//+optional
	Modes []MMode `json:"modes,omitempty"`
}

// StorageIdentifiers carries various identifiers that help correlate the identify of a storage instance
// that is backing a PVC across kubernetes clusters.
type StorageIdentifiers struct {
	// StorageProvisioners contains the provisioner name of the CSI driver used to provision this
	// PVC (extracted from the storageClass that was used for provisioning)
	//+optional
	StorageProvisioner string `json:"csiProvisioner,omitempty"`

	// StorageID contains the globally unique storage identifier, as reported by the storage backend
	// on the StorageClass as the value for the label "ramendr.openshift.io/storageid", that identifies
	// the storage backend that was used to provision the volume. It is used to label different StorageClasses
	// across different kubernetes clusters, that potentially share the same storage backend.
	// It also contains any maintenance modes that the storage backend requires during vaious Ramen actions
	//+optional
	StorageID Identifier `json:"storageID,omitempty"`

	// ReplicationID contains the globally unique replication identifier, as reported by the storage backend
	// on the VolumeReplicationClass as the value for the label "ramendr.openshift.io/replicationid", that
	// identifies the storage backends across 2 (or more) storage instances where the volume is replicated
	// It also contains any maintenance modes that the replication backend requires during vaious Ramen actions
	//+optional
	ReplicationID Identifier `json:"replicationID,omitempty"`
}

type ProtectedPVC struct {
	// Name of the namespace the PVC is in
	//+optional
	Namespace string `json:"namespace,omitempty"`

	// Name of the VolRep/PVC resource
	//+optional
	Name string `json:"name,omitempty"`

	// VolSyncPVC can be used to denote whether this PVC is protected by VolSync. Defaults to "false".
	//+optional
	ProtectedByVolSync bool `json:"protectedByVolSync,omitempty"`

	//+optional
	StorageIdentifiers `json:",inline,omitempty"`

	// Name of the StorageClass required by the claim.
	//+optional
	StorageClassName *string `json:"storageClassName,omitempty"`

	// Annotations for the PVC
	//+optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// Labels for the PVC
	//+optional
	Labels map[string]string `json:"labels,omitempty"`

	// AccessModes set in the claim to be replicated
	//+optional
	AccessModes []corev1.PersistentVolumeAccessMode `json:"accessModes,omitempty"`

	// Resources set in the claim to be replicated
	//+optional
	Resources corev1.VolumeResourceRequirements `json:"resources,omitempty"`

	// Conditions for this protected pvc
	//+optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Time of the most recent successful synchronization for the PVC, if
	// protected in the async or volsync mode
	//+optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// Duration of recent synchronization for PVC, if
	// protected in the async or volsync mode
	//+optional
	LastSyncDuration *metav1.Duration `json:"lastSyncDuration,omitempty"`

	// Bytes transferred per sync, if protected in async mode only
	LastSyncBytes *int64 `json:"lastSyncBytes,omitempty"`
}

type KubeObjectsCaptureIdentifier struct {
	Number int64 `json:"number"`
	//+nullable
	StartTime       metav1.Time `json:"startTime,omitempty"`
	StartGeneration int64       `json:"startGeneration,omitempty"`
}

type KubeObjectProtectionStatus struct {
	//+optional
	CaptureToRecoverFrom *KubeObjectsCaptureIdentifier `json:"captureToRecoverFrom,omitempty"`
}

// VolumeReplicationGroupStatus defines the observed state of VolumeReplicationGroup
type VolumeReplicationGroupStatus struct {
	State State `json:"state,omitempty"`

	// All the protected pvcs
	ProtectedPVCs []ProtectedPVC `json:"protectedPVCs,omitempty"`

	// Conditions are the list of VRG's summary conditions and their status.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// observedGeneration is the last generation change the operator has dealt with
	//+optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	//+nullable
	LastUpdateTime metav1.Time `json:"lastUpdateTime,omitempty"`
	//+optional
	KubeObjectProtection KubeObjectProtectionStatus `json:"kubeObjectProtection,omitempty"`

	PrepareForFinalSyncComplete bool `json:"prepareForFinalSyncComplete,omitempty"`
	FinalSyncComplete           bool `json:"finalSyncComplete,omitempty"`

	// lastGroupSyncTime is the time of the most recent successful synchronization of all PVCs
	//+optional
	LastGroupSyncTime *metav1.Time `json:"lastGroupSyncTime,omitempty"`

	// lastGroupSyncDuration is the max time from all the successful synced PVCs
	//+optional
	LastGroupSyncDuration *metav1.Duration `json:"lastGroupSyncDuration,omitempty"`

	// lastGroupSyncBytes is the total bytes transferred from the most recent
	// successful synchronization of all PVCs
	//+optional
	LastGroupSyncBytes *int64 `json:"lastGroupSyncBytes,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=vrg
// +kubebuilder:printcolumn:JSONPath=".spec.replicationState",name=desiredState,type=string
// +kubebuilder:printcolumn:JSONPath=".status.state",name=currentState,type=string

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
