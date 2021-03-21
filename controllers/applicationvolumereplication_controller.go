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
	"strings"

	spokeClusterV1 "github.com/open-cluster-management/api/cluster/v1"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	plrv1 "github.com/open-cluster-management/multicloud-operators-placementrule/pkg/apis/apps/v1"
	"github.com/open-cluster-management/multicloud-operators-placementrule/pkg/utils"
	subv1 "github.com/open-cluster-management/multicloud-operators-subscription/pkg/apis/apps/v1"
	"github.com/prometheus/common/log"

	"github.com/go-logr/logr"
	errorswrapper "github.com/pkg/errors"
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
func (r *ApplicationVolumeReplicationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.Log.WithValues("applicationvolumereplication", req.NamespacedName)
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
			return ctrl.Result{}, fmt.Errorf("failed to get the subscription list for namespace %s", avr.Namespace)
		}

		return ctrl.Result{}, errorswrapper.Wrap(err, "failed to get placement rule")
	}

	placementDecisions, requeue := r.processSubscriptions(avr, subscriptionList)

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
		result := fmt.Sprintf("AVR decisions and subscriptions count mismatch %d:%d",
			len(avr.Status.Decisions), len(subscriptionList.Items))
		logger.Info(result)

		return ctrl.Result{Requeue: requeue}, nil
	}

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
		if r.isSubsriptionPausedForDR(subscription.GetLabels()) {
			if err := r.processPausedSubscription(avr, subscription); err != nil {
				r.Log.Error(err, fmt.Sprintf("failed to process paused Subscription %s", subscription.Name))
			}
			// Stop processing this subscription. We'll wait for the next Reconciler iteration
			continue
		}

		if subscription.Status.Phase == subv1.SubscriptionSubscribed {
			r.Log.Info("skipping local subscription", "name", subscription.Name)

			continue
		}

		// Skip this subscription if a manifestwork already exist
		if found, err := r.findVRGManifestWork(avr, subscription); err != nil && found {
			continue
		}
		// This subscription is ready for manifest (VRG) creation
		r.Log.Info("Subscription is unpaused", "Name", subscription.Name)

		placementDecision, err := r.processUnpausedSubscription(&subscriptionList.Items[idx])
		if err != nil {
			requeue = true

			continue
		}

		placementDecisions[subscription.Name] = placementDecision
	}

	r.Log.Info("Returning Placement Decisions", "Total", len(placementDecisions))

	return placementDecisions, requeue
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
	subscription subv1.Subscription) error {
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

	err = r.restorePVsToHomeCluster(subscription, homeClusterName)
	if err != nil {
		return err
	}

	return r.unpauseSubscription(subscription)
}

