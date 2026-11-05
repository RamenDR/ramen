// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"testing"
	"time"

	volrep "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	rmnutil "github.com/ramendr/ramen/internal/controller/util"
)

const resyncTestVRGeneration = 2

func resyncTestCondition(conditionType string, status metav1.ConditionStatus, generation int64) metav1.Condition {
	return metav1.Condition{
		Type:               conditionType,
		Status:             status,
		ObservedGeneration: generation,
		Reason:             "Test",
		LastTransitionTime: metav1.Now(),
	}
}

func notReplicatingConditions(generation int64) []metav1.Condition {
	return []metav1.Condition{
		resyncTestCondition(volrep.ConditionValidated, metav1.ConditionTrue, generation),
		resyncTestCondition(volrep.ConditionCompleted, metav1.ConditionTrue, generation),
		resyncTestCondition(volrep.ConditionReplicating, metav1.ConditionFalse, generation),
	}
}

func replicatingConditions() []metav1.Condition {
	return []metav1.Condition{
		resyncTestCondition(volrep.ConditionValidated, metav1.ConditionTrue, resyncTestVRGeneration),
		resyncTestCondition(volrep.ConditionCompleted, metav1.ConditionTrue, resyncTestVRGeneration),
		resyncTestCondition(volrep.ConditionReplicating, metav1.ConditionTrue, resyncTestVRGeneration),
	}
}

func resyncingConditions() []metav1.Condition {
	return []metav1.Condition{
		resyncTestCondition(volrep.ConditionCompleted, metav1.ConditionTrue, resyncTestVRGeneration),
		resyncTestCondition(volrep.ConditionDegraded, metav1.ConditionTrue, resyncTestVRGeneration),
		resyncTestCondition(volrep.ConditionResyncing, metav1.ConditionTrue, resyncTestVRGeneration),
	}
}

func resyncCompletedConditions() []metav1.Condition {
	return []metav1.Condition{
		resyncTestCondition(volrep.ConditionCompleted, metav1.ConditionTrue, resyncTestVRGeneration),
		resyncTestCondition(volrep.ConditionDegraded, metav1.ConditionFalse, resyncTestVRGeneration),
		resyncTestCondition(volrep.ConditionResyncing, metav1.ConditionFalse, resyncTestVRGeneration),
	}
}

func resyncTestVR(state volrep.ReplicationState, conditions []metav1.Condition) *volrep.VolumeReplication {
	return &volrep.VolumeReplication{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "pvc-a",
			Namespace:  "vrg-ns",
			Generation: resyncTestVRGeneration,
		},
		Spec: volrep.VolumeReplicationSpec{
			ReplicationState: state,
		},
		Status: volrep.VolumeReplicationStatus{
			ObservedGeneration: resyncTestVRGeneration,
			Conditions:         conditions,
		},
	}
}

func promotedConditions() []metav1.Condition {
	return []metav1.Condition{
		resyncTestCondition(volrep.ConditionCompleted, metav1.ConditionTrue, resyncTestVRGeneration),
		resyncTestCondition(volrep.ConditionDegraded, metav1.ConditionFalse, resyncTestVRGeneration),
		resyncTestCondition(volrep.ConditionResyncing, metav1.ConditionFalse, resyncTestVRGeneration),
		resyncTestCondition(volrep.ConditionReplicating, metav1.ConditionFalse, resyncTestVRGeneration),
	}
}

// resyncTestVGR returns a VGR in the given state, whose status conditions are all stamped with the VGR generation,
// as csi-addons does when copying the status from the backing VR.
func resyncTestVGR(state volrep.ReplicationState, vrName string, conditions []metav1.Condition,
) *volrep.VolumeGroupReplication {
	vgrConditions := make([]metav1.Condition, len(conditions))
	for i := range conditions {
		vgrConditions[i] = conditions[i]
		vgrConditions[i].ObservedGeneration = resyncTestVRGeneration
	}

	return &volrep.VolumeGroupReplication{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "vgr-a",
			Namespace:  "vrg-ns",
			Generation: resyncTestVRGeneration,
		},
		Spec: volrep.VolumeGroupReplicationSpec{
			ReplicationState:      state,
			VolumeReplicationName: vrName,
		},
		Status: volrep.VolumeGroupReplicationStatus{
			VolumeReplicationStatus: volrep.VolumeReplicationStatus{
				ObservedGeneration: resyncTestVRGeneration,
				Conditions:         vgrConditions,
			},
		},
	}
}

// resyncTestVRGInstance builds a primary async VRGInstance backed by a fake client holding the supplied VR.
func resyncTestVRGInstance(t *testing.T, volRep *volrep.VolumeReplication, annotated, deleted bool) *VRGInstance {
	t.Helper()

	return resyncTestVRGInstanceWithObjects(t, annotated, deleted, volRep)
}

