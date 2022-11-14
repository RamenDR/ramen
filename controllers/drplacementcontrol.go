// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

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

const (
	// Annotations for MW and PlacementRule
	DRPCNameAnnotation      = "drplacementcontrol.ramendr.openshift.io/drpc-name"
	DRPCNamespaceAnnotation = "drplacementcontrol.ramendr.openshift.io/drpc-namespace"
)

var (
	WaitForPVRestoreToComplete         error = errorswrapper.New("Waiting for PV restore to complete...")
	WaitForVolSyncDestRepToComplete    error = errorswrapper.New("Waiting for VolSync RD to complete...")
	WaitForSourceCluster               error = errorswrapper.New("Waiting for primary to provide Protected PVCs...")
	WaitForVolSyncManifestWorkCreation error = errorswrapper.New("Waiting for VolSync ManifestWork to be created...")
	WaitForVolSyncRDInfoAvailibility   error = errorswrapper.New("Waiting for VolSync RDInfo...")
)

type DRPCInstance struct {
	reconciler           *DRPlacementControlReconciler
	ctx                  context.Context
	log                  logr.Logger
	instance             *rmn.DRPlacementControl
	savedInstanceStatus  rmn.DRPlacementControlStatus
	drPolicy             *rmn.DRPolicy
	drClusters           []rmn.DRCluster
	mcvRequestInProgress bool
	volSyncDisabled      bool
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
		if err := d.reconciler.updateDRPCStatus(d.instance, d.userPlacementRule, d.log); err != nil {
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

//nolint:funlen
func (d *DRPCInstance) RunInitialDeployment() (bool, error) {
	d.log.Info("Running initial deployment")

	const done = true

	homeCluster, homeClusterNamespace := d.getHomeClusterForInitialDeploy()

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
		// IF deployed on cluster that is not the preferred HomeCluster, then we are done
		return done, nil
	}

	// Ensure that initial deployment is complete
	if !deployed || !d.isUserPlRuleUpdated(homeCluster) {
		d.setStatusInitiating()

		_, err := d.startDeploying(homeCluster, homeClusterNamespace)
		if err != nil {
			d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
				d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), err.Error())

			return !done, err
		}

		d.setConditionOnInitialDeploymentCompletion()

		return !done, nil
	}

	// If we get here, the deployment is successful
	err := d.EnsureVolSyncReplicationSetup(homeCluster)
	if err != nil {
		return !done, err
	}

	// If for whatever reason, the DRPC status is missing (i.e. DRPC could have been deleted mistakingly and
	// recreated again), we should update it with whatever status we are at.
	if d.getLastDRState() == rmn.DRState("") {
		// Update our 'well known' preferred placement
		d.updatePreferredDecision()
		d.setDRState(rmn.Deployed)
	}

	d.setConditionOnInitialDeploymentCompletion()

	d.setProgression(rmn.ProgressionCompleted)

	d.setActionDuration()

	return done, nil
}

