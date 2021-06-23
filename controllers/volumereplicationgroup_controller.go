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
	"net/url"
	"reflect"
	"regexp"

	"github.com/go-logr/logr"

	volrep "github.com/csi-addons/volume-replication-operator/api/v1alpha1"
	volrepController "github.com/csi-addons/volume-replication-operator/controllers"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
)

// VolumeReplicationGroupReconciler reconciles a VolumeReplicationGroup object
type VolumeReplicationGroupReconciler struct {
	client.Client
	Log            logr.Logger
	ObjStoreGetter ObjectStoreGetter
	Scheme         *runtime.Scheme
}

// SetupWithManager sets up the controller with the Manager.
func (r *VolumeReplicationGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	pvcPredicate := pvcPredicateFunc()
	pvcMapFun := handler.EnqueueRequestsFromMapFunc(handler.MapFunc(func(obj client.Object) []reconcile.Request {
		log := ctrl.Log.WithName("pvcmap").WithName("VolumeReplicationGroup")

		pvc, ok := obj.(*corev1.PersistentVolumeClaim)
		if !ok {
			log.Info("PersistentVolumeClaim(PVC) map function received non-PVC resource")

			return []reconcile.Request{}
		}

		return filterPVC(mgr, pvc,
			log.WithValues("pvc", types.NamespacedName{Name: pvc.Name, Namespace: pvc.Namespace}))
	}))

	r.Log.Info("Adding VolumeReplicationGroup and PersistentVolumeClaims controllers")

	return ctrl.NewControllerManagedBy(mgr).
		For(&ramendrv1alpha1.VolumeReplicationGroup{}).
		Watches(&source.Kind{Type: &corev1.PersistentVolumeClaim{}}, pvcMapFun, builder.WithPredicates(pvcPredicate)).
		Owns(&volrep.VolumeReplication{}).
		Complete(r)
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
	log logr.Logger) bool {
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

func filterPVC(mgr manager.Manager, pvc *corev1.PersistentVolumeClaim, log logr.Logger) []reconcile.Request {
	req := []reconcile.Request{}

	var vrgs ramendrv1alpha1.VolumeReplicationGroupList

	listOptions := []client.ListOption{
		client.InNamespace(pvc.Namespace),
	}

	// decide if reconcile request needs to be sent to the
	// corresponding VolumeReplicationGroup CR by:
	// - whether there is a VolumeReplicationGroup CR in the namespace
	//   to which the the pvc belongs to.
	// - whether the labels on pvc match the label selectors from
	//    VolumeReplicationGroup CR.
	err := mgr.GetClient().List(context.TODO(), &vrgs, listOptions...)
	if err != nil {
		log.Error(err, "Failed to get list of VolumeReplicationGroup resources")

		return []reconcile.Request{}
	}

	for _, vrg := range vrgs.Items {
		vrgLabelSelector := vrg.Spec.PVCSelector
		selector, err := metav1.LabelSelectorAsSelector(&vrgLabelSelector)
		// continue if we fail to get the labels for this object hoping
		// that pvc might actually belong to  some other vrg instead of
		// this. If not found, then reconcile request would not be sent
		if err != nil {
			log.Error(err, "Failed to get the label selector from VolumeReplicationGroup", "vrgName", vrg.Name)

			continue
		}

		if selector.Matches(labels.Set(pvc.GetLabels())) {
			log.Info("Found VolumeReplicationGroup with matching labels",
				"vrg", vrg.Name, "labeled", selector)

			req = append(req, reconcile.Request{NamespacedName: types.NamespacedName{Name: vrg.Name, Namespace: vrg.Namespace}})
		}
	}

	return req
}

// nolint: lll // disabling line length linter
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=volumereplicationgroups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=volumereplicationgroups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=volumereplicationgroups/finalizers,verbs=update
// +kubebuilder:rbac:groups=replication.storage.openshift.io,resources=volumereplications,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=replication.storage.openshift.io,resources=volumereplicationclasses,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=core,resources=persistentvolumes,verbs=get;list;watch;update;patch

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
	log := r.Log.WithValues("VolumeReplicationGroup", req.NamespacedName)

	log.Info("Entering reconcile loop")

	defer log.Info("Exiting reconcile loop")

	v := VRGInstance{
		reconciler:    r,
		ctx:           ctx,
		log:           log,
		instance:      &ramendrv1alpha1.VolumeReplicationGroup{},
		pvcList:       &corev1.PersistentVolumeClaimList{},
		replClassList: &volrep.VolumeReplicationClassList{},
	}

	// Fetch the VolumeReplicationGroup instance
	if err := r.Get(ctx, req.NamespacedName, v.instance); err != nil {
		if errors.IsNotFound(err) {
			log.Info("Resource not found")

			return ctrl.Result{}, nil
		}

		log.Error(err, "Failed to get resource")

		return ctrl.Result{}, fmt.Errorf("failed to reconcile VolumeReplicationGroup (%v), %w",
			req.NamespacedName, err)
	}

	return v.processVRG()
}

type VRGInstance struct {
	reconciler    *VolumeReplicationGroupReconciler
	ctx           context.Context
	log           logr.Logger
	instance      *ramendrv1alpha1.VolumeReplicationGroup
	pvcList       *corev1.PersistentVolumeClaimList
	replClassList *volrep.VolumeReplicationClassList
	vrcUpdated    bool
}

const (
	// Finalizers
	vrgFinalizerName        = "volumereplicationgroups.ramendr.openshift.io/vrg-protection"
	pvcVRFinalizerProtected = "volumereplicationgroups.ramendr.openshift.io/pvc-vr-protection"
	pvcInUse                = "kubernetes.io/pvc-protection"

	// Annotations
	pvcVRAnnotationProtectedKey   = "volumereplicationgroups.ramendr.openshift.io/vr-protected"
	pvcVRAnnotationProtectedValue = "protected"
	pvVRAnnotationRetentionKey    = "volumereplicationgroups.ramendr.openshift.io/vr-retained"
	pvVRAnnotationRetentionValue  = "retained"
)

func (v *VRGInstance) processVRG() (ctrl.Result, error) {
	v.initializeStatus()

	if err := v.validateVRGState(); err != nil {
		msg := "VolumeReplicationGroup state is invalid"
		setVRGErrorCondition(&v.instance.Status.Conditions, v.instance.Generation, msg)

		if err = v.updateVRGStatus(false); err != nil {
			v.log.Error(err, "Status update failed")
			// Since updating status failed, reconcile
			return ctrl.Result{Requeue: true}, nil
		}
		// No requeue, as there is no reconcile till user changes desired spec to a valid value
		return ctrl.Result{}, nil
	}

	if err := v.updatePVCList(); err != nil {
		v.log.Error(err, "Failed to update PersistentVolumeClaims for resource")

		msg := "Failed to get list of pvcs"
		setVRGErrorCondition(&v.instance.Status.Conditions, v.instance.Generation, msg)

		if err = v.updateVRGStatus(false); err != nil {
			v.log.Error(err, "VRG Status update failed")
		}

		return ctrl.Result{Requeue: true}, nil
	}

	if err := v.validateSchedule(); err != nil {
		v.log.Error(err, "Failed to validate the scheduling interval")

		msg := "Failed to validate scheduling interval"
		setVRGErrorCondition(&v.instance.Status.Conditions, v.instance.Generation, msg)

		if err = v.updateVRGStatus(false); err != nil {
			v.log.Error(err, "VRG Status update failed")

			return ctrl.Result{Requeue: true}, nil
		}

		// No requeue, as there is no reconcile till user changes desired spec to a valid value
		return ctrl.Result{}, nil
	}

	v.log = v.log.WithName("vrginstance").WithValues("State", v.instance.Spec.ReplicationState)

	switch {
	case !v.instance.GetDeletionTimestamp().IsZero():
		v.log = v.log.WithValues("Finalize", true)

		return v.processForDeletion()
	case v.instance.Spec.ReplicationState == ramendrv1alpha1.Primary:
		return v.processAsPrimary()
	default: // Secondary, not primary and not deleted
		return v.processAsSecondary()
	}
}

