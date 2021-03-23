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
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
)

const (
	// ManifestWorkNameFormat is a formated a string used to generate the manifest name
	// The format is name-namespace-type-mw where:
	// - name is the subscription name
	// - namespace is the subscription namespace
	// - type is either vrg OR pv string
	ManifestWorkNameFormat string = "%s-%s-%s-mw"
	// RamenDRLabelName is the label used to pause/unpause a subsription
	RamenDRLabelName string = "ramendr"
)

// ApplicationVolumeReplicationReconciler reconciles a ApplicationVolumeReplication object
type ApplicationVolumeReplicationReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// SetupWithManager sets up the controller with the Manager.
func (r *ApplicationVolumeReplicationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ramendrv1alpha1.ApplicationVolumeReplication{}).
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

	err := r.Client.Get(context.TODO(), req.NamespacedName, avr)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, errorswrapper.Wrap(err, "failed to get AVR object")
	}

	subscriptionList := &subv1.SubscriptionList{}
	listOptions := &client.ListOptions{Namespace: avr.Namespace}

	err = r.Client.List(context.TODO(), subscriptionList, listOptions)
	if err != nil {
		if !errors.IsNotFound(err) {
			logger.Error(err, "failed to find subscription list", "namespace", avr.Namespace)

			return ctrl.Result{Requeue: true}, nil
		}

		return ctrl.Result{}, errorswrapper.Wrap(err, "failed to list subscriptions")
	}

	placementDecisions, requeue := r.processSubscriptions(avr, subscriptionList)
	if len(placementDecisions) == 0 {
		logger.Info("no placement decisions found", "namespace", avr.Namespace)

		return ctrl.Result{Requeue: requeue}, nil
	}

	if err := r.updateAVRStatus(ctx, avr, placementDecisions); err != nil {
		logger.Error(err, "failed to update status")

		return ctrl.Result{Requeue: true}, nil
	}

	logger.Info("Completed creating manifestwork", "Placement Decisions", len(avr.Status.Decisions),
		"Subsriptions", len(subscriptionList.Items), "requeue", requeue)

	return ctrl.Result{Requeue: requeue}, nil
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

func (r *ApplicationVolumeReplicationReconciler) processSubscriptionList(
	subscriptionList *subv1.SubscriptionList) (
	ramendrv1alpha1.SubscriptionPlacementDecisionMap, bool) {
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
	const requeue = true
	// Check to see if this subscription is paused for DR. If it is, then restore PVs to the new destination
	// cluster, unpause the subscription, and skip it until the next reconciler iteration
	if r.isSubsriptionPausedForDR(subscription.GetLabels()) {
		if err := r.processPausedSubscription(avr, subscription); err != nil {
			r.Log.Error(err, fmt.Sprintf("failed to process paused Subscription %s", subscription.Name))
		}
		// Stop processing this subscription. We'll wait for the next Reconciler iteration
		r.Log.Info("Skipping paused subscription", "name", subscription.Name)

		return nil, requeue
	}

	// Skip this subscription if a manifestwork already exist
	if found, err := r.findVRGManifestWork(avr, subscription); err != nil {
		return nil, requeue
	} else if found {
		r.Log.Info("Skipping existing mainifestwork for subscription", "name", subscription.Name)

		return nil, !requeue
	}

	// This subscription is ready for manifest (VRG) creation
	r.Log.Info("Subscription is unpaused", "Name", subscription.Name)

	placementDecision, err := r.processUnpausedSubscription(subscription)
	if err != nil {
		return nil, requeue
	}

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
	subscription *subv1.Subscription) error {
	r.Log.Info("Processing paused subscription", "name", subscription.Name)
	// find target cluster (which can be the failover cluster)
	homeClusterName := r.findNextHomeCluster(avr, subscription)

	if homeClusterName == "" {
		return errorswrapper.New("failed to find new home cluster")
	}

	err := r.deleteExistingManfiestWork(avr, subscription)
	if err != nil {
		return err
	}

	err = r.restoreBackedupPVs(subscription, homeClusterName)
	if err != nil {
		return err
	}

	return r.unpauseSubscription(subscription)
}

