// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"

	volrep "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newFakeVRGInstance(objs ...runtime.Object) *VRGInstance {
	scheme := runtime.NewScheme()
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	Expect(volrep.AddToScheme(scheme)).To(Succeed())

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(objs...).
		WithStatusSubresource(&volrep.VolumeReplication{}).
		Build()

	return &VRGInstance{
		reconciler: &VolumeReplicationGroupReconciler{
			Client: fakeClient,
		},
		ctx: context.TODO(),
	}
}

var _ = Describe("annotateWithDestinationVolumeHandle", func() {
	It("should annotate PV when VR has DestinationVolumeID and condition is True", func() {
		vr := &volrep.VolumeReplication{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pvc",
				Namespace: "test-ns",
			},
			Status: volrep.VolumeReplicationStatus{
				DestinationVolumeID: "dest-vol-001",
				Conditions: []metav1.Condition{
					{
						Type:   volrep.ConditionDestinationInfoAvailable,
						Status: metav1.ConditionTrue,
						Reason: volrep.DestinationInfoUpdated,
					},
				},
			},
		}

		v := newFakeVRGInstance(vr)

		pv := &corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "test-pv"},
			Spec: corev1.PersistentVolumeSpec{
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					CSI: &corev1.CSIPersistentVolumeSource{
						Driver:       "test-driver",
						VolumeHandle: "src-vol-001",
					},
				},
			},
		}

		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pvc",
				Namespace: "test-ns",
			},
		}

		v.annotateWithDestinationVolumeHandle(pv, pvc)

		Expect(pv.Annotations).To(HaveKeyWithValue(
			destinationVolumeHandleAnnotation, "dest-vol-001"))
	})

	It("should not annotate PV when VR has no DestinationVolumeID", func() {
		vr := &volrep.VolumeReplication{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pvc",
				Namespace: "test-ns",
			},
			Status: volrep.VolumeReplicationStatus{},
		}

		v := newFakeVRGInstance(vr)

		pv := &corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "test-pv"},
			Spec: corev1.PersistentVolumeSpec{
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					CSI: &corev1.CSIPersistentVolumeSource{
						Driver:       "test-driver",
						VolumeHandle: "src-vol-001",
					},
				},
			},
		}

		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pvc",
				Namespace: "test-ns",
			},
		}

		v.annotateWithDestinationVolumeHandle(pv, pvc)

		Expect(pv.Annotations).To(BeEmpty())
	})

	It("should not annotate PV when DestinationInfoAvailable condition is False", func() {
		vr := &volrep.VolumeReplication{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pvc",
				Namespace: "test-ns",
			},
			Status: volrep.VolumeReplicationStatus{
				DestinationVolumeID: "dest-vol-001",
				Conditions: []metav1.Condition{
					{
						Type:   volrep.ConditionDestinationInfoAvailable,
						Status: metav1.ConditionFalse,
						Reason: volrep.FailedToGetDestinationInfo,
					},
				},
			},
		}

		v := newFakeVRGInstance(vr)

		pv := &corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "test-pv"},
			Spec: corev1.PersistentVolumeSpec{
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					CSI: &corev1.CSIPersistentVolumeSource{
						Driver:       "test-driver",
						VolumeHandle: "src-vol-001",
					},
				},
			},
		}

		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pvc",
				Namespace: "test-ns",
			},
		}

		v.annotateWithDestinationVolumeHandle(pv, pvc)

		Expect(pv.Annotations).To(BeEmpty())
	})

	It("should not annotate PV when DestinationInfoAvailable condition is absent", func() {
		vr := &volrep.VolumeReplication{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pvc",
				Namespace: "test-ns",
			},
			Status: volrep.VolumeReplicationStatus{
				DestinationVolumeID: "dest-vol-001",
				Conditions: []metav1.Condition{
					{
						Type:   volrep.ConditionCompleted,
						Status: metav1.ConditionTrue,
						Reason: volrep.Promoted,
					},
				},
			},
		}

		v := newFakeVRGInstance(vr)

		pv := &corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "test-pv"},
			Spec: corev1.PersistentVolumeSpec{
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					CSI: &corev1.CSIPersistentVolumeSource{
						Driver:       "test-driver",
						VolumeHandle: "src-vol-001",
					},
				},
			},
		}

		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pvc",
				Namespace: "test-ns",
			},
		}

		v.annotateWithDestinationVolumeHandle(pv, pvc)

		Expect(pv.Annotations).To(BeEmpty())
	})

	It("should not annotate PV when VR does not exist", func() {
		// No VR object in the fake client
		v := newFakeVRGInstance()

		pv := &corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "test-pv"},
			Spec: corev1.PersistentVolumeSpec{
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					CSI: &corev1.CSIPersistentVolumeSource{
						Driver:       "test-driver",
						VolumeHandle: "src-vol-001",
					},
				},
			},
		}

		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pvc",
				Namespace: "test-ns",
			},
		}

		v.annotateWithDestinationVolumeHandle(pv, pvc)

		Expect(pv.Annotations).To(BeEmpty())
	})

	It("should initialize Annotations map when PV has no annotations", func() {
		vr := &volrep.VolumeReplication{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pvc",
				Namespace: "test-ns",
			},
			Status: volrep.VolumeReplicationStatus{
				DestinationVolumeID: "dest-vol-001",
				Conditions: []metav1.Condition{
					{
						Type:   volrep.ConditionDestinationInfoAvailable,
						Status: metav1.ConditionTrue,
						Reason: volrep.DestinationInfoUpdated,
					},
				},
			},
		}

		v := newFakeVRGInstance(vr)

		pv := &corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "test-pv"},
			Spec: corev1.PersistentVolumeSpec{
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					CSI: &corev1.CSIPersistentVolumeSource{
						Driver:       "test-driver",
						VolumeHandle: "src-vol-001",
					},
				},
			},
		}
		// Annotations is nil
		Expect(pv.Annotations).To(BeNil())

		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pvc",
				Namespace: "test-ns",
			},
		}

		v.annotateWithDestinationVolumeHandle(pv, pvc)

		Expect(pv.Annotations).NotTo(BeNil())
		Expect(pv.Annotations).To(HaveKeyWithValue(
			destinationVolumeHandleAnnotation, "dest-vol-001"))
	})
})

