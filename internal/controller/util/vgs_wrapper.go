// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"fmt"

	publicgroupsnapv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumegroupsnapshot/v1"
	groupsnapv1beta1 "github.com/red-hat-storage/external-snapshotter/client/v8/apis/volumegroupsnapshot/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// VolumeGroupSnapshotClassWrapper provides a unified view of both public and private VGS class APIs.
type VolumeGroupSnapshotClassWrapper interface {
	GetName() string
	GetDriver() string
	GetLabels() map[string]string
	GetAnnotations() map[string]string
	GetDeletionPolicy() string
}

type privateVGSCWrapper struct {
	vgsc *groupsnapv1beta1.VolumeGroupSnapshotClass
}

type publicVGSCWrapper struct {
	vgsc *publicgroupsnapv1.VolumeGroupSnapshotClass
}

func (w *privateVGSCWrapper) GetName() string              { return w.vgsc.Name }
func (w *privateVGSCWrapper) GetDriver() string            { return w.vgsc.Driver }
func (w *privateVGSCWrapper) GetLabels() map[string]string { return w.vgsc.Labels }
func (w *privateVGSCWrapper) GetAnnotations() map[string]string {
	return w.vgsc.Annotations
}
func (w *privateVGSCWrapper) GetDeletionPolicy() string { return string(w.vgsc.DeletionPolicy) }

func (w *publicVGSCWrapper) GetName() string              { return w.vgsc.Name }
func (w *publicVGSCWrapper) GetDriver() string            { return w.vgsc.Driver }
func (w *publicVGSCWrapper) GetLabels() map[string]string { return w.vgsc.Labels }
func (w *publicVGSCWrapper) GetAnnotations() map[string]string {
	return w.vgsc.Annotations
}
func (w *publicVGSCWrapper) GetDeletionPolicy() string { return string(w.vgsc.DeletionPolicy) }

var (
	localVGSAPI               schema.GroupVersion
	forcePrivateVGSAPIForTest bool
)

// ForcePrivateVGSAPIForTesting pins local VGS API selection to the private API.
// Use in envtest suites that ship both public and private VGS CRDs under hack/test
// but still exercise the private code path / existing private-typed tests.
// The pin is sticky: EnsureLocalVGSAPI will not override it via CRD detection.
func ForcePrivateVGSAPIForTesting() {
	forcePrivateVGSAPIForTest = true
	localVGSAPI = groupsnapv1beta1.SchemeGroupVersion
}

// EnsureLocalVGSAPI detects the local VGS API and stores it for UsePublicVGSAPI /
// UsePrivateVGSAPI. Call from SetupWithManager or reconcile before using VGS helpers.
// When ForcePrivateVGSAPIForTesting has been called, keeps the private pin.
func EnsureLocalVGSAPI(ctx context.Context, apiReader client.Reader) error {
	if forcePrivateVGSAPIForTest {
		localVGSAPI = groupsnapv1beta1.SchemeGroupVersion

		return nil
	}

	gv, err := SelectVGSGroupVersion(ctx, apiReader)
	if err != nil {
		return err
	}

	localVGSAPI = gv

	return nil
}

// UsePublicVGSAPI reports whether Ramen should use the public VolumeGroupSnapshot API.
// Requires a prior successful EnsureLocalVGSAPI (or ForcePrivateVGSAPIForTesting).
func UsePublicVGSAPI() bool {
	return localVGSAPI == publicgroupsnapv1.SchemeGroupVersion
}

// UsePrivateVGSAPI reports whether Ramen should use the private VolumeGroupSnapshot API.
// Requires a prior successful EnsureLocalVGSAPI (or ForcePrivateVGSAPIForTesting).
// Public API takes precedence when both CRDs are installed.
func UsePrivateVGSAPI() bool {
	return localVGSAPI == groupsnapv1beta1.SchemeGroupVersion
}

// SelectVGSGroupVersion picks the VGS API GroupVersion using the same precedence as
// UsePublicVGSAPI / UsePrivateVGSAPI: public if the public CRD is installed, otherwise
// private if installed.
func SelectVGSGroupVersion(ctx context.Context, apiReader client.Reader) (schema.GroupVersion, error) {
	return resolveVGSGroupVersion(func(crdName string) (bool, error) {
		return IsCRDInstalled(ctx, apiReader, crdName), nil
	})
}

// resolveVGSGroupVersion applies the public-first precedence rule given a function
// that reports whether a named CRD exists. Shared by local and managed-cluster callers.
func resolveVGSGroupVersion(crdExists func(string) (bool, error)) (schema.GroupVersion, error) {
	if exists, err := crdExists(VGSCRDName); err != nil {
		return schema.GroupVersion{}, fmt.Errorf("checking public VGS CRD %q: %w", VGSCRDName, err)
	} else if exists {
		return publicgroupsnapv1.SchemeGroupVersion, nil
	}

	if exists, err := crdExists(VGSCRDPrivateName); err != nil {
		return schema.GroupVersion{}, fmt.Errorf("checking private VGS CRD %q: %w", VGSCRDPrivateName, err)
	} else if exists {
		return groupsnapv1beta1.SchemeGroupVersion, nil
	}

	return schema.GroupVersion{}, fmt.Errorf("VolumeGroupSnapshot CRD is required. " +
		"Please install either the public (groupsnapshot.storage.k8s.io) or private (groupsnapshot.storage.openshift.io) " +
		"VolumeGroupSnapshot CRD and restart the operator")
}