func (r *ApplicationVolumeReplicationReconciler) findVRGManifestWork(
	avr *ramendrv1alpha1.ApplicationVolumeReplication,
	subscription *subv1.Subscription) (bool, error) {
	const notFound = false

	if d, found := avr.Status.Decisions[subscription.Name]; found {
		mw := &ocmworkv1.ManifestWork{}
		vrgMWName := fmt.Sprintf(ManifestWorkNameFormat, subscription.Name, subscription.Namespace, "vrg")

		err := r.Client.Get(context.TODO(), types.NamespacedName{Name: vrgMWName, Namespace: d.HomeCluster}, mw)
		if err != nil {
			if errors.IsNotFound(err) {
				r.Log.Info("ManifestWork not found for Subscription", "name", subscription.Name)

				return notFound, nil
			}

			return notFound, errorswrapper.Wrap(err, "failed to retrieve manifestwork")
		}

		return found, nil
	}

	return notFound, nil
}

func (r *ApplicationVolumeReplicationReconciler) deleteExistingManfiestWork(
	avr *ramendrv1alpha1.ApplicationVolumeReplication,
	subscription *subv1.Subscription) error {
	if d, found := avr.Status.Decisions[subscription.Name]; found {
		mw := &ocmworkv1.ManifestWork{}
		vrgMWName := fmt.Sprintf(ManifestWorkNameFormat, subscription.Name, subscription.Namespace, "vrg")

		err := r.Client.Get(context.TODO(), types.NamespacedName{Name: vrgMWName, Namespace: d.HomeCluster}, mw)
		if err != nil {
			if errors.IsNotFound(err) {
				return nil
			}

			r.Log.Error(err, "Could not fetch ManifestWork")

			return errorswrapper.Wrap(err, "failed to retrieve manifestWork")
		}

		r.Log.Info("deleting ManifestWork", "name", mw.Name)

		return r.Client.Delete(context.TODO(), mw)
	}

	return nil
}

func (r *ApplicationVolumeReplicationReconciler) restoreBackedupPVs(
	subscription *subv1.Subscription, homeClusterName string) error {
	r.Log.Info("Restoring PVs to managed cluster", "name", homeClusterName)

	// TODO: get PVs from S3
	pvList, err := r.listPVsFromS3Store()
	if err != nil {
		return errorswrapper.Wrap(err, "failed to retrieve PVs from S3 store")
	}

	r.Log.Info("PV k8s objects", "len", len(pvList))
	// Create manifestwork for all PVs for this subscription
	return r.createOrUpdatePVsManifestWork(subscription.Name, subscription.Namespace, homeClusterName, pvList)
}

func (r *ApplicationVolumeReplicationReconciler) createOrUpdatePVsManifestWork(
	name string, namespace string, homeClusterName string, pvList []string) error {
	r.Log.Info("Creating manifest work for PVs", "subscription",
		name, "cluster", homeClusterName, "PV count", len(pvList))

	manifestWork := r.generatePVManifestWork(name, namespace, homeClusterName, pvList)

	return r.createOrUpdateManifestWork(manifestWork, homeClusterName)
}

func (r *ApplicationVolumeReplicationReconciler) unpauseSubscription(subscription *subv1.Subscription) error {
	labels := subscription.GetLabels()
	if labels == nil {
		r.Log.Info("no labels found for subscription", "name", subscription.Name)

		return fmt.Errorf("failed to find labels for subscription %s", subscription.Name)
	}

	r.Log.Info(fmt.Sprintf("Unpausing subscription (%v)", subscription))

	labels[subv1.LabelSubscriptionPause] = "false"
	subscription.SetLabels(labels)

	return r.Client.Update(context.TODO(), subscription)
}

func (r *ApplicationVolumeReplicationReconciler) processUnpausedSubscription(
	subscription *subv1.Subscription) (ramendrv1alpha1.SubscriptionPlacementDecision, error) {
	homeCluster, peerCluster, err := r.selectPlacementDecision(subscription)
	if err != nil {
		return ramendrv1alpha1.SubscriptionPlacementDecision{}, err
	}

	if err := r.createOrUpdateVRGRolesManifestWork(homeCluster); err != nil {
		r.Log.Error(err, "failed to create or update VolumeReplicationGroup Roles manifest")

		return ramendrv1alpha1.SubscriptionPlacementDecision{}, err
	}

	if err := r.createOrUpdateVRGManifestWork(
		subscription.Name, subscription.Namespace, homeCluster); err != nil {
		r.Log.Error(err, "failed to create or update VolumeReplicationGroup manifest")

		return ramendrv1alpha1.SubscriptionPlacementDecision{}, err
	}

	return ramendrv1alpha1.SubscriptionPlacementDecision{
		HomeCluster: homeCluster,
		PeerCluster: peerCluster,
	}, nil
}

