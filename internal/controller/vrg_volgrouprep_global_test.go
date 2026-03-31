// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"
	"fmt"

	volrep "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	vrgController "github.com/ramendr/ramen/internal/controller"
)

type globalVGRTest struct {
	scName        string
	vgrcName      string
	replicationID string
	vrgA          *globalVRGInstance
	vrgB          *globalVRGInstance
	peerClass     ramendrv1alpha1.PeerClass
}

type globalVRGInstance struct {
	namespace string
	vrgName   string
}

func newGlobalVGRTest() *globalVGRTest {
	suffix := newRandomNamespaceSuffix()

	return &globalVGRTest{
		scName:        fmt.Sprintf("global-sc-%s", suffix),
		vgrcName:      fmt.Sprintf("global-vgrc-%s", suffix),
		replicationID: fmt.Sprintf("repl-%s", suffix),
		vrgA: &globalVRGInstance{
			namespace: fmt.Sprintf("global-ns-a-%s", suffix),
			vrgName:   fmt.Sprintf("vrg-a-%s", suffix),
		},
		vrgB: &globalVRGInstance{
			namespace: fmt.Sprintf("global-ns-b-%s", suffix),
			vrgName:   fmt.Sprintf("vrg-b-%s", suffix),
		},
	}
}

var _ = Describe("GlobalVGR", Ordered, func() {
	var g *globalVGRTest

	BeforeAll(func() {
		g = newGlobalVGRTest()
		createGlobalSC(g.scName, g.replicationID)
		createGlobalVGRC(g.vgrcName, g.replicationID)
		g.peerClass = buildGlobalPeerClass(g.replicationID, g.scName)

		namespaceCreate(g.vrgA.namespace)
		createBoundPVCAndPV(g.vrgA.namespace, g.scName, g.vrgA.vrgName)
		createGlobalVRG(g.vrgA.vrgName, g.vrgA.namespace, []ramendrv1alpha1.PeerClass{g.peerClass})

		namespaceCreate(g.vrgB.namespace)
		createBoundPVCAndPV(g.vrgB.namespace, g.scName, g.vrgB.vrgName)
		createGlobalVRG(g.vrgB.vrgName, g.vrgB.namespace, []ramendrv1alpha1.PeerClass{g.peerClass})
	})

	It("labels both VRGs with the global VGR name", func() {
		g.waitForGlobalVGRLabel(g.vrgA)
		g.waitForGlobalVGRLabel(g.vrgB)
	})

	It("reaches primary consensus", func() {
		globalState := vrgController.VRGConditionTypeGlobalState
		g.verifyVRGCondition(g.vrgA, globalState, metav1.ConditionTrue,
			vrgController.ConditionReasonConsensusReached)
		g.verifyVRGCondition(g.vrgB, globalState, metav1.ConditionTrue,
			vrgController.ConditionReasonConsensusReached)
	})

	It("creates a global VGR in primary state with correct spec", func() {
		g.verifyGlobalVGRReplicationState(volrep.Primary)

		vgr := &volrep.VolumeGroupReplication{}
		Expect(apiReader.Get(context.TODO(), g.globalVGRKey(), vgr)).To(Succeed())
		Expect(vgr.Namespace).To(Equal(ramenNamespace))
		Expect(vgr.Spec.VolumeGroupReplicationClassName).To(Equal(g.vgrcName))
	})

	It("marks VRGs DataReady and DataProtected after VGR promotion", func() {
		g.updateGlobalVGRStatus(volrep.PrimaryState)
		g.verifyVRGCondition(g.vrgA, vrgController.VRGConditionTypeDataReady, metav1.ConditionTrue, "")
		g.verifyVRGCondition(g.vrgB, vrgController.VRGConditionTypeDataReady, metav1.ConditionTrue, "")
		g.verifyVRGCondition(g.vrgA, vrgController.VRGConditionTypeDataProtected, metav1.ConditionTrue, "")
		g.verifyVRGCondition(g.vrgB, vrgController.VRGConditionTypeDataProtected, metav1.ConditionTrue, "")
	})

	It("blocks consensus when VRG-B moves to secondary", func() {
		g.updateVRGSpec(g.vrgB, ramendrv1alpha1.Secondary)

		globalState := vrgController.VRGConditionTypeGlobalState
		g.verifyVRGCondition(g.vrgB, globalState, metav1.ConditionFalse,
			vrgController.ConditionReasonConsensusNotReached)
	})

	AfterAll(func() { g.cleanupAll() })
})

