// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/exp/slices"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clrapiv1beta1 "open-cluster-management.io/api/cluster/v1beta1"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/core"
	rmnutil "github.com/ramendr/ramen/internal/controller/util"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// Annotations for MW and PlacementRule
	DRPCNameAnnotation      = "drplacementcontrol.ramendr.openshift.io/drpc-name"
	DRPCNamespaceAnnotation = "drplacementcontrol.ramendr.openshift.io/drpc-namespace"

	// Annotation that stores the UID of DRPC that created the resource on the managed cluster using a ManifestWork
	DRPCUIDAnnotation = "drplacementcontrol.ramendr.openshift.io/drpc-uid"

	// Annotation for the last cluster on which the application was running
	LastAppDeploymentCluster = "drplacementcontrol.ramendr.openshift.io/last-app-deployment-cluster"

	// Annotation for application namespace on the managed cluster
	DRPCAppNamespace = "drplacementcontrol.ramendr.openshift.io/app-namespace"
)

var (
	ErrWaitForAppResourceRestoreToComplete = errors.New("waiting for App resources to be restored")
	ErrWaitForVolSyncDestRepToComplete     = errors.New("waiting for VolSync RD to complete")
	ErrWaitForSourceCluster                = errors.New("waiting for primary to provide Protected PVCs")
	ErrWaitForVolSyncManifestWorkCreation  = errors.New("waiting for VolSync ManifestWork to be created")
	ErrWaitForVolSyncRDInfoAvailability    = errors.New("waiting for VolSync RDInfo")
)

type DRType string

const (
	DRTypeSync  = DRType("sync")
	DRTypeAsync = DRType("async")
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
	userPlacement        client.Object
	vrgs                 map[string]*rmn.VolumeReplicationGroup
	vrgNamespace         string
	ramenConfig          *rmn.RamenConfig
	mwu                  rmnutil.MWUtil
	drType               DRType
}

func (d *DRPCInstance) startProcessing() bool {
	d.log.Info("Starting to process placement")

	requeue := true
	done, processingErr := d.processPlacement()

	if d.shouldUpdateStatus() || d.statusUpdateTimeElapsed() {
		if err := d.reconciler.updateDRPCStatus(d.ctx, d.instance, d.userPlacement, d.log); err != nil {
			errMsg := fmt.Sprintf("error from update DRPC status: %v", err)
			if processingErr != nil {
				errMsg += fmt.Sprintf(", error from process placement: %v", processingErr)
			}

			d.log.Info(errMsg)

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
		err := fmt.Errorf("PreferredCluster not set. Placement (%v)", d.userPlacement)
		addOrUpdateCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), err.Error())
		// needStatusUpdate is not set. Still better to capture the event to report later
		rmnutil.ReportIfNotPresent(d.reconciler.eventRecorder, d.instance, corev1.EventTypeWarning,
			rmnutil.EventReasonDeployFail, err.Error())

		return !done, err
	}

	d.log.Info(fmt.Sprintf("Using homeCluster %s for initial deployment",
		homeCluster))

	// Check if we already deployed in the homeCluster or elsewhere
	deployed, clusterName := d.isDeployed(homeCluster)
	if deployed && clusterName != homeCluster {
		err := d.ensureVRGManifestWork(clusterName)
		if err != nil {
			return !done, err
		}

		// IF deployed on cluster that is not the preferred HomeCluster, then we are done
		return done, nil
	}

	// Ensure that initial deployment is complete
	if !deployed || !d.isUserPlRuleUpdated(homeCluster) {
		d.setStatusInitiating()

		_, err := d.startDeploying(homeCluster, homeClusterNamespace)
		if err != nil {
			addOrUpdateCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
				d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), err.Error())

			return !done, err
		}

		d.setConditionOnInitialDeploymentCompletion()

		return !done, nil
	}

	err := d.ensureVRGManifestWork(clusterName)
	if err != nil {
		return !done, err
	}

	// If we get here, the deployment is successful
	err = d.EnsureSecondaryReplicationSetup(homeCluster)
	if err != nil {
		return !done, err
	}

	// Update our 'well known' preferred placement
	d.updatePreferredDecision()
	d.setDRState(rmn.Deployed)

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

	// FIXME: The question is, should we care about dynamic home cluster selection. This feature has
	// always been available, but we never used it.  If not used, why have it, and keep carrying it?

	// if homeCluster == "" && d.drpcPlacementRule != nil && len(d.drpcPlacementRule.Status.Decisions) != 0 {
	// 	homeCluster = d.drpcPlacementRule.Status.Decisions[0].ClusterName
	// 	homeClusterNamespace = d.drpcPlacementRule.Status.Decisions[0].ClusterNamespace
	// }

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
	plRule := ConvertToPlacementRule(d.userPlacement)
	if plRule != nil {
		return len(plRule.Status.Decisions) > 0 &&
			plRule.Status.Decisions[0].ClusterName == homeCluster
	}

	// Otherwise, it is a Placement object
	plcmt := ConvertToPlacement(d.userPlacement)
	if plcmt != nil {
		clusterDecision := d.reconciler.getClusterDecision(d.userPlacement)

		return clusterDecision.ClusterName == homeCluster
	}

	return false
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
	for clusterName, vrg := range d.vrgs {
		if clusterName == clusterToSkip {
			continue
		}

		// We are checking for the initial deployment. Only return the cluster if the VRG on it is primary.
		if isVRGPrimary(vrg) {
			return clusterName, true
		}
	}

	return "", false
}

func (d *DRPCInstance) startDeploying(homeCluster, homeClusterNamespace string) (bool, error) {
	const done = true

	// Make sure we record the state that we are deploying
	d.setDRState(rmn.Deploying)
	d.setProgression(rmn.ProgressionCreatingMW)
	// Create VRG first, to leverage user PlacementRule decision to skip placement and move to cleanup
	err := d.createVRGManifestWork(homeCluster, rmn.Primary)
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return false, err
	}

	// TODO: Why are we not waiting for ClusterDataReady here? Are there any corner cases?

	// We have a home cluster
	d.setProgression(rmn.ProgressionUpdatingPlRule)

	err = d.updateUserPlacementRule(homeCluster, homeClusterNamespace)
	if err != nil {
		rmnutil.ReportIfNotPresent(d.reconciler.eventRecorder, d.instance, corev1.EventTypeWarning,
			rmnutil.EventReasonDeployFail, err.Error())

		return !done, err
	}

	// All good, update the preferred decision and state
	d.instance.Status.PreferredDecision.ClusterName = d.instance.Spec.PreferredCluster
	d.instance.Status.PreferredDecision.ClusterNamespace = d.instance.Spec.PreferredCluster

	d.log.Info("Updated PreferredDecision", "PreferredDecision", d.instance.Status.PreferredDecision)

	d.setDRState(rmn.Deployed)

	return done, nil
}

// RunFailover:
// 0. Check if failoverCluster is a valid target as Secondary (or already is a Primary)
// 1. If already failed over or in the process (VRG on failoverCluster is Primary), ensure failover is complete and
// then ensure cleanup
// 2. Else, if failover is initiated (VRG ManifestWork is create as Primary), then try again till VRG manifests itself
// on the failover cluster
// 3. Else, initiate failover to the desired failoverCluster (switchToFailoverCluster)
func (d *DRPCInstance) RunFailover() (bool, error) {
	d.log.Info("Entering RunFailover", "state", d.getLastDRState())

	const done = true

	if d.instance.Spec.FailoverCluster == "" {
		const msg = "missing value for spec.FailoverCluster"

		addOrUpdateCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), msg)

		return done, fmt.Errorf(msg)
	}

	failoverCluster := d.instance.Spec.FailoverCluster
	if !d.isValidFailoverTarget(failoverCluster) {
		err := fmt.Errorf("unable to start failover, spec.FailoverCluster (%s) is not a valid Secondary target",
			failoverCluster)
		addOrUpdateCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), err.Error())

		return !done, err
	}

	// IFF VRG exists and it is primary in the failoverCluster, then ensure failover is complete and
	// clean up and setup VolSync if needed.
	if d.vrgExistsAndPrimary(failoverCluster) {
		d.updatePreferredDecision()
		d.setDRState(rmn.FailedOver)
		addOrUpdateCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			metav1.ConditionTrue, string(d.instance.Status.Phase), "Completed")

		// Make sure VolRep 'Data' and VolSync 'setup' conditions are ready
		ready := d.checkReadiness(failoverCluster)
		if !ready {
			d.log.Info("VRGCondition not ready to finish failover")
			d.setProgression(rmn.ProgressionWaitForReadiness)

			return !done, nil
		}

		return d.ensureFailoverActionCompleted(failoverCluster)
	} else if yes, err := d.mwExistsAndPlacementUpdated(failoverCluster); yes || err != nil {
		// We have to wait for the VRG to appear on the failoverCluster or
		// in case of an error, try again later
		return !done, err
	}

	d.setStatusInitiating()

	return d.switchToFailoverCluster()
}

