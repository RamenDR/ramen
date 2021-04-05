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

			requeue = true

			// continue processing other PVCs and return the need to requeue
			continue
		}

		volRep := &volrep.VolumeReplication{}

		err := v.reconciler.Get(v.ctx, pvcNamespacedName, volRep)
		if err != nil {
			if errors.IsNotFound(err) {
				requeue = v.createVolumeReplication(pvc)
			} else {
				// requeue on failure to ensure any PVC not having a corresponding VR CR
				requeue = true
				log.Error(err, "failed to get VolumeReplication resource")
			}
		}
	}

	return requeue
}

func (v *VRGInstance) createVolumeReplication(
	pvc corev1.PersistentVolumeClaim) (requeue bool) {
	requeue = false

	// Prior to creating VR, upload PV metadata to object store
	if err := v.uploadPV(pvc); err != nil {
		v.log.Error(err, "failed to upload PV metadata")

		requeue = true
		// PV metadata replication to object store has failed.
		// No point in creating VR to enable PV data replication.
		return
	}

	if err := v.createVolumeReplicationForPVC(pvc); err != nil {
		v.log.Error(err, "failed to create VolumeReplication resource")

		requeue = true
	}

	return
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
			Namespace: pvc.Namespace,
		},
		Spec: volrep.VolumeReplicationSpec{
			DataSource: &corev1.TypedLocalObjectReference{
				Kind: "PersistentVolumeClaim",
				Name: pvc.Name,
			},
			// Get the state of VolumeReplication from
			// VolumeReplicationGroupSpec
			State: v.instance.Spec.ReplicationState,
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
