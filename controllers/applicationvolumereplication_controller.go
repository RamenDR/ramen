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

	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	plrv1 "github.com/open-cluster-management/multicloud-operators-placementrule/pkg/apis/apps/v1"
	subv1 "github.com/open-cluster-management/multicloud-operators-subscription/pkg/apis/apps/v1"
	fndv2 "github.com/tjanssen3/multicloud-operators-foundation/v2/pkg/apis/view/v1beta1"

	"github.com/go-logr/logr"
	errorswrapper "github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
)

const (
	// ManifestWorkNameFormat is a formated a string used to generate the manifest name
	// The format is name-namespace-type-mw where:
	// - name is the subscription name
	// - namespace is the subscription namespace
	// - type is either "vrg", "pv", or "roles"
	ManifestWorkNameFormat string = "%s-%s-%s-mw"

	// ManifestWork VRG Type
	MWTypeVRG string = "vrg"

	// ManifestWork PV Type
	MWTypePV string = "pv"

	// ManifestWork Roles Type
	MWTypeRoles string = "roles"

	// Ramen scheduler
	RamenScheduler string = "Ramen"
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

	if len(conditions) == 0 {
		return recentConditions
	}

	// len(conditions) > 0; conditions are sorted
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

	subList, userPlRule, err := r.getSubscriptionsAndPlacementRule(ctx, avr)
	if err != nil {
		return ctrl.Result{}, err
	}

	clonedPlRule, err := r.getOrClonePlacementRule(ctx, avr, userPlRule)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Make sure that we give time to the cloned PlacementRule to run and produces decisions
	if len(clonedPlRule.Status.Decisions) == 0 {
		const initialWaitTime = 5

		return ctrl.Result{RequeueAfter: time.Second * initialWaitTime}, nil
	}

	// check if this is the initial deployement.  0 decision indicates that this is
	// the first time using this placement rule.
	if len(userPlRule.Status.Decisions) == 0 {
		updated, err := r.updateUserPlacementRule(clonedPlRule, userPlRule)
		if err != nil {
			return ctrl.Result{}, err
		}

		if updated {
			const subWaitTime = 2
			// Wait for a moment to give a chance to the subscription to run
			return ctrl.Result{RequeueAfter: time.Second * subWaitTime}, nil
		}
	}

	a := AVRInstance{
		reconciler: r, ctx: ctx, log: logger, instance: avr, needStatusUpdate: false,
		subscriptionList: subList, userPlacementRule: userPlRule, clonedPlacementRule: clonedPlRule,
	}

	requeue := a.startProcessing()

	done := !requeue
	r.Callback(a.instance.Name, done)

	return ctrl.Result{Requeue: requeue}, nil
}

func (r *ApplicationVolumeReplicationReconciler) getSubscriptionsAndPlacementRule(ctx context.Context,
	avr *rmn.ApplicationVolumeReplication) (*subv1.SubscriptionList, *plrv1.PlacementRule, error) {
	r.Log.Info("Getting subscriptions matching",
		"namespace", avr.Namespace, "matchLabels", avr.Spec.SubscriptionSelector.MatchLabels)

	subscriptionList := &subv1.SubscriptionList{}
	listOptions := []client.ListOption{
		client.InNamespace(avr.Namespace),
		client.MatchingLabels(avr.Spec.SubscriptionSelector.MatchLabels),
	}

	err := r.Client.List(ctx, subscriptionList, listOptions...)
	if err != nil {
		if !errors.IsNotFound(err) {
			r.Log.Error(err, "failed to find subscriptions")

			return nil, nil, errorswrapper.Wrap(err, "No subscriptions found")
		}

		return nil, nil, errorswrapper.Wrap(err, "failed to list subscriptions")
	}

	// Make sure all subscriptions have the same placement
	placement, err := r.ensureSubsHaveSamePlacement(subscriptionList)
	if err != nil {
		r.Log.Error(err, "unable to ensure subscriptions have the same placement rule")

		return nil, nil, err
	}

	// Make sure that the scheduler used for this set of subscriptions is the ramen scheduler
	err = r.ensureDRPlacementScheduler(placement)
	if err != nil {
		r.Log.Error(err, "Failed to ensure our placement scheduler", "placementRef", placement.PlacementRef)

		return nil, nil, err
	}

	placementRule := &plrv1.PlacementRule{}
	plRef := placement.PlacementRef

	err = r.Client.Get(context.TODO(), types.NamespacedName{Name: plRef.Name, Namespace: plRef.Namespace}, placementRule)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get placementrule error: %w", err)
	}

	return subscriptionList, placementRule, nil
}

