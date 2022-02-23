package controllers

import (
	"fmt"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	rmnutil "github.com/ramendr/ramen/controllers/util"
	"k8s.io/apimachinery/pkg/api/errors"
)

func (d *DRPCInstance) EnsureVolSyncReplicationSetup(homeCluster string) error {
	vsRepNeeded, err := d.isVolSyncReplicationRequired(homeCluster)
	if err != nil {
		return err
	}

	if !vsRepNeeded {
		d.log.Info("No PVCs found that require VolSync replication")

		return nil
	}

	return d.ensureVolSyncReplicationDestination(homeCluster)
}

func (d *DRPCInstance) ensureVolSyncReplicationDestination(srcCluster string) error {
	d.log.Info("Ensuring VolSync replication destination")

	// TODO: Check if we need this block here.
	// We can check for condition per PVC instead
	// ready := d.isVRGConditionDataReady(srcCluster)
	// if !ready {
	// 	d.log.Info("Waiting... VRG condition not ready")

	// 	return fmt.Errorf("VRG condition not ready")
	// }

	// Make sure we have Source and Destination VRGs
	const maxNumberOfVRGs = 2
	if len(d.vrgs) != maxNumberOfVRGs {
		// Create the destination VRG
		err := d.createVolSyncDestManifestWork(srcCluster)
		if err != nil {
			return err
		}

		return WaitForVolSyncManifestWorkCreation
	}

	srcVRG, found := d.vrgs[srcCluster]
	if !found {
		return fmt.Errorf("failed to find source VolSync VRG in cluster %s. VRGs %v", srcCluster, d.vrgs)
	}

	if len(srcVRG.Status.ProtectedPVCs) == 0 {
		d.log.Info("waiting for the source cluster to provide the list of Protected PVCs")

		return WaitForVolSyncSrcRepToComplete
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

		// TODO: Should we handle more than one dstVRG? For now, just settle for one.
		break
	}

	return nil
}

func (d *DRPCInstance) containsMismatchVolSyncPVCs(srcVRG *rmn.VolumeReplicationGroup,
	dstVRG *rmn.VolumeReplicationGroup) bool {
	for _, protectedPVC := range srcVRG.Status.ProtectedPVCs {
		if !protectedPVC.ProtectedByVolSync {
			continue
		}

		var mismatch = true // assume a mismatch
		for _, rdSpec := range dstVRG.Spec.VolSync.RDSpec {
			if protectedPVC.Name == rdSpec.ProtectedPVC.Name {
				mismatch = false

				break
			}
		}

		// We only need one mismatch to return true
		if mismatch {
			return true
		}
	}

	return false
}

func (d *DRPCInstance) updateDestinationVRG(clusterName string, srcVRG *rmn.VolumeReplicationGroup,
	dstVRG *rmn.VolumeReplicationGroup) error {
	// clear RDInfo
	dstVRG.Spec.VolSync.RDSpec = nil
	for _, protectedPVC := range srcVRG.Status.ProtectedPVCs {
		if !protectedPVC.ProtectedByVolSync {
			continue
		}

		rdSpec := rmn.VolSyncReplicationDestinationSpec{
			ProtectedPVC: protectedPVC,
			SSHKeys:      "test-volsync-ssh-keys", //FIXME:
		}

		dstVRG.Spec.VolSync.RDSpec = append(dstVRG.Spec.VolSync.RDSpec, rdSpec)
	}

	return d.updateVRGSpec(clusterName, dstVRG)
}

func (d *DRPCInstance) isVolSyncReplicationRequired(homeCluster string) (bool, error) {
	const required = true

	d.log.Info("Checking whether we have VolSync PVCs that need replication", "cluster", homeCluster)

	vrg := d.vrgs[homeCluster]

	if vrg == nil {
		d.log.Info(fmt.Sprintf("VRG not available on cluster %s - VRGs %v", homeCluster, d.vrgs))

		return false, fmt.Errorf("failed to find VRG on homeCluster %s", homeCluster)
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
		d.log.Info("VRG not available on cluster", "cluster", homeCluster)

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
	vrgMWName := d.mwu.BuildManifestWorkName(rmnutil.MWTypeVRG)
	d.log.Info(fmt.Sprintf("Updating VRG ownedby MW %s for cluster %s", vrgMWName, clusterName))

	mw, err := d.mwu.FindManifestWork(vrgMWName, clusterName)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}

		d.log.Error(err, "failed to update VRG")

		return fmt.Errorf("failed to update VRG %s, in namespace %s (%w)",
			vrgMWName, clusterName, err)
	}

	vrg, err := d.extractVRGFromManifestWork(mw)
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

	d.log.Info(fmt.Sprintf("Updated VRG running in cluster %s. VRG (%+v)", clusterName, vrg))

	return nil
}

func (d *DRPCInstance) createVolSyncDestManifestWork(srcCluster string) error {
	// create VRG ManifestWork
	d.log.Info("Creating VRG ManifestWork for source and destination clusters",
		"Last State:", d.getLastDRState(), "homeCluster", srcCluster)

	// Create or update ManifestWork for all the peers
	for _, drCluster := range d.drPolicy.Spec.DRClusterSet {
		dstCluster := drCluster.Name
		if dstCluster == srcCluster {
			// skip source cluster
			continue
		}

		err := d.ensureNamespaceExistsOnManagedCluster(dstCluster)
		if err != nil {
			return fmt.Errorf("Creating ManifestWork couldn't ensure namespace '%s' on cluster %s exists",
				d.instance.Namespace, drCluster.Name)
		}

		vrg := d.generateVRG()
		vrg.Spec.ReplicationState = rmn.Secondary

		if err := d.mwu.CreateOrUpdateVRGManifestWork(
			d.instance.Name, d.instance.Namespace,
			dstCluster, vrg); err != nil {
			d.log.Error(err, "failed to create or update VolumeReplicationGroup manifest")

			return fmt.Errorf("failed to create or update VolumeReplicationGroup manifest in namespace %s (%w)", dstCluster, err)
		}

		// For now, assume only a pair of clusters in the DRClusterSet
		break
	}

	return nil
}

func (d *DRPCInstance) resetVolSyncRDOnPrimary(clusterName string) error {
	vrgMWName := d.mwu.BuildManifestWorkName(rmnutil.MWTypeVRG)
	d.log.Info(fmt.Sprintf("Resetting RD VRG ownedby MW %s for cluster %s", vrgMWName, clusterName))

	mw, err := d.mwu.FindManifestWork(vrgMWName, clusterName)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}

		d.log.Error(err, "failed to update VRG state")

		return fmt.Errorf("failed to update VRG state for %s, in namespace %s (%w)",
			vrgMWName, clusterName, err)
	}

	vrg, err := d.extractVRGFromManifestWork(mw)
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
