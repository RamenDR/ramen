// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	publicgroupsnapv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumegroupsnapshot/v1"
	groupsnapv1beta1 "github.com/red-hat-storage/external-snapshotter/client/v8/apis/volumegroupsnapshot/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
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

// localVGSAPIKind is the VGS API selected for this process's local cluster.
type localVGSAPIKind int

const (
	localVGSAPINone localVGSAPIKind = iota
	localVGSAPIPublic
	localVGSAPIPrivate
)

var (
	localVGSAPIMu         sync.Mutex
	localVGSAPICached     bool
	localVGSAPI           localVGSAPIKind
	localVGSAPIGeneration uint64
	localVGSAPILastLogErr string
)

// ResetLocalVGSAPICache clears the process-local VGS API selection cache.
// Intended for tests that need to re-detect after changing CRDs.
func ResetLocalVGSAPICache() {
	localVGSAPIMu.Lock()
	defer localVGSAPIMu.Unlock()

	localVGSAPICached = false
	localVGSAPI = localVGSAPINone
	localVGSAPIGeneration++
	localVGSAPILastLogErr = ""
}

func cachedLocalVGSAPI(ctx context.Context, apiReader client.Reader) localVGSAPIKind {
	localVGSAPIMu.Lock()

	if localVGSAPICached {
		kind := localVGSAPI
		localVGSAPIMu.Unlock()

		return kind
	}

	generation := localVGSAPIGeneration
	localVGSAPIMu.Unlock()

	kind, err := detectLocalVGSAPI(ctx, apiReader)
	if err != nil {
		logLocalVGSAPIDetectionError(err)

		return localVGSAPINone
	}

	localVGSAPIMu.Lock()
	defer localVGSAPIMu.Unlock()

	// Do not store a result detected before the most recent reset.
	if generation != localVGSAPIGeneration {
		return localVGSAPINone
	}

	if !localVGSAPICached {
		localVGSAPI = kind
		localVGSAPICached = true
		localVGSAPILastLogErr = ""
	}

	return localVGSAPI
}

// logLocalVGSAPIDetectionError logs detection failures without spamming: the same
// error message is logged at most once until it changes, the cache is reset, or
// detection succeeds. Callers only see bools, so this is where the error is visible.
func logLocalVGSAPIDetectionError(err error) {
	errMsg := err.Error()

	localVGSAPIMu.Lock()

	shouldLog := errMsg != localVGSAPILastLogErr
	if shouldLog {
		localVGSAPILastLogErr = errMsg
	}
	localVGSAPIMu.Unlock()

	if shouldLog {
		ctrl.Log.WithName("vgs-api").Info(
			"VGS API detection failed; will retry until a public or private API is available",
			"error", err,
		)
	}
}

// detectLocalVGSAPI selects the local VGS API. A nil error means the result may be
// cached (public or private). Non-nil errors are not cached so detection can retry
// (API unavailable, RBAC not ready, CRDs still installing, timeouts, unsupported GV).
func detectLocalVGSAPI(ctx context.Context, apiReader client.Reader) (localVGSAPIKind, error) {
	gv, err := SelectVGSGroupVersion(ctx, apiReader)
	if err != nil {
		return localVGSAPINone, err
	}

	switch gv {
	case publicgroupsnapv1.SchemeGroupVersion:
		return localVGSAPIPublic, nil
	case groupsnapv1beta1.SchemeGroupVersion:
		return localVGSAPIPrivate, nil
	default:
		return localVGSAPINone, fmt.Errorf("unsupported VGS group version %q", gv.String())
	}
}

// UsePublicVGSAPI reports whether Ramen should use the public VolumeGroupSnapshot API.
// Requires the public VGS CRD (groupsnapshot.storage.k8s.io) to be installed.
// A successful public/private selection is cached for the process lifetime; failures
// (API unavailable, RBAC not ready, CRDs still installing, timeouts) are not cached.
func UsePublicVGSAPI(ctx context.Context, apiReader client.Reader) bool {
	return cachedLocalVGSAPI(ctx, apiReader) == localVGSAPIPublic
}

// UsePrivateVGSAPI reports whether Ramen should use the private VolumeGroupSnapshot API.
// Public API takes precedence when both CRDs are installed.
// A successful public/private selection is cached for the process lifetime; failures
// (API unavailable, RBAC not ready, CRDs still installing, timeouts) are not cached.
func UsePrivateVGSAPI(ctx context.Context, apiReader client.Reader) bool {
	return cachedLocalVGSAPI(ctx, apiReader) == localVGSAPIPrivate
}

