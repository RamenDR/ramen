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
	errorswrapper "github.com/pkg/errors"
	plrv1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	rmnutil "github.com/ramendr/ramen/controllers/util"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var WaitForPVRestoreToComplete = errorswrapper.New("Waiting for PV restore to complete...")
var WaitForVolSyncDestRepToComplete = errorswrapper.New("Waiting for VolSync RD to complete...")
var WaitForVolSyncSrcRepToComplete = errorswrapper.New("Waiting for VolSync RS to complete...")
var WaitForVolSyncManifestWorkCreation = errorswrapper.New("Waiting for VolSync ManifestWork to be created...")
var WaitForVolSyncRDInfoAvailibility = errorswrapper.New("Waiting for VolSync RDInfo...")

type DRPCInstance struct {
	reconciler           *DRPlacementControlReconciler
	ctx                  context.Context
	log                  logr.Logger
	instance             *rmn.DRPlacementControl
	drPolicy             *rmn.DRPolicy
	drClusters           []rmn.DRCluster
	needStatusUpdate     bool
	mcvRequestInProgress bool
	userPlacementRule    *plrv1.PlacementRule
	drpcPlacementRule    *plrv1.PlacementRule
	vrgs                 map[string]*rmn.VolumeReplicationGroup
	mwu                  rmnutil.MWUtil
	metricsTimer         timerInstance
}

func (d *DRPCInstance) startProcessing() bool {
	d.log.Info("Starting to process placement")

	requeue := true
	done, processingErr := d.processPlacement()

	if d.shouldUpdateStatus() || d.statusUpdateTimeElapsed() {
		if err := d.reconciler.updateDRPCStatus(d.instance, d.userPlacementRule); err != nil {
			d.log.Error(err, "failed to update status")

			return requeue
		}
	}

	if processingErr != nil {
		d.log.Info("Process placement", "error", processingErr.Error())

		return requeue
	}

	requeue = !done
	d.log.Info("Completed processing placement", "requeue", requeue)

	return requeue
}

func (d *DRPCInstance) processPlacement() (bool, error) {
	d.log.Info("Process DRPC Placement", "DRAction", d.instance.Spec.Action)

	switch d.instance.Spec.Action {
	case rmn.ActionFailover:
		return d.RunFailover()
	case rmn.ActionRelocate:
		return d.RunRelocate()
	}

	// Not a failover or a relocation.  Must be an initial deployment.
	return d.RunInitialDeployment()
}

func (d *DRPCInstance) RunInitialDeployment() (bool, error) {
	d.log.Info("Running initial deployment")

	const done = true

	homeCluster, homeClusterNamespace := d.getHomeCluster()

	if homeCluster == "" {
		err := fmt.Errorf("PreferredCluster not set and unable to find home cluster in DRPCPlacementRule (%v)",
			d.drpcPlacementRule)
		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), err.Error())
		// needStatusUpdate is not set. Still better to capture the event to report later
		rmnutil.ReportIfNotPresent(d.reconciler.eventRecorder, d.instance, corev1.EventTypeWarning,
			rmnutil.EventReasonDeployFail, err.Error())

		return !done, err
	}

	d.log.Info(fmt.Sprintf("Using homeCluster %s for initial deployment, uPlRule Decision %+v",
		homeCluster, d.userPlacementRule.Status.Decisions))

	// Check if we already deployed in the homeCluster or elsewhere
	deployed, clusterName := d.isDeployed(homeCluster)
	if deployed && clusterName != homeCluster {
		// IF deployed on cluster that is not the preferred HomeCluster, and
		// VolSync is not used, then we are done
		if !d.isVolSyncReplicationNeeded(homeCluster) {
			return done, nil
		}
	}

	// Ensure that initial deployment is complete
	if deployed && d.isUserPlRuleUpdated(homeCluster) {
		if d.isVolSyncReplicationNeeded(homeCluster) {
			err := d.EnsureVolSyncReplicationSetup(homeCluster)
			if err != nil {
				return !done, err
			}
		}

		// If for whatever reason, the DRPC status is missing (i.e. DRPC could have been deleted mistakingly and
		// recreated again), we should update it with whatever status we are at.
		if d.getLastDRState() == rmn.DRState("") {
			d.instance.Status.PreferredDecision = d.userPlacementRule.Status.Decisions[0]
			d.setDRState(rmn.Deployed)
			d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
				d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), "Already deployed")

			d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionPeerReady, d.instance.Generation,
				metav1.ConditionTrue, rmn.ReasonSuccess, "Ready")
		}

		return done, nil
	}

	result, err := d.startDeploying(homeCluster, homeClusterNamespace)
	if err != nil {
		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), err.Error())

		return !done, err
	}

	d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
		d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), "Initial deployment completed")

	d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionPeerReady, d.instance.Generation,
		metav1.ConditionTrue, rmn.ReasonSuccess, "Ready")

	return result, nil
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
func (d *DRPCInstance) isDeployed(homeCluster string) (bool, string) {
	if d.isVRGAlreadyDeployedOnTargetCluster(homeCluster) {
		d.log.Info(fmt.Sprintf("Already deployed on homeCluster %s. Last state: %s",
			homeCluster, d.getLastDRState()))

		return true, homeCluster
	}

	clusterName, found := d.isVRGAlreadyDeployedElsewhere(homeCluster)
	if found {
		errMsg := fmt.Sprintf("Failed to place deployment on cluster %s, as it is active on cluster %s",
			homeCluster, clusterName)
		d.log.Info(errMsg)

		return true, clusterName
	}

	return false, ""
}

