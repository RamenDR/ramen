// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"fmt"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	rmnutil "github.com/ramendr/ramen/internal/controller/util"
	"github.com/ramendr/ramen/internal/controller/volsync"
	"k8s.io/apimachinery/pkg/api/errors"
)

func (d *DRPCInstance) EnsureVolSyncReplicationSetup(homeCluster string) error {
	d.log.Info(fmt.Sprintf("Ensure VolSync replication has been setup for cluster %s", homeCluster))

	if d.volSyncDisabled {
		d.log.Info("VolSync is disabled")

		return nil
	}

	vsRepNeeded, err := d.IsVolSyncReplicationRequired(homeCluster)
	if err != nil {
		return err
	}

	if !vsRepNeeded {
		d.log.Info("No PVCs found that require VolSync replication")

		return nil
	}

	err = d.ensureVolSyncReplicationCommon(homeCluster)
	if err != nil {
		return err
	}

	return d.ensureVolSyncReplicationDestination(homeCluster)
}

func (d *DRPCInstance) ensureVolSyncReplicationCommon(srcCluster string) error {
	// Make sure we have Source and Destination VRGs - Source should already have been created at this point
	d.setProgression(rmn.ProgressionEnsuringVolSyncSetup)

	// Create or update the destination VRG
	err := d.createVolSyncDestManifestWork(srcCluster)
	if err != nil {
		return err
	}

	vrgMWCount := d.mwu.GetVRGManifestWorkCount(rmnutil.DRPolicyClusterNames(d.drPolicy))

	const maxNumberOfVRGs = 2
	if len(d.vrgs) != maxNumberOfVRGs || vrgMWCount != maxNumberOfVRGs {
		return WaitForVolSyncManifestWorkCreation
	}

	if _, found := d.vrgs[srcCluster]; !found {
		return fmt.Errorf("failed to find source VolSync VRG in cluster %s. VRGs %v", srcCluster, d.vrgs)
	}

	// Now we should have a source and destination VRG created
	// Since we will use VolSync - create/ensure & propagate a shared psk rsynctls secret to both the src and dst clusters
	pskSecretNameHub := fmt.Sprintf("%s-vs-secret-hub", d.instance.GetName())

	// Ensure/Create the secret on the hub
	pskSecretHub, err := volsync.ReconcileVolSyncReplicationSecret(d.ctx, d.reconciler.Client, d.instance,
		pskSecretNameHub, d.instance.GetNamespace(), d.log)
	if err != nil {
		d.log.Error(err, "Unable to create psk secret on hub for VolSync")

		return fmt.Errorf("%w", err)
	}

	// Propagate the secret to all clusters
	// Note that VRG spec will not contain the psk secret name, we're going to name based on the VRG name itself
	pskSecretNameCluster := volsync.GetVolSyncPSKSecretNameFromVRGName(d.instance.GetName()) // VRG name == DRPC name

	clustersToPropagateSecret := []string{}
	for clusterName := range d.vrgs {
		clustersToPropagateSecret = append(clustersToPropagateSecret, clusterName)
	}

	err = volsync.PropagateSecretToClusters(d.ctx, d.reconciler.Client, pskSecretHub,
		d.instance, clustersToPropagateSecret, pskSecretNameCluster, d.vrgNamespace, d.log)
	if err != nil {
		d.log.Error(err, "Error propagating secret to clusters", "clustersToPropagateSecret", clustersToPropagateSecret)

		return fmt.Errorf("%w", err)
	}

	return nil
}

func (d *DRPCInstance) ensureVolSyncReplicationDestination(srcCluster string) error {
	d.setProgression(rmn.ProgressionSettingupVolsyncDest)

	srcVRG, found := d.vrgs[srcCluster]
	if !found {
		return fmt.Errorf("failed to find source VolSync VRG in cluster %s. VRGs %v", srcCluster, d.vrgs)
	}

	d.log.Info("Ensuring VolSync replication destination")

	if len(srcVRG.Status.ProtectedPVCs) == 0 {
		d.log.Info("ProtectedPVCs on pirmary cluster is empty")

		return WaitForSourceCluster
	}

	for dstCluster, dstVRG := range d.vrgs {
		if dstCluster == srcCluster {
			continue
		}

		if dstVRG == nil {
			return fmt.Errorf("invalid VolSync VRG entry")
		}

		volSyncPVCCount := d.getVolSyncPVCCount(srcCluster)
		if len(dstVRG.Spec.VolSync.RDSpec) != volSyncPVCCount || d.containsMismatchVolSyncPVCs(srcVRG, dstVRG) {
			err := d.updateDestinationVRG(dstCluster, srcVRG, dstVRG)
			if err != nil {
				return fmt.Errorf("failed to update dst VRG on cluster %s - %w", dstCluster, err)
			}
		}

		d.log.Info(fmt.Sprintf("Ensured VolSync replication destination for cluster %s", dstCluster))
		// TODO: Should we handle more than one dstVRG? For now, just settle for one.
		break
	}

	return nil
}