// isValidFailoverTarget determines if the passed in cluster is a valid target to failover to. A valid failover target
// may already be Primary
// NOTE: Currently there is a gap where, right after DR protection when a Secondary VRG is not yet created for VolSync
// workloads, a failover if initiated will pass these checks. When we fix to retain VRG for VR as well, a more
// deterministic check for VRG as Secondary can be performed.
func (d *DRPCInstance) isValidFailoverTarget(cluster string) bool {
	annotations := make(map[string]string)
	annotations[DRPCNameAnnotation] = d.instance.GetName()
	annotations[DRPCNamespaceAnnotation] = d.instance.GetNamespace()

	vrg, err := d.reconciler.MCVGetter.GetVRGFromManagedCluster(d.instance.Name, d.vrgNamespace, cluster, annotations)
	if err != nil {
		d.log.Info("Failed to get VRG from managed cluster", "name", d.instance.Name, "namespace", d.vrgNamespace,
			"cluster", cluster, "annotations", annotations, "error", err)

		return false
	}

	if isVRGPrimary(vrg) {
		// VRG is Primary, valid target with possible failover in progress
		return true
	}

	// Valid target only if VRG is protecting PVCs with VS and its status is also Secondary
	if vrg.Status.State != rmn.SecondaryState || vrg.Status.ObservedGeneration != vrg.Generation {
		d.log.Info(fmt.Sprintf("VRG on %s has not transitioned to secondary yet. Spec-State/Status-State %s/%s",
			cluster, vrg.Spec.ReplicationState, vrg.Status.State))

		return false
	}

	return true
}

func (d *DRPCInstance) checkClusterFenced(cluster string, drClusters []rmn.DRCluster) (bool, error) {
	for i := range drClusters {
		if drClusters[i].Name != cluster {
			continue
		}

		drClusterFencedCondition := rmnutil.FindCondition(drClusters[i].Status.Conditions, rmn.DRClusterConditionTypeFenced)
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
	addOrUpdateCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
		d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), "Starting failover")
	addOrUpdateCondition(&d.instance.Status.Conditions, rmn.ConditionPeerReady, d.instance.Generation,
		metav1.ConditionFalse, rmn.ReasonNotStarted,
		fmt.Sprintf("Started failover to cluster %q", d.instance.Spec.FailoverCluster))
	d.setProgression(rmn.ProgressionCheckingFailoverPrerequisites)

	curHomeCluster := d.getCurrentHomeClusterName(d.instance.Spec.FailoverCluster, d.drClusters)
	if curHomeCluster == "" {
		msg := "Invalid Failover request. Current home cluster does not exists"
		d.log.Info(msg)
		addOrUpdateCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), msg)

		err := fmt.Errorf("failover requested on invalid state %v", d.instance.Status)
		rmnutil.ReportIfNotPresent(d.reconciler.eventRecorder, d.instance, corev1.EventTypeWarning,
			rmnutil.EventReasonSwitchFailed, err.Error())

		return done, err
	}

	if met, err := d.checkFailoverPrerequisites(curHomeCluster); !met || err != nil {
		return !done, err
	}

	d.setProgression(rmn.ProgressionFailingOverToCluster)

	newHomeCluster := d.instance.Spec.FailoverCluster

	err := d.switchToCluster(newHomeCluster, "")
	if err != nil {
		addOrUpdateCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), err.Error())
		rmnutil.ReportIfNotPresent(d.reconciler.eventRecorder, d.instance, corev1.EventTypeWarning,
			rmnutil.EventReasonSwitchFailed, err.Error())

		return !done, err
	}

	d.updatePreferredDecision()
	d.setDRState(rmn.FailedOver)
	addOrUpdateCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
		d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), "Completed")
	d.log.Info("Failover completed", "state", d.getLastDRState())

	// The failover is complete, but we still need to clean up the failed primary.
	// hence, returning a NOT done
	return !done, nil
}

func (d *DRPCInstance) getCurrentHomeClusterName(toCluster string, drClusters []rmn.DRCluster) string {
	clusterDecision := d.reconciler.getClusterDecision(d.userPlacement)
	if clusterDecision.ClusterName != "" {
		return clusterDecision.ClusterName
	}

	if d.instance.Status.PreferredDecision.ClusterName != "" {
		return d.instance.Status.PreferredDecision.ClusterName
	}

	// otherwise, just return the peer cluster
	for i := range drClusters {
		if drClusters[i].Name != toCluster {
			return drClusters[i].Name
		}
	}

	// If all fails, then we have no curHomeCluster
	return ""
}

// checkFailoverPrerequisites checks for any failover prerequisites that need to be met on the
// failoverCluster before initiating a failover.
// Returns:
//   - bool: Indicating if prerequisites are met
//   - error: Any error in determining the prerequisite status
func (d *DRPCInstance) checkFailoverPrerequisites(curHomeCluster string) (bool, error) {
	var (
		met bool
		err error
	)

	if d.drType == DRTypeSync {
		met, err = d.checkMetroFailoverPrerequisites(curHomeCluster)
	} else {
		met = d.checkRegionalFailoverPrerequisites()
	}

	if err == nil && met {
		return true, nil
	}

	msg := "Waiting for spec.failoverCluster to meet failover prerequisites"

	if err != nil {
		msg = err.Error()

		rmnutil.ReportIfNotPresent(d.reconciler.eventRecorder, d.instance, corev1.EventTypeWarning,
			rmnutil.EventReasonSwitchFailed, err.Error())
	}

	addOrUpdateCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
		d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), msg)

	return met, err
}

// checkMetroFailoverPrerequisites checks for any MetroDR failover prerequisites that need to be met on the
// failoverCluster before initiating a failover from the curHomeCluster.
// Returns:
//   - bool: Indicating if prerequisites are met
//   - error: Any error in determining the prerequisite status
func (d *DRPCInstance) checkMetroFailoverPrerequisites(curHomeCluster string) (bool, error) {
	met := true

	d.setProgression(rmn.ProgressionWaitForFencing)

	fenced, err := d.checkClusterFenced(curHomeCluster, d.drClusters)
	if err != nil {
		return !met, err
	}

	if !fenced {
		return !met, fmt.Errorf("current home cluster %s is not fenced", curHomeCluster)
	}

	return met, nil
}

// checkRegionalFailoverPrerequisites checks for any RegionalDR failover prerequisites that need to be met on the
// failoverCluster before initiating a failover.
// Returns:
//   - bool: Indicating if prerequisites are met
func (d *DRPCInstance) checkRegionalFailoverPrerequisites() bool {
	d.setProgression(rmn.ProgressionWaitForStorageMaintenanceActivation)

	for _, drCluster := range d.drClusters {
		if drCluster.Name != d.instance.Spec.FailoverCluster {
			continue
		}

		// we want to work with failover cluster only, because the previous primary cluster might be unreachable
		if required, activationsRequired := requiresRegionalFailoverPrerequisites(
			d.ctx,
			d.reconciler.APIReader,
			[]string{drCluster.Spec.S3ProfileName},
			d.instance.GetName(), d.vrgNamespace,
			d.vrgs, d.instance.Spec.FailoverCluster,
			d.reconciler.ObjStoreGetter, d.log); required {
			return checkFailoverMaintenanceActivations(drCluster, activationsRequired, d.log)
		}

		break
	}

	return true
}

// requiresRegionalFailoverPrerequisites checks protected PVCs as reported by the last known Primary cluster
// to determine if this instance requires failover maintenance modes to be active prior to initiating
// a failover
func requiresRegionalFailoverPrerequisites(
	ctx context.Context,
	apiReader client.Reader,
	s3ProfileNames []string,
	drpcName string,
	vrgNamespace string,
	vrgs map[string]*rmn.VolumeReplicationGroup,
	failoverCluster string,
	objectStoreGetter ObjectStoreGetter,
	log logr.Logger,
) (
	bool,
	map[string]rmn.StorageIdentifiers,
) {
	activationsRequired := map[string]rmn.StorageIdentifiers{}

	vrg := getLastKnownPrimaryVRG(vrgs, failoverCluster)
	if vrg == nil {
		vrg = GetLastKnownVRGPrimaryFromS3(ctx, apiReader, s3ProfileNames, drpcName, vrgNamespace, objectStoreGetter, log)
		if vrg == nil {
			// TODO: Is this an error, should we ensure at least one VRG is found in the edge cases?
			// Potentially missing VRG and so stop failover? How to recover in that case?
			log.Info("Failed to find last known primary", "cluster", failoverCluster)

			return false, activationsRequired
		}
	}

	for _, protectedPVC := range vrg.Status.ProtectedPVCs {
		if len(protectedPVC.StorageIdentifiers.ReplicationID.Modes) == 0 {
			continue
		}

		if !hasMode(protectedPVC.StorageIdentifiers.ReplicationID.Modes, rmn.MModeFailover) {
			continue
		}

		// TODO: Assumption is that if there is a mMode then the ReplicationID is a must, err otherwise?
		key := protectedPVC.StorageIdentifiers.StorageProvisioner + protectedPVC.StorageIdentifiers.ReplicationID.ID
		if _, ok := activationsRequired[key]; !ok {
			activationsRequired[key] = protectedPVC.StorageIdentifiers
		}
	}

	return len(activationsRequired) != 0, activationsRequired
}

