/*
Copyright 2021 The RamenDR authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/ghodss/yaml"
	"github.com/go-logr/logr"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	plrv1 "github.com/open-cluster-management/multicloud-operators-placementrule/pkg/apis/apps/v1"
	errorswrapper "github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	rmnutil "github.com/ramendr/ramen/controllers/util"
)

type DRPCInstance struct {
	reconciler           *DRPlacementControlReconciler
	ctx                  context.Context
	log                  logr.Logger
	instance             *rmn.DRPlacementControl
	drPolicy             *rmn.DRPolicy
	needStatusUpdate     bool
	mcvRequestInProgress bool
	userPlacementRule    *plrv1.PlacementRule
	drpcPlacementRule    *plrv1.PlacementRule
	vrgs                 map[string]*rmn.VolumeReplicationGroup
	mwu                  rmnutil.MWUtil
}

func (d *DRPCInstance) startProcessing() bool {
	d.log.Info("Starting to process placement")

	requeue := true

	done, err := d.processPlacement()
	if err != nil {
		d.log.Info("Process placement", "error", err.Error())

		return requeue
	}

	if d.needStatusUpdate || d.statusUpdateTimeElapsed() {
		if err := d.updateDRPCStatus(); err != nil {
			d.log.Error(err, "failed to update status")

			return requeue
		}
	}

	requeue = !done
	d.log.Info("Completed processing placement", "requeue", requeue)

	return requeue
}

func (d *DRPCInstance) processPlacement() (bool, error) {
	d.log.Info("Process DRPC Placement", "DRAction", d.instance.Spec.Action)

	switch d.instance.Spec.Action {
	case rmn.ActionFailover:
		return d.runFailover()
	case rmn.ActionRelocate:
		return d.runRelocate()
	}

	// Not a failover or a relocation.  Must be an initial deployment.
	return d.runInitialDeployment()
}

func (d *DRPCInstance) runInitialDeployment() (bool, error) {
	d.log.Info("Running initial deployment")

	const done = true

	homeCluster, homeClusterNamespace := d.getHomeCluster()

	if homeCluster == "" {
		err := fmt.Errorf("PreferredCluster not set and unable to find home cluster in DRPCPlacementRule (%v)",
			d.drpcPlacementRule)
		// needStatusUpdate is not set. Still better to capture the event to report later
		rmnutil.ReportIfNotPresent(d.reconciler.eventRecorder, d.instance, corev1.EventTypeWarning,
			rmnutil.EventReasonDeployFail, err.Error())

		return !done, err
	}

	d.log.Info(fmt.Sprintf("Using homeCluster %s for initial deployment, uPlRule Decision %v",
		homeCluster, d.userPlacementRule.Status.Decisions))

	// Check if we already deployed in the homeCluster or elsewhere
	clusterName, deployed := d.isDeployed(homeCluster)
	if deployed && clusterName != homeCluster {
		return done, nil
	}

	if deployed && d.isUserPlRuleUpdated(homeCluster) {
		// If for whatever reason, the DRPC status is missing (i.e. DRPC could have been deleted mistakingly and
		// recreated again), we should update it with whatever status we are at.
		if d.getLastDRState() == rmn.DRState("") {
			d.instance.Status.PreferredDecision = d.userPlacementRule.Status.Decisions[0]
			d.setDRState(rmn.Deployed)
		}

		return done, nil
	}

	return d.startDeploying(homeCluster, homeClusterNamespace)
}

func (d *DRPCInstance) getHomeCluster() (string, string) {
	// Check if the user wants to use the preferredCluster
	homeCluster := ""
	homeClusterNamespace := ""

	if d.instance.Spec.PreferredCluster != "" {
		homeCluster = d.instance.Spec.PreferredCluster
		homeClusterNamespace = homeCluster
	}

	if homeCluster == "" && d.drpcPlacementRule != nil && len(d.drpcPlacementRule.Status.Decisions) != 0 {
		homeCluster = d.drpcPlacementRule.Status.Decisions[0].ClusterName
		homeClusterNamespace = d.drpcPlacementRule.Status.Decisions[0].ClusterNamespace
	}

	return homeCluster, homeClusterNamespace
}

// isDeployed check to see if the initial deployment is already complete to this
// homeCluster or elsewhere
func (d *DRPCInstance) isDeployed(homeCluster string) (string, bool) {
	if d.isVRGDeployedAndProtected(homeCluster) {
		d.log.Info(fmt.Sprintf("Already deployed to %s. Last state: %s",
			homeCluster, d.getLastDRState()))

		return homeCluster, true
	}

	clusterName, found := d.isAlreadyDeployedElsewhere(homeCluster)
	if found {
		errMsg := fmt.Sprintf("Failed to place deployment on cluster %s, as it is active on another cluster",
			homeCluster)
		d.log.Info(errMsg)

		return clusterName, true
	}

	return "", false
}

func (d *DRPCInstance) isUserPlRuleUpdated(homeCluster string) bool {
	return len(d.userPlacementRule.Status.Decisions) > 0 &&
		d.userPlacementRule.Status.Decisions[0].ClusterName == homeCluster
}

// isAlreadyFullyDeployedAndProtected will check whether a VRG exists in the targetCluster and
// whether it is in protected state, and primary.
func (d *DRPCInstance) isVRGDeployedAndProtected(targetCluster string) bool {
	d.log.Info(fmt.Sprintf("isAlreadyDeployedAndProtected? - %+v", d.vrgs))

	vrg := d.getCachedVRG(targetCluster)
	if vrg == nil {
		return false
	}

	if !d.isVRGProtected(vrg) {
		return false
	}

	return d.isVRGPrimary(vrg)
}

func (d *DRPCInstance) getCachedVRG(clusterName string) *rmn.VolumeReplicationGroup {
	vrg, found := d.vrgs[clusterName]
	if !found {
		d.log.Info("VRG not found on cluster", "clusterName", clusterName)

		return nil
	}

	return vrg
}

func (d *DRPCInstance) isVRGProtected(vrg *rmn.VolumeReplicationGroup) bool {
	// TODO:
	// 		UNCOMMENT THIS CODE ONCE PR #255 IS MERGED
	//
	// dataProtectedCondition := findCondition(vrg.Status.Conditions, VRGConditionTypeClusterDataProtected)
	// if dataProtectedCondition == nil {
	// 	d.log.Info("VRG is not protected yet", "cluster", targetCluster)
	//
	// 	return false
	// }
	return true
}

func (d *DRPCInstance) isAlreadyDeployedElsewhere(clusterToSkip string) (string, bool) {
	for clusterName := range d.vrgs {
		if clusterName == clusterToSkip {
			continue
		}

		return clusterName, true
	}

	return "", false
}

func (d *DRPCInstance) startDeploying(homeCluster, homeClusterNamespace string) (bool, error) {
	const done = true

	// Make sure we record the state that we are deploying
	d.setDRState(rmn.Deploying)
	setMetricsTimerFromDRState(rmn.Deploying, timerStart)

	// Create VRG first, to leverage user PlacementRule decision to skip placement and move to cleanup
	err := d.createVRGManifestWork(homeCluster)
	if err != nil {
		return false, err
	}

	// We have a home cluster
	err = d.updateUserPlacementRule(homeCluster, homeClusterNamespace)
	if err != nil {
		rmnutil.ReportIfNotPresent(d.reconciler.eventRecorder, d.instance, corev1.EventTypeWarning,
			rmnutil.EventReasonDeployFail, err.Error())

		return !done, err
	}

	// All good, update the preferred decision and state
	if len(d.userPlacementRule.Status.Decisions) > 0 {
		d.instance.Status.PreferredDecision = d.userPlacementRule.Status.Decisions[0]
	}

	d.advanceToNextDRState()

	d.log.Info(fmt.Sprintf("DRPC (%+v)", d.instance))
	setMetricsTimerFromDRState(rmn.Deployed, timerStop)

	return done, nil
}

//
// runFailover:
// 1. If failoverCluster empty, then fail it and we are done
// 2. If already failed over, then ensure clean up and we are done
// 3. Set VRG for the preferredCluster to secondary
// 4. Restore PV to failoverCluster
// 5. Update UserPlacementRule decision to failoverCluster
// 6. Create VRG for the failoverCluster as Primary
// 7. Update DRPC status
// 8. Delete VRG MW from preferredCluster once the VRG state has changed to Secondary
//
func (d *DRPCInstance) runFailover() (bool, error) {
	d.log.Info("Entering runFailover", "state", d.getLastDRState())

	const done = true

	if d.isRelocationInProgress() {
		return done, fmt.Errorf("invalid state %s for the selected action %v",
			d.getLastDRState(), d.instance.Spec.Action)
	}

	// We are done if empty
	if d.instance.Spec.FailoverCluster == "" {
		return done, fmt.Errorf("failover cluster not set. FailoverCluster is a mandatory field")
	}

	// We are done if we have already failed over
	if len(d.userPlacementRule.Status.Decisions) > 0 {
		if d.instance.Spec.FailoverCluster == d.userPlacementRule.Status.Decisions[0].ClusterName {
			d.log.Info(fmt.Sprintf("Already failed over to %s. Last state: %s",
				d.userPlacementRule.Status.Decisions[0].ClusterName, d.getLastDRState()))

			err := d.ensureCleanup(d.instance.Spec.FailoverCluster)
			if err != nil {
				return !done, err
			}

			return done, nil
		}
	}

	return d.switchToFailoverCluster()
}

func (d *DRPCInstance) switchToFailoverCluster() (bool, error) {
	const done = true
	// Make sure we record the state that we are failing over
	d.setDRState(rmn.FailingOver)
	setMetricsTimerFromDRState(rmn.FailingOver, timerStart)

	// Save the current home cluster
	curHomeCluster := d.getCurrentHomeClusterName()

	if curHomeCluster == "" {
		d.log.Info("Invalid Failover request. Current home cluster does not exists")
		err := fmt.Errorf("failover requested on invalid state %v", d.instance.Status)
		rmnutil.ReportIfNotPresent(d.reconciler.eventRecorder, d.instance, corev1.EventTypeWarning,
			rmnutil.EventReasonSwitchFailed, err.Error())

		return done, err
	}

	// Set VRG in the failed cluster (preferred cluster) to secondary
	err := d.updateVRGStateToSecondary(curHomeCluster)
	if err != nil {
		d.log.Error(err, "Failed to update existing VRG manifestwork to secondary")
		rmnutil.ReportIfNotPresent(d.reconciler.eventRecorder, d.instance, corev1.EventTypeWarning,
			rmnutil.EventReasonSwitchFailed, err.Error())

		return !done, err
	}

	newHomeCluster := d.instance.Spec.FailoverCluster

	const restorePVs = true

	result, err := d.executeRelocation(newHomeCluster, "", restorePVs)
	if err != nil {
		rmnutil.ReportIfNotPresent(d.reconciler.eventRecorder, d.instance, corev1.EventTypeWarning,
			rmnutil.EventReasonSwitchFailed, err.Error())

		return !done, err
	}

	d.advanceToNextDRState()
	d.log.Info("Exiting runFailover", "state", d.getLastDRState())
	setMetricsTimerFromDRState(rmn.FailingOver, timerStop)

	return result, nil
}

func (d *DRPCInstance) getCurrentHomeClusterName() string {
	curHomeCluster := ""
	if len(d.userPlacementRule.Status.Decisions) > 0 {
		curHomeCluster = d.userPlacementRule.Status.Decisions[0].ClusterName
	}

	if curHomeCluster == "" {
		curHomeCluster = d.instance.Status.PreferredDecision.ClusterName
	}

	return curHomeCluster
}

func (d *DRPCInstance) runRelocate() (bool, error) {
	d.log.Info("Entering runRelocate", "state", d.getLastDRState())

	const done = true

	if d.isFailoverInProgress() {
		return done, fmt.Errorf("invalid state %s for the selected action %v",
			d.getLastDRState(), d.instance.Spec.Action)
	}

	preferredCluster := d.instance.Spec.PreferredCluster
	preferredClusterNamespace := preferredCluster
	// We are done if empty. Relocation requires preferredCluster to be configured
	if preferredCluster == "" {
		return !done, fmt.Errorf("preferred cluster not valid")
	}

	if preferredCluster == d.instance.Spec.FailoverCluster {
		return done, fmt.Errorf("failoverCluster and preferredCluster can't be the same %s/%s",
			d.instance.Spec.FailoverCluster, preferredCluster)
	}

	// We are done if already failed back
	if d.hasAlreadySwitchedOver(preferredCluster) {
		err := d.ensureCleanup(preferredCluster)
		if err != nil {
			return !done, err
		}

		return done, nil
	}

	if len(d.vrgs) == 0 {
		return !done, fmt.Errorf("no VRGs exists. Can't relocate")
	}

	// For relocation, the current home cluster is not the preferredCluster
	if !d.isCurrentHomeClusterReadyForRelocation(preferredCluster) {
		d.log.Info("Waiting for current home cluster to be ready for relocation")

		return !done, nil
	}

	return d.relocate(preferredCluster, preferredClusterNamespace, rmn.Relocating)
}

func (d *DRPCInstance) relocate(preferredCluster, preferredClusterNamespace string, drState rmn.DRState) (bool, error) {
	const done = true
	// Make sure we record the state that we are failing over
	d.setDRState(drState)
	setMetricsTimerFromDRState(drState, timerStart)

	// Setting up relocation insures that all VRGs in all managed cluster are secondaries
	err := d.setupRelocation(preferredCluster)
	if err != nil {
		return !done, err
	}

	// After we insured that all clusters are secondaries, we then insure
	// that the current home cluster is ready for relocation.
	// For relocation, the current home cluster is not the preferredCluster
	if !d.isCurrentHomeClusterReadyForRelocation(preferredCluster) {
		d.log.Info("Waiting for current home cluster to be ready for relocation")

		return !done, nil
	}

	const restorePVs = true

	_, err = d.executeRelocation(preferredCluster, preferredClusterNamespace, restorePVs)
	if err != nil {
		return !done, err
	}

	// All good so far, update DRPC decision and state
	if len(d.userPlacementRule.Status.Decisions) > 0 {
		d.instance.Status.PreferredDecision = d.userPlacementRule.Status.Decisions[0]
	}

	d.advanceToNextDRState()
	d.log.Info("Done", "Last known state", d.getLastDRState())
	setMetricsTimerFromDRState(drState, timerStop)

	return done, nil
}

func (d *DRPCInstance) setupRelocation(preferredCluster string) error {
	if len(d.userPlacementRule.Status.Decisions) != 0 {
		// clear current user PlacementRule's decision
		err := d.clearUserPlacementRuleStatus()
		if err != nil {
			return err
		}
	}

	// During relocation, the preferredCluster does not contain a VRG or the VRG is already
	// secondary. We need to skip checking if the VRG for it is secondary to avoid messing up with the
	// order of execution (it could be refactored better to avoid this complexity). IOW, if we first update
	// VRG in all clusters to secondaries, and then we call executeRelocation, and If executeRelocation does not
	// complete in one shot, then coming back to this loop will reset the preferredCluster to secondary again.
	clusterToSkip := preferredCluster
	if !d.ensureVRGIsSecondaryEverywhere(clusterToSkip) {
		// During relocation, both clusters should be up and both must be secondaries before we proceed.
		success := d.moveVRGToSecondaryEverywhere()
		if !success {
			return fmt.Errorf("waiting for VRGs to move to secondaries everywhere")
		}
	}

	return nil
}

// executeRelocation is a series of steps to creating, updating, and cleaning up
// the necessary objects for the failover or relocation
func (d *DRPCInstance) executeRelocation(targetCluster, targetClusterNamespace string, restorePVs bool) (bool, error) {
	d.log.Info("executeRelocation", "cluster", targetCluster, "restorePVs", restorePVs)

	createdOrUpdated, err := d.createVRGManifestWorkAsPrimary(targetCluster)
	if err != nil {
		return false, err
	}

	if createdOrUpdated && restorePVs {
		// We just created MWs. Give it time until the PV restore is complete
		return false, fmt.Errorf("%w)", WaitForPVRestoreToComplete)
	}

	// already a primary
	if restorePVs {
		restored, err := d.checkPVsHaveBeenRestored(targetCluster)
		if err != nil {
			return false, err
		}

		d.log.Info("Checked whether PVs have been restored", "Yes?", restored)

		if !restored {
			return false, fmt.Errorf("%w)", WaitForPVRestoreToComplete)
		}
	}

	err = d.updateUserPlacementRule(targetCluster, targetClusterNamespace)
	if err != nil {
		return false, err
	}

	// Attempt to delete VRG MW from failed clusters
	// This is attempt to clean up is not guaranteed to complete at this stage. Deleting the old VRG
	// requires guaranteeing that the VRG has transitioned to secondary.
	clusterToSkip := targetCluster

	return d.cleanupSecondaries(clusterToSkip)
}

func (d *DRPCInstance) createVRGManifestWorkAsPrimary(targetCluster string) (bool, error) {
	d.log.Info("create or update VRG if it does not exists or is not primary", "cluster", targetCluster)

	const created = true

	vrg, err := d.getVRGFromManifestWork(targetCluster)
	if err != nil {
		if !errors.IsNotFound(err) {
			return !created, err
		}
	}

	if vrg != nil {
		if vrg.Spec.ReplicationState == rmn.Primary {
			d.log.Info("VRG MW already Primary on this cluster", "name", vrg.Name, "cluster", targetCluster)

			return !created, nil
		}
	}

	err = d.createVRGManifestWork(targetCluster)
	if err != nil {
		return !created, err
	}

	return created, nil
}

func (d *DRPCInstance) getVRGFromManifestWork(clusterName string) (*rmn.VolumeReplicationGroup, error) {
	vrgMWName := d.mwu.BuildManifestWorkName(rmnutil.MWTypeVRG)

	mw, err := d.mwu.FindManifestWork(vrgMWName, clusterName)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}

	vrg, err := d.extractVRGFromManifestWork(mw)
	if err != nil {
		return nil, err
	}

	return vrg, nil
}

func (d *DRPCInstance) hasAlreadySwitchedOver(targetCluster string) bool {
	if len(d.userPlacementRule.Status.Decisions) > 0 &&
		targetCluster == d.userPlacementRule.Status.Decisions[0].ClusterName {
		d.log.Info(fmt.Sprintf("Already switched over to cluster %s. Last state: %v",
			targetCluster, d.getLastDRState()))

		return true
	}

	return false
}

//nolint:cyclop
func (d *DRPCInstance) isCurrentHomeClusterReadyForRelocation(targetCluster string) bool {
	const ready = true
	// This should not happen for relocation
	if len(d.vrgs) == 0 {
		d.log.Info("VRG count is 0. Should not happen for relocation")

		return !ready
	}
	// IF len is greater than 1, then the only possibility for this to happen is
	// when the targetCluster has already been moved to primary. We must insure
	// that condition is true
	if len(d.vrgs) > 1 {
		return d.findPrimaryCluster() == targetCluster
	}
	// We have 1 VRG and that VRG may or may not be a primary. But we know that
	// it is the current home cluster.
	curHomeCluster := ""
	for cn := range d.vrgs {
		curHomeCluster = cn
	}

	d.log.Info("Getting latest VRG object", "cluster", curHomeCluster)

	// Get fresh VRG
	vrg, err := d.reconciler.MCVGetter.GetVRGFromManagedCluster(d.instance.Name, d.instance.Namespace, curHomeCluster)
	if err != nil {
		d.log.Info("Failed to get VRG through MCV", "error", err)
		// Only NotFound error is accepted
		if errors.IsNotFound(err) {
			return ready
		}

		return !ready
	}

	dataReadyCondition := findCondition(vrg.Status.Conditions, VRGConditionTypeDataReady)
	if dataReadyCondition == nil {
		d.log.Info("VRG Condition not available", "cluster", curHomeCluster)

		return !ready
	}

	clusterDataReadyCondition := findCondition(vrg.Status.Conditions, VRGConditionTypeClusterDataReady)
	if clusterDataReadyCondition == nil {
		d.log.Info("VRG Condition ClusterData not available", "cluster", curHomeCluster)

		return !ready
	}

	return dataReadyCondition.Status == metav1.ConditionTrue &&
		dataReadyCondition.ObservedGeneration == vrg.Generation &&
		clusterDataReadyCondition.Status == metav1.ConditionTrue &&
		clusterDataReadyCondition.ObservedGeneration == vrg.Generation
}

func (d *DRPCInstance) findPrimaryCluster() string {
	for clusterName, vrg := range d.vrgs {
		if d.isVRGPrimary(vrg) {
			return clusterName
		}
	}

	return ""
}

func (d *DRPCInstance) moveVRGToSecondaryEverywhere() bool {
	failedCount := 0

	for _, drCluster := range d.drPolicy.Spec.DRClusterSet {
		clusterName := drCluster.Name

		err := d.updateVRGStateToSecondary(clusterName)
		if err != nil {
			d.log.Error(err, "Failed to update VRG to secondary", "cluster", clusterName)

			failedCount++
		}
	}

	if failedCount != 0 {
		d.log.Info("Failed to update VRG to secondary", "FailedCount", failedCount)

		return false
	}

	return d.ensureVRGIsSecondaryEverywhere("")
}

// outputs a string for use in creating a ManagedClusterView name
// example: when looking for a vrg with name 'demo' in the namespace 'ramen', input: ("demo", "ramen", "vrg")
// this will give output "demo-ramen-vrg-mcv"
func BuildManagedClusterViewName(resourceName, resourceNamespace, resource string) string {
	return fmt.Sprintf("%s-%s-%s-mcv", resourceName, resourceNamespace, resource)
}

func (d *DRPCInstance) cleanupSecondaries(skipCluster string) (bool, error) {
	for _, drCluster := range d.drPolicy.Spec.DRClusterSet {
		clusterName := drCluster.Name
		if skipCluster == clusterName {
			continue
		}

		// If VRG hasn't been deleted, then make sure that the MW for it is deleted and
		// return and wait
		mwDeleted, err := d.ensureVRGManifestWorkOnClusterDeleted(clusterName)
		if err != nil {
			return false, nil
		}

		if !mwDeleted {
			return false, nil
		}

		d.log.Info("MW has been deleted. Check the VRG")

		if !d.ensureVRGDeleted(clusterName) {
			d.log.Info("VRG has not been deleted yet", "cluster", clusterName)

			return false, nil
		}

		mcvName := BuildManagedClusterViewName(d.instance.Name, d.instance.Namespace, "vrg")
		// MW is deleted, VRG is deleted, so we no longer need MCV for the VRG
		err = d.reconciler.deleteManagedClusterView(clusterName, mcvName)
		if err != nil {
			return false, err
		}
	}

	return true, nil
}

func (d *DRPCInstance) updateUserPlacementRule(homeCluster, homeClusterNamespace string) error {
	d.log.Info("Updating userPlacementRule", "name", d.userPlacementRule.Name)

	if homeClusterNamespace == "" {
		homeClusterNamespace = homeCluster
	}

	newPD := []plrv1.PlacementDecision{
		{
			ClusterName:      homeCluster,
			ClusterNamespace: homeClusterNamespace,
		},
	}

	status := plrv1.PlacementRuleStatus{
		Decisions: newPD,
	}

	return d.updateUserPlacementRuleStatus(status)
}

func (d *DRPCInstance) clearUserPlacementRuleStatus() error {
	d.log.Info("Updating userPlacementRule", "name", d.userPlacementRule.Name)

	status := plrv1.PlacementRuleStatus{}

	return d.updateUserPlacementRuleStatus(status)
}

func (d *DRPCInstance) updateUserPlacementRuleStatus(status plrv1.PlacementRuleStatus) error {
	d.log.Info("Updating userPlacementRule", "name", d.userPlacementRule.Name)

	if !reflect.DeepEqual(status, d.userPlacementRule.Status) {
		d.userPlacementRule.Status = status
		if err := d.reconciler.Status().Update(d.ctx, d.userPlacementRule); err != nil {
			d.log.Error(err, "failed to update user PlacementRule")

			return fmt.Errorf("failed to update userPlRule %s (%w)", d.userPlacementRule.Name, err)
		}

		d.log.Info("Updated user PlacementRule status", "Decisions", d.userPlacementRule.Status.Decisions)
	}

	return nil
}

func (d *DRPCInstance) createVRGManifestWork(homeCluster string) error {
	d.log.Info("Creating VRG ManifestWork",
		"Last State:", d.getLastDRState(), "cluster", homeCluster)

	if err := d.mwu.CreateOrUpdateVRGManifestWork(
		d.instance.Name, d.instance.Namespace,
		homeCluster, d.drPolicy,
		d.instance.Spec.PVCSelector); err != nil {
		d.log.Error(err, "failed to create or update VolumeReplicationGroup manifest")

		return fmt.Errorf("failed to create or update VolumeReplicationGroup manifest in namespace %s (%w)", homeCluster, err)
	}

	return nil
}

func (d *DRPCInstance) isVRGPrimary(vrg *rmn.VolumeReplicationGroup) bool {
	return (vrg.Spec.ReplicationState == rmn.Primary)
}

func (d *DRPCInstance) checkPVsHaveBeenRestored(homeCluster string) (bool, error) {
	d.log.Info("Checking whether PVs have been restored", "cluster", homeCluster)

	vrg, err := d.reconciler.MCVGetter.GetVRGFromManagedCluster(d.instance.Name,
		d.instance.Namespace, homeCluster)
	if err != nil {
		d.log.Info("Failed to get VRG through MCV", "error", err)

		return false, fmt.Errorf("%w", err)
	}

	clusterDataReady := findCondition(vrg.Status.Conditions, VRGConditionTypeClusterDataReady)
	if clusterDataReady == nil {
		d.log.Info("Waiting for PVs to be restored", "cluster", homeCluster)

		return false, nil
	}

	return clusterDataReady.Status == metav1.ConditionTrue && clusterDataReady.ObservedGeneration == vrg.Generation, nil
}

func (d *DRPCInstance) ensureCleanup(clusterToSkip string) error {
	d.log.Info("ensure cleanup on secondaries")

	idx, condition := GetDRPCCondition(&d.instance.Status, rmn.ConditionPeerReady)

	if idx == -1 {
		d.log.Info("Generating new condition")
		condition = d.newCondition(rmn.ConditionPeerReady)
		d.instance.Status.Conditions = append(d.instance.Status.Conditions, *condition)
		idx = len(d.instance.Status.Conditions) - 1
		d.needStatusUpdate = true
	}

	d.log.Info(fmt.Sprintf("Condition %v", condition))

	if condition.Reason == rmn.ReasonSuccess &&
		condition.Status == metav1.ConditionTrue &&
		condition.ObservedGeneration == d.instance.Generation {
		d.log.Info("Condition values tallied, cleanup is considered complete")

		return nil
	}

	d.updateCondition(condition, idx, rmn.ReasonCleaning, metav1.ConditionFalse)

	clean, err := d.cleanupSecondaries(clusterToSkip)
	if err != nil {
		return err
	}

	if !clean {
		return fmt.Errorf("failed to clean secondaries")
	}

	d.updateCondition(condition, idx, rmn.ReasonSuccess, metav1.ConditionTrue)

	return nil
}

func (d *DRPCInstance) updateCondition(condition *metav1.Condition, idx int, reason string,
	status metav1.ConditionStatus) {
	condition.Reason = reason
	condition.Status = status
	condition.ObservedGeneration = d.instance.Generation
	d.instance.Status.Conditions[idx] = *condition
	d.needStatusUpdate = true
}

func (d *DRPCInstance) ensureVRGManifestWorkOnClusterDeleted(clusterName string) (bool, error) {
	d.log.Info("Ensuring MW for the VRG is deleted", "cluster", clusterName)

	const done = true

	mwName := d.mwu.BuildManifestWorkName(rmnutil.MWTypeVRG)
	mw := &ocmworkv1.ManifestWork{}

	err := d.reconciler.Get(d.ctx, types.NamespacedName{Name: mwName, Namespace: clusterName}, mw)
	if err != nil {
		if errors.IsNotFound(err) {
			return done, nil
		}

		return !done, fmt.Errorf("failed to retrieve ManifestWork (%w)", err)
	}

	// We have to make sure that the VRG for the MW was set to secondary,
	updated, err := d.hasVRGStateBeenUpdatedToSecondary(clusterName)
	if err != nil {
		return !done, fmt.Errorf("failed to check whether VRG replication state has been updated to secondary (%w)", err)
	}

	// If it is not set to secondary, then update it
	if !updated {
		err = d.updateVRGStateToSecondary(clusterName)
		// We need to wait for the MW to go to applied state
		return !done, err
	}

	// if !IsManifestInAppliedState(mw) {
	// 	d.log.Info(fmt.Sprintf("ManifestWork %s/%s NOT in Applied state", mw.Namespace, mw.Name))
	// 	// Wait for MW to be applied. The DRPC reconciliation will be called then
	// 	return done, nil
	// }

	// d.log.Info("VRG ManifestWork is in Applied state", "name", mw.Name, "cluster", clusterName)

	if d.ensureVRGIsSecondaryOnCluster(clusterName) {
		err := d.mwu.DeleteManifestWorksForCluster(clusterName)
		if err != nil {
			return !done, fmt.Errorf("%w", err)
		}

		return done, nil
	}

	d.log.Info("Request not complete yet", "cluster", clusterName)
	// IF we get here, either the VRG has not transitioned to secondary (yet) or delete didn't succeed. In either cases,
	// we need to make sure that the VRG object is deleted. IOW, we still have to wait
	return !done, nil
}

// ensureVRGIsSecondaryEverywhere iterates through all the clusters in the DRCluster set,
// and for each cluster, it checks whether the VRG (if exists) is secondary. It will skip
// a cluster if provided. It returns true if all clusters report secondary for the VRG,
// otherwise, it returns false
func (d *DRPCInstance) ensureVRGIsSecondaryEverywhere(clusterToSkip string) bool {
	for _, drCluster := range d.drPolicy.Spec.DRClusterSet {
		clusterName := drCluster.Name
		if clusterToSkip == clusterName {
			continue
		}

		if !d.ensureVRGIsSecondaryOnCluster(clusterName) {
			d.log.Info("Still waiting for VRG to transition to secondary", "cluster", clusterName)

			return false
		}
	}

	return true
}

//
// ensureVRGIsSecondaryOnCluster returns true whether the VRG is secondary or it does not exists on the cluster
//
func (d *DRPCInstance) ensureVRGIsSecondaryOnCluster(clusterName string) bool {
	d.log.Info(fmt.Sprintf("Ensure VRG %s is secondary on cluster %s", d.instance.Name, clusterName))

	d.mcvRequestInProgress = false

	vrg, err := d.reconciler.MCVGetter.GetVRGFromManagedCluster(d.instance.Name,
		d.instance.Namespace, clusterName)
	if err != nil {
		if errors.IsNotFound(err) {
			return true // ensured
		}

		d.log.Info("Failed to get VRG", "errorValue", err)

		d.mcvRequestInProgress = true

		return false
	}

	if vrg.Status.State != rmn.SecondaryState {
		d.log.Info(fmt.Sprintf("vrg status replication state for cluster %s is %v",
			clusterName, vrg))

		return false
	}

	return true
}

func (d *DRPCInstance) ensureVRGDeleted(clusterName string) bool {
	d.mcvRequestInProgress = false

	vrg, err := d.reconciler.MCVGetter.GetVRGFromManagedCluster(d.instance.Name,
		d.instance.Namespace, clusterName)
	if err != nil {
		d.log.Info("Failed to get VRG using MCV", "error", err)
		// Only NotFound error is accepted
		if errors.IsNotFound(err) {
			return true // ensured
		}

		d.mcvRequestInProgress = true
	}

	d.log.Info(fmt.Sprintf("Got VRG using MCV -- %v", vrg))

	return false
}

func (d *DRPCInstance) hasVRGStateBeenUpdatedToSecondary(clusterName string) (bool, error) {
	vrgMWName := d.mwu.BuildManifestWorkName(rmnutil.MWTypeVRG)
	d.log.Info("Check if VRG has been updated to secondary", "name", vrgMWName, "cluster", clusterName)

	vrg, err := d.getVRGFromManifestWork(clusterName)
	if err != nil {
		d.log.Error(err, "failed to check whether VRG state is secondary")

		return false, fmt.Errorf("failed to check whether VRG state for %s is secondary, in namespace %s (%w)",
			vrgMWName, clusterName, err)
	}

	if vrg.Spec.ReplicationState == rmn.Secondary {
		d.log.Info("VRG MW already secondary on this cluster", "name", vrg.Name, "cluster", clusterName)

		return true, nil
	}

	return false, err
}

func (d *DRPCInstance) updateVRGStateToSecondary(clusterName string) error {
	vrgMWName := d.mwu.BuildManifestWorkName(rmnutil.MWTypeVRG)
	d.log.Info(fmt.Sprintf("Updating VRG ownedby MW %s to secondary for cluster %s", vrgMWName, clusterName))

	mw, err := d.mwu.FindManifestWork(vrgMWName, clusterName)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}

		d.log.Error(err, "failed to update VRG state")

		return fmt.Errorf("failed to update VRG state for %s, in namespace %s (%w)",
			vrgMWName, clusterName, err)
	}

	vrg, err := d.extractVRGFromManifestWork(mw)
	if err != nil {
		d.log.Error(err, "failed to update VRG state")

		return err
	}

	if vrg.Spec.ReplicationState == rmn.Secondary {
		d.log.Info(fmt.Sprintf("VRG %s already secondary on this cluster %s", vrg.Name, mw.Namespace))

		return nil
	}

	vrg.Spec.ReplicationState = rmn.Secondary

	vrgClientManifest, err := d.mwu.GenerateManifest(vrg)
	if err != nil {
		d.log.Error(err, "failed to generate manifest")

		return fmt.Errorf("failed to generate VRG manifest (%w)", err)
	}

	mw.Spec.Workload.Manifests[0] = *vrgClientManifest

	err = d.reconciler.Update(d.ctx, mw)
	if err != nil {
		return fmt.Errorf("failed to update MW (%w)", err)
	}

	d.log.Info(fmt.Sprintf("Updated VRG running in cluster %s to secondary. VRG (%v)", clusterName, vrg))

	return nil
}

func (d *DRPCInstance) extractVRGFromManifestWork(mw *ocmworkv1.ManifestWork) (*rmn.VolumeReplicationGroup, error) {
	if len(mw.Spec.Workload.Manifests) == 0 {
		return nil, fmt.Errorf("invalid VRG ManifestWork for type: %s", mw.Name)
	}

	vrgClientManifest := &mw.Spec.Workload.Manifests[0]
	vrg := &rmn.VolumeReplicationGroup{}

	err := yaml.Unmarshal(vrgClientManifest.RawExtension.Raw, &vrg)
	if err != nil {
		return nil, fmt.Errorf("unable to unmarshal VRG object (%w)", err)
	}

	return vrg, nil
}

func (d *DRPCInstance) updateDRPCStatus() error {
	d.log.Info("Updating DRPC status")

	if len(d.userPlacementRule.Status.Decisions) != 0 {
		vrg, err := d.reconciler.MCVGetter.GetVRGFromManagedCluster(d.instance.Name, d.instance.Namespace,
			d.userPlacementRule.Status.Decisions[0].ClusterName)
		if err != nil {
			// VRG must have been deleted if the error is NotFound. In either case,
			// we don't have a VRG
			d.log.Info("Failed to get VRG from managed cluster", "errMsg", err)

			d.instance.Status.ResourceConditions = rmn.VRGConditions{}
		} else {
			d.instance.Status.ResourceConditions.ResourceMeta.Kind = vrg.Kind
			d.instance.Status.ResourceConditions.ResourceMeta.Name = vrg.Name
			d.instance.Status.ResourceConditions.ResourceMeta.Namespace = vrg.Namespace
			d.instance.Status.ResourceConditions.Conditions = vrg.Status.Conditions
		}
	}

	d.instance.Status.LastUpdateTime = metav1.Now()

	if err := d.reconciler.Status().Update(d.ctx, d.instance); err != nil {
		return errorswrapper.Wrap(err, "failed to update DRPC status")
	}

	d.log.Info(fmt.Sprintf("Updated DRPC Status %+v", d.instance.Status))

	return nil
}

func (d *DRPCInstance) advanceToNextDRState() {
	lastDRState := d.getLastDRState()
	nextState := lastDRState

	switch lastDRState {
	case rmn.Deploying:
		nextState = rmn.Deployed
	case rmn.FailingOver:
		nextState = rmn.FailedOver
	case rmn.Relocating:
		nextState = rmn.Relocated
	case rmn.Deployed:
	case rmn.FailedOver:
	case rmn.Relocated:
	}

	d.setDRState(nextState)
}

func (d *DRPCInstance) setDRState(nextState rmn.DRState) {
	if d.instance.Status.Phase != nextState {
		d.log.Info(fmt.Sprintf("Phase: Current '%s'. Next '%s'",
			d.instance.Status.Phase, nextState))

		d.instance.Status.Phase = nextState
		d.updateConditions()

		d.reportEvent(nextState)

		d.needStatusUpdate = true
	}
}

func (d *DRPCInstance) reportEvent(nextState rmn.DRState) {
	eventReason := "unknown state"
	eventType := corev1.EventTypeWarning
	msg := "next state not known"

	switch nextState {
	case rmn.Deploying:
		eventReason = rmnutil.EventReasonDeploying
		eventType = corev1.EventTypeNormal
		msg = "Deploying the application and VRG"
	case rmn.Deployed:
		eventReason = rmnutil.EventReasonDeploySuccess
		eventType = corev1.EventTypeNormal
		msg = "Successfully deployed the application and VRG"
	case rmn.FailingOver:
		eventReason = rmnutil.EventReasonFailingOver
		eventType = corev1.EventTypeWarning
		msg = "Failing over the application and VRG"
	case rmn.FailedOver:
		eventReason = rmnutil.EventReasonFailoverSuccess
		eventType = corev1.EventTypeNormal
		msg = "Successfully failedover the application and VRG"
	case rmn.Relocating:
		eventReason = rmnutil.EventReasonRelocating
		eventType = corev1.EventTypeNormal
		msg = "Relocating the application and VRG"
	case rmn.Relocated:
		eventReason = rmnutil.EventReasonRelocationSuccess
		eventType = corev1.EventTypeNormal
		msg = "Successfully relocated the application and VRG"
	}

	rmnutil.ReportIfNotPresent(d.reconciler.eventRecorder, d.instance, eventType,
		eventReason, msg)
}

func (d *DRPCInstance) updateConditions() {
	d.log.Info(fmt.Sprintf("Current Conditions '%v'", d.instance.Status.Conditions))

	for _, condType := range []string{rmn.ConditionAvailable, rmn.ConditionPeerReady} {
		condition := d.newCondition(condType)

		idx, _ := GetDRPCCondition(&d.instance.Status, condType)
		if idx == -1 {
			d.instance.Status.Conditions = append(d.instance.Status.Conditions, *condition)
		} else {
			d.instance.Status.Conditions[idx] = *condition
		}
	}

	d.log.Info(fmt.Sprintf("Updated Conditions '%v'", d.instance.Status.Conditions))
}

func (d *DRPCInstance) newCondition(condType string) *metav1.Condition {
	return &metav1.Condition{
		Type:   condType,
		Status: d.getConditionStatus(condType),
		// ObservedGeneration: d.getObservedGeneration(condType),
		LastTransitionTime: metav1.Now(),
		Reason:             d.getConditionReason(condType),
		Message:            d.getConditionMessage(condType),
		ObservedGeneration: d.instance.Generation,
	}
}

func (d *DRPCInstance) getConditionStatus(condType string) metav1.ConditionStatus {
	if condType == rmn.ConditionAvailable {
		return d.getConditionStatusForTypeAvailable()
	} else if condType == rmn.ConditionPeerReady {
		return d.getConditionStatusForTypePeerReady()
	}

	return metav1.ConditionUnknown
}

func (d *DRPCInstance) getConditionStatusForTypeAvailable() metav1.ConditionStatus {
	if d.isInFinalPhase() {
		return metav1.ConditionTrue
	}

	if d.isInProgressingPhase() {
		return metav1.ConditionFalse
	}

	return metav1.ConditionUnknown
}

func (d *DRPCInstance) getConditionStatusForTypePeerReady() metav1.ConditionStatus {
	if d.isInFinalPhase() {
		if !d.peerReady() {
			return metav1.ConditionFalse
		}

		return metav1.ConditionTrue
	}

	if d.isInProgressingPhase() {
		return metav1.ConditionFalse
	}

	return metav1.ConditionUnknown
}

func (d *DRPCInstance) peerReady() bool {
	// Deployed phase requires no cleanup and no further reconcile for cleanup
	if d.instance.Status.Phase == rmn.Deployed {
		d.log.Info("Initial deployed phase detected")

		return true
	}

	idx, condition := GetDRPCCondition(&d.instance.Status, rmn.ConditionPeerReady)
	d.log.Info(fmt.Sprintf("DRPC Condition %v", condition))

	if idx == -1 {
		d.log.Info("Found missing PeerReady condition")

		return false
	}

	if condition.Reason == rmn.ReasonSuccess &&
		condition.Status == metav1.ConditionTrue &&
		condition.ObservedGeneration == d.instance.Generation {
		return true
	}

	return false
}

func (d *DRPCInstance) getConditionReason(condType string) string {
	if condType == rmn.ConditionAvailable {
		return string(d.instance.Status.Phase)
	} else if condType == rmn.ConditionPeerReady {
		return d.getReasonForConditionTypePeerReady()
	}

	return rmn.ReasonUnknown
}

func (d *DRPCInstance) getReasonForConditionTypePeerReady() string {
	if d.isInFinalPhase() {
		if !d.peerReady() {
			return rmn.ReasonCleaning
		}

		return rmn.ReasonSuccess
	}

	if d.isInProgressingPhase() {
		return rmn.ReasonProgressing
	}

	return rmn.ReasonUnknown
}

func (d *DRPCInstance) getConditionMessage(condType string) string {
	return fmt.Sprintf("Condition type %s", condType)
}

//nolint:exhaustive
func (d *DRPCInstance) isInFinalPhase() bool {
	switch d.instance.Status.Phase {
	case rmn.Deployed:
		fallthrough
	case rmn.FailedOver:
		fallthrough
	case rmn.Relocated:
		return true
	default:
		return false
	}
}

//nolint:exhaustive
func (d *DRPCInstance) isInProgressingPhase() bool {
	switch d.instance.Status.Phase {
	case rmn.Deploying:
		fallthrough
	case rmn.FailingOver:
		fallthrough
	case rmn.Relocating:
		return true
	default:
		return false
	}
}

func (d *DRPCInstance) getLastDRState() rmn.DRState {
	return d.instance.Status.Phase
}

//nolint:exhaustive
func (d *DRPCInstance) getRequeueDuration() time.Duration {
	d.log.Info("Getting requeue duration", "last known DR state", d.getLastDRState())

	const (
		failoverRequeueDelay   = time.Minute * 5
		relocationRequeueDelay = time.Second * 2
	)

	duration := time.Second // second

	switch d.getLastDRState() {
	case rmn.FailingOver:
		duration = failoverRequeueDelay
	case rmn.Relocating:
		duration = relocationRequeueDelay
	}

	return duration
}

func (d *DRPCInstance) isFailoverInProgress() bool {
	return d.isInProgress(d.instance.Spec.FailoverCluster)
}

func (d *DRPCInstance) isRelocationInProgress() bool {
	return d.isInProgress(d.instance.Spec.PreferredCluster)
}

func (d *DRPCInstance) isInProgress(targetCluster string) bool {
	const inprogress = true

	// IF no vrg Manifestwork for the failoverCluster, then we are not inpgrogress
	mwName := d.mwu.BuildManifestWorkName(rmnutil.MWTypeVRG)

	_, err := d.mwu.FindManifestWork(mwName, targetCluster)
	if err != nil {
		if errors.IsNotFound(err) {
			return !inprogress
		}
	}

	// IF we have MW or retreiving it failed with an error other than NotFound,
	// then check if we have VRG in the cache. If no vrg object exists, then
	// just assume we are not inprogress
	vrg := d.getCachedVRG(targetCluster)
	if vrg == nil {
		return !inprogress
	}

	// IF we have a VRG and it is in protected state, then we are not inprogress
	if d.isVRGProtected(vrg) {
		return !inprogress
	}

	// IF none of the above, then we are in-progress
	return inprogress
}
