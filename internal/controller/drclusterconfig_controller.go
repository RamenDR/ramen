// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"time"

	csiaddonsv1alpha1 "github.com/csi-addons/kubernetes-csi-addons/api/csiaddons/v1alpha1"
	volrep "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	"github.com/go-logr/logr"
	netattdefv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	"golang.org/x/time/rate"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	clusterv1alpha1 "open-cluster-management.io/api/cluster/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/util"
)

const (
	drCConfigFinalizerName    = "drclusterconfigs.ramendr.openshift.io/finalizer"
	drCConfigOwnerLabel       = "drclusterconfigs.ramendr.openshift.io/owner"
	drCConfigOwnerName        = "ramen"
	clusterIDClusterClaimName = "id.k8s.io"

	maxReconcileBackoff = 5 * time.Minute
)

// DRClusterConfig condition reasons
const (
	DRClusterConfigConditionReasonInitializing = "Initializing"

	DRClusterConfigConditionConfigurationProcessed = "Succeeded"
	DRClusterConfigConditionConfigurationFailed    = "Failed"

	DRClusterConfigS3Reachable        = "S3Reachable"
	DRClusterConfigS3Unreachable      = "S3Unreachable"
	DRClusterConfigS3ConnectionFailed = "S3ConnectionFailed"
	DRClusterConfigS3BucketNotFound   = "S3BucketNotFound"
	DRClusterConfigS3ListFailed       = "S3ListFailed"
	NADCRDMissing                     = "NADCRDMissing"
	NADWatchNotRegistered             = "NADWatchNotRegistered"
	StaticIPDiscoveryEnabled          = "StaticIPDiscoveryEnabled"
)

// DRClusterConfigReconciler reconciles a DRClusterConfig object
type DRClusterConfigReconciler struct {
	client.Client
	Scheme             *runtime.Scheme
	APIReader          client.Reader
	Log                logr.Logger
	NadWatchRegistered bool
	RateLimiter        *workqueue.TypedRateLimiter[reconcile.Request]
	ObjectStoreGetter  ObjectStoreGetter
}

// NADDRNetworkLabel is an opt-in marker for Ramen's static-IP disaster
// recovery workflow.
//
// NADs labeled with:
//
//	ramendr.openshift.io/dr-network: "true"
//
// are included in DR network discovery and validation. NADs labeled
// "false", or NADs that do not carry this label, are ignored.
const (
	NADDRNetworkLabel      = "ramendr.openshift.io/dr-network"
	NADResourceName        = "network-attachment-definitions.k8s.cni.cncf.io"
	StaticIPDiscoveryReady = "StaticIPDiscoveryReady"
)

// nadGVK is the GroupVersionKind used when listing NADs via the unstructured client.
// NAD is not a first-class Go type in this module, so we use dynamic listing.
var nadGVK = schema.GroupVersionKind{
	Group:   "k8s.cni.cncf.io",
	Version: "v1",
	Kind:    "NetworkAttachmentDefinitionList",
}

//nolint:lll
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drclusterconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drclusterconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=drclusterconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=snapshot.storage.k8s.io,resources=volumesnapshotclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=replication.storage.openshift.io,resources=volumereplicationclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=clusterclaims,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups=csiaddons.openshift.io,resources=networkfenceclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=csiaddons.openshift.io,resources=csiaddonsnodes,verbs=get;list;watch
// +kubebuilder:rbac:groups="k8s.cni.cncf.io",resources=network-attachment-definitions,verbs=get;list;watch

func (r *DRClusterConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("drcc", req.NamespacedName.Name, "rid", util.GetRID())
	log.Info("Entering reconcile loop")

	defer log.Info("Exiting reconcile loop")

	drCConfig := &ramen.DRClusterConfig{}
	if err := r.Client.Get(ctx, req.NamespacedName, drCConfig); err != nil {
		log.Info("Reconcile error", "error", err)

		return ctrl.Result{}, client.IgnoreNotFound(fmt.Errorf("get: %w", err))
	}

	// Ensure there is ony one DRClusterConfig for the cluster
	if _, err := r.GetDRClusterConfig(ctx); err != nil {
		log.Info("Reconcile error", "error", err)

		return ctrl.Result{}, err
	}

	// save status prior to update and do deepEqual pre returning from processing funcs (in each ones' status.update())
	savedDRCConfigStatus := &ramen.DRClusterConfigStatus{}
	drCConfig.Status.DeepCopyInto(savedDRCConfigStatus)

	if savedDRCConfigStatus.Conditions == nil {
		savedDRCConfigStatus.Conditions = []metav1.Condition{}
	}

	if drCConfig.Status.Conditions == nil {
		// Set the DRClusterConfig conditions to unknown as nothing is known at this point
		msg := "Initializing DRClusterConfig"
		setDRClusterConfigInitialCondition(&drCConfig.Status.Conditions, drCConfig.Generation, msg)
	}

	var (
		res ctrl.Result
		err error
	)

	if util.ResourceIsDeleted(drCConfig) {
		res, err = r.processDeletion(ctx, log, drCConfig)
	} else {
		res, err = r.processCreateOrUpdate(ctx, log, drCConfig)

		// Update status
		if err := r.statusUpdate(ctx, drCConfig, savedDRCConfigStatus); err != nil {
			r.Log.Info("failed to update status", "failure", err)
		}
	}

	return res, err
}