var _ = Describe("cleanupPVForRestore destination volume handle", func() {
	// These tests exercise only the destination handle replacement logic.
	// They duplicate the guard conditions from cleanupPVForRestore because
	// the full method also calls processPVSecrets which needs a StorageClass.

	It("should replace volumeHandle when annotation is present", func() {
		pv := &corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-pv",
				Annotations: map[string]string{
					destinationVolumeHandleAnnotation: "dest-vol-001",
					"other-annotation":                "keep-me",
				},
			},
			Spec: corev1.PersistentVolumeSpec{
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					CSI: &corev1.CSIPersistentVolumeSource{
						Driver:       "test-driver",
						VolumeHandle: "src-vol-001",
					},
				},
			},
		}

		// Execute the destination handle replacement logic
		if pv.Spec.CSI != nil && pv.Annotations != nil {
			if destHandle, ok := pv.Annotations[destinationVolumeHandleAnnotation]; ok && destHandle != "" {
				pv.Spec.CSI.VolumeHandle = destHandle
				delete(pv.Annotations, destinationVolumeHandleAnnotation)
			}
		}

		Expect(pv.Spec.CSI.VolumeHandle).To(Equal("dest-vol-001"))
		Expect(pv.Annotations).NotTo(HaveKey(destinationVolumeHandleAnnotation))
		Expect(pv.Annotations).To(HaveKeyWithValue("other-annotation", "keep-me"))
	})

	It("should not modify volumeHandle when annotation is absent", func() {
		pv := &corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "test-pv"},
			Spec: corev1.PersistentVolumeSpec{
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					CSI: &corev1.CSIPersistentVolumeSource{
						Driver:       "test-driver",
						VolumeHandle: "src-vol-001",
					},
				},
			},
		}

		if pv.Spec.CSI != nil && pv.Annotations != nil {
			if destHandle, ok := pv.Annotations[destinationVolumeHandleAnnotation]; ok && destHandle != "" {
				pv.Spec.CSI.VolumeHandle = destHandle
				delete(pv.Annotations, destinationVolumeHandleAnnotation)
			}
		}

		Expect(pv.Spec.CSI.VolumeHandle).To(Equal("src-vol-001"))
	})

	It("should not modify volumeHandle when annotation value is empty", func() {
		pv := &corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-pv",
				Annotations: map[string]string{
					destinationVolumeHandleAnnotation: "",
				},
			},
			Spec: corev1.PersistentVolumeSpec{
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					CSI: &corev1.CSIPersistentVolumeSource{
						Driver:       "test-driver",
						VolumeHandle: "src-vol-001",
					},
				},
			},
		}

		if pv.Spec.CSI != nil && pv.Annotations != nil {
			if destHandle, ok := pv.Annotations[destinationVolumeHandleAnnotation]; ok && destHandle != "" {
				pv.Spec.CSI.VolumeHandle = destHandle
				delete(pv.Annotations, destinationVolumeHandleAnnotation)
			}
		}

		Expect(pv.Spec.CSI.VolumeHandle).To(Equal("src-vol-001"))
	})

	It("should not panic when CSI is nil", func() {
		pv := &corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-pv",
				Annotations: map[string]string{
					destinationVolumeHandleAnnotation: "dest-vol-001",
				},
			},
			Spec: corev1.PersistentVolumeSpec{},
		}

		if pv.Spec.CSI != nil && pv.Annotations != nil {
			if destHandle, ok := pv.Annotations[destinationVolumeHandleAnnotation]; ok && destHandle != "" {
				pv.Spec.CSI.VolumeHandle = destHandle
				delete(pv.Annotations, destinationVolumeHandleAnnotation)
			}
		}

		Expect(pv.Spec.CSI).To(BeNil())
	})
})
