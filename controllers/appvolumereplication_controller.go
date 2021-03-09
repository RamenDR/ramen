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

const manifestWorkNameFormat string = "%s-vrg-manifestwork"

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

	//
	// TODO failover case will be handled before we get to this point
	// On a failover, AVR.Spec.FailedCluster should be filled in.
	//

	// Create a ManifestWork that represents the VolumeReplicationGroup CR for the hub to
	// deploy on the managed cluster. A manifest workload is defined as a set of Kubernetes
	// resources. The ManifestWork must be created in the cluster namespace on the hub, so that
	// agent on the corresponding managed cluster can access this resource and deploy on the
	// managed cluster.
	homeCluster, peerCluster, err := r.createVrgManifestWork(avr)
	if err != nil {
		logger.Error(err, "failed to create or update AVG ManifestWork")

		return ctrl.Result{}, err
	}

	if err := r.updateAvrStatus(ctx, avr, homeCluster, peerCluster); err != nil {
		logger.Error(err, "failed to update status")

		return ctrl.Result{}, err
	}

	logger.Info("created VolumeReplicationGroup manifest for ", "home cluster", homeCluster)

	return ctrl.Result{}, nil
}

func (r *AppVolumeReplicationReconciler) createVrgManifestWork(
	avr *ramendrv1alpha1.AppVolumeReplication) (string, string, error) {
	const empty string = ""

	// Select cluster decisions from all Subscriptions of an app.
	placementDecisions, homeCluster, err := r.selectPlacementDecisions(avr.Namespace)
	if err != nil {
		r.Log.Info("failed to get placement targets from subscriptions", "Namespace", avr.Namespace, " error: ", err)

		return empty, empty, err
	}

	if homeCluster == "" {
		return empty, empty,
			fmt.Errorf("no home cluster configured in any of the subscriptions in namespace %s", avr.Namespace)
	}

	peerCluster, err := r.selectPeerClusterFromAVRPlacement(avr.Namespace, avr.Spec.Placement, homeCluster)
	if err != nil {
		r.Log.Error(err, "unable to get peer cluster")

		return empty, empty, err
	}

	if peerCluster == "" {
		return empty, empty, fmt.Errorf("no peer cluster found in namespace %s", avr.Namespace)
	}

	// Validate home and peer clusters
	_, p := placementDecisions[homeCluster]
	_, s := placementDecisions[peerCluster]

	if !p || !s {
		keys := make([]string, 0, len(placementDecisions))
		for k := range placementDecisions {
			keys = append(keys, k)
		}

		r.Log.Info("disjoint set of clusters detected",
			"Subscripts clusters:", keys,
			"AVR clusters:", homeCluster+","+peerCluster)

		return empty, empty, fmt.Errorf("invalid AVR placement rule configuration. HomeCluster: %s, PeerCluster: %s",
			homeCluster, peerCluster)
	}

	if err := r.createOrUpdateManifestWork(avr.Name, avr.Namespace, homeCluster, peerCluster); err != nil {
		r.Log.Error(err, "failed to create or update manifest")

		return empty, empty, err
	}

	return homeCluster, peerCluster, nil
}

func (r *AppVolumeReplicationReconciler) updateAvrStatus(ctx context.Context,
	avr *ramendrv1alpha1.AppVolumeReplication, homeCluster string, peerCluster string) error {
	r.Log.Info("updating AVR status")

	avr.Status = ramendrv1alpha1.AppVolumeReplicationStatus{
		HomeCluster: homeCluster,
		PeerCluster: peerCluster,
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

	var homeCluster string

	err := r.Client.List(context.TODO(), subscriptionList, listOptions)
	if err != nil {
		if !errors.IsNotFound(err) {
			// requeue the request
			return nil, "", fmt.Errorf("failed to get subscription list with namespace %s", namespace)
		}

		return nil, "", moreerrors.Wrap(err, "failed to get placement rule")
	}
	// TODO Investigate whether we need multiple subscriptions per application for DR use case.
	// TODO check for replica count and should we fail if it is more than 1?
	for _, subscription := range subscriptionList.Items {
		// The subscription phase describes the phasing of the subscriptions. Propagated means
		// this subscription is the "parent" sitting in hub. Statuses is a map where the key is
		// the cluster name and value is the aggregated status
		if subscription.Status.Phase != subv1.SubscriptionPropagated || subscription.Status.Statuses == nil {
			continue
		}

		r.extractPlacements(subscription, placementDecisions, &homeCluster)
	}

	return placementDecisions, homeCluster, nil
}

func (r *AppVolumeReplicationReconciler) extractPlacements(
	subscription subv1.Subscription, placementDecisions map[string]bool, homeCluster *string) {
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

		r.extractPlacementDecisionsAndHomeCluster(
			placementRule.Status.Decisions, subscription.Status.Statuses, placementDecisions, homeCluster)
	}
}

func (r *AppVolumeReplicationReconciler) extractPlacementDecisionsAndHomeCluster(
	decisions []plrv1.PlacementDecision, subStatuses subv1.SubscriptionClusterStatusMap,
	placementDecisions map[string]bool, homeCluster *string) {
	if len(decisions) == 0 {
		return
	}

	for _, decision := range decisions {
		placementDecisions[decision.ClusterName] = true
		// pick the first home cluster found
		if *homeCluster == "" && subStatuses[decision.ClusterName] != nil {
			*homeCluster = decision.ClusterName
		}
	}
}

// TODO: Should the peer be a set of clusters?
func (r *AppVolumeReplicationReconciler) selectPeerClusterFromAVRPlacement(
	namespace string, placement *plrv1.Placement, homeCluster string) (string, error) {
	var peerCluster string

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
			// select the peer cluster
			if homeCluster != decision.ClusterName {
				peerCluster = decision.ClusterName

				break
			}
		}
	}

	return peerCluster, nil
}

func (r *AppVolumeReplicationReconciler) createOrUpdateManifestWork(name string, namespace string,
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
			Name:      fmt.Sprintf(manifestWorkNameFormat, homeCluster),
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
