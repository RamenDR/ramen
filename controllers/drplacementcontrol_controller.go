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
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	volrep "github.com/csi-addons/volume-replication-operator/api/v1alpha1"
	rmn "github.com/ramendr/ramen/api/v1alpha1"
)

const (
	// ManifestWorkNameFormat is a formated a string used to generate the manifest name
	// The format is name-namespace-type-mw where:
	// - name is the DRPC name
	// - namespace is the DRPC namespace
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

	// Annotations for MW and PlacementRule
	DRPCNameAnnotation      = "drplacementcontrol.ramendr.openshift.io/drpc-name"
	DRPCNamespaceAnnotation = "drplacementcontrol.ramendr.openshift.io/drpc-namespace"
)

var ErrSameHomeCluster = errorswrapper.New("new home cluster is the same as current home cluster")

type PVDownloader interface {
	DownloadPVs(ctx context.Context, r client.Reader, objStoreGetter ObjectStoreGetter,
		s3Endpoint, s3Region string, s3SecretName types.NamespacedName,
		callerTag string, s3Bucket string) ([]corev1.PersistentVolume, error)
}

// ProgressCallback of function type
type ProgressCallback func(string)

// DRPlacementControlReconciler reconciles a DRPlacementControl object
type DRPlacementControlReconciler struct {
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

func ManifestWorkPredicateFunc() predicate.Funcs {
	mwPredicate := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			log := ctrl.Log.WithName("ManifestWork")

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

			log.Info(fmt.Sprintf("Update event for MW %s/%s: oldStatus %+v, newStatus %+v",
				oldMW.Name, oldMW.Namespace, oldMW.Status, newMW.Status))

			return !reflect.DeepEqual(oldMW.Status, newMW.Status)
		},
	}

	return mwPredicate
}

func filterMW(mw *ocmworkv1.ManifestWork) []ctrl.Request {
	return []ctrl.Request{
		reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      mw.Annotations[DRPCNameAnnotation],
				Namespace: mw.Annotations[DRPCNamespaceAnnotation],
			},
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *DRPlacementControlReconciler) SetupWithManager(mgr ctrl.Manager) error {
	genChangedPred := predicate.GenerationChangedPredicate{}
	mwPred := ManifestWorkPredicateFunc()

	mwMapFun := handler.EnqueueRequestsFromMapFunc(handler.MapFunc(func(obj client.Object) []reconcile.Request {
		mw, ok := obj.(*ocmworkv1.ManifestWork)
		if !ok {
			ctrl.Log.Info("ManifestWork map function received non-MW resource")

			return []reconcile.Request{}
		}

		// ctrl.Log.Info(fmt.Sprintf("MapFunc for ManifestWork (%v)", mw))
		return filterMW(mw)
	}))

	return ctrl.NewControllerManagedBy(mgr).
		For(&rmn.DRPlacementControl{}).
		WithEventFilter(genChangedPred).
		Watches(&source.Kind{Type: &ocmworkv1.ManifestWork{}}, mwMapFun, builder.WithPredicates(mwPred)).
		Complete(r)
}

//nolint:lll
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drplacementcontrols,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drplacementcontrols/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drplacementcontrols/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps.open-cluster-management.io,resources=placementrules,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps.open-cluster-management.io,resources=placementrules/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=managedclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=work.open-cluster-management.io,resources=manifestworks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the DRPlacementControl object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.7.0/pkg/reconcile
func (r *DRPlacementControlReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.Log.WithValues("DRPC", req.NamespacedName)

	logger.Info("Entering reconcile loop")
	defer logger.Info("Exiting reconcile loop")

	drpc := &rmn.DRPlacementControl{}

	err := r.Client.Get(ctx, req.NamespacedName, drpc)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, errorswrapper.Wrap(err, "failed to get DRPC object")
	}

	drpcPlRule, userPlRule, err := r.getPlacementRules(ctx, drpc)
	if err != nil {
		logger.Error(err, "failed to get PlacementRules")

		return ctrl.Result{}, err
	}

	// Make sure that we give time to the cloned PlacementRule to run and produces decisions
	if len(drpcPlRule.Status.Decisions) == 0 {
		const initialWaitTime = 5

		return ctrl.Result{RequeueAfter: time.Second * initialWaitTime}, nil
	}

	d := DRPCInstance{
		reconciler: r, ctx: ctx, log: logger, instance: drpc, needStatusUpdate: false,
		userPlacementRule: userPlRule, drpcPlacementRule: drpcPlRule,
	}

	requeue := d.startProcessing()
	if !requeue {
		r.Callback(d.instance.Name)
	}

	logger.Info("Finished processing", "Requeue?", requeue)

	return ctrl.Result{Requeue: requeue}, nil
}

func (r *DRPlacementControlReconciler) getPlacementRules(ctx context.Context,
	drpc *rmn.DRPlacementControl) (*plrv1.PlacementRule, *plrv1.PlacementRule, error) {
	userPlRule, err := r.getUserPlacementRule(ctx, drpc)
	if err != nil {
		return nil, nil, err
	}

	if err = r.annotatePlacementRule(ctx, drpc, userPlRule); err != nil {
		return nil, nil, err
	}

	drpcPlRule, err := r.getOrClonePlacementRule(ctx, drpc, userPlRule)
	if err != nil {
		return nil, nil, err
	}

	return drpcPlRule, userPlRule, nil
}