func (d *DRPCInstance) isUserPlRuleUpdated(homeCluster string) bool {
	return len(d.userPlacementRule.Status.Decisions) > 0 &&
		d.userPlacementRule.Status.Decisions[0].ClusterName == homeCluster
}

// isVRGAlreadyDeployedOnTargetCluster will check whether a VRG exists in the targetCluster and
// whether it is in protected state, and primary.
func (d *DRPCInstance) isVRGAlreadyDeployedOnTargetCluster(targetCluster string) bool {
	d.log.Info(fmt.Sprintf("isAlreadyDeployedAndProtected? - %q", reflect.ValueOf(d.vrgs).MapKeys()))

	return d.getCachedVRG(targetCluster) != nil
}

func (d *DRPCInstance) getCachedVRG(clusterName string) *rmn.VolumeReplicationGroup {
	vrg, found := d.vrgs[clusterName]
	if !found {
		d.log.Info("VRG not found on cluster", "Name", clusterName)

		return nil
	}

	return vrg
}

func (d *DRPCInstance) isVRGAlreadyDeployedElsewhere(clusterToSkip string) (string, bool) {
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
	d.setMetricsTimerFromDRState(rmn.Deploying)

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
	d.setMetricsTimerFromDRState(rmn.Deployed)

	return done, nil
}

//
// RunFailover:
// 1. If failoverCluster empty, then fail it and we are done
// 2. If already failed over, then ensure clean up and we are done
// 3. Set VRG for the preferredCluster to secondary
// 5. Update UserPlacementRule decision to failoverCluster
// 6. Create VRG for the failoverCluster as Primary
// 7. Update DRPC status
// 8. Delete VRG MW from preferredCluster once the VRG state has changed to Secondary
//
func (d *DRPCInstance) RunFailover() (bool, error) {
	d.log.Info("Entering RunFailover", "state", d.getLastDRState())

	const done = true

	// We are done if empty
	if d.instance.Spec.FailoverCluster == "" {
		msg := "failover cluster not set. FailoverCluster is a mandatory field"
		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), msg)

		return done, fmt.Errorf(msg)
	}

	// Failover cluster does not have a VRG yet, then start failover
	// NOTE: If an initial spec started with the failover action, it will be failed over to the
	// provided failover cluster. This is an inadvertent outcome, but deemed not an issue.
	failoverClusterVRG, ok := d.vrgs[d.instance.Spec.FailoverCluster]
	if !ok || d.isVRGSecondary(failoverClusterVRG) {
		return d.switchToFailoverCluster()
	}

	// If VRG at failover cluster is still Secondary, report error as we cannot proceed
	// TODO: Secondary will only be cleaned up if it reestablished a sync, so check if it is a healthy
	// secondary and recover from there? and if not fail!
	if d.isVRGSecondary(failoverClusterVRG) {
		msg := "failover cluster has not recovered from last placement action"
		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), msg)

		return done, fmt.Errorf(msg)
	}

	// VRG is primary in the failoverCluster, we are done if we have already failed over
	if d.hasAlreadySwitchedOver(d.instance.Spec.FailoverCluster) {
		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			metav1.ConditionTrue, string(d.instance.Status.Phase), "Completed")

		ready := d.isVRGConditionDataReady(d.instance.Spec.FailoverCluster)
		if !ready {
			d.log.Info("VRGCondition not ready")

			return !done, nil
		}

		err := d.resetRDInfoOnPrimary(d.instance.Spec.FailoverCluster)
		if err != nil {
			return !done, err
		}

		clusterToSkip := d.instance.Spec.FailoverCluster
		err = d.EnsureCleanup(clusterToSkip)
		if err != nil {
			return !done, err
		}

		// Make sure we have updated list of VRGs

		err = d.EnsureVolSyncReplicationSetup(d.instance.Spec.FailoverCluster)
		if err != nil {
			return !done, err
		}

		return done, nil
	}

	return d.switchToFailoverCluster()
}

func (d *DRPCInstance) checkClusterFenced(cluster string, drClusters []rmn.DRCluster) (bool, error) {
	for i := range drClusters {
		if drClusters[i].Name != cluster {
			continue
		}

		drClusterFencedCondition := findCondition(drClusters[i].Status.Conditions, rmn.DRClusterConditionTypeFenced)
		if drClusterFencedCondition == nil {
			d.log.Info("drCluster fenced condition not available", "cluster", drClusters[i].Name)

			return false, nil
		}

		if drClusterFencedCondition.Status != metav1.ConditionTrue ||
			drClusterFencedCondition.ObservedGeneration != drClusters[i].Generation {
			d.log.Info("drCluster fenced condition is not true", "cluster", drClusters[i].Name)

			return false, nil
		}

		return true, nil
	}

	return false, fmt.Errorf("failed to get the fencing status for the cluster %s", cluster)
}

