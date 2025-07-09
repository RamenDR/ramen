// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"fmt"
	"reflect"

	"github.com/go-logr/logr"
	plrv1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ocmworkv1 "open-cluster-management.io/api/work/v1"
	viewv1beta1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/view/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	rmnutil "github.com/ramendr/ramen/internal/controller/util"
	clrapiv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
)

func ManifestWorkPredicateFunc() predicate.Funcs {
	mwPredicate := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			log := ctrl.Log.WithName("Predicate").WithName("ManifestWork")

			oldMW, ok := e.ObjectOld.DeepCopyObject().(*ocmworkv1.ManifestWork)
			if !ok {
				log.Info("Failed to deep copy older ManifestWork")

				return false
			}
			newMW, ok := e.ObjectNew.DeepCopyObject().(*ocmworkv1.ManifestWork)
			if !ok {
				log.Info("Failed to deep copy newer ManifestWork")

				return false
			}

			log.Info(fmt.Sprintf("Update event for MW %s/%s", oldMW.Name, oldMW.Namespace))

			return !reflect.DeepEqual(oldMW.Status, newMW.Status)
		},
	}

	return mwPredicate
}

func filterMW(mw *ocmworkv1.ManifestWork) []ctrl.Request {
	if mw.Annotations[DRPCNameAnnotation] == "" ||
		mw.Annotations[DRPCNamespaceAnnotation] == "" {
		return []ctrl.Request{}
	}

	return []ctrl.Request{
		reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      mw.Annotations[DRPCNameAnnotation],
				Namespace: mw.Annotations[DRPCNamespaceAnnotation],
			},
		},
	}
}

func ManagedClusterViewPredicateFunc() predicate.Funcs {
	log := ctrl.Log.WithName("Predicate").WithName("MCV")
	mcvPredicate := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldMCV, ok := e.ObjectOld.DeepCopyObject().(*viewv1beta1.ManagedClusterView)
			if !ok {
				log.Info("Failed to deep copy older MCV")

				return false
			}
			newMCV, ok := e.ObjectNew.DeepCopyObject().(*viewv1beta1.ManagedClusterView)
			if !ok {
				log.Info("Failed to deep copy newer MCV")

				return false
			}

			log.Info(fmt.Sprintf("Update event for MCV %s/%s", oldMCV.Name, oldMCV.Namespace))

			return !reflect.DeepEqual(oldMCV.Status, newMCV.Status)
		},
	}

	return mcvPredicate
}

func filterMCV(mcv *viewv1beta1.ManagedClusterView) []ctrl.Request {
	if mcv.Annotations[DRPCNameAnnotation] == "" ||
		mcv.Annotations[DRPCNamespaceAnnotation] == "" {
		return []ctrl.Request{}
	}

	return []ctrl.Request{
		reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      mcv.Annotations[DRPCNameAnnotation],
				Namespace: mcv.Annotations[DRPCNamespaceAnnotation],
			},
		},
	}
}

func PlacementRulePredicateFunc() predicate.Funcs {
	log := ctrl.Log.WithName("DRPCPredicate").WithName("UserPlRule")
	usrPlRulePredicate := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			log.Info("Delete event")

			return true
		},
	}

	return usrPlRulePredicate
}

func filterUsrPlRule(usrPlRule *plrv1.PlacementRule) []ctrl.Request {
	if usrPlRule.Annotations[DRPCNameAnnotation] == "" ||
		usrPlRule.Annotations[DRPCNamespaceAnnotation] == "" {
		return []ctrl.Request{}
	}

	return []ctrl.Request{
		reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      usrPlRule.Annotations[DRPCNameAnnotation],
				Namespace: usrPlRule.Annotations[DRPCNamespaceAnnotation],
			},
		},
	}
}

func PlacementPredicateFunc() predicate.Funcs {
	log := ctrl.Log.WithName("DRPCPredicate").WithName("UserPlmnt")
	usrPlmntPredicate := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			log.Info("Delete event")

			return true
		},
	}

	return usrPlmntPredicate
}

func filterUsrPlmnt(usrPlmnt *clrapiv1beta1.Placement) []ctrl.Request {
	if usrPlmnt.Annotations[DRPCNameAnnotation] == "" ||
		usrPlmnt.Annotations[DRPCNamespaceAnnotation] == "" {
		return []ctrl.Request{}
	}

	return []ctrl.Request{
		reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      usrPlmnt.Annotations[DRPCNameAnnotation],
				Namespace: usrPlmnt.Annotations[DRPCNamespaceAnnotation],
			},
		},
	}
}