func (r *DRClusterConfigReconciler) statusUpdate(ctx context.Context, obj *ramen.DRClusterConfig,
	savedStatus *ramen.DRClusterConfigStatus,
) error {
	if !reflect.DeepEqual(obj.Status, savedStatus) {
		if err := r.Client.Status().Update(ctx, obj); err != nil {
			r.Log.Info("Failed to update drClusterConfig status", "name", obj.Name, "namespace", obj.Namespace,
				"error", err)

			return fmt.Errorf("failed to update drClusterConfig status (%s/%s)", obj.Name, obj.Namespace)
		}
	}

	return nil
}

func setDRClusterConfigInitialCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	util.SetStatusConditionIfNotFound(conditions, metav1.Condition{
		Type:               ramen.DRClusterConfigConfigurationProcessed,
		Reason:             DRClusterConfigConditionReasonInitializing,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionUnknown,
		Message:            message,
	})
	util.SetStatusConditionIfNotFound(conditions, metav1.Condition{
		Type:               ramen.DRClusterConfigS3Healthy,
		Reason:             DRClusterConfigConditionReasonInitializing,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionUnknown,
		Message:            message,
	})
}

func setDRClusterConfigConfigurationProcessedCondition(conditions *[]metav1.Condition, observedGeneration int64,
	message string, conditionStatus metav1.ConditionStatus, reason string,
) {
	util.SetStatusCondition(conditions, metav1.Condition{
		Type:               ramen.DRClusterConfigConfigurationProcessed,
		Reason:             reason,
		ObservedGeneration: observedGeneration,
		Status:             conditionStatus,
		Message:            message,
	})
}

func setDRClusterConfigS3HealthyCondition(conditions *[]metav1.Condition, observedGeneration int64,
	message string, conditionStatus metav1.ConditionStatus, reason string,
) {
	util.SetStatusCondition(conditions, metav1.Condition{
		Type:               ramen.DRClusterConfigS3Healthy,
		Reason:             reason,
		ObservedGeneration: observedGeneration,
		Status:             conditionStatus,
		Message:            message,
	})
}

func (r *DRClusterConfigReconciler) GetDRClusterConfig(ctx context.Context) (*ramen.DRClusterConfig, error) {
	drcConfigs := &ramen.DRClusterConfigList{}
	if err := r.Client.List(ctx, drcConfigs); err != nil {
		return nil, fmt.Errorf("failed to list DRClusterConfig, %w", err)
	}

	if len(drcConfigs.Items) == 0 {
		return nil, fmt.Errorf("failed to find DRClusterConfig")
	}

	if len(drcConfigs.Items) > 1 {
		return nil, fmt.Errorf("multiple DRClusterConfigs found")
	}

	return &drcConfigs.Items[0], nil
}

// processDeletion ensures all cluster claims created by drClusterConfig are deleted, before removing the finalizer on
// the resource itself
func (r *DRClusterConfigReconciler) processDeletion(
	ctx context.Context,
	log logr.Logger,
	drCConfig *ramen.DRClusterConfig,
) (ctrl.Result, error) {
	if err := util.NewResourceUpdater(drCConfig).
		RemoveFinalizer(drCConfigFinalizerName).
		Update(ctx, r.Client); err != nil {
		log.Info("Reconcile error", "error", err)

		return ctrl.Result{Requeue: true},
			fmt.Errorf("failed to remove finalizer for DRClusterConfig resource, %w", err)
	}

	return ctrl.Result{}, nil
}

