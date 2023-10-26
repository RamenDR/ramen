// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/exp/slices"
	"golang.org/x/time/rate"

	volrep "github.com/csi-addons/kubernetes-csi-addons/apis/replication.storage/v1alpha1"
	"github.com/google/uuid"
	"github.com/ramendr/ramen/controllers/kubeobjects"
	"github.com/ramendr/ramen/controllers/kubeobjects/velero"
	recipe "github.com/ramendr/recipe/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	rmnutil "github.com/ramendr/ramen/controllers/util"
	"github.com/ramendr/ramen/controllers/volsync"
)

// VolumeReplicationGroupReconciler reconciles a VolumeReplicationGroup object
type VolumeReplicationGroupReconciler struct {
	client.Client
	APIReader      client.Reader
	Log            logr.Logger
	ObjStoreGetter ObjectStoreGetter
	Scheme         *runtime.Scheme
	eventRecorder  *rmnutil.EventReporter
	kubeObjects    kubeobjects.RequestsManager
}

// SetupWithManager sets up the controller with the Manager.
func (r *VolumeReplicationGroupReconciler) SetupWithManager(
	mgr ctrl.Manager, ramenConfig *ramendrv1alpha1.RamenConfig,
) error {
	r.eventRecorder = rmnutil.NewEventReporter(mgr.GetEventRecorderFor("controller_VolumeReplicationGroup"))

	r.Log.Info("Adding VolumeReplicationGroup controller")

	rateLimiter := workqueue.NewMaxOfRateLimiter(
		workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 1*time.Minute),
		// defaults from client-go
		//nolint: gomnd
		&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(10), 100)},
	)

	builder := ctrl.NewControllerManagedBy(mgr).
		WithOptions(ctrlcontroller.Options{
			MaxConcurrentReconciles: getMaxConcurrentReconciles(r.Log),
			RateLimiter:             rateLimiter,
		}).
		For(&ramendrv1alpha1.VolumeReplicationGroup{},
			builder.WithPredicates(
				predicate.Or(
					predicate.GenerationChangedPredicate{},
					predicate.AnnotationChangedPredicate{},
					predicate.LabelChangedPredicate{},
				),
			),
		).
		Watches(&source.Kind{Type: &corev1.PersistentVolumeClaim{}},
			handler.EnqueueRequestsFromMapFunc(r.pvcMapFunc),
			builder.WithPredicates(pvcPredicateFunc()),
		).
		Watches(&source.Kind{Type: &corev1.ConfigMap{}}, handler.EnqueueRequestsFromMapFunc(r.configMapFun)).
		Owns(&volrep.VolumeReplication{})

	if !ramenConfig.VolSync.Disabled {
		builder.Owns(&volsyncv1alpha1.ReplicationDestination{}).
			Owns(&volsyncv1alpha1.ReplicationSource{})
	} else {
		r.Log.Info("VolSync disabled; don't own volsync resources")
	}

	r.kubeObjects = velero.RequestsManager{}
	if !ramenConfig.KubeObjectProtection.Disabled {
		r.Log.Info("Kube object protection enabled; watch kube objects requests")
		kubeObjectsRequestsWatch(builder, r.kubeObjects)
	} else {
		r.Log.Info("Kube object protection disabled; don't watch kube objects requests")
	}

	return builder.Complete(r)
}

func (r *VolumeReplicationGroupReconciler) pvcMapFunc(obj client.Object) []reconcile.Request {
	log := ctrl.Log.WithName("pvcmap").WithName("VolumeReplicationGroup")

	pvc, ok := obj.(*corev1.PersistentVolumeClaim)
	if !ok {
		log.Info("PersistentVolumeClaim(PVC) map function received non-PVC resource")

		return []reconcile.Request{}
	}

	return filterPVC(r.Client, pvc,
		log.WithValues("pvc", types.NamespacedName{Name: pvc.Name, Namespace: pvc.Namespace}))
}

func (r *VolumeReplicationGroupReconciler) configMapFun(configmap client.Object) []reconcile.Request {
	log := ctrl.Log.WithName("configmap").WithName("VolumeReplicationGroup")

	if configmap.GetName() != drClusterOperatorConfigMapName || configmap.GetNamespace() != NamespaceName() {
		return []reconcile.Request{}
	}

	log.Info("Update in ramen-dr-cluster-operator-config configuration map")

	req := []reconcile.Request{}

	var vrgs ramendrv1alpha1.VolumeReplicationGroupList
	if err := r.Client.List(context.TODO(), &vrgs); err != nil {
		return []reconcile.Request{}
	}

	for _, vrg := range vrgs.Items {
		log.Info("Adding VolumeReplicationGroup to reconcile request",
			"vrg", vrg.Name, "namespace", vrg.Namespace)

		req = append(req, reconcile.Request{NamespacedName: types.NamespacedName{Name: vrg.Name, Namespace: vrg.Namespace}})
	}

	return req
}

func init() {
	// Register custom metrics with the global Prometheus registry here
}

