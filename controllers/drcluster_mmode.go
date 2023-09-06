// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"github.com/go-logr/logr"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	viewv1beta1 "github.com/stolostron/multicloud-operators-foundation/pkg/apis/view/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/util"
)

// clusterMModeHandler handles all related maintenance modes that the DRCluster needs
// to manage
// NOTE: Currently this is limited in implementation to just handling the Failover mode
// during regional DR failovers, and parts of the implementation are not generic to handle
// an arbitrary future mode
func (u *drclusterInstance) clusterMModeHandler() error {
	allActivations, err := u.mModeActivationsRequired()
	if err != nil {
		u.requeue = true

		return err
	}

	if activated := checkFailoverMaintenanceActivations(*u.object, allActivations, u.log); !activated {
		u.activateRegionalFailoverPrequisites(allActivations)
	}

	survivors, err := u.pruneMModesActivations(allActivations)
	if err != nil {
		u.log.Error(err, "Error pruning maintenance mode manifests")

		u.requeue = true
	}

	u.log.Info("Survivor count", "survivors", len(survivors))

	u.updateMModeActivationStatus(survivors)

	return nil
}

// mModeActivationsRequired determines all required maintenance modes for the current cluster based
// on the DRPCs that are failing over to this cluster and their required maintenance modes. It returns
// a map of StorageIdentifiers, with the key being the <ProvisionerName>+<ReplicationID>
func (u *drclusterInstance) mModeActivationsRequired() (map[string]ramen.StorageIdentifiers, error) {
	allActivations := map[string]ramen.StorageIdentifiers{}

	drpcCollections, err := DRPCsFailingOverToCluster(u.client, u.log, u.object.GetName())
	if err != nil {
		u.requeue = true

		return nil, err
	}

	for _, drpcCollection := range drpcCollections {
		vrgs, err := u.getVRGs(drpcCollection)
		if err != nil {
			u.log.Info("Failed to get VRGs for DRPC that is failing over",
				"DRPCName", drpcCollection.drpc.GetName(),
				"DRPCNamespace", drpcCollection.drpc.GetNamespace())

			u.requeue = true

			continue
		}

		required, activationsRequired := requiresRegionalFailoverPrerequisites(
			u.ctx,
			u.reconciler.APIReader,
			[]string{u.object.Spec.S3ProfileName},
			drpcCollection.drpc.GetName(),
			drpcCollection.drpc.GetNamespace(),
			vrgs,
			u.object.GetName(),
			u.reconciler.ObjectStoreGetter,
			u.log)
		if !required {
			continue
		}

		for key, storageIdentifiers := range activationsRequired {
			if _, ok := allActivations[key]; ok {
				continue
			}

			allActivations[key] = storageIdentifiers
		}
	}

	u.log.Info("Activations required", "count", len(allActivations))

	return allActivations, nil
}

// getVRGs is a helper function to get the VRGs for the passed in DRPC and DRPolicy association
func (u *drclusterInstance) getVRGs(drpcCollection DRPCAndPolicy) (map[string]*ramen.VolumeReplicationGroup, error) {
	placementObj, err := getPlacementOrPlacementRule(u.ctx, u.client, drpcCollection.drpc, u.log)
	if err != nil {
		return nil, err
	}

	vrgNamespace, err := selectVRGNamespace(u.client, u.log, drpcCollection.drpc, placementObj)
	if err != nil {
		return nil, err
	}

	vrgs, failedToQueryCluster, err := getVRGsFromManagedClusters(
		u.reconciler.MCVGetter,
		drpcCollection.drpc,
		drpcCollection.drPolicy,
		vrgNamespace,
		u.log)
	if err != nil {
		return nil, err
	}

	if failedToQueryCluster != "" && len(vrgs) == 0 {
		// TODO: If no VRG, get from s3 store (hub recovery)
		return vrgs, nil
	}

	return vrgs, nil
}

// activateRegionalFailoverPrequisites activates all regional failover maintenance modes as desired
// by the passed in required activations
func (u *drclusterInstance) activateRegionalFailoverPrequisites(
	activationsRequired map[string]ramen.StorageIdentifiers,
) {
	for _, identifier := range activationsRequired {
		u.log.Info("Activating maintenance mode",
			"provisioner", identifier.StorageProvisioner,
			"ReplciationID", identifier.ReplicationID)

		if err := u.activateRegionalFailoverPrequisite(identifier); err != nil {
			u.log.Error(err, "Error activating maintenance mode",
				"provisioner", identifier.StorageProvisioner,
				"ReplciationID", identifier.ReplicationID)

			u.requeue = true

			continue
		}
	}
}