func (d *DRPCInstance) switchToFailoverCluster() (bool, error) {
	const done = true
	// Make sure we record the state that we are failing over
	d.setDRState(rmn.FailingOver)
	d.setMetricsTimerFromDRState(rmn.FailingOver)
	d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
		d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), "Starting failover")
	d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionPeerReady, d.instance.Generation,
		metav1.ConditionFalse, rmn.ReasonNotStarted,
		fmt.Sprintf("Started failover to cluster %q", d.instance.Spec.FailoverCluster))

	// Save the current home cluster
	curHomeCluster := d.getCurrentHomeClusterName()

	if curHomeCluster == "" {
		msg := "Invalid Failover request. Current home cluster does not exists"
		d.log.Info(msg)
		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), msg)

		err := fmt.Errorf("failover requested on invalid state %v", d.instance.Status)
		rmnutil.ReportIfNotPresent(d.reconciler.eventRecorder, d.instance, corev1.EventTypeWarning,
			rmnutil.EventReasonSwitchFailed, err.Error())

		return done, err
	}

	if isMetroAction(d.drPolicy, d.drClusters, curHomeCluster, d.instance.Spec.FailoverCluster) {
		fenced, err := d.checkClusterFenced(curHomeCluster, d.drClusters)
		if err != nil {
			return !done, err
		}

		if !fenced {
			return done, fmt.Errorf("current home cluster %s is not fenced", curHomeCluster)
		}
	}

	newHomeCluster := d.instance.Spec.FailoverCluster

	const restorePVs = true

	err := d.executeRelocation(newHomeCluster, "", restorePVs)
	if err != nil {
		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), err.Error())
		rmnutil.ReportIfNotPresent(d.reconciler.eventRecorder, d.instance, corev1.EventTypeWarning,
			rmnutil.EventReasonSwitchFailed, err.Error())

		return !done, err
	}

	d.advanceToNextDRState()
	d.setMetricsTimerFromDRState(rmn.FailedOver)
	d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
		d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), "Completed")
	d.log.Info("Failover completed", "state", d.getLastDRState())

	// The failover is complete, but we still need to clean up the failed primary.
	// hence, returning a NOT done
	return !done, nil
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

// runRelocate checks if pre-conditions for relocation are met, and if so performs the relocation
// Pre-requisites for relocation are checked as follows:
//  - The exists at least one VRG across clusters (there is no state where we do not have a VRG as
//    primary or secondary once initial deployment is complete)
//  - Ensures that there is only one primary, before further state transitions
//    - If there are multiple primaries, wait for one of the primaries to transition
//      to a secondary. This can happen if MCV reports older VRG state as MW is being applied
//      to the cluster.
//  - Check if peers are ready
//    - If there are secondaries in flight, ensure they report secondary as the observed state
//      before moving forward
//    - preferredCluster should not report as Secondary, as it will never transition out of delete state
//      in the future, as there would be no primary. This can happen, if in between relocate the
//      preferred cluster was switched
//      - User needs to recover by changing the preferredCluster back to the initial intent
//  - Check if we already relocated to the preferredCluster, and ensure cleanup actions
//  - Check if current primary (that is not the preferred cluster), is ready to switch over
//  - Relocate!
func (d *DRPCInstance) RunRelocate() (bool, error) {
	d.log.Info("Entering RunRelocate", "state", d.getLastDRState())

	const done = true

	preferredCluster := d.instance.Spec.PreferredCluster
	preferredClusterNamespace := preferredCluster

	homeCluster, err := d.validateRelocation(preferredCluster)
	if err != nil {
		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), err.Error())

		return !done, err
	}

	// We are done if already relocated; if there were secondaries they are cleaned up above
	if d.hasAlreadySwitchedOver(preferredCluster) {
		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			metav1.ConditionTrue, string(d.instance.Status.Phase), "Completed")

		err := d.EnsureCleanup(preferredCluster)
		if err != nil {
			return !done, err
		}

		return done, nil
	}

	// Check if current primary (that is not the preferred cluster), is ready to switch over
	if homeCluster != "" && homeCluster != preferredCluster && !d.readyToSwitchOver(homeCluster, preferredCluster) {
		errMsg := fmt.Sprintf("current cluster (%s) has not completed protection actions", homeCluster)
		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), errMsg)

		return !done, fmt.Errorf(errMsg)
	}

	return d.relocate(preferredCluster, preferredClusterNamespace, rmn.Relocating)
}

func clusterListContains(clNames []string, cName string) bool {
	for _, clName := range clNames {
		if clName == cName {
			return true
		}
	}

	return false
}

func (d *DRPCInstance) areMultipleVRGsPrimary() bool {
	numOfPrimaries := 0

	for _, vrg := range d.vrgs {
		if d.isVRGPrimary(vrg) {
			numOfPrimaries++
		}
	}

	return numOfPrimaries > 1
}

func (d *DRPCInstance) validatePeerReady() bool {
	condition := findCondition(d.instance.Status.Conditions, rmn.ConditionPeerReady)
	d.log.Info(fmt.Sprintf("Condition %v", condition))

	if condition == nil || condition.Status == metav1.ConditionTrue {
		return true
	}

	return false
}

func (d *DRPCInstance) arePeersReady() (bool, string, []string) {
	var peerClusters []string

	homeCluster := ""

	for cn, vrg := range d.vrgs {
		if d.isVRGPrimary(vrg) && homeCluster == "" {
			homeCluster = cn
		}

		if d.isVRGSecondary(vrg) {
			peerClusters = append(peerClusters, cn)
		}
	}

	return len(peerClusters) == 0, homeCluster, peerClusters
}

