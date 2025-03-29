// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/exp/slices"
	"golang.org/x/time/rate"

	volrep "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	"github.com/google/uuid"
	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	"github.com/ramendr/ramen/internal/controller/kubeobjects/velero"
	"github.com/ramendr/ramen/internal/controller/util"
	"golang.org/x/exp/maps" // TODO replace with "maps" in go1.21+
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/volsync"
)

// VolumeReplicationGroupReconciler reconciles a VolumeReplicationGroup object
type VolumeReplicationGroupReconciler struct {
	client.Client
	APIReader           client.Reader
	Log                 logr.Logger
	ObjStoreGetter      ObjectStoreGetter
	Scheme              *runtime.Scheme
	eventRecorder       *util.EventReporter
	kubeObjects         kubeobjects.RequestsManager
	RateLimiter         *workqueue.TypedRateLimiter[reconcile.Request]
	veleroCRsAreWatched bool
}

// SetupWithManager sets up the controller with the Manager.
func (r *VolumeReplicationGroupReconciler) SetupWithManager(
	mgr ctrl.Manager, ramenConfig *ramendrv1alpha1.RamenConfig,
) error {
	r.eventRecorder = util.NewEventReporter(mgr.GetEventRecorderFor("controller_VolumeReplicationGroup"))

	r.Log.Info("Adding VolumeReplicationGroup controller")

	rateLimiter := workqueue.NewTypedMaxOfRateLimiter(
		workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](1*time.Second, 1*time.Minute),
		// defaults from client-go
		//nolint:mnd
		&workqueue.TypedBucketRateLimiter[reconcile.Request]{Limiter: rate.NewLimiter(rate.Limit(10), 100)},
	)
	if r.RateLimiter != nil {
		rateLimiter = *r.RateLimiter
	}

	ctrlBuilder := ctrl.NewControllerManagedBy(mgr).
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
		Watches(&corev1.PersistentVolumeClaim{},
			handler.EnqueueRequestsFromMapFunc(r.pvcMapFunc),
			builder.WithPredicates(pvcPredicateFunc()),
		).
		Watches(&volrep.VolumeReplication{},
			handler.EnqueueRequestsFromMapFunc(r.VRMapFunc),
			builder.WithPredicates(util.CreateOrDeleteOrResourceVersionUpdatePredicate{}),
		).
		Watches(&corev1.ConfigMap{}, handler.EnqueueRequestsFromMapFunc(r.configMapFun)).
		Owns(&volrep.VolumeReplication{})

	if !ramenConfig.VolSync.Disabled {
		r.Log.Info("VolSync enabled; adding owns and watches")
		ctrlBuilder = r.addVolsyncOwnsAndWatches(ctrlBuilder)
	} else {
		r.Log.Info("VolSync disabled; don't own volsync resources")
	}

	r.kubeObjects = velero.RequestsManager{}

	if !ramenConfig.KubeObjectProtection.Disabled {
		ctrlBuilder = r.addKubeObjectsOwnsAndWatches(ctrlBuilder)
	} else {
		r.Log.Info("Kube object protection disabled; don't watch kube objects requests")
	}

	return ctrlBuilder.Complete(r)
}

type objectToReconcileRequestsMapper struct {
	reader client.Reader
	log    logr.Logger
}

func (r *VolumeReplicationGroupReconciler) pvcMapFunc(ctx context.Context, obj client.Object) []reconcile.Request {
	log := ctrl.Log.WithName("pvcmap").WithName("VolumeReplicationGroup")

	pvc, ok := obj.(*corev1.PersistentVolumeClaim)
	if !ok {
		log.Info("PersistentVolumeClaim(PVC) map function received non-PVC resource")

		return []reconcile.Request{}
	}

	return filterPVC(r.Client, pvc,
		log.WithValues("pvc", types.NamespacedName{Name: pvc.Name, Namespace: pvc.Namespace}))
}

