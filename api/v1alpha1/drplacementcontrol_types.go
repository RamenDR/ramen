// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DRAction which will be either a Failover or Relocate action
// +kubebuilder:validation:Enum=Failover;Relocate
type DRAction string

// These are the valid values for DRAction
const (
	// Failover, restore PVs to the TargetCluster
	ActionFailover = DRAction("Failover")

	// Relocate, restore PVs to the designated TargetCluster.  PreferredCluster will change
	// to be the TargetCluster.
	ActionRelocate = DRAction("Relocate")
)

// DRState for keeping track of the DR placement
type DRState string

// These are the valid values for DRState
const (
	// WaitForUser, state recorded in DRPC status to indicate that we are
	// waiting for the user to take an action after hub recover.
	WaitForUser = DRState("WaitForUser")

	// Initiating, state recorded in the DRPC status to indicate that this
	// action (Deploy/Failover/Relocate) is preparing for execution. There
	// is NO follow up state called 'Initiated'
	Initiating = DRState("Initiating")

	// Deploying, state recorded in the DRPC status to indicate that the
	// initial deployment is in progress. Deploying means selecting the
	// preffered cluster and creating a VRG MW for it and waiting for MW
	// to be applied in the managed cluster
	Deploying = DRState("Deploying")

	// Deployed, this is the state that will be recorded in the DRPC status
	// when initial deplyment has been performed successfully
	Deployed = DRState("Deployed")

	// FailingOver, state recorded in the DRPC status when the failover
	// is initiated but has not been completed yet
	FailingOver = DRState("FailingOver")

	// FailedOver, state recorded in the DRPC status when the failover
	// process has completed
	FailedOver = DRState("FailedOver")

	// Relocating, state recorded in the DRPC status to indicate that the
	// relocation is in progress
	Relocating = DRState("Relocating")

	// Relocated, state recorded in
	Relocated = DRState("Relocated")

	Deleting = DRState("Deleting")
)

const (
	// Available condition provides the latest available observation regarding the readiness of the cluster,
	// in status.preferredDecision, for workload deployment.
	ConditionAvailable = "Available"

	// PeerReady condition provides the latest available observation regarding the readiness of a peer cluster
	// to failover or relocate the workload.
	ConditionPeerReady = "PeerReady"

	// Protected condition provides the latest available observation regarding the protection status of the workload,
	// on the cluster it is expected to be available on.
	ConditionProtected = "Protected"
)

const (
	ReasonProgressing = "Progressing"
	ReasonCleaning    = "Cleaning"
	ReasonSuccess     = "Success"
	ReasonNotStarted  = "NotStarted"
	ReasonPaused      = "Paused"
)

const (
	ReasonProtectedUnknown     = "Unknown"
	ReasonProtectedProgressing = "Progressing"
	ReasonProtectedError       = "Error"
	ReasonProtected            = "Protected"
)

type ProgressionStatus string

const (
	ProgressionCompleted                           = ProgressionStatus("Completed")
	ProgressionCreatingMW                          = ProgressionStatus("CreatingMW")
	ProgressionUpdatingPlRule                      = ProgressionStatus("UpdatingPlRule")
	ProgressionWaitForReadiness                    = ProgressionStatus("WaitForReadiness")
	ProgressionCleaningUp                          = ProgressionStatus("Cleaning Up")
	ProgressionWaitOnUserToCleanUp                 = ProgressionStatus("WaitOnUserToCleanUp")
	ProgressionCheckingFailoverPrequisites         = ProgressionStatus("CheckingFailoverPrequisites")
	ProgressionFailingOverToCluster                = ProgressionStatus("FailingOverToCluster")
	ProgressionWaitForFencing                      = ProgressionStatus("WaitForFencing")
	ProgressionWaitForStorageMaintenanceActivation = ProgressionStatus("WaitForStorageMaintenanceActivation")
	ProgressionPreparingFinalSync                  = ProgressionStatus("PreparingFinalSync")
	ProgressionClearingPlacement                   = ProgressionStatus("ClearingPlacement")
	ProgressionRunningFinalSync                    = ProgressionStatus("RunningFinalSync")
	ProgressionFinalSyncComplete                   = ProgressionStatus("FinalSyncComplete")
	ProgressionEnsuringVolumesAreSecondary         = ProgressionStatus("EnsuringVolumesAreSecondary")
	ProgressionWaitingForResourceRestore           = ProgressionStatus("WaitingForResourceRestore")
	ProgressionUpdatedPlacement                    = ProgressionStatus("UpdatedPlacement")
	ProgressionEnsuringVolSyncSetup                = ProgressionStatus("EnsuringVolSyncSetup")
	ProgressionSettingupVolsyncDest                = ProgressionStatus("SettingUpVolSyncDest")
	ProgressionDeleting                            = ProgressionStatus("Deleting")
	ProgressionDeleted                             = ProgressionStatus("Deleted")
	ProgressionActionPaused                        = ProgressionStatus("Paused")
)