// TODO: Currently DRPC and VRG both validate the schedule. However,
//       there is a difference. While DRPC validates the scheduling
//       interval for DRPolicy resource, VRG validates for itself.
//       Once DRPolicy reconciler is implemented, perhaps validating
//       schedule can be moved to "utils" package and both VRG and
//       DRPolicy can consume validateSchedule from utils package.
func (v *VRGInstance) validateSchedule() error {
	v.log.Info("Validating schedule")

	if v.instance.Spec.SchedulingInterval == "" {
		return fmt.Errorf("scheduling interval empty (%s)", v.instance.Name)
	}

	re := regexp.MustCompile(`^\d+[mhd]$`)
	if !re.MatchString(v.instance.Spec.SchedulingInterval) {
		return fmt.Errorf("failed to match the scheduling interval string %s", v.instance.Spec.SchedulingInterval)
	}

	return nil
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

func (v *VRGInstance) initializeStatus() {
	// create ProtectedPVCs map for status
	if v.instance.Status.ProtectedPVCs == nil {
		v.instance.Status.ProtectedPVCs = make(ramendrv1alpha1.ProtectedPVCMap)

		// Set the VRG available condition.status to unknown as nothing is
		// known at this point
		msg := "Initializing VolumeReplicationGroup"
		setVRGInitialCondition(&v.instance.Status.Conditions, v.instance.Generation, msg)
	}
}

// updatePVCList fetches and updates the PVC list to process for the current instance of VRG
func (v *VRGInstance) updatePVCList() error {
	labelSelector := v.instance.Spec.PVCSelector

	v.log.Info("Fetching PersistentVolumeClaims", "labeled", labels.Set(labelSelector.MatchLabels))
	listOptions := []client.ListOption{
		client.InNamespace(v.instance.Namespace),
		client.MatchingLabels(labelSelector.MatchLabels),
	}

	if err := v.reconciler.List(v.ctx, v.pvcList, listOptions...); err != nil {
		v.log.Error(err, "Failed to list PersistentVolumeClaims",
			"labeled", labels.Set(labelSelector.MatchLabels))

		return fmt.Errorf("failed to list PersistentVolumeClaims, %w", err)
	}

	v.log.Info("Found PersistentVolumeClaims", "count", len(v.pvcList.Items))

	return nil
}

func (v *VRGInstance) updateReplicationClassList() error {
	labelSelector := v.instance.Spec.ReplicationClassSelector

	v.log.Info("Fetching VolumeReplicationClass", "labeled", labels.Set(labelSelector.MatchLabels))
	listOptions := []client.ListOption{
		client.MatchingLabels(labelSelector.MatchLabels),
	}

	if err := v.reconciler.List(v.ctx, v.replClassList, listOptions...); err != nil {
		v.log.Error(err, "Failed to list Replication Classes",
			"labeled", labels.Set(labelSelector.MatchLabels))

		return fmt.Errorf("failed to list Replication Classes, %w", err)
	}

	v.log.Info("Found Replication Classes", "count", len(v.replClassList.Items))

	return nil
}

// finalizeVRG cleans up managed resources and removes the VRG finalizer for resource deletion
func (v *VRGInstance) processForDeletion() (ctrl.Result, error) {
	v.log.Info("Entering processing VolumeReplicationGroup")

	defer v.log.Info("Exiting processing VolumeReplicationGroup")

	if !containsString(v.instance.ObjectMeta.Finalizers, vrgFinalizerName) {
		v.log.Info("Finalizer missing from resource", "finalizer", vrgFinalizerName)

		return ctrl.Result{}, nil
	}

	if v.reconcileVRsForDeletion() {
		v.log.Info("Requeuing as reconciling VolumeReplication for deletion failed")

		return ctrl.Result{Requeue: true}, nil
	}

	if err := v.removeFinalizer(vrgFinalizerName); err != nil {
		v.log.Info("Failed to remove finalizer", "finalizer", vrgFinalizerName, "errorValue", err)

		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
}

// reconcileVRsForDeletion cleans up VR resources managed by VRG and also cleans up changes made to PVCs
// TODO: Currently removes VR requests unconditionally, needs to ensure it is managed by VRG
func (v *VRGInstance) reconcileVRsForDeletion() bool {
	requeue := false

	for idx := range v.pvcList.Items {
		pvc := &v.pvcList.Items[idx]
		pvcNamespacedName := types.NamespacedName{Name: pvc.Name, Namespace: pvc.Namespace}
		log := v.log.WithValues("pvc", pvcNamespacedName.String())

		requeueResult, skip := v.preparePVCForVRProtection(pvc, log)
		if requeueResult {
			requeue = true

			continue
		}

		if skip {
			continue
		}

		if v.reconcileVRForDeletion(pvc, log) {
			requeue = true

			continue
		}

		log.Info("Successfully processed VolumeReplication for PersistentVolumeClaim")
	}

	return requeue
}

func (v *VRGInstance) reconcileVRForDeletion(pvc *corev1.PersistentVolumeClaim, log logr.Logger) bool {
	const requeue bool = true

	pvcNamespacedName := types.NamespacedName{Name: pvc.Name, Namespace: pvc.Namespace}

	if v.instance.Spec.ReplicationState == ramendrv1alpha1.Secondary {
		requeueResult, skip := v.reconcileVRAsSecondary(pvc, log)
		if requeueResult {
			log.Info("Requeuing due to failure in reconciling VolumeReplication resource as secondary")

			return requeue
		}

		if skip {
			log.Info("Skipping further processing of VolumeReplication resource as it is not ready")

			return !requeue
		}
	} else if err := v.processVRAsPrimary(pvcNamespacedName, log); err != nil {
		log.Info("Requeuing due to failure in getting or creating VolumeReplication resource for PersistentVolumeClaim",
			"errorValue", err)

		return requeue
	}

	if err := v.preparePVCForVRDeletion(pvc, log); err != nil {
		log.Info("Requeuing due to failure in preparing PersistentVolumeClaim for VolumeReplication deletion",
			"errorValue", err)

		return requeue
	}

	if err := v.deleteVR(pvcNamespacedName, log); err != nil {
		log.Info("Requeuing due to failure in finalizing VolumeReplication resource for PersistentVolumeClaim",
			"errorValue", err)

		return requeue
	}

	return !requeue
}

// removeFinalizer removes VRG finalizer form the resource
func (v *VRGInstance) removeFinalizer(finalizer string) error {
	v.instance.ObjectMeta.Finalizers = removeString(v.instance.ObjectMeta.Finalizers, finalizer)
	if err := v.reconciler.Update(v.ctx, v.instance); err != nil {
		v.log.Error(err, "Failed to remove finalizer", "finalizer", finalizer)

		return fmt.Errorf("failed to remove finalizer from VolumeReplicationGroup resource (%s/%s), %w",
			v.instance.Namespace, v.instance.Name, err)
	}

	return nil
}

// processAsPrimary reconciles the current instance of VRG as primary
func (v *VRGInstance) processAsPrimary() (ctrl.Result, error) {
	v.log.Info("Entering processing VolumeReplicationGroup")

	defer v.log.Info("Exiting processing VolumeReplicationGroup")

	if err := v.addFinalizer(vrgFinalizerName); err != nil {
		v.log.Info("Failed to add finalizer", "finalizer", vrgFinalizerName, "errorValue", err)

		msg := "Failed to add finalizer to VolumeReplicationGroup"
		setVRGErrorCondition(&v.instance.Status.Conditions, v.instance.Generation, msg)

		if err = v.updateVRGStatus(false); err != nil {
			v.log.Error(err, "VRG Status update failed")
		}

		return ctrl.Result{Requeue: true}, nil
	}

	requeue := false
	requeue = v.reconcileVRsAsPrimary()

	if err := v.updateVRGStatus(true); err != nil {
		requeue = true
	}

	if requeue {
		v.log.Info("Requeuing resource")

		return ctrl.Result{Requeue: requeue}, nil
	}

	return ctrl.Result{}, nil
}

// reconcileVRsAsPrimary creates/updates VolumeReplication CR for each pvc
// from pvcList. If it fails (even for one pvc), then requeue is set to true.
func (v *VRGInstance) reconcileVRsAsPrimary() bool {
	requeue := false

	for idx := range v.pvcList.Items {
		pvc := &v.pvcList.Items[idx]
		pvcNamespacedName := types.NamespacedName{Name: pvc.Name, Namespace: pvc.Namespace}
		log := v.log.WithValues("pvc", pvcNamespacedName.String())

		requeueResult, skip := v.preparePVCForVRProtection(pvc, log)
		if requeueResult {
			requeue = true

			continue
		}

		if skip {
			continue
		}

		if err := v.processVRAsPrimary(pvcNamespacedName, log); err != nil {
			log.Info("Requeuing due to failure in getting or creating VolumeReplication resource for PersistentVolumeClaim",
				"errorValue", err)

			requeue = true

			continue
		}

		log.Info("Successfully processed VolumeReplication for PersistentVolumeClaim")
	}

	return requeue
}

// processAsSecondary reconciles the current instance of VRG as secondary
func (v *VRGInstance) processAsSecondary() (ctrl.Result, error) {
	v.log.Info("Entering processing VolumeReplicationGroup")

	defer v.log.Info("Exiting processing VolumeReplicationGroup")

	if err := v.addFinalizer(vrgFinalizerName); err != nil {
		v.log.Info("Failed to add finalizer", "finalizer", vrgFinalizerName, "errorValue", err)

		msg := "Failed to add finalizer to VolumeReplicationGroup"
		setVRGErrorCondition(&v.instance.Status.Conditions, v.instance.Generation, msg)

		if err = v.updateVRGStatus(false); err != nil {
			v.log.Error(err, "VRG Status update failed")
		}

		return ctrl.Result{Requeue: true}, nil
	}

	requeue := v.reconcileVRsAsSecondary()

	if err := v.updateVRGStatus(true); err != nil {
		requeue = true
	}

	if requeue {
		v.log.Info("Requeuing resource")

		return ctrl.Result{Requeue: requeue}, nil
	}

	return ctrl.Result{}, nil
}

// reconcileVRsAsSecondary reconciles VolumeReplication resources for the VRG as secondary
func (v *VRGInstance) reconcileVRsAsSecondary() bool {
	requeue := false

	for idx := range v.pvcList.Items {
		pvc := &v.pvcList.Items[idx]
		pvcNamespacedName := types.NamespacedName{Name: pvc.Name, Namespace: pvc.Namespace}
		log := v.log.WithValues("pvc", pvcNamespacedName.String())

		requeueResult, skip := v.preparePVCForVRProtection(pvc, log)
		if requeueResult {
			requeue = true

			continue
		}

		if skip {
			continue
		}

		requeueResult, skip = v.reconcileVRAsSecondary(pvc, log)
		if requeueResult {
			requeue = true

			continue
		}

		if skip {
			continue
		}

		log.Info("Successfully processed VolumeReplication for PersistentVolumeClaim")
	}

	return requeue
}

func (v *VRGInstance) reconcileVRAsSecondary(pvc *corev1.PersistentVolumeClaim, log logr.Logger) (bool, bool) {
	const (
		requeue bool = true
		skip    bool = true
	)

	if !v.isPVCReadyForSecondary(pvc, log) {
		// 1) Dont requeue as a reconcile would be
		//    triggered when the events that indicate
		//    pvc being ready are caught by predicate.
		// 2) skip as this pvc is not ready for marking
		//    VolRep as secondary. Set the conditions to
		//    PVCProgressing.
		return !requeue, skip
	}

	pvcNamespacedName := types.NamespacedName{Name: pvc.Name, Namespace: pvc.Namespace}
	if err := v.processVRAsSecondary(pvcNamespacedName, log); err != nil {
		log.Info("Requeuing due to failure in getting or creating VolumeReplication resource for PersistentVolumeClaim",
			"errorValue", err)

		// Needs a requeue. And further processing of
		// VolRep can be skipped as processVRAsSecondary
		// failed.
		return requeue, skip
	}

	return !requeue, !skip
}

// isPVCReadyForSecondary checks if a PVC is ready to be marked as Secondary
func (v *VRGInstance) isPVCReadyForSecondary(pvc *corev1.PersistentVolumeClaim, log logr.Logger) bool {
	const ready bool = true

	// If PVC is not being deleted, it is not ready for Secondary
	if pvc.GetDeletionTimestamp().IsZero() {
		log.Info("VolumeReplication cannot become Secondary, as its PersistentVolumeClaim is not marked for deletion")

		msg := "PVC not being deleted. Not ready to become Secondary"
		v.updateProtectedPVCCondition(pvc.Name, PVCProgressing, msg)

		return !ready
	}

	// If PVC is still in use, it is not ready for Secondary
	if containsString(pvc.ObjectMeta.Finalizers, pvcInUse) {
		log.Info("VolumeReplication cannot become Secondary, as its PersistentVolumeClaim is still in use")

		msg := "PVC still in use"
		v.updateProtectedPVCCondition(pvc.Name, PVCProgressing, msg)

		return !ready
	}

	return ready
}

// preparePVCForVRProtection processes prerequisites of any PVC that needs VR protection. It returns
// a requeue if preparation failed, and returns skip if PVC can be skipped for VR protection
func (v *VRGInstance) preparePVCForVRProtection(pvc *corev1.PersistentVolumeClaim,
	log logr.Logger) (bool, bool) {
	const (
		requeue bool = true
		skip    bool = true
	)

	// if PVC protection is complete, return
	if pvc.Annotations[pvcVRAnnotationProtectedKey] == pvcVRAnnotationProtectedValue {
		return !requeue, !skip
	}

	// Dont requeue. There will be a reconcile request when predicate sees that pvc is ready.
	if skipResult, msg := skipPVC(pvc, log); skipResult {
		// @msg should not be nil as the decision is to skip the pvc.
		// msg should contain info on why that decision was made.
		if msg == "" {
			msg = "PVC not ready"
		}
		// Since pvc is skipped, mark the condition for the PVC as progressing. Even for
		// deletion this applies where if the VR protection finalizer is absent for pvc and
		// it is being deleted.
		v.updateProtectedPVCCondition(pvc.Name, PVCProgressing, msg)

		return !requeue, skip
	}

	return v.protectPVC(pvc, log)
}

func (v *VRGInstance) protectPVC(pvc *corev1.PersistentVolumeClaim,
	log logr.Logger) (bool, bool) {
	const (
		requeue bool = true
		skip    bool = true
	)
	// Add VR finalizer to PVC for deletion protection
	if err := v.addProtectedFinalizerToPVC(pvc, log); err != nil {
		log.Info("Requeuing, as adding PersistentVolumeClaim finalizer failed", "errorValue", err)

		msg := "Failed to add Protected Finalizer to PVC"
		v.updateProtectedPVCCondition(pvc.Name, PVCError, msg)

		return requeue, !skip
	}

	if err := v.retainPVForPVC(*pvc, log); err != nil { // Change PV `reclaimPolicy` to "Retain"
		log.Info("Requeuing, as retaining PersistentVolume failed", "errorValue", err)

		msg := "Failed to retain PV for PVC"
		v.updateProtectedPVCCondition(pvc.Name, PVCError, msg)

		return requeue, !skip
	}

	// If faulted post backing up PV but prior to VR creation, PVC on a failover will fail to attach,
	// and it is better to not have silent data loss, but be explicit on the failure.
	if err := v.uploadPV(*pvc); err != nil {
		log.Info("Requeuing, as uploading PersistentVolume failed", "errorValue", err)

		msg := "Failed to upload PV metadata"
		v.updateProtectedPVCCondition(pvc.Name, PVCError, msg)

		return requeue, !skip
	}

	// Annotate that PVC protection is complete, skip if being deleted
	if pvc.GetDeletionTimestamp().IsZero() {
		if err := v.addProtectedAnnotationForPVC(pvc, log); err != nil {
			log.Info("Requeuing, as annotating PersistentVolumeClaim failed", "errorValue", err)

			msg := "Failed to add protected annotatation to PVC"
			v.updateProtectedPVCCondition(pvc.Name, PVCError, msg)

			return requeue, !skip
		}
	}

	return !requeue, !skip
}

// This function indicates whether to proceed with the pvc processing
// or not. It mainly checks the following things.
// - Whether pvc is bound or not. If not bound, then no need to
//   process the pvc any further. It can be skipped until it is ready.
// - Whether the pvc is being deleted and VR protection finalizer is
//   not there. If the finalizer is there, then VolumeReplicationGroup
//   need to remove the finalizer for the pvc being deleted. However,
//   if the finalizer is not there, then no need to process the pvc
//   any further and it can be skipped. The pvc will go away eventually.
func skipPVC(pvc *corev1.PersistentVolumeClaim, log logr.Logger) (bool, string) {
	if pvc.Status.Phase != corev1.ClaimBound {
		log.Info("Skipping handling of VR as PersistentVolumeClaim is not bound", "pvcPhase", pvc.Status.Phase)

		msg := "PVC not bound yet"
		// v.updateProtectedPVCCondition(pvc.Name, PVCProgressing, msg)

		return true, msg
	}

	return isPVCDeletedAndNotProtected(pvc, log)
}

func isPVCDeletedAndNotProtected(pvc *corev1.PersistentVolumeClaim, log logr.Logger) (bool, string) {
	// If PVC deleted but not yet protected with a finalizer, skip it!
	if !containsString(pvc.Finalizers, pvcVRFinalizerProtected) && !pvc.GetDeletionTimestamp().IsZero() {
		log.Info("Skipping PersistentVolumeClaim, as it is marked for deletion and not yet protected")

		msg := "Skipping pvc marked for deletion"
		// v.updateProtectedPVCCondition(pvc.Name, PVCProgressing, msg)

		return true, msg
	}

	return false, ""
}

// preparePVCForVRDeletion
func (v *VRGInstance) preparePVCForVRDeletion(pvc *corev1.PersistentVolumeClaim,
	log logr.Logger) error {
	// If PVC does not have the VR finalizer we are done
	if !containsString(pvc.Finalizers, pvcVRFinalizerProtected) {
		return nil
	}

	// Change PV `reclaimPolicy` back to stored state
	if err := v.undoPVRetentionForPVC(*pvc, log); err != nil {
		return err
	}

	// TODO: Delete the PV from the backing store? But when is it safe to do so?
	// We can delete the PV when VRG (and hence VR) is being deleted as primary, as that is when the
	// application is finally being undeployed, and also the PV would be garbage collected.

	// Remove VR finalizer from PVC and the annotation (PVC maybe left behind, so remove the annotation)
	return v.removeProtectedFinalizerFromPVC(pvc, log)
}

// retainPVForPVC updates the PV reclaim policy to retain for a given PVC
func (v *VRGInstance) retainPVForPVC(pvc corev1.PersistentVolumeClaim, log logr.Logger) error {
	// Get PV bound to PVC
	pv := &corev1.PersistentVolume{}
	pvObjectKey := client.ObjectKey{
		Name: pvc.Spec.VolumeName,
	}

	if err := v.reconciler.Get(v.ctx, pvObjectKey, pv); err != nil {
		log.Error(err, "Failed to get PersistentVolume", "volumeName", pvc.Spec.VolumeName)

		return fmt.Errorf("failed to get PersistentVolume resource (%s) for"+
			" PersistentVolumeClaim resource (%s/%s) belonging to VolumeReplicationGroup (%s/%s), %w",
			pvc.Spec.VolumeName, pvc.Namespace, pvc.Name, v.instance.Namespace, v.instance.Name, err)
	}

	// Check reclaimPolicy of PV, if already set to retain
	if pv.Spec.PersistentVolumeReclaimPolicy == corev1.PersistentVolumeReclaimRetain {
		return nil
	}

	// if not retained, retain PV, and add an annotation to denote this is updated for VR needs
	pv.Spec.PersistentVolumeReclaimPolicy = corev1.PersistentVolumeReclaimRetain
	if pv.ObjectMeta.Annotations == nil {
		pv.ObjectMeta.Annotations = map[string]string{}
	}

	pv.ObjectMeta.Annotations[pvVRAnnotationRetentionKey] = pvVRAnnotationRetentionValue

	if err := v.reconciler.Update(v.ctx, pv); err != nil {
		log.Error(err, "Failed to update PersistentVolume reclaim policy")

		return fmt.Errorf("failed to update PersistentVolume resource (%s) reclaim policy for"+
			" PersistentVolumeClaim resource (%s/%s) belonging to VolumeReplicationGroup (%s/%s), %w",
			pvc.Spec.VolumeName, pvc.Namespace, pvc.Name, v.instance.Namespace, v.instance.Name, err)
	}

	return nil
}

// undoPVRetentionForPVC updates the PV reclaim policy back to its saved state
func (v *VRGInstance) undoPVRetentionForPVC(pvc corev1.PersistentVolumeClaim, log logr.Logger) error {
	// Get PV bound to PVC
	pv := &corev1.PersistentVolume{}
	pvObjectKey := client.ObjectKey{
		Name: pvc.Spec.VolumeName,
	}

	if err := v.reconciler.Get(v.ctx, pvObjectKey, pv); err != nil {
		log.Error(err, "Failed to get PersistentVolume", "volumeName", pvc.Spec.VolumeName)

		return fmt.Errorf("failed to get PersistentVolume resource (%s) for"+
			" PersistentVolumeClaim resource (%s/%s) belonging to VolumeReplicationGroup (%s/%s), %w",
			pvc.Spec.VolumeName, pvc.Namespace, pvc.Name, v.instance.Namespace, v.instance.Name, err)
	}

	if v, ok := pv.ObjectMeta.Annotations[pvVRAnnotationRetentionKey]; !ok || v != pvVRAnnotationRetentionValue {
		return nil
	}

	pv.Spec.PersistentVolumeReclaimPolicy = corev1.PersistentVolumeReclaimDelete
	delete(pv.ObjectMeta.Annotations, pvVRAnnotationRetentionKey)

	if err := v.reconciler.Update(v.ctx, pv); err != nil {
		log.Error(err, "Failed to update PersistentVolume reclaim policy", "volumeName", pvc.Spec.VolumeName)

		return fmt.Errorf("failed to update PersistentVolume resource (%s) reclaim policy for"+
			" PersistentVolumeClaim resource (%s/%s) belonging to VolumeReplicationGroup (%s/%s), %w",
			pvc.Spec.VolumeName, pvc.Namespace, pvc.Name, v.instance.Namespace, v.instance.Name, err)
	}

	return nil
}

// uploadPV checks if the VRG spec has been configured with an s3 endpoint,
// validates the S3 endpoint, connects to it, gets the PV metadata of
// the input PVC, creates a bucket in s3 store, upload's the PV metadata to
// s3 store and downloads it for verification.  If an s3 endpoint is not
// configured, then it assumes that VRG is running in a backup-less mode and
// does not return an error, but logs a one-time warning.
func (v *VRGInstance) uploadPV(pvc corev1.PersistentVolumeClaim) (err error) {
	vrgName := v.instance.Name
	s3Endpoint := v.instance.Spec.S3Endpoint
	s3Region := v.instance.Spec.S3Region
	s3Bucket := constructBucketName(v.instance.Namespace, vrgName)

	if err := v.validateS3Endpoint(s3Endpoint, s3Bucket); err != nil {
		if errors.IsServiceUnavailable(err) {
			// Implies unconfigured object store: backup-less mode
			return nil
		}

		return err
	}

	v.log.Info("Uploading PersistentVolume metadata to object store")

	objectStore, err :=
		v.reconciler.ObjStoreGetter.objectStore(v.ctx, v.reconciler,
			s3Endpoint,
			s3Region,
			types.NamespacedName{ /* secretName */
				Name:      v.instance.Spec.S3SecretName,
				Namespace: v.instance.Namespace,
			},
			vrgName, /* debugTag */
		)
	if err != nil {
		return fmt.Errorf("failed to get client for endpoint %s, err %w",
			s3Endpoint, err)
	}

	pv := corev1.PersistentVolume{}
	volumeName := pvc.Spec.VolumeName
	pvObjectKey := client.ObjectKey{Name: volumeName}

	// Get PV from k8s
	if err := v.reconciler.Get(v.ctx, pvObjectKey, &pv); err != nil {
		return fmt.Errorf("failed to get PV metadata from k8s, %w", err)
	}

	// Create the bucket in object store, without assuming its existence
	if err := objectStore.createBucket(s3Bucket); err != nil {
		return fmt.Errorf("unable to create s3Bucket %s, %w", s3Bucket, err)
	}

	// Upload PV to object store
	if err := objectStore.uploadPV(s3Bucket, pv.Name, pv); err != nil {
		return fmt.Errorf("error uploading PV %s, err %w", pv.Name, err)
	}

	// Verify upload of PV to object store
	if err := objectStore.verifyPVUpload(s3Bucket, pv.Name, pv); err != nil {
		return fmt.Errorf("error verifying PV %s, err %w", pv.Name, err)
	}

	return nil
}

// backupLessWarning is a map with VRG name as the key
var backupLessWarning = make(map[string]bool)

// If the the s3 endpoint is not set, then the VRG has been configured to run
// in a backup-less mode to simply control VR CRs alone without backing up
// PV k8s metadata to an object store.
func (v *VRGInstance) validateS3Endpoint(s3Endpoint, s3Bucket string) error {
	vrgName := v.instance.Name

	if s3Endpoint != "" {
		_, err := url.ParseRequestURI(s3Endpoint)
		if err != nil {
			return fmt.Errorf("invalid spec.S3Endpoint <%s> for "+
				"s3Bucket %s in VRG %s, %w", s3Endpoint, s3Bucket, vrgName, err)
		}

		backupLessWarning[vrgName] = false // Reset backup-less warning

		return nil
	}

	// No endpoint implies, backup-less mode
	err := errors.NewServiceUnavailable("missing s3Endpoint in VRG.spec")

	if prevWarned := backupLessWarning[vrgName]; prevWarned {
		// previously logged warning about backup-less mode of operation
		return err
	}

	v.log.Info("VolumeReplicationGroup ", vrgName, " running in backup-less mode.")

	backupLessWarning[vrgName] = true // Remember that an error was logged

	return err
}

// processVRAsPrimary processes VR to change its state to primary, with the assumption that the
// related PVC is prepared for VR protection
func (v *VRGInstance) processVRAsPrimary(vrNamespacedName types.NamespacedName, log logr.Logger) error {
	return v.createOrUpdateVR(vrNamespacedName, volrep.Primary, log)
}

// processVRAsSecondary processes VR to change its state to secondary, with the assumption that the
// related PVC is prepared for VR as secondary
func (v *VRGInstance) processVRAsSecondary(vrNamespacedName types.NamespacedName, log logr.Logger) error {
	return v.createOrUpdateVR(vrNamespacedName, volrep.Secondary, log)
}

// createOrUpdateVR updates an existing VR resource if found, or creates it if required
// While both creating and updating the VolumeReplication resource, conditions.status
// for the protected PVC (corresponding to the VolumeReplication resource) is set as
// PVCProgressing. When the VolumeReplication resource changes its state either due to
// successful reaching of the desired state or due to some error, VolumeReplicationGroup
// would get a reconcile. And then the conditions for the appropriate Protected PVC can
// be set as either Replicating or Error.
func (v *VRGInstance) createOrUpdateVR(vrNamespacedName types.NamespacedName,
	state volrep.ReplicationState, log logr.Logger) error {
	volRep := &volrep.VolumeReplication{}

	err := v.reconciler.Get(v.ctx, vrNamespacedName, volRep)
	if err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, "Failed to get VolumeReplication resource", "resource", vrNamespacedName)

			// Failed to get VolRep and error is not IsNotFound. It is not
			// clear if the associated VolRep exists or not. If exists, then
			// is it replicating or not. So, mark the protected pvc as error
			// with condition.status as Unknown.
			msg := "Failed to get VolumeReplication resource"
			v.updateProtectedPVCCondition(vrNamespacedName.Name, PVCErrorUnknown, msg)

			return fmt.Errorf("failed to get VolumeReplication resource"+
				" (%s/%s) belonging to VolumeReplicationGroup (%s/%s), %w",
				vrNamespacedName.Namespace, vrNamespacedName.Name, v.instance.Namespace, v.instance.Name, err)
		}

		// Create VR for PVC
		if err = v.createVR(vrNamespacedName, state); err != nil {
			log.Error(err, "Failed to create VolumeReplication resource", "resource", vrNamespacedName)

			msg := "Failed to create VolumeReplication resource"
			v.updateProtectedPVCCondition(vrNamespacedName.Name, PVCError, msg)

			return fmt.Errorf("failed to create VolumeReplication resource"+
				" (%s/%s) belonging to VolumeReplicationGroup (%s/%s), %w",
				vrNamespacedName.Namespace, vrNamespacedName.Name, v.instance.Namespace, v.instance.Name, err)
		}

		// Just created VolRep. Mark status.conditions as Progressing.
		msg := "Created VolumeReplication resource for PVC"
		v.updateProtectedPVCCondition(vrNamespacedName.Name, PVCProgressing, msg)

		return nil
	}

	return v.updateVR(volRep, state, log)
}