func (d *DRPCInstance) getHomeClusterForInitialDeploy() (string, string) {
	// Check if the user wants to use the preferredCluster
	homeCluster := ""
	homeClusterNamespace := ""

	if d.instance.Spec.PreferredCluster != "" {
		homeCluster = d.instance.Spec.PreferredCluster
		homeClusterNamespace = d.instance.Spec.PreferredCluster
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

		// Update our 'well known' preferred placement
		d.updatePreferredDecision()

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
	d.setProgression(rmn.ProgressionCreatingMW)
	// Create VRG first, to leverage user PlacementRule decision to skip placement and move to cleanup
	err := d.createVRGManifestWork(homeCluster)
	if err != nil {
		return false, err
	}

	// We have a home cluster
	d.setProgression(rmn.ProgressionUpdatingPlRule)

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

	d.setDRState(rmn.Deployed)
	d.setMetricsTimerFromDRState(rmn.Deployed)

	return done, nil
}

// RunFailover:
// 1. If failoverCluster empty, then fail it and we are done
// 2. If already failed over, then ensure clean up and we are done
// 3. Set VRG for the preferredCluster to secondary
// 5. Update UserPlacementRule decision to failoverCluster
// 6. Create VRG for the failoverCluster as Primary
// 7. Update DRPC status
// 8. Delete VRG MW from preferredCluster once the VRG state has changed to Secondary
func (d *DRPCInstance) RunFailover() (bool, error) {
	d.log.Info("Entering RunFailover", "state", d.getLastDRState())

	if d.isPlacementNeedsFixing() {
		err := d.fixupPlacementForFailover()
		if err != nil {
			d.log.Info("Couldn't fix up placement for Failover")
		}
	}

	const done = true

	// We are done if empty
	if d.instance.Spec.FailoverCluster == "" {
		msg := "failover cluster not set. FailoverCluster is a mandatory field"
		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), msg)

		return done, fmt.Errorf(msg)
	}

	// IFF VRG exists and it is primary in the failoverCluster, the clean up and setup VolSync if needed.
	if d.vrgExistsAndPrimary(d.instance.Spec.FailoverCluster) {
		d.setDRState(rmn.FailedOver)
		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			metav1.ConditionTrue, string(d.instance.Status.Phase), "Completed")

		// Make sure VolRep 'Data' and VolSync 'setup' conditions are ready
		ready := d.checkReadinessAfterFailover(d.instance.Spec.FailoverCluster)
		if !ready {
			d.log.Info("VRGCondition not ready to finish failover")
			d.setProgression(rmn.ProgressionWaitForReadiness)

			return !done, nil
		}

		d.setProgression(rmn.ProgressionCleaningUp)

		err := d.ensureCleanupAndVolSyncReplicationSetup(d.instance.Spec.FailoverCluster)
		if err != nil {
			return !done, err
		}

		d.setProgression(rmn.ProgressionCompleted)

		d.setActionDuration()

		return done, nil
	}

	d.setStatusInitiating()

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
	d.setProgression(rmn.ProgressionFailingOverToCluster)
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

	err := d.switchToCluster(newHomeCluster, "", restorePVs)
	if err != nil {
		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), err.Error())
		rmnutil.ReportIfNotPresent(d.reconciler.eventRecorder, d.instance, corev1.EventTypeWarning,
			rmnutil.EventReasonSwitchFailed, err.Error())

		return !done, err
	}

	d.setDRState(rmn.FailedOver)
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
//   - The exists at least one VRG across clusters (there is no state where we do not have a VRG as
//     primary or secondary once initial deployment is complete)
//   - Ensures that there is only one primary, before further state transitions
//   - If there are multiple primaries, wait for one of the primaries to transition
//     to a secondary. This can happen if MCV reports older VRG state as MW is being applied
//     to the cluster.
//   - Check if peers are ready
//   - If there are secondaries in flight, ensure they report secondary as the observed state
//     before moving forward
//   - preferredCluster should not report as Secondary, as it will never transition out of delete state
//     in the future, as there would be no primary. This can happen, if in between relocate the
//     preferred cluster was switched
//   - User needs to recover by changing the preferredCluster back to the initial intent
//   - Check if we already relocated to the preferredCluster, and ensure cleanup actions
//   - Check if current primary (that is not the preferred cluster), is ready to switch over
//   - Relocate!
//
//nolint:funlen,gocognit,cyclop
func (d *DRPCInstance) RunRelocate() (bool, error) {
	d.log.Info("Entering RunRelocate", "state", d.getLastDRState(), "progression", d.getProgression())

	if d.isPlacementNeedsFixing() {
		err := d.fixupPlacementForRelocate()
		if err != nil {
			d.log.Info("Couldn't fix up placement for Relocate")
		}
	}

	const done = true

	preferredCluster := d.instance.Spec.PreferredCluster
	preferredClusterNamespace := d.instance.Spec.PreferredCluster

	// Before relocating to the preferredCluster, do a quick validation and select the current preferred cluster.
	curHomeCluster, err := d.validateAndSelectCurrentPrimary(preferredCluster)
	if err != nil {
		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), err.Error())

		return !done, err
	}

	// We are done if already relocated; if there were secondaries they are cleaned up above
	if curHomeCluster != "" && d.vrgExistsAndPrimary(preferredCluster) {
		d.setDRState(rmn.Relocated)
		d.setProgression(rmn.ProgressionCleaningUp)
		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			metav1.ConditionTrue, string(d.instance.Status.Phase), "Completed")

		// Cleanup and setup VolSync if enabled
		err = d.ensureCleanupAndVolSyncReplicationSetup(preferredCluster)
		if err != nil {
			return !done, err
		}

		d.setProgression(rmn.ProgressionCompleted)

		d.setActionDuration()

		return done, nil
	}

	d.setStatusInitiating()

	// Check if current primary (that is not the preferred cluster), is ready to switch over
	if curHomeCluster != "" && curHomeCluster != preferredCluster &&
		!d.readyToSwitchOver(curHomeCluster, preferredCluster) {
		errMsg := fmt.Sprintf("current cluster (%s) has not completed protection actions", curHomeCluster)
		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), errMsg)

		return !done, fmt.Errorf(errMsg)
	}

	if !d.validatePeerReady() {
		return !done, fmt.Errorf("clean up on secondaries pending (%+v)", d.instance)
	}

	if curHomeCluster != "" && curHomeCluster != preferredCluster {
		result, err := d.quiesceAndRunFinalSync(curHomeCluster)
		if err != nil {
			return !done, err
		}

		if !result {
			return !done, nil
		}
	}

	return d.relocate(preferredCluster, preferredClusterNamespace, rmn.Relocating)
}

func (d *DRPCInstance) ensureCleanupAndVolSyncReplicationSetup(srcCluster string) error {
	// If we have VolSync replication, this is the perfect time to reset the RDSpec
	// on the primary. This will cause the RD to be cleared on the primary
	err := d.ResetVolSyncRDOnPrimary(srcCluster)
	if err != nil {
		return err
	}

	// Check if the reset has already been applied. ResetVolSyncRDOnPrimary resets the VRG
	// in the MW, but the VRGs in the vrgs slice are fetched using using MCV.
	vrg, ok := d.vrgs[srcCluster]
	if !ok || len(vrg.Spec.VolSync.RDSpec) != 0 {
		return fmt.Errorf(fmt.Sprintf("Waiting for RDSpec count on cluster %s to go to zero. VRG OK? %v",
			srcCluster, ok))
	}

	clusterToSkip := srcCluster

	err = d.EnsureCleanup(clusterToSkip)
	if err != nil {
		return err
	}

	// After we ensured peers are clean, The VolSync ReplicationSource (RS) will automatically get
	// created, but for the ReplicationDestination, we need to explicitly tell the VRG to create it.
	err = d.EnsureVolSyncReplicationSetup(srcCluster)
	if err != nil {
		return err
	}

	return nil
}

