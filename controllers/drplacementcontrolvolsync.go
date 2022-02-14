package controllers

import (
	"fmt"

	"github.com/ghodss/yaml"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	rmn "github.com/ramendr/ramen/api/v1alpha1"
	rmnutil "github.com/ramendr/ramen/controllers/util"
	"k8s.io/apimachinery/pkg/api/errors"
)

func (d *DRPCInstance) EnsureVolSyncReplicationSetup(homeCluster string) error {
	if !d.isVolSyncReplicationNeeded(homeCluster) {
		d.log.Info("No PVCs found that requires VolSync replication")

		return nil
	}

	err := d.ensureVolSyncReplicationDestination(homeCluster)
	if err != nil {
		return err
	}

	return d.ensureVolSyncReplicationSource(homeCluster)
}

func (d *DRPCInstance) ensureVolSyncReplicationDestination(srcCluster string) error {
	d.log.Info("Ensuring VolSync replication destination")

	ready := d.isVRGConditionDataReady(srcCluster)
	if !ready {
		d.log.Info("Waiting... VRG condition not ready")

		return fmt.Errorf("VRG condition not ready")
	}

	// Make sure we have Source and Destination VRGs
	const maxNumberOfVSRG = 2
	if len(d.vrgs) != maxNumberOfVSRG {
		// Create the destination VRG
		err := d.createVolSyncDestManifestWork(srcCluster)
		if err != nil {
			return err
		}

		return WaitForVolSyncManifestWorkCreation
	}

	srcVSRG, found := d.vrgs[srcCluster]
	if !found {
		return fmt.Errorf("failed to find VSRG in cluster %s", srcCluster)
	}

	if len(srcVSRG.Status.ProtectedPVCs) == 0 {
		d.log.Info("waiting for the source cluster to provide the list of Protected PVCs")

		return WaitForVolSyncSrcRepToComplete
	}

	for dstCluster, dstVSRG := range d.vrgs {
		if dstCluster == srcCluster {
			continue
		}

		if dstVSRG == nil {
			return fmt.Errorf("invalid VolumeReplicationGroup")
		}

		volSyncPVCCount := d.getVolSyncPVCCount(srcCluster)
		if len(dstVSRG.Spec.VolSync.RDSpec) != volSyncPVCCount {
			err := d.updateDestinationVSRG(dstCluster, srcVSRG, dstVSRG)
			if err != nil {
				return fmt.Errorf("failed to update dst VSRG on cluster %s - %w", dstCluster, err)
			}
		}

		// Only need one dstVSRG for now
		break
	}

	return nil
}

func (d *DRPCInstance) updateDestinationVSRG(clusterName string, srcVSRG *rmn.VolumeReplicationGroup,
	dstVSRG *rmn.VolumeReplicationGroup) error {
	// clear RDInfo
	dstVSRG.Spec.VolSync.RDSpec = nil
	for _, protectedPVC := range srcVSRG.Status.ProtectedPVCs {
		if !protectedPVC.ProtectedByVolSync {
			continue
		}

		rdSpec := rmn.VolSyncReplicationDestinationSpec{
			ProtectedPVC: protectedPVC,
			SSHKeys:      "test-volsync-ssh-keys", //FIXME:
		}

		dstVSRG.Spec.VolSync.RDSpec = append(dstVSRG.Spec.VolSync.RDSpec, rdSpec)
	}

	return d.updateVSRGSpec(clusterName, dstVSRG)
}