func (r *ApplicationVolumeReplicationReconciler) ensureSubsHaveSamePlacement(
	subscriptions *subv1.SubscriptionList) (*plrv1.Placement, error) {
	if len(subscriptions.Items) == 0 {
		return nil, fmt.Errorf("empty subscriptionList")
	}

	subscription := subscriptions.Items[0]
	placement := subscription.Spec.Placement

	for _, sub := range subscriptions.Items {
		if sub.Spec.Placement == nil || sub.Spec.Placement.PlacementRef == nil {
			return nil, fmt.Errorf("invalid subscription placement object. (SubName %s)", sub.Name)
		}

		if !reflect.DeepEqual(placement, sub.Spec.Placement) {
			return nil, fmt.Errorf("not all subscriptions for this AVR have same placement")
		}
	}

	// Make sure the the placementRef is namespaced. If not, use the subscription's namespace
	if placement.PlacementRef.Namespace == "" {
		placement.PlacementRef.Namespace = subscription.Namespace
	}

	return placement, nil
}

func (r *ApplicationVolumeReplicationReconciler) ensureDRPlacementScheduler(
	placement *plrv1.Placement) error {
	plRef := placement.PlacementRef

	placementRule := &plrv1.PlacementRule{}

	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: plRef.Name, Namespace: plRef.Namespace}, placementRule)
	if err != nil {
		return fmt.Errorf("failed to get placementrule error: %w", err)
	}

	scName := placementRule.Spec.SchedulerName
	if scName != "" && scName != RamenScheduler {
		return fmt.Errorf("placementRule %s does not have the ramen scheduler. Scheduler %s", placementRule.Name, scName)
	}

	return nil
}

func (r *ApplicationVolumeReplicationReconciler) getOrClonePlacementRule(ctx context.Context,
	avr *rmn.ApplicationVolumeReplication, userPlRule *plrv1.PlacementRule) (*plrv1.PlacementRule, error) {
	// 1. Get ClonedPlacementRule
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
				r.Log.Error(err, "Failed to created a cloned placementRule", "name", clonedPlRuleName)

				return nil, fmt.Errorf("failed to create cloned placementrule error: %w", err)
			}
		} else {
			r.Log.Error(err, "Failed to get cloned placementRule", "name", clonedPlRuleName)

			return nil, fmt.Errorf("failed to get placementrule error: %w", err)
		}
	}

	return clonedPlRule, nil
}

func (r *ApplicationVolumeReplicationReconciler) clonePlacementRule(ctx context.Context,
	avr *rmn.ApplicationVolumeReplication, userPlRule *plrv1.PlacementRule,
	clonedPlRuleName string) (*plrv1.PlacementRule, error) {
	clonedPlRule := &plrv1.PlacementRule{}

	userPlRule.DeepCopyInto(clonedPlRule)
	r.Log.Info("Creating a clone placementRule from", "name", userPlRule.Name)

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
	clonedPlRule, userPlRule *plrv1.PlacementRule) (bool, error) {
	const updated = true

	if !reflect.DeepEqual(clonedPlRule.Status.Decisions, userPlRule.Status.Decisions) {
		clonedPlRule.Status.DeepCopyInto(&userPlRule.Status)

		r.Log.Info("Deep copied our PlacementRule decision into user PlacementRule",
			"UserPlRule", userPlRule.Status.Decisions)

		if err := r.Status().Update(context.TODO(), userPlRule); err != nil {
			return !updated, errorswrapper.Wrap(err, "failed to update userPlRule")
		}

		return updated, nil
	}

	return !updated, nil
}

