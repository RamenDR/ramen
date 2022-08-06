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
	"reflect"
	"strings"

	"github.com/google/uuid"
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

	csiaddonsv1alpha1 "github.com/csi-addons/kubernetes-csi-addons/apis/csiaddons/v1alpha1"
	"github.com/go-logr/logr"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/util"
)

// DRClusterReconciler reconciles a DRCluster object
type DRClusterReconciler struct {
	client.Client
	APIReader         client.Reader
	Scheme            *runtime.Scheme
	MCVGetter         util.ManagedClusterViewGetter
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

//nolint:gosec
const (
	StorageAnnotationSecretName      = "drcluster.ramendr.openshift.io/storage-secret-name"
	StorageAnnotationSecretNamespace = "drcluster.ramendr.openshift.io/storage-secret-namespace"
	StorageAnnotationClusterID       = "drcluster.ramendr.openshift.io/storage-clusterid"
	StorageAnnotationDriver          = "drcluster.ramendr.openshift.io/storage-driver"
)

const (
	DRClusterNameAnnotation = "drcluster.ramendr.openshift.io/drcluster-name"
)

//nolint:lll
//+kubebuilder:rbac:groups=ramendr.openshift.io,resources=drclusters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ramendr.openshift.io,resources=drclusters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ramendr.openshift.io,resources=drclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=addon.open-cluster-management.io,resources=managedclusteraddons,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups=work.open-cluster-management.io,resources=manifestworks,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=list;watch

//nolint:funlen
func (r *DRClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// TODO: Validate managedCluster name? and also ensure it is not deleted!
	// TODO: Setup views for storage class and VRClass to read and report IDs
	log := ctrl.Log.WithName("controllers").WithName("drcluster").WithValues("name",
		req.NamespacedName.Name, "rid", uuid.New())
	log.Info("reconcile enter")

	defer log.Info("reconcile exit")

	var (
		requeue        bool
		reconcileError error
	)

	drcluster := &ramen.DRCluster{}
	if err := r.Client.Get(ctx, req.NamespacedName, drcluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(fmt.Errorf("get: %w", err))
	}

	manifestWorkUtil := &util.MWUtil{Client: r.Client, Ctx: ctx, Log: log, InstName: drcluster.Name, InstNamespace: ""}

	u := &drclusterInstance{
		ctx: ctx, object: drcluster, client: r.Client, log: log, reconciler: r,
		mwUtil: manifestWorkUtil,
	}

	u.initializeStatus()

	_, ramenConfig, err := ConfigMapGet(ctx, r.APIReader)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("config map get: %w", u.validatedSetFalseAndUpdate("ConfigMapGetFailed", err))
	}

	if !u.object.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.processDeletion(u)
	}

	log.Info("create/update")

	if err := u.addLabelsAndFinalizers(); err != nil {
		return ctrl.Result{}, fmt.Errorf("finalizer add update: %w", u.validatedSetFalseAndUpdate("FinalizerAddFailed", err))
	}

	if err := drClusterDeploy(u, ramenConfig); err != nil {
		return ctrl.Result{}, fmt.Errorf("drclusters deploy: %w", u.validatedSetFalseAndUpdate("DrClustersDeployFailed", err))
	}

	if err = validateCIDRsFormat(drcluster, log); err != nil {
		return ctrl.Result{}, fmt.Errorf("drclusters CIDRs validate: %w",
			u.validatedSetFalseAndUpdate(ReasonValidationFailed, err))
	}

	requeue, err = u.clusterFenceHandle()
	if err != nil {
		// On error proceed with S3 validation, as fencing is independent of S3
		reconcileError = fmt.Errorf("failed to handle cluster fencing: %w", err)
	}

	if reason, err := validateS3Profile(ctx, r.APIReader, r.ObjectStoreGetter, drcluster, req.NamespacedName.String(),
		log); err != nil {
		return ctrl.Result{}, fmt.Errorf("drclusters s3Profile validate: %w", u.validatedSetFalseAndUpdate(reason, err))
	}

	setDRClusterValidatedCondition(&drcluster.Status.Conditions, drcluster.Generation, "Validated the cluster")

	if err := u.statusUpdate(); err != nil {
		log.Info("failed to update status", "failure", err)
	}

	return ctrl.Result{Requeue: requeue}, reconcileError
}

