/*
Copyright 2021.

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

	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	plrv1 "github.com/open-cluster-management/multicloud-operators-placementrule/pkg/apis/apps/v1"
	subv1 "github.com/open-cluster-management/multicloud-operators-subscription/pkg/apis/apps/v1"

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

// AppVolumeReplicationReconciler reconciles a AppVolumeReplication object
type AppVolumeReplicationReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
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
	logger.Info("processing reconcile loop")

	avr := &ramendrv1alpha1.AppVolumeReplication{}

	err := r.Client.Get(context.TODO(), req.NamespacedName, avr)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Return and don't requeue
			return ctrl.Result{}, nil
		}

		// requeue the request
		return ctrl.Result{}, moreerrors.Wrap(err, "failed to get AVR object")
	}

	primaryCluster, secondaryCluster, err := r.createAvgManifestWork(avr)
	if err != nil {
		logger.Error(err, "failed to create or update AVG ManifestWork")

		return ctrl.Result{}, err
	}

	if err := r.updateAppVolumeReplicationStatus(ctx, avr, primaryCluster, secondaryCluster); err != nil {
		logger.Error(err, "failed to update status")

		return ctrl.Result{}, err
	}

	logger.Info("created VolumeReplicationGroup manifest for ", "primary cluster", primaryCluster)

	return ctrl.Result{}, nil
}

func (r *AppVolumeReplicationReconciler) createAvgManifestWork(
	avr *ramendrv1alpha1.AppVolumeReplication) (string, string, error) {
	const empty string = ""

	// Select cluster decisions from all Subscriptions of an app.
	placementDecisions, primaryCluster, err := r.selectPlacementDecisions(avr.Namespace)
	if err != nil {
		r.Log.Info("failed to get placement targets from subscriptions", "Namespace", avr.Namespace, " error: ", err)

		return empty, empty, err
	}

	if primaryCluster == "" {
		return empty, empty,
			fmt.Errorf("no primary cluster configured in any of the subscriptions in namespace %s", avr.Namespace)
	}

	secondaryCluster, err := r.selectSecondaryClusterFromAVRPlacement(avr.Namespace, avr.Spec.Placement, primaryCluster)
	if err != nil {
		r.Log.Error(err, "unable to get secondary cluster")

		return empty, empty, err
	}

	if secondaryCluster == "" {
		return empty, empty, fmt.Errorf("no secondary cluster found in namespace %s", avr.Namespace)
	}

	// Validate primary and secondary clusters
	_, p := placementDecisions[primaryCluster]
	_, s := placementDecisions[secondaryCluster]

	if !p || !s {
		keys := make([]string, 0, len(placementDecisions))
		for k := range placementDecisions {
			keys = append(keys, k)
		}

		r.Log.Info("disjoint set of clusters detected",
			"Subscripts clusters:", keys,
			"AVR clusters:", primaryCluster+","+secondaryCluster)

		return empty, empty, fmt.Errorf("invalid AVR placement rule configuration. PrimaryCluster: %s, SecondaryCluster: %s",
			primaryCluster, secondaryCluster)
	}

	if err := r.createOrUpdateManifestWork(avr.Name, avr.Namespace, primaryCluster, secondaryCluster); err != nil {
		r.Log.Error(err, "failed to create or update manifest")

		return empty, empty, err
	}

	return primaryCluster, secondaryCluster, nil
}

func (r *AppVolumeReplicationReconciler) updateAppVolumeReplicationStatus(ctx context.Context,
	avr *ramendrv1alpha1.AppVolumeReplication, primaryCluster string, secondaryCluster string) error {
	r.Log.Info("updating AVR status")

	avr.Status = ramendrv1alpha1.AppVolumeReplicationStatus{
		PrimaryCluster:   primaryCluster,
		SecondaryCluster: secondaryCluster,
	}
	if err := r.Client.Status().Update(ctx, avr); err != nil {
		return moreerrors.Wrap(err, "failed to update AVR status")
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AppVolumeReplicationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ramendrv1alpha1.AppVolumeReplication{}).
		Complete(r)
}

func (r *AppVolumeReplicationReconciler) selectPlacementDecisions(namespace string) (map[string]bool, string, error) {
	subscriptionList := &subv1.SubscriptionList{}
	listOptions := &client.ListOptions{Namespace: namespace}
	placementDecisions := make(map[string]bool)

	var primaryCluster string

	err := r.Client.List(context.TODO(), subscriptionList, listOptions)
	if err != nil {
		if !errors.IsNotFound(err) {
			// requeue the request
			return nil, "", fmt.Errorf("failed to get subscription list with namespace %s", namespace)
		}
	}

	for _, subscription := range subscriptionList.Items {
		if subscription.Status.Phase != subv1.SubscriptionPropagated || subscription.Status.Statuses == nil {
			continue
		}

		r.extractPlacements(subscription, placementDecisions, &primaryCluster)
	}

	return placementDecisions, primaryCluster, nil
}

func (r *AppVolumeReplicationReconciler) extractPlacements(
	subscription subv1.Subscription, placementDecisions map[string]bool, primaryCluster *string) {
	pl := subscription.Spec.Placement
	if pl != nil && pl.PlacementRef != nil {
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
			return
		}

		r.extractPlacementDecisionsAndPrimary(
			placementRule.Status.Decisions, subscription.Status.Statuses, placementDecisions, primaryCluster)
	}
}

func (r *AppVolumeReplicationReconciler) extractPlacementDecisionsAndPrimary(
	decisions []plrv1.PlacementDecision, subStatuses subv1.SubscriptionClusterStatusMap,
	placementDecisions map[string]bool, primaryCluster *string) {
	if len(decisions) == 0 {
		return
	}

	for _, decision := range decisions {
		placementDecisions[decision.ClusterName] = true
		// pick the first primary cluster found
		if *primaryCluster == "" && subStatuses[decision.ClusterName] != nil {
			*primaryCluster = decision.ClusterName
		}
	}
}

func (r *AppVolumeReplicationReconciler) selectSecondaryClusterFromAVRPlacement(
	namespace string, placement *plrv1.Placement, primaryCluster string) (string, error) {
	var secondaryCluster string

	if placement != nil && placement.PlacementRef != nil {
		plRef := placement.PlacementRef
		if plRef.Namespace == "" {
			plRef.Namespace = namespace
		}

		placementRule := &plrv1.PlacementRule{}

		err := r.Client.Get(context.TODO(), types.NamespacedName{Name: plRef.Name, Namespace: plRef.Namespace}, placementRule)
		if err != nil {
			return "", moreerrors.Wrap(err, "failed to get placement rule")
		}

		if len(placementRule.Status.Decisions) == 0 {
			return "", fmt.Errorf("AVR PlacementRule has no decisions. Placement rule name %s", plRef.Name)
		}

		for _, decision := range placementRule.Status.Decisions {
			// select the secondary cluster
			if primaryCluster != decision.ClusterName {
				secondaryCluster = decision.ClusterName

				break
			}
		}
	}

	return secondaryCluster, nil
}

func (r *AppVolumeReplicationReconciler) createOrUpdateManifestWork(name string, namespace string,
	primaryCluster string, secondaryCluster string) error {
	r.Log.Info("attempt to create and update ManifestWork")
	manifestWork := r.createManifestWork(name, namespace, primaryCluster, secondaryCluster)
	found := &ocmworkv1.ManifestWork{}

	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: manifestWork.Name, Namespace: namespace}, found)
	if err != nil {
		if !errors.IsNotFound(err) {
			r.Log.Error(err, "Could not fetch ManifestWork")

			return moreerrors.Wrap(err, "failed to fetch manifestWork")
		}

		r.Log.Info("Creating ManifestWork", "ManifestWork", manifestWork)

		if err := r.Client.Create(context.TODO(), manifestWork); err != nil {
			return moreerrors.Wrap(err, "failed to create manifestWork")
		}
	} else if !reflect.DeepEqual(found.Spec, manifestWork.Spec) {
		manifestWork.Spec.DeepCopyInto(&found.Spec)
		err := r.Client.Update(context.TODO(), found)
		if err != nil {
			return moreerrors.Wrap(err, "failed to update manifestWork")
		}
	}

	return nil
}

func (r *AppVolumeReplicationReconciler) createManifestWork(name string, namespace string,
	primaryCluster string, secondaryCluster string) *ocmworkv1.ManifestWork {
	vrgClientManifest := r.createVRGClientManifest(name, namespace, primaryCluster, secondaryCluster)

	manifestwork := &ocmworkv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-vrg-manifestwork", primaryCluster),
			Namespace: namespace, Labels: map[string]string{"app": "VRG"},
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
	primaryCluster string, secondaryCluster string) *ocmworkv1.Manifest {
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
	`, name, namespace, primaryCluster, secondaryCluster))
	vrgClientManifest := &ocmworkv1.Manifest{}
	vrgClientManifest.RawExtension = runtime.RawExtension{Raw: vrgClientJSON}

	return vrgClientManifest
}