func (d *DRPCInstance) validateRelocation(preferredCluster string) (string, error) {
	// Relocation requires preferredCluster to be configured
	if preferredCluster == "" {
		return "", fmt.Errorf("preferred cluster not valid")
	}

	// No VRGs found, invalid state, possibly deployment was not started
	if len(d.vrgs) == 0 {
		return "", fmt.Errorf("no VRGs exists. Can't relocate")
	}

	// Check for at most a single cluster in primary state
	if d.areMultipleVRGsPrimary() {
		return "", fmt.Errorf("multiple primaries in transition detected")
	}
	// Pre-relocate cleanup
	ready, homeCluster, secondaries := d.arePeersReady()
	if !ready {
		if homeCluster == "" {
			// Ensure preferred cluster is not reporting as secondary
			if clusterListContains(secondaries, preferredCluster) {
				return "", fmt.Errorf("cannot cleanup secondaries, as no valid primary found")
			}
		}

		// Ensure secondaries have transitioned to the required state
		if !d.ensureVRGIsSecondaryEverywhere(homeCluster) {
			return "", fmt.Errorf("waiting for VRGs to move to secondaries everywhere")
		}
	}

	return homeCluster, nil
}

// readyToSwitchOver checks whether the PV data is ready and the cluster data has been protected.
// ClusterDataProtected condition indicates whether all PV related cluster data for an App (Managed
// by this DRPC instance) has been protected (uploaded to the S3 store(s)) or not.
func (d *DRPCInstance) readyToSwitchOver(homeCluster string, preferredCluster string) bool {
	d.log.Info("Checking whether VRG is available", "cluster", homeCluster)

	if isMetroAction(d.drPolicy, d.drClusters, homeCluster, preferredCluster) {
		// check fencing status in the preferredCluster
		fenced, err := d.checkClusterFenced(preferredCluster, d.drClusters)
		if err != nil {
			return false
		}

		if fenced {
			return false
		}
	}
	// Allow switch over when PV data is ready and the cluster data is protected
	return d.isVRGConditionDataReady(homeCluster) && d.isVRGConditionClusterDataReady(homeCluster)
}

func (d *DRPCInstance) isVRGConditionDataReady(homeCluster string) bool {
	const ready = true

	d.log.Info("Checking whether VRG DataReay is true", "cluster", homeCluster)

	vrg := d.vrgs[homeCluster]

	if vrg == nil {
		d.log.Info("VRG not available on cluster", "cluster", homeCluster)

		return !ready
	}

	dataReadyCondition := findCondition(vrg.Status.Conditions, VRGConditionTypeDataReady)
	if dataReadyCondition == nil {
		d.log.Info("VRG DataReady condition not available", "cluster", homeCluster)

		return !ready
	}

	return dataReadyCondition.Status == metav1.ConditionTrue &&
		dataReadyCondition.ObservedGeneration == vrg.Generation
}

func (d *DRPCInstance) isVRGConditionClusterDataReady(homeCluster string) bool {
	const ready = true

	d.log.Info("Checking whether VRG ClusterDataReay is true", "cluster", homeCluster)

	vrg := d.vrgs[homeCluster]

	if vrg == nil {
		d.log.Info("VRG not available on cluster", "cluster", homeCluster)

		return !ready
	}

	clusterDataProtectedCondition := findCondition(vrg.Status.Conditions, VRGConditionTypeClusterDataProtected)
	if clusterDataProtectedCondition == nil {
		d.log.Info("VRG ClusterData condition not available", "cluster", homeCluster)

		return !ready
	}

	return clusterDataProtectedCondition.Status == metav1.ConditionTrue &&
		clusterDataProtectedCondition.ObservedGeneration == vrg.Generation
}

func (d *DRPCInstance) relocate(preferredCluster, preferredClusterNamespace string, drState rmn.DRState) (bool, error) {
	const done = true

	if !d.validatePeerReady() {
		return !done, fmt.Errorf("clean up on secondaries pending (%+v)", d.instance)
	}

	// Make sure we record the state that we are failing over
	d.setDRState(drState)
	d.setMetricsTimerFromDRState(drState)
	d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
		d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), "Starting relocation")

	// Setting up relocation ensures that all VRGs in all managed cluster are secondaries
	err := d.setupRelocation(preferredCluster)
	if err != nil {
		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), err.Error())

		return !done, err
	}

	const restorePVs = true

	err = d.executeRelocation(preferredCluster, preferredClusterNamespace, restorePVs)
	if err != nil {
		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), err.Error())

		return !done, err
	}

	// All good so far, update DRPC decision and state
	if len(d.userPlacementRule.Status.Decisions) > 0 {
		d.instance.Status.PreferredDecision = d.userPlacementRule.Status.Decisions[0]
	}

	d.advanceToNextDRState()
	d.setMetricsTimerFromDRState(d.getLastDRState())
	d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
		d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), "Completed")

	d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionPeerReady, d.instance.Generation,
		metav1.ConditionFalse, rmn.ReasonNotStarted,
		fmt.Sprintf("Started relocation to cluster %q", preferredCluster))

	d.log.Info("Relocation completed", "State", d.getLastDRState())

	// The relocation is complete, but we still need to clean up the previous
	// primary, hence, returning a NOT done
	return !done, nil
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

	if !d.ensureDataProtected(clusterToSkip) {
		return fmt.Errorf("waiting for complete data resync to happen to target cluster")
	}

	return nil
}