func (v *VRGInstance) updateVR(volRep *volrep.VolumeReplication,
	state volrep.ReplicationState, log logr.Logger) error {
	// If state is already as desired, check the status
	if volRep.Spec.ReplicationState == state {
		log.Info("VolumeReplication and VolumeReplicationGroup state match. Proceeding to status check")

		return v.checkVRStatus(volRep)
	}

	volRep.Spec.ReplicationState = state
	if err := v.reconciler.Update(v.ctx, volRep); err != nil {
		log.Error(err, "Failed to update VolumeReplication resource",
			"name", volRep.Name, "namespace", volRep.Namespace,
			"state", state)

		msg := "Failed to update VolumeReplication resource"
		v.updateProtectedPVCCondition(volRep.Name, PVCError, msg)

		return fmt.Errorf("failed to update VolumeReplication resource"+
			" (%s/%s) as %s, belonging to VolumeReplicationGroup (%s/%s), %w",
			volRep.Namespace, volRep.Name, state,
			v.instance.Namespace, v.instance.Name, err)
	}

	// Just updated the state of the VolRep. Mark it as progressing.
	msg := "Updated VolumeReplication resource for PVC"
	v.updateProtectedPVCCondition(volRep.Name, PVCProgressing, msg)

	return nil
}

// createVR creates a VolumeReplication CR with a PVC as its data source.
func (v *VRGInstance) createVR(vrNamespacedName types.NamespacedName, state volrep.ReplicationState) error {
	volumeReplicationClass, err := v.selectVolumeReplicationClass(vrNamespacedName)
	if err != nil {
		return fmt.Errorf("failed to find the appropriate VolumeReplicationClass (%s) %w",
			v.instance.Name, err)
	}

	volRep := &volrep.VolumeReplication{
		ObjectMeta: metav1.ObjectMeta{
			Name:      vrNamespacedName.Name,
			Namespace: vrNamespacedName.Namespace,
		},
		Spec: volrep.VolumeReplicationSpec{
			DataSource: corev1.TypedLocalObjectReference{
				Kind:     "PersistentVolumeClaim",
				Name:     vrNamespacedName.Name,
				APIGroup: new(string),
			},
			ReplicationState:       state,
			VolumeReplicationClass: volumeReplicationClass,
		},
	}

	// Let VRG receive notification for any changes to VolumeReplication CR
	// created by VRG.
	if err := ctrl.SetControllerReference(v.instance, volRep, v.reconciler.Scheme); err != nil {
		return fmt.Errorf("failed to set owner reference to VolumeReplication resource (%s/%s)",
			volRep.Name, volRep.Namespace)
	}

	v.log.Info("Creating VolumeReplication resource", "resource", volRep)

	if err := v.reconciler.Create(v.ctx, volRep); err != nil {
		return fmt.Errorf("failed to create VolumeReplication resource (%s)", vrNamespacedName)
	}

	return nil
}

