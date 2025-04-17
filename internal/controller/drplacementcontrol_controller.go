// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"golang.org/x/exp/slices"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	recipecore "github.com/ramendr/ramen/internal/controller/core"
	plrv1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	argocdv1alpha1hack "github.com/ramendr/ramen/internal/controller/argocd"
	rmnutil "github.com/ramendr/ramen/internal/controller/util"
	"github.com/ramendr/ramen/internal/controller/volsync"
	clrapiv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
)

const (
	// DRPC CR finalizer
	DRPCFinalizer string = "drpc.ramendr.openshift.io/finalizer"

	// Ramen scheduler
	RamenScheduler string = "ramen"

	ClonedPlacementRuleNameFormat string = "drpc-plrule-%s-%s"

	// StatusCheckDelay is used to frequencly update the DRPC status when the reconciler is idle.
	// This is needed in order to sync up the DRPC status and the VRG status.
	StatusCheckDelay = time.Minute * 10

	// PlacementDecisionName format, prefix is the Placement name, and suffix is a PlacementDecision index
	PlacementDecisionName = "%s-decision-%d"

	// Maximum retries to create PlacementDecisionName with an increasing index in case of conflicts
	// with existing PlacementDecision resources
	MaxPlacementDecisionConflictCount = 5

	DestinationClusterAnnotationKey = "drplacementcontrol.ramendr.openshift.io/destination-cluster"

	DoNotDeletePVCAnnotation    = "drplacementcontrol.ramendr.openshift.io/do-not-delete-pvc"
	DoNotDeletePVCAnnotationVal = "true"
)

var ErrInitialWaitTimeForDRPCPlacementRule = errors.New("waiting for DRPC Placement to produces placement decision")

// ProgressCallback of function type
type ProgressCallback func(string, string)

// DRPlacementControlReconciler reconciles a DRPlacementControl object
type DRPlacementControlReconciler struct {
	client.Client
	APIReader                      client.Reader
	Log                            logr.Logger
	MCVGetter                      rmnutil.ManagedClusterViewGetter
	Scheme                         *runtime.Scheme
	Callback                       ProgressCallback
	eventRecorder                  *rmnutil.EventReporter
	savedInstanceStatus            rmn.DRPlacementControlStatus
	ObjStoreGetter                 ObjectStoreGetter
	RateLimiter                    *workqueue.TypedRateLimiter[reconcile.Request]
	numClustersQueriedSuccessfully int
}

// SetupWithManager sets up the controller with the Manager.
//
//nolint:funlen
func (r *DRPlacementControlReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return r.setupWithManagerAndAddWatchers(mgr)
}

//nolint:lll
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drplacementcontrols,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drplacementcontrols/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drplacementcontrols/finalizers,verbs=update
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drpolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps.open-cluster-management.io,resources=placementrules,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps.open-cluster-management.io,resources=placementrules/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps.open-cluster-management.io,resources=placementrules/finalizers,verbs=get;create;update;patch;delete
// +kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=managedclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=work.open-cluster-management.io,resources=manifestworks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=view.open-cluster-management.io,resources=managedclusterviews,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=placementbindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=get;create;patch;update
// +kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=placementdecisions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=placementdecisions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=placements,verbs=get;list;watch;update
// +kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=placements/finalizers,verbs=update
// +kubebuilder:rbac:groups=argoproj.io,resources=applicationsets,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the DRPlacementControl object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.7.0/pkg/reconcile
//
//nolint:funlen,gocognit,gocyclo,cyclop
func (r *DRPlacementControlReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.Log.WithValues("DRPC", req.NamespacedName, "rid", uuid.New())

	logger.Info("Entering reconcile loop")
	defer logger.Info("Exiting reconcile loop")

	drpc := &rmn.DRPlacementControl{}

	err := r.APIReader.Get(ctx, req.NamespacedName, drpc)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info(fmt.Sprintf("DRPC object not found %v", req.NamespacedName))
			// Request object not found, could have been deleted after reconcile request.
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, fmt.Errorf("failed to get DRPC object: %w", err)
	}

	// Save a copy of the instance status to be used for the VRG status update comparison
	drpc.Status.DeepCopyInto(&r.savedInstanceStatus)

	ensureDRPCConditionsInited(&drpc.Status.Conditions, drpc.Generation, "Initialization")

	_, ramenConfig, err := ConfigMapGet(ctx, r.APIReader)
	if err != nil {
		err = fmt.Errorf("failed to get the ramen configMap: %w", err)
		r.recordFailure(ctx, drpc, nil, "Error", err.Error(), logger)

		return ctrl.Result{}, err
	}

	var placementObj client.Object

	placementObj, err = getPlacementOrPlacementRule(ctx, r.Client, drpc, logger)
	if err != nil && !(k8serrors.IsNotFound(err) && rmnutil.ResourceIsDeleted(drpc)) {
		r.recordFailure(ctx, drpc, placementObj, "Error", err.Error(), logger)

		return ctrl.Result{}, err
	}

	if isBeingDeleted(drpc, placementObj) {
		// DPRC depends on User PlacementRule/Placement. If DRPC or/and the User PlacementRule is deleted,
		// then the DRPC should be deleted as well. The least we should do here is to clean up DPRC.
		err := r.processDeletion(ctx, drpc, placementObj, logger)
		if err != nil {
			logger.Info(fmt.Sprintf("Error in deleting DRPC: (%v)", err))

			statusErr := r.setDeletionStatusAndUpdate(ctx, drpc)
			if statusErr != nil {
				err = fmt.Errorf("drpc deletion failed: %w and status update failed: %w", err, statusErr)
			}

			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	err = ensureDRPCValidNamespace(drpc, ramenConfig)
	if err != nil {
		r.recordFailure(ctx, drpc, placementObj, "Error", err.Error(), logger)

		return ctrl.Result{}, err
	}

	err = r.ensureNoConflictingDRPCs(ctx, drpc, ramenConfig, logger)
	if err != nil {
		r.recordFailure(ctx, drpc, placementObj, "Error", err.Error(), logger)

		return ctrl.Result{}, err
	}

	drPolicy, err := r.getAndEnsureValidDRPolicy(ctx, drpc, logger)
	if err != nil {
		r.recordFailure(ctx, drpc, placementObj, "Error", err.Error(), logger)

		return ctrl.Result{}, err
	}

	// Updates labels, finalizers and set the placement as the owner of the DRPC
	updated, err := r.updateAndSetOwner(ctx, drpc, placementObj, logger)
	if err != nil {
		return ctrl.Result{}, err
	}

	if updated {
		// Reload before proceeding
		return ctrl.Result{Requeue: true}, nil
	}

	// Rebuild DRPC state if needed
	requeue, err := r.ensureDRPCStatusConsistency(ctx, drpc, drPolicy, placementObj, logger)
	if err != nil {
		return ctrl.Result{}, err
	}

	if requeue {
		return ctrl.Result{Requeue: true}, r.updateDRPCStatus(ctx, drpc, placementObj, logger)
	}

	d, err := r.createDRPCInstance(ctx, drPolicy, drpc, placementObj, ramenConfig, logger)
	if err != nil && !errors.Is(err, ErrInitialWaitTimeForDRPCPlacementRule) {
		err2 := r.updateDRPCStatus(ctx, drpc, placementObj, logger)

		return ctrl.Result{}, fmt.Errorf("failed to create DRPC instance (%w) and (%v)", err, err2)
	}

	if errors.Is(err, ErrInitialWaitTimeForDRPCPlacementRule) {
		const initialWaitTime = 5

		r.recordFailure(ctx, drpc, placementObj, "Waiting",
			fmt.Sprintf("%v - wait time: %v", ErrInitialWaitTimeForDRPCPlacementRule, initialWaitTime), logger)

		return ctrl.Result{RequeueAfter: time.Second * initialWaitTime}, nil
	}

	return r.reconcileDRPCInstance(d, logger)
}

func (r *DRPlacementControlReconciler) setDeletionStatusAndUpdate(
	ctx context.Context, drpc *rmn.DRPlacementControl,
) error {
	updated := updateDRPCProgression(drpc, rmn.ProgressionDeleting, r.Log)
	drpc.Status.Phase = rmn.Deleting
	drpc.Status.ObservedGeneration = drpc.Generation

	if updated {
		if err := r.Status().Update(ctx, drpc); err != nil {
			return fmt.Errorf("failed to update DRPC status: (%w)", err)
		}
	}

	return nil
}

func (r *DRPlacementControlReconciler) recordFailure(ctx context.Context, drpc *rmn.DRPlacementControl,
	placementObj client.Object, reason, msg string, log logr.Logger,
) {
	needsUpdate := addOrUpdateCondition(&drpc.Status.Conditions, rmn.ConditionAvailable,
		drpc.Generation, metav1.ConditionFalse, reason, msg)
	if needsUpdate {
		err := r.updateDRPCStatus(ctx, drpc, placementObj, log)
		if err != nil {
			log.Info(fmt.Sprintf("Failed to update DRPC status (%v)", err))
		}
	}
}

func (r *DRPlacementControlReconciler) setLastSyncTimeMetric(syncMetrics *SyncTimeMetrics,
	t *metav1.Time, log logr.Logger,
) {
	if syncMetrics == nil {
		return
	}

	log.Info(fmt.Sprintf("Setting metric: (%s)", LastSyncTimestampSeconds))

	if t == nil {
		syncMetrics.LastSyncTime.Set(0)

		return
	}

	syncMetrics.LastSyncTime.Set(float64(t.ProtoTime().Seconds))
}

func (r *DRPlacementControlReconciler) setLastSyncDurationMetric(syncDurationMetrics *SyncDurationMetrics,
	t *metav1.Duration, log logr.Logger,
) {
	if syncDurationMetrics == nil {
		return
	}

	log.Info(fmt.Sprintf("setting metric: (%s)", LastSyncDurationSeconds))

	if t == nil {
		syncDurationMetrics.LastSyncDuration.Set(0)

		return
	}

	syncDurationMetrics.LastSyncDuration.Set(t.Seconds())
}

func (r *DRPlacementControlReconciler) setLastSyncBytesMetric(syncDataBytesMetrics *SyncDataBytesMetrics,
	b *int64, log logr.Logger,
) {
	if syncDataBytesMetrics == nil {
		return
	}

	log.Info(fmt.Sprintf("setting metric: (%s)", LastSyncDataBytes))

	if b == nil {
		syncDataBytesMetrics.LastSyncDataBytes.Set(0)

		return
	}

	syncDataBytesMetrics.LastSyncDataBytes.Set(float64(*b))
}

// setWorkloadProtectionMetric sets the workload protection info metric, where 0 indicates not protected and
// 1 indicates protected
func (r *DRPlacementControlReconciler) setWorkloadProtectionMetric(workloadProtectionMetrics *WorkloadProtectionMetrics,
	conditions []metav1.Condition, log logr.Logger,
) {
	if workloadProtectionMetrics == nil {
		return
	}

	log.Info(fmt.Sprintf("setting metric: (%s)", WorkloadProtectionStatus))

	protected := 0

	for idx := range conditions {
		if conditions[idx].Type == rmn.ConditionProtected && conditions[idx].Status == metav1.ConditionTrue {
			protected = 1

			break
		}
	}

	workloadProtectionMetrics.WorkloadProtectionStatus.Set(float64(protected))
}

//nolint:funlen
func (r *DRPlacementControlReconciler) createDRPCInstance(
	ctx context.Context,
	drPolicy *rmn.DRPolicy,
	drpc *rmn.DRPlacementControl,
	placementObj client.Object,
	ramenConfig *rmn.RamenConfig,
	log logr.Logger,
) (*DRPCInstance, error) {
	log.Info("Creating DRPC instance")

	drClusters, err := GetDRClusters(ctx, r.Client, drPolicy)
	if err != nil {
		return nil, err
	}

	// We only create DRPC PlacementRule if the preferred cluster is not configured
	err = r.getDRPCPlacementRule(ctx, drpc, placementObj, drPolicy, log)
	if err != nil {
		return nil, err
	}

	vrgNamespace, err := selectVRGNamespace(r.Client, r.Log, drpc, placementObj)
	if err != nil {
		return nil, err
	}

	vrgs, cqs, _, err := getVRGsFromManagedClusters(r.MCVGetter, drpc, drClusters, vrgNamespace, log)
	if err != nil {
		return nil, err
	}

	r.numClustersQueriedSuccessfully = cqs

	d := &DRPCInstance{
		reconciler:      r,
		ctx:             ctx,
		log:             log,
		instance:        drpc,
		userPlacement:   placementObj,
		drPolicy:        drPolicy,
		drClusters:      drClusters,
		vrgs:            vrgs,
		vrgNamespace:    vrgNamespace,
		volSyncDisabled: ramenConfig.VolSync.Disabled,
		ramenConfig:     ramenConfig,
		mwu: rmnutil.MWUtil{
			Client:          r.Client,
			APIReader:       r.APIReader,
			Ctx:             ctx,
			Log:             log,
			InstName:        drpc.Name,
			TargetNamespace: vrgNamespace,
		},
	}

	d.drType = DRTypeAsync

	isMetro, _ := dRPolicySupportsMetro(drPolicy, drClusters, nil)
	if isMetro {
		d.volSyncDisabled = true
		d.drType = DRTypeSync

		log.Info("volsync is set to disabled")
	}

	if !d.volSyncDisabled && drpcInAdminNamespace(drpc, ramenConfig) {
		d.volSyncDisabled = !ramenConfig.MultiNamespace.VolsyncSupported
	}

	// Save the instance status
	d.instance.Status.DeepCopyInto(&d.savedInstanceStatus)

	return d, nil
}

func (r *DRPlacementControlReconciler) createSyncMetricsInstance(
	drPolicy *rmn.DRPolicy, drpc *rmn.DRPlacementControl,
) *SyncMetrics {
	syncTimeMetricLabels := SyncTimeMetricLabels(drPolicy, drpc)
	syncTimeMetrics := NewSyncTimeMetric(syncTimeMetricLabels)

	syncDurationMetricLabels := SyncDurationMetricLabels(drPolicy, drpc)
	syncDurationMetrics := NewSyncDurationMetric(syncDurationMetricLabels)

	syncDataBytesLabels := SyncDataBytesMetricLabels(drPolicy, drpc)
	syncDataMetrics := NewSyncDataBytesMetric(syncDataBytesLabels)

	return &SyncMetrics{
		SyncTimeMetrics:      syncTimeMetrics,
		SyncDurationMetrics:  syncDurationMetrics,
		SyncDataBytesMetrics: syncDataMetrics,
	}
}

func (r *DRPlacementControlReconciler) createWorkloadProtectionMetricsInstance(
	drpc *rmn.DRPlacementControl,
) *WorkloadProtectionMetrics {
	workloadProtectionLabels := WorkloadProtectionStatusLabels(drpc)
	workloadProtectionMetrics := NewWorkloadProtectionStatusMetric(workloadProtectionLabels)

	return &WorkloadProtectionMetrics{
		WorkloadProtectionStatus: workloadProtectionMetrics.WorkloadProtectionStatus,
	}
}

// isBeingDeleted returns true if either DRPC, user placement, or both are being deleted
func isBeingDeleted(drpc *rmn.DRPlacementControl, usrPl client.Object) bool {
	return rmnutil.ResourceIsDeleted(drpc) ||
		(usrPl != nil && rmnutil.ResourceIsDeleted(usrPl))
}

//nolint:unparam
func (r *DRPlacementControlReconciler) reconcileDRPCInstance(d *DRPCInstance, log logr.Logger) (ctrl.Result, error) {
	// Last status update time BEFORE we start processing
	var beforeProcessing metav1.Time
	if d.instance.Status.LastUpdateTime != nil {
		beforeProcessing = *d.instance.Status.LastUpdateTime
	}

	if !ensureVRGsManagedByDRPC(d.log, d.mwu, d.vrgs, d.instance, d.vrgNamespace) {
		log.Info("Requeing... VRG adoption in progress")

		return ctrl.Result{Requeue: true}, nil
	}

	requeue := d.startProcessing()
	log.Info("Finished processing", "Requeue?", requeue)

	if !requeue {
		log.Info("Done reconciling", "state", d.getLastDRState())
		r.Callback(d.instance.Name, string(d.getLastDRState()))
	}

	if d.mcvRequestInProgress && d.getLastDRState() != "" {
		duration := d.getRequeueDuration()
		log.Info(fmt.Sprintf("Requeing after %v", duration))

		return reconcile.Result{RequeueAfter: duration}, nil
	}

	if requeue {
		log.Info("Requeing...")

		return ctrl.Result{Requeue: true}, nil
	}

	// Last status update time AFTER processing
	var afterProcessing metav1.Time
	if d.instance.Status.LastUpdateTime != nil {
		afterProcessing = *d.instance.Status.LastUpdateTime
	}

	requeueTimeDuration := r.getStatusCheckDelay(beforeProcessing, afterProcessing)
	log.Info("Requeue time", "duration", requeueTimeDuration)

	return ctrl.Result{RequeueAfter: requeueTimeDuration}, nil
}

func (r *DRPlacementControlReconciler) getAndEnsureValidDRPolicy(ctx context.Context,
	drpc *rmn.DRPlacementControl, log logr.Logger,
) (*rmn.DRPolicy, error) {
	drPolicy, err := GetDRPolicy(ctx, r.Client, drpc, log)
	if err != nil {
		return nil, fmt.Errorf("failed to get DRPolicy %w", err)
	}

	if rmnutil.ResourceIsDeleted(drPolicy) {
		// If drpolicy is deleted then return
		// error to fail drpc reconciliation
		return nil, fmt.Errorf("drPolicy '%s' referred by the DRPC is deleted, DRPC reconciliation would fail",
			drpc.Spec.DRPolicyRef.Name)
	}

	if err := rmnutil.DrpolicyValidated(drPolicy); err != nil {
		return nil, fmt.Errorf("DRPolicy not valid %w", err)
	}

	return drPolicy, nil
}

func GetDRPolicy(ctx context.Context, client client.Client,
	drpc *rmn.DRPlacementControl, log logr.Logger,
) (*rmn.DRPolicy, error) {
	drPolicy := &rmn.DRPolicy{}
	name := drpc.Spec.DRPolicyRef.Name
	namespace := drpc.Spec.DRPolicyRef.Namespace

	err := client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, drPolicy)
	if err != nil {
		log.Error(err, "failed to get DRPolicy")

		return nil, fmt.Errorf("%w", err)
	}

	return drPolicy, nil
}

