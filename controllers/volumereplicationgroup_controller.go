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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
)

// AddToSchemes may be used to add all resources defined in the project to a Scheme
var AddToSchemes runtime.SchemeBuilder

// AddToScheme adds all Resources to the Scheme
func AddToScheme(s *runtime.Scheme) error {
	return AddToSchemes.AddToScheme(s)
}

// AddToManagerFuncs is a list of functions to add all Controllers to the Manager
var AddToManagerFuncs = []func(manager.Manager) error{
	Add,
}

func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// AddToManager adds all Controllers to the Manager
func AddToManager(m manager.Manager) error {
	for _, f := range AddToManagerFuncs {
		if err := f(m); err != nil {
			return err
		}
	}

	return nil
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &VolumeReplicationGroupReconciler{
		Client: mgr.GetClient(),
		Log:    ctrl.Log.WithName("controllers").WithName("pvc"),
		Scheme: mgr.GetScheme(),
	}
}

// VolumeReplicationGroupReconciler reconciles a VolumeReplicationGroup object
type VolumeReplicationGroupReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

func AddSchemesAndControllers(mgr manager.Manager) error {
	ctrl.Log.WithName("setup").Info("Registering Components.")

	// Setup Scheme for all resources
	if err := AddToScheme(mgr.GetScheme()); err != nil {
		ctrl.Log.WithName("setup").Error(err, "")

		return err
	}

	// Setup all Controllers
	if err := AddToManager(mgr); err != nil {
		ctrl.Log.WithName("setup").Error(err, "")

		return err
	}

	return nil
}

func getPredicateFunc() predicate.Funcs {
	// predicate functions send reconcile requests for create and delete events.
	// For them the filtering of whether the pvc belongs to the any of the
	// VolumeReplicationGroup CRs and identifying such a CR is done in the
	// map function by comparing namespaces and labels.
	// But for update of pvc, the reconcile request should be sent only for
	// spec changes. Do that comparison here.
	pvcPredicate := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			log.Info("create event from pvc")

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

			return !reflect.DeepEqual(oldPVC.Spec, newPVC.Spec)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			log.Info("delete event from pvc")

			return true
		},
	}

	return pvcPredicate
}

func filterPVC(mgr manager.Manager, pvc *corev1.PersistentVolumeClaim) []reconcile.Request {
	req := []reconcile.Request{}

	var vrgs ramendrv1alpha1.VolumeReplicationGroupList

	err := mgr.GetClient().List(context.TODO(), &vrgs)
	if err != nil {
		log.Errorf("failed to get list of VolumeReplicationGroup CRs")

		return []reconcile.Request{}
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

				req = append(req, reconcile.Request{NamespacedName: types.NamespacedName{Name: vrg.Name, Namespace: vrg.Namespace}})
			}
		}
	}

	return req
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("pvc-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return fmt.Errorf("failed to add a new controller for pvc %w", err)
	}

	pvcPredicate := getPredicateFunc()

	pvcMapFun := handler.EnqueueRequestsFromMapFunc(handler.MapFunc(func(obj client.Object) []reconcile.Request {
		pvc, ok := obj.(*corev1.PersistentVolumeClaim)
		if !ok {
			// Not a pvc, returning empty
			log.Errorf("pvc handler received non-pvc")

			return []reconcile.Request{}
		}

		pvcNamespace := pvc.Namespace
		log.Info("pvc Namespace: ", pvcNamespace)

		// get the list of VolumeReplicationGroup resources
		var vrgs ramendrv1alpha1.VolumeReplicationGroupList
		err = mgr.GetClient().List(context.TODO(), &vrgs)
		if err != nil {
			log.Errorf("failed to get list of VolumeReplicationGroup CRs")

			return []reconcile.Request{}
		}

		req := filterPVC(mgr, pvc)

		return req
	}))

	err = c.Watch(&source.Kind{Type: &corev1.PersistentVolumeClaim{}}, pvcMapFun, pvcPredicate)
	if err != nil {
		return fmt.Errorf("failed to add a watcher for pvc %w", err)
	}

	log.Info("Successfully added the pvc watching controller to the manager")

	return nil
}