// namespacedName applies to both VolumeReplication resource and pvc as of now.
// This is because, VolumeReplication resource for a pvc that is created by the
// VolumeReplicationGroup has the same name as pvc. But in future if it changes
// functions to be changed would be processVRAsPrimary(), processVRAsSecondary()
// to either receive pvc NamespacedName or pvc itself as an additional argument.
func (v *VRGInstance) selectVolumeReplicationClass(namespacedName types.NamespacedName) (string, error) {
	className := ""

	if !v.vrcUpdated {
		if err := v.updateReplicationClassList(); err != nil {
			v.log.Error(err, "Failed to get VolumeReplicationClass list")

			return className, fmt.Errorf("failed to get VolumeReplicationClass list")
		}

		v.vrcUpdated = true
	}

	if len(v.replClassList.Items) == 0 {
		v.log.Info("No VolumeReplicationClass available")

		return className, fmt.Errorf("no VolumeReplicationClass available")
	}

	storageClass, err := v.getStorageClass(namespacedName)
	if err != nil {
		v.log.Info(fmt.Sprintf("Failed to get the storageclass of pvc %s",
			namespacedName))

		return className, fmt.Errorf("failed to get the storageclass of pvc %s (%w)",
			namespacedName, err)
	}

	for index := range v.replClassList.Items {
		replicationClass := &v.replClassList.Items[index]
		if storageClass.Provisioner != replicationClass.Spec.Provisioner {
			continue
		}

		schedulingInterval, found := replicationClass.Spec.Parameters["schedulingInterval"]
		if !found {
			// schedule not present in parameters of this replicationClass.
			continue
		}

		// ReplicationClass that matches both VRG schedule and pvc provisioner
		if schedulingInterval == v.instance.Spec.SchedulingInterval {
			className = replicationClass.Name

			break
		}
	}

	if className == "" {
		v.log.Info(fmt.Sprintf("No VolumeReplicationClass found to match provisioner and schedule %s/%s",
			storageClass.Provisioner, v.instance.Spec.SchedulingInterval))

		return className, fmt.Errorf("no VolumeReplicationClass found to match provisioner and schedule")
	}

	return className, nil
}