// executeRelocation is a series of steps to creating, updating, and cleaning up
// the necessary objects for the failover or relocation
func (d *DRPCInstance) executeRelocation(targetCluster, targetClusterNamespace string, restorePVs bool) error {
	d.log.Info("executeRelocation", "cluster", targetCluster, "restorePVs", restorePVs)

	createdOrUpdated, err := d.createVRGManifestWorkAsPrimary(targetCluster)
	if err != nil {
		return err
	}

	if createdOrUpdated && restorePVs {
		// We just created MWs. Give it time until the PV restore is complete
		return fmt.Errorf("%w)", WaitForPVRestoreToComplete)
	}

	// already a primary
	if restorePVs {
		restored, err := d.checkPVsHaveBeenRestored(targetCluster)
		if err != nil {
			return err
		}

		d.log.Info("Checked whether PVs have been restored", "Yes?", restored)

		if !restored {
			return fmt.Errorf("%w)", WaitForPVRestoreToComplete)
		}
	}

	err = d.updateUserPlacementRule(targetCluster, targetClusterNamespace)
	if err != nil {
		return err
	}

	return nil
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
		d.log.Info(fmt.Sprintf("Already %q to cluster %s", d.getLastDRState(), targetCluster))

		return true
	}

	return false
}

func (d *DRPCInstance) moveVRGToSecondaryEverywhere() bool {
	d.log.Info("Move VRG to secondary everywhere")

	failedCount := 0

	for _, clusterName := range rmnutil.DrpolicyClusterNames(d.drPolicy) {
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
	for _, clusterName := range rmnutil.DrpolicyClusterNames(d.drPolicy) {
		if skipCluster == clusterName {
			continue
		}

		// If VRG hasn't been deleted, then make sure that the MW for it is deleted and
		// return and wait
		mwDeleted, err := d.ensureVRGManifestWorkOnClusterDeleted(clusterName)
		if err != nil {
			return false, err
		}

		if !mwDeleted {
			return false, nil
		}

		d.log.Info("MW has been deleted. Check the VRG")

		if !d.ensureVRGDeleted(clusterName) {
			d.log.Info("VRG has not been deleted yet", "cluster", clusterName)

			return false, nil
		}

		mcvNameVRG := BuildManagedClusterViewName(d.instance.Name, d.instance.Namespace, rmnutil.MWTypeVRG)
		// MW is deleted, VRG is deleted, so we no longer need MCV for the VRG
		err = d.reconciler.deleteManagedClusterView(clusterName, mcvNameVRG)
		if err != nil {
			return false, err
		}

		mcvNameNS := BuildManagedClusterViewName(d.instance.Name, d.instance.Namespace, rmnutil.MWTypeNS)
		// MCV for Namespace is no longer needed
		err = d.reconciler.deleteManagedClusterView(clusterName, mcvNameNS)
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

	newStatus := plrv1.PlacementRuleStatus{
		Decisions: newPD,
	}

	return d.reconciler.updateUserPlacementRuleStatus(d.userPlacementRule, newStatus)
}

func (d *DRPCInstance) clearUserPlacementRuleStatus() error {
	d.log.Info("Updating userPlacementRule", "name", d.userPlacementRule.Name)

	newStatus := plrv1.PlacementRuleStatus{}

	return d.reconciler.updateUserPlacementRuleStatus(d.userPlacementRule, newStatus)
}

func (d *DRPCInstance) createVRGManifestWork(homeCluster string) error {
	// TODO: check if VRG MW here as a less expensive way to validate if Namespace exists
	err := d.ensureNamespaceExistsOnManagedCluster(homeCluster)
	if err != nil {
		return fmt.Errorf("createVRGManifestWork couldn't ensure namespace '%s' on cluster %s exists",
			d.instance.Namespace, homeCluster)
	}

	// create VRG ManifestWork
	d.log.Info("Creating VRG ManifestWork",
		"Last State:", d.getLastDRState(), "cluster", homeCluster)

	vrg := d.generateVRG()

	if err := d.mwu.CreateOrUpdateVRGManifestWork(
		d.instance.Name, d.instance.Namespace,
		homeCluster, vrg); err != nil {
		d.log.Error(err, "failed to create or update VolumeReplicationGroup manifest")

		return fmt.Errorf("failed to create or update VolumeReplicationGroup manifest in namespace %s (%w)", homeCluster, err)
	}

	return nil
}

func (d *DRPCInstance) generateVRG() rmn.VolumeReplicationGroup {
	vrg := rmn.VolumeReplicationGroup{
		TypeMeta:   metav1.TypeMeta{Kind: "VolumeReplicationGroup", APIVersion: "ramendr.openshift.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: d.instance.Name, Namespace: d.instance.Namespace},
		Spec: rmn.VolumeReplicationGroupSpec{
			PVCSelector:      d.instance.Spec.PVCSelector,
			ReplicationState: rmn.Primary,
			S3Profiles:       rmnutil.DRPolicyS3Profiles(d.drPolicy, d.drClusters).List(),
		},
	}

	vrg.Spec.Async = d.generateVRGSpecAsync()
	vrg.Spec.Sync = d.generateVRGSpecSync()

	return vrg
}

func (d *DRPCInstance) generateVRGSpecAsync() rmn.VRGAsyncSpec {
	if dRPolicySupportsRegional(d.drPolicy, d.drClusters) {
		return rmn.VRGAsyncSpec{
			ReplicationClassSelector: d.drPolicy.Spec.ReplicationClassSelector,
			SchedulingInterval:       d.drPolicy.Spec.SchedulingInterval,
			Mode:                     rmn.AsyncModeEnabled,
		}
	}

	return rmn.VRGAsyncSpec{
		SchedulingInterval: "365d", // this is mandatory, spoof it!
		Mode:               rmn.AsyncModeDisabled,
	}
}

func (d *DRPCInstance) generateVRGSpecSync() rmn.VRGSyncSpec {
	if supports, _ := dRPolicySupportsMetro(d.drPolicy, d.drClusters); supports {
		return rmn.VRGSyncSpec{
			Mode: rmn.SyncModeEnabled,
		}
	}

	return rmn.VRGSyncSpec{
		Mode: rmn.SyncModeDisabled,
	}
}

func dRPolicySupportsRegional(drpolicy *rmn.DRPolicy, drClusters []rmn.DRCluster) bool {
	return rmnutil.DrpolicyRegionNamesAsASet(drpolicy, drClusters).Len() > 1
}

func dRPolicySupportsMetro(drpolicy *rmn.DRPolicy, drclusters []rmn.DRCluster) (
	supportsMetro bool,
	metroMap map[rmn.Region][]string) {
	allRegionsMap := make(map[rmn.Region][]string)
	metroMap = make(map[rmn.Region][]string)

	for _, managedCluster := range rmnutil.DrpolicyClusterNames(drpolicy) {
		for _, v := range drclusters {
			if v.Name == managedCluster {
				allRegionsMap[v.Spec.Region] = append(
					allRegionsMap[v.Spec.Region],
					managedCluster)
			}
		}
	}

	for k, v := range allRegionsMap {
		if len(v) > 1 {
			supportsMetro = true
			metroMap[k] = v
		}
	}

	return supportsMetro, metroMap
}

func isMetroAction(drpolicy *rmn.DRPolicy, drClusters []rmn.DRCluster, from string, to string) bool {
	var regionFrom, regionTo rmn.Region

	for _, managedCluster := range rmnutil.DrpolicyClusterNames(drpolicy) {
		if managedCluster == from {
			regionFrom = drClusterRegion(drClusters, managedCluster)
		}

		if managedCluster == to {
			regionTo = drClusterRegion(drClusters, managedCluster)
		}
	}

	return regionFrom == regionTo
}

func drClusterRegion(drClusters []rmn.DRCluster, cluster string) (region rmn.Region) {
	for _, drCluster := range drClusters {
		if drCluster.Name != cluster {
			continue
		}

		region = drCluster.Spec.Region

		return
	}

	return
}

func (d *DRPCInstance) ensureNamespaceExistsOnManagedCluster(homeCluster string) error {
	// verify namespace exists on target cluster
	namespaceExists, err := d.namespaceExistsOnManagedCluster(homeCluster)

	d.log.Info(fmt.Sprintf("createVRGManifestWork: namespace '%s' exists on cluster %s: %t",
		d.instance.Namespace, homeCluster, namespaceExists))

	if !namespaceExists { // attempt to create it
		err := d.mwu.CreateOrUpdateNamespaceManifest(d.instance.Name, d.instance.Namespace, homeCluster)
		if err != nil {
			return fmt.Errorf("failed to create namespace '%s' on cluster %s: %w", d.instance.Namespace, homeCluster, err)
		}

		d.log.Info(fmt.Sprintf("Created Namespace '%s' on cluster %s", d.instance.Namespace, homeCluster))

		return nil // created namespace
	}

	// namespace exists already
	if err != nil {
		return fmt.Errorf("failed to verify if namespace '%s' on cluster %s exists: %w",
			d.instance.Namespace, homeCluster, err)
	}

	return nil
}

func (d *DRPCInstance) isVRGPrimary(vrg *rmn.VolumeReplicationGroup) bool {
	return (vrg.Spec.ReplicationState == rmn.Primary)
}

func (d *DRPCInstance) isVRGSecondary(vrg *rmn.VolumeReplicationGroup) bool {
	return (vrg.Spec.ReplicationState == rmn.Secondary)
}

func (d *DRPCInstance) checkPVsHaveBeenRestored(homeCluster string) (bool, error) {
	d.log.Info("Checking whether PVs have been restored", "cluster", homeCluster)

	vrg, err := d.reconciler.MCVGetter.GetVRGFromManagedCluster(d.instance.Name,
		d.instance.Namespace, homeCluster)
	if err != nil {
		return false, fmt.Errorf("failed to VRG using MCV (error: %w)", err)
	}

	// ClusterDataReady condition tells us whether the PVs have been applied on the
	// target cluster or not
	clusterDataReady := findCondition(vrg.Status.Conditions, VRGConditionTypeClusterDataReady)
	if clusterDataReady == nil {
		d.log.Info("Waiting for PVs to be restored", "cluster", homeCluster)

		return false, nil
	}

	return clusterDataReady.Status == metav1.ConditionTrue && clusterDataReady.ObservedGeneration == vrg.Generation, nil
}

func (d *DRPCInstance) EnsureCleanup(clusterToSkip string) error {
	d.log.Info("ensuring cleanup on secondaries")

	condition := findCondition(d.instance.Status.Conditions, rmn.ConditionPeerReady)

	if condition == nil {
		msg := "Starting cleanup check"
		d.log.Info(msg)
		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionPeerReady, d.instance.Generation,
			metav1.ConditionFalse, rmn.ReasonProgressing, msg)

		condition = findCondition(d.instance.Status.Conditions, rmn.ConditionPeerReady)
	}

	d.log.Info(fmt.Sprintf("Condition %v", condition))

	if condition.Reason == rmn.ReasonSuccess &&
		condition.Status == metav1.ConditionTrue &&
		condition.ObservedGeneration == d.instance.Generation {
		d.log.Info("Condition values tallied, cleanup is considered complete")

		return nil
	}

	clean, err := d.cleanupSecondaries(clusterToSkip)
	if err != nil {
		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionPeerReady, d.instance.Generation,
			metav1.ConditionFalse, rmn.ReasonCleaning, err.Error())

		return err
	}

	if !clean {
		msg := "cleaning secondaries"
		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionPeerReady, d.instance.Generation,
			metav1.ConditionFalse, rmn.ReasonCleaning, msg)

		return fmt.Errorf("waiting to clean secondaries")
	}

	d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionPeerReady, d.instance.Generation,
		metav1.ConditionTrue, rmn.ReasonSuccess, "Cleaned")

	return nil
}

