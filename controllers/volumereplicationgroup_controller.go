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

	volrep "github.com/csi-addons/volume-replication-operator/api/v1alpha1"
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

		return filterPVC(mgr, pvc, log.WithValues("pvcname", pvc.Name, "pvcNamespace", pvc.Namespace))
	}))

	r.Log.Info("Adding VolumeReplicationGroup and PersistentVolumeClaims controllers")

	return ctrl.NewControllerManagedBy(mgr).
		For(&ramendrv1alpha1.VolumeReplicationGroup{}).
		Watches(&source.Kind{Type: &corev1.PersistentVolumeClaim{}}, pvcMapFun, builder.WithPredicates(pvcPredicate)).
		Owns(&volrep.VolumeReplication{}).
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
// +kubebuilder:rbac:groups=replication.storage.openshift.io,resources=volumereplications,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=persistentvolumes,verbs=get;list;watch

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

	if v.instance.Spec.ReplicationState != volrep.Primary &&
		v.instance.Spec.ReplicationState != volrep.Secondary {
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
	case v.instance.Spec.ReplicationState == volrep.Primary:
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
	vrgFinalizerName = "volumereplicationgroups.ramendr.openshift.io/protection"
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

func (v *VRGInstance) finalizeChildren() error {
	return v.deleteRelatedItems()
}

func (v *VRGInstance) deleteRelatedItems() error {
	// add logic to perform the following things.
	// - Remove the VolumeReplication CRs associated with pvcs
	//   belonging to the application protected by this VolumeReplicationGroup CR.
	// - Delete the backed up PV metadata from the backup store.
	//
	// As of now deletion of VolumeReplication CRs is done.
	// TODO: Delete the backed up PV metadata from the backup store.
	return v.deleteVolumeReplicationResources()
}

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
	requeue = v.handlePersistentVolumeClaims()

	if requeue {
		v.log.Info("Requeuing resource")

		return ctrl.Result{Requeue: requeue}, nil
	}

	return ctrl.Result{}, nil
}

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

// handlePersistentVolumeClaims creates VolumeReplication CR for each pvc
// from pvcList. If it fails (even for one pvc), then requeue is set to true.
// For now, keeping creation of VolumeReplication CR and backing up of PV
// metadata in separate functions and calling them separately. In future,
// if creation of VolumeReplication CR and backing up of pv metadata should
// happen together (i.e for each pvc create VolumeReplication CR and then
// backup corresponding PV metadata), then it can be put in a single function.
func (v *VRGInstance) handlePersistentVolumeClaims() bool {
	requeue := true

	if requeueResult := v.createVolumeReplicationResources(); requeueResult {
		v.log.Info("Requeuing resource")

		return requeue
	}

	if requeueResult := v.handlePersistentVolumes(); requeueResult {
		v.log.Info("Requeuing resource")

		return requeue
	}

	return !requeue
}

func (v *VRGInstance) createVolumeReplicationResources() bool {
	requeue := false

	for _, pvc := range v.pvcList.Items {
		pvcNamespacedName := types.NamespacedName{Name: pvc.Name, Namespace: pvc.Namespace}
		log := v.log.WithValues("pvc", pvcNamespacedName.String())

		// if the PVC is not bound yet, dont proceed.
		if pvc.Status.Phase != corev1.ClaimBound {
			log.Info("Requeuing as PersistentVolumeClaim is not bound",
				"pvcPhase", pvc.Status.Phase)

			// continue processing other PVCs and return the need to requeue.
			requeue = true

			continue
		}

		// createOrUpdateVolumeReplicationResource returns false only if
		//     - For the pvc there is a corresponding VolumeReplication (VR)
		//       resource available who state matches with the state of
		//       the VolumeReplicationGroup (VRG) resource and the status
		//       is healthy.
		// In such cases, no need to reconcile the VolumeReplicationGroup
		// resource. Situations which need reconilation of VRG are:
		//     i) VR was not not found and got created.
		//     ii)VR was not found and its creation failed.
		//     iii) VR state and VRG state did not match and VR update was done
		//     iv) VR state and VRG state did not match and VR update failed
		//     v) VR status is not shown as healthy
		if requeueResult := v.createOrUpdateVolumeReplicationResource(pvc); requeueResult {
			requeue = true
		}
	}

	return requeue
}

