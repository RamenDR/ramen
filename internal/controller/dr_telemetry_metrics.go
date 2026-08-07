// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	rmn "github.com/ramendr/ramen/api/v1alpha1"
)

// drPolicyDRType classifies a DRPolicy as DRTypeMetro, DRTypeRegional or
// DRTypeUnknown for the ramen_dr_policy_type telemetry metric. It reuses
// dRPolicySupportsMetro so that the classification matches Ramen's
// operational Metro detection: status peerClasses when present, with a
// fallback to spec.schedulingInterval for policies without peerClasses.
func drPolicyDRType(drpolicy *rmn.DRPolicy) string {
	metro, _, err := dRPolicySupportsMetro(drpolicy, nil)
	if err != nil {
		return DRTypeUnknown
	}

	if metro {
		return DRTypeMetro
	}

	return DRTypeRegional
}