func (d *DRPCInstance) quiesceAndRunFinalSync(homeCluster string) (bool, error) {
	const done = true

	result, err := d.prepareForFinalSync(homeCluster)
	if err != nil {
		return !done, err
	}

	if !result {
		d.setProgression(rmn.ProgressionPreparingFinalSync)

		return !done, nil
	}

	if len(d.userPlacementRule.Status.Decisions) != 0 {
		d.setDRState(rmn.Relocating)
		d.setMetricsTimerFromDRState(rmn.Relocating)
		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), "Starting quiescing for relocation")

		// clear current user PlacementRule's decision
		d.setProgression(rmn.ProgressionClearingPlRule)

		err := d.clearUserPlacementRuleStatus()
		if err != nil {
			return !done, err
		}
	}

	// Ensure final sync has been taken
	result, err = d.runFinalSync(homeCluster)
	if err != nil {
		return !done, err
	}

	if !result {
		d.setProgression(rmn.ProgressionRunningFinalSync)

		return !done, nil
	}

	d.setProgression(rmn.ProgressionFinalSyncComplete)

	return done, nil
}

func (d *DRPCInstance) prepareForFinalSync(homeCluster string) (bool, error) {
	d.log.Info(fmt.Sprintf("Preparing final sync on cluster %s", homeCluster))

	const done = true

	vrg, ok := d.vrgs[homeCluster]

	if !ok {
		d.log.Info(fmt.Sprintf("prepareForFinalSync: VRG not available on cluster %s", homeCluster))

		return !done, fmt.Errorf("VRG not found on Cluster %s", homeCluster)
	}

	if !vrg.Status.PrepareForFinalSyncComplete {
		err := d.updateVRGToPrepareForFinalSync(homeCluster)
		if err != nil {
			return !done, err
		}

		// updated VRG to run final sync. Give it time...
		d.log.Info(fmt.Sprintf("Giving enough time to prepare for final sync on cluster %s", homeCluster))

		return !done, nil
	}

	d.log.Info("Preparing for final sync completed", "cluster", homeCluster)

	return done, nil
}

func (d *DRPCInstance) runFinalSync(homeCluster string) (bool, error) {
	d.log.Info(fmt.Sprintf("Running final sync on cluster %s", homeCluster))

	const done = true

	vrg, ok := d.vrgs[homeCluster]

	if !ok {
		d.log.Info(fmt.Sprintf("runFinalSync: VRG not available on cluster %s", homeCluster))

		return !done, fmt.Errorf("VRG not found on Cluster %s", homeCluster)
	}

	if !vrg.Status.FinalSyncComplete {
		err := d.updateVRGToRunFinalSync(homeCluster)
		if err != nil {
			return !done, err
		}

		// updated VRG to run final sync. Give it time...
		d.log.Info(fmt.Sprintf("Giving it enough time to run final sync on cluster %s", homeCluster))

		return !done, nil
	}

	d.log.Info("Running final sync completed", "cluster", homeCluster)

	return done, nil
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
	d.log.Info(fmt.Sprintf("validatePeerReady -- Condition %v", condition))

	if condition == nil || condition.Status == metav1.ConditionTrue {
		return true
	}

	return false
}

func (d *DRPCInstance) selectCurrentPrimaryAndSecondaries() (string, []string) {
	var secondaryVRGs []string

	primaryVRG := ""

	for cn, vrg := range d.vrgs {
		if d.isVRGPrimary(vrg) && primaryVRG == "" {
			primaryVRG = cn
		}

		if d.isVRGSecondary(vrg) {
			secondaryVRGs = append(secondaryVRGs, cn)
		}
	}

	return primaryVRG, secondaryVRGs
}

func (d *DRPCInstance) validateAndSelectCurrentPrimary(preferredCluster string) (string, error) {
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
	homeCluster, _ := d.selectCurrentPrimaryAndSecondaries()

	return homeCluster, nil
}

// readyToSwitchOver checks whether the PV data is protected and the cluster data has been protected.
// ClusterDataProtected condition indicates whether all PV related cluster data for an App (Managed
// by this DRPC instance) has been protected (uploaded to the S3 store(s)) or not.
func (d *DRPCInstance) readyToSwitchOver(homeCluster string, preferredCluster string) bool {
	d.log.Info(fmt.Sprintf("Checking if VRG Data is available on cluster %s", homeCluster))

	if isMetroAction(d.drPolicy, d.drClusters, homeCluster, preferredCluster) {
		// check fencing status in the preferredCluster
		fenced, err := d.checkClusterFenced(preferredCluster, d.drClusters)
		if err != nil {
			d.log.Info(fmt.Sprintf("Checking if Cluster %s is Fenced failed %v",
				preferredCluster, err.Error()))

			return false
		}

		if fenced {
			d.log.Info(fmt.Sprintf("Cluster %s is Fenced", preferredCluster))

			return false
		}
	}
	// Allow switch over when PV data is protected and the cluster data is protected
	return d.isVRGConditionMet(homeCluster, VRGConditionTypeDataReady) &&
		d.isVRGConditionMet(homeCluster, VRGConditionTypeClusterDataProtected)
}

func (d *DRPCInstance) checkReadinessAfterFailover(homeCluster string) bool {
	return d.isVRGConditionMet(homeCluster, VRGConditionTypeDataReady) &&
		d.isVRGConditionMet(homeCluster, VRGConditionTypeClusterDataReady)
}

