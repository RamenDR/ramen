// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package volsync

import (
	corev1 "k8s.io/api/core/v1"
)

var DefaultRsyncServiceType corev1.ServiceType = corev1.ServiceTypeClusterIP

var DefaultScheduleCronSpec = "*/10 * * * *" // Every 10 mins