// pvcPredicateFunc sends reconcile requests for create and delete events.
// For them the filtering of whether the pvc belongs to the any of the
// VolumeReplicationGroup CRs and identifying such a CR is done in the
// map function by comparing namespaces and labels.
// But for update of pvc, the reconcile request should be sent only for
// specific changes. Do that comparison here.
func pvcPredicateFunc() predicate.Funcs {
	pvcPredicate := predicate.Funcs{
		// NOTE: Create predicate is retained, to help with logging the event
		CreateFunc: func(e event.CreateEvent) bool {
			log := ctrl.Log.WithName("pvcmap").WithName("VolumeReplicationGroup")

			log.Info("Create event for PersistentVolumeClaim")

			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			log := ctrl.Log.WithName("pvcmap").WithName("VolumeReplicationGroup")
			oldPVC, ok := e.ObjectOld.DeepCopyObject().(*corev1.PersistentVolumeClaim)
			if !ok {
				log.Info("Failed to deep copy older PersistentVolumeClaim")

				return false
			}
			newPVC, ok := e.ObjectNew.DeepCopyObject().(*corev1.PersistentVolumeClaim)
			if !ok {
				log.Info("Failed to deep copy newer PersistentVolumeClaim")

				return false
			}

			log.Info("Update event for PersistentVolumeClaim")

			return updateEventDecision(oldPVC, newPVC, log)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			// PVC deletion is held back till VRG deletion. This is to
			// avoid races between subscription deletion and updating
			// VRG state. If VRG state is not updated prior to subscription
			// cleanup, then PVC deletion (triggered by subscription
			// cleanup) would leaving behind VolRep resource with stale
			// state (as per the current VRG state).
			return false
		},
	}

	return pvcPredicate
}

func updateEventDecision(oldPVC *corev1.PersistentVolumeClaim,
	newPVC *corev1.PersistentVolumeClaim,
	log logr.Logger,
) bool {
	const requeue bool = true

	pvcNamespacedName := types.NamespacedName{Name: newPVC.Name, Namespace: newPVC.Namespace}
	predicateLog := log.WithValues("pvc", pvcNamespacedName.String())
	// If finalizers change then deep equal of spec fails to catch it, we may want more
	// conditions here, compare finalizers and also status.phase to catch bound PVCs
	if !reflect.DeepEqual(oldPVC.Spec, newPVC.Spec) {
		predicateLog.Info("Reconciling due to change in spec")

		return requeue
	}

	if oldPVC.Status.Phase != corev1.ClaimBound && newPVC.Status.Phase == corev1.ClaimBound {
		predicateLog.Info("Reconciling due to phase change", "oldPhase", oldPVC.Status.Phase,
			"newPhase", newPVC.Status.Phase)

		return requeue
	}

	// This check may not be needed and can lead to some
	// unnecessary reconciles being triggered when the
	// pod that uses this pvc gets rescheduled to some
	// other node and pvcInUse finalizer is removed as
	// no pod is mounting it.
	if containsString(oldPVC.ObjectMeta.Finalizers, pvcInUse) &&
		!containsString(newPVC.ObjectMeta.Finalizers, pvcInUse) {
		predicateLog.Info("Reconciling due to pvc not in use")

		return requeue
	}

	// If newPVC is not yet bound, dont requeue.
	// If the newPVC is being deleted and VR protection finalizer is
	// not there, then dont requeue.
	// skipResult false means, the above conditions are not met.
	if skipResult, _ := skipPVC(newPVC, predicateLog); !skipResult {
		predicateLog.Info("Reconciling due to VR Protection finalizer")

		return requeue
	}

	predicateLog.Info("Not Requeuing", "oldPVC Phase", oldPVC.Status.Phase,
		"newPVC phase", newPVC.Status.Phase)

	return !requeue
}

func filterPVC(reader client.Reader, pvc *corev1.PersistentVolumeClaim, log logr.Logger) []reconcile.Request {
	req := []reconcile.Request{}

	var vrgs ramendrv1alpha1.VolumeReplicationGroupList

	// decide if reconcile request needs to be sent to the
	// corresponding VolumeReplicationGroup CR by:
	// - whether there is a VolumeReplicationGroup CR that selects the
	//   PVC's namespace.
	// - whether the labels on pvc match the label selectors from
	//    VolumeReplicationGroup CR.
	err := reader.List(context.TODO(), &vrgs)
	if err != nil {
		log.Error(err, "Failed to get list of VolumeReplicationGroup resources")

		return []reconcile.Request{}
	}

	for _, vrg := range vrgs.Items {
		vrgLabelSelector, namespaceNames, err := GetPVCLabelSelector(context.TODO(), reader, vrg, log)
		if err != nil {
			log.Error(err, "Failed to get the label selector from GetPVCLabelSelector", "vrgName", vrg.Name)

			continue
		}

		selector, err := metav1.LabelSelectorAsSelector(&vrgLabelSelector)
		// continue if we fail to get the labels for this object hoping
		// that pvc might actually belong to  some other vrg instead of
		// this. If not found, then reconcile request would not be sent
		if err != nil {
			log.Error(err, "Failed to get the label selector from VolumeReplicationGroup", "vrgName", vrg.Name)

			continue
		}

		if selector.Matches(labels.Set(pvc.GetLabels())) && slices.Contains(namespaceNames, pvc.Namespace) {
			log.Info("Found VolumeReplicationGroup with matching labels",
				"vrg", vrg.Name, "labeled", selector)

			req = append(req, reconcile.Request{NamespacedName: types.NamespacedName{Name: vrg.Name, Namespace: vrg.Namespace}})
		}
	}

	return req
}

