// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"strings"
	"testing"

	volrep "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
)

const (
	offloadedTestProvisioner = "k8s.io/minikube-hostpath"
	offloadedTestStorageID   = "sid-1"
	offloadedTestGroupID     = "grid-1"
	offloadedTestSCName      = "standard"
	offloadedTestSelectorKey = "ramendr.openshift.io/class"
	offloadedTestSelectorVal = "chosen"
	offloadedTestInterval    = "5m"
)

func TestSelectOffloadedVGRClass(t *testing.T) {
	tests := []struct {
		name       string
		async      *ramendrv1alpha1.VRGAsyncSpec
		classes    []*volrep.VolumeGroupReplicationClass
		wantName   string
		wantErr    string
		errWithout string
	}{
		{
			name:  "async selects the class whose interval matches",
			async: offloadedTestAsync(),
			classes: []*volrep.VolumeGroupReplicationClass{
				offloadedTestClass("other-interval", "1m", offloadedTestStorageID, true, false),
				offloadedTestClass("matched", offloadedTestInterval, offloadedTestStorageID, true, false),
			},
			wantName: "matched",
		},
		{
			name:  "async rejects a different interval",
			async: offloadedTestAsync(),
			classes: []*volrep.VolumeGroupReplicationClass{
				offloadedTestClass("other-interval", "1m", offloadedTestStorageID, true, false),
			},
			wantErr: "schedulingInterval " + offloadedTestInterval,
		},
		{
			name:  "async honors the replication class selector",
			async: offloadedTestAsync(),
			classes: []*volrep.VolumeGroupReplicationClass{
				offloadedTestClass("unlabeled", offloadedTestInterval, offloadedTestStorageID, false, false),
				offloadedTestClass("labeled", offloadedTestInterval, offloadedTestStorageID, true, false),
			},
			wantName: "labeled",
		},
		{
			name: "sync selects a class without reading a schedule",
			classes: []*volrep.VolumeGroupReplicationClass{
				offloadedTestClass("sync-class", offloadedTestInterval, offloadedTestStorageID, false, false),
			},
			wantName: "sync-class",
		},
		{
			name: "sync reports a miss without an interval",
			classes: []*volrep.VolumeGroupReplicationClass{
				offloadedTestClass("wrong-storage", offloadedTestInterval, "other-sid", false, false),
			},
			wantErr:    "provisioner, storageID and groupReplicationID",
			errWithout: "schedulingInterval",
		},
		{
			name: "several matches use the default class",
			classes: []*volrep.VolumeGroupReplicationClass{
				offloadedTestClass("plain", offloadedTestInterval, offloadedTestStorageID, false, false),
				offloadedTestClass("defaulted", offloadedTestInterval, offloadedTestStorageID, false, true),
			},
			wantName: "defaulted",
		},
		{
			name: "several matches without a default annotation fail",
			classes: []*volrep.VolumeGroupReplicationClass{
				offloadedTestClass("one", offloadedTestInterval, offloadedTestStorageID, false, false),
				offloadedTestClass("two", offloadedTestInterval, offloadedTestStorageID, false, false),
			},
			wantErr: "no default annotation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertOffloadedVGRClass(t, tt.async, tt.classes, tt.wantName, tt.wantErr, tt.errWithout)
		})
	}
}

func TestProcessOffloadedPVCs(t *testing.T) {
	pvcList := &corev1.PersistentVolumeClaimList{Items: []corev1.PersistentVolumeClaim{*offloadedTestPVC()}}
	peer := func(offloaded bool) ramendrv1alpha1.PeerClass {
		return ramendrv1alpha1.PeerClass{
			StorageClassName: offloadedTestSCName,
			StorageID:        []string{offloadedTestStorageID},
			Offloaded:        offloaded,
		}
	}

	tests := []struct {
		name      string
		offloaded bool
		peers     []ramendrv1alpha1.PeerClass
		want      bool
		wantErr   string
	}{
		{
			name:      "an empty peer list is not offloaded",
			offloaded: true,
		},
		{
			name:  "a non-offloaded peer stays not offloaded",
			peers: []ramendrv1alpha1.PeerClass{peer(false)},
		},
		{
			name:      "the peer and the storage class must agree",
			offloaded: false,
			peers:     []ramendrv1alpha1.PeerClass{peer(true)},
			wantErr:   "mismatched peerClass offload support",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := offloadedTestVRG(t, nil, offloadedTestStorageClass(tt.offloaded))

			got, err := v.processOffloadedPVCs(pvcList, tt.peers)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %v, want substring %q", err, tt.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("process PVCs: %v", err)
			}

			if got != tt.want {
				t.Fatalf("offloaded %v, want %v", got, tt.want)
			}
		})
	}
}