// pruneClusterClaims will prune all ClusterClaims created by drClusterConfig that are not in the
// passed in survivor list
func (r *DRClusterConfigReconciler) pruneClusterClaims(ctx context.Context, log logr.Logger, survivors []string) error {
	matchLabels := map[string]string{
		drCConfigOwnerLabel: drCConfigOwnerName,
	}

	listOptions := []client.ListOption{
		client.MatchingLabels(matchLabels),
	}

	claims := &clusterv1alpha1.ClusterClaimList{}
	if err := r.Client.List(ctx, claims, listOptions...); err != nil {
		return fmt.Errorf("failed to list ClusterClaims, %w", err)
	}

	for idx := range claims.Items {
		if slices.Contains(survivors, claims.Items[idx].GetName()) {
			continue
		}

		if err := r.Client.Delete(ctx, &claims.Items[idx]); err != nil {
			return fmt.Errorf("failed to delete ClusterClaim %s, %w", claims.Items[idx].GetName(), err)
		}

		log.Info("Pruned ClusterClaim", "claimName", claims.Items[idx].GetName())
	}

	return nil
}

// processCreateOrUpdate protects the resource with a finalizer and updates DRClusterConfig for various storage related
// classes in the cluster. It would finally prune stale ClusterClaims from previous reconciliations, to cleanup upgraded
// clusters which had OCM based claims created for the same.
func (r *DRClusterConfigReconciler) processCreateOrUpdate(
	ctx context.Context,
	log logr.Logger,
	drCConfig *ramen.DRClusterConfig,
) (ctrl.Result, error) {
	// Validate cluster ID
	if err := r.validateClusterIDFromClaim(ctx, drCConfig); err != nil {
		log.Error(err, "failed to validate cluster ID claim")
		setDRClusterConfigConfigurationProcessedCondition(&drCConfig.Status.Conditions, drCConfig.Generation,
			err.Error(), metav1.ConditionFalse, DRClusterConfigConditionConfigurationFailed,
		)

		return ctrl.Result{Requeue: true}, err
	}

	if err := util.NewResourceUpdater(drCConfig).
		AddFinalizer(drCConfigFinalizerName).
		Update(ctx, r.Client); err != nil {
		log.Info("Reconcile error", "error", err)
		setDRClusterConfigConfigurationProcessedCondition(&drCConfig.Status.Conditions, drCConfig.Generation,
			err.Error(), metav1.ConditionFalse, DRClusterConfigConditionConfigurationFailed)

		return ctrl.Result{Requeue: true}, fmt.Errorf("failed to add finalizer for DRClusterConfig resource, %w", err)
	}

	err := r.UpdateStatus(ctx, drCConfig)
	if err != nil {
		log.Info("Reconcile error", "error", err)
		setDRClusterConfigConfigurationProcessedCondition(&drCConfig.Status.Conditions, drCConfig.Generation,
			err.Error(), metav1.ConditionFalse, DRClusterConfigConditionConfigurationFailed)

		return ctrl.Result{Requeue: true}, err
	}

	// As an earlier version is out with ClusterClaims, ensure we prune all claims going forward to address orphaned
	// claims due to upgrades.
	if err := r.pruneClusterClaims(ctx, log, []string{}); err != nil {
		log.Info("Reconcile error", "error", err)
		setDRClusterConfigConfigurationProcessedCondition(&drCConfig.Status.Conditions, drCConfig.Generation,
			err.Error(), metav1.ConditionFalse, DRClusterConfigConditionConfigurationFailed)

		return ctrl.Result{Requeue: true}, err
	}

	setDRClusterConfigConfigurationProcessedCondition(&drCConfig.Status.Conditions, drCConfig.Generation,
		"Configuration processed and validated", metav1.ConditionTrue, DRClusterConfigConditionConfigurationProcessed)

	r.updateStaticIPDiscoveryCondition(ctx, drCConfig)

	if err := r.reconcileDRClusterConfigS3Healthy(ctx, drCConfig); err != nil {
		log.Info("Reconcile error", "error", err)

		return ctrl.Result{Requeue: true}, err
	}

	return ctrl.Result{}, nil
}

