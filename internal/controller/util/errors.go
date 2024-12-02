// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0package util

package util

import "errors"

// OperationInProgress error is return when an operation is in progress and we wait for the desired state. The error
// string should describe the operation for logging the error.
type OperationInProgress string

func (e OperationInProgress) Error() string { return string(e) }

// Called by errors.Is() to match target.
func (OperationInProgress) Is(target error) bool {
	_, ok := target.(OperationInProgress)

	return ok
}

// IsOperationInProgress returns true if err or error wrapped by it is an OperationInProgress error.
func IsOperationInProgress(err error) bool {
	return errors.Is(err, OperationInProgress(""))
}
