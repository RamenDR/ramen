package controllers_test

import (
	"context"
	"fmt"

	volrep "github.com/csi-addons/volume-replication-operator/api/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Test VolumeReplicationGroup", func() {
	// Try the simple case of creating VRG, PVC, PV and
	// check whether VolRep resources are created or not
	var vrgTestCases []vrgTest
	Context("in primary state", func() {
		It("sets up PVCs, PVs and VRGs", func() {
			for c := 0; c < 5; c++ {
				v := newVRGTestCase(c)
				vrgTestCases = append(vrgTestCases, v)
			}
		})
		It("waits for VRG to create a VR for each PVC", func() {
			for c := 0; c < len(vrgTestCases); c++ {
				v := vrgTestCases[c]
				expectedVRCount := len(v.pvcNames)
				v.waitForVRCountToMatch(expectedVRCount)
			}
		})
		It("cleans up after testing", func() {
			for c := 0; c < len(vrgTestCases); c++ {
				v := vrgTestCases[c]
				v.cleanup()
			}
		})
	})

	// Creates VRG. PVCs and PV are created with Status.Phase
	// set to pending and VolRep should not be created until
	// all the PVCs and PVs are bound. So, these tests then
	// change the Status.Phase of PVCs and PVs to bound state,
	// and then checks whether appropriate number of VolRep
	// resources have been created or not.
	var vrgTests []vrgTest
	Context("in primary state", func() {
		It("sets up non-bound PVCs, PVs and then bind them", func() {
			for c := 0; c < 5; c++ {
				// Test the scenario where the pvc is not bound yet
				// and expect no VRs to be created.
				v := newVRGTestCaseBindInfo(c, corev1.ClaimPending, corev1.VolumePending, false)
				vrgTests = append(vrgTests, v)
			}
		})
		It("expect no VR to be created as PVC not bound", func() {
			for c := 0; c < len(vrgTests); c++ {
				v := vrgTests[c]
				v.waitForVRCountToMatch(0)
			}
		})
		It("bind each pv to corresponding pvc", func() {
			for c := 0; c < len(vrgTests); c++ {
				v := vrgTests[c]
				v.bindPVAndPVC()
				v.verifyPVCBindingToPV(true)
			}
		})
		It("waits for VRG to create a VR for each PVC bind", func() {
			for c := 0; c < len(vrgTests); c++ {
				v := vrgTests[c]
				expectedVRCount := len(v.pvcNames)
				v.waitForVRCountToMatch(expectedVRCount)
			}
		})

		// TODO: Add tests for checking the status of VRG
		It("cleans up after testing", func() {
			for c := 0; c < len(vrgTests); c++ {
				v := vrgTests[c]
				v.cleanup()
			}
		})
	})
})

type vrgTest struct {
	namespace string
	pvNames   []string
	pvcNames  []string
	vrgName   string
}

// Use to generate unique object names across multiple VRG test cases
var testCaseNumber = 0

// newVRGTestCase creates a new namespace, zero or more PVCs (equal to the
// input pvcCount), a PV for each PVC, and a VRG in primary state, with
// label selector that points to the PVCs created.
func newVRGTestCase(pvcCount int) vrgTest {
	return newVRGTestCaseBindInfo(pvcCount, corev1.ClaimBound, corev1.VolumeBound,
		true)
}

// newVRGTestCaseBindInfo creates a new namespace, zero or more PVCs (equal
// to the input pvcCount), a PV for each PVC, and a VRG in primary state,
// with label selector that points to the PVCs created. Each PVC is created
// with Status.Phase set to ClaimPending instead of ClaimBound. Expectation
// is that, until pvc is not bound, VolRep resources should not be created
// by VRG.
func newVRGTestCaseBindInfo(pvcCount int, claimBindInfo corev1.PersistentVolumeClaimPhase,
	volumeBindInfo corev1.PersistentVolumePhase, checkBind bool) vrgTest {
	pvcLabels := map[string]string{}
	objectNameSuffix := 'a' + testCaseNumber
	testCaseNumber++ // each invocation of this function is a new test case

	v := vrgTest{
		namespace: fmt.Sprintf("envtest-ns-%c", objectNameSuffix),
		vrgName:   fmt.Sprintf("vrg-%c", objectNameSuffix),
	}

	By("Creating namespace " + v.namespace)
	v.createNamespace()

	// Setup PVC labels
	if pvcCount > 0 {
		pvcLabels["appclass"] = "platinum"
		pvcLabels["environment"] = fmt.Sprintf("dev.AZ1-%c", objectNameSuffix)
	}

	// Create the requested number of PVs and corresponding PVCs
	for i := 0; i < pvcCount; i++ {
		pvName := fmt.Sprintf("pv-%c-%02d", objectNameSuffix, i)
		pvcName := fmt.Sprintf("pvc-%c-%02d", objectNameSuffix, i)
		v.createPVC(pvcName, v.namespace, pvName, pvcLabels, claimBindInfo)
		v.createPV(pvName, pvcName, volumeBindInfo)
		v.pvNames = append(v.pvNames, pvName)
		v.pvcNames = append(v.pvcNames, pvcName)
	}

	v.createVRG(pvcLabels)

	// If checkBind is true, then check whether PVCs and PVs are
	// bound. Otherwise expect them to not have been bound.
	v.verifyPVCBindingToPV(checkBind)

	return v
}