func (r *DRClusterConfigReconciler) reconcileDRClusterConfigS3Healthy(
	ctx context.Context, drCConfig *ramen.DRClusterConfig,
) error {
	// Fetch the ramen config resource
	_, ramenConfig, err := ConfigMapGet(ctx, r.Client)
	if err != nil {
		return fmt.Errorf("failed to get Ramen configmap: %w", err)
	}

	// Iterate all profiles listed in it and check for existing healthy ones
	for profileIdx := range ramenConfig.S3StoreProfiles {
		// for each profile, check that it has an actual secret attached to its secretRef ID
		profile := ramenConfig.S3StoreProfiles[profileIdx]
		secretRef := profile.S3SecretRef
		secret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: secretRef.Name, Namespace: secretRef.Namespace},
		}

		if err := r.Client.Get(ctx, types.NamespacedName{
			Namespace: secret.Namespace,
			Name:      secret.Name,
		}, secret); err != nil {
			if !k8serrors.IsNotFound(err) {
				setDRClusterConfigS3HealthyCondition(&drCConfig.Status.Conditions, drCConfig.Generation,
					fmt.Sprintf("Found an unhealthy S3 profile %q for which there's a faulty secret", profile.S3ProfileName),
					metav1.ConditionFalse, DRClusterConfigS3Unreachable)

				return fmt.Errorf("failed to get secret: %w", err)
			}
			// If there's no secret attached to the secretRef's namespacedname -- mark profile as unhealthy
			setDRClusterConfigS3HealthyCondition(&drCConfig.Status.Conditions, drCConfig.Generation,
				fmt.Sprintf("Found an unhealthy S3 profile %q for which there's no secret", profile.S3ProfileName),
				metav1.ConditionFalse, DRClusterConfigS3Unreachable)

			return fmt.Errorf("secret not found: %w", err)
		}
		// Profile does have a secret. Check if it has connectivity and record in status accordingly
		objectStore, reason, err := S3ProfileValidate(ctx, r.Client, r.ObjectStoreGetter, profile.S3ProfileName, r.Log)
		if err != nil {
			setDRClusterConfigS3HealthyCondition(&drCConfig.Status.Conditions, drCConfig.Generation, err.Error(),
				metav1.ConditionFalse, reason)

			return fmt.Errorf("failed to validate s3 profile: %w", err)
		}

		if err := objectStore.HeadBucket(); err != nil {
			setDRClusterConfigS3HealthyCondition(&drCConfig.Status.Conditions, drCConfig.Generation,
				fmt.Sprintf("%s: %s", profile.S3ProfileName, err.Error()),
				metav1.ConditionFalse, DRClusterConfigS3BucketNotFound)

			return fmt.Errorf("failed to validate s3 profile: %s: %w", profile.S3ProfileName, err)
		}

		listKeyPrefix := types.NamespacedName{Name: drCConfig.Name, Namespace: drCConfig.Namespace}.String()
		if _, err := objectStore.ListKeys(listKeyPrefix); err != nil {
			setDRClusterConfigS3HealthyCondition(&drCConfig.Status.Conditions, drCConfig.Generation,
				fmt.Sprintf("%s: %s", profile.S3ProfileName, err.Error()),
				metav1.ConditionFalse, DRClusterConfigS3ListFailed)

			return fmt.Errorf("failed to validate s3 profile: %s: %w", profile.S3ProfileName, err)
		}
	}
	// All S3 profiles are healthy -- record to status and exit
	setDRClusterConfigS3HealthyCondition(&drCConfig.Status.Conditions, drCConfig.Generation,
		fmt.Sprintf("All S3 profiles are healthy"), metav1.ConditionTrue, DRClusterConfigS3Reachable)

	return nil
}

// validateClusterIDFromClaim fetches the cluster ID claim and validates it against the DRClusterConfig.
// It only returns an error and leaves status handling to the caller.
func (r *DRClusterConfigReconciler) validateClusterIDFromClaim(
	ctx context.Context,
	drCConfig *ramen.DRClusterConfig,
) error {
	clusterID, err := r.getClusterID(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cluster ID claim: %w", err)
	}

	if drCConfig.Spec.ClusterID != clusterID {
		return fmt.Errorf("cluster ID claim value %q differs from DRClusterConfig ClusterID %q",
			clusterID, drCConfig.Spec.ClusterID)
	}

	return nil
}

// getClusterID fetches the cluster ID directly from the id.k8s.io ClusterClaim.
func (r *DRClusterConfigReconciler) getClusterID(ctx context.Context) (string, error) {
	claim := &clusterv1alpha1.ClusterClaim{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: clusterIDClusterClaimName}, claim); err != nil {
		return "", fmt.Errorf("failed to get ClusterClaim %q: %w", clusterIDClusterClaimName, err)
	}

	if claim.Spec.Value == "" {
		return "", fmt.Errorf("ClusterClaim %q has an empty cluster ID value", clusterIDClusterClaimName)
	}

	return claim.Spec.Value, nil
}

