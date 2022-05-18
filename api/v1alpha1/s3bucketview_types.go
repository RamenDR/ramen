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

// S3BucketViewSpec defines the desired state of S3BucketView
type S3BucketViewSpec struct {
	// ProfileName is the name of the S3 profile in the Ramen operator config map
	// specifying the bucket to be queried
	ProfileName string `json:"profileName"`
}

// S3BucketViewStatus defines the observed state of S3BucketView
type S3BucketViewStatus struct {
	// SampleTime is a timestamp representing the node time when the specified
	// S3 bucket was last queried. It is represented in RFC3339 form and is in UTC.
	SampleTime metav1.Time `json:"sampleTime,omitempty"`

	// VolumeRelicationGroups is a list of VolumeReplicationGroup objects represented in
	// the specified S3 bucket when it was last queried.
	VolumeReplicationGroups []VolumeReplicationGroup `json:"volumeReplicationGroups,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Cluster

// S3BucketView is the Schema for the s3bucketviews API
type S3BucketView struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec S3BucketViewSpec `json:"spec,omitempty"`
	// +optional
	Status *S3BucketViewStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// S3BucketViewList contains a list of S3BucketView
type S3BucketViewList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []S3BucketView `json:"items"`
}

func init() {
	SchemeBuilder.Register(&S3BucketView{}, &S3BucketViewList{})
}