type AVRInstance struct {
	reconciler          *ApplicationVolumeReplicationReconciler
	ctx                 context.Context
	log                 logr.Logger
	instance            *rmn.ApplicationVolumeReplication
	needStatusUpdate    bool
	subscriptionList    *subv1.SubscriptionList
	userPlacementRule   *plrv1.PlacementRule
	clonedPlacementRule *plrv1.PlacementRule
}

func (a *AVRInstance) startProcessing() bool {
	a.log.Info("Starting to process subscriptions", "Total", len(a.subscriptionList.Items))
	requeue := a.processSubscriptions()

	if a.needStatusUpdate {
		if err := a.updateAVRStatus(); err != nil {
			a.log.Error(err, "failed to update status")

			requeue = true
		}
	}

	return requeue
}

func (a *AVRInstance) processSubscriptions() bool {
	a.log.Info("Process subscriptions", "total", len(a.subscriptionList.Items))

	done := a.runPrerequisites()
	if !done {
		return true // requeue
	}

	updated, err := a.reconciler.updateUserPlacementRule(a.clonedPlacementRule, a.userPlacementRule)
	if err != nil {
		a.log.Error(err, "updateUserPlacementRule")

		return true // requeue
	}

	if updated {
		a.log.Info("PlacementRule was updated", "name", a.userPlacementRule)

		return true // requeue
	}

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

		needRequeue := a.processSubscription(&a.subscriptionList.Items[idx])

		if needRequeue {
			a.log.Info("Requeue for subscription", "name", subscription.Name)

			requeue = true
		}
	}

	a.log.Info(fmt.Sprintf("AVR (%+v)", a.instance))
	a.log.Info(fmt.Sprintf("Made %d decision(s) out of %d subscription(s). Requeue? %v",
		len(a.instance.Status.Decisions), len(a.subscriptionList.Items), requeue))

	return requeue
}

func (a *AVRInstance) processSubscription(subscription *subv1.Subscription) bool {
	a.log.Info("Processing subscription", "name", subscription.Name,
		"current state", a.instance.Status.LastKnownDRStates[subscription.Name])

	return a.createVRGManifestForSubscription(subscription)
}

func (a *AVRInstance) createVRGManifestForSubscription(subscription *subv1.Subscription) bool {
	a.log.Info("Processing initial deployment", "FinalState", a.instance.Status.LastKnownDRStates[subscription.Name])

	const requeue = true

	if a.instance.Status.LastKnownDRStates[subscription.Name] == rmn.Initial {
		return !requeue
	}

	exists, err := a.vrgManifestWorkAlreadyExists(subscription)
	if err != nil {
		return requeue
	}

	if exists {
		return !requeue
	}

	// VRG ManifestWork does not exist, start the process to create it
	placementDecision, err := a.processVRGManifestWork(subscription)
	if err != nil {
		a.log.Error(err, "Failed to process subscription", "name", subscription.Name)

		return requeue
	}

	a.log.Info(fmt.Sprintf("placementDecisions %+v - requeue: %t", placementDecision, !requeue))

	if a.instance.Status.Decisions == nil {
		a.instance.Status.Decisions = make(rmn.SubscriptionPlacementDecisionMap)
	}

	a.instance.Status.Decisions[subscription.Name] = &placementDecision
	a.needStatusUpdate = true

	return !requeue
}

func (a *AVRInstance) runPrerequisites() bool {
	doneCount := 0
	consideredSubsCount := 0

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

		consideredSubsCount++

		done, err := a.runPrerequisitsForSubscription(&a.subscriptionList.Items[idx])
		if err != nil {
			continue
		}

		if done {
			a.log.Info("Done running prerequisits for subscription", "name", subscription.Name)

			doneCount++
		}
	}

	a.log.Info("After running prerequisits", "DoneCount", doneCount, "ConsideredSubsCount", consideredSubsCount)

	return doneCount == consideredSubsCount
}

func (a *AVRInstance) runPrerequisitsForSubscription(subscription *subv1.Subscription) (bool, error) {
	switch a.instance.Spec.Action {
	case rmn.ActionFailover:
		return a.runFailoverPrerequisites(subscription)
	case rmn.ActionFailback:
		return a.runFailbackPrerequisites(subscription)
	}

	// Neither failover nor failback
	return true, nil
}

