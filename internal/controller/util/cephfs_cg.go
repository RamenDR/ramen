// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"fmt"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	ramenutils "github.com/backube/volsync/controllers/utils"
	"github.com/go-logr/logr"
	groupsnapv1beta1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumegroupsnapshot/v1beta1"
	vsv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// do not delete the vs in a vgs, only for testing
	SkipDeleteAnnotaion = "rgd.ramendr.openshift.io/do-not-delete"

	RGDOwnerLabel        = "ramendr.openshift.io/rgd"
	RGSOwnerLabel string = "ramendr.openshift.io/rgs"
)

func IsReplicationGroupDestinationReady(
	ctx context.Context, k8sClient client.Client,
	rgd *ramendrv1alpha1.ReplicationGroupDestination,
) (bool, error) {
	if rgd == nil || len(rgd.Spec.RDSpecs) == 0 {
		return false, nil
	}

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

		if rd.Status == nil || rd.Status.RsyncTLS == nil || rd.Status.RsyncTLS.Address == nil {
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
	if k8serrors.IsNotFound(err) {
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
	if k8serrors.IsNotFound(err) {
		return nil
	}

	return err
}

func GetVolumeGroupSnapshotClassFromPVCsStorageClass(
	ctx context.Context,
	k8sClient client.Client,
	volumeGroupSnapshotClassSelector metav1.LabelSelector,
	pvcsConsistencyGroupSelector metav1.LabelSelector,
	namespace []string,
	logger logr.Logger,
) (string, error) {
	volumeGroupSnapshotClasses, err := GetVolumeGroupSnapshotClasses(ctx, k8sClient, volumeGroupSnapshotClassSelector)
	if err != nil {
		return "", err
	}

	pvcs, err := ListPVCsByPVCSelector(ctx, k8sClient, logger, pvcsConsistencyGroupSelector, namespace, false)
	if err != nil {
		return "", err
	}

	storageClassProviders := []string{}

	for _, pvc := range pvcs.Items {
		storageClass := &storagev1.StorageClass{}

		if err := k8sClient.Get(ctx,
			types.NamespacedName{Name: *pvc.Spec.StorageClassName},
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
) ([]groupsnapv1beta1.VolumeGroupSnapshotClass, error) {
	selector, err := metav1.LabelSelectorAsSelector(&volumeGroupSnapshotClassSelector)
	if err != nil {
		return nil, fmt.Errorf("unable to use volume snapshot label selector (%w)", err)
	}

	listOptions := []client.ListOption{
		client.MatchingLabelsSelector{
			Selector: selector,
		},
	}

	vgscList := &groupsnapv1beta1.VolumeGroupSnapshotClassList{}
	if err := k8sClient.List(ctx, vgscList, listOptions...); err != nil {
		return nil, fmt.Errorf("error listing volumegroupsnapshotclasses (%w)", err)
	}

	return vgscList.Items, nil
}

func VolumeGroupSnapshotClassMatchStorageProviders(
	volumeGroupSnapshotClass groupsnapv1beta1.VolumeGroupSnapshotClass, storageClassProviders []string,
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
	k8sClient client.Client, imageName, imageNamespace, rgdName string,
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

		labels[ramenutils.DoNotDeleteLabelKey] = "true"
		labels[RGDOwnerLabel] = rgdName
		volumeSnapshot.SetLabels(labels)

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
		if _, ok := vs.Annotations[SkipDeleteAnnotaion]; ok {
			// get SkipDeleteAnnotaion, do not delete the volume snapshot
			continue
		}

		if !VSInRGD(vs, rgd) {
			if err := k8sClient.Delete(ctx, &vsv1.VolumeSnapshot{
				ObjectMeta: metav1.ObjectMeta{Name: vs.Name, Namespace: vs.Namespace},
			}); err != nil {
				return err
			}
		}
	}

	return nil
}

func VSInRGD(vs vsv1.VolumeSnapshot, rgd *ramendrv1alpha1.ReplicationGroupDestination) bool {
	if rgd == nil {
		return false
	}

	for _, image := range rgd.Status.LatestImages {
		if image.Name == vs.Name {
			return true
		}
	}

	return false
}

func CheckImagesReadyToUse(
	ctx context.Context, k8sClient client.Client,
	latestImages map[string]*corev1.TypedLocalObjectReference,
	namespace string,
	log logr.Logger,
) (bool, error) {
	if len(latestImages) == 0 {
		log.Info("LatestImages is empty")

		return false, nil
	}

	for pvcName := range latestImages {
		latestImage := latestImages[pvcName]
		if latestImage == nil {
			log.Info("Image is nil to check")

			return false, nil
		}

		volumeSnapshot := &vsv1.VolumeSnapshot{}
		if err := k8sClient.Get(ctx,
			types.NamespacedName{Name: latestImage.Name, Namespace: namespace},
			volumeSnapshot,
		); err != nil {
			log.Error(err, "Failed to get volume snapshot")

			return false, err
		}

		if volumeSnapshot.Status == nil || volumeSnapshot.Status.ReadyToUse == nil ||
			(volumeSnapshot.Status.ReadyToUse != nil && !*volumeSnapshot.Status.ReadyToUse) {
			log.Info("Volume snapshot is not ready to use", "VolumeSnapshot", volumeSnapshot.Name)

			return false, nil
		}
	}

	return true, nil
}

func GetVolumeSnapshotsOwnedByVolumeGroupSnapshot(
	ctx context.Context,
	k8sClient client.Client,
	vgs *groupsnapv1beta1.VolumeGroupSnapshot,
	logger logr.Logger,
) ([]vsv1.VolumeSnapshot, error) {
	volumeSnapshotList := &vsv1.VolumeSnapshotList{}
	options := []client.ListOption{
		client.InNamespace(vgs.Namespace),
	}

	if err := k8sClient.List(ctx, volumeSnapshotList, options...); err != nil {
		return nil, err
	}

	logger.Info("GetVolumeSnapshotsOwnedByVolumeGroupSnapshot", "VolumeSnapshotList", volumeSnapshotList.Items)

	var volumeSnapshots []vsv1.VolumeSnapshot

	for _, snapshot := range volumeSnapshotList.Items {
		for _, owner := range snapshot.ObjectMeta.OwnerReferences {
			if owner.Kind == "VolumeGroupSnapshot" && owner.Name == vgs.Name && owner.UID == vgs.UID {
				volumeSnapshots = append(volumeSnapshots, snapshot)

				break
			}
		}
	}

	return volumeSnapshots, nil
}
