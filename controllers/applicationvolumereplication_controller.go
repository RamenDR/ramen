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
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/ghodss/yaml"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	plrv1 "github.com/open-cluster-management/multicloud-operators-placementrule/pkg/apis/apps/v1"

	"github.com/go-logr/logr"
	errorswrapper "github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	volrep "github.com/csi-addons/volume-replication-operator/api/v1alpha1"
	rmn "github.com/ramendr/ramen/api/v1alpha1"
)

const (
	// ManifestWorkNameFormat is a formated a string used to generate the manifest name
	// The format is name-namespace-type-mw where:
	// - name is the AVR name
	// - namespace is the AVR namespace
	// - type is either "vrg", "pv", or "roles"
	ManifestWorkNameFormat string = "%s-%s-%s-mw"

	// ManifestWork VRG Type
	MWTypeVRG string = "vrg"

	// ManifestWork PV Type
	MWTypePV string = "pv"

	// ManifestWork Roles Type
	MWTypeRoles string = "roles"

	// Ramen scheduler
	RamenScheduler string = "ramen"

	// Label for VRG to indicate whether to delete it or not
	Deleting string = "deleting"
)

var ErrSameHomeCluster = errorswrapper.New("new home cluster is the same as current home cluster")

type PVDownloader interface {
	DownloadPVs(ctx context.Context, r client.Reader, objStoreGetter ObjectStoreGetter,
		s3Endpoint, s3Region string, s3SecretName types.NamespacedName,
		callerTag string, s3Bucket string) ([]corev1.PersistentVolume, error)
}

// ProgressCallback of function type
type ProgressCallback func(string, bool)

// ApplicationVolumeReplicationReconciler reconciles a ApplicationVolumeReplication object
type ApplicationVolumeReplicationReconciler struct {
	client.Client
	Log            logr.Logger
	PVDownloader   PVDownloader
	ObjStoreGetter ObjectStoreGetter
	Scheme         *runtime.Scheme
	Callback       ProgressCallback
}

func IsManifestInAppliedState(mw *ocmworkv1.ManifestWork) bool {
	applied := false
	degraded := false
	conditions := mw.Status.Conditions

	if len(conditions) > 0 {
		// get most recent conditions that have ConditionTrue status
		recentConditions := filterByConditionStatus(getMostRecentConditions(conditions), metav1.ConditionTrue)

		for _, condition := range recentConditions {
			if condition.Type == ocmworkv1.WorkApplied {
				applied = true
			} else if condition.Type == ocmworkv1.WorkDegraded {
				degraded = true
			}
		}

		// if most recent timestamp contains Applied and Degraded states, don't trust it's actually Applied
		if degraded {
			applied = false
		}
	}

	return applied
}

func filterByConditionStatus(conditions []metav1.Condition, status metav1.ConditionStatus) []metav1.Condition {
	filtered := make([]metav1.Condition, 0)

	for _, condition := range conditions {
		if condition.Status == status {
			filtered = append(filtered, condition)
		}
	}

	return filtered
}

// return Conditions with most recent timestamps only (allows duplicates)
func getMostRecentConditions(conditions []metav1.Condition) []metav1.Condition {
	recentConditions := make([]metav1.Condition, 0)

	// sort conditions by timestamp. Index 0 = most recent
	sort.Slice(conditions, func(a, b int) bool {
		return conditions[b].LastTransitionTime.Before(&conditions[a].LastTransitionTime)
	})

	mostRecentTimestamp := conditions[0].LastTransitionTime

	// loop through conditions until not in the most recent one anymore
	for index := range conditions {
		// only keep conditions with most recent timestamp
		if conditions[index].LastTransitionTime == mostRecentTimestamp {
			recentConditions = append(recentConditions, conditions[index])
		} else {
			break
		}
	}

	return recentConditions
}

func BuildManifestWorkName(name, namespace, mwType string) string {
	return fmt.Sprintf(ManifestWorkNameFormat, name, namespace, mwType)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ApplicationVolumeReplicationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	pred := predicate.GenerationChangedPredicate{}

	return ctrl.NewControllerManagedBy(mgr).
		For(&rmn.ApplicationVolumeReplication{}).
		WithEventFilter(pred).
		Complete(r)
}

//nolint:lll
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=applicationvolumereplications,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=applicationvolumereplications/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=applicationvolumereplications/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ApplicationVolumeReplication object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.7.0/pkg/reconcile
func (r *ApplicationVolumeReplicationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.Log.WithValues("AVR", req.NamespacedName)

	logger.Info("Entering reconcile loop")
	defer logger.Info("Exiting reconcile loop")

	avr := &rmn.ApplicationVolumeReplication{}

	err := r.Client.Get(ctx, req.NamespacedName, avr)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, errorswrapper.Wrap(err, "failed to get AVR object")
	}

	avrPlRule, userPlRule, err := r.getPlacementRules(ctx, avr)
	if err != nil {
		logger.Error(err, "failed to get PlacementRules")

		return ctrl.Result{}, err
	}

	// Make sure that we give time to the cloned PlacementRule to run and produces decisions
	if len(avrPlRule.Status.Decisions) == 0 {
		const initialWaitTime = 5

		return ctrl.Result{RequeueAfter: time.Second * initialWaitTime}, nil
	}

	// check if this is the initial deployement.  0 decision indicates that this is
	// the first time using this placement rule.
	if len(userPlRule.Status.Decisions) == 0 {
		updated, err := r.updateUserPlacementRule(avrPlRule, userPlRule)
		if err != nil {
			return ctrl.Result{}, err
		}

		if updated {
			const subWaitTime = 2
			// Wait for a moment to give a chance to the user PlacementRule to run
			return ctrl.Result{RequeueAfter: time.Second * subWaitTime}, nil
		}
	}

	a := AVRInstance{
		reconciler: r, ctx: ctx, log: logger, instance: avr, needStatusUpdate: false,
		userPlacementRule: userPlRule, avrPlacementRule: avrPlRule,
	}

	requeue := a.startProcessing()

	done := !requeue
	r.Callback(a.instance.Name, done)

	return ctrl.Result{Requeue: requeue}, nil
}

