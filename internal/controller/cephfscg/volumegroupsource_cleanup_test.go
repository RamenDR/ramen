// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package cephfscg_test

import (
	"context"
	"errors"
	"os"

	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	vgsv1beta1 "github.com/red-hat-storage/external-snapshotter/client/v8/apis/volumegroupsnapshot/v1beta1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	internalController "github.com/ramendr/ramen/internal/controller"
	"github.com/ramendr/ramen/internal/controller/cephfscg"
	"github.com/ramendr/ramen/internal/controller/util"
)

const holdFinalizer = "test.ramen.dev/hold"

var _ = Describe("VolumeGroupSnapshot deletion cleanup", func() {
	const (
		ns      = "vgs-cleanup-ns"
		rgsName = "vgs-cleanup"
		pvcName = "cleanup-app"
	)

	var handler cephfscg.VolumeGroupSourceHandler

	BeforeEach(func() {
		ensureNamespace(ns)

		rgs := &ramendrv1alpha1.ReplicationGroupSource{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rgsName,
				Namespace: ns,
				Labels:    map[string]string{util.VRGOwnerNameLabel: "cleanup-vrg"},
			},
			Spec: ramendrv1alpha1.ReplicationGroupSourceSpec{
				VolumeGroupSnapshotClassName: "vgsc",
				VolumeGroupSnapshotSource: &metav1.LabelSelector{
					MatchLabels: map[string]string{"app": "cleanup"},
				},
			},
		}
		handler = cephfscg.NewVolumeGroupSourceHandler(
			k8sClient, rgs, internalController.DefaultCephFSCSIDriverName, nil, testLogger,
		)
	})

	It("does not create a VolumeGroupSnapshot when the ReplicationGroupSource is deleting", func() {
		rgs := &ramendrv1alpha1.ReplicationGroupSource{
			ObjectMeta: metav1.ObjectMeta{
				Name:              rgsName,
				Namespace:         ns,
				DeletionTimestamp: &metav1.Time{Time: metav1.Now().Time},
				Finalizers:        []string{cephfscg.RGSProtectionFinalizer},
			},
		}

		_, err := handler.CreateOrUpdateVolumeGroupSnapshot(context.Background(), rgs)
		Expect(err).To(HaveOccurred())
		Expect(vgsExists(rgsName, ns)).To(BeFalse())
	})

	It("removes a not-ready group snapshot and the member group-protection finalizer", func() {
		createNotReadyVGS(rgsName, ns, nil)
		createMemberSnapshot(rgsName, ns, "member-ready-false", pvcName)
		createPlainPVC(ns, "vs-"+pvcName)

		// envtest adds kubernetes.io/pvc-protection and does not run the
		// controller that clears it, so the first pass waits on that PVC.
		Expect(handler.CleanVolumeGroupSnapshot(context.Background())).To(
			MatchError(cephfscg.ErrVolumeGroupSnapshotCleanupPending))
		Expect(vgsExists(rgsName, ns)).To(BeFalse())
		Expect(volumeSnapshotExists("member-ready-false", ns)).To(BeFalse())

		stripFinalizer(ns, "vs-"+pvcName, "kubernetes.io/pvc-protection")
		Expect(handler.CleanVolumeGroupSnapshot(context.Background())).To(Succeed())
		Expect(pvcExists(ns, "vs-"+pvcName)).To(BeFalse())
	})

	It("keeps the member group-protection finalizer while the VolumeGroupSnapshot still exists", func() {
		createNotReadyVGS(rgsName, ns, []string{holdFinalizer})
		createMemberSnapshot(rgsName, ns, "member-held", pvcName)

		err := handler.CleanVolumeGroupSnapshot(context.Background())
		Expect(err).To(MatchError(cephfscg.ErrVolumeGroupSnapshotCleanupPending))

		vs := &snapv1.VolumeSnapshot{}
		Expect(k8sClient.Get(context.Background(), types.NamespacedName{
			Name: "member-held", Namespace: ns,
		}, vs)).To(Succeed())
		Expect(vs.Finalizers).To(ContainElement(cephfscg.VolumeSnapshotInGroupProtectionFinalizer))

		Expect(k8sClient.Get(context.Background(), types.NamespacedName{
			Name: rgsName, Namespace: ns,
		}, &vgsv1beta1.VolumeGroupSnapshot{})).To(Succeed())
	})

	It("deletes a leftover VolumeGroupSnapshotContent after the group snapshot is gone", func() {
		contentName := "groupsnapcontent-vgs-cleanup"
		Expect(k8sClient.Create(context.Background(), &vgsv1beta1.VolumeGroupSnapshotContent{
			ObjectMeta: metav1.ObjectMeta{Name: contentName},
			Spec: vgsv1beta1.VolumeGroupSnapshotContentSpec{
				DeletionPolicy: snapv1.VolumeSnapshotContentDelete,
				Driver:         internalController.DefaultCephFSCSIDriverName,
				VolumeGroupSnapshotRef: corev1.ObjectReference{
					Name:      rgsName,
					Namespace: ns,
				},
				Source: vgsv1beta1.VolumeGroupSnapshotContentSource{
					VolumeHandles: []string{"vol-handle"},
				},
			},
		})).To(Succeed())

		Expect(handler.CleanVolumeGroupSnapshot(context.Background())).To(Succeed())
		err := k8sClient.Get(context.Background(), types.NamespacedName{Name: contentName},
			&vgsv1beta1.VolumeGroupSnapshotContent{})
		Expect(k8serrors.IsNotFound(err)).To(BeTrue())
	})

	AfterEach(func() {
		releaseAndDeleteVGS(rgsName, ns)
		stripVolumeSnapshotFinalizers(ns, "member-ready-false", "member-held")
		deleteIfPresent(ns, "member-ready-false", "member-held")
		stripFinalizer(ns, "vs-"+pvcName, "kubernetes.io/pvc-protection")
		deletePVCIfPresent(ns, "vs-"+pvcName)
		Expect(client.IgnoreNotFound(k8sClient.Delete(context.Background(), &vgsv1beta1.VolumeGroupSnapshotContent{
			ObjectMeta: metav1.ObjectMeta{Name: "groupsnapcontent-vgs-cleanup"},
		}))).To(Succeed())
	})
})

