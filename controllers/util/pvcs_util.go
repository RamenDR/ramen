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

package util

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	CreatedByLabelKey          = "app.kubernetes.io/created-by"
	CreatedByLabelValueVolSync = "volsync"
)

func ListPVCsByPVCSelector(ctx context.Context, k8sClient client.Client, pvcLabelSelector metav1.LabelSelector,
	namespace string, volSyncDisabled bool, logger logr.Logger) (*corev1.PersistentVolumeClaimList, error) {
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
		client.InNamespace(namespace),
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

	return pvcList, nil
}
