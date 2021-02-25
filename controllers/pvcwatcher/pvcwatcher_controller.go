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

package pvcwatcher

import (
	"context"
	"fmt"
	"reflect"

	"github.com/go-logr/logr"
	"github.com/prometheus/common/log"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
)

// VolumeReplicationGroupReconciler reconciles a VolumeReplicationGroup object
type PersistentVolumeClaimReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// reconcilePV determines whether we want to reconcile a given PVC
func reconcilePVC(mgr manager.Manager, obj runtime.Object) bool {
	pvc, ok := obj.(*corev1.PersistentVolumeClaim)
	if !ok {
		return false
	}

	var vrgs ramendrv1alpha1.VolumeReplicationGroupList

	err := mgr.GetClient().List(context.TODO(), &vrgs)
	if err != nil {
		log.Errorf("failed to get list of VolumeReplicationGroup CRs")

		return false
	}

	pvcNamespace := pvc.Namespace

	// decide if reconcile request needs to be sent to the
	// corresponding VolumeReplicationGroup CR by:
	// - whether there is a VolumeReplicationGroup CR in the namespace
	//   to which the the pvc belongs to.
	// - whether the labels on pvc match the label selectors from
	//    VolumeReplicationGroup CR.
	for _, vrg := range vrgs.Items {
		if vrg.Namespace == pvcNamespace {
			vrgLabelSelector := vrg.Spec.ApplicationLabels
			selector, err := metav1.LabelSelectorAsSelector(&vrgLabelSelector)
			// continue if we fail to get the labels for this object hoping
			// that pvc might actually belong to  some other vrg instead of
			// this. If not found, then reconcile request would not be sent
			if err != nil {
				log.Error("failed to get the label selector as selector")

				continue
			}

			if selector.Matches(labels.Set(pvc.GetLabels())) {
				log.Info("found volume replication group that belongs to namespace with matching labels: ", pvcNamespace)

				return true
			}
		}
	}

	return false
}

func pvcPredicateFunc(mgr manager.Manager) predicate.Funcs {
	// predicate functions send reconcile requests for create and delete events.
	// For them the filtering of whether the pvc belongs to the any of the
	// VolumeReplicationGroup CRs and identifying such a CR is done in the
	// map function by comparing namespaces and labels.
	// But for update of pvc, the reconcile request should be sent only for
	// spec changes. Do that comparison here.
	pvcPredicate := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			log.Info("create event from pvc")

			return reconcilePVC(mgr, e.Object)
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

			matches := reconcilePVC(mgr, e.ObjectNew)
			if matches {
				return !reflect.DeepEqual(oldPVC.Spec, newPVC.Spec)
			}

			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			log.Info("delete event from pvc")

			return reconcilePVC(mgr, e.Object)
		},
	}

	return pvcPredicate
}

// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=persistentvolumes,verbs=get;list;watch

// Reconcile ...
func (r *PersistentVolumeClaimReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = r.Log.WithValues("PVC Reconciler ", req.NamespacedName)

	pvc := &corev1.PersistentVolumeClaim{}
	if err := r.Get(context.TODO(), req.NamespacedName, pvc); err != nil {
		if errors.IsNotFound(err) {
			log.Info("PersistentVolumeClaim not found")

			return reconcile.Result{}, nil
		}

		return ctrl.Result{}, fmt.Errorf("failed to reconcile PersistentVolumeClaim %w", err)
	}

	log.Info("Processing pvc in namespace: ", "Namespaced Name: ", req.NamespacedName)

	err := r.HandlePersistentVolumeClaim(ctx, pvc)
	if err != nil {
		log.Info("Failed to handle the Persistent Volume Claim. ", "PVC Name: ", pvc.Name)

		return ctrl.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *PersistentVolumeClaimReconciler) HandlePersistentVolume(
	pv *corev1.PersistentVolume) error {
	template := "%-42s%-42s%-8s%-10s%-8s\n"

	log.Info("---------- PV(PVC WATCHER) ----------")
	log.Infof(template, "NAME", "CLAIM REF", "STATUS", "CAPACITY", "RECLAIM POLICY")

	var capacity resource.Quantity

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

	log.Info("Total capacity Used: ", capacity.String())

	return nil
}

func (r *PersistentVolumeClaimReconciler) HandlePersistentVolumeClaim(
	ctx context.Context,
	pvc *corev1.PersistentVolumeClaim) error {
	// if the PVC is not bound yet, dont proceed.
	if pvc.Status.Phase != corev1.ClaimBound {
		return fmt.Errorf("PVC is not yet bound status: %v", pvc.Status.Phase)
	}

	volumeName := pvc.Spec.VolumeName
	pv := &corev1.PersistentVolume{}
	pvObjectKey := client.ObjectKey{
		Name: pvc.Spec.VolumeName,
	}

	if err := r.Get(ctx, pvObjectKey, pv); err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			log.Info("PersistentVolume resource not found. Ignoring since object must be deleted", volumeName)
		}

		return fmt.Errorf("unable to PV %v (err: %w)", pvObjectKey, err)
	}

	err := r.HandlePersistentVolume(pv)
	if err != nil {
		return fmt.Errorf("failed to handle PV %v (err: %w)", pvObjectKey, err)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PersistentVolumeClaimReconciler) SetupWithManager(mgr ctrl.Manager) error {
	pvcPredicate := pvcPredicateFunc(mgr)

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.PersistentVolumeClaim{}, builder.WithPredicates(pvcPredicate)).
		Complete(r)
}