// TestRejectOffloadedPVCsWithoutPeers covers a sync VolumeReplicationGroup that did not take the
// offloaded path. Nothing creates a VolumeGroupReplication there, so the PVC must not be accepted.
func TestRejectOffloadedPVCsWithoutPeers(t *testing.T) {
	tests := []struct {
		name      string
		offloaded bool
		noSC      bool
		noSCName  bool
		wantErr   string
	}{
		{
			name:      "an offloaded PVC is rejected",
			offloaded: true,
			wantErr:   "has no peer classes",
		},
		{
			name:      "a non-offloaded PVC is accepted",
			offloaded: false,
		},
		{
			name:    "a named StorageClass that cannot be read is an error",
			noSC:    true,
			wantErr: "failed to get the storageclass",
		},
		{
			name:     "a PVC without a StorageClass name is accepted",
			noSC:     true,
			noSCName: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pvc := offloadedTestPVC()
			if tt.noSCName {
				pvc.Spec.StorageClassName = nil
			}

			pvcList := &corev1.PersistentVolumeClaimList{Items: []corev1.PersistentVolumeClaim{*pvc}}

			objects := []client.Object{offloadedTestStorageClass(tt.offloaded)}
			if tt.noSC {
				objects = nil
			}

			v := offloadedTestVRG(t, nil, objects...)

			v.instance.Spec.Sync = &ramendrv1alpha1.VRGSyncSpec{}

			requireErrorContains(t, v.updateSyncPVCs(pvcList), tt.wantErr)
		})
	}
}

// Test helpers

// requireErrorContains fails unless err is nil for an empty want, or contains want otherwise.
func requireErrorContains(t *testing.T, err error, want string) {
	t.Helper()

	if want == "" {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		return
	}

	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error %v, want substring %q", err, want)
	}
}

func offloadedTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}

	if err := storagev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add storage scheme: %v", err)
	}

	if err := volrep.AddToScheme(scheme); err != nil {
		t.Fatalf("add replication scheme: %v", err)
	}

	return scheme
}

func offloadedTestVRG(t *testing.T, async *ramendrv1alpha1.VRGAsyncSpec, objects ...client.Object) *VRGInstance {
	t.Helper()

	return &VRGInstance{
		ctx: context.Background(),
		log: logr.Discard(),
		reconciler: &VolumeReplicationGroupReconciler{
			Client: fake.NewClientBuilder().WithScheme(offloadedTestScheme(t)).WithObjects(objects...).Build(),
		},
		instance: &ramendrv1alpha1.VolumeReplicationGroup{
			Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{Async: async},
		},
		storageClassCache: map[string]*storagev1.StorageClass{},
	}
}

func offloadedTestStorageClass(offloaded bool) *storagev1.StorageClass {
	labels := map[string]string{
		StorageIDLabel:          offloadedTestStorageID,
		GroupReplicationIDLabel: offloadedTestGroupID,
	}
	if offloaded {
		labels[StorageOffloadedLabel] = ""
	}

	return &storagev1.StorageClass{
		ObjectMeta:  metav1.ObjectMeta{Name: offloadedTestSCName, Labels: labels},
		Provisioner: offloadedTestProvisioner,
	}
}

func offloadedTestPVC() *corev1.PersistentVolumeClaim {
	scName := offloadedTestSCName

	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "busybox-pvc", Namespace: "app"},
		Spec:       corev1.PersistentVolumeClaimSpec{StorageClassName: &scName},
	}
}

func offloadedTestClass(
	name, interval, storageID string, selected, defaultClass bool,
) *volrep.VolumeGroupReplicationClass {
	labels := map[string]string{
		StorageIDLabel:          storageID,
		GroupReplicationIDLabel: offloadedTestGroupID,
	}
	if selected {
		labels[offloadedTestSelectorKey] = offloadedTestSelectorVal
	}

	var annotations map[string]string
	if defaultClass {
		annotations = map[string]string{defaultVRCAnnotationKey: "true"}
	}

	parameters := map[string]string{}
	if interval != "" {
		parameters[ReplicationClassScheduleKey] = interval
	}

	return &volrep.VolumeGroupReplicationClass{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels, Annotations: annotations},
		Spec: volrep.VolumeGroupReplicationClassSpec{
			Provisioner: offloadedTestProvisioner,
			Parameters:  parameters,
		},
	}
}

func offloadedTestAsync() *ramendrv1alpha1.VRGAsyncSpec {
	return &ramendrv1alpha1.VRGAsyncSpec{
		SchedulingInterval: offloadedTestInterval,
		ReplicationClassSelector: metav1.LabelSelector{
			MatchLabels: map[string]string{offloadedTestSelectorKey: offloadedTestSelectorVal},
		},
	}
}

func assertOffloadedVGRClass(
	t *testing.T,
	async *ramendrv1alpha1.VRGAsyncSpec,
	classes []*volrep.VolumeGroupReplicationClass,
	wantName, wantErr, errWithout string,
) {
	t.Helper()

	objects := make([]client.Object, 0, 1+len(classes))
	objects = append(objects, offloadedTestStorageClass(true))

	for _, class := range classes {
		objects = append(objects, class)
	}

	got, err := offloadedTestVRG(t, async, objects...).selectOffloadedVGRClass(offloadedTestPVC())
	if wantErr != "" {
		if err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Fatalf("error %v, want substring %q", err, wantErr)
		}

		if errWithout != "" && strings.Contains(err.Error(), errWithout) {
			t.Fatalf("error %v contains %q", err, errWithout)
		}

		return
	}

	if err != nil {
		t.Fatalf("select class: %v", err)
	}

	if got.GetName() != wantName {
		t.Fatalf("class %s, want %s", got.GetName(), wantName)
	}
}
