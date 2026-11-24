// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers //nolint:testpackage

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
)

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

// checkMetroSupportUsingPeerClass builds the metro cluster list that haveOverlappingMetroZones
// reads. A metro policy contributing no clusters lets two policies share a managed cluster.
var _ = Describe("checkMetroSupportUsingPeerClass", func() {
	names := map[string]string{"cid-1": "dr1", "cid-2": "dr2"}

	DescribeTable("reports the clusters of a sync policy",
		func(peer rmn.PeerClass, want [][]string) {
			policy := &rmn.DRPolicy{}
			policy.Status.Sync.PeerClasses = []rmn.PeerClass{peer}

			metro, clusters := checkMetroSupportUsingPeerClass(policy, names)

			Expect(metro).To(BeTrue())
			Expect(clusters).To(Equal(want))
		},
		Entry("an offloaded peer reports its clusters",
			metroPeerClass(true, "sid-1", "sid-2"), [][]string{{"dr1", "dr2"}}),
		Entry("a non-offloaded peer reports its clusters",
			metroPeerClass(false, "sid-1"), [][]string{{"dr1", "dr2"}}),
		Entry("an offloaded peer with a single storage ID still reports",
			metroPeerClass(true, "sid-1"), [][]string{{"dr1", "dr2"}}),
		Entry("a non-offloaded peer with two storage IDs is skipped",
			metroPeerClass(false, "sid-1", "sid-2"), [][]string{}),
	)
})

var _ = Describe("syncPeersAreOffloaded", func() {
	asyncVRG := &rmn.VolumeReplicationGroup{
		Spec: rmn.VolumeReplicationGroupSpec{
			Async: &rmn.VRGAsyncSpec{PeerClasses: []rmn.PeerClass{{Offloaded: true}}},
		},
	}

	DescribeTable("decides whether fencing can be skipped",
		func(vrgs map[string]*rmn.VolumeReplicationGroup, want bool) {
			Expect(syncPeersAreOffloaded(vrgs)).To(Equal(want))
		},
		Entry("no VRGs requires fencing",
			map[string]*rmn.VolumeReplicationGroup{}, false),
		Entry("offloaded sync peers skip fencing",
			map[string]*rmn.VolumeReplicationGroup{"c1": syncVRG(true)}, true),
		Entry("a non-offloaded sync peer requires fencing",
			map[string]*rmn.VolumeReplicationGroup{"c1": syncVRG(false)}, false),
		Entry("one non-offloaded peer among offloaded ones requires fencing",
			map[string]*rmn.VolumeReplicationGroup{"c1": syncVRG(true, false)}, false),
		Entry("every VRG must agree",
			map[string]*rmn.VolumeReplicationGroup{
				"c1": syncVRG(true),
				"c2": syncVRG(false),
			}, false),
		Entry("a VRG without sync peers does not veto",
			map[string]*rmn.VolumeReplicationGroup{
				"c1": syncVRG(true),
				"c2": syncVRG(),
			}, true),
		Entry("an empty sync peer list alone requires fencing",
			map[string]*rmn.VolumeReplicationGroup{"c1": syncVRG()}, false),
		Entry("spec.sync nil requires fencing",
			map[string]*rmn.VolumeReplicationGroup{"c1": {}}, false),
		Entry("spec.async is not read",
			map[string]*rmn.VolumeReplicationGroup{"c1": asyncVRG}, false),
		Entry("a nil VRG is skipped",
			map[string]*rmn.VolumeReplicationGroup{
				"c1": syncVRG(true),
				"c2": nil,
			}, true),
	)
})