func (r *ApplicationVolumeReplicationReconciler) getPlacementRules(ctx context.Context,
	avr *rmn.ApplicationVolumeReplication) (*plrv1.PlacementRule, *plrv1.PlacementRule, error) {
	userPlRule, err := r.getUserPlacementRule(ctx, avr)
	if err != nil {
		return nil, nil, err
	}

	avrPlRule, err := r.getOrClonePlacementRule(ctx, avr, userPlRule)
	if err != nil {
		return nil, nil, err
	}

	return avrPlRule, userPlRule, nil
}

func (r *ApplicationVolumeReplicationReconciler) getUserPlacementRule(ctx context.Context,
	avr *rmn.ApplicationVolumeReplication) (*plrv1.PlacementRule, error) {
	r.Log.Info("Getting User PlacementRule", "placement", avr.Spec.Placement)

	if avr.Spec.Placement == nil || avr.Spec.Placement.PlacementRef == nil {
		return nil, fmt.Errorf("invalid user placementRule for AVR %s", avr.Name)
	}

	plRef := avr.Spec.Placement.PlacementRef

	if plRef.Namespace == "" {
		plRef.Namespace = avr.Namespace
	}

	userPlacementRule := &plrv1.PlacementRule{}

	err := r.Client.Get(ctx,
		types.NamespacedName{Name: plRef.Name, Namespace: plRef.Namespace}, userPlacementRule)
	if err != nil {
		return nil, fmt.Errorf("failed to get placementrule error: %w", err)
	}

	scName := userPlacementRule.Spec.SchedulerName
	if scName != "" && scName != RamenScheduler {
		return nil, fmt.Errorf("placementRule %s does not have the ramen scheduler. Scheduler used %s",
			userPlacementRule.Name, scName)
	}

	return userPlacementRule, nil
}

func (r *ApplicationVolumeReplicationReconciler) getOrClonePlacementRule(ctx context.Context,
	avr *rmn.ApplicationVolumeReplication, userPlRule *plrv1.PlacementRule) (*plrv1.PlacementRule, error) {
	r.Log.Info("Getting PlacementRule or cloning it", "placement", avr.Spec.Placement)

	clonedPlRule := &plrv1.PlacementRule{}
	clonedPlRuleName := fmt.Sprintf("%s-%s", userPlRule.Name, avr.Name)

	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      clonedPlRuleName,
		Namespace: userPlRule.Namespace,
	}, clonedPlRule)
	if err != nil {
		if errors.IsNotFound(err) {
			clonedPlRule, err = r.clonePlacementRule(ctx, avr, userPlRule, clonedPlRuleName)
			if err != nil {
				return nil, fmt.Errorf("failed to create cloned placementrule error: %w", err)
			}
		} else {
			r.Log.Error(err, "Failed to get avr placementRule", "name", clonedPlRuleName)

			return nil, fmt.Errorf("failed to get placementrule error: %w", err)
		}
	}

	return clonedPlRule, nil
}

func (r *ApplicationVolumeReplicationReconciler) clonePlacementRule(ctx context.Context,
	avr *rmn.ApplicationVolumeReplication, userPlRule *plrv1.PlacementRule,
	clonedPlRuleName string) (*plrv1.PlacementRule, error) {
	r.Log.Info("Creating a clone placementRule from", "name", userPlRule.Name)

	clonedPlRule := &plrv1.PlacementRule{}

	userPlRule.DeepCopyInto(clonedPlRule)

	clonedPlRule.Name = clonedPlRuleName
	clonedPlRule.ResourceVersion = ""
	clonedPlRule.Spec.SchedulerName = ""

	err := r.addClusterPeersToPlacementRule(ctx, avr, clonedPlRule)
	if err != nil {
		r.Log.Error(err, "Failed to add cluster peers to cloned placementRule", "name", clonedPlRuleName)

		return nil, err
	}

	err = r.Create(ctx, clonedPlRule)
	if err != nil {
		r.Log.Error(err, "failed to clone placement rule", "name", clonedPlRule.Name)

		return nil, errorswrapper.Wrap(err, "failed to coone PlacementRule")
	}

	return clonedPlRule, nil
}

func (r *ApplicationVolumeReplicationReconciler) addClusterPeersToPlacementRule(ctx context.Context,
	avr *rmn.ApplicationVolumeReplication, plRule *plrv1.PlacementRule) error {
	clPeersRef := avr.Spec.DRClusterPeersRef
	clusterPeers := &rmn.DRClusterPeers{}

	if clPeersRef.Namespace == "" {
		clPeersRef.Namespace = avr.Namespace
	}

	err := r.Client.Get(ctx,
		types.NamespacedName{Name: clPeersRef.Name, Namespace: clPeersRef.Namespace}, clusterPeers)
	if err != nil {
		return fmt.Errorf("failed to get cluster peers using %s/%s. Error (%w)",
			clPeersRef.Name, clPeersRef.Namespace, err)
	}

	if len(clusterPeers.Spec.ClusterNames) == 0 {
		return fmt.Errorf("invalid DRClusterPeers configuration. Name %s", clusterPeers.Name)
	}

	for idx := range clusterPeers.Spec.ClusterNames {
		plRule.Spec.Clusters = append(plRule.Spec.Clusters, plrv1.GenericClusterReference{
			Name: clusterPeers.Spec.ClusterNames[idx],
		})
	}

	return nil
}

