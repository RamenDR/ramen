// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"reflect"
	"sort"
	"testing"

	volrep "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	"github.com/go-logr/logr"
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

func assertNodeAffinity(t *testing.T, got, want *corev1.VolumeNodeAffinity) {
	t.Helper()

	sortNodeAffinityExprs(got)
	sortNodeAffinityExprs(want)

	if !reflect.DeepEqual(got, want) {
		t.Errorf("node affinity = %+v, want %+v", got, want)
	}
}

func TestAccessibleTopologyToNodeAffinity(t *testing.T) {
	tests := []struct {
		name     string
		topology string
		want     *corev1.VolumeNodeAffinity
		wantErr  bool
	}{
		{name: "invalid json", topology: `{"zone":`, wantErr: true},
		{name: "not a list", topology: `{"` + nodeAffinityTestZoneKey + `":"z1"}`, wantErr: true},
		{name: "empty list", topology: `[]`},
		{name: "only empty segments", topology: `[{},{}]`},
		{
			name:     "single segment single key",
			topology: `[{"` + nodeAffinityTestZoneKey + `":"z1"}]`,
			want:     requiredAffinity(inTerm(nodeAffinityTestZoneKey, "z1")),
		},
		{
			name: "single segment multiple keys are ANDed in one term",
			topology: `[{"` + nodeAffinityTestZoneKey + `":"z1","` +
				nodeAffinityTestRegionKey + `":"r1"}]`,
			want: requiredAffinity(inTerm(nodeAffinityTestRegionKey, "r1", nodeAffinityTestZoneKey, "z1")),
		},
		{
			name: "multiple segments are ORed as separate terms, empty segments skipped",
			topology: `[{"` + nodeAffinityTestZoneKey + `":"z1"},{},{"` +
				nodeAffinityTestZoneKey + `":"z2"}]`,
			want: requiredAffinity(inTerm(nodeAffinityTestZoneKey, "z1"), inTerm(nodeAffinityTestZoneKey, "z2")),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := accessibleTopologyToNodeAffinity(tc.topology)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}

			assertNodeAffinity(t, got, tc.want)
		})
	}
}

func TestUpdatePVNodeAffinityForRestore(t *testing.T) {
	topology := `[{"` + nodeAffinityTestZoneKey + `":"z1"}]`
	existing := requiredAffinity(inTerm("kubernetes.io/hostname", "node-a"))

	tests := []struct {
		name    string
		async   bool
		vrcs    []volrep.VolumeReplicationClass
		initial *corev1.VolumeNodeAffinity
		want    *corev1.VolumeNodeAffinity
		wantErr bool
	}{
		{
			name:    "sync VRG leaves affinity untouched",
			vrcs:    []volrep.VolumeReplicationClass{nodeAffinityTestVRC(topology)},
			initial: existing,
			want:    existing,
		},
		{name: "no matching VRC is not an error", async: true, initial: existing, want: existing},
		{
			name:    "VRC without topology leaves affinity untouched",
			async:   true,
			vrcs:    []volrep.VolumeReplicationClass{nodeAffinityTestVRC("")},
			initial: existing,
			want:    existing,
		},
		{
			name:    "VRC with empty topology list leaves affinity untouched",
			async:   true,
			vrcs:    []volrep.VolumeReplicationClass{nodeAffinityTestVRC("[]")},
			initial: existing,
			want:    existing,
		},
		{
			name:    "VRC topology replaces existing affinity",
			async:   true,
			vrcs:    []volrep.VolumeReplicationClass{nodeAffinityTestVRC(topology)},
			initial: existing,
			want:    requiredAffinity(inTerm(nodeAffinityTestZoneKey, "z1")),
		},
		{
			name:    "invalid VRC topology is an error",
			async:   true,
			vrcs:    []volrep.VolumeReplicationClass{nodeAffinityTestVRC("not-json")},
			initial: existing,
			want:    existing,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := nodeAffinityTestVRG(tc.async, tc.vrcs...)
			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "pv"},
				Spec:       corev1.PersistentVolumeSpec{NodeAffinity: tc.initial.DeepCopy()},
			}

			err := v.updatePVNodeAffinityForRestore(pv, nodeAffinityTestSC())
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}

			assertNodeAffinity(t, pv.Spec.NodeAffinity, tc.want.DeepCopy())
		})
	}
}
