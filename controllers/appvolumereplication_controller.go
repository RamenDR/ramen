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
	"strings"

	spokeClusterV1 "github.com/open-cluster-management/api/cluster/v1"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	plrv1 "github.com/open-cluster-management/multicloud-operators-placementrule/pkg/apis/apps/v1"
	"github.com/open-cluster-management/multicloud-operators-placementrule/pkg/utils"
	subv1 "github.com/open-cluster-management/multicloud-operators-subscription/pkg/apis/apps/v1"
	"github.com/prometheus/common/log"

	"github.com/go-logr/logr"
	moreerrors "github.com/pkg/errors"
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
	ManifestWorkNameFormat string = "%s-vrg-manifestwork"
	// RamenDRLabelName is the label used to pause/unpause a subsription
	RamenDRLabelName string = "ramendr"
)

// AppVolumeReplicationReconciler reconciles a AppVolumeReplication object
type AppVolumeReplicationReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// SetupWithManager sets up the controller with the Manager.
func (r *AppVolumeReplicationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ramendrv1alpha1.AppVolumeReplication{}).
		Complete(r)
}

//nolint:lll
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=appvolumereplications,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=appvolumereplications/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=appvolumereplications/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the AppVolumeReplication object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.7.0/pkg/reconcile
func (r *AppVolumeReplicationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.Log.WithValues("appvolumereplication", req.NamespacedName)
	logger.Info("Entering reconcile loop")

	defer logger.Info("Exiting reconcile loop")

	avr := &ramendrv1alpha1.AppVolumeReplication{}

	err := r.Client.Get(context.TODO(), req.NamespacedName, avr)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, moreerrors.Wrap(err, "failed to get AVR object")
	}

	subscriptionList := &subv1.SubscriptionList{}
	listOptions := &client.ListOptions{Namespace: avr.Namespace}

	err = r.Client.List(context.TODO(), subscriptionList, listOptions)
	if err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to get the subscription list for namespace %s", avr.Namespace)
		}

		return ctrl.Result{}, moreerrors.Wrap(err, "failed to get placement rule")
	}

	placementDecisions := r.processSubscriptions(avr, subscriptionList)

	if err != nil {
		return ctrl.Result{}, moreerrors.Wrap(err, "failed to get placement rule")
	}

	if len(placementDecisions) == 0 {
		logger.Info("Requeing due to not finding any placement decisions")

		return ctrl.Result{Requeue: true}, nil
	}

	if err := r.updateAVRStatus(ctx, avr, placementDecisions); err != nil {
		logger.Error(err, "failed to update status")

		return ctrl.Result{}, err
	}

	logger.Info("Completed creating manifestwork", "Placement Decisions", len(avr.Status.Decisions),
		"Subsriptions", len(subscriptionList.Items))

	if len(avr.Status.Decisions) != len(subscriptionList.Items) {
		result := fmt.Sprintf("AVR decisions a compared to subscriptions %d:%d",
			len(avr.Status.Decisions), len(subscriptionList.Items))
		logger.Info(result)

		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
}

func (r *AppVolumeReplicationReconciler) processSubscriptions(
	avr *ramendrv1alpha1.AppVolumeReplication,
	subscriptionList *subv1.SubscriptionList) ramendrv1alpha1.SubscriptionPlacementDecisionMap {
	placementDecisions := ramendrv1alpha1.SubscriptionPlacementDecisionMap{}

	for _, subscription := range subscriptionList.Items {
		if r.isSubsriptionPaused(subscription.GetLabels()) {
			//nolint:wsl
			if r.isFailover(subscription) {
				// TODO: APPLY PV K8s metadata to the NEW target cluster

				// Unpause subscription
				r.unpauseSubscription(subscription)
			} else if avr.Spec.FailoverCluster != "" {
				// We couldn't detect why the subsription is paused. We'll the failover using the FailoverCluster

				// TODO: APPLY PV K8s metadata to the NEW target cluster

				// Unpause subscription
				r.unpauseSubscription(subscription)
			}

			// Stop processing this subscription. We'll wait for the next Reconciler iteration
			continue
		}

		// This subscription is ready for manifest (VRG) creation
		r.Log.Info("Subscription is unpaused", "Name", subscription.Name)

		subPlDecision, err := r.createVRGForSubscription(subscription)
		if err != nil {
			r.Log.Error(err, fmt.Sprintf("Failed to create VRG for Subscription %s", subscription.Name))

			continue
		}

		placementDecisions[subscription.Name] = subPlDecision
	}

	r.Log.Info("Returning Placement Decisions", "Total", len(placementDecisions))

	return placementDecisions
}

