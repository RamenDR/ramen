// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	clrapiv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	ocmworkv1 "open-cluster-management.io/api/work/v1"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	controllers "github.com/ramendr/ramen/internal/controller"
)

// resetAppSet clears the ResourceVersion and UID that the API server stamped
// onto the package-level appSet var during Create.  Must be called before
// createAppSet() (via InitialDeploymentAsync) so that the Create call does not
// fail with "resourceVersion should not be set on objects to be created".
// The package-level appSet var is shared across all test contexts; once a
// previous test creates and deletes it the in-memory struct still carries the
// API-server-assigned fields.
func resetAppSet() {
	appSet.ResourceVersion = ""
	appSet.UID = ""
}

// setDRPCDryRunExpectationTo patches the shared DRPC spec with DryRun and action fields,
// using the same retry-on-conflict pattern as setDRPCSpecExpectationTo.
func setDRPCDryRunExpectationTo(
	preferredCluster, failoverCluster string,
	action rmn.DRAction,
	dryRun bool,
) {
	drpcLookupKey := types.NamespacedName{Name: DRPCCommonName, Namespace: DefaultDRPCNamespace}
	latestDRPC := &rmn.DRPlacementControl{}

	retryErr := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := k8sClient.Get(context.TODO(), drpcLookupKey, latestDRPC); err != nil {
			return err
		}

		latestDRPC.Spec.DryRun = dryRun
		latestDRPC.Spec.Action = action
		latestDRPC.Spec.PreferredCluster = preferredCluster
		latestDRPC.Spec.FailoverCluster = failoverCluster

		return k8sClient.Update(context.TODO(), latestDRPC)
	})

	Expect(retryErr).NotTo(HaveOccurred())

	Eventually(func() bool {
		latestDRPC = getLatestDRPC(DefaultDRPCNamespace)

		return latestDRPC.Spec.DryRun == dryRun && latestDRPC.Spec.Action == action
	}, timeout, interval).Should(BeTrue(), "failed to update DRPC DryRun/action on time")
}

// enterDryRunTestingFailover drives the DRPC from a freshly-deployed state into
// Progression=TestingFailover and asserts every observable that must hold while
// the dry-run is active.  Both the promote and revert tests call this as their
// shared "enter test-failover" step, so the assertions are written once and
// exercised by both.
//
// Pre-condition:  app is deployed to East1, LastAppDeploymentCluster=east1-cluster.
// Post-condition: DRPC is in Progression=TestingFailover; West1 ManifestWork is Applied.
//
// Mirrors the style of recoverToFailoverCluster / relocateToPreferredCluster in
// drplacementcontrol_controller_test.go.
//
//nolint:funlen
func enterDryRunTestingFailover() {
	By("patching DRPC with DryRun=true, Action=Failover, FailoverCluster=West1")
	setDRPCDryRunExpectationTo(
		East1ManagedCluster, // preferredCluster — app stays on East1
		West1ManagedCluster, // failoverCluster — must differ from LastAppDeploymentCluster
		rmn.ActionFailover,
		true,
	)

	By("waiting for the controller to write the test-failover-dryrun annotation")
	// processTestFailoverFlowIfEnabled() lines 173-194: once all guards pass the
	// controller writes test-failover-dryrun="true" to the DRPC and requeues.
	Eventually(func() string {
		return getLatestDRPC(DefaultDRPCNamespace).
			GetAnnotations()[controllers.DRPCTestFailoverDryRunAnnotation]
	}, timeout, interval).Should(
		Equal(controllers.DRPCTestFailoverDryRunAnnotationValueTrue),
		"controller must write test-failover-dryrun=true annotation")

	By("faking West1 VRG ManifestWork as Applied")
	// FakeMCVGetter reads VRG state from the ManifestWork; WorkApplied+WorkAvailable
	// makes it return a Primary VRG satisfying vrgExistsAndPrimary(West1).
	updateManifestWorkStatus(West1ManagedCluster, DefaultDRPCNamespace, "vrg", ocmworkv1.WorkApplied)

	By("waiting for Progression=TestingFailover")
	// RunFailover() lines 757-768: with DryRun=true and VRG Primary on the test
	// cluster, the controller sets TestingFailover instead of calling
	// ensureFailoverActionCompleted().
	Eventually(func() rmn.ProgressionStatus {
		return getLatestDRPC(DefaultDRPCNamespace).Status.Progression
	}, timeout, interval).Should(
		Equal(rmn.ProgressionTestingFailover),
		"controller must reach ProgressionTestingFailover once VRG is Primary on test cluster")

	By("verifying Progression has NOT advanced to Completed — dryRun holds in TestingFailover")
	// The controller requeues after 1 minute so Progression stays TestingFailover.
	// If the DryRun branch were deleted, Completed would appear here.
	Consistently(func() rmn.ProgressionStatus {
		return getLatestDRPC(DefaultDRPCNamespace).Status.Progression
	}, timeout/2, interval).ShouldNot(
		Equal(rmn.ProgressionCompleted),
		"Progression must not advance to Completed during an active dryRun")

	By("verifying LastAppDeploymentCluster is NOT mutated during dryRun")
	// updateUserPlacementRule() line 1915: the !DryRun guard prevents writing
	// LastAppDeploymentCluster when DryRun=true.
	Consistently(func() string {
		return getLatestDRPC(DefaultDRPCNamespace).GetAnnotations()[controllers.LastAppDeploymentCluster]
	}, timeout/2, interval).Should(Equal(East1ManagedCluster),
		"LastAppDeploymentCluster must be preserved while DryRun=true")

	By("verifying DRPCLastAction is NOT mutated during dryRun")
	// Same !DryRun guard: DRPCLastAction must stay as it was at initial deployment ("").
	Consistently(func() string {
		return getLatestDRPC(DefaultDRPCNamespace).GetAnnotations()[controllers.DRPCLastAction]
	}, timeout/2, interval).Should(Equal(""),
		"DRPCLastAction must remain empty while DryRun=true")

	By("verifying East1 VRG ManifestWork is still Primary — source app is not disrupted")
	// The controller must NOT demote East1 to Secondary as it would in a real failover.
	Eventually(func() bool {
		eastVRG, err := GetFakeVRGFromMCVUsingMW(East1ManagedCluster, DefaultDRPCNamespace)
		if err != nil {
			return false
		}

		return eastVRG.Spec.ReplicationState == rmn.Primary
	}, timeout, interval).Should(BeTrue(),
		"East1 VRG must remain Primary — source app must not be disrupted")

	By("verifying West1 VRG ManifestWork is Primary with test-failover-dryrun annotation")
	// setVRGAnnotations() lines 2315-2318: only the failover cluster's VRG gets the
	// test-failover-dryrun="true" annotation, enabling AutoResync in VRG controller.
	Eventually(func() bool {
		westVRG, err := GetFakeVRGFromMCVUsingMW(West1ManagedCluster, DefaultDRPCNamespace)
		if err != nil {
			return false
		}

		return westVRG.Spec.ReplicationState == rmn.Primary &&
			westVRG.GetAnnotations()[controllers.DRPCTestFailoverDryRunAnnotation] ==
				controllers.DRPCTestFailoverDryRunAnnotationValueTrue
	}, timeout, interval).Should(BeTrue(),
		"West1 VRG must be Primary with test-failover-dryrun=true annotation")
}