// UpdateSupportedClasses updates DRClusterConfig status with a list of storage related classes that are marked for DR
// support. The list is sorted alphabetically to avoid out of order listing and status updates due to the same
func (r *DRClusterConfigReconciler) UpdateStatus(
	ctx context.Context,
	drCConfig *ramen.DRClusterConfig,
) error {
	if err := r.updateStorageClassesStatus(ctx, drCConfig); err != nil {
		return err
	}

	if err := r.updateStorageAccessDetailsStatus(ctx, drCConfig); err != nil {
		return err
	}

	if err := r.updateNetworkAttachmentsStatus(ctx, drCConfig); err != nil {
		return err
	}

	return nil
}

func (r *DRClusterConfigReconciler) updateStorageClassesStatus(
	ctx context.Context,
	drCConfig *ramen.DRClusterConfig,
) error {
	var err error

	if drCConfig.Status.StorageClasses, err = r.listDRSupportedSCs(ctx); err != nil {
		return err
	}

	slices.Sort(drCConfig.Status.StorageClasses)

	if drCConfig.Status.VolumeSnapshotClasses, err = r.listDRSupportedVSCs(ctx); err != nil {
		return err
	}

	slices.Sort(drCConfig.Status.VolumeSnapshotClasses)

	if drCConfig.Status.VolumeReplicationClasses, err = r.listDRSupportedVRCs(ctx); err != nil {
		return err
	}

	slices.Sort(drCConfig.Status.VolumeReplicationClasses)

	if drCConfig.Status.VolumeGroupReplicationClasses, err = r.listDRSupportedVGRCs(ctx); err != nil {
		return err
	}

	slices.Sort(drCConfig.Status.VolumeGroupReplicationClasses)

	if drCConfig.Status.VolumeGroupSnapshotClasses, err = r.listDRSupportedVGSCs(ctx); err != nil {
		return err
	}

	slices.Sort(drCConfig.Status.VolumeGroupSnapshotClasses)

	if drCConfig.Status.NetworkFenceClasses, err = r.listDRSupportedNFCs(ctx); err != nil {
		return err
	}

	slices.Sort(drCConfig.Status.NetworkFenceClasses)

	return nil
}

func (r *DRClusterConfigReconciler) updateStorageAccessDetailsStatus(
	ctx context.Context,
	drCConfig *ramen.DRClusterConfig,
) error {
	storageAccessDetails, err := r.listStorageAccessDetails(ctx)
	if err != nil {
		return err
	}

	drCConfig.Status.StorageAccessDetails = storageAccessDetails

	return nil
}

func (r *DRClusterConfigReconciler) updateNetworkAttachmentsStatus(
	ctx context.Context,
	drCConfig *ramen.DRClusterConfig,
) error {
	nads, err := r.listDRSupportedNADs(ctx)
	if err != nil {
		return err
	}

	drCConfig.Status.NetworkAttachments = nads

	r.Log.Info("NAD discovery completed",
		"drclusterconfig", drCConfig.Name,
		"count", len(nads))

	return nil
}

// listDRSupportedSCs returns a list of StorageClasses that are marked as DR supported
func (r *DRClusterConfigReconciler) listDRSupportedSCs(ctx context.Context) ([]string, error) {
	scs := []string{}

	sClasses := &storagev1.StorageClassList{}
	if err := r.Client.List(ctx, sClasses); err != nil {
		return nil, fmt.Errorf("failed to list StorageClasses, %w", err)
	}

	for i := range sClasses.Items {
		if !util.HasLabel(&sClasses.Items[i], StorageIDLabel) {
			continue
		}

		scs = append(scs, sClasses.Items[i].Name)
	}

	return scs, nil
}

// listDRSupportedVSCs returns a list of VolumeSnapshotClasses that are marked as DR supported
func (r *DRClusterConfigReconciler) listDRSupportedVSCs(ctx context.Context) ([]string, error) {
	vscs := []string{}

	vsClasses := &snapv1.VolumeSnapshotClassList{}
	if err := r.Client.List(ctx, vsClasses); err != nil {
		return nil, fmt.Errorf("failed to list VolumeSnapshotClasses, %w", err)
	}

	for i := range vsClasses.Items {
		if !util.HasLabel(&vsClasses.Items[i], StorageIDLabel) {
			continue
		}

		vscs = append(vscs, vsClasses.Items[i].Name)
	}

	return vscs, nil
}