func (r *VolumeReplicationGroupReconciler) configMapFun(
	ctx context.Context, configmap client.Object,
) []reconcile.Request {
	log := ctrl.Log.WithName("configmap").WithName("VolumeReplicationGroup")

	if configmap.GetName() != DrClusterOperatorConfigMapName || configmap.GetNamespace() != RamenOperatorNamespace() {
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
	log := ctrl.Log.WithName("pvcmap").WithName("VolumeReplicationGroup")
	pvcPredicate := predicate.Funcs{
		// NOTE: Create predicate is retained, to help with logging the event
		CreateFunc: func(e event.CreateEvent) bool {
			log.Info("Create event for PersistentVolumeClaim")

			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
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
			o := e.Object
			log.Info("PVC Delete", "namespace", o.GetNamespace(), "name", o.GetName())

			return true
		},
	}

	return pvcPredicate
}

func updateEventDecision(oldPVC, newPVC *corev1.PersistentVolumeClaim, log logr.Logger) bool {
	const requeue bool = true

	predicateLog := log.WithValues("pvc", types.NamespacedName{Name: newPVC.Name, Namespace: newPVC.Namespace}.String())

	if addedOrRemoved, added, removed := pvcFinalizerAddedOrRemoved(oldPVC, newPVC); addedOrRemoved {
		predicateLog.Info("Not reconciling due to finalizer addition or removal", "added", added, "removed", removed)

		return !requeue
	}

	if added, protected, archived := pvcAnnotationAdded(oldPVC, newPVC); added {
		predicateLog.Info("Not reconciling due to annotation addition", "protected", protected, "archived", archived)

		return !requeue
	}

	if oldLabels, newLabels := oldPVC.GetLabels(), newPVC.GetLabels(); !maps.Equal(oldLabels, newLabels) {
		predicateLog.Info("Reconciling due to label change", "before", oldLabels, "after", newLabels)

		return requeue
	}

	// If finalizers change then deep equal of spec fails to catch it, we may want more
	// conditions here, compare finalizers and also status.phase to catch bound PVCs
	if !reflect.DeepEqual(oldPVC.Spec, newPVC.Spec) {
		predicateLog.Info("Reconciling due to change in spec")

		return requeue
	}

	if oldPVC.Status.Phase != corev1.ClaimBound && newPVC.Status.Phase == corev1.ClaimBound {
		predicateLog.Info("Reconciling due to phase change", "before", oldPVC.Status.Phase, "after", newPVC.Status.Phase)

		return requeue
	}

	// This check may not be needed and can lead to some unnecessary reconciles being triggered when the
	// pod that uses this pvc gets rescheduled to some other node and pvcInUse finalizer is removed as
	// no pod is mounting it.
	if controllerutil.ContainsFinalizer(oldPVC, pvcInUse) && !controllerutil.ContainsFinalizer(newPVC, pvcInUse) {
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

	predicateLog.Info("Not Requeuing", "oldPVC Phase", oldPVC.Status.Phase, "newPVC phase", newPVC.Status.Phase)

	return !requeue
}

func pvcFinalizerAddedOrRemoved(oldPVC, newPVC *corev1.PersistentVolumeClaim) (bool, bool, bool) {
	contained := controllerutil.ContainsFinalizer(oldPVC, PvcVRFinalizerProtected)
	contains := controllerutil.ContainsFinalizer(newPVC, PvcVRFinalizerProtected)
	added := !contained && contains
	removed := contained && !contains

	return added || removed, added, removed
}

func pvcAnnotationAdded(oldPVC, newPVC *corev1.PersistentVolumeClaim) (bool, bool, bool) {
	before := oldPVC.GetAnnotations()
	after := newPVC.GetAnnotations()
	_, protectedBefore := before[pvcVRAnnotationProtectedKey]
	_, protectedAfter := after[pvcVRAnnotationProtectedKey]
	_, archivedBefore := before[pvcVRAnnotationArchivedKey]
	_, archivedAfter := after[pvcVRAnnotationArchivedKey]
	protectedAdded := !protectedBefore && protectedAfter
	archivedAdded := !archivedBefore && archivedAfter

	return protectedAdded || archivedAdded, protectedAdded, archivedAdded
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

	_, ramenConfig, err := ConfigMapGet(context.TODO(), reader)
	if err != nil {
		log.Error(err, "Failed to get Ramen config")

		return []reconcile.Request{}
	}

	for _, vrg := range vrgs.Items {
		log1 := log.WithValues("vrg", vrg.Name)

		pvcSelector, err := GetPVCSelector(context.TODO(), reader, vrg, *ramenConfig, log)
		if err != nil {
			continue
		}

		// continue if we fail to get the labels for this object hoping
		// that pvc might actually belong to  some other vrg instead of
		// this. If not found, then reconcile request would not be sent
		selector, err := metav1.LabelSelectorAsSelector(&pvcSelector.LabelSelector)
		if err != nil {
			log1.Error(err, "Failed to get the label selector from VolumeReplicationGroup")

			continue
		}

		vrgNamespacedName := types.NamespacedName{Name: vrg.Name, Namespace: vrg.Namespace}
		namespaceSelected := slices.Contains(pvcSelector.NamespaceNames, pvc.Namespace)
		labelMatch := selector.Matches(labels.Set(pvc.GetLabels()))
		ownerMatch := util.OwnerNamespacedName(pvc) == vrgNamespacedName

		if labelMatch && namespaceSelected || ownerMatch {
			log1.Info("Found VolumeReplicationGroup with matching labels or owner",
				"vrg", vrgNamespacedName.String(), "selector", selector,
				"namespaces selected", pvcSelector.NamespaceNames,
				"label match", labelMatch, "owner match", ownerMatch)

			req = append(req, reconcile.Request{NamespacedName: vrgNamespacedName})
		}
	}

	return req
}

//nolint:lll // disabling line length linter
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=volumereplicationgroups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=volumereplicationgroups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=volumereplicationgroups/finalizers,verbs=update
// +kubebuilder:rbac:groups=replication.storage.openshift.io,resources=volumereplications,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=replication.storage.openshift.io,resources=volumereplicationclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups=storage.k8s.io,resources=volumeattachments,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch
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
// +kubebuilder:rbac:groups="apiextensions.k8s.io",resources=customresourcedefinitions,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=pods/exec,verbs=create

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the VolumeReplicationGroup object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.7.0/pkg/reconcile
//
//nolint:funlen
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
		if k8serrors.IsNotFound(err) {
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
	adminNamespaceVRG := vrgInAdminNamespace(v.instance, v.ramenConfig)

	if adminNamespaceVRG && !r.veleroCRsAreWatched {
		return ctrl.Result{},
			fmt.Errorf("VRG {%s/%s} with kube object protection doesn't work if velero/oadp is not installed. "+
				"Please install velero/oadp and restart the operator", v.instance.Namespace, v.instance.Name)
	}

	v.volSyncHandler = volsync.NewVSHandler(ctx, r.Client, log, v.instance,
		v.instance.Spec.Async, cephFSCSIDriverNameOrDefault(v.ramenConfig),
		volSyncDestinationCopyMethodOrDefault(v.ramenConfig), adminNamespaceVRG)

	if v.instance.Status.ProtectedPVCs == nil {
		v.instance.Status.ProtectedPVCs = []ramendrv1alpha1.ProtectedPVC{}
	}
	// Save a copy of the instance status to be used for the VRG status update comparison
	v.instance.Status.DeepCopyInto(&v.savedInstanceStatus)
	v.vrgStatusPvcNamespacesSetIfUnset()
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
	recipeElements       RecipeElements
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
	s3StoreAccessors     []s3StoreAccessor
	result               ctrl.Result
}

// struct with pv with volrepclass and volsync

const (
	// Finalizers
	vrgFinalizerName        = "volumereplicationgroups.ramendr.openshift.io/vrg-protection"
	PvcVRFinalizerProtected = "volumereplicationgroups.ramendr.openshift.io/pvc-vr-protection"
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

	// Consistency group label
	ConsistencyGroupLabel = "ramendr.openshift.io/consistency-group"

	// VolumeReplicationClass label
	VolumeReplicationIDLabel = "ramendr.openshift.io/replicationid"

	// Maintenance mode label
	MModesLabel = "ramendr.openshift.io/maintenancemodes"

	// VolumeReplicationClass schedule parameter key
	VRClassScheduleKey = "schedulingInterval"
)

func (v *VRGInstance) requeue() {
	v.result.Requeue = true
}

//nolint:cyclop
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

	if v.instance.Spec.ProtectedNamespaces != nil && len(*v.instance.Spec.ProtectedNamespaces) > 0 {
		if v.instance.Namespace != RamenOperandsNamespace(*v.ramenConfig) {
			return v.invalid(fmt.Errorf("VolumeReplicationGroup is not allowed to protect namespaces"),
				"VolumeReplicationGroup is not in the admin namespace", false)
		}
	}

	var err error

	v.recipeElements, err = RecipeElementsGet(v.ctx, v.reconciler.Client, *v.instance, *v.ramenConfig, v.log)
	if err != nil {
		return v.invalid(err, "Failed to get recipe", false)
	}

	v.log.Info("Recipe", "elements", v.recipeElements)

	if err := v.updatePVCList(); err != nil {
		return v.invalid(err, "Failed to process list of PVCs to protect", true)
	}

	if err := v.labelPVCsForCG(); err != nil {
		return v.invalid(err, "Failed to label PVCs for consistency groups", true)
	}

	v.log = v.log.WithValues("State", v.instance.Spec.ReplicationState)
	v.s3StoreAccessorsGet()

	if util.ResourceIsDeleted(v.instance) {
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
			util.ResourceIsDeleted(v.instance),
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
			util.ResourceIsDeleted(v.instance))

		v.log.Error(err, "Invalid request detected")

		return err
	}

	return nil
}