func GetDRClusters(ctx context.Context, client client.Client, drPolicy *rmn.DRPolicy) ([]rmn.DRCluster, error) {
	drClusters := []rmn.DRCluster{}

	for _, managedCluster := range rmnutil.DRPolicyClusterNames(drPolicy) {
		drCluster := &rmn.DRCluster{}

		err := client.Get(ctx, types.NamespacedName{Name: managedCluster}, drCluster)
		if err != nil {
			return nil, fmt.Errorf("failed to get DRCluster (%s) %w", managedCluster, err)
		}

		// TODO: What if the DRCluster is deleted? If new DRPC fail reconciliation
		drClusters = append(drClusters, *drCluster)
	}

	return drClusters, nil
}

// updateObjectMetadata updates drpc labels, annotations and finalizer, and also updates placementObj finalizer
func (r DRPlacementControlReconciler) updateObjectMetadata(ctx context.Context,
	drpc *rmn.DRPlacementControl, placementObj client.Object, log logr.Logger,
) error {
	var update bool

	update = rmnutil.AddLabel(drpc, rmnutil.OCMBackupLabelKey, rmnutil.OCMBackupLabelValue)
	update = rmnutil.AddFinalizer(drpc, DRPCFinalizer) || update

	vrgNamespace, err := selectVRGNamespace(r.Client, r.Log, drpc, placementObj)
	if err != nil {
		return err
	}

	update = rmnutil.AddAnnotation(drpc, DRPCAppNamespace, vrgNamespace) || update

	if update {
		if err := r.Update(ctx, drpc); err != nil {
			log.Error(err, "Failed to add annotations, labels, or finalizer to drpc")

			return fmt.Errorf("%w", err)
		}
	}

	// add finalizer to User PlacementRule/Placement
	finalizerAdded := rmnutil.AddFinalizer(placementObj, DRPCFinalizer)
	if finalizerAdded {
		if err := r.Update(ctx, placementObj); err != nil {
			log.Error(err, "Failed to add finalizer to user placement rule")

			return fmt.Errorf("%w", err)
		}
	}

	return nil
}

func (r *DRPlacementControlReconciler) processDeletion(ctx context.Context,
	drpc *rmn.DRPlacementControl, placementObj client.Object, log logr.Logger,
) error {
	log.Info("Processing DRPC deletion")

	if !controllerutil.ContainsFinalizer(drpc, DRPCFinalizer) {
		return nil
	}

	// Run finalization logic for dprc.
	// If the finalization logic fails, don't remove the finalizer so
	// that we can retry during the next reconciliation.
	if err := r.finalizeDRPC(ctx, drpc, placementObj, log); err != nil {
		return err
	}

	if placementObj != nil && controllerutil.ContainsFinalizer(placementObj, DRPCFinalizer) {
		if err := r.finalizePlacement(ctx, placementObj); err != nil {
			return err
		}
	}

	updateDRPCProgression(drpc, rmn.ProgressionDeleted, r.Log)
	// Remove DRPCFinalizer from DRPC.
	controllerutil.RemoveFinalizer(drpc, DRPCFinalizer)

	if err := r.Update(ctx, drpc); err != nil {
		return fmt.Errorf("failed to update drpc %w", err)
	}

	r.Callback(drpc.Name, "deleted")

	return nil
}

//nolint:funlen,cyclop
func (r *DRPlacementControlReconciler) finalizeDRPC(ctx context.Context, drpc *rmn.DRPlacementControl,
	placementObj client.Object, log logr.Logger,
) error {
	log.Info("Finalizing DRPC")

	clonedPlRuleName := fmt.Sprintf(ClonedPlacementRuleNameFormat, drpc.Name, drpc.Namespace)
	// delete cloned placementrule, if one created.
	if drpc.Spec.PreferredCluster == "" {
		if err := r.deleteClonedPlacementRule(ctx, clonedPlRuleName, drpc.Namespace, log); err != nil {
			return err
		}
	}

	vrgNamespace, err := selectVRGNamespace(r.Client, r.Log, drpc, placementObj)
	if err != nil {
		return err
	}

	mwu := rmnutil.MWUtil{
		Client:          r.Client,
		APIReader:       r.APIReader,
		Ctx:             ctx,
		Log:             r.Log,
		InstName:        drpc.Name,
		TargetNamespace: vrgNamespace,
	}

	drPolicy, err := GetDRPolicy(ctx, r.Client, drpc, log)
	if err != nil {
		return fmt.Errorf("failed to get DRPolicy while finalizing DRPC (%w)", err)
	}

	// Cleanup volsync secret-related resources (policy/plrule/binding)
	if err := volsync.CleanupSecretPropagation(ctx, r.Client, drpc, r.Log); err != nil {
		return fmt.Errorf("failed to clean up volsync secret-related resources (%w)", err)
	}

	// cleanup for VRG artifacts
	if err = r.cleanupVRGs(ctx, drPolicy, log, mwu, drpc, vrgNamespace); err != nil {
		return err
	}

	// delete namespace manifestwork
	for _, drClusterName := range rmnutil.DRPolicyClusterNames(drPolicy) {
		annotations := make(map[string]string)
		annotations[DRPCNameAnnotation] = drpc.Name
		annotations[DRPCNamespaceAnnotation] = drpc.Namespace

		if err := mwu.DeleteNamespaceManifestWork(drClusterName, annotations); err != nil {
			return err
		}
	}

	// delete metrics if matching labels are found
	syncTimeMetricLabels := SyncTimeMetricLabels(drPolicy, drpc)
	DeleteSyncTimeMetric(syncTimeMetricLabels)

	syncDurationMetricLabels := SyncDurationMetricLabels(drPolicy, drpc)
	DeleteSyncDurationMetric(syncDurationMetricLabels)

	syncDataBytesMetricLabels := SyncDataBytesMetricLabels(drPolicy, drpc)
	DeleteSyncDataBytesMetric(syncDataBytesMetricLabels)

	workloadProtectionLabels := WorkloadProtectionStatusLabels(drpc)
	DeleteWorkloadProtectionStatusMetric(workloadProtectionLabels)

	return nil
}

