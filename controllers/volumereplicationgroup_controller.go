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

	"github.com/go-logr/logr"
	volrep "github.com/shyamsundarr/volrep-shim-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
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
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// s3Warning is a map of s3 bucket to s3 endpoint to avoid repetitive
// log messages when operating in backup-less mode.
var s3Warning map[string]string

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
		// The actual thing that the controller owns is
		// the VolumeReplication CR. Change the below
		// line when VolumeReplication CR is ready.
		Owns(&ramendrv1alpha1.VolumeReplicationGroup{}).
		Complete(r)
}

func pvcPredicateFunc() predicate.Funcs {
	// predicate functions send reconcile requests for create and delete events.
	// For them the filtering of whether the pvc belongs to the any of the
	// VolumeReplicationGroup CRs and identifying such a CR is done in the
	// map function by comparing namespaces and labels.
	// But for update of pvc, the reconcile request should be sent only for
	// spec changes. Do that comparison here.
	pvcPredicate := predicate.Funcs{
		// This predicate function can be removed as the only thing it is
		// doing currently is to log the pvc creation event that is received.
		// Even without this predicate function, by default the reconcile
		// request would be sent (i.e. equivalent of returning true from here).
		// However having a log here will be useful for debugging purposes where
		// one can verify and distinguish between below 2 events.
		// 1) pvc creation for the application for which VRG CR exists.
		//    (i.e. application is disaster protected). For this reconcile
		//    logic will be triggered.
		// 2) pvc creation for the application for which VRG CR does not exist
		//    (i.e. application is not disaster protected). For this reconcile
		//    logic is not triggered.
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

			return !reflect.DeepEqual(oldPVC.Spec, newPVC.Spec)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			log := ctrl.Log.WithName("pvcmap").WithName("VolumeReplicationGroup")

			log.Info("delete event for PersistentVolumeClaim")

			// PVC deletes are not of interest, as the added finalizers by VRG would be removed
			// when VRGs are deleted, which would trigger the delete of the PVC and as required
			// the underlying PV
			return false
		},
	}

	return pvcPredicate
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
// +kubebuilder:rbac:groups=replication.storage.ramen.io,resources=volumereplications,verbs=get;list;watch;create;update;patch;delete
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
		reconciler: r,
		ctx:        ctx,
		log:        log,
		instance:   &ramendrv1alpha1.VolumeReplicationGroup{},
		pvcList:    &corev1.PersistentVolumeClaimList{},
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

	if err := v.updatePVCList(); err != nil {
		log.Error(err, "Failed to update PersistentVolumeClaims for resource")

		// TODO: Update status of VRG to reflect error in reconcile for user consumption?
		return ctrl.Result{Requeue: true}, nil
	}

	if v.instance.Spec.ReplicationState != volrep.ReplicationPrimary &&
		v.instance.Spec.ReplicationState != volrep.ReplicationSecondary {
		err := fmt.Errorf("invalid or unknown replication state detected (deleted %v, desired replicationState %v)",
			v.instance.GetDeletionTimestamp().IsZero(),
			v.instance.Spec.ReplicationState)
		log.Error(err, "Invalid request detected")

		// No requeue, as there is no reconcile till user changes desired spec to a valid value
		return ctrl.Result{}, err
	}

	v.log = log.WithName("vrginstance").WithValues("State", v.instance.Spec.ReplicationState)

	switch {
	case !v.instance.GetDeletionTimestamp().IsZero():
		v.log = v.log.WithValues("Finalize", true)

		return v.finalizeVRG()
	case v.instance.Spec.ReplicationState == volrep.ReplicationPrimary:
		return v.processAsPrimary()
	default: // Secondary, not primary and not deleted
		return v.processAsSecondary()
	}
}

type VRGInstance struct {
	reconciler *VolumeReplicationGroupReconciler
	ctx        context.Context
	log        logr.Logger
	instance   *ramendrv1alpha1.VolumeReplicationGroup
	pvcList    *corev1.PersistentVolumeClaimList
}