// containsMismatchVolSyncPVCs returns true if a VolSync protected pvc in the source VRG is not
// found in the destination VRG RDSpecs.  Since we never delete protected PVCS from the source VRG,
// we don't check for other case - a protected PVC in destination not found in the source.
func (d *DRPCInstance) containsMismatchVolSyncPVCs(srcVRG *rmn.VolumeReplicationGroup,
	dstVRG *rmn.VolumeReplicationGroup,
) bool {
	for _, protectedPVC := range srcVRG.Status.ProtectedPVCs {
		if !protectedPVC.ProtectedByVolSync {
			continue
		}

		for _, rdSpec := range dstVRG.Spec.VolSync.RDSpec {
			if protectedPVC.Name == rdSpec.ProtectedPVC.Name &&
				protectedPVC.Namespace == rdSpec.ProtectedPVC.Namespace {
				return false
			}
		}

		// VolSync PVC not found in destination.
		return true
	}

	return false
}

func (d *DRPCInstance) updateDestinationVRG(clusterName string, srcVRG *rmn.VolumeReplicationGroup,
	dstVRG *rmn.VolumeReplicationGroup,
) error {
	// clear RDSpec
	dstVRG.Spec.VolSync.RDSpec = nil

	for _, protectedPVC := range srcVRG.Status.ProtectedPVCs {
		if !protectedPVC.ProtectedByVolSync {
			continue
		}

		rdSpec := rmn.VolSyncReplicationDestinationSpec{
			ProtectedPVC: protectedPVC,
		}

		dstVRG.Spec.VolSync.RDSpec = append(dstVRG.Spec.VolSync.RDSpec, rdSpec)
	}

	return d.updateVRGSpec(clusterName, dstVRG)
}

func (d *DRPCInstance) IsVolSyncReplicationRequired(homeCluster string) (bool, error) {
	if d.volSyncDisabled {
		d.log.Info("VolSync is disabled")

		return false, nil
	}

	const required = true

	d.log.Info("Checking if there are PVCs for VolSync replication...", "cluster", homeCluster)

	vrg := d.vrgs[homeCluster]

	if vrg == nil {
		d.log.Info(fmt.Sprintf("isVolSyncReplicationRequired: VRG not available on cluster %s - VRGs %v",
			homeCluster, d.vrgs))

		return false, fmt.Errorf("failed to find VRG on homeCluster %s", homeCluster)
	}

	if len(vrg.Status.ProtectedPVCs) == 0 {
		return false, WaitForSourceCluster
	}

	for _, protectedPVC := range vrg.Status.ProtectedPVCs {
		if protectedPVC.ProtectedByVolSync {
			return required, nil
		}
	}

	return !required, nil
}

func (d *DRPCInstance) getVolSyncPVCCount(homeCluster string) int {
	pvcCount := 0
	vrg := d.vrgs[homeCluster]

	if vrg == nil {
		d.log.Info(fmt.Sprintf("getVolSyncPVCCount: VRG not available on cluster %s", homeCluster))

		return pvcCount
	}

	for _, protectedPVC := range vrg.Status.ProtectedPVCs {
		if protectedPVC.ProtectedByVolSync {
			pvcCount++
		}
	}

	return pvcCount
}

func (d *DRPCInstance) updateVRGSpec(clusterName string, tgtVRG *rmn.VolumeReplicationGroup) error {
	mw, err := d.mwu.FindManifestWorkByType(rmnutil.MWTypeVRG, clusterName)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}

		d.log.Error(err, "failed to update VRG")

		return fmt.Errorf("failed to update VRG MW, in namespace %s (%w)",
			clusterName, err)
	}

	d.log.Info(fmt.Sprintf("Updating VRG ownedby MW %s for cluster %s", mw.Name, clusterName))

	vrg, err := rmnutil.ExtractVRGFromManifestWork(mw)
	if err != nil {
		d.log.Error(err, "failed to update VRG state")

		return err
	}

	if vrg.Spec.ReplicationState != rmn.Secondary {
		d.log.Info(fmt.Sprintf("VRG %s is not secondary on this cluster %s", vrg.Name, mw.Namespace))

		return fmt.Errorf("failed to update MW due to wrong VRG state (%v) for the request",
			vrg.Spec.ReplicationState)
	}

	vrg.Spec.VolSync.RDSpec = tgtVRG.Spec.VolSync.RDSpec

	vrgClientManifest, err := d.mwu.GenerateManifest(vrg)
	if err != nil {
		d.log.Error(err, "failed to generate manifest")

		return fmt.Errorf("failed to generate VRG manifest (%w)", err)
	}

	mw.Spec.Workload.Manifests[0] = *vrgClientManifest

	err = d.reconciler.Update(d.ctx, mw)
	if err != nil {
		return fmt.Errorf("failed to update MW (%w)", err)
	}

	d.log.Info(fmt.Sprintf("Updated VRG running in cluster %s. VRG (%s)", clusterName, vrg.Name))

	return nil
}

