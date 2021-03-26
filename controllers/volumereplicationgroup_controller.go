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
	"reflect"

	"github.com/go-logr/logr"
	"github.com/prometheus/common/log"
	volrep "github.com/shyamsundarr/volrep-shim-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
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

type VRGInstance struct {
	reconciler *VolumeReplicationGroupReconciler
	ctx        context.Context
	instance   *ramendrv1alpha1.VolumeReplicationGroup
	pvcList    *corev1.PersistentVolumeClaimList
}

const (
	vrgFinalizerName = "volumereplicationgroup.storage.io"
)

func newPredicateFunc() predicate.Funcs {
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
			log.Debug("create event from pvc")

			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldPVC, ok := e.ObjectOld.DeepCopyObject().(*corev1.PersistentVolumeClaim)
			if !ok {
				log.Error("Update: failed to read old PVC, not reconciling")

				return false
			}
			newPVC, ok := e.ObjectNew.DeepCopyObject().(*corev1.PersistentVolumeClaim)
			if !ok {
				log.Error("Update: failed to read new PVC, not reconciling")

				return false
			}
			log.Debug("Update event from pvc")

			return !reflect.DeepEqual(oldPVC.Spec, newPVC.Spec)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			log.Info("delete event from pvc")

			// return false. Because, for delete event on a pvc, it is
			// the finalizer which would actually delete the backed up
			// pv and the VolumeReplication CR created for that pvc.
			// In future if there arises a need to unconditionally
			// do a reconcile, return true. If there is a need to do
			// reconcile for certain conditions, then add the necessary
			// checks here.
			return false
		},
	}

	return pvcPredicate
}