const (
	// Finalizers
	vrgFinalizerName        = "volumereplicationgroups.ramendr.openshift.io/vrg-protection"
	pvcVRFinalizerProtected = "volumereplicationgroups.ramendr.openshift.io/pvc-vr-protection"

	// Annotations
	pvcVRAnnotationProtectedKey   = "volumereplicationgroups.ramendr.openshift.io/vr-protected"
	pvcVRAnnotationProtectedValue = "protected"
	pvVRAnnotationRetentionKey    = "volumereplicationgroups.ramendr.openshift.io/vr-retained"
	pvVRAnnotationRetentionValue  = "retained"
)

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

// finalizeVRG cleans up managed resources and removes the VRG finalizer for resource deletion
func (v *VRGInstance) finalizeVRG() (ctrl.Result, error) {
	if !containsString(v.instance.ObjectMeta.Finalizers, vrgFinalizerName) {
		v.log.Info("Finalizer missing from resource", "finalizer", vrgFinalizerName)

		return ctrl.Result{}, nil
	}

	if err := v.finalizeChildren(); err != nil {
		v.log.Info("Failed to finalize child resources", "errorValue", err)

		return ctrl.Result{Requeue: true}, nil
	}

	if err := v.removeFinalizer(vrgFinalizerName); err != nil {
		v.log.Info("Failed to remove finalizer", "finalizer", vrgFinalizerName, "errorValue", err)

		return ctrl.Result{Requeue: true}, nil
	}

	// stop reconciliation as the item is being deleted
	return ctrl.Result{}, nil
}

// finalizeChildren cleans up children resources of a given VRG
func (v *VRGInstance) finalizeChildren() error {
	// add logic to perform the following things.
	// - Remove the VolumeReplication CRs associated with pvcs
	//   belonging to the application protected by this VolumeReplicationGroup CR.
	// - Delete the backed up PV metadata from the backup store.
	//
	// As of now deletion of VolumeReplication CRs is done.
	// TODO: Delete the backed up PV metadata from the backup store.
	return v.deleteVolumeReplicationResources()
}

// deleteVolumeReplicationResources cleans up VR resources managed by VRG
// TODO: Currently removes VR requests unconditionally, needs to ensure it is managed by VRG
func (v *VRGInstance) deleteVolumeReplicationResources() error {
	vrList := &volrep.VolumeReplicationList{}

	listOptions := []client.ListOption{
		client.InNamespace(v.instance.Namespace),
	}

	if err := v.reconciler.List(v.ctx, vrList, listOptions...); err != nil {
		if errors.IsNotFound(err) {
			v.log.Info("No VolumeReplication resources found")

			return nil
		}

		v.log.Error(err, "Failed to list VolumeReplication resources")

		return fmt.Errorf("failed to list VolumeReplication resources, %w", err)
	}

	v.log.Info("VolumeReplication resources found", "vrList", vrList)

	for idx := range vrList.Items {
		// Stop at the first instance of failure and handle
		// the deletion of remaining VolumeReplication CRs
		// in the next reconcile request.
		vr := vrList.Items[idx]
		if err := v.reconciler.Delete(v.ctx, &vr); err != nil {
			v.log.Error(err, "Failed to delete VolumeReplication resource", "name",
				vr.Name, "namespace", vr.Namespace)

			return fmt.Errorf("failed to delete VolumeReplication resource (%s:%s), %w",
				vr.Name, vr.Namespace, err)
		}
	}

	return nil
}

// removeFinalizer removes VRG finalizer form the resource
func (v *VRGInstance) removeFinalizer(finalizer string) error {
	v.instance.ObjectMeta.Finalizers = removeString(v.instance.ObjectMeta.Finalizers, finalizer)
	if err := v.reconciler.Update(v.ctx, v.instance); err != nil {
		v.log.Error(err, "Failed to remove finalizer", "finalizer", finalizer)

		return fmt.Errorf("failed to remove finalizer from VolumeReplicationGroup resource (%s/%s), %w",
			v.instance.Name, v.instance.Namespace, err)
	}

	return nil
}

