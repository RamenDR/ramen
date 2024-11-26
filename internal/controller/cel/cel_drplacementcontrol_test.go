// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package cel_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	// gomegaTypes "github.com/onsi/gomega/types"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	// validationErrors "k8s.io/kube-openapi/pkg/validation/errors"
	// "sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("DRPC-CEL", func() {
	getObjectRef := func(objectName string) corev1.ObjectReference {
		return corev1.ObjectReference{Name: objectName}
	}

	getLabelSelector := func(appclass, environment string) metav1.LabelSelector {
		return metav1.LabelSelector{
			MatchLabels: map[string]string{
				"appclass":    appclass,
				"environment": environment,
			},
		}
	}

	createNamespace := func(ns *corev1.Namespace) {
		Expect(k8sClient.Create(context.TODO(), ns)).To(Succeed())
	}

	DRPCs := []ramen.DRPlacementControl{}

	populateDRPCs := func() {
		DRPCs = nil

		drpc1Namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "drpc1-ns"},
		}
		drpc2Namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "drpc2-ns"},
		}

		createNamespace(drpc1Namespace)
		createNamespace(drpc2Namespace)

		drpc1 := &ramen.DRPlacementControl{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "drpc-1",
				Namespace: drpc1Namespace.Name,
			},
			Spec: ramen.DRPlacementControlSpec{
				PlacementRef: getObjectRef("placement-sample1"),
				DRPolicyRef:  getObjectRef("drpolicy-sample1"),
				PVCSelector:  getLabelSelector("cel1", "dev.cel1"),
			},
		}

		drpc2 := &ramen.DRPlacementControl{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "drpc-2",
				Namespace: drpc2Namespace.Name,
			},
			Spec: ramen.DRPlacementControlSpec{},
		}

		DRPCs = append(DRPCs, *drpc1, *drpc2)
	}

	createDRPC := func(drpc *ramen.DRPlacementControl) {
		Expect(k8sClient.Create(context.TODO(), drpc)).To(Succeed())
	}

	getDRPC := func(drpc *ramen.DRPlacementControl) *ramen.DRPlacementControl {
		err := k8sClient.Get(context.TODO(),
			types.NamespacedName{Name: drpc.Name, Namespace: drpc.Namespace},
			drpc)

		Expect(err).NotTo(HaveOccurred())

		return drpc
	}

	deleteDRPC := func(drpc *ramen.DRPlacementControl) {
		Expect(k8sClient.Delete(context.TODO(), drpc)).To(Succeed())
		Eventually(func() bool {
			return errors.IsNotFound(k8sClient.Get(context.TODO(), types.NamespacedName{
				Name: drpc.Name, Namespace: drpc.Namespace,
			}, drpc))
		}, timeout, interval).Should(BeTrue())
	}

	Specify("DRPC initialize tests", func() {
		populateDRPCs()
	})

	When("a valid DRPC is given to create", func() {
		It("should return success", func() {
			drpc := DRPCs[0].DeepCopy()
			createDRPC(drpc)
			getDRPC(drpc)
			deleteDRPC(drpc)
		})
	})

	When("modifying PlacementRef in DRPC", func() {
		It("should fail to modify", func() {
			drpc := DRPCs[0].DeepCopy()
			createDRPC(drpc)
			drpc.Spec.PlacementRef = getObjectRef("placement-sample-new")
			Expect(k8sClient.Update(context.TODO(), drpc)).NotTo(Succeed())
			deleteDRPC(drpc)
		})
	})

	When("modifying DRPolicyRef in DRPC", func() {
		It("should fail to modify", func() {
			drpc := DRPCs[0].DeepCopy()
			createDRPC(drpc)
			drpc.Spec.DRPolicyRef = getObjectRef("drpolicy-sample-new")
			Expect(k8sClient.Update(context.TODO(), drpc)).NotTo(Succeed())
			deleteDRPC(drpc)
		})
	})

	When("modifying PVCSelector in DRPC", func() {
		It("should fail to modify", func() {
			drpc := DRPCs[0].DeepCopy()
			createDRPC(drpc)
			drpc.Spec.PVCSelector = getLabelSelector("new-cel", "new-environment")
			Expect(k8sClient.Update(context.TODO(), drpc)).NotTo(Succeed())
			deleteDRPC(drpc)
		})
	})
})