func (r *DRPlacementControlReconciler) cleanupVRGs(
	ctx context.Context,
	drPolicy *rmn.DRPolicy,
	log logr.Logger,
	mwu rmnutil.MWUtil,
	drpc *rmn.DRPlacementControl,
	vrgNamespace string,
) error {
	drClusters, err := GetDRClusters(ctx, r.Client, drPolicy)
	if err != nil {
		return fmt.Errorf("failed to get drclusters. Error (%w)", err)
	}

	// Verify VRGs have been deleted
	vrgs, _, _, err := getVRGsFromManagedClusters(r.MCVGetter, drpc, drClusters, vrgNamespace, log)
	if err != nil {
		return fmt.Errorf("failed to retrieve VRGs. We'll retry later. Error (%w)", err)
	}

	// We have to ensure the secondary VRG is deleted before deleting the primary VRG. This will fail until there
	// is no secondary VRG in the vrgs list.
	if err := r.ensureVRGsDeleted(mwu, vrgs, drpc, vrgNamespace, rmn.Secondary); err != nil {
		return err
	}

	// This will fail until there is no primary VRG in the vrgs list.
	if err := r.ensureVRGsDeleted(mwu, vrgs, drpc, vrgNamespace, rmn.Primary); err != nil {
		return err
	}

	if len(vrgs) != 0 {
		return fmt.Errorf("waiting for VRGs count to go to zero")
	}

	// delete MCVs
	if err := r.deleteAllManagedClusterViews(drpc, rmnutil.DRPolicyClusterNames(drPolicy)); err != nil {
		return fmt.Errorf("error in deleting MCV (%w)", err)
	}

	return nil
}

// ensureVRGsDeleted ensure that secondary or primary VRGs are deleted. Return an error if a vrg could not be deleted,
// or deletion is in progress. Return nil if vrg of specified type was not found.
func (r *DRPlacementControlReconciler) ensureVRGsDeleted(
	mwu rmnutil.MWUtil,
	vrgs map[string]*rmn.VolumeReplicationGroup,
	drpc *rmn.DRPlacementControl,
	vrgNamespace string,
	replicationState rmn.ReplicationState,
) error {
	var inProgress bool

	for cluster, vrg := range vrgs {
		if vrg.Spec.ReplicationState == replicationState {
			if !ensureVRGsManagedByDRPC(r.Log, mwu, vrgs, drpc, vrgNamespace) {
				return fmt.Errorf("%s VRG adoption in progress", replicationState)
			}

			if err := mwu.DeleteManifestWork(mwu.BuildManifestWorkName(rmnutil.MWTypeVRG), cluster); err != nil {
				return fmt.Errorf("failed to delete %s VRG manifestwork for cluster %q: %w", replicationState, cluster, err)
			}

			inProgress = true
		}
	}

	if inProgress {
		return fmt.Errorf("%s VRG manifestwork deletion in progress", replicationState)
	}

	return nil
}

func (r *DRPlacementControlReconciler) deleteAllManagedClusterViews(
	drpc *rmn.DRPlacementControl, clusterNames []string,
) error {
	// Only after the VRGs have been deleted, we delete the MCVs for the VRGs and the NS
	for _, drClusterName := range clusterNames {
		// Delete MCV for the VRG
		err := r.MCVGetter.DeleteVRGManagedClusterView(drpc.Name, drpc.Namespace, drClusterName, rmnutil.MWTypeVRG)
		if err != nil {
			return fmt.Errorf("failed to delete VRG MCV %w", err)
		}

		// Delete MCV for Namespace
		err = r.MCVGetter.DeleteNamespaceManagedClusterView(drpc.Name, drpc.Namespace, drClusterName, rmnutil.MWTypeNS)
		if err != nil {
			return fmt.Errorf("failed to delete namespace MCV %w", err)
		}
	}

	return nil
}

func (r *DRPlacementControlReconciler) getDRPCPlacementRule(ctx context.Context,
	drpc *rmn.DRPlacementControl, placementObj client.Object,
	drPolicy *rmn.DRPolicy, log logr.Logger,
) error {
	var drpcPlRule *plrv1.PlacementRule
	// create the cloned placementrule if and only if the Spec.PreferredCluster is not provided
	if drpc.Spec.PreferredCluster == "" {
		var err error

		plRule := ConvertToPlacementRule(placementObj)
		if plRule == nil {
			return fmt.Errorf("invalid user PlacementRule")
		}

		drpcPlRule, err = r.getOrClonePlacementRule(ctx, drpc, drPolicy, plRule, log)
		if err != nil {
			log.Error(err, "failed to get DRPC PlacementRule")

			return err
		}

		// Make sure that we give time to the DRPC PlacementRule to run and produces decisions
		if drpcPlRule != nil && len(drpcPlRule.Status.Decisions) == 0 {
			return ErrInitialWaitTimeForDRPCPlacementRule
		}
	} else {
		log.Info("Preferred cluster is configured. Dynamic selection is disabled",
			"PreferredCluster", drpc.Spec.PreferredCluster)
	}

	return nil
}

func (r *DRPlacementControlReconciler) finalizePlacement(
	ctx context.Context,
	placementObj client.Object,
) error {
	controllerutil.RemoveFinalizer(placementObj, DRPCFinalizer)

	err := r.Update(ctx, placementObj)
	if err != nil {
		return fmt.Errorf("failed to update User PlacementRule/Placement %w", err)
	}

	return nil
}

func (r *DRPlacementControlReconciler) updateAndSetOwner(
	ctx context.Context,
	drpc *rmn.DRPlacementControl,
	usrPlacement client.Object,
	log logr.Logger,
) (bool, error) {
	if err := r.annotateObject(ctx, drpc, usrPlacement, log); err != nil {
		return false, err
	}

	if err := r.updateObjectMetadata(ctx, drpc, usrPlacement, log); err != nil {
		return false, err
	}

	return r.setDRPCOwner(ctx, drpc, usrPlacement, log)
}

func getPlacementOrPlacementRule(
	ctx context.Context,
	k8sclient client.Client,
	drpc *rmn.DRPlacementControl,
	log logr.Logger,
) (client.Object, error) {
	log.Info("Getting user placement object", "placementRef", drpc.Spec.PlacementRef)

	var usrPlacement client.Object

	var err error

	usrPlacement, err = getPlacementRule(ctx, k8sclient, drpc, log)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// PlacementRule not found. Check Placement instead
			usrPlacement, err = getPlacement(ctx, k8sclient, drpc, log)
		}

		if err != nil {
			return nil, err
		}
	} else {
		// Assert that there is no Placement object in the same namespace and with the same name as the PlacementRule
		_, err = getPlacement(ctx, k8sclient, drpc, log)
		if err == nil {
			return nil, fmt.Errorf(
				"can't proceed. PlacementRule and Placement CR with the same name exist on the same namespace")
		}
	}

	return usrPlacement, nil
}

func getPlacementRule(ctx context.Context, k8sclient client.Client,
	drpc *rmn.DRPlacementControl, log logr.Logger,
) (*plrv1.PlacementRule, error) {
	log.Info("Trying user PlacementRule", "usrPR", drpc.Spec.PlacementRef.Name+"/"+drpc.Spec.PlacementRef.Namespace)

	plRuleNamespace := drpc.Spec.PlacementRef.Namespace
	if plRuleNamespace == "" {
		plRuleNamespace = drpc.Namespace
	}

	if plRuleNamespace != drpc.Namespace {
		return nil, fmt.Errorf("referenced PlacementRule namespace (%s)"+
			" differs from DRPlacementControl resource namespace (%s)",
			drpc.Spec.PlacementRef.Namespace, drpc.Namespace)
	}

	usrPlRule := &plrv1.PlacementRule{}

	err := k8sclient.Get(ctx,
		types.NamespacedName{Name: drpc.Spec.PlacementRef.Name, Namespace: plRuleNamespace}, usrPlRule)
	if err != nil {
		log.Info(fmt.Sprintf("Get PlacementRule returned: %v", err))

		return nil, err
	}

	// If either DRPC or PlacementRule are being deleted, disregard everything else.
	if !isBeingDeleted(drpc, usrPlRule) {
		scName := usrPlRule.Spec.SchedulerName
		if scName != RamenScheduler {
			return nil, fmt.Errorf("placementRule %s does not have the ramen scheduler. Scheduler used %s",
				usrPlRule.Name, scName)
		}

		if usrPlRule.Spec.ClusterReplicas == nil || *usrPlRule.Spec.ClusterReplicas != 1 {
			log.Info("User PlacementRule replica count is not set to 1, reconciliation will only" +
				" schedule it to a single cluster")
		}
	}

	return usrPlRule, nil
}

func getPlacement(ctx context.Context, k8sclient client.Client,
	drpc *rmn.DRPlacementControl, log logr.Logger,
) (*clrapiv1beta1.Placement, error) {
	log.Info("Trying user Placement", "usrP", drpc.Spec.PlacementRef.Name+"/"+drpc.Spec.PlacementRef.Namespace)

	plmntNamespace := drpc.Spec.PlacementRef.Namespace
	if plmntNamespace == "" {
		plmntNamespace = drpc.Namespace
	}

	if plmntNamespace != drpc.Namespace {
		return nil, fmt.Errorf("referenced Placement namespace (%s)"+
			" differs from DRPlacementControl resource namespace (%s)",
			drpc.Spec.PlacementRef.Namespace, drpc.Namespace)
	}

	usrPlmnt := &clrapiv1beta1.Placement{}

	err := k8sclient.Get(ctx,
		types.NamespacedName{Name: drpc.Spec.PlacementRef.Name, Namespace: plmntNamespace}, usrPlmnt)
	if err != nil {
		log.Info(fmt.Sprintf("Get Placement returned: %v", err))

		return nil, err
	}

	// If either DRPC or Placement are being deleted, disregard everything else.
	if !isBeingDeleted(drpc, usrPlmnt) {
		if value, ok := usrPlmnt.GetAnnotations()[clrapiv1beta1.PlacementDisableAnnotation]; !ok || value == "false" {
			return nil, fmt.Errorf("placement %s must be disabled in order for Ramen to be the scheduler",
				usrPlmnt.Name)
		}

		if usrPlmnt.Spec.NumberOfClusters == nil || *usrPlmnt.Spec.NumberOfClusters != 1 {
			log.Info("User Placement number of clusters is not set to 1, reconciliation will only" +
				" schedule it to a single cluster")
		}
	}

	return usrPlmnt, nil
}

func (r *DRPlacementControlReconciler) annotateObject(ctx context.Context,
	drpc *rmn.DRPlacementControl, obj client.Object, log logr.Logger,
) error {
	if rmnutil.ResourceIsDeleted(obj) {
		return nil
	}

	if obj.GetAnnotations() == nil {
		obj.SetAnnotations(map[string]string{})
	}

	ownerName := obj.GetAnnotations()[DRPCNameAnnotation]
	ownerNamespace := obj.GetAnnotations()[DRPCNamespaceAnnotation]

	if ownerName == "" {
		obj.GetAnnotations()[DRPCNameAnnotation] = drpc.Name
		obj.GetAnnotations()[DRPCNamespaceAnnotation] = drpc.Namespace

		err := r.Update(ctx, obj)
		if err != nil {
			log.Error(err, "Failed to update Object annotation", "objName", obj.GetName())

			return fmt.Errorf("failed to update Object %s annotation '%s/%s' (%w)",
				obj.GetName(), DRPCNameAnnotation, drpc.Name, err)
		}

		return nil
	}

	if ownerName != drpc.Name || ownerNamespace != drpc.Namespace {
		log.Info("Object not owned by this DRPC", "objName", obj.GetName())

		return fmt.Errorf("object %s not owned by this DRPC '%s/%s'",
			obj.GetName(), drpc.Name, drpc.Namespace)
	}

	return nil
}

