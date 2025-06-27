// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"encoding/json"
	"fmt"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	rmnutil "github.com/ramendr/ramen/internal/controller/util"
	"github.com/ramendr/ramen/internal/controller/volsync"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	ocmworkv1 "open-cluster-management.io/api/work/v1"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func (d *DRPCInstance) EnsureSecondaryReplicationSetup(srcCluster string) error {
	d.setProgression(rmn.ProgressionEnsuringVolSyncSetup)

	// Create or update the destination VRG
	opResult, err := d.createOrUpdateSecondaryManifestWork(srcCluster)
	if err != nil {
		return err
	}

	if opResult == ctrlutil.OperationResultCreated {
		return ErrWaitForVolSyncManifestWorkCreation
	}

	if _, found := d.vrgs[srcCluster]; !found {
		return fmt.Errorf("failed to find source VRG in cluster %s. VRGs %v", srcCluster, d.vrgs)
	}

	err = d.EnsureVolSyncReplicationSetup(srcCluster)
	if err != nil {
		return err
	}

	if !rmnutil.IsSubmarinerEnabled(d.instance.GetAnnotations()) {
		d.log.Info("Ensuring VolSync replication source")
		err = d.ensureVolSyncReplicationSource(srcCluster)
		if err != nil {
			return err
		}
	}

	return nil

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

		rdInfoLen := len(dstVSRG.Status.RDInfo)
		if rdInfoLen == 0 {
			return fmt.Errorf("Waiting for VolSync RDInfo")
		}

		err := d.updateSourceVSRG(srcCluster, srcVSRG, dstVSRG)
		if err != nil {
			return fmt.Errorf("failed to update dst VSRG on cluster %s - %w", srcCluster, err)
		}

		d.log.Info("Replication Destination", "info", dstVSRG.Status.RDInfo)
		break
	}

	return nil
}

func (d *DRPCInstance) updateSourceVSRG(clusterName string, srcVSRG *rmn.VolumeReplicationGroup,
	dstVSRG *rmn.VolumeReplicationGroup) error {
	// Clear any existing RDSpec in the source VRG
	srcVSRG.Spec.VolSync.RDSpec = nil

	for _, rdInfo := range dstVSRG.Status.RDInfo {
		pskSecretNameCluster := volsync.GetVolSyncPSKSecretNameFromVRGName(d.instance.GetName())

		rsSpec := rmn.VolSyncReplicationSourceSpec{
			ProtectedPVC: rdInfo.ProtectedPVC,
			RsyncTLS: &rmn.RsyncTLSConfig{
				Address: rdInfo.RsyncTLS.Address,
				TLSSecretRef: &corev1.LocalObjectReference{
					Name: pskSecretNameCluster,
				},
			},
		}

		srcVSRG.Spec.VolSync.RSSpec = d.AppendOrUpdate(srcVSRG.Spec.VolSync.RSSpec, rsSpec)
	}

	return d.updateVSRGSpec(clusterName, srcVSRG)
}

// AppendOrUpdate adds rsSpec to the rsSpecList if not present,
// or updates the existing entry with the same PVCName.
func (d *DRPCInstance) AppendOrUpdate(rsSpecList []rmn.VolSyncReplicationSourceSpec,
	rsSpec rmn.VolSyncReplicationSourceSpec) []rmn.VolSyncReplicationSourceSpec {
	for i, info := range rsSpecList {
		if info.ProtectedPVC.Name == rsSpec.ProtectedPVC.Name &&
			info.ProtectedPVC.Namespace == rsSpec.ProtectedPVC.Namespace {
			rsSpecList[i] = rsSpec
			return rsSpecList
		}
	}
	return append(rsSpecList, rsSpec)
}

