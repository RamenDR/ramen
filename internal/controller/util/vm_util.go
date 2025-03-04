package util

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/ramendr/ramen/internal/controller/core"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	virtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