// processAsPrimary reconciles the current instance of VRG as primary
func (v *VRGInstance) processAsPrimary() (ctrl.Result, error) {
	if err := v.addFinalizer(vrgFinalizerName); err != nil {
		v.log.Info("Failed to add finalizer", "finalizer", vrgFinalizerName, "errorValue", err)

		return ctrl.Result{Requeue: true}, nil
	}

	requeue := false
	requeue = v.reconcileVRsAsPrimary()

	// TODO: Update status

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

		if requeueResult := v.preparePVCForVRProtection(pvc, log); requeueResult {
			requeue = true

			continue
		}

		if err := v.processVRAsPrimary(pvcNamespacedName, log); err != nil {
			log.Info("Requeuing due to failure in getting or creating VolumeReplication resource for PersistentVolumeClaim",
				"errorValue", err)

			requeue = true

			continue
		}

		if requeueResult := v.processPVCAsVRProtected(pvc, log); requeueResult {
			requeue = true

			continue
		}

		log.Info("Successfully processed VolumeReplication for PersistentVolumeClaim")
	}

	// TODO: updateVRStatus even when resource requires a requeue

	return requeue
}

// preparePVCForVRProtection processes prerequisites of any PVC that needs VR protection
func (v *VRGInstance) preparePVCForVRProtection(pvc *corev1.PersistentVolumeClaim,
	log logr.Logger) bool {
	const requeue bool = true

	// if PVC protection is complete, return
	if pvc.Annotations[pvcVRAnnotationProtectedKey] == pvcVRAnnotationProtectedValue {
		return !requeue
	}

	// if the PVC is not bound yet, don't proceed
	if pvc.Status.Phase != corev1.ClaimBound {
		log.Info("Requeuing as PersistentVolumeClaim is not bound", "pvcPhase", pvc.Status.Phase)

		return requeue
	}

	// TODO: Should we ignore PVCs marked for deletion?

	// Add VR finalizer to PVC for deletion protection
	if err := v.addProtectedFinalizerToPVC(pvc, log); err != nil {
		log.Info("Requeuing as adding PersistentVolumeClaim finalizer failed", "errorValue", err)

		return requeue
	}

	// Change PV `reclaimPolicy` to "Retain"
	if err := v.retainPVForPVC(*pvc, log); err != nil {
		log.Info("Requeuing as retaining PersistentVolume failed", "errorValue", err)

		return requeue
	}

	return !requeue
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
	pv.ObjectMeta.Annotations[pvVRAnnotationRetentionKey] = pvVRAnnotationRetentionValue

	if err := v.reconciler.Update(v.ctx, pv); err != nil {
		log.Error(err, "Failed to update PersistentVolume reclaim policy")

		return fmt.Errorf("failed to update PersistentVolume resource (%s) reclaim policy for"+
			" PersistentVolumeClaim resource (%s/%s) belonging to VolumeReplicationGroup (%s/%s), %w",
			pvc.Spec.VolumeName, pvc.Namespace, pvc.Name, v.instance.Namespace, v.instance.Name, err)
	}

	return nil
}

// processVRAsPrimary processes VR to change its state to primary, with the assumption that the
// related PVC is prepared for VR protection
func (v *VRGInstance) processVRAsPrimary(vrNamespacedName types.NamespacedName, log logr.Logger) error {
	volRep := &volrep.VolumeReplication{}

	err := v.reconciler.Get(v.ctx, vrNamespacedName, volRep)
	if err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, "Failed to get VolumeReplication resource", "resource", vrNamespacedName)

			return fmt.Errorf("failed to get VolumeReplication resource"+
				" (%s/%s) belonging to VolumeReplicationGroup (%s/%s), %w",
				vrNamespacedName.Namespace, vrNamespacedName.Name, v.instance.Namespace, v.instance.Name, err)
		}

		// Create VR for PVC
		if err = v.createVR(vrNamespacedName); err != nil {
			log.Error(err, "Failed to create VolumeReplication resource", "resource", vrNamespacedName)

			return fmt.Errorf("failed to create VolumeReplication resource"+
				" (%s/%s) belonging to VolumeReplicationGroup (%s/%s), %w",
				vrNamespacedName.Namespace, vrNamespacedName.Name, v.instance.Namespace, v.instance.Name, err)
		}
	}

	if volRep.Spec.State == volrep.ReplicationPrimary {
		return nil
	}

	volRep.Spec.State = volrep.ReplicationPrimary
	if err = v.reconciler.Update(v.ctx, volRep); err != nil {
		log.Error(err, "Failed to update VolumeReplication resource",
			"resource", vrNamespacedName,
			"state", volrep.ReplicationPrimary)

		return fmt.Errorf("failed to update VolumeReplication resource"+
			" (%s/%s) as %s, belonging to VolumeReplicationGroup (%s/%s), %w",
			vrNamespacedName.Namespace, vrNamespacedName.Name, volrep.ReplicationPrimary,
			v.instance.Namespace, v.instance.Name, err)
	}

	return nil
}

