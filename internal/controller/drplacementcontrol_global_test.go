// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ocmworkv1 "open-cluster-management.io/api/work/v1"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	controllers "github.com/ramendr/ramen/internal/controller"
	rmnutil "github.com/ramendr/ramen/internal/controller/util"
)

const (
	// Both DRPCs use DRPCCommonName because GetFakeVRGFromMCVUsingMW is
	// hardcoded to look up ManifestWorks by that name. Different namespaces
	// keep them distinct.
	globalNSA      = "global-ns-a"
	globalNSB      = "global-ns-b"
	globalVGRValue = "global-shared-fs"
	globalPlRuleA  = "global-plrule-a"
	globalPlRuleB  = "global-plrule-b"
)

var _ = Describe("DRPlacementControl Global VGR Consensus", Ordered, func() {
	BeforeAll(func() {
		createNamespacesAsync(getNamespaceObj(globalNSA))
		createNamespace(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: globalNSB}})

		populateDRClusters()
		createManagedClusters(asyncClusters)
		createDRClustersAsync()
		createDRPolicyAsync()

		createPlacementRule(globalPlRuleA, globalNSA)
		createPlacementRule(globalPlRuleB, globalNSB)
	})

	Context("deploy two global DRPCs", func() {
		It("should deploy both to East1ManagedCluster", func() {
			createGlobalDRPC(globalNSA, globalPlRuleA, globalVGRValue)
			createGlobalDRPC(globalNSB, globalPlRuleB, globalVGRValue)

			updateGlobalMWStatus(East1ManagedCluster, globalNSA)
			updateGlobalMWStatus(East1ManagedCluster, globalNSB)

			waitForGlobalDRPCPhase(globalNSA, rmn.Deployed)
			waitForGlobalDRPCPhase(globalNSB, rmn.Deployed)
		})

		It("should have the global VGR label on both DRPCs", func() {
			drpcA := &rmn.DRPlacementControl{}
			Expect(apiReader.Get(context.TODO(),
				types.NamespacedName{Name: DRPCCommonName, Namespace: globalNSA}, drpcA)).To(Succeed())
			Expect(drpcA.GetLabels()[controllers.GlobalVGRLabel]).To(Equal(globalVGRValue))

			drpcB := &rmn.DRPlacementControl{}
			Expect(apiReader.Get(context.TODO(),
				types.NamespacedName{Name: DRPCCommonName, Namespace: globalNSB}, drpcB)).To(Succeed())
			Expect(drpcB.GetLabels()[controllers.GlobalVGRLabel]).To(Equal(globalVGRValue))
		})

		It("should have secondary VRG MW on peer cluster", func() {
			for _, ns := range []string{globalNSA, globalNSB} {
				key := types.NamespacedName{
					Name:      rmnutil.ManifestWorkName(DRPCCommonName, ns, "vrg"),
					Namespace: West1ManagedCluster,
				}

				Eventually(func() error {
					return k8sClient.Get(context.TODO(), key, &ocmworkv1.ManifestWork{})
				}, timeout, interval).Should(Succeed(),
					fmt.Sprintf("secondary VRG MW %s not found on %s", key.Name, West1ManagedCluster))
			}
		})
	})

	Context("failover consensus", func() {
		It("should block DRPC-A when only it triggers failover (action mismatch)", func() {
			setGlobalDRPCAction(globalNSA, West1ManagedCluster, rmn.ActionFailover)

			waitForGlobalDRPCProgression(globalNSA, rmn.ProgressionWaitOnGlobalAction)
			waitForGlobalDRPCCondition(globalNSA, metav1.ConditionFalse)
		})

		It("should still block when DRPC-B failovers to a different target (target mismatch)", func() {
			setGlobalDRPCAction(globalNSB, East1ManagedCluster, rmn.ActionFailover)

			waitForGlobalDRPCProgression(globalNSA, rmn.ProgressionWaitOnGlobalAction)
			waitForGlobalDRPCCondition(globalNSA, metav1.ConditionFalse)
		})

		It("should unblock both when DRPC-B aligns to the same target", func() {
			setGlobalDRPCAction(globalNSB, West1ManagedCluster, rmn.ActionFailover)

			updateGlobalMWStatus(West1ManagedCluster, globalNSA)
			updateGlobalMWStatus(West1ManagedCluster, globalNSB)

			waitForGlobalDRPCPhase(globalNSA, rmn.FailedOver)
			waitForGlobalDRPCPhase(globalNSB, rmn.FailedOver)
		})

		It("should have GlobalAction condition True on both", func() {
			waitForGlobalDRPCCondition(globalNSA, metav1.ConditionTrue)
			waitForGlobalDRPCCondition(globalNSB, metav1.ConditionTrue)
		})
	})

	Context("relocate consensus", func() {
		It("should block DRPC-A when only it triggers relocate", func() {
			setGlobalDRPCAction(globalNSA, West1ManagedCluster, rmn.ActionRelocate)

			waitForGlobalDRPCProgression(globalNSA, rmn.ProgressionWaitOnGlobalAction)
			waitForGlobalDRPCCondition(globalNSA, metav1.ConditionFalse)
		})

		It("should unblock both when DRPC-B also triggers relocate", func() {
			setGlobalDRPCAction(globalNSB, West1ManagedCluster, rmn.ActionRelocate)

			updateGlobalMWStatus(East1ManagedCluster, globalNSA)
			updateGlobalMWStatus(East1ManagedCluster, globalNSB)

			waitForGlobalDRPCPhase(globalNSA, rmn.Relocated)
			waitForGlobalDRPCPhase(globalNSB, rmn.Relocated)
		})

		It("should have GlobalAction condition True on both", func() {
			waitForGlobalDRPCCondition(globalNSA, metav1.ConditionTrue)
			waitForGlobalDRPCCondition(globalNSB, metav1.ConditionTrue)
		})
	})

	AfterAll(func() {
		Expect(forceCleanupClusterAfterAErrorTest()).To(Succeed())

		for _, ns := range []string{globalNSA, globalNSB} {
			Expect(k8sClient.Delete(context.TODO(), &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: ns},
			})).To(Succeed())
		}
	})
})