func (r *DRPlacementControlReconciler) setDRPCOwner(
	ctx context.Context, drpc *rmn.DRPlacementControl, owner client.Object, log logr.Logger,
) (bool, error) {
	const updated = true

	for _, ownerReference := range drpc.GetOwnerReferences() {
		if ownerReference.Name == owner.GetName() {
			return !updated, nil // ownerreference already set
		}
	}

	err := ctrl.SetControllerReference(owner, drpc, r.Client.Scheme())
	if err != nil {
		return !updated, fmt.Errorf("failed to set DRPC owner %w", err)
	}

	err = r.Update(ctx, drpc)
	if err != nil {
		return !updated, fmt.Errorf("failed to update drpc %s (%w)", drpc.GetName(), err)
	}

	log.Info(fmt.Sprintf("Object %s owns DRPC %s", owner.GetName(), drpc.GetName()))

	return updated, nil
}

func (r *DRPlacementControlReconciler) getOrClonePlacementRule(ctx context.Context,
	drpc *rmn.DRPlacementControl, drPolicy *rmn.DRPolicy,
	userPlRule *plrv1.PlacementRule, log logr.Logger,
) (*plrv1.PlacementRule, error) {
	log.Info("Getting PlacementRule or cloning it", "placement", drpc.Spec.PlacementRef)

	clonedPlRuleName := fmt.Sprintf(ClonedPlacementRuleNameFormat, drpc.Name, drpc.Namespace)

	clonedPlRule, err := r.getClonedPlacementRule(ctx, clonedPlRuleName, drpc.Namespace, log)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			clonedPlRule, err = r.clonePlacementRule(ctx, drPolicy, userPlRule, clonedPlRuleName, log)
			if err != nil {
				return nil, fmt.Errorf("failed to create cloned placementrule error: %w", err)
			}
		} else {
			log.Error(err, "Failed to get drpc placementRule", "name", clonedPlRuleName)

			return nil, err
		}
	}

	return clonedPlRule, nil
}

func (r *DRPlacementControlReconciler) getClonedPlacementRule(ctx context.Context,
	clonedPlRuleName, namespace string, log logr.Logger,
) (*plrv1.PlacementRule, error) {
	log.Info("Getting cloned PlacementRule", "name", clonedPlRuleName)

	clonedPlRule := &plrv1.PlacementRule{}

	err := r.Client.Get(ctx, types.NamespacedName{Name: clonedPlRuleName, Namespace: namespace}, clonedPlRule)
	if err != nil {
		return nil, fmt.Errorf("failed to get placementrule error: %w", err)
	}

	return clonedPlRule, nil
}

func (r *DRPlacementControlReconciler) clonePlacementRule(ctx context.Context,
	drPolicy *rmn.DRPolicy, userPlRule *plrv1.PlacementRule,
	clonedPlRuleName string, log logr.Logger,
) (*plrv1.PlacementRule, error) {
	log.Info("Creating a clone placementRule from", "name", userPlRule.Name)

	clonedPlRule := &plrv1.PlacementRule{}

	recipecore.ObjectCreatedByRamenSetLabel(clonedPlRule)

	userPlRule.DeepCopyInto(clonedPlRule)

	clonedPlRule.Name = clonedPlRuleName
	clonedPlRule.ResourceVersion = ""
	clonedPlRule.Spec.SchedulerName = ""

	err := r.addClusterPeersToPlacementRule(drPolicy, clonedPlRule, log)
	if err != nil {
		log.Error(err, "Failed to add cluster peers to cloned placementRule", "name", clonedPlRuleName)

		return nil, err
	}

	err = r.Create(ctx, clonedPlRule)
	if err != nil {
		log.Error(err, "failed to clone placement rule", "name", clonedPlRule.Name)

		return nil, fmt.Errorf("failed to create PlacementRule: %w", err)
	}

	return clonedPlRule, nil
}

func getVRGsFromManagedClusters(
	mcvGetter rmnutil.ManagedClusterViewGetter,
	drpc *rmn.DRPlacementControl,
	drClusters []rmn.DRCluster,
	vrgNamespace string,
	log logr.Logger,
) (map[string]*rmn.VolumeReplicationGroup, int, string, error) {
	vrgs := map[string]*rmn.VolumeReplicationGroup{}

	annotations := make(map[string]string)
	annotations[DRPCNameAnnotation] = drpc.Name
	annotations[DRPCNamespaceAnnotation] = drpc.Namespace

	var numClustersQueriedSuccessfully int

	var failedCluster string

	for i := range drClusters {
		drCluster := &drClusters[i]

		vrg, err := mcvGetter.GetVRGFromManagedCluster(drpc.Name, vrgNamespace, drCluster.Name, annotations)
		if err != nil {
			// Only NotFound error is accepted
			if k8serrors.IsNotFound(err) {
				log.Info(fmt.Sprintf("VRG not found on %q", drCluster.Name))

				numClustersQueriedSuccessfully++

				continue
			}

			failedCluster = drCluster.Name

			log.Info(fmt.Sprintf("failed to retrieve VRG from %s. err (%v).", drCluster.Name, err))

			continue
		}

		numClustersQueriedSuccessfully++

		if rmnutil.ResourceIsDeleted(drCluster) {
			log.Info("Skipping VRG on deleted drcluster", "drcluster", drCluster.Name, "vrg", vrg.Name)

			continue
		}

		vrgs[drCluster.Name] = vrg

		log.Info("VRG location", "VRG on", drCluster.Name, "replicationState", vrg.Spec.ReplicationState)
	}

	// We are done if we successfully queried all drClusters
	if numClustersQueriedSuccessfully == len(drClusters) {
		return vrgs, numClustersQueriedSuccessfully, "", nil
	}

	if numClustersQueriedSuccessfully == 0 {
		return vrgs, 0, "", fmt.Errorf("failed to retrieve VRGs from clusters")
	}

	return vrgs, numClustersQueriedSuccessfully, failedCluster, nil
}

func (r *DRPlacementControlReconciler) deleteClonedPlacementRule(ctx context.Context,
	name, namespace string, log logr.Logger,
) error {
	plRule, err := r.getClonedPlacementRule(ctx, name, namespace, log)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}

		return err
	}

	err = r.Client.Delete(ctx, plRule)
	if err != nil {
		return fmt.Errorf("failed to delete cloned plRule %w", err)
	}

	return nil
}

func (r *DRPlacementControlReconciler) addClusterPeersToPlacementRule(
	drPolicy *rmn.DRPolicy, plRule *plrv1.PlacementRule, log logr.Logger,
) error {
	if len(rmnutil.DRPolicyClusterNames(drPolicy)) == 0 {
		return fmt.Errorf("DRPolicy %s is missing DR clusters", drPolicy.Name)
	}

	for _, v := range rmnutil.DRPolicyClusterNames(drPolicy) {
		plRule.Spec.Clusters = append(plRule.Spec.Clusters, plrv1.GenericClusterReference{Name: v})
	}

	log.Info(fmt.Sprintf("Added clusters %v to placementRule from DRPolicy %s", plRule.Spec.Clusters, drPolicy.Name))

	return nil
}

// statusUpdateTimeElapsed returns whether it is time to update DRPC status or not
// DRPC status is updated at least once every StatusCheckDelay in order to refresh
// the VRG status.
func (d *DRPCInstance) statusUpdateTimeElapsed() bool {
	if d.instance.Status.LastUpdateTime == nil {
		return false
	}

	return d.instance.Status.LastUpdateTime.Add(StatusCheckDelay).Before(time.Now())
}

// getStatusCheckDelay returns the reconciliation requeue time duration when no requeue
// has been requested. We want the reconciliation to run at least once every StatusCheckDelay
// in order to refresh DRPC status with VRG status. The reconciliation will be called at any time.
// If it is called before the StatusCheckDelay has elapsed, and the DRPC status was not updated,
// then we must return the remaining time rather than the full StatusCheckDelay to prevent
// starving the status update, which is scheduled for at least once every StatusCheckDelay.
//
// Example: Assume at 10:00am was the last time when the reconciler ran and updated the status.
// The StatusCheckDelay is hard coded to 10 minutes.  If nothing is happening in the system that
// requires the reconciler to run, then the next run would be at 10:10am. If however, for any reason
// the reconciler is called, let's say, at 10:08am, and no update to the DRPC status was needed,
// then the requeue time duration should be 2 minutes and NOT the full StatusCheckDelay. That is:
// 10:00am + StatusCheckDelay - 10:08am = 2mins
func (r *DRPlacementControlReconciler) getStatusCheckDelay(
	beforeProcessing metav1.Time, afterProcessing metav1.Time,
) time.Duration {
	if beforeProcessing != afterProcessing {
		// DRPC's VRG status update processing time has changed during this
		// iteration of the reconcile loop.  Hence, the next attempt to update
		// the status should be after a delay of a standard polling interval
		// duration.
		return StatusCheckDelay
	}

	// DRPC's VRG status update processing time has NOT changed during this
	// iteration of the reconcile loop.  Hence, the next attempt to update the
	// status should be after the remaining duration of this polling interval has
	// elapsed: (beforeProcessing + StatusCheckDelay - time.Now())
	// If the scheduled time is already in the past, requeue immediately.
	remaining := time.Until(beforeProcessing.Add(StatusCheckDelay))

	return max(0, remaining)
}

// updateDRPCStatus updates the DRPC sub-resource status with,
// - the current instance DRPC status as updated during reconcile
// - any updated VRG status as needs to be reflected in DRPC
// It also updates latest metrics for the current instance of DRPC.
//
//nolint:cyclop
func (r *DRPlacementControlReconciler) updateDRPCStatus(
	ctx context.Context, drpc *rmn.DRPlacementControl, userPlacement client.Object, log logr.Logger,
) error {
	log.Info("Updating DRPC status")

	r.updateResourceCondition(ctx, drpc, userPlacement, log)

	// set metrics if DRPC is not being deleted and if finalizer exists
	if !isBeingDeleted(drpc, userPlacement) && controllerutil.ContainsFinalizer(drpc, DRPCFinalizer) {
		if err := r.setDRPCMetrics(ctx, drpc, log); err != nil {
			// log the error but do not return the error
			log.Info("Failed to set drpc metrics", "errMSg", err)
		}
	}

	// TODO: This is too generic, why are all conditions reported for the current generation?
	// Each condition should choose for itself, no?
	for i, condition := range drpc.Status.Conditions {
		if condition.ObservedGeneration != drpc.Generation {
			drpc.Status.Conditions[i].ObservedGeneration = drpc.Generation
		}
	}

	if reflect.DeepEqual(r.savedInstanceStatus, drpc.Status) {
		log.Info("No need to update DRPC Status")

		return nil
	}

	now := metav1.Now()
	drpc.Status.LastUpdateTime = &now

	if err := r.Status().Update(ctx, drpc); err != nil {
		return fmt.Errorf("failed to update DRPC status: %w", err)
	}

	log.Info("Updated DRPC Status")

	return nil
}