func (r *DRPlacementControlReconciler) getUserPlacementRule(ctx context.Context,
	drpc *rmn.DRPlacementControl) (*plrv1.PlacementRule, error) {
	r.Log.Info("Getting User PlacementRule", "placement", drpc.Spec.PlacementRef)

	if drpc.Spec.PlacementRef.Namespace == "" {
		drpc.Spec.PlacementRef.Namespace = drpc.Namespace
	}

	userPlacementRule := &plrv1.PlacementRule{}

	err := r.Client.Get(ctx,
		types.NamespacedName{Name: drpc.Spec.PlacementRef.Name, Namespace: drpc.Spec.PlacementRef.Namespace},
		userPlacementRule)
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

func (r *DRPlacementControlReconciler) annotatePlacementRule(ctx context.Context,
	drpc *rmn.DRPlacementControl, plRule *plrv1.PlacementRule) error {
	if plRule.ObjectMeta.Annotations == nil {
		plRule.ObjectMeta.Annotations = map[string]string{}
	}

	ownerName := plRule.ObjectMeta.Annotations[DRPCNameAnnotation]
	ownerNamespace := plRule.ObjectMeta.Annotations[DRPCNamespaceAnnotation]

	if ownerName == "" {
		plRule.ObjectMeta.Annotations[DRPCNameAnnotation] = drpc.Name
		plRule.ObjectMeta.Annotations[DRPCNamespaceAnnotation] = drpc.Namespace

		err := r.Update(ctx, plRule)
		if err != nil {
			r.Log.Error(err, "Failed to update PlacementRule annotation", "PlRuleName", plRule.Name)

			return fmt.Errorf("failed to update PlacementRule %s annotation '%s/%s' (%w)",
				plRule.Name, DRPCNameAnnotation, drpc.Name, err)
		}

		return nil
	}

	if ownerName != drpc.Name || ownerNamespace != drpc.Namespace {
		r.Log.Info("PlacementRule not owned by this DRPC", "PlRuleName", plRule.Name)

		return fmt.Errorf("PlacementRule %s not owned by this DRPC '%s/%s'",
			plRule.Name, drpc.Name, drpc.Namespace)
	}

	return nil
}

func (r *DRPlacementControlReconciler) getOrClonePlacementRule(ctx context.Context,
	drpc *rmn.DRPlacementControl, userPlRule *plrv1.PlacementRule) (*plrv1.PlacementRule, error) {
	r.Log.Info("Getting PlacementRule or cloning it", "placement", drpc.Spec.PlacementRef)

	clonedPlRule := &plrv1.PlacementRule{}
	clonedPlRuleName := fmt.Sprintf("%s-%s", userPlRule.Name, drpc.Name)

	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      clonedPlRuleName,
		Namespace: userPlRule.Namespace,
	}, clonedPlRule)
	if err != nil {
		if errors.IsNotFound(err) {
			clonedPlRule, err = r.clonePlacementRule(ctx, drpc, userPlRule, clonedPlRuleName)
			if err != nil {
				return nil, fmt.Errorf("failed to create cloned placementrule error: %w", err)
			}
		} else {
			r.Log.Error(err, "Failed to get drpc placementRule", "name", clonedPlRuleName)

			return nil, fmt.Errorf("failed to get placementrule error: %w", err)
		}
	}

	return clonedPlRule, nil
}