// verifyDryRunPromotion drives the DRPC from TestingFailover through a real failover
// after DryRun is cleared while keeping FailoverCluster=West1.  It is shared between
// the AppSet and DiscoveredApp promote tests; the only difference is the intermediate
// progression state the controller passes through before reaching Completed.
//
//   - AppSet:        intermediateProgression = ProgressionCleaningUp
//     (ensureFailoverActionCompleted sets CleaningUp while East1 VRG transitions to Secondary)
//   - DiscoveredApp: intermediateProgression = ProgressionWaitOnUserToCleanUp
//     (cleanupSecondary calls setDiscoveredAppGCProgression which sets WaitOnUserToCleanUp)
func verifyDryRunPromotion(intermediateProgression rmn.ProgressionStatus) {
	By("setting DryRun=false, keeping Action=Failover and FailoverCluster=West1")
	// detectPromotionOrRevert() sees FailoverCluster==testFailoverCluster && !DryRun
	// → routes to handlePromotion() which updates annotations then requeues.
	// Normal failover then completes because West1 ManifestWork is already Applied.
	setDRPCDryRunExpectationTo(
		East1ManagedCluster,
		West1ManagedCluster,
		rmn.ActionFailover,
		false,
	)

	By("waiting for intermediate Progression=" + string(intermediateProgression))
	Eventually(func() rmn.ProgressionStatus {
		return getLatestDRPC(DefaultDRPCNamespace).Status.Progression
	}, timeout, interval).Should(Equal(intermediateProgression),
		"Progression must transition through "+string(intermediateProgression)+" during promote")

	By("waiting for Phase=FailedOver and Progression=Completed")
	waitForDRPCPhaseAndProgression(DefaultDRPCNamespace, rmn.FailedOver)

	By("verifying test-failover-dryrun annotation was removed")
	// handlePromotion() calls cleanupTestFailoverAnnotation() before updating DRPC.
	Expect(getLatestDRPC(DefaultDRPCNamespace).
		GetAnnotations()[controllers.DRPCTestFailoverDryRunAnnotation]).
		To(BeEmpty(), "test-failover-dryrun annotation must be removed after promotion")

	By("verifying LastAppDeploymentCluster was updated to West1")
	Expect(getLatestDRPC(DefaultDRPCNamespace).
		GetAnnotations()[controllers.LastAppDeploymentCluster]).
		To(Equal(West1ManagedCluster), "LastAppDeploymentCluster must be West1 after promotion")

	By("verifying DRPCLastAction was updated to Failover")
	Expect(getLatestDRPC(DefaultDRPCNamespace).
		GetAnnotations()[controllers.DRPCLastAction]).
		To(Equal(string(rmn.ActionFailover)), "DRPCLastAction must be updated to Failover after promotion")
}