func (r *ApplicationVolumeReplicationReconciler) updateUserPlacementRule(
	avrPlRule, userPlRule *plrv1.PlacementRule) (bool, error) {
	const updated = true

	if !reflect.DeepEqual(avrPlRule.Status.Decisions, userPlRule.Status.Decisions) {
		avrPlRule.Status.DeepCopyInto(&userPlRule.Status)

		r.Log.Info("Copied AVR PlacementRule Status into the User PlacementRule Status",
			"Decisions", userPlRule.Status.Decisions)

		if err := r.Status().Update(context.TODO(), userPlRule); err != nil {
			return !updated, errorswrapper.Wrap(err, "failed to update userPlRule")
		}

		return updated, nil
	}

	return !updated, nil
}

type AVRInstance struct {
	reconciler        *ApplicationVolumeReplicationReconciler
	ctx               context.Context
	log               logr.Logger
	instance          *rmn.ApplicationVolumeReplication
	needStatusUpdate  bool
	userPlacementRule *plrv1.PlacementRule
	avrPlacementRule  *plrv1.PlacementRule
}

func (a *AVRInstance) startProcessing() bool {
	a.log.Info("Starting to process placement for AVR", "name", a.instance.Name)
	requeue := a.processPlacement()

	if a.needStatusUpdate {
		if err := a.updateAVRStatus(); err != nil {
			a.log.Error(err, "failed to update status")

			requeue = true
		}
	}

	// Now clean up the VRG in ReplicationState secondary
	// TODO: Watch for ManagedClusterView changes and execute this block instead.
	a.log.Info("Cleaning up VRG ManifestWork with ReplicationState as 'secondary'")
	err := a.cleanupSecondaryVRGManifestWork()
	if err != nil {
		a.log.Info(fmt.Sprintf("Failed: %v", err))
	}

	a.log.Info("Completed processing placement for AVR", "name", a.instance.Name, "requeue", requeue)

	return requeue
}

func (a *AVRInstance) processPlacement() bool {
	a.log.Info("Process AVR Placement", "name", a.avrPlacementRule)

	requeue := true

	done, err := a.runPrerequisitesForUserAction()
	if err != nil {
		a.log.Error(err, "processPlacement")

		return requeue
	}

	if !done {
		a.log.Info("Ran prerequisites but we'll wait until all outstanding operations complete")

		return requeue
	}

	// Running prerequisits is complete.  That also mean that ManifestWork is applied as well.
	// Now try to update user placementRule
	neededUpdate, err := a.reconciler.updateUserPlacementRule(a.avrPlacementRule, a.userPlacementRule)
	if err != nil {
		a.log.Error(err, "processPlacement")

		return requeue
	}

	// If we needed to update user placement rule, then we need to wait for it
	// to have a chance to run. Hence, requeueing
	if neededUpdate {
		a.log.Info("PlacementRule was updated, but we'll give it a chance to run", "PlRuleDecisions",
			a.userPlacementRule.Status.Decisions)

		return requeue
	}

	requeue = a.createVRGManifest()

	a.log.Info(fmt.Sprintf("AVR (%+v)", a.instance))
	a.log.Info(fmt.Sprintf("Made a decision %+v. Requeue? %v", a.instance.Status.Decision, requeue))

	a.advanceToNextDRState()

	return requeue
}

func (a *AVRInstance) runPrerequisitesForUserAction() (bool, error) {
	switch a.instance.Spec.Action {
	case rmn.ActionFailover:
		return a.runFailoverPrerequisites()
	case rmn.ActionFailback:
		return a.runFailbackPrerequisites()
	case rmn.ActionRelocate:
		return a.runRelocatePrerequisites()
	}

	// Not a failover, a failback, or a relocation.  Must be an initial deployement.
	a.resetDRState()

	return true, nil
}

func (a *AVRInstance) runFailoverPrerequisites() (bool, error) {
	a.log.Info("Processing prerequisites for a failover", "Last State", a.instance.Status.LastKnownDRState)
	a.setDRState(rmn.FailingOver)

	return a.runPrerequisites(a.instance.Spec.FailoverCluster)
}

func (a *AVRInstance) runFailbackPrerequisites() (bool, error) {
	a.log.Info("Processing prerequisites for a failback", "last State", a.instance.Status.LastKnownDRState)
	a.setDRState(rmn.FailingBack)

	return a.runPrerequisites(a.instance.Spec.PreferredCluster)
}

func (a *AVRInstance) runRelocatePrerequisites() (bool, error) {
	a.log.Info("Processing prerequisites for a relocation", "last State", a.instance.Status.LastKnownDRState)

	// TODO: implement relocation
	return true, nil
}

func (a *AVRInstance) runPrerequisites(targetCluster string) (bool, error) {
	const done = true

	newHomeCluster, err := a.findNextHomeCluster(targetCluster)
	if err != nil {
		if errorswrapper.Is(err, ErrSameHomeCluster) {
			return done, nil
		}

		return !done, err
	}

	mwName := BuildManifestWorkName(a.instance.Name, a.instance.Namespace, MWTypePV)

	// Try to find whether we have already created a ManifestWork for this
	pvMW, err := a.findManifestWork(mwName, newHomeCluster)
	if err != nil {
		a.log.Error(err, "Failed to find 'PV restore' ManifestWork")

		return !done, err
	}

	if pvMW != nil {
		a.log.Info(fmt.Sprintf("Found manifest work (%v)", pvMW))

		if !IsManifestInAppliedState(pvMW) {
			return !done, nil
		}

		a.log.Info(fmt.Sprintf("ManifestWork %s/%s in Applied state", pvMW.Namespace, pvMW.Name))

		return done, nil
	}

	// Always return NOT done whether restore succeeds or not.
	// We want the MW to be in Applied state before we proceed to creating the VRG MW
	return !done, a.cleanupAndRestore(newHomeCluster)
}