// listDRSupportedVRCs returns a list of VolumeReplicationClasses that are marked as DR supported
func (r *DRClusterConfigReconciler) listDRSupportedVRCs(ctx context.Context) ([]string, error) {
	vrcs := []string{}

	vrClasses := &volrep.VolumeReplicationClassList{}
	if err := r.Client.List(ctx, vrClasses); err != nil {
		return nil, fmt.Errorf("failed to list VolumeReplicationClasses, %w", err)
	}

	for i := range vrClasses.Items {
		if !util.HasLabel(&vrClasses.Items[i], ReplicationIDLabel) {
			continue
		}

		vrcs = append(vrcs, vrClasses.Items[i].Name)
	}

	return vrcs, nil
}

// listDRSupportedVGRCs returns a list of VolumeGroupReplicationClasses that are marked as DR supported
func (r *DRClusterConfigReconciler) listDRSupportedVGRCs(ctx context.Context) ([]string, error) {
	vgrcs := []string{}

	vgrClasses := &volrep.VolumeGroupReplicationClassList{}
	if err := r.Client.List(ctx, vgrClasses); err != nil {
		return nil, fmt.Errorf("failed to list VolumeGroupReplicationClasses, %w", err)
	}

	for i := range vgrClasses.Items {
		if !util.HasLabel(&vgrClasses.Items[i], GroupReplicationIDLabel) {
			continue
		}

		vgrcs = append(vgrcs, vgrClasses.Items[i].Name)
	}

	return vgrcs, nil
}