func (v *vrgTest) createNamespace() {
	By("creating namespace " + v.namespace)

	appNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: v.namespace}}
	err := k8sClient.Create(context.TODO(), appNamespace)
	expectedErr := errors.NewAlreadyExists(
		schema.GroupResource{Resource: "namespaces"}, v.namespace)
	Expect(err).To(SatisfyAny(BeNil(), MatchError(expectedErr)),
		"failed to create namespace %s", v.namespace)
}

func (v *vrgTest) createPV(pvName, claimName string, bindInfo corev1.PersistentVolumePhase) {
	By("creating PV " + pvName)

	capacity := corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")}
	accessModes := []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
	hostPathType := corev1.HostPathDirectoryOrCreate
	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: pvName},
		Spec: corev1.PersistentVolumeSpec{
			Capacity: capacity,
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: "/tmp/kube",
					Type: &hostPathType,
				},
			},
			AccessModes: accessModes,
			ClaimRef: &corev1.ObjectReference{
				Kind:      "PersistentVolumeClaim",
				Namespace: v.namespace,
				Name:      claimName,
				// UID:       types.UID(claimName),
			},
			PersistentVolumeReclaimPolicy: "Delete",
			StorageClassName:              "manual",
			MountOptions:                  []string{},
			NodeAffinity: &corev1.VolumeNodeAffinity{
				Required: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      "KeyNode",
									Operator: corev1.NodeSelectorOpIn,
									Values: []string{
										"node1",
									},
								},
							},
						},
					},
				},
			},
		},
		Status: corev1.PersistentVolumeStatus{
			Phase: corev1.VolumeBound,
		},
	}

	err := k8sClient.Create(context.TODO(), pv)
	expectedErr := errors.NewAlreadyExists(
		schema.GroupResource{Resource: "persistentvolumes"}, pvName)
	Expect(err).To(SatisfyAny(BeNil(), MatchError(expectedErr)),
		"failed to create PV %s", pvName)

	pv.Status.Phase = bindInfo
	err = k8sClient.Status().Update(context.TODO(), pv)
	Expect(err).To(BeNil(),
		"failed to update status of PV %s", pvName)
}

func (v *vrgTest) createPVC(pvcName, namespace, volumeName string, labels map[string]string,
	bindInfo corev1.PersistentVolumeClaimPhase) {
	By("creating PVC " + pvcName)

	capacity := corev1.ResourceList{
		corev1.ResourceStorage: resource.MustParse("1Gi"),
	}
	storageclass := "manual"
	accessModes := []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
	pvc := &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Labels:    labels,
			Namespace: namespace,
			// ResourceVersion: "1",
			SelfLink: "/api/v1/namespaces/testns/persistentvolumeclaims/" + pvcName,
			UID:      types.UID(volumeName),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      accessModes,
			Resources:        corev1.ResourceRequirements{Requests: capacity},
			VolumeName:       volumeName,
			StorageClassName: &storageclass,
		},
	}

	err := k8sClient.Create(context.TODO(), pvc)
	expectedErr := errors.NewAlreadyExists(
		schema.GroupResource{Resource: "persistentvolumeclaims"}, pvcName)
	Expect(err).To(SatisfyAny(BeNil(), MatchError(expectedErr)),
		"failed to create PVC %s", pvcName)

	pvc.Status.Phase = bindInfo
	pvc.Status.AccessModes = accessModes
	pvc.Status.Capacity = capacity
	err = k8sClient.Status().Update(context.TODO(), pvc)
	Expect(err).To(BeNil(),
		"failed to update status of PVC %s", pvcName)
}

func (v *vrgTest) bindPVAndPVC() {
	By("Waiting for PVC to get bound to PVs for " + v.vrgName)

	for i := 0; i < len(v.pvcNames); i++ {
		// Bind PV
		pv := v.getPV(v.pvNames[i])
		pv.Status.Phase = corev1.VolumeBound
		err := k8sClient.Status().Update(context.TODO(), pv)
		Expect(err).To(BeNil(),
			"failed to update status of PV %s", v.pvNames[i])

		i := i // capture i for use in closure

		// Bind PVC
		pvc := v.getPVC(v.pvcNames[i])
		pvc.Status.Phase = corev1.ClaimBound
		err = k8sClient.Status().Update(context.TODO(), pvc)
		Expect(err).To(BeNil(),
			"failed to update status of PVC %s", v.pvcNames[i])
	}
}