func GetPVCLabelSelector(
	ctx context.Context, reader client.Reader, vrg ramendrv1alpha1.VolumeReplicationGroup, log logr.Logger,
) (pvcSelector metav1.LabelSelector, pvcNamespaceNames []string, err error) {
	pvcSelector, pvcNamespaceNames = vrg.Spec.PVCSelector, []string{vrg.Namespace}

	if !RecipeInfoExistsOnVRG(vrg) {
		return
	}

	var recipe recipe.Recipe

	if recipe, err = GetRecipeWithName(ctx, reader, vrg.Spec.KubeObjectProtection.RecipeRef.Name,
		vrg.GetNamespace()); err != nil {
		log.Error(err, "GetRecipeWithName error: %s-%s", vrg.Name, vrg.Namespace)

		return
	}

	if recipe.Spec.Volumes == nil {
		return
	}

	if recipe.Spec.Volumes.LabelSelector != nil {
		pvcSelector = *recipe.Spec.Volumes.LabelSelector
	} else {
		pvcSelector = metav1.LabelSelector{}
	}

	if len(recipe.Spec.Volumes.IncludedNamespaces) > 0 {
		pvcNamespaceNames = recipe.Spec.Volumes.IncludedNamespaces
	}

	return pvcSelector, pvcNamespaceNames, err
}

//nolint: lll // disabling line length linter
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=volumereplicationgroups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=volumereplicationgroups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=volumereplicationgroups/finalizers,verbs=update
// +kubebuilder:rbac:groups=replication.storage.openshift.io,resources=volumereplications,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=replication.storage.openshift.io,resources=volumereplicationclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups=storage.k8s.io,resources=volumeattachments,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=persistentvolumes,verbs=get;list;watch;update;patch;create
// +kubebuilder:rbac:groups=volsync.backube,resources=replicationdestinations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=volsync.backube,resources=replicationsources,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=snapshot.storage.k8s.io,resources=volumesnapshots,verbs=get;list;watch;update;delete
// +kubebuilder:rbac:groups=snapshot.storage.k8s.io,resources=volumesnapshotclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=multicluster.x-k8s.io,resources=serviceexports,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=get;create;patch;update
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=recipes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the VolumeReplicationGroup object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.7.0/pkg/reconcile
func (r *VolumeReplicationGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("VolumeReplicationGroup", req.NamespacedName, "rid", uuid.New())

	log.Info("Entering reconcile loop")

	defer log.Info("Exiting reconcile loop")

	v := VRGInstance{
		reconciler:        r,
		ctx:               ctx,
		log:               log,
		instance:          &ramendrv1alpha1.VolumeReplicationGroup{},
		volRepPVCs:        []corev1.PersistentVolumeClaim{},
		volSyncPVCs:       []corev1.PersistentVolumeClaim{},
		replClassList:     &volrep.VolumeReplicationClassList{},
		namespacedName:    req.NamespacedName.String(),
		objectStorers:     make(map[string]cachedObjectStorer),
		storageClassCache: make(map[string]*storagev1.StorageClass),
	}

	// Fetch the VolumeReplicationGroup instance
	if err := r.APIReader.Get(ctx, req.NamespacedName, v.instance); err != nil {
		if errors.IsNotFound(err) {
			log.Info("Resource not found")

			return ctrl.Result{}, nil
		}

		log.Error(err, "Failed to get resource")

		return ctrl.Result{}, fmt.Errorf("failed to reconcile VolumeReplicationGroup (%v), %w",
			req.NamespacedName, err)
	}

	_, ramenConfig, err := ConfigMapGet(ctx, r.APIReader)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Ramen configmap: %w", err)
	}

	v.ramenConfig = ramenConfig
	v.volSyncHandler = volsync.NewVSHandler(ctx, r.Client, log, v.instance,
		v.instance.Spec.Async, cephFSCSIDriverNameOrDefault(v.ramenConfig),
		volSyncDestinationCopyMethodOrDefault(v.ramenConfig))

	if v.instance.Status.ProtectedPVCs == nil {
		v.instance.Status.ProtectedPVCs = []ramendrv1alpha1.ProtectedPVC{}
	}
	// Save a copy of the instance status to be used for the VRG status update comparison
	v.instance.Status.DeepCopyInto(&v.savedInstanceStatus)

	setVRGInitialCondition(&v.instance.Status.Conditions, v.instance.Generation,
		"Initializing VolumeReplicationGroup")

	res := v.processVRG()
	delayResetIfRequeueTrue(&res, v.log)
	log.Info("Reconcile return", "result", res,
		"VolRep count", len(v.volRepPVCs), "VolSync count", len(v.volSyncPVCs))

	return res, nil
}

type cachedObjectStorer struct {
	storer ObjectStorer
	err    error
}

type VRGInstance struct {
	reconciler           *VolumeReplicationGroupReconciler
	ctx                  context.Context
	log                  logr.Logger
	instance             *ramendrv1alpha1.VolumeReplicationGroup
	savedInstanceStatus  ramendrv1alpha1.VolumeReplicationGroupStatus
	ramenConfig          *ramendrv1alpha1.RamenConfig
	volRepPVCs           []corev1.PersistentVolumeClaim
	volSyncPVCs          []corev1.PersistentVolumeClaim
	replClassList        *volrep.VolumeReplicationClassList
	storageClassCache    map[string]*storagev1.StorageClass
	vrgObjectProtected   *metav1.Condition
	kubeObjectsProtected *metav1.Condition
	vrcUpdated           bool
	namespacedName       string
	volSyncHandler       *volsync.VSHandler
	objectStorers        map[string]cachedObjectStorer
}