func (r *ApplicationVolumeReplicationReconciler) findNextHomeCluster(
	avr *ramendrv1alpha1.ApplicationVolumeReplication,
	subscription *subv1.Subscription) string {
	// FOR NOW the user has to specify the Failover Cluster.  Later we may derive that
	// from the subscription/placementrule
	return avr.Spec.FailoverClusters[subscription.Name]
}

func (r *ApplicationVolumeReplicationReconciler) selectPlacementDecision(
	subscription *subv1.Subscription) (string, string, error) {
	// The subscription phase describes the phasing of the subscriptions. Propagated means
	// this subscription is the "parent" sitting in hub. Statuses is a map where the key is
	// the cluster name and value is the aggregated status
	if subscription.Status.Phase != subv1.SubscriptionPropagated || subscription.Status.Statuses == nil {
		return "", "", fmt.Errorf("subscription %s not ready", subscription.Name)
	}

	pl := subscription.Spec.Placement
	if pl == nil || pl.PlacementRef == nil {
		return "", "", fmt.Errorf("placement not set for subscription %s", subscription.Name)
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
		return "", "", fmt.Errorf("failed to retrieve placementRule using placementRef %s/%s", plRef.Namespace, plRef.Name)
	}

	return r.extractHomeClusterAndPeerCluster(subscription, placementRule)
}

func (r *ApplicationVolumeReplicationReconciler) extractHomeClusterAndPeerCluster(
	subscription *subv1.Subscription, placementRule *plrv1.PlacementRule) (string, string, error) {
	subStatuses := subscription.Status.Statuses

	const clusterCount = 2

	// decisions := placementRule.Status.Decisions
	clmap, err := utils.PlaceByGenericPlacmentFields(
		r.Client, placementRule.Spec.GenericPlacementFields, nil, placementRule)
	if err != nil {
		return "", "", fmt.Errorf("failed to get cluster map for placement %s error: %w", placementRule.Name, err)
	}

	err = r.filterClusters(placementRule, clmap)
	if err != nil {
		return "", "", fmt.Errorf("failed to filter clusters. Cluster len %d, error (%w)", len(clmap), err)
	}

	if len(clmap) != clusterCount {
		return "", "", fmt.Errorf("PlacementRule %s should have made 2 decisions. Found %d", placementRule.Name, len(clmap))
	}

	idx := 0

	clusters := make([]spokeClusterV1.ManagedCluster, clusterCount)
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
		return "", "", fmt.Errorf("mismatch between placementRule %s decisions and subscription %s statuses",
			placementRule.Name, subscription.Name)
	}

	return homeCluster, peerCluster, nil
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

func (r *ApplicationVolumeReplicationReconciler) createOrUpdateVRGManifestWork(name string, namespace string,
	homeCluster string) error {
	manifestWork, err := r.generateVRGManifestWork(name, namespace, homeCluster)
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
	name string, namespace string, homeClusterName string, pvList []string) *ocmworkv1.ManifestWork {
	manifests := r.generatePVManifest(pvList)

	return r.newManifestWork(
		fmt.Sprintf(ManifestWorkNameFormat, name, namespace, "pv"),
		homeClusterName,
		map[string]string{"app": "PV"},
		manifests)
}

// This function follow a slightly different pattern than the rest, simply because the pvList that come
// from the S3 store will contain PV objects already converted to a string.
func (r *ApplicationVolumeReplicationReconciler) generatePVManifest(pvList []string) []ocmworkv1.Manifest {
	manifests := []ocmworkv1.Manifest{}

	for _, pv := range pvList {
		pvClientManifest := ocmworkv1.Manifest{}
		pvClientManifest.RawExtension = runtime.RawExtension{Raw: []byte(pv)}

		manifests = append(manifests, pvClientManifest)
	}

	return manifests
}

func (r *ApplicationVolumeReplicationReconciler) generateVRGManifestWork(name string, namespace string,
	homeCluster string) (*ocmworkv1.ManifestWork, error) {
	vrgClientManifest, err := r.generateVRGManifest(name, namespace)
	if err != nil {
		r.Log.Error(err, "failed to generate VolumeReplication")

		return nil, err
	}

	manifests := []ocmworkv1.Manifest{*vrgClientManifest}

	return r.newManifestWork(
		fmt.Sprintf(ManifestWorkNameFormat, name, namespace, "vrg"),
		homeCluster,
		map[string]string{"app": "VRG"},
		manifests), nil
}