func (a *AVRInstance) cleanupAndRestore(newHomeCluster string) error {
	a.log.Info("Using new home cluster", "name", newHomeCluster)
	// cleanup old manifests
	err := a.cleanup()
	if err != nil {
		return err
	}

	// Restore PVs to the new home cluster
	return a.restore(newHomeCluster)
}

func (a *AVRInstance) cleanup() error {
	// first delete the MW for PVs
	pvMWName := BuildManifestWorkName(a.instance.Name, a.instance.Namespace, MWTypePV)

	err := a.deleteExistingManifestWork(pvMWName)
	if err != nil {
		a.log.Error(err, "Failed to delete existing PV manifestwork")

		return err
	}

	// Next, Update VRG to secondary
	vrgMWName := BuildManifestWorkName(a.instance.Name, a.instance.Namespace, MWTypeVRG)

	err = a.updateVRGToSecondary(vrgMWName)
	if err != nil {
		a.log.Error(err, "Failed to update existing VRG manifestwork")

		return err
	}

	return nil
}

func (a *AVRInstance) restore(newHomeCluster string) error {
	// Restore from PV backup location
	err := a.restorePVFromBackup(newHomeCluster)
	if err != nil {
		a.log.Error(err, "Failed to restore PVs from backup")

		return err
	}

	return nil
}

func (a *AVRInstance) createVRGManifest() bool {
	a.log.Info("Processing deployment", "Last State", a.instance.Status.LastKnownDRState)

	const requeue = true

	homeCluster, peerCluster, err := a.extractHomeClusterAndPeerCluster()
	if err != nil {
		a.log.Info(fmt.Sprintf("Unable to select placement decision (%v)", err))

		return requeue
	}

	mw, err := a.vrgManifestWorkAlreadyExists(homeCluster)
	if err != nil {
		return requeue
	}

	if mw != nil {
		primary, err := a.isVRGPrimary(mw)
		if err != nil {
			a.log.Error(err, "Failed to check whether the VRG is primary or not", "mw", mw.Name)

			return requeue
		}
		if primary {
			// Found MW and the VRG already a primary
			return !requeue
		}
	}

	// VRG ManifestWork does not exist, start the process to create it
	placementDecision, err := a.processVRGManifestWork(homeCluster, peerCluster)
	if err != nil {
		a.log.Error(err, "Failed to process VRG ManifestWork", "avr", a.instance.Name)

		return requeue
	}

	a.log.Info(fmt.Sprintf("placementDecisions %+v - requeue: %t", placementDecision, !requeue))

	a.instance.Status.Decision = placementDecision
	a.needStatusUpdate = true

	return !requeue
}

func (a *AVRInstance) vrgManifestWorkAlreadyExists(homeCluster string) (*ocmworkv1.ManifestWork, error) {
	if a.instance.Status.Decision == (rmn.PlacementDecision{}) {
		return nil, nil
	}

	mwName := BuildManifestWorkName(a.instance.Name, a.instance.Namespace, MWTypeVRG)

	const exists = true

	mw, err := a.findManifestWork(mwName, homeCluster)
	if err != nil {
		a.log.Error(err, "failed to find ManifestWork")

		return nil, err
	}

	if mw == nil {
		a.log.Info(fmt.Sprintf("Mainifestwork (%s) does not exist", mwName))

		return nil, nil
	}

	a.log.Info(fmt.Sprintf("Mainifestwork exists (%v)", mw))

	return mw, nil
}

func (a *AVRInstance) findManifestWork(mwName, homeCluster string) (*ocmworkv1.ManifestWork, error) {
	if homeCluster != "" {
		mw := &ocmworkv1.ManifestWork{}

		err := a.reconciler.Get(a.ctx, types.NamespacedName{Name: mwName, Namespace: homeCluster}, mw)
		if err != nil {
			if errors.IsNotFound(err) {
				return nil, nil
			}

			return nil, errorswrapper.Wrap(err, "failed to retrieve manifestwork")
		}

		return mw, nil
	}

	return nil, nil
}

func (a *AVRInstance) isVRGPrimary(mw *ocmworkv1.ManifestWork) (bool, error) {
	vrgClientManifest := &mw.Spec.Workload.Manifests[0]
	vrg := &rmn.VolumeReplicationGroup{}

	err := yaml.Unmarshal(vrgClientManifest.RawExtension.Raw, &vrg)
	if err != nil {
		return false, errorswrapper.Wrap(err, fmt.Sprintf("unable to update VRG object %s as secondary", vrg.Name))
	}

	return (vrg.Spec.ReplicationState == volrep.Primary), nil
}

func (a *AVRInstance) deleteExistingManifestWork(mwName string) error {
	a.log.Info("Try to delete existing ManifestWork")

	if a.instance.Status.Decision == (rmn.PlacementDecision{}) {
		return nil
	}

	d := a.instance.Status.Decision
	mw := &ocmworkv1.ManifestWork{}

	err := a.reconciler.Get(a.ctx, types.NamespacedName{Name: mwName, Namespace: d.HomeCluster}, mw)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}

		return fmt.Errorf("failed to retrieve manifestwork for type: %s. Error: %w", mwName, err)
	}

	a.log.Info("deleting ManifestWork", "name", mw.Name)

	return a.reconciler.Delete(a.ctx, mw)
}