// DRPlacementControlSpec defines the desired state of DRPlacementControl
type DRPlacementControlSpec struct {
	// PlacementRef is the reference to the PlacementRule used by DRPC
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="placementRef is immutable"
	PlacementRef v1.ObjectReference `json:"placementRef"`

	// ProtectedNamespaces is a list of namespaces that are protected by the DRPC.
	// Omitting this field means resources are only protected in the namespace controlled by the PlacementRef.
	// If this field is set, the PlacementRef and the DRPC must be in the RamenOpsNamespace as set in the Ramen Config.
	// If this field is set, the protected namespace resources are treated as unmanaged.
	// You can use a recipe to filter and coordinate the order of the resources that are protected.
	// +kubebuilder:validation:Optional
	ProtectedNamespaces *[]string `json:"protectedNamespaces,omitempty"`

	// DRPolicyRef is the reference to the DRPolicy participating in the DR replication for this DRPC
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="drPolicyRef is immutable"
	DRPolicyRef v1.ObjectReference `json:"drPolicyRef"`

	// PreferredCluster is the cluster name that the user preferred to run the application on
	PreferredCluster string `json:"preferredCluster,omitempty"`

	// FailoverCluster is the cluster name that the user wants to failover the application to.
	// If not sepcified, then the DRPC will select the surviving cluster from the DRPolicy
	FailoverCluster string `json:"failoverCluster,omitempty"`

	// Label selector to identify all the PVCs that need DR protection.
	// This selector is assumed to be the same for all subscriptions that
	// need DR protection. It will be passed in to the VRG when it is created
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="pvcSelector is immutable"
	PVCSelector metav1.LabelSelector `json:"pvcSelector"`

	// Action is either Failover or Relocate operation
	Action DRAction `json:"action,omitempty"`

	// +optional
	KubeObjectProtection *KubeObjectProtectionSpec `json:"kubeObjectProtection,omitempty"`
}

// PlacementDecision defines the decision made by controller
type PlacementDecision struct {
	ClusterName      string `json:"clusterName,omitempty"`
	ClusterNamespace string `json:"clusterNamespace,omitempty"`
}

// VRGResourceMeta represents the VRG resource.
type VRGResourceMeta struct {
	// Kind is the kind of the Kubernetes resource.
	Kind string `json:"kind"`

	// Name is the name of the Kubernetes resource.
	Name string `json:"name"`

	// Namespace is the namespace of the Kubernetes resource.
	Namespace string `json:"namespace"`

	// A sequence number representing a specific generation of the desired state.
	Generation int64 `json:"generation"`

	// List of PVCs that are protected by the VRG resource
	//+optional
	ProtectedPVCs []string `json:"protectedpvcs,omitempty"`

	// ResourceVersion is a value used to identify the version of the
	// VRG resource object
	//+optional
	ResourceVersion string `json:"resourceVersion,omitempty"`
}

// VRGConditions represents the conditions of the resources deployed on a
// managed cluster.
type VRGConditions struct {
	// ResourceMeta represents the VRG resoure.
	// +required
	ResourceMeta VRGResourceMeta `json:"resourceMeta,omitempty"`

	// Conditions represents the conditions of this resource on a managed cluster.
	// +required
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// DRPlacementControlStatus defines the observed state of DRPlacementControl
type DRPlacementControlStatus struct {
	Phase              DRState            `json:"phase,omitempty"`
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	ActionStartTime    *metav1.Time       `json:"actionStartTime,omitempty"`
	ActionDuration     *metav1.Duration   `json:"actionDuration,omitempty"`
	Progression        ProgressionStatus  `json:"progression,omitempty"`
	PreferredDecision  PlacementDecision  `json:"preferredDecision,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	ResourceConditions VRGConditions      `json:"resourceConditions,omitempty"`

	// LastUpdateTime is when was the last time a condition or the overall status was updated
	LastUpdateTime *metav1.Time `json:"lastUpdateTime,omitempty"`

	// lastGroupSyncTime is the time of the most recent successful synchronization of all PVCs
	//+optional
	LastGroupSyncTime *metav1.Time `json:"lastGroupSyncTime,omitempty"`

	// lastGroupSyncDuration is the longest time taken to sync
	// from the most recent successful synchronization of all PVCs
	//+optional
	LastGroupSyncDuration *metav1.Duration `json:"lastGroupSyncDuration,omitempty"`

	// lastGroupSyncBytes is the total bytes transferred from the most recent
	// successful synchronization of all PVCs
	//+optional
	LastGroupSyncBytes *int64 `json:"lastGroupSyncBytes,omitempty"`

	// lastKubeObjectProtectionTime is the time of the most recent successful kube object protection
	//+optional
	LastKubeObjectProtectionTime *metav1.Time `json:"lastKubeObjectProtectionTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:JSONPath=".metadata.creationTimestamp",name=Age,type=date
// +kubebuilder:printcolumn:JSONPath=".spec.preferredCluster",name=preferredCluster,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.failoverCluster",name=failoverCluster,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.action",name=desiredState,type=string
// +kubebuilder:printcolumn:JSONPath=".status.phase",name=currentState,type=string
// +kubebuilder:printcolumn:JSONPath=".status.progression",name=progression,type=string,priority=2
// +kubebuilder:printcolumn:JSONPath=".status.actionStartTime",name=start time,type=string,priority=2
// +kubebuilder:printcolumn:JSONPath=".status.actionDuration",name=duration,type=string,priority=2
// +kubebuilder:printcolumn:JSONPath=".status.conditions[0].status",name=available,type=string,priority=2
// +kubebuilder:printcolumn:JSONPath=".status.conditions[1].status",name=peer ready,type=string,priority=2
// +kubebuilder:printcolumn:JSONPath=".status.conditions[2].status",name=protected,type=string,priority=2
// +kubebuilder:resource:shortName=drpc

// DRPlacementControl is the Schema for the drplacementcontrols API
type DRPlacementControl struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DRPlacementControlSpec   `json:"spec,omitempty"`
	Status DRPlacementControlStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DRPlacementControlList contains a list of DRPlacementControl
type DRPlacementControlList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DRPlacementControl `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DRPlacementControl{}, &DRPlacementControlList{})
}