var _ = Describe("GlobalVGR deletion consensus", Ordered, func() {
	var g *globalVGRTest

	BeforeAll(func() {
		g = newGlobalVGRTest()
		createGlobalSC(g.scName, g.replicationID)
		createGlobalVGRC(g.vgrcName, g.replicationID)
		g.peerClass = buildGlobalPeerClass(g.replicationID, g.scName)

		namespaceCreate(g.vrgA.namespace)
		createBoundPVCAndPV(g.vrgA.namespace, g.scName, g.vrgA.vrgName)
		createGlobalVRG(g.vrgA.vrgName, g.vrgA.namespace, []ramendrv1alpha1.PeerClass{g.peerClass})

		namespaceCreate(g.vrgB.namespace)
		createBoundPVCAndPV(g.vrgB.namespace, g.scName, g.vrgB.vrgName)
		createGlobalVRG(g.vrgB.vrgName, g.vrgB.namespace, []ramendrv1alpha1.PeerClass{g.peerClass})

		g.waitForGlobalVGRLabel(g.vrgA)
		g.waitForGlobalVGRLabel(g.vrgB)
		g.updateGlobalVGRStatus(volrep.PrimaryState)
		g.verifyVRGCondition(g.vrgA, vrgController.VRGConditionTypeDataReady, metav1.ConditionTrue, "")
		g.verifyVRGCondition(g.vrgB, vrgController.VRGConditionTypeDataReady, metav1.ConditionTrue, "")
	})

	It("keeps VGR when only one VRG is deleted", func() {
		g.deleteVRG(g.vrgA)
		g.verifyGlobalVGRExists()
	})

	It("completes VRG-A deletion", func() {
		g.verifyVRGDeleted(g.vrgA)
	})

	It("cleans PVC finalizers for the deleted VRG-A", func() {
		g.verifyPVCFinalizerAbsent(g.vrgA)
	})

	It("keeps VRG-B healthy after VRG-A deletion", func() {
		g.verifyVRGCondition(g.vrgB, vrgController.VRGConditionTypeDataReady, metav1.ConditionTrue, "")
		g.verifyVRGCondition(g.vrgB, vrgController.VRGConditionTypeDataProtected, metav1.ConditionTrue, "")
	})

	It("deletes VGR once all VRGs are deleted", func() {
		g.deleteVRG(g.vrgB)
		g.verifyGlobalVGRDeleted()
	})

	It("completes VRG-B deletion", func() {
		g.verifyVRGDeleted(g.vrgB)
	})

	It("cleans PVC finalizers for the deleted VRG-B", func() {
		g.verifyPVCFinalizerAbsent(g.vrgB)
	})

	AfterAll(func() { g.cleanupAll() })
})

var _ = Describe("GlobalVGR mixed GroupReplicationID", Ordered, func() {
	var g *globalVGRTest

	BeforeAll(func() {
		g = newGlobalVGRTest()
		replID2 := g.replicationID + "-2"
		sc2Name := g.scName + "-2"

		createGlobalSC(g.scName, g.replicationID)
		createGlobalSC(sc2Name, replID2)
		createGlobalVGRC(g.vgrcName, g.replicationID)

		namespaceCreate(g.vrgA.namespace)
		createBoundPVCAndPV(g.vrgA.namespace, g.scName, g.vrgA.vrgName+"-0")
		createBoundPVCAndPV(g.vrgA.namespace, sc2Name, g.vrgA.vrgName+"-1")

		createGlobalVRG(g.vrgA.vrgName, g.vrgA.namespace, []ramendrv1alpha1.PeerClass{
			buildGlobalPeerClass(g.replicationID, g.scName),
			buildGlobalPeerClass(replID2, sc2Name),
		})
	})

	It("does not label VRG with global VGR name", func() {
		vrgKey := types.NamespacedName{Name: g.vrgA.vrgName, Namespace: g.vrgA.namespace}

		Consistently(func() string {
			vrg := &ramendrv1alpha1.VolumeReplicationGroup{}
			if err := apiReader.Get(context.TODO(), vrgKey, vrg); err != nil {
				return ""
			}

			return vrg.GetLabels()[vrgController.GlobalVGRLabel]
		}, vrgtimeout, vrginterval).Should(BeEmpty(),
			"VRG %s should not get global VGR label with mixed GroupReplicationIDs", vrgKey)
	})

	AfterAll(func() { g.cleanupAll() })
})