func (d *DRPCInstance) isVRGConditionMet(cluster string, conditionType string) bool {
	const ready = true

	d.log.Info(fmt.Sprintf("Checking if VRG is %s on cluster %s", conditionType, cluster))

	vrg := d.vrgs[cluster]

	if vrg == nil {
		d.log.Info(fmt.Sprintf("isVRGConditionMet: VRG not available on cluster %s", cluster))

		return !ready
	}

	condition := findCondition(vrg.Status.Conditions, conditionType)
	if condition == nil {
		d.log.Info(fmt.Sprintf("VRG %s condition not available on cluster %s", conditionType, cluster))

		return !ready
	}

	d.log.Info(fmt.Sprintf("VRG status condition: %+v", condition))

	return condition.Status == metav1.ConditionTrue &&
		condition.ObservedGeneration == vrg.Generation
}

func (d *DRPCInstance) relocate(preferredCluster, preferredClusterNamespace string, drState rmn.DRState) (bool, error) {
	const done = true

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

	err = d.switchToCluster(preferredCluster, preferredClusterNamespace, restorePVs)
	if err != nil {
		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), err.Error())

		return !done, err
	}

	d.setDRState(rmn.Relocated)
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
	d.log.Info(fmt.Sprintf("setupRelocation to preferredCluster %s", preferredCluster))

	// During relocation, the preferredCluster does not contain a VRG or the VRG is already
	// secondary. We need to skip checking if the VRG for it is secondary to avoid messing up with the
	// order of execution (it could be refactored better to avoid this complexity). IOW, if we first update
	// VRG in all clusters to secondaries, and then we call switchToCluster, and If switchToCluster does not
	// complete in one shot, then coming back to this loop will reset the preferredCluster to secondary again.
	clusterToSkip := preferredCluster
	if !d.ensureVRGIsSecondaryEverywhere(clusterToSkip) {
		d.setProgression(rmn.ProgressionMovingToSecondary)
		// During relocation, both clusters should be up and both must be secondaries before we proceed.
		if !d.moveVRGToSecondaryEverywhere() {
			return fmt.Errorf("failed to move VRG to secondary everywhere")
		}

		if !d.ensureVRGIsSecondaryEverywhere("") {
			return fmt.Errorf("waiting for VRGs to move to secondaries everywhere")
		}
	}

	if !d.ensureDataProtected(clusterToSkip) {
		return fmt.Errorf("waiting for data protection")
	}

	return nil
}

// switchToCluster is a series of steps to creating, updating, and cleaning up
// the necessary objects for the failover or relocation
func (d *DRPCInstance) switchToCluster(targetCluster, targetClusterNamespace string, restorePVs bool) error {
	d.log.Info("switchToCluster", "cluster", targetCluster, "restorePVs", restorePVs)

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

		d.log.Info(fmt.Sprintf("PVs Restored? %v", restored))

		if !restored {
			d.setProgression(rmn.ProgressionWaitingForPVRestore)

			return fmt.Errorf("%w)", WaitForPVRestoreToComplete)
		}
	}

	err = d.updateUserPlacementRule(targetCluster, targetClusterNamespace)
	if err != nil {
		return err
	}

	d.setProgression(rmn.ProgressionUpdatedPlRule)

	return nil
}

func (d *DRPCInstance) createVRGManifestWorkAsPrimary(targetCluster string) (bool, error) {
	d.log.Info("create or update VRG if it does not exists or is not primary", "cluster", targetCluster)

	vrg, err := d.getVRGFromManifestWork(targetCluster)
	if err != nil {
		if !errors.IsNotFound(err) {
			return false, err
		}
	}

	if vrg != nil {
		if vrg.Spec.ReplicationState == rmn.Primary {
			d.log.Info("VRG MW already Primary on this cluster", "name", vrg.Name, "cluster", targetCluster)

			return false, nil
		}

		_, err := d.updateVRGState(targetCluster, rmn.Primary)
		if err != nil {
			d.log.Info(fmt.Sprintf("Failed to update VRG to primary on cluster %s. Err (%v)", targetCluster, err))

			return false, err
		}

		return true, nil
	}

	err = d.createVRGManifestWork(targetCluster)
	if err != nil {
		return false, err
	}

	return true, nil
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

func (d *DRPCInstance) vrgExistsAndPrimary(targetCluster string) bool {
	vrg, ok := d.vrgs[targetCluster]
	if !ok || !d.isVRGPrimary(vrg) {
		return false
	}

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
		_, err := d.updateVRGState(clusterName, rmn.Secondary)
		if err != nil {
			if errors.IsNotFound(err) {
				continue
			}

			d.log.Info(fmt.Sprintf("Failed to update VRG to secondary on cluster %s. Error %s",
				clusterName, err.Error()))

			failedCount++
		}
	}

	if failedCount != 0 {
		d.log.Info("Failed to update VRG to secondary", "FailedCount", failedCount)

		return false
	}

	return true
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

		err = d.reconciler.MCVGetter.DeleteVRGManagedClusterView(d.instance.Name, d.instance.Namespace, clusterName,
			rmnutil.MWTypeVRG)
		// MW is deleted, VRG is deleted, so we no longer need MCV for the VRG
		if err != nil {
			d.log.Info("Deletion of VRG MCV failed")

			return false, fmt.Errorf("deletion of VRG MCV failed %w", err)
		}

		err = d.reconciler.MCVGetter.DeleteNamespaceManagedClusterView(d.instance.Name, d.instance.Namespace, clusterName,
			rmnutil.MWTypeNS)
		// MCV for Namespace is no longer needed
		if err != nil {
			d.log.Info("Deletion of Namespace MCV failed")

			return false, fmt.Errorf("deletion of namespace MCV failed %w", err)
		}
	}

	return true, nil
}