// DryRun functional tests — DRPC level.
//
// These tests use the full reconciler stack (InitialDeploymentAsync + FakeMCVGetter +
// ManifestWork) so every assertion reflects real controller behavior, not just field
// persistence.
//
// Pattern (identical to other isolated Context blocks in this package):
//   - Each Context owns its full stack: setup, action, assertions, and cleanup.
//   - Variables follow the project standard: concrete *clrapiv1beta1.Placement + *rmn.DRPlacementControl.
//   - DRPC spec mutations use setDRPCDryRunExpectationTo (mirrors setDRPCSpecExpectationTo).
//   - enterDryRunTestingFailover() is the shared helper that drives the DRPC into
//     Progression=TestingFailover and asserts all active-dryRun invariants.
//   - All tests use UsePlacementWithAppSet (Placement + ApplicationSet), which is
//     the modern deployment model replacing the legacy PlacementRule/Subscription path.
//
// Tests covered:
//  1. Validation rejection — failoverCluster == LastAppDeploymentCluster
//  2. Validation rejection — Action=Relocate with DryRun=true
//  3. Promote path — DryRun→false keeps FailoverCluster → real failover completes
//  4. Revert path  — DryRun→false clears Action → back to Deployed
//  5. Lag-blocked  — lastGroupSyncTime > 3× schedulingInterval blocks dryRun
var _ = Describe("DRPlacementControl DryRun", func() {
	// -------------------------------------------------------------------------
	// Test 1: Reject when failoverCluster equals LastAppDeploymentCluster
	// -------------------------------------------------------------------------
	Context("DryRun Reconciler - Async DR: reject same-cluster test-failover (AppSet)", func() {
		// After initial deployment to East1 the controller writes
		// LastAppDeploymentCluster=East1.  Setting DryRun=true with
		// FailoverCluster=East1 must be rejected: you cannot test-failover to
		// the cluster the app is already running on.
		var (
			placement *clrapiv1beta1.Placement
			drpc      *rmn.DRPlacementControl
		)

		Specify("DRClusters", func() {
			populateDRClusters()
		})

		When("An Application is deployed for the first time", func() {
			It("Should deploy to East1ManagedCluster", func() {
				By("Initial Deployment")

				resetAppSet()

				UseApplicationSet = true
				getBaseVRG(DefaultDRPCNamespace).ObjectMeta.Namespace = ApplicationNamespace

				var placementObj interface{ GetName() string }

				placementObj, drpc = InitialDeploymentAsync(
					DefaultDRPCNamespace, UserPlacementName, East1ManagedCluster, UsePlacementWithAppSet)

				var ok bool

				placement, ok = placementObj.(*clrapiv1beta1.Placement)
				Expect(ok).To(BeTrue())
				Expect(placement).NotTo(BeNil())
				verifyInitialDRPCDeployment(placement, East1ManagedCluster)
			})
		})

		//nolint:dupl
		When("DryRun=true is set with failoverCluster equal to the current app cluster", func() {
			It("should set ConditionAvailable=False with a same-cluster rejection message", func() {
				By("patching DRPC with DryRun=true, Action=Failover, FailoverCluster=East1 (same as current)")
				setDRPCDryRunExpectationTo(
					West1ManagedCluster, // preferredCluster
					East1ManagedCluster, // failoverCluster — same as LastAppDeploymentCluster
					rmn.ActionFailover,
					true,
				)

				By("waiting for ConditionAvailable=False with the exact same-cluster rejection message")
				// recordFailure() writes ConditionAvailable=False with the full error from
				// processTestFailoverFlowIfEnabled() lines 162-164.
				Eventually(func() bool {
					d := getLatestDRPC(DefaultDRPCNamespace)
					_, cond := getDRPCCondition(&d.Status, rmn.ConditionAvailable)

					return cond != nil &&
						cond.Status == metav1.ConditionFalse &&
						strings.Contains(cond.Message,
							"dryRun failover target cannot be the same as current deployment cluster: "+
								East1ManagedCluster)
				}, timeout, interval).Should(BeTrue(),
					"expected ConditionAvailable=False with same-cluster rejection message")

				By("verifying Progression did not change from Completed — rejection must be a no-op")
				Consistently(func() rmn.ProgressionStatus {
					return getLatestDRPC(DefaultDRPCNamespace).Status.Progression
				}, timeout/2, interval).Should(Equal(rmn.ProgressionCompleted),
					"Progression must remain Completed when dryRun is rejected — no failover state machine entered")

				By("verifying LastAppDeploymentCluster was NOT mutated during the rejected dryRun")
				Consistently(func() string {
					return getLatestDRPC(DefaultDRPCNamespace).
						GetAnnotations()[controllers.LastAppDeploymentCluster]
				}, timeout/2, interval).Should(Equal(East1ManagedCluster),
					"LastAppDeploymentCluster must remain east1-cluster — never mutated on a rejected dryRun")

				By("verifying test-failover-dryrun annotation was NOT written on a rejected dryRun")
				Consistently(func() string {
					return getLatestDRPC(DefaultDRPCNamespace).
						GetAnnotations()[controllers.DRPCTestFailoverDryRunAnnotation]
				}, timeout/2, interval).Should(BeEmpty(),
					"test-failover-dryrun annotation must not be written when dryRun validation fails")
			})
		})

		Specify("Cleanup after same-cluster dryRun rejection test", func() {
			deleteUserPlacement()
			deleteDRPC()
			waitForCompletion("deleted")
			deleteAppSet()
			resetAppSet()

			UseApplicationSet = false

			deleteDRPolicyAsync()
			ensureDRPolicyIsDeleted(drpc.Spec.DRPolicyRef.Name)
			deleteDRClustersAsync()
		})
	})

	// -------------------------------------------------------------------------
	// Test 2: Reject when Action=Relocate with DryRun=true
	// -------------------------------------------------------------------------
	Context("DryRun Reconciler - Async DR: reject DryRun=true with Action=Relocate (AppSet)", func() {
		// DryRun is only meaningful for Failover.  Any other action combined
		// with DryRun=true must be rejected with a clear failure condition.
		var (
			placement *clrapiv1beta1.Placement
			drpc      *rmn.DRPlacementControl
		)

		Specify("DRClusters", func() {
			populateDRClusters()
		})

		When("An Application is deployed for the first time", func() {
			It("Should deploy to East1ManagedCluster", func() {
				By("Initial Deployment")

				resetAppSet()

				UseApplicationSet = true
				getBaseVRG(DefaultDRPCNamespace).ObjectMeta.Namespace = ApplicationNamespace

				var placementObj interface{ GetName() string }

				placementObj, drpc = InitialDeploymentAsync(
					DefaultDRPCNamespace, UserPlacementName, East1ManagedCluster, UsePlacementWithAppSet)

				var ok bool

				placement, ok = placementObj.(*clrapiv1beta1.Placement)
				Expect(ok).To(BeTrue())
				Expect(placement).NotTo(BeNil())
				verifyInitialDRPCDeployment(placement, East1ManagedCluster)
			})
		})

		//nolint:dupl
		When("DryRun=true is set with Action=Relocate", func() {
			It("should set ConditionAvailable=False with 'action is not failover' message", func() {
				By("patching DRPC with DryRun=true and Action=Relocate")
				setDRPCDryRunExpectationTo(
					West1ManagedCluster, // preferredCluster
					"",                  // failoverCluster — irrelevant, action check fires first
					rmn.ActionRelocate,
					true,
				)

				By("waiting for ConditionAvailable=False with the exact action rejection message")
				// The action check at processTestFailoverFlowIfEnabled() lines 152-157 fires
				// before any other logic.
				Eventually(func() bool {
					d := getLatestDRPC(DefaultDRPCNamespace)
					_, cond := getDRPCCondition(&d.Status, rmn.ConditionAvailable)

					return cond != nil &&
						cond.Status == metav1.ConditionFalse &&
						strings.Contains(cond.Message, "dryRun is enabled but action is not failover")
				}, timeout, interval).Should(BeTrue(),
					"expected ConditionAvailable=False with 'dryRun is enabled but action is not failover'")

				By("verifying Progression did not change from Completed — rejection must be a no-op")
				Consistently(func() rmn.ProgressionStatus {
					return getLatestDRPC(DefaultDRPCNamespace).Status.Progression
				}, timeout/2, interval).Should(Equal(rmn.ProgressionCompleted),
					"Progression must remain Completed when dryRun is rejected")

				By("verifying test-failover-dryrun annotation was NOT written")
				Consistently(func() string {
					return getLatestDRPC(DefaultDRPCNamespace).
						GetAnnotations()[controllers.DRPCTestFailoverDryRunAnnotation]
				}, timeout/2, interval).Should(BeEmpty(),
					"test-failover-dryrun annotation must not be written when action is not Failover")
			})
		})

		Specify("Cleanup after Relocate+DryRun rejection test", func() {
			deleteUserPlacement()
			deleteDRPC()
			waitForCompletion("deleted")
			deleteAppSet()
			resetAppSet()

			UseApplicationSet = false

			deleteDRPolicyAsync()
			ensureDRPolicyIsDeleted(drpc.Spec.DRPolicyRef.Name)
			deleteDRClustersAsync()
		})
	})

	// -------------------------------------------------------------------------
	// Test 3: Promote path — enter TestingFailover, then DryRun→false keeping
	//         FailoverCluster=West1 converts the test into a real failover.
	// -------------------------------------------------------------------------
	Context("DryRun Reconciler - Async DR: promote test-failover to real failover (AppSet)", func() {
		// enterDryRunTestingFailover() drives the DRPC to TestingFailover and
		// asserts all active-dryRun invariants (annotation written, Progression
		// held, annotations preserved, both VRG ManifestWorks correct).
		//
		// Promotion: DryRun→false while FailoverCluster=West1 stays set.
		// handlePromotion() removes the annotation, updates LastAppDeploymentCluster
		// and DRPCLastAction, then requeues so the normal failover flow completes.
		var (
			placement *clrapiv1beta1.Placement
			drpc      *rmn.DRPlacementControl
		)

		Specify("DRClusters", func() {
			populateDRClusters()
		})

		When("An Application is deployed for the first time", func() {
			It("Should deploy to East1ManagedCluster", func() {
				By("Initial Deployment")

				resetAppSet()

				UseApplicationSet = true
				getBaseVRG(DefaultDRPCNamespace).ObjectMeta.Namespace = ApplicationNamespace

				var placementObj interface{ GetName() string }

				placementObj, drpc = InitialDeploymentAsync(
					DefaultDRPCNamespace, UserPlacementName, East1ManagedCluster, UsePlacementWithAppSet)

				var ok bool

				placement, ok = placementObj.(*clrapiv1beta1.Placement)
				Expect(ok).To(BeTrue())
				Expect(placement).NotTo(BeNil())
				verifyInitialDRPCDeployment(placement, East1ManagedCluster)
			})
		})

		When("DryRun=true is set with a valid Failover config", func() {
			It("should reach TestingFailover with all active-dryRun invariants satisfied", func() {
				enterDryRunTestingFailover()
			})
		})

		When("DryRun is cleared while keeping FailoverCluster=West1 (promotion)", func() {
			It("should complete as a real failover with Phase=FailedOver and updated annotations", func() {
				// AppSet apps go through CleaningUp while East1 VRG transitions to Secondary.
				verifyDryRunPromotion(rmn.ProgressionCleaningUp)
			})
		})

		Specify("Cleanup after promote test", func() {
			deleteUserPlacement()
			deleteDRPC()
			waitForCompletion("deleted")
			deleteAppSet()
			resetAppSet()

			UseApplicationSet = false

			deleteDRPolicyAsync()
			ensureDRPolicyIsDeleted(drpc.Spec.DRPolicyRef.Name)
			deleteDRClustersAsync()
		})
	})

	// -------------------------------------------------------------------------
	// Test 4: Revert path — enter TestingFailover, then DryRun→false clearing
	//         Action and FailoverCluster reverts the app back to Deployed on East1.
	// -------------------------------------------------------------------------
	Context("DryRun Reconciler - Async DR: revert test-failover back to original state (AppSet)", func() {
		// enterDryRunTestingFailover() drives the DRPC to TestingFailover and
		// asserts all active-dryRun invariants.
		//
		// Revert: DryRun→false with Action="" and FailoverCluster="" cleared.
		// validateTestFailoverRevertScenario checks savedLastAction=="" → requires Action="".
		// handleRevert() demotes West1, restores East1 as Primary, removes the annotation,
		// and sets Phase=Deployed, Progression=Completed.
		//
		// The revert path requires the controller to first persist ProgressionCleaningUp before
		// it can demote West1 across reconcile cycles.  We give it a dedicated It block so the
		// reconciler settles at TestingFailover before the revert It fires — matching the project
		// pattern used by recoverToFailoverCluster / relocateToPreferredCluster.
		var (
			placement *clrapiv1beta1.Placement
			drpc      *rmn.DRPlacementControl
		)

		Specify("DRClusters", func() {
			populateDRClusters()
		})

		When("An Application is deployed for the first time", func() {
			It("Should deploy to East1ManagedCluster", func() {
				By("Initial Deployment")

				resetAppSet()

				UseApplicationSet = true
				getBaseVRG(DefaultDRPCNamespace).ObjectMeta.Namespace = ApplicationNamespace

				var placementObj interface{ GetName() string }

				placementObj, drpc = InitialDeploymentAsync(
					DefaultDRPCNamespace, UserPlacementName, East1ManagedCluster, UsePlacementWithAppSet)

				var ok bool

				placement, ok = placementObj.(*clrapiv1beta1.Placement)
				Expect(ok).To(BeTrue())
				Expect(placement).NotTo(BeNil())
				verifyInitialDRPCDeployment(placement, East1ManagedCluster)
			})
		})

		When("DryRun=true is set with a valid Failover config", func() {
			It("should reach TestingFailover with all active-dryRun invariants satisfied", func() {
				enterDryRunTestingFailover()
			})
		})

		When("DryRun is cleared with Action=empty (revert to original state)", func() {
			It("should initiate the revert by patching the DRPC and priming East1 ManifestWork", func() {
				By("faking East1 VRG ManifestWork as Applied before patching DRPC")
				// updateManifestWork inside ensureActionCompleted does a plain Update (no retry).
				// Setting Applied status first ensures the East1 MW resourceVersion is current
				// before the controller fetches it, preventing a conflict error on the first
				// revert cycle that would otherwise block West1 from being demoted to Secondary.
				updateManifestWorkStatus(East1ManagedCluster, DefaultDRPCNamespace, "vrg", ocmworkv1.WorkApplied)

				By("setting DryRun=false, Action=empty, FailoverCluster=West1 (retained)")
				// detectPromotionOrRevert() line 298: isPromotion requires Action==Failover,
				// so Action="" with FailoverCluster=West1 still routes to handleRevert().
				// validateTestFailoverRevertScenario: savedLastAction=="" requires Action="".
				setDRPCDryRunExpectationTo(
					East1ManagedCluster,
					West1ManagedCluster, // FailoverCluster retained — revert doesn't require clearing it
					"",                  // Action cleared — savedLastAction="" requires Action=""
					false,
				)
			})

			It("should complete with Phase=Deployed and annotations restored", func() {
				By("waiting for Phase=Deployed and Progression=CleaningUp — revert must set phase immediately")
				// handleRevert() sets Phase=Deployed (via setDRState) immediately after
				// validateTestFailoverRevertScenario passes, then sets ProgressionCleaningUp
				// before calling ensureActionCompleted(East1) to demote West1.
				Eventually(func() bool {
					d := getLatestDRPC(DefaultDRPCNamespace)

					return d.Status.Phase == rmn.Deployed && d.Status.Progression == rmn.ProgressionCleaningUp
				}, timeout, interval).Should(BeTrue(),
					"Phase must be Deployed and Progression must be CleaningUp during revert")

				By("waiting for Phase=Deployed and Progression=Completed")
				// Once West1 VRG is Secondary, handleRevert() restores Phase=Deployed and Progression=Completed.
				// Use timeout*2 — same budget as waitForCompletion.
				Eventually(func() bool {
					d := getLatestDRPC(DefaultDRPCNamespace)

					return d.Status.Phase == rmn.Deployed && d.Status.Progression == rmn.ProgressionCompleted
				}, timeout*2, interval).Should(BeTrue(),
					"Timed out waiting for Phase=Deployed and Progression=Completed after revert")

				By("verifying test-failover-dryrun annotation was removed")
				// handleRevert() → cleanupTestFailoverAnnotation() removes the annotation from DRPC.
				Expect(getLatestDRPC(DefaultDRPCNamespace).
					GetAnnotations()[controllers.DRPCTestFailoverDryRunAnnotation]).
					To(BeEmpty(),
						"test-failover-dryrun annotation must be removed after revert")

				By("verifying LastAppDeploymentCluster is still East1 after revert")
				// During dryRun LastAppDeploymentCluster was never mutated (!DryRun guard).
				// After revert it must still reflect the original app cluster.
				Expect(getLatestDRPC(DefaultDRPCNamespace).
					GetAnnotations()[controllers.LastAppDeploymentCluster]).
					To(Equal(East1ManagedCluster),
						"LastAppDeploymentCluster must remain East1 after revert")
			})
		})

		Specify("Cleanup after revert test", func() {
			deleteUserPlacement()
			deleteDRPC()
			waitForCompletion("deleted")
			deleteAppSet()
			resetAppSet()

			UseApplicationSet = false

			deleteDRPolicyAsync()
			ensureDRPolicyIsDeleted(drpc.Spec.DRPolicyRef.Name)
			deleteDRClustersAsync()
		})
	})

	// -------------------------------------------------------------------------
	// Test 5: DryRun blocked — lastGroupSyncTime lagging behind 3× scheduling interval
	// -------------------------------------------------------------------------
	Context("DryRun Reconciler - Async DR: block dryRun when replication is lagging (AppSet)", func() {
		// isGroupSyncLagging() in drplacementcontrol.go returns true when
		// DRPC.Status.LastGroupSyncTime is older than 3 × DRPolicy.Spec.SchedulingInterval.
		// The test DRPolicy uses SchedulingInterval="1h", so the threshold is 3 hours.
		// We inject a stale sync time (4 hours ago) via fakeLastGroupSyncTime so the
		// reconciler sees a lagging clock without any wall-clock dependency.
		//
		// Expected behavior:
		//   - ConditionAvailable=False with the lag rejection message
		//   - Progression stays Completed — no failover state machine entered
		//   - LastAppDeploymentCluster is NOT mutated
		var (
			placement *clrapiv1beta1.Placement
			drpc      *rmn.DRPlacementControl
		)

		Specify("DRClusters", func() {
			populateDRClusters()
		})

		When("An Application is deployed for the first time", func() {
			It("Should deploy to East1ManagedCluster", func() {
				By("Initial Deployment")

				resetAppSet()

				UseApplicationSet = true
				getBaseVRG(DefaultDRPCNamespace).ObjectMeta.Namespace = ApplicationNamespace

				var placementObj interface{ GetName() string }

				placementObj, drpc = InitialDeploymentAsync(
					DefaultDRPCNamespace, UserPlacementName, East1ManagedCluster, UsePlacementWithAppSet)

				var ok bool

				placement, ok = placementObj.(*clrapiv1beta1.Placement)
				Expect(ok).To(BeTrue())
				Expect(placement).NotTo(BeNil())
				verifyInitialDRPCDeployment(placement, East1ManagedCluster)
			})
		})

		//nolint:dupl
		When("DryRun=true is set but replication is lagging beyond 3× the scheduling interval", func() {
			It("should set ConditionAvailable=False with the lag rejection message and not start dryRun", func() {
				By("injecting a stale LastGroupSyncTime (4 hours ago) into the fake VRG getter")
				// fakeLastGroupSyncTime is read by GetFakeVRGFromMCVUsingMW on every reconcile.
				// The test DRPolicy SchedulingInterval="1h" → threshold = 3h.
				// 4h > 3h, so isGroupSyncLagging() returns true and blocks dryRunReadyToFailover().
				stale := metav1.NewTime(time.Now().Add(-4 * time.Hour))
				fakeLastGroupSyncTime = &stale

				By("patching DRPC with DryRun=true, Action=Failover, FailoverCluster=West1")
				// processTestFailoverFlowIfEnabled() validates action and cluster first (passes),
				// writes the annotation, then on the next reconcile RunFailover() hits the
				// dryRunReadyToFailover() guard at drplacementcontrol.go:777 and blocks.
				setDRPCDryRunExpectationTo(
					East1ManagedCluster,
					West1ManagedCluster,
					rmn.ActionFailover,
					true,
				)

				By("waiting for ConditionAvailable=False with the lag rejection message")
				// isGroupSyncLagging() at drplacementcontrol.go:836 writes ConditionAvailable=False
				// with the exact message: "cannot start dry-run failover: lastGroupSyncTime is lagging
				// behind, check workload and cluster replication state"
				Eventually(func() bool {
					d := getLatestDRPC(DefaultDRPCNamespace)
					_, cond := getDRPCCondition(&d.Status, rmn.ConditionAvailable)

					return cond != nil &&
						cond.Status == metav1.ConditionFalse &&
						strings.Contains(cond.Message,
							"cannot start dry-run failover: lastGroupSyncTime is lagging behind")
				}, timeout, interval).Should(BeTrue(),
					"expected ConditionAvailable=False with the lag rejection message")

				By("verifying Progression did not advance to TestingFailover — dryRun is blocked")
				// dryRunReadyToFailover() returns false → RunFailover() returns !done immediately.
				// The controller never calls switchToFailoverCluster() so Progression never reaches
				// TestingFailover.
				Consistently(func() rmn.ProgressionStatus {
					return getLatestDRPC(DefaultDRPCNamespace).Status.Progression
				}, timeout/2, interval).ShouldNot(
					Equal(rmn.ProgressionTestingFailover),
					"Progression must not reach TestingFailover when replication is lagging")

				By("verifying LastAppDeploymentCluster was NOT mutated")
				// dryRunReadyToFailover() blocks before updateUserPlacementRule() is ever called.
				Consistently(func() string {
					return getLatestDRPC(DefaultDRPCNamespace).
						GetAnnotations()[controllers.LastAppDeploymentCluster]
				}, timeout/2, interval).Should(Equal(East1ManagedCluster),
					"LastAppDeploymentCluster must remain East1 while dryRun is blocked on lag")
			})
		})

		Specify("Cleanup after lag-blocked dryRun test", func() {
			// Restore fakeLastGroupSyncTime so subsequent tests get a fresh sync time.
			fakeLastGroupSyncTime = nil

			deleteUserPlacement()
			deleteDRPC()
			waitForCompletion("deleted")
			deleteAppSet()
			resetAppSet()

			UseApplicationSet = false

			deleteDRPolicyAsync()
			ensureDRPolicyIsDeleted(drpc.Spec.DRPolicyRef.Name)
			deleteDRClustersAsync()
		})
	})
})