const (
	// Finalizers
	vrgFinalizerName        = "volumereplicationgroups.ramendr.openshift.io/vrg-protection"
	pvcVRFinalizerProtected = "volumereplicationgroups.ramendr.openshift.io/pvc-vr-protection"
	pvcInUse                = "kubernetes.io/pvc-protection"

	// Annotations
	pvcVRAnnotationProtectedKey      = "volumereplicationgroups.ramendr.openshift.io/vr-protected"
	pvcVRAnnotationProtectedValue    = "protected"
	pvcVRAnnotationArchivedKey       = "volumereplicationgroups.ramendr.openshift.io/vr-archived"
	pvcVRAnnotationArchivedVersionV1 = "archiveV1"
	pvVRAnnotationRetentionKey       = "volumereplicationgroups.ramendr.openshift.io/vr-retained"
	pvVRAnnotationRetentionValue     = "retained"
	RestoreAnnotation                = "volumereplicationgroups.ramendr.openshift.io/ramen-restore"
	RestoredByRamen                  = "True"

	// StorageClass label
	StorageIDLabel = "ramendr.openshift.io/storageid"

	// VolumeReplicationClass label
	VolumeReplicationIDLabel = "ramendr.openshift.io/replicationid"

	// Maintenance mode label
	MModesLabel = "ramendr.openshift.io/maintenancemodes"
)

func (v *VRGInstance) processVRG() ctrl.Result {
	if err := v.validateVRGState(); err != nil {
		// No requeue, as there is no reconcile till user changes desired spec to a valid value
		return v.invalid(err, "VolumeReplicationGroup state is invalid", false)
	}

	// If neither of Async or Sync mode is provided, then
	// dont requeue. Just return error.
	if err := v.validateVRGMode(); err != nil {
		// No requeue, as there is no reconcile till user changes desired spec to a valid value
		return v.invalid(err, "VolumeReplicationGroup mode is invalid", false)
	}

	if err := v.updatePVCList(); err != nil {
		return v.invalid(err, "Failed to process list of PVCs to protect", true)
	}

	v.log = v.log.WithName("vrginstance").WithValues("State", v.instance.Spec.ReplicationState)

	if !v.instance.GetDeletionTimestamp().IsZero() {
		v.log = v.log.WithValues("Finalize", true)

		return v.processForDeletion()
	}

	if err := v.addFinalizer(vrgFinalizerName); err != nil {
		return v.dataError(err, "Failed to add finalizer to VolumeReplicationGroup", true)
	}

	switch {
	case v.instance.Spec.ReplicationState == ramendrv1alpha1.Primary:
		return v.processAsPrimary()
	default: // Secondary, not primary and not deleted
		return v.processAsSecondary()
	}
}

func (v *VRGInstance) validateVRGState() error {
	if v.instance.Spec.ReplicationState != ramendrv1alpha1.Primary &&
		v.instance.Spec.ReplicationState != ramendrv1alpha1.Secondary {
		err := fmt.Errorf("invalid or unknown replication state detected (deleted %v, desired replicationState %v)",
			!v.instance.GetDeletionTimestamp().IsZero(),
			v.instance.Spec.ReplicationState)

		v.log.Error(err, "Invalid request detected")

		return err
	}

	return nil
}

// Expectation is that either the sync mode (for MetroDR)
// or the async mode (for RegionalDR) is enabled. If none of
// them is enabled, then return error.
// This needs more thought as this function is making a
// compulsion that either of sync or async mode should be there.
func (v *VRGInstance) validateVRGMode() error {
	async := v.instance.Spec.Async != nil
	sync := v.instance.Spec.Sync != nil

	if !sync && !async {
		err := fmt.Errorf("neither of sync or async mode is enabled (deleted %v)",
			!v.instance.GetDeletionTimestamp().IsZero())

		v.log.Error(err, "Invalid request detected")

		return err
	}

	return nil
}

func (v *VRGInstance) clusterDataRestore(result *ctrl.Result) error {
	if v.instance.Spec.PrepareForFinalSync || v.instance.Spec.RunFinalSync {
		msg := "PV restore skipped, as VRG is orchestrating final sync"
		setVRGClusterDataReadyCondition(&v.instance.Status.Conditions, v.instance.Generation, msg)

		return nil
	}

	clusterDataReady := findCondition(v.instance.Status.Conditions, VRGConditionTypeClusterDataReady)
	if clusterDataReady != nil {
		v.log.Info("ClusterDataReady condition",
			"status", clusterDataReady.Status,
			"reason", clusterDataReady.Reason,
			"message", clusterDataReady.Message,
			"observedGeneration", clusterDataReady.ObservedGeneration,
			"generation", v.instance.Generation,
		)

		if clusterDataReady.Status == metav1.ConditionTrue &&
			clusterDataReady.ObservedGeneration == v.instance.Generation {
			v.log.Info("VRG's ClusterDataReady condition found. PV restore must have already been applied")

			return nil
		}
	} else {
		v.log.Info("ClusterDataReady condition absent")
	}

	err := v.restorePVsForVolSync()
	if err != nil {
		v.log.Info("VolSync PV restore failed")

		result.Requeue = true

		return fmt.Errorf("failed to restore PVs for VolSync (%w)", err)
	}

	err = v.clusterDataRestoreForVolRep(result)
	if err != nil {
		v.log.Info("VolRep ClusterData restore failed")

		return fmt.Errorf("failed to restore ClusterData for VolRep (%w)", err)
	}

	// Only after both succeed, we mark ClusterDataReady as true
	msg := "Restored cluster data"
	setVRGClusterDataReadyCondition(&v.instance.Status.Conditions, v.instance.Generation, msg)

	return nil
}