func (d *DRPCInstance) namespaceExistsOnManagedCluster(cluster string) (bool, error) {
	exists := true

	// create ManagedClusterView to check if namespace exists
	_, err := d.reconciler.MCVGetter.GetNamespaceFromManagedCluster(d.instance.Name, cluster, d.instance.Namespace)
	if err != nil {
		if errors.IsNotFound(err) { // successfully detected that Namespace is not found by ManagedClusterView
			d.log.Info(fmt.Sprintf("Namespace '%s' not found on cluster %s", d.instance.Namespace, cluster))

			return !exists, nil
		}

		d.log.Error(err, "failed get Namespace from ManagedCluster")

		return !exists, errorswrapper.Wrap(err, "failed to get Namespace from managedcluster")
	}

	return exists, nil // namespace exists and looks good to use
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
	//	d.log.Info(fmt.Sprintf("ManifestWork %s/%s NOT in Applied state", mw.Namespace, mw.Name))
	//	// Wait for MW to be applied. The DRPC reconciliation will be called then
	//	return done, nil
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
	for _, clusterName := range rmnutil.DrpolicyClusterNames(d.drPolicy) {
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

// ensureVRGIsSecondaryOnCluster returns true when VRG is secondary or it does not exists on the cluster
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
		d.log.Info(fmt.Sprintf("vrg status replication state for cluster %s is %+v",
			clusterName, vrg))

		return false
	}

	return true
}

