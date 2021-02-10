/*
Copyright 2021.

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
	"github.com/go-logr/logr"
	"github.com/prometheus/common/log"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
        "k8s.io/apimachinery/pkg/labels"
        corev1 "k8s.io/api/core/v1"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
)

// VolumeReplicationGroupReconciler reconciles a VolumeReplicationGroup object
type VolumeReplicationGroupReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=volumereplicationgroups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=volumereplicationgroups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=volumereplicationgroups/finalizers,verbs=update

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
	_ = v.Log.WithValues("volumereplicationgroup", req.NamespacedName)

	// Fetch the VolumeReplicationGroup instance
	volRepGroup := &ramendrv1alpha1.VolumeReplicationGroup{}
	err := v.Get(ctx, req.NamespacedName, volRepGroup)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			log.Info("VolumeReplicationGroup resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get VolumeReplicationGroup")
		return ctrl.Result{}, err
	}
	log.Info("Processing VolumeReplicationGroup ", volRepGroup.Spec, " in ns: ", req.NamespacedName)

        labelSelector := volRepGroup.Spec.ApplicationLabels
        log.Info("Label Selector: ", labels.Set(labelSelector.MatchLabels))
        listOptions := []client.ListOption{
        //                client.InNamespace("default"),
                        client.MatchingLabels(labelSelector.MatchLabels),
        }

        log.Info("List Options", listOptions)
        pvcList := &corev1.PersistentVolumeClaimList{}
        if err = v.List(ctx, pvcList, listOptions...); err != nil {
                        log.Error(err, "Failed to list PVC")
                        return ctrl.Result{}, err
                }
        log.Info("PVC List: ", pvcList)

	/*
		// Check if the VolumeReplication CR already exists for the PVs of this application, if not create a new one
		for all PVs of this application {
			volRep := &appsv1.Deployment{}
			err = v.Get(ctx, types.NamespacedName{Name: volRepGroup.Name, Namespace: volRepGroup.Namespace}, volRep)
			if err != nil && errors.IsNotFound(err) {
				// Define a new VolumeReplication CR
				newVolRep := v.deploymentForMemcached(volRepGroup)
				log.Info("Creating a new VolumeReplication CR", "Deployment.Namespace", newVolRep.Namespace, "Deployment.Name", newVolRep.Name)
				err = v.Create(ctx, newVolRep)
				if err != nil {
					log.Error(err, "Failed to create new VolumeReplication CR", "Deployment.Namespace", newVolRep.Namespace, "Deployment.Name", newVolRep.Name)
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
					log.Error(err, "Failed to update Deployment", "Deployment.Namespace", volRep.Namespace, "Deployment.Name", volRep.Name)
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
			log.Error(err, "Failed to list VolumeReplication CRs", "VolumeReplicationGroup.Namespace", volRepGroup.Namespace, "VolumeReplicationGroup.Name", volRepGroup.Name)
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

	return ctrl.Result{}, nil

}

// SetupWithManager sets up the controller with the Manager.
func (v *VolumeReplicationGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ramendrv1alpha1.VolumeReplicationGroup{}).
		Complete(v)
}
