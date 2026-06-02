// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"sort"

	volrep "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
)

const (
	nodeAffinityTestProvisioner = "test.csi.example.com"
	nodeAffinityTestSchedule    = "5m"
	nodeAffinityTestZoneKey     = "topology.kubernetes.io/zone"
	nodeAffinityTestRegionKey   = "topology.kubernetes.io/region"
)

// nodeAffinityTestVRG builds a VRGInstance with a pre-populated VolumeReplicationClass list, so that
// replication class selection does not need a client.
func nodeAffinityTestVRG(async bool, vrcs ...volrep.VolumeReplicationClass) *VRGInstance {
	v := &VRGInstance{
		log: logr.Discard(),
		instance: &ramendrv1alpha1.VolumeReplicationGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "vrg", Namespace: "vrg-ns"},
		},
		replClassList:    &volrep.VolumeReplicationClassList{Items: vrcs},
		grpReplClassList: &volrep.VolumeGroupReplicationClassList{},
		vrcUpdated:       true,
	}

	if async {
		v.instance.Spec.Async = &ramendrv1alpha1.VRGAsyncSpec{SchedulingInterval: nodeAffinityTestSchedule}
	}

	return v
}

func nodeAffinityTestVRC(topology string) volrep.VolumeReplicationClass {
	params := map[string]string{ReplicationClassScheduleKey: nodeAffinityTestSchedule}
	if topology != "" {
		params[VRCParameterAccessibleTopology] = topology
	}

	return volrep.VolumeReplicationClass{
		ObjectMeta: metav1.ObjectMeta{Name: "vrc"},
		Spec: volrep.VolumeReplicationClassSpec{
			Provisioner: nodeAffinityTestProvisioner,
			Parameters:  params,
		},
	}
}

func nodeAffinityTestSC() *storagev1.StorageClass {
	return &storagev1.StorageClass{
		ObjectMeta:  metav1.ObjectMeta{Name: "sc"},
		Provisioner: nodeAffinityTestProvisioner,
	}
}

func inTerm(kv ...string) corev1.NodeSelectorTerm {
	term := corev1.NodeSelectorTerm{}

	for i := 0; i+1 < len(kv); i += 2 {
		term.MatchExpressions = append(term.MatchExpressions, corev1.NodeSelectorRequirement{
			Key:      kv[i],
			Operator: corev1.NodeSelectorOpIn,
			Values:   []string{kv[i+1]},
		})
	}

	return term
}

func requiredAffinity(terms ...corev1.NodeSelectorTerm) *corev1.VolumeNodeAffinity {
	return &corev1.VolumeNodeAffinity{
		Required: &corev1.NodeSelector{NodeSelectorTerms: terms},
	}
}

// sortNodeAffinityExprs sorts match expressions within each term by key, as they are generated from
// map iteration and their order is not deterministic.
func sortNodeAffinityExprs(affinity *corev1.VolumeNodeAffinity) {
	if affinity == nil || affinity.Required == nil {
		return
	}

	for i := range affinity.Required.NodeSelectorTerms {
		exprs := affinity.Required.NodeSelectorTerms[i].MatchExpressions
		sort.Slice(exprs, func(a, b int) bool { return exprs[a].Key < exprs[b].Key })
	}
}

func expectNodeAffinity(got, want *corev1.VolumeNodeAffinity) {
	GinkgoHelper()

	sortNodeAffinityExprs(got)
	sortNodeAffinityExprs(want)

	if want == nil {
		Expect(got).To(BeNil())

		return
	}

	Expect(got).To(Equal(want))
}

var _ = Describe("VRG volrep node affinity", func() {
	DescribeTable("accessibleTopologyToNodeAffinity",
		func(topology string, want *corev1.VolumeNodeAffinity, wantErr bool) {
			got, err := accessibleTopologyToNodeAffinity(topology)
			if wantErr {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).NotTo(HaveOccurred())
			}

			expectNodeAffinity(got, want)
		},
		Entry("invalid json", `{"zone":`, nil, true),
		Entry("not a list", `{"`+nodeAffinityTestZoneKey+`":"z1"}`, nil, true),
		Entry("empty list", `[]`, nil, false),
		Entry("only empty segments", `[{},{}]`, nil, false),
		Entry("single segment single key",
			`[{"`+nodeAffinityTestZoneKey+`":"z1"}]`,
			requiredAffinity(inTerm(nodeAffinityTestZoneKey, "z1")),
			false,
		),
		Entry("single segment multiple keys are ANDed in one term",
			`[{"`+nodeAffinityTestZoneKey+`":"z1","`+nodeAffinityTestRegionKey+`":"r1"}]`,
			requiredAffinity(inTerm(nodeAffinityTestRegionKey, "r1", nodeAffinityTestZoneKey, "z1")),
			false,
		),
		Entry("multiple segments are ORed as separate terms, empty segments skipped",
			`[{"`+nodeAffinityTestZoneKey+`":"z1"},{},{"`+nodeAffinityTestZoneKey+`":"z2"}]`,
			requiredAffinity(inTerm(nodeAffinityTestZoneKey, "z1"), inTerm(nodeAffinityTestZoneKey, "z2")),
			false,
		),
	)

	Describe("updatePVNodeAffinityForRestore", func() {
		topology := `[{"` + nodeAffinityTestZoneKey + `":"z1"}]`
		existing := requiredAffinity(inTerm("kubernetes.io/hostname", "node-a"))

		DescribeTable("updates the restored PV node affinity",
			func(async bool, vrcs []volrep.VolumeReplicationClass, want *corev1.VolumeNodeAffinity, wantErr bool) {
				v := nodeAffinityTestVRG(async, vrcs...)
				pv := &corev1.PersistentVolume{
					ObjectMeta: metav1.ObjectMeta{Name: "pv"},
					Spec:       corev1.PersistentVolumeSpec{NodeAffinity: existing.DeepCopy()},
				}

				err := v.updatePVNodeAffinityForRestore(pv, nodeAffinityTestSC())
				if wantErr {
					Expect(err).To(HaveOccurred())
				} else {
					Expect(err).NotTo(HaveOccurred())
				}

				expectNodeAffinity(pv.Spec.NodeAffinity, want.DeepCopy())
			},
			Entry("sync VRG leaves affinity untouched",
				false, []volrep.VolumeReplicationClass{nodeAffinityTestVRC(topology)}, existing, false),
			Entry("no matching VRC is not an error",
				true, nil, existing, false),
			Entry("VRC without topology leaves affinity untouched",
				true, []volrep.VolumeReplicationClass{nodeAffinityTestVRC("")}, existing, false),
			Entry("VRC with empty topology list leaves affinity untouched",
				true, []volrep.VolumeReplicationClass{nodeAffinityTestVRC("[]")}, existing, false),
			Entry("VRC topology replaces existing affinity",
				true, []volrep.VolumeReplicationClass{nodeAffinityTestVRC(topology)},
				requiredAffinity(inTerm(nodeAffinityTestZoneKey, "z1")), false),
			Entry("invalid VRC topology is an error",
				true, []volrep.VolumeReplicationClass{nodeAffinityTestVRC("not-json")}, existing, true),
		)
	})
})