// --- Test helpers ---

func (g *globalVGRTest) globalVGRKey() types.NamespacedName {
	return types.NamespacedName{
		Name:      fmt.Sprintf("global-%s", g.replicationID),
		Namespace: ramenNamespace,
	}
}

func createGlobalSC(name, replID string) {
	Expect(k8sClient.Create(context.TODO(), &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				vrgController.StorageIDLabel:          storageIDs[0],
				vrgController.StorageOffloadedLabel:   "true",
				vrgController.GroupReplicationIDLabel: replID,
			},
		},
		Provisioner: "manual.storage.com",
	})).To(Succeed())
}

func createGlobalVGRC(name, replID string) {
	Expect(k8sClient.Create(context.TODO(), &volrep.VolumeGroupReplicationClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				vrgController.StorageIDLabel:          storageIDs[0],
				vrgController.GroupReplicationIDLabel: replID,
				vrgController.GlobalReplicationLabel:  "true",
				"protection":                          "ramen",
			},
			Annotations: map[string]string{
				"replication.storage.openshift.io/is-default-class": "true",
			},
		},
		Spec: volrep.VolumeGroupReplicationClassSpec{
			Provisioner: "manual.storage.com",
			Parameters:  map[string]string{"schedulingInterval": "0m"},
		},
	})).To(Succeed())
}

func buildGlobalPeerClass(replID, scName string) ramendrv1alpha1.PeerClass {
	return ramendrv1alpha1.PeerClass{
		ReplicationID:      replID,
		GroupReplicationID: replID,
		StorageID:          []string{storageIDs[0], storageIDs[1]},
		StorageClassName:   scName,
		Grouping:           true,
		Offloaded:          true,
		Global:             true,
	}
}

func createBoundPVCAndPV(namespace, scName, suffix string) {
	pvName := fmt.Sprintf("pv-%s", suffix)
	pvcName := fmt.Sprintf("pvc-%s", suffix)

	pvObj := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: pvName},
		Spec: corev1.PersistentVolumeSpec{
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				HostPath: &corev1.HostPathVolumeSource{Path: "/tmp/" + pvName},
			},
			AccessModes:                   []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Capacity:                      corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
			ClaimRef:                      &corev1.ObjectReference{Namespace: namespace, Name: pvcName},
			StorageClassName:              scName,
			PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimRetain,
		},
	}

	Expect(k8sClient.Create(context.TODO(), pvObj)).To(Succeed())

	pvObj.Status.Phase = corev1.VolumeBound
	Expect(k8sClient.Status().Update(context.TODO(), pvObj)).To(Succeed())

	pvcObj := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: pvcName, Namespace: namespace,
			Labels: map[string]string{"appclass": "global"},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
			},
			VolumeName:       pvName,
			StorageClassName: &scName,
		},
	}

	Expect(k8sClient.Create(context.TODO(), pvcObj)).To(Succeed())

	pvcObj.Status.Phase = corev1.ClaimBound
	pvcObj.Status.AccessModes = pvcObj.Spec.AccessModes
	pvcObj.Status.Capacity = pvcObj.Spec.Resources.Requests
	Expect(k8sClient.Status().Update(context.TODO(), pvcObj)).To(Succeed())
}