// --- Test helpers ---

func setGlobalDRPCAction(namespace, failover string, action rmn.DRAction) {
	key := types.NamespacedName{Name: DRPCCommonName, Namespace: namespace}

	retryErr := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		drpc := &rmn.DRPlacementControl{}
		if err := k8sClient.Get(context.TODO(), key, drpc); err != nil {
			return err
		}

		drpc.Spec.Action = action
		drpc.Spec.PreferredCluster = East1ManagedCluster
		drpc.Spec.FailoverCluster = failover

		return k8sClient.Update(context.TODO(), drpc)
	})
	Expect(retryErr).NotTo(HaveOccurred())

	Eventually(func() rmn.DRAction {
		drpc := &rmn.DRPlacementControl{}
		Expect(apiReader.Get(context.TODO(), key, drpc)).To(Succeed())

		return drpc.Spec.Action
	}, timeout, interval).Should(Equal(action))
}

func updateGlobalMWStatus(clusterNS, vrgNS string) {
	key := types.NamespacedName{
		Name:      rmnutil.ManifestWorkName(DRPCCommonName, vrgNS, "vrg"),
		Namespace: clusterNS,
	}
	mw := &ocmworkv1.ManifestWork{}

	Eventually(func() error {
		return k8sClient.Get(context.TODO(), key, mw)
	}, timeout, interval).Should(Succeed(),
		fmt.Sprintf("MW %s not found in %s", key.Name, clusterNS))

	now := time.Now().Local()
	status := ocmworkv1.ManifestWorkStatus{
		Conditions: []metav1.Condition{
			{
				Type:               ocmworkv1.WorkApplied,
				LastTransitionTime: metav1.Time{Time: now},
				Status:             metav1.ConditionTrue,
				Reason:             "test",
			},
			{
				Type:               ocmworkv1.WorkAvailable,
				LastTransitionTime: metav1.Time{Time: now},
				Status:             metav1.ConditionTrue,
				Reason:             "test",
			},
		},
	}

	retryErr := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := k8sClient.Get(context.TODO(), key, mw); err != nil {
			return err
		}

		mw.Status = status

		return k8sClient.Status().Update(context.TODO(), mw)
	})
	Expect(retryErr).NotTo(HaveOccurred())
}

func waitForGlobalDRPCCondition(namespace string, expected metav1.ConditionStatus) {
	Eventually(func() metav1.ConditionStatus {
		drpc := &rmn.DRPlacementControl{}
		Expect(apiReader.Get(context.TODO(),
			types.NamespacedName{Name: DRPCCommonName, Namespace: namespace}, drpc)).To(Succeed())

		_, cond := getDRPCCondition(&drpc.Status, rmn.ConditionGlobalAction)
		if cond == nil {
			return metav1.ConditionUnknown
		}

		return cond.Status
	}, timeout, interval).Should(Equal(expected),
		fmt.Sprintf("DRPC %s/%s GlobalAction condition not %s", namespace, DRPCCommonName, expected))
}

func waitForGlobalDRPCProgression(namespace string, expected rmn.ProgressionStatus) {
	Eventually(func() rmn.ProgressionStatus {
		drpc := &rmn.DRPlacementControl{}
		Expect(apiReader.Get(context.TODO(),
			types.NamespacedName{Name: DRPCCommonName, Namespace: namespace}, drpc)).To(Succeed())

		return drpc.Status.Progression
	}, timeout, interval).Should(Equal(expected),
		fmt.Sprintf("DRPC %s/%s progression not %s", namespace, DRPCCommonName, expected))
}

func waitForGlobalDRPCPhase(namespace string, expected rmn.DRState) {
	Eventually(func() rmn.DRState {
		drpc := &rmn.DRPlacementControl{}
		Expect(apiReader.Get(context.TODO(),
			types.NamespacedName{Name: DRPCCommonName, Namespace: namespace}, drpc)).To(Succeed())

		return drpc.Status.Phase
	}, timeout, interval).Should(Equal(expected),
		fmt.Sprintf("DRPC %s/%s phase not %s", namespace, DRPCCommonName, expected))
}

func createGlobalDRPC(namespace, placementName, globalLabel string) {
	drpc := &rmn.DRPlacementControl{
		ObjectMeta: metav1.ObjectMeta{
			Name:      DRPCCommonName,
			Namespace: namespace,
			Labels: map[string]string{
				controllers.GlobalVGRLabel: globalLabel,
			},
		},
		Spec: rmn.DRPlacementControlSpec{
			PlacementRef: corev1.ObjectReference{
				Name: placementName,
			},
			DRPolicyRef: corev1.ObjectReference{
				Name: AsyncDRPolicyName,
			},
			PVCSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"appclass":    "gold",
					"environment": "dev.AZ1",
				},
			},
			KubeObjectProtection: &rmn.KubeObjectProtectionSpec{},
			PreferredCluster:     East1ManagedCluster,
		},
	}

	Expect(k8sClient.Create(context.TODO(), drpc)).Should(Succeed())
}