func filterPVC(mgr manager.Manager, pvc *corev1.PersistentVolumeClaim) []reconcile.Request {
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
		log.Errorf("failed to get list of VolumeReplicationGroup CRs")

		return []reconcile.Request{}
	}

	for _, vrg := range vrgs.Items {
		vrgLabelSelector := vrg.Spec.PVCSelector
		selector, err := metav1.LabelSelectorAsSelector(&vrgLabelSelector)
		// continue if we fail to get the labels for this object hoping
		// that pvc might actually belong to  some other vrg instead of
		// this. If not found, then reconcile request would not be sent
		if err != nil {
			log.Error("failed to get the label selector as selector")

			continue
		}

		if selector.Matches(labels.Set(pvc.GetLabels())) {
			log.Info("found volume replication group that belongs to namespace with matching labels: ", pvc.Namespace)

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
	_ = r.Log.WithValues("VolumeReplicationGroup", req.NamespacedName)

	log.Infof("entering reconcile for %p", &r)

	defer log.Infof("exiting reconcile for %p", &r)

	v := VRGInstance{
		reconciler: r,
		ctx:        ctx,
		instance:   &ramendrv1alpha1.VolumeReplicationGroup{},
		pvcList:    &corev1.PersistentVolumeClaimList{},
	}

	// Fetch the VolumeReplicationGroup instance
	if err := r.Get(ctx, req.NamespacedName, v.instance); err != nil {
		if errors.IsNotFound(err) {
			log.Info("VolumeReplicationGroup resource not found. Ignoring since object must have been deleted: ",
				req.NamespacedName)

			return ctrl.Result{}, nil
		}

		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get VolumeReplicationGroup")

		return ctrl.Result{}, fmt.Errorf("failed to reconcile VolumeReplicationGroup %w", err)
	}

	if err := v.updatePVCList(); err != nil {
		log.Error(err, "Handling of Persistent Volume Claims of application failed")

		return ctrl.Result{}, err
	}

	if v.instance.Spec.ReplicationState != volrep.ReplicationPrimary &&
		v.instance.Spec.ReplicationState != volrep.ReplicationSecondary {
		// No requeue, as there is no reconcile till user changes desired spec to a valid value
		// TODO: Update status of VRG to reflect error in reconcile for user consumption, and requeue
		// without returning errors
		return ctrl.Result{},
			fmt.Errorf("invalid or unknown replication state detected (deleted %v, desired replicationState %v)",
				v.instance.GetDeletionTimestamp().IsZero(),
				v.instance.Spec.ReplicationState)
	}

	log.Info("Processing VolumeReplicationGroup ", v.instance.Spec, " in ns: ", req.NamespacedName)

	switch {
	case !v.instance.GetDeletionTimestamp().IsZero():
		return v.finalizeVRG()
	case v.instance.Spec.ReplicationState == volrep.ReplicationPrimary:
		return v.processAsPrimary()
	default: // Secondary, not primary and not deleted
		return v.processAsSecondary()
	}
}

func (v *VRGInstance) finalizeVRG() (ctrl.Result, error) {
	if !containsString(v.instance.ObjectMeta.Finalizers, vrgFinalizerName) {
		return ctrl.Result{}, nil
	}

	if err := v.finalizeChildren(); err != nil {
		log.Errorf("failed to finalize child resources: %s/%s", v.instance.Name, v.instance.Namespace)

		return ctrl.Result{Requeue: true}, nil
	}

	if err := v.removeFinalizer(vrgFinalizerName); err != nil {
		log.Errorf("failed to remove the finalizer from vrg: %s/%s", v.instance.Name, v.instance.Namespace)

		return ctrl.Result{Requeue: true}, nil
	}

	// stop reconciliation as the item is being deleted
	return ctrl.Result{}, nil
}

// processAsPrimary reconciles the current instance of VRG as primary
func (v *VRGInstance) processAsPrimary() (ctrl.Result, error) {
	if err := v.addFinalizer(vrgFinalizerName); err != nil {
		log.Errorf("failed to add the finalizer to vrg: %s/%s", v.instance.Name, v.instance.Namespace)

		return ctrl.Result{Requeue: true}, nil
	}

	requeue := false
	requeue = v.handlePersistentVolumeClaims()

	if requeue {
		return ctrl.Result{Requeue: requeue}, nil
	}

	return ctrl.Result{}, nil
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

func (v *VRGInstance) addFinalizer(finalizer string) error {
	if !containsString(v.instance.ObjectMeta.Finalizers, finalizer) {
		v.instance.ObjectMeta.Finalizers = append(v.instance.ObjectMeta.Finalizers, finalizer)
		if err := v.reconciler.Update(v.ctx, v.instance); err != nil {
			return fmt.Errorf("failed to add the finalizer to VRG CR %w", err)
		}
	}

	return nil
}

func (v *VRGInstance) finalizeChildren() error {
	if err := v.deleteRelatedItems(); err != nil {
		return fmt.Errorf("failed to delete VolumeReplicationGroup related resources %w", err)
	}

	return nil
}

func (v *VRGInstance) removeFinalizer(finalizer string) error {
	v.instance.ObjectMeta.Finalizers = removeString(v.instance.ObjectMeta.Finalizers, finalizer)
	if err := v.reconciler.Update(v.ctx, v.instance); err != nil {
		return fmt.Errorf("failed to remove the finalizer from VRG CR %w", err)
	}

	return nil
}

func (v *VRGInstance) deleteRelatedItems() error {
	// add logic to perform the following things.
	// - Remove the VolumeReplication CRs associated with pvcs
	//   belonging to the application protected by this VolumeReplicationGroup CR.
	// - Delete the backed up PV metadata from the backup store.
	//
	// As of now deletion of VolumeReplication CRs is done.
	// TODO: Delete the backed up PV metadata from the backup store.
	if err := v.deleteVolumeReplicationResources(); err != nil {
		return fmt.Errorf("failed to delete the VolumeReplication CRs %w", err)
	}

	return nil
}

func (v *VRGInstance) deleteVolumeReplicationResources() error {
	vrList := &volrep.VolumeReplicationList{}

	log.Info("Namespace: ", v.instance.Namespace)
	listOptions := []client.ListOption{
		client.InNamespace(v.instance.Namespace),
	}

	if err := v.reconciler.List(v.ctx, vrList, listOptions...); err != nil {
		if errors.IsNotFound(err) {
			log.Info("Listing failed as there are no VR CRs")

			return nil
		}

		log.Error(err, "Failed to list VolumeReplication CRs")

		return fmt.Errorf("failed to list VolumeReplication CRs %w", err)
	}

	log.Info("volumeReplication CR List: ", vrList)

	for _, vr := range vrList.Items {
		// Stop at the first instance of failure and handle
		// the deletion of remaining VolumeReplication CRs
		// in the next reconcile request.
		tmpVR := vr
		if err := v.reconciler.Delete(v.ctx, &tmpVR); err != nil {
			return fmt.Errorf("failed to delete the VolumeReplication CR %s:%s", vr.Name, vr.Namespace)
		}
	}

	return nil
}

// updatePVCList fetches and updates the PVC list to process for the current instance of VRG
func (v *VRGInstance) updatePVCList() error {
	labelSelector := v.instance.Spec.PVCSelector

	log.Info("Label Selector: ", labels.Set(labelSelector.MatchLabels))
	log.Info("Namespace: ", v.instance.Namespace)
	listOptions := []client.ListOption{
		client.InNamespace(v.instance.Namespace),
		client.MatchingLabels(labelSelector.MatchLabels),
	}

	if err := v.reconciler.List(v.ctx, v.pvcList, listOptions...); err != nil {
		log.Error(err, "Failed to list PVC")

		return fmt.Errorf("failed to list PVC %w", err)
	}

	log.Info("Found PVCs, count: ", len(v.pvcList.Items))

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
		log.Error("failed to get or create VolumeReplication CRs for pvc list")

		return requeue
	}

	if requeueResult := v.handlePersistentVolumes(); requeueResult {
		log.Error("failed to handle Persistent Volumes for pvc list")

		return requeue
	}

	return !requeue
}

// HandlePersistentVolumes handles bound PVs
func (v *VRGInstance) handlePersistentVolumes() bool {
	requeue := false

	if err := v.printPersistentVolumes(); err != nil {
		log.Error(err, "failed to print the persistent volumes for pvc list %w")

		requeue = true
	}

	return requeue
}

// Prints the bound Persistent Volumes.
func (v *VRGInstance) printPersistentVolumes() error {
	template := "%-42s%-42s%-8s%-10s%-8s\n"

	log.Info("---------- PVs ----------")
	log.Infof(template, "NAME", "CLAIM REF", "STATUS", "CAPACITY", "RECLAIM POLICY")

	var capacity resource.Quantity

	for _, pvc := range v.pvcList.Items {
		// if the PVC is not bound yet, dont proceed.
		if pvc.Status.Phase != corev1.ClaimBound {
			return fmt.Errorf("PVC is not yet bound status: %v", pvc.Status.Phase)
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
				log.Info("PersistentVolume resource not found. Ignoring since object must be deleted", volumeName)

				continue
			}

			return fmt.Errorf("unable to PV %v (err: %w)", pvObjectKey, err)
		}

		claimRefUID := ""
		if pv.Spec.ClaimRef != nil {
			claimRefUID += pv.Spec.ClaimRef.Namespace
			claimRefUID += "/"
			claimRefUID += pv.Spec.ClaimRef.Name
		}

		reclaimPolicyStr := string(pv.Spec.PersistentVolumeReclaimPolicy)

		quant := pv.Spec.Capacity[corev1.ResourceStorage]
		capacity.Add(quant)
		log.Infof(template, pv.Name, claimRefUID, string(pv.Status.Phase), quant.String(),
			reclaimPolicyStr)
	}

	log.Info("Total capacity Used: ", capacity.String())

	return nil
}

