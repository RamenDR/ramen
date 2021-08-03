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
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DRAction which will be either a failover or failback action
// +kubebuilder:validation:Enum=Failover;Failback;Relocate
type DRAction string

// These are the valid values for DRAction
const (
	// Failover, restore PVs to the TargetCluster
	ActionFailover DRAction = "Failover"

	// Failback, restore PVs to the PreferredCluster
	ActionFailback DRAction = "Failback"

	// Relocate, restore PVs to the designated TargetCluster.  PreferredCluster will change
	// to be the TargetCluster.
	ActionRelocate DRAction = "Relocate"
)

// DRPlacementControlSpec defines the desired state of DRPlacementControl
type DRPlacementControlSpec struct {
	// PlacementRef is the reference to the PlacementRule used by DRPC
	PlacementRef v1.ObjectReference `json:"placementRef"`

	// DRPolicyRef is the reference to the DRPolicy participating in the DR replication for this DRPC
	DRPolicyRef v1.ObjectReference `json:"drPolicyRef"`

	// PreferredCluster is the cluster name that the user preferred to run the application on
	PreferredCluster string `json:"preferredCluster,omitempty"`

	// FailoverCluster is the cluster name that the user wants to failover the application to.
	// If not sepcified, then the DRPC will select the surviving cluster from the DRPolicy
	FailoverCluster string `json:"failoverCluster,omitempty"`

	// Label selector to identify all the PVCs that need DR protection.
	// This selector is assumed to be the same for all subscriptions that
	// need DR protection. It will be passed in to the VRG when it is created
	PVCSelector metav1.LabelSelector `json:"pvcSelector"`

	// Action is either failover or failback operation
	Action DRAction `json:"action,omitempty"`
}

// DRState for keeping track of the DR placement
// +kubebuilder:validation:Enum=Initial;Failing-over;Failed-over;Failing-back;Failed-back;Relocating;Relocated
type DRState string

// These are the valid values for DRState
const (
	// Initial, this is the state that will be recorded in the DRPC status
	// when initial deplyment has been performed successfully
	Initial DRState = "Initial"

	// FailingOver, state recorded in the DRPC status when the failover
	// is initiated but has not been completed yet
	FailingOver DRState = "Failing-over"

	// FailedOver, state recorded in the DRPC status when the failover
	// process has completed
	FailedOver DRState = "Failed-over"

	// FailingBack, state recorded in the DRPC status when the failback
	// is initiated but has not been completed yet
	FailingBack DRState = "Failing-back"

	// FailedBack, state recorded in the DRPC status when the failback
	// process has completed
	FailedBack DRState = "Failed-back"

	// TODO: implement the relocation
	Relocating DRState = "Relocating"
	Relocated  DRState = "Relocated"
)

// DRPlacementControlStatus defines the observed state of DRPlacementControl
type DRPlacementControlStatus struct {
	PreferredDecision plrv1.PlacementDecision `json:"preferredDecision,omitempty"`
	LastKnownDRState  DRState                 `json:"lastKnownDRState,omitempty"`
	LastUpdateTime    metav1.Time             `json:"lastUpdateTime"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
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