func (a *AVRInstance) runFailoverPrerequisites(subscription *subv1.Subscription) (bool, error) {
	a.log.Info("Processing prerequisites fo a failover", "FinalState",
		a.instance.Status.LastKnownDRStates[subscription.Name])

	if a.instance.Status.LastKnownDRStates[subscription.Name] == rmn.FailedOver {
		return true, nil
	}

	return a.runRestore(subscription)
}

func (a *AVRInstance) runFailbackPrerequisites(subscription *subv1.Subscription) (bool, error) {
	a.log.Info("Processing prerequisites fo a failback", "FinalState",
		a.instance.Status.LastKnownDRStates[subscription.Name])

	if a.instance.Status.LastKnownDRStates[subscription.Name] == rmn.FailedBack {
		return true, nil
	}

	return a.runRestore(subscription)
}

func (a *AVRInstance) runRestore(subscription *subv1.Subscription) (bool, error) {
	const done = true
	// find new home cluster (could be the failover cluster)
	newHomeCluster, err := a.findHomeClusterFromClonedPlRule()
	if err != nil {
		a.log.Error(err, "Failed to find new home cluster for subscription",
			"name", subscription.Name, "newHomeCluster", newHomeCluster)

		return !done, err
	}

	newHomeCluster, err = a.validateHomeClusterSelection(subscription, newHomeCluster)
	if err != nil {
		a.log.Info("Failed to validate new home cluster selection for subscription",
			"name", subscription.Name, "newHomeCluster", newHomeCluster, "errMsg", err.Error())

		if errorswrapper.Is(err, ErrSameHomeCluster) {
			return true, nil
		}

		return !done, err
	}

	a.log.Info("Found new home cluster", "name", newHomeCluster)

	mwName := BuildManifestWorkName(subscription.Name, subscription.Namespace, MWTypePV)

	// Try to find whether we have already created a ManifestWork for this
	pvMW, err := a.findManifestWork(mwName, newHomeCluster)
	if err != nil {
		a.log.Error(err,
			fmt.Sprintf("Failed to find 'PV restore' ManifestWork for subscription %s", subscription.Name))

		return !done, err
	}

	if pvMW != nil {
		a.log.Info(fmt.Sprintf("Found manifest work (%v)", pvMW))

		if !IsManifestInAppliedState(pvMW) {
			return !done, nil
		}

		return done, nil
	}

	err = a.cleanupAndRestore(subscription, newHomeCluster)
	if err != nil {
		return !done, err
	}

	// return not done because old MWs have been cleaned up successfully but
	// the new PR MW may not in Applied state yet.
	return !done, nil
}

// outputs a string for use in creating a ManagedClusterView name
// example: when looking for a vrg with name 'demo' in the namespace 'ramen', input: ("demo", "ramen", "vrg")
// this will give output "demo-ramen-vrg-mcv"
func BuildManagedClusterViewName(resourceName, resourceNamespace, resource string) string {
	return fmt.Sprintf("%s-%s-%s-mcv", resourceName, resourceNamespace, resource)
}

func (r *ApplicationVolumeReplicationReconciler) getVRGFromManagedCluster(
	resourceName string, resourceNamespace string, managedCluster string) (*rmn.VolumeReplicationGroup, error) {
	// get VRG and verify status through ManagedClusterView
	mcvMeta := metav1.ObjectMeta{
		Name:      BuildManagedClusterViewName(resourceName, resourceNamespace, "vrg"),
		Namespace: managedCluster,
	}

	mcvViewscope := fndv2.ViewScope{
		Resource:  "VolumeReplicationGroup",
		Name:      resourceName,
		Namespace: resourceNamespace,
	}

	vrg := &rmn.VolumeReplicationGroup{}

	err := r.getManagedClusterResource(mcvMeta, mcvViewscope, vrg)

	return vrg, err
}

func (r *ApplicationVolumeReplicationReconciler) isVRGReadyForFailback(
	vrg *rmn.VolumeReplicationGroup) bool {
	ready := true

	// TODO: really validate VRG status here

	return ready
}

