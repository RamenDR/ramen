// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ramendr/ramen/internal/controller/core"
)

const (
	CreatedByLabelKey          = "app.kubernetes.io/created-by"
	CreatedByLabelValueVolSync = "volsync"

	PodVolumePVCClaimIndexName    string = "spec.volumes.persistentVolumeClaim.claimName"
	VolumeAttachmentToPVIndexName string = "spec.source.persistentVolumeName"

	ConsistencyGroupLabel = "ramendr.openshift.io/consistency-group"

	SuffixForFinalsyncPVC = "-for-finalsync"

	annImportEndpoint = "cdi.kubevirt.io/storage.import.endpoint"
	annPopulatorKind  = "cdi.kubevirt.io/storage.populator.kind"
	expectedKind      = "VolumeImportSource"
)

// nolint:funlen
func ListPVCsByPVCSelector(
	ctx context.Context,
	k8sClient client.Client,
	logger logr.Logger,
	pvcLabelSelector metav1.LabelSelector,
	namespaces []string,
	volSyncDisabled bool,
	recipeName string,
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

		// Update the label selector to filter out PVCs created by ramen
		notCreatedByRamen, err := labels.NewRequirement(
			CreatedByRamenLabel, selection.NotIn, []string{"true"})
		if err != nil {
			logger.Error(err, "error updating PVC label selector for created by ramen label")

			return nil, fmt.Errorf("error updating PVC label selector for created by ramen label, %w", err)
		}

		updatedPVCSelector = updatedPVCSelector.Add(*notCreatedByRamen)
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

	nsSet := sets.New[string](namespaces...)

	pvcs, err = filterPVCs(ctx, k8sClient, pvcList.Items, nsSet, recipeName, logger)
	if err != nil {
		return nil, err
	}

	logger.Info(fmt.Sprintf("Returning %d PVCs in namespace(s) %v", len(pvcs), namespaces))

	pvcList.Items = pvcs

	return pvcList, nil
}

func ListPVCsByCGLabel(
	ctx context.Context,
	k8sClient client.Client,
	namespace string,
	cgLabelVal string,
	logger logr.Logger,
) (*corev1.PersistentVolumeClaimList, error) {
	logger.Info("Fetching PVCs in CG", "GC Label", cgLabelVal)

	if cgLabelVal == "" {
		logger.Info("CG label value is empty, returning empty PVC list")

		return &corev1.PersistentVolumeClaimList{}, nil
	}

	listOptions := []client.ListOption{
		client.InNamespace(namespace),
		client.MatchingLabels{
			ConsistencyGroupLabel: cgLabelVal,
		},
	}

	pvcList := &corev1.PersistentVolumeClaimList{}
	if err := k8sClient.List(ctx, pvcList, listOptions...); err != nil {
		logger.Error(err, "Failed to list PVCs using CG label", "label", cgLabelVal)

		return nil, fmt.Errorf("failed to list PVCs using CG label, %w", err)
	}

	logger.Info(fmt.Sprintf("Found %d PVCs using CG label %s", len(pvcList.Items), cgLabelVal))

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

		pod, ok := o.(*corev1.Pod)
		if !ok {
			return res
		}

		for _, vol := range pod.Spec.Volumes {
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

			va, ok := o.(*storagev1.VolumeAttachment)
			if !ok {
				return res
			}

			sourcePVName := va.Spec.Source.PersistentVolumeName
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

func GetPVC(ctx context.Context, k8sClient client.Client, pvcNamespacedName types.NamespacedName,
) (*corev1.PersistentVolumeClaim, error) {
	pvc := &corev1.PersistentVolumeClaim{}

	err := k8sClient.Get(ctx, pvcNamespacedName, pvc)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}

	return pvc, nil
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

func HashPVC(pvc *corev1.PersistentVolumeClaim) string {
	pvcCopy := pvc.DeepCopy()

	minimal := map[string]interface{}{
		"spec": pvcCopy.Spec,
	}

	canonicalJSON, err := toCanonicalJSON(minimal)
	if err != nil {
		return ""
	}

	sha256Sum := sha256.Sum256(canonicalJSON)

	return hex.EncodeToString(sha256Sum[:])
}

func toCanonicalJSON(obj map[string]interface{}) ([]byte, error) {
	jsonBytes, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}

	var generic interface{}
	if err := json.Unmarshal(jsonBytes, &generic); err != nil {
		return nil, err
	}

	sorted := sortJSON(generic)

	return json.Marshal(sorted)
}

func sortJSON(v interface{}) interface{} {
	switch v := v.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}

		sort.Strings(keys)

		sorted := make(map[string]interface{}, len(v))
		for _, k := range keys {
			sorted[k] = sortJSON(v[k])
		}

		return sorted
	case []interface{}:
		for i := range v {
			v[i] = sortJSON(v[i])
		}
	}

	return v
}