// updateResourceCondition updates DRPC status sub-resource with updated status from VRG if one exists,
// - The status update is NOT intended for a VRG that should be cleaned up on a peer cluster
// It also updates DRPC ConditionProtected based on current state of VRG.
//
//nolint:funlen,cyclop
func (r *DRPlacementControlReconciler) updateResourceCondition(
	ctx context.Context, drpc *rmn.DRPlacementControl, userPlacement client.Object, log logr.Logger,
) {
	vrgNamespace, err := selectVRGNamespace(r.Client, log, drpc, userPlacement)
	if err != nil {
		log.Info("Failed to select VRG namespace", "error", err)

		return
	}

	clusterName := r.clusterForVRGStatus(drpc, userPlacement, log)
	if clusterName == "" {
		log.Info("Unable to determine managed cluster from which to inspect VRG, " +
			"skipping processing ResourceConditions")

		return
	}

	annotations := make(map[string]string)
	annotations[DRPCNameAnnotation] = drpc.Name
	annotations[DRPCNamespaceAnnotation] = drpc.Namespace

	vrg, err := r.MCVGetter.GetVRGFromManagedCluster(drpc.Name, vrgNamespace,
		clusterName, annotations)
	if err != nil {
		log.Info("Failed to get VRG from managed cluster. Trying s3 store...", "errMsg", err.Error())

		// The VRG from the s3 store might be stale, however, the worst case should be at most around 1 minute.
		vrg = GetLastKnownVRGPrimaryFromS3(ctx, r.APIReader,
			GetAvailableS3Profiles(ctx, r.Client, drpc, log),
			drpc.GetName(), vrgNamespace, r.ObjStoreGetter, log)
		if vrg == nil {
			log.Info("Failed to get VRG from s3 store")

			drpc.Status.ResourceConditions = rmn.VRGConditions{}

			updateProtectedConditionUnknown(drpc, clusterName)

			return
		}

		if vrg.ResourceVersion < drpc.Status.ResourceConditions.ResourceMeta.ResourceVersion {
			log.Info("VRG resourceVersion is lower than the previously recorded VRG's resourceVersion in DRPC")
			// if the VRG resourceVersion is less, then leave the DRPC ResourceConditions.ResourceMeta.ResourceVersion as is.
			return
		}
	}

	drpc.Status.ResourceConditions.ResourceMeta.Kind = vrg.Kind
	drpc.Status.ResourceConditions.ResourceMeta.Name = vrg.Name
	drpc.Status.ResourceConditions.ResourceMeta.Namespace = vrg.Namespace
	drpc.Status.ResourceConditions.ResourceMeta.Generation = vrg.Generation
	drpc.Status.ResourceConditions.ResourceMeta.ResourceVersion = vrg.ResourceVersion
	drpc.Status.ResourceConditions.Conditions = vrg.Status.Conditions

	protectedPVCs := []string{}
	for _, protectedPVC := range vrg.Status.ProtectedPVCs {
		protectedPVCs = append(protectedPVCs, protectedPVC.Name)
	}

	drpc.Status.ResourceConditions.ResourceMeta.ProtectedPVCs = protectedPVCs

	if rmnutil.IsCGEnabled(vrg.GetAnnotations()) {
		drpc.Status.ResourceConditions.ResourceMeta.PVCGroups = vrg.Status.PVCGroups
	}

	if vrg.Status.LastGroupSyncTime != nil || drpc.Spec.Action != rmn.ActionRelocate {
		drpc.Status.LastGroupSyncTime = vrg.Status.LastGroupSyncTime
		drpc.Status.LastGroupSyncDuration = vrg.Status.LastGroupSyncDuration
		drpc.Status.LastGroupSyncBytes = vrg.Status.LastGroupSyncBytes
	}

	if vrg.Status.KubeObjectProtection.CaptureToRecoverFrom != nil {
		drpc.Status.LastKubeObjectProtectionTime = &vrg.Status.KubeObjectProtection.CaptureToRecoverFrom.EndTime
	}

	updateDRPCProtectedCondition(drpc, vrg, clusterName)
}

// clusterForVRGStatus determines which cluster's VRG should be inspected for status updates to DRPC
func (r *DRPlacementControlReconciler) clusterForVRGStatus(
	drpc *rmn.DRPlacementControl, userPlacement client.Object, log logr.Logger,
) string {
	clusterName := ""

	clusterDecision := r.getClusterDecision(userPlacement)
	if clusterDecision != nil && clusterDecision.ClusterName != "" {
		clusterName = clusterDecision.ClusterName
	}

	switch drpc.Spec.Action {
	case rmn.ActionFailover:
		// Failover can rely on inspecting VRG from clusterDecision as it is never made nil, hence till
		// placementDecision is changed to failoverCluster, we can inspect VRG from the existing cluster
		return clusterName
	case rmn.ActionRelocate:
		if drpc.Status.ObservedGeneration != drpc.Generation {
			log.Info("DPRC observedGeneration mismatches current generation, using ClusterDecision instead",
				"Cluster", clusterName)

			return clusterName
		}

		// We will inspect VRG from the non-preferredCluster until it reports Secondary, and then switch to the
		// preferredCluster. This is done using Status.Progression for the DRPC
		if IsPreRelocateProgression(drpc.Status.Progression) {
			if value, ok := drpc.GetAnnotations()[LastAppDeploymentCluster]; ok && value != "" {
				log.Info("Using cluster from LastAppDeploymentCluster annotation", "Cluster", value)

				return value
			}

			log.Info("DPRC missing LastAppDeploymentCluster annotation, using ClusterDecision instead",
				"Cluster", clusterName)

			return clusterName
		}

		log.Info("Using DRPC preferredCluster, Relocate progression detected as switching to preferred cluster")

		return drpc.Spec.PreferredCluster
	}

	// In cases of initial deployment use VRG from the preferredCluster
	log.Info("Using DRPC preferredCluster, initial deploy detected")

	return drpc.Spec.PreferredCluster
}

func (r *DRPlacementControlReconciler) setDRPCMetrics(ctx context.Context,
	drpc *rmn.DRPlacementControl, log logr.Logger,
) error {
	log.Info("setting WorkloadProtectionMetrics")

	workloadProtectionMetrics := r.createWorkloadProtectionMetricsInstance(drpc)
	r.setWorkloadProtectionMetric(workloadProtectionMetrics, drpc.Status.Conditions, log)

	drPolicy, err := GetDRPolicy(ctx, r.Client, drpc, log)
	if err != nil {
		return fmt.Errorf("failed to get DRPolicy %w", err)
	}

	drClusters, err := GetDRClusters(ctx, r.Client, drPolicy)
	if err != nil {
		return err
	}

	// do not set sync metrics if metro-dr
	isMetro, _ := dRPolicySupportsMetro(drPolicy, drClusters, nil)
	if isMetro {
		return nil
	}

	log.Info("setting SyncMetrics")

	syncMetrics := r.createSyncMetricsInstance(drPolicy, drpc)

	if syncMetrics != nil {
		r.setLastSyncTimeMetric(&syncMetrics.SyncTimeMetrics, drpc.Status.LastGroupSyncTime, log)
		r.setLastSyncDurationMetric(&syncMetrics.SyncDurationMetrics, drpc.Status.LastGroupSyncDuration, log)
		r.setLastSyncBytesMetric(&syncMetrics.SyncDataBytesMetrics, drpc.Status.LastGroupSyncBytes, log)
	}

	return nil
}

func ConvertToPlacementRule(placementObj interface{}) *plrv1.PlacementRule {
	var pr *plrv1.PlacementRule

	if obj, ok := placementObj.(*plrv1.PlacementRule); ok {
		pr = obj
	}

	return pr
}

func ConvertToPlacement(placementObj interface{}) *clrapiv1beta1.Placement {
	var p *clrapiv1beta1.Placement

	if obj, ok := placementObj.(*clrapiv1beta1.Placement); ok {
		p = obj
	}

	return p
}

func (r *DRPlacementControlReconciler) getClusterDecision(placementObj interface{},
) *clrapiv1beta1.ClusterDecision {
	switch obj := placementObj.(type) {
	case *plrv1.PlacementRule:
		return r.getClusterDecisionFromPlacementRule(obj)
	case *clrapiv1beta1.Placement:
		return r.getClusterDecisionFromPlacement(obj)
	default:
		return &clrapiv1beta1.ClusterDecision{}
	}
}

func (r *DRPlacementControlReconciler) getClusterDecisionFromPlacementRule(plRule *plrv1.PlacementRule,
) *clrapiv1beta1.ClusterDecision {
	var clusterName string
	if len(plRule.Status.Decisions) > 0 {
		clusterName = plRule.Status.Decisions[0].ClusterName
	}

	return &clrapiv1beta1.ClusterDecision{
		ClusterName: clusterName,
		Reason:      "PlacementRule decision",
	}
}

// getPlacementDecisionFromPlacement returns a PlacementDecision for the passed in Placement if found, and nil otherwise
// - The PlacementDecision is determined by listing all PlacementDecisions in the Placement namespace filtered on the
// Placement label as set by OCM
// - Function also ensures there is only one decision for a Placement, as the needed by the Ramen orchestrators, and
// if not returns an error
func (r *DRPlacementControlReconciler) getPlacementDecisionFromPlacement(placement *clrapiv1beta1.Placement,
) (*clrapiv1beta1.PlacementDecision, error) {
	matchLabels := map[string]string{
		clrapiv1beta1.PlacementLabel: placement.GetName(),
	}

	listOptions := []client.ListOption{
		client.InNamespace(placement.GetNamespace()),
		client.MatchingLabels(matchLabels),
	}

	plDecisions := &clrapiv1beta1.PlacementDecisionList{}
	if err := r.List(context.TODO(), plDecisions, listOptions...); err != nil {
		return nil, fmt.Errorf("failed to list PlacementDecisions (placement: %s)",
			placement.GetNamespace()+"/"+placement.GetName())
	}

	if len(plDecisions.Items) == 0 {
		return nil, nil
	}

	if len(plDecisions.Items) > 1 {
		return nil, fmt.Errorf("multiple PlacementDecisions found for Placement (count: %d, placement: %s)",
			len(plDecisions.Items), placement.GetNamespace()+"/"+placement.GetName())
	}

	plDecision := plDecisions.Items[0]
	r.Log.Info("Found ClusterDecision", "ClsDedicision", plDecision.Status.Decisions)

	if len(plDecision.Status.Decisions) > 1 {
		return nil, fmt.Errorf("multiple placements found in PlacementDecision"+
			" (count: %d, Placement: %s, PlacementDecision: %s)",
			len(plDecision.Status.Decisions),
			placement.GetNamespace()+"/"+placement.GetName(),
			plDecision.GetName()+"/"+plDecision.GetNamespace())
	}

	return &plDecision, nil
}

// getClusterDecisionFromPlacement returns the cluster decision for a given Placement if found
func (r *DRPlacementControlReconciler) getClusterDecisionFromPlacement(placement *clrapiv1beta1.Placement,
) *clrapiv1beta1.ClusterDecision {
	plDecision, err := r.getPlacementDecisionFromPlacement(placement)
	if err != nil {
		// TODO: err ignored by this caller
		r.Log.Info("failed to get placement decision", "error", err)

		return &clrapiv1beta1.ClusterDecision{}
	}

	if plDecision == nil || len(plDecision.Status.Decisions) == 0 {
		return &clrapiv1beta1.ClusterDecision{}
	}

	return &plDecision.Status.Decisions[0]
}

func (r *DRPlacementControlReconciler) updateUserPlacementStatusDecision(ctx context.Context,
	userPlacement interface{}, newCD *clrapiv1beta1.ClusterDecision,
) error {
	switch obj := userPlacement.(type) {
	case *plrv1.PlacementRule:
		return r.createOrUpdatePlacementRuleDecision(ctx, obj, newCD)
	case *clrapiv1beta1.Placement:
		return r.createOrUpdatePlacementDecision(ctx, obj, newCD)
	default:
		return fmt.Errorf("failed to find Placement or PlacementRule")
	}
}

