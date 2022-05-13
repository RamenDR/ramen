/*
Copyright 2022 The RamenDR authors.

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
	"net"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/go-logr/logr"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/util"
)

// DRClusterReconciler reconciles a DRCluster object
type DRClusterReconciler struct {
	client.Client
	APIReader         client.Reader
	Scheme            *runtime.Scheme
	ObjectStoreGetter ObjectStoreGetter
}

// DRCluster condition reasons
const (
	DRClusterConditionReasonInitializing = "Initializing"
	DRClusterConditionReasonFencing      = "Fencing"
	DRClusterConditionReasonUnfencing    = "Unfencing"
	DRClusterConditionReasonCleaning     = "Cleaning"
	DRClusterConditionReasonFenced       = "Fenced"
	DRClusterConditionReasonUnfenced     = "Unfenced"
	DRClusterConditionReasonClean        = "Clean"
	DRClusterConditionReasonValidated    = "Succeeded"

	DRClusterConditionReasonFenceError   = "FenceError"
	DRClusterConditionReasonUnfenceError = "UnfenceError"
	DRClusterConditionReasonCleanError   = "CleanError"

	DRClusterConditionReasonError        = "Error"
	DRClusterConditionReasonErrorUnknown = "UnknownError"
)

//nolint:lll
//+kubebuilder:rbac:groups=ramendr.openshift.io,resources=drclusters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ramendr.openshift.io,resources=drclusters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ramendr.openshift.io,resources=drclusters/finalizers,verbs=update
//+kubebuilder:rbac:groups=work.open-cluster-management.io,resources=manifestworks,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the DRCluster object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.9.2/pkg/reconcile
func (r *DRClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.Log.WithName("controllers").WithName("drcluster").WithValues("name", req.NamespacedName.Name)
	log.Info("reconcile enter")

	defer log.Info("reconcile exit")

	drcluster := &ramen.DRCluster{}
	if err := r.Client.Get(ctx, req.NamespacedName, drcluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(fmt.Errorf("get: %w", err))
	}

	var manifestWorkUtil util.MWUtil
	u := &drclusterUpdater{ctx, drcluster, r.Client, log, r, manifestWorkUtil}
	u.initializeStatus()

	_, ramenConfig, err := ConfigMapGet(ctx, r.APIReader)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("config map get: %w", u.validatedSetFalse("ConfigMapGetFailed", err))
	}

	manifestWorkUtil = util.MWUtil{Client: r.Client, Ctx: ctx, Log: log, InstName: "", InstNamespace: ""}
	u.mwUtil = manifestWorkUtil

	// DRCluster is marked for deletion
	if !drcluster.ObjectMeta.DeletionTimestamp.IsZero() {
		log.Info("delete")

		// Undeploy manifests
		if err := drClusterUndeploy(drcluster, &manifestWorkUtil); err != nil {
			return ctrl.Result{}, fmt.Errorf("drclusters undeploy: %w", err)
		}

		if err := u.finalizerRemove(); err != nil {
			return ctrl.Result{}, fmt.Errorf("finalizer remove update: %w", err)
		}

		return ctrl.Result{}, nil
	}

	log.Info("create/update")

	if err := u.addLabelsAndFinalizers(); err != nil {
		return ctrl.Result{}, fmt.Errorf("finalizer add update: %w", u.validatedSetFalse("FinalizerAddFailed", err))
	}

	reason, err := validateDRCluster(ctx, drcluster, r.APIReader, r.ObjectStoreGetter, req.NamespacedName.String(), log)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("validate: %w", u.validatedSetFalse(reason, err))
	}

	if err := drClusterDeploy(drcluster, &manifestWorkUtil, ramenConfig); err != nil {
		return ctrl.Result{}, fmt.Errorf("drclusters deploy: %w", u.validatedSetFalse("DrClustersDeployFailed", err))
	}

	// TODO: Setup views for storage class and VRClass to read and report IDs

	setDRClusterValidatedCondition(&drcluster.Status.Conditions, drcluster.Generation, "Validated the cluster")

	return r.processFencing(u)
}

func (u *drclusterUpdater) initializeStatus() {
	// TODO: Only initialize those conditions whose status is
	//       not available.
	// Set the DRCluster conditions to unknown as nothing is known at this point
	msg := "Initializing DRCluster"
	setDRClusterInitialCondition(&u.object.Status.Conditions, u.object.Generation, msg)
}

func validateDRCluster(ctx context.Context, drcluster *ramen.DRCluster, apiReader client.Reader,
	objectStoreGetter ObjectStoreGetter, listKeyPrefix string,
	log logr.Logger,
) (string, error) {
	reason, err := validateS3Profile(ctx, apiReader, objectStoreGetter, drcluster, listKeyPrefix, log)
	if err != nil {
		return reason, err
	}

	err = validateCIDRsFormat(drcluster, log)
	if err != nil {
		return ReasonValidationFailed, err
	}

	// TODO: Validate managedCluster name? and also ensure it is not deleted!

	return "", nil
}

func validateS3Profile(ctx context.Context, apiReader client.Reader,
	objectStoreGetter ObjectStoreGetter,
	drcluster *ramen.DRCluster, listKeyPrefix string, log logr.Logger) (string, error) {
	if drcluster.Spec.ClusterFence == ramen.ClusterFenceStateFenced ||
		drcluster.Spec.ClusterFence == ramen.ClusterFenceStateManuallyFenced {
		return "", nil
	}

	if reason, err := s3ProfileValidate(ctx, apiReader, objectStoreGetter,
		drcluster.Spec.S3ProfileName, listKeyPrefix, log); err != nil {
		return reason, err
	}

	return "", nil
}

func s3ProfileValidate(ctx context.Context, apiReader client.Reader,
	objectStoreGetter ObjectStoreGetter, s3ProfileName, listKeyPrefix string,
	log logr.Logger,
) (string, error) {
	objectStore, err := objectStoreGetter.ObjectStore(ctx, apiReader, s3ProfileName, "drpolicy validation", log)
	if err != nil {
		return "s3ConnectionFailed", fmt.Errorf("%s: %w", s3ProfileName, err)
	}

	if _, err := objectStore.ListKeys(listKeyPrefix); err != nil {
		return "s3ListFailed", fmt.Errorf("%s: %w", s3ProfileName, err)
	}

	return "", nil
}

func validateCIDRsFormat(drcluster *ramen.DRCluster, log logr.Logger) error {
	// validate the CIDRs format
	invalidCidrs := []string{}

	for i := range drcluster.Spec.CIDRs {
		if _, _, err := net.ParseCIDR(drcluster.Spec.CIDRs[i]); err != nil {
			invalidCidrs = append(invalidCidrs, drcluster.Spec.CIDRs[i])

			log.Error(err, ReasonValidationFailed)
		}
	}

	if len(invalidCidrs) > 0 {
		return fmt.Errorf("invalid CIDRs specified %s", strings.Join(invalidCidrs, ", "))
	}

	return nil
}

func (r DRClusterReconciler) processFencing(u *drclusterUpdater) (ctrl.Result, error) {
	requeue, err := u.clusterFenceHandle()
	if err != nil {
		if updateErr := u.statusUpdate(); updateErr != nil {
			u.log.Error(updateErr, "status update failed")
		}

		return ctrl.Result{}, fmt.Errorf("failed to handle cluster fencing: %w", err)
	}

	return ctrl.Result{Requeue: requeue}, u.statusUpdate()
}

type drclusterUpdater struct {
	ctx        context.Context
	object     *ramen.DRCluster
	client     client.Client
	log        logr.Logger
	reconciler *DRClusterReconciler
	mwUtil     util.MWUtil
}

func (u *drclusterUpdater) validatedSetFalse(reason string, err error) error {
	if err1 := u.statusConditionSet(ramen.DRClusterValidated, metav1.ConditionFalse, reason, err.Error()); err1 != nil {
		return err1
	}

	return err
}

func (u *drclusterUpdater) statusConditionSet(
	conditionType string,
	status metav1.ConditionStatus,
	reason, message string,
) error {
	conditions := &u.object.Status.Conditions

	if util.GenericStatusConditionSet(u.object, conditions, conditionType, status, reason, message, u.log) {
		return u.statusUpdate()
	}

	return nil
}

func (u *drclusterUpdater) statusUpdate() error {
	return u.client.Status().Update(u.ctx, u.object)
}

const drClusterFinalizerName = "drclusters.ramendr.openshift.io/ramen"

func (u *drclusterUpdater) addLabelsAndFinalizers() error {
	return util.GenericAddLabelsAndFinalizers(u.ctx, u.object, drClusterFinalizerName, u.client, u.log)
}

func (u *drclusterUpdater) finalizerRemove() error {
	return util.GenericFinalizerRemove(u.ctx, u.object, drClusterFinalizerName, u.client, u.log)
}

// TODO:
// 1) For now by default fenceStatus is ClusterFenceStateUnfenced.
//    However, we need to handle explicit unfencing operation to unfence
//    a fenced cluster below, by deleting the fencing CR created by
//    ramen.
//
// 2) How to differentiate between ClusterFenceStateUnfenced being
//    set because a manually fenced cluster is manually unfenced against the
//    requirement to unfence a cluster that has been fenced by ramen.
//
// 3) Handle Ramen driven fencing here
//
func (u *drclusterUpdater) clusterFenceHandle() (bool, error) {
	if u.object.Spec.ClusterFence == ramen.ClusterFenceStateUnfenced {
		return u.clusterUnfence()
	}

	if u.object.Spec.ClusterFence == ramen.ClusterFenceStateManuallyFenced {
		setDRClusterFencedCondition(&u.object.Status.Conditions, u.object.Generation, "Cluster Manually fenced")
		// no requeue is needed and no error as this is a manual fence
		return false, nil
	}

	if u.object.Spec.ClusterFence == ramen.ClusterFenceStateManuallyUnfenced {
		setDRClusterCleanCondition(&u.object.Status.Conditions, u.object.Generation,
			"Cluster Manually Unfenced and clean")
		// no requeue is needed and no error as this is a manual unfence
		return false, nil
	}

	if u.object.Spec.ClusterFence == ramen.ClusterFenceStateFenced {
		return u.clusterFence()
	}

	// This is needed when a DRCluster is created fresh without any fencing related information.
	// That is cluster being clean without any NetworkFence CR. Or is it? What if someone just
	// edits the resource and removes the entire line that has fencing state? Should that be
	// treated as cluster being clean or unfence?
	setDRClusterCleanCondition(&u.object.Status.Conditions, u.object.Generation, "Cluster Clean")

	return false, nil
}

func (u *drclusterUpdater) clusterFence() (bool, error) {
	// Ideally, here it should collect all the DRClusters available
	// in the cluster and then match the appropriate peer cluster
	// out of them by looking at the storage relationships. However,
	// currently, DRCluster does not contain the storage relationship
	// identity. Until that capability is not available, the alternate
	// way is to collect all the DRPolicies and out of them choose the
	// cluster whose region is same is current DRCluster's region.
	// And that matching cluster is chosen as the peer cluster where
	// the fencing resource is created to fence off this cluster.
	drpolicies, err := util.GetAllDRPolicies(u.ctx, u.reconciler.APIReader)
	if err != nil {
		return true, fmt.Errorf("getting all drpolicies failed: %w", err)
	}

	peerCluster, err := getPeerCluster(u.ctx, drpolicies, u.reconciler, u.object, u.log)
	if err != nil {
		return true, fmt.Errorf("failed to get the peer cluster for the cluster %s: %w",
			u.object.Name, err)
	}

	return u.fenceClusterOnCluster(&peerCluster)
}

func (u *drclusterUpdater) clusterUnfence() (bool, error) {
	// Ideally, here it should collect all the DRClusters available
	// in the cluster and then match the appropriate peer cluster
	// out of them by looking at the storage relationships. However,
	// currently, DRCluster does not contain the storage relationship
	// identity. Until that capability is not available, the alternate
	// way is to collect all the DRPolicies and out of them choose the
	// cluster whose region is same is current DRCluster's region.
	// And that matching cluster is chosen as the peer cluster where
	// the fencing resource is created to fence off this cluster.
	drpolicies, err := util.GetAllDRPolicies(u.ctx, u.reconciler.APIReader)
	if err != nil {
		return true, fmt.Errorf("getting all drpolicies failed: %w", err)
	}

	peerCluster, err := getPeerCluster(u.ctx, drpolicies, u.reconciler, u.object,
		u.log)
	if err != nil {
		return true, fmt.Errorf("failed to get the peer cluster for the cluster %s: %w",
			u.object.Name, err)
	}

	requeue, err := u.unfenceClusterOnCluster(&peerCluster)
	if err != nil {
		return requeue, fmt.Errorf("unfence operation to fence off cluster %s on cluster %s failed",
			u.object.Name, peerCluster.Name)
	}

	if requeue {
		u.log.Info("requing as cluster unfence operation is not complete")

		return requeue, nil
	}

	// once this cluster is unfenced. Clean the fencing resource.
	return u.cleanCluster(peerCluster)
}

//
// if the fencing CR (via MCV) exists; then
//    if the status of fencing CR shows fenced
//       return dontRequeue, nil
//    else
//       return requeue, error
//    endif
// else
//    Create the fencing CR MW with Fenced state
//    return requeue, nil
// endif
func (u *drclusterUpdater) fenceClusterOnCluster(peerCluster *ramen.DRCluster) (bool, error) {
	u.log.Info(fmt.Sprintf("initiating the cluster fence from the cluster %s", peerCluster.Name))
	// TODO: Check if NetworkFence resource exist via MCV and
	//       take appropriate decisions?

	if err := u.createNFManifestWork(u.object, peerCluster, u.log); err != nil {
		setDRClusterFencingFailedCondition(&u.object.Status.Conditions, u.object.Generation,
			"NeworkFence ManifestWork creation failed")

		return true, fmt.Errorf("failed to create the NetworkFence MW on cluster %s to fence %s",
			peerCluster.Name, u.object.Name)
	}

	setDRClusterFencedCondition(&u.object.Status.Conditions, u.object.Generation,
		"Cluster successfully fenced")

	return false, nil
}

//
// if the fencing CR (via MCV) exist; then
//    if the status of fencing CR shows unfenced
//       return dontRequeue, nil
//    else
//       return requeue, error
//    endif
// else
//    Create the fencing CR MW with Unfenced state
//    return requeue, nil
// endif
// TODO: Remove the below linter check skipper
func (u *drclusterUpdater) unfenceClusterOnCluster(peerCluster *ramen.DRCluster) (bool, error) {
	u.log.Info(fmt.Sprintf("initiating the cluster unfence from the cluster %s", peerCluster.Name))
	// TODO: Check if NetworkFence resource exist via MCV and
	//       take appropriate decisions?

	if err := u.createNFManifestWork(u.object, peerCluster, u.log); err != nil {
		setDRClusterUnfencingFailedCondition(&u.object.Status.Conditions, u.object.Generation,
			"NeworkFence ManifestWork for unfence failed")

		return true, fmt.Errorf("failed to generate NetworkFence MW on cluster %s to unfence %s",
			peerCluster.Name, u.object.Name)
	}

	setDRClusterUnfencedCondition(&u.object.Status.Conditions, u.object.Generation,
		"Cluster successfully unfenced")

	return false, nil
}

// We are here means following things have been confirmed.
// 1) Fencing CR MCV was obtained.
// 2) MCV for the Fencing CR showed the cluster as unfenced
//
// * Proceed to delete the ManifestWork for the fencingCR
// * Issue a requeue
func (u *drclusterUpdater) cleanCluster(peerCluster ramen.DRCluster) (bool, error) {
	u.log.Info(fmt.Sprintf("cleaning the cluster fence resource from the cluster %s", peerCluster.Name))
	// TODO: delete the fencing CR MW.

	err := u.mwUtil.DeleteManifestWork(fmt.Sprintf(util.ManifestWorkNameFormat,
		u.object.Name, peerCluster.Name, util.MWTypeNF), peerCluster.Name)
	if err != nil {
		if errors.IsNotFound(err) {
			setDRClusterCleanCondition(&u.object.Status.Conditions, u.object.Generation, "fencing resource cleaned from cluster")

			return false, nil
		}

		setDRClusterCleaningFailedCondition(&u.object.Status.Conditions, u.object.Generation,
			"failed to clean fencing resource")

		return true, fmt.Errorf("failed to create NetworkFence resource from cluster %s", peerCluster.Name)
	}

	setDRClusterCleaningCondition(&u.object.Status.Conditions, u.object.Generation, "NetworkFence resource clean started")

	// Since ManifestWork for the fencing CR delete request
	// has been just issued, requeue is needed to ensure that
	// the fencing CR has indeed been deleted from the cluster.
	return true, nil
}

func getPeerCluster(ctx context.Context, list ramen.DRPolicyList, reconciler *DRClusterReconciler,
	object *ramen.DRCluster, log logr.Logger) (ramen.DRCluster, error) {
	var peerCluster ramen.DRCluster

	found := false

	log.Info(fmt.Sprintf("number of DRPolicies found: %d", len(list.Items)))

	for i := range list.Items {
		drp := &list.Items[i]

		log.Info(fmt.Sprintf("DRPolicy: %s, DRClusters: (%d) %v", drp.Name, len(drp.Spec.DRClusters),
			drp.Spec.DRClusters))

		// TODO: let policy = [e1, e2, e3]. Now, if e1 has to be fenced off,
		//       it will be created on either of e2 or e3. And later when e1
		//       has to be unfenced, the unfence should go to the same cluster
		//       where fencing CR was created. For now, assumption is that
		//       drPolicies will be having 2 clusters.
		for _, cluster := range drp.Spec.DRClusters {
			// skip if cluster is this drCluster
			if cluster == object.Name {
				drCluster, err := getPeerFromPolicy(ctx, reconciler, log, drp, object)
				if err != nil {
					log.Error(err, fmt.Sprintf("failed to get peer cluster for cluster %s", cluster))

					break
				}

				peerCluster = *drCluster
				found = true

				break
			}
		}

		if found {
			break
		}
	}

	if !found {
		return peerCluster, fmt.Errorf("failed to find the peer cluster for cluster %s", object.Name)
	}

	return peerCluster, nil
}

func getPeerFromPolicy(ctx context.Context, reconciler *DRClusterReconciler, log logr.Logger,
	drPolicy *ramen.DRPolicy, drCluster *ramen.DRCluster) (*ramen.DRCluster, error) {
	peerCluster := &ramen.DRCluster{}
	found := false

	for _, cluster := range drPolicy.Spec.DRClusters {
		if cluster == drCluster.Name {
			// skip myself
			continue
		}

		// search for the drCluster object for the peer cluster in the
		// same namespace as this cluster
		if err := reconciler.APIReader.Get(ctx,
			types.NamespacedName{Name: cluster, Namespace: drCluster.Namespace}, peerCluster); err != nil {
			log.Error(err, fmt.Sprintf("failed to get the DRCluster resource with name %s", cluster))
			// for now continue. As we just need to get one DRCluster with matching
			// region.
			continue
		}

		if !peerCluster.ObjectMeta.DeletionTimestamp.IsZero() {
			log.Info(fmt.Sprintf("peer cluster %s of cluster %s is being deleted",
				peerCluster.Name, drCluster.Name))
			// for now continue. We just need to get one DRCluster with
			// matching region
			continue
		}

		if drCluster.Spec.Region == peerCluster.Spec.Region {
			found = true

			break
		}
	}

	if !found {
		return nil, fmt.Errorf("count not find the peer cluster for %s", drCluster.Name)
	}

	return peerCluster, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DRClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ramen.DRCluster{}).
		Watches(
			&source.Kind{Type: &corev1.ConfigMap{}},
			handler.EnqueueRequestsFromMapFunc(r.drClusterConfigMapMapFunc),
		).
		Complete(r)
}

func (r *DRClusterReconciler) drClusterConfigMapMapFunc(configMap client.Object) []reconcile.Request {
	if configMap.GetName() != HubOperatorConfigMapName || configMap.GetNamespace() != NamespaceName() {
		return []reconcile.Request{}
	}

	drcusters := &ramen.DRClusterList{}
	if err := r.Client.List(context.TODO(), drcusters); err != nil {
		return []reconcile.Request{}
	}

	requests := make([]reconcile.Request, len(drcusters.Items))
	for i, drcluster := range drcusters.Items {
		requests[i].Name = drcluster.GetName()
	}

	return requests
}

func setDRClusterInitialCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               ramen.DRClusterValidated,
		Reason:             DRClusterConditionReasonInitializing,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionUnknown,
		Message:            message,
	})
	setStatusCondition(conditions, metav1.Condition{
		Type:               ramen.DRClusterConditionTypeFenced,
		Reason:             DRClusterConditionReasonInitializing,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionUnknown,
		Message:            message,
	})
	setStatusCondition(conditions, metav1.Condition{
		Type:               ramen.DRClusterConditionTypeClean,
		Reason:             DRClusterConditionReasonInitializing,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionUnknown,
		Message:            message,
	})
}

// sets conditions when DRCluster is being fenced
// This means, a ManifestWork has been just created
// for NetworkFence CR and we have not yet seen the
// status of it.
// unfence = true, fence = false, clean = true
// TODO: Remove the linter skip when this function is used
//nolint:deadcode,unused
func setDRClusterFencingCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               ramen.DRClusterConditionTypeFenced,
		Reason:             DRClusterConditionReasonFencing,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	})
	setStatusCondition(conditions, metav1.Condition{
		Type:               ramen.DRClusterConditionTypeClean,
		Reason:             DRClusterConditionReasonFencing,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionTrue,
		Message:            message,
	})
}

// sets conditions when DRCluster is being unfenced
// This means, a ManifestWork has been just created/updated
// for NetworkFence CR and we have not yet seen the
// status of it.
// clean is false, because, the cluster is already fenced
// due to NetworkFence CR.
// unfence = false, fence = true, clean = false
// TODO: Remove the linter skip when this function is used
//nolint:deadcode,unused
func setDRClusterUnfencingCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               ramen.DRClusterConditionTypeFenced,
		Reason:             DRClusterConditionReasonUnfencing,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionTrue,
		Message:            message,
	})
	setStatusCondition(conditions, metav1.Condition{
		Type:               ramen.DRClusterConditionTypeClean,
		Reason:             DRClusterConditionReasonUnfencing,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	})
}

// sets conditions when the NetworkFence CR is being cleaned
// This means, a ManifestWork has been just deleted for
// NetworkFence CR and we have not yet seen the
// status of it.
// clean is false, because, it is yet not sure if the NetworkFence
// CR has been deleted or not.
// unfence = true, fence = false, clean = false
// TODO: Remove the linter skip when this function is used
func setDRClusterCleaningCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               ramen.DRClusterConditionTypeFenced,
		Reason:             DRClusterConditionReasonCleaning,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	})
	setStatusCondition(conditions, metav1.Condition{
		Type:               ramen.DRClusterConditionTypeClean,
		Reason:             DRClusterConditionReasonCleaning,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	})
}

// DRCluster is validated
func setDRClusterValidatedCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               ramen.DRClusterValidated,
		Reason:             DRClusterConditionReasonValidated,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionTrue,
		Message:            message,
	})
}

// sets conditions when cluster has been successfully
// fenced via NetworkFence CR which still exists.
// Hence clean is false.
// unfence = false, fence = true, clean = false
func setDRClusterFencedCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               ramen.DRClusterConditionTypeFenced,
		Reason:             DRClusterConditionReasonFenced,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionTrue,
		Message:            message,
	})
	setStatusCondition(conditions, metav1.Condition{
		Type:               ramen.DRClusterConditionTypeClean,
		Reason:             DRClusterConditionReasonFenced,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	})
}

// sets conditions when cluster has been successfully
// unfenced via NetworkFence CR which still exists.
// Hence clean is false.
// unfence = true, fence = flase, clean = false
func setDRClusterUnfencedCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               ramen.DRClusterConditionTypeFenced,
		Reason:             DRClusterConditionReasonUnfenced,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	})
	setStatusCondition(conditions, metav1.Condition{
		Type:               ramen.DRClusterConditionTypeClean,
		Reason:             DRClusterConditionReasonFenced,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	})
}

// sets conditions when the NetworkFence CR for this cluster
// has been successfully deleted. Since cleaning of NetworkFence
// CR is done after a successful unfence,
// unfence = true, fence = flase, clean = true
func setDRClusterCleanCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               ramen.DRClusterConditionTypeFenced,
		Reason:             DRClusterConditionReasonClean,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	})
	setStatusCondition(conditions, metav1.Condition{
		Type:               ramen.DRClusterConditionTypeClean,
		Reason:             DRClusterConditionReasonClean,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionTrue,
		Message:            message,
	})
}

// sets conditions when the attempt to fence the cluster
// fails. Since, fencing is done via NetworkFence CR, after
// on a clean machine assumed to have no fencing CRs for
// this cluster,
// unfence = true, fence = flase, clean = true
// TODO: Remove the linter skip when this function is used
func setDRClusterFencingFailedCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               ramen.DRClusterConditionTypeFenced,
		Reason:             DRClusterConditionReasonFenceError,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	})
	setStatusCondition(conditions, metav1.Condition{
		Type:               ramen.DRClusterConditionTypeClean,
		Reason:             DRClusterConditionReasonFenceError,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionTrue,
		Message:            message,
	})
}

// sets conditions when the attempt to unfence the cluster
// fails. Since, unfencing is done via NetworkFence CR, after
// successful fencing operation,
// unfence = false, fence = true, clean = false
// TODO: Remove the linter skip when this function is used
func setDRClusterUnfencingFailedCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               ramen.DRClusterConditionTypeFenced,
		Reason:             DRClusterConditionReasonUnfenceError,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionTrue,
		Message:            message,
	})
	setStatusCondition(conditions, metav1.Condition{
		Type:               ramen.DRClusterConditionTypeClean,
		Reason:             DRClusterConditionReasonUnfenceError,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	})
}

// sets conditions when the attempt to delete the fencing CR
// fails. Since, cleaning is always called after a successful
// Unfence operation, unfence = true, fence = false, clean = false
// TODO: Remove the linter skip when this function is used
func setDRClusterCleaningFailedCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               ramen.DRClusterConditionTypeFenced,
		Reason:             DRClusterConditionReasonCleanError,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	})
	setStatusCondition(conditions, metav1.Condition{
		Type:               ramen.DRClusterConditionTypeClean,
		Reason:             DRClusterConditionReasonCleanError,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	})
}

func (u *drclusterUpdater) createNFManifestWork(targetCluster *ramen.DRCluster, peerCluster *ramen.DRCluster,
	log logr.Logger) error {
	// create NetworkFence ManifestWork
	log.Info(fmt.Sprintf("Creating NetworkFence ManifestWork on cluster %s to perform fencing op on cluster %s",
		peerCluster.Name, targetCluster.Name))

	nf := generateNF(targetCluster)

	if err := u.mwUtil.CreateOrUpdateNFManifestWork(
		u.object.Name, u.object.Namespace,
		peerCluster.Name, nf); err != nil {
		log.Error(err, "failed to create or update NetworkFence manifest")

		return fmt.Errorf("failed to create or update NetworkFence manifest in cluster %s to fence off cluster %s (%w)",
			peerCluster.Name, targetCluster.Name, err)
	}

	return nil
}

func generateNF(targetCluster *ramen.DRCluster) util.NetworkFence {
	// To ensure deterministic naming of the fencing CR, the resource name
	// is generated as
	// "network-fence" + name of the cluster being fenced
	resourceName := "network-fence-" + targetCluster.Name

	nf := util.NetworkFence{
		// TODO: There is no way currently to get the information such as
		// the storage CSI driver, secrets and storage specific parameters
		// until DRCluster is capable of exporting such information in its
		// status based on its reconciliation. Until then, the fencing CR
		// would be incomplete and incapable of performing fencing operation.
		TypeMeta:   metav1.TypeMeta{Kind: "NetworkFence", APIVersion: "csiaddons.openshift.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: resourceName},
		Spec: util.NetworkFenceSpec{
			FenceState: util.FenceState(targetCluster.Spec.ClusterFence),
			Cidrs:      targetCluster.Spec.CIDRs,
		},
	}

	return nf
}