func (r *ApplicationVolumeReplicationReconciler) findVRGManifestWork(
	avr *ramendrv1alpha1.ApplicationVolumeReplication,
	subscription subv1.Subscription) (bool, error) {
	const notFound = false

	if d, found := avr.Status.Decisions[subscription.Name]; found {
		mw := &ocmworkv1.ManifestWork{}
		vrgMWName := fmt.Sprintf(ManifestWorkNameFormat, subscription.Name, subscription.Namespace, "vrg")

		err := r.Client.Get(context.TODO(), types.NamespacedName{Name: vrgMWName, Namespace: d.HomeCluster}, mw)
		if err != nil {
			if errors.IsNotFound(err) {
				r.Log.Error(err, "Could not fetch ManifestWork")

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
	subscription subv1.Subscription) error {
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

func (r *ApplicationVolumeReplicationReconciler) processUnpausedSubscription(
	subscription *subv1.Subscription) (*ramendrv1alpha1.SubscriptionPlacementDecision, error) {
	homeCluster, peerCluster, err := r.selectPlacementDecision(subscription)
	if err != nil {
		return nil, err
	}

	err = r.createOrUpdateManifestWorkForVRG(subscription.Name, subscription.Namespace, homeCluster)
	if err != nil {
		r.Log.Error(err, fmt.Sprintf("Failed to create VRG for Subscription %s", subscription.Name))

		return nil, err
	}

	return &ramendrv1alpha1.SubscriptionPlacementDecision{
		HomeCluster: homeCluster,
		PeerCluster: peerCluster,
	}, nil
}

func (r *ApplicationVolumeReplicationReconciler) restorePVsToHomeCluster(
	subscription subv1.Subscription, homeClusterName string) error {
	r.Log.Info("Restoring PVs to managed cluster", "name", homeClusterName)

	// TODO: get PVs from S3
	pvList, err := r.listPVsFromS3Store()
	if err != nil {
		return errorswrapper.Wrap(err, "failed to retrieve PVs from S3 store")
	}

	r.Log.Info("PV k8s objects", "len", len(pvList))
	// Create manifestwork for all PVs for this subscription
	return r.createOrUpdateManifestWorkForPVs(subscription.Name, subscription.Namespace, homeClusterName, pvList)
}

func (r *ApplicationVolumeReplicationReconciler) unpauseSubscription(subscription subv1.Subscription) error {
	labels := subscription.GetLabels()
	if labels == nil {
		r.Log.Info("no labels found for subscription", "name", subscription.Name)

		return fmt.Errorf("failed to find labels for subscription %s", subscription.Name)
	}

	log.Info("Unpausing subscription: ", subscription)

	labels[subv1.LabelSubscriptionPause] = "false"
	subscription.SetLabels(labels)

	newInstance := new(subv1.Subscription)
	subscription.DeepCopyInto(newInstance)

	return r.Client.Update(context.TODO(), newInstance)
}

func (r *ApplicationVolumeReplicationReconciler) findNextHomeCluster(
	avr *ramendrv1alpha1.ApplicationVolumeReplication,
	subscription subv1.Subscription) string {
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

// Create a ManifestWork that represents the VolumeReplicationGroup CR for the hub to
// deploy on the managed cluster. A manifest workload is defined as a set of Kubernetes
// resources. The ManifestWork must be created in the cluster namespace on the hub, so that
// agent on the corresponding managed cluster can access this resource and deploy on the
// managed cluster. We create one ManifestWork for each VRG CR.
func (r *ApplicationVolumeReplicationReconciler) createOrUpdateManifestWorkForVRG(name string, namespace string,
	homeCluster string) error {
	r.Log.Info("attempt to create or update ManifestWork for VRG", "name", name, "namespace", namespace)

	manifestWork, err := r.createManifestWorkForVRG(name, namespace, homeCluster)
	if err != nil {
		return err
	}

	return r.doCreateOrUpdateManifestWork(manifestWork, homeCluster)
}

func (r *ApplicationVolumeReplicationReconciler) createOrUpdateManifestWorkForPVs(
	name string, namespace string, homeClusterName string, pvList []string) error {
	r.Log.Info("Creating manifest work for PVs", "subscription",
		name, "cluster", homeClusterName, "PV count", len(pvList))

	manifestWork := r.createManifestWorkForPVs(name, namespace, homeClusterName, pvList)

	return r.doCreateOrUpdateManifestWork(manifestWork, homeClusterName)
}

func (r *ApplicationVolumeReplicationReconciler) doCreateOrUpdateManifestWork(
	manifestWork *ocmworkv1.ManifestWork, homeCluster string) error {
	found := &ocmworkv1.ManifestWork{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: manifestWork.Name, Namespace: homeCluster}, found)

	if err != nil {
		if !errors.IsNotFound(err) {
			r.Log.Error(err, "Could not fetch ManifestWork")

			return errorswrapper.Wrap(err, "failed to fetch manifestWork")
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

func (r *ApplicationVolumeReplicationReconciler) createManifestWorkForPVs(
	name string, namespace string, homeClusterName string, pvList []string) *ocmworkv1.ManifestWork {
	pvManifestArray := r.createPVsClientManifest(pvList)

	return r.createManifestWork(
		fmt.Sprintf(ManifestWorkNameFormat, name, namespace, "pv"),
		homeClusterName,
		map[string]string{"app": "PV"},
		pvManifestArray)
}

func (r *ApplicationVolumeReplicationReconciler) createManifestWorkForVRG(name string, namespace string,
	homeCluster string) (*ocmworkv1.ManifestWork, error) {
	vrgClientManifest, err := r.createVRGClientManifest(name, namespace)
	if err != nil {
		return nil, err
	}

	vrgManifestArray := []ocmworkv1.Manifest{*vrgClientManifest}

	return r.createManifestWork(
		fmt.Sprintf(ManifestWorkNameFormat, name, namespace, "vrg"),
		homeCluster,
		map[string]string{"app": "VRG"},
		vrgManifestArray), nil
}

func (r *ApplicationVolumeReplicationReconciler) createManifestWork(name string, homeClusterName string,
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

func (r *ApplicationVolumeReplicationReconciler) createVRGClientManifest(name string, namespace string) (*ocmworkv1.Manifest, error) {
	vrg := &ramendrv1alpha1.VolumeReplicationGroup{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "ramendr.openshift.io/v1alpha1",
			Kind:       "VolumeReplicationGroup",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
			PVCSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"appClass":    "gold",
					"environment": "dev.AZ1",
				},
			},
			VolumeReplicationClass: "volume-rep-class",
			ReplicationState: "Primary",
		},
	}

	vrgInByteArray, err := json.Marshal(vrg)
	if err != nil {
		return nil, errorswrapper.Wrap(err, "failed to marshal VRG object")
	}

	vrgClientManifest := &ocmworkv1.Manifest{}
	vrgClientManifest.RawExtension = runtime.RawExtension{Raw: vrgInByteArray}

	return vrgClientManifest, nil
}

func (r *ApplicationVolumeReplicationReconciler) createPVsClientManifest(pvList []string) []ocmworkv1.Manifest {
	pvManifestArray := []ocmworkv1.Manifest{}

	for _, pv := range pvList {
		pvClientManifest := ocmworkv1.Manifest{}
		pvClientManifest.RawExtension = runtime.RawExtension{Raw: []byte(pv)}

		pvManifestArray = append(pvManifestArray, pvClientManifest)
	}

	return pvManifestArray
}

func (r *ApplicationVolumeReplicationReconciler) updateAVRStatus(
	ctx context.Context,
	avr *ramendrv1alpha1.ApplicationVolumeReplication,
	placementDecisions ramendrv1alpha1.SubscriptionPlacementDecisionMap) error {
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
