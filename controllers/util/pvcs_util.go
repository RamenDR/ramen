// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"golang.org/x/exp/slices"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	CreatedByLabelKey          = "app.kubernetes.io/created-by"
	CreatedByLabelValueVolSync = "volsync"

	PodVolumePVCClaimIndexName    string = "spec.volumes.persistentVolumeClaim.claimName"
	VolumeAttachmentToPVIndexName string = "spec.source.persistentVolumeName"
)

func ListPVCsByPVCSelector(
	ctx context.Context,
	k8sClient client.Client,
	logger logr.Logger,
	pvcLabelSelector metav1.LabelSelector,
	namespaces []string,
	volSyncDisabled bool,
) (*corev1.PersistentVolumeClaimList, error) {
	// convert metav1.LabelSelector to a labels.Selector
	pvcSelector, err := metav1.LabelSelectorAsSelector(&pvcLabelSelector)
	if err != nil {
		logger.Error(err, "error with PVC label selector", "pvcSelector", pvcLabelSelector)

		return nil, fmt.Errorf("error with PVC label selector, %w", err)
	}

	updatedPVCSelector := pvcSelector

	if !volSyncDisabled {
		// Update the label selector to filter out PVCs created by VolSync
		notCreatedByVolsyncReq, err := labels.NewRequirement(
			CreatedByLabelKey, selection.NotIn, []string{CreatedByLabelValueVolSync})
		if err != nil {
			logger.Error(err, "error updating PVC label selector")

			return nil, fmt.Errorf("error updating PVC label selector, %w", err)
		}

		updatedPVCSelector = pvcSelector.Add(*notCreatedByVolsyncReq)
	}

	logger.Info("Fetching PersistentVolumeClaims", "pvcSelector", updatedPVCSelector)

	listOptions := []client.ListOption{
		client.MatchingLabelsSelector{
			Selector: updatedPVCSelector,
		},
	}

	pvcList := &corev1.PersistentVolumeClaimList{}
	if err := k8sClient.List(ctx, pvcList, listOptions...); err != nil {
		logger.Error(err, "Failed to list PersistentVolumeClaims", "pvcSelector", updatedPVCSelector)

		return nil, fmt.Errorf("failed to list PersistentVolumeClaims, %w", err)
	}

	logger.Info(fmt.Sprintf("Found %d PVCs using label selector %v", len(pvcList.Items), updatedPVCSelector))

	var pvcs []corev1.PersistentVolumeClaim

	for _, pvc := range pvcList.Items {
		if slices.Contains(namespaces, pvc.Namespace) {
			pvcs = append(pvcs, pvc)
		}
	}

	pvcList.Items = pvcs

	return pvcList, nil
}

// IsPVCInUseByPod determines if there are any pod resources that reference the pvcName in the current
// pvcNamespace and returns true if found. Further if inUsePodMustBeReady is true, returns true only if
// the pod is in Ready state.
// TODO: Should we trust the cached list here, or fetch it from the API server?
func IsPVCInUseByPod(ctx context.Context,
	k8sClient client.Client,
	log logr.Logger,
	pvcNamespacedName types.NamespacedName,
	inUsePodMustBeReady bool,
) (bool, error) {
	log = log.WithValues("pvc", pvcNamespacedName.String())
	podUsingPVCList := &corev1.PodList{}

	err := k8sClient.List(ctx,
		podUsingPVCList, // Our custom index - needs to be setup in the cache (see IndexFieldsForVSHandler())
		client.MatchingFields{PodVolumePVCClaimIndexName: pvcNamespacedName.Name},
		client.InNamespace(pvcNamespacedName.Namespace))
	if err != nil {
		log.Error(err, "unable to lookup pods to see if they are using pvc")

		return false, fmt.Errorf("unable to lookup pods to check if pvc is in use (%w)", err)
	}

	if len(podUsingPVCList.Items) == 0 {
		return false /* Not in use by any pod */, nil
	}

	mountingPodIsReady := false

	inUsePods := []string{}
	for _, pod := range podUsingPVCList.Items {
		inUsePods = append(inUsePods, fmt.Sprintf("pod: %s, phase: %s", pod.GetName(), pod.Status.Phase))

		if pod.Status.Phase == corev1.PodRunning {
			// Assuming in use by running pod if at least 1 pod mounting the PVC is in Running phase
			// and has the Ready podCondition set to True
			mountingPodIsReady = isPodReady(pod.Status.Conditions)
		}
	}

	log.Info("pvc is in use by pod(s)", "pods", inUsePods)

	if inUsePodMustBeReady {
		return mountingPodIsReady, nil
	}

	return true, nil
}

