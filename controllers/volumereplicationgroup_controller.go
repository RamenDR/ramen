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
	"fmt"
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

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	pvcList := &corev1.PersistentVolumeClaimList{}

	pvcList, err = v.HandlePersistentVolumeClaims(ctx, volRepGroup)

	if err != nil {
		log.Error("Handling of Persistent Volume Claims of application failed", volRepGroup.Spec.ApplicationName)
		return ctrl.Result{}, err
	}

	//err = v.CreateVolumeReplicationCRsFromPVC(ctx, &pvcList.Items[0])

        requeue := false

	for _, pvc := range pvcList.Items {
		volRep := &ramendrv1alpha1.VolumeReplication{}
		err = v.Get(ctx, types.NamespacedName{Name: pvc.Name, Namespace: pvc.Namespace}, volRep)
		if err != nil && errors.IsNotFound(err) {
			err = v.CreateVolumeReplicationCRsFromPVC(ctx, &pvc)
                        requeue = true

		} else if err != nil {
			log.Error(err, "Failed to get VolumeReplication CR")
			return ctrl.Result{}, err
		}
	}

        // Requeue if new CR is created   
        if requeue == true{
                return ctrl.Result{Requeue: true}, nil
        }

	return ctrl.Result{}, nil

}

func (v *VolumeReplicationGroupReconciler) HandlePersistentVolumeClaims(ctx context.Context, volRepGroup *ramendrv1alpha1.VolumeReplicationGroup) (*corev1.PersistentVolumeClaimList, error) {
	labelSelector := volRepGroup.Spec.ApplicationLabels
	log.Info("Label Selector: ", labels.Set(labelSelector.MatchLabels))
	log.Info("Namespace: ", volRepGroup.Namespace)
	listOptions := []client.ListOption{
		//                client.InNamespace("default"),
		client.InNamespace(volRepGroup.Namespace),
		client.MatchingLabels(labelSelector.MatchLabels),
	}

	log.Info("List Options", listOptions)
	pvcList := &corev1.PersistentVolumeClaimList{}
	if err := v.List(ctx, pvcList, listOptions...); err != nil {
		log.Error(err, "Failed to list PVC")
		return pvcList, err
	}
	log.Info("PVC List: ", pvcList)

	return pvcList, nil
}

func (v *VolumeReplicationGroupReconciler) HandlePersistentVolumes(ctx context.Context, pvcList *corev1.PersistentVolumeClaimList) error {

	log.Info("---------- PVs ----------")

	//log.infof allows providing formatting directives.
	//Providing formatting directives to log.info errors
	template := "%-42s%-42s%-8s%-10s%-8s\n"
	log.Infof(template, "NAME", "CLAIM REF", "STATUS", "CAPACITY", "RECLAIM POLICY")

	var cap resource.Quantity

	// https://gitlab.cncf.ci/kubernetes/kubernetes/blob/720f041985a97d0645884b5a2e0f58ab4fb9951d/staging/src/k8s.io/api/core/v1/types.go
	// As per the above website, pvList is a structure and to get the actual list of PVs
	// use pvList.Items (check the type PersistentVolumeStatus)
	for _, pvc := range pvcList.Items {

		//if the PVC is not bound yet, dont proceed.
		if pvc.Status.Phase != corev1.ClaimBound {
			// Returning error is essential for the caller of this function to
			// realize that everything is not correct and take appropriate actions.
			// Hence use fmt.Errorf instead of log.Errorf as fmt.Errorf returns
			// a non-nil info in "err" variable.
			err := fmt.Errorf("PVC is not yet bound status: %v", pvc.Status.Phase)
			return err
		}

		volumeName := pvc.Spec.VolumeName

		pv := &corev1.PersistentVolume{}

		pvObjectKey := client.ObjectKey{
			Name: pvc.Spec.VolumeName,
		}

		err := v.Get(ctx, pvObjectKey, pv)

		if err != nil {
			if errors.IsNotFound(err) {
				// Request object not found, could have been deleted after reconcile request.
				// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
				// Return and don't requeue
				log.Info("PersistentVolume resource not found. Ignoring since object must be deleted", volumeName)
				//return ctrl.Result{}, nil
				continue
			}
			// Error reading the object - requeue the request.
			log.Error(err, "Failed to get VolumeReplicationGroup")
			return err
		}

		claimRefUID := ""
		if pv.Spec.ClaimRef != nil {
			claimRefUID += pv.Spec.ClaimRef.Namespace
			claimRefUID += "/"
			claimRefUID += pv.Spec.ClaimRef.Name
		}

		reclaimPolicyStr := string(pv.Spec.PersistentVolumeReclaimPolicy)

		quant := pv.Spec.Capacity[corev1.ResourceStorage]
		cap.Add(quant)
		log.Infof(template, pv.Name, claimRefUID, string(pv.Status.Phase), quant.String(),
			reclaimPolicyStr)
	}

	log.Info("-----------------------------")
	log.Info("Total capacity Used: ", cap.String())
	log.Info("-----------------------------")

	return nil

}

func (v *VolumeReplicationGroupReconciler) CreateVolumeReplicationCRsFromPVC(ctx context.Context, pvc *corev1.PersistentVolumeClaim) error {

	cr := &ramendrv1alpha1.VolumeReplication{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvc.Name,
			Namespace: pvc.ObjectMeta.Namespace,
		},
		Spec: ramendrv1alpha1.VolumeReplicationSpec{
			DataSource: &corev1.TypedLocalObjectReference{
				Kind: "PersistentVolumeClaim",
				Name: pvc.Name,
			},
			State: "Primary",
		},
	}

	log.Info("Created CR: ", cr)

	err := v.Create(ctx, cr)

	if err != nil {
		log.Error("Error Creating CR")
	}
	return err

}

// SetupWithManager sets up the controller with the Manager.
func (v *VolumeReplicationGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ramendrv1alpha1.VolumeReplicationGroup{}).

		// TODO: Add code to keep watching the PVCs that get
		//       created for the application that is being
		//       disaster protected.
		//Watches(&corev1.PersistentVolumeClaim{}).

		// The actual thing that the controller owns is
		// the VolumeReplication CR. Change the below
		// line when VolumeReplication CR is ready.
		Owns(&ramendrv1alpha1.VolumeReplicationGroup{}).
		Complete(v)
}