// createVolumeReplicationCRForPVC creates a VolumeReplication CR with a PVC as its data source.
func (v *VRGInstance) createVR(vrNamespacedName types.NamespacedName) error {
	cr := &volrep.VolumeReplication{
		ObjectMeta: metav1.ObjectMeta{
			Name:      vrNamespacedName.Name,
			Namespace: vrNamespacedName.Namespace,
		},
		Spec: volrep.VolumeReplicationSpec{
			DataSource: &corev1.TypedLocalObjectReference{
				Kind: "PersistentVolumeClaim",
				Name: vrNamespacedName.Name,
			},
			// Get the state of VolumeReplication from
			// VolumeReplicationGroupSpec
			State: v.instance.Spec.ReplicationState,
		},
	}

	return v.reconciler.Create(v.ctx, cr)
}

// processPVCAsVRProtected processes a given PVC that is VR protected, to ensure post protection
// tasks are complete for the given PVC
func (v *VRGInstance) processPVCAsVRProtected(pvc *corev1.PersistentVolumeClaim,
	log logr.Logger) bool {
	const requeue bool = true

	// if PVC protection is complete, return
	if pvc.Annotations[pvcVRAnnotationProtectedKey] == pvcVRAnnotationProtectedValue {
		return !requeue
	}

	// Backup PV bound to PVC, before creating VR for PVC
	log.Info("Uploading PersistentVolume")

	// If faulted here, PV will be present but VR may not be enabled causing application failure on peer as,
	// PVC on peer will bind to PV, and in reality the backend will not have any volume as VR is not yet created.
	// Hence, we should create VR and then backup PV once VR status is as expected
	if err := v.uploadBackingPV(*pvc); err != nil {
		log.Info("Requeuing as uploading PersistentVolume failed", "errorValue", err)

		return requeue
	}

	// Annotate that PVC protection is complete
	if err := v.addProtectedAnnotationForPVC(pvc, log); err != nil {
		log.Info("Requeuing as annotating PersistentVolumeClaim failed", "errorValue", err)

		return requeue
	}

	return !requeue
}