func (u *drclusterInstance) initializeStatus() {
	// Save a copy of the instance status to be used for the DRCluster status update comparison
	u.object.Status.DeepCopyInto(&u.savedInstanceStatus)

	if u.savedInstanceStatus.Conditions == nil {
		u.savedInstanceStatus.Conditions = []metav1.Condition{}
	}

	if u.object.Status.Conditions == nil {
		// Set the DRCluster conditions to unknown as nothing is known at this point
		msg := "Initializing DRCluster"
		setDRClusterInitialCondition(&u.object.Status.Conditions, u.object.Generation, msg)
		u.setDRClusterPhase(ramen.Starting)
	}
}

func validateS3Profile(ctx context.Context, apiReader client.Reader,
	objectStoreGetter ObjectStoreGetter,
	drcluster *ramen.DRCluster, listKeyPrefix string, log logr.Logger) (string, error) {
	if drcluster.Spec.S3ProfileName != NoS3StoreAvailable {
		if reason, err := s3ProfileValidate(ctx, apiReader, objectStoreGetter,
			drcluster.Spec.S3ProfileName, listKeyPrefix, log); err != nil {
			return reason, err
		}
	}

	return "", nil
}

func s3ProfileValidate(ctx context.Context, apiReader client.Reader,
	objectStoreGetter ObjectStoreGetter, s3ProfileName, listKeyPrefix string,
	log logr.Logger,
) (string, error) {
	objectStore, _, err := objectStoreGetter.ObjectStore(
		ctx, apiReader, s3ProfileName, "drpolicy validation", log)
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

func (r DRClusterReconciler) processDeletion(u *drclusterInstance) (ctrl.Result, error) {
	u.log.Info("delete")

	// Undeploy manifests
	if err := drClusterUndeploy(u.object, u.mwUtil); err != nil {
		return ctrl.Result{}, fmt.Errorf("drclusters undeploy: %w", err)
	}

	if u.object.Spec.ClusterFence == ramen.ClusterFenceStateFenced ||
		u.object.Spec.ClusterFence == ramen.ClusterFenceStateUnfenced {
		requeue, err := u.handleDeletion()
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("cleanup update: %w", err)
		}

		if requeue {
			return ctrl.Result{Requeue: true}, nil
		}
	}

	if err := u.finalizerRemove(); err != nil {
		return ctrl.Result{}, fmt.Errorf("finalizer remove update: %w", err)
	}

	return ctrl.Result{}, nil
}

type drclusterInstance struct {
	ctx                 context.Context
	object              *ramen.DRCluster
	client              client.Client
	log                 logr.Logger
	reconciler          *DRClusterReconciler
	savedInstanceStatus ramen.DRClusterStatus
	mwUtil              *util.MWUtil
}

func (u *drclusterInstance) validatedSetFalseAndUpdate(reason string, err error) error {
	if err1 := u.statusConditionSetAndUpdate(ramen.DRClusterValidated,
		metav1.ConditionFalse, reason, err.Error()); err1 != nil {
		return err1
	}

	return err
}

func (u *drclusterInstance) statusConditionSetAndUpdate(
	conditionType string,
	status metav1.ConditionStatus,
	reason, message string,
) error {
	conditions := &u.object.Status.Conditions

	util.GenericStatusConditionSet(u.object, conditions, conditionType, status, reason, message, u.log)

	return u.statusUpdate()
}

func (u *drclusterInstance) statusUpdate() error {
	if !reflect.DeepEqual(u.savedInstanceStatus, u.object.Status) {
		if err := u.client.Status().Update(u.ctx, u.object); err != nil {
			u.log.Info(fmt.Sprintf("Failed to update drCluster status (%s/%s/%v)",
				u.object.Name, u.object.Namespace, err))

			return fmt.Errorf("failed to update drCluster status (%s/%s)", u.object.Name, u.object.Namespace)
		}

		u.log.Info(fmt.Sprintf("Updated drCluster Status %+v", u.object.Status))

		return nil
	}

	u.log.Info(fmt.Sprintf("Nothing to update %+v", u.object.Status))

	return nil
}

const drClusterFinalizerName = "drclusters.ramendr.openshift.io/ramen"

func (u *drclusterInstance) addLabelsAndFinalizers() error {
	return util.GenericAddLabelsAndFinalizers(u.ctx, u.object, drClusterFinalizerName, u.client, u.log)
}