// For CSI drivers that support it, volume attachments will be created for the PV to indicate which node
// they are attached to.  If a volume attachment exists, then we know the PV may not be ready to have a final
// replication sync performed (I/Os may still not be completely written out).
// This is a best-effort, as some CSI drivers may not support volume attachments (CSI driver Spec.AttachRequired: false)
// in this case, we won't find a volumeattachment and will just assume the PV is not in use anymore.
func IsPVAttachedToNode(ctx context.Context,
	k8sClient client.Client,
	log logr.Logger,
	pvc *corev1.PersistentVolumeClaim,
) (bool, error) {
	pvcNamespacedName := types.NamespacedName{Namespace: pvc.Namespace, Name: pvc.Name}
	pvName := pvc.Spec.VolumeName
	log = log.WithValues("pvc", pvcNamespacedName.String(), "pv", pvName)

	if pvName == "" {
		// Assuming if no volumename is set, the PVC has not been bound yet, so return false for in-use
		log.V(1).Info("pvc has no VolumeName set, assuming not in-use")

		return false, nil
	}

	// Lookup volumeattachments to determine if the PVC is mounted to a node - use our index
	// (needs to be setup in the cache - see IndexFieldsForVSHandler())
	volAttachmentList := &storagev1.VolumeAttachmentList{}

	// Volume attachments are cluster-scoped, so no need to restrict query to our namespace
	err := k8sClient.List(ctx,
		volAttachmentList,
		client.MatchingFields{VolumeAttachmentToPVIndexName: pvName})
	if err != nil {
		log.Error(err, "unable to lookup volumeattachments to see if pv for pvc is in use")
	}

	if len(volAttachmentList.Items) == 0 {
		// PV for our PVC is Not attached to any node
		return false, nil
	}

	attachedNodes := []string{}
	for _, volAttachment := range volAttachmentList.Items {
		attachedNodes = append(attachedNodes, volAttachment.Spec.NodeName)
	}

	log.Info("pvc is attached to node(s), assuming in-use", "nodes", attachedNodes)

	return true, nil
}

// VSHandler will either look at VolumeAttachments or pods to determine if a PVC is mounted
// To do this, it requires an index on pods and volumeattachments to keep track of persistent volume claims mounted
func IndexFieldsForVSHandler(ctx context.Context, fieldIndexer client.FieldIndexer) error {
	// Index on pods - used to be able to check if a pvc is mounted to a pod
	err := fieldIndexer.IndexField(ctx, &corev1.Pod{}, PodVolumePVCClaimIndexName, func(o client.Object) []string {
		var res []string
		for _, vol := range o.(*corev1.Pod).Spec.Volumes {
			if vol.PersistentVolumeClaim == nil {
				continue
			}
			// just return the raw field value -- the indexer will take care of dealing with namespaces for us
			res = append(res, vol.PersistentVolumeClaim.ClaimName)
		}

		return res
	})
	if err != nil {
		return fmt.Errorf("%w", err)
	}

	// Index on volumeattachments - used to be able to check if a pvc is mounted to a node
	// This will be the preferred check to determine if a PVC is unmounted (i.e. if no volume attachment
	// to any node, then the PV for a PVC is unmounted).  However not all storage drivers may support this.
	return fieldIndexer.IndexField(ctx, &storagev1.VolumeAttachment{}, VolumeAttachmentToPVIndexName,
		func(o client.Object) []string {
			var res []string
			sourcePVName := o.(*storagev1.VolumeAttachment).Spec.Source.PersistentVolumeName
			if sourcePVName != nil {
				res = append(res, *sourcePVName)
			}

			return res
		})
}

func isPodReady(podConditions []corev1.PodCondition) bool {
	for _, podCondition := range podConditions {
		if podCondition.Type == corev1.PodReady && podCondition.Status == corev1.ConditionTrue {
			return true
		}
	}

	return false
}

func DeletePVC(ctx context.Context,
	k8sClient client.Client,
	pvcName, namespace string,
	log logr.Logger,
) error {
	pvcToDelete := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: namespace,
		},
	}

	err := k8sClient.Delete(ctx, pvcToDelete)
	if err != nil {
		if !kerrors.IsNotFound(err) {
			log.Error(err, "error deleting pvc", "pvcName", pvcName)

			return fmt.Errorf("error deleting pvc (%w)", err)
		}
	} else {
		log.Info("deleted pvc", "pvcName", pvcName)
	}

	return nil
}