func (a *AVRInstance) restorePVFromBackup(homeCluster string) error {
	a.log.Info("Restoring PVs to new managed cluster", "name", homeCluster)

	pvList, err := a.listPVsFromS3Store()
	if err != nil {
		return errorswrapper.Wrap(err, "failed to retrieve PVs from S3 store")
	}

	a.log.Info(fmt.Sprintf("Found %d PVs", len(pvList)))

	if len(pvList) == 0 {
		return nil
	}

	// Create manifestwork for all PVs for this AVR
	return a.createOrUpdatePVsManifestWork(a.instance.Name, a.instance.Namespace, homeCluster, pvList)
}

func (a *AVRInstance) createOrUpdatePVsManifestWork(
	name string, namespace string, homeClusterName string, pvList []corev1.PersistentVolume) error {
	a.log.Info("Creating manifest work for PVs", "AVR",
		name, "cluster", homeClusterName, "PV count", len(pvList))

	mwName := BuildManifestWorkName(name, namespace, MWTypePV)

	manifestWork, err := a.generatePVManifestWork(mwName, homeClusterName, pvList)
	if err != nil {
		return err
	}

	return a.createOrUpdateManifestWork(manifestWork, homeClusterName)
}

func (a *AVRInstance) processVRGManifestWork(homeCluster, peerCluster string) (rmn.PlacementDecision, error) {
	a.log.Info("Processing VRG ManifestWork")

	if err := a.createOrUpdateVRGRolesManifestWork(homeCluster); err != nil {
		a.log.Error(err, "failed to create or update VolumeReplicationGroup Roles manifest")

		return rmn.PlacementDecision{}, err
	}

	if err := a.createOrUpdateVRGManifestWork(
		a.instance.Name, a.instance.Namespace,
		homeCluster, a.instance.Spec.S3Endpoint, a.instance.Spec.S3SecretName, a.instance.Spec.PVCSelector); err != nil {
		a.log.Error(err, "failed to create or update VolumeReplicationGroup manifest")

		return rmn.PlacementDecision{}, err
	}

	preferredHomeCluster := ""

	d := a.instance.Status.Decision
	if d.PreferredHomeCluster == "" {
		preferredHomeCluster = d.HomeCluster
	}

	return rmn.PlacementDecision{
		HomeCluster:          homeCluster,
		PeerCluster:          peerCluster,
		PreferredHomeCluster: preferredHomeCluster,
	}, nil
}

func (a *AVRInstance) findNextHomeCluster(targetCluster string) (string, error) {
	var newHomeCluster string

	if targetCluster != "" {
		newHomeCluster = targetCluster
	} else {
		homeCluster, err := a.findHomeClusterFromAVRPlRule()
		if err != nil {
			a.log.Error(err, "Failed to find new home cluster")

			return "", err
		}

		newHomeCluster = homeCluster
	}

	newHomeCluster, err := a.validateHomeClusterSelection(newHomeCluster)
	if err != nil {
		a.log.Info("Failed to validate new home cluster selection", "newHomeCluster", newHomeCluster, "errMsg", err.Error())

		return "", err
	}

	return newHomeCluster, nil
}

func (a *AVRInstance) findHomeClusterFromAVRPlRule() (string, error) {
	a.log.Info("Finding the next home cluster from placementRule", "name", a.avrPlacementRule.Name)

	if len(a.avrPlacementRule.Status.Decisions) == 0 {
		return "", fmt.Errorf("no decisions were found in placementRule %s", a.avrPlacementRule.Name)
	}

	const requiredClusterReplicas = 1

	if a.avrPlacementRule.Spec.ClusterReplicas != nil &&
		*a.avrPlacementRule.Spec.ClusterReplicas != requiredClusterReplicas {
		return "", fmt.Errorf("PlacementRule %s Required cluster replicas %d != %d",
			a.avrPlacementRule.Name, requiredClusterReplicas, *a.avrPlacementRule.Spec.ClusterReplicas)
	}

	return a.avrPlacementRule.Status.Decisions[0].ClusterName, nil
}

func (a *AVRInstance) validateHomeClusterSelection(newHomeCluster string) (string, error) {
	if a.instance.Status.Decision == (rmn.PlacementDecision{}) {
		return newHomeCluster, nil
	}

	action := a.instance.Spec.Action
	if action == "" {
		return newHomeCluster, fmt.Errorf("action not set for AVR %s", a.instance.Name)
	}

	switch action {
	case rmn.ActionFailover:
		return a.validateFailover(newHomeCluster)
	case rmn.ActionFailback:
		return a.validateFailback(newHomeCluster)
	case rmn.ActionRelocate:
		return a.validateRelocation(newHomeCluster)
	default:
		return newHomeCluster, fmt.Errorf("unknown action %s", action)
	}
}

func (a *AVRInstance) validateFailover(newHomeCluster string) (string, error) {
	// If FailoverCluster is not empty, then don't validate anything.  Just use it.
	if a.instance.Spec.FailoverCluster != "" {
		if newHomeCluster != a.instance.Spec.FailoverCluster {
			return newHomeCluster, fmt.Errorf("new HomeCluster %s not the same as the configured FailoverCluster %s",
				newHomeCluster, a.instance.Spec.FailoverCluster)
		}

		return newHomeCluster, nil
	}

	if d := a.instance.Status.Decision; d != (rmn.PlacementDecision{}) {
		switch newHomeCluster {
		case d.PeerCluster:
			return newHomeCluster, nil
		case d.HomeCluster:
			return newHomeCluster, errorswrapper.Wrap(ErrSameHomeCluster, newHomeCluster)
		case d.PreferredHomeCluster:
			return newHomeCluster,
				fmt.Errorf("miconfiguration detected on failover! (n:%s,p:%s)", newHomeCluster, d.PreferredHomeCluster)
		}
	}

	return newHomeCluster, fmt.Errorf("unknown error (n:%s,avrStatus:%v)", newHomeCluster, a.instance.Status)
}