// discAppProtectedNamespace is the application namespace that discovered-app dryRun
// tests declare in Spec.ProtectedNamespaces.  It must be different from the DRPC
// namespace (which is the admin namespace, DefaultDRPCNamespace = "drpc-namespace"),
// because the production controller rejects a DRPC whose ProtectedNamespaces list
// contains the admin namespace itself.
//
// In production (e2e): DRPC lives in "ramen-ops", ProtectedNamespaces = [appNS].
// In unit tests:       DRPC lives in DefaultDRPCNamespace, ProtectedNamespaces = [discAppProtectedNamespace].
const discAppProtectedNamespace = "disapp-app-ns"

// createDRPCDiscoveredApp creates a DRPC configured for a discovered application.
// The DRPC must live in the admin namespace (DefaultDRPCNamespace, which is set as
// RamenOpsNamespace by initDiscoveredAppDeployment).  ProtectedNamespaces points to
// discAppProtectedNamespace — a separate application namespace — matching the e2e
// pattern where the DRPC is in ramen-ops and ProtectedNamespaces = [appNamespace].
func createDRPCDiscoveredApp(namespace string) *rmn.DRPlacementControl {
	protectedNS := []string{discAppProtectedNamespace}
	drpc := &rmn.DRPlacementControl{
		ObjectMeta: metav1.ObjectMeta{
			Name:      DRPCCommonName,
			Namespace: namespace,
		},
		Spec: rmn.DRPlacementControlSpec{
			PlacementRef: corev1.ObjectReference{
				Name: UserPlacementName,
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
			ProtectedNamespaces:  &protectedNS,
		},
	}

	Expect(k8sClient.Create(context.TODO(), drpc)).Should(Succeed())

	return drpc
}

// verifyInitialDiscoveredAppDeployment is the discovered-app equivalent of
// verifyInitialDRPCDeployment.  The differences from the standard version:
//
//  1. The NS ManifestWork check is skipped.  For discovered apps the controller
//     creates the NS MW on the PEER cluster (West1) for discAppProtectedNamespace,
//     not on East1.  verifyNSManifestWork(…, East1) would therefore always fail.
//
//  2. The ManifestWork count on East1 is higher (extra VSRG secondary MW created
//     by EnsureSecondaryReplicationSetup), so we use BeNumerically(">=", 2) instead
//     of the exact BeElementOf(3,4) count used by the non-discovered-app path.
func verifyInitialDiscoveredAppDeployment(userPlacement *clrapiv1beta1.Placement, preferredCluster string) {
	verifyVRGManifestWorkCreatedAsPrimary(userPlacement.GetNamespace(), preferredCluster)
	updateManifestWorkStatus(preferredCluster, userPlacement.GetNamespace(), "vrg", ocmworkv1.WorkApplied)
	verifyUserPlacementRuleDecision(userPlacement.GetName(), userPlacement.GetNamespace(), preferredCluster)
	verifyDRPCStatusPreferredClusterExpectation(userPlacement.GetNamespace(), rmn.Deployed)
	waitForCompletion(string(rmn.Deployed))
	// Also wait for Progression=Completed so that subsequent tests that assert
	// Progression stays Completed start from a clean, fully-settled state.
	waitForDRPCPhaseAndProgression(userPlacement.GetNamespace(), rmn.Deployed)

	latestDRPC := getLatestDRPC(userPlacement.GetNamespace())

	Expect(latestDRPC.Status.Phase).To(Equal(rmn.Deployed))

	_, condition := getDRPCCondition(&latestDRPC.Status, rmn.ConditionAvailable)
	Expect(condition).NotTo(BeNil())
	Expect(condition.Reason).To(Equal(string(rmn.Deployed)))
	Expect(latestDRPC.GetAnnotations()[controllers.LastAppDeploymentCluster]).To(Equal(preferredCluster))
}

// initDiscoveredAppDeployment sets up the initial deployment for a discovered app
// dryRun test context.  It creates a Placement (managed by ramen, not OCM) plus a
// DRPC with ProtectedNamespaces set, then waits for the initial Deployed state.
// Returns the DRPC so callers can reference Spec.DRPolicyRef in cleanup.
//
// Namespace layout (mirrors e2e pattern):
//   - DRPC lives in `namespace` (= DefaultDRPCNamespace = "drpc-namespace"), which is
//     registered as RamenOpsNamespace so drpcInAdminNamespace() returns true.
//   - ProtectedNamespaces = [discAppProtectedNamespace] — a separate "app" namespace.
//     This must differ from the admin namespace; the controller rejects a DRPC whose
//     ProtectedNamespaces contains the admin namespace itself.
//
// Setting RamenOpsNamespace BEFORE createDRClustersAsync() means the DRCluster
// ManifestWork gets created with 11 items (10 base + 1 RamenOpsNamespace Namespace
// object).  verifyVRGManifestWorkCreatedAsPrimary accepts both 10 and 11.
func initDiscoveredAppDeployment() *rmn.DRPlacementControl {
	// Register DefaultDRPCNamespace as the admin namespace so DRPC validation passes.
	ramenConfig.RamenOpsNamespace = DefaultDRPCNamespace

	configMapUpdate()

	createNamespacesAsync(getNamespaceObj(DefaultDRPCNamespace))
	createManagedClusters(asyncClusters)
	createDRClustersAsync()
	createDRPolicyAsync()
	createPlacementDecision()
	createPlacement(UserPlacementName, DefaultDRPCNamespace)

	drpc := createDRPCDiscoveredApp(DefaultDRPCNamespace)

	verifyInitialDiscoveredAppDeployment(
		getLatestUserPlacement(UserPlacementName, DefaultDRPCNamespace), East1ManagedCluster)

	return drpc
}

// cleanupDiscoveredAppAdminNamespace restores ramenConfig.RamenOpsNamespace to ""
// after a discovered-app dryRun test context is torn down.  Must be called from
// every Specify("Cleanup …") block that follows initDiscoveredAppDeployment.
func cleanupDiscoveredAppAdminNamespace() {
	ramenConfig.RamenOpsNamespace = ""

	configMapUpdate()
}

// DryRun functional tests — DRPC level (Discovered App).
//
// Mirrors the AppSet suite above but uses a DRPC with Spec.ProtectedNamespaces set,
// which makes isDiscoveredApp() return true in the production controller.  The key
// behavioral difference is the revert cleanup path: instead of ProgressionCleaningUp
// (ACM handles cleanup) the controller sets ProgressionWaitOnUserToCleanUp, waiting
// for the user to delete the workload from the failover cluster (West1).
//
// The e2e equivalent is: ./run.sh -test.run TestDR/disapp-deploy-cephfs/Failover
// where failoverRelocateDiscoveredApps() waits for WaitOnUserToCleanUp then deletes
// the app from currentCluster (the failover cluster) before the controller completes.
var _ = Describe("DRPlacementControl DryRun - Discovered App", func() {
	// -------------------------------------------------------------------------
	// Test 1: Reject when failoverCluster equals LastAppDeploymentCluster
	// -------------------------------------------------------------------------
	Context("DryRun Reconciler - Async DR: reject same-cluster test-failover (DiscoveredApp)", func() {
		var drpc *rmn.DRPlacementControl

		Specify("DRClusters", func() {
			populateDRClusters()
		})

		When("An Application is deployed for the first time", func() {
			It("Should deploy to East1ManagedCluster", func() {
				By("Initial Deployment")

				drpc = initDiscoveredAppDeployment()
			})
		})

		//nolint:dupl
		When("DryRun=true is set with failoverCluster equal to the current app cluster", func() {
			It("should set ConditionAvailable=False with a same-cluster rejection message", func() {
				By("patching DRPC with DryRun=true, Action=Failover, FailoverCluster=East1 (same as current)")
				setDRPCDryRunExpectationTo(
					West1ManagedCluster,
					East1ManagedCluster,
					rmn.ActionFailover,
					true,
				)

				By("waiting for ConditionAvailable=False with the same-cluster rejection message")
				Eventually(func() bool {
					d := getLatestDRPC(DefaultDRPCNamespace)
					_, cond := getDRPCCondition(&d.Status, rmn.ConditionAvailable)

					return cond != nil &&
						cond.Status == metav1.ConditionFalse &&
						strings.Contains(cond.Message,
							"dryRun failover target cannot be the same as current deployment cluster: "+
								East1ManagedCluster)
				}, timeout, interval).Should(BeTrue(),
					"expected ConditionAvailable=False with same-cluster rejection message")

				By("verifying Progression did not change from Completed")
				Consistently(func() rmn.ProgressionStatus {
					return getLatestDRPC(DefaultDRPCNamespace).Status.Progression
				}, timeout/2, interval).Should(Equal(rmn.ProgressionCompleted),
					"Progression must remain Completed when dryRun is rejected")

				By("verifying LastAppDeploymentCluster was NOT mutated")
				Consistently(func() string {
					return getLatestDRPC(DefaultDRPCNamespace).
						GetAnnotations()[controllers.LastAppDeploymentCluster]
				}, timeout/2, interval).Should(Equal(East1ManagedCluster),
					"LastAppDeploymentCluster must not change on a rejected dryRun")

				By("verifying test-failover-dryrun annotation was NOT written")
				Consistently(func() string {
					return getLatestDRPC(DefaultDRPCNamespace).
						GetAnnotations()[controllers.DRPCTestFailoverDryRunAnnotation]
				}, timeout/2, interval).Should(BeEmpty(),
					"test-failover-dryrun annotation must not be written when dryRun validation fails")
			})
		})

		Specify("Cleanup after same-cluster dryRun rejection test (DiscoveredApp)", func() {
			cleanupDiscoveredAppAdminNamespace()
			deleteUserPlacement()
			deleteDRPC()
			waitForCompletion("deleted")
			deleteDRPolicyAsync()
			ensureDRPolicyIsDeleted(drpc.Spec.DRPolicyRef.Name)
			deleteDRClustersAsync()
		})
	})

	// -------------------------------------------------------------------------
	// Test 2: Reject when Action=Relocate with DryRun=true
	// -------------------------------------------------------------------------
	Context("DryRun Reconciler - Async DR: reject DryRun=true with Action=Relocate (DiscoveredApp)", func() {
		var drpc *rmn.DRPlacementControl

		Specify("DRClusters", func() {
			populateDRClusters()
		})

		When("An Application is deployed for the first time", func() {
			It("Should deploy to East1ManagedCluster", func() {
				By("Initial Deployment")

				drpc = initDiscoveredAppDeployment()
			})
		})

		//nolint:dupl
		When("DryRun=true is set with Action=Relocate", func() {
			It("should set ConditionAvailable=False with 'action is not failover' message", func() {
				By("patching DRPC with DryRun=true and Action=Relocate")
				setDRPCDryRunExpectationTo(
					West1ManagedCluster,
					"",
					rmn.ActionRelocate,
					true,
				)

				By("waiting for ConditionAvailable=False with the action rejection message")
				Eventually(func() bool {
					d := getLatestDRPC(DefaultDRPCNamespace)
					_, cond := getDRPCCondition(&d.Status, rmn.ConditionAvailable)

					return cond != nil &&
						cond.Status == metav1.ConditionFalse &&
						strings.Contains(cond.Message, "dryRun is enabled but action is not failover")
				}, timeout, interval).Should(BeTrue(),
					"expected ConditionAvailable=False with 'dryRun is enabled but action is not failover'")

				By("verifying Progression did not change from Completed")
				Consistently(func() rmn.ProgressionStatus {
					return getLatestDRPC(DefaultDRPCNamespace).Status.Progression
				}, timeout/2, interval).Should(Equal(rmn.ProgressionCompleted),
					"Progression must remain Completed when dryRun is rejected")

				By("verifying test-failover-dryrun annotation was NOT written")
				Consistently(func() string {
					return getLatestDRPC(DefaultDRPCNamespace).
						GetAnnotations()[controllers.DRPCTestFailoverDryRunAnnotation]
				}, timeout/2, interval).Should(BeEmpty(),
					"test-failover-dryrun annotation must not be written when action is not Failover")
			})
		})

		Specify("Cleanup after Relocate+DryRun rejection test (DiscoveredApp)", func() {
			cleanupDiscoveredAppAdminNamespace()
			deleteUserPlacement()
			deleteDRPC()
			waitForCompletion("deleted")
			deleteDRPolicyAsync()
			ensureDRPolicyIsDeleted(drpc.Spec.DRPolicyRef.Name)
			deleteDRClustersAsync()
		})
	})

	// -------------------------------------------------------------------------
	// Test 3: Promote path
	// -------------------------------------------------------------------------
	Context("DryRun Reconciler - Async DR: promote test-failover to real failover (DiscoveredApp)", func() {
		var drpc *rmn.DRPlacementControl

		Specify("DRClusters", func() {
			populateDRClusters()
		})

		When("An Application is deployed for the first time", func() {
			It("Should deploy to East1ManagedCluster", func() {
				By("Initial Deployment")

				drpc = initDiscoveredAppDeployment()
			})
		})

		When("DryRun=true is set with a valid Failover config", func() {
			It("should reach TestingFailover with all active-dryRun invariants satisfied", func() {
				enterDryRunTestingFailover()
			})
		})

		When("DryRun is cleared while keeping FailoverCluster=West1 (promotion)", func() {
			It("should complete as a real failover with Phase=FailedOver and updated annotations", func() {
				// Discovered apps go through WaitOnUserToCleanUp while East1 VRG transitions to Secondary.
				verifyDryRunPromotion(rmn.ProgressionWaitOnUserToCleanUp)
			})
		})

		Specify("Cleanup after promote test (DiscoveredApp)", func() {
			cleanupDiscoveredAppAdminNamespace()
			deleteUserPlacement()
			deleteDRPC()
			waitForCompletion("deleted")
			deleteDRPolicyAsync()
			ensureDRPolicyIsDeleted(drpc.Spec.DRPolicyRef.Name)
			deleteDRClustersAsync()
		})
	})

	// -------------------------------------------------------------------------
	// Test 4: Revert path — WaitOnUserToCleanUp then simulate user deleting
	//         workload from West1 (the failover cluster).
	// -------------------------------------------------------------------------
	Context("DryRun Reconciler - Async DR: revert test-failover back to original state (DiscoveredApp)", func() {
		// Discovered apps do NOT use ACM to clean up the workload on the failover
		// cluster.  Instead the controller sets ProgressionWaitOnUserToCleanUp and
		// waits for the user to delete the workload from the failover cluster (West1).
		//
		// In e2e this is: failoverRelocateDiscoveredApps() calls
		//   deployers.DeleteDiscoveredAppsAndWait(ctx, currentCluster, appNamespace)
		// where currentCluster=West1 (the cluster the app was running on during dryRun).
		//
		// In envtest we simulate that deletion by updating West1's ManifestWork status
		// to WorkApplied with a Secondary VRG spec (what the controller writes when it
		// demotes West1), causing ensureVRGIsSecondaryOnCluster(West1) to return true
		// and completing the revert.
		var drpc *rmn.DRPlacementControl

		Specify("DRClusters", func() {
			populateDRClusters()
		})

		When("An Application is deployed for the first time", func() {
			It("Should deploy to East1ManagedCluster", func() {
				By("Initial Deployment")

				drpc = initDiscoveredAppDeployment()
			})
		})

		When("DryRun=true is set with a valid Failover config", func() {
			It("should reach TestingFailover with all active-dryRun invariants satisfied", func() {
				enterDryRunTestingFailover()
			})
		})

		When("DryRun is cleared with Action=empty (revert to original state)", func() {
			It("should initiate revert and reach WaitOnUserToCleanUp", func() {
				By("faking East1 VRG ManifestWork as Applied before patching DRPC")
				// Same pre-prime as AppSet revert: ensures East1 MW resourceVersion is
				// current before the controller's ensureActionCompleted() Update call.
				updateManifestWorkStatus(East1ManagedCluster, DefaultDRPCNamespace, "vrg", ocmworkv1.WorkApplied)

				By("setting DryRun=false, Action=empty, FailoverCluster=West1 (retained)")
				// detectPromotionOrRevert() line 298: isPromotion requires Action==Failover,
				// so Action="" with FailoverCluster=West1 still routes to handleRevert().
				setDRPCDryRunExpectationTo(
					East1ManagedCluster,
					West1ManagedCluster, // FailoverCluster retained — revert doesn't require clearing it
					"",                  // Action cleared — savedLastAction="" requires Action=""
					false,
				)

				By("waiting for Phase=Deployed and Progression=WaitOnUserToCleanUp")
				// handleRevert() sets Phase=Deployed (via setDRState) immediately after
				// validateTestFailoverRevertScenario passes, then setDiscoveredAppGCProgression
				// sets WaitOnUserToCleanUp — the signal that the user must delete the workload
				// from the failover cluster (West1) before cleanup can complete.
				Eventually(func() bool {
					d := getLatestDRPC(DefaultDRPCNamespace)

					return d.Status.Phase == rmn.Deployed &&
						d.Status.Progression == rmn.ProgressionWaitOnUserToCleanUp
				}, timeout, interval).Should(BeTrue(),
					"Phase must be Deployed and Progression must be WaitOnUserToCleanUp during revert")

				By("verifying Phase and Progression stay until user cleans up West1")
				// cleanupSecondary() line 2728-2732: on each reconcile, setDiscoveredAppGCProgression
				// re-sets WaitOnUserToCleanUp, then ensureVRGIsSecondaryOnCluster(West1) returns false
				// because the user hasn't deleted the workload yet. Must not advance.
				Consistently(func() bool {
					d := getLatestDRPC(DefaultDRPCNamespace)

					return d.Status.Phase == rmn.Deployed &&
						d.Status.Progression == rmn.ProgressionWaitOnUserToCleanUp
				}, timeout, interval).Should(BeTrue(),
					"Progression must remain WaitOnUserToCleanUp until user deletes workload from West1")
			})

			It("should complete with Phase=Deployed after simulating user cleanup on West1", func() {
				By("simulating user workload deletion on West1 (the failover cluster)")
				// In e2e the user deletes the app from West1; the VRG on West1 then
				// transitions to Secondary (the controller wrote Secondary to the MW when
				// it called ensureActionCompleted → EnsureCleanup → setVRGAction Secondary).
				// We simulate that state by marking West1's ManifestWork as Applied, which
				// makes GetFakeVRGFromMCVUsingMW return Status.State=SecondaryState, causing
				// ensureVRGIsSecondaryOnCluster(West1) to return true on the next reconcile.
				updateManifestWorkStatus(West1ManagedCluster, DefaultDRPCNamespace, "vrg", ocmworkv1.WorkApplied)

				By("waiting for Phase=Deployed and Progression=Completed")
				Eventually(func() bool {
					d := getLatestDRPC(DefaultDRPCNamespace)

					return d.Status.Phase == rmn.Deployed && d.Status.Progression == rmn.ProgressionCompleted
				}, timeout*2, interval).Should(BeTrue(),
					"Timed out waiting for Phase=Deployed and Progression=Completed after discovered app revert")

				By("verifying test-failover-dryrun annotation was removed")
				Expect(getLatestDRPC(DefaultDRPCNamespace).
					GetAnnotations()[controllers.DRPCTestFailoverDryRunAnnotation]).
					To(BeEmpty(), "test-failover-dryrun annotation must be removed after revert")

				By("verifying LastAppDeploymentCluster is still East1 after revert")
				Expect(getLatestDRPC(DefaultDRPCNamespace).
					GetAnnotations()[controllers.LastAppDeploymentCluster]).
					To(Equal(East1ManagedCluster), "LastAppDeploymentCluster must remain East1 after revert")
			})
		})

		Specify("Cleanup after revert test (DiscoveredApp)", func() {
			cleanupDiscoveredAppAdminNamespace()
			deleteUserPlacement()
			deleteDRPC()
			waitForCompletion("deleted")
			deleteDRPolicyAsync()
			ensureDRPolicyIsDeleted(drpc.Spec.DRPolicyRef.Name)
			deleteDRClustersAsync()
		})
	})

	// -------------------------------------------------------------------------
	// Test 5: DryRun blocked — lastGroupSyncTime lagging behind 3× scheduling interval
	// -------------------------------------------------------------------------
	Context("DryRun Reconciler - Async DR: block dryRun when replication is lagging (DiscoveredApp)", func() {
		var drpc *rmn.DRPlacementControl

		Specify("DRClusters", func() {
			populateDRClusters()
		})

		When("An Application is deployed for the first time", func() {
			It("Should deploy to East1ManagedCluster", func() {
				By("Initial Deployment")

				drpc = initDiscoveredAppDeployment()
			})
		})

		//nolint:dupl
		When("DryRun=true is set but replication is lagging beyond 3× the scheduling interval", func() {
			It("should set ConditionAvailable=False with the lag rejection message and not start dryRun", func() {
				By("injecting a stale LastGroupSyncTime (4 hours ago)")

				stale := metav1.NewTime(time.Now().Add(-4 * time.Hour))
				fakeLastGroupSyncTime = &stale

				By("patching DRPC with DryRun=true, Action=Failover, FailoverCluster=West1")
				setDRPCDryRunExpectationTo(
					East1ManagedCluster,
					West1ManagedCluster,
					rmn.ActionFailover,
					true,
				)

				By("waiting for ConditionAvailable=False with the lag rejection message")
				Eventually(func() bool {
					d := getLatestDRPC(DefaultDRPCNamespace)
					_, cond := getDRPCCondition(&d.Status, rmn.ConditionAvailable)

					return cond != nil &&
						cond.Status == metav1.ConditionFalse &&
						strings.Contains(cond.Message,
							"cannot start dry-run failover: lastGroupSyncTime is lagging behind")
				}, timeout, interval).Should(BeTrue(),
					"expected ConditionAvailable=False with the lag rejection message")

				By("verifying Progression did not advance to TestingFailover")
				Consistently(func() rmn.ProgressionStatus {
					return getLatestDRPC(DefaultDRPCNamespace).Status.Progression
				}, timeout/2, interval).ShouldNot(
					Equal(rmn.ProgressionTestingFailover),
					"Progression must not reach TestingFailover when replication is lagging")

				By("verifying LastAppDeploymentCluster was NOT mutated")
				Consistently(func() string {
					return getLatestDRPC(DefaultDRPCNamespace).
						GetAnnotations()[controllers.LastAppDeploymentCluster]
				}, timeout/2, interval).Should(Equal(East1ManagedCluster),
					"LastAppDeploymentCluster must remain East1 while dryRun is blocked on lag")
			})
		})

		Specify("Cleanup after lag-blocked dryRun test (DiscoveredApp)", func() {
			fakeLastGroupSyncTime = nil

			cleanupDiscoveredAppAdminNamespace()

			deleteUserPlacement()
			deleteDRPC()
			waitForCompletion("deleted")
			deleteDRPolicyAsync()
			ensureDRPolicyIsDeleted(drpc.Spec.DRPolicyRef.Name)
			deleteDRClustersAsync()
		})
	})
})