var _ = Describe("Diff sync unprotect", func() {
	const (
		ns      = "diff-cleanup-ns"
		rgsName = "diff-cleanup"
	)

	var handler cephfscg.VolumeGroupSourceHandler

	BeforeEach(func() {
		ensureNamespace(ns)
		rgs := &ramendrv1alpha1.ReplicationGroupSource{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rgsName,
				Namespace: ns,
				Labels:    map[string]string{util.VRGOwnerNameLabel: "diff-vrg"},
			},
			Spec: ramendrv1alpha1.ReplicationGroupSourceSpec{
				VolumeGroupSnapshotClassName: "vgsc",
				VolumeGroupSnapshotSource: &metav1.LabelSelector{
					MatchLabels: map[string]string{"app": "diff-cleanup"},
				},
			},
		}
		handler = cephfscg.NewDiffVolumeGroupSourceHandler(
			k8sClient, rgs, internalController.DefaultCephFSCSIDriverName, nil, testLogger,
		)
	})

	It("deletes preserved previous VolumeGroupSnapshots on unprotect", func() {
		createLabeledVGS(rgsName+"-prev", ns, rgsName, cephfscg.VGSStatusPrevious)
		createLabeledVGS(rgsName+"-curr", ns, rgsName, cephfscg.VGSStatusCurrent)
		createMemberSnapshot(rgsName+"-prev", ns, "diff-member", "diff-app")

		Expect(handler.DeleteOwnedVolumeGroupSnapshots(context.Background())).To(Succeed())
		Expect(vgsExists(rgsName+"-prev", ns)).To(BeFalse())
		Expect(vgsExists(rgsName+"-curr", ns)).To(BeFalse())
		Expect(volumeSnapshotExists("diff-member", ns)).To(BeFalse())
	})

	It("still preserves the current VolumeGroupSnapshot between syncs", func() {
		createLabeledVGS(rgsName+"-keep", ns, rgsName, cephfscg.VGSStatusCurrent)
		updateVGSReady(rgsName+"-keep", ns)

		Expect(handler.CleanVolumeGroupSnapshot(context.Background())).To(Succeed())

		vgs := &vgsv1beta1.VolumeGroupSnapshot{}
		Expect(k8sClient.Get(context.Background(), types.NamespacedName{
			Name: rgsName + "-keep", Namespace: ns,
		}, vgs)).To(Succeed())
		Expect(vgs.Labels[cephfscg.VGSStatusLabelKey]).To(Equal(cephfscg.VGSStatusPrevious))
	})

	AfterEach(func() {
		for _, name := range []string{rgsName + "-prev", rgsName + "-curr", rgsName + "-keep"} {
			releaseAndDeleteVGS(name, ns)
		}

		deleteIfPresent(ns, "diff-member")
	})
})