func (d *DRPCInstance) ensureVolSyncReplicationSource(srcCluster string) error {
	d.log.Info("Ensuring VolSync replication source")

	const maxNumberOfVSRG = 2
	if len(d.vrgs) != maxNumberOfVSRG {
		return fmt.Errorf("wrong number of VRGS %v", d.vrgs)
	}

	srcVSRG, found := d.vrgs[srcCluster]
	if !found {
		return fmt.Errorf("failed to find the source VSRG in cluster %s", srcCluster)
	}

	for dstCluster, dstVSRG := range d.vrgs {
		if dstCluster == srcCluster {
			continue
		}

		if dstVSRG == nil {
			return fmt.Errorf("invalid VolSyncReplicationGroup")
		}

		rdInfoLen := len(dstVSRG.Status.VolSyncRepStatus.RDInfo)
		if rdInfoLen == 0 {
			return WaitForVolSyncRDInfoAvailibility
		}

		if len(dstVSRG.Status.VolSyncRepStatus.RDInfo) != len(srcVSRG.Spec.VolSync.RSSpec) {
			err := d.updateSourceVSRG(srcCluster, srcVSRG, dstVSRG)
			if err != nil {
				return fmt.Errorf("failed to update dst VSRG on cluster %s - %w", srcCluster, err)
			}
		}

		d.log.Info("Replication Destination", "info", dstVSRG.Status.VolSyncRepStatus.RDInfo)
		// We only need one
		break
	}

	return nil
}

func (d *DRPCInstance) updateSourceVSRG(clusterName string, srcVSRG *rmn.VolumeReplicationGroup,
	dstVSRG *rmn.VolumeReplicationGroup) error {
	// clear RDInfo
	srcVSRG.Spec.VolSync.RDSpec = nil
	for _, rdInfo := range dstVSRG.Status.VolSyncRepStatus.RDInfo {
		rsSpec := rmn.VolSyncReplicationSourceSpec{
			PVCName: rdInfo.PVCName,
			Address: rdInfo.Address,
			SSHKeys: "test-volsync-ssh-keys", //FIXME:
		}

		srcVSRG.Spec.VolSync.RSSpec = append(srcVSRG.Spec.VolSync.RSSpec, rsSpec)
	}

	return d.updateVSRGSpec(clusterName, srcVSRG)
}

func (d *DRPCInstance) isVolSyncReplicationNeeded(homeCluster string) bool {
	const required = true

	d.log.Info("Checking whether We have VolSync PVCs that need replication", "cluster", homeCluster)

	vrg := d.vrgs[homeCluster]

	if vrg == nil {
		d.log.Info("VRG not available on cluster", "cluster", homeCluster)

		return !required
	}

	for _, protectedPVC := range vrg.Status.ProtectedPVCs {
		if protectedPVC.ProtectedByVolSync {
			return required
		}
	}

	return !required
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

func (d *DRPCInstance) updateVSRGSpec(clusterName string, tgtVSRG *rmn.VolumeReplicationGroup) error {
	vsrgMWName := d.mwu.BuildManifestWorkName(rmnutil.MWTypeVRG)
	d.log.Info(fmt.Sprintf("Updating VSRG ownedby MW %s for cluster %s", vsrgMWName, clusterName))

	mw, err := d.mwu.FindManifestWork(vsrgMWName, clusterName)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}

		d.log.Error(err, "failed to update VSRG")

		return fmt.Errorf("failed to update VRG %s, in namespace %s (%w)",
			vsrgMWName, clusterName, err)
	}

	vsrg, err := d.extractVSRGFromManifestWork(mw)
	if err != nil {
		d.log.Error(err, "failed to update VSRG state")

		return err
	}

	if vsrg.Spec.ReplicationState == rmn.Primary {
		vsrg.Spec.VolSync.RSSpec = tgtVSRG.Spec.VolSync.RSSpec
	} else if vsrg.Spec.ReplicationState == rmn.Secondary {
		vsrg.Spec.VolSync.RDSpec = tgtVSRG.Spec.VolSync.RDSpec
	} else {
		d.log.Info(fmt.Sprintf("VSRG %s is neither primary nor secondary on this cluster %s", vsrg.Name, mw.Namespace))

		return fmt.Errorf("failed to update MW due to wrong state")
	}

	vsrgClientManifest, err := d.mwu.GenerateManifest(vsrg)
	if err != nil {
		d.log.Error(err, "failed to generate manifest")

		return fmt.Errorf("failed to generate VSRG manifest (%w)", err)
	}

	mw.Spec.Workload.Manifests[0] = *vsrgClientManifest

	err = d.reconciler.Update(d.ctx, mw)
	if err != nil {
		return fmt.Errorf("failed to update MW (%w)", err)
	}

	d.log.Info(fmt.Sprintf("Updated VSRG running in cluster %s. VSRG (%+v)", clusterName, vsrg))

	return nil
}