func (v *VRGInstance) clusterDataRestore(result *ctrl.Result) (int, error) {
	v.log.Info("Restoring PVs and PVCs")

	numRestoredForVS, err := v.restorePVsAndPVCsForVolSync()
	if err != nil {
		v.log.Info("VolSync PV/PVC restore failed")

		return numRestoredForVS, fmt.Errorf("failed to restore PV/PVC for VolSync (%w)", err)
	}

	numRestoredForVR, err := v.restorePVsAndPVCsForVolRep(result)
	if err != nil {
		v.log.Info("VolRep PV/PVC restore failed")

		return numRestoredForVS + numRestoredForVR, fmt.Errorf("failed to restore PV/PVC for VolRep (%w)", err)
	}

	// Only after both succeed, we mark ClusterDataReady as true
	var msg string
	if numRestoredForVS+numRestoredForVR == 0 {
		msg = "Nothing to restore"
	} else {
		msg = fmt.Sprintf("Restored %d volsync PVs/PVCs and %d volrep PVs/PVCs", numRestoredForVS, numRestoredForVR)
	}

	setVRGClusterDataReadyCondition(&v.instance.Status.Conditions, v.instance.Generation, msg)

	return numRestoredForVS + numRestoredForVR, nil
}

func (v *VRGInstance) listPVCsByVrgPVCSelector() (*corev1.PersistentVolumeClaimList, error) {
	return v.listPVCsByPVCSelector(v.recipeElements.PvcSelector.LabelSelector)
}

func (v *VRGInstance) listPVCsOwnedByVrg() (*corev1.PersistentVolumeClaimList, error) {
	vrg := v.instance

	return v.listPVCsByPVCSelector(metav1.LabelSelector{MatchLabels: util.OwnerLabels(vrg)})
}

func (v *VRGInstance) listPVCsByPVCSelector(labelSelector metav1.LabelSelector,
) (*corev1.PersistentVolumeClaimList, error) {
	return util.ListPVCsByPVCSelector(v.ctx, v.reconciler.Client, v.log,
		labelSelector,
		v.recipeElements.PvcSelector.NamespaceNames,
		v.instance.Spec.VolSync.Disabled,
	)
}

// updatePVCList fetches and updates the PVC list to process for the current instance of VRG
func (v *VRGInstance) updatePVCList() error {
	pvcList, err := v.listPVCsByVrgPVCSelector()
	if err != nil {
		return err
	}

	if v.instance.Spec.Async == nil {
		err := v.validateSyncPVCs(pvcList)
		if err != nil {
			return err
		}

		v.volRepPVCs = make([]corev1.PersistentVolumeClaim, len(pvcList.Items))
		total := copy(v.volRepPVCs, pvcList.Items)

		v.log.Info("Found PersistentVolumeClaims", "count", total)

		return nil
	}

	if err := v.updateReplicationClassList(); err != nil {
		v.log.Error(err, "Failed to get VolumeReplicationClass list")

		return fmt.Errorf("failed to get VolumeReplicationClass list")
	}

	if util.ResourceIsDeleted(v.instance) {
		v.separatePVCsUsingVRGStatus(pvcList)
		v.log.Info(fmt.Sprintf("Separated PVCs (%d) into VolRepPVCs (%d) and VolSyncPVCs (%d)",
			len(pvcList.Items), len(v.volRepPVCs), len(v.volSyncPVCs)))

		return nil
	}

	// Separate PVCs targeted for VolRep from PVCs targeted for VolSync
	return v.separateAsyncPVCs(pvcList)
}

func (v *VRGInstance) labelPVCsForCG() error {
	if v.instance.Spec.Async == nil {
		return nil
	}

	if !util.IsCGEnabled(v.instance.GetAnnotations()) {
		return nil
	}

	for idx := range v.volRepPVCs {
		pvc := &v.volRepPVCs[idx]

		if err := v.addConsistencyGroupLabel(pvc); err != nil {
			return fmt.Errorf("failed to label PVC %s/%s for consistency group (%w)",
				pvc.GetNamespace(), pvc.GetName(), err)
		}
	}

	for idx := range v.volSyncPVCs {
		pvc := &v.volSyncPVCs[idx]

		if err := v.addConsistencyGroupLabel(pvc); err != nil {
			return fmt.Errorf("failed to label PVC %s/%s for consistency group (%w)",
				pvc.GetNamespace(), pvc.GetName(), err)
		}
	}

	return nil
}

func (v *VRGInstance) addConsistencyGroupLabel(pvc *corev1.PersistentVolumeClaim) error {
	cgLabelVal, err := v.getCGLabelValue(pvc.Spec.StorageClassName, pvc.GetName(), pvc.GetNamespace())
	if err != nil {
		return err
	}

	// Add a CG label to indicate that this PVC belongs to a consistency group.
	return util.NewResourceUpdater(pvc).
		AddLabel(ConsistencyGroupLabel, cgLabelVal).
		Update(v.ctx, v.reconciler.Client)
}

func (v *VRGInstance) getCGLabelValue(scName *string, pvcName, pvcNamespace string) (string, error) {
	if scName == nil || *scName == "" {
		return "", fmt.Errorf("missing storage class name for PVC %s/%s", pvcNamespace, pvcName)
	}

	storageClass := &storagev1.StorageClass{}
	if err := v.reconciler.Get(v.ctx, types.NamespacedName{Name: *scName}, storageClass); err != nil {
		v.log.Info(fmt.Sprintf("Failed to get the storageclass %s", *scName))

		return "", fmt.Errorf("failed to get the storageclass with name %s (%w)", *scName, err)
	}

	storageID, ok := storageClass.GetLabels()[StorageIDLabel]
	if !ok {
		v.log.Info("Missing storageID for PVC %s/%s", pvcNamespace, pvcName)

		return "", fmt.Errorf("missing storageID for PVC %s/%s", pvcNamespace, pvcName)
	}

	// FIXME: a temporary workaround for issue DFBUGS-1209
	// Remove this block once DFBUGS-1209 is fixed
	cgLabelVal := "cephfs-" + storageID
	if storageClass.Provisioner != DefaultCephFSCSIDriverName {
		cgLabelVal = "rbd-" + storageID
	}

	return cgLabelVal, nil
}

func (v *VRGInstance) updateReplicationClassList() error {
	if v.vrcUpdated {
		return nil
	}

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

	v.vrcUpdated = true

	v.log.Info("Number of Replication Classes", "count", len(v.replClassList.Items))

	return nil
}