func (v *VRGInstance) listPVCsByPVCSelector() (*corev1.PersistentVolumeClaimList, error) {
	labelSelector, namespaceNames, err := GetPVCLabelSelector(v.ctx, v.reconciler.Client, *v.instance, v.log)
	if err != nil {
		return nil, err
	}

	return rmnutil.ListPVCsByPVCSelector(v.ctx, v.reconciler.Client, v.log, labelSelector,
		namespaceNames,
		v.instance.Spec.VolSync.Disabled)
}

// updatePVCList fetches and updates the PVC list to process for the current instance of VRG
func (v *VRGInstance) updatePVCList() error {
	pvcList, err := v.listPVCsByPVCSelector()
	if err != nil {
		return err
	}

	if v.instance.Spec.Async == nil || v.instance.Spec.VolSync.Disabled {
		v.volRepPVCs = make([]corev1.PersistentVolumeClaim, len(pvcList.Items))
		total := copy(v.volRepPVCs, pvcList.Items)

		v.log.Info("Found PersistentVolumeClaims", "count", total)

		return nil
	}

	if !v.vrcUpdated {
		if err := v.updateReplicationClassList(); err != nil {
			v.log.Error(err, "Failed to get VolumeReplicationClass list")

			return fmt.Errorf("failed to get VolumeReplicationClass list")
		}

		v.vrcUpdated = true
	}

	if !v.instance.GetDeletionTimestamp().IsZero() {
		v.separatePVCsUsingVRGStatus(pvcList)
		v.log.Info(fmt.Sprintf("Separated PVCs (%d) into VolRepPVCs (%d) and VolSyncPVCs (%d)",
			len(pvcList.Items), len(v.volRepPVCs), len(v.volSyncPVCs)))

		return nil
	}

	if len(v.replClassList.Items) == 0 {
		v.volSyncPVCs = make([]corev1.PersistentVolumeClaim, len(pvcList.Items))
		numCopied := copy(v.volSyncPVCs, pvcList.Items)
		v.log.Info("No VolumeReplicationClass available. Using all PVCs with VolSync", "pvcCount", numCopied)

		return nil
	}

	// Separate PVCs targeted for VolRep from PVCs targeted for VolSync
	return v.separatePVCsUsingStorageClassProvisioner(pvcList)
}

func (v *VRGInstance) updateReplicationClassList() error {
	labelSelector := v.instance.Spec.Async.ReplicationClassSelector

	v.log.Info("Fetching VolumeReplicationClass", "labeled", labels.Set(labelSelector.MatchLabels))
	listOptions := []client.ListOption{
		client.MatchingLabels(labelSelector.MatchLabels),
	}

	if err := v.reconciler.List(v.ctx, v.replClassList, listOptions...); err != nil {
		v.log.Error(err, "Failed to list Replication Classes",
			"labeled", labels.Set(labelSelector.MatchLabels))

		return fmt.Errorf("failed to list Replication Classes, %w", err)
	}

	v.log.Info("Number of Replication Classes", "count", len(v.replClassList.Items))

	return nil
}

func (v *VRGInstance) separatePVCsUsingVRGStatus(pvcList *corev1.PersistentVolumeClaimList) {
	for idx := range pvcList.Items {
		pvc := &pvcList.Items[idx]

		for _, protectedPVC := range v.instance.Status.ProtectedPVCs {
			if pvc.Name == protectedPVC.Name {
				if protectedPVC.ProtectedByVolSync {
					v.volSyncPVCs = append(v.volSyncPVCs, *pvc)
				} else {
					v.volRepPVCs = append(v.volRepPVCs, *pvc)
				}
			}
		}
	}
}

func (v *VRGInstance) separatePVCsUsingStorageClassProvisioner(pvcList *corev1.PersistentVolumeClaimList) error {
	for idx := range pvcList.Items {
		pvc := &pvcList.Items[idx]
		scName := pvc.Spec.StorageClassName

		if scName == nil || *scName == "" {
			return fmt.Errorf("missing storage class name for PVC %s", pvc.GetName())
		}

		storageClass := &storagev1.StorageClass{}
		if err := v.reconciler.Get(v.ctx, types.NamespacedName{Name: *scName}, storageClass); err != nil {
			v.log.Info(fmt.Sprintf("Failed to get the storageclass %s", *scName))

			return fmt.Errorf("failed to get the storageclass with name %s (%w)", *scName, err)
		}

		replicationClassMatchFound := false

		for _, replicationClass := range v.replClassList.Items {
			if storageClass.Provisioner == replicationClass.Spec.Provisioner {
				v.volRepPVCs = append(v.volRepPVCs, *pvc)
				replicationClassMatchFound = true

				break
			}
		}

		if !replicationClassMatchFound {
			v.volSyncPVCs = append(v.volSyncPVCs, *pvc)
		}
	}

	v.log.Info(fmt.Sprintf("Found %d PVCs targeted for VolRep and %d targeted for VolSync",
		len(v.volRepPVCs), len(v.volSyncPVCs)))

	return nil
}