func (d *DRPCInstance) extractVSRGFromManifestWork(mw *ocmworkv1.ManifestWork) (*rmn.VolumeReplicationGroup, error) {
	if len(mw.Spec.Workload.Manifests) == 0 {
		return nil, fmt.Errorf("invalid VSRG ManifestWork for type: %s", mw.Name)
	}

	vsrgClientManifest := &mw.Spec.Workload.Manifests[0]
	vsrg := &rmn.VolumeReplicationGroup{}

	err := yaml.Unmarshal(vsrgClientManifest.RawExtension.Raw, &vsrg)
	if err != nil {
		return nil, fmt.Errorf("unable to unmarshal VSRG object (%w)", err)
	}

	return vsrg, nil
}

func (d *DRPCInstance) createVolSyncDestManifestWork(srcCluster string) error {
	// create VSRG ManifestWork
	d.log.Info("Creating VSRG ManifestWork for source and destination clusters",
		"Last State:", d.getLastDRState(), "homeCluster", srcCluster)

	// Create or update ManifestWork for all the peers
	for _, drCluster := range d.drPolicy.Spec.DRClusterSet {
		dstCluster := drCluster.Name
		if dstCluster == srcCluster {
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

func (d *DRPCInstance) resetRDInfoOnPrimary(clusterName string) error {
	vsrgMWName := d.mwu.BuildManifestWorkName(rmnutil.MWTypeVRG)
	d.log.Info(fmt.Sprintf("Resetting RD Info for VSRG ownedby MW %s for cluster %s", vsrgMWName, clusterName))

	mw, err := d.mwu.FindManifestWork(vsrgMWName, clusterName)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}

		d.log.Error(err, "failed to update VSRG state")

		return fmt.Errorf("failed to update VSRG state for %s, in namespace %s (%w)",
			vsrgMWName, clusterName, err)
	}

	vsrg, err := d.extractVSRGFromManifestWork(mw)
	if err != nil {
		d.log.Error(err, "failed to extract VSRG state")

		return err
	}

	if vsrg.Spec.ReplicationState != rmn.Primary {
		d.log.Info(fmt.Sprintf("VSRG %s not primary on this cluster %s", vsrg.Name, mw.Namespace))

		return fmt.Errorf(fmt.Sprintf("VSRG %s not primary on this cluster %s", vsrg.Name, mw.Namespace))
	}

	if len(vsrg.Spec.VolSync.RDSpec) == 0 {
		d.log.Info(fmt.Sprintf("RDSpec for %s has already been cleared on this cluster %s", vsrg.Name, mw.Namespace))

		return nil
	}

	vsrg.Spec.VolSync.RDSpec = nil
	vsrg.Status.VolSyncRepStatus.RDInfo = []rmn.VolSyncReplicationDestinationInfo{}

	vsrgClientManifest, err := d.mwu.GenerateManifest(vsrg)
	if err != nil {
		d.log.Error(err, "failed to generate manifest")

		return fmt.Errorf("failed to generate VRG manifest (%w)", err)
	}

	mw.Spec.Workload.Manifests[0] = *vsrgClientManifest

	err = d.reconciler.Update(d.ctx, mw)
	if err != nil {
		return fmt.Errorf("failed to update MW (%w)", err)
	}

	d.log.Info(fmt.Sprintf("Updated VSRG running in cluster %s to secondary. VRG (%v)", clusterName, vsrg))

	return nil
}