func DRClusterPredicateFunc() predicate.Funcs {
	log := ctrl.Log.WithName("DRPCPredicate").WithName("DRCluster")
	drClusterPredicate := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			log.Info("Update event")

			drcOld, ok := e.ObjectOld.(*rmn.DRCluster)
			if !ok {
				return false
			}

			drcNew, ok := e.ObjectNew.(*rmn.DRCluster)
			if !ok {
				return false
			}

			return DRClusterUpdateOfInterest(drcOld, drcNew)
		},
	}

	return drClusterPredicate
}

func DRPolicyPredicateFunc() predicate.Funcs {
	log := ctrl.Log.WithName("DRPCPredicate").WithName("DRPolicy")
	drPolicyPredicate := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			log.Info("Update event")

			drpOld, ok := e.ObjectOld.(*rmn.DRPolicy)
			if !ok {
				return false
			}

			drpNew, ok := e.ObjectNew.(*rmn.DRPolicy)
			if !ok {
				return false
			}

			return RequiresDRPCReconciliation(drpOld, drpNew)
		},
	}

	return drPolicyPredicate
}

// DRClusterUpdateOfInterest checks if the new DRCluster resource as compared to the older version
// requires any attention, it checks for the following updates:
//   - If any maintenance mode is reported as activated
//   - If drcluster was marked for deletion
//
// TODO: Needs some logs for easier troubleshooting
func DRClusterUpdateOfInterest(oldDRCluster, newDRCluster *rmn.DRCluster) bool {
	for _, mModeNew := range newDRCluster.Status.MaintenanceModes {
		// Check if new conditions have failover activated, if not this maintenance mode is NOT of interest
		conditionNew := getFailoverActivatedCondition(mModeNew)
		if conditionNew == nil ||
			conditionNew.Status == metav1.ConditionFalse ||
			conditionNew.Status == metav1.ConditionUnknown {
			continue
		}

		// Check if failover maintenance mode was already activated as part of an older update to DRCluster, if NOT
		// this change is of interest
		if activated := checkFailoverActivation(oldDRCluster, mModeNew.StorageProvisioner, mModeNew.TargetID); !activated {
			return true
		}
	}

	// Exhausted all failover activation checks, the only interesting update is deleting a drcluster.
	return rmnutil.ResourceIsDeleted(newDRCluster)
}

// RequiresDRPCReconciliation determines if the updated DRPolicy resource, compared to the previous version,
// requires reconciliation of the DRPCs. Reconciliation is needed if the DRPolicy has been newly activated, or
// peerClasses have been updated in the DRPolicy status.
// This check helps avoid delays in reconciliation by ensuring timely updates when necessary.
func RequiresDRPCReconciliation(oldDRPolicy, newDRPolicy *rmn.DRPolicy) bool {
	err1 := rmnutil.DrpolicyValidated(oldDRPolicy)
	err2 := rmnutil.DrpolicyValidated(newDRPolicy)

	return err1 != err2 ||
		!reflect.DeepEqual(oldDRPolicy.Status.Async.PeerClasses, newDRPolicy.Status.Async.PeerClasses) ||
		!reflect.DeepEqual(oldDRPolicy.Status.Sync.PeerClasses, newDRPolicy.Status.Sync.PeerClasses)
}

// checkFailoverActivation checks if provided provisioner and storage instance is activated as per the
// passed in DRCluster resource status. It currently only checks for:
//   - Failover activation condition
func checkFailoverActivation(drcluster *rmn.DRCluster, provisioner string, targetID string) bool {
	for _, mMode := range drcluster.Status.MaintenanceModes {
		if !(mMode.StorageProvisioner == provisioner && mMode.TargetID == targetID) {
			continue
		}

		condition := getFailoverActivatedCondition(mMode)
		if condition == nil ||
			condition.Status == metav1.ConditionFalse ||
			condition.Status == metav1.ConditionUnknown {
			return false
		}

		return true
	}

	return false
}