// activateRegionalFailoverPrequisite activates a regional failover maintenance mode as desired
// for the passed in storage identifier
func (u *drclusterInstance) activateRegionalFailoverPrequisite(identifier ramen.StorageIdentifiers) error {
	mMode := ramen.MaintenanceMode{
		TypeMeta:   metav1.TypeMeta{Kind: "MaintenanceMode", APIVersion: "ramendr.openshift.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: identifier.ReplicationID.ID},
		Spec: ramen.MaintenanceModeSpec{
			StorageProvisioner: identifier.StorageProvisioner,
			TargetID:           identifier.ReplicationID.ID,
			Modes:              []ramen.MMode{ramen.MModeFailover},
		},
	}

	annotations := make(map[string]string)
	annotations[DRClusterNameAnnotation] = u.object.GetName()

	err := u.mwUtil.CreateOrUpdateMModeManifestWork(identifier.ReplicationID.ID, u.object.GetName(), mMode, annotations)
	if err != nil {
		u.log.Error(err, "Error creating or updating maintenance mode manifest", "name", identifier.ReplicationID)

		return err
	}

	return nil
}

// pruneMModesActivations prunes the current active maintenance modes, and only retains
// those that are currently required. It returns a map of maintenance mode manifest work that
// are still required and not pruned, the keys being the targetID for the maintenance mode.
func (u *drclusterInstance) pruneMModesActivations(
	activationsRequired map[string]ramen.StorageIdentifiers,
) (map[string]*ocmworkv1.ManifestWork, error) {
	mModeMWs, err := u.mwUtil.ListMModeManifests(u.object.GetName())
	if err != nil {
		u.requeue = true

		return nil, err
	}

	survivors := make(map[string]*ocmworkv1.ManifestWork, 0)

	for idx := range mModeMWs.Items {
		mModeRequest, err := util.ExtractMModeFromManifestWork(&mModeMWs.Items[idx])
		if err != nil {
			u.log.Error(err, "Error extracting resource from manifest", "name", mModeMWs.Items[idx].GetName())

			u.requeue = true

			continue
		}

		// Check if maintenance mode is still required, if not expire it
		mModeKey := mModeRequest.Spec.StorageProvisioner + mModeRequest.Spec.TargetID
		if _, ok := activationsRequired[mModeKey]; !ok {
			u.log.Info("Pruning maintenance mode activaiton", "name", mModeMWs.Items[idx].GetName())

			if err := u.expireClusterMModeActivation(&mModeMWs.Items[idx]); err != nil {
				u.log.Error(err, "Error expiring maintenance mode", "name", mModeMWs.Items[idx].GetName())

				u.requeue = true
			}

			continue
		}

		survivors[mModeRequest.Spec.TargetID] = &mModeMWs.Items[idx]
	}

	return survivors, nil
}

// expireClusterMModeActivation expires the maintenance mode that is active, after a linger duration
// TODO: Add the expiration/linger feature, for corner case races between DRPC chcking DRCluster status, while
// DRCluster is in the process of deleting the maintenance mode
func (u *drclusterInstance) expireClusterMModeActivation(mw *ocmworkv1.ManifestWork) error {
	return u.mwUtil.DeleteManifestWork(mw.GetName(), mw.GetNamespace())
}

// updateMModeActivationStatus updates maintenance mode status for the cluster based on available
// and required maintenance mode views, while also pruning expired views
func (u *drclusterInstance) updateMModeActivationStatus(survivors map[string]*ocmworkv1.ManifestWork) {
	// Ensure required views are present
	u.createMModeMCV(survivors)

	// Get a list of all views for maintenance mode on the cluster
	mModeMCVs, err := u.reconciler.MCVGetter.ListMModesMCVs(u.object.GetName())
	if err != nil {
		u.log.Error(err, "Error listing maintenance mode views")

		u.requeue = true
	}

	// Reset maintenance mode status for the cluster
	u.object.Status.MaintenanceModes = []ramen.ClusterMaintenanceMode{}

	// Update maintenance mode status for the cluster from views that are valid
	for idx := range mModeMCVs.Items {
		var clusterMaintenanceMode ramen.ClusterMaintenanceMode

		mMode := u.pruneMModeMCV(&mModeMCVs.Items[idx], survivors)
		if mMode == nil {
			// Check if maintenance mode is part of survivors, if so update some status
			key := util.ClusterScopedResourceNameFromMCVName(mModeMCVs.Items[idx].GetName())

			mwMMode := survivors[key]
			if mwMMode == nil {
				continue
			}

			if mMode, err = util.ExtractMModeFromManifestWork(mwMMode); err != nil {
				continue
			}

			clusterMaintenanceMode = ramen.ClusterMaintenanceMode{
				StorageProvisioner: mMode.Spec.StorageProvisioner,
				TargetID:           mMode.Spec.TargetID,
				State:              ramen.MModeStateUnknown,
			}
		} else {
			clusterMaintenanceMode = ramen.ClusterMaintenanceMode{
				StorageProvisioner: mMode.Spec.StorageProvisioner,
				TargetID:           mMode.Spec.TargetID,
				State:              mMode.Status.State,
				Conditions:         mMode.Status.Conditions,
			}
		}

		u.object.Status.MaintenanceModes = append(u.object.Status.MaintenanceModes, clusterMaintenanceMode)

		u.log.Info("Appended maintenance mode status", "status", clusterMaintenanceMode)
	}
}

// createMModeMCV creates managed cluster views for all maintenance mode manifests that are passed in
func (u *drclusterInstance) createMModeMCV(manifests map[string]*ocmworkv1.ManifestWork) {
	for key, manifest := range manifests {
		mModeRequest, err := util.ExtractMModeFromManifestWork(manifests[key])
		if err != nil {
			u.log.Error(err, "Error extracting resource from manifest", "name", manifest.GetName())

			u.requeue = true

			continue
		}

		annotations := make(map[string]string)
		annotations[DRClusterNameAnnotation] = u.object.GetName()

		// Ignore returned MCV, as we are interested only in creating the resource (if not present)
		if _, err := u.reconciler.MCVGetter.GetMModeFromManagedCluster(
			mModeRequest.Spec.TargetID,
			u.object.GetName(),
			annotations,
		); err != nil {
			u.log.Info("Error creating view", "name", mModeRequest.Spec.TargetID, "error", err)

			u.requeue = true
		}
	}
}

// pruneMModeMCV uses the passed in maintenance mode view and the survivor list, to extract and return
// a maintenance mode resource view (with status), or if the view is no longer required prunes it
func (u *drclusterInstance) pruneMModeMCV(
	inMModeMCV *viewv1beta1.ManagedClusterView,
	survivors map[string]*ocmworkv1.ManifestWork,
) *ramen.MaintenanceMode {
	mMode := &ramen.MaintenanceMode{}

	// If view is available then no prune checks, prune only when resource is not found
	err := u.reconciler.MCVGetter.GetResource(inMModeMCV, mMode)
	if err == nil {
		return mMode
	}

	// Other errors require us to try later
	if !errors.IsNotFound(err) {
		u.log.Error(err, "Error fetching viewed resource")

		u.requeue = true

		return nil
	}

	// Do not prune if it is part of survivors (or if already pruned)
	key := util.ClusterScopedResourceNameFromMCVName(inMModeMCV.GetName())
	if _, ok := survivors[key]; ok || !inMModeMCV.GetDeletionTimestamp().IsZero() {
		u.log.Info("Skipping pruning view", "name", inMModeMCV.GetName())

		return nil
	}

	// Prune MCV as this maintenance mode is not part of survivors and is not being deleted and MW is not found
	u.log.Info("Pruning view", "name", inMModeMCV.GetName())

	if err := u.reconciler.MCVGetter.DeleteManagedClusterView(
		u.object.GetName(),
		inMModeMCV.GetName(),
		u.log,
	); err != nil {
		u.requeue = true
	}

	return nil
}

func drClusterMModeCleanup(
	drcluster *ramen.DRCluster,
	mwu *util.MWUtil,
	mcv util.ManagedClusterViewGetter,
	log logr.Logger,
) error {
	mModeMWs, err := mwu.ListMModeManifests(drcluster.GetNamespace())
	if err != nil {
		return err
	}

	for _, mw := range mModeMWs.Items {
		if err := mwu.DeleteManifestWork(mw.GetName(), mw.GetNamespace()); err != nil {
			return err
		}
	}

	mModeMCVs, err := mcv.ListMModesMCVs(drcluster.GetName())
	if err != nil {
		return err
	}

	for _, view := range mModeMCVs.Items {
		if err := mcv.DeleteManagedClusterView(drcluster.GetName(), view.GetName(), log); err != nil {
			return err
		}
	}

	return nil
}