func (r *DRPlacementControlReconciler) createOrUpdatePlacementRuleDecision(ctx context.Context,
	plRule *plrv1.PlacementRule, newCD *clrapiv1beta1.ClusterDecision,
) error {
	newStatus := plrv1.PlacementRuleStatus{}

	if newCD != nil {
		newStatus = plrv1.PlacementRuleStatus{
			Decisions: []plrv1.PlacementDecision{
				{
					ClusterName:      newCD.ClusterName,
					ClusterNamespace: newCD.ClusterName,
				},
			},
		}
	}

	if !reflect.DeepEqual(newStatus, plRule.Status) {
		plRule.Status = newStatus
		if err := r.Status().Update(ctx, plRule); err != nil {
			r.Log.Error(err, "failed to update user PlacementRule")

			return fmt.Errorf("failed to update userPlRule %s (%w)", plRule.GetName(), err)
		}

		r.Log.Info("Updated user PlacementRule status", "Decisions", plRule.Status.Decisions)
	}

	return nil
}

// createOrUpdatePlacementDecision updates the PlacementDecision status for the given Placement with the passed
// in new decision. If an existing PlacementDecision is not found, ad new Placement decision is created.
func (r *DRPlacementControlReconciler) createOrUpdatePlacementDecision(ctx context.Context,
	placement *clrapiv1beta1.Placement, newCD *clrapiv1beta1.ClusterDecision,
) error {
	plDecision, err := r.getPlacementDecisionFromPlacement(placement)
	if err != nil {
		return err
	}

	if plDecision == nil {
		if plDecision, err = r.createPlacementDecision(ctx, placement); err != nil {
			return err
		}
	} else if plDecision.GetLabels()[rmnutil.ExcludeFromVeleroBackup] != "true" {
		err = rmnutil.NewResourceUpdater(plDecision).
			AddLabel(rmnutil.ExcludeFromVeleroBackup, "true").
			Update(ctx, r.Client)
		if err != nil {
			return err
		}
	}

	plDecision.Status = clrapiv1beta1.PlacementDecisionStatus{
		Decisions: []clrapiv1beta1.ClusterDecision{},
	}

	if newCD != nil {
		plDecision.Status = clrapiv1beta1.PlacementDecisionStatus{
			Decisions: []clrapiv1beta1.ClusterDecision{
				{
					ClusterName: newCD.ClusterName,
					Reason:      newCD.Reason,
				},
			},
		}
	}

	if err := r.Status().Update(ctx, plDecision); err != nil {
		return fmt.Errorf("failed to update placementDecision status (%w)", err)
	}

	r.Log.Info("Created/Updated PlacementDecision", "PlacementDecision", plDecision.Status.Decisions)

	return nil
}

// createPlacementDecision creates a new PlacementDecision for the given Placement. The PlacementDecision is
// named in a predetermined format, and is searchable using the Placement name label against the PlacementDecision.
// On conflicts with existing PlacementDecisions, the function retries, with limits, with different names to generate
// a new PlacementDecision.
func (r *DRPlacementControlReconciler) createPlacementDecision(ctx context.Context,
	placement *clrapiv1beta1.Placement,
) (*clrapiv1beta1.PlacementDecision, error) {
	index := 1

	plDecision := &clrapiv1beta1.PlacementDecision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf(PlacementDecisionName, placement.GetName(), index),
			Namespace: placement.Namespace,
		},
	}

	// Set the Placement object to be the owner.  When it is deleted, the PlacementDecision is deleted
	err := ctrl.SetControllerReference(placement, plDecision, r.Client.Scheme())
	if err != nil {
		return nil, fmt.Errorf("failed to set controller reference %w", err)
	}

	plDecision.ObjectMeta.Labels = map[string]string{
		clrapiv1beta1.PlacementLabel:    placement.GetName(),
		rmnutil.ExcludeFromVeleroBackup: "true",
	}

	recipecore.ObjectCreatedByRamenSetLabel(plDecision)

	owner := metav1.NewControllerRef(placement, clrapiv1beta1.GroupVersion.WithKind("Placement"))
	plDecision.ObjectMeta.OwnerReferences = []metav1.OwnerReference{*owner}

	for index <= MaxPlacementDecisionConflictCount {
		if err = r.Create(ctx, plDecision); err == nil {
			return plDecision, nil
		}

		if !k8serrors.IsAlreadyExists(err) {
			return nil, err
		}

		index++

		plDecision.ObjectMeta.Name = fmt.Sprintf(PlacementDecisionName, placement.GetName(), index)
	}

	return nil, fmt.Errorf("multiple PlacementDecision conflicts found, unable to create a new"+
		" PlacementDecision for Placement %s", placement.GetNamespace()+"/"+placement.GetName())
}

func getApplicationDestinationNamespace(
	client client.Client,
	log logr.Logger,
	placement client.Object,
) (string, error) {
	appSetList := argocdv1alpha1hack.ApplicationSetList{}
	if err := client.List(context.TODO(), &appSetList); err != nil {
		// If ApplicationSet CRD is not found in the API server,
		// default to Subscription behavior, and return the placement namespace as the target VRG namespace
		if meta.IsNoMatchError(err) {
			return placement.GetNamespace(), nil
		}

		return "", fmt.Errorf("ApplicationSet list: %w", err)
	}

	log.Info("Retrieved ApplicationSets", "count", len(appSetList.Items))
	//
	// TODO: change the following loop to use an index field on AppSet instead for faster lookup
	//
	for i := range appSetList.Items {
		appSet := &appSetList.Items[i]
		if len(appSet.Spec.Generators) > 0 &&
			appSet.Spec.Generators[0].ClusterDecisionResource != nil {
			name := appSet.Spec.Generators[0].ClusterDecisionResource.LabelSelector.MatchLabels[clrapiv1beta1.PlacementLabel]
			if name == placement.GetName() {
				log.Info("Found ApplicationSet for Placement", "name", appSet.Name, "placement", placement.GetName())
				// Retrieving the Destination.Namespace from Application.Spec requires iterating through all Applications
				// and checking their ownerReferences, which can be time-consuming. Alternatively, we can get the same
				// information from the ApplicationSet spec template section as it is done here.
				return appSet.Spec.Template.Spec.Destination.Namespace, nil
			}
		}
	}

	log.Info(fmt.Sprintf("Placement %s does not belong to any ApplicationSet. Defaulting the dest namespace to %s",
		placement.GetName(), placement.GetNamespace()))

	// Didn't find any ApplicationSet using this Placement. Assuming it is for Subscription.
	// Returning its own namespace as the default namespace
	return placement.GetNamespace(), nil
}

func selectVRGNamespace(
	client client.Client,
	log logr.Logger,
	drpc *rmn.DRPlacementControl,
	placementObj client.Object,
) (string, error) {
	if drpc.GetAnnotations() != nil && drpc.GetAnnotations()[DRPCAppNamespace] != "" {
		return drpc.GetAnnotations()[DRPCAppNamespace], nil
	}

	switch placementObj.(type) {
	case *clrapiv1beta1.Placement:
		vrgNamespace, err := getApplicationDestinationNamespace(client, log, placementObj)
		if err != nil {
			return "", err
		}

		return vrgNamespace, nil
	default:
		return drpc.Namespace, nil
	}
}

func addOrUpdateCondition(conditions *[]metav1.Condition, conditionType string,
	observedGeneration int64, status metav1.ConditionStatus, reason, msg string,
) bool {
	newCondition := metav1.Condition{
		Type:               conditionType,
		Status:             status,
		ObservedGeneration: observedGeneration,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            msg,
	}

	existingCondition := rmnutil.FindCondition(*conditions, conditionType)
	if existingCondition == nil ||
		existingCondition.Status != newCondition.Status ||
		existingCondition.ObservedGeneration != newCondition.ObservedGeneration ||
		existingCondition.Reason != newCondition.Reason ||
		existingCondition.Message != newCondition.Message {
		rmnutil.SetStatusCondition(conditions, newCondition)

		return true
	}

	return false
}

// Initial creation of the DRPC status condition. This will also preserve the ordering of conditions in the array
func ensureDRPCConditionsInited(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	time := metav1.NewTime(time.Now())

	rmnutil.SetStatusConditionIfNotFound(conditions, metav1.Condition{
		Type:               rmn.ConditionAvailable,
		Reason:             string(rmn.Initiating),
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionTrue,
		LastTransitionTime: time,
		Message:            message,
	})
	rmnutil.SetStatusConditionIfNotFound(conditions, metav1.Condition{
		Type:               rmn.ConditionPeerReady,
		Reason:             string(rmn.Initiating),
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionTrue,
		LastTransitionTime: time,
		Message:            message,
	})
	rmnutil.SetStatusConditionIfNotFound(conditions, metav1.Condition{
		Type:               rmn.ConditionProtected,
		Reason:             string(rmn.ReasonProtectedUnknown),
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		LastTransitionTime: time,
		Message:            message,
	})
}

func GetAvailableS3Profiles(ctx context.Context, client client.Client,
	drpc *rmn.DRPlacementControl, log logr.Logger,
) []string {
	drPolicy, err := GetDRPolicy(ctx, client, drpc, log)
	if err != nil {
		log.Info("Failed to get DRPolicy", "err", err)

		return []string{}
	}

	drClusters, err := GetDRClusters(ctx, client, drPolicy)
	if err != nil {
		log.Info("Failed to get DRClusters", "err", err)

		return []string{}
	}

	return AvailableS3Profiles(drClusters)
}

func AvailableS3Profiles(drClusters []rmn.DRCluster) []string {
	profiles := sets.New[string]()

	for i := range drClusters {
		drCluster := &drClusters[i]
		if rmnutil.ResourceIsDeleted(drCluster) {
			continue
		}

		profiles.Insert(drCluster.Spec.S3ProfileName)
	}

	return sets.List(profiles)
}

type Progress int

const (
	Continue      = 1
	AllowFailover = 2
	Stop          = 3
)

func (r *DRPlacementControlReconciler) ensureDRPCStatusConsistency(
	ctx context.Context,
	drpc *rmn.DRPlacementControl,
	drPolicy *rmn.DRPolicy,
	placementObj client.Object,
	log logr.Logger,
) (bool, error) {
	requeue := true

	log.Info("Ensure DRPC Status Consistency")

	// This will always be false the first time the DRPC resource is first created OR after hub recovery
	if drpc.Status.Phase != "" && drpc.Status.Phase != rmn.WaitForUser {
		return !requeue, nil
	}

	dstCluster := drpc.Spec.PreferredCluster
	if drpc.Spec.Action == rmn.ActionFailover {
		dstCluster = drpc.Spec.FailoverCluster
	}

	progress, msg, err := r.determineDRPCState(ctx, drpc, drPolicy, placementObj, dstCluster, log)

	log.Info(msg)

	if err != nil {
		return requeue, err
	}

	switch progress {
	case Continue:
		return !requeue, nil
	case AllowFailover:
		drpc.Status.Phase = rmn.WaitForUser
		drpc.Status.ObservedGeneration = drpc.Generation
		updateDRPCProgression(drpc, rmn.ProgressionActionPaused, log)
		addOrUpdateCondition(&drpc.Status.Conditions, rmn.ConditionAvailable,
			drpc.Generation, metav1.ConditionTrue, rmn.ReasonSuccess, msg)
		addOrUpdateCondition(&drpc.Status.Conditions, rmn.ConditionPeerReady, drpc.Generation,
			metav1.ConditionTrue, rmn.ReasonSuccess, "Failover allowed")

		return requeue, nil
	default:
		msg := fmt.Sprintf("Operation Paused - User Intervention Required. %s", msg)

		log.Info(msg)
		updateDRPCProgression(drpc, rmn.ProgressionActionPaused, log)
		addOrUpdateCondition(&drpc.Status.Conditions, rmn.ConditionAvailable,
			drpc.Generation, metav1.ConditionFalse, rmn.ReasonPaused, msg)
		addOrUpdateCondition(&drpc.Status.Conditions, rmn.ConditionPeerReady, drpc.Generation,
			metav1.ConditionFalse, rmn.ReasonPaused, "User Intervention Required")

		return requeue, nil
	}
}