// getLastKnownPrimaryVRG gets the last known Primary VRG from the cluster that is not the current targetCluster
// This is done inspecting VRGs from the MCV reports, and in case not found, fetching it from the s3 store
func getLastKnownPrimaryVRG(
	vrgs map[string]*rmn.VolumeReplicationGroup,
	targetCluster string,
) *rmn.VolumeReplicationGroup {
	var vrgToInspect *rmn.VolumeReplicationGroup

	for drcluster, vrg := range vrgs {
		if drcluster == targetCluster {
			continue
		}

		if isVRGPrimary(vrg) {
			// TODO: Potentially when there are more than on primary VRGs find the best one?
			vrgToInspect = vrg

			break
		}
	}

	if vrgToInspect == nil {
		return nil
	}

	return vrgToInspect
}

func GetLastKnownVRGPrimaryFromS3(
	ctx context.Context,
	apiReader client.Reader,
	s3ProfileNames []string,
	sourceVrgName string,
	sourceVrgNamespace string,
	objectStoreGetter ObjectStoreGetter,
	log logr.Logger,
) *rmn.VolumeReplicationGroup {
	var latestVrg *rmn.VolumeReplicationGroup

	var latestUpdateTime time.Time

	for _, s3ProfileName := range s3ProfileNames {
		objectStorer, _, err := objectStoreGetter.ObjectStore(
			ctx, apiReader, s3ProfileName, "drpolicy validation", log)
		if err != nil {
			log.Info("Creating object store failed", "error", err)

			continue
		}

		sourcePathNamePrefix := s3PathNamePrefix(sourceVrgNamespace, sourceVrgName)

		vrg := &rmn.VolumeReplicationGroup{}
		if err := vrgObjectDownload(objectStorer, sourcePathNamePrefix, vrg); err != nil {
			log.Info(fmt.Sprintf("Failed to get VRG from s3 store - s3ProfileName %s. Err %v", s3ProfileName, err))

			continue
		}

		if !isVRGPrimary(vrg) {
			log.Info("Found a non-primary vrg on s3 store", "name", vrg.GetName(), "namespace", vrg.GetNamespace())

			continue
		}

		// Compare lastUpdateTime with the latestUpdateTime
		if latestVrg == nil || vrg.Status.LastUpdateTime.After(latestUpdateTime) {
			latestUpdateTime = vrg.Status.LastUpdateTime.Time
			latestVrg = vrg

			log.Info("Found a primary vrg on s3 store", "name",
				latestVrg.GetName(), "namespace", latestVrg.GetNamespace(), "s3Store", s3ProfileName)
		}
	}

	return latestVrg
}

// hasMode is a helper routine that checks if a list of modes has the passed in mode
func hasMode(modes []rmn.MMode, mode rmn.MMode) bool {
	for _, modeInList := range modes {
		if modeInList == mode {
			return true
		}
	}

	return false
}

// checkFailoverMaintenanceActivations checks if all required storage backend maintenance activations are met
func checkFailoverMaintenanceActivations(drCluster rmn.DRCluster,
	activationsRequired map[string]rmn.StorageIdentifiers,
	log logr.Logger,
) bool {
	for _, activationRequired := range activationsRequired {
		if !checkActivationForStorageIdentifier(
			drCluster.Status.MaintenanceModes,
			activationRequired,
			rmn.MModeConditionFailoverActivated,
			log,
		) {
			return false
		}
	}

	return true
}

// checkActivationForStorageIdentifier checks if provided storageIdentifier failover maintenance mode is
// in an activated state as reported in the passed in ClusterMaintenanceMode list
func checkActivationForStorageIdentifier(
	mModeStatus []rmn.ClusterMaintenanceMode,
	storageIdentifier rmn.StorageIdentifiers,
	activation rmn.MModeStatusConditionType,
	log logr.Logger,
) bool {
	for _, statusMMode := range mModeStatus {
		log.Info("Processing ClusterMaintenanceMode for match", "clustermode", statusMMode, "desiredmode", storageIdentifier)

		if statusMMode.StorageProvisioner != storageIdentifier.StorageProvisioner ||
			statusMMode.TargetID != storageIdentifier.ReplicationID.ID {
			continue
		}

		for _, condition := range statusMMode.Conditions {
			if condition.Type != string(activation) {
				continue
			}

			if condition.Status == metav1.ConditionTrue {
				return true
			}

			return false
		}

		return false
	}

	return false
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
//nolint:gocognit,cyclop,funlen
func (d *DRPCInstance) RunRelocate() (bool, error) {
	d.log.Info("Entering RunRelocate", "state", d.getLastDRState(), "progression", d.getProgression())

	const done = true

	if d.reconciler.numClustersQueriedSuccessfully != len(d.drPolicy.Spec.DRClusters) {
		d.log.Info("Can't progress with relocation -- Not all clusters are reachable",
			"numClustersQueriedSuccessfully", d.reconciler.numClustersQueriedSuccessfully,
			"NumOfClusters", len(d.drPolicy.Spec.DRClusters))

		return !done, nil
	}

	preferredCluster := d.instance.Spec.PreferredCluster
	preferredClusterNamespace := d.instance.Spec.PreferredCluster

	// Before relocating to the preferredCluster, do a quick validation and select the current preferred cluster.
	curHomeCluster, err := d.validateAndSelectCurrentPrimary(preferredCluster)
	if err != nil {
		addOrUpdateCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), err.Error())

		return !done, err
	}

	// If already relocated to preferredCluster; ensure required setup is complete
	if curHomeCluster != "" && d.vrgExistsAndPrimary(preferredCluster) {
		d.setDRState(rmn.Relocating)
		d.updatePreferredDecision()

		ready := d.checkReadiness(preferredCluster)
		if !ready {
			d.log.Info("VRGCondition not ready to finish relocation")
			d.setProgression(rmn.ProgressionWaitForReadiness)

			return !done, nil
		}

		d.setDRState(rmn.Relocated)
		addOrUpdateCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			metav1.ConditionTrue, string(d.instance.Status.Phase), "Completed")

		return d.ensureRelocateActionCompleted(preferredCluster)
	}

	d.setStatusInitiating()

	// Check if current primary (that is not the preferred cluster), is ready to switch over
	if curHomeCluster != "" && curHomeCluster != preferredCluster &&
		!d.readyToSwitchOver(curHomeCluster, preferredCluster) {
		errMsg := fmt.Sprintf("current cluster (%s) has not completed protection actions", curHomeCluster)
		addOrUpdateCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), errMsg)

		return !done, fmt.Errorf("%s", errMsg)
	}

	if d.getLastDRState() != rmn.Relocating && !d.validatePeerReady() {
		return !done, fmt.Errorf("clean up secondaries is pending, peer is not ready")
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

func (d *DRPCInstance) ensureRelocateActionCompleted(srcCluster string) (bool, error) {
	d.setProgression(rmn.ProgressionCleaningUp)

	return d.ensureActionCompleted(srcCluster)
}

func (d *DRPCInstance) ensureFailoverActionCompleted(srcCluster string) (bool, error) {
	// This is the time to cleanup the workload from the preferredCluster.
	// For managed apps, it will be done automatically by ACM, when we update
	// the placement to the targetCluster. For discovered apps, we have to let
	// the user know that they need to clean up the apps.
	// So set the progression to wait on user to clean up.
	// If not discovered apps, then we can set the progression to cleaning up.
	if isDiscoveredApp(d.instance) {
		d.setProgression(rmn.ProgressionWaitOnUserToCleanUp)
	} else {
		d.setProgression(rmn.ProgressionCleaningUp)
	}

	return d.ensureActionCompleted(srcCluster)
}

func isDiscoveredApp(drpc *rmn.DRPlacementControl) bool {
	return drpc.Spec.ProtectedNamespaces != nil && len(*drpc.Spec.ProtectedNamespaces) > 0
}

func (d *DRPCInstance) ensureActionCompleted(srcCluster string) (bool, error) {
	const done = true

	err := d.ensureVRGManifestWork(srcCluster)
	if err != nil {
		return !done, err
	}

	err = d.ensurePlacement(srcCluster)
	if err != nil {
		return !done, err
	}

	// Cleanup and setup VolSync if enabled
	err = d.ensureCleanupAndSecondaryReplicationSetup(srcCluster)
	if err != nil {
		return !done, err
	}

	d.setProgression(rmn.ProgressionCompleted)

	d.setActionDuration()

	return done, nil
}

func (d *DRPCInstance) ensureCleanupAndSecondaryReplicationSetup(srcCluster string) error {
	// If we have VolSync replication, this is the perfect time to reset the RDSpec
	// on the primary. This will cause the RD to be cleared on the primary
	err := d.ResetVolSyncRDOnPrimary(srcCluster)
	if err != nil {
		return err
	}

	// Check if the reset has already been applied. ResetVolSyncRDOnPrimary resets the VRG
	// in the MW, but the VRGs in the vrgs slice are fetched using MCV.
	vrg, ok := d.vrgs[srcCluster]
	if !ok || len(vrg.Spec.VolSync.RDSpec) != 0 {
		return fmt.Errorf("waiting for RDSpec count on cluster %s to go to zero. VRG OK? %v", srcCluster, ok)
	}

	// Ensure cleanup waits for the VRG to be secondary on the clusters other than srcCluster
	err = d.EnsureCleanup(srcCluster)
	if err != nil {
		return err
	}

	// After we ensured peers are clean, The VolSync ReplicationSource (RS) will automatically get
	// created, but for the ReplicationDestination, we need to explicitly tell the VRG to create it.
	err = d.EnsureSecondaryReplicationSetup(srcCluster)
	if err != nil {
		return err
	}

	return nil
}

//nolint:cyclop
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

	clusterDecision := d.reconciler.getClusterDecision(d.userPlacement)
	if clusterDecision.ClusterName != "" {
		d.setDRState(rmn.Relocating)
		addOrUpdateCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), "Starting quiescing for relocation")

		// We are going to clear the placement, this is when ACM will start
		// deleting the workloads from the current cluster. In case of
		// discovered apps, we have to let the user know that they need to
		// clean up the apps from the current cluster. So set the progression
		// to wait on user to clean up. For non-discovered apps, we can set the
		// progression to clearing placement.
		if isDiscoveredApp(d.instance) {
			d.setProgression(rmn.ProgressionWaitOnUserToCleanUp)
		} else {
			// clear current user PlacementRule's decision
			d.setProgression(rmn.ProgressionClearingPlacement)
		}

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
		if isDiscoveredApp(d.instance) {
			d.setProgression(rmn.ProgressionWaitOnUserToCleanUp)
		} else {
			d.setProgression(rmn.ProgressionRunningFinalSync)
		}

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
		if isVRGPrimary(vrg) {
			numOfPrimaries++
		}
	}

	return numOfPrimaries > 1
}