func (d *DRPCInstance) updateUserPlacementRule(homeCluster, homeClusterNamespace string) error {
	d.log.Info(fmt.Sprintf("Updating userPlacementRule %s homeCluster %s",
		d.userPlacementRule.Name, homeCluster))

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

	return d.reconciler.updateUserPlacementRuleStatus(d.userPlacementRule, newStatus, d.log)
}

func (d *DRPCInstance) clearUserPlacementRuleStatus() error {
	d.log.Info("Clearing userPlacementRule", "name", d.userPlacementRule.Name)

	newStatus := plrv1.PlacementRuleStatus{}

	return d.reconciler.updateUserPlacementRuleStatus(d.userPlacementRule, newStatus, d.log)
}

func (d *DRPCInstance) updatePreferredDecision() {
	if d.instance.Spec.PreferredCluster != "" &&
		reflect.DeepEqual(d.instance.Status.PreferredDecision, plrv1.PlacementDecision{}) {
		d.instance.Status.PreferredDecision = plrv1.PlacementDecision{
			ClusterName:      d.instance.Spec.PreferredCluster,
			ClusterNamespace: d.instance.Spec.PreferredCluster,
		}
	}
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

	vrg := d.generateVRG(rmn.Primary)
	vrg.Spec.VolSync.Disabled = d.volSyncDisabled

	annotations := make(map[string]string)

	annotations[DRPCNameAnnotation] = d.mwu.InstName
	annotations[DRPCNamespaceAnnotation] = d.mwu.InstNamespace

	if err := d.mwu.CreateOrUpdateVRGManifestWork(
		d.instance.Name, d.instance.Namespace,
		homeCluster, vrg, annotations); err != nil {
		d.log.Error(err, "failed to create or update VolumeReplicationGroup manifest")

		return fmt.Errorf("failed to create or update VolumeReplicationGroup manifest in namespace %s (%w)", homeCluster, err)
	}

	return nil
}

func vrgAction(drpcAction rmn.DRAction) rmn.VRGAction {
	switch drpcAction {
	case rmn.ActionFailover:
		return rmn.VRGActionFailover
	case rmn.ActionRelocate:
		return rmn.VRGActionRelocate
	default:
		return ""
	}
}

func (d *DRPCInstance) setVRGAction(vrg *rmn.VolumeReplicationGroup) {
	action := vrgAction(d.instance.Spec.Action)
	if action == "" {
		return
	}

	vrg.Spec.Action = action
}