func (a *AVRInstance) validateFailback(newHomeCluster string) (string, error) {
	if d := a.instance.Status.Decision; d != (rmn.PlacementDecision{}) {
		switch newHomeCluster {
		case d.PreferredHomeCluster:
			return newHomeCluster, nil
		case d.HomeCluster:
			return newHomeCluster, errorswrapper.Wrap(ErrSameHomeCluster, newHomeCluster)
		case d.PeerCluster:
			return "", fmt.Errorf("miconfiguration detected on failback! (n:%s,p:%s)", newHomeCluster, d.PreferredHomeCluster)
		}
	}

	return "", fmt.Errorf("unknown error (n:%s,avrStatus:%v)", newHomeCluster, a.instance.Status)
}

func (a *AVRInstance) validateRelocation(newHomeCluster string) (string, error) {
	// TODO: impelement relocation validation
	return newHomeCluster, nil
}

func (a *AVRInstance) extractHomeClusterAndPeerCluster() (string, string, error) {
	const empty = ""

	a.log.Info("Extracting home and peer clusters", "PlRuleName", a.avrPlacementRule,
		"UserPlRule", a.userPlacementRule.Name)

	clusterPeers := &rmn.DRClusterPeers{}
	clPeersRef := a.instance.Spec.DRClusterPeersRef

	if clPeersRef.Namespace == "" {
		clPeersRef.Namespace = a.instance.Namespace
	}

	err := a.reconciler.Get(a.ctx,
		types.NamespacedName{
			Name:      clPeersRef.Name,
			Namespace: clPeersRef.Namespace,
		}, clusterPeers)
	if err != nil {
		return empty, empty,
			fmt.Errorf("failed to retrieve DRClusterPeers %s (%w)",
				a.instance.Spec.DRClusterPeersRef.Name, err)
	}

	if len(clusterPeers.Spec.ClusterNames) == 0 {
		return empty, empty, fmt.Errorf("no clusters configured in DRClusterPeers %s", clusterPeers.Name)
	}

	cl1 := clusterPeers.Spec.ClusterNames[0]
	cl2 := clusterPeers.Spec.ClusterNames[1]

	var homeCluster string

	var peerCluster string

	targetCluster := a.avrPlacementRule.Status.Decisions[0].ClusterName

	switch {
	case cl1 == targetCluster:
		homeCluster = cl1
		peerCluster = cl2
	case cl2 == targetCluster:
		homeCluster = cl2
		peerCluster = cl1
	default:
		return empty, empty, fmt.Errorf("PlacementRule %s has no destionation matching DRClusterPeers %v - SubStatuses %v",
			a.avrPlacementRule.Name, clusterPeers.Spec.ClusterNames, a.avrPlacementRule.Status.Decisions)
	}

	return homeCluster, peerCluster, nil
}

func (a *AVRInstance) createOrUpdateVRGRolesManifestWork(namespace string) error {
	manifestWork, err := a.generateVRGRolesManifestWork(namespace)
	if err != nil {
		return err
	}

	return a.createOrUpdateManifestWork(manifestWork, namespace)
}

func (a *AVRInstance) createOrUpdateVRGManifestWork(
	name, namespace, homeCluster, s3Endpoint, s3SecretName string, pvcSelector metav1.LabelSelector) error {
	a.log.Info(fmt.Sprintf("Create or Update manifestwork %s:%s:%s:%s:%s",
		name, namespace, homeCluster, s3Endpoint, s3SecretName))

	manifestWork, err := a.generateVRGManifestWork(name, namespace, homeCluster, s3Endpoint, s3SecretName, pvcSelector)
	if err != nil {
		return err
	}

	return a.createOrUpdateManifestWork(manifestWork, homeCluster)
}

func (a *AVRInstance) generateVRGRolesManifestWork(namespace string) (*ocmworkv1.ManifestWork, error) {
	vrgClusterRole, err := a.generateVRGClusterRoleManifest()
	if err != nil {
		a.log.Error(err, "failed to generate VolumeReplicationGroup ClusterRole manifest")

		return nil, err
	}

	vrgClusterRoleBinding, err := a.generateVRGClusterRoleBindingManifest()
	if err != nil {
		a.log.Error(err, "failed to generate VolumeReplicationGroup ClusterRoleBinding manifest")

		return nil, err
	}

	manifests := []ocmworkv1.Manifest{*vrgClusterRole, *vrgClusterRoleBinding}

	return a.newManifestWork(
		"ramendr-vrg-roles",
		namespace,
		map[string]string{},
		manifests), nil
}

func (a *AVRInstance) generateVRGClusterRoleManifest() (*ocmworkv1.Manifest, error) {
	return a.generateManifest(&rbacv1.ClusterRole{
		TypeMeta:   metav1.TypeMeta{Kind: "ClusterRole", APIVersion: "rbac.authorization.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "open-cluster-management:klusterlet-work-sa:agent:volrepgroup-edit"},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"ramendr.openshift.io"},
				Resources: []string{"volumereplicationgroups"},
				Verbs:     []string{"create", "get", "list", "update", "delete"},
			},
		},
	})
}

func (a *AVRInstance) generateVRGClusterRoleBindingManifest() (*ocmworkv1.Manifest, error) {
	return a.generateManifest(&rbacv1.ClusterRoleBinding{
		TypeMeta:   metav1.TypeMeta{Kind: "ClusterRoleBinding", APIVersion: "rbac.authorization.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "open-cluster-management:klusterlet-work-sa:agent:volrepgroup-edit"},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "klusterlet-work-sa",
				Namespace: "open-cluster-management-agent",
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "open-cluster-management:klusterlet-work-sa:agent:volrepgroup-edit",
		},
	})
}