func createGlobalVRG(name, namespace string, peerClasses []ramendrv1alpha1.PeerClass) {
	Expect(k8sClient.Create(context.TODO(), &ramendrv1alpha1.VolumeReplicationGroup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
			PVCSelector:      metav1.LabelSelector{MatchLabels: map[string]string{"appclass": "global"}},
			ReplicationState: ramendrv1alpha1.Primary,
			Async: &ramendrv1alpha1.VRGAsyncSpec{
				SchedulingInterval:       "0m",
				ReplicationClassSelector: metav1.LabelSelector{MatchLabels: map[string]string{"protection": "ramen"}},
				PeerClasses:              peerClasses,
			},
			S3Profiles: []string{s3Profiles[vrgS3ProfileNumber].S3ProfileName},
		},
	})).To(Succeed())
}

func (g *globalVGRTest) verifyGlobalVGRReplicationState(expected volrep.ReplicationState) {
	Eventually(func() volrep.ReplicationState {
		vgr := &volrep.VolumeGroupReplication{}
		if err := apiReader.Get(context.TODO(), g.globalVGRKey(), vgr); err != nil {
			return ""
		}

		return vgr.Spec.ReplicationState
	}, vrgtimeout, vrginterval).Should(Equal(expected),
		"waiting for global VGR %s ReplicationState=%s", g.globalVGRKey(), expected)
}

func (g *globalVGRTest) waitForGlobalVGRLabel(inst *globalVRGInstance) {
	vrgKey := types.NamespacedName{Name: inst.vrgName, Namespace: inst.namespace}

	Eventually(func() string {
		vrg := &ramendrv1alpha1.VolumeReplicationGroup{}
		if err := apiReader.Get(context.TODO(), vrgKey, vrg); err != nil {
			return ""
		}

		return vrg.GetLabels()[vrgController.GlobalVGRLabel]
	}, vrgtimeout, vrginterval).Should(Equal(g.globalVGRKey().Name),
		"waiting for global VGR label on VRG %s", vrgKey)
}

func (g *globalVGRTest) verifyVRGCondition(
	inst *globalVRGInstance, condType string, expectedStatus metav1.ConditionStatus, expectedReason string,
) {
	vrgKey := types.NamespacedName{Name: inst.vrgName, Namespace: inst.namespace}

	Eventually(func() metav1.ConditionStatus {
		vrg := &ramendrv1alpha1.VolumeReplicationGroup{}
		if err := apiReader.Get(context.TODO(), vrgKey, vrg); err != nil {
			return metav1.ConditionUnknown
		}

		cond := meta.FindStatusCondition(vrg.Status.Conditions, condType)
		if cond == nil {
			return metav1.ConditionUnknown
		}

		return cond.Status
	}, vrgtimeout, vrginterval).Should(Equal(expectedStatus),
		"waiting for VRG %s condition %s=%s", vrgKey, condType, expectedStatus)

	if expectedReason != "" {
		vrg := &ramendrv1alpha1.VolumeReplicationGroup{}
		Expect(apiReader.Get(context.TODO(), vrgKey, vrg)).To(Succeed())

		cond := meta.FindStatusCondition(vrg.Status.Conditions, condType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Reason).To(Equal(expectedReason))
	}
}

func (g *globalVGRTest) verifyVRGDeleted(inst *globalVRGInstance) {
	vrgKey := types.NamespacedName{Name: inst.vrgName, Namespace: inst.namespace}

	Eventually(func() bool {
		return errors.IsNotFound(apiReader.Get(context.TODO(), vrgKey, &ramendrv1alpha1.VolumeReplicationGroup{}))
	}, vrgtimeout*10, vrginterval).Should(BeTrue(),
		"VRG %s should be fully deleted", vrgKey)
}