var _ = Describe("ReplicationGroupSource deletion reconcile", func() {
	const (
		opNS    = "ramen-cg-cleanup"
		wlNS    = "cg-rgs-reconcile"
		rgsName = "cg-reconcile-rgs"
		vrgName = "cg-reconcile-vrg"
	)

	var reconciler *internalController.ReplicationGroupSourceReconciler

	BeforeEach(func() {
		releaseAndDeleteRGS(rgsName, wlNS)
		releaseAndDeleteVGS(rgsName, wlNS)
		stripVolumeSnapshotFinalizers(wlNS, "reconcile-member")
		deleteIfPresent(wlNS, "reconcile-member")

		origType := internalController.ControllerType
		origNS, hadNS := os.LookupEnv("POD_NAMESPACE")
		internalController.ControllerType = ramendrv1alpha1.DRClusterType

		Expect(os.Setenv("POD_NAMESPACE", opNS)).To(Succeed())

		DeferCleanup(func() {
			internalController.ControllerType = origType

			if hadNS {
				Expect(os.Setenv("POD_NAMESPACE", origNS)).To(Succeed())

				return
			}

			Expect(os.Unsetenv("POD_NAMESPACE")).To(Succeed())
		})

		ensureNamespace(opNS)
		ensureNamespace(wlNS)
		ensureRamenConfig(opNS)

		reconciler = &internalController.ReplicationGroupSourceReconciler{
			Client: k8sClient,
			Scheme: scheme.Scheme,
			Log:    testLogger,
		}
		reconciler.SetVolumeGroupSnapshotCRsWatchedForTest(true)
	})

	It("does not create a VolumeGroupSnapshot when the owning VRG is deleting", func() {
		createDeletingVRG(vrgName, wlNS)

		schedule := "* * * * *"
		createRGS(rgsName, wlNS, vrgName, false, &schedule)

		for range 2 {
			_, err := reconciler.Reconcile(context.Background(), ctrlRequest(rgsName, wlNS))
			Expect(err).NotTo(HaveOccurred())
		}

		Expect(vgsExists(rgsName, wlNS)).To(BeFalse())

		rgs := &ramendrv1alpha1.ReplicationGroupSource{}
		Expect(k8sClient.Get(context.Background(), types.NamespacedName{
			Name: rgsName, Namespace: wlNS,
		}, rgs)).To(Succeed())
		Expect(rgs.Finalizers).To(ContainElement(cephfscg.RGSProtectionFinalizer))
	})

	It("removes the RGS finalizer only after a not-ready group snapshot is gone", func() {
		createDeletingVRG(vrgName, wlNS)
		createRGS(rgsName, wlNS, vrgName, true, nil)
		createNotReadyVGS(rgsName, wlNS, nil)
		createMemberSnapshot(rgsName, wlNS, "reconcile-member", "reconcile-app")

		_, err := reconciler.Reconcile(context.Background(), ctrlRequest(rgsName, wlNS))
		Expect(err).NotTo(HaveOccurred())
		Expect(vgsExists(rgsName, wlNS)).To(BeFalse())
		Expect(volumeSnapshotExists("reconcile-member", wlNS)).To(BeFalse())

		err = k8sClient.Get(context.Background(), types.NamespacedName{
			Name: rgsName, Namespace: wlNS,
		}, &ramendrv1alpha1.ReplicationGroupSource{})
		Expect(k8serrors.IsNotFound(err)).To(BeTrue())
	})

	It("makes VRG deletion requeue while the ReplicationGroupSource still exists", func() {
		createDeletingVRG(vrgName, wlNS)
		createRGS(rgsName, wlNS, vrgName, false, nil)

		err := cephfscg.DeleteRGS(context.Background(), k8sClient, vrgName, wlNS, testLogger)
		Expect(errors.Is(err, cephfscg.ErrReplicationGroupSourceDeleting)).To(BeTrue())

		rgs := &ramendrv1alpha1.ReplicationGroupSource{}
		Expect(k8sClient.Get(context.Background(), types.NamespacedName{
			Name: rgsName, Namespace: wlNS,
		}, rgs)).To(Succeed())
		Expect(rgs.DeletionTimestamp.IsZero()).To(BeFalse())
		Expect(rgs.Finalizers).To(ContainElement(cephfscg.RGSProtectionFinalizer))
	})

	AfterEach(func() {
		releaseAndDeleteRGS(rgsName, wlNS)
		deleteIfPresent(wlNS, "reconcile-member")
		releaseAndDeleteVGS(rgsName, wlNS)
	})
})