func (d *DRPCInstance) validatePeerReady() bool {
	condition := rmnutil.FindCondition(d.instance.Status.Conditions, rmn.ConditionPeerReady)
	if condition == nil || condition.Status == metav1.ConditionTrue {
		return true
	}

	d.log.Info("validatePeerReady", "Condition", condition)

	return false
}

func (d *DRPCInstance) selectCurrentPrimaryAndSecondaries() (string, []string) {
	var secondaryVRGs []string

	primaryVRG := ""

	for cn, vrg := range d.vrgs {
		if isVRGPrimary(vrg) && primaryVRG == "" {
			primaryVRG = cn
		}

		if isVRGSecondary(vrg) {
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

// readyToSwitchOver checks App resources are ready and the cluster data has been protected.
// ClusterDataProtected condition indicates if the related cluster data for an App (Managed
// by this DRPC instance) has been protected (uploaded to the S3 store(s)) or not.
func (d *DRPCInstance) readyToSwitchOver(homeCluster string, preferredCluster string) bool {
	d.log.Info(fmt.Sprintf("Checking if VRG Data is available on cluster %s", homeCluster))

	if d.drType == DRTypeSync {
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
	// Allow switch over when PV data is ready and the cluster data is protected
	return d.isVRGConditionMet(homeCluster, VRGConditionTypeDataReady) &&
		d.isVRGConditionMet(homeCluster, VRGConditionTypeClusterDataProtected)
}

func (d *DRPCInstance) checkReadiness(homeCluster string) bool {
	vrg := d.vrgs[homeCluster]
	if vrg == nil {
		return false
	}

	return d.isVRGConditionMet(homeCluster, VRGConditionTypeDataReady) &&
		d.isVRGConditionMet(homeCluster, VRGConditionTypeClusterDataReady) &&
		vrg.Status.State == rmn.PrimaryState
}

func (d *DRPCInstance) isVRGConditionMet(cluster string, conditionType string) bool {
	const ready = true

	d.log.Info(fmt.Sprintf("Checking if VRG is %s on cluster %s", conditionType, cluster))

	vrg := d.vrgs[cluster]

	if vrg == nil {
		d.log.Info(fmt.Sprintf("isVRGConditionMet: VRG not available on cluster %s", cluster))

		return !ready
	}

	condition := rmnutil.FindCondition(vrg.Status.Conditions, conditionType)
	if condition == nil {
		d.log.Info(fmt.Sprintf("VRG %s condition not available on cluster %s", conditionType, cluster))

		return !ready
	}

	d.log.Info(fmt.Sprintf("VRG status condition: %s is %s", conditionType, condition.Status))

	return condition.Status == metav1.ConditionTrue &&
		condition.ObservedGeneration == vrg.Generation
}

func (d *DRPCInstance) relocate(preferredCluster, preferredClusterNamespace string, drState rmn.DRState) (bool, error) {
	const done = true

	d.setDRState(drState)
	addOrUpdateCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
		d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), "Starting relocation")
	addOrUpdateCondition(&d.instance.Status.Conditions, rmn.ConditionPeerReady, d.instance.Generation,
		metav1.ConditionFalse, rmn.ReasonNotStarted,
		fmt.Sprintf("Relocation in progress to cluster %q", preferredCluster))

	// Setting up relocation ensures that all VRGs in all managed cluster are secondaries
	err := d.setupRelocation(preferredCluster)
	if err != nil {
		addOrUpdateCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), err.Error())

		return !done, err
	}

	err = d.switchToCluster(preferredCluster, preferredClusterNamespace)
	if err != nil {
		addOrUpdateCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
			d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), err.Error())

		return !done, err
	}

	d.updatePreferredDecision()
	d.setDRState(rmn.Relocated)
	addOrUpdateCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
		d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), "Completed")

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
		if isDiscoveredApp(d.instance) {
			d.setProgression(rmn.ProgressionWaitOnUserToCleanUp)
		} else {
			d.setProgression(rmn.ProgressionEnsuringVolumesAreSecondary)
		}
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

// switchToCluster is a series of steps for switching to the targetCluster as Primary,
// - It moves VRG to Primary on the targetCluster and ensures that VRG reports required readiness
// - Once VRG is ready, it updates the placement to trigger workload roll out to the targetCluster
// NOTE:
// Currently this function never gets to invoke updateUserPlacementRule as, if a VRG is found to be ready in
// checkReadiness, then the same VRG would have been found as Primary in RunFailover or RunRelocate, which would
// hence start processing the switching to cluster in those functions rather than here.
// As a result only when a VRG is not found as Primary (IOW nil from MCV), would checkReadiness be called and that
// would report false, till the VRG is found as above.
// TODO: This hence can be corrected to remove the call to updateUserPlacementRule and further lines of code
func (d *DRPCInstance) switchToCluster(targetCluster, targetClusterNamespace string) error {
	d.log.Info("switchToCluster", "cluster", targetCluster)

	createdOrUpdated, err := d.createVRGManifestWorkAsPrimary(targetCluster)
	if err != nil {
		return err
	}

	if createdOrUpdated {
		d.setProgression(rmn.ProgressionWaitingForResourceRestore)

		// We just created MWs. Give it time until the App resources have been restored
		return fmt.Errorf("%w)", ErrWaitForAppResourceRestoreToComplete)
	}

	if !d.checkReadiness(targetCluster) {
		d.setProgression(rmn.ProgressionWaitingForResourceRestore)

		return fmt.Errorf("%w)", ErrWaitForAppResourceRestoreToComplete)
	}

	err = d.updateUserPlacementRule(targetCluster, targetClusterNamespace)
	if err != nil {
		return err
	}

	d.setProgression(rmn.ProgressionUpdatedPlacement)

	return nil
}

