// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	volrep "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	rmnutil "github.com/ramendr/ramen/internal/controller/util"
)

var _ = Describe("VRGInstance labelChildVRsOwnedByVGR", func() {
	const (
		vgrNamespace = "vgr-ns"
		vgrName      = "vgr-cg"
		vgrUID       = types.UID("vgr-uid-1234")
	)

	newVGR := func() *volrep.VolumeGroupReplication {
		return &volrep.VolumeGroupReplication{
			ObjectMeta: metav1.ObjectMeta{
				Name:      vgrName,
				Namespace: vgrNamespace,
				UID:       vgrUID,
			},
		}
	}

	// childVR builds a VolumeReplication owned by the given owner reference.
	childVR := func(name string, owner metav1.OwnerReference) *volrep.VolumeReplication {
		return &volrep.VolumeReplication{
			ObjectMeta: metav1.ObjectMeta{
				Name:            name,
				Namespace:       vgrNamespace,
				OwnerReferences: []metav1.OwnerReference{owner},
			},
		}
	}

	vgrOwnerRef := metav1.OwnerReference{
		Kind: "VolumeGroupReplication",
		Name: vgrName,
		UID:  vgrUID,
	}

	buildVRG := func(objs ...client.Object) *VRGInstance {
		scheme := runtime.NewScheme()
		Expect(volrep.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(objs...).
			Build()

		return &VRGInstance{
			ctx: context.TODO(),
			log: logr.Discard(),
			reconciler: &VolumeReplicationGroupReconciler{
				Client: fakeClient,
			},
		}
	}

	labelOf := func(v *VRGInstance, name string) string {
		vr := &volrep.VolumeReplication{}
		Expect(v.reconciler.Get(v.ctx,
			types.NamespacedName{Name: name, Namespace: vgrNamespace}, vr)).To(Succeed())

		return vr.GetLabels()[rmnutil.CreatedByRamenLabel]
	}

	It("labels VolumeReplications owned by the VGR", func() {
		vgr := newVGR()
		owned := childVR("vr-owned", vgrOwnerRef)
		v := buildVRG(owned)

		v.labelChildVRsOwnedByVGR(vgr, v.log)

		Expect(labelOf(v, "vr-owned")).To(Equal("transitive"))
	})

	It("does not label VolumeReplications owned by a different VGR", func() {
		vgr := newVGR()
		otherOwner := metav1.OwnerReference{
			Kind: "VolumeGroupReplication",
			Name: "some-other-vgr",
			UID:  types.UID("other-uid"),
		}
		foreign := childVR("vr-foreign", otherOwner)
		v := buildVRG(foreign)

		v.labelChildVRsOwnedByVGR(vgr, v.log)

		Expect(labelOf(v, "vr-foreign")).To(BeEmpty())
	})

	It("is idempotent and preserves the label across repeated calls", func() {
		vgr := newVGR()
		owned := childVR("vr-owned", vgrOwnerRef)
		v := buildVRG(owned)

		v.labelChildVRsOwnedByVGR(vgr, v.log)
		v.labelChildVRsOwnedByVGR(vgr, v.log)

		Expect(labelOf(v, "vr-owned")).To(Equal("transitive"))
	})
})
