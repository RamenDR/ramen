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
	// DRClusterType operates as the DR cluster controller on a peer cluster
	DRClusterType ControllerType = "dr-cluster"

	// DRHubType operates as the DR hub controller on a cluster managing DR across peer clusters
	DRHubType ControllerType = "dr-hub"
)

// When naming a S3 bucket, follow the bucket naming rules at:
// https://docs.aws.amazon.com/AmazonS3/latest/userguide/bucketnamingrules.html
// - Bucket names must be between 3 and 63 characters long.
// - Bucket names can consist only of lowercase letters, numbers, dots (.), and hyphens (-).
// - Bucket names must begin and end with a letter or number.
// - Bucket names must not be formatted as an IP address (for example, 192.168.5.4).
// - Bucket names must be unique within a partition. A partition is a grouping of Regions.
// - Buckets used with Amazon S3 Transfer Acceleration can't have dots (.) in their names.

// Profile of a S3 compatible store to replicate the relevant Kubernetes cluster
// state (in etcd), such as PV state, across clusters protected by Ramen.
// - DRProtectionControl and VolumeReplicationGroup objects specify the S3
//   profile that should be used to protect the cluster state of the relevant
//   PVs.
// - A single S3 store profile can be used by multiple DRProtectionControl and
//   VolumeReplicationGroup objects.
// - See DRPolicy type for additional details about S3 configuration options
type S3StoreProfile struct {
	// Name of this S3 profile
	S3ProfileName string `json:"s3ProfileName"`

	// Name of the S3 bucket to protect and recover PV related cluster-data of
	// subscriptions protected by this DR policy.  This S3 bucket name is used
	// across all DR policies that use this S3 profile. Objects deposited in
	// this bucket are prefixed with the namespace-qualified name of the VRG to
	// uniquely identify objects of a particular subscription (an instance of an
	// application).  A single S3 bucket at a given endpoint may be shared by
	// multiple DR placements that are concurrently active in a given hub.
	// However, sharing an S3 bucket across multiple hub clusters can cause
	// object key name conflicts of cluster data uploaded to the bucket,
	// resulting in undefined and undesired side-effects. Hence, do not share an
	// S3 bucket at a given S3 endpoint across multiple hub clusters.  Bucket
	// name should follow AWS bucket naming rules:
	// https://docs.aws.amazon.com/AmazonS3/latest/userguide/bucketnamingrules.html
	S3Bucket string `json:"s3Bucket"`

	// S3 compatible endpoint of the object store of this S3 profile
	S3CompatibleEndpoint string `json:"s3CompatibleEndpoint"`

	// S3 Region; the AWS go client SDK does not have a default region; hence,
	// this is a mandatory field.
	// https://docs.aws.amazon.com/sdk-for-go/v1/developer-guide/configuring-sdk.html
	S3Region string `json:"s3Region"`

	// Reference to the secret that contains the S3 access key id and s3 secret
	// access key with the keys AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY
	// respectively.
	S3SecretRef v1.SecretReference `json:"s3SecretRef"`
	//+optional
	VeleroNamespaceSecretName string `json:"VeleroNamespaceSecretName,omitempty"`
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

	// MaxConcurrentReconciles is the maximum number of concurrent Reconciles which can be run.
	// Defaults to 1.
	MaxConcurrentReconciles int `json:",omitempty"`

	// dr-cluster operator deployment/undeployment automation configuration
	DrClusterOperator struct {
		// dr-cluster operator deployment/undeployment automation enabled
		DeploymentAutomationEnabled bool `json:"deploymentAutomationEnabled,omitempty"`

		// Enable s3 secret distribution and management across dr-clusters
		S3SecretDistributionEnabled bool `json:"s3SecretDistributionEnabled,omitempty"`

		// channel name
		ChannelName string `json:"channelName,omitempty"`

		// package name
		PackageName string `json:"packageName,omitempty"`

		// namespace name
		NamespaceName string `json:"namespaceName,omitempty"`

		// catalog source name
		CatalogSourceName string `json:"catalogSourceName,omitempty"`

		// catalog source namespace name
		CatalogSourceNamespaceName string `json:"catalogSourceNamespaceName,omitempty"`

		// cluster service version name
		ClusterServiceVersionName string `json:"clusterServiceVersionName,omitempty"`
	} `json:"drClusterOperator,omitempty"`

	// VolSync configuration
	VolSync struct {
		// Disabled is used to disable VolSync usage in Ramen. Defaults to false.
		Disabled bool `json:"disabled,omitempty"`

		// Default cephFS CSIDriver name used to enable ROX volumes. If this name matches
		// the PVC's storageclass provisioner, a new storageclass will be created and the
		// name of it passed to VolSync alongside the readOnly flag access mode.
		CephFSCSIDriverName string `json:"cephFSCSIDriverName,omitempty"`
	} `json:"volSync,omitempty"`

	KubeObjectProtection struct {
		// Disabled is used to disable KubeObjectProtection usage in Ramen.
		Disabled bool `json:"disabled,omitempty"`
		// Velero namespace input
		VeleroNamespaceName string `json:"veleroNamespaceName,omitempty"`
	} `json:"kubeObjectProtection,omitempty"`
}

func init() {
	SchemeBuilder.Register(&RamenConfig{})
}