func (r *AppVolumeReplicationReconciler) isFailover(subscription subv1.Subscription) bool {
	return subscription.Status.Phase != ""
}

func (r *AppVolumeReplicationReconciler) createVRGForSubscription(
	subscription subv1.Subscription) (*ramendrv1alpha1.SubscriptionPlacementDecision, error) {
	// Create a ManifestWork that represents the VolumeReplicationGroup CR for the hub to
	// deploy on the managed cluster. A manifest workload is defined as a set of Kubernetes
	// resources. The ManifestWork must be created in the cluster namespace on the hub, so that
	// agent on the corresponding managed cluster can access this resource and deploy on the
	// managed cluster. We create one ManifestWork for each VRG CR.
	homeCluster, peerCluster, err := r.createVRGManifestWork(subscription)
	if err != nil {
		r.Log.Info("failed to create or update ManifestWork for", "subscription:", subscription.Name, "error:", err)

		return nil, moreerrors.Wrap(err, "failed to create or update ManifestWork")
	}

	r.Log.Info("created VolumeReplicationGroup manifest for ", "subscription:", subscription.Name,
		"HomeCluster:", homeCluster, "PeerCluster:", peerCluster)

	return &ramendrv1alpha1.SubscriptionPlacementDecision{
		HomeCluster: homeCluster,
		PeerCluster: peerCluster,
	}, nil
}

func (r *AppVolumeReplicationReconciler) unpauseSubscription(subscription subv1.Subscription) {
	labels := subscription.GetLabels()
	if labels == nil {
		r.Log.Info("no labels found for subscription", "name", subscription.Name)

		return
	}

	if r.isSubsriptionPaused(labels) {
		log.Info("Unpausing subscription: ", subscription)

		labels[subv1.LabelSubscriptionPause] = "false"
		subscription.SetLabels(labels)

		newInstance := new(subv1.Subscription)
		subscription.DeepCopyInto(newInstance)

		err := r.Client.Update(context.TODO(), newInstance)
		if err != nil {
			r.Log.Error(err, "failed to update labels for subscription", "name", subscription.Name)
		}
	}
}

func (r *AppVolumeReplicationReconciler) isSubsriptionPaused(labels map[string]string) bool {
	return labels != nil &&
		labels[RamenDRLabelName] != "" &&
		strings.EqualFold(labels[RamenDRLabelName], "protected") &&
		labels[subv1.LabelSubscriptionPause] != "" &&
		strings.EqualFold(labels[subv1.LabelSubscriptionPause], "true")
}

func (r *AppVolumeReplicationReconciler) createVRGManifestWork(
	subscription subv1.Subscription) (string, string, error) {
	const empty string = ""

	// Select cluster decisions from each Subscriptions of an app.
	homeCluster, peerCluster, err := r.selectPlacementDecision(&subscription)
	if err != nil {
		return empty, empty, fmt.Errorf("failed to get placement targets from subscription %s with error: %w",
			subscription.Name, err)
	}

	if homeCluster == "" {
		return empty, empty,
			fmt.Errorf("no home cluster configured in subscription %s", subscription.Name)
	}

	if peerCluster == "" {
		return empty, empty, fmt.Errorf("no peer cluster found for subscription %s", subscription.Name)
	}

	if err := r.doCreateOrUpdateVRGManifestWork(
		subscription.Name, subscription.Namespace, homeCluster, peerCluster); err != nil {
		r.Log.Error(err, "failed to create or update manifest")

		return empty, empty, err
	}

	return homeCluster, peerCluster, nil
}

