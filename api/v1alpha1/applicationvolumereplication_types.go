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

// Action that will be taken by Ramen when a subscription is paused
// +kubebuilder:validation:Enum=Failover;Failback
type Action string

// These are the valid values for Action
const (
	// Failover, restore PVs to the new home cluster, and unpause subscription
	ActionFailover Action = "Failover"

	// Failback, restore PVs to the original home cluster, and unpause subscription
	ActionFailback Action = "Failback"
)

// DREnabledSubscriptionsMap defines the action used for failover per subscription.
// Key is subscription name. Value is either empty, 'failover', or 'failback'
type DREnabledSubscriptionsMap map[string]Action

// ApplicationVolumeReplicationSpec defines the desired state of ApplicationVolumeReplication
type ApplicationVolumeReplicationSpec struct {
	// DREnabledSubscriptions holds Subscription name as the key and an Action as the value
	DREnabledSubscriptions DREnabledSubscriptionsMap `json:"drEnabledSubscriptions,omitempty"`

	// Label selector to identify all the subscriptions that belong to an application that
	// needs DR protection. This selection is needed to select subscriptions on the hub
	SubscriptionSelector metav1.LabelSelector `json:"subscriptionSelector"`

	// Label selector to identify all the PVCs that need DR protection.
	// This selector is assumed to be the same for all subscriptions that
	// need DR protection. It will be passed in to the VRG when it is created
	PVCSelector metav1.LabelSelector `json:"pvcSelector"`

	// S3 Endpoint to replicate PV metadata; this is for all VRGs.
	// The value of this field, will be progated to every VRG.
	// See VRG spec for more details.
	S3Endpoint string `json:"s3Endpoint"`

	// S3 Region: https://docs.aws.amazon.com/general/latest/gr/rande.html
	S3Region string `json:"s3Region"`

	// Name of k8s secret that contains the credentials to access the S3 endpoint.
	// If S3Endpoint is used, also specify the k8s secret that contains the S3
	// access key id and secret access key set using the keys: AWS_ACCESS_KEY_ID
	// and AWS_SECRET_ACCESS_KEY.  The value of this field, will be progated to every VRG.
	// See VRG spec for more details.
	S3SecretName string `json:"s3SecretName"`
}

// DRState for each subscription
// +kubebuilder:validation:Enum=Initial;Failing-over;Failed-over;Failing-back;Failed-back
type DRState string

// These are the valid values for DRState
const (
	// Initial, this is the state that will be recorded in the AVR status
	// when initial deplyment has been performed successfully (per subscription)
	Initial DRState = "Initial"

	// FailingOver, state recorded in the AVR status per subscription when the failover
	// is initiated but has not been completed yet
	FailingOver DRState = "Failing-over"

	// FailedOver, state recorded in the AVR status per subscription when the failover
	// process has completed
	FailedOver DRState = "Failed-over"

	// FailingBack, state recorded in the AVR status per subscription when the failback
	// is initiated but has not been completed yet
	FailingBack DRState = "Failing-back"

	// FailedBack, state recorded in the AVR status per subscription when the failback
	// process has completed
	FailedBack DRState = "Failed-back"
)

// LastKnownDRStateMap defines per subscription the last state, key is subscription name
type LastKnownDRStateMap map[string]DRState

// SubscriptionPlacementDecision lists each subscription with its home and peer clusters
type SubscriptionPlacementDecision struct {
	HomeCluster     string `json:"homeCluster,omitempty"`
	PeerCluster     string `json:"peerCluster,omitempty"`
	PrevHomeCluster string `json:"prevHomeCluster,omitempty"`
}

// SubscriptionPlacementDecisionMap defines per subscription placement decision, key is subscription name
type SubscriptionPlacementDecisionMap map[string]*SubscriptionPlacementDecision

// ApplicationVolumeReplicationStatus defines the observed state of ApplicationVolumeReplication
type ApplicationVolumeReplicationStatus struct {
	Decisions         SubscriptionPlacementDecisionMap `json:"decisions,omitempty"`
	LastKnownDRStates LastKnownDRStateMap              `json:"lastKnownDRStates,omitempty"`
	LastUpdateTime    metav1.Time                      `json:"lastUpdateTime"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ApplicationVolumeReplication is the Schema for the applicationvolumereplications API
type ApplicationVolumeReplication struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ApplicationVolumeReplicationSpec   `json:"spec,omitempty"`
	Status ApplicationVolumeReplicationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ApplicationVolumeReplicationList contains a list of ApplicationVolumeReplication
type ApplicationVolumeReplicationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ApplicationVolumeReplication `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ApplicationVolumeReplication{}, &ApplicationVolumeReplicationList{})
}
