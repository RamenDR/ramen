// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package velero_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	"github.com/ramendr/ramen/internal/controller/kubeobjects/velero"
)

func TestBSLSpec(t *testing.T) {
	secretKeyRef := &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: "s3-secret"},
		Key:                  "aws",
	}
	loc := kubeobjects.StorageLocation{
		S3URL:          "https://s3.example.com",
		Bucket:         "bucket",
		Region:         "us-east-1",
		KeyPrefix:      "ns/vrg/kube-objects/1/",
		SecretKeyRef:   secretKeyRef,
		CACertificates: []byte("ca-bundle"),
	}

	spec := velero.BSLSpec(loc, true)

	assert.Equal(t, "aws", spec.Provider)
	require.NotNil(t, spec.ObjectStorage)
	assert.Equal(t, "bucket", spec.ObjectStorage.Bucket)
	assert.Equal(t, "ns/vrg/kube-objects/1/velero/", spec.ObjectStorage.Prefix)
	assert.Equal(t, []byte("ca-bundle"), spec.ObjectStorage.CACert)
	assert.Equal(t, map[string]string{
		"region":            "us-east-1",
		"s3ForcePathStyle":  "true",
		"s3Url":             "https://s3.example.com",
		"checksumAlgorithm": "",
	}, spec.Config)
	assert.Same(t, secretKeyRef, spec.Credential)

	// With disableBackupSync=true (capture side), backup sync must be disabled so
	// Velero does not re-create the Backup CRs that Ramen deletes after each capture.
	require.NotNil(t, spec.BackupSyncPeriod)
	assert.Zero(t, spec.BackupSyncPeriod.Duration)
}

func TestBSLSpecBackupSyncEnabled(t *testing.T) {
	loc := kubeobjects.StorageLocation{
		S3URL:     "https://s3.example.com",
		Bucket:    "bucket",
		Region:    "us-east-1",
		KeyPrefix: "ns/vrg/kube-objects/1/",
	}

	spec := velero.BSLSpec(loc, false)

	// With disableBackupSync=false (recover side), backup sync must stay enabled so
	// Velero imports the dummy Backup CR status from S3; otherwise the backup fails
	// with "backup already exists in object storage" and the restore fails.
	assert.Nil(t, spec.BackupSyncPeriod)
}
