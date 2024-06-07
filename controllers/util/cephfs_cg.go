package util

import (
	"context"
	"fmt"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	"github.com/go-logr/logr"
	groupsnapv1alpha1 "github.com/kubernetes-csi/external-snapshotter/client/v7/apis/volumegroupsnapshot/v1alpha1"
	vsv1 "github.com/kubernetes-csi/external-snapshotter/client/v7/apis/volumesnapshot/v1"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	ManualStringAnnotaion        = "ramendr.openshift.io/manual-string"
	RGDOwnerLabel                = "ramendr.openshift.io/rgd"
	CleanupLabelKey              = "volsync.backube/cleanup"
	RGSOwnerLabel         string = "ramendr.openshift.io/rgs"
)

func IsFSCGSupport(mgr manager.Manager) (bool, error) {
	k8sClient, err := client.New(mgr.GetConfig(), client.Options{Scheme: mgr.GetScheme()})
	if err != nil {
		return false, err
	}

	vgsCRD := &apiextensionsv1.CustomResourceDefinition{}
	if err := k8sClient.Get(context.Background(),
		types.NamespacedName{Name: "volumegroupsnapshots.groupsnapshot.storage.k8s.io"},
		vgsCRD,
	); err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

func GetPVCLatestImageRGD(
	pvcname string, rgd ramendrv1alpha1.ReplicationGroupDestination,
) *corev1.TypedLocalObjectReference {
	return rgd.Status.LatestImages[pvcname]
}

func IsReplicationGroupDestinationReady(
	ctx context.Context, k8sClient client.Client,
	rgd *ramendrv1alpha1.ReplicationGroupDestination,
) (bool, error) {
	if len(rgd.Status.ReplicationDestinations) != len(rgd.Spec.RDSpecs) {
		return false, nil
	}

	for _, rdRef := range rgd.Status.ReplicationDestinations {
		rd := &volsyncv1alpha1.ReplicationDestination{}
		if err := k8sClient.Get(ctx,
			types.NamespacedName{Name: rdRef.Name, Namespace: rdRef.Namespace}, rd,
		); err != nil {
			return false, err
		}

		if rd.Status == nil {
			return false, nil
		}

		if rd.Status.RsyncTLS == nil || rd.Status.RsyncTLS.Address == nil {
			return false, nil
		}
	}

	return true, nil
}

func DeleteReplicationGroupSource(
	ctx context.Context, k8sClient client.Client,
	replicationGroupSourceName, replicationGroupSourceNamespace string,
) error {
	rgs := &ramendrv1alpha1.ReplicationGroupSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      replicationGroupSourceName,
			Namespace: replicationGroupSourceNamespace,
		},
	}

	err := k8sClient.Delete(ctx, rgs)

	if errors.IsNotFound(err) {
		return nil
	}

	return err
}

func DeleteReplicationGroupDestination(
	ctx context.Context, k8sClient client.Client,
	replicationGroupDestinationName, replicationGroupDestinationNamespace string,
) error {
	rgd := &ramendrv1alpha1.ReplicationGroupDestination{
		ObjectMeta: metav1.ObjectMeta{
			Name:      replicationGroupDestinationName,
			Namespace: replicationGroupDestinationNamespace,
		},
	}

	err := k8sClient.Delete(ctx, rgd)

	if errors.IsNotFound(err) {
		return nil
	}

	return err
}

func ListPVCsByCephFSCGLabel(
	ctx context.Context, k8sClient client.Client, log logr.Logger,
	pvcLabelSelector metav1.LabelSelector, namespace string,
) (*corev1.PersistentVolumeClaimList, error) {
	return ListPVCsByPVCSelector(ctx, k8sClient, log,
		pvcLabelSelector,
		[]string{namespace},
		true,
	)
}

func CheckIfPVCMatchLabel(pvcLabels map[string]string, pvcLabelSelector *metav1.LabelSelector) (bool, error) {
	if pvcLabelSelector == nil {
		return false, nil
	}

	selector, err := metav1.LabelSelectorAsSelector(pvcLabelSelector)
	if err != nil {
		return false, err
	}

	return selector.Matches(labels.Set(pvcLabels)), nil
}