// uploadPV checks if the VRG spec has been configured with an s3 endpoint,
// validates the S3 endpoint, connects to it, gets the PV metadata of
// the input PVC, creates a bucket in s3 store, upload's the PV metadata to
// s3 store and downloads it for verification.  If an s3 endpoint is not
// configured, then it assumes that VRG is running in a backup-less mode and
// does not return an error, but logs a one-time warning.
func (v *VRGInstance) uploadBackingPV(pvc corev1.PersistentVolumeClaim) error {
	// Create an s3 bucket name from the VRG's namespace and name, separating
	// the two with a forward slash.  Note: VRG's namespace and name may also
	// have dots or hyphens in their names.
	vrgName := v.instance.Name
	s3Bucket := constructBucketName(v.instance.Namespace, vrgName)

	s3Endpoint := v.instance.Spec.S3Endpoint

	if s3Endpoint == "" {
		// VRG is configured to run in a backup-less mode.  validateS3Endpoint()
		// logs a warning message once about this backup-less mode; hence, no
		// need to log any error here.
		return nil
	}

	if err := validateS3Endpoint(s3Endpoint, s3Bucket, v.log); err != nil {
		return err
	}

	s3Conn, err := connectToS3Endpoint(v.ctx, v.reconciler, s3Endpoint,
		types.NamespacedName{ /* secretName */
			Name:      v.instance.Spec.S3SecretName,
			Namespace: v.instance.Namespace,
		}, vrgName, /* debugTag */
	)
	if err != nil {
		return err
	}

	pv := corev1.PersistentVolume{}
	volumeName := pvc.Spec.VolumeName
	pvObjectKey := client.ObjectKey{
		Name: volumeName,
	}

	if err := v.reconciler.Get(v.ctx, pvObjectKey, &pv); err != nil {
		return fmt.Errorf("failed to get PV metadata from k8s, %w", err)
	}

	// Create the s3Bucket, without caching or assuming its existence
	if err := s3Conn.createBucket(s3Bucket); err != nil {
		return fmt.Errorf("unable to create s3Bucket %s, %w",
			s3Bucket, err)
	}

	if err := s3Conn.uploadPV(s3Bucket, pv.Name, pv); err != nil {
		return err
	}

	if err := s3Conn.verifyPVUpload(s3Bucket, pv.Name, pv); err != nil {
		return err
	}

	// Remove after Benamar uses this method in AVR
	if _, err := s3Conn.downloadPVs(s3Bucket); err != nil {
		return err
	}

	return err
}

// If the the s3 endpoint is not set, then the VRG has been configured to run
// in a backup-less mode to simply control VR CRs alone without backing up
// PV k8s metadata to an object store.
func validateS3Endpoint(s3Endpoint, s3Bucket string, log logr.Logger) error {
	if s3Endpoint != "" {
		_, err := url.ParseRequestURI(s3Endpoint)
		if err != nil {
			return fmt.Errorf("invalid spec.S3Endpoint <%s> for "+
				"s3Bucket %s, %w", s3Endpoint, s3Bucket, err)
		}

		return nil
	}

	if prevEndpoint, prevWarned := s3Warning[s3Bucket]; prevWarned &&
		prevEndpoint == s3Endpoint {
		return nil // previously warned about backup-less mode of operation
	}

	log.Info("VolumeReplicationGroup ", s3Bucket, "running in backup-less mode.")

	s3Warning[s3Bucket] = s3Endpoint

	return nil
}

// processAsSecondary reconciles the current instance of VRG as secondary
func (v *VRGInstance) processAsSecondary() (ctrl.Result, error) {
	return v.processAsPrimary()
}

func (v *VRGInstance) addProtectedAnnotationForPVC(pvc *corev1.PersistentVolumeClaim, log logr.Logger) error {
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
				v.instance.Name, v.instance.Namespace, err)
		}
	}

	return nil
}

func (v *VRGInstance) addProtectedFinalizerToPVC(pvc *corev1.PersistentVolumeClaim,
	log logr.Logger) error {
	if _, ok := pvc.Annotations[pvcVRFinalizerProtected]; ok {
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

/* TODO: uncomment when used with delete workflow (make linter happy for now)
func (v *VRGInstance) removeProtectedFinalizerFromPVC(pvc *corev1.PersistentVolumeClaim,
	log logr.Logger) error {
	return v.removeFinalizerFromPVC(pvc, pvcVRFinalizerProtected, log)
}

func (v *VRGInstance) removeFinalizerFromPVC(pvc *corev1.PersistentVolumeClaim,
	finalizer string,
	log logr.Logger) error {
	if containsString(pvc.ObjectMeta.Finalizers, finalizer) {
		pvc.ObjectMeta.Finalizers = removeString(pvc.ObjectMeta.Finalizers, finalizer)
		if err := v.reconciler.Update(v.ctx, pvc); err != nil {
			log.Error(err, "Failed to remove finalizer", "finalizer", finalizer)

			return fmt.Errorf("failed to remove finalizer (%s) from PersistentVolumeClaim resource"+
				" (%s/%s) detected as part of VolumeReplicationGroup (%s/%s), %w",
				finalizer, pvc.Namespace, pvc.Name, v.instance.Namespace, v.instance.Name, err)
		}
	}

	return nil
}*/

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
