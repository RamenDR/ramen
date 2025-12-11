package util

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	virtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/ramendr/ramen/internal/controller/core"
)

const (
	KindVirtualMachine       = "VirtualMachine"
	KubeVirtAPIVersionPrefix = "kubevirt.io/" // prefix match on group; version follows (e.g., v1)
)

func ListVMsByLabelSelector(
	ctx context.Context,
	apiReader client.Reader,
	logger logr.Logger,
	vmLabelSelector []string,
	namespaces []string,
) ([]string, error) {
	foundVMs := []string{}

	for _, ns := range namespaces {
		for _, ls := range vmLabelSelector {
			matchLabels := map[string]string{
				core.VMLabelSelector: ls,
			}

			listOptions := []client.ListOption{
				client.InNamespace(ns),
				client.MatchingLabels(matchLabels),
			}

			vmList := &virtv1.VirtualMachineList{}
			if err := apiReader.List(context.TODO(), vmList, listOptions...); err != nil {
				return nil, err
			}

			for _, v := range vmList.Items {
				foundVMs = append(foundVMs, v.Name)
			}

			logger.Info(
				fmt.Sprintf("VMs with labelSelector[%#v: %s] found in NS[%s] are %#v", listOptions, ls,
					ns, foundVMs))
		}
	}

	return foundVMs, nil
}

func ListVMsByVMNamespace(
	ctx context.Context,
	apiReader client.Reader,
	log logr.Logger,
	vmNamespaceList []string,
	vmList []string,
) ([]virtv1.VirtualMachine, error) {
	var foundVMs []virtv1.VirtualMachine
	// Set to track seen (ns, name) pairs
	seen := make(map[string]struct{}, len(vmNamespaceList)*len(vmList))

	for _, ns := range vmNamespaceList {
		for _, vm := range vmList {
			foundVM := &virtv1.VirtualMachine{}
			key := ns + "/" + vm
			// Skip if we've already successfully added this (ns, name) pair
			if _, already := seen[key]; already {
				// Skip duplicates of the same (namespace, name)
				continue
			}

			vmLookUp := types.NamespacedName{Namespace: ns, Name: vm}
			if err := apiReader.Get(ctx, vmLookUp, foundVM); err != nil {
				if k8serrors.IsNotFound(err) {
					continue
				}

				return nil, err
			}

			// Mark as seen only when it actually exists, to verify duplicate entries in the input spec
			seen[key] = struct{}{}

			foundVMs = append(foundVMs, *foundVM.DeepCopy())
		}
	}

	return foundVMs, nil
}

// IsVMDeletionInProgress returns true if any listed KubeVirt VM within the given protected NS is in deletion state.
// Skips VMs that cannot be fetched (likely already deleted); checks all (namespace, name) pairs.
func IsVMDeletionInProgress(ctx context.Context,
	k8sclient client.Client,
	vmList []string,
	vmNamespaceList []string,
	log logr.Logger,
) bool {
	foundVMs, err := ListVMsByVMNamespace(ctx, k8sclient,
		log, vmNamespaceList, vmList)
	if err != nil {
		// Skip and requeue for Get API errors
		return true
	}

	for _, vm := range foundVMs {
		if !vm.GetDeletionTimestamp().IsZero() {
			log.Info("VM deletion is in progress", "VM", vm)

			return true
		}
	}

	return false
}

// DeleteVMs deletes the given KubeVirt VMs across the provided namespaces.
// Stops on the first get/delete error and returns it;
func DeleteVMs(
	ctx context.Context,
	k8sclient client.Client,
	foundVMs []virtv1.VirtualMachine,
	vmList []string,
	vmNamespaceList []string,
	log logr.Logger,
) error {
	for _, vm := range foundVMs {
		ns := vm.GetNamespace()

		vmName := vm.GetName()

		// Foreground deletion option
		deleteOpts := &client.DeleteOptions{
			GracePeriodSeconds: nil,
			PropagationPolicy: func() *metav1.DeletionPropagation {
				p := metav1.DeletePropagationForeground

				return &p
			}(),
		}
		if err := k8sclient.Delete(ctx, &vm, deleteOpts); err != nil {
			log.Error(err, "Failed to delete VM", "namespace", ns, "name", vmName)

			return fmt.Errorf("failed to delete VM %s/%s: %w", ns, vmName, err)
		}

		log.Info("Deleted VMs successfully", "from namespace", ns, "VM name", vmName)
	}

	return nil
}