func (d *DRPCInstance) generateVRG(repState rmn.ReplicationState) rmn.VolumeReplicationGroup {
	vrg := rmn.VolumeReplicationGroup{
		TypeMeta:   metav1.TypeMeta{Kind: "VolumeReplicationGroup", APIVersion: "ramendr.openshift.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: d.instance.Name, Namespace: d.instance.Namespace},
		Spec: rmn.VolumeReplicationGroupSpec{
			PVCSelector:          d.instance.Spec.PVCSelector,
			ReplicationState:     repState,
			S3Profiles:           rmnutil.DRPolicyS3Profiles(d.drPolicy, d.drClusters).List(),
			KubeObjectProtection: d.instance.Spec.KubeObjectProtection,
		},
	}

	d.setVRGAction(&vrg)
	vrg.Spec.Async = d.generateVRGSpecAsync()
	vrg.Spec.Sync = d.generateVRGSpecSync()

	return vrg
}

func (d *DRPCInstance) generateVRGSpecAsync() *rmn.VRGAsyncSpec {
	if dRPolicySupportsRegional(d.drPolicy, d.drClusters) {
		return &rmn.VRGAsyncSpec{
			ReplicationClassSelector:    d.drPolicy.Spec.ReplicationClassSelector,
			VolumeSnapshotClassSelector: d.drPolicy.Spec.VolumeSnapshotClassSelector,
			SchedulingInterval:          d.drPolicy.Spec.SchedulingInterval,
		}
	}

	return nil
}

func (d *DRPCInstance) generateVRGSpecSync() *rmn.VRGSyncSpec {
	if supports, _ := dRPolicySupportsMetro(d.drPolicy, d.drClusters); supports {
		return &rmn.VRGSyncSpec{}
	}

	return nil
}

func dRPolicySupportsRegional(drpolicy *rmn.DRPolicy, drClusters []rmn.DRCluster) bool {
	return rmnutil.DrpolicyRegionNamesAsASet(drpolicy, drClusters).Len() > 1
}

func dRPolicySupportsMetro(drpolicy *rmn.DRPolicy, drclusters []rmn.DRCluster) (
	supportsMetro bool,
	metroMap map[rmn.Region][]string,
) {
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
		annotations := make(map[string]string)

		annotations[DRPCNameAnnotation] = d.mwu.InstName
		annotations[DRPCNamespaceAnnotation] = d.mwu.InstNamespace

		err := d.mwu.CreateOrUpdateNamespaceManifest(d.instance.Name, d.instance.Namespace, homeCluster, annotations)
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
	d.log.Info("Checking if PVs have been restored", "cluster", homeCluster)

	annotations := make(map[string]string)

	annotations[DRPCNameAnnotation] = d.instance.Name
	annotations[DRPCNamespaceAnnotation] = d.instance.Namespace

	vrg, err := d.reconciler.MCVGetter.GetVRGFromManagedCluster(d.instance.Name,
		d.instance.Namespace, homeCluster, annotations)
	if err != nil {
		return false, fmt.Errorf("waiting for PVs to be restored on cluster %s (status: %w)", homeCluster, err)
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

//nolint:funlen,gocognit,cyclop
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

	if condition.Reason == rmn.ReasonSuccess &&
		condition.Status == metav1.ConditionTrue &&
		condition.ObservedGeneration == d.instance.Generation {
		d.log.Info("Condition values tallied, cleanup is considered complete")

		return nil
	}

	d.log.Info(fmt.Sprintf("PeerReady Condition %v", condition))

	// IFF we have VolSync PVCs, then no need to clean up
	homeCluster := clusterToSkip

	repReq, err := d.IsVolSyncReplicationRequired(homeCluster)
	if err != nil {
		return fmt.Errorf("failed to check if VolSync replication is required (%w)", err)
	}

	if repReq {
		d.log.Info("VolSync needs both VRGs. No need to clean up secondary")
		d.log.Info("Ensure secondary on peer")

		peersReady := true

		for _, clusterName := range rmnutil.DrpolicyClusterNames(d.drPolicy) {
			if clusterToSkip == clusterName {
				continue
			}

			justUpdated, err := d.updateVRGState(clusterName, rmn.Secondary)
			if err != nil {
				peersReady = false

				break
			}

			// IFF just updated, no need to use MCV to check if the state has been
			// applied. Wait for the next round of reconcile. Otherwise, check if
			// the change to secondary has been reflected.
			if justUpdated || !d.ensureVRGIsSecondaryOnCluster(clusterName) {
				peersReady = false

				break
			}
		}

		if !peersReady {
			return fmt.Errorf("still waiting for peer to be ready")
		}

		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionPeerReady, d.instance.Generation,
			metav1.ConditionTrue, rmn.ReasonSuccess, "Ready")

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
	_, err := d.reconciler.MCVGetter.GetNamespaceFromManagedCluster(d.instance.Name, cluster, d.instance.Namespace, nil)
	if err != nil {
		if errors.IsNotFound(err) { // successfully detected that Namespace is not found by ManagedClusterView
			d.log.Info(fmt.Sprintf("Namespace '%s' not found on cluster %s", d.instance.Namespace, cluster))

			return !exists, nil
		}

		d.log.Info(fmt.Sprintf("Failed to get Namespace from ManagedCluster -- Err: %v", err.Error()))

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

	// If .spec.ReplicateSpec has not already been updated to secondary, then update it.
	// If we do update it to secondary, then we have to wait for the MW to be applied
	updated, err := d.updateVRGState(clusterName, rmn.Secondary)
	if err != nil || updated {
		return !done, err
	}

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

	annotations := make(map[string]string)

	annotations[DRPCNameAnnotation] = d.instance.Name
	annotations[DRPCNamespaceAnnotation] = d.instance.Namespace

	vrg, err := d.reconciler.MCVGetter.GetVRGFromManagedCluster(d.instance.Name,
		d.instance.Namespace, clusterName, annotations)
	if err != nil {
		if errors.IsNotFound(err) {
			return true // ensured
		}

		d.log.Info("Failed to get VRG", "errorValue", err)

		d.mcvRequestInProgress = true

		return false
	}

	if vrg.Status.State != rmn.SecondaryState {
		d.log.Info(fmt.Sprintf("VRG on %s has not transitioned to secondary yet. Spec-State/Status-State %s/%s",
			clusterName, vrg.Spec.ReplicationState, vrg.Status.State))

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

	annotations := make(map[string]string)

	annotations[DRPCNameAnnotation] = d.instance.Name
	annotations[DRPCNamespaceAnnotation] = d.instance.Namespace

	vrg, err := d.reconciler.MCVGetter.GetVRGFromManagedCluster(d.instance.Name,
		d.instance.Namespace, clusterName, annotations)
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

	annotations := make(map[string]string)

	annotations[DRPCNameAnnotation] = d.instance.Name
	annotations[DRPCNamespaceAnnotation] = d.instance.Namespace

	vrg, err := d.reconciler.MCVGetter.GetVRGFromManagedCluster(d.instance.Name,
		d.instance.Namespace, clusterName, annotations)
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

func (d *DRPCInstance) updateVRGState(clusterName string, state rmn.ReplicationState) (bool, error) {
	d.log.Info(fmt.Sprintf("Updating VRG ReplicationState to secondary for cluster %s", clusterName))

	vrg, err := d.getVRGFromManifestWork(clusterName)
	if err != nil {
		return false, fmt.Errorf("failed to update VRG state. ClusterName %s (%w)",
			clusterName, err)
	}

	if vrg.Spec.ReplicationState == state {
		d.log.Info(fmt.Sprintf("VRG ReplicationState %s already %s on this cluster %s",
			vrg.Name, state, clusterName))

		return false, nil
	}

	vrg.Spec.ReplicationState = state
	if state == rmn.Secondary {
		// Turn off the final sync flags
		vrg.Spec.PrepareForFinalSync = false
		vrg.Spec.RunFinalSync = false
	}

	d.setVRGAction(vrg)

	err = d.updateManifestWork(clusterName, vrg)
	if err != nil {
		return false, err
	}

	d.log.Info(fmt.Sprintf("Updated VRG %s running in cluster %s to secondary", vrg.Name, clusterName))

	return true, nil
}

func (d *DRPCInstance) updateVRGToPrepareForFinalSync(clusterName string) error {
	d.log.Info(fmt.Sprintf("Updating VRG Spec to prepare for final sync on cluster %s", clusterName))

	vrg, err := d.getVRGFromManifestWork(clusterName)
	if err != nil {
		return fmt.Errorf("failed to update VRG state. ClusterName %s (%w)",
			clusterName, err)
	}

	if vrg.Spec.PrepareForFinalSync {
		d.log.Info(fmt.Sprintf("VRG %s on cluster %s already has the prepare for final sync flag set",
			vrg.Name, clusterName))

		return nil
	}

	vrg.Spec.PrepareForFinalSync = true
	vrg.Spec.RunFinalSync = false

	err = d.updateManifestWork(clusterName, vrg)
	if err != nil {
		return err
	}

	d.log.Info(fmt.Sprintf("Updated VRG %s running in cluster %s to prepare for the final sync",
		vrg.Name, clusterName))

	return nil
}

func (d *DRPCInstance) updateVRGToRunFinalSync(clusterName string) error {
	d.log.Info(fmt.Sprintf("Updating VRG Spec to run final sync on cluster %s", clusterName))

	vrg, err := d.getVRGFromManifestWork(clusterName)
	if err != nil {
		return fmt.Errorf("failed to update VRG state. ClusterName %s (%w)",
			clusterName, err)
	}

	if vrg.Spec.RunFinalSync {
		d.log.Info(fmt.Sprintf("VRG %s on cluster %s already has the final sync flag set",
			vrg.Name, clusterName))

		return nil
	}

	vrg.Spec.RunFinalSync = true
	vrg.Spec.PrepareForFinalSync = false

	err = d.updateManifestWork(clusterName, vrg)
	if err != nil {
		return err
	}

	d.log.Info(fmt.Sprintf("Updated VRG %s running in cluster %s to run the final sync",
		vrg.Name, clusterName))

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

func (d *DRPCInstance) updateManifestWork(clusterName string, vrg *rmn.VolumeReplicationGroup) error {
	vrgMWName := d.mwu.BuildManifestWorkName(rmnutil.MWTypeVRG)

	mw, err := d.mwu.FindManifestWork(vrgMWName, clusterName)
	if err != nil {
		return fmt.Errorf("%w", err)
	}

	vrgClientManifest, err := d.mwu.GenerateManifest(vrg)
	if err != nil {
		d.log.Error(err, "failed to generate manifest")

		return fmt.Errorf("failed to generate VRG manifest (%w)", err)
	}

	mw.Spec.Workload.Manifests[0] = *vrgClientManifest

	return d.reconciler.Update(d.ctx, mw)
}

func (d *DRPCInstance) setDRState(nextState rmn.DRState) {
	if d.instance.Status.Phase != nextState {
		d.log.Info(fmt.Sprintf("Phase: Current '%s'. Next '%s'",
			d.instance.Status.Phase, nextState))

		d.instance.Status.Phase = nextState
		d.reportEvent(nextState)
	}
}

func (d *DRPCInstance) setProgression(nextProgression rmn.ProgressionStatus) {
	if d.instance.Status.Progression != nextProgression {
		d.log.Info(fmt.Sprintf("Progression: Current '%s'. Next '%s'",
			d.instance.Status.Phase, nextProgression))

		d.instance.Status.Progression = nextProgression
	}
}

func (d *DRPCInstance) shouldUpdateStatus() bool {
	for _, condition := range d.instance.Status.Conditions {
		if condition.ObservedGeneration != d.instance.Generation {
			return true
		}
	}

	if !reflect.DeepEqual(d.savedInstanceStatus, d.instance.Status) {
		return true
	}

	homeCluster := ""
	if len(d.userPlacementRule.Status.Decisions) != 0 {
		homeCluster = d.userPlacementRule.Status.Decisions[0].ClusterName
	}

	if homeCluster == "" {
		return false
	}

	vrg := d.vrgs[homeCluster]
	if vrg == nil {
		return false
	}

	if !vrg.Status.LastGroupSyncTime.Equal(d.instance.Status.LastGroupSyncTime) {
		return true
	}

	return !reflect.DeepEqual(d.instance.Status.ResourceConditions.Conditions, vrg.Status.Conditions)
}

//nolint:exhaustive
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
	case rmn.Initiating:
		fallthrough
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

func (d *DRPCInstance) getProgression() rmn.ProgressionStatus {
	return d.instance.Status.Progression
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

const (
	bucketFactor = 2.0
	bucketCount  = 12
)

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
			Buckets: prometheus.ExponentialBuckets(1.0, bucketFactor, bucketCount),
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
			Buckets: prometheus.ExponentialBuckets(1.0, bucketFactor, bucketCount),
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
			Buckets: prometheus.ExponentialBuckets(1.0, bucketFactor, bucketCount),
		}),
	)
)

func init() {
	// register custom metrics with the global Prometheus registry
	metrics.Registry.MustRegister(failoverTime.gauge, failoverTime.histogram)
	metrics.Registry.MustRegister(relocateTime.gauge, relocateTime.histogram)
	metrics.Registry.MustRegister(deployTime.gauge, deployTime.histogram)
}

//nolint:exhaustive
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
	wrapper *timerWrapper, desiredTimerState timerState, reconcileState rmn.DRState,
) {
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

func (d *DRPCInstance) setConditionOnInitialDeploymentCompletion() {
	d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
		d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), "Initial deployment completed")

	d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionPeerReady, d.instance.Generation,
		metav1.ConditionTrue, rmn.ReasonSuccess, "Ready")
}