func (g *globalVGRTest) verifyPVCFinalizerAbsent(inst *globalVRGInstance) {
	pvcKey := types.NamespacedName{
		Name:      fmt.Sprintf("pvc-%s", inst.vrgName),
		Namespace: inst.namespace,
	}

	Eventually(func() bool {
		pvc := &corev1.PersistentVolumeClaim{}
		if err := apiReader.Get(context.TODO(), pvcKey, pvc); err != nil {
			return true
		}

		for _, f := range pvc.Finalizers {
			if f == vrgController.PvcVRFinalizerProtected {
				return false
			}
		}

		return true
	}, vrgtimeout*10, vrginterval).Should(BeTrue(),
		"PVC %s should not have the VR protection finalizer", pvcKey)
}

func (g *globalVGRTest) verifyGlobalVGRExists() {
	Consistently(func() error {
		return apiReader.Get(context.TODO(), g.globalVGRKey(), &volrep.VolumeGroupReplication{})
	}, vrgtimeout, vrginterval).Should(Succeed(),
		"global VGR %s should still exist", g.globalVGRKey())
}

func (g *globalVGRTest) verifyGlobalVGRDeleted() {
	Eventually(func() bool {
		err := apiReader.Get(context.TODO(), g.globalVGRKey(), &volrep.VolumeGroupReplication{})

		return errors.IsNotFound(err)
	}, vrgtimeout*10, vrginterval).Should(BeTrue(),
		"global VGR %s should be deleted", g.globalVGRKey())
}

func (g *globalVGRTest) updateGlobalVGRStatus(state volrep.State) {
	vgr := &volrep.VolumeGroupReplication{}
	Expect(k8sClient.Get(context.TODO(), g.globalVGRKey(), vgr)).To(Succeed())

	vgr.Status = volrep.VolumeGroupReplicationStatus{
		VolumeReplicationStatus: volrep.VolumeReplicationStatus{
			ObservedGeneration: vgr.Generation,
			State:              state,
		},
	}

	Expect(k8sClient.Status().Update(context.TODO(), vgr)).To(Succeed())
}

func (g *globalVGRTest) updateVRGSpec(inst *globalVRGInstance, state ramendrv1alpha1.ReplicationState) {
	vrgKey := types.NamespacedName{Name: inst.vrgName, Namespace: inst.namespace}

	Expect(retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		vrg := &ramendrv1alpha1.VolumeReplicationGroup{}
		if err := k8sClient.Get(context.TODO(), vrgKey, vrg); err != nil {
			return err
		}

		vrg.Spec.ReplicationState = state

		return k8sClient.Update(context.TODO(), vrg)
	})).To(Succeed())
}

func (g *globalVGRTest) deleteVRG(inst *globalVRGInstance) {
	vrgKey := types.NamespacedName{Name: inst.vrgName, Namespace: inst.namespace}
	vrg := &ramendrv1alpha1.VolumeReplicationGroup{}

	if err := k8sClient.Get(context.TODO(), vrgKey, vrg); err != nil {
		return
	}

	err := k8sClient.Delete(context.TODO(), vrg)
	if err != nil && !errors.IsNotFound(err) {
		Expect(err).ToNot(HaveOccurred())
	}
}

func deleteIgnoreNotFound(obj client.Object) {
	err := k8sClient.Delete(context.TODO(), obj)
	if err != nil && !errors.IsNotFound(err) {
		Expect(err).ToNot(HaveOccurred())
	}
}

func (g *globalVGRTest) cleanupAll() {
	g.deleteVRG(g.vrgA)
	g.deleteVRG(g.vrgB)

	pvList := &corev1.PersistentVolumeList{}
	Expect(k8sClient.List(context.TODO(), pvList)).To(Succeed())

	for idx := range pvList.Items {
		sc := pvList.Items[idx].Spec.StorageClassName
		if sc == g.scName || sc == g.scName+"-2" {
			deleteIgnoreNotFound(&pvList.Items[idx])
		}
	}

	for _, ns := range []string{g.vrgA.namespace, g.vrgB.namespace} {
		deleteIgnoreNotFound(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	}

	for _, scName := range []string{g.scName, g.scName + "-2"} {
		deleteIgnoreNotFound(&storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: scName}})
	}

	deleteIgnoreNotFound(&volrep.VolumeGroupReplicationClass{ObjectMeta: metav1.ObjectMeta{Name: g.vgrcName}})
}