func (v *VRGInstance) separatePVCsUsingVRGStatus(pvcList *corev1.PersistentVolumeClaimList) {
	for idx := range pvcList.Items {
		pvc := &pvcList.Items[idx]

		for _, protectedPVC := range v.instance.Status.ProtectedPVCs {
			if pvc.Name == protectedPVC.Name && pvc.Namespace == protectedPVC.Namespace {
				if protectedPVC.ProtectedByVolSync {
					v.volSyncPVCs = append(v.volSyncPVCs, *pvc)
				} else {
					v.volRepPVCs = append(v.volRepPVCs, *pvc)
				}
			}
		}
	}
}

//nolint:gocognit, nestif
func (v *VRGInstance) validateSyncPVCs(pvcList *corev1.PersistentVolumeClaimList) error {
	peerClasses := v.instance.Spec.Sync.PeerClasses
	if len(peerClasses) == 0 {
		return nil
	}

	for idx := range pvcList.Items {
		pvc := &pvcList.Items[idx]
		scName := pvc.Spec.StorageClassName

		storageClass, err := v.validateAndGetStorageClass(scName, pvc)
		if err != nil {
			return err
		}

		_, err = v.findPeerClassMatchingSC(storageClass, peerClasses, pvc)
		if err != nil {
			return err
		}
	}

	return nil
}

func (v *VRGInstance) separatePVCsUsingOnlySC(storageClass *storagev1.StorageClass, pvc *corev1.PersistentVolumeClaim) {
	v.log.Info("separating PVC using only sc provisioner")

	replicationClassMatchFound := false

	pvcEnabledForVolSync := util.IsPVCMarkedForVolSync(v.instance.GetAnnotations())

	if !pvcEnabledForVolSync {
		for _, replicationClass := range v.replClassList.Items {
			if storageClass.Provisioner == replicationClass.Spec.Provisioner {
				v.volRepPVCs = append(v.volRepPVCs, *pvc)
				replicationClassMatchFound = true

				break
			}
		}
	}

	if !replicationClassMatchFound {
		v.volSyncPVCs = append(v.volSyncPVCs, *pvc)
	}
}

func (v *VRGInstance) separatePVCUsingPeerClassAndSC(peerClasses []ramendrv1alpha1.PeerClass,
	storageClass *storagev1.StorageClass, pvc *corev1.PersistentVolumeClaim,
) error {
	v.log.Info("separate PVC using peerClasses")

	peerClass, err := v.findPeerClassMatchingSC(storageClass, peerClasses, pvc)
	if err != nil {
		return err
	}

	if peerClass == nil {
		msg := fmt.Sprintf("peerClass matching storageClass %s not found for async PVC", storageClass.GetName())
		v.updatePVCDataReadyCondition(pvc.Namespace, pvc.Name, VRGConditionReasonPeerClassNotFound, msg)

		return errors.New(msg)
	}

	pvcEnabledForVolSync := util.IsPVCMarkedForVolSync(v.instance.GetAnnotations())

	if !pvcEnabledForVolSync {
		if peerClass.ReplicationID != "" {
			replicationClass := v.findReplicationClassUsingPeerClass(peerClass, storageClass)
			if replicationClass != nil {
				v.volRepPVCs = append(v.volRepPVCs, *pvc)

				return nil
			}

			return fmt.Errorf("failed to find replicationClass matching peerClass for PVC %s/%s",
				pvc.Namespace, pvc.Name)
		}
	}

	if v.instance.Spec.VolSync.Disabled {
		return fmt.Errorf("failed to find matching peerClass for PVC and VolSync is disabled")
	}

	snapClass, err := v.findVolSnapClass(storageClass)
	if err != nil {
		return err
	}

	if snapClass == nil {
		return fmt.Errorf("failed to find snapshotClass for PVC %s/%s", pvc.Namespace, pvc.Name)
	}

	v.volSyncPVCs = append(v.volSyncPVCs, *pvc)

	return nil
}

//nolint:gocognit,cyclop
func (v *VRGInstance) separateAsyncPVCs(pvcList *corev1.PersistentVolumeClaimList) error {
	peerClasses := v.instance.Spec.Async.PeerClasses

	for idx := range pvcList.Items {
		pvc := &pvcList.Items[idx]
		scName := pvc.Spec.StorageClassName

		storageClass, err := v.validateAndGetStorageClass(scName, pvc)
		if err != nil {
			return err
		}

		if len(peerClasses) == 0 {
			v.separatePVCsUsingOnlySC(storageClass, pvc)
		} else {
			err = v.separatePVCUsingPeerClassAndSC(peerClasses, storageClass, pvc)
			if err != nil {
				return err
			}
		}
	}

	v.log.Info(fmt.Sprintf("Found %d PVCs targeted for VolRep and %d targeted for VolSync",
		len(v.volRepPVCs), len(v.volSyncPVCs)))

	if len(pvcList.Items) != (len(v.volRepPVCs) + len(v.volSyncPVCs)) {
		return fmt.Errorf("no PVCs are procted")
	}

	return nil
}

func (v *VRGInstance) findReplicationClassUsingPeerClass(
	peerClass *ramendrv1alpha1.PeerClass,
	storageClass *storagev1.StorageClass,
) *volrep.VolumeReplicationClass {
	for _, replicationClass := range v.replClassList.Items {
		rIDFromReplicationClass := replicationClass.GetLabels()[VolumeReplicationIDLabel]
		sIDfromReplicationClass := replicationClass.GetLabels()[StorageIDLabel]

		matched := sIDfromReplicationClass == storageClass.GetLabels()[StorageIDLabel] &&
			rIDFromReplicationClass == peerClass.ReplicationID &&
			replicationClass.Spec.Provisioner == storageClass.Provisioner

		if matched {
			return &replicationClass
		}

		continue
	}

	return nil
}

func (v *VRGInstance) findVolSnapClass(storageClass *storagev1.StorageClass,
) (*snapv1.VolumeSnapshotClass, error) {
	SnaphotClasses, err := v.volSyncHandler.GetVolumeSnapshotClasses()
	if err != nil {
		return nil, err
	}

	for _, snapshotClass := range SnaphotClasses {
		sIDFromSnapClass := snapshotClass.GetLabels()[StorageIDLabel]

		matched := sIDFromSnapClass == storageClass.GetLabels()[StorageIDLabel] &&
			snapshotClass.Driver == storageClass.Provisioner

		if matched {
			return &snapshotClass, nil
		}

		continue
	}

	return nil, nil
}

func (v *VRGInstance) validateAndGetStorageClass(scName *string, pvc *corev1.PersistentVolumeClaim,
) (*storagev1.StorageClass, error) {
	if scName == nil || *scName == "" {
		return nil, fmt.Errorf("missing storage class name for PVC %s/%s", pvc.GetNamespace(), pvc.GetName())
	}

	storageClass := &storagev1.StorageClass{}
	if err := v.reconciler.Get(v.ctx, types.NamespacedName{Name: *scName}, storageClass); err != nil {
		return nil, fmt.Errorf("failed to get the storageclass with name %s (%w)", *scName, err)
	}

	return storageClass, nil
}