func (a *AVRInstance) generatePVManifestWork(
	mwName, homeClusterName string, pvList []corev1.PersistentVolume) (*ocmworkv1.ManifestWork, error) {
	manifests, err := a.generatePVManifest(pvList)
	if err != nil {
		return nil, err
	}

	return a.newManifestWork(
		mwName,
		homeClusterName,
		map[string]string{"app": "PV"},
		manifests), nil
}

// This function follow a slightly different pattern than the rest, simply because the pvList that come
// from the S3 store will contain PV objects already converted to a string.
func (a *AVRInstance) generatePVManifest(
	pvList []corev1.PersistentVolume) ([]ocmworkv1.Manifest, error) {
	manifests := []ocmworkv1.Manifest{}

	for _, pv := range pvList {
		pvClientManifest, err := a.generateManifest(pv)
		// Either all succeed or none
		if err != nil {
			a.log.Error(err, "failed to generate manifest for PV")

			return nil, err
		}

		manifests = append(manifests, *pvClientManifest)
	}

	return manifests, nil
}

func (a *AVRInstance) generateVRGManifestWork(
	name, namespace, homeCluster, s3Endpoint, s3SecretName string,
	pvcSelector metav1.LabelSelector) (*ocmworkv1.ManifestWork, error) {
	vrgClientManifest, err := a.generateVRGManifest(name, namespace, s3Endpoint, s3SecretName, pvcSelector)
	if err != nil {
		a.log.Error(err, "failed to generate VolumeReplicationGroup manifest")

		return nil, err
	}

	manifests := []ocmworkv1.Manifest{*vrgClientManifest}

	return a.newManifestWork(
		fmt.Sprintf(ManifestWorkNameFormat, name, namespace, MWTypeVRG),
		homeCluster,
		map[string]string{"app": "VRG"},
		manifests), nil
}

func (a *AVRInstance) generateVRGManifest(
	name, namespace, s3Endpoint, s3SecretName string, pvcSelector metav1.LabelSelector) (*ocmworkv1.Manifest, error) {
	return a.generateManifest(&rmn.VolumeReplicationGroup{
		TypeMeta:   metav1.TypeMeta{Kind: "VolumeReplicationGroup", APIVersion: "ramendr.openshift.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: rmn.VolumeReplicationGroupSpec{
			PVCSelector:            pvcSelector,
			VolumeReplicationClass: "volume-rep-class",
			ReplicationState:       volrep.Primary,
			S3Endpoint:             s3Endpoint,
			S3SecretName:           s3SecretName,
		},
	})
}

func (a *AVRInstance) generateManifest(obj interface{}) (*ocmworkv1.Manifest, error) {
	objJSON, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal %v to JSON, error %w", obj, err)
	}

	manifest := &ocmworkv1.Manifest{}
	manifest.RawExtension = runtime.RawExtension{Raw: objJSON}

	return manifest, nil
}

func (a *AVRInstance) newManifestWork(name string, mcNamespace string,
	labels map[string]string, manifests []ocmworkv1.Manifest) *ocmworkv1.ManifestWork {
	return &ocmworkv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: mcNamespace, Labels: labels,
		},
		Spec: ocmworkv1.ManifestWorkSpec{
			Workload: ocmworkv1.ManifestsTemplate{
				Manifests: manifests,
			},
		},
	}
}

func (a *AVRInstance) createOrUpdateManifestWork(
	mw *ocmworkv1.ManifestWork,
	managedClusternamespace string) error {
	foundMW := &ocmworkv1.ManifestWork{}

	err := a.reconciler.Get(a.ctx,
		types.NamespacedName{Name: mw.Name, Namespace: managedClusternamespace},
		foundMW)
	if err != nil {
		if !errors.IsNotFound(err) {
			return errorswrapper.Wrap(err, fmt.Sprintf("failed to fetch ManifestWork %s", mw.Name))
		}

		// Let AVR receive notification for any changes to ManifestWork CR created by it.
		// if err := ctrl.SetControllerReference(a.instance, mw, a.reconciler.Scheme); err != nil {
		// 	return fmt.Errorf("failed to set owner reference to ManifestWork resource (%s/%s) (%v)",
		// 		mw.Name, mw.Namespace, err)
		// }

		a.log.Info("Creating ManifestWork", "MW", mw)

		return a.reconciler.Create(a.ctx, mw)
	}

	if !reflect.DeepEqual(foundMW.Spec, mw.Spec) {
		mw.Spec.DeepCopyInto(&foundMW.Spec)

		a.log.Info("ManifestWork exists.", "MW", mw)

		return a.reconciler.Update(a.ctx, foundMW)
	}

	return nil
}