// getFailoverActivatedCondition is a helper routine that returns the FailoverActivated condition
// from a given ClusterMaintenanceMode if found, or nil otherwise
func getFailoverActivatedCondition(mMode rmn.ClusterMaintenanceMode) *metav1.Condition {
	for _, condition := range mMode.Conditions {
		if condition.Type != string(rmn.MModeConditionFailoverActivated) {
			continue
		}

		return &condition
	}

	return nil
}

// FilterDRCluster filters for DRPC resources that should be reconciled due to a DRCluster watch event
func (r *DRPlacementControlReconciler) FilterDRCluster(drcluster *rmn.DRCluster) []ctrl.Request {
	log := ctrl.Log.WithName("DRPCFilter").WithName("DRCluster").WithValues("cluster", drcluster)

	var drpcCollections []DRPCAndPolicy

	var err error

	if rmnutil.ResourceIsDeleted(drcluster) {
		drpcCollections, err = DRPCsUsingDRCluster(r.Client, log, drcluster)
	} else {
		drpcCollections, err = DRPCsFailingOverToCluster(r.Client, log, drcluster.GetName())
	}

	if err != nil {
		log.Info("Failed to process filter")

		return nil
	}

	requests := make([]reconcile.Request, 0)
	for idx := range drpcCollections {
		requests = append(requests,
			reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      drpcCollections[idx].drpc.GetName(),
					Namespace: drpcCollections[idx].drpc.GetNamespace(),
				},
			})
	}

	return requests
}

type DRPCAndPolicy struct {
	drpc     *rmn.DRPlacementControl
	drPolicy *rmn.DRPolicy
}

// DRPCsUsingDRCluster finds DRPC resources using the DRcluster.
func DRPCsUsingDRCluster(k8sclient client.Client, log logr.Logger, drcluster *rmn.DRCluster) ([]DRPCAndPolicy, error) {
	drpolicies := &rmn.DRPolicyList{}
	if err := k8sclient.List(context.TODO(), drpolicies); err != nil {
		log.Error(err, "Failed to list DRPolicies", "drcluster", drcluster.GetName())

		return nil, err
	}

	found := []DRPCAndPolicy{}

	for i := range drpolicies.Items {
		drpolicy := &drpolicies.Items[i]

		if rmnutil.DrpolicyContainsDrcluster(drpolicy, drcluster.GetName()) {
			log.Info("Found DRPolicy referencing DRCluster", "drpolicy", drpolicy.GetName())

			drpcs, err := DRPCsUsingDRPolicy(k8sclient, log, drpolicy)
			if err != nil {
				return nil, err
			}

			for _, drpc := range drpcs {
				found = append(found, DRPCAndPolicy{drpc: drpc, drPolicy: drpolicy})
			}
		}
	}

	return found, nil
}

// DRPCsUsingDRPolicy finds DRPC resources that reference the DRPolicy.
func DRPCsUsingDRPolicy(
	k8sclient client.Client,
	log logr.Logger,
	drpolicy *rmn.DRPolicy,
) ([]*rmn.DRPlacementControl, error) {
	drpcs := &rmn.DRPlacementControlList{}
	if err := k8sclient.List(context.TODO(), drpcs); err != nil {
		log.Error(err, "Failed to list DRPCs", "drpolicy", drpolicy.GetName())

		return nil, err
	}

	found := []*rmn.DRPlacementControl{}

	for i := range drpcs.Items {
		drpc := &drpcs.Items[i]

		if drpc.Spec.DRPolicyRef.Name != drpolicy.GetName() {
			continue
		}

		log.Info("Found DRPC referencing drpolicy",
			"name", drpc.GetName(),
			"namespace", drpc.GetNamespace(),
			"drpolicy", drpolicy.GetName())

		found = append(found, drpc)
	}

	return found, nil
}

