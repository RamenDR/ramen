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

// Managed cluster information
type ManagedCluster struct {
	// Name of this managed cluster as configured in OCM/ACM
	Name string `json:"name"`

	// S3 profile name (in Ramen config) to use as a source to restore PV
	// related cluster state during recovery or relocate actions of applications
	// to this managed cluster;  hence, this S3 profile should be available to
	// successfully move the workload to this managed cluster.  For applications
	// that are active on this managed cluster, their PV related cluster state
	// is stored to S3 profiles of all other managed clusters in the same
	// DRPolicy to enable recovery or relocate actions on those managed clusters.
	S3ProfileName string `json:"s3ProfileName"`
}

// DRPolicySpec defines the desired state of DRPolicy
type DRPolicySpec struct {
	// Important: Run "make" to regenerate code after modifying this file

	// scheduling Interval for replicating Persistent Volume
	// data to a peer cluster. Interval is typically in the
	// form <num><m,h,d>. Here <num> is a number, 'm' means
	// minutes, 'h' means hours and 'd' stands for days.
	SchedulingInterval string `json:"schedulingInterval"`

	// Label selector to identify all the VolumeReplicationClasses.
	// This selector is assumed to be the same for all subscriptions that
	// need DR protection. It will be passed in to the VRG when it is created
	//+optional
	ReplicationClassSelector metav1.LabelSelector `json:"replicationClassSelector,omitempty"`

	// The set of managed clusters governed by this policy, which have
	// replication relationship enabled between them.
	DRClusterSet []ManagedCluster `json:"drClusterSet"`
}

// DRPolicyStatus defines the observed state of DRPolicy
// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
// Important: Run "make" to regenerate code after modifying this file
type DRPolicyStatus struct{}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// DRPolicy is the Schema for the drpolicies API
type DRPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DRPolicySpec    `json:"spec,omitempty"`
	Status *DRPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DRPolicyList contains a list of DRPolicy
type DRPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DRPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DRPolicy{}, &DRPolicyList{})
}

// Return a list of unique S3 profiles to upload the relevant cluster state of
// the given home cluster
func (d *DRPolicy) S3UploadProfileList(homeCluster string) (s3ProfileList []string) {
	for _, drCluster := range d.Spec.DRClusterSet {
		if drCluster.Name != homeCluster {
			// This drCluster is not the home cluster and is hence, a candidate to
			// upload cluster state to if this S3 profile is not already on the list.
			found := false

			for _, s3ProfileName := range s3ProfileList {
				if s3ProfileName == drCluster.S3ProfileName {
					found = true
				}
			}

			if !found {
				s3ProfileList = append(s3ProfileList, drCluster.S3ProfileName)
			}
		}
	}

	return
}

// Return the S3 profile to download the relevant cluster state to the given
// home cluster
func (d *DRPolicy) S3DownloadProfile(homeCluster string) (s3Profile string) {
	for _, drCluster := range d.Spec.DRClusterSet {
		if drCluster.Name == homeCluster {
			s3Profile = drCluster.S3ProfileName
		}
	}

	return
}