// listDRSupportedVGSCs returns a list of VolumeGroupSnapshotClasses that are marked as DR supported
func (r *DRClusterConfigReconciler) listDRSupportedVGSCs(ctx context.Context) ([]string, error) {
	vgscs := []string{}

	vgsClassWrappers, err := util.GetVolumeGroupSnapshotClasses(
		ctx, r.Client, metav1.LabelSelector{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list VolumeGroupSnapshotClasses, %w", err)
	}

	for _, vgscWrapper := range vgsClassWrappers {
		if _, ok := vgscWrapper.GetLabels()[StorageIDLabel]; !ok {
			continue
		}

		vgscs = append(vgscs, vgscWrapper.GetName())
	}

	return vgscs, nil
}

// listDRSupportedNFCs returns a list of NetworkFenceClass
func (r *DRClusterConfigReconciler) listDRSupportedNFCs(ctx context.Context) ([]string, error) {
	nfcs := []string{}

	nfClasses := &csiaddonsv1alpha1.NetworkFenceClassList{}
	if err := r.Client.List(ctx, nfClasses); err != nil {
		return nil, fmt.Errorf("failed to list NetworkFenceClasses, %w", err)
	}

	for i := range nfClasses.Items {
		if !util.HasAnnotation(&nfClasses.Items[i], StorageIDLabel) {
			continue
		}

		nfcs = append(nfcs, nfClasses.Items[i].Name)
	}

	return nfcs, nil
}

// listMatchingNFCClientStatus returns a list of listMatchingNFCClientStatus which refer to networkFenceClass
func (r *DRClusterConfigReconciler) listMatchingNFCClientStatus(ctx context.Context) (
	[]csiaddonsv1alpha1.NetworkFenceClientStatus, error,
) {
	csiNFClientStatus := []csiaddonsv1alpha1.NetworkFenceClientStatus{}

	nfcs, err := r.listDRSupportedNFCs(ctx)
	if err != nil {
		return csiNFClientStatus, err
	}

	csiAddonsNodeList := &csiaddonsv1alpha1.CSIAddonsNodeList{}
	if err := r.Client.List(ctx, csiAddonsNodeList); err != nil {
		return csiNFClientStatus, fmt.Errorf("failed to list CSIAddonsNodes, %w", err)
	}

	for i := range csiAddonsNodeList.Items {
		if len(csiAddonsNodeList.Items[i].Status.NetworkFenceClientStatus) == 0 {
			continue
		}

		nfClientStatuses := csiAddonsNodeList.Items[i].Status.NetworkFenceClientStatus

		// consider only the NetworkFenceClientStatus which match the NFC Name
		for _, nfClientStatus := range nfClientStatuses {
			for _, nfc := range nfcs {
				if nfClientStatus.NetworkFenceClassName == nfc {
					csiNFClientStatus = append(csiNFClientStatus, nfClientStatus)
				}
			}
		}
	}

	return csiNFClientStatus, nil
}

func (r *DRClusterConfigReconciler) listStorageAccessDetails(ctx context.Context) ([]ramen.StorageAccessDetail, error) {
	nfcClientStatus, err := r.listMatchingNFCClientStatus(ctx)
	if err != nil {
		return nil, err
	}

	provisionerCIDRs := make(map[string][]string)

	for _, status := range nfcClientStatus {
		nf := &csiaddonsv1alpha1.NetworkFenceClass{}
		if err := r.Client.Get(ctx, types.NamespacedName{Name: status.NetworkFenceClassName}, nf); err != nil {
			r.Log.Info("failed to get NetworkFenceClass", "name", status.NetworkFenceClassName, "error", err)

			continue
		}

		for _, cl := range status.ClientDetails {
			provisionerCIDRs[nf.Spec.Provisioner] = append(provisionerCIDRs[nf.Spec.Provisioner], cl.Cidrs...)
		}
	}

	storageAccessDetails := []ramen.StorageAccessDetail{}
	for provisioner, cidrs := range provisionerCIDRs {
		storageAccessDetails = append(storageAccessDetails, ramen.StorageAccessDetail{
			StorageProvisioner: provisioner,
			CIDRs:              cidrs,
		})
	}

	return storageAccessDetails, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DRClusterConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	drccMapFn := handler.EnqueueRequestsFromMapFunc(handler.MapFunc(
		func(ctx context.Context, obj client.Object) []reconcile.Request {
			drcConfig, err := r.GetDRClusterConfig(ctx)
			if err != nil {
				ctrl.Log.Info(fmt.Sprintf("failed processing DRClusterConfig mapping, %v", err))

				return []ctrl.Request{}
			}

			return []ctrl.Request{
				reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name: drcConfig.GetName(),
					},
				},
			}
		}),
	)

	drccPredFn := builder.WithPredicates(predicate.NewPredicateFuncs(
		func(object client.Object) bool {
			return true
		}),
	)

	rateLimiter := workqueue.NewTypedMaxOfRateLimiter(
		workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](1*time.Second, maxReconcileBackoff),
		// defaults from client-go
		//nolint:mnd
		&workqueue.TypedBucketRateLimiter[reconcile.Request]{Limiter: rate.NewLimiter(rate.Limit(10), 100)},
	)

	if r.RateLimiter != nil {
		rateLimiter = *r.RateLimiter
	}

	controller := ctrl.NewControllerManagedBy(mgr)

	controllerBuilder := controller.WithOptions(ctrlcontroller.Options{
		RateLimiter: rateLimiter,
	}).For(&ramen.DRClusterConfig{}).
		Watches(&storagev1.StorageClass{}, drccMapFn, drccPredFn).
		Watches(&snapv1.VolumeSnapshotClass{}, drccMapFn, drccPredFn).
		Watches(&volrep.VolumeReplicationClass{}, drccMapFn, drccPredFn).
		Watches(&volrep.VolumeGroupReplicationClass{}, drccMapFn, drccPredFn).
		Watches(&csiaddonsv1alpha1.NetworkFenceClass{}, drccMapFn, drccPredFn).
		Watches(&csiaddonsv1alpha1.CSIAddonsNode{}, drccMapFn, drccPredFn)

	if err := util.EnsureLocalVGSAPI(context.TODO(), mgr.GetAPIReader()); err != nil {
		r.Log.Info("VolumeGroupSnapshotClass API not available, skipping watch", "reason", err.Error())
	} else {
		controllerBuilder = util.WatchesVolumeGroupSnapshotClass(controllerBuilder, r.Log, drccMapFn, drccPredFn)
	}

	// NAD CRD is optional (requires Multus). Only register the watch when the
	// CRD is already present at startup; on clusters without Multus the
	// RequeueAfter in processCreateOrUpdate re-checks periodically.
	if r.nadCRDInstalled(context.Background()) {
		r.NadWatchRegistered = true
		controllerBuilder = controller.Watches(&netattdefv1.NetworkAttachmentDefinition{}, drccMapFn, drccPredFn)
	}

	return controllerBuilder.Complete(r)
}

func (r *DRClusterConfigReconciler) nadCRDInstalled(ctx context.Context) bool {
	return util.IsCRDInstalled(ctx, r.APIReader, NADResourceName)
}

// parseCNIType extracts the CNI plugin type from spec.config (a JSON string)
// of a NetworkAttachmentDefinition.
// Returns "unknown" with a non-nil error when config is absent or the type field is not set.
func parseCNIType(nad *netattdefv1.NetworkAttachmentDefinition) (string, error) {
	if nad.Spec.Config == "" {
		return "unknown", fmt.Errorf("spec.config is empty")
	}

	// spec.config is a JSON string containing the CNI configuration.
	var cfg struct {
		Type    string `json:"type"`
		Plugins []struct {
			Type string `json:"type"`
		} `json:"plugins"`
	}

	if err := json.Unmarshal([]byte(nad.Spec.Config), &cfg); err != nil {
		return "unknown", fmt.Errorf("spec.config is not valid JSON: %w", err)
	}

	// Simple CNI config.
	if cfg.Type != "" {
		return cfg.Type, nil
	}

	// Multus conflist format: first plugin is the primary CNI.
	if len(cfg.Plugins) > 0 && cfg.Plugins[0].Type != "" {
		return cfg.Plugins[0].Type, nil
	}

	return "unknown", nil
}