func (v *VRGInstance) findPeerClassMatchingSC(
	storageClass *storagev1.StorageClass,
	peerClasses []ramendrv1alpha1.PeerClass,
	pvc *corev1.PersistentVolumeClaim,
) (*ramendrv1alpha1.PeerClass, error) {
	var peerClass *ramendrv1alpha1.PeerClass

	if _, ok := storageClass.GetLabels()[StorageIDLabel]; !ok {
		return nil, fmt.Errorf("label (%s) not found in storageClass for PVC %s", StorageIDLabel, pvc.Name)
	}

	for idx := range peerClasses {
		if storageClass.GetName() == peerClasses[idx].StorageClassName {
			peerClass = &peerClasses[idx]
		}
	}

	if peerClass == nil {
		msg := fmt.Sprintf("peerClass matching storageClass %s not found for PVC", storageClass.GetName())
		v.updatePVCDataReadyCondition(pvc.Namespace, pvc.Name, VRGConditionReasonPeerClassNotFound, msg)

		return nil, errors.New(msg)
	}

	if !slices.Contains(peerClass.StorageID, storageClass.GetLabels()[StorageIDLabel]) {
		return nil, fmt.Errorf("storageID mismatch between peerClass (%s) and StorageClass (%s)",
			peerClass.StorageID[0], storageClass.GetLabels()[StorageIDLabel])
	}

	/* Removed as a comment, as tests set the same sID across both SCs and hence in async we get len as 1 always!
	sIDLen := 0

	switch v.instance.Spec.Sync != nil {
	case true:
		sIDLen = 1
	case false:
		sIDLen = 2
	}

	if len(peerClass.StorageID) != sIDLen {
		return nil, fmt.Errorf("peerClass with insufficient storageIDs found %v %v", peerClass.StorageID, sIDLen)
	}*/

	return peerClass, nil
}