// SelectVGSGroupVersion picks the VGS API GroupVersion using the same precedence as
// UsePublicVGSAPI / UsePrivateVGSAPI: public if the public CRD is installed, otherwise
// private if installed.
func SelectVGSGroupVersion(ctx context.Context, apiReader client.Reader) (schema.GroupVersion, error) {
	if IsCRDInstalled(ctx, apiReader, VGSCRDName) {
		return SelectPublicVGSGroupVersion(), nil
	}

	if IsCRDInstalled(ctx, apiReader, VGSCRDPrivateName) {
		return SelectPrivateVGSGroupVersion(), nil
	}

	return schema.GroupVersion{}, fmt.Errorf("no usable VGS API CRD")
}

// SelectPublicVGSGroupVersion returns the public VolumeGroupSnapshot GroupVersion.
func SelectPublicVGSGroupVersion() schema.GroupVersion {
	return publicgroupsnapv1.SchemeGroupVersion
}

// SelectPrivateVGSGroupVersion returns the private VolumeGroupSnapshot GroupVersion.
func SelectPrivateVGSGroupVersion() schema.GroupVersion {
	return groupsnapv1beta1.SchemeGroupVersion
}

// NewVolumeGroupSnapshot returns an empty VolumeGroupSnapshot of the appropriate API type.
func NewVolumeGroupSnapshot(ctx context.Context, apiReader client.Reader, name, namespace string) client.Object {
	if UsePublicVGSAPI(ctx, apiReader) {
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
func GetVolumeGroupSnapshot(
	ctx context.Context,
	k8sClient client.Client,
	apiReader client.Reader,
	name, namespace string,
) (client.Object, error) {
	vgs := NewVolumeGroupSnapshot(ctx, apiReader, name, namespace)
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, vgs); err != nil {
		return nil, err
	}

	return vgs, nil
}

// ListVolumeGroupSnapshots lists VolumeGroupSnapshots using the appropriate API.
func ListVolumeGroupSnapshots(
	ctx context.Context,
	k8sClient client.Client,
	apiReader client.Reader,
	listOptions ...client.ListOption,
) ([]client.Object, error) {
	if UsePublicVGSAPI(ctx, apiReader) {
		vgsList := &publicgroupsnapv1.VolumeGroupSnapshotList{}
		if err := k8sClient.List(ctx, vgsList, listOptions...); err != nil {
			return nil, err
		}

		objects := make([]client.Object, 0, len(vgsList.Items))
		for i := range vgsList.Items {
			objects = append(objects, &vgsList.Items[i])
		}

		return objects, nil
	}

	vgsList := &groupsnapv1beta1.VolumeGroupSnapshotList{}
	if err := k8sClient.List(ctx, vgsList, listOptions...); err != nil {
		return nil, err
	}

	objects := make([]client.Object, 0, len(vgsList.Items))
	for i := range vgsList.Items {
		objects = append(objects, &vgsList.Items[i])
	}

	return objects, nil
}

// GetStatus returns the VolumeGroupSnapshot status subresource, or nil.
func GetStatus(vgs client.Object) any {
	switch obj := vgs.(type) {
	case *publicgroupsnapv1.VolumeGroupSnapshot:
		return obj.Status
	case *groupsnapv1beta1.VolumeGroupSnapshot:
		return obj.Status
	default:
		return nil
	}
}

// VolumeGroupSnapshotIsReady reports whether the VolumeGroupSnapshot is ready to use.
func VolumeGroupSnapshotIsReady(vgs client.Object) bool {
	switch status := GetStatus(vgs).(type) {
	case *publicgroupsnapv1.VolumeGroupSnapshotStatus:
		return status != nil && status.ReadyToUse != nil && *status.ReadyToUse
	case *groupsnapv1beta1.VolumeGroupSnapshotStatus:
		return status != nil && status.ReadyToUse != nil && *status.ReadyToUse
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
func GetVolumeGroupSnapshotClasses(
	ctx context.Context,
	k8sClient client.Client,
	apiReader client.Reader,
	volumeGroupSnapshotClassSelector metav1.LabelSelector,
) ([]VolumeGroupSnapshotClassWrapper, error) {
	if UsePublicVGSAPI(ctx, apiReader) {
		return getPublicVolumeGroupSnapshotClasses(ctx, k8sClient, volumeGroupSnapshotClassSelector)
	}

	return getPrivateVolumeGroupSnapshotClasses(ctx, k8sClient, volumeGroupSnapshotClassSelector)
}

func getPublicVolumeGroupSnapshotClasses(
	ctx context.Context,
	k8sClient client.Client,
	volumeGroupSnapshotClassSelector metav1.LabelSelector,
) ([]VolumeGroupSnapshotClassWrapper, error) {
	selector, err := metav1.LabelSelectorAsSelector(&volumeGroupSnapshotClassSelector)
	if err != nil {
		return nil, fmt.Errorf("unable to use volume snapshot label selector (%w)", err)
	}

	vgscList := &publicgroupsnapv1.VolumeGroupSnapshotClassList{}
	if err := k8sClient.List(ctx, vgscList, client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return nil, fmt.Errorf("error listing public volumegroupsnapshotclasses (%w)", err)
	}

	wrappers := make([]VolumeGroupSnapshotClassWrapper, 0, len(vgscList.Items))
	for i := range vgscList.Items {
		wrappers = append(wrappers, &publicVGSCWrapper{vgsc: &vgscList.Items[i]})
	}

	return wrappers, nil
}

func getPrivateVolumeGroupSnapshotClasses(
	ctx context.Context,
	k8sClient client.Client,
	volumeGroupSnapshotClassSelector metav1.LabelSelector,
) ([]VolumeGroupSnapshotClassWrapper, error) {
	selector, err := metav1.LabelSelectorAsSelector(&volumeGroupSnapshotClassSelector)
	if err != nil {
		return nil, fmt.Errorf("unable to use volume snapshot label selector (%w)", err)
	}

	vgscList := &groupsnapv1beta1.VolumeGroupSnapshotClassList{}
	if err := k8sClient.List(ctx, vgscList, client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return nil, fmt.Errorf("error listing private volumegroupsnapshotclasses (%w)", err)
	}

	wrappers := make([]VolumeGroupSnapshotClassWrapper, 0, len(vgscList.Items))
	for i := range vgscList.Items {
		wrappers = append(wrappers, &privateVGSCWrapper{vgsc: &vgscList.Items[i]})
	}

	return wrappers, nil
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

// GetVGSClassFromMCV unmarshals a VolumeGroupSnapshotClass from ManagedClusterView using the
// apiVersion returned by the managed cluster.
func GetVGSClassFromMCV(
	_ context.Context,
	_ client.Reader,
	mcvResult []byte,
) (VolumeGroupSnapshotClassWrapper, error) {
	var apiVersionCheck struct {
		APIVersion string `json:"apiVersion"`
	}

	if err := json.Unmarshal(mcvResult, &apiVersionCheck); err != nil {
		return nil, fmt.Errorf("failed to unmarshal apiVersion from MCV result: %w", err)
	}

	if apiVersionCheck.APIVersion == publicgroupsnapv1.SchemeGroupVersion.String() {
		publicVGSC := &publicgroupsnapv1.VolumeGroupSnapshotClass{}
		if err := json.Unmarshal(mcvResult, publicVGSC); err != nil {
			return nil, fmt.Errorf("failed to unmarshal public VolumeGroupSnapshotClass: %w", err)
		}

		return &publicVGSCWrapper{vgsc: publicVGSC}, nil
	}

	privateVGSC := &groupsnapv1beta1.VolumeGroupSnapshotClass{}
	if err := json.Unmarshal(mcvResult, privateVGSC); err != nil {
		return nil, fmt.Errorf("failed to unmarshal private VolumeGroupSnapshotClass: %w", err)
	}

	return &privateVGSCWrapper{vgsc: privateVGSC}, nil
}

// NewEmptyVolumeGroupSnapshotClass returns an empty VolumeGroupSnapshotClass for the given GroupVersion.
func NewEmptyVolumeGroupSnapshotClass(gv schema.GroupVersion) (interface{}, error) {
	switch gv {
	case publicgroupsnapv1.SchemeGroupVersion:
		return &publicgroupsnapv1.VolumeGroupSnapshotClass{}, nil
	case groupsnapv1beta1.SchemeGroupVersion:
		return &groupsnapv1beta1.VolumeGroupSnapshotClass{}, nil
	default:
		return nil, fmt.Errorf("unsupported VGS GroupVersion %s", gv)
	}
}

// WrapVolumeGroupSnapshotClass wraps a public or private VolumeGroupSnapshotClass.
func WrapVolumeGroupSnapshotClass(vgsc interface{}) (VolumeGroupSnapshotClassWrapper, error) {
	switch v := vgsc.(type) {
	case *publicgroupsnapv1.VolumeGroupSnapshotClass:
		return NewPublicVGSCWrapper(v), nil
	case *groupsnapv1beta1.VolumeGroupSnapshotClass:
		return NewPrivateVGSCWrapper(v), nil
	default:
		return nil, fmt.Errorf("unexpected VGSC type %T", vgsc)
	}
}

// NewPublicVGSCWrapper creates a wrapper for a public VolumeGroupSnapshotClass (tests).
func NewPublicVGSCWrapper(vgsc *publicgroupsnapv1.VolumeGroupSnapshotClass) VolumeGroupSnapshotClassWrapper {
	return &publicVGSCWrapper{vgsc: vgsc}
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