func GetTmpPVCNameForFinalSync(pvcName string) string {
	return fmt.Sprintf("%s%s", pvcName, SuffixForFinalsyncPVC)
}

// IsTemporaryImportPVC determines whether the given PVC is the CDI-created
// "prime" import PVC used in the VM recipe workflow to rebind a target PVC.
// It validates that:
//   - The PVC name follows the expected pattern (starts with "prime" and does not contain "scratch").
//   - The associated recipe name is exactly "vm-recipe".
//   - The PVC was created via CDI VolumeImportSource annotations, not DataSourceRef or DataSource.
//   - The PVC//   - The PVC includes CDI import annotations and is owned by another PVC, indicating it is part of
func IsTemporaryImportPVC(pvc *corev1.PersistentVolumeClaim, recipeName string) bool {
	// Collapse initial guards into a single boolean
	pvcName := pvc.GetName()
	ok := pvc != nil &&
		recipeName == core.VMRecipeName &&
		strings.HasPrefix(pvcName, "prime") &&
		!strings.Contains(pvcName, "scratch")

	if !ok {
		return false
	}

	if !isAnnotationBasedCDIImport(pvc) {
		return false
	}

	for _, owner := range pvc.GetOwnerReferences() {
		if owner.Kind == "PersistentVolumeClaim" {
			return true
		}
	}

	return false
}

func isAnnotationBasedCDIImport(pvc *corev1.PersistentVolumeClaim) bool {
	anns := pvc.GetAnnotations()

	return anns != nil &&
		anns[annImportEndpoint] != "" &&
		anns[annPopulatorKind] == expectedKind &&
		pvc.Spec.DataSourceRef == nil &&
		pvc.Spec.DataSource == nil
}

// If a temporary importer prime PVC related to VM is found, it attempts to update its label and skips it from the result.
func filterPVCs(
	ctx context.Context,
	k8sClient client.Client,
	items []corev1.PersistentVolumeClaim,
	nsSet sets.Set[string],
	recipeName string,
	log logr.Logger,
) ([]corev1.PersistentVolumeClaim, error) {
	out := make([]corev1.PersistentVolumeClaim, 0, len(items))

	for i := range items {
		pvc := items[i]

		if !nsSet.Has(pvc.Namespace) {
			continue
		}

		if IsTemporaryImportPVC(&pvc, recipeName) {
			err := UpdateVMRecipePvcLabel(ctx, k8sClient, &pvc, log)
			if err != nil {
				log.Error(err, "Failed to unset VM recipe PVC label selector",
					"recipe", recipeName, "labelKey", core.VMLabelSelector)

				return nil, fmt.Errorf(
					"failed to unset vm recipe pvc label selector %q on %s/%s for recipe %q: %w",
					core.VMLabelSelector, pvc.Namespace, pvc.Name, recipeName, err,
				)
			}

			log.V(1).Info("Successfully unset VM recipe label selector on temporary import PVC",
				"recipe", recipeName,
				"labelKey", core.VMLabelSelector,
			)

			continue
		}

		out = append(out, pvc)
	}

	return out, nil
}

func UpdateVMRecipePvcLabel(ctx context.Context, c client.Client,
	pvc *corev1.PersistentVolumeClaim,
	log logr.Logger,
) error {
	if pvc.Labels == nil {
		return nil
	}

	if len(pvc.Labels[core.VMLabelSelector]) > 0 {
		delete(pvc.Labels, core.VMLabelSelector)
		delete(pvc.Labels, LabelOwnerNamespaceName)
		delete(pvc.Labels, LabelOwnerName)
		delete(pvc.Labels, ConsistencyGroupLabel)
		log.Info("Unsetting vm label selector from pvc", pvc.Name,
			core.VMLabelSelector,
		)

		return c.Update(ctx, pvc)
	}

	return nil
}