// resyncTestT is the subset of testing.T used by the resync test helpers, so that they can also be used from
// Ginkgo specs with GinkgoT().
type resyncTestT interface {
	Helper()
	Fatalf(format string, args ...any)
}

// resyncTestVRGInstanceWithObjects builds a primary async VRGInstance backed by a fake client holding the
// supplied objects.
func resyncTestVRGInstanceWithObjects(t resyncTestT, annotated, deleted bool, objs ...client.Object) *VRGInstance {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	if err := volrep.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	vrg := &ramendrv1alpha1.VolumeReplicationGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "vrg", Namespace: "vrg-ns", Generation: 1},
		Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
			ReplicationState: ramendrv1alpha1.Primary,
			Async:            &ramendrv1alpha1.VRGAsyncSpec{},
		},
	}

	if annotated {
		vrg.SetAnnotations(map[string]string{rmnutil.ResyncOnPrimaryAnnotation: "true"})
	}

	if deleted {
		deletedAt := metav1.NewTime(time.Unix(1000, 0))
		vrg.SetDeletionTimestamp(&deletedAt)
	}

	return &VRGInstance{
		reconciler: &VolumeReplicationGroupReconciler{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build(),
		},
		ctx:      context.TODO(),
		log:      logr.Discard(),
		instance: vrg,
	}
}

type resyncOnPrimaryCase struct {
	name       string
	annotated  bool
	deleted    bool
	vrState    volrep.ReplicationState
	conditions []metav1.Condition
	wantState  volrep.ReplicationState
	wantReady  bool
}

func assertResyncOnPrimary(t *testing.T, tc resyncOnPrimaryCase) {
	t.Helper()

	v := resyncTestVRGInstance(t, resyncTestVR(tc.vrState, tc.conditions), tc.annotated, tc.deleted)
	pvc := livePVC("pvc-a")

	volRep := &volrep.VolumeReplication{}
	if err := v.reconciler.Get(v.ctx, types.NamespacedName{Name: "pvc-a", Namespace: "vrg-ns"}, volRep); err != nil {
		t.Fatalf("Get VR: %v", err)
	}

	_, ready, err := v.updateVR(&pvc, volRep, volrep.Primary, logr.Discard())
	if err != nil {
		t.Fatalf("updateVR() error = %v", err)
	}

	if ready != tc.wantReady {
		t.Errorf("updateVR() ready = %v, want %v", ready, tc.wantReady)
	}

	got := &volrep.VolumeReplication{}
	if err := v.reconciler.Get(v.ctx, types.NamespacedName{Name: "pvc-a", Namespace: "vrg-ns"}, got); err != nil {
		t.Fatalf("Get VR: %v", err)
	}

	if got.Spec.ReplicationState != tc.wantState {
		t.Errorf("VR replicationState = %q, want %q", got.Spec.ReplicationState, tc.wantState)
	}

	if tc.wantState != volrep.Resync {
		return
	}

	protectedPVC := v.findProtectedPVC("vrg-ns", "pvc-a")
	if protectedPVC == nil {
		t.Fatal("protected PVC not found")
	}

	cond := rmnutil.FindCondition(protectedPVC.Conditions, VRGConditionTypeReplicationHealthy)
	if cond == nil || cond.Status != metav1.ConditionFalse {
		t.Errorf("ReplicationHealthy condition = %+v, want status False", cond)
	}
}

func TestResyncOnPrimary(t *testing.T) {
	t.Parallel()

	tests := []resyncOnPrimaryCase{
		{
			name: "no annotation, not replicating, stays primary", vrState: volrep.Primary,
			conditions: notReplicatingConditions(resyncTestVRGeneration), wantState: volrep.Primary, wantReady: true,
		},
		{
			name: "annotation, not replicating, moves to resync", annotated: true, vrState: volrep.Primary,
			conditions: notReplicatingConditions(resyncTestVRGeneration), wantState: volrep.Resync,
		},
		{
			name: "annotation, stale not replicating condition, stays primary", annotated: true, vrState: volrep.Primary,
			conditions: notReplicatingConditions(resyncTestVRGeneration - 1), wantState: volrep.Primary,
		},
		{
			name: "annotation, replicating, stays primary", annotated: true, vrState: volrep.Primary,
			conditions: replicatingConditions(), wantState: volrep.Primary, wantReady: true,
		},
		{
			name: "annotation, resync in progress, stays resync", annotated: true, vrState: volrep.Resync,
			conditions: resyncingConditions(), wantState: volrep.Resync,
		},
		{
			name: "annotation, resync completed, returns to primary", annotated: true, vrState: volrep.Resync,
			conditions: resyncCompletedConditions(), wantState: volrep.Primary,
		},
		{
			name: "annotation removed, resync in progress, returns to primary", vrState: volrep.Resync,
			conditions: resyncingConditions(), wantState: volrep.Primary,
		},
		{
			name: "annotation, VRG deleted, resync in progress, returns to primary", annotated: true, deleted: true,
			vrState: volrep.Resync, conditions: resyncingConditions(), wantState: volrep.Primary,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertResyncOnPrimary(t, tt)
		})
	}
}

