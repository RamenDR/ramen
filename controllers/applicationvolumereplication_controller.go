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
	"strings"

	spokeClusterV1 "github.com/open-cluster-management/api/cluster/v1"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	plrv1 "github.com/open-cluster-management/multicloud-operators-placementrule/pkg/apis/apps/v1"
	"github.com/open-cluster-management/multicloud-operators-placementrule/pkg/utils"
	subv1 "github.com/open-cluster-management/multicloud-operators-subscription/pkg/apis/apps/v1"
	fndv2 "github.com/tjanssen3/multicloud-operators-foundation/v2/pkg/apis/view/v1beta1"

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

	rmn "github.com/ramendr/ramen/api/v1alpha1"
)

const (
	// RamenDRLabelName is the label used to pause/unpause a subsription
	RamenDRLabelName string = "ramendr"

	// ManifestWorkNameFormat is a formated a string used to generate the manifest name
	// The format is name-namespace-type-mw where:
	// - name is the subscription name
	// - namespace is the subscription namespace
	// - type is either vrg OR pv string
	ManifestWorkNameFormat string = "%s-%s-%s-mw"

	// ManifestWork VRG Type
	MWTypeVRG string = "vrg"

	// ManifestWork PV Type
	MWTypePV string = "pv"

	// ManifestWork Roles Type
	MWTypeRoles string = "roles"
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

	if len(conditions) > 0 {
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
	logger := r.Log.WithValues("ApplicationVolumeReplication", req.NamespacedName)
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

	subscriptionList := &subv1.SubscriptionList{}
	listOptions := []client.ListOption{
		client.InNamespace(avr.Namespace),
		client.MatchingLabels(avr.Spec.SubscriptionSelector.MatchLabels),
	}

	err = r.Client.List(ctx, subscriptionList, listOptions...)
	if err != nil {
		if !errors.IsNotFound(err) {
			logger.Error(err, "failed to find subscription list", "namespace", avr.Namespace)

			return ctrl.Result{Requeue: true}, nil
		}

		return ctrl.Result{}, errorswrapper.Wrap(err, "failed to list subscriptions")
	}

	a := AVRInstance{
		reconciler:       r,
		ctx:              ctx,
		log:              logger,
		instance:         avr,
		subscriptionList: subscriptionList,
	}

	requeue := a.processSubscriptions()

	if err := a.updateAVRStatus(); err != nil {
		logger.Error(err, "failed to update status")

		return ctrl.Result{Requeue: true}, nil
	}

	r.Log.Info(fmt.Sprintf("AVR (%+v)", avr))

	logger.Info(fmt.Sprintf("Processed %d/%d subscriptions. AVR Status Decisions %d. Requeue? %v",
		len(a.instance.Status.Decisions), len(subscriptionList.Items), len(avr.Status.Decisions), requeue))

	done := !requeue
	r.Callback(a.instance.Name, done)

	return ctrl.Result{Requeue: requeue}, nil
}

type AVRInstance struct {
	reconciler       *ApplicationVolumeReplicationReconciler
	ctx              context.Context
	log              logr.Logger
	instance         *rmn.ApplicationVolumeReplication
	subscriptionList *subv1.SubscriptionList
}

// For each subscription
//		Check if it is paused for failover
//			- restore PVs to the failed over cluster
// 			- unpause
//          - go to next subscription
//		otherwise, select placement decisions
//			- extract home cluster from placementrule.status.decisions
//			- extract peer cluster from the clusters forming the dr pair
//				example: ManagedCluster Set {A, B, C, D}
//						 Pl.GenericPlacementField results in DR_Set = {A, B}
//						 plRule{Status.Decision=A}
//						 homeCluster = A
//						 peerCluster = (DR_Set - A) = B
//		create or update ManifestWork
// returns placement decisions which can be the decisions for only a subset of subscriptions
//
func (a *AVRInstance) processSubscriptions() bool {
	a.log.Info("Process subscriptions", "total", len(a.subscriptionList.Items))

	requeue := false

	for idx, subscription := range a.subscriptionList.Items {
		// On the hub ignore any managed cluster subscriptions, as the hub maybe a managed cluster itself.
		// SubscriptionSubscribed means this subscription is child sitting in managed cluster
		// Placement.Local is true for a local subscription, and can be used in the absence of Status
		if subscription.Status.Phase == subv1.SubscriptionSubscribed ||
			(subscription.Spec.Placement != nil && subscription.Spec.Placement.Local != nil &&
				*subscription.Spec.Placement.Local) {
			a.log.Info("Skipping local subscription", "name", subscription.Name)

			continue
		}

		placementDecision, needRequeue := a.processSubscription(&a.subscriptionList.Items[idx])

		if needRequeue {
			a.log.Info("Requeue for subscription", "name", subscription.Name)

			requeue = true

			continue
		}

		if a.instance.Status.Decisions == nil {
			a.instance.Status.Decisions = make(rmn.SubscriptionPlacementDecisionMap)
		}

		if placementDecision != nil {
			a.instance.Status.Decisions[subscription.Name] = placementDecision
		}
	}

	a.log.Info("Returning Placement Decisions", "Total", len(a.instance.Status.Decisions))

	return requeue
}

func (a *AVRInstance) processSubscription(
	subscription *subv1.Subscription) (*rmn.SubscriptionPlacementDecision, bool) {
	a.log.Info("Processing subscription", "name", subscription.Name)

	a.log.Info(fmt.Sprintf("processSubscription: current state '%s'",
		a.instance.Status.LastKnownDRStates[subscription.Name]))

	const requeue = true
	// Check to see if this subscription is paused for DR. If it is, then restore PVs to the new destination
	// cluster, unpause the subscription, and skip it until the next reconciler iteration
	if a.isSubsriptionPausedForDR(subscription.GetLabels()) {
		a.log.Info("Subscription is paused", "name", subscription.Name)

		unpause := a.processPausedSubscription(subscription)
		if !unpause {
			a.log.Info(fmt.Sprintf("Paused subscription %s will be reprocessed later", subscription.Name))

			return nil, requeue
		}

		a.log.Info("Unpausing subscription", "name", subscription.Name)

		err := a.unpauseSubscription(subscription)
		if err != nil {
			a.log.Error(err, "failed to unpause subscription", "name", subscription.Name)

			return nil, requeue
		}

		// Subscription has been unpaused. Stop processing it and wait for the next Reconciler iteration
		a.log.Info("Subscription is unpaused. It will be processed in the next reconciler iteration",
			"name", subscription.Name)

		return nil, requeue
	}

	a.log.Info("Subscription is unpaused", "name", subscription.Name)

	exists, err := a.vrgManifestWorkAlreadyExists(subscription)
	if err != nil {
		return nil, requeue
	}

	if exists {
		return nil, !requeue
	}
	// This subscription is ready for manifest (VRG) creation
	placementDecision, err := a.processUnpausedSubscription(subscription)
	if err != nil {
		a.log.Error(err, "Failed to process unpaused subscription", "name", subscription.Name)

		return nil, requeue
	}

	a.log.Info(fmt.Sprintf("placementDecisions %+v - requeue: %t", placementDecision, !requeue))

	return &placementDecision, !requeue
}

func (a *AVRInstance) isSubsriptionPausedForDR(labels map[string]string) bool {
	return labels != nil &&
		labels[RamenDRLabelName] != "" &&
		strings.EqualFold(labels[RamenDRLabelName], "protected") &&
		labels[subv1.LabelSubscriptionPause] != "" &&
		strings.EqualFold(labels[subv1.LabelSubscriptionPause], "true")
}

// processPausedSubscription selects the target cluster from the subscription or
// from the user selected cluster and restores all PVs (belonging to the subscription)
// to the target cluster and then it unpauses the subscription
func (a *AVRInstance) processPausedSubscription(
	subscription *subv1.Subscription) bool {
	a.log.Info("Processing paused subscription", "name", subscription.Name)

	const unpause = true

	// find new home cluster (could be the failover cluster)
	newHomeCluster, err := a.findNextHomeClusterFromPlacementRule(subscription)
	if err != nil {
		a.log.Error(err, "Failed to find new home cluster for subscription",
			"name", subscription.Name, "newHomeCluster", newHomeCluster)
		// DON'T unpause
		return !unpause
	}

	newHomeCluster, err = a.validateHomeClusterSelection(subscription, newHomeCluster)
	if err != nil {
		a.log.Info("Failed to validate new home cluster selection for subscription",
			"name", subscription.Name, "newHomeCluster", newHomeCluster, "errMsg", err.Error())

		// UNPAUSE if and only if the current cluster is the newly selected cluster. This inidicates
		// that the user has selected to failover/failback to the same current cluster.
		return unpause && (errorswrapper.Is(err, ErrSameHomeCluster))
	}

	a.log.Info("Found new home cluster", "name", newHomeCluster)

	mwName := BuildManifestWorkName(subscription.Name, subscription.Namespace, MWTypePV)

	pvMW, err := a.findManifestWork(mwName, newHomeCluster)
	if err != nil {
		a.log.Error(err,
			fmt.Sprintf("Failed to find 'PV restore' ManifestWork for subscription %s", subscription.Name))
		// DON'T unpause
		return !unpause
	}

	if pvMW != nil {
		a.log.Info(fmt.Sprintf("Found manifest work (%v)", pvMW))
		// Unpause if the ManifestWork has been applied
		return IsManifestInAppliedState(pvMW)
	}

	ready := a.reconciler.isManagedClusterReadyForFailback(subscription, newHomeCluster)
	a.reconciler.Log.Info(newHomeCluster, "is ready for failback:", ready)

	if !ready {
		return !unpause
	}

	return a.cleanupAndRestore(subscription, newHomeCluster)
}

func (r *ApplicationVolumeReplicationReconciler) isManagedClusterReadyForFailback(
	subscription *subv1.Subscription, newHomeCluster string) bool {
	ready := false

	// get VRG and verify status through ManagedClusterView
	mcvMeta := metav1.ObjectMeta{
		Name:      "mcv-avr-reconciler",
		Namespace: newHomeCluster,
	}

	mcvViewscope := fndv2.ViewScope{
		Resource:  "VolumeReplicationGroup",
		Name:      subscription.Name,
		Namespace: subscription.Namespace,
	}

	vrg := &rmn.VolumeReplicationGroup{}

	err := r.getManagedClusterResource(mcvMeta, mcvViewscope, vrg)

	if err == nil {
		r.Log.Info("getManagedClusterResource success")
		ready = r.isVRGReadyForFailover(vrg)
	} else {
		r.Log.Info("getManagedClusterResource failed with error:", err)
	}

	return ready
}

func (r *ApplicationVolumeReplicationReconciler) isVRGReadyForFailover(
	vrg *rmn.VolumeReplicationGroup) bool {
	ready := true

	// TODO: really validate VRG status here

	return ready
}

func (a *AVRInstance) cleanupAndRestore(
	subscription *subv1.Subscription,
	newHomeCluster string) bool {
	const unpause = true

	pvMWName := fmt.Sprintf(ManifestWorkNameFormat, subscription.Name, subscription.Namespace, MWTypePV)

	err := a.deleteExistingManifestWork(subscription, pvMWName)
	if err != nil {
		a.log.Error(err, "Failed to delete existing PV manifestwork for subscription", "name", subscription.Name)

		return !unpause
	}

	vrgMWName := fmt.Sprintf(ManifestWorkNameFormat, subscription.Name, subscription.Namespace, MWTypeVRG)

	err = a.deleteExistingManifestWork(subscription, vrgMWName)
	if err != nil {
		a.log.Error(err, "Failed to delete existing VRG manifestwork for subscription", "name", subscription.Name)

		return !unpause
	}

	err = a.restorePVFromBackup(subscription, newHomeCluster)
	if err != nil {
		a.log.Error(err, "Failed to restore PVs from backup for subscription", "name", subscription.Name)

		return !unpause
	}

	a.moveToNextDRState(subscription.Name)

	return unpause
}

func (a *AVRInstance) vrgManifestWorkAlreadyExists(
	subscription *subv1.Subscription) (bool, error) {
	if a.instance.Status.Decisions == nil {
		return false, nil
	}

	if d, found := a.instance.Status.Decisions[subscription.Name]; found {
		// Skip this subscription if a manifestwork already exist for it
		mwName := BuildManifestWorkName(subscription.Name, subscription.Namespace, MWTypeVRG)

		mw, err := a.findManifestWork(mwName, d.HomeCluster)
		if err != nil {
			a.log.Error(err, "findManifestWork()", "name", subscription.Name)

			return false, err
		}

		if mw != nil {
			a.log.Info(fmt.Sprintf("Mainifestwork exists for subscription %s (%v)", subscription.Name, mw))

			return true, nil
		}
	}

	return false, nil
}

func (a *AVRInstance) findManifestWork(
	mwName, homeCluster string) (*ocmworkv1.ManifestWork, error) {
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

func (a *AVRInstance) deleteExistingManifestWork(
	subscription *subv1.Subscription, mwName string) error {
	a.log.Info("Try to delete ManifestWork for subscription", "name", subscription.Name)

	if d, found := a.instance.Status.Decisions[subscription.Name]; found {
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

	return nil
}

func (a *AVRInstance) restorePVFromBackup(
	subscription *subv1.Subscription, homeCluster string) error {
	a.log.Info("Restoring PVs to new managed cluster", "name", homeCluster)

	pvList, err := a.listPVsFromS3Store(subscription)
	if err != nil {
		return errorswrapper.Wrap(err, "failed to retrieve PVs from S3 store")
	}

	a.log.Info(fmt.Sprintf("Found %d PVs for subscription %s", len(pvList), subscription.Name))

	if len(pvList) == 0 {
		return nil
	}

	// Create manifestwork for all PVs for this subscription
	return a.createOrUpdatePVsManifestWork(subscription.Name, subscription.Namespace, homeCluster, pvList)
}

func (a *AVRInstance) createOrUpdatePVsManifestWork(
	name string, namespace string, homeClusterName string, pvList []corev1.PersistentVolume) error {
	a.log.Info("Creating manifest work for PVs", "subscription",
		name, "cluster", homeClusterName, "PV count", len(pvList))

	mwName := BuildManifestWorkName(name, namespace, MWTypePV)

	manifestWork, err := a.generatePVManifestWork(mwName, homeClusterName, pvList)
	if err != nil {
		return err
	}

	return a.createOrUpdateManifestWork(manifestWork, homeClusterName)
}

func (a *AVRInstance) unpauseSubscription(subscription *subv1.Subscription) error {
	// Subscription might have been changed since we last read it.  Get the latest...
	subLookupKey := types.NamespacedName{Name: subscription.Name, Namespace: subscription.Namespace}
	latestSub := &subv1.Subscription{}

	err := a.reconciler.Get(a.ctx, subLookupKey, latestSub)
	if err != nil {
		return fmt.Errorf("failed to retrieve subscription %+v", subLookupKey)
	}

	labels := latestSub.GetLabels()
	if labels == nil {
		return fmt.Errorf("failed to find labels for subscription %s", latestSub.Name)
	}

	labels[subv1.LabelSubscriptionPause] = "false"
	latestSub.SetLabels(labels)

	return a.reconciler.Update(a.ctx, latestSub)
}

func (a *AVRInstance) processUnpausedSubscription(
	subscription *subv1.Subscription) (rmn.SubscriptionPlacementDecision, error) {
	a.log.Info("Processing unpaused Subscription", "name", subscription.Name)

	homeCluster, peerCluster, err := a.selectPlacementDecision(subscription)
	if err != nil {
		a.log.Info(fmt.Sprintf("Unable to select placement decision (%v)", err))

		return rmn.SubscriptionPlacementDecision{}, err
	}

	if err := a.createOrUpdateVRGRolesManifestWork(homeCluster); err != nil {
		a.log.Error(err, "failed to create or update VolumeReplicationGroup Roles manifest")

		return rmn.SubscriptionPlacementDecision{}, err
	}

	if err := a.createOrUpdateVRGManifestWork(
		subscription.Name, subscription.Namespace,
		homeCluster, a.instance.Spec.S3Endpoint, a.instance.Spec.S3SecretName, a.instance.Spec.PVCSelector); err != nil {
		a.log.Error(err, "failed to create or update VolumeReplicationGroup manifest")

		return rmn.SubscriptionPlacementDecision{}, err
	}

	prevHomeCluster := ""

	if d, found := a.instance.Status.Decisions[subscription.Name]; found {
		switch {
		case d.PrevHomeCluster == "":
			prevHomeCluster = d.HomeCluster
		case d.PrevHomeCluster == homeCluster:
			prevHomeCluster = ""
		}
	}

	a.moveToNextDRState(subscription.Name)

	return rmn.SubscriptionPlacementDecision{
		HomeCluster:     homeCluster,
		PeerCluster:     peerCluster,
		PrevHomeCluster: prevHomeCluster,
	}, nil
}

func (a *AVRInstance) findNextHomeClusterFromPlacementRule(
	subscription *subv1.Subscription) (string, error) {
	a.log.Info("Finding the next home cluster for subscription", "name", subscription.Name)

	placementRule, err := a.getPlacementRule(subscription)
	if err != nil {
		return "", err
	}

	if len(placementRule.Status.Decisions) == 0 {
		return "", fmt.Errorf("no decisions were found in placementRule %s", placementRule.Name)
	}

	const requiredClusterReplicas = 1

	if placementRule.Spec.ClusterReplicas != nil && *placementRule.Spec.ClusterReplicas != requiredClusterReplicas {
		return "", fmt.Errorf("PlacementRule %s Required cluster replicas %d != %d",
			placementRule.Name, requiredClusterReplicas, *placementRule.Spec.ClusterReplicas)
	}

	return placementRule.Status.Decisions[0].ClusterName, nil
}

func (a *AVRInstance) validateHomeClusterSelection(
	subscription *subv1.Subscription, newHomeCluster string) (string, error) {
	if _, found := a.instance.Status.Decisions[subscription.Name]; !found {
		return newHomeCluster, nil
	}

	action, found := a.instance.Spec.DREnabledSubscriptions[subscription.Name]
	if !found {
		return newHomeCluster, fmt.Errorf("action not found for subscription %s (%+v)",
			subscription.Name, a.instance.Spec.DREnabledSubscriptions)
	}

	switch action {
	case rmn.ActionFailover:
		return a.validateFailover(subscription, newHomeCluster)
	case rmn.ActionFailback:
		return a.validateFailback(subscription, newHomeCluster)
	default:
		return newHomeCluster, fmt.Errorf("unknown action %s", action)
	}
}

func (a *AVRInstance) validateFailover(
	subscription *subv1.Subscription, newHomeCluster string) (string, error) {
	if d, found := a.instance.Status.Decisions[subscription.Name]; found {
		switch newHomeCluster {
		case d.PeerCluster:
			return newHomeCluster, nil
		case d.HomeCluster:
			return newHomeCluster, errorswrapper.Wrap(ErrSameHomeCluster, newHomeCluster)
		case d.PrevHomeCluster:
			return newHomeCluster,
				fmt.Errorf("miconfiguration detected on failover! (n:%s,p:%s)", newHomeCluster, d.PrevHomeCluster)
		}
	}

	return newHomeCluster, fmt.Errorf("unknown error (n:%s,avrStatus:%v)", newHomeCluster, a.instance.Status)
}

func (a *AVRInstance) validateFailback(
	subscription *subv1.Subscription, newHomeCluster string) (string, error) {
	if d, found := a.instance.Status.Decisions[subscription.Name]; found {
		switch newHomeCluster {
		case d.PrevHomeCluster:
			return newHomeCluster, nil
		case d.HomeCluster:
			return newHomeCluster, errorswrapper.Wrap(ErrSameHomeCluster, newHomeCluster)
		case d.PeerCluster:
			return "", fmt.Errorf("miconfiguration detected on failover! (n:%s,p:%s)", newHomeCluster, d.PrevHomeCluster)
		}
	}

	return "", fmt.Errorf("unknown error (n:%s,avrStatus:%v)", newHomeCluster, a.instance.Status)
}

func (a *AVRInstance) selectPlacementDecision(
	subscription *subv1.Subscription) (string, string, error) {
	a.log.Info("Selecting placement decisions for subscription", "name", subscription.Name)

	// get the placement rule fo this subscription
	placementRule, err := a.getPlacementRule(subscription)
	if err != nil {
		return "", "", fmt.Errorf("failed to retrieve placementRule using placementRef %s/%s",
			placementRule.Namespace, placementRule.Name)
	}

	return a.extractHomeClusterAndPeerCluster(subscription, placementRule)
}

func (a *AVRInstance) getPlacementRule(
	subscription *subv1.Subscription) (*plrv1.PlacementRule, error) {
	a.log.Info("Getting placement decisions for subscription", "name", subscription.Name)
	// The subscription phase describes the phasing of the subscriptions. Propagated means
	// this subscription is the "parent" sitting in hub. Statuses is a map where the key is
	// the cluster name and value is the aggregated status
	if subscription.Status.Phase != subv1.SubscriptionPropagated || subscription.Status.Statuses == nil {
		return nil, fmt.Errorf("subscription %s not ready", subscription.Name)
	}

	pl := subscription.Spec.Placement
	if pl == nil || pl.PlacementRef == nil {
		return nil, fmt.Errorf("placement not set for subscription %s", subscription.Name)
	}

	plRef := pl.PlacementRef

	// if application subscription PlacementRef namespace is empty, then apply
	// the application subscription namespace as the PlacementRef namespace
	if plRef.Namespace == "" {
		plRef.Namespace = subscription.Namespace
	}

	// get the placement rule fo this subscription
	placementRule := &plrv1.PlacementRule{}

	err := a.reconciler.Get(a.ctx,
		types.NamespacedName{Name: plRef.Name, Namespace: plRef.Namespace}, placementRule)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve placementRule using placementRef %s/%s", plRef.Namespace, plRef.Name)
	}

	return placementRule, nil
}

func (a *AVRInstance) extractHomeClusterAndPeerCluster(
	subscription *subv1.Subscription, placementRule *plrv1.PlacementRule) (string, string, error) {
	const empty = ""

	a.log.Info(fmt.Sprintf("Extracting home and peer clusters from subscription (%s) and PlacementRule (%s)",
		subscription.Name, placementRule.Name))

	subStatuses := subscription.Status.Statuses

	if subStatuses == nil {
		return empty, empty,
			fmt.Errorf("invalid subscription Status.Statuses. PlacementRule %s, Subscription %s",
				placementRule.Name, subscription.Name)
	}

	const maxClusterCount = 2

	clmap, err := a.getManagedClustersUsingPlacementRule(placementRule, maxClusterCount)
	if err != nil {
		return empty, empty, err
	}

	idx := 0

	clusters := make([]spokeClusterV1.ManagedCluster, maxClusterCount)
	for _, c := range clmap {
		clusters[idx] = *c
		idx++
	}

	d1 := clusters[0]
	d2 := clusters[1]

	var homeCluster string

	var peerCluster string

	switch {
	case subStatuses[d1.Name] != nil:
		homeCluster = d1.Name
		peerCluster = d2.Name
	case subStatuses[d2.Name] != nil:
		homeCluster = d2.Name
		peerCluster = d1.Name
	default:
		return empty, empty, fmt.Errorf("mismatch between placementRule %s decisions and subscription %s statuses",
			placementRule.Name, subscription.Name)
	}

	return homeCluster, peerCluster, nil
}

func (a *AVRInstance) getManagedClustersUsingPlacementRule(
	placementRule *plrv1.PlacementRule, maxClusterCount int) (map[string]*spokeClusterV1.ManagedCluster, error) {
	const requiredClusterReplicas = 1

	clmap, err := utils.PlaceByGenericPlacmentFields(
		a.reconciler.Client, placementRule.Spec.GenericPlacementFields, nil, placementRule)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster map for placement %s error: %w", placementRule.Name, err)
	}

	if placementRule.Spec.ClusterReplicas != nil && *placementRule.Spec.ClusterReplicas != requiredClusterReplicas {
		return nil, fmt.Errorf("PlacementRule %s Required cluster replicas %d != %d",
			placementRule.Name, requiredClusterReplicas, *placementRule.Spec.ClusterReplicas)
	}

	err = a.filterClusters(placementRule, clmap)
	if err != nil {
		return nil, fmt.Errorf("failed to filter clusters. Cluster len %d, error (%w)", len(clmap), err)
	}

	if len(clmap) != maxClusterCount {
		return nil, fmt.Errorf("PlacementRule %s should have made %d decisions. Found %d",
			placementRule.Name, maxClusterCount, len(clmap))
	}

	return clmap, nil
}

// --- UNIMPLEMENTED --- FAKE function *****
func (a *AVRInstance) filterClusters(
	placementRule *plrv1.PlacementRule, clmap map[string]*spokeClusterV1.ManagedCluster) error {
	a.log.Info("All good for now", "placementRule", placementRule.Name, "cluster len", len(clmap))
	// This is just to satisfy the linter for now.
	if len(clmap) == 0 {
		return fmt.Errorf("no clusters found for placementRule %s", placementRule.Name)
	}

	return nil
}

func (a *AVRInstance) createOrUpdateVRGRolesManifestWork(namespace string) error {
	// TODO: Enhance to remember clusters where this has been checked to reduce repeated Gets of the object
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

func (a *AVRInstance) generateVRGRolesManifestWork(namespace string) (
	*ocmworkv1.ManifestWork,
	error) {
	vrgClusterRole, err := a.generateVRGClusterRoleManifest()
	if err != nil {
		a.log.Error(err, "failed to generate VolumeReplicationGroup ClusterRole manifest", "namespace", namespace)

		return nil, err
	}

	vrgClusterRoleBinding, err := a.generateVRGClusterRoleBindingManifest()
	if err != nil {
		a.log.Error(err, "failed to generate VolumeReplicationGroup ClusterRoleBinding manifest", "namespace", namespace)

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
			a.log.Error(err, "failed to generate VolumeReplication")

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
			ReplicationState:       "Primary",
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

		a.log.Info("Creating", "ManifestWork", mw)

		return a.reconciler.Create(a.ctx, mw)
	}

	if !reflect.DeepEqual(foundMW.Spec, mw.Spec) {
		mw.Spec.DeepCopyInto(&foundMW.Spec)

		a.log.Info("ManifestWork exists. Updating", "ManifestWork", mw)

		return a.reconciler.Update(a.ctx, foundMW)
	}

	return nil
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

/*
Description: create a new ManagedClusterView object, or update the existing one with the same name.
Requires:
	1) meta: specifies MangedClusterView name and managed cluster search information
	2) viewscope: once the managed cluster is found, use this information to find the resource.
		Optional params: Namespace, Resource, Group, Version, Kind. Resource can be used by itself, Kind requires Version
Returns: ManagedClusterView, error
*/
func (r *ApplicationVolumeReplicationReconciler) createOrGetManagedClusterView(
	meta metav1.ObjectMeta, viewscope fndv2.ViewScope) (*fndv2.ManagedClusterView, error) {
	mcv := &fndv2.ManagedClusterView{
		ObjectMeta: meta,
		Spec: fndv2.ViewSpec{
			Scope: viewscope,
		},
	}

	err := r.Create(context.TODO(), mcv)
	errorDescription := ""

	if errors.IsAlreadyExists(err) {
		err = r.Get(context.TODO(), types.NamespacedName{Name: meta.Name, Namespace: meta.Namespace}, mcv)

		if err != nil {
			errorDescription = "failed to get existing ManagedClusterView"
		} else {
			mcv.Spec.Scope = viewscope // update scope to match input
		}
	} else {
		errorDescription = "failed to create ManagedClusterView"
	}

	return mcv, errorswrapper.Wrap(err, errorDescription)
}

/*
Description: queries a managed cluster for a resource type, and populates a variable with the results.
Requires:
	1) meta: information of the new/existing resource; defines which cluster(s) to search
	2) viewscope: query information for managed cluster resource. Example: resource, name.
	3) interface: empty variable to populate results into
Returns: error if encountered (nil if no error occurred). See results on interface object.
*/
func (r *ApplicationVolumeReplicationReconciler) getManagedClusterResource(
	meta metav1.ObjectMeta, viewscope fndv2.ViewScope, resource interface{}) error {
	// create MCV first
	mcv, err := r.createOrGetManagedClusterView(meta, viewscope)
	if err != nil {
		err = fmt.Errorf("could not get or create ManagedClusterView")

		return errorswrapper.Wrap(err, "getManagedClusterResource failed")
	}

	// get query results
	gotResource := false
	err = fmt.Errorf("failed to find a resource with ManagedClusterView")

	recentConditions := filterByConditionStatus(getMostRecentConditions(mcv.Status.Conditions), metav1.ConditionTrue)

	if len(mcv.Status.Conditions) > 0 {
		for _, condition := range recentConditions {
			if !gotResource && condition.Type == fndv2.ConditionViewProcessing {
				// convert raw data to usable object
				err = json.Unmarshal(mcv.Status.Result.Raw, resource)

				if err == nil {
					gotResource = true
				} else {
					err = fmt.Errorf("failed to Unmarshal data from ManagedClusterView to resource")
				}
			}
		}
	}

	return errorswrapper.Wrap(err, "getManagedClusterResource results")
}

func (a *AVRInstance) listPVsFromS3Store(
	subscription *subv1.Subscription) ([]corev1.PersistentVolume, error) {
	s3SecretLookupKey := types.NamespacedName{
		Name:      a.instance.Spec.S3SecretName,
		Namespace: a.instance.Namespace,
	}

	s3Bucket := constructBucketName(subscription.Namespace, subscription.Name)

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

func (a *AVRInstance) moveToNextDRState(subscriptionName string) {
	nextState := rmn.Initial

	if state, found := a.instance.Status.LastKnownDRStates[subscriptionName]; found {
		switch state {
		case rmn.Initial:
			nextState = rmn.FailingOver
		case rmn.FailingOver:
			nextState = rmn.FailedOver
		case rmn.FailedOver:
			nextState = rmn.FailingBack
		case rmn.FailingBack:
			nextState = rmn.FailedBack
		case rmn.FailedBack:
			nextState = rmn.FailingOver
		}
	}

	if a.instance.Status.LastKnownDRStates == nil {
		a.instance.Status.LastKnownDRStates = make(rmn.LastKnownDRStateMap)
	}

	a.log.Info(fmt.Sprintf("moveToNextDRState: current state '%s' - next state '%s'",
		a.instance.Status.LastKnownDRStates[subscriptionName], nextState))

	a.instance.Status.LastKnownDRStates[subscriptionName] = nextState
}
