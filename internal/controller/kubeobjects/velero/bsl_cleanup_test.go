// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package velero_test

import (
	"testing"
	"time"

	velero "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	veleropkg "github.com/ramendr/ramen/internal/controller/kubeobjects/velero"
)

func TestIsBSLStale(t *testing.T) {
	t.Parallel()

	bslTime := metav1.NewTime(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	backupTime := metav1.NewTime(bslTime.Add(time.Minute))

	tests := []struct {
		name   string
		phase  velero.BackupStorageLocationPhase
		backup *velero.Backup
		want   bool
	}{
		{"unavailable without backup", velero.BackupStorageLocationPhaseUnavailable, nil, true},
		{"empty phase without backup", "", nil, true},
		{"available without paired backup", velero.BackupStorageLocationPhaseAvailable, nil, true},
		{
			"available with backup created after BSL",
			velero.BackupStorageLocationPhaseAvailable,
			&velero.Backup{ObjectMeta: metav1.ObjectMeta{CreationTimestamp: backupTime}},
			false,
		},
		{
			"available with BSL created after backup",
			velero.BackupStorageLocationPhaseAvailable,
			&velero.Backup{ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(bslTime.Add(-time.Minute))}},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bsl := &velero.BackupStorageLocation{
				ObjectMeta: metav1.ObjectMeta{CreationTimestamp: bslTime},
				Status:     velero.BackupStorageLocationStatus{Phase: tt.phase},
			}
			if got := veleropkg.IsBSLStale(bsl, tt.backup); got != tt.want {
				t.Fatalf("IsBSLStale() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsBSLStale_AvailableWithOldValidationNotStaleWhenBackupPaired(t *testing.T) {
	t.Parallel()

	bslTime := metav1.NewTime(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	backupTime := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	old := metav1.Now()

	bsl := &velero.BackupStorageLocation{
		ObjectMeta: metav1.ObjectMeta{CreationTimestamp: bslTime},
		Status: velero.BackupStorageLocationStatus{
			Phase:              velero.BackupStorageLocationPhaseAvailable,
			LastValidationTime: &old,
		},
	}
	backup := &velero.Backup{ObjectMeta: metav1.ObjectMeta{CreationTimestamp: backupTime}}

	if veleropkg.IsBSLStale(bsl, backup) {
		t.Fatal("Available BSL with old LastValidationTime and valid backup pair must not be stale")
	}
}