func (u *drclusterInstance) finalizerRemove() error {
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
func (u *drclusterInstance) clusterFenceHandle() (bool, error) {
	switch u.object.Spec.ClusterFence {
	case ramen.ClusterFenceStateUnfenced:
		return u.clusterUnfence()

	case ramen.ClusterFenceStateManuallyFenced:
		setDRClusterFencedCondition(&u.object.Status.Conditions, u.object.Generation, "Cluster Manually fenced")
		u.setDRClusterPhase(ramen.Fenced)
		// no requeue is needed and no error as this is a manual fence
		return false, nil

	case ramen.ClusterFenceStateManuallyUnfenced:
		setDRClusterCleanCondition(&u.object.Status.Conditions, u.object.Generation,
			"Cluster Manually Unfenced and clean")
		u.setDRClusterPhase(ramen.Unfenced)
		// no requeue is needed and no error as this is a manual unfence
		return false, nil

	case ramen.ClusterFenceStateFenced:
		return u.clusterFence()

	default:
		// This is needed when a DRCluster is created fresh without any fencing related information.
		// That is cluster being clean without any NetworkFence CR. Or is it? What if someone just
		// edits the resource and removes the entire line that has fencing state? Should that be
		// treated as cluster being clean or unfence?
		setDRClusterCleanCondition(&u.object.Status.Conditions, u.object.Generation, "Cluster Clean")
		u.setDRClusterPhase(ramen.Available)

		return false, nil
	}
}

func (u *drclusterInstance) handleDeletion() (bool, error) {
	drpolicies, err := util.GetAllDRPolicies(u.ctx, u.reconciler.APIReader)
	if err != nil {
		return true, fmt.Errorf("getting all drpolicies failed: %w", err)
	}

	peerCluster, err := getPeerCluster(u.ctx, drpolicies, u.reconciler, u.object, u.log)
	if err != nil {
		return true, fmt.Errorf("failed to get the peer cluster for the cluster %s: %w",
			u.object.Name, err)
	}

	return u.cleanClusters([]ramen.DRCluster{*u.object, peerCluster})
}

func (u *drclusterInstance) clusterFence() (bool, error) {
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

func (u *drclusterInstance) clusterUnfence() (bool, error) {
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
	return u.cleanClusters([]ramen.DRCluster{*u.object, peerCluster})
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
func (u *drclusterInstance) fenceClusterOnCluster(peerCluster *ramen.DRCluster) (bool, error) {
	if !u.isFencingOrFenced() {
		u.log.Info(fmt.Sprintf("initiating the cluster fence from the cluster %s", peerCluster.Name))

		if err := u.createNFManifestWork(u.object, peerCluster, u.log); err != nil {
			setDRClusterFencingFailedCondition(&u.object.Status.Conditions, u.object.Generation,
				fmt.Sprintf("NetworkFence ManifestWork creation failed: %v", err))

			u.log.Info(fmt.Sprintf("Failed to generate NetworkFence MW on cluster %s to unfence %s",
				peerCluster.Name, u.object.Name))

			return true, fmt.Errorf("failed to create the NetworkFence MW on cluster %s to fence %s: %w",
				peerCluster.Name, u.object.Name, err)
		}

		setDRClusterFencingCondition(&u.object.Status.Conditions, u.object.Generation,
			"ManifestWork for NetworkFence fence operation created")
		u.setDRClusterPhase(ramen.Fencing)
		// just created fencing resource. Requeue and then check.
		return true, nil
	}

	annotations := make(map[string]string)
	annotations[DRPCNameAnnotation] = u.object.Name

	nf, err := u.reconciler.MCVGetter.GetNFFromManagedCluster(u.object.Name,
		u.object.Namespace, peerCluster.Name, annotations)
	if err != nil {
		// dont update the status or conditions. Return requeue, nil as
		// this indicates that NetworkFence resource might have been not yet
		// created in the manged cluster or MCV for it might not have been
		// created yet. This assumption is because, drCluster does not delete
		// the NetworkFence resource as part of fencing.
		return true, fmt.Errorf("failed to get NetworkFence using MCV (error: %w)", err)
	}

	if nf.Status.Result != csiaddonsv1alpha1.FencingOperationResultSucceeded {
		setDRClusterFencingFailedCondition(&u.object.Status.Conditions, u.object.Generation,
			"fencing operation not successful")

		u.log.Info("Fencing operation not successful", "cluster", u.object.Name)

		return true, fmt.Errorf("fencing operation result not successful")
	}

	setDRClusterFencedCondition(&u.object.Status.Conditions, u.object.Generation,
		"Cluster successfully fenced")
	u.advanceToNextPhase()

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
func (u *drclusterInstance) unfenceClusterOnCluster(peerCluster *ramen.DRCluster) (bool, error) {
	if !u.isUnfencingOrUnfenced() {
		u.log.Info(fmt.Sprintf("initiating the cluster unfence from the cluster %s", peerCluster.Name))

		if err := u.createNFManifestWork(u.object, peerCluster, u.log); err != nil {
			setDRClusterUnfencingFailedCondition(&u.object.Status.Conditions, u.object.Generation,
				"NeworkFence ManifestWork for unfence failed")

			u.log.Info(fmt.Sprintf("Failed to generate NetworkFence MW on cluster %s to unfence %s",
				peerCluster.Name, u.object.Name))

			return true, fmt.Errorf("failed to generate NetworkFence MW on cluster %s to unfence %s",
				peerCluster.Name, u.object.Name)
		}

		setDRClusterUnfencingCondition(&u.object.Status.Conditions, u.object.Generation,
			"ManifestWork for NetworkFence unfence operation created")
		u.setDRClusterPhase(ramen.Unfencing)

		// just created NetworkFence resource to unfence. Requeue and then check.
		return true, nil
	}

	annotations := make(map[string]string)
	annotations[DRClusterNameAnnotation] = u.object.Name

	nf, err := u.reconciler.MCVGetter.GetNFFromManagedCluster(u.object.Name,
		u.object.Namespace, peerCluster.Name, annotations)
	if err != nil {
		if errors.IsNotFound(err) {
			return u.requeueIfNFMWExists(peerCluster)
		}

		return true, fmt.Errorf("failed to get NetworkFence using MCV (error: %w", err)
	}

	if nf.Status.Result != csiaddonsv1alpha1.FencingOperationResultSucceeded {
		setDRClusterUnfencingFailedCondition(&u.object.Status.Conditions, u.object.Generation,
			"unfencing operation not successful")

		u.log.Info("Unfencing operation not successful", "cluster", u.object.Name)

		return true, fmt.Errorf("un operation result not successful")
	}

	setDRClusterUnfencedCondition(&u.object.Status.Conditions, u.object.Generation,
		"Cluster successfully unfenced")
	u.advanceToNextPhase()

	return false, nil
}

func (u *drclusterInstance) requeueIfNFMWExists(peerCluster *ramen.DRCluster) (bool, error) {
	nfMWName := u.mwUtil.BuildManifestWorkName(util.MWTypeNF)

	_, mwErr := u.mwUtil.FindManifestWork(nfMWName, peerCluster.Name)
	if mwErr != nil {
		if errors.IsNotFound(mwErr) {
			u.log.Info("NetworkFence and MW for it not found. Cleaned")

			return false, nil
		}

		return true, fmt.Errorf("failed to get MW for NetworkFence %w", mwErr)
	}

	return true, fmt.Errorf("NetworkFence not found. But MW still exists")
}

// We are here means following things have been confirmed.
// 1) Fencing CR MCV was obtained.
// 2) MCV for the Fencing CR showed the cluster as unfenced
//
// * Proceed to delete the ManifestWork for the fencingCR
// * Issue a requeue
func (u *drclusterInstance) cleanClusters(clusters []ramen.DRCluster) (bool, error) {
	u.log.Info("initiating the removal of NetworkFence resource ")

	needRequeue := false
	cleanedCount := 0

	for _, cluster := range clusters {
		requeue, err := u.removeFencingCR(cluster)
		// Can just error alone be checked?
		if err != nil {
			needRequeue = true
		} else {
			if !requeue {
				cleanedCount++
			} else {
				needRequeue = true
			}
		}
	}

	switch cleanedCount {
	case len(clusters):
		setDRClusterCleanCondition(&u.object.Status.Conditions, u.object.Generation, "fencing resource cleaned from cluster")
	default:
		setDRClusterCleaningCondition(&u.object.Status.Conditions, u.object.Generation, "NetworkFence resource clean started")
	}

	return needRequeue, nil
}

func (u *drclusterInstance) removeFencingCR(cluster ramen.DRCluster) (bool, error) {
	u.log.Info(fmt.Sprintf("cleaning the cluster fence resource from the cluster %s", cluster.Name))

	err := u.mwUtil.DeleteManifestWork(fmt.Sprintf(util.ManifestWorkNameFormat,
		u.object.Name, cluster.Name, util.MWTypeNF), cluster.Name)
	if err != nil {
		if errors.IsNotFound(err) {
			return u.ensureNetworkFenceDeleted(cluster.Name)
		}

		return true, fmt.Errorf("failed to delete NetworkFence resource from cluster %s", cluster.Name)
	}

	setDRClusterCleaningCondition(&u.object.Status.Conditions, u.object.Generation, "NetworkFence resource clean started")

	// Since ManifestWork for the fencing CR delete request
	// has been just issued, requeue is needed to ensure that
	// the fencing CR has indeed been deleted from the cluster.
	return true, nil
}

func (u *drclusterInstance) ensureNetworkFenceDeleted(clusterName string) (bool, error) {
	annotations := make(map[string]string)
	annotations[DRClusterNameAnnotation] = u.object.Name

	if _, err := u.reconciler.MCVGetter.GetNFFromManagedCluster(u.object.Name,
		u.object.Namespace, clusterName, annotations); err != nil {
		if errors.IsNotFound(err) {
			return u.deleteNFMCV(clusterName)
		}

		return true, fmt.Errorf("failed to get MCV for NetworkFence on %s (%w)", clusterName, err)
	}

	// We are here means, successfully NetworkFence MCV is obtained. Hence
	// we need to wait for NetworkFence deletion to complete. Requeue.
	return true, nil
}

func (u *drclusterInstance) deleteNFMCV(clusterName string) (bool, error) {
	mcvNameNF := util.BuildManagedClusterViewName(u.object.Name, u.object.Namespace, util.MWTypeNF)

	err := u.reconciler.MCVGetter.DeleteNFManagedClusterView(u.object.Name, u.object.Namespace, clusterName, mcvNameNF)
	if err != nil {
		u.log.Info("Failed to delete the MCV for NetworkFence")

		return true, fmt.Errorf("failed to delete MCV for NetworkFence %w", err)
	}

	u.log.Info("Deleted the MCV for NetworkFence")
	// successfully deleted the MCV for NetworkFence. No need to requeue
	return false, nil
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
	setStatusConditionIfNotFound(conditions, metav1.Condition{
		Type:               ramen.DRClusterValidated,
		Reason:             DRClusterConditionReasonInitializing,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionUnknown,
		Message:            message,
	})
	setStatusConditionIfNotFound(conditions, metav1.Condition{
		Type:               ramen.DRClusterConditionTypeFenced,
		Reason:             DRClusterConditionReasonInitializing,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionUnknown,
		Message:            message,
	})
	setStatusConditionIfNotFound(conditions, metav1.Condition{
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
// unfence = true, fence = false, clean = false
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
// unfence = true, fence = false, clean = true
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
// unfence = true, fence = false, clean = true
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
//nolint:deadcode,unused
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

func (u *drclusterInstance) createNFManifestWork(targetCluster *ramen.DRCluster, peerCluster *ramen.DRCluster,
	log logr.Logger) error {
	// create NetworkFence ManifestWork
	log.Info(fmt.Sprintf("Creating NetworkFence ManifestWork on cluster %s to perform fencing op on cluster %s",
		peerCluster.Name, targetCluster.Name))

	nf, err := generateNF(targetCluster)
	if err != nil {
		return fmt.Errorf("failed to generate network fence resource: %w", err)
	}

	annotations := make(map[string]string)
	annotations[DRClusterNameAnnotation] = u.object.Name

	if err := u.mwUtil.CreateOrUpdateNFManifestWork(
		u.object.Name, u.object.Namespace,
		peerCluster.Name, nf, annotations); err != nil {
		log.Error(err, "failed to create or update NetworkFence manifest")

		return fmt.Errorf("failed to create or update NetworkFence manifest in cluster %s to fence off cluster %s (%w)",
			peerCluster.Name, targetCluster.Name, err)
	}

	return nil
}

// this function fills the storage specific details in the NetworkFence resource.
// Currently it fills those details based on the annotations that are set on the
// DRCluster resource. However, in future it can be changed to get the storage
// specific details (such as driver, parameters, secret etc) from the status of
// the DRCluster resource.
func fillStorageDetails(cluster *ramen.DRCluster, nf *csiaddonsv1alpha1.NetworkFence) error {
	storageDriver, ok := cluster.Annotations[StorageAnnotationDriver]
	if !ok {
		return fmt.Errorf("failed to find storage driver in annotations")
	}

	storageSecretName, ok := cluster.Annotations[StorageAnnotationSecretName]
	if !ok {
		return fmt.Errorf("failed to find storage secret name in annotations")
	}

	storageSecretNamespace, ok := cluster.Annotations[StorageAnnotationSecretNamespace]
	if !ok {
		return fmt.Errorf("failed to find storage secret namespace in annotations")
	}

	clusterID, ok := cluster.Annotations[StorageAnnotationClusterID]
	if !ok {
		return fmt.Errorf("failed to find storage cluster id in annotations")
	}

	parameters := map[string]string{"clusterID": clusterID}

	nf.Spec.Secret.Name = storageSecretName
	nf.Spec.Secret.Namespace = storageSecretNamespace
	nf.Spec.Driver = storageDriver
	nf.Spec.Parameters = parameters

	return nil
}

func generateNF(targetCluster *ramen.DRCluster) (csiaddonsv1alpha1.NetworkFence, error) {
	if len(targetCluster.Spec.CIDRs) == 0 {
		return csiaddonsv1alpha1.NetworkFence{}, fmt.Errorf("CIDRs has no values")
	}

	// To ensure deterministic naming of the fencing CR, the resource name
	// is generated as
	// "network-fence" + name of the cluster being fenced
	resourceName := "network-fence-" + targetCluster.Name

	nf := csiaddonsv1alpha1.NetworkFence{
		// TODO: There is no way currently to get the information such as
		// the storage CSI driver, secrets and storage specific parameters
		// until DRCluster is capable of exporting such information in its
		// status based on its reconciliation. Until then, the fencing CR
		// would be incomplete and incapable of performing fencing operation.
		TypeMeta:   metav1.TypeMeta{Kind: "NetworkFence", APIVersion: "csiaddons.openshift.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: resourceName},
		Spec: csiaddonsv1alpha1.NetworkFenceSpec{
			FenceState: csiaddonsv1alpha1.FenceState(targetCluster.Spec.ClusterFence),
			Cidrs:      targetCluster.Spec.CIDRs,
		},
	}

	if err := fillStorageDetails(targetCluster, &nf); err != nil {
		return nf, fmt.Errorf("failed to create network fence resource with storage detai: %w", err)
	}

	return nf, nil
}

//nolint:exhaustive
func (u *drclusterInstance) isFencingOrFenced() bool {
	switch u.getLastDRClusterPhase() {
	case ramen.Fencing:
		fallthrough
	case ramen.Fenced:
		return true
	}

	return false
}

//nolint:exhaustive
func (u *drclusterInstance) isUnfencingOrUnfenced() bool {
	switch u.getLastDRClusterPhase() {
	case ramen.Unfencing:
		fallthrough
	case ramen.Unfenced:
		return true
	}

	return false
}

func (u *drclusterInstance) getLastDRClusterPhase() ramen.DRClusterPhase {
	return u.object.Status.Phase
}

func (u *drclusterInstance) setDRClusterPhase(nextPhase ramen.DRClusterPhase) {
	if u.object.Status.Phase != nextPhase {
		u.log.Info(fmt.Sprintf("Phase: Current '%s'. Next '%s'",
			u.object.Status.Phase, nextPhase))

		u.object.Status.Phase = nextPhase
	}
}

func (u *drclusterInstance) advanceToNextPhase() {
	lastPhase := u.getLastDRClusterPhase()
	nextPhase := lastPhase

	switch lastPhase {
	case ramen.Fencing:
		nextPhase = ramen.Fenced
	case ramen.Unfencing:
		nextPhase = ramen.Unfenced
	case ramen.Available:
	case ramen.Fenced:
	case ramen.Unfenced:
	case ramen.Starting:
	}

	u.setDRClusterPhase(nextPhase)
}