func ensureNamespace(name string) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	err := k8sClient.Create(context.Background(), ns)
	Expect(client.IgnoreAlreadyExists(err)).To(Succeed())
}

func ensureRamenConfig(namespace string) {
	cm, err := internalController.ConfigMapNew(
		namespace,
		internalController.DrClusterOperatorConfigMapName,
		&ramendrv1alpha1.RamenConfig{},
	)
	Expect(err).NotTo(HaveOccurred())

	err = k8sClient.Create(context.Background(), cm)
	Expect(client.IgnoreAlreadyExists(err)).To(Succeed())
}

func createDeletingVRG(name, namespace string) {
	vrg := &ramendrv1alpha1.VolumeReplicationGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  namespace,
			Finalizers: []string{"volumereplicationgroups.ramendr.openshift.io/vrg-protection"},
		},
		Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
			ReplicationState: ramendrv1alpha1.Primary,
			S3Profiles:       []string{"s3profile"},
		},
	}
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(context.Background(), vrg))).To(Succeed())
	Expect(client.IgnoreNotFound(k8sClient.Delete(context.Background(), vrg))).To(Succeed())
}

func createRGS(name, namespace, vrgName string, deleting bool, schedule *string) {
	rgs := &ramendrv1alpha1.ReplicationGroupSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				util.VRGOwnerNameLabel:      vrgName,
				util.VRGOwnerNamespaceLabel: namespace,
			},
			Finalizers: []string{cephfscg.RGSProtectionFinalizer},
		},
		Spec: ramendrv1alpha1.ReplicationGroupSourceSpec{
			VolumeGroupSnapshotClassName: "vgsc",
			VolumeGroupSnapshotSource: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "reconcile"},
			},
			Trigger: &ramendrv1alpha1.ReplicationSourceTriggerSpec{Schedule: schedule},
		},
	}
	Expect(k8sClient.Create(context.Background(), rgs)).To(Succeed())

	if deleting {
		Expect(client.IgnoreNotFound(k8sClient.Delete(context.Background(), rgs))).To(Succeed())
	}
}

func createNotReadyVGS(name, namespace string, finalizers []string) {
	className := "vgsc"
	vgs := &vgsv1beta1.VolumeGroupSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  namespace,
			Finalizers: finalizers,
			Labels:     map[string]string{util.RGSOwnerLabel: name},
		},
		Spec: vgsv1beta1.VolumeGroupSnapshotSpec{
			VolumeGroupSnapshotClassName: &className,
			Source: vgsv1beta1.VolumeGroupSnapshotSource{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "cleanup"}},
			},
		},
	}
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(context.Background(), vgs))).To(Succeed())
}

func createLabeledVGS(name, namespace, owner, status string) {
	className := "vgsc"
	vgs := &vgsv1beta1.VolumeGroupSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				util.RGSOwnerLabel:         owner,
				cephfscg.VGSStatusLabelKey: status,
			},
		},
		Spec: vgsv1beta1.VolumeGroupSnapshotSpec{
			VolumeGroupSnapshotClassName: &className,
			Source: vgsv1beta1.VolumeGroupSnapshotSource{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "diff-cleanup"}},
			},
		},
	}
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(context.Background(), vgs))).To(Succeed())
}

func updateVGSReady(name, namespace string) {
	vgs := &vgsv1beta1.VolumeGroupSnapshot{}
	Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: name, Namespace: namespace}, vgs)).To(Succeed())

	ready := true
	vgs.Status = &vgsv1beta1.VolumeGroupSnapshotStatus{ReadyToUse: &ready}
	Expect(k8sClient.Status().Update(context.Background(), vgs)).To(Succeed())
}

