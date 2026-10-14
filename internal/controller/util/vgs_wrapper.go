// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"fmt"
	"strings"

	publicgroupsnapv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumegroupsnapshot/v1"
	groupsnapv1beta1 "github.com/red-hat-storage/external-snapshotter/client/v8/apis/volumegroupsnapshot/v1beta1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
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
// UsePublicVGSAPI / UsePrivateVGSAPI: public if its CRD is installed and serves the
// public client's version, otherwise private under the same rule.
func SelectVGSGroupVersion(ctx context.Context, apiReader client.Reader) (schema.GroupVersion, error) {
	return resolveVGSGroupVersion(func(crdName string) (*apiextensionsv1.CustomResourceDefinition, error) {
		crd := &apiextensionsv1.CustomResourceDefinition{}
		if err := apiReader.Get(ctx, types.NamespacedName{Name: crdName}, crd); err != nil {
			if k8serrors.IsNotFound(err) {
				return nil, nil
			}

			return nil, err
		}

		return crd, nil
	})
}

// crdServesVersion reports whether the CRD serves the given API version.
// Selecting an API version the CRD does not serve is fatal at a distance:
// every informer on that GroupVersion fails cache sync and takes the manager
// down with it, so mere CRD existence is not enough to pick an API.
func crdServesVersion(crd *apiextensionsv1.CustomResourceDefinition, version string) bool {
	for _, v := range crd.Spec.Versions {
		if v.Name == version && v.Served {
			return true
		}
	}

	return false
}

func crdServedVersions(crd *apiextensionsv1.CustomResourceDefinition) []string {
	served := []string{}

	for _, v := range crd.Spec.Versions {
		if v.Served {
			served = append(served, v.Name)
		}
	}

	return served
}

// resolveVGSGroupVersion applies the public-first precedence rule given a function
// that returns a named CRD ((nil, nil) when absent). A candidate qualifies only if
// its CRD serves the version Ramen's client for that API speaks. Shared by local
// and managed-cluster callers.
func resolveVGSGroupVersion(
	getCRD func(string) (*apiextensionsv1.CustomResourceDefinition, error),
) (schema.GroupVersion, error) {
	candidates := []struct {
		crdName string
		gv      schema.GroupVersion
	}{
		{VGSCRDName, publicgroupsnapv1.SchemeGroupVersion},
		{VGSCRDPrivateName, groupsnapv1beta1.SchemeGroupVersion},
	}

	mismatches := []string{}

	for _, c := range candidates {
		crd, err := getCRD(c.crdName)
		if err != nil {
			return schema.GroupVersion{}, fmt.Errorf("checking VGS CRD %q: %w", c.crdName, err)
		}

		if crd == nil {
			continue
		}

		if crdServesVersion(crd, c.gv.Version) {
			return c.gv, nil
		}

		mismatches = append(mismatches, fmt.Sprintf("%s is installed but serves [%s], not the required %s",
			c.crdName, strings.Join(crdServedVersions(crd), ", "), c.gv.Version))
	}

	msg := "VolumeGroupSnapshot CRD is required. " +
		"Please install either the public (groupsnapshot.storage.k8s.io) or private (groupsnapshot.storage.openshift.io) " +
		"VolumeGroupSnapshot CRD and restart the operator"
	if len(mismatches) > 0 {
		msg += ": " + strings.Join(mismatches, "; ")
	}

	return schema.GroupVersion{}, fmt.Errorf("%s", msg)
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

// GetVolumeGroupSnapshotClasses returns VGS classes using the appropriate API.
// Requires a prior successful EnsureLocalVGSAPI (or ForcePrivateVGSAPIForTesting).
func GetVolumeGroupSnapshotClasses(
	ctx context.Context,
	k8sClient client.Client,
	volumeGroupSnapshotClassSelector metav1.LabelSelector,
) ([]VolumeGroupSnapshotClassWrapper, error) {
	selector, err := metav1.LabelSelectorAsSelector(&volumeGroupSnapshotClassSelector)
	if err != nil {
		return nil, fmt.Errorf("unable to use volume snapshot label selector (%w)", err)
	}

	if UsePublicVGSAPI() {
		return listVolumeGroupSnapshotClasses(
			ctx, k8sClient, selector,
			&publicgroupsnapv1.VolumeGroupSnapshotClassList{},
			func(list *publicgroupsnapv1.VolumeGroupSnapshotClassList) []VolumeGroupSnapshotClassWrapper {
				wrappers := make([]VolumeGroupSnapshotClassWrapper, 0, len(list.Items))
				for i := range list.Items {
					wrappers = append(wrappers, &publicVGSCWrapper{vgsc: &list.Items[i]})
				}

				return wrappers
			},
		)
	}

	return listVolumeGroupSnapshotClasses(
		ctx, k8sClient, selector,
		&groupsnapv1beta1.VolumeGroupSnapshotClassList{},
		func(list *groupsnapv1beta1.VolumeGroupSnapshotClassList) []VolumeGroupSnapshotClassWrapper {
			wrappers := make([]VolumeGroupSnapshotClassWrapper, 0, len(list.Items))
			for i := range list.Items {
				wrappers = append(wrappers, &privateVGSCWrapper{vgsc: &list.Items[i]})
			}

			return wrappers
		},
	)
}

func listVolumeGroupSnapshotClasses[L client.ObjectList](
	ctx context.Context,
	k8sClient client.Client,
	selector labels.Selector,
	list L,
	wrap func(L) []VolumeGroupSnapshotClassWrapper,
) ([]VolumeGroupSnapshotClassWrapper, error) {
	if err := k8sClient.List(ctx, list, client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return nil, fmt.Errorf("error listing volumegroupsnapshotclasses (%w)", err)
	}

	return wrap(list), nil
}

// VolumeGroupSnapshotClassMatchStorageProviders checks if a VGS class matches any storage provider.
func VolumeGroupSnapshotClassMatchStorageProviders(
	volumeGroupSnapshotClass VolumeGroupSnapshotClassWrapper,
	storageClassProviders []string,
) bool {
	for _, storageClassProvider := range storageClassProviders {
		if storageClassProvider == volumeGroupSnapshotClass.GetDriver() {
			return true
		}
	}

	return false
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
