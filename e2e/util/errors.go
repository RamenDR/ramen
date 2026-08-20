// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import "errors"

// ErrUnrecoverable marks a health check failure that should not be retried.
var ErrUnrecoverable = errors.New("unrecoverable error")
