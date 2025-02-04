// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const DefaultDRPolicyName = "dr-policy"

// nolint:unparam
func GetDRPolicy(client client.Client, name string) (*ramen.DRPolicy, error) {
	drpolicy := &ramen.DRPolicy{}
	key := types.NamespacedName{Name: name}

	err := client.Get(context.Background(), key, drpolicy)
	if err != nil {
		return nil, err
	}

	return drpolicy, nil
}