// determineDRPCState runs the following algorithm
// 1. Stop Condition for Both Failed Queries:
//    If attempts to query 2 clusters result in failure for both, the process is halted.

// 2. Initial Deployment without VRGs:
//    If 2 clusters are successfully queried, and no VRGs are found, proceed with the
//    initial deployment.

// 3. Handling Failures with S3 Store Check:
//    - If 2 clusters are queried, 1 fails, and 0 VRGs are found, perform the following checks:
//       - If the VRG is found in the S3 store, ensure that the DRPC action matches the VRG action.
//       If not, stop until the action is corrected, allowing failover if necessary (set PeerReady).
//       - If the VRG is not found in the S3 store and the failed cluster is not the destination
//       cluster, continue with the initial deployment.

// 4. Verification and Failover for VRGs on Failover Cluster:
//    If 2 clusters are queried, 1 fails, and 1 VRG is found on the failover cluster, check
//    the action:
//       - If the actions don't match, stop until corrected by the user.
//       - If they match, also stop but allow failover if the VRG in-hand is a secondary.
//       Otherwise, continue.

// 5. Handling VRGs on Destination Cluster:
//    If 2 clusters are queried successfully and 1 or more VRGs are found, and one of the
//    VRGs is on the destination cluster, perform the following checks:
//       - Continue with the action only if the DRPC and the found VRG action match.
//       - Stop until someone investigates if there is a mismatch, but allow failover to
//       take place (set PeerReady).

//  6. Otherwise, default to allowing Failover:
//     If none of the above conditions apply, allow failover (set PeerReady) but stop until
//     someone makes the necessary change.
//
//nolint:funlen,nestif,gocognit,gocyclo,cyclop
func (r *DRPlacementControlReconciler) determineDRPCState(
	ctx context.Context,
	drpc *rmn.DRPlacementControl,
	drPolicy *rmn.DRPolicy,
	placementObj client.Object,
	dstCluster string,
	log logr.Logger,
) (Progress, string, error) {
	log.Info("Rebuild DRPC state")

	vrgNamespace, err := selectVRGNamespace(r.Client, log, drpc, placementObj)
	if err != nil {
		log.Info("Failed to select VRG namespace")

		return Stop, "", err
	}

	drClusters, err := GetDRClusters(ctx, r.Client, drPolicy)
	if err != nil {
		return Stop, "", err
	}

	vrgs, successfullyQueriedClusterCount, failedCluster, err := getVRGsFromManagedClusters(
		r.MCVGetter, drpc, drClusters, vrgNamespace, log)
	if err != nil {
		log.Info("Failed to get a list of VRGs")

		return Stop, "", err
	}

	mwu := rmnutil.MWUtil{
		Client:          r.Client,
		APIReader:       r.APIReader,
		Ctx:             ctx,
		Log:             log,
		InstName:        drpc.Name,
		TargetNamespace: vrgNamespace,
	}

	if !ensureVRGsManagedByDRPC(log, mwu, vrgs, drpc, vrgNamespace) {
		msg := "VRG adoption in progress"

		return Stop, msg, nil
	}

	// IF 2 clusters queried, and both queries failed, then STOP
	if successfullyQueriedClusterCount == 0 {
		msg := "Stop - Number of clusters queried is 0"

		return Stop, msg, nil
	}

	// IF 2 clusters queried successfully and no VRGs, then continue with initial deployment
	if successfullyQueriedClusterCount == 2 && len(vrgs) == 0 {
		log.Info("Queried 2 clusters successfully")

		return Continue, "", nil
	}

	if drpc.Status.Phase == rmn.WaitForUser &&
		drpc.Spec.Action == rmn.ActionFailover &&
		drpc.Spec.FailoverCluster != failedCluster {
		log.Info("Continue. The action is failover and the failoverCluster is accessible")

		return Continue, "", nil
	}

	// IF queried 2 clusters queried, 1 failed and 0 VRG found, then check s3 store.
	// IF the VRG found in the s3 store, ensure that the DRPC action and the VRG action match. IF not, stop until
	// the action is corrected, but allow failover to take place if needed (set PeerReady)
	// If the VRG is not found in the s3 store and the failedCluster is not the destination cluster, then continue
	// with initial deploy
	if successfullyQueriedClusterCount == 1 && len(vrgs) == 0 {
		vrg := GetLastKnownVRGPrimaryFromS3(ctx, r.APIReader,
			AvailableS3Profiles(drClusters), drpc.GetName(), vrgNamespace, r.ObjStoreGetter, log)
		if vrg == nil {
			// IF the failed cluster is not the dest cluster, then this could be an initial deploy
			if failedCluster != dstCluster {
				return Continue, "", nil
			}

			msg := fmt.Sprintf("Unable to query all clusters and failed to get VRG from s3 store. Failed to query %s",
				failedCluster)

			return Stop, msg, nil
		}

		log.Info("Got VRG From s3", "VRG Spec", vrg.Spec, "VRG Annotations", vrg.GetAnnotations())

		if drpc.Spec.Action != rmn.DRAction(vrg.Spec.Action) {
			msg := fmt.Sprintf("Failover is allowed - Two different actions - drpcAction is '%s' and vrgAction from s3 is '%s'",
				drpc.Spec.Action, vrg.Spec.Action)

			return AllowFailover, msg, nil
		}

		if dstCluster == vrg.GetAnnotations()[DestinationClusterAnnotationKey] &&
			dstCluster != failedCluster {
			log.Info(fmt.Sprintf("VRG from s3. Same dstCluster %s/%s. Proceeding...",
				dstCluster, vrg.GetAnnotations()[DestinationClusterAnnotationKey]))

			return Continue, "", nil
		}

		msg := fmt.Sprintf("Failover is allowed - drpcAction:'%s'. vrgAction:'%s'. DRPCDstClstr:'%s'. vrgDstClstr:'%s'.",
			drpc.Spec.Action, vrg.Spec.Action, dstCluster, vrg.GetAnnotations()[DestinationClusterAnnotationKey])

		return AllowFailover, msg, nil
	}

	// IF 2 clusters queried, 1 failed and 1 VRG found on the failover cluster, then check the action, if they don't
	// match, stop until corrected by the user. If they do match, then also stop but allow failover if the VRG in-hand
	// is a secondary. Otherwise, continue...
	if successfullyQueriedClusterCount == 1 && len(vrgs) == 1 {
		var clusterName string

		var vrg *rmn.VolumeReplicationGroup
		for k, v := range vrgs {
			clusterName, vrg = k, v

			break
		}

		// Post-HubRecovery, if the retrieved VRG from the surviving cluster is secondary, it wrongly halts
		// reconciliation for the workload. Only proceed if the retrieved VRG is primary.
		if vrg.Spec.ReplicationState == rmn.Primary &&
			drpc.Spec.Action != rmn.DRAction(vrg.Spec.Action) &&
			dstCluster == clusterName {
			msg := fmt.Sprintf("Stop - Two different actions for the same cluster - drpcAction:'%s'. vrgAction:'%s'",
				drpc.Spec.Action, vrg.Spec.Action)

			return Stop, msg, nil
		}

		if dstCluster != clusterName && vrg.Spec.ReplicationState == rmn.Secondary {
			log.Info(fmt.Sprintf("Failover is allowed. Action/dstCluster/ReplicationState %s/%s/%s",
				drpc.Spec.Action, dstCluster, vrg.Spec.ReplicationState))

			msg := "Failover is allowed - Primary is assumed to be on the failed cluster"

			return AllowFailover, msg, nil
		}

		log.Info("Same action, dstCluster, and ReplicationState is primary. Continuing")

		return Continue, "", nil
	}

	// Finally, IF 2 clusters queried successfully and 1 or more VRGs found, and if one of the VRGs is on the dstCluster,
	// then continue with action if and only if DRPC and the found VRG action match. otherwise, stop until someone
	// investigates but allow failover to take place (set PeerReady)
	if successfullyQueriedClusterCount == 2 && len(vrgs) >= 1 {
		var clusterName string

		var vrg *rmn.VolumeReplicationGroup

		for k, v := range vrgs {
			clusterName, vrg = k, v
			if vrg.Spec.ReplicationState == rmn.Primary {
				break
			}
		}

		// This can happen if a hub is recovered in the middle of a Relocate
		if vrg.Spec.ReplicationState == rmn.Secondary && len(vrgs) == 2 {
			msg := "Stop - Both VRGs have the same secondary state"

			return Stop, msg, nil
		}

		if drpc.Spec.Action == rmn.DRAction(vrg.Spec.Action) && dstCluster == clusterName {
			log.Info(fmt.Sprintf("Same Action and dest cluster %s/%s", drpc.Spec.Action, dstCluster))

			return Continue, "", nil
		}

		msg := fmt.Sprintf("Failover is allowed - VRGs count:'%d'. drpcAction:'%s'."+
			" vrgAction:'%s'. DstCluster:'%s'. vrgOnCluster '%s'",
			len(vrgs), drpc.Spec.Action, vrg.Spec.Action, dstCluster, clusterName)

		return AllowFailover, msg, nil
	}

	// IF none of the above, then allow failover (set PeerReady), but stop until someone makes the change
	msg := "Failover is allowed - User intervention is required"

	return AllowFailover, msg, nil
}

// ensureVRGsManagedByDRPC ensures that VRGs reported by ManagedClusterView are managed by the current instance of
// DRPC. This is done using the DRPC UID annotation on the viewed VRG matching the current DRPC UID and if not
// creating or updating the existing ManifestWork for the VRG.
// Returns a bool indicating true if VRGs are managed by the current DRPC resource
func ensureVRGsManagedByDRPC(
	log logr.Logger,
	mwu rmnutil.MWUtil,
	vrgs map[string]*rmn.VolumeReplicationGroup,
	drpc *rmn.DRPlacementControl,
	vrgNamespace string,
) bool {
	ensured := true

	for cluster, viewVRG := range vrgs {
		if rmnutil.ResourceIsDeleted(viewVRG) {
			log.Info("VRG reported by view undergoing deletion, during adoption",
				"cluster", cluster, "namespace", viewVRG.Namespace, "name", viewVRG.Name)

			continue
		}

		if viewVRG.GetAnnotations() != nil {
			if v, ok := viewVRG.Annotations[DRPCUIDAnnotation]; ok && v == string(drpc.UID) {
				continue
			}
		}

		adopted := adoptVRG(log, mwu, viewVRG, cluster, drpc, vrgNamespace)

		ensured = ensured && adopted
	}

	return ensured
}

// adoptVRG creates or updates the VRG ManifestWork to ensure that the current DRPC is managing the VRG resource
// Returns a bool indicating if adoption was completed (which is mostly false except when VRG MW is deleted)
func adoptVRG(
	log logr.Logger,
	mwu rmnutil.MWUtil,
	viewVRG *rmn.VolumeReplicationGroup,
	cluster string,
	drpc *rmn.DRPlacementControl,
	vrgNamespace string,
) bool {
	adopted := true

	mw, err := mwu.FindManifestWorkByType(rmnutil.MWTypeVRG, cluster)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			log.Info("error fetching VRG ManifestWork during adoption", "error", err, "cluster", cluster)

			return !adopted
		}

		adoptOrphanVRG(log, mwu, viewVRG, cluster, drpc, vrgNamespace)

		return !adopted
	}

	if rmnutil.ResourceIsDeleted(mw) {
		log.Info("VRG ManifestWork found deleted during adoption", "cluster", cluster)

		return adopted
	}

	vrg, err := rmnutil.ExtractVRGFromManifestWork(mw)
	if err != nil {
		log.Info("error extracting VRG from ManifestWork during adoption", "error", err, "cluster", cluster)

		return !adopted
	}

	// NOTE: upgrade use case, to add DRPC UID for existing VRG MW
	adoptExistingVRGManifestWork(log, mwu, vrg, cluster, drpc, vrgNamespace)

	return !adopted
}

