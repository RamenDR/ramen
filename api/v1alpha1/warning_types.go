// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

const (
	// WarningNonConsistencyGroupDeprecated is recorded while VolumeReplication
	// is used without a consistency group.
	WarningNonConsistencyGroupDeprecated = "NonConsistencyGroupDeprecated"
)

// Warning is one advisory recorded on a resource status.
// Name identifies the warning. Message explains it to a person.
// A resource may report more than one warning at a time.
type Warning struct {
	// name identifies this warning. It stays stable so a caller can match one
	// deprecation when several are present.
	Name string `json:"name"`

	// message is the human-readable explanation.
	Message string `json:"message"`
}