func (d *DRPCInstance) setDRPCCondition(conditions *[]metav1.Condition, condType string,
	observedGeneration int64, status metav1.ConditionStatus, reason, msg string,
) {
	SetDRPCStatusCondition(conditions, condType, observedGeneration, status, reason, msg)
}

func (d *DRPCInstance) isPlacementNeedsFixing() bool {
	// Needs fixing if and only if the DRPC Status is empty, the Placement decision is empty, and
	// the we have VRG(s) in the managed clusters
	d.log.Info(fmt.Sprintf("Check placement if needs fixing: PrD %v, PlD %v, VRGs %d",
		d.instance.Status.PreferredDecision, d.userPlacementRule.Status.Decisions, len(d.vrgs)))

	if reflect.DeepEqual(d.instance.Status.PreferredDecision, plrv1.PlacementDecision{}) &&
		len(d.userPlacementRule.Status.Decisions) == 0 && len(d.vrgs) > 0 {
		return true
	}

	return false
}

func (d *DRPCInstance) selectCurrentPrimaries() []string {
	var primaries []string

	for clusterName, vrg := range d.vrgs {
		if d.isVRGPrimary(vrg) {
			primaries = append(primaries, clusterName)
		}
	}

	return primaries
}

func (d *DRPCInstance) selectPrimaryForFailover(primaries []string) string {
	for _, clusterName := range primaries {
		if clusterName == d.instance.Spec.FailoverCluster {
			return clusterName
		}
	}

	return ""
}

