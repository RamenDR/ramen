// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gomegatypes "github.com/onsi/gomega/types"

	"github.com/ramendr/ramen/controllers/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("PVCS_Util", func() {
	var testNamespace *corev1.Namespace
	var testCtx context.Context
	var cancel context.CancelFunc

	BeforeEach(func() {
		testCtx, cancel = context.WithCancel(context.TODO())

		// Create namespace for test
		testNamespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "pvc-util-test-ns-",
			},
		}
		Expect(k8sClient.Create(testCtx, testNamespace)).To(Succeed())
		Expect(testNamespace.GetName()).NotTo(BeEmpty())
	})

	AfterEach(func() {
		// All resources are namespaced, so this should clean it all up
		Expect(k8sClient.Delete(testCtx, testNamespace)).To(Succeed())

		cancel()
	})

	Describe("List PVCs by PVCSelector", func() {
		var pvcA *corev1.PersistentVolumeClaim
		var pvcB *corev1.PersistentVolumeClaim
		var pvcC *corev1.PersistentVolumeClaim
		var pvcD *corev1.PersistentVolumeClaim

		pvcCount := 4

		BeforeEach(func() {
			// Create some PVCs
			pvcA = createTestPVC(testCtx, testNamespace.GetName(),
				map[string]string{
					"test-label":           "aaa",
					util.CreatedByLabelKey: util.CreatedByLabelValueVolSync, // Created by volsync
					"mylabel":              "abc",
					"new-label":            "test",
				})

			pvcB = createTestPVC(testCtx, testNamespace.GetName(),
				map[string]string{
					"test-label": "bbb",
					"another":    "somethingelse",
					"new-label":  "test",
				})

			pvcC = createTestPVC(testCtx, testNamespace.GetName(),
				map[string]string{
					"test-label":           "ccc",
					util.CreatedByLabelKey: util.CreatedByLabelValueVolSync, // Created by volsync
					"mylabel":              "abc",
					"another":              "whynot",
					"new-label":            "test",
				})

			pvcD = createTestPVC(testCtx, testNamespace.GetName(),
				map[string]string{
					"test-label": "ddd",
					"mylabel":    "abc",
					"another":    "whynot",
				})
		})

		Context("When labelSelector is empty", func() {
			var pvcSelector metav1.LabelSelector

			It("Should list all PVCs when VolSync is disabled", func() {
				pvcList, err := util.ListPVCsByPVCSelector(testCtx, k8sClient, testLogger, pvcSelector,
					[]string{testNamespace.GetName()},
					true /* Volsync Disabled */)
				Expect(err).NotTo(HaveOccurred())
				Expect(pvcList).NotTo(BeNil())
				Expect(len(pvcList.Items)).To(Equal(pvcCount))
				Expect(pvcList.Items).Should(ConsistOf(
					HavePVCName(pvcA.GetName()),
					HavePVCName(pvcB.GetName()),
					HavePVCName(pvcC.GetName()),
					HavePVCName(pvcD.GetName()),
				))
			})

			It("Should filter out VolSync PVCs when VolSync is not disabled", func() {
				pvcList, err := util.ListPVCsByPVCSelector(testCtx, k8sClient, testLogger, pvcSelector,
					[]string{testNamespace.GetName()},
					false /* Volsync NOT disabled */)
				Expect(err).NotTo(HaveOccurred())
				Expect(pvcList).NotTo(BeNil())
				Expect(len(pvcList.Items)).To(Equal(pvcCount - 2)) // 2 PVCs are VolSync PVCs
				Expect(pvcList.Items).Should(ConsistOf(
					HavePVCName(pvcB.GetName()),
					HavePVCName(pvcD.GetName()),
				))
			})
		})

		Context("With a labelSelector with matchLabels", func() {
			pvcSelector := metav1.LabelSelector{
				MatchLabels: map[string]string{
					"mylabel": "abc", // Matches pvcA, pvcC, pvcD
				},
			}

			It("Should list matching PVCs when VolSync is disabled", func() {
				pvcList, err := util.ListPVCsByPVCSelector(testCtx, k8sClient, testLogger, pvcSelector,
					[]string{testNamespace.GetName()},
					true /* Volsync Disabled */)
				Expect(err).NotTo(HaveOccurred())
				Expect(pvcList).NotTo(BeNil())
				Expect(len(pvcList.Items)).To(Equal(3))
				Expect(pvcList.Items).Should(ConsistOf(
					HavePVCName(pvcA.GetName()),
					HavePVCName(pvcC.GetName()),
					HavePVCName(pvcD.GetName()),
				))
			})

			It("Should list matching PVCs and filter out VolSync PVCs when VolSync is not disabled", func() {
				pvcList, err := util.ListPVCsByPVCSelector(testCtx, k8sClient, testLogger, pvcSelector,
					[]string{testNamespace.GetName()},
					false /* Volsync NOT Disabled */)
				Expect(err).NotTo(HaveOccurred())
				Expect(pvcList).NotTo(BeNil())
				Expect(len(pvcList.Items)).To(Equal(1))
				Expect(pvcList.Items).Should(ConsistOf(
					HavePVCName(pvcD.GetName()),
				))
			})
		})

		Context("With a labelSelector with multiple matchLabels", func() {
			pvcSelector := metav1.LabelSelector{
				MatchLabels: map[string]string{
					"mylabel": "abc",    // Matches pvcA, pvcC, pvcD
					"another": "whynot", // Matches pvcC, pvcD
				},
			}

			It("Should list matching PVCs when VolSync is disabled", func() {
				pvcList, err := util.ListPVCsByPVCSelector(testCtx, k8sClient, testLogger, pvcSelector,
					[]string{testNamespace.GetName()},
					true /* Volsync Disabled */)
				Expect(err).NotTo(HaveOccurred())
				Expect(pvcList).NotTo(BeNil())
				Expect(len(pvcList.Items)).To(Equal(2))
				Expect(pvcList.Items).Should(ConsistOf(
					HavePVCName(pvcC.GetName()),
					HavePVCName(pvcD.GetName()),
				))
			})

			It("Should list matching PVCs and filter out VolSync PVCs when VolSync is not disabled", func() {
				pvcList, err := util.ListPVCsByPVCSelector(testCtx, k8sClient, testLogger, pvcSelector,
					[]string{testNamespace.GetName()},
					false /* Volsync NOT Disabled */)
				Expect(err).NotTo(HaveOccurred())
				Expect(pvcList).NotTo(BeNil())
				Expect(len(pvcList.Items)).To(Equal(1))
				Expect(pvcList.Items).Should(ConsistOf(
					HavePVCName(pvcD.GetName()),
				))
			})
		})

		Context("With a labelSelector with matchLabels and matchExpresssions", func() {
			pvcSelector := metav1.LabelSelector{
				MatchLabels: map[string]string{
					"new-label": "test", // Matches pvcA, pvcB, pvcC
				},
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "test-label",
						Operator: metav1.LabelSelectorOpIn,
						Values:   []string{"bbb", "ddd", "ccc"}, // Should match pvcB, pvcC, pvcD
					},
				},
			}
			// Overall this selector should AND matchLabels and MatchExpresssions, so should match pvcB & pvcC

			It("Should list matching PVCs when VolSync is disabled", func() {
				pvcList, err := util.ListPVCsByPVCSelector(testCtx, k8sClient, testLogger, pvcSelector,
					[]string{testNamespace.GetName()},
					true /* Volsync Disabled */)
				Expect(err).NotTo(HaveOccurred())
				Expect(pvcList).NotTo(BeNil())
				Expect(len(pvcList.Items)).To(Equal(2))
				Expect(pvcList.Items).Should(ConsistOf(
					HavePVCName(pvcB.GetName()),
					HavePVCName(pvcC.GetName()),
				))
			})

			It("Should list matching PVCs and filter out VolSync PVCs when VolSync is not disabled", func() {
				pvcList, err := util.ListPVCsByPVCSelector(testCtx, k8sClient, testLogger, pvcSelector,
					[]string{testNamespace.GetName()},
					false /* Volsync NOT Disabled */)
				Expect(err).NotTo(HaveOccurred())
				Expect(pvcList).NotTo(BeNil())
				Expect(len(pvcList.Items)).To(Equal(1))
				Expect(pvcList.Items).Should(ConsistOf(
					HavePVCName(pvcB.GetName()),
				))
			})
		})
	})
})

func createTestPVC(ctx context.Context, namespace string, labels map[string]string) *corev1.PersistentVolumeClaim {
	pvcCapacity := resource.MustParse("1Gi")

	// Create dummy PVC with the desired labels
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-pvc-",
			Namespace:    namespace,
			Labels:       labels,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: pvcCapacity,
				},
			},
		},
	}

	Expect(k8sClient.Create(ctx, pvc)).To(Succeed())

	return pvc
}

// HavePVCName returns a matcher that expects the PVC to have the given name.
func HavePVCName(name string) gomegatypes.GomegaMatcher {
	return WithTransform(func(pvc corev1.PersistentVolumeClaim) string {
		return pvc.GetName()
	}, Equal(name))
}
