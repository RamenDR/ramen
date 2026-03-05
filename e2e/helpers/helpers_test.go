// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package helpers_test

import (
	"testing"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/helpers"
)

func TestUnifiedDiff(t *testing.T) {
	t.Run("strings", func(t *testing.T) {
		a := `line 1
line 2
`
		b := `line 1
modified
`
		actualDiff := helpers.UnifiedDiff(t, a, b)

		expectedDiff := `--- expected
+++ actual
@@ -1,2 +1,2 @@
 line 1
-line 2
+modified
`
		if actualDiff != expectedDiff {
			t.Fatalf("expected:\n%s\ngot:\n%s", expectedDiff, actualDiff)
		}
	})

	t.Run("equal objects", func(t *testing.T) {
		a := config.PVCSpec{
			Name:             "rbd",
			StorageClassName: "rbd-ceph-block",
			AccessModes:      "ReadWriteOnce",
		}
		b := a

		actualDiff := helpers.UnifiedDiff(t, a, b)

		expectedDiff := ""

		if actualDiff != expectedDiff {
			t.Fatalf("expected:\n%s\ngot:\n%s", expectedDiff, actualDiff)
		}
	})

	t.Run("different objects", func(t *testing.T) {
		a := config.PVCSpec{
			Name:             "rbd",
			StorageClassName: "rbd-ceph-block",
			AccessModes:      "ReadWriteOnce",
		}
		b := a
		b.StorageClassName = "rbd-cephfs"

		actualDiff := helpers.UnifiedDiff(t, a, b)

		expectedDiff := `--- expected
+++ actual
@@ -1,3 +1,3 @@
 accessModes: ReadWriteOnce
 name: rbd
-storageClassName: rbd-ceph-block
+storageClassName: rbd-cephfs
`

		if actualDiff != expectedDiff {
			t.Fatalf("expected:\n%s\ngot:\n%s", expectedDiff, actualDiff)
		}
	})

	t.Run("nill", func(t *testing.T) {
		a := config.PVCSpec{
			Name:             "rbd",
			StorageClassName: "rbd-ceph-block",
			AccessModes:      "ReadWriteOnce",
		}
		actualDiff := helpers.UnifiedDiff(t, &a, nil)

		expectedDiff := `--- expected
+++ actual
@@ -1,3 +1 @@
-accessModes: ReadWriteOnce
-name: rbd
-storageClassName: rbd-ceph-block
+null
`
		if actualDiff != expectedDiff {
			t.Fatalf("expected:\n%s\ngot:\n%s", expectedDiff, actualDiff)
		}
	})
}