func createMemberSnapshot(vgsName, namespace, vsName, pvcName string) {
	vgs := &vgsv1beta1.VolumeGroupSnapshot{}
	Expect(k8sClient.Get(context.Background(), types.NamespacedName{
		Name: vgsName, Namespace: namespace,
	}, vgs)).To(Succeed())

	vs := &snapv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      vsName,
			Namespace: namespace,
			Finalizers: []string{
				cephfscg.VolumeSnapshotInGroupProtectionFinalizer,
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: vgsv1beta1.SchemeGroupVersion.String(),
				Kind:       "VolumeGroupSnapshot",
				Name:       vgs.Name,
				UID:        vgs.UID,
			}},
		},
		Spec: snapv1.VolumeSnapshotSpec{
			Source: snapv1.VolumeSnapshotSource{PersistentVolumeClaimName: &pvcName},
		},
	}

	Expect(client.IgnoreAlreadyExists(k8sClient.Create(context.Background(), vs))).To(Succeed())
}

func createPlainPVC(namespace, name string) {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resourceQuantity(),
				},
			},
		},
	}
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(context.Background(), pvc))).To(Succeed())
}

func resourceQuantity() resource.Quantity {
	return resource.MustParse("1Gi")
}

func vgsExists(name, namespace string) bool {
	err := k8sClient.Get(context.Background(), types.NamespacedName{Name: name, Namespace: namespace},
		&vgsv1beta1.VolumeGroupSnapshot{})

	return !k8serrors.IsNotFound(err)
}

func volumeSnapshotExists(name, namespace string) bool {
	err := k8sClient.Get(context.Background(), types.NamespacedName{Name: name, Namespace: namespace},
		&snapv1.VolumeSnapshot{})

	return !k8serrors.IsNotFound(err)
}

func pvcExists(namespace, name string) bool {
	err := k8sClient.Get(context.Background(), types.NamespacedName{Name: name, Namespace: namespace},
		&corev1.PersistentVolumeClaim{})

	return !k8serrors.IsNotFound(err)
}

func releaseAndDeleteVGS(name, namespace string) {
	vgs := &vgsv1beta1.VolumeGroupSnapshot{}
	if err := k8sClient.Get(context.Background(), types.NamespacedName{
		Name: name, Namespace: namespace,
	}, vgs); err != nil {
		return
	}

	controllerutil.RemoveFinalizer(vgs, holdFinalizer)
	Expect(client.IgnoreNotFound(k8sClient.Update(context.Background(), vgs))).To(Succeed())
	Expect(client.IgnoreNotFound(k8sClient.Delete(context.Background(), vgs))).To(Succeed())
}

func releaseAndDeleteRGS(name, namespace string) {
	rgs := &ramendrv1alpha1.ReplicationGroupSource{}
	if err := k8sClient.Get(context.Background(), types.NamespacedName{
		Name: name, Namespace: namespace,
	}, rgs); err != nil {
		return
	}

	controllerutil.RemoveFinalizer(rgs, cephfscg.RGSProtectionFinalizer)
	Expect(client.IgnoreNotFound(k8sClient.Update(context.Background(), rgs))).To(Succeed())
	Expect(client.IgnoreNotFound(k8sClient.Delete(context.Background(), rgs))).To(Succeed())
}

func stripFinalizer(namespace, name, finalizer string) {
	pvc := &corev1.PersistentVolumeClaim{}
	if err := k8sClient.Get(context.Background(), types.NamespacedName{
		Name: name, Namespace: namespace,
	}, pvc); err != nil {
		return
	}

	controllerutil.RemoveFinalizer(pvc, finalizer)
	Expect(client.IgnoreNotFound(k8sClient.Update(context.Background(), pvc))).To(Succeed())
}

func stripVolumeSnapshotFinalizers(namespace string, names ...string) {
	for _, name := range names {
		vs := &snapv1.VolumeSnapshot{}
		if err := k8sClient.Get(context.Background(), types.NamespacedName{
			Name: name, Namespace: namespace,
		}, vs); err != nil {
			continue
		}

		vs.Finalizers = nil
		Expect(client.IgnoreNotFound(k8sClient.Update(context.Background(), vs))).To(Succeed())
	}
}

func deleteIfPresent(namespace string, names ...string) {
	for _, name := range names {
		Expect(client.IgnoreNotFound(k8sClient.Delete(context.Background(), &snapv1.VolumeSnapshot{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		}))).To(Succeed())
	}
}

func deletePVCIfPresent(namespace, name string) {
	Expect(client.IgnoreNotFound(k8sClient.Delete(context.Background(), &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}))).To(Succeed())
}

func ctrlRequest(name, namespace string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: namespace}}
}
