// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// nolint:unparam
func GetDRPolicy(cluster Cluster, name string) (*ramen.DRPolicy, error) {
	drpolicy := &ramen.DRPolicy{}
	key := k8stypes.NamespacedName{Name: name}

	err := cluster.Client.Get(context.Background(), key, drpolicy)
	if err != nil {
		return nil, err
	}

	return drpolicy, nil
}
