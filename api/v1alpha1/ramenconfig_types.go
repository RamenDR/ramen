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
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	cfg "sigs.k8s.io/controller-runtime/pkg/config/v1alpha1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ControllerType is the type of controller to run
// +kubebuilder:validation:Enum=dr-hub;dr-cluster
type ControllerType string

const (
	// DRCluster operates as the DR cluster controller on a peer cluster
	DRCluster ControllerType = "dr-cluster"

	// DRHub operates as the DR hub controller on a cluster managing DR across peer clusters
	DRHub ControllerType = "dr-hub"
)

// Definition of a S3 store profile that Ramen can use to replicate the etcd
// cluster data of PVs.  A single S3 store profile can be used by one or more
// VolumeReplicationGroup objects.  The name of the profile is maintained by the
// container of this profile.
type S3StoreProfile struct {
	// Name of this profile
	ProfileName string `json:"profileName"`

	// S3 compatible endpoint of this profile
	S3CompatibleEndpoint string `json:"s3CompatibleEndpoint"`

	// S3 Region: https://docs.aws.amazon.com/general/latest/gr/rande.html
	S3Region string `json:"s3Region,omitempty"`

	// Reference to the secret that contains the S3 access key id and s3 secret
	// access key with the keys AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY
	// respectively.
	S3SecretRef v1.SecretReference `json:"s3SecretRef"`
}

//+kubebuilder:object:root=true

// RamenConfig is the Schema for the ramenconfig API
type RamenConfig struct {
	metav1.TypeMeta `json:",inline"`

	// ControllerManagerConfigurationSpec returns the configurations for controllers
	cfg.ControllerManagerConfigurationSpec `json:",inline"`

	// RamenControllerType defines the type of controller to run
	RamenControllerType ControllerType `json:"ramenControllerType"`

	// Map of S3 store profiles
	S3StoreProfiles []S3StoreProfile `json:"s3StoreProfiles,omitempty"`
}

func init() {
	SchemeBuilder.Register(&RamenConfig{})
}