func (d *DRPCInstance) createVRGManifestWorkAsPrimary(targetCluster string) (bool, error) {
	d.log.Info("create or update VRG if it does not exists or is not primary", "cluster", targetCluster)

	vrg, err := d.getVRGFromManifestWork(targetCluster)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
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

	err = d.createVRGManifestWork(targetCluster, rmn.Primary)
	if err != nil {
		return false, err
	}

	return true, nil
}

func (d *DRPCInstance) getVRGFromManifestWork(clusterName string) (*rmn.VolumeReplicationGroup, error) {
	mw, err := d.mwu.FindManifestWorkByType(rmnutil.MWTypeVRG, clusterName)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}

	vrg, err := rmnutil.ExtractVRGFromManifestWork(mw)
	if err != nil {
		return nil, err
	}

	return vrg, nil
}

func (d *DRPCInstance) vrgExistsAndPrimary(targetCluster string) bool {
	vrg, ok := d.vrgs[targetCluster]
	if !ok || !isVRGPrimary(vrg) {
		return false
	}

	if rmnutil.ResourceIsDeleted(vrg) {
		return false
	}

	d.log.Info(fmt.Sprintf("Already %q to cluster %s", d.getLastDRState(), targetCluster))

	return true
}

func (d *DRPCInstance) mwExistsAndPlacementUpdated(targetCluster string) (bool, error) {
	_, err := d.mwu.FindManifestWorkByType(rmnutil.MWTypeVRG, targetCluster)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return false, nil
		}

		return false, err
	}

	clusterDecision := d.reconciler.getClusterDecision(d.userPlacement)
	if clusterDecision.ClusterName == "" ||
		clusterDecision.ClusterName != targetCluster {
		return false, nil
	}

	return true, nil
}

