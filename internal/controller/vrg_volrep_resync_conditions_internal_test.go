// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	volrep "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	rmnutil "github.com/ramendr/ramen/internal/controller/util"
)

// expectResyncOnPrimaryConditions verifies that, while a primary VR or VGR is resyncing to the secondary, the PVC
// and the VRG are DataReady, so that kube objects can be restored, while the PVC is not yet protected.
func expectResyncOnPrimaryConditions(v *VRGInstance, pvc corev1.PersistentVolumeClaim) {
	protectedPVC := v.findProtectedPVC(pvc.Namespace, pvc.Name)
	Expect(protectedPVC).NotTo(BeNil())

	dataReady := rmnutil.FindCondition(protectedPVC.Conditions, VRGConditionTypeDataReady)
	Expect(dataReady).NotTo(BeNil())
	Expect(dataReady.Status).To(Equal(metav1.ConditionTrue))
	Expect(dataReady.Reason).To(Equal(VRGConditionReasonReady))

	dataProtected := rmnutil.FindCondition(protectedPVC.Conditions, VRGConditionTypeDataProtected)
	Expect(dataProtected).NotTo(BeNil())
	Expect(dataProtected.Status).To(Equal(metav1.ConditionFalse))

	replicationHealthy := rmnutil.FindCondition(protectedPVC.Conditions, VRGConditionTypeReplicationHealthy)
	Expect(replicationHealthy).NotTo(BeNil())
	Expect(replicationHealthy.Status).To(Equal(metav1.ConditionFalse))

	v.volRepPVCs = []corev1.PersistentVolumeClaim{pvc}

	vrgDataReady := v.aggregateVolRepDataReadyCondition()
	Expect(vrgDataReady.Status).To(Equal(metav1.ConditionTrue))
	Expect(vrgDataReady.Reason).To(Equal(VRGConditionReasonReady))
}

var _ = Describe("VRG resync on primary conditions", func() {
	DescribeTable("VolumeReplication",
		func(vrState volrep.ReplicationState, conditions []metav1.Condition) {
			v := resyncTestVRGInstanceWithObjects(GinkgoT(), true, false, resyncTestVR(vrState, conditions))
			pvc := livePVC("pvc-a")

			volRep := &volrep.VolumeReplication{}
			Expect(v.reconciler.Get(v.ctx, types.NamespacedName{Name: "pvc-a", Namespace: "vrg-ns"}, volRep)).
				To(Succeed())

			_, _, err := v.updateVR(&pvc, volRep, volrep.Primary, logr.Discard())
			Expect(err).NotTo(HaveOccurred())

			got := &volrep.VolumeReplication{}
			Expect(v.reconciler.Get(v.ctx, types.NamespacedName{Name: "pvc-a", Namespace: "vrg-ns"}, got)).
				To(Succeed())
			Expect(got.Spec.ReplicationState).To(Equal(volrep.Resync))

			expectResyncOnPrimaryConditions(v, pvc)
		},
		Entry("is DataReady when moved from primary to resync",
			volrep.Primary, notReplicatingConditions(resyncTestVRGeneration)),
		Entry("is DataReady while resync is in progress",
			volrep.Resync, resyncingConditions()),
	)

	DescribeTable("VolumeGroupReplication",
		func(vgrState, vrState volrep.ReplicationState, conditions []metav1.Condition) {
			v := resyncTestVRGInstanceWithObjects(GinkgoT(), true, false,
				resyncTestVGR(vgrState, "pvc-a", conditions), resyncTestVR(vrState, conditions))
			pvc := livePVC("pvc-a")
			key := types.NamespacedName{Name: "vgr-a", Namespace: "vrg-ns"}

			volGroupRep := &volrep.VolumeGroupReplication{}
			Expect(v.reconciler.Get(v.ctx, key, volGroupRep)).To(Succeed())

			_, _, err := v.updateVGR([]*corev1.PersistentVolumeClaim{&pvc}, volGroupRep, volrep.Primary,
				logr.Discard())
			Expect(err).NotTo(HaveOccurred())

			got := &volrep.VolumeGroupReplication{}
			Expect(v.reconciler.Get(v.ctx, key, got)).To(Succeed())
			Expect(got.Spec.ReplicationState).To(Equal(volrep.Resync))

			expectResyncOnPrimaryConditions(v, pvc)
		},
		Entry("is DataReady when moved from primary to resync",
			volrep.Primary, volrep.Primary, promotedConditions()),
		Entry("is DataReady while resync is in progress",
			volrep.Resync, volrep.Resync, resyncingConditions()),
	)
})