// finalizeVRG cleans up managed resources and removes the VRG finalizer for resource deletion
func (v *VRGInstance) processForDeletion() ctrl.Result {
	v.log.Info("Entering processing VolumeReplicationGroup for deletion")

	defer v.log.Info("Exiting processing VolumeReplicationGroup")

	if !containsString(v.instance.ObjectMeta.Finalizers, vrgFinalizerName) {
		v.log.Info("Finalizer missing from resource", "finalizer", vrgFinalizerName)

		return ctrl.Result{}
	}

	if v.deleteVRGHandleMode() {
		v.log.Info("Requeuing as reconciling VolumeReplication for deletion failed")

		return ctrl.Result{Requeue: true}
	}

	result := ctrl.Result{}
	if err := v.kubeObjectsProtectionDelete(&result); err != nil {
		v.log.Info("Kube objects protection deletion failed", "error", err)

		return result
	}

	if v.instance.Spec.ReplicationState == ramendrv1alpha1.Primary {
		if err := v.deleteClusterDataInS3Stores(v.log); err != nil {
			v.log.Info("Requeuing due to failure in deleting cluster data from S3 stores",
				"errorValue", err)

			return ctrl.Result{Requeue: true}
		}
	}

	if err := v.removeFinalizer(vrgFinalizerName); err != nil {
		v.log.Info("Failed to remove finalizer", "finalizer", vrgFinalizerName, "errorValue", err)

		return ctrl.Result{Requeue: true}
	}

	rmnutil.ReportIfNotPresent(v.reconciler.eventRecorder, v.instance, corev1.EventTypeNormal,
		rmnutil.EventReasonDeleteSuccess, "Deletion Success")

	return ctrl.Result{}
}

// For now, async mode and sync mode can be enabled only in either or fashion
// and the function reconcileVRsForDeletion is capable of handling it for both.
// However, in the future, we may want to enable both modes at the same time
// and might call different functions for those modes. This function is in
// preparation of that need.
func (v *VRGInstance) deleteVRGHandleMode() bool {
	return v.reconcileVRsForDeletion()
}

// addFinalizer adds a finalizer to VRG, to act as deletion protection
func (v *VRGInstance) addFinalizer(finalizer string) error {
	if containsString(v.instance.ObjectMeta.Finalizers, finalizer) {
		return nil
	}

	v.instance.ObjectMeta.Finalizers = append(v.instance.ObjectMeta.Finalizers, finalizer)
	status := v.instance.Status

	if err := v.reconciler.Update(v.ctx, v.instance); err != nil {
		v.log.Error(err, "Failed to add finalizer", "finalizer", finalizer)

		return fmt.Errorf("failed to add finalizer to VolumeReplicationGroup resource (%s/%s), %w",
			v.instance.Namespace, v.instance.Name, err)
	}

	v.instance.Status = status

	return nil
}

// removeFinalizer removes VRG finalizer form the resource
func (v *VRGInstance) removeFinalizer(finalizer string) error {
	v.instance.ObjectMeta.Finalizers = removeString(v.instance.ObjectMeta.Finalizers, finalizer)
	status := v.instance.Status

	if err := v.reconciler.Update(v.ctx, v.instance); err != nil {
		v.log.Error(err, "Failed to remove finalizer", "finalizer", finalizer)

		return fmt.Errorf("failed to remove finalizer from VolumeReplicationGroup resource (%s/%s), %w",
			v.instance.Namespace, v.instance.Name, err)
	}

	v.instance.Status = status

	return nil
}

// processAsPrimary reconciles the current instance of VRG as primary
func (v *VRGInstance) processAsPrimary() ctrl.Result {
	v.log.Info("Entering processing VolumeReplicationGroup as Primary")

	defer v.log.Info("Exiting processing VolumeReplicationGroup")

	result := ctrl.Result{}
	if err := v.clusterDataRestore(&result); err != nil {
		return v.clusterDataError(err, "Failed to restore PVs", result)
	}

	result = v.reconcileAsPrimary()

	// If requeue is false, then VRG was successfully processed as primary.
	// Hence the event to be generated is Success of type normal.
	// Expectation is that, if something failed and requeue is true, then
	// appropriate event might have been captured at the time of failure.
	if !result.Requeue {
		rmnutil.ReportIfNotPresent(v.reconciler.eventRecorder, v.instance, corev1.EventTypeNormal,
			rmnutil.EventReasonPrimarySuccess, "Primary Success")
	}

	return v.updateVRGConditionsAndStatus(result)
}

func (v *VRGInstance) reconcileAsPrimary() ctrl.Result {
	var finalSyncPrepared struct {
		volSync bool
	}

	vrg := v.instance
	s3StoreAccessors := v.s3StoreAccessorsGet()
	result := ctrl.Result{}
	result.Requeue = v.reconcileVolSyncAsPrimary(&finalSyncPrepared.volSync)
	v.reconcileVolRepsAsPrimary(&result.Requeue)
	v.kubeObjectsProtectPrimary(&result, s3StoreAccessors)
	v.vrgObjectProtect(&result, s3StoreAccessors)

	if vrg.Spec.PrepareForFinalSync {
		vrg.Status.PrepareForFinalSyncComplete = finalSyncPrepared.volSync
	}

	return result
}

// processAsSecondary reconciles the current instance of VRG as secondary
func (v *VRGInstance) processAsSecondary() ctrl.Result {
	v.log.Info("Entering processing VolumeReplicationGroup as Secondary")

	defer v.log.Info("Exiting processing VolumeReplicationGroup")

	v.instance.Status.LastGroupSyncTime = nil

	result := v.reconcileAsSecondary()

	// If requeue is false, then VRG was successfully processed as Secondary.
	// Hence the event to be generated is Success of type normal.
	// Expectation is that, if something failed and requeue is true, then
	// appropriate event might have been captured at the time of failure.
	if !result.Requeue {
		rmnutil.ReportIfNotPresent(v.reconciler.eventRecorder, v.instance, corev1.EventTypeNormal,
			rmnutil.EventReasonSecondarySuccess, "Secondary Success")
	}

	return v.updateVRGConditionsAndStatus(result)
}