func (r *DRPlacementControlReconciler) clonePlacementRule(ctx context.Context,
	drpc *rmn.DRPlacementControl, userPlRule *plrv1.PlacementRule,
	clonedPlRuleName string) (*plrv1.PlacementRule, error) {
	r.Log.Info("Creating a clone placementRule from", "name", userPlRule.Name)

	clonedPlRule := &plrv1.PlacementRule{}

	userPlRule.DeepCopyInto(clonedPlRule)

	clonedPlRule.Name = clonedPlRuleName
	clonedPlRule.ResourceVersion = ""
	clonedPlRule.Spec.SchedulerName = ""

	err := r.addClusterPeersToPlacementRule(ctx, drpc, clonedPlRule)
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

func (r *DRPlacementControlReconciler) addClusterPeersToPlacementRule(ctx context.Context,
	drpc *rmn.DRPlacementControl, plRule *plrv1.PlacementRule) error {
	clPeersRef := drpc.Spec.DRPolicyRef
	clusterPeers := &rmn.DRPolicy{}

	err := r.Client.Get(ctx,
		types.NamespacedName{Name: clPeersRef.Name}, clusterPeers)
	if err != nil {
		return fmt.Errorf("failed to get cluster peers using %s/%s. Error (%w)",
			clPeersRef.Name, clPeersRef.Namespace, err)
	}

	if len(clusterPeers.Spec.ClusterNames) == 0 {
		return fmt.Errorf("invalid DRPolicy configuration. Name %s", clusterPeers.Name)
	}

	for idx := range clusterPeers.Spec.ClusterNames {
		plRule.Spec.Clusters = append(plRule.Spec.Clusters, plrv1.GenericClusterReference{
			Name: clusterPeers.Spec.ClusterNames[idx],
		})
	}

	r.Log.Info(fmt.Sprintf("Added %v clusters to ClusterPeers %s", plRule.Spec.Clusters, clusterPeers.Name))

	return nil
}

type DRPCInstance struct {
	reconciler        *DRPlacementControlReconciler
	ctx               context.Context
	log               logr.Logger
	instance          *rmn.DRPlacementControl
	needStatusUpdate  bool
	userPlacementRule *plrv1.PlacementRule
	drpcPlacementRule *plrv1.PlacementRule
}

func (d *DRPCInstance) startProcessing() bool {
	d.log.Info("Starting to process placement")

	requeue := true

	done, err := d.processPlacement()
	if err != nil {
		d.log.Info("Failed to process placement", "error", err)

		return requeue
	}

	if d.needStatusUpdate {
		if err := d.updateDRPCStatus(); err != nil {
			d.log.Error(err, "failed to update status")

			return requeue
		}
	}

	requeue = !done
	d.log.Info("Completed processing placement", "requeue", requeue)

	return requeue
}

func (d *DRPCInstance) processPlacement() (bool, error) {
	d.log.Info("Process DRPC Placement", "name", d.drpcPlacementRule)

	switch d.instance.Spec.Action {
	case rmn.ActionFailover:
		return d.runFailover()
	case rmn.ActionFailback:
		return d.runFailback()
	case rmn.ActionRelocate:
		return d.runRelocate()
	}

	// Not a failover, a failback, or a relocation.  Must be an initial deployment.
	return d.runInitialDeployment()
}

func (d *DRPCInstance) runInitialDeployment() (bool, error) {
	d.log.Info("Running initial deployment")
	d.resetDRState()

	const done = true

	// 1. Check if the user wants to use the preferredCluster
	homeCluster := ""
	homeClusterNamespace := ""

	if d.instance.Spec.PreferredCluster != "" {
		homeCluster = d.instance.Spec.PreferredCluster
		homeClusterNamespace = homeCluster
	}

	if homeCluster == "" && len(d.drpcPlacementRule.Status.Decisions) != 0 {
		homeCluster = d.drpcPlacementRule.Status.Decisions[0].ClusterName
		homeClusterNamespace = d.drpcPlacementRule.Status.Decisions[0].ClusterNamespace
	}

	if homeCluster == "" {
		return !done, fmt.Errorf("PreferredCluster not set and unable to find home cluster in DRPCPlacementRule (%s)",
			d.drpcPlacementRule.Name)
	}

	// We have a home cluster
	err := d.updateUserPlRuleAndCreateVRGMW(homeCluster, homeClusterNamespace)
	if err != nil {
		return !done, err
	}

	// All good, update the preferred decision and state
	if len(d.userPlacementRule.Status.Decisions) > 0 {
		d.instance.Status.PreferredDecision = d.userPlacementRule.Status.Decisions[0]
	}

	d.advanceToNextDRState()

	d.log.Info(fmt.Sprintf("DRPC (%+v)", d.instance))

	return done, nil
}

//
// runFailover:
// 1. If failoverCluster empty, then fail it and we are done
// 2. If already failed over, then ensure clean up and we are done
// 3. Set VRG for the preferredCluster to secondary
// 4. Restore PV to failoverCluster
// 5. Update UserPlacementRule decision to failoverCluster
// 6. Create VRG for the failoverCluster as Primary
// 7. Update DRPC status
// 8. Delete VRG MW from preferredCluster once the VRG state has changed to Secondary
//
func (d *DRPCInstance) runFailover() (bool, error) {
	d.log.Info("Entering runFailover", "state", d.instance.Status.LastKnownDRState)

	const done = true

	// We are done if empty
	if d.instance.Spec.FailoverCluster == "" {
		return done, fmt.Errorf("failover cluster not set. FailoverCluster is a mandatory field")
	}

	newHomeCluster := d.instance.Spec.FailoverCluster

	// We are done if we have already failed over
	if len(d.userPlacementRule.Status.Decisions) > 0 {
		if d.instance.Spec.FailoverCluster == d.userPlacementRule.Status.Decisions[0].ClusterName {
			d.log.Info("Already failed over", "state", d.instance.Status.LastKnownDRState)

			err := d.ensureCleanup()
			if err != nil {
				return !done, err
			}

			return done, nil
		}
	}

	// Make sure we record the state that we are failing over
	d.setDRState(rmn.FailingOver)

	// Save the current home cluster
	curHomeCluster := ""
	if len(d.userPlacementRule.Status.Decisions) > 0 {
		curHomeCluster = d.userPlacementRule.Status.Decisions[0].ClusterName
	}

	if curHomeCluster == "" {
		curHomeCluster = d.instance.Status.PreferredDecision.ClusterName
	}

	// Set VRG in the failed cluster (preferred cluster) to secondary
	err := d.updateVRGStateToSecondary(curHomeCluster)
	if err != nil {
		d.log.Error(err, "Failed to update existing VRG manifestwork to secondary")

		return !done, err
	}

	result, err := d.runPlacementTask(newHomeCluster, "")
	if err != nil {
		return !done, err
	}

	d.advanceToNextDRState()
	d.log.Info("Exiting runFailover", "state", d.instance.Status.LastKnownDRState)

	return result, nil
}

//
// runFailback:
// 1. If preferredCluster not set, get it from DRPC status
// 2. If still empty, fail it
// 3. If the preferredCluster is the failoverCluster, fail it
// 4. If preferredCluster is the same as the userPlacementRule decision, do nothing
// 5. Clear the user PlacementRule decision
// 6. Update VRG.Spec.ReplicationState to secondary for all DR clusters
// 7. Ensure that the VRG status reflects the previous step.  If not, then wait.
// 8. Restore PV to preferredCluster
// 9. Update UserPlacementRule decision to preferredCluster
// 10. Create VRG for the preferredCluster as Primary
// 11. Update DRPC status
// 12. Delete VRG MW from failoverCluster once the VRG state has changed to Secondary
//
func (d *DRPCInstance) runFailback() (bool, error) {
	d.log.Info("Entering runFailback", "state", d.instance.Status.LastKnownDRState)

	const done = true

	// 1. We are done if empty
	preferredCluster, preferredClusterNamespace := d.getPreferredClusterNamespaced()

	if preferredCluster == "" {
		return !done, fmt.Errorf("failback cluster not valid")
	}

	if preferredCluster == d.instance.Spec.FailoverCluster {
		return done, fmt.Errorf("failoverCluster and preferredCluster can't be the same %s/%s",
			d.instance.Spec.FailoverCluster, preferredCluster)
	}

	// We are done if already failed back
	if d.isAlreadyFailedBack(preferredCluster) {
		err := d.ensureCleanup()
		if err != nil {
			return !done, err
		}

		return done, nil
	}

	// Make sure we record the state that we are failing over
	d.setDRState(rmn.FailingBack)

	// During failback, both clusters should be up and both clusters should be secondaries.
	ensured, err := d.processVRGForSecondaries()
	if err != nil || !ensured {
		return !done, err
	}

	result, err := d.runPlacementTask(preferredCluster, preferredClusterNamespace)
	if err != nil {
		return !done, err
	}

	// 8. All good so far, update DRPC decision and state
	if len(d.userPlacementRule.Status.Decisions) > 0 {
		d.instance.Status.PreferredDecision = d.userPlacementRule.Status.Decisions[0]
	}

	d.advanceToNextDRState()
	d.log.Info("Exiting runFailback", "state", d.instance.Status.LastKnownDRState)

	return result, nil
}

func (d *DRPCInstance) runRelocate() (bool, error) {
	d.log.Info("Processing prerequisites for a relocation", "last State", d.instance.Status.LastKnownDRState)

	// TODO: implement relocation
	return true, nil
}

// runPlacementTast is a series of steps to creating, updating, and cleaning up
// the necessary objects for the failover, failback, or relocation
func (d *DRPCInstance) runPlacementTask(targetCluster, targetClusterNamespace string) (bool, error) {
	// Restore PV to preferredCluster
	err := d.createPVManifestWorkForRestore(targetCluster)
	if err != nil {
		return false, err
	}

	err = d.updateUserPlRuleAndCreateVRGMW(targetCluster, targetClusterNamespace)
	if err != nil {
		return false, err
	}

	// 9. Attempt to delete VRG MW from failed clusters
	// This is attempt to clean up is not guaranteed to complete at this stage. Deleting the old VRG
	// requires guaranteeing that the VRG has transitioned to secondary.
	clusterToSkip := targetCluster

	return d.cleanup(clusterToSkip)
}

func (d *DRPCInstance) isAlreadyFailedBack(targetCluster string) bool {
	if len(d.userPlacementRule.Status.Decisions) > 0 &&
		targetCluster == d.userPlacementRule.Status.Decisions[0].ClusterName {
		d.log.Info("Already failed back", "state", d.instance.Status.LastKnownDRState)

		return true
	}

	return false
}

func (d *DRPCInstance) getPreferredClusterNamespaced() (string, string) {
	preferredCluster := d.instance.Spec.PreferredCluster
	preferredClusterNamespace := preferredCluster

	if preferredCluster == "" {
		if d.instance.Status.PreferredDecision != (plrv1.PlacementDecision{}) {
			preferredCluster = d.instance.Status.PreferredDecision.ClusterName
			preferredClusterNamespace = d.instance.Status.PreferredDecision.ClusterNamespace
		}
	}

	return preferredCluster, preferredClusterNamespace
}

func (d *DRPCInstance) processVRGForSecondaries() (bool, error) {
	const ensured = true

	if len(d.userPlacementRule.Status.Decisions) != 0 {
		// clear current user PlacementRule's decision
		err := d.updateUserPlacementRule("", "")
		if err != nil {
			return !ensured, err
		}
	}

	failedCount := 0

	for _, cluster := range d.drpcPlacementRule.Spec.Clusters {
		err := d.updateVRGStateToSecondary(cluster.Name)
		if err != nil {
			d.log.Error(err, "Failed to update VRG to secondary", "cluster", cluster.Name)

			failedCount++
		}
	}

	if failedCount != 0 {
		d.log.Info("Failed to update VRG to secondary", "FailedCount", failedCount)

		return !ensured, nil
	}

	for _, cluster := range d.drpcPlacementRule.Spec.Clusters {
		if !d.ensureVRGIsSecondary(cluster.Name) {
			d.log.Info("Still waiting for VRG to transition to secondary", "cluster", cluster.Name)

			return !ensured, nil
		}
	}

	return ensured, nil
}

// outputs a string for use in creating a ManagedClusterView name
// example: when looking for a vrg with name 'demo' in the namespace 'ramen', input: ("demo", "ramen", "vrg")
// this will give output "demo-ramen-vrg-mcv"
func BuildManagedClusterViewName(resourceName, resourceNamespace, resource string) string {
	return fmt.Sprintf("%s-%s-%s-mcv", resourceName, resourceNamespace, resource)
}

func (r *DRPlacementControlReconciler) getVRGFromManagedCluster(
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

func (d *DRPCInstance) updateUserPlRuleAndCreateVRGMW(homeCluster, homeClusterNamespace string) error {
	err := d.updateUserPlacementRule(homeCluster, homeClusterNamespace)
	if err != nil {
		return err
	}

	// Creating VRG ManifestWork will be running in parallel with ACM deploying app resources
	err = d.createVRGManifestWork(homeCluster)
	if err != nil {
		return err
	}

	return nil
}

func (d *DRPCInstance) createPVManifestWorkForRestore(newPrimary string) error {
	pvMWName := BuildManifestWorkName(d.instance.Name, d.instance.Namespace, MWTypePV)

	existAndApplied, err := d.isManifestExistAndApplied(pvMWName, newPrimary)
	if err != nil && !errors.IsNotFound(err) {
		d.log.Error(err, "actuall here")

		return err
	}

	if existAndApplied {
		d.log.Info("MW for PVs exists and applied", "newPrimary", newPrimary)

		return nil
	}

	d.log.Info("Restore PVs", "newPrimary", newPrimary)

	return d.restore(newPrimary)
}

func (d *DRPCInstance) isManifestExistAndApplied(mwName, newPrimary string) (bool, error) {
	// Try to find whether we have already created a ManifestWork for this
	mw, err := d.findManifestWork(mwName, newPrimary)
	if err != nil {
		if !errors.IsNotFound(err) {
			d.log.Error(err, "Failed to find 'PV restore' ManifestWork")

			return false, nil
		}

		return false, err
	}

	if mw != nil {
		d.log.Info(fmt.Sprintf("Found an existing manifest work (%v)", mw))
		// Check if the MW is in Applied state
		if IsManifestInAppliedState(mw) {
			d.log.Info(fmt.Sprintf("ManifestWork %s/%s in Applied state", mw.Namespace, mw.Name))

			return true, nil
		}

		d.log.Info("MW is not in applied state", "namespace/name", mw.Namespace+"/"+mw.Name)
	}

	return false, nil
}

func (d *DRPCInstance) restore(newHomeCluster string) error {
	// Restore from PV backup location
	err := d.restorePVFromBackup(newHomeCluster)
	if err != nil {
		d.log.Error(err, "Failed to restore PVs from backup")

		return err
	}

	return nil
}

func (d *DRPCInstance) cleanup(skipCluster string) (bool, error) {
	for _, cluster := range d.drpcPlacementRule.Spec.Clusters {
		if skipCluster == cluster.Name {
			continue
		}

		mwDeleted, err := d.ensureOldVRGMWOnClusterDeleted(cluster.Name)
		if err != nil {
			d.log.Error(err, "failed to ensure that the VRG MW is deleted")

			return false, err
		}

		if !mwDeleted {
			return false, nil
		}
	}

	return true, nil
}

func (d *DRPCInstance) updateUserPlacementRule(homeCluster, homeClusterNamespace string) error {
	d.log.Info("Updating userPlacementRule", "name", d.userPlacementRule.Name)

	if homeClusterNamespace == "" {
		homeClusterNamespace = homeCluster
	}

	newPD := []plrv1.PlacementDecision{
		{
			ClusterName:      homeCluster,
			ClusterNamespace: homeClusterNamespace,
		},
	}

	if !reflect.DeepEqual(newPD, d.userPlacementRule.Status.Decisions) {
		d.userPlacementRule.Status.Decisions = newPD
		if err := d.reconciler.Status().Update(d.ctx, d.userPlacementRule); err != nil {
			d.log.Error(err, "failed to update user PlacementRule")

			return fmt.Errorf("failed to update userPlRule %s (%w)", d.userPlacementRule.Name, err)
		}

		d.log.Info("Updated user PlacementRule status", "Decisions", d.userPlacementRule.Status.Decisions)
	}

	return nil
}

func (d *DRPCInstance) createVRGManifestWork(homeCluster string) error {
	d.log.Info("Processing deployment",
		"Last State", d.instance.Status.LastKnownDRState, "cluster", homeCluster)

	mw, err := d.vrgManifestWorkExists(homeCluster)
	if err != nil {
		return err
	}

	if mw != nil {
		primary, err := d.isVRGPrimary(mw)
		if err != nil {
			d.log.Error(err, "Failed to check whether the VRG is primary or not", "mw", mw.Name)

			return err
		}

		if primary {
			// Found MW and the VRG already a primary
			return nil
		}
	}

	// VRG ManifestWork does not exist, create it.
	return d.processVRGManifestWork(homeCluster)
}

func (d *DRPCInstance) vrgManifestWorkExists(homeCluster string) (*ocmworkv1.ManifestWork, error) {
	if d.instance.Status.PreferredDecision == (plrv1.PlacementDecision{}) {
		return nil, nil
	}

	mwName := BuildManifestWorkName(d.instance.Name, d.instance.Namespace, MWTypeVRG)

	mw, err := d.findManifestWork(mwName, homeCluster)
	if err != nil {
		if !errors.IsNotFound(err) {
			d.log.Error(err, "failed to find ManifestWork")

			return nil, err
		}
	}

	if mw == nil {
		d.log.Info(fmt.Sprintf("Manifestwork %s does not exist for cluster %s", mwName, homeCluster))

		return nil, nil
	}

	d.log.Info(fmt.Sprintf("Manifestwork %s exists (%v)", mw.Name, mw))

	return mw, nil
}

func (d *DRPCInstance) findManifestWork(mwName, homeCluster string) (*ocmworkv1.ManifestWork, error) {
	if homeCluster != "" {
		mw := &ocmworkv1.ManifestWork{}

		err := d.reconciler.Get(d.ctx, types.NamespacedName{Name: mwName, Namespace: homeCluster}, mw)
		if err != nil {
			if errors.IsNotFound(err) {
				return nil, fmt.Errorf("MW not found (%w)", err)
			}

			return nil, fmt.Errorf("failed to retrieve manifestwork (%w)", err)
		}

		return mw, nil
	}

	return nil, nil
}

func (d *DRPCInstance) isVRGPrimary(mw *ocmworkv1.ManifestWork) (bool, error) {
	vrgClientManifest := &mw.Spec.Workload.Manifests[0]
	vrg := &rmn.VolumeReplicationGroup{}

	err := yaml.Unmarshal(vrgClientManifest.RawExtension.Raw, &vrg)
	if err != nil {
		return false, errorswrapper.Wrap(err, fmt.Sprintf("unable to get vrg from ManifestWork %s", mw.Name))
	}

	return (vrg.Spec.ReplicationState == volrep.Primary), nil
}

func (d *DRPCInstance) ensureCleanup() error {
	d.log.Info("ensure cleanup on secondaries")

	if len(d.userPlacementRule.Status.Decisions) == 0 {
		return nil
	}

	clusterToSkip := d.userPlacementRule.Status.Decisions[0].ClusterName

	_, err := d.cleanup(clusterToSkip)
	if err != nil {
		return err
	}

	return nil
}

// cleanupPVForRestore cleans up required PV fields, to ensure restore succeeds to a new cluster, and
// rebinding the PV to a newly created PVC with the same claimRef succeeds
func (d *DRPCInstance) cleanupPVForRestore(pv *corev1.PersistentVolume) {
	pv.ResourceVersion = ""
	if pv.Spec.ClaimRef != nil {
		pv.Spec.ClaimRef.UID = ""
		pv.Spec.ClaimRef.ResourceVersion = ""
		pv.Spec.ClaimRef.APIVersion = ""
	}
}

func (d *DRPCInstance) ensureOldVRGMWOnClusterDeleted(clusterName string) (bool, error) {
	d.log.Info("Ensuring that previous cluster VRG MW is deleted for ", "cluster", clusterName)

	const done = true

	mwName := BuildManifestWorkName(d.instance.Name, d.instance.Namespace, MWTypeVRG)
	mw := &ocmworkv1.ManifestWork{}

	err := d.reconciler.Get(d.ctx, types.NamespacedName{Name: mwName, Namespace: clusterName}, mw)
	if err != nil {
		if errors.IsNotFound(err) {
			// the MW is gone.  Check the VRG if it is deleted
			if d.ensureVRGDeleted(clusterName) {
				return done, nil
			}

			return !done, nil
		}

		return !done, fmt.Errorf("failed to retrieve manifestwork %s from %s (%w)", mwName, clusterName, err)
	}

	d.log.Info("VRG ManifestWork exists", "name", mw.Name, "clusterName", clusterName)
	// The VRG's MW exists. That most likely means that the VRG Object in the managed cluster exists as well. That also
	// means that we either didn't attempt to set the VRG to secondary, or the MangedCluster may not be reachable.
	// In either cases, we have to make sure that the VRG for the MW was set to secondary (if it is not, then set it) OR
	// the MW has not been applied yet. In that later case, we will just need return DONE and we will wait for the
	// MW status state to change to 'Applied' in order to proceed with deleting the MW, which causes the VRG to be deleted.
	updated, err := d.hasVRGStateBeenUpdatedToSecondary(clusterName)
	if err != nil {
		return !done, fmt.Errorf("failed to check whether VRG replication state has been updated to secondary (%w)", err)
	}

	if !updated {
		err = d.updateVRGStateToSecondary(clusterName)
		// in either case, we need to wait for the MW to go to applied state
		return !done, err
	}

	if !IsManifestInAppliedState(mw) {
		d.log.Info(fmt.Sprintf("ManifestWork %s/%s NOT in Applied state", mw.Namespace, mw.Name))
		// Wait for MW to be applied. The DRPC reconciliation will be called then
		return done, nil
	}

	d.log.Info("VRG ManifestWork is in Applied state", "name", mw.Name, "cluster", clusterName)

	if d.ensureVRGIsSecondary(clusterName) {
		err := d.deleteManifestWorks(clusterName)
		if err != nil {
			return !done, err
		}
	}

	// IF we get here, either the VRG has not transitioned to secondary (yet) or delete didn't succeed. In either cases,
	// we need to make sure that the VRG object is deleted. IOW, we still have to wait
	return !done, nil
}

func (d *DRPCInstance) ensureVRGIsSecondary(clusterName string) bool {
	d.log.Info(fmt.Sprintf("Ensure VRG %s is secondary on cluster %s", d.instance.Name, clusterName))

	vrg, err := d.reconciler.getVRGFromManagedCluster(d.instance.Name, d.instance.Namespace, clusterName)
	if err != nil {
		// VRG must have been deleted if the error is Not Found
		return errors.IsNotFound(err)
	}

	if vrg.Status == nil || vrg.Status.ReplicationState != volrep.Secondary {
		d.log.Info(fmt.Sprintf("vrg status replication state for cluster %s is %v",
			clusterName, vrg.Status.ReplicationState))

		return false
	}

	return true
}

func (d *DRPCInstance) ensureVRGDeleted(clusterName string) bool {
	_, err := d.reconciler.getVRGFromManagedCluster(d.instance.Name, d.instance.Namespace, clusterName)
	// We only accept a NOT FOUND error status
	return errors.IsNotFound(err)
}

func (d *DRPCInstance) deleteManifestWorks(clusterName string) error {
	err := d.deleteVRGManifestWork(clusterName)
	if err != nil {
		d.log.Error(err, "failed to delete MW for VRG")

		return err
	}

	err = d.deletePVManifestWork(clusterName)
	if err != nil {
		d.log.Error(err, "failed to delete MW for PVs")

		return err
	}

	//
	// NOTE: WE ARE NOT DELETING ROLES MW.  I SEE NO REASON TO DELETE THEM.
	// TO KEEP IT CLEAN, SHOULD WE???
	//
	return nil
}

func (d *DRPCInstance) deletePVManifestWork(fromCluster string) error {
	pvMWName := BuildManifestWorkName(d.instance.Name, d.instance.Namespace, MWTypePV)
	pvMWNamespace := fromCluster

	return d.deleteManifestWork(pvMWName, pvMWNamespace)
}

func (d *DRPCInstance) deleteVRGManifestWork(fromCluster string) error {
	vrgMWName := BuildManifestWorkName(d.instance.Name, d.instance.Namespace, MWTypeVRG)
	vrgMWNamespace := fromCluster

	return d.deleteManifestWork(vrgMWName, vrgMWNamespace)
}

func (d *DRPCInstance) deleteManifestWork(mwName, mwNamespace string) error {
	d.log.Info("Delete ManifestWork from", "namespace", mwNamespace, "name", mwName)

	if d.instance.Status.PreferredDecision == (plrv1.PlacementDecision{}) {
		return nil
	}

	mw := &ocmworkv1.ManifestWork{}

	err := d.reconciler.Get(d.ctx, types.NamespacedName{Name: mwName, Namespace: mwNamespace}, mw)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}

		return fmt.Errorf("failed to retrieve manifestwork for type: %s. Error: %w", mwName, err)
	}

	d.log.Info("Deleting ManifestWork", "name", mw.Name, "namespace", mwNamespace)

	return d.reconciler.Delete(d.ctx, mw)
}