func (a *AVRInstance) cleanupAndRestore(
	subscription *subv1.Subscription,
	newHomeCluster string) error {
	pvMWName := fmt.Sprintf(ManifestWorkNameFormat, subscription.Name, subscription.Namespace, MWTypePV)

	err := a.deleteExistingManifestWork(subscription, pvMWName)
	if err != nil {
		a.log.Error(err, "Failed to delete existing PV manifestwork for subscription", "name", subscription.Name)

		return err
	}

	vrgMWName := fmt.Sprintf(ManifestWorkNameFormat, subscription.Name, subscription.Namespace, MWTypeVRG)

	err = a.deleteExistingManifestWork(subscription, vrgMWName)
	if err != nil {
		a.log.Error(err, "Failed to delete existing VRG manifestwork for subscription", "name", subscription.Name)

		return err
	}

	err = a.restorePVFromBackup(subscription, newHomeCluster)
	if err != nil {
		a.log.Error(err, "Failed to restore PVs from backup for subscription", "name", subscription.Name)

		return err
	}

	a.advanceToNextDRState(subscription.Name)

	return nil
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

	for idx := range pvList {
		a.cleanupPVForRestore(&pvList[idx])
	}

	// Create manifestwork for all PVs for this subscription
	return a.createOrUpdatePVsManifestWork(subscription.Name, subscription.Namespace, homeCluster, pvList)
}

// cleanupPVForRestore cleans up required PV fields, to ensure restore succeeds to a new cluster, and
// rebinding the PV to a newly created PVC with the same claimRef succeeds
func (a *AVRInstance) cleanupPVForRestore(pv *corev1.PersistentVolume) {
	pv.ResourceVersion = ""
	if pv.Spec.ClaimRef != nil {
		pv.Spec.ClaimRef.UID = ""
		pv.Spec.ClaimRef.ResourceVersion = ""
		pv.Spec.ClaimRef.APIVersion = ""
	}
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

func (a *AVRInstance) processVRGManifestWork(
	subscription *subv1.Subscription) (rmn.SubscriptionPlacementDecision, error) {
	a.log.Info("Processing Subscription", "name", subscription.Name)

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

	a.advanceToNextDRState(subscription.Name)

	return rmn.SubscriptionPlacementDecision{
		HomeCluster:     homeCluster,
		PeerCluster:     peerCluster,
		PrevHomeCluster: prevHomeCluster,
	}, nil
}

func (a *AVRInstance) findHomeClusterFromClonedPlRule() (string, error) {
	a.log.Info("Finding the next home cluster from placementRule", "name", a.clonedPlacementRule.Name)

	if len(a.clonedPlacementRule.Status.Decisions) == 0 {
		return "", fmt.Errorf("no decisions were found in placementRule %s", a.clonedPlacementRule.Name)
	}

	const requiredClusterReplicas = 1

	if a.clonedPlacementRule.Spec.ClusterReplicas != nil &&
		*a.clonedPlacementRule.Spec.ClusterReplicas != requiredClusterReplicas {
		return "", fmt.Errorf("PlacementRule %s Required cluster replicas %d != %d",
			a.clonedPlacementRule.Name, requiredClusterReplicas, *a.clonedPlacementRule.Spec.ClusterReplicas)
	}

	return a.clonedPlacementRule.Status.Decisions[0].ClusterName, nil
}

func (a *AVRInstance) validateHomeClusterSelection(
	subscription *subv1.Subscription, newHomeCluster string) (string, error) {
	if _, found := a.instance.Status.Decisions[subscription.Name]; !found {
		return newHomeCluster, nil
	}

	action := a.instance.Spec.Action
	if action == "" {
		return newHomeCluster, fmt.Errorf("action not set for AVR %s", a.instance.Name)
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
			return "", fmt.Errorf("miconfiguration detected on failback! (n:%s,p:%s)", newHomeCluster, d.PrevHomeCluster)
		}
	}

	return "", fmt.Errorf("unknown error (n:%s,avrStatus:%v)", newHomeCluster, a.instance.Status)
}