func (v *vrgTest) createVRG(pvcLabels map[string]string) {
	By("creating VRG " + v.vrgName)

	vrg := &ramendrv1alpha1.VolumeReplicationGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      v.vrgName,
			Namespace: v.namespace,
		},
		Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
			PVCSelector:      metav1.LabelSelector{MatchLabels: pvcLabels},
			ReplicationState: "primary",
		},
	}
	err := k8sClient.Create(context.TODO(), vrg)
	expectedErr := errors.NewAlreadyExists(
		schema.GroupResource{
			Group:    "ramendr.openshift.io",
			Resource: "volumereplicationgroups",
		},
		v.vrgName)
	Expect(err).To(SatisfyAny(BeNil(), MatchError(expectedErr)),
		"failed to create VRG %s in %s", v.vrgName, v.namespace)
}

func (v *vrgTest) verifyPVCBindingToPV(checkBind bool) {
	By("Waiting for PVC to get bound to PVs for " + v.vrgName)

	for i := 0; i < len(v.pvcNames); i++ {
		_ = v.getPV(v.pvNames[i])
		i := i // capture i for use in closure
		Eventually(func() bool {
			pvc := v.getPVC(v.pvcNames[i])

			if checkBind == true {
				return pvc.Status.Phase == corev1.ClaimBound
			}

			return pvc.Status.Phase != corev1.ClaimBound
		}, timeout, interval).Should(BeTrue(),
			"while waiting for PVC %s to bind with PV %s",
			v.pvcNames[i], v.pvNames[i])
	}
}

func (v *vrgTest) getPV(pvName string) *corev1.PersistentVolume {
	pvLookupKey := types.NamespacedName{Name: pvName}
	pv := &corev1.PersistentVolume{}
	err := k8sClient.Get(context.TODO(), pvLookupKey, pv)
	Expect(err).NotTo(HaveOccurred(),
		"failed to get PV %s", pvName)

	return pv
}

func (v *vrgTest) getPVC(pvcName string) *corev1.PersistentVolumeClaim {
	key := types.NamespacedName{
		Namespace: v.namespace,
		Name:      pvcName,
	}

	pvc := &corev1.PersistentVolumeClaim{}
	err := k8sClient.Get(context.TODO(), key, pvc)
	Expect(err).NotTo(HaveOccurred(),
		"failed to get PVC %s", pvcName)

	return pvc
}

func (v *vrgTest) getVRG(vrgName string) *ramendrv1alpha1.VolumeReplicationGroup {
	key := types.NamespacedName{
		Namespace: v.namespace,
		Name:      vrgName,
	}

	vrg := &ramendrv1alpha1.VolumeReplicationGroup{}
	err := k8sClient.Get(context.TODO(), key, vrg)
	Expect(err).NotTo(HaveOccurred(),
		"failed to get VRG %s", vrgName)

	return vrg
}

func (v *vrgTest) cleanup() {
	v.cleanupPVCs()
	v.cleanupVRG()
	v.cleanupNamespace()
}

func (v *vrgTest) cleanupPVCs() {
	for _, pvcName := range v.pvcNames {
		if pvc := v.getPVC(pvcName); pvc != nil {
			err := k8sClient.Delete(context.TODO(), pvc)
			Expect(err).To(BeNil(),
				"failed to delete PVC %s", pvcName)
		}
	}
}

func (v *vrgTest) cleanupVRG() {
	vrg := v.getVRG(v.vrgName)
	err := k8sClient.Delete(context.TODO(), vrg)
	Expect(err).To(BeNil(),
		"failed to delete VRG %s", v.vrgName)
	v.waitForVRCountToMatch(0)
}

func (v *vrgTest) cleanupNamespace() {
	By("deleting namespace " + v.namespace)

	appNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: v.namespace}}
	err := k8sClient.Delete(context.TODO(), appNamespace)
	Expect(err).To(BeNil(),
		"failed to delete namespace %s", v.namespace)
	v.waitForNamespaceDeletion()
}

func (v *vrgTest) waitForVRCountToMatch(vrCount int) {
	By("Waiting for VRs to be deleted in " + v.namespace)

	// selector, err := metav1.LabelSelectorAsSelector(&vrg.Spec.PVCSelector)
	// Expect(err).To(BeNil())

	Eventually(func() int {
		listOptions := &client.ListOptions{
			// LabelSelector: selector,
			Namespace: v.namespace,
		}
		volRepList := &volrep.VolumeReplicationList{}
		err := k8sClient.List(context.TODO(), volRepList, listOptions)
		Expect(err).NotTo(HaveOccurred(),
			"failed to get a list of VRs in namespace %s", v.namespace)

		return len(volRepList.Items)
	}, timeout, interval).Should(BeNumerically("==", vrCount),
		"while waiting for VR count of %d in VRG %s of namespace %s",
		vrCount, v.vrgName, v.namespace)
}

func (v *vrgTest) waitForNamespaceDeletion() {
	By("Waiting for namespace deletion " + v.namespace)

	appNamespace := &corev1.Namespace{}
	nsObjectKey := client.ObjectKey{Name: v.namespace}

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), nsObjectKey, appNamespace)

		return err == nil
	}, timeout, interval).Should(BeTrue(),
		"while waiting for namespace %s to be deleted", v.namespace)
}