func (r *DRClusterConfigReconciler) listDRSupportedNADs(ctx context.Context) ([]ramen.NetworkAttachment, error) {
	// The NAD CRD is optional — it is only present when Multus is installed.
	if !r.nadCRDInstalled(ctx) {
		r.Log.Info(
			"Skipping NAD discovery because the NetworkAttachmentDefinition CRD is not installed",
			"gvk", nadGVK.String(),
		)

		return nil, nil
	}

	nadList := &netattdefv1.NetworkAttachmentDefinitionList{}
	nadList.SetGroupVersionKind(nadGVK)

	if err := r.Client.List(ctx, nadList,
		client.MatchingLabels{NADDRNetworkLabel: "true"},
	); err != nil {
		return nil, fmt.Errorf("failed to list NADs with label %s=true: %w", NADDRNetworkLabel, err)
	}

	nads := []ramen.NetworkAttachment{}

	for i := range nadList.Items {
		item := &nadList.Items[i]

		cniType, err := parseCNIType(item)
		if err != nil {
			// Non-fatal: record as unknown so the NAD still appears in status.
			r.Log.Error(err, "Failed to parse CNI type; using 'unknown'",
				"nad", item.GetName(), "namespace", item.GetNamespace())

			cniType = "unknown"
		}

		nads = append(nads, ramen.NetworkAttachment{
			Name:      item.GetName(),
			Namespace: item.GetNamespace(),
			CNIType:   cniType,
		})
	}

	// Sort for stable comparison — matches the slices.Sort pattern used for
	// StorageClasses and VolumeReplicationClasses in the same reconciler.
	slices.SortFunc(nads, func(a, b ramen.NetworkAttachment) int {
		nsNameA := a.Namespace + "/" + a.Name
		nsNameB := b.Namespace + "/" + b.Name

		switch {
		case nsNameA < nsNameB:
			return -1
		case nsNameA > nsNameB:
			return 1
		default:
			return 0
		}
	})

	return nads, nil
}

func setStaticIPDiscoveryCondition(
	conditions *[]metav1.Condition,
	status metav1.ConditionStatus,
	reason, message string,
) {
	util.SetStatusCondition(conditions, metav1.Condition{
		Type:    StaticIPDiscoveryReady,
		Status:  status,
		Reason:  reason,
		Message: message,
	})
}

// Determine static-IP discovery readiness based on NAD CRD availability
// and whether the NAD watch was successfully registered at controller startup.
//
// Scenarios:
// 1. NAD CRD absent:
//   - Static-IP discovery is unavailable.
//   - Report NADCRDMissing.
//   - If the CRD is installed later, the controller must be restarted
//     to register the NAD watch.
//
// 2. NAD CRD present but watch not registered:
//   - Static-IP discovery remains unavailable because NAD changes
//     cannot be observed.
//   - Report that a controller restart is required.
//
// 3. NAD CRD present and watch registered:
//   - Static-IP discovery is fully operational.
//   - NAD create/update/delete events will be observed and processed.
func (r *DRClusterConfigReconciler) updateStaticIPDiscoveryCondition(
	ctx context.Context,
	drCConfig *ramen.DRClusterConfig,
) {
	if !r.nadCRDInstalled(ctx) {
		r.NadWatchRegistered = false

		setStaticIPDiscoveryCondition(
			&drCConfig.Status.Conditions,
			metav1.ConditionFalse,
			NADCRDMissing,
			"NetworkAttachmentDefinition CRD is not installed on the cluster",
		)

		return
	}

	if !r.NadWatchRegistered {
		setStaticIPDiscoveryCondition(
			&drCConfig.Status.Conditions,
			metav1.ConditionFalse,
			NADWatchNotRegistered,
			"NetworkAttachmentDefinition CRD is available, but NAD monitoring is disabled until the controller is restarted",
		)

		return
	}

	setStaticIPDiscoveryCondition(
		&drCConfig.Status.Conditions,
		metav1.ConditionTrue,
		StaticIPDiscoveryEnabled,
		"NetworkAttachmentDefinition monitoring is enabled and static IP discovery is operational",
	)
}