// DRPCsFailingOverToCluster lists DRPC resources that are failing over to the passed in drcluster
//
//nolint:gocognit
func DRPCsFailingOverToCluster(k8sclient client.Client, log logr.Logger, drcluster string) ([]DRPCAndPolicy, error) {
	drpolicies := &rmn.DRPolicyList{}
	if err := k8sclient.List(context.TODO(), drpolicies); err != nil {
		// TODO: If we get errors, do we still get an event later and/or for all changes from where we
		// processed the last DRCluster update?
		log.Error(err, "Failed to list DRPolicies")

		return nil, err
	}

	drpcCollections := make([]DRPCAndPolicy, 0)

	for drpolicyIdx := range drpolicies.Items {
		drpolicy := &drpolicies.Items[drpolicyIdx]

		if rmnutil.DrpolicyContainsDrcluster(drpolicy, drcluster) {
			drClusters, err := GetDRClusters(context.TODO(), k8sclient, drpolicy)
			if err != nil || len(drClusters) <= 1 {
				log.Error(err, "Failed to get DRClusters")

				return nil, err
			}

			// Skip if policy is of type metro, fake the from and to cluster
			metro, _, err := dRPolicySupportsMetro(drpolicy, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to check if DRPolicy supports Metro: %w", err)
			}

			if metro {
				log.Info("Sync DRPolicy detected, skipping!")

				break
			}

			log.Info("Processing DRPolicy referencing DRCluster", "drpolicy", drpolicy.GetName())

			drpcs, err := DRPCsFailingOverToClusterForPolicy(k8sclient, log, drpolicy, drcluster)
			if err != nil {
				return nil, err
			}

			for idx := range drpcs {
				dprcCollection := DRPCAndPolicy{
					drpc:     drpcs[idx],
					drPolicy: drpolicy,
				}

				drpcCollections = append(drpcCollections, dprcCollection)
			}
		}
	}

	return drpcCollections, nil
}

// DRPCsFailingOverToClusterForPolicy filters DRPC resources that reference the DRPolicy and are failing over
// to the target cluster passed in
//
//nolint:gocognit
func DRPCsFailingOverToClusterForPolicy(
	k8sclient client.Client,
	log logr.Logger,
	drpolicy *rmn.DRPolicy,
	drcluster string,
) ([]*rmn.DRPlacementControl, error) {
	drpcs := &rmn.DRPlacementControlList{}
	if err := k8sclient.List(context.TODO(), drpcs); err != nil {
		log.Error(err, "Failed to list DRPCs", "drpolicy", drpolicy.GetName())

		return nil, err
	}

	filteredDRPCs := make([]*rmn.DRPlacementControl, 0)

	for idx := range drpcs.Items {
		drpc := &drpcs.Items[idx]

		if drpc.Spec.DRPolicyRef.Name != drpolicy.GetName() {
			continue
		}

		if rmnutil.ResourceIsDeleted(drpc) {
			continue
		}

		if !(drpc.Spec.Action == rmn.ActionFailover && drpc.Spec.FailoverCluster == drcluster) {
			continue
		}

		if condition := meta.FindStatusCondition(drpc.Status.Conditions, rmn.ConditionAvailable); condition != nil &&
			condition.Status == metav1.ConditionTrue &&
			condition.ObservedGeneration == drpc.Generation {
			continue
		}

		log.Info("DRPC detected as failing over to cluster",
			"name", drpc.GetName(),
			"namespace", drpc.GetNamespace(),
			"drpolicy", drpolicy.GetName())

		filteredDRPCs = append(filteredDRPCs, drpc)
	}

	return filteredDRPCs, nil
}

// FilterDRPCsForDRPolicyUpdate filters and returns the DRPC resources that need reconciliation
// in response to a DRPolicy update event. This ensures that only relevant DRPCs are processed
// based on the changes in the associated DRPolicy.
func (r *DRPlacementControlReconciler) FilterDRPCsForDRPolicyUpdate(drpolicy *rmn.DRPolicy) []ctrl.Request {
	log := ctrl.Log.WithName("DRPCFilter").WithName("DRPolicy").WithValues("policy", drpolicy)

	drpcs := &rmn.DRPlacementControlList{}

	err := r.List(context.TODO(), drpcs)
	if err != nil {
		log.Info("Failed to process DRPolicy filter")

		return []ctrl.Request{}
	}

	requests := make([]reconcile.Request, 0)

	for _, drpc := range drpcs.Items {
		if drpc.Spec.DRPolicyRef.Name == drpolicy.GetName() {
			requests = append(requests,
				reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      drpc.GetName(),
						Namespace: drpc.GetNamespace(),
					},
				})
		}
	}

	return requests
}

