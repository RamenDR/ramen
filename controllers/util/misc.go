// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	OCMBackupLabelKey   string = "cluster.open-cluster-management.io/backup"
	OCMBackupLabelValue string = "resource"
)

func GenericAddLabelsAndFinalizers(
	ctx context.Context,
	object client.Object,
	finalizerName string,
	client client.Client,
	log logr.Logger,
) error {
	labelAdded := AddLabel(object, OCMBackupLabelKey, OCMBackupLabelValue)
	finalizerAdded := AddFinalizer(object, finalizerName)

	if finalizerAdded || labelAdded {
		log.Info("finalizer or label add")

		return client.Update(ctx, object)
	}

	return nil
}

func GenericFinalizerRemove(
	ctx context.Context,
	object client.Object,
	finalizerName string,
	client client.Client,
	log logr.Logger,
) error {
	finalizerCount := len(object.GetFinalizers())
	controllerutil.RemoveFinalizer(object, finalizerName)

	if len(object.GetFinalizers()) != finalizerCount {
		log.Info("finalizer remove")

		return client.Update(ctx, object)
	}

	return nil
}

func AddLabel(obj client.Object, key, value string) bool {
	const labelAdded = true

	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}

	if _, ok := labels[key]; !ok {
		labels[key] = value
		obj.SetLabels(labels)

		return labelAdded
	}

	return !labelAdded
}

func AddFinalizer(obj client.Object, finalizer string) bool {
	const finalizerAdded = true

	if !controllerutil.ContainsFinalizer(obj, finalizer) {
		controllerutil.AddFinalizer(obj, finalizer)

		return finalizerAdded
	}

	return !finalizerAdded
}