// finalizeVRG cleans up managed resources and removes the VRG finalizer for resource deletion
func (v *VRGInstance) processForDeletion() ctrl.Result {
	v.log.Info("Entering processing VolumeReplicationGroup for deletion")

	defer v.log.Info("Exiting processing VolumeReplicationGroup")

	if err := v.disownPVCs(); err != nil {
		v.log.Info("Disowning PVCs failed", "error", err)

		return ctrl.Result{Requeue: true}
	}

	if err := v.cleanupResources(); err != nil {
		v.log.Info("Cleanup owned resources failed", "error", err)

		return ctrl.Result{Requeue: true}
	}

	if !containsString(v.instance.ObjectMeta.Finalizers, vrgFinalizerName) {
		v.log.Info("Finalizer missing from resource", "finalizer", vrgFinalizerName)

		return ctrl.Result{}
	}

	if v.deleteVRGHandleMode(); v.result.Requeue {
		v.log.Info("Requeuing as reconciling VolumeReplication for deletion failed")

		return v.result
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

	util.ReportIfNotPresent(v.reconciler.eventRecorder, v.instance, corev1.EventTypeNormal,
		util.EventReasonDeleteSuccess, "Deletion Success")

	return ctrl.Result{}
}

// For now, async mode and sync mode can be enabled only in either or fashion
// and the function reconcileVRsForDeletion is capable of handling it for both.
// However, in the future, we may want to enable both modes at the same time
// and might call different functions for those modes. This function is in
// preparation of that need.
func (v *VRGInstance) deleteVRGHandleMode() {
	v.reconcileVRsForDeletion()
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

	// clusterDataProtected looks at the v.kubeObjectsProtected condition
	// to determine if the cluster data is protected. If it is nil, then it is
	// considered as success. So we should set it as false here and set it as
	// true if protection is not required or if protection is successful.
	v.kubeObjectsCaptureStatusFalse("KubeObjectsCaptureNotStarted", "Kube objects capture has not started")

	if err := v.pvcsDeselectedUnprotect(); err != nil {
		return v.dataError(err, "PVCs deselected unprotect failed", v.result.Requeue)
	}

	if v.shouldRestoreClusterData() {
		v.result.Requeue = true

		numOfRestoredRes, err := v.clusterDataRestore(&v.result)
		if err != nil {
			return v.clusterDataError(err, "Failed to restore PVs/PVCs", v.result)
		}

		// Save status and requeue if we restored any resources (PV/PVCs). Otherwise, continue
		if numOfRestoredRes != 0 {
			return v.updateVRGConditionsAndStatus(v.result)
		}
	}

	v.reconcileAsPrimary()

	if v.result.Requeue {
		return v.updateVRGConditionsAndStatus(v.result)
	}

	if v.shouldRestoreKubeObjects() {
		err := v.kubeObjectsRecover(&v.result)
		if err != nil {
			v.log.Info("Kube objects restore failed", "error", err)
			v.errorConditionLogAndSet(err, "Failed to restore kube objects", setVRGKubeObjectsErrorCondition)

			return v.updateVRGConditionsAndStatus(v.result)
		}

		// save status and requeue if kube objects are restored
		v.log.Info("Kube objects restored")
		setVRGKubeObjectsReadyCondition(&v.instance.Status.Conditions, v.instance.Generation, "Kube objects restored")
		v.result.Requeue = true

		return v.updateVRGConditionsAndStatus(v.result)
	}

	v.kubeObjectsProtectPrimary(&v.result)

	if v.result.Requeue {
		return v.updateVRGConditionsAndStatus(v.result)
	}

	v.vrgObjectProtect(&v.result)

	if v.result.Requeue {
		return v.updateVRGConditionsAndStatus(v.result)
	}

	// If requeue is false, then VRG was successfully processed as primary.
	// Hence the event to be generated is Success of type normal.
	// Expectation is that, if something failed and requeue is true, then
	// appropriate event might have been captured at the time of failure.
	if !v.result.Requeue {
		util.ReportIfNotPresent(v.reconciler.eventRecorder, v.instance, corev1.EventTypeNormal,
			util.EventReasonPrimarySuccess, "Primary Success")
	}

	return v.updateVRGConditionsAndStatus(v.result)
}

func (v *VRGInstance) shouldRestoreClusterData() bool {
	if v.instance.Spec.PrepareForFinalSync || v.instance.Spec.RunFinalSync {
		setVRGClusterDataReadyCondition(
			&v.instance.Status.Conditions,
			v.instance.Generation,
			"PV and PVC restore skipped, as VRG is orchestrating final sync",
		)

		return false
	}

	clusterDataReady := findCondition(v.instance.Status.Conditions, VRGConditionTypeClusterDataReady)
	if clusterDataReady == nil {
		return true
	}

	v.log.Info("ClusterDataReady condition",
		"status", clusterDataReady.Status,
		"reason", clusterDataReady.Reason,
		"message", clusterDataReady.Message,
		"observedGeneration", clusterDataReady.ObservedGeneration,
		"generation", v.instance.Generation,
	)

	if clusterDataReady.Status == metav1.ConditionTrue {
		if clusterDataReady.ObservedGeneration == v.instance.Generation {
			return false
		}

		// If generation is older, and reason is restored, then skip the restore. Reason is updated as Secondary to
		// ensure that a VRG that is changed from Secondary to Primary would perform the restore initially.
		// Also, update the condition such that a newer generation is recorded.
		if clusterDataReady.Reason == VRGConditionReasonClusterDataRestored {
			setVRGClusterDataReadyCondition(
				&v.instance.Status.Conditions,
				v.instance.Generation,
				"PV and PVC restore skipped, an older generation as Primary has already applied the restore",
			)

			return false
		}
	}

	return true
}

func (v *VRGInstance) shouldRestoreKubeObjects() bool {
	if v.instance.Spec.PrepareForFinalSync || v.instance.Spec.RunFinalSync {
		setVRGKubeObjectsReadyCondition(
			&v.instance.Status.Conditions,
			v.instance.Generation,
			"k8s resource restore skipped, as VRG is orchestrating final sync",
		)

		return false
	}

	KubeObjectsRestored := findCondition(v.instance.Status.Conditions, VRGConditionTypeKubeObjectsReady)
	if KubeObjectsRestored == nil {
		return true
	}

	v.log.Info("KubeObjectsReady condition",
		"status", KubeObjectsRestored.Status,
		"reason", KubeObjectsRestored.Reason,
		"message", KubeObjectsRestored.Message,
		"observedGeneration", KubeObjectsRestored.ObservedGeneration,
		"generation", v.instance.Generation,
	)

	if KubeObjectsRestored.Status == metav1.ConditionTrue {
		if KubeObjectsRestored.ObservedGeneration == v.instance.Generation {
			return false
		}

		// If generation is older, and reason is restored, then skip the restore. Reason is updated as Secondary to
		// ensure that a VRG that is changed from Secondary to Primary would perform the restore initially.
		// Also, update the condition such that a newer generation is recorded.
		if KubeObjectsRestored.Reason == VRGConditionReasonKubeObjectsRestored {
			setVRGKubeObjectsReadyCondition(
				&v.instance.Status.Conditions,
				v.instance.Generation,
				"k8s resource restore skipped, an older generation as Primary has already applied the restore",
			)

			return false
		}
	}

	return true
}

func (v *VRGInstance) reconcileAsPrimary() {
	var finalSyncPrepared struct {
		volSync bool
	}

	vrg := v.instance
	v.result.Requeue = v.reconcileVolSyncAsPrimary(&finalSyncPrepared.volSync)
	v.reconcileVolRepsAsPrimary()

	if vrg.Spec.PrepareForFinalSync {
		vrg.Status.PrepareForFinalSyncComplete = finalSyncPrepared.volSync
	}
}

func (v *VRGInstance) pvcsDeselectedUnprotect() error {
	log := v.log.WithName("PvcsDeselectedUnprotect")

	pvcsOwned, err := v.listPVCsOwnedByVrg()
	if err != nil {
		log.Error(err, "PVCs owned by VRG list")

		return err
	}

	pvcsVr := util.ObjectsMap(v.volRepPVCs...)
	pvcsVs := util.ObjectsMap(v.volSyncPVCs...)

	for i := range pvcsOwned.Items {
		pvc := pvcsOwned.Items[i]
		pvcNamespacedName := client.ObjectKeyFromObject(&pvc)
		log1 := log.WithValues("pvc", pvcNamespacedName.String())

		if _, ok := pvcsVr[pvcNamespacedName]; !ok {
			v.pvcUnprotectVolRep(pvc, log1.WithName("VolRep"))
		}

		if _, ok := pvcsVs[pvcNamespacedName]; !ok {
			v.pvcUnprotectVolSync(pvc, log1.WithName("VolSync"))
		}
	}

	v.cleanupProtectedPVCs(pvcsVr, pvcsVs, log)

	return nil
}

func (v *VRGInstance) cleanupProtectedPVCs(
	pvcsVr, pvcsVs map[client.ObjectKey]corev1.PersistentVolumeClaim, log logr.Logger,
) {
	if !v.ramenConfig.VolumeUnprotectionEnabled {
		log.Info("Volume unprotection disabled")

		return
	}

	if v.instance.Spec.Async != nil && !VolumeUnprotectionEnabledForAsyncVolRep {
		log.Info("Volume unprotection disabled for async mode")

		return
	}
	// clean up the PVCs that are part of protected pvcs but not in v.volReps and v.volSyncs
	protectedPVCsFiltered := make([]ramendrv1alpha1.ProtectedPVC, 0)

	for _, protectedPVC := range v.instance.Status.ProtectedPVCs {
		pvcNamespacedName := client.ObjectKey{Namespace: protectedPVC.Namespace, Name: protectedPVC.Name}

		if _, ok := pvcsVr[pvcNamespacedName]; ok {
			protectedPVCsFiltered = append(protectedPVCsFiltered, protectedPVC)

			continue
		}

		if _, ok := pvcsVs[pvcNamespacedName]; ok {
			protectedPVCsFiltered = append(protectedPVCsFiltered, protectedPVC)

			continue
		}
	}

	v.instance.Status.ProtectedPVCs = protectedPVCsFiltered
}

// processAsSecondary reconciles the current instance of VRG as secondary
func (v *VRGInstance) processAsSecondary() ctrl.Result {
	v.log.Info("Entering processing VolumeReplicationGroup as Secondary")

	defer v.log.Info("Exiting processing VolumeReplicationGroup")

	v.instance.Status.LastGroupSyncTime = nil

	if v.resetInitialStatusAsSecondary() {
		v.result.Requeue = true

		return v.result
	}

	result := v.reconcileAsSecondary()

	// If requeue is false, then VRG was successfully processed as Secondary.
	// Hence the event to be generated is Success of type normal.
	// Expectation is that, if something failed and requeue is true, then
	// appropriate event might have been captured at the time of failure.
	if !result.Requeue {
		util.ReportIfNotPresent(v.reconciler.eventRecorder, v.instance, corev1.EventTypeNormal,
			util.EventReasonSecondarySuccess, "Secondary Success")
	}

	return v.updateVRGConditionsAndStatus(result)
}

func (v *VRGInstance) reconcileAsSecondary() ctrl.Result {
	result := ctrl.Result{}
	result.Requeue = v.reconcileVolSyncAsSecondary() || result.Requeue
	result.Requeue = v.reconcileVolRepsAsSecondary() || result.Requeue

	// We already have the vrg.spec.state set to Secondary, so the user has been
	// asked to cleanup the resources and we cannot upload the kube resources
	// here. This final sync of kube resources should happen before the user is
	// asked to cleanup the resources. This bug will be fixed in the future
	// after we reconcile the volsync and volrep processes to be similar.
	// TODO: Do a final sync of kube resources at the same place where we do the
	// final sync of the volsync resources.

	// Clear the conditions only if there are no more work as secondary and the RDSpec is not empty.
	// Note: When using VolSync, we preserve the secondary and we need the status of the VRG to be
	// clean. In all other cases, the VRG will be deleted and we don't care about the its conditions.
	if !result.Requeue && len(v.instance.Spec.VolSync.RDSpec) > 0 {
		v.instance.Status.Conditions = []metav1.Condition{}
	}

	return result
}

// resetInitialStatusAsSecondary resets required initial conditions to start processing VRG as Secondary, if these are
// updated, VRG needs to be requeued for a reconcile to ensure the updates are preserved before further processing.
func (v *VRGInstance) resetInitialStatusAsSecondary() bool {
	clusterDataReady := findCondition(v.instance.Status.Conditions, VRGConditionTypeClusterDataReady)
	kubeObjectsReady := findCondition(v.instance.Status.Conditions, VRGConditionTypeKubeObjectsReady)

	update := false
	if clusterDataReady == nil || clusterDataReady.Reason != VRGConditionReasonClusterDataUnused {
		update = true
	}

	if kubeObjectsReady == nil || kubeObjectsReady.Reason != VRGConditionReasonClusterDataUnused {
		update = true
	}

	// Set the conditions to the current generation irresepective of update required
	setVRGClusterDataReadyConditionUnused(
		&v.instance.Status.Conditions,
		v.instance.Generation,
		"PV and PVC restore skipped as Secondary",
	)

	setVRGKubeObjectsReadyConditionUnused(
		&v.instance.Status.Conditions,
		v.instance.Generation,
		"k8s resource restore skipped as Secondary",
	)

	if update {
		v.updateVRGStatus(v.result)
	}

	return update
}

func (v *VRGInstance) invalid(err error, msg string, requeue bool) ctrl.Result {
	util.ReportIfNotPresent(v.reconciler.eventRecorder, v.instance, corev1.EventTypeWarning,
		util.EventReasonValidationFailed, err.Error())

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
	// Check if as Secondary things would be updated accordingly, should protectedPVC be cleared?
	// cleanupProtectedPVCs
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
			v.log.Info(fmt.Sprintf("Failed to update VRG status (%v/%s)",
				err, v.instance.Name))

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

// updateStatusState updates VRG status.State to the observed state, considering required conditions for cases:
//   - Volsync reports DataReady when VRG is Primary and ignores(nil) it when VRG is Secondary
//   - Volsync ignores(nil) DataProtected when VRG is Primary
//   - Volrep reports DataReady for both Primary and Secondary
//   - Volrep as Secondary is additionally only complete when it is either DataProtected (in the relocate action), or
//     it is DataProtected with replication reestablished (in the failover action). The latter is folded into DataReady
//     condition reporting by Volrep, thus requiring inspection of DataProtected for Volrep only when action is
//     relocate.
//
// status.State as Secondary for Volrep is a final state, i.e all PVCs are ensured as Secondary
// status.State as Secondary for Volsync may switch between true/false, as newer PVCs are protected
// status.State as Primary can switch between true/false, as new PVCs are protected (or PVCs are deleted), when
// workload is active on the cluster
// status.State as Primary is a final state, when VRG is rolled out initially before the workload is placed
// on the cluster
func (v *VRGInstance) updateStatusState() {
	dataReadyCondition := findCondition(v.instance.Status.Conditions, VRGConditionTypeDataReady)
	if dataReadyCondition.Status != metav1.ConditionTrue ||
		dataReadyCondition.ObservedGeneration != v.instance.Generation {
		v.instance.Status.State = ramendrv1alpha1.UnknownState

		return
	}

	StatusState := getStatusStateFromSpecState(v.instance.Spec.ReplicationState)
	if v.instance.Spec.ReplicationState == ramendrv1alpha1.Primary {
		v.instance.Status.State = StatusState

		return
	}

	v.updateStatusStateForSecondary()
}

// updateStatusStateForSecondary is a helper to handle cases where VRG desired state is Secondary and its
// status.State needs to be updated. See updateStatusState for more details.
func (v *VRGInstance) updateStatusStateForSecondary() {
	if v.instance.Spec.Action != ramendrv1alpha1.VRGActionRelocate {
		v.instance.Status.State = ramendrv1alpha1.SecondaryState

		return
	}

	dataProtectedCondition := findCondition(v.instance.Status.Conditions, VRGConditionTypeDataProtected)
	if dataProtectedCondition == nil {
		if len(v.volRepPVCs) == 0 {
			// VRG is exclusively using volsync
			v.instance.Status.State = ramendrv1alpha1.SecondaryState

			return
		}

		v.instance.Status.State = ramendrv1alpha1.UnknownState

		return
	}

	if dataProtectedCondition.Status == metav1.ConditionTrue &&
		dataProtectedCondition.ObservedGeneration == v.instance.Generation {
		v.instance.Status.State = ramendrv1alpha1.SecondaryState

		return
	}

	v.instance.Status.State = ramendrv1alpha1.UnknownState
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
		msg := fmt.Sprintf("merging %s condition", conditionName)
		v.log.Info(msg, "subconditions", subconditions)
		finalCondition := util.MergeConditions(setStatusCondition,
			&v.instance.Status.Conditions,
			[]string{VRGConditionReasonUnused},
			subconditions...)
		msg = fmt.Sprintf("updated %s status to %s", conditionName, finalCondition.Status)
		v.log.Info(msg, "finalCondition", finalCondition)
	}

	var volSyncDataReady, volSyncDataProtected, volSyncClusterDataProtected *metav1.Condition
	if v.instance.Spec.Sync == nil {
		volSyncDataReady = v.aggregateVolSyncDataReadyCondition()
		volSyncDataProtected, volSyncClusterDataProtected = v.aggregateVolSyncDataProtectedConditions()
	}

	logAndSet(VRGConditionTypeDataReady,
		volSyncDataReady,
		v.aggregateVolRepDataReadyCondition(),
	)

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

func (v *VRGInstance) vrgReadyStatus(reason string) *metav1.Condition {
	v.log.Info("Marking VRG ready with replicating reason", "reason", reason)

	if v.instance.Spec.ReplicationState == ramendrv1alpha1.Secondary {
		msg := "PVCs in the VolumeReplicationGroup group are replicating"
		if reason == VRGConditionReasonUnused {
			msg = "PVC protection as secondary is complete, or no PVCs needed protection using VolumeReplication scheme"
			if v.instance.Spec.Sync != nil {
				msg = "PVC protection as secondary is complete, or no PVCs needed protection"
			}
		} else {
			reason = VRGConditionReasonReplicating
		}

		return newVRGDataReplicatingCondition(v.instance.Generation, reason, msg)
	}

	// VRG as primary
	msg := "PVCs in the VolumeReplicationGroup are ready for use"
	if reason == VRGConditionReasonUnused {
		msg = "No PVCs are protected using VolumeReplication scheme"
		if v.instance.Spec.Sync != nil {
			msg = "No PVCs are protected, no PVCs found matching the selector"
		}
	}

	return newVRGAsPrimaryReadyCondition(v.instance.Generation, reason, msg)
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

// isVRGReasonError returns true if the passed in VRG condition reason matches any errors reported as the Reason
func isVRGReasonError(condition *metav1.Condition) bool {
	return condition.Reason == VRGConditionReasonError ||
		condition.Reason == VRGConditionReasonErrorUnknown ||
		condition.Reason == VRGConditionReasonUploadError ||
		condition.Reason == VRGConditionReasonClusterDataAnnotationFailed
}

func (v *VRGInstance) s3StoreAccessorsGet() {
	vrg := v.instance
	v.s3StoreAccessors = s3StoreAccessorsGet(
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

func vrgInAdminNamespace(vrg *ramendrv1alpha1.VolumeReplicationGroup, ramenConfig *ramendrv1alpha1.RamenConfig) bool {
	vrgAdminNamespaceNames := vrgAdminNamespaceNames(*ramenConfig)

	return slices.Contains(vrgAdminNamespaceNames, vrg.Namespace)
}

func filterVRGDependentObjects(reader client.Reader, obj client.Object, log logr.Logger) []reconcile.Request {
	req := []reconcile.Request{}

	var vrgs ramendrv1alpha1.VolumeReplicationGroupList

	err := reader.List(context.TODO(), &vrgs)
	if err != nil {
		log.Error(err, "Failed to get list of VolumeReplicationGroup resources")

		return req
	}

	for _, vrg := range vrgs.Items {
		log1 := log.WithValues("vrg", vrg.Name)

		if vrg.Spec.ProtectedNamespaces == nil || len(*vrg.Spec.ProtectedNamespaces) == 0 {
			continue
		}

		if slices.Contains(*vrg.Spec.ProtectedNamespaces, obj.GetNamespace()) {
			log1.Info("Found VolumeReplicationGroup with matching namespace",
				"vrg", vrg.Name, "namespace", obj.GetNamespace())

			req = append(req, reconcile.Request{NamespacedName: types.NamespacedName{
				Name:      vrg.Name,
				Namespace: vrg.Namespace,
			}})

			break
		}
	}

	return req
}

func (r *VolumeReplicationGroupReconciler) VRMapFunc(ctx context.Context, obj client.Object) []reconcile.Request {
	log := ctrl.Log.WithName("vrmap").WithName("VolumeReplicationGroup")

	vr, ok := obj.(*volrep.VolumeReplication)
	if !ok {
		log.Info("map function received non-vr resource")

		return []reconcile.Request{}
	}

	return filterVRGDependentObjects(r.Client, obj,
		log.WithValues("vr", types.NamespacedName{Name: vr.Name, Namespace: vr.Namespace}))
}

func (r *VolumeReplicationGroupReconciler) RDMapFunc(ctx context.Context, obj client.Object) []reconcile.Request {
	log := ctrl.Log.WithName("rdmap").WithName("VolumeReplicationGroup")

	rd, ok := obj.(*volsyncv1alpha1.ReplicationDestination)
	if !ok {
		log.Info("map function received not a replication destination resource")

		return []reconcile.Request{}
	}

	return filterVRGDependentObjects(r.Client, obj,
		log.WithValues("rd", types.NamespacedName{Name: rd.Name, Namespace: rd.Namespace}))
}

func (r *VolumeReplicationGroupReconciler) RSMapFunc(ctx context.Context, obj client.Object) []reconcile.Request {
	log := ctrl.Log.WithName("rsmap").WithName("VolumeReplicationGroup")

	rs, ok := obj.(*volsyncv1alpha1.ReplicationSource)
	if !ok {
		log.Info("map function received not a replication source resource")

		return []reconcile.Request{}
	}

	return filterVRGDependentObjects(r.Client, obj,
		log.WithValues("rs", types.NamespacedName{Name: rs.Name, Namespace: rs.Namespace}))
}

func (r *VolumeReplicationGroupReconciler) addVolsyncOwnsAndWatches(ctrlBuilder *builder.Builder) *builder.Builder {
	ctrlBuilder.Owns(&volsyncv1alpha1.ReplicationDestination{}).
		Owns(&volsyncv1alpha1.ReplicationSource{}).
		Owns(&ramendrv1alpha1.ReplicationGroupSource{}).
		Owns(&ramendrv1alpha1.ReplicationGroupDestination{}).
		Watches(&volsyncv1alpha1.ReplicationDestination{},
			handler.EnqueueRequestsFromMapFunc(r.RDMapFunc),
			builder.WithPredicates(util.CreateOrDeleteOrResourceVersionUpdatePredicate{}),
		).
		Watches(&volsyncv1alpha1.ReplicationSource{},
			handler.EnqueueRequestsFromMapFunc(r.RSMapFunc),
			builder.WithPredicates(util.CreateOrDeleteOrResourceVersionUpdatePredicate{}),
		)

	return ctrlBuilder
}

func (r *VolumeReplicationGroupReconciler) addKubeObjectsOwnsAndWatches(ctrlBuilder *builder.Builder) *builder.Builder {
	r.Log.Info("Kube object protection enabled; watch kube objects requests")

	// Find if velero CRDs are present in the cluster
	veleroCRDs := []string{
		"backups.velero.io",
		"backupstoragelocations.velero.io",
		"restores.velero.io",
	}

	missingCRDs := false

	for _, crd := range veleroCRDs {
		installedCRD := &apiextensionsv1.CustomResourceDefinition{}
		if err := r.APIReader.Get(context.TODO(), types.NamespacedName{Name: crd}, installedCRD); err != nil {
			r.Log.Info("Cannot fetch Velero CRD", "CRD", crd, "error", err)

			missingCRDs = true
		}
	}

	if missingCRDs {
		r.Log.Info("Cannot fetch Velero CRD; Kubernetes object protection won't work unless Velero/OADP is installed")

		return ctrlBuilder
	}

	kubeObjectsRequestsWatch(ctrlBuilder, r.Scheme, r.kubeObjects)

	// watch for recipe objects
	objectToReconcileRequestsMapper := objectToReconcileRequestsMapper{reader: r.Client, log: ctrl.Log}
	recipesWatch(ctrlBuilder, objectToReconcileRequestsMapper)

	r.veleroCRsAreWatched = true

	return ctrlBuilder
}