// nolint: lll // disabling line length linter
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=volumereplicationgroups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=volumereplicationgroups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=volumereplicationgroups/finalizers,verbs=update
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
func (v *VolumeReplicationGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = v.Log.WithValues("volumereplicationgroup reconciler", req.NamespacedName)

	// Fetch the VolumeReplicationGroup instance
	volRepGroup := &ramendrv1alpha1.VolumeReplicationGroup{}
	if err := v.Get(ctx, req.NamespacedName, volRepGroup); err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			log.Info("VolumeReplicationGroup resource not found. Ignoring since object must have been deleted: ",
				req.NamespacedName)

			return ctrl.Result{}, nil
		}

		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get VolumeReplicationGroup")

		return ctrl.Result{}, fmt.Errorf("failed to reconcile VolumeReplicationGroup %w", err)
	}

	log.Info("Processing VolumeReplicationGroup ", volRepGroup.Spec, " in ns: ", req.NamespacedName)

	pvcList, err := v.HandlePersistentVolumeClaims(ctx, volRepGroup)
	if err != nil {
		log.Error("Handling of Persistent Volume Claims of application failed: ", volRepGroup.Spec.ApplicationName)

		return ctrl.Result{}, err
	}

	err = v.HandlePersistentVolumes(ctx, pvcList)
	if err != nil {
		log.Error("Handling of Persistent Volumes for PVCs of application failed: ", volRepGroup.Spec.ApplicationName)

		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// Pseudo code for reconciler
/*
   // Check if the VolumeReplication CR already exists for the PVs of this application, if not create a new one
   for all PVs of this application {
	   volRep := &appsv1.Deployment{}
	   err = v.Get(ctx, types.NamespacedName{Name: volRepGroup.Name, Namespace: volRepGroup.Namespace}, volRep)
	   if err != nil && errors.IsNotFound(err) {
		   // Define a new VolumeReplication CR
		   newVolRep := v.deploymentForMemcached(volRepGroup)
		   log.Info("Creating a new VolumeReplication CR",
					"Deployment.Namespace",
					newVolRep.Namespace,
					"Deployment.Name",
					newVolRep.Name)
		   err = v.Create(ctx, newVolRep)
		   if err != nil {
			   log.Error(err, "Failed to create new VolumeReplication CR",
						 "Deployment.Namespace", newVolRep.Namespace,
						 "Deployment.Name", newVolRep.Name)
			   return ctrl.Result{}, err
		   }
		   // VolumeReplication CR created successfully - return and requeue
		   return ctrl.Result{Requeue: true}, nil
	   } else if err != nil {
		   log.Error(err, "Failed to get VolumeReplication CR")
		   return ctrl.Result{}, err
	   }

	   log.Info(Labels Found: "%s\n", labelsForVolumeReplication(volRepGroup.Name))

	   // Ensure the VolumeReplication fields as the same as the VolumeReplicationGroup spec
	   size := volRepGroup.Spec.Size
	   if *volRep.Spec.Replicas != size {
		   volRep.Spec.Replicas = &size
		   err = v.Update(ctx, volRep)
		   if err != nil {
			   log.Error(err, "Failed to update Deployment",
						 "Deployment.Namespace", volRep.Namespace, "Deployment.Name", volRep.Name)
			   return ctrl.Result{}, err
		   }
		   // Spec updated - return and requeue
		   return ctrl.Result{Requeue: true}, nil
	   }
   }

   // Update the VolumeReplicationGroup status using the children VolumeReplication CRs
   // List the VolumeReplication CRs for this volRepGroup
   volRepList := &corev1.VolumeReplicationList{}
   listOpts := []client.ListOption{
	   client.InNamespace(volRepGroup.Namespace),
	   client.MatchingLabels(labelsForVolumeReplication(volRepGroup.Name)),
   }
   if err = v.List(ctx, volRepList, listOpts...); err != nil {
	   log.Error(err, "Failed to list VolumeReplication CRs",
				 "VolumeReplicationGroup.Namespace", volRepGroup.Namespace,
				 "VolumeReplicationGroup.Name", volRepGroup.Name)
	   return ctrl.Result{}, err
   }
   volRepNames := getVolRepNames(volRepList.Items)

   // Update status.Nodes if needed
   if !reflect.DeepEqual(volRepNames, volRepGroup.Status.VolumeReplications) {
	   volRepGroup.Status.VolumeReplications = volRepNames
	   err := v.Status().Update(ctx, volRepGroup)
	   if err != nil {
		   log.Error(err, "Failed to update VolumeReplicationGroup status")
		   return ctrl.Result{}, err
	   }
   }
*/

// HandlePersistentVolumeClaims handles PVCs in the VRG
func (v *VolumeReplicationGroupReconciler) HandlePersistentVolumeClaims(
	ctx context.Context,
	volRepGroup *ramendrv1alpha1.VolumeReplicationGroup) (
	*corev1.PersistentVolumeClaimList,
	error) {
	labelSelector := volRepGroup.Spec.ApplicationLabels
	pvcList := &corev1.PersistentVolumeClaimList{}

	log.Info("Label Selector: ", labels.Set(labelSelector.MatchLabels))
	log.Info("Namespace: ", volRepGroup.Namespace)
	listOptions := []client.ListOption{
		client.InNamespace(volRepGroup.Namespace),
		client.MatchingLabels(labelSelector.MatchLabels),
	}

	if err := v.List(ctx, pvcList, listOptions...); err != nil {
		log.Error(err, "Failed to list PVC")

		return pvcList, fmt.Errorf("failed to list PVC %w", err)
	}

	log.Info("PVC List: ", pvcList)

	return pvcList, nil
}

// HandlePersistentVolumes handles bound PVs
func (v *VolumeReplicationGroupReconciler) HandlePersistentVolumes(
	ctx context.Context,
	pvcList *corev1.PersistentVolumeClaimList) error {
	template := "%-42s%-42s%-8s%-10s%-8s\n"

	log.Info("---------- PVs ----------")
	log.Infof(template, "NAME", "CLAIM REF", "STATUS", "CAPACITY", "RECLAIM POLICY")

	var capacity resource.Quantity

	for _, pvc := range pvcList.Items {
		// if the PVC is not bound yet, dont proceed.
		if pvc.Status.Phase != corev1.ClaimBound {
			return fmt.Errorf("PVC is not yet bound status: %v", pvc.Status.Phase)
		}

		volumeName := pvc.Spec.VolumeName
		pv := &corev1.PersistentVolume{}
		pvObjectKey := client.ObjectKey{
			Name: pvc.Spec.VolumeName,
		}

		if err := v.Get(ctx, pvObjectKey, pv); err != nil {
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

// SetupWithManager sets up the controller with the Manager.
func (v *VolumeReplicationGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ramendrv1alpha1.VolumeReplicationGroup{}).

		// TODO: Add code to keep watching the PVCs that get
		//       created for the application that is being
		//       disaster protected.
		// Watches(&corev1.PersistentVolumeClaim{}).

		// The actual thing that the controller owns is
		// the VolumeReplication CR. Change the below
		// line when VolumeReplication CR is ready.
		Owns(&ramendrv1alpha1.VolumeReplicationGroup{}).
		Complete(v)
}
