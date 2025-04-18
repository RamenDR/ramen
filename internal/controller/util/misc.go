// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"fmt"
	"hash/crc32"
	"reflect"

	"github.com/google/uuid"
	rmn "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/core"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	OCMBackupLabelKey   string = "cluster.open-cluster-management.io/backup"
	OCMBackupLabelValue string = "ramen"

	IsCGEnabledAnnotation = "drplacementcontrol.ramendr.openshift.io/is-cg-enabled"

	// When this annotation is set to true, VolSync will protect RBD PVCs.
	UseVolSyncAnnotation = "drplacementcontrol.ramendr.openshift.io/use-volsync-for-pvc-protection"

	JobNameMaxLength     = validation.DNS1123LabelMaxLength
	ServiceNameMaxLength = validation.DNS1123LabelMaxLength
)

type ResourceUpdater struct {
	obj         client.Object
	objModified bool
	err         error
}

func NewResourceUpdater(obj client.Object) *ResourceUpdater {
	return &ResourceUpdater{
		obj:         obj,
		objModified: false,
		err:         nil,
	}
}

func (u *ResourceUpdater) AddLabel(key, value string) *ResourceUpdater {
	added := AddLabel(u.obj, key, value)

	u.objModified = u.objModified || added

	return u
}

func (u *ResourceUpdater) AddFinalizer(finalizerName string) *ResourceUpdater {
	added := AddFinalizer(u.obj, finalizerName)

	u.objModified = u.objModified || added

	return u
}

func (u *ResourceUpdater) AddOwner(owner metav1.Object, scheme *runtime.Scheme) *ResourceUpdater {
	added, err := AddOwnerReference(u.obj, owner, scheme)
	if err != nil {
		u.err = err
	}

	u.objModified = u.objModified || added

	return u
}

func (u *ResourceUpdater) RemoveFinalizer(finalizerName string) *ResourceUpdater {
	finalizersUpdated := controllerutil.RemoveFinalizer(u.obj, finalizerName)

	u.objModified = u.objModified || finalizersUpdated

	return u
}

func (u *ResourceUpdater) Update(ctx context.Context, client client.Client) error {
	if u.err != nil {
		return u.err
	}

	if u.objModified {
		return client.Update(ctx, u.obj)
	}

	return nil
}

func AddLabel(obj client.Object, key, value string) bool {
	const labelAdded = true

	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}

	if v, ok := labels[key]; !ok || v != value {
		labels[key] = value
		obj.SetLabels(labels)

		return labelAdded
	}

	return !labelAdded
}

func UpdateLabel(obj client.Object, key, newValue string) bool {
	const labelUpdated = true

	labels := obj.GetLabels()
	if labels == nil {
		return !labelUpdated
	}

	if currValue, ok := labels[key]; ok {
		if currValue != newValue {
			labels[key] = newValue
			obj.SetLabels(labels)

			return labelUpdated
		}
	}

	return !labelUpdated
}

func HasLabel(obj client.Object, key string) bool {
	labels := obj.GetLabels()
	for k := range labels {
		if k == key {
			return true
		}
	}

	return false
}

func HasLabelWithValue(obj client.Object, key string, value string) bool {
	labels := obj.GetLabels()
	for k, v := range labels {
		if k == key && v == value {
			return true
		}
	}

	return false
}

func AddAnnotation(obj client.Object, key, value string) bool {
	const added = true

	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}

	if keyValue, ok := annotations[key]; !ok || keyValue != value {
		annotations[key] = value
		obj.SetAnnotations(annotations)

		return added
	}

	return !added
}

func AddOwnerReference(obj, owner metav1.Object, scheme *runtime.Scheme) (bool, error) {
	currentOwnerRefs := obj.GetOwnerReferences()

	err := controllerutil.SetOwnerReference(owner, obj, scheme)
	if err != nil {
		return false, err
	}

	ownerAdded := !reflect.DeepEqual(obj.GetOwnerReferences(), currentOwnerRefs)

	return ownerAdded, nil
}

func AddFinalizer(obj client.Object, finalizer string) bool {
	const finalizerAdded = true

	if !controllerutil.ContainsFinalizer(obj, finalizer) {
		controllerutil.AddFinalizer(obj, finalizer)

		return finalizerAdded
	}

	return !finalizerAdded
}

// UpdateStringMap copies all key/value pairs in src adding them to map
// referenced by the dst pointer. When a key in src is already present in dst,
// the value in dst will be overwritten by the value associated with the key in
// src.  The dst map is created if needed.
func UpdateStringMap(dst *map[string]string, src map[string]string) {
	if *dst == nil && len(src) > 0 {
		*dst = make(map[string]string, len(src))
	}

	for key, val := range src {
		(*dst)[key] = val
	}
}

// OptionalEqual returns True if optional field values are equal, or one of them is unset.
func OptionalEqual(a, b string) bool {
	return a == "" || b == "" || a == b
}

func CreateRamenOpsNamespace(ctx context.Context, k8sClient client.Client, ramenconfig *rmn.RamenConfig) error {
	if ramenconfig.RamenOpsNamespace == "" {
		return nil
	}

	return CreateNamespaceIfNotExists(ctx, k8sClient, ramenconfig.RamenOpsNamespace)
}

func CreateNamespaceIfNotExists(ctx context.Context, k8sClient client.Client, namespace string) error {
	ns := &corev1.Namespace{}

	err := k8sClient.Get(ctx, types.NamespacedName{Name: namespace}, ns)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			ns.Name = namespace
			core.ObjectCreatedByRamenSetLabel(ns)

			err = k8sClient.Create(ctx, ns)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}

	return nil
}

func IsCGEnabled(annotations map[string]string) bool {
	return annotations[IsCGEnabledAnnotation] == "true"
}

func IsPVCMarkedForVolSync(annotations map[string]string) bool {
	return annotations[UseVolSyncAnnotation] == "true"
}

func TrimToK8sResourceNameLength(name string) string {
	const maxLength = 63
	if len(name) > maxLength {
		return name[:maxLength]
	}

	return name
}

func GetJobName(namePrefix string, ownerName string) string {
	return getShortenedResourceName(namePrefix, ownerName, JobNameMaxLength)
}

func GetServiceName(namePrefix string, ownerName string) string {
	return getShortenedResourceName(namePrefix, ownerName, ServiceNameMaxLength)
}

func getShortenedResourceName(namePrefix string, ownerName string, maxLength int) string {
	name := namePrefix + ownerName

	if len(name) > maxLength {
		return namePrefix + GetHashedName(ownerName)
	}

	// No need to shorten, use original name
	return name
}

// Implements the string shortening algorithm, required to match volsync resources names.
// https://github.com/backube/volsync/pull/1519
func GetHashedName(name string) string {
	return fmt.Sprintf("%08x", crc32.ChecksumIEEE([]byte(name)))
}

func GetRID() string {
	return GetHashedName(uuid.New().String())
}