// NewVolumeGroupSnapshot returns an empty VolumeGroupSnapshot of the appropriate API type.
// Requires a prior successful EnsureLocalVGSAPI (or ForcePrivateVGSAPIForTesting).
func NewVolumeGroupSnapshot(name, namespace string) client.Object {
	if UsePublicVGSAPI() {
		return &publicgroupsnapv1.VolumeGroupSnapshot{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
		}
	}

	return &groupsnapv1beta1.VolumeGroupSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

// GetVolumeGroupSnapshot retrieves a VolumeGroupSnapshot using the appropriate API.
// Requires a prior successful EnsureLocalVGSAPI (or ForcePrivateVGSAPIForTesting).
func GetVolumeGroupSnapshot(
	ctx context.Context,
	k8sClient client.Client,
	name, namespace string,
) (client.Object, error) {
	vgs := NewVolumeGroupSnapshot(name, namespace)
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, vgs); err != nil {
		return nil, err
	}

	return vgs, nil
}

// ListVolumeGroupSnapshots lists VolumeGroupSnapshots using the appropriate API.
// Requires a prior successful EnsureLocalVGSAPI (or ForcePrivateVGSAPIForTesting).
func ListVolumeGroupSnapshots(
	ctx context.Context,
	k8sClient client.Client,
	listOptions ...client.ListOption,
) ([]client.Object, error) {
	if UsePublicVGSAPI() {
		list := &publicgroupsnapv1.VolumeGroupSnapshotList{}
		if err := k8sClient.List(ctx, list, listOptions...); err != nil {
			return nil, err
		}

		return vgsListToObjects(list.Items), nil
	}

	list := &groupsnapv1beta1.VolumeGroupSnapshotList{}
	if err := k8sClient.List(ctx, list, listOptions...); err != nil {
		return nil, err
	}

	return vgsListToObjects(list.Items), nil
}

func vgsListToObjects[T any, PT interface {
	*T
	client.Object
}](items []T) []client.Object {
	objects := make([]client.Object, 0, len(items))
	for i := range items {
		objects = append(objects, PT(&items[i]))
	}

	return objects
}

// HasStatus reports whether the VolumeGroupSnapshot status has been
// populated by the snapshotter (i.e. the Status pointer is non-nil).
func HasStatus(vgs client.Object) bool {
	switch obj := vgs.(type) {
	case *publicgroupsnapv1.VolumeGroupSnapshot:
		return obj != nil && obj.Status != nil
	case *groupsnapv1beta1.VolumeGroupSnapshot:
		return obj != nil && obj.Status != nil
	default:
		return false
	}
}

// VolumeGroupSnapshotIsReady reports whether the VolumeGroupSnapshot is ready to use.
func VolumeGroupSnapshotIsReady(vgs client.Object) bool {
	switch obj := vgs.(type) {
	case *publicgroupsnapv1.VolumeGroupSnapshot:
		return obj != nil && obj.Status != nil && obj.Status.ReadyToUse != nil && *obj.Status.ReadyToUse
	case *groupsnapv1beta1.VolumeGroupSnapshot:
		return obj != nil && obj.Status != nil && obj.Status.ReadyToUse != nil && *obj.Status.ReadyToUse
	default:
		return false
	}
}

// SetVolumeGroupSnapshotClassName sets the VolumeGroupSnapshotClassName on a VolumeGroupSnapshot.
func SetVolumeGroupSnapshotClassName(vgs client.Object, className *string) {
	switch obj := vgs.(type) {
	case *publicgroupsnapv1.VolumeGroupSnapshot:
		obj.Spec.VolumeGroupSnapshotClassName = className
	case *groupsnapv1beta1.VolumeGroupSnapshot:
		obj.Spec.VolumeGroupSnapshotClassName = className
	}
}

// SetVolumeGroupSnapshotSourceSelector sets the source selector on a VolumeGroupSnapshot.
func SetVolumeGroupSnapshotSourceSelector(vgs client.Object, selector *metav1.LabelSelector) {
	switch obj := vgs.(type) {
	case *publicgroupsnapv1.VolumeGroupSnapshot:
		obj.Spec.Source.Selector = selector
	case *groupsnapv1beta1.VolumeGroupSnapshot:
		obj.Spec.Source.Selector = selector
	}
}

// NewVolumeGroupSnapshotClassForGV returns an empty VGSC object and a wrapper over the same
// instance. Callers that populate the object (e.g. via ManagedClusterView) can then read
// fields through the wrapper without a second wrap step.
func NewVolumeGroupSnapshotClassForGV(gv schema.GroupVersion) (interface{}, VolumeGroupSnapshotClassWrapper, error) {
	switch gv {
	case publicgroupsnapv1.SchemeGroupVersion:
		obj := &publicgroupsnapv1.VolumeGroupSnapshotClass{}

		return obj, &publicVGSCWrapper{vgsc: obj}, nil
	case groupsnapv1beta1.SchemeGroupVersion:
		obj := &groupsnapv1beta1.VolumeGroupSnapshotClass{}

		return obj, &privateVGSCWrapper{vgsc: obj}, nil
	default:
		return nil, nil, fmt.Errorf("unsupported VGS GroupVersion %s", gv)
	}
}

// NewPrivateVGSCWrapper creates a wrapper for a private VolumeGroupSnapshotClass (tests).
func NewPrivateVGSCWrapper(vgsc *groupsnapv1beta1.VolumeGroupSnapshotClass) VolumeGroupSnapshotClassWrapper {
	return &privateVGSCWrapper{vgsc: vgsc}
}

// NewPrivateVGSCWrappers wraps private VolumeGroupSnapshotClasses for tests.
func NewPrivateVGSCWrappers(
	vgscs ...*groupsnapv1beta1.VolumeGroupSnapshotClass,
) []VolumeGroupSnapshotClassWrapper {
	wrappers := make([]VolumeGroupSnapshotClassWrapper, len(vgscs))
	for i := range vgscs {
		wrappers[i] = NewPrivateVGSCWrapper(vgscs[i])
	}

	return wrappers
}