func (a *AVRInstance) selectPlacementDecision(
	subscription *subv1.Subscription) (string, string, error) {
	a.log.Info("Selecting placement decisions for subscription", "name", subscription.Name)

	return a.extractHomeClusterAndPeerCluster(subscription)
}

func (a *AVRInstance) extractHomeClusterAndPeerCluster(
	subscription *subv1.Subscription) (string, string, error) {
	const empty = ""

	a.log.Info("Extracting home and peer clusters", "Subscription", subscription.Name,
		"UserPlacementRule", a.userPlacementRule.Name)

	subStatuses := subscription.Status.Statuses

	if subStatuses == nil {
		return empty, empty,
			fmt.Errorf("invalid subscription Status.Statuses. Subscription %s", subscription.Name)
	}

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

	switch {
	case subStatuses[cl1] != nil:
		homeCluster = cl1
		peerCluster = cl2
	case subStatuses[cl2] != nil:
		homeCluster = cl2
		peerCluster = cl1
	default:
		return empty, empty, fmt.Errorf("subscription %s has no destionation matching DRClusterPeers %v - SubStatuses %v",
			subscription.Name, clusterPeers.Spec.ClusterNames, subStatuses)
	}

	return homeCluster, peerCluster, nil
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

		a.log.Info("Creating ManifestWork", "MW", mw)

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
	mcv, err := r.getOrCreateManagedClusterView(meta, viewscope)
	if err != nil {
		return errorswrapper.Wrap(err, "getManagedClusterResource failed")
	}

	// get query results
	recentConditions := filterByConditionStatus(getMostRecentConditions(mcv.Status.Conditions), metav1.ConditionTrue)

	// want single recent Condition with correct Type; otherwise: bad path
	switch len(recentConditions) {
	case 0:
		err = errors.NewNotFound(schema.GroupResource{}, "failed to find a resource")
	case 1:
		if recentConditions[0].Type != fndv2.ConditionViewProcessing {
			err = errors.NewNotFound(schema.GroupResource{}, "found invalid Condition.Type for ManagedClusterView results")
		}
	default:
		err = fmt.Errorf("found multiple resources with ManagedClusterView - this should not be possible")
	}

	if err != nil {
		return errorswrapper.Wrap(err, "getManagedClusterResource results")
	}

	// good path: convert raw data to usable object
	err = json.Unmarshal(mcv.Status.Result.Raw, resource)
	if err != nil {
		return errorswrapper.Wrap(err, "failed to Unmarshal data from ManagedClusterView to resource")
	}

	return nil // success
}

/*
Description: create a new ManagedClusterView object, or update the existing one with the same name.
Requires:
	1) meta: specifies MangedClusterView name and managed cluster search information
	2) viewscope: once the managed cluster is found, use this information to find the resource.
		Optional params: Namespace, Resource, Group, Version, Kind. Resource can be used by itself, Kind requires Version
Returns: ManagedClusterView, error
*/
func (r *ApplicationVolumeReplicationReconciler) getOrCreateManagedClusterView(
	meta metav1.ObjectMeta, viewscope fndv2.ViewScope) (*fndv2.ManagedClusterView, error) {
	mcv := &fndv2.ManagedClusterView{
		ObjectMeta: meta,
		Spec: fndv2.ViewSpec{
			Scope: viewscope,
		},
	}

	err := r.Get(context.TODO(), types.NamespacedName{Name: meta.Name, Namespace: meta.Namespace}, mcv)
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.Create(context.TODO(), mcv)
		}

		if err != nil {
			return nil, errorswrapper.Wrap(err, "failed to getOrCreateManagedClusterView")
		}
	}

	if mcv.Spec.Scope != viewscope {
		r.Log.Info("WARNING: existing ManagedClusterView has different ViewScope than desired one")
	}

	return mcv, nil
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

func (a *AVRInstance) advanceToNextDRState(subscriptionName string) {
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

	a.log.Info(fmt.Sprintf("advanceToNextDRState: current state '%s' - next state '%s'",
		a.instance.Status.LastKnownDRStates[subscriptionName], nextState))

	a.instance.Status.LastKnownDRStates[subscriptionName] = nextState
	a.needStatusUpdate = true
}