type resyncOnPrimaryVGRCase struct {
	name         string
	annotated    bool
	global       bool
	vgrState     volrep.ReplicationState
	vrName       string
	vrState      volrep.ReplicationState
	vrConditions []metav1.Condition
	wantState    volrep.ReplicationState
}

func assertResyncOnPrimaryVGR(t *testing.T, tc resyncOnPrimaryVGRCase) {
	t.Helper()

	vgr := resyncTestVGR(tc.vgrState, tc.vrName, tc.vrConditions)
	v := resyncTestVRGInstanceWithObjects(t, tc.annotated, false, vgr, resyncTestVR(tc.vrState, tc.vrConditions))

	if tc.global {
		v.instance.SetLabels(map[string]string{GlobalVGRLabel: "global-id"})
	}

	pvc := livePVC("pvc-a")
	key := types.NamespacedName{Name: "vgr-a", Namespace: "vrg-ns"}

	volGroupRep := &volrep.VolumeGroupReplication{}
	if err := v.reconciler.Get(v.ctx, key, volGroupRep); err != nil {
		t.Fatalf("Get VGR: %v", err)
	}

	if _, _, err := v.updateVGR([]*corev1.PersistentVolumeClaim{&pvc}, volGroupRep, volrep.Primary,
		logr.Discard()); err != nil {
		t.Fatalf("updateVGR() error = %v", err)
	}

	got := &volrep.VolumeGroupReplication{}
	if err := v.reconciler.Get(v.ctx, key, got); err != nil {
		t.Fatalf("Get VGR: %v", err)
	}

	if got.Spec.ReplicationState != tc.wantState {
		t.Errorf("VGR replicationState = %q, want %q", got.Spec.ReplicationState, tc.wantState)
	}
}

func TestResyncOnPrimaryVGR(t *testing.T) {
	t.Parallel()

	tests := []resyncOnPrimaryVGRCase{
		{
			name: "no annotation, not replicating, stays primary", vgrState: volrep.Primary, vrName: "pvc-a",
			vrState: volrep.Primary, vrConditions: promotedConditions(), wantState: volrep.Primary,
		},
		{
			name: "annotation, not replicating, moves to resync", annotated: true, vgrState: volrep.Primary,
			vrName: "pvc-a", vrState: volrep.Primary, vrConditions: promotedConditions(), wantState: volrep.Resync,
		},
		{
			name: "annotation, replicating, stays primary", annotated: true, vgrState: volrep.Primary,
			vrName: "pvc-a", vrState: volrep.Primary, vrConditions: replicatingConditions(), wantState: volrep.Primary,
		},
		{
			name: "annotation, global VGR, not replicating, stays primary", annotated: true, global: true,
			vgrState: volrep.Primary, vrName: "pvc-a", vrState: volrep.Primary, vrConditions: promotedConditions(),
			wantState: volrep.Primary,
		},
		{
			name: "annotation, backing VR not yet known, stays primary", annotated: true, vgrState: volrep.Primary,
			vrState: volrep.Primary, vrConditions: promotedConditions(), wantState: volrep.Primary,
		},
		{
			name: "annotation, resync not yet propagated to backing VR, stays resync", annotated: true,
			vgrState: volrep.Resync, vrName: "pvc-a", vrState: volrep.Primary, vrConditions: promotedConditions(),
			wantState: volrep.Resync,
		},
		{
			name: "annotation, resync in progress, stays resync", annotated: true, vgrState: volrep.Resync,
			vrName: "pvc-a", vrState: volrep.Resync, vrConditions: resyncingConditions(), wantState: volrep.Resync,
		},
		{
			name: "annotation, resync completed, returns to primary", annotated: true, vgrState: volrep.Resync,
			vrName: "pvc-a", vrState: volrep.Resync, vrConditions: resyncCompletedConditions(),
			wantState: volrep.Primary,
		},
		{
			name: "annotation removed, resync in progress, returns to primary", vgrState: volrep.Resync,
			vrName: "pvc-a", vrState: volrep.Resync, vrConditions: resyncingConditions(), wantState: volrep.Primary,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertResyncOnPrimaryVGR(t, tt)
		})
	}
}