// createVolSyncDestManifestWork creates volsync Secondaries skipping the cluster referenced in clusterToSkip.
// Typically, clusterToSkip is passed in as the cluster where volsync is the Primary.
func (d *DRPCInstance) createVolSyncDestManifestWork(clusterToSkip string) error {
	// create VRG ManifestWork
	d.log.Info("Creating VRG ManifestWork for destination clusters",
		"Last State:", d.getLastDRState(), "homeCluster", clusterToSkip)

	// Create or update ManifestWork for all the peers
	for _, dstCluster := range rmnutil.DRPolicyClusterNames(d.drPolicy) {
		if dstCluster == clusterToSkip {
			// skip source cluster
			continue
		}

		err := d.ensureNamespaceManifestWork(dstCluster)
		if err != nil {
			return fmt.Errorf("creating ManifestWork couldn't ensure namespace '%s' on cluster %s exists",
				d.instance.Namespace, dstCluster)
		}

		annotations := make(map[string]string)

		annotations[DRPCNameAnnotation] = d.instance.Name
		annotations[DRPCNamespaceAnnotation] = d.instance.Namespace

		vrg := d.generateVRG(dstCluster, rmn.Secondary)
		if err := d.mwu.CreateOrUpdateVRGManifestWork(
			d.instance.Name, d.vrgNamespace,
			dstCluster, vrg, annotations); err != nil {
			d.log.Error(err, "failed to create or update VolumeReplicationGroup manifest")

			return fmt.Errorf("failed to create or update VolumeReplicationGroup manifest in namespace %s (%w)", dstCluster, err)
		}

		// For now, assume only a pair of clusters in the DRClusterSet
		break
	}

	return nil
}

func (d *DRPCInstance) ResetVolSyncRDOnPrimary(clusterName string) error {
	if d.volSyncDisabled {
		d.log.Info("VolSync is disabled")

		return nil
	}

	mw, err := d.mwu.FindManifestWorkByType(rmnutil.MWTypeVRG, clusterName)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}

		d.log.Error(err, "failed to update VRG state")

		return fmt.Errorf("failed to update VRG state for MW, in namespace %s (%w)",
			clusterName, err)
	}

	d.log.Info(fmt.Sprintf("Resetting RD VRG ownedby MW %s for cluster %s", mw.Name, clusterName))

	vrg, err := rmnutil.ExtractVRGFromManifestWork(mw)
	if err != nil {
		d.log.Error(err, "failed to extract VRG state")

		return err
	}

	if vrg.Spec.ReplicationState != rmn.Primary {
		d.log.Info(fmt.Sprintf("VRG %s not primary on this cluster %s", vrg.Name, mw.Namespace))

		return fmt.Errorf(fmt.Sprintf("VRG %s not primary on this cluster %s", vrg.Name, mw.Namespace))
	}

	if len(vrg.Spec.VolSync.RDSpec) == 0 {
		d.log.Info(fmt.Sprintf("RDSpec for %s has already been cleared on this cluster %s", vrg.Name, mw.Namespace))

		return nil
	}

	vrg.Spec.VolSync.RDSpec = nil

	vrgClientManifest, err := d.mwu.GenerateManifest(vrg)
	if err != nil {
		d.log.Error(err, "failed to generate manifest")

		return fmt.Errorf("failed to generate VRG manifest (%w)", err)
	}

	mw.Spec.Workload.Manifests[0] = *vrgClientManifest

	err = d.reconciler.Update(d.ctx, mw)
	if err != nil {
		return fmt.Errorf("failed to update MW (%w)", err)
	}

	d.log.Info(fmt.Sprintf("Updated VRG running in cluster %s to secondary. VRG (%v)", clusterName, vrg))

	return nil
}
