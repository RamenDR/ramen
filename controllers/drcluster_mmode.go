// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	viewv1beta1 "github.com/stolostron/multicloud-operators-foundation/pkg/apis/view/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/util"
)

// TODO:
//  - Watch created MW and MCV and trigger DRCluster reconciles
//  - Delete MW and MCV as part of DRCluster deletion
//  - Grant klusterlet RBAC for MMode CRUD

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

	if activated := checkFailoverMaintenanceActivations(*u.object, allActivations); !activated {
		u.activateRegionalFailoverPrequisites(allActivations)
	}

	survivors, err := u.pruneMModesActivations(allActivations)
	if err != nil {
		u.log.Error(err, "Error pruning maintenance mode manifests")

		u.requeue = true
	}

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

		required, activationsRequired := requiresRegionalFailoverPrequisites(vrgs, u.object.GetName())
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
// by the passed in required activations, that are currently inactive
func (u *drclusterInstance) activateRegionalFailoverPrequisites(
	activationsRequired map[string]ramen.StorageIdentifiers,
) {
	for _, identifier := range activationsRequired {
		if checkActivationForStorageIdentifier(
			u.object.Status.MaintenanceModes,
			identifier,
			ramen.MModeConditionFailoverActivated,
		) {
			continue
		}

		u.log.Info("Activating maintenance mode",
			"provisioner", identifier.CSIProvisioner,
			"ReplciationID", identifier.ReplicationID)

		if err := u.activateRegionalFailoverPrequisite(identifier); err != nil {
			u.log.Error(err, "Error activating maintenance mode",
				"provisioner", identifier.CSIProvisioner,
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
		ObjectMeta: metav1.ObjectMeta{Name: identifier.ReplicationID},
		Spec: ramen.MaintenanceModeSpec{
			StorageProvisioner: identifier.CSIProvisioner,
			TargetID:           identifier.ReplicationID,
			Modes:              []ramen.MMode{ramen.Failover},
		},
	}

	err := u.mwUtil.CreateOrUpdateMModeManifestWork(identifier.ReplicationID, u.object.GetName(), mMode, nil)
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
		mModeRequest, err := u.mwUtil.ExtractMModeFromManifestWork(&mModeMWs.Items[idx])
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

				continue
			}
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
		mMode := u.pruneMModeMCV(&mModeMCVs.Items[idx], survivors)
		if mMode == nil {
			continue
		}

		clusterMaintenanceMode := ramen.ClusterMaintenanceMode{
			StorageProvisioner: mMode.Spec.StorageProvisioner,
			TargetID:           mMode.Spec.TargetID,
			State:              mMode.Status.State,
			Conditions:         mMode.Status.Conditions,
		}
		u.object.Status.MaintenanceModes = append(u.object.Status.MaintenanceModes, clusterMaintenanceMode)

		u.log.Info("Appended maintenance mode status", "status", clusterMaintenanceMode)
	}
}

// createMModeMCV creates managed cluster views for all maintenance mode manifests that are passed in
func (u *drclusterInstance) createMModeMCV(manifests map[string]*ocmworkv1.ManifestWork) {
	for key, manifest := range manifests {
		mModeRequest, err := u.mwUtil.ExtractMModeFromManifestWork(manifests[key])
		if err != nil {
			u.log.Error(err, "Error extracting resource from manifest", "name", manifest.GetName())

			u.requeue = true

			continue
		}

		// Ignore returned MCV, as we are interested only in creating the resrouce (if not present)
		if _, err := u.reconciler.MCVGetter.GetMModeFromManagedCluster(
			mModeRequest.Spec.TargetID,
			u.object.GetName(),
			nil,
		); err != nil {
			u.log.Error(err, "Error creating view", "name", mModeRequest.Spec.TargetID)

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
	if _, ok := survivors[key]; ok || inMModeMCV.GetDeletionTimestamp().IsZero() {
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