func (v *VRGInstance) reconcileAsSecondary() ctrl.Result {
	vrg := v.instance
	s3StoreAccessors := v.s3StoreAccessorsGet()
	result := ctrl.Result{}
	result.Requeue = v.reconcileVolSyncAsSecondary() || result.Requeue
	result.Requeue = v.reconcileVolRepsAsSecondary() || result.Requeue

	if vrg.Spec.Action == ramendrv1alpha1.VRGActionRelocate {
		v.relocate(&result, s3StoreAccessors)
	}

	// Clear the conditions only if there are no more work as secondary and the RDSpec is not empty.
	// Note: When using VolSync, we preserve the secondary and we need the status of the VRG to be
	// clean. In all other cases, the VRG will be deleted and we don't care about the its conditions.
	if !result.Requeue && len(v.instance.Spec.VolSync.RDSpec) > 0 {
		v.instance.Status.Conditions = []metav1.Condition{}
	}

	return result
}

func (v *VRGInstance) relocate(result *ctrl.Result, s3StoreAccessors []s3StoreAccessor) {
	vrg := v.instance

	if clusterDataProtected := meta.FindStatusCondition(vrg.Status.Conditions,
		VRGConditionTypeClusterDataProtected,
	); clusterDataProtected != nil && (clusterDataProtected.Status != metav1.ConditionTrue ||
		clusterDataProtected.ObservedGeneration != vrg.Generation) {
		v.kubeObjectsProtectSecondary(result, s3StoreAccessors)
	}
}

func (v *VRGInstance) invalid(err error, msg string, requeue bool) ctrl.Result {
	rmnutil.ReportIfNotPresent(v.reconciler.eventRecorder, v.instance, corev1.EventTypeWarning,
		rmnutil.EventReasonValidationFailed, err.Error())

	return v.dataError(err, msg, requeue)
}

func (v *VRGInstance) dataError(err error, msg string, requeue bool) ctrl.Result {
	v.errorConditionLogAndSet(err, msg, setVRGDataErrorCondition)

	return v.updateVRGStatus(ctrl.Result{Requeue: requeue})
}

func (v *VRGInstance) clusterDataError(err error, msg string, result ctrl.Result) ctrl.Result {
	v.errorConditionLogAndSet(err, msg, setVRGClusterDataErrorCondition)

	return v.updateVRGStatus(result)
}

func (v *VRGInstance) errorConditionLogAndSet(err error, msg string,
	conditionSet func(*[]metav1.Condition, int64, string),
) {
	v.log.Info(msg, "error", err)
	conditionSet(&v.instance.Status.Conditions, v.instance.Generation,
		fmt.Sprintf("%s: %v", msg, err),
	)
}

func (v *VRGInstance) updateVRGConditionsAndStatus(result ctrl.Result) ctrl.Result {
	v.updateVRGConditions()

	return v.updateVRGStatus(result)
}

func (v *VRGInstance) updateVRGStatus(result ctrl.Result) ctrl.Result {
	v.log.Info("Updating VRG status")

	v.updateStatusState()

	v.instance.Status.ObservedGeneration = v.instance.Generation

	if !reflect.DeepEqual(v.savedInstanceStatus, v.instance.Status) {
		v.instance.Status.LastUpdateTime = metav1.Now()
		if err := v.reconciler.Status().Update(v.ctx, v.instance); err != nil {
			v.log.Info(fmt.Sprintf("Failed to update VRG status (%v/%+v)",
				err, v.instance.Status))

			result.Requeue = true

			return result
		}

		dataReadyCondition := findCondition(v.instance.Status.Conditions, VRGConditionTypeDataReady)
		v.log.Info(fmt.Sprintf("Updated VRG Status VolRep pvccount (%d), VolSync pvccount(%d)"+
			" DataReady Condition (%s)",
			len(v.volRepPVCs), len(v.volSyncPVCs), dataReadyCondition))

		return result
	}

	v.log.Info(fmt.Sprintf("Nothing to update VolRep pvccount (%d), VolSync pvccount(%d)",
		len(v.volRepPVCs), len(v.volSyncPVCs)))

	return result
}

func (v *VRGInstance) updateStatusState() {
	dataReadyCondition := findCondition(v.instance.Status.Conditions, VRGConditionTypeDataReady)
	if dataReadyCondition == nil {
		if v.instance.Spec.ReplicationState == ramendrv1alpha1.Secondary &&
			len(v.instance.Spec.VolSync.RDSpec) > 0 {
			v.instance.Status.State = ramendrv1alpha1.SecondaryState

			return
		}

		v.log.Info("Failed to find the DataReady condition in status")

		return
	}

	StatusState := getStatusStateFromSpecState(v.instance.Spec.ReplicationState)

	// update Status.State to reflect the state in spec
	// only after successful transition of the resource
	// (from primary->secondary or vise versa). That
	// successful completion of transition can be seen
	// in dataReadyCondition.Status being set to True.
	if dataReadyCondition.Status == metav1.ConditionTrue {
		v.instance.Status.State = StatusState

		return
	}

	// If VRG available condition is not true and the reason
	// is Error, then mark Status.State as UnknownState instead
	// of Primary or Secondary.
	if dataReadyCondition.Reason == VRGConditionReasonError {
		v.instance.Status.State = ramendrv1alpha1.UnknownState

		return
	}

	// If the state in spec is anything apart from
	// primary or secondary, then explicitly set
	// the Status.State to UnknownState.
	if StatusState == ramendrv1alpha1.UnknownState {
		v.instance.Status.State = StatusState
	}
}

