package util

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	virtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ramendr/ramen/internal/controller/core"
)

const (
	KindVirtualMachine = "VirtualMachine"
	KubeVirtAPIVersion = "kubevirt.io/v1"
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

	for _, ns := range vmNamespaceList {
		for _, vm := range vmList {
			foundVM := &virtv1.VirtualMachine{}

			vmLookUp := types.NamespacedName{Namespace: ns, Name: vm}
			if err := apiReader.Get(ctx, vmLookUp, foundVM); err != nil {
				if k8serrors.IsNotFound(err) {
					continue
				}

				return nil, err
			}

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
) ([]virtv1.VirtualMachine, bool, error) {
	foundVMs, err := ListVMsByVMNamespace(ctx, k8sclient,
		log, vmNamespaceList, vmList)
	if err != nil {
		// Skip and requeue for Get API errors
		return nil, true, err
	}

	for _, vm := range foundVMs {
		if ResourceIsDeleted(&vm) {
			log.Info("VM deletion is in progress", "VM", vm.Name)

			return foundVMs, true, nil
		}
	}

	return foundVMs, false, nil
}

// DeleteVMs deletes the given KubeVirt VMs across the provided namespaces.
// Stops on the first get/delete error and returns it;
func DeleteVMs(
	ctx context.Context,
	k8sclient client.Client,
	foundVMs []virtv1.VirtualMachine,
	log logr.Logger,
) error {
	for _, vm := range foundVMs {
		ns := vm.GetNamespace()

		vmName := vm.GetName()

		if err := k8sclient.Delete(ctx, &vm); err != nil {
			log.Error(err, "Failed to delete VM", "namespace", ns, "name", vmName)

			return fmt.Errorf("failed to delete VM %s/%s: %w", ns, vmName, err)
		}

		log.Info("Deleted VM successfully", "namespace", ns, "name", vmName)
	}

	return nil
}

// TODO: Merge this function with IsPVCInUseByPod to avoid duplication.
// Both functions perform similar checks, so refactor them into a single reusable utility.
// This requires careful handling to ensure compatibility and correctness across all call sites.
func IsUsedByVirtLauncherPod(ctx context.Context, c client.Client, obj client.Object,
	log logr.Logger,
) (client.Object, error) {
	// Patch PVC only if its not exclusively owned by any controller
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

	var vmName client.Object
	for _, pod := range podList.Items {
		vmName, err = IsOwnedByVM(ctx, c, &pod, log)
		if err != nil {
			// If not owned by this pod continue to check on the next pod(PV shared across VMs?)
			continue
		}
	}

	if vmName == nil {
		return nil, nil
	}

	log.Info("Got the VM owning the PVC", "PVC", pvcName,
		"set to owned by VM", vmName.GetName(), "VM kind", vmName.GetObjectKind())

	return vmName, nil
}

// This allows VM to declare as owner of PVC and has a dependency on the object without specifying it as a controller.
func UpdatePvcWithVMOwnerRef(
	ctx context.Context,
	c client.Client,
	ownerVM client.Object,
	pvcName, pvcNamespace string,
	log logr.Logger,
) error {
	pvcLookupKey := types.NamespacedName{Namespace: pvcNamespace, Name: pvcName}

	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.Get(ctx, pvcLookupKey, pvc); err != nil {
		log.Error(err, "Failed to get PVC", "namespace", pvcNamespace, "PVCname", pvcName)

		return err
	}

	needsUpdate, err := AddOwnerReference(pvc, ownerVM, c.Scheme())
	if err != nil {
		return fmt.Errorf("failed to add owner reference: %w", err)
	}

	if !needsUpdate {
		log.Info("PVC already has the desired owner reference; no update needed",
			"PVC name", pvc.GetName(), "Owned by VM", ownerVM.GetName())

		return nil
	}

	if err := c.Update(ctx, pvc); err != nil {
		log.Error(err, "Failed to update PVC with owner reference",
			"namespace", pvcNamespace, "PVCname", pvcName)

		return fmt.Errorf("failed to update PVC with owner reference: %w", err)
	}

	log.Info("Successfully updated PVC with owner reference to VM",
		"PVC name", pvc.GetName(), "Owned by VM", ownerVM.GetName())

	return nil
}

type item struct {
	ns    string
	owner metav1.OwnerReference
}

func getStackOfOwners(obj client.Object) []item {
	owners := obj.GetOwnerReferences()
	ownerNS := obj.GetNamespace()

	stack := make([]item, 0, len(owners))
	for _, o := range owners {
		stack = append(stack, item{ns: ownerNS, owner: o})
	}

	return stack
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
	stack := getStackOfOwners(obj)

	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		// If this owner is a KubeVirt VM, return it
		if isKubeVirtVM(cur.owner) {
			vmMeta, err := fetchPartialMeta(ctx, c, cur.ns, cur.owner)
			if err != nil {
				log.Info("Failed to fetch VM owner ", "gvk",
					gvkString(cur.owner), "name", cur.owner.Name, "err", err)

				continue
			}

			if vmMeta.GetUID() == cur.owner.UID {
				return vmMeta, nil
			}

			continue
		}

		// Try fetching only the owner's metadata
		ownerMeta, err := fetchPartialMeta(ctx, c, cur.ns, cur.owner)
		if err != nil {
			log.Info("Failed to fetch owner ", "gvk",
				gvkString(cur.owner), "name", cur.owner.Name, "err", err)

			continue
		}

		if ownerMeta.GetUID() != cur.owner.UID {
			// UID mismatch; skip
			continue
		}

		// Otherwise, enqueue its parents (same namespace assumption for KubeVirt chain)
		nestedOwners := ownerMeta.GetOwnerReferences()
		for _, nestedOwner := range nestedOwners {
			stack = append(stack, item{ns: cur.ns, owner: nestedOwner})
		}
	}

	return nil, fmt.Errorf("no VM found in ownership chain")
}

func isKubeVirtVM(o metav1.OwnerReference) bool {
	return o.Kind == KindVirtualMachine && o.APIVersion == KubeVirtAPIVersion
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

func gvkString(o metav1.OwnerReference) string {
	return o.APIVersion + "/" + o.Kind
}