func (v *VRGInstance) createVolumeReplicationResources() bool {
	requeue := false

	for _, pvc := range v.pvcList.Items {
		// if the PVC is not bound yet, dont proceed.
		if pvc.Status.Phase != corev1.ClaimBound {
			log.Errorf("PVC is not yet bound status: %v", pvc.Status.Phase)

			requeue = true

			// continue processing other PVCs and return the need to requeue
			continue
		}

		volRep := &volrep.VolumeReplication{}

		err := v.reconciler.Get(v.ctx, types.NamespacedName{Name: pvc.Name, Namespace: pvc.Namespace}, volRep)
		if err != nil {
			if errors.IsNotFound(err) {
				if err = v.createVolumeReplicationForPVC(pvc); err == nil {
					continue
				}
			}

			// requeue on failure to ensure any PVC not having a corresponding VR CR
			requeue = true

			log.Error(err, "failed to get or create VolumeReplication CR for PVC ", pvc.Name, pvc.Namespace)
		}
	}

	return requeue
}

// createVolumeReplicationCRForPVC creates a VolumeReplication CR with a PVC as its data source.
func (v *VRGInstance) createVolumeReplicationForPVC(pvc corev1.PersistentVolumeClaim) error {
	cr := &volrep.VolumeReplication{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvc.Name,
			Namespace: pvc.ObjectMeta.Namespace,
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

	log.Info("Creating CR: ", cr)

	return v.reconciler.Create(v.ctx, cr)
}

// SetupWithManager sets up the controller with the Manager.
func (r *VolumeReplicationGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	pvcPredicate := newPredicateFunc()
	pvcMapFun := handler.EnqueueRequestsFromMapFunc(handler.MapFunc(func(obj client.Object) []reconcile.Request {
		pvc, ok := obj.(*corev1.PersistentVolumeClaim)
		if !ok {
			// Not a pvc, returning empty
			log.Errorf("pvc handler received non-pvc")

			return []reconcile.Request{}
		}

		log.Info("pvc Namespace: ", pvc.Namespace)
		req := filterPVC(mgr, pvc)

		return req
	}))

	log.Info("Adding VRG and PVC controller")

	return ctrl.NewControllerManagedBy(mgr).
		For(&ramendrv1alpha1.VolumeReplicationGroup{}).
		Watches(&source.Kind{Type: &corev1.PersistentVolumeClaim{}}, pvcMapFun, builder.WithPredicates(pvcPredicate)).
		// The actual thing that the controller owns is
		// the VolumeReplication CR. Change the below
		// line when VolumeReplication CR is ready.
		Owns(&ramendrv1alpha1.VolumeReplicationGroup{}).
		Complete(r)
}
