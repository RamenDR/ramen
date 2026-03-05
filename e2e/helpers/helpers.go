// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

// package helpers provides testing helper functions.
package helpers

import (
	"testing"

	"github.com/aymanbagabas/go-udiff"
	"sigs.k8s.io/yaml"
)

func MarshalYAML(t *testing.T, a any) string {
	t.Helper()

	data, err := yaml.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func UnifiedDiff(t *testing.T, expected, actual any) string {
	t.Helper()

	expectedString := marshal(t, expected)
	actualString := marshal(t, actual)

	return udiff.Unified("expected", "actual", expectedString, actualString)
}

func marshal(t *testing.T, obj any) string {
	t.Helper()

	if s, ok := obj.(string); ok {
		return s
	}

	return MarshalYAML(t, obj)
}