// adoptExistingVRGManifestWork updates an existing VRG ManifestWork as managed by the current DRPC resource
func adoptExistingVRGManifestWork(
	log logr.Logger,
	mwu rmnutil.MWUtil,
	vrg *rmn.VolumeReplicationGroup,
	cluster string,
	drpc *rmn.DRPlacementControl,
	vrgNamespace string,
) {
	log.Info("adopting existing VRG ManifestWork", "cluster", cluster, "namespace", vrg.Namespace, "name", vrg.Name)

	if vrg.GetAnnotations() == nil {
		vrg.Annotations = make(map[string]string)
	}

	if v, ok := vrg.Annotations[DRPCUIDAnnotation]; ok && v == string(drpc.UID) {
		// Annotation may already be set but not reflected on the resource view yet
		log.Info("detected VRGs DRPC UID annotation as existing",
			"cluster", cluster, "namespace", vrg.Namespace, "name", vrg.Name)

		return
	}

	vrg.Annotations[DRPCUIDAnnotation] = string(drpc.UID)

	annotations := make(map[string]string)
	annotations[DRPCNameAnnotation] = drpc.Name
	annotations[DRPCNamespaceAnnotation] = drpc.Namespace

	_, err := mwu.CreateOrUpdateVRGManifestWork(drpc.Name, vrgNamespace, cluster, *vrg, annotations)
	if err != nil {
		log.Info("error updating VRG via ManifestWork during adoption", "error", err, "cluster", cluster)
	}
}

// adoptOpphanVRG creates a missing ManifestWork for a VRG found via a ManagedClusterView
func adoptOrphanVRG(
	log logr.Logger,
	mwu rmnutil.MWUtil,
	viewVRG *rmn.VolumeReplicationGroup,
	cluster string,
	drpc *rmn.DRPlacementControl,
	vrgNamespace string,
) {
	log.Info("adopting orphaned VRG ManifestWork",
		"cluster", cluster, "namespace", viewVRG.Namespace, "name", viewVRG.Name)

	annotations := make(map[string]string)
	annotations[DRPCNameAnnotation] = drpc.Name
	annotations[DRPCNamespaceAnnotation] = drpc.Namespace

	// Adopt the namespace as well
	err := mwu.CreateOrUpdateNamespaceManifest(drpc.Name, vrgNamespace, cluster, annotations)
	if err != nil {
		log.Info("error creating namespace via ManifestWork during adoption", "error", err, "cluster", cluster)

		return
	}

	vrg := constructVRGFromView(viewVRG)
	if vrg.GetAnnotations() == nil {
		vrg.Annotations = make(map[string]string)
	}

	vrg.Annotations[DRPCUIDAnnotation] = string(drpc.UID)

	if _, err := mwu.CreateOrUpdateVRGManifestWork(
		drpc.Name, vrgNamespace,
		cluster, *vrg, annotations); err != nil {
		log.Info("error creating VRG via ManifestWork during adoption", "error", err, "cluster", cluster)
	}
}

// constructVRGFromView selectively constructs a VRG from a view, using its spec and only those annotations that
// would be set by the hub on the ManifestWork
func constructVRGFromView(viewVRG *rmn.VolumeReplicationGroup) *rmn.VolumeReplicationGroup {
	vrg := &rmn.VolumeReplicationGroup{
		TypeMeta: metav1.TypeMeta{Kind: "VolumeReplicationGroup", APIVersion: "ramendr.openshift.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      viewVRG.Name,
			Namespace: viewVRG.Namespace,
		},
	}

	viewVRG.Spec.DeepCopyInto(&vrg.Spec)

	for k, v := range viewVRG.GetAnnotations() {
		switch k {
		case DestinationClusterAnnotationKey:
			fallthrough
		case DoNotDeletePVCAnnotation:
			fallthrough
		case rmnutil.IsCGEnabledAnnotation:
			fallthrough
		case rmnutil.UseVolSyncAnnotation:
			fallthrough
		case DRPCUIDAnnotation:
			rmnutil.AddAnnotation(vrg, k, v)
		default:
		}
	}

	return vrg
}

func ensureDRPCValidNamespace(drpc *rmn.DRPlacementControl, ramenConfig *rmn.RamenConfig) error {
	if drpcInAdminNamespace(drpc, ramenConfig) {
		if !ramenConfig.MultiNamespace.FeatureEnabled {
			return fmt.Errorf("drpc cannot be in admin namespace when multinamespace feature is disabled")
		}

		if drpc.Spec.ProtectedNamespaces == nil || len(*drpc.Spec.ProtectedNamespaces) == 0 {
			return fmt.Errorf("drpc in admin namespace must have protected namespaces")
		}

		adminNamespace := drpcAdminNamespaceName(*ramenConfig)
		if slices.Contains(*drpc.Spec.ProtectedNamespaces, adminNamespace) {
			return fmt.Errorf("admin namespace cannot be a protected namespace, admin namespace: %s", adminNamespace)
		}

		return nil
	}

	if isDiscoveredApp(drpc) {
		adminNamespace := drpcAdminNamespaceName(*ramenConfig)

		return fmt.Errorf("drpc in non-admin namespace(%v) cannot have protected namespaces, admin-namespaces: %v",
			drpc.Namespace, adminNamespace)
	}

	return nil
}

func drpcsProtectCommonNamespace(drpcProtectedNs []string, otherDRPCProtectedNs []string) bool {
	for _, ns := range drpcProtectedNs {
		if slices.Contains(otherDRPCProtectedNs, ns) {
			return true
		}
	}

	return false
}

func (r *DRPlacementControlReconciler) getProtectedNamespaces(drpc *rmn.DRPlacementControl,
	log logr.Logger,
) ([]string, error) {
	if isDiscoveredApp(drpc) {
		return *drpc.Spec.ProtectedNamespaces, nil
	}

	placementObj, err := getPlacementOrPlacementRule(context.TODO(), r.Client, drpc, log)
	if err != nil {
		return []string{}, err
	}

	vrgNamespace, err := selectVRGNamespace(r.Client, log, drpc, placementObj)
	if err != nil {
		return []string{}, err
	}

	return []string{vrgNamespace}, nil
}

func (r *DRPlacementControlReconciler) ensureNoConflictingDRPCs(ctx context.Context,
	drpc *rmn.DRPlacementControl, ramenConfig *rmn.RamenConfig, log logr.Logger,
) error {
	drpcList := &rmn.DRPlacementControlList{}
	if err := r.Client.List(ctx, drpcList); err != nil {
		return fmt.Errorf("failed to list DRPlacementControls (%w)", err)
	}

	for i := range drpcList.Items {
		otherDRPC := &drpcList.Items[i]

		// Skip the drpc itself
		if otherDRPC.Name == drpc.Name && otherDRPC.Namespace == drpc.Namespace {
			continue
		}

		if err := r.twoDRPCsConflict(ctx, drpc, otherDRPC, ramenConfig, log); err != nil {
			return err
		}
	}

	return nil
}

func (r *DRPlacementControlReconciler) twoDRPCsConflict(ctx context.Context,
	drpc *rmn.DRPlacementControl, otherDRPC *rmn.DRPlacementControl, ramenConfig *rmn.RamenConfig, log logr.Logger,
) error {
	drpcIsInAdminNamespace := drpcInAdminNamespace(drpc, ramenConfig)
	otherDRPCIsInAdminNamespace := drpcInAdminNamespace(otherDRPC, ramenConfig)

	// we don't check for conflicts between drpcs in non-admin namespace
	if !drpcIsInAdminNamespace && !otherDRPCIsInAdminNamespace {
		return nil
	}

	// If the drpcs don't have common clusters, they definitely don't conflict
	common, err := r.drpcHaveCommonClusters(ctx, drpc, otherDRPC, log)
	if err != nil {
		return fmt.Errorf("failed to check if drpcs have common clusters (%w)", err)
	}

	if !common {
		return nil
	}

	drpcProtectedNamespaces, err := r.getProtectedNamespaces(drpc, log)
	if err != nil {
		return fmt.Errorf("failed to get protected namespaces for drpc: %v, %w", drpc.Name, err)
	}

	otherDRPCProtectedNamespaces, err := r.getProtectedNamespaces(otherDRPC, log)
	if err != nil {
		return fmt.Errorf("failed to get protected namespaces for drpc: %v, %w", otherDRPC.Name, err)
	}

	independentVMProtection := drpcProtectVMInNS(drpc, otherDRPC, ramenConfig)
	if independentVMProtection {
		return nil
	}

	conflict := drpcsProtectCommonNamespace(drpcProtectedNamespaces, otherDRPCProtectedNamespaces)
	if conflict {
		return fmt.Errorf("drpc: %s and drpc: %s protect common resources from the same namespace",
			drpc.Name, otherDRPC.Name)
	}

	return nil
}

func drpcInAdminNamespace(drpc *rmn.DRPlacementControl, ramenConfig *rmn.RamenConfig) bool {
	adminNamespace := drpcAdminNamespaceName(*ramenConfig)

	return adminNamespace == drpc.Namespace
}

func (r *DRPlacementControlReconciler) drpcHaveCommonClusters(ctx context.Context,
	drpc, otherDRPC *rmn.DRPlacementControl, log logr.Logger,
) (bool, error) {
	drpolicy, err := GetDRPolicy(ctx, r.Client, drpc, log)
	if err != nil {
		return false, fmt.Errorf("failed to get DRPolicy %w", err)
	}

	otherDrpolicy, err := GetDRPolicy(ctx, r.Client, otherDRPC, log)
	if err != nil {
		return false, fmt.Errorf("failed to get DRPolicy %w", err)
	}

	drpolicyClusters := rmnutil.DRPolicyClusterNamesAsASet(drpolicy)
	otherDrpolicyClusters := rmnutil.DRPolicyClusterNamesAsASet(otherDrpolicy)

	return drpolicyClusters.Intersection(otherDrpolicyClusters).Len() > 0, nil
}

func drpcProtectVMInNS(drpc *rmn.DRPlacementControl, otherdrpc *rmn.DRPlacementControl,
	ramenConfig *rmn.RamenConfig,
) bool {
	if (drpc.Spec.KubeObjectProtection == nil || drpc.Spec.KubeObjectProtection.RecipeRef == nil) ||
		(otherdrpc.Spec.KubeObjectProtection == nil || otherdrpc.Spec.KubeObjectProtection.RecipeRef == nil) {
		return false
	}

	drpcRecipeName := drpc.Spec.KubeObjectProtection.RecipeRef.Name
	otherDrpcRecipeName := otherdrpc.Spec.KubeObjectProtection.RecipeRef.Name

	// Both the DRPCs are associated with vm-recipe, and protecting VM resources.
	// Support for protecting independent VMs
	if drpcRecipeName == recipecore.VMRecipeName && otherDrpcRecipeName == recipecore.VMRecipeName {
		ramenOpsNS := RamenOperandsNamespace(*ramenConfig)

		if drpc.Spec.KubeObjectProtection.RecipeRef.Namespace == ramenOpsNS &&
			otherdrpc.Spec.KubeObjectProtection.RecipeRef.Namespace == ramenOpsNS {
			return !twoVMDRPCsConflict(drpc, otherdrpc)
		}
	}

	return false
}

func twoVMDRPCsConflict(drpc *rmn.DRPlacementControl, otherdrpc *rmn.DRPlacementControl) bool {
	drpcVMList := sets.NewString(drpc.Spec.KubeObjectProtection.RecipeParameters["PROTECTED_VMS"]...)
	otherdrpcVMList := sets.NewString(otherdrpc.Spec.KubeObjectProtection.RecipeParameters["PROTECTED_VMS"]...)

	conflict := drpcVMList.Intersection(otherdrpcVMList)

	if len(conflict) == 0 {
		return false
	}

	// Mark the latest drpc as unavailable if conflicting resources found

	if (drpc.Status.ObservedGeneration == 0) ||
		(drpc.Status.ObservedGeneration > 0 && otherdrpc.Status.ObservedGeneration > 0) {
		return true
	}

	return false
}
