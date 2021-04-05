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

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
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

type pvDownloader interface {
	DownloadPVs(ctx context.Context, r client.Reader, objStoreGetter ObjectStoreGetter,
		s3Endpoint string, s3SecretName types.NamespacedName,
		callerTag string, s3Bucket string) ([]corev1.PersistentVolume, error)
}

// ProgressCallback of function type
type ProgressCallback func(string, bool)

// ApplicationVolumeReplicationReconciler reconciles a ApplicationVolumeReplication object
type ApplicationVolumeReplicationReconciler struct {
	client.Client
	Log            logr.Logger
	PVDownloader   pvDownloader
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
		For(&ramendrv1alpha1.ApplicationVolumeReplication{}).
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

	avr := &ramendrv1alpha1.ApplicationVolumeReplication{}

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

	placementDecisions, requeue := r.processSubscriptions(avr, subscriptionList)
	if len(placementDecisions) != 0 {
		if err := r.updateAVRStatus(ctx, avr, placementDecisions); err != nil {
			logger.Error(err, "failed to update status")

			return ctrl.Result{Requeue: true}, nil
		}

		r.Log.Info(fmt.Sprintf("AVR (%+v)", avr))
	}

	logger.Info(fmt.Sprintf("Processed %d/%d subscriptions. AVR Status Decisions %d. Requeue? %v",
		len(placementDecisions), len(subscriptionList.Items), len(avr.Status.Decisions), requeue))

	done := !requeue
	r.Callback(avr.Name, done)

	return ctrl.Result{Requeue: requeue}, nil
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
func (r *ApplicationVolumeReplicationReconciler) processSubscriptions(
	avr *ramendrv1alpha1.ApplicationVolumeReplication,
	subscriptionList *subv1.SubscriptionList) (ramendrv1alpha1.SubscriptionPlacementDecisionMap, bool) {
	placementDecisions := ramendrv1alpha1.SubscriptionPlacementDecisionMap{}

	r.Log.Info("Process subscriptions", "total", len(subscriptionList.Items))

	requeue := false

	for idx, subscription := range subscriptionList.Items {
		// On the hub ignore any managed cluster subscriptions, as the hub maybe a managed cluster itself.
		// SubscriptionSubscribed means this subscription is child sitting in managed cluster
		// Placement.Local is true for a local subscription, and can be used in the absence of Status
		if subscription.Status.Phase == subv1.SubscriptionSubscribed ||
			(subscription.Spec.Placement != nil && subscription.Spec.Placement.Local != nil &&
				*subscription.Spec.Placement.Local) {
			r.Log.Info("Skipping local subscription", "name", subscription.Name)

			continue
		}

		placementDecision, needRequeue := r.processSubscription(avr, &subscriptionList.Items[idx])

		if needRequeue {
			r.Log.Info("Requeue for subscription", "name", subscription.Name)

			requeue = true

			continue
		}

		if placementDecision != nil {
			placementDecisions[subscription.Name] = placementDecision
		}
	}

	r.Log.Info("Returning Placement Decisions", "Total", len(placementDecisions))

	return placementDecisions, requeue
}

func (r *ApplicationVolumeReplicationReconciler) processSubscription(
	avr *ramendrv1alpha1.ApplicationVolumeReplication,
	subscription *subv1.Subscription) (*ramendrv1alpha1.SubscriptionPlacementDecision, bool) {
	r.Log.Info("Processing subscription", "name", subscription.Name)

	const requeue = true
	// Check to see if this subscription is paused for DR. If it is, then restore PVs to the new destination
	// cluster, unpause the subscription, and skip it until the next reconciler iteration
	if r.isSubsriptionPausedForDR(subscription.GetLabels()) {
		r.Log.Info("Subscription is paused", "name", subscription.Name)

		unpause := r.processPausedSubscription(avr, subscription)
		if !unpause {
			r.Log.Info(fmt.Sprintf("Paused subscription %s will be reprocessed later", subscription.Name))

			return nil, requeue
		}

		r.Log.Info("Unpausing subscription", "name", subscription.Name)

		err := r.unpauseSubscription(subscription)
		if err != nil {
			r.Log.Error(err, "failed to unpause subscription", "name", subscription.Name)

			return nil, requeue
		}

		// Subscription has been unpaused. Stop processing it and wait for the next Reconciler iteration
		r.Log.Info("Subscription unpaused. It will be processed in the next reconciler iteration", "name", subscription.Name)

		return nil, requeue
	}

	r.Log.Info("Subscription is unpaused", "name", subscription.Name)

	exists, err := r.vrgManifestWorkAlreadyExists(avr, subscription)
	if err != nil {
		return nil, requeue
	}

	if exists {
		return nil, !requeue
	}
	// This subscription is ready for manifest (VRG) creation
	placementDecision, err := r.processUnpausedSubscription(avr, subscription)
	if err != nil {
		r.Log.Error(err, "Failed to process unpaused subscription", "name", subscription.Name)

		return nil, requeue
	}

	r.Log.Info(fmt.Sprintf("placementDecisions %+v - requeue: %t", placementDecision, !requeue))

	return &placementDecision, !requeue
}

func (r *ApplicationVolumeReplicationReconciler) isSubsriptionPausedForDR(labels map[string]string) bool {
	return labels != nil &&
		labels[RamenDRLabelName] != "" &&
		strings.EqualFold(labels[RamenDRLabelName], "protected") &&
		labels[subv1.LabelSubscriptionPause] != "" &&
		strings.EqualFold(labels[subv1.LabelSubscriptionPause], "true")
}

// processPausedSubscription selects the target cluster from the subscription or
// from the user selected cluster and restores all PVs (belonging to the subscription)
// to the target cluster and then it unpauses the subscription
func (r *ApplicationVolumeReplicationReconciler) processPausedSubscription(
	avr *ramendrv1alpha1.ApplicationVolumeReplication,
	subscription *subv1.Subscription) bool {
	r.Log.Info("Processing paused subscription", "name", subscription.Name)

	const unpause = true

	// find new home cluster (could be the failover cluster)
	newHomeCluster, err := r.findNextHomeClusterFromPlacementRule(subscription)
	if err != nil {
		r.Log.Error(err, "Failed to find new home cluster for subscription",
			"name", subscription.Name, "newHomeCluster", newHomeCluster)
		// DON'T unpause
		return !unpause
	}

	newHomeCluster, err = r.validateHomeClusterSelection(avr, subscription, newHomeCluster)
	if err != nil {
		r.Log.Info("Failed to validate new home cluster selection for subscription",
			"name", subscription.Name, "newHomeCluster", newHomeCluster, "errMsg", err.Error())

		// UNPAUSE if and only if the current cluster is the newly selected cluster. This inidicates
		// that the user has selected to failover/failback to the same current cluster.
		return unpause && (errorswrapper.Is(err, ErrSameHomeCluster))
	}

	r.Log.Info("Found new home cluster", "name", newHomeCluster)

	mwName := BuildManifestWorkName(subscription.Name, subscription.Namespace, MWTypePV)

	pvMW, err := r.findManifestWork(mwName, newHomeCluster)
	if err != nil {
		r.Log.Error(err,
			fmt.Sprintf("Failed to find PV restore ManifestWork: avr %s, subscription %s", avr.Name, subscription.Name))
		// DON'T unpause
		return !unpause
	}

	if pvMW != nil {
		r.Log.Info(fmt.Sprintf("Found manifest work (%v)", pvMW))
		// Unpause if the ManifestWork has been applied
		return IsManifestInAppliedState(pvMW)
	}

	return r.cleanupAndRestore(avr, subscription, newHomeCluster)
}

func (r *ApplicationVolumeReplicationReconciler) cleanupAndRestore(
	avr *ramendrv1alpha1.ApplicationVolumeReplication,
	subscription *subv1.Subscription,
	newHomeCluster string) bool {
	const unpause = true

	pvMWName := fmt.Sprintf(ManifestWorkNameFormat, subscription.Name, subscription.Namespace, MWTypePV)

	err := r.deleteExistingManfiestWork(avr, subscription, pvMWName)
	if err != nil {
		r.Log.Error(err, "Failed to delete existing PV manifestwork for subscription", "name", subscription.Name)

		return !unpause
	}

	vrgMWName := fmt.Sprintf(ManifestWorkNameFormat, subscription.Name, subscription.Namespace, MWTypeVRG)

	err = r.deleteExistingManfiestWork(avr, subscription, vrgMWName)
	if err != nil {
		r.Log.Error(err, "Failed to delete existing VRG manifestwork for subscription", "name", subscription.Name)

		return !unpause
	}

	err = r.restorePVFromBackup(avr, subscription, newHomeCluster)
	if err != nil {
		r.Log.Error(err, "Failed to restore PVs from backup for subscription", "name", subscription.Name)

		return !unpause
	}

	return unpause
}

func (r *ApplicationVolumeReplicationReconciler) vrgManifestWorkAlreadyExists(
	avr *ramendrv1alpha1.ApplicationVolumeReplication,
	subscription *subv1.Subscription) (bool, error) {
	if avr.Status.Decisions == nil {
		return false, nil
	}

	if d, found := avr.Status.Decisions[subscription.Name]; found {
		// Skip this subscription if a manifestwork already exist for it
		mwName := BuildManifestWorkName(subscription.Name, subscription.Namespace, MWTypeVRG)

		mw, err := r.findManifestWork(mwName, d.HomeCluster)
		if err != nil {
			r.Log.Error(err, "findManifestWork()", "name", subscription.Name)

			return false, err
		}

		if mw != nil {
			r.Log.Info(fmt.Sprintf("Mainifestwork exists for subscription %s (%v)", subscription.Name, mw))

			return true, nil
		}
	}

	return false, nil
}

func (r *ApplicationVolumeReplicationReconciler) findManifestWork(
	mwName, homeCluster string) (*ocmworkv1.ManifestWork, error) {
	if homeCluster != "" {
		mw := &ocmworkv1.ManifestWork{}

		err := r.Client.Get(context.TODO(), types.NamespacedName{Name: mwName, Namespace: homeCluster}, mw)
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

func (r *ApplicationVolumeReplicationReconciler) deleteExistingManfiestWork(
	avr *ramendrv1alpha1.ApplicationVolumeReplication,
	subscription *subv1.Subscription, mwName string) error {
	r.Log.Info("Try to delete ManifestWork for subscription", "name", subscription.Name)

	if d, found := avr.Status.Decisions[subscription.Name]; found {
		mw := &ocmworkv1.ManifestWork{}

		err := r.Client.Get(context.TODO(), types.NamespacedName{Name: mwName, Namespace: d.HomeCluster}, mw)
		if err != nil {
			if errors.IsNotFound(err) {
				return nil
			}

			return fmt.Errorf("failed to retrieve manifestwork for type: %s. Error: %w", mwName, err)
		}

		r.Log.Info("deleting ManifestWork", "name", mw.Name)

		return r.Client.Delete(context.TODO(), mw)
	}

	return nil
}

func (r *ApplicationVolumeReplicationReconciler) restorePVFromBackup(
	avr *ramendrv1alpha1.ApplicationVolumeReplication,
	subscription *subv1.Subscription, homeCluster string) error {
	r.Log.Info("Restoring PVs to new managed cluster", "name", homeCluster)

	// TODO: get PVs from S3
	pvList, err := r.listPVsFromS3Store(avr, subscription)
	if err != nil {
		return errorswrapper.Wrap(err, "failed to retrieve PVs from S3 store")
	}

	r.Log.Info(fmt.Sprintf("Found %d PVs for subscription %s", len(pvList), subscription.Name))

	if len(pvList) == 0 {
		return nil
	}

	// Create manifestwork for all PVs for this subscription
	return r.createOrUpdatePVsManifestWork(subscription.Name, subscription.Namespace, homeCluster, pvList)
}

func (r *ApplicationVolumeReplicationReconciler) createOrUpdatePVsManifestWork(
	name string, namespace string, homeClusterName string, pvList []corev1.PersistentVolume) error {
	r.Log.Info("Creating manifest work for PVs", "subscription",
		name, "cluster", homeClusterName, "PV count", len(pvList))

	mwName := BuildManifestWorkName(name, namespace, MWTypePV)

	manifestWork, err := r.generatePVManifestWork(mwName, homeClusterName, pvList)
	if err != nil {
		return err
	}

	return r.createOrUpdateManifestWork(manifestWork, homeClusterName)
}

func (r *ApplicationVolumeReplicationReconciler) unpauseSubscription(subscription *subv1.Subscription) error {
	// Subscription might have been changed since we last read it.  Get the latest...
	subLookupKey := types.NamespacedName{Name: subscription.Name, Namespace: subscription.Namespace}
	latestSub := &subv1.Subscription{}

	err := r.Client.Get(context.TODO(), subLookupKey, latestSub)
	if err != nil {
		return fmt.Errorf("failed to retrieve subscription %+v", subLookupKey)
	}

	labels := latestSub.GetLabels()
	if labels == nil {
		return fmt.Errorf("failed to find labels for subscription %s", latestSub.Name)
	}

	labels[subv1.LabelSubscriptionPause] = "false"
	latestSub.SetLabels(labels)

	return r.Client.Update(context.TODO(), latestSub)
}

func (r *ApplicationVolumeReplicationReconciler) processUnpausedSubscription(
	avr *ramendrv1alpha1.ApplicationVolumeReplication,
	subscription *subv1.Subscription) (ramendrv1alpha1.SubscriptionPlacementDecision, error) {
	r.Log.Info("Processing unpaused Subscription", "name", subscription.Name)

	homeCluster, peerCluster, err := r.selectPlacementDecision(subscription)
	if err != nil {
		r.Log.Info(fmt.Sprintf("Unable to select placement decision (%v)", err))

		return ramendrv1alpha1.SubscriptionPlacementDecision{}, err
	}

	if err := r.createOrUpdateVRGRolesManifestWork(homeCluster); err != nil {
		r.Log.Error(err, "failed to create or update VolumeReplicationGroup Roles manifest")

		return ramendrv1alpha1.SubscriptionPlacementDecision{}, err
	}

	if err := r.createOrUpdateVRGManifestWork(
		subscription.Name, subscription.Namespace,
		homeCluster, avr.Spec.S3Endpoint, avr.Spec.S3SecretName, avr.Spec.PVCSelector); err != nil {
		r.Log.Error(err, "failed to create or update VolumeReplicationGroup manifest")

		return ramendrv1alpha1.SubscriptionPlacementDecision{}, err
	}

	prevHomeCluster := ""

	if d, found := avr.Status.Decisions[subscription.Name]; found {
		switch {
		case d.PrevHomeCluster == "":
			prevHomeCluster = d.HomeCluster
		case d.PrevHomeCluster == homeCluster:
			prevHomeCluster = ""
		}
	}

	return ramendrv1alpha1.SubscriptionPlacementDecision{
		HomeCluster:     homeCluster,
		PeerCluster:     peerCluster,
		PrevHomeCluster: prevHomeCluster,
	}, nil
}

func (r *ApplicationVolumeReplicationReconciler) findNextHomeClusterFromPlacementRule(
	subscription *subv1.Subscription) (string, error) {
	r.Log.Info("Finding the next home cluster for subscription", "name", subscription.Name)

	placementRule, err := r.getPlacementRule(subscription)
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

func (r *ApplicationVolumeReplicationReconciler) validateHomeClusterSelection(
	avr *ramendrv1alpha1.ApplicationVolumeReplication,
	subscription *subv1.Subscription, newHomeCluster string) (string, error) {
	if _, found := avr.Status.Decisions[subscription.Name]; !found {
		return newHomeCluster, nil
	}

	action, found := avr.Spec.DREnabledSubscriptions[subscription.Name]
	if !found {
		return newHomeCluster, fmt.Errorf("action not found for subscription %s (%+v)",
			subscription.Name, avr.Spec.DREnabledSubscriptions)
	}

	switch action {
	case ramendrv1alpha1.ActionFailover:
		return r.validateFailover(avr, subscription, newHomeCluster)
	case ramendrv1alpha1.ActionFailback:
		return r.validateFailback(avr, subscription, newHomeCluster)
	default:
		return newHomeCluster, fmt.Errorf("unknown action %s", action)
	}
}

func (r *ApplicationVolumeReplicationReconciler) validateFailover(
	avr *ramendrv1alpha1.ApplicationVolumeReplication,
	subscription *subv1.Subscription, newHomeCluster string) (string, error) {
	if d, found := avr.Status.Decisions[subscription.Name]; found {
		switch {
		case d.PeerCluster == newHomeCluster:
			return newHomeCluster, nil
		case d.HomeCluster == newHomeCluster:
			return newHomeCluster, errorswrapper.Wrap(ErrSameHomeCluster, newHomeCluster)
		case d.PrevHomeCluster == newHomeCluster:
			return newHomeCluster,
				fmt.Errorf("miconfiguration detected on failover! (n:%s,p:%s)", newHomeCluster, d.PrevHomeCluster)
		}
	}

	return newHomeCluster, fmt.Errorf("unknown error (n:%s,avrStatus:%v)", newHomeCluster, avr.Status)
}

func (r *ApplicationVolumeReplicationReconciler) validateFailback(
	avr *ramendrv1alpha1.ApplicationVolumeReplication,
	subscription *subv1.Subscription, newHomeCluster string) (string, error) {
	if d, found := avr.Status.Decisions[subscription.Name]; found {
		switch {
		case d.PrevHomeCluster == newHomeCluster:
			return newHomeCluster, nil
		case d.HomeCluster == newHomeCluster:
			return newHomeCluster, errorswrapper.Wrap(ErrSameHomeCluster, newHomeCluster)
		case d.PeerCluster == newHomeCluster:
			return "", fmt.Errorf("miconfiguration detected on failover! (n:%s,p:%s)", newHomeCluster, d.PrevHomeCluster)
		}
	}

	return "", fmt.Errorf("unknown error (n:%s,avrStatus:%v)", newHomeCluster, avr.Status)
}

func (r *ApplicationVolumeReplicationReconciler) selectPlacementDecision(
	subscription *subv1.Subscription) (string, string, error) {
	r.Log.Info("Selecting placement decisions for subscription", "name", subscription.Name)

	// get the placement rule fo this subscription
	placementRule, err := r.getPlacementRule(subscription)
	if err != nil {
		return "", "", fmt.Errorf("failed to retrieve placementRule using placementRef %s/%s",
			placementRule.Namespace, placementRule.Name)
	}

	return r.extractHomeClusterAndPeerCluster(subscription, placementRule)
}

func (r *ApplicationVolumeReplicationReconciler) getPlacementRule(
	subscription *subv1.Subscription) (*plrv1.PlacementRule, error) {
	r.Log.Info("Getting placement decisions for subscription", "name", subscription.Name)
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

	err := r.Client.Get(context.TODO(),
		types.NamespacedName{Name: plRef.Name, Namespace: plRef.Namespace}, placementRule)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve placementRule using placementRef %s/%s", plRef.Namespace, plRef.Name)
	}

	return placementRule, nil
}

func (r *ApplicationVolumeReplicationReconciler) extractHomeClusterAndPeerCluster(
	subscription *subv1.Subscription, placementRule *plrv1.PlacementRule) (string, string, error) {
	const empty = ""

	r.Log.Info(fmt.Sprintf("Extracting home and peer clusters from subscription (%s) and PlacementRule (%s)",
		subscription.Name, placementRule.Name))

	subStatuses := subscription.Status.Statuses

	if subStatuses == nil {
		return empty, empty,
			fmt.Errorf("invalid subscription Status.Statuses. PlacementRule %s, Subscription %s",
				placementRule.Name, subscription.Name)
	}

	const maxClusterCount = 2

	clmap, err := r.getManagedClustersUsingPlacementRule(placementRule, maxClusterCount)
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

func (r *ApplicationVolumeReplicationReconciler) getManagedClustersUsingPlacementRule(
	placementRule *plrv1.PlacementRule, maxClusterCount int) (map[string]*spokeClusterV1.ManagedCluster, error) {
	const requiredClusterReplicas = 1

	clmap, err := utils.PlaceByGenericPlacmentFields(
		r.Client, placementRule.Spec.GenericPlacementFields, nil, placementRule)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster map for placement %s error: %w", placementRule.Name, err)
	}

	if placementRule.Spec.ClusterReplicas != nil && *placementRule.Spec.ClusterReplicas != requiredClusterReplicas {
		return nil, fmt.Errorf("PlacementRule %s Required cluster replicas %d != %d",
			placementRule.Name, requiredClusterReplicas, *placementRule.Spec.ClusterReplicas)
	}

	err = r.filterClusters(placementRule, clmap)
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
func (r *ApplicationVolumeReplicationReconciler) filterClusters(
	placementRule *plrv1.PlacementRule, clmap map[string]*spokeClusterV1.ManagedCluster) error {
	r.Log.Info("All good for now", "placementRule", placementRule.Name, "cluster len", len(clmap))
	// This is just to satisfy the linter for now.
	if len(clmap) == 0 {
		return fmt.Errorf("no clusters found for placementRule %s", placementRule.Name)
	}

	return nil
}

func (r *ApplicationVolumeReplicationReconciler) createOrUpdateVRGRolesManifestWork(namespace string) error {
	// TODO: Enhance to remember clusters where this has been checked to reduce repeated Gets of the object
	manifestWork, err := r.generateVRGRolesManifestWork(namespace)
	if err != nil {
		return err
	}

	return r.createOrUpdateManifestWork(manifestWork, namespace)
}

func (r *ApplicationVolumeReplicationReconciler) createOrUpdateVRGManifestWork(
	name, namespace, homeCluster, s3Endpoint, s3SecretName string, pvcSelector metav1.LabelSelector) error {
	r.Log.Info(fmt.Sprintf("Create or Update manifestwork %s:%s:%s:%s:%s",
		name, namespace, homeCluster, s3Endpoint, s3SecretName))

	manifestWork, err := r.generateVRGManifestWork(name, namespace, homeCluster, s3Endpoint, s3SecretName, pvcSelector)
	if err != nil {
		return err
	}

	return r.createOrUpdateManifestWork(manifestWork, homeCluster)
}

func (r *ApplicationVolumeReplicationReconciler) generateVRGRolesManifestWork(namespace string) (
	*ocmworkv1.ManifestWork,
	error) {
	vrgClusterRole, err := r.generateVRGClusterRoleManifest()
	if err != nil {
		r.Log.Error(err, "failed to generate VolumeReplicationGroup ClusterRole manifest", "namespace", namespace)

		return nil, err
	}

	vrgClusterRoleBinding, err := r.generateVRGClusterRoleBindingManifest()
	if err != nil {
		r.Log.Error(err, "failed to generate VolumeReplicationGroup ClusterRoleBinding manifest", "namespace", namespace)

		return nil, err
	}

	manifests := []ocmworkv1.Manifest{*vrgClusterRole, *vrgClusterRoleBinding}

	return r.newManifestWork(
		"ramendr-vrg-roles",
		namespace,
		map[string]string{},
		manifests), nil
}

func (r *ApplicationVolumeReplicationReconciler) generateVRGClusterRoleManifest() (*ocmworkv1.Manifest, error) {
	return r.generateManifest(&rbacv1.ClusterRole{
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

func (r *ApplicationVolumeReplicationReconciler) generateVRGClusterRoleBindingManifest() (*ocmworkv1.Manifest, error) {
	return r.generateManifest(&rbacv1.ClusterRoleBinding{
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

func (r *ApplicationVolumeReplicationReconciler) generatePVManifestWork(
	mwName, homeClusterName string, pvList []corev1.PersistentVolume) (*ocmworkv1.ManifestWork, error) {
	manifests, err := r.generatePVManifest(pvList)
	if err != nil {
		return nil, err
	}

	return r.newManifestWork(
		mwName,
		homeClusterName,
		map[string]string{"app": "PV"},
		manifests), nil
}

// This function follow a slightly different pattern than the rest, simply because the pvList that come
// from the S3 store will contain PV objects already converted to a string.
func (r *ApplicationVolumeReplicationReconciler) generatePVManifest(
	pvList []corev1.PersistentVolume) ([]ocmworkv1.Manifest, error) {
	manifests := []ocmworkv1.Manifest{}

	for _, pv := range pvList {
		pvClientManifest, err := r.generateManifest(pv)
		// Either all succeed or none
		if err != nil {
			r.Log.Error(err, "failed to generate VolumeReplication")

			return nil, err
		}

		manifests = append(manifests, *pvClientManifest)
	}

	return manifests, nil
}

func (r *ApplicationVolumeReplicationReconciler) generateVRGManifestWork(
	name, namespace, homeCluster, s3Endpoint, s3SecretName string,
	pvcSelector metav1.LabelSelector) (*ocmworkv1.ManifestWork, error) {
	vrgClientManifest, err := r.generateVRGManifest(name, namespace, s3Endpoint, s3SecretName, pvcSelector)
	if err != nil {
		r.Log.Error(err, "failed to generate VolumeReplicationGroup manifest")

		return nil, err
	}

	manifests := []ocmworkv1.Manifest{*vrgClientManifest}

	return r.newManifestWork(
		fmt.Sprintf(ManifestWorkNameFormat, name, namespace, MWTypeVRG),
		homeCluster,
		map[string]string{"app": "VRG"},
		manifests), nil
}

func (r *ApplicationVolumeReplicationReconciler) generateVRGManifest(
	name, namespace, s3Endpoint, s3SecretName string, pvcSelector metav1.LabelSelector) (*ocmworkv1.Manifest, error) {
	return r.generateManifest(&ramendrv1alpha1.VolumeReplicationGroup{
		TypeMeta:   metav1.TypeMeta{Kind: "VolumeReplicationGroup", APIVersion: "ramendr.openshift.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
			PVCSelector:            pvcSelector,
			VolumeReplicationClass: "volume-rep-class",
			ReplicationState:       "Primary",
			S3Endpoint:             s3Endpoint,
			S3SecretName:           s3SecretName,
		},
	})
}

func (r *ApplicationVolumeReplicationReconciler) generateManifest(obj interface{}) (*ocmworkv1.Manifest, error) {
	objJSON, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal %v to JSON, error %w", obj, err)
	}

	manifest := &ocmworkv1.Manifest{}
	manifest.RawExtension = runtime.RawExtension{Raw: objJSON}

	return manifest, nil
}

func (r *ApplicationVolumeReplicationReconciler) newManifestWork(name string, mcNamespace string,
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

func (r *ApplicationVolumeReplicationReconciler) createOrUpdateManifestWork(
	mw *ocmworkv1.ManifestWork,
	managedClusternamespace string) error {
	foundMW := &ocmworkv1.ManifestWork{}

	err := r.Client.Get(context.TODO(),
		types.NamespacedName{Name: mw.Name, Namespace: managedClusternamespace},
		foundMW)
	if err != nil {
		if !errors.IsNotFound(err) {
			return errorswrapper.Wrap(err, fmt.Sprintf("failed to fetch ManifestWork %s", mw.Name))
		}

		r.Log.Info("Creating", "ManifestWork", mw)

		return r.Client.Create(context.TODO(), mw)
	}

	if !reflect.DeepEqual(foundMW.Spec, mw.Spec) {
		mw.Spec.DeepCopyInto(&foundMW.Spec)

		r.Log.Info("ManifestWork exists. Updating", "ManifestWork", mw)

		return r.Client.Update(context.TODO(), foundMW)
	}

	return nil
}

func (r *ApplicationVolumeReplicationReconciler) updateAVRStatus(
	ctx context.Context,
	avr *ramendrv1alpha1.ApplicationVolumeReplication,
	placementDecisions ramendrv1alpha1.SubscriptionPlacementDecisionMap) error {
	r.Log.Info("Updated AVR status", "name", avr.Name)

	avr.Status = ramendrv1alpha1.ApplicationVolumeReplicationStatus{
		Decisions: placementDecisions,
	}
	if err := r.Client.Status().Update(ctx, avr); err != nil {
		return errorswrapper.Wrap(err, "failed to update AVR status")
	}

	r.Log.Info(fmt.Sprintf("Updated AVR %s - Status %+v", avr.Name, avr.Status))

	return nil
}

func (r *ApplicationVolumeReplicationReconciler) listPVsFromS3Store(
	avr *ramendrv1alpha1.ApplicationVolumeReplication,
	subscription *subv1.Subscription) ([]corev1.PersistentVolume, error) {
	s3SecretLookupKey := types.NamespacedName{
		Name:      avr.Spec.S3SecretName,
		Namespace: avr.Namespace,
	}

	s3Bucket := constructBucketName(subscription.Namespace, subscription.Name)

	return r.PVDownloader.DownloadPVs(
		context.TODO(), r.Client, r.ObjStoreGetter, avr.Spec.S3Endpoint, s3SecretLookupKey, avr.Name, s3Bucket)
}

type ObjectStorePVDownloader struct{}

func (s ObjectStorePVDownloader) DownloadPVs(ctx context.Context, r client.Reader,
	objStoreGetter ObjectStoreGetter, s3Endpoint string, s3SecretName types.NamespacedName,
	callerTag string, s3Bucket string) ([]corev1.PersistentVolume, error) {
	objectStore, err := objStoreGetter.objectStore(ctx, r, s3Endpoint, s3SecretName, callerTag)
	if err != nil {
		return nil, fmt.Errorf("error when downloading PVs, err %w", err)
	}

	return objectStore.downloadPVs(s3Bucket)
}