func (r *ApplicationVolumeReplicationReconciler) generateVRGManifest(name string,
	namespace string) (*ocmworkv1.Manifest, error) {
	return r.generateManifest(&ramendrv1alpha1.VolumeReplicationGroup{
		TypeMeta:   metav1.TypeMeta{Kind: "VolumeReplicationGroup", APIVersion: "ramendr.openshift.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
			PVCSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"appclass":    "gold",
					"environment": "dev.AZ1",
				},
			},
			VolumeReplicationClass: "volume-rep-class",
			ReplicationState:       "Primary",
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

func (r *ApplicationVolumeReplicationReconciler) newManifestWork(name string, homeClusterName string,
	labels map[string]string, manifests []ocmworkv1.Manifest) *ocmworkv1.ManifestWork {
	return &ocmworkv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: homeClusterName, Labels: labels,
		},
		Spec: ocmworkv1.ManifestWorkSpec{
			Workload: ocmworkv1.ManifestsTemplate{
				Manifests: manifests,
			},
		},
	}
}

func (r *ApplicationVolumeReplicationReconciler) createOrUpdateManifestWork(
	work *ocmworkv1.ManifestWork,
	managedClusternamespace string) error {
	found := &ocmworkv1.ManifestWork{}

	err := r.Client.Get(context.TODO(),
		types.NamespacedName{Name: work.Name, Namespace: managedClusternamespace},
		found)
	if err != nil {
		if !errors.IsNotFound(err) {
			r.Log.Error(err, "failed to fetch ManifestWork", "name", work.Name, "namespace", managedClusternamespace)

			return errorswrapper.Wrap(err, "failed to fetch ManifestWork")
		}

		r.Log.Info("Creating", "ManifestWork", work)

		return r.Client.Create(context.TODO(), work)
	}

	if !reflect.DeepEqual(found.Spec, work.Spec) {
		work.Spec.DeepCopyInto(&found.Spec)

		r.Log.Info("Updating", "ManifestWork", work)

		return r.Client.Update(context.TODO(), found)
	}

	return nil
}

func (r *ApplicationVolumeReplicationReconciler) updateAVRStatus(
	ctx context.Context,
	avr *ramendrv1alpha1.ApplicationVolumeReplication,
	placementDecisions ramendrv1alpha1.SubscriptionPlacementDecisionMap) error {
	r.Log.Info("updating AVR status")

	avr.Status = ramendrv1alpha1.ApplicationVolumeReplicationStatus{
		Decisions: placementDecisions,
	}
	if err := r.Client.Status().Update(ctx, avr); err != nil {
		return errorswrapper.Wrap(err, "failed to update AVR status")
	}

	return nil
}

// --- UNIMPLEMENTED ---.  FAKE DATA INSIDE THAT WILL BE REPLACED WITH THE ACTUALL CALL TO S3 BUCKET FOR
// EACH SUBSCRIPTION THAT WE ARE FAILING OVER TO A DIFFERENT MANAGED CLUSTER
func (r *ApplicationVolumeReplicationReconciler) listPVsFromS3Store() ([]string, error) {
	pv1 := `{
		"apiVersion": "v1",
		"kind": "PersistentVolume",
		"metadata": {
		   "name": "pv0001"
		},
		"spec": {
		   "capacity": {
			  "storage": "1Gi"
		   },
		   "accessModes": [
			  "ReadWriteOnce"
		   ],
		   "nfs": {
			  "path": "/tmp",
			  "server": "172.17.0.2"
		   },
		   "persistentVolumeReclaimPolicy": "Recycle",
		   "claimRef": {
			  "name": "claim1",
			  "namespace": "default"
		   }
		}
	 }`

	pv2 := `{
		"apiVersion": "v1",
		"kind": "PersistentVolume",
		"metadata": {
		   "name": "pv0002"
		},
		"spec": {
		   "capacity": {
			  "storage": "1Gi"
		   },
		   "accessModes": [
			  "ReadWriteOnce"
		   ],
		   "nfs": {
			  "path": "/tmp",
			  "server": "172.17.0.2"
		   },
		   "persistentVolumeReclaimPolicy": "Recycle",
		   "claimRef": {
			  "name": "claim2",
			  "namespace": "default"
		   }
		}
	 }`

	var pvList []string
	pvList = append(pvList, pv1, pv2)
	// THIS CHECK IS ONLY TO SATISFY THE LINTER WITHOUT MAKING TOO MUCH (UNNECESSARY) CHANGES TO THIS FUNCTION
	// SO, IGNORE FOR NOW
	if len(pvList) == 0 {
		return pvList, fmt.Errorf("array length mismatch %d", len(pvList))
	}

	return pvList, nil
}