func (r *AppVolumeReplicationReconciler) selectPlacementDecision(
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

func (r *AppVolumeReplicationReconciler) extractHomeClusterAndPeerCluster(
	subscription *subv1.Subscription, placementRule *plrv1.PlacementRule) (string, string, error) {
	subStatuses := subscription.Status.Statuses

	const clusterCount = 2

	// decisions := placementRule.Status.Decisions
	clmap, err := utils.PlaceByGenericPlacmentFields(
		r.Client, placementRule.Spec.GenericPlacementFields, nil, placementRule)
	if err != nil {
		return "", "", fmt.Errorf("failed to get cluster map for placement %s error: %w", placementRule.Name, err)
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

func (r *AppVolumeReplicationReconciler) doCreateOrUpdateVRGManifestWork(name string, namespace string,
	homeCluster string, peerCluster string) error {
	r.Log.Info("attempt to create and update ManifestWork")

	manifestWork := r.createManifestWork(name, namespace, homeCluster, peerCluster)

	found := &ocmworkv1.ManifestWork{}

	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: manifestWork.Name, Namespace: homeCluster}, found)
	if err != nil {
		if !errors.IsNotFound(err) {
			r.Log.Error(err, "Could not fetch ManifestWork")

			return moreerrors.Wrap(err, "failed to fetch manifestWork")
		}

		r.Log.Info("Creating ManifestWork", "ManifestWork", manifestWork)

		return r.Client.Create(context.TODO(), manifestWork)
	} else if !reflect.DeepEqual(found.Spec, manifestWork.Spec) {
		manifestWork.Spec.DeepCopyInto(&found.Spec)

		r.Log.Info("Updating ManifestWork", "ManifestWork", manifestWork)

		return r.Client.Update(context.TODO(), found)
	}

	return nil
}

func (r *AppVolumeReplicationReconciler) createManifestWork(name string, namespace string,
	homeCluster string, peerCluster string) *ocmworkv1.ManifestWork {
	vrgClientManifest := r.createVRGClientManifest(name, namespace, homeCluster, peerCluster)

	manifestwork := &ocmworkv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf(ManifestWorkNameFormat, homeCluster),
			Namespace: homeCluster, Labels: map[string]string{"app": "VRG"},
		},
		Spec: ocmworkv1.ManifestWorkSpec{
			Workload: ocmworkv1.ManifestsTemplate{
				Manifests: []ocmworkv1.Manifest{
					*vrgClientManifest,
				},
			},
		},
	}

	return manifestwork
}

func (r *AppVolumeReplicationReconciler) createVRGClientManifest(name string, namespace string,
	homeCluster string, peerCluster string) *ocmworkv1.Manifest {
	// TODO: try to use the VRG object, then marshal it into byte array.
	vrgClientJSON := []byte(fmt.Sprintf(`
	{
		"apiVersion": "ramendr.openshift.io/v1alpha1",
		"kind": "VolumeReplicationGroup",
		"metadata": {
		   "name": "%s",
		   "namespace": "%s"
		},
		"spec": {
		   "affinedCluster": "us-central",
		   "applicationLabels": {
			  "matchExpressions": [
				 {
					"key": "apptype",
					"operator": "NotIn",
					"values": [
					   "stateless"
					]
				 },
				 {
					"key": "backend",
					"operator": "In",
					"values": [
					   "ceph"
					]
				 }
			  ],
			  "matchLabels": {
				 "appclass": "gold",
				 "environment": "dev.AZ1"
			  }
		   },
		   "applicationName": "sample-app",
		   "asyncRPOGoalSeconds": 3600,
		   "clusterPeersList": [
			  "%s",
			  "%s"
		   ]
		}
	 }
	`, name, namespace, homeCluster, peerCluster))
	vrgClientManifest := &ocmworkv1.Manifest{}
	vrgClientManifest.RawExtension = runtime.RawExtension{Raw: vrgClientJSON}

	return vrgClientManifest
}

func (r *AppVolumeReplicationReconciler) updateAVRStatus(
	ctx context.Context,
	avr *ramendrv1alpha1.AppVolumeReplication, placementDecisions ramendrv1alpha1.SubscriptionPlacementDecisionMap) error {
	r.Log.Info("updating AVR status")

	// Merge (if necessary) the placement decisions into the AVR.Status.Decisions
	if avr.Status.Decisions == nil {
		avr.Status.Decisions = placementDecisions
	} else {
		for k, v := range placementDecisions {
			avr.Status.Decisions[k] = v
		}
	}

	return r.Client.Status().Update(ctx, avr)
}