// if the fetched SCs are stashed, fetching it again for the next PVC can be avoided
// saving a call to the API server
func (v *VRGInstance) getStorageClass(namespacedName types.NamespacedName) (*storagev1.StorageClass, error) {
	var pvc *corev1.PersistentVolumeClaim

	for index := range v.pvcList.Items {
		pvcItem := &v.pvcList.Items[index]

		pvcNamespacedName := types.NamespacedName{Name: pvcItem.Name, Namespace: pvcItem.Namespace}
		if pvcNamespacedName == namespacedName {
			pvc = pvcItem

			break
		}
	}

	if pvc == nil {
		v.log.Info("failed to get the pvc with namespaced name", namespacedName)

		// Need the storage driver of pvc. If pvc is not found return error.
		return nil, fmt.Errorf("failed to get the pvc with namespaced name %s", namespacedName)
	}

	scName := pvc.Spec.StorageClassName

	storageClass := &storagev1.StorageClass{}
	if err := v.reconciler.Get(v.ctx, types.NamespacedName{Name: *scName}, storageClass); err != nil {
		v.log.Info(fmt.Sprintf("Failed to get the storageclass %s", *scName))

		return nil, fmt.Errorf("failed to get the storageclass with name %s (%w)",
			*scName, err)
	}

	return storageClass, nil
}

