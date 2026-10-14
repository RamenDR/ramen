// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"errors"
	"testing"

	"github.com/go-logr/logr"
	plrv1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	rmnutil "github.com/ramendr/ramen/internal/controller/util"
)

// failoverRecordFixture builds a DRPCInstance around a fake client whose
// status-subresource writes are counted, so tests can observe whether (and
// that) the FailingOver transition is persisted rather than only staged in
// memory.
func failoverRecordFixture(t *testing.T, phase rmn.DRState, statusErr error) (*DRPCInstance, *int, client.Client) {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := rmn.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	if err := plrv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	drpc := &rmn.DRPlacementControl{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "ramen-ops"},
		Spec: rmn.DRPlacementControlSpec{
			Action:          rmn.ActionFailover,
			FailoverCluster: "dr2",
		},
	}
	drpc.Status.Phase = phase

	statusWrites := 0
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(drpc).
		WithStatusSubresource(&rmn.DRPlacementControl{}).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, c client.Client, subResourceName string,
				obj client.Object, opts ...client.SubResourceUpdateOption,
			) error {
				statusWrites++

				if statusErr != nil {
					return statusErr
				}

				return c.SubResource(subResourceName).Update(ctx, obj, opts...)
			},
		}).
		Build()

	d := &DRPCInstance{
		ctx:           context.Background(),
		log:           logr.Discard(),
		instance:      drpc,
		userPlacement: &plrv1.PlacementRule{ObjectMeta: metav1.ObjectMeta{Name: "pl", Namespace: "ramen-ops"}},
		reconciler: &DRPlacementControlReconciler{
			Client:        cl,
			Scheme:        scheme,
			Log:           logr.Discard(),
			eventRecorder: rmnutil.NewEventReporter(record.NewFakeRecorder(8)),
		},
	}

	return d, &statusWrites, cl
}

// Once the peer cluster's VRG ManifestWork is promoted, any observer — or a
// hub that restarts mid-action — must find the FailingOver phase already
// recorded in the API server. recordFailoverStart is that record-before-act
// step: on the transition into FailingOver it must persist the status, not
// merely stage it for the end of the reconcile.
func TestRecordFailoverStartPersistsTransition(t *testing.T) {
	d, statusWrites, cl := failoverRecordFixture(t, rmn.Deployed, nil)

	if err := d.recordFailoverStart(); err != nil {
		t.Fatal(err)
	}

	if *statusWrites == 0 {
		t.Fatal("FailingOver transition was not persisted before returning")
	}

	persisted := &rmn.DRPlacementControl{}
	if err := cl.Get(context.Background(),
		client.ObjectKeyFromObject(d.instance), persisted); err != nil {
		t.Fatal(err)
	}

	if persisted.Status.Phase != rmn.FailingOver {
		t.Fatalf("persisted phase = %q, want FailingOver", persisted.Status.Phase)
	}

	var peerReady *metav1.Condition

	for i := range persisted.Status.Conditions {
		if persisted.Status.Conditions[i].Type == rmn.ConditionPeerReady {
			peerReady = &persisted.Status.Conditions[i]
		}
	}

	if peerReady == nil || peerReady.Status != metav1.ConditionFalse {
		t.Fatalf("persisted PeerReady=False condition missing, got %+v", persisted.Status.Conditions)
	}
}

// Requeues that are already in FailingOver must not add a status write per
// reconcile; the record is needed only on the transition.
func TestRecordFailoverStartSkipsPersistWhenAlreadyFailingOver(t *testing.T) {
	d, statusWrites, _ := failoverRecordFixture(t, rmn.FailingOver, nil)

	if err := d.recordFailoverStart(); err != nil {
		t.Fatal(err)
	}

	if *statusWrites != 0 {
		t.Fatalf("expected no status writes on requeue, got %d", *statusWrites)
	}
}

// A failed record must abort the failover attempt: acting on an unrecorded
// transition is exactly the window this function exists to close.
func TestRecordFailoverStartPropagatesPersistFailure(t *testing.T) {
	boom := errors.New("boom")
	d, _, _ := failoverRecordFixture(t, rmn.Deployed, boom)

	if err := d.recordFailoverStart(); !errors.Is(err, boom) {
		t.Fatalf("expected persist error to propagate, got: %v", err)
	}
}