func (d *DRPCInstance) moveVRGToSecondaryEverywhere() bool {
	d.log.Info("Move VRG to secondary everywhere")

	failedCount := 0

	for _, clusterName := range rmnutil.DRPolicyClusterNames(d.drPolicy) {
		_, err := d.updateVRGState(clusterName, rmn.Secondary)
		if err != nil {
			if k8serrors.IsNotFound(err) {
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

func (d *DRPCInstance) updateUserPlacementRule(homeCluster, reason string) error {
	d.log.Info(fmt.Sprintf("Updating user Placement %s homeCluster %s",
		d.userPlacement.GetName(), homeCluster))

	added := rmnutil.AddAnnotation(d.instance, LastAppDeploymentCluster, homeCluster)
	if added {
		if err := d.reconciler.Update(d.ctx, d.instance); err != nil {
			return err
		}
	}

	newPD := &clrapiv1beta1.ClusterDecision{
		ClusterName: homeCluster,
		Reason:      reason,
	}

	return d.reconciler.updateUserPlacementStatusDecision(d.ctx, d.userPlacement, newPD)
}

func (d *DRPCInstance) clearUserPlacementRuleStatus() error {
	d.log.Info("Clearing user Placement", "name", d.userPlacement.GetName())

	return d.reconciler.updateUserPlacementStatusDecision(d.ctx, d.userPlacement, nil)
}

func (d *DRPCInstance) updatePreferredDecision() {
	if d.instance.Spec.PreferredCluster != "" &&
		reflect.DeepEqual(d.instance.Status.PreferredDecision, rmn.PlacementDecision{}) {
		d.instance.Status.PreferredDecision = rmn.PlacementDecision{
			ClusterName:      d.instance.Spec.PreferredCluster,
			ClusterNamespace: d.instance.Spec.PreferredCluster,
		}
	}
}

// createVRGManifestWork is called to create a new VRG ManifestWork on homeCluster
func (d *DRPCInstance) createVRGManifestWork(homeCluster string, repState rmn.ReplicationState) error {
	err := d.ensureNamespaceManifestWork(homeCluster)
	if err != nil {
		return fmt.Errorf("createVRGManifestWork couldn't ensure namespace '%s' on cluster %s exists",
			d.vrgNamespace, homeCluster)
	}

	// Safety latch to ensure VRG MW is not present
	vrg, err := d.getVRGFromManifestWork(homeCluster)
	if (err != nil && !k8serrors.IsNotFound(err)) || vrg != nil {
		if err != nil {
			return fmt.Errorf("error (%w) determining ManifestWork for VolumeReplicationGroup resource "+
				"exists on cluster %s", err, homeCluster)
		}

		if vrg.Spec.ReplicationState != repState {
			return fmt.Errorf("ManifestWork for VolumeReplicationGroup resource "+
				"exists with mismatching state (%s) on cluster %s",
				vrg.Spec.ReplicationState, homeCluster)
		}

		return fmt.Errorf("VolumeReplicationGroup ManifestWork for cluster %s in state %s exists (%w)",
			homeCluster, string(vrg.Spec.ReplicationState), k8serrors.NewAlreadyExists(
				schema.GroupResource{
					Group:    rmn.GroupVersion.Group,
					Resource: "VolumeReplicationGroup",
				}, vrg.Name))
	}

	// create VRG ManifestWork
	d.log.Info("Creating VRG ManifestWork", "ReplicationState", repState,
		"Last State:", d.getLastDRState(), "cluster", homeCluster)

	newVRG := d.newVRG(homeCluster, repState, nil)
	annotations := make(map[string]string)

	annotations[DRPCNameAnnotation] = d.instance.Name
	annotations[DRPCNamespaceAnnotation] = d.instance.Namespace

	if _, err := d.mwu.CreateOrUpdateVRGManifestWork(
		d.instance.Name, d.vrgNamespace,
		homeCluster, newVRG, annotations); err != nil {
		d.log.Error(err, "failed to create or update VolumeReplicationGroup manifest")

		return fmt.Errorf("failed to create or update VolumeReplicationGroup manifest in namespace %s (%w)", homeCluster, err)
	}

	return nil
}

// ensureVRGManifestWork ensures that the VRG ManifestWork exists and matches the current VRG state.
func (d *DRPCInstance) ensureVRGManifestWork(homeCluster string) error {
	d.log.Info("Ensure VRG ManifestWork",
		"Last State:", d.getLastDRState(), "cluster", homeCluster)

	mw, mwErr := d.mwu.FindManifestWorkByType(rmnutil.MWTypeVRG, homeCluster)
	if mwErr != nil {
		if k8serrors.IsNotFound(mwErr) {
			return fmt.Errorf("failed to find ManifestWork for VolumeReplicationGroup from cluster %s", homeCluster)
		}

		return fmt.Errorf("error (%w) in finding ManifestWork for VolumeReplicationGroup from cluster %s",
			mwErr, homeCluster)
	}

	vrg, err := rmnutil.ExtractVRGFromManifestWork(mw)
	if err != nil {
		return fmt.Errorf("error extracting VRG from ManifestWork for cluster %s. Error: %w", homeCluster, err)
	}

	// Safety latch to ensure VRG to update is Primary
	if vrg.Spec.ReplicationState != rmn.Primary {
		return fmt.Errorf("invalid update for VolumeReplicationGroup in %s spec.replicationState on cluster %s",
			vrg.Spec.ReplicationState, homeCluster)
	}

	// Safety latch to ensure a view exists for the existing VRG ManifestWork
	if d.vrgs[homeCluster] == nil {
		return fmt.Errorf("missing VolumeReplicationGroup view for cluster %s, while attempting to update an instance",
			homeCluster)
	}

	d.updateVRGOptionalFields(vrg, d.vrgs[homeCluster], homeCluster)

	return d.mwu.UpdateVRGManifestWork(vrg, mw)
}

// equalClusterIDSlices compares 2 slices of clusterID strings and reports true if both contain the same clusterIDs.
// This is not a generic routine that can be used for any pair of string slices for equality checks, as a case of,
// - ["a", "b", "b"] compared to ["b", "a", "a"], would return true
// It is used for clusterIDs with the limitation, because clusterIDs are based on UUIDs and collision has ignorable
// probability
func equalClusterIDSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for _, v := range b {
		if !slices.Contains(a, v) {
			return false
		}
	}

	// check the other way to ensure there are no extra elements in a that are not contained in b
	for _, v := range a {
		if !slices.Contains(b, v) {
			return false
		}
	}

	return true
}

// updatePeerClass conditionally updates an existing peerClass in to, with values from. If existing peerClass claims
// a replicationID then the from should also claim a replicationID, else both should not. This ensures that a peerClass
// is updated with latest storage/replication IDs, but only if the underlying replication scheme remains unchanged.
func updatePeerClass(log logr.Logger, to []rmn.PeerClass, from rmn.PeerClass, scName string) {
	for toIdx := range to {
		if (to[toIdx].StorageClassName != scName) ||
			(!equalClusterIDSlices(to[toIdx].ClusterIDs, from.ClusterIDs)) {
			continue
		}

		if to[toIdx].ReplicationID == "" && from.ReplicationID == "" {
			to[toIdx] = from

			break
		}

		if to[toIdx].ReplicationID != "" && from.ReplicationID != "" {
			to[toIdx] = from

			break
		}

		log.Info("Unable to update mismatching peerClass", "peerClass", to[toIdx], "from", from)

		break
	}
}

// hasPeerClass finds a peer in the passed in list of peerClasses and returns true if a peer matches the passed in
// storage class name and represents the cluster in the clusterIDs list
// Also see peerClassMatchesPeer
func hasPeerClass(vrgPeerClasses []rmn.PeerClass, scName string, clusterIDs []string) bool {
	for peerClassVRGIdx := range vrgPeerClasses {
		if (vrgPeerClasses[peerClassVRGIdx].StorageClassName == scName) &&
			(equalClusterIDSlices(vrgPeerClasses[peerClassVRGIdx].ClusterIDs, clusterIDs)) {
			return true
		}
	}

	return false
}

// updatePeers see updateVRGDRTypeSpec
func updatePeers(
	log logr.Logger,
	vrgFromView *rmn.VolumeReplicationGroup,
	vrgPeerClasses, policyPeerClasses []rmn.PeerClass,
) []rmn.PeerClass {
	peerClasses := vrgPeerClasses

	for pvcIdx := range vrgFromView.Status.ProtectedPVCs {
		if vrgFromView.Status.ProtectedPVCs[pvcIdx].StorageClassName == nil ||
			len(*vrgFromView.Status.ProtectedPVCs[pvcIdx].StorageClassName) == 0 {
			continue
		}

		for policyPeerClassIdx := range policyPeerClasses {
			if policyPeerClasses[policyPeerClassIdx].StorageClassName !=
				*vrgFromView.Status.ProtectedPVCs[pvcIdx].StorageClassName {
				continue
			}

			if hasPeerClass(
				vrgPeerClasses,
				*vrgFromView.Status.ProtectedPVCs[pvcIdx].StorageClassName,
				policyPeerClasses[policyPeerClassIdx].ClusterIDs,
			) {
				updatePeerClass(
					log,
					peerClasses,
					policyPeerClasses[policyPeerClassIdx],
					*vrgFromView.Status.ProtectedPVCs[pvcIdx].StorageClassName,
				)

				break
			}

			if hasPeerClass(
				peerClasses,
				*vrgFromView.Status.ProtectedPVCs[pvcIdx].StorageClassName,
				policyPeerClasses[policyPeerClassIdx].ClusterIDs,
			) {
				break
			}

			peerClasses = append(
				peerClasses,
				policyPeerClasses[policyPeerClassIdx],
			)
		}
	}

	return peerClasses
}

// updateVRGAsyncSpec see updateVRGDRTypeSpec
func (d *DRPCInstance) updateVRGAsyncSpec(vrgFromView, vrg *rmn.VolumeReplicationGroup) {
	// vrg will be updated with final contents of asyncSpec
	asyncSpec := d.newVRGSpecAsync()
	if len(asyncSpec.PeerClasses) == 0 {
		// Retain peerClasses from VRG
		if vrg.Spec.Async != nil && len(vrg.Spec.Async.PeerClasses) != 0 {
			asyncSpec.PeerClasses = vrg.Spec.Async.PeerClasses
		}

		vrg.Spec.Async = asyncSpec

		return
	}

	// If there is no async spec in VRG, update it with generated spec
	// NOTE: Cannot happen! VRG is type Async and is being updated so Async cannot be nil, this is just safety
	if vrg.Spec.Async == nil {
		vrg.Spec.Async = asyncSpec

		return
	}

	asyncSpec.PeerClasses = updatePeers(
		d.log,
		vrgFromView,
		vrg.Spec.Async.PeerClasses,
		d.drPolicy.Status.Async.PeerClasses,
	)

	// TODO: prune peerClasses not in policy and not in use by VRG

	vrg.Spec.Async = asyncSpec
}

// updateVRGSyncSpec see updateVRGDRTypeSpec
func (d *DRPCInstance) updateVRGSyncSpec(vrgFromView, vrg *rmn.VolumeReplicationGroup) {
	// vrg will be updated with final contents of syncSpec
	syncSpec := d.newVRGSpecSync()
	if len(syncSpec.PeerClasses) == 0 {
		// Retain peerClasses from VRG
		if vrg.Spec.Sync != nil && len(vrg.Spec.Sync.PeerClasses) != 0 {
			syncSpec.PeerClasses = vrg.Spec.Sync.PeerClasses
		}

		vrg.Spec.Sync = syncSpec

		return
	}

	// If there is no sync spec in VRG, update it with generated spec
	// NOTE: Cannot happen! VRG is type Sync and is being updated so Sync cannot be nil, this is just safety
	if vrg.Spec.Sync == nil {
		vrg.Spec.Sync = syncSpec

		return
	}

	syncSpec.PeerClasses = updatePeers(d.log, vrgFromView, vrg.Spec.Sync.PeerClasses, d.drPolicy.Status.Sync.PeerClasses)

	// TODO: prune peerClasses not in policy and not in use by VRG

	vrg.Spec.Sync = syncSpec
}

// updateVRGDRTypeSpec updates VRG Sync/Async spec based on the DR type.
// Update works to ensure VRG is updated with peerClasses that it requires, based on reported PVCs that the VRG is
// attempting to protect. If a VRG is attempting to protect a PVC for which is is lacking a peerClass and that is
// available as part of the DRPolicy its peerClasses are updated. For existing peerClasses the VRG information is
// updated conditionally (see updatePeerClass), this is done to avoid any protection mechanism conflicts.
// For example, if a VRG carried a peerClass without the replicationID (ie it would choose to protect the PVC using
// Volsync and VolumeSnapshots), then it is not updated with a peerClass that NOW supports native VolumeReplication,
// as that would void existing protection. To change replication schemes a workload needs to be DR disabled and then
// reenabled to catch up to the latest available peer information for an SC.
func (d *DRPCInstance) updateVRGDRTypeSpec(vrgFromCluster, generatedVRG *rmn.VolumeReplicationGroup) {
	switch d.drType {
	case DRTypeSync:
		d.updateVRGSyncSpec(vrgFromCluster, generatedVRG)
	case DRTypeAsync:
		d.updateVRGAsyncSpec(vrgFromCluster, generatedVRG)
	}
}

// updateVRGOptionalFields ensures that the optional fields in the VRG object are up to date.
// This function does not modify the following fields:
//   - ObjectMeta.Name
//   - ObjectMeta.Namespace
//   - Spec.PVCSelector
//   - Spec.ReplicationState
//   - Spec.PrepareForFinalSync
//   - Spec.RunFinalSync
//   - Spec.VolSync.RDSpec
//
// These fields are either set during the initial creation of the VRG (e.g., name and namespace)
// or updated as needed, such as the PrepareForFinalSync and RunFinalSync fields.
func (d *DRPCInstance) updateVRGOptionalFields(vrg, vrgFromView *rmn.VolumeReplicationGroup, homeCluster string) {
	vrg.ObjectMeta.Annotations = map[string]string{
		DestinationClusterAnnotationKey: homeCluster,
		DoNotDeletePVCAnnotation:        d.instance.GetAnnotations()[DoNotDeletePVCAnnotation],
		DRPCUIDAnnotation:               string(d.instance.UID),
		rmnutil.IsCGEnabledAnnotation:   d.instance.GetAnnotations()[rmnutil.IsCGEnabledAnnotation],
		rmnutil.UseVolSyncAnnotation:    d.instance.GetAnnotations()[rmnutil.UseVolSyncAnnotation],
	}

	vrg.Spec.ProtectedNamespaces = d.instance.Spec.ProtectedNamespaces
	vrg.Spec.S3Profiles = AvailableS3Profiles(d.drClusters)
	vrg.Spec.KubeObjectProtection = d.instance.Spec.KubeObjectProtection
	vrg.Spec.VolSync.Disabled = d.volSyncDisabled
	d.setVRGAction(vrg)

	// If vrgFromView nil, then vrg is newly generated, Sync/Async spec is updated unconditionally
	if vrgFromView == nil {
		switch d.drType {
		case DRTypeSync:
			vrg.Spec.Sync = d.newVRGSpecSync()
		case DRTypeAsync:
			vrg.Spec.Async = d.newVRGSpecAsync()
		}
	} else {
		d.updateVRGDRTypeSpec(vrgFromView, vrg)
	}
}

func (d *DRPCInstance) ensurePlacement(homeCluster string) error {
	clusterDecision := d.reconciler.getClusterDecision(d.userPlacement)
	if clusterDecision.ClusterName == "" ||
		homeCluster != clusterDecision.ClusterName {
		d.updatePreferredDecision()

		return d.updateUserPlacementRule(homeCluster, homeCluster)
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

func (d *DRPCInstance) newVRG(
	dstCluster string,
	repState rmn.ReplicationState,
	vrgFromView *rmn.VolumeReplicationGroup,
) rmn.VolumeReplicationGroup {
	vrg := rmn.VolumeReplicationGroup{
		TypeMeta: metav1.TypeMeta{Kind: "VolumeReplicationGroup", APIVersion: "ramendr.openshift.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      d.instance.Name,
			Namespace: d.vrgNamespace,
		},
		Spec: rmn.VolumeReplicationGroupSpec{
			PVCSelector:      d.instance.Spec.PVCSelector,
			ReplicationState: repState,
		},
	}

	core.ObjectCreatedByRamenSetLabel(&vrg)

	d.updateVRGOptionalFields(&vrg, vrgFromView, dstCluster)

	return vrg
}

func (d *DRPCInstance) newVRGSpecAsync() *rmn.VRGAsyncSpec {
	return &rmn.VRGAsyncSpec{
		ReplicationClassSelector:         d.drPolicy.Spec.ReplicationClassSelector,
		VolumeSnapshotClassSelector:      d.drPolicy.Spec.VolumeSnapshotClassSelector,
		VolumeGroupSnapshotClassSelector: d.drPolicy.Spec.VolumeGroupSnapshotClassSelector,
		SchedulingInterval:               d.drPolicy.Spec.SchedulingInterval,
		PeerClasses:                      d.drPolicy.Status.Async.PeerClasses,
	}
}

func (d *DRPCInstance) newVRGSpecSync() *rmn.VRGSyncSpec {
	return &rmn.VRGSyncSpec{
		PeerClasses: d.drPolicy.Status.Sync.PeerClasses,
	}
}

func dRPolicySupportsMetro(drpolicy *rmn.DRPolicy, drclusters []rmn.DRCluster) (bool, map[string][]string) {
	syncPeerClasses := drpolicy.Status.Sync.PeerClasses
	aSyncPeerClasses := drpolicy.Status.Async.PeerClasses

	if len(syncPeerClasses) == 0 && len(aSyncPeerClasses) == 0 {
		return checkMetroSupportUsingRegion(drpolicy, drclusters)
	}

	if len(syncPeerClasses) != 0 {
		return checkMetroSupportUsingPeerClass(drpolicy)
	}

	return false, nil
}

func checkMetroSupportUsingPeerClass(drPolicy *rmn.DRPolicy) (bool, map[string][]string) {
	peerClasses := drPolicy.Status.Sync.PeerClasses
	if len(peerClasses) == 0 {
		return false, nil
	}

	metroMap := make(map[string][]string)
	for _, peerClass := range peerClasses {
		metroMap[peerClass.StorageClassName] = peerClass.ClusterIDs
	}

	return true, metroMap
}

func checkMetroSupportUsingRegion(drpolicy *rmn.DRPolicy, drclusters []rmn.DRCluster) (
	supportsMetro bool,
	metroMap map[string][]string,
) {
	allRegionsMap := make(map[rmn.Region][]string)
	metroMap = make(map[string][]string)

	for _, managedCluster := range rmnutil.DRPolicyClusterNames(drpolicy) {
		for _, v := range drclusters {
			if v.Name == managedCluster {
				if v.Spec.Region == "" {
					return supportsMetro, metroMap
				}

				allRegionsMap[v.Spec.Region] = append(
					allRegionsMap[v.Spec.Region],
					managedCluster)
			}
		}
	}

	for k, v := range allRegionsMap {
		if len(v) > 1 {
			supportsMetro = true
			metroMap[string(k)] = v
		}
	}

	return supportsMetro, metroMap
}

func (d *DRPCInstance) ensureNamespaceManifestWork(homeCluster string) error {
	// Ensure the MW for the namespace exists
	mw, err := d.mwu.FindManifestWorkByType(rmnutil.MWTypeNS, homeCluster)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return fmt.Errorf("failed to get NS MW (%w)", err)
		}

		annotations := make(map[string]string)

		annotations[DRPCNameAnnotation] = d.instance.Name
		annotations[DRPCNamespaceAnnotation] = d.instance.Namespace

		err := d.mwu.CreateOrUpdateNamespaceManifest(d.instance.Name, d.vrgNamespace, homeCluster, annotations)
		if err != nil {
			return fmt.Errorf("failed to create namespace '%s' on cluster %s: %w", d.vrgNamespace, homeCluster, err)
		}

		d.log.Info(fmt.Sprintf("Created Namespace '%s' on cluster %s", d.vrgNamespace, homeCluster))

		return nil // created namespace
	}

	// Ensure the OCM backup label does not exists, otherwise, remove it.
	labels := mw.GetLabels()
	if labels == nil {
		return nil
	}

	if _, ok := labels[rmnutil.OCMBackupLabelKey]; ok {
		delete(mw.Labels, rmnutil.OCMBackupLabelKey)

		return d.reconciler.Update(d.ctx, mw)
	}

	return nil
}

func isVRGPrimary(vrg *rmn.VolumeReplicationGroup) bool {
	return (vrg.Spec.ReplicationState == rmn.Primary)
}

func isVRGSecondary(vrg *rmn.VolumeReplicationGroup) bool {
	return (vrg.Spec.ReplicationState == rmn.Secondary)
}

func (d *DRPCInstance) EnsureCleanup(clusterToSkip string) error {
	d.log.Info("ensuring cleanup on secondaries")

	condition := rmnutil.FindCondition(d.instance.Status.Conditions, rmn.ConditionPeerReady)

	// Because we init conditions we will always find the condition and not move it to ReasonProgressing?
	if condition == nil {
		msg := "Starting cleanup check"
		d.log.Info(msg)
		addOrUpdateCondition(&d.instance.Status.Conditions, rmn.ConditionPeerReady, d.instance.Generation,
			metav1.ConditionFalse, rmn.ReasonProgressing, msg)

		condition = rmnutil.FindCondition(d.instance.Status.Conditions, rmn.ConditionPeerReady)
	}

	if condition.Reason == rmn.ReasonSuccess &&
		condition.Status == metav1.ConditionTrue &&
		condition.ObservedGeneration == d.instance.Generation {
		d.log.Info("Condition values tallied, cleanup is considered complete")

		return nil
	}

	d.log.Info(fmt.Sprintf("PeerReady Condition is %s, msg: %s", condition.Status, condition.Message))

	return d.cleanupSecondaries(clusterToSkip)
}

//nolint:gocognit
func (d *DRPCInstance) cleanupSecondaries(clusterToSkip string) error {
	d.log.Info("Ensure secondary setup on peer")

	peersReady := true

	for _, clusterName := range rmnutil.DRPolicyClusterNames(d.drPolicy) {
		if clusterToSkip == clusterName {
			continue
		}

		// Update PeerReady condition to appropriate reasons in here!
		justUpdated, err := d.updateVRGState(clusterName, rmn.Secondary)
		if err != nil {
			d.log.Info(fmt.Sprintf("Failed to update VRG state for cluster %s. Err (%v)", clusterName, err))

			peersReady = false

			// Recreate the VRG ManifestWork for the secondary. This typically happens during Hub Recovery.
			// Ideally this will never be called due to adoption of VRG in place, in the case of upgrades from older
			// scheme were VRG was not preserved for VR workloads, this can be hit IFF the upgrade happened when some
			// workload was not in peerReady state.
			if k8serrors.IsNotFound(err) {
				err := d.EnsureSecondaryReplicationSetup(clusterToSkip)
				if err != nil {
					return err
				}
			}

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

	addOrUpdateCondition(&d.instance.Status.Conditions, rmn.ConditionPeerReady, d.instance.Generation,
		metav1.ConditionTrue, rmn.ReasonSuccess, "Ready")

	return nil
}

// ensureVRGIsSecondaryEverywhere iterates through all the clusters in the DRCluster set,
// and for each cluster, it checks whether the VRG (if exists) is secondary. It will skip
// a cluster if provided. It returns true if all clusters report secondary for the VRG,
// otherwise, it returns false
func (d *DRPCInstance) ensureVRGIsSecondaryEverywhere(clusterToSkip string) bool {
	for _, clusterName := range rmnutil.DRPolicyClusterNames(d.drPolicy) {
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
		d.vrgNamespace, clusterName, annotations)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return true // ensured
		}

		d.log.Info("Failed to get VRG", "errorValue", err)

		d.mcvRequestInProgress = true

		return false
	}

	if vrg.Status.State != rmn.SecondaryState || vrg.Status.ObservedGeneration != vrg.Generation {
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
// about to move the workload to the preferredCluster. And before doing that, DataProtected
// has to be ensured. This can only be done at the other cluster which has been moved to
// secondary by now.
func (d *DRPCInstance) ensureDataProtected(targetCluster string) bool {
	for _, clusterName := range rmnutil.DRPolicyClusterNames(d.drPolicy) {
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
		d.vrgNamespace, clusterName, annotations)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// expectation is that VRG should be present. Otherwise, this function
			// would not have been called. Return false
			d.log.Info("VRG not found", "errorValue", err)

			return false
		}

		d.log.Info("Failed to get VRG", "errorValue", err)

		d.mcvRequestInProgress = true

		return false
	}

	dataProtectedCondition := rmnutil.FindCondition(vrg.Status.Conditions, VRGConditionTypeDataProtected)
	if dataProtectedCondition == nil {
		d.log.Info(fmt.Sprintf("VRG DataProtected condition not available for cluster %s (%v)",
			clusterName, vrg))

		return false
	}

	if dataProtectedCondition.Status != metav1.ConditionTrue ||
		dataProtectedCondition.ObservedGeneration != vrg.Generation {
		d.log.Info(fmt.Sprintf("VRG data protection is not complete for cluster %s for %v",
			clusterName, vrg))

		return false
	}

	return true
}

func (d *DRPCInstance) updateVRGState(clusterName string, state rmn.ReplicationState) (bool, error) {
	d.log.Info(fmt.Sprintf("Updating VRG ReplicationState to %s for cluster %s", state, clusterName))

	vrg, err := d.getVRGFromManifestWork(clusterName)
	if err != nil {
		return false, fmt.Errorf("failed to update VRG state. ClusterName %s (%w)",
			clusterName, err)
	}

	if vrg.Spec.ReplicationState == state {
		d.log.Info(fmt.Sprintf("VRG.Spec.ReplicationState %s already set to %s on cluster %s",
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

	d.log.Info(fmt.Sprintf("Updated VRG %s running on cluster %s to %s", vrg.Name, clusterName, state))

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

func (d *DRPCInstance) updateManifestWork(clusterName string, vrg *rmn.VolumeReplicationGroup) error {
	mw, err := d.mwu.FindManifestWorkByType(rmnutil.MWTypeVRG, clusterName)
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
		d.instance.Status.ObservedGeneration = d.instance.Generation
		d.reportEvent(nextState)
	}
}

func updateDRPCProgression(
	drpc *rmn.DRPlacementControl, nextProgression rmn.ProgressionStatus, log logr.Logger,
) bool {
	if drpc.Status.Progression != nextProgression {
		// caller of this function is always d.setProgression()
		// caller of d.setProgression() makes the progression decision.
		// Use ancestorLevel=2 to get the caller of the caller.
		// nolint: mnd
		decisionFunction := getCallerFunction(2)

		log.Info(fmt.Sprintf("function %v changing Progression from '%s' to '%s'",
			decisionFunction,
			drpc.Status.Progression, nextProgression))

		drpc.Status.Progression = nextProgression

		return true
	}

	return false
}

/*
DRPC Status.Progression has several distinct progressions depending on the action being performed. The following
comment is to help identify which progressions belong to which actions for reference purposes.

deployProgressions are used to indicate progression during initial deployment processing

	deployProgressions := {
		ProgressionCreatingMW,
		ProgressionUpdatingPlRule,
		ProgressionEnsuringVolSyncSetup,
		ProgressionSettingupVolsyncDest,
		ProgressionCompleted,
	}

failoverProgressions are used to indicate progression during failover action processing
- preFailoverProgressions indicates Progressions that are noted before creating VRG on the failoverCluster
- postFailoverProgressions indicates Progressions that are noted post creating VRG on the failoverCluster

	preFailoverProgressions := {
		ProgressionCheckingFailoverPrerequisites,
		ProgressionWaitForFencing,
		ProgressionWaitForStorageMaintenanceActivation,
	}

	postFailoverProgressions := {
		ProgressionFailingOverToCluster,
		ProgressionWaitingForResourceRestore,
		ProgressionEnsuringVolSyncSetup,
		ProgressionSettingupVolsyncDest,
		ProgressionWaitForReadiness,
		ProgressionUpdatedPlacement,
		ProgressionCompleted,
		ProgressionCleaningUp,
		ProgressionWaitOnUserToCleanUp,
	}

relocateProgressions are used to indicate progression during relocate action processing
- preSwitch indicates Progressions that are noted before creating VRG on the preferredCluster
- postSwitch indicates Progressions that are noted post creating VRG on the preferredCluster

	preRelocateProgressions := []rmn.ProgressionStatus{
		rmn.ProgressionPreparingFinalSync,
		rmn.ProgressionClearingPlacement,
		rmn.ProgressionRunningFinalSync,
		rmn.ProgressionFinalSyncComplete,
		rmn.ProgressionEnsuringVolumesAreSecondary,
		rmn.ProgressionWaitOnUserToCleanUp,
	}

	postRelocateProgressions := {
		ProgressionCompleted,
		ProgressionCleaningUp,
		ProgressionWaitingForResourceRestore,
		ProgressionWaitForReadiness,
		ProgressionUpdatedPlacement,
		ProgressionEnsuringVolSyncSetup,
		ProgressionSettingupVolsyncDest,
	}

specialProgressions are used to indicate special cases irrespective of action or initial deployment

	specialProgressions := {
		ProgressionDeleting,
		ProgressionActionPaused,
	}
*/
func (d *DRPCInstance) setProgression(nextProgression rmn.ProgressionStatus) {
	updateDRPCProgression(d.instance, nextProgression, d.log)
}

func IsPreRelocateProgression(status rmn.ProgressionStatus) bool {
	preRelocateProgressions := []rmn.ProgressionStatus{
		rmn.ProgressionPreparingFinalSync,
		rmn.ProgressionClearingPlacement,
		rmn.ProgressionRunningFinalSync,
		rmn.ProgressionFinalSyncComplete,
		rmn.ProgressionEnsuringVolumesAreSecondary,
	}

	return slices.Contains(preRelocateProgressions, status)
}

//nolint:cyclop
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

	clusterDecision := d.reconciler.getClusterDecision(d.userPlacement)
	if clusterDecision != nil && clusterDecision.ClusterName != "" {
		homeCluster = clusterDecision.ClusterName
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

	if vrg.Status.LastGroupSyncDuration != d.instance.Status.LastGroupSyncDuration {
		return true
	}

	if vrg.Status.LastGroupSyncBytes != d.instance.Status.LastGroupSyncBytes {
		return true
	}

	if vrg.Status.KubeObjectProtection.CaptureToRecoverFrom != nil {
		vrgKubeObjectProtectionTime := vrg.Status.KubeObjectProtection.CaptureToRecoverFrom.EndTime
		if !vrgKubeObjectProtectionTime.Equal(d.instance.Status.LastKubeObjectProtectionTime) {
			return true
		}
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

func (d *DRPCInstance) setConditionOnInitialDeploymentCompletion() {
	addOrUpdateCondition(&d.instance.Status.Conditions, rmn.ConditionAvailable, d.instance.Generation,
		d.getConditionStatusForTypeAvailable(), string(d.instance.Status.Phase), "Initial deployment completed")

	addOrUpdateCondition(&d.instance.Status.Conditions, rmn.ConditionPeerReady, d.instance.Generation,
		metav1.ConditionTrue, rmn.ReasonSuccess, "Ready")
}

func (d *DRPCInstance) setStatusInitiating() {
	if !(d.instance.Status.Phase == "" ||
		d.instance.Status.Phase == rmn.WaitForUser ||
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

func getCallerFunction(ancestorLevel int) string {
	// this is a util function and the caller is not going to count this
	// function in the skiplevel. Incrementing the skiplevel by 1
	ancestorLevel++

	pc, _, _, ok := goruntime.Caller(ancestorLevel)
	if !ok {
		return "unknown"
	}

	details := goruntime.FuncForPC(pc)
	if details == nil {
		return "unknown"
	}

	if !strings.Contains(details.Name(), "github.com/ramendr/ramen/internal/controller.") {
		return "unknown"
	}

	return strings.TrimPrefix(details.Name(), "github.com/ramendr/ramen/internal/controller.")
}