func (d *DRPCInstance) restorePVFromBackup(homeCluster string) error {
	d.log.Info("Restoring PVs to new managed cluster", "name", homeCluster)

	pvList, err := d.listPVsFromS3Store()
	if err != nil {
		return errorswrapper.Wrap(err, "failed to retrieve PVs from S3 store")
	}

	d.log.Info(fmt.Sprintf("Found %d PVs", len(pvList)))

	if len(pvList) == 0 {
		return nil
	}

	for idx := range pvList {
		d.cleanupPVForRestore(&pvList[idx])
	}

	// Create manifestwork for all PVs for this DRPC
	return d.createOrUpdatePVsManifestWork(d.instance.Name, d.instance.Namespace, homeCluster, pvList)
}

func (d *DRPCInstance) createOrUpdatePVsManifestWork(
	name string, namespace string, homeClusterName string, pvList []corev1.PersistentVolume) error {
	d.log.Info("Create manifest work for PVs", "DRPC",
		name, "cluster", homeClusterName, "PV count", len(pvList))

	mwName := BuildManifestWorkName(name, namespace, MWTypePV)

	manifestWork, err := d.generatePVManifestWork(mwName, homeClusterName, pvList)
	if err != nil {
		return err
	}

	return d.createOrUpdateManifestWork(manifestWork, homeClusterName)
}