// Check for DataProtected condition to be true everywhere except the
// preferredCluster where the app is being relocated to.
// This is because, preferredCluster wont have a VRG in a secondary state when
// relocate is started at first. preferredCluster will get VRG as primary when DRPC is
// about to move the workload to the preferredCluser. And before doing that, DataProtected
// has to be ensured. This can only be done at the other cluster which has been moved to
// secondary by now.
func (d *DRPCInstance) ensureDataProtected(targetCluster string) bool {
	for _, clusterName := range rmnutil.DrpolicyClusterNames(d.drPolicy) {
		if targetCluster == clusterName {
			continue
		}

		if !d.ensureDataProtectedOnCluster(clusterName) {
			d.log.Info("Still waiting for data sync to complete", "cluster", clusterName)

			return false
		}
	}

	return true
}

func (d *DRPCInstance) ensureDataProtectedOnCluster(clusterName string) bool {
	// this check is done only for relocation. Since this function can be called during
	// failover as well, trying to ensure that data is completely synced in the new
	// cluster where the app is going to be placed might not be successful. Only for
	// relocate this check is made.
	d.log.Info(fmt.Sprintf("Ensure VRG %s as secondary has the data protected on  %s",
		d.instance.Name, clusterName))

	d.mcvRequestInProgress = false

	vrg, err := d.reconciler.MCVGetter.GetVRGFromManagedCluster(d.instance.Name,
		d.instance.Namespace, clusterName)
	if err != nil {
		if errors.IsNotFound(err) {
			// expectation is that VRG should be present. Otherwise, this function
			// would not have been called. Return false
			d.log.Info("VRG not found", "errorValue", err)

			return false
		}

		d.log.Info("Failed to get VRG", "errorValue", err)

		d.mcvRequestInProgress = true

		return false
	}

	dataProtectedCondition := findCondition(vrg.Status.Conditions, VRGConditionTypeDataProtected)
	if dataProtectedCondition == nil {
		d.log.Info(fmt.Sprintf("VRG DataProtected condition not available for cluster %s (%v)",
			clusterName, vrg))

		return false
	}

	if dataProtectedCondition.Status != metav1.ConditionTrue {
		d.log.Info(fmt.Sprintf("VRG data protection is not complete for cluster %s for %v",
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
		// Only NotFound error is accepted
		if errors.IsNotFound(err) {
			return true // ensured
		}

		d.log.Info("Failed to get VRG", "error", err)

		d.mcvRequestInProgress = true
		// Retry again
		return false
	}

	d.log.Info(fmt.Sprintf("VRG not deleted(%v)", vrg))

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
		d.reportEvent(nextState)
		d.needStatusUpdate = true
	}
}

func (d *DRPCInstance) shouldUpdateStatus() bool {
	if d.needStatusUpdate {
		return true
	}

	for _, condition := range d.instance.Status.Conditions {
		if condition.ObservedGeneration != d.instance.Generation {
			return true
		}
	}

	return false
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

func (d *DRPCInstance) getConditionStatusForTypeAvailable() metav1.ConditionStatus {
	if d.isInFinalPhase() {
		return metav1.ConditionTrue
	}

	if d.isInProgressingPhase() {
		return metav1.ConditionFalse
	}

	return metav1.ConditionUnknown
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

// prometheus metrics
type timerState string

const (
	timerStart timerState = "start"
	timerStop  timerState = "stop"
)

type timerWrapper struct {
	gauge     prometheus.GaugeVec  // used for "last only" fine-grained timer
	histogram prometheus.Histogram // used for cumulative data
}

type timerInstance struct {
	timer          prometheus.Timer // use prometheus.NewTimer to use/reuse this timer across reconciles
	reconcileState rmn.DRState      // used to track for spurious reconcile avoidance
}

// set default values for guageWrapper
func newTimerWrapper(gauge *prometheus.GaugeVec, histogram prometheus.Histogram) timerWrapper {
	wrapper := timerWrapper{}

	wrapper.gauge = *gauge
	wrapper.histogram = histogram

	return wrapper
}

var (
	failoverTime = newTimerWrapper(
		prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "ramen_failover_time",
				Help: "Duration of the last failover event for individual DRPCs",
			},
			[]string{
				"time",
			},
		),
		prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "ramen_failover_histogram",
			Help:    "Histogram of all failover timers (seconds) across all DRPCs",
			Buckets: prometheus.ExponentialBuckets(1.0, 2.0, 12), // start=1.0, factor=2.0, buckets=12
		}),
	)

	relocateTime = newTimerWrapper(
		prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "ramen_relocate_time",
				Help: "Duration of the last relocate time for individual DRPCs",
			},
			[]string{
				"time",
			},
		),
		prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "ramen_relocate_histogram",
			Help:    "Histogram of all relocate timers (seconds) across all DRPCs",
			Buckets: prometheus.ExponentialBuckets(1.0, 2.0, 12), // start=1.0, factor=2.0, buckets=12
		}),
	)

	deployTime = newTimerWrapper(
		prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "ramen_initial_deploy_time",
				Help: "Duration of the last initial deploy time",
			},
			[]string{
				"time",
			},
		),
		prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "ramen_initial_deploy_histogram",
			Help:    "Histogram of all initial deploymet timers (seconds)",
			Buckets: prometheus.ExponentialBuckets(1.0, 2.0, 12), // start=1.0, factor=2.0, buckets=12
		}),
	)
)

