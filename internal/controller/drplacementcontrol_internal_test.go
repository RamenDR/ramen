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

func TestSyncPeersAreOffloaded(t *testing.T) {
	t.Parallel()

	asyncVRG := &rmn.VolumeReplicationGroup{
		Spec: rmn.VolumeReplicationGroupSpec{
			Async: &rmn.VRGAsyncSpec{PeerClasses: []rmn.PeerClass{{Offloaded: true}}},
		},
	}

	tests := []struct {
		name string
		vrgs map[string]*rmn.VolumeReplicationGroup
		want bool
	}{
		{
			name: "no VRGs requires fencing",
			vrgs: map[string]*rmn.VolumeReplicationGroup{},
			want: false,
		},
		{
			name: "offloaded sync peers skip fencing",
			vrgs: map[string]*rmn.VolumeReplicationGroup{"c1": syncVRG(true)},
			want: true,
		},
		{
			name: "a non-offloaded sync peer requires fencing",
			vrgs: map[string]*rmn.VolumeReplicationGroup{"c1": syncVRG(false)},
			want: false,
		},
		{
			name: "one non-offloaded peer among offloaded ones requires fencing",
			vrgs: map[string]*rmn.VolumeReplicationGroup{"c1": syncVRG(true, false)},
			want: false,
		},
		{
			name: "every VRG must agree",
			vrgs: map[string]*rmn.VolumeReplicationGroup{
				"c1": syncVRG(true),
				"c2": syncVRG(false),
			},
			want: false,
		},
		{
			name: "a VRG without sync peers does not veto",
			vrgs: map[string]*rmn.VolumeReplicationGroup{
				"c1": syncVRG(true),
				"c2": syncVRG(),
			},
			want: true,
		},
		{
			name: "an empty sync peer list alone requires fencing",
			vrgs: map[string]*rmn.VolumeReplicationGroup{"c1": syncVRG()},
			want: false,
		},
		{
			name: "spec.sync nil requires fencing",
			vrgs: map[string]*rmn.VolumeReplicationGroup{"c1": {}},
			want: false,
		},
		{
			name: "spec.async is not read",
			vrgs: map[string]*rmn.VolumeReplicationGroup{"c1": asyncVRG},
			want: false,
		},
		{
			name: "a nil VRG is skipped",
			vrgs: map[string]*rmn.VolumeReplicationGroup{
				"c1": syncVRG(true),
				"c2": nil,
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := syncPeersAreOffloaded(tt.vrgs); got != tt.want {
				t.Errorf("syncPeersAreOffloaded() = %v, want %v", got, tt.want)
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

func syncVRG(offloaded ...bool) *rmn.VolumeReplicationGroup {
	peers := make([]rmn.PeerClass, len(offloaded))
	for i := range offloaded {
		peers[i] = rmn.PeerClass{Offloaded: offloaded[i]}
	}

	return &rmn.VolumeReplicationGroup{
		Spec: rmn.VolumeReplicationGroupSpec{
			Sync: &rmn.VRGSyncSpec{PeerClasses: peers},
		},
	}
}