func GetVolumeGroupSnapshotClassFromPVCsStorageClass(
	ctx context.Context,
	k8sClient client.Client,
	volumeGroupSnapshotClassSelector metav1.LabelSelector,
	pvcsConsistencyGroupSelector *metav1.LabelSelector,
	namespace string,
	logger logr.Logger,
) (string, error) {
	volumeGroupSnapshotClasses, err := GetVolumeGroupSnapshotClasses(ctx, k8sClient, volumeGroupSnapshotClassSelector)
	if err != nil {
		return "", err
	}

	pvcs, err := ListPVCsByCephFSCGLabel(ctx, k8sClient, logger, *pvcsConsistencyGroupSelector, namespace)
	if err != nil {
		return "", err
	}

	storageClassProviders := []string{}

	for _, pvc := range pvcs.Items {
		storageClass := &storagev1.StorageClass{}

		if err := k8sClient.Get(ctx,
			types.NamespacedName{Name: *pvc.Spec.StorageClassName, Namespace: pvc.Namespace},
			storageClass,
		); err != nil {
			return "", err
		}

		storageClassProviders = append(storageClassProviders, storageClass.Provisioner)
	}

	var matchedVolumeGroupSnapshotClassName string

	for _, volumeGroupSnapshotClass := range volumeGroupSnapshotClasses {
		if VolumeGroupSnapshotClassMatchStorageProviders(volumeGroupSnapshotClass, storageClassProviders) {
			matchedVolumeGroupSnapshotClassName = volumeGroupSnapshotClass.Name
		}
	}

	if matchedVolumeGroupSnapshotClassName == "" {
		noVSCFoundErr := fmt.Errorf("unable to find matching volumegroupsnapshotclass for storage provisioner %s",
			storageClassProviders)
		logger.Error(noVSCFoundErr, "No VolumeGroupSnapshotClass found")

		return "", noVSCFoundErr
	}

	return matchedVolumeGroupSnapshotClassName, nil
}

func GetVolumeGroupSnapshotClasses(
	ctx context.Context,
	k8sClient client.Client,
	volumeGroupSnapshotClassSelector metav1.LabelSelector,
) ([]groupsnapv1alpha1.VolumeGroupSnapshotClass, error) {
	selector, err := metav1.LabelSelectorAsSelector(&volumeGroupSnapshotClassSelector)
	if err != nil {
		return nil, fmt.Errorf("unable to use volume snapshot label selector (%w)", err)
	}

	listOptions := []client.ListOption{
		client.MatchingLabelsSelector{
			Selector: selector,
		},
	}

	vgscList := &groupsnapv1alpha1.VolumeGroupSnapshotClassList{}
	if err := k8sClient.List(ctx, vgscList, listOptions...); err != nil {
		return nil, fmt.Errorf("error listing volumegroupsnapshotclasses (%w)", err)
	}

	return vgscList.Items, nil
}

func VolumeGroupSnapshotClassMatchStorageProviders(
	volumeGroupSnapshotClass groupsnapv1alpha1.VolumeGroupSnapshotClass, storageClassProviders []string,
) bool {
	for _, storageClassProvider := range storageClassProviders {
		if storageClassProvider == volumeGroupSnapshotClass.Driver {
			return true
		}
	}

	return false
}

func IsRDExist(rdspec ramendrv1alpha1.VolSyncReplicationDestinationSpec,
	rdspecs []ramendrv1alpha1.VolSyncReplicationDestinationSpec,
) bool {
	for _, rd := range rdspecs {
		if rd.ProtectedPVC.Name == rdspec.ProtectedPVC.Name &&
			rd.ProtectedPVC.Namespace == rdspec.ProtectedPVC.Namespace {
			return true
		}
	}

	return false
}

func DeferDeleteImage(ctx context.Context,
	k8sClient client.Client, imageName, imageNamespace, manual, rgdName string,
) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		volumeSnapshot := &vsv1.VolumeSnapshot{}
		if err := k8sClient.Get(ctx,
			types.NamespacedName{Name: imageName, Namespace: imageNamespace},
			volumeSnapshot); err != nil {
			return err
		}

		labels := volumeSnapshot.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}

		delete(labels, CleanupLabelKey)
		labels[RGDOwnerLabel] = rgdName
		volumeSnapshot.SetLabels(labels)

		annotations := volumeSnapshot.GetAnnotations()
		if annotations == nil {
			annotations = make(map[string]string)
		}
		annotations[ManualStringAnnotaion] = manual
		volumeSnapshot.SetAnnotations(annotations)

		return k8sClient.Update(ctx, volumeSnapshot)
	})
}

func CleanExpiredRDImages(ctx context.Context,
	k8sClient client.Client, rgd *ramendrv1alpha1.ReplicationGroupDestination,
) error {
	volumeSnapshotList := &vsv1.VolumeSnapshotList{}
	options := []client.ListOption{
		client.MatchingLabels{RGDOwnerLabel: rgd.Name},
	}

	if err := k8sClient.List(ctx, volumeSnapshotList, options...); err != nil {
		return err
	}

	for _, vs := range volumeSnapshotList.Items {
		ManualString, ok := vs.Annotations[ManualStringAnnotaion]
		if ok && ManualString != rgd.Status.LastSyncStartTime.String() {
			if err := k8sClient.Delete(ctx, &vsv1.VolumeSnapshot{
				ObjectMeta: metav1.ObjectMeta{Name: vs.Name, Namespace: vs.Namespace},
			}); err != nil {
				return err
			}
		}
	}

	return nil
}
