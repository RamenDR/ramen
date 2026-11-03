// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"reflect"
	"testing"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
)

// TestCheckMetroSupportUsingPeerClass covers the metro cluster list that haveOverlappingMetroZones
// reads. A metro policy contributing no clusters lets two policies share a managed cluster.
func TestCheckMetroSupportUsingPeerClass(t *testing.T) {
	t.Parallel()

	names := map[string]string{"cid-1": "dr1", "cid-2": "dr2"}

	tests := []struct {
		name string
		peer rmn.PeerClass
		want [][]string
	}{
		{
			name: "an offloaded peer reports its clusters",
			peer: metroPeerClass(true, "sid-1", "sid-2"),
			want: [][]string{{"dr1", "dr2"}},
		},
		{
			name: "a non-offloaded peer reports its clusters",
			peer: metroPeerClass(false, "sid-1"),
			want: [][]string{{"dr1", "dr2"}},
		},
		{
			name: "an offloaded peer with a single storage ID still reports",
			peer: metroPeerClass(true, "sid-1"),
			want: [][]string{{"dr1", "dr2"}},
		},
		{
			name: "a non-offloaded peer with two storage IDs is skipped",
			peer: metroPeerClass(false, "sid-1", "sid-2"),
			want: [][]string{},
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			policy := &rmn.DRPolicy{}
			policy.Status.Sync.PeerClasses = []rmn.PeerClass{tt.peer}

			metro, clusters := checkMetroSupportUsingPeerClass(policy, names)
			if !metro {
				t.Fatal("checkMetroSupportUsingPeerClass() metro = false, want true")
			}

			if !reflect.DeepEqual(clusters, tt.want) {
				t.Errorf("clusters = %v, want %v", clusters, tt.want)
			}
		})
	}
}

// Test helpers

func metroPeerClass(offloaded bool, storageIDs ...string) rmn.PeerClass {
	return rmn.PeerClass{
		ClusterIDs:       []string{"cid-1", "cid-2"},
		StorageClassName: "sc",
		StorageID:        storageIDs,
		Offloaded:        offloaded,
	}
}
