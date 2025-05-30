// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package types

import "fmt"

// GetCluster returns the cluster from the env that matches clusterName.
// If not found, it returns an empty Cluster and an error.
func (e *Env) GetCluster(clusterName string) (Cluster, error) {
	switch clusterName {
	case e.C1.Name:
		return e.C1, nil
	case e.C2.Name:
		return e.C2, nil
	case e.Hub.Name:
		return e.Hub, nil
	default:
		return Cluster{}, fmt.Errorf("cluster %q not found in environment", clusterName)
	}
}
