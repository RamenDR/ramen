// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/s3"
)

const (
	// S3StoreMetadataKey is the S3 object key for the metadata file
	S3StoreMetadataKey = ".metadata"

	// S3StoreSchemaVersion is the current schema version
	S3StoreSchemaVersion = "v1"
)

// S3StoreMetadata is a small manifest at <namespace>/<vrg>/.metadata in each S3 profile.
// Schema version v1 carries only schemaVersion and clusterID (primary managed cluster).
// Cluster data protection is not duplicated here; use the VRG ClusterDataProtected condition.
// Consumers use this manifest to decide whether PV/PVC/VRG/VGR/VR kube manifests need a full
// re-upload (e.g. after an empty bucket, manual deletion of .metadata, or schema bump).
type S3StoreMetadata struct {
	// SchemaVersion is the format version of this file (for migrations).
	SchemaVersion string `json:"schemaVersion"`

	// ClusterID is the managed cluster name where the workload is primary
	// (from VRG destination-cluster annotation when set).
	ClusterID string `json:"clusterID"`
}

// NeedsS3ClusterDataResync reports whether cluster-data manifests should be treated as missing
// in S3: no metadata file at this prefix (new/empty bucket or first upload), or stored schema
// is older than the operator expects. Endpoint/profile cosmetic changes do not trigger resync
// when the same bucket still holds the same keys and backup data.
func NeedsS3ClusterDataResync(metadata *S3StoreMetadata) (bool, string) {
	if metadata == nil {
		return true, "metadata not found (first upload or empty store after S3 recreation)"
	}

	if metadata.SchemaVersion != S3StoreSchemaVersion {
		return true, fmt.Sprintf("schema version mismatch: store=%s, current=%s",
			metadata.SchemaVersion, S3StoreSchemaVersion)
	}

	return false, ""
}

// NewS3StoreMetadata returns metadata with the current schema and primary cluster ID.
func NewS3StoreMetadata(clusterID string) *S3StoreMetadata {
	return &S3StoreMetadata{
		SchemaVersion: S3StoreSchemaVersion,
		ClusterID:     clusterID,
	}
}

// ============================================================================
// REPOSITORY - Handles S3 persistence
// ============================================================================

// S3MetadataRepository manages S3 metadata persistence
type S3MetadataRepository struct {
	objectStore ObjectStorer
	pathPrefix  string
}

// NewS3MetadataRepository creates a new repository
func NewS3MetadataRepository(objectStore ObjectStorer, vrgNamespace, vrgName string) *S3MetadataRepository {
	return &S3MetadataRepository{
		objectStore: objectStore,
		pathPrefix:  s3PathNamePrefix(vrgNamespace, vrgName),
	}
}

// Get retrieves metadata from S3
func (r *S3MetadataRepository) Get() (*S3StoreMetadata, bool, error) {
	metadataKey := r.pathPrefix + S3StoreMetadataKey

	var metadata S3StoreMetadata

	err := r.objectStore.DownloadObject(metadataKey, &metadata)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || isAwsErrCodeNoSuchKey(err) {
			return nil, false, nil
		}

		return nil, false, fmt.Errorf("failed to download metadata: %w", err)
	}

	return &metadata, true, nil
}

// Create creates new metadata in S3
func (r *S3MetadataRepository) Create(metadata *S3StoreMetadata) error {
	metadataKey := r.pathPrefix + S3StoreMetadataKey

	exists, err := r.objectStore.ObjectExists(metadataKey)
	if err != nil {
		return fmt.Errorf("failed to check existing metadata: %w", err)
	}

	if exists {
		return fmt.Errorf("metadata already exists")
	}

	return r.save(metadata)
}

// Update updates existing metadata in S3 (creates if not exists)
func (r *S3MetadataRepository) Update(metadata *S3StoreMetadata) error {
	return r.save(metadata)
}

// Save saves metadata to S3 (internal)
func (r *S3MetadataRepository) save(metadata *S3StoreMetadata) error {
	metadataKey := r.pathPrefix + S3StoreMetadataKey

	if err := r.objectStore.UploadObject(metadataKey, metadata); err != nil {
		return fmt.Errorf("failed to upload metadata: %w", err)
	}

	return nil
}

// Delete removes metadata from S3
func (r *S3MetadataRepository) Delete() error {
	metadataKey := r.pathPrefix + S3StoreMetadataKey

	if err := r.objectStore.DeleteObject(metadataKey); err != nil {
		return fmt.Errorf("failed to delete metadata: %w", err)
	}

	return nil
}

func isAwsErrCodeNoSuchKey(err error) bool {
	if err == nil {
		return false
	}

	if awsErr, ok := err.(awserr.Error); ok {
		return awsErr.Code() == s3.ErrCodeNoSuchKey
	}

	var awsErr awserr.Error
	if errors.As(err, &awsErr) {
		return awsErr.Code() == s3.ErrCodeNoSuchKey
	}

	errMsg := err.Error()

	return errors.Is(err, fs.ErrNotExist) ||
		strings.Contains(errMsg, "NoSuchKey") ||
		strings.Contains(errMsg, "no such key")
}