func getStatusStateFromSpecState(state ramendrv1alpha1.ReplicationState) ramendrv1alpha1.State {
	switch state {
	case ramendrv1alpha1.Primary:
		return ramendrv1alpha1.PrimaryState
	case ramendrv1alpha1.Secondary:
		return ramendrv1alpha1.SecondaryState
	default:
		return ramendrv1alpha1.UnknownState
	}
}

// updateVRGConditions updates three summary conditions VRGConditionTypeDataReady,
// VRGConditionTypeClusterDataProtected and VRGConditionDataProtected at the VRG
// level based on the corresponding PVC level conditions in the VRG:
//
// The VRGConditionTypeClusterDataReady summary condition is not a PVC level
// condition and is updated elsewhere.
func (v *VRGInstance) updateVRGConditions() {
	logAndSet := func(conditionName string, subconditions ...*metav1.Condition) {
		v.log.Info(conditionName, "subconditions", subconditions)
		rmnutil.ConditionSetFirstFalseOrLastTrue(setStatusCondition, &v.instance.Status.Conditions, subconditions...)
	}
	logAndSet(VRGConditionTypeDataReady,
		v.aggregateVolSyncDataReadyCondition(),
		v.aggregateVolRepDataReadyCondition(),
	)

	volSyncDataProtected, volSyncClusterDataProtected := v.aggregateVolSyncDataProtectedConditions()

	logAndSet(VRGConditionTypeDataProtected,
		volSyncDataProtected,
		v.aggregateVolRepDataProtectedCondition(),
	)
	logAndSet(VRGConditionTypeClusterDataProtected,
		volSyncClusterDataProtected,
		v.aggregateVolRepClusterDataProtectedCondition(),
		v.vrgObjectProtected,
		v.kubeObjectsProtected,
	)
	v.updateVRGLastGroupSyncTime()
	v.updateVRGLastGroupSyncDuration()
	v.updateLastGroupSyncBytes()
}

func (v *VRGInstance) vrgReadyStatus() *metav1.Condition {
	if v.instance.Spec.ReplicationState == ramendrv1alpha1.Secondary {
		v.log.Info("Marking VRG ready with replicating reason")

		msg := "PVCs in the VolumeReplicationGroup group are replicating"

		return newVRGDataReplicatingCondition(v.instance.Generation, msg)
	}

	// VRG as primary
	v.log.Info("Marking VRG data ready after establishing replication")

	msg := "PVCs in the VolumeReplicationGroup are ready for use"

	return newVRGAsPrimaryReadyCondition(v.instance.Generation, msg)
}

func (v *VRGInstance) updateVRGLastGroupSyncTime() {
	var leastLastSyncTime *metav1.Time

	for _, protectedPVC := range v.instance.Status.ProtectedPVCs {
		// If any protected PVC reports nil, report that back (no sync time available)
		if protectedPVC.LastSyncTime == nil {
			leastLastSyncTime = nil

			break
		}

		if leastLastSyncTime == nil {
			leastLastSyncTime = protectedPVC.LastSyncTime

			continue
		}

		if protectedPVC.LastSyncTime != nil && protectedPVC.LastSyncTime.Before(leastLastSyncTime) {
			leastLastSyncTime = protectedPVC.LastSyncTime
		}
	}

	v.instance.Status.LastGroupSyncTime = leastLastSyncTime
}

func (v *VRGInstance) updateVRGLastGroupSyncDuration() {
	var maxLastSyncDuration *metav1.Duration

	for _, protectedPVC := range v.instance.Status.ProtectedPVCs {
		if maxLastSyncDuration == nil && protectedPVC.LastSyncDuration != nil {
			maxLastSyncDuration = new(metav1.Duration)
			*maxLastSyncDuration = *protectedPVC.LastSyncDuration

			continue
		}

		if protectedPVC.LastSyncDuration != nil &&
			protectedPVC.LastSyncDuration.Duration > maxLastSyncDuration.Duration {
			*maxLastSyncDuration = *protectedPVC.LastSyncDuration
		}
	}

	v.instance.Status.LastGroupSyncDuration = maxLastSyncDuration
}

func (v *VRGInstance) updateLastGroupSyncBytes() {
	var totalLastSyncBytes *int64

	for _, protectedPVC := range v.instance.Status.ProtectedPVCs {
		if totalLastSyncBytes == nil && protectedPVC.LastSyncBytes != nil {
			totalLastSyncBytes = new(int64)
			*totalLastSyncBytes = *protectedPVC.LastSyncBytes

			continue
		}

		if protectedPVC.LastSyncBytes != nil {
			*totalLastSyncBytes += *protectedPVC.LastSyncBytes
		}
	}

	v.instance.Status.LastGroupSyncBytes = totalLastSyncBytes
}

func (v *VRGInstance) s3StoreAccessorsGet() []s3StoreAccessor {
	vrg := v.instance

	return s3StoreAccessorsGet(
		vrg.Spec.S3Profiles,
		func(s3ProfileName string) (ObjectStorer, ramendrv1alpha1.S3StoreProfile, error) {
			return v.reconciler.ObjStoreGetter.ObjectStore(
				v.ctx, v.reconciler.APIReader, s3ProfileName, v.namespacedName, v.log,
			)
		},
		v.log,
	)
}

// It might be better move the helper functions like these to a separate
// package or a separate go file?
func containsString(values []string, s string) bool {
	for _, item := range values {
		if item == s {
			return true
		}
	}

	return false
}

func removeString(values []string, s string) []string {
	result := []string{}

	for _, item := range values {
		if item == s {
			continue
		}

		result = append(result, item)
	}

	return result
}