func (d *DRPCInstance) processVRGManifestWork(homeCluster string) error {
	d.log.Info("Processing VRG ManifestWork", "cluster", homeCluster)

	if err := d.createOrUpdateVRGRolesManifestWork(homeCluster); err != nil {
		d.log.Error(err, "failed to create or update VolumeReplicationGroup Roles manifest")

		return err
	}

	if err := d.createOrUpdateVRGManifestWork(
		d.instance.Name, d.instance.Namespace,
		homeCluster, d.instance.Spec.S3Endpoint, d.instance.Spec.S3SecretName, d.instance.Spec.PVCSelector); err != nil {
		d.log.Error(err, "failed to create or update VolumeReplicationGroup manifest")

		return err
	}

	return nil
}

func (d *DRPCInstance) createOrUpdateVRGRolesManifestWork(namespace string) error {
	manifestWork, err := d.generateVRGRolesManifestWork(namespace)
	if err != nil {
		return err
	}

	return d.createOrUpdateManifestWork(manifestWork, namespace)
}

func (d *DRPCInstance) createOrUpdateVRGManifestWork(
	name, namespace, homeCluster, s3Endpoint, s3SecretName string, pvcSelector metav1.LabelSelector) error {
	d.log.Info(fmt.Sprintf("Create or Update manifestwork %s:%s:%s:%s:%s",
		name, namespace, homeCluster, s3Endpoint, s3SecretName))

	manifestWork, err := d.generateVRGManifestWork(name, namespace, homeCluster, s3Endpoint, s3SecretName, pvcSelector)
	if err != nil {
		return err
	}

	return d.createOrUpdateManifestWork(manifestWork, homeCluster)
}