//nolint:funlen
func (r *DRPlacementControlReconciler) setupWithManagerAndAddWatchers(mgr ctrl.Manager) error {
	mwPred := ManifestWorkPredicateFunc()

	mwMapFun := handler.EnqueueRequestsFromMapFunc(handler.MapFunc(
		func(ctx context.Context, obj client.Object) []reconcile.Request {
			mw, ok := obj.(*ocmworkv1.ManifestWork)
			if !ok {
				return []reconcile.Request{}
			}

			ctrl.Log.Info(fmt.Sprintf("DRPC: Filtering ManifestWork (%s/%s)", mw.Name, mw.Namespace))

			return filterMW(mw)
		}))

	mcvPred := ManagedClusterViewPredicateFunc()

	mcvMapFun := handler.EnqueueRequestsFromMapFunc(handler.MapFunc(
		func(ctx context.Context, obj client.Object) []reconcile.Request {
			mcv, ok := obj.(*viewv1beta1.ManagedClusterView)
			if !ok {
				return []reconcile.Request{}
			}

			ctrl.Log.Info(fmt.Sprintf("DRPC: Filtering MCV (%s/%s)", mcv.Name, mcv.Namespace))

			return filterMCV(mcv)
		}))

	usrPlRulePred := PlacementRulePredicateFunc()

	usrPlRuleMapFun := handler.EnqueueRequestsFromMapFunc(handler.MapFunc(
		func(ctx context.Context, obj client.Object) []reconcile.Request {
			usrPlRule, ok := obj.(*plrv1.PlacementRule)
			if !ok {
				return []reconcile.Request{}
			}

			ctrl.Log.Info(fmt.Sprintf("DRPC: Filtering User PlacementRule (%s/%s)", usrPlRule.Name, usrPlRule.Namespace))

			return filterUsrPlRule(usrPlRule)
		}))

	usrPlmntPred := PlacementPredicateFunc()

	usrPlmntMapFun := handler.EnqueueRequestsFromMapFunc(handler.MapFunc(
		func(ctx context.Context, obj client.Object) []reconcile.Request {
			usrPlmnt, ok := obj.(*clrapiv1beta1.Placement)
			if !ok {
				return []reconcile.Request{}
			}

			ctrl.Log.Info(fmt.Sprintf("DRPC: Filtering User Placement (%s/%s)", usrPlmnt.Name, usrPlmnt.Namespace))

			return filterUsrPlmnt(usrPlmnt)
		}))

	drClusterPred := DRClusterPredicateFunc()

	drClusterMapFun := handler.EnqueueRequestsFromMapFunc(handler.MapFunc(
		func(ctx context.Context, obj client.Object) []reconcile.Request {
			drCluster, ok := obj.(*rmn.DRCluster)
			if !ok {
				return []reconcile.Request{}
			}

			ctrl.Log.Info(fmt.Sprintf("DRPC Map: Filtering DRCluster (%s)", drCluster.Name))

			return r.FilterDRCluster(drCluster)
		}))

	drPolicyPred := DRPolicyPredicateFunc()

	drPolicyMapFun := handler.EnqueueRequestsFromMapFunc(handler.MapFunc(
		func(ctx context.Context, obj client.Object) []reconcile.Request {
			drPolicy, ok := obj.(*rmn.DRPolicy)
			if !ok {
				return []reconcile.Request{}
			}

			ctrl.Log.Info(fmt.Sprintf("DRPC Map: Filtering DRPolicy (%s)", drPolicy.Name))

			return r.FilterDRPCsForDRPolicyUpdate(drPolicy)
		}))

	r.eventRecorder = rmnutil.NewEventReporter(mgr.GetEventRecorderFor("controller_DRPlacementControl"))

	options := ctrlcontroller.Options{
		MaxConcurrentReconciles: getMaxConcurrentReconciles(ctrl.Log),
	}
	if r.RateLimiter != nil {
		options.RateLimiter = *r.RateLimiter
	}

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(options).
		For(&rmn.DRPlacementControl{}).
		Watches(&ocmworkv1.ManifestWork{}, mwMapFun, builder.WithPredicates(mwPred)).
		Watches(&viewv1beta1.ManagedClusterView{}, mcvMapFun, builder.WithPredicates(mcvPred)).
		Watches(&plrv1.PlacementRule{}, usrPlRuleMapFun, builder.WithPredicates(usrPlRulePred)).
		Watches(&clrapiv1beta1.Placement{}, usrPlmntMapFun, builder.WithPredicates(usrPlmntPred)).
		Watches(&rmn.DRCluster{}, drClusterMapFun, builder.WithPredicates(drClusterPred)).
		Watches(&rmn.DRPolicy{}, drPolicyMapFun, builder.WithPredicates(drPolicyPred)).
		Complete(r)
}