func IsUsedByVirtLauncherPod(ctx context.Context, c client.Client, obj client.Object,
	log logr.Logger,
) (client.Object, error) {
	// skip checking if virt-launcher pod is using the PVC when ownerrefences is >0,
	// as PVC may be managed by cdi.kubevirt.io controller or any user application controller.
	if len(obj.GetOwnerReferences()) > 0 {
		return nil, nil
	}
	// Patch PVC only if its not exclusively owned by any controller
	// Get the Pod
	podList := &corev1.PodList{}
	pvcName := obj.GetName()
	pvcNamespace := obj.GetNamespace()

	err := c.List(ctx, podList, client.MatchingFields{PodVolumePVCClaimIndexName: pvcName},
		client.InNamespace(pvcNamespace))
	if err != nil {
		log.Error(err, "error getting pods list from protected namespace", "namespace", pvcNamespace)

		return nil, err
	}

	if len(podList.Items) == 0 {
		log.Info("Not is use by any pod")

		return nil, nil
	}

	for _, pod := range podList.Items {
		for _, vol := range pod.Spec.Volumes {
			if vol.PersistentVolumeClaim != nil && vol.PersistentVolumeClaim.ClaimName == pvcName {
				vmName, err := IsOwnedByVM(ctx, c, &pod, log)
				if err != nil {
					log.Error(err, "Skipping cleanup",
						"pod", pod.Name,
						"vm", vmName,
						"reason", "invalid ownerReferences",
						"action", "manual cleanup required")

					return nil, nil
				}

				log.Info("Got the VM owning the PVC", "PVC", pvcName,
					"set to owned by VM", vmName.GetName(), "VM kind", vmName.GetObjectKind())

				return vmName, nil
			}
		}
	}

	return nil, nil
}

// This allows VM to declare as owner of PVC and has a dependency on the object without specifying it as a controller.
func PatchPvcWithVMOwnerRef(ctx context.Context, c client.Client, ownerVM client.Object,
	pvcName, pvcNamespace string, log logr.Logger,
) error {
	pvcLookupKey := types.NamespacedName{Namespace: pvcNamespace, Name: pvcName}

	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.Get(ctx, pvcLookupKey, pvc); err != nil {
		log.Error(err, "Failed to get PVC", "namespace", pvcNamespace, "PVCname", pvcName)

		return err
	}

	// 1. Capture the original state of the PVC before modification
	// It's crucial to create a deep copy to represent the *unmodified* base for the patch calculation
	pvcOriginal := pvc.DeepCopy()

	// 2. Add the OwnerReference to the local in-memory PVC object
	// This mutates the 'pvc' object in place
	err := controllerutil.SetOwnerReference(ownerVM, pvc, c.Scheme())
	if err != nil {
		return fmt.Errorf("failed to add owner reference: %w", err)
	}

	// 3. Use the client.Patch helper to calculate the difference (the patch)
	// between 'pvcOriginal' and the now-modified 'pvc' object.
	// We specifically use client.MergeFrom to generate a MergePatch payload.
	patch := client.MergeFrom(pvcOriginal)

	// 4. Execute the Patch API call
	// This sends only the 'metadata.ownerReferences' change to the Kubernetes API server
	if err := c.Patch(ctx, pvc, patch); err != nil {
		return fmt.Errorf("failed to patch PVC with owner reference: %w", err)
	}

	log.Info("Successfully patched PVC with owner reference to VM",
		"PVC name", pvc.GetName(), "Owned by VM", ownerVM.GetName())

	return nil
}

// IsOwnedByVM walks the owner chain and returns the VM metadata object if found.
// It prefers KubeVirt VM owners (kind=VirtualMachine, apigroup starts with kubevirt.io/)
// Assuming all the owners are from same namespace
// Typical KubeVirt ownership depth (PVC→DV→VM or PVC→VMI→VM or virt-launcher-pod->VMI->VM)
func IsOwnedByVM(
	ctx context.Context,
	c client.Client,
	obj client.Object,
	log logr.Logger,
) (client.Object, error) {
	owners := obj.GetOwnerReferences()
	// Breadth-first/flat traversal to reduce cognitive complexity
	type queued struct {
		ns    string
		owner metav1.OwnerReference
	}

	q := make([]queued, 0, len(owners))
	for _, o := range owners {
		q = append(q, queued{ns: obj.GetNamespace(), owner: o})
	}

	for len(q) > 0 {
		cur := q[0]
		q = q[1:]

		// Try fetching only the owner's metadata
		ownerMeta, err := fetchPartialMeta(ctx, c, cur.ns, cur.owner)
		if err != nil {
			log.Info("Failed to fetch owner", "gvk", cur.owner.APIVersion+"/"+cur.owner.Kind, "name", cur.owner.Name, "err", err)

			continue
		}

		if ownerMeta.GetUID() != cur.owner.UID {
			// UID mismatch; skip
			continue
		}

		// If this owner is a KubeVirt VM, return it
		if isKubeVirtVM(cur.owner) {
			return ownerMeta, nil
		}

		// Otherwise, enqueue its parents (same namespace assumption for KubeVirt chain)
		nestedOwners := ownerMeta.GetOwnerReferences()
		for _, nestedOwner := range nestedOwners {
			q = append(q, queued{ns: cur.ns, owner: nestedOwner})
		}
	}

	return nil, fmt.Errorf("no VM owner found")
}

func isKubeVirtVM(o metav1.OwnerReference) bool {
	return o.Kind == KindVirtualMachine && strings.HasPrefix(o.APIVersion, KubeVirtAPIVersionPrefix)
}

// Fetch only metadata of the owner
func fetchPartialMeta(
	ctx context.Context,
	c client.Client,
	ns string,
	o metav1.OwnerReference,
) (*metav1.PartialObjectMetadata, error) {
	objMeta := &metav1.PartialObjectMetadata{}
	objMeta.SetGroupVersionKind(schema.FromAPIVersionAndKind(o.APIVersion, o.Kind))

	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: o.Name}, objMeta); err != nil {
		return nil, err
	}

	return objMeta, nil
}