func (v *VRGInstance) createOrUpdateVolumeReplicationResource(pvc corev1.PersistentVolumeClaim) bool {
	requeue := true
	volRep := &volrep.VolumeReplication{}

	err := v.reconciler.Get(v.ctx, types.NamespacedName{Name: pvc.Name, Namespace: pvc.Namespace}, volRep)
	if err != nil {
		// As of now this function does not return anything. Calling this
		// function means reconcile is needed if getting VolumeReplication
		// resource failed. When the behavior of this function changes, then
		// the return value has to be checked and appropriately handled.
		v.createRelatedItems(pvc, err)

		return requeue
	}

	if err := v.volRepStateCheck(volRep); err != nil {
		return requeue
	}

	if err := v.volRepStatusCheck(volRep); err != nil {
		return requeue
	}

	return !requeue
}

// This function does not return anything. Since, this function is
// called upon not finding the VolumeReplication resource for the PVC,
// it's return value is interpreted as true irrespective of whether it
// successfully created the related items or not.
// If successfully created everything, then to check the state and
// status of the VolumeReplication resource a reconcile has to happen.
// If something failed, then again to correctly create the necessary
// resources (VolumeReplication CR and upload of PV metadata) a
// reconcile is needed.
//
// This function assumes that error is not nil. The caller of this
// function has to do the check for err being nil or not. If that
// check is added here or if the entire logic of this function is
// moved to the caller of this function, then the linter complains
// due to "nestif" complexity being high.
func (v *VRGInstance) createRelatedItems(pvc corev1.PersistentVolumeClaim, err error) {
	if errors.IsNotFound(err) {
		v.log.Info("Uploading PersistentVolume")

		if err = v.uploadPV(pvc); err != nil {
			v.log.Error(err, "failed to upload PV metadata")
			// There's no point enabling PV data replication if
			// replication of PV metadata to object store has failed.
			return
		}

		if err = v.createVolumeReplicationForPVC(pvc); err != nil {
			v.log.Info("Requeuing due to failure in creating VolumeReplication resource for pvc",
				"errorValue", err)
		}
		// requeue is set to true if VR is freshly created. This is to ensure
		// that in the next reconcile loop, VR status can be checked to see
		// whether it is in a correct expected state or not.
		return
	}

	// Being here means, creation of VolumeReplication resource failed.
	v.log.Info("Requeuing due to failure in getting VolumeReplication resource for pvc",
		"errorValue", err)
}

func (v *VRGInstance) volRepStateCheck(volRep *volrep.VolumeReplication) error {
	if v.instance.Spec.ReplicationState == volRep.Spec.ReplicationState {
		return nil
	}

	v.log.Info("VolumeReplicationGroup state and VolumeReplication state mismatch")

	volRep.Spec.ReplicationState = v.instance.Spec.ReplicationState
	if err := v.reconciler.Update(v.ctx, volRep); err != nil {
		v.log.Info("Failed to update the VolumeReplicate state (%s/%s)", volRep.Name, volRep.Namespace)

		return fmt.Errorf("failed to update the VolumeReplication state (%s/%s)", volRep.Name, volRep.Namespace)
	}

	// Reconcile has to happen irrespective of whether the state update
	// succeeded or not.
	return fmt.Errorf("updated the VolumeReplication resource (%s/%s). Reconciling", volRep.Name, volRep.Namespace)
}