func (d *DRPCInstance) fixupPlacementForFailover() error {
	d.log.Info("Fixing PlacementRule for failover...")

	var primary string

	var primaries []string

	var secondaries []string

	if d.areMultipleVRGsPrimary() {
		primaries := d.selectCurrentPrimaries()
		primary = d.selectPrimaryForFailover(primaries)
	} else {
		primary, secondaries = d.selectCurrentPrimaryAndSecondaries()
	}

	// IFF we have a primary cluster, and it points to the failoverCluster, then rebuild the
	// drpc status with it.
	if primary != "" && primary == d.instance.Spec.FailoverCluster {
		err := d.updateUserPlacementRule(primary, "")
		if err != nil {
			return err
		}

		// Update our 'well known' preferred placement
		d.updatePreferredDecision()

		peerReadyConditionStatus := metav1.ConditionTrue
		peerReadyMsg := "Ready"
		// IFF more than one primary then the failover hasn't entered the cleanup phase.
		// IFF we have a secondary, then the failover hasn't completed the cleanup phase.
		// We need to start where it was left off (best guess).
		if len(primaries) > 1 || len(secondaries) > 0 {
			d.instance.Status.Phase = rmn.FailingOver
			peerReadyConditionStatus = metav1.ConditionFalse
			peerReadyMsg = "NotReady"
		}

		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionPeerReady, d.instance.Generation,
			peerReadyConditionStatus, rmn.ReasonSuccess, peerReadyMsg)

		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			metav1.ConditionTrue, string(d.instance.Status.Phase), "Available")

		return nil
	}

	return fmt.Errorf("detected a failover, but it can't rebuild the state")
}

func (d *DRPCInstance) fixupPlacementForRelocate() error {
	if d.areMultipleVRGsPrimary() {
		return fmt.Errorf("unconstructable state. Can't have multiple primaries on 'Relocate'")
	}

	primary, secondaries := d.selectCurrentPrimaryAndSecondaries()
	d.log.Info(fmt.Sprintf("Fixing PlacementRule for relocation. Primary (%s), Secondaries (%v)",
		primary, secondaries))

	// IFF we have a primary cluster, then update the PlacementRule and reset PeerReady to false
	// Setting the PeerReady condition status to false allows peer cleanup if necessary
	if primary != "" {
		err := d.updateUserPlacementRule(primary, "")
		if err != nil {
			return err
		}

		// Assume that it is not clean
		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionPeerReady, d.instance.Generation,
			metav1.ConditionFalse, rmn.ReasonNotStarted, "NotReady")
	} else if len(secondaries) > 1 {
		// Use case 3: After Hub Recovery, the DRPC Action found to be 'Relocate', and ALL VRGs are secondary
		d.setDRPCCondition(&d.instance.Status.Conditions, rmn.ConditionPeerReady, d.instance.Generation,
			metav1.ConditionTrue, rmn.ReasonSuccess,
			fmt.Sprintf("Fixed for relocation to %q", d.instance.Spec.PreferredCluster))
	}

	// Update our 'well known' preferred placement
	d.updatePreferredDecision()

	return nil
}

func (d *DRPCInstance) setStatusInitiating() {
	if !(d.instance.Status.Phase == "" ||
		d.instance.Status.Phase == rmn.Deployed ||
		d.instance.Status.Phase == rmn.FailedOver ||
		d.instance.Status.Phase == rmn.Relocated) {
		return
	}

	d.setDRState(rmn.Initiating)
	d.setProgression("")

	d.instance.Status.ActionStartTime = &metav1.Time{Time: time.Now()}
	d.instance.Status.ActionDuration = nil
}

func (d *DRPCInstance) setActionDuration() {
	if !(d.instance.Status.ActionDuration == nil && d.instance.Status.ActionStartTime != nil) {
		return
	}

	duration := time.Since(d.instance.Status.ActionStartTime.Time)
	d.instance.Status.ActionDuration = &metav1.Duration{Duration: duration}

	d.log.Info(fmt.Sprintf("%s transition completed. Started at: %v and it took: %v",
		fmt.Sprintf("%v", d.instance.Status.Phase), d.instance.Status.ActionStartTime, duration))
}
