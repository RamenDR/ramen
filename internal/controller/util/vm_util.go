package util

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	virtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

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
) []virtv1.VirtualMachine {
	foundVM := &virtv1.VirtualMachine{}

	foundVMList := make([]virtv1.VirtualMachine, 0, len(vmList))
	for _, ns := range vmNamespaceList {
		for _, vm := range vmList {
			vmLookUp := types.NamespacedName{Namespace: ns, Name: vm}
			if err := apiReader.Get(ctx, vmLookUp, foundVM); err != nil {
				continue
			}

			foundVMList = append(foundVMList, *foundVM)
		}
	}

	if len(foundVMList) > 0 {
		return foundVMList
	}

	return nil
}

// IsVMDeletionInProgress returns true if any listed KubeVirt VM within the given protected NS is in deletion state.
// Skips VMs that cannot be fetched (likely already deleted); checks all (namespace, name) pairs.
func IsVMDeletionInProgress(ctx context.Context,
	k8sclient client.Client,
	vmList []string,
	vmNamespaceList []string,
	log logr.Logger,
) bool {
	log.Info("Checking if VirtualMachines are being deleted",
		"vmCount", len(vmList),
		"vmNames", vmList)

	foundVM := &virtv1.VirtualMachine{}

	for _, ns := range vmNamespaceList {
		for _, vm := range vmList {
			vmLookUp := types.NamespacedName{Namespace: ns, Name: vm}
			if err := k8sclient.Get(ctx, vmLookUp, foundVM); err != nil {
				// Continuing with remaining list of VMs as the current one might already have been deleted
				continue
			}

			if !foundVM.GetDeletionTimestamp().IsZero() {
				// Deletion of vm has been requested
				log.Info("VM deletion is in progress", "VM", vm)

				return true
			}
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