func (d *DRPCInstance) generateVRGRolesManifestWork(namespace string) (*ocmworkv1.ManifestWork, error) {
	vrgClusterRole, err := d.generateVRGClusterRoleManifest()
	if err != nil {
		d.log.Error(err, "failed to generate VolumeReplicationGroup ClusterRole manifest")

		return nil, err
	}

	vrgClusterRoleBinding, err := d.generateVRGClusterRoleBindingManifest()
	if err != nil {
		d.log.Error(err, "failed to generate VolumeReplicationGroup ClusterRoleBinding manifest")

		return nil, err
	}

	manifests := []ocmworkv1.Manifest{*vrgClusterRole, *vrgClusterRoleBinding}

	return d.newManifestWork(
		"ramendr-vrg-roles",
		namespace,
		map[string]string{},
		manifests), nil
}

func (d *DRPCInstance) generateVRGClusterRoleManifest() (*ocmworkv1.Manifest, error) {
	return d.generateManifest(&rbacv1.ClusterRole{
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

func (d *DRPCInstance) generateVRGClusterRoleBindingManifest() (*ocmworkv1.Manifest, error) {
	return d.generateManifest(&rbacv1.ClusterRoleBinding{
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

func (d *DRPCInstance) generatePVManifestWork(
	mwName, homeClusterName string, pvList []corev1.PersistentVolume) (*ocmworkv1.ManifestWork, error) {
	manifests, err := d.generatePVManifest(pvList)
	if err != nil {
		return nil, err
	}

	return d.newManifestWork(
		mwName,
		homeClusterName,
		map[string]string{"app": "PV"},
		manifests), nil
}

// This function follow a slightly different pattern than the rest, simply because the pvList that come
// from the S3 store will contain PV objects already converted to a string.
func (d *DRPCInstance) generatePVManifest(
	pvList []corev1.PersistentVolume) ([]ocmworkv1.Manifest, error) {
	manifests := []ocmworkv1.Manifest{}

	for _, pv := range pvList {
		pvClientManifest, err := d.generateManifest(pv)
		// Either all succeed or none
		if err != nil {
			d.log.Error(err, "failed to generate manifest for PV")

			return nil, err
		}

		manifests = append(manifests, *pvClientManifest)
	}

	return manifests, nil
}

func (d *DRPCInstance) generateVRGManifestWork(
	name, namespace, homeCluster, s3Endpoint, s3SecretName string,
	pvcSelector metav1.LabelSelector) (*ocmworkv1.ManifestWork, error) {
	vrgClientManifest, err := d.generateVRGManifest(name, namespace, s3Endpoint, s3SecretName, pvcSelector)
	if err != nil {
		d.log.Error(err, "failed to generate VolumeReplicationGroup manifest")

		return nil, err
	}

	manifests := []ocmworkv1.Manifest{*vrgClientManifest}

	return d.newManifestWork(
		fmt.Sprintf(ManifestWorkNameFormat, name, namespace, MWTypeVRG),
		homeCluster,
		map[string]string{"app": "VRG"},
		manifests), nil
}

func (d *DRPCInstance) generateVRGManifest(
	name, namespace, s3Endpoint, s3SecretName string, pvcSelector metav1.LabelSelector) (*ocmworkv1.Manifest, error) {
	return d.generateManifest(&rmn.VolumeReplicationGroup{
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

func (d *DRPCInstance) generateManifest(obj interface{}) (*ocmworkv1.Manifest, error) {
	objJSON, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal %v to JSON, error %w", obj, err)
	}

	manifest := &ocmworkv1.Manifest{}
	manifest.RawExtension = runtime.RawExtension{Raw: objJSON}

	return manifest, nil
}

func (d *DRPCInstance) newManifestWork(name string, mcNamespace string,
	labels map[string]string, manifests []ocmworkv1.Manifest) *ocmworkv1.ManifestWork {
	return &ocmworkv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: mcNamespace,
			Labels:    labels,
			Annotations: map[string]string{
				DRPCNameAnnotation:      d.instance.Name,
				DRPCNamespaceAnnotation: d.instance.Namespace,
			},
		},
		Spec: ocmworkv1.ManifestWorkSpec{
			Workload: ocmworkv1.ManifestsTemplate{
				Manifests: manifests,
			},
		},
	}
}

func (d *DRPCInstance) createOrUpdateManifestWork(
	mw *ocmworkv1.ManifestWork,
	managedClusternamespace string) error {
	foundMW := &ocmworkv1.ManifestWork{}

	err := d.reconciler.Get(d.ctx,
		types.NamespacedName{Name: mw.Name, Namespace: managedClusternamespace},
		foundMW)
	if err != nil {
		if !errors.IsNotFound(err) {
			return errorswrapper.Wrap(err, fmt.Sprintf("failed to fetch ManifestWork %s", mw.Name))
		}

		// Let DRPC receive notification for any changes to ManifestWork CR created by it.
		// if err := ctrl.SetControllerReference(d.instance, mw, d.reconciler.Scheme); err != nil {
		// 	return fmt.Errorf("failed to set owner reference to ManifestWork resource (%s/%s) (%v)",
		// 		mw.Name, mw.Namespace, err)
		// }

		d.log.Info("Creating ManifestWork for", "cluster", managedClusternamespace, "MW", mw)

		return d.reconciler.Create(d.ctx, mw)
	}

	if !reflect.DeepEqual(foundMW.Spec, mw.Spec) {
		mw.Spec.DeepCopyInto(&foundMW.Spec)

		d.log.Info("ManifestWork exists.", "MW", mw)

		return d.reconciler.Update(d.ctx, foundMW)
	}

	return nil
}

func (d *DRPCInstance) hasVRGStateBeenUpdatedToSecondary(clusterName string) (bool, error) {
	vrgMWName := BuildManifestWorkName(d.instance.Name, d.instance.Namespace, MWTypeVRG)
	d.log.Info("Check if VRG has been updated to secondary", "name", vrgMWName, "cluster", clusterName)

	mw, err := d.findManifestWork(vrgMWName, clusterName)
	if err != nil {
		d.log.Error(err, "failed to check whether VRG state is secondary")

		return false, err
	}

	vrg, err := d.getVRGFromManifestWork(mw)
	if err != nil {
		d.log.Error(err, "failed to check whether VRG state is secondary")

		return false, err
	}

	if vrg.Spec.ReplicationState == volrep.Secondary {
		d.log.Info("VRG MW already secondary on this cluster", "name", vrg.Name, "cluster", mw.Namespace)

		return true, nil
	}

	return false, err
}

func (d *DRPCInstance) updateVRGStateToSecondary(clusterName string) error {
	vrgMWName := BuildManifestWorkName(d.instance.Name, d.instance.Namespace, MWTypeVRG)
	d.log.Info(fmt.Sprintf("Updating VRG ownedby MW %s to secondary for cluster %s", vrgMWName, clusterName))

	mw, err := d.findManifestWork(vrgMWName, clusterName)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}

		d.log.Error(err, "failed to update VRG state")

		return err
	}

	vrg, err := d.getVRGFromManifestWork(mw)
	if err != nil {
		d.log.Error(err, "failed to update VRG state")

		return err
	}

	if vrg.Spec.ReplicationState == volrep.Secondary {
		d.log.Info(fmt.Sprintf("VRG %s already secondary on this cluster %s", vrg.Name, mw.Namespace))

		return nil
	}

	vrg.Spec.ReplicationState = volrep.Secondary

	vrgClientManifest, err := d.generateManifest(vrg)
	if err != nil {
		d.log.Error(err, "failed to generate manifest")

		return err
	}

	mw.Spec.Workload.Manifests[0] = *vrgClientManifest

	err = d.reconciler.Update(d.ctx, mw)
	if err != nil {
		return fmt.Errorf("failed to update MW (%w)", err)
	}

	d.log.Info(fmt.Sprintf("Updated VRG running in cluster %s to secondary. VRG (%v)", clusterName, vrg))

	return nil
}

func (d *DRPCInstance) getVRGFromManifestWork(mw *ocmworkv1.ManifestWork) (*rmn.VolumeReplicationGroup, error) {
	if len(mw.Spec.Workload.Manifests) == 0 {
		return nil, fmt.Errorf("invalid VRG ManifestWork for type: %s", mw.Name)
	}

	vrgClientManifest := &mw.Spec.Workload.Manifests[0]
	vrg := &rmn.VolumeReplicationGroup{}

	err := yaml.Unmarshal(vrgClientManifest.RawExtension.Raw, &vrg)
	if err != nil {
		return nil, fmt.Errorf("unable to unmarshal VRG object (%w)", err)
	}

	return vrg, nil
}

func (d *DRPCInstance) updateDRPCStatus() error {
	d.log.Info("Updating DRPC status")

	d.instance.Status.LastUpdateTime = metav1.Now()

	if err := d.reconciler.Status().Update(d.ctx, d.instance); err != nil {
		return errorswrapper.Wrap(err, "failed to update DRPC status")
	}

	d.log.Info(fmt.Sprintf("Updated DRPC Status %+v", d.instance.Status))

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
func (r *DRPlacementControlReconciler) getManagedClusterResource(
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
func (r *DRPlacementControlReconciler) getOrCreateManagedClusterView(
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

func (d *DRPCInstance) listPVsFromS3Store() ([]corev1.PersistentVolume, error) {
	s3SecretLookupKey := types.NamespacedName{
		Name:      d.instance.Spec.S3SecretName,
		Namespace: d.instance.Namespace,
	}

	s3Bucket := constructBucketName(d.instance.Namespace, d.instance.Name)

	return d.reconciler.PVDownloader.DownloadPVs(
		d.ctx, d.reconciler.Client, d.reconciler.ObjStoreGetter, d.instance.Spec.S3Endpoint, d.instance.Spec.S3Region,
		s3SecretLookupKey, d.instance.Name, s3Bucket)
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

func (d *DRPCInstance) advanceToNextDRState() {
	var nextState rmn.DRState

	switch d.instance.Status.LastKnownDRState {
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

	d.setDRState(nextState)
}

func (d *DRPCInstance) resetDRState() {
	d.log.Info("Resetting last known DR state", "lndrs", d.instance.Status.LastKnownDRState)

	d.setDRState(rmn.DRState(""))
}

func (d *DRPCInstance) setDRState(nextState rmn.DRState) {
	if d.instance.Status.LastKnownDRState != nextState {
		d.log.Info(fmt.Sprintf("LastKnownDRState: Current State '%s'. Next State '%s'",
			d.instance.Status.LastKnownDRState, nextState))

		d.instance.Status.LastKnownDRState = nextState
		d.needStatusUpdate = true
	}
}