func (v *VRGInstance) updateVRGStatus(updateConditions bool) error {
	v.log.Info("Updating VRG status")

	if updateConditions {
		v.updateVRGConditions()
	}

	v.updateStatusState()

	v.instance.Status.ObservedGeneration = v.instance.Generation
	v.instance.Status.LastUpdateTime = metav1.Now()

	if err := v.reconciler.Status().Update(v.ctx, v.instance); err != nil {
		v.log.Info(fmt.Sprintf("Failed to update VRG status (%s/%s)",
			v.instance.Name, v.instance.Namespace))

		return fmt.Errorf("failed to update VRG status (%s/%s)", v.instance.Name, v.instance.Namespace)
	}

	v.log.Info(fmt.Sprintf("Updated VRG Status %+v", v.instance.Status))

	return nil
}

func (v *VRGInstance) updateStatusState() {
	vrgCondition := findCondition(v.instance.Status.Conditions, VRGConditionAvailable)
	if vrgCondition == nil {
		v.log.Info("Failed to find the Available condition in status")

		return
	}

	StatusState := getStatusStateFromSpecState(v.instance.Spec.ReplicationState)

	// update Status.State to reflect the state in spec
	// only after successful transition of the resource
	// (from primary->secondary or vise versa). That
	// successful completion of transition can be seen
	// in vrgCondition.Status being set to True.
	if vrgCondition.Status == metav1.ConditionTrue {
		v.instance.Status.State = StatusState

		return
	}

	// If VRG available condition is not true and the reason
	// is Error, then mark Status.State as UnknownState instead
	// of Primary or Secondary.
	if vrgCondition.Reason == VRGError {
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

//
// Follow this logic to update VRG (and also ProtectedPVC) conditions
// while reconciling VolumeReplicationGroup resource.
//
// For both Primary and Secondary:
// if getting VolRep fails and volrep does not exist:
//    ProtectedPVC.conditions.Available.Status = False
//    ProtectedPVC.conditions.Available.Reason = Progressing
//    return
// if getting VolRep fails and some other error:
//    ProtectedPVC.conditions.Available.Status = Unknown
//    ProtectedPVC.conditions.Available.Reason = Error
//
// This below if condition check helps in undersanding whether
// promotion/demotion has been successfully completed or not.
// if VolRep.Status.Conditions[Completed].Status == True
//    ProtectedPVC.conditions.Available.Status = True
//    ProtectedPVC.conditions.Available.Reason = Replicating
// else
//    ProtectedPVC.conditions.Available.Status = False
//    ProtectedPVC.conditions.Available.Reason = Error
//
// if all ProtectedPVCs are Replicating, then
//    VRG.conditions.Available.Status = true
//    VRG.conditions.Available.Reason = Replicating
// if atleast one ProtectedPVC.conditions[Available].Reason == Error
//    VRG.conditions.Available.Status = false
//    VRG.conditions.Available.Reason = Error
// if no ProtectedPVCs is in error and atleast one is progressing, then
//    VRG.conditions.Available.Status = false
//    VRG.conditions.Available.Reason = Progressing
//
func (v *VRGInstance) updateVRGConditions() {
	vrgReady := true
	vrgProgressing := false

	for _, protectedPVC := range v.instance.Status.ProtectedPVCs {
		condition := findCondition(protectedPVC.Conditions, PVCConditionAvailable)
		if condition == nil {
			vrgReady = false

			break
		}

		if condition.Reason == PVCProgressing {
			vrgReady = false
			vrgProgressing = true

			break
		}

		if condition.Reason == PVCError || condition.Reason == PVCErrorUnknown {
			vrgReady = false
			// If there is even a single protected pvc
			// that saw some error, then entire VRG
			// should mark its condition as error. So
			// set vrgPogressing to false.
			vrgProgressing = false

			break
		}
	}

	if vrgReady {
		v.log.Info("Marking VRG available with replicating reason")

		msg := "PVCs in the VolumeReplicationGroup group are replicating"
		setVRGReplicatingCondition(&v.instance.Status.Conditions, v.instance.Generation, msg)

		return
	}

	if vrgProgressing {
		v.log.Info("Marking VRG not available with progressing reason")

		msg := "VolumeReplicationGroup is progressing"
		setVRGProgressingCondition(&v.instance.Status.Conditions, v.instance.Generation, msg)

		return
	}

	// None of the VRG Ready and VRG Progressing conditions are met.
	// Set Error condition for VRG.
	v.log.Info("Marking VRG not available with error. All PVCs are not ready")

	msg := "All PVCs of the VolumeReplicationGroup are not available"
	setVRGErrorCondition(&v.instance.Status.Conditions, v.instance.Generation, msg)
}

func (v *VRGInstance) checkVRStatus(volRep *volrep.VolumeReplication) error {
	// When the generation in the status is updated, VRG would get a reconcile
	// as it owns VolumeReplication resource.
	if volRep.Generation != volRep.Status.ObservedGeneration {
		v.log.Info("Generation from the resource and status not same")

		msg := "VolumeReplication generation not updated in status"
		v.updateProtectedPVCCondition(volRep.Name, PVCProgressing, msg)

		return nil
	}

	switch {
	case v.instance.Spec.ReplicationState == ramendrv1alpha1.Primary:
		return v.validateVRStatus(volRep, ramendrv1alpha1.Primary)
	case v.instance.Spec.ReplicationState == ramendrv1alpha1.Secondary:
		return v.validateVRStatus(volRep, ramendrv1alpha1.Secondary)
	default:
		msg := "VolumeReplicationGroup state invalid"
		v.updateProtectedPVCCondition(volRep.Name, PVCError, msg)

		return fmt.Errorf("invalid Replication State %s for VolumeReplicationGroup (%s:%s)",
			string(v.instance.Spec.ReplicationState), v.instance.Name, v.instance.Namespace)
	}
}

// VolumeReplication controller sets volRep.Status.State to primary/secondary
// only after the successful promotion/demotion of the underlying storage volume
// consumed by the pvc.It is necessary to get the appropriate condition out of
// different conditions a VolRep status can have. Doing this may be helpful in
// getting a more clear view of the VolumeReplication resource's current situation.
//
// As of now, name of the VolumeReplication resource and pvc that it represents is
// same. So, using the name from volRep resource to get protectedPVC condition from
// vrg.Status. In future if things change, some changes might have to be necessary.
func (v *VRGInstance) validateVRStatus(volRep *volrep.VolumeReplication, state ramendrv1alpha1.ReplicationState) error {
	var (
		stateString = "unknown"
		action      = "unknown"
	)

	switch state {
	case ramendrv1alpha1.Primary:
		stateString = "primary"
		action = "promoted"
	case ramendrv1alpha1.Secondary:
		stateString = "secondary"
		action = "demoted"
	}

	volRepCondition := findCondition(volRep.Status.Conditions, volrepController.ConditionCompleted)
	if volRepCondition == nil {
		v.log.Info("failed to get the Completed condition from VolumeReplication resource", "volRep",
			volRep.Name)

		msg := "Failed to get the Completed condition from VolumeReplication resource for pvc."
		v.updateProtectedPVCCondition(volRep.Name, PVCError, msg)

		return nil
	}

	if volRepCondition.Status == metav1.ConditionFalse {
		v.log.Info(fmt.Sprintf("VolumeReplication resource is not %s (%s)", action, volRep.Name))

		msg := fmt.Sprintf("VolumeReplication resource for pvc not %s to %s", action, stateString)
		v.updateProtectedPVCCondition(volRep.Name, PVCError, msg)

		return nil
	}

	if volRepCondition.Status == metav1.ConditionUnknown {
		v.log.Info(fmt.Sprintf("VolumeReplication resource is not %s (%s). ConditionStatus unknown",
			action, volRep.Name))

		msg := fmt.Sprintf("VolumeReplication resource for pvc not %s to %s. ConditionStatus unknown",
			action, stateString)
		v.updateProtectedPVCCondition(volRep.Name, PVCErrorUnknown, msg)

		return nil
	}

	v.log.Info(fmt.Sprintf("ConditionCompleted is true for state %s (Name: %s)", stateString, volRep.Name))

	// Being here means, status of VolumeReplication resource has been
	// successfully updated to reflect the appropriate state. (i.e. the
	// volume has been successfully promoted/demoted). So, marking the
	// condition as PVCReplicating. Even by checking different conditions
	// of the VolRep resource it is difficult to determine whether the
	// replication has been completed or not.
	msg := "VolumeReplication resource for the pvc is replicating"
	v.updateProtectedPVCCondition(volRep.Name, PVCReplicating, msg)

	return nil
}

func (v *VRGInstance) updateProtectedPVCCondition(name, reason, message string) {
	if pvcProtected, found := v.instance.Status.ProtectedPVCs[name]; found {
		setConditionProtectedPVC(pvcProtected, reason, message, v.instance.Generation)
		v.instance.Status.ProtectedPVCs[name] = pvcProtected

		return
	}

	pvcProtected := &ramendrv1alpha1.ProtectedPVC{Name: name}
	setConditionProtectedPVC(pvcProtected, reason, message, v.instance.Generation)
	v.instance.Status.ProtectedPVCs[name] = pvcProtected
}

func setConditionProtectedPVC(protectedPVC *ramendrv1alpha1.ProtectedPVC, reason, message string,
	observedGeneration int64) {
	switch {
	case reason == PVCError:
		setPVCErrorCondition(&protectedPVC.Conditions, observedGeneration, message)
	case reason == PVCReplicating:
		setPVCReplicatingCondition(&protectedPVC.Conditions, observedGeneration, message)
	case reason == PVCProgressing:
		setPVCProgressingCondition(&protectedPVC.Conditions, observedGeneration, message)
	case reason == PVCErrorUnknown:
		setPVCErrorUnknownCondition(&protectedPVC.Conditions, observedGeneration, message)
	default:
		// if appropriate reason is not provided, then treated as error.
		message = "Unknown reason"
		setPVCErrorCondition(&protectedPVC.Conditions, observedGeneration, message)
	}
}

// deleteVR deletes a VolumeReplication instance if found
func (v *VRGInstance) deleteVR(vrNamespacedName types.NamespacedName, log logr.Logger) error {
	cr := &volrep.VolumeReplication{
		ObjectMeta: metav1.ObjectMeta{
			Name:      vrNamespacedName.Name,
			Namespace: vrNamespacedName.Namespace,
		},
	}

	err := v.reconciler.Delete(v.ctx, cr)
	if err == nil || errors.IsNotFound(err) {
		return nil
	}

	log.Error(err, "Failed to delete VolumeReplication resource")

	return fmt.Errorf("failed to delete VolumeReplication resource (%s/%s), %w",
		vrNamespacedName.Namespace, vrNamespacedName.Name, err)
}

func (v *VRGInstance) addProtectedAnnotationForPVC(pvc *corev1.PersistentVolumeClaim, log logr.Logger) error {
	if pvc.ObjectMeta.Annotations == nil {
		pvc.ObjectMeta.Annotations = map[string]string{}
	}

	pvc.ObjectMeta.Annotations[pvcVRAnnotationProtectedKey] = pvcVRAnnotationProtectedValue

	if err := v.reconciler.Update(v.ctx, pvc); err != nil {
		log.Error(err, "Failed to update PersistentVolumeClaim annotation")

		return fmt.Errorf("failed to update PersistentVolumeClaim (%s/%s) annotation (%s) belonging to"+
			"VolumeReplicationGroup (%s/%s), %w",
			pvc.Namespace, pvc.Name, pvcVRAnnotationProtectedKey, v.instance.Namespace, v.instance.Name, err)
	}

	return nil
}

// addFinalizer adds a finalizer to VRG, to act as deletion protection
func (v *VRGInstance) addFinalizer(finalizer string) error {
	if !containsString(v.instance.ObjectMeta.Finalizers, finalizer) {
		v.instance.ObjectMeta.Finalizers = append(v.instance.ObjectMeta.Finalizers, finalizer)
		if err := v.reconciler.Update(v.ctx, v.instance); err != nil {
			v.log.Error(err, "Failed to add finalizer", "finalizer", finalizer)

			return fmt.Errorf("failed to add finalizer to VolumeReplicationGroup resource (%s/%s), %w",
				v.instance.Namespace, v.instance.Name, err)
		}
	}

	return nil
}

func (v *VRGInstance) addProtectedFinalizerToPVC(pvc *corev1.PersistentVolumeClaim,
	log logr.Logger) error {
	if containsString(pvc.Finalizers, pvcVRFinalizerProtected) {
		return nil
	}

	return v.addFinalizerToPVC(pvc, pvcVRFinalizerProtected, log)
}

func (v *VRGInstance) addFinalizerToPVC(pvc *corev1.PersistentVolumeClaim,
	finalizer string,
	log logr.Logger) error {
	if !containsString(pvc.ObjectMeta.Finalizers, finalizer) {
		pvc.ObjectMeta.Finalizers = append(pvc.ObjectMeta.Finalizers, finalizer)
		if err := v.reconciler.Update(v.ctx, pvc); err != nil {
			log.Error(err, "Failed to add finalizer", "finalizer", finalizer)

			return fmt.Errorf("failed to add finalizer (%s) to PersistentVolumeClaim resource"+
				" (%s/%s) belonging to VolumeReplicationGroup (%s/%s), %w",
				finalizer, pvc.Namespace, pvc.Name, v.instance.Namespace, v.instance.Name, err)
		}
	}

	return nil
}

func (v *VRGInstance) removeProtectedFinalizerFromPVC(pvc *corev1.PersistentVolumeClaim,
	log logr.Logger) error {
	return v.removeFinalizerFromPVC(pvc, pvcVRFinalizerProtected, log)
}

// removeFinalizerFromPVC removes the VR finalizer on PVC and also the protected annotation from the PVC
func (v *VRGInstance) removeFinalizerFromPVC(pvc *corev1.PersistentVolumeClaim,
	finalizer string,
	log logr.Logger) error {
	if containsString(pvc.ObjectMeta.Finalizers, finalizer) {
		pvc.ObjectMeta.Finalizers = removeString(pvc.ObjectMeta.Finalizers, finalizer)
		delete(pvc.ObjectMeta.Annotations, pvcVRAnnotationProtectedKey)

		if err := v.reconciler.Update(v.ctx, pvc); err != nil {
			log.Error(err, "Failed to remove finalizer", "finalizer", finalizer)

			return fmt.Errorf("failed to remove finalizer (%s) from PersistentVolumeClaim resource"+
				" (%s/%s) detected as part of VolumeReplicationGroup (%s/%s), %w",
				finalizer, pvc.Namespace, pvc.Name, v.instance.Namespace, v.instance.Name, err)
		}
	}

	return nil
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
