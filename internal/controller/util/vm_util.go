package util

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
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
) ([]string, error) {
	var foundVMList []string

	var notFoundErr error

	foundVM := &virtv1.VirtualMachine{}

	for _, ns := range vmNamespaceList {
		for _, vm := range vmList {
			vmLookUp := types.NamespacedName{Namespace: ns, Name: vm}
			if err := apiReader.Get(ctx, vmLookUp, foundVM); err != nil {
				if !k8serrors.IsNotFound(err) {
					return nil, err
				}

				if notFoundErr == nil {
					notFoundErr = err
				}

				continue
			}

			foundVMList = append(foundVMList, foundVM.Name)
		}
	}

	if len(foundVMList) > 0 {
		return foundVMList, nil
	}

	return nil, notFoundErr
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
	vmList []string,
	vmNamespaceList []string,
	log logr.Logger,
) error {
	for _, ns := range vmNamespaceList {
		for _, vmName := range vmList {
			vm := &virtv1.VirtualMachine{}
			key := client.ObjectKey{Name: vmName, Namespace: ns}

			if err := k8sclient.Get(ctx, key, vm); err != nil {
				log.Error(err, "Failed to get VM", "namespace", ns, "name", vmName)

				return fmt.Errorf("failed to get VM %s/%s: %w", ns, vmName, err)
			}

			if err := k8sclient.Delete(ctx, vm); err != nil {
				log.Error(err, "Failed to delete VM", "namespace", ns, "name", vmName)

				return fmt.Errorf("failed to delete VM %s/%s: %w", ns, vmName, err)
			}

			log.Info("Deleted VM successfully", "namespace", ns, "name", vmName)
		}
	}

	return nil
}

// IsOwnedByVM recursively traverses ownerReferences until it finds a VirtualMachine.
func IsOwnedByVM(ctx context.Context, c client.Client, obj client.Object,
	owners []metav1.OwnerReference, log logr.Logger) (string, error) {

	for _, owner := range owners {
		if owner.Kind == KindVirtualMachine && owner.APIVersion == KubeVirtAPIVersion {
			return owner.Name, nil // Found VM root
		}

		// Fetch only metadata of the owner
		ownerMeta := &metav1.PartialObjectMetadata{}
		ownerMeta.SetGroupVersionKind(schema.FromAPIVersionAndKind(owner.APIVersion, owner.Kind))

		if err := c.Get(ctx, client.ObjectKey{Namespace: obj.GetNamespace(), Name: owner.Name}, ownerMeta); err != nil {
			log.Info("Failed to fetch owner", "error", err)
			continue // skip if not found
		}

		// Continue traversal with the owner
		// Recursively check its ownerReferences
		obj = ownerMeta
		nestedOwners := obj.GetOwnerReferences()
		if len(nestedOwners) > 0 {
			vmName, err := IsOwnedByVM(ctx, c, obj, nestedOwners, log)
			if err == nil {
				return vmName, nil
			}
		}
	}
	return "", fmt.Errorf("no VM owner found")
}