func (a *AVRInstance) updateVRGToSecondary(mwName string) error {
	a.log.Info("Update ManifestWork", "name", mwName)

	if a.instance.Status.Decision == (rmn.PlacementDecision{}) {
		return nil
	}

	d := a.instance.Status.Decision
	mw := &ocmworkv1.ManifestWork{}

	err := a.reconciler.Get(a.ctx, types.NamespacedName{Name: mwName, Namespace: d.HomeCluster}, mw)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}

		return fmt.Errorf("failed to retrieve manifestwork for type: %s. Error: %w", mwName, err)
	}

	a.log.Info("Found ManifestWork. Update the VRG manifest", "name", mw.Name)

	if len(mw.Spec.Workload.Manifests) == 0 {
		return fmt.Errorf("invalid VRG ManifestWork for type: %s", mwName)
	}

	vrgClientManifest := &mw.Spec.Workload.Manifests[0]
	vrg := &rmn.VolumeReplicationGroup{}

	err = yaml.Unmarshal(vrgClientManifest.RawExtension.Raw, &vrg)
	if err != nil {
		return errorswrapper.Wrap(err, fmt.Sprintf("unable to update VRG object %s as secondary", vrg.Name))
	}

	vrgLabels := vrg.GetLabels()
	if vrgLabels == nil {
		vrgLabels = make(map[string]string)
	}

	vrg.Spec.ReplicationState = volrep.Secondary

	vrgClientManifest, err = a.generateManifest(vrg)
	if err != nil {
		a.log.Error(err, "failed to generate VolumeReplication")

		return err
	}

	a.log.Info(fmt.Sprintf("VRG Manifest %v", vrg))

	mw.Spec.Workload.Manifests[0] = *vrgClientManifest

	mwLabels := mw.GetLabels()
	if mwLabels == nil {
		mwLabels = make(map[string]string)
	}

	// Now that the VRG will be secondary, Labeling the MW for deleting will cause
	// two things, 1) the VRG will switch from primary to secondary, and once that
	// complete, 2) delete the MW which cause the VRG in secondary to be also deleted.
	mwLabels[Deleting] = "true"
	mw.SetLabels(mwLabels)

	return a.reconciler.Update(a.ctx, mw)
}

func (a *AVRInstance) updateAVRStatus() error {
	a.log.Info("Updating AVR status")

	a.instance.Status.LastUpdateTime = metav1.Now()

	if err := a.reconciler.Status().Update(a.ctx, a.instance); err != nil {
		return errorswrapper.Wrap(err, "failed to update AVR status")
	}

	a.log.Info(fmt.Sprintf("Updated AVR Status %+v", a.instance.Status))

	return nil
}

func (a *AVRInstance) listPVsFromS3Store() ([]corev1.PersistentVolume, error) {
	s3SecretLookupKey := types.NamespacedName{
		Name:      a.instance.Spec.S3SecretName,
		Namespace: a.instance.Namespace,
	}

	s3Bucket := constructBucketName(a.instance.Namespace, a.instance.Name)

	return a.reconciler.PVDownloader.DownloadPVs(
		a.ctx, a.reconciler.Client, a.reconciler.ObjStoreGetter, a.instance.Spec.S3Endpoint, a.instance.Spec.S3Region,
		s3SecretLookupKey, a.instance.Name, s3Bucket)
}

type ObjectStorePVDownloader struct{}

func (s ObjectStorePVDownloader) DownloadPVs(ctx context.Context, r client.Reader,
	objStoreGetter ObjectStoreGetter, s3Endpoint, s3Region string, s3SecretName types.NamespacedName,
	callerTag string, s3Bucket string) ([]corev1.PersistentVolume, error) {
	objectStore, err := objStoreGetter.objectStore(ctx, r, s3Endpoint, s3Region, s3SecretName, callerTag)
	if err != nil {
		return nil, fmt.Errorf("error when downloading PVs, err %w", err)
	}

	return objectStore.downloadPVs(s3Bucket)
}

func (a *AVRInstance) advanceToNextDRState() {
	var nextState rmn.DRState

	switch a.instance.Status.LastKnownDRState {
	case rmn.DRState(""):
		nextState = rmn.Initial
	case rmn.FailingOver:
		nextState = rmn.FailedOver
	case rmn.FailingBack:
		nextState = rmn.FailedBack
	case rmn.Relocating:
		nextState = rmn.Relocated
	case rmn.Initial:
	case rmn.FailedOver:
	case rmn.FailedBack:
	case rmn.Relocated:
	default:
		nextState = rmn.DRState("")
	}

	a.setDRState(nextState)
}

func (a *AVRInstance) resetDRState() {
	a.log.Info("Resetting last known DR state", "lndrs", a.instance.Status.LastKnownDRState)

	a.setDRState(rmn.DRState(""))
}

func (a *AVRInstance) setDRState(nextState rmn.DRState) {
	if a.instance.Status.LastKnownDRState != nextState {
		a.log.Info(fmt.Sprintf("LastKnownDRState: curState '%s' - nextState '%s'",
			a.instance.Status.LastKnownDRState, nextState))

		a.instance.Status.LastKnownDRState = nextState
		a.needStatusUpdate = true
	}
}

func (a *AVRInstance) cleanupSecondaryVRGManifestWork() error {
	namereq := metav1.LabelSelectorRequirement{}
	namereq.Key = Deleting
	namereq.Operator = metav1.LabelSelectorOpIn
	namereq.Values = append(namereq.Values, "true")

	labelSelector := &metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{namereq},
	}

	a.log.Info(fmt.Sprintf("Using ManifestWork LabelSelector %v", labelSelector))

	mwSelector, err := metav1.LabelSelectorAsSelector(labelSelector)
	if err != nil {
		return err
	}

	mwList := &ocmworkv1.ManifestWorkList{}

	err = a.reconciler.List(context.TODO(), mwList, &client.ListOptions{LabelSelector: mwSelector})

	if err != nil && !errors.IsNotFound(err) {
		a.log.Error(err, "error listing ManifestWorks")

		return err
	}

	for _, mw := range mwList.Items {
		mwName := BuildManifestWorkName(a.instance.Name, a.instance.Namespace, MWTypeVRG)
		if mwName == mw.Name {
			// TODO: Use ManagedClusterView to check whether the VRG is ready for deletion
			// TODO: call deleteExistingManifestWork once ManagedClusterView confirms that
			// the VRG is in secondary state
			a.log.Info("Deleting ManifestWork", "MW Name", mwName)
			break
		}
	}

	return nil
}