func init() {
	// register custom metrics with the global Prometheus registry
	metrics.Registry.MustRegister(failoverTime.gauge, failoverTime.histogram)
	metrics.Registry.MustRegister(relocateTime.gauge, relocateTime.histogram)
	metrics.Registry.MustRegister(deployTime.gauge, deployTime.histogram)
}

func (d *DRPCInstance) setMetricsTimerFromDRState(stateDR rmn.DRState) {
	switch stateDR {
	case rmn.FailingOver:
		d.setMetricsTimer(&failoverTime, timerStart, stateDR)
	case rmn.FailedOver:
		d.setMetricsTimer(&failoverTime, timerStop, stateDR)
	case rmn.Relocating:
		d.setMetricsTimer(&relocateTime, timerStart, stateDR)
	case rmn.Relocated:
		d.setMetricsTimer(&relocateTime, timerStop, stateDR)
	case rmn.Deploying:
		d.setMetricsTimer(&deployTime, timerStart, stateDR)
	case rmn.Deployed:
		d.setMetricsTimer(&deployTime, timerStop, stateDR)
	default:
		// not supported
	}
}

func (d *DRPCInstance) setMetricsTimer(
	wrapper *timerWrapper, desiredTimerState timerState, reconcileState rmn.DRState) {
	switch desiredTimerState {
	case timerStart:
		if reconcileState != d.metricsTimer.reconcileState {
			d.metricsTimer.timer.ObserveDuration() // stop gauge timer in case one is still running

			d.metricsTimer.reconcileState = reconcileState
			d.metricsTimer.timer = *prometheus.NewTimer(
				prometheus.ObserverFunc(wrapper.gauge.WithLabelValues(d.instance.Name).Set))
		}
	case timerStop:
		d.metricsTimer.timer.ObserveDuration()                                      // stop gauge timer
		wrapper.histogram.Observe(d.metricsTimer.timer.ObserveDuration().Seconds()) // add timer to histogram
		d.metricsTimer.reconcileState = reconcileState
	}
}

func (d *DRPCInstance) setDRPCCondition(conditions *[]metav1.Condition, condType string,
	observedGeneration int64, status metav1.ConditionStatus, reason, msg string) {
	needStatusUpdate := SetDRPCStatusCondition(conditions, condType, observedGeneration, status, reason, msg)
	if !d.needStatusUpdate {
		d.needStatusUpdate = needStatusUpdate
	}
}