func (v *VRGInstance) volRepStatusCheck(volRep *volrep.VolumeReplication) error {
	// Currently it is not sure what is the expected State in the volrep.Status for
	// each ReplicationState for volrep.spec. Hence, comparing only whether failures
	// to determine the return value. If there are more tight constraints for the
	// expected status of different states, then below conditions have to be changed.
	if volRep.Spec.ReplicationState == volrep.Primary && volRep.Status.State == volrep.ReplicationFailure {
		v.log.Info("Primary VolumeReplication is not replicating")

		return fmt.Errorf("primary VolumeReplication resource not replicating %s/%s", volRep.Name, volRep.Namespace)
	}

	if volRep.Spec.ReplicationState == volrep.Secondary && volRep.Status.State == volrep.ReplicationFailure {
		v.log.Info("Secondary VolumeReplication is not replicating/resyncing")

		return fmt.Errorf("secondary VolumeReplication resource not replicating/resyncing %s/%s",
			volRep.Name, volRep.Namespace)
	}

	if volRep.Spec.ReplicationState == volrep.Resync && volRep.Status.State != volrep.Resyncing {
		v.log.Info("Resync VolumeReplication is not resyncing")

		return fmt.Errorf("resync VolumeReplication resource not resyncing %s/%s", volRep.Name, volRep.Namespace)
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

// createVolumeReplicationCRForPVC creates a VolumeReplication CR with a PVC as its data source.
func (v *VRGInstance) createVolumeReplicationForPVC(pvc corev1.PersistentVolumeClaim) error {
	cr := &volrep.VolumeReplication{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvc.Name,
			Namespace: pvc.ObjectMeta.Namespace,
		},
		Spec: volrep.VolumeReplicationSpec{
			DataSource: corev1.TypedLocalObjectReference{
				Kind: "PersistentVolumeClaim",
				Name: pvc.Name,
			},
			// Is it better to set the VolumeReplicationClass of VR
			// by referring to StorageClass of the pvc instead of
			// inheriting the VolumeReplicationClass from VRG?
			VolumeReplicationClass: v.instance.Spec.VolumeReplicationClass,
			// Get the state of VolumeReplication from
			// VolumeReplicationGroupSpec
			ReplicationState: v.instance.Spec.ReplicationState,
		},
	}

	v.log.Info("Creating VolumeReplication resource", "resource", cr)

	return v.reconciler.Create(v.ctx, cr)
}

// HandlePersistentVolumes handles bound PVs
func (v *VRGInstance) handlePersistentVolumes() bool {
	requeue := false

	if err := v.printPersistentVolumes(); err != nil {
		v.log.Info("Failed to print the PersistentVolume resources for PersistentVolumesClaims resource list",
			"errorValue", err)

		requeue = true
	}

	return requeue
}

// Prints the bound Persistent Volumes.
func (v *VRGInstance) printPersistentVolumes() error {
	template := "%-42s%-42s%-8s%-10s%-8s\n"

	v.log.Info("---------- PVs ----------")
	v.log.Info(fmt.Sprintf(template, "NAME", "CLAIM REF", "STATUS", "CAPACITY", "RECLAIM POLICY"))

	for _, pvc := range v.pvcList.Items {
		// if the PVC is not bound yet, dont proceed.
		if pvc.Status.Phase != corev1.ClaimBound {
			v.log.Info("PersistentVolumeClaim is not bound", "pvcName", pvc.Name,
				"pvcNamespace", pvc.Namespace, "pvcPhase", pvc.Status.Phase)

			return fmt.Errorf("persistentVolumeClaim (%s/%s) is not bound (phase: %v)",
				pvc.Name, pvc.Namespace, pvc.Status.Phase)
		}

		volumeName := pvc.Spec.VolumeName
		pv := &corev1.PersistentVolume{}
		pvObjectKey := client.ObjectKey{
			Name: pvc.Spec.VolumeName,
		}

		if err := v.reconciler.Get(v.ctx, pvObjectKey, pv); err != nil {
			if errors.IsNotFound(err) {
				// Request object not found, could have been deleted after reconcile request.
				// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
				// Return and don't requeue
				v.log.Info("Ignoring PersistentVolumeClaim as PersistentVolume resource is not found",
					"pvcName", pvc.Name, "pvcNamespace", pvc.Namespace, "pvName", volumeName)

				continue
			}

			return fmt.Errorf("failed to get persistentVolume (%s) for PersistentVolumeClaim (%s/%s), %w",
				volumeName, pvc.Name, pvc.Namespace, err)
		}

		claimRefUID := ""
		if pv.Spec.ClaimRef != nil {
			claimRefUID += pv.Spec.ClaimRef.Namespace
			claimRefUID += "/"
			claimRefUID += pv.Spec.ClaimRef.Name
		}

		reclaimPolicyStr := string(pv.Spec.PersistentVolumeReclaimPolicy)
		quant := pv.Spec.Capacity[corev1.ResourceStorage]
		v.log.Info(fmt.Sprintf(template, pv.Name, claimRefUID, string(pv.Status.Phase), quant.String(),
			reclaimPolicyStr))
	}

	return nil
}

// processAsSecondary reconciles the current instance of VRG as secondary
func (v *VRGInstance) processAsSecondary() (ctrl.Result, error) {
	return v.processAsPrimary()
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