func (d *DRPCInstance) updateVSRGSpec(clusterName string, tgtVSRG *rmn.VolumeReplicationGroup) error {
	vsrgMWName := d.mwu.BuildManifestWorkName(rmnutil.MWTypeVRG)
	d.log.Info(fmt.Sprintf("Updating VSRG ownedby MW %s for cluster %s", vsrgMWName, clusterName))

	mw, err := d.mwu.FindManifestWork(vsrgMWName, clusterName)
	if err != nil {
		if k8serrors.IsNotFound(err) {
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

	err := json.Unmarshal(vsrgClientManifest.RawExtension.Raw, &vsrg)
	if err != nil {
		return nil, fmt.Errorf("unable to unmarshal VSRG object (%w)", err)
	}

	return vsrg, nil
}

func (d *DRPCInstance) EnsureVolSyncReplicationSetup(srcCluster string) error {
	vsRepNeeded, err := d.IsVolSyncReplicationRequired(srcCluster)
	if err != nil {
		return err
	}

	if !vsRepNeeded {
		d.log.Info("No PVCs found that require VolSync replication")

		return nil
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
	for _, drCluster := range d.drClusters {
		clustersToPropagateSecret = append(clustersToPropagateSecret, drCluster.Name)
	}

	err = volsync.PropagateSecretToClusters(d.ctx, d.reconciler.Client, pskSecretHub,
		d.instance, clustersToPropagateSecret, pskSecretNameCluster, d.vrgNamespace, d.log)
	if err != nil {
		d.log.Error(err, "Error propagating secret to clusters", "clustersToPropagateSecret", clustersToPropagateSecret)

		return fmt.Errorf("%w", err)
	}

	return nil
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
		return false, ErrWaitForSourceCluster
	}

	for _, protectedPVC := range vrg.Status.ProtectedPVCs {
		if protectedPVC.ProtectedByVolSync {
			return required, nil
		}
	}

	return !required, nil
}

// createOrUpdateSecondaryManifestWork creates or updates volsync Secondaries skipping the cluster srcCluster.
// The srcCluster is primary cluster.
func (d *DRPCInstance) createOrUpdateSecondaryManifestWork(srcCluster string) (ctrlutil.OperationResult, error) {
	// create VRG ManifestWork
	d.log.Info("Creating or updating VRG ManifestWork for destination clusters",
		"Last State:", d.getLastDRState(), "homeCluster", srcCluster)

	// Create or update ManifestWork for all the peers
	for _, dstCluster := range rmnutil.DRPolicyClusterNames(d.drPolicy) {
		if dstCluster == srcCluster {
			// skip source cluster
			continue
		}

		err := d.ensureNamespaceManifestWork(dstCluster)
		if err != nil {
			return ctrlutil.OperationResultNone,
				fmt.Errorf("creating ManifestWork couldn't ensure namespace '%s' on cluster %s exists",
					d.instance.Namespace, dstCluster)
		}

		annotations := make(map[string]string)

		annotations[DRPCNameAnnotation] = d.instance.Name
		annotations[DRPCNamespaceAnnotation] = d.instance.Namespace

		vrg, err := d.refreshVRGSecondarySpec(srcCluster, dstCluster)
		if err != nil {
			return ctrlutil.OperationResultNone, err
		}

		opResult, err := d.mwu.CreateOrUpdateVRGManifestWork(
			d.instance.Name, d.vrgNamespace,
			dstCluster, *vrg, annotations)
		if err != nil {
			d.log.Error(err, "failed to create or update VolumeReplicationGroup manifest")

			return ctrlutil.OperationResultNone,
				fmt.Errorf("failed to create or update VRG MW in namespace %s (%w)", dstCluster, err)
		}

		d.log.Info(fmt.Sprintf("Ensured VolSync replication for destination cluster %s. op %s", dstCluster, opResult))

		// For now, assume only a pair of clusters in the DRClusterSet
		return opResult, nil
	}

	return ctrlutil.OperationResultNone, nil
}

func (d *DRPCInstance) refreshVRGSecondarySpec(srcCluster, dstCluster string) (*rmn.VolumeReplicationGroup, error) {
	d.setProgression(rmn.ProgressionSettingupVolsyncDest)

	srcVRGView, found := d.vrgs[srcCluster]
	if !found {
		return nil, fmt.Errorf("failed to find source VolSync VRG in cluster %s. VRGs %v", srcCluster, d.vrgs)
	}

	srcVRG, err := d.getVRGFromManifestWork(srcCluster)
	if err != nil {
		return nil, fmt.Errorf("failed to find source VRG ManifestWork in cluster %s", srcCluster)
	}

	dstVRG := d.newVRG(dstCluster, rmn.Secondary, nil)

	if d.drType == DRTypeAsync {
		if len(srcVRGView.Status.ProtectedPVCs) != 0 {
			d.resetRDSpec(srcVRGView, &dstVRG)
		}

		// Update destination VRG peerClasses with the source classes, such that when secondary is promoted to primary
		// on actions, it uses the same peerClasses as the primary
		dstVRG.Spec.Async.PeerClasses = srcVRG.Spec.Async.PeerClasses
	} else {
		dstVRG.Spec.Sync.PeerClasses = srcVRG.Spec.Sync.PeerClasses
	}

	return &dstVRG, nil
}

func (d *DRPCInstance) resetRDSpec(srcVRG, dstVRG *rmn.VolumeReplicationGroup,
) {
	dstVRG.Spec.VolSync.RDSpec = nil

	for _, protectedPVC := range srcVRG.Status.ProtectedPVCs {
		if !protectedPVC.ProtectedByVolSync {
			continue
		}

		protectedPVC.LastSyncBytes = nil
		protectedPVC.LastSyncTime = nil
		protectedPVC.LastSyncDuration = nil
		protectedPVC.Conditions = nil

		rdSpec := rmn.VolSyncReplicationDestinationSpec{
			ProtectedPVC: protectedPVC,
		}
		dstVRG.Spec.VolSync.RDSpec = append(dstVRG.Spec.VolSync.RDSpec, rdSpec)
	}
}

func (d *DRPCInstance) ResetVolSyncRDOnPrimary(clusterName string) error {
	if d.volSyncDisabled {
		d.log.Info("VolSync is disabled")

		return nil
	}

	mw, err := d.mwu.FindManifestWorkByType(rmnutil.MWTypeVRG, clusterName)
	if err != nil {
		if k8serrors.IsNotFound(err) {
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

		return fmt.Errorf("vrg %s is not set as primary on this cluster, %s", vrg.Name, mw.Namespace)
	}

	if len(vrg.Spec.VolSync.RDSpec) == 0 {
		d.log.Info(fmt.Sprintf("RDSpec for %s has already been cleared on this cluster %s", vrg.Name, mw.Namespace))

		return nil
	}

	vrg.Spec.VolSync.RDSpec = nil

	return d.mwu.UpdateVRGManifestWork(vrg, mw)
}
