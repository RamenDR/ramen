// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package velero_test

import (
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
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

	spec := velero.BSLSpec(loc)

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

	// Backup sync must stay disabled so Velero does not re-create the Backup CRs
	// that Ramen deletes after each capture.
	require.NotNil(t, spec.BackupSyncPeriod)
	assert.Zero(t, spec.BackupSyncPeriod.Duration)
}

type recordedLog struct {
	msg  string
	keys map[string]any
}

type recordingSink struct {
	logs []recordedLog
}

func (*recordingSink) Init(logr.RuntimeInfo) {
	// LogSink requires Init. Status logging does not use it.
}

func (*recordingSink) Enabled(int) bool { return true }

func (s *recordingSink) Info(_ int, msg string, keysAndValues ...any) {
	s.logs = append(s.logs, recordedLog{msg: msg, keys: keyValues(keysAndValues)})
}

func (*recordingSink) Error(error, string, ...any) {
	// Status logging does not record errors.
}

func (s *recordingSink) WithValues(...any) logr.LogSink { return s }

func (s *recordingSink) WithName(string) logr.LogSink { return s }

func keyValues(keysAndValues []any) map[string]any {
	keys := make(map[string]any, len(keysAndValues)/2)
	for i := 0; i+1 < len(keysAndValues); i += 2 {
		key, ok := keysAndValues[i].(string)
		if !ok {
			continue
		}

		keys[key] = keysAndValues[i+1]
	}

	return keys
}

func (s *recordingSink) keys(msg string) map[string]any {
	for _, entry := range s.logs {
		if entry.msg == msg {
			return entry.keys
		}
	}

	return nil
}

func TestBackupStatusLogCounters(t *testing.T) {
	backup := &veleroapi.Backup{}
	backup.Status.VolumeSnapshotsAttempted = 1
	backup.Status.VolumeSnapshotsCompleted = 2
	backup.Status.CSIVolumeSnapshotsAttempted = 3
	backup.Status.CSIVolumeSnapshotsCompleted = 4
	backup.Status.BackupItemOperationsAttempted = 5
	backup.Status.BackupItemOperationsCompleted = 6
	backup.Status.BackupItemOperationsFailed = 7
	backup.Status.QueuePosition = 8
	backup.Status.HookStatus = &veleroapi.HookStatus{HooksAttempted: 9, HooksFailed: 10}

	sink := &recordingSink{}
	velero.BackupStatusLog(backup, logr.New(sink))

	keys := sink.keys("Backup")
	require.NotNil(t, keys)
	assert.Equal(t, 1, keys["volume snapshots attempted"])
	assert.Equal(t, 2, keys["volume snapshots completed"])
	assert.Equal(t, 3, keys["csi volume snapshots attempted"])
	assert.Equal(t, 4, keys["csi volume snapshots completed"])
	assert.Equal(t, 5, keys["backup item operations attempted"])
	assert.Equal(t, 6, keys["backup item operations completed"])
	assert.Equal(t, 7, keys["backup item operations failed"])
	assert.Equal(t, 8, keys["queue position"])

	hookKeys := sink.keys("Backup hooks")
	require.NotNil(t, hookKeys)
	assert.Equal(t, 9, hookKeys["hooks attempted"])
	assert.Equal(t, 10, hookKeys["hooks failed"])
}

func TestBackupStatusLogNilProgressAndHookStatus(t *testing.T) {
	sink := &recordingSink{}
	velero.BackupStatusLog(&veleroapi.Backup{}, logr.New(sink))

	keys := sink.keys("Backup")
	require.NotNil(t, keys)
	assert.Equal(t, 0, keys["volume snapshots attempted"])
	assert.Equal(t, 0, keys["queue position"])
	assert.Nil(t, sink.keys("Backup hooks"))
	assert.Nil(t, sink.keys("Items"))
}

func TestRestoreStatusLogCounters(t *testing.T) {
	restore := &veleroapi.Restore{}
	restore.Status.RestoreItemOperationsAttempted = 1
	restore.Status.RestoreItemOperationsCompleted = 2
	restore.Status.RestoreItemOperationsFailed = 3
	restore.Status.HookStatus = &veleroapi.HookStatus{HooksAttempted: 4, HooksFailed: 5}

	sink := &recordingSink{}
	velero.RestoreStatusLog(restore, logr.New(sink))

	keys := sink.keys("Restore")
	require.NotNil(t, keys)
	assert.Equal(t, 1, keys["restore item operations attempted"])
	assert.Equal(t, 2, keys["restore item operations completed"])
	assert.Equal(t, 3, keys["restore item operations failed"])

	hookKeys := sink.keys("Restore hooks")
	require.NotNil(t, hookKeys)
	assert.Equal(t, 4, hookKeys["hooks attempted"])
	assert.Equal(t, 5, hookKeys["hooks failed"])
}

func TestRestoreStatusLogNilProgressAndHookStatus(t *testing.T) {
	sink := &recordingSink{}
	velero.RestoreStatusLog(&veleroapi.Restore{}, logr.New(sink))

	keys := sink.keys("Restore")
	require.NotNil(t, keys)
	assert.Equal(t, 0, keys["restore item operations attempted"])
	assert.Nil(t, sink.keys("Restore hooks"))
	assert.Nil(t, sink.keys("Items"))
}
