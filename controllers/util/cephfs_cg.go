package util

import (
	"context"

	"github.com/go-logr/logr"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func GetPVCLatestImageRGD(
	pvcname string, rgd ramendrv1alpha1.ReplicationGroupDestination,
) *corev1.TypedLocalObjectReference {
	return rgd.Status.LatestImages[pvcname]
}

func CreateOrUpdateReplicationGroupDestination(
	ctx context.Context, k8sClient client.Client,
	replicationGroupDestinationName, replicationGroupDestinationNamespace string,
	volumeSnapshotClassSelector metav1.LabelSelector,
	rdSpecs []ramendrv1alpha1.VolSyncReplicationDestinationSpec,
) (*ramendrv1alpha1.ReplicationGroupDestination, error) {
	rgd := &ramendrv1alpha1.ReplicationGroupDestination{
		ObjectMeta: metav1.ObjectMeta{
			Name:      replicationGroupDestinationName,
			Namespace: replicationGroupDestinationNamespace,
		},
	}

	_, err := ctrlutil.CreateOrUpdate(ctx, k8sClient, rgd, func() error {
		rgd.Spec.VolumeSnapshotClassSelector = volumeSnapshotClassSelector
		rgd.Spec.RDSpecs = rdSpecs

		return nil
	})
	if err != nil {
		return nil, err
	}

	return rgd, nil
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

func CheckIfPVCMatchLabel(pvcLabels map[string]string, pvcLabelSelector metav1.LabelSelector) (bool, error) {
	selector, err := metav1.LabelSelectorAsSelector(&pvcLabelSelector)
	if err != nil {
		return false, err
	}

	return selector.Matches(labels.Set(pvcLabels)), nil
}

func CreateOrUpdateReplicationGroupSource(
	ctx context.Context, k8sClient client.Client,
	replicationGroupSourceName, replicationGroupSourceNamespace string,
	volumeGroupSnapshotClassName string,
	volumeGroupSnapshotSource metav1.LabelSelector,
	trigger ramendrv1alpha1.ReplicationSourceTriggerSpec,
) (*ramendrv1alpha1.ReplicationGroupSource, error) {
	rgs := &ramendrv1alpha1.ReplicationGroupSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      replicationGroupSourceName,
			Namespace: replicationGroupSourceNamespace,
		},
	}

	_, err := ctrlutil.CreateOrUpdate(ctx, k8sClient, rgs, func() error {
		rgs.Spec.Trigger = &trigger
		rgs.Spec.VolumeGroupSnapshotClassName = volumeGroupSnapshotClassName
		rgs.Spec.VolumeGroupSnapshotSource = &volumeGroupSnapshotSource

		return nil
	})
	if err != nil {
		return nil, err
	}

	return rgs, nil
}
