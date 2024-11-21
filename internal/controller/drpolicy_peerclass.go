// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"fmt"
	"slices"

	volrep "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	"github.com/go-logr/logr"
	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	"github.com/stolostron/multicloud-operators-foundation/pkg/apis/view/v1beta1"
	storagev1 "k8s.io/api/storage/v1"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/util"
)

// classLists contains [storage|snapshot|replication]classes from ManagedClusters with the required ramen storageID or,
// replicationID labels
type classLists struct {
	clusterID string
	sClasses  []*storagev1.StorageClass
	vsClasses []*snapv1.VolumeSnapshotClass
	vrClasses []*volrep.VolumeReplicationClass
}

// peerInfo contains a single peer relationship between a PAIR of clusters for a common storageClassName across
// these peers. This should directly translate to DRPolicy.Status.[Async|Sync] updates.
// NOTE: storageID discussed in comments relates to the value of the label "ramendr.openshift.io/storageid" for the
// respective class. replicationID in comments relates to the value of the label "ramendr.openshift.io/replicationid"
// for the respective class
type peerInfo struct {
	// replicationID is an empty string (indicating no common VolumeReplicationClass) or the common replicationID value
	// for the corresponding VRClass on each peer
	replicationID string

	// storageIDs is a list containing,
	// - A single storageID if the storageClassName across the peers have the same storageID, denoting a synchronous
	// pairing for the storageClassName
	// -  It is a pair of storageIDs, if there exists an asynchronous pairing between the clusters, either due to
	// a common replicationID or due to required VolumeSnapshotClasses on each cluster
	storageIDs []string

	// storageClassName is the name of a StorageClass that is common across the peers
	storageClassName string

	// clusterIDs is a list of 2 IDs that denote the IDs for the clusters in this peer relationship
	clusterIDs []string
}

// peerClassMatchesPeer compares the storage class name across the PeerClass and passed in peer for a match, and if
// matched compares the clusterIDs that this peer represents. No further matching is required to determine a unique
// PeerClass matching a peer
func peerClassMatchesPeer(pc ramen.PeerClass, peer peerInfo) bool {
	if pc.StorageClassName != peer.storageClassName {
		return false
	}

	if !slices.Equal(pc.ClusterIDs, peer.clusterIDs) {
		return false
	}

	return true
}

// findStatusPeerInPeers finds PeerClass in passed in peers, and returns true and the peer if founds
func findStatusPeerInPeers(pc ramen.PeerClass, peers []peerInfo) (bool, peerInfo) {
	for _, peer := range peers {
		if !peerClassMatchesPeer(pc, peer) {
			continue
		}

		return true, peer
	}

	return false, peerInfo{}
}

// findPeerInStatusPeer finds passed in peer in passed in list of PeerClass, and returns true if found
func findPeerInStatusPeer(peer peerInfo, pcs []ramen.PeerClass) bool {
	for _, pc := range pcs {
		if !peerClassMatchesPeer(pc, peer) {
			continue
		}

		return true
	}

	return false
}

func peerClassFromPeer(peer peerInfo) ramen.PeerClass {
	return ramen.PeerClass{
		ClusterIDs:       peer.clusterIDs,
		StorageClassName: peer.storageClassName,
		StorageID:        peer.storageIDs,
		ReplicationID:    peer.replicationID,
	}
}

// pruneAndUpdateStatusPeers prunes the peer classes in status based on current peers that are passed in, and also adds
// new peers to status.
func pruneAndUpdateStatusPeers(statusPeers []ramen.PeerClass, peers []peerInfo) []ramen.PeerClass {
	outStatusPeers := []ramen.PeerClass{}

	// Prune and update existing
	for _, peerClass := range statusPeers {
		found, peer := findStatusPeerInPeers(peerClass, peers)
		if !found {
			continue
		}

		outStatusPeers = append(outStatusPeers, peerClassFromPeer(peer))
	}

	// Add new peers
	for _, peer := range peers {
		found := findPeerInStatusPeer(peer, statusPeers)
		if found {
			continue
		}

		outStatusPeers = append(outStatusPeers, peerClassFromPeer(peer))
	}

	return outStatusPeers
}

// updatePeerClassStatus updates the DRPolicy.Status.[Async|Sync] peer lists based on passed in peerInfo values
func updatePeerClassStatus(u *drpolicyUpdater, syncPeers, asyncPeers []peerInfo) error {
	u.object.Status.Async.PeerClasses = pruneAndUpdateStatusPeers(u.object.Status.Async.PeerClasses, asyncPeers)
	u.object.Status.Sync.PeerClasses = pruneAndUpdateStatusPeers(u.object.Status.Sync.PeerClasses, syncPeers)

	return u.statusUpdate()
}

// provisionerMatchesSC inspects StorageClass named scName in the passed in classLists and returns true if its
// provisioner value matches the driver
func provisionerMatchesSC(scName string, cl classLists, driver string) bool {
	for scIdx := range cl.sClasses {
		if cl.sClasses[scIdx].GetName() != scName {
			continue
		}

		if cl.sClasses[scIdx].Provisioner != driver {
			return false
		}

		return true
	}

	return false
}

// hasVSClassMatchingSID returns if classLists has a VolumeSnapshotClass matching the passed in storageID
func hasVSClassMatchingSID(scName string, cl classLists, sID string) bool {
	for idx := range cl.vsClasses {
		sid := cl.vsClasses[idx].GetLabels()[StorageIDLabel]
		if sid == "" || sid != sID {
			continue
		}

		if !provisionerMatchesSC(scName, cl, cl.vsClasses[idx].Driver) {
			continue
		}

		return true
	}

	return false
}

// isAsyncVSClassPeer inspects provided pair of classLists for a matching VolumeSnapshotClass, that is linked to the
// StorageClass whose storageID is respectively sIDA or sIDB
func isAsyncVSClassPeer(scName string, clA, clB classLists, sIDA, sIDB string) bool {
	// No provisioner match as we can do cross provisioner VSC based protection
	return hasVSClassMatchingSID(scName, clA, sIDA) && hasVSClassMatchingSID(scName, clB, sIDB)
}

// getVRID inspects VolumeReplicationClass in the passed in classLists at the specified index, and returns,
// - an empty string if the VRClass fails to match the passed in storageID, schedule or provisioner, or
// - the value of replicationID on the VRClass
func getVRID(scName string, cl classLists, vrcIdx int, inSID string, schedule string) string {
	sID := cl.vrClasses[vrcIdx].GetLabels()[StorageIDLabel]
	if sID == "" || inSID != sID {
		return ""
	}

	if cl.vrClasses[vrcIdx].Spec.Parameters[VRClassScheduleKey] != schedule {
		return ""
	}

	if !provisionerMatchesSC(scName, cl, cl.vrClasses[vrcIdx].Spec.Provisioner) {
		return ""
	}

	rID := cl.vrClasses[vrcIdx].GetLabels()[VolumeReplicationIDLabel]

	return rID
}

// getAsyncVRClassPeer inspects if there is a common replicationID among the vrClasses in the passed in classLists,
// that relate to the corresponding storageIDs and schedule, and returns the replicationID or "" if there was no match
func getAsyncVRClassPeer(scName string, clA, clB classLists, sIDA, sIDB string, schedule string) string {
	for vrcAidx := range clA.vrClasses {
		ridA := getVRID(scName, clA, vrcAidx, sIDA, schedule)
		if ridA == "" {
			continue
		}

		for vrcBidx := range clB.vrClasses {
			ridB := getVRID(scName, clB, vrcBidx, sIDB, schedule)
			if ridB == "" {
				continue
			}

			if ridA != ridB {
				continue
			}

			return ridA
		}
	}

	return ""
}

// getAsyncPeers determines if scName in the first classList has asynchronous peers in the remaining classLists.
// The clusterID and sID are the corresponding IDs for the first cluster in the classList, and the schedule is
// the desired asynchronous schedule that requires to be matched
// nolint:gocognit
func getAsyncPeers(scName string, clusterID string, sID string, cls []classLists, schedule string) []peerInfo {
	peers := []peerInfo{}

	for _, cl := range cls[1:] {
		for scIdx := range cl.sClasses {
			if cl.sClasses[scIdx].GetName() != scName {
				continue
			}

			sIDcl := cl.sClasses[scIdx].GetLabels()[StorageIDLabel]
			if sID == sIDcl {
				break
			}

			rID := getAsyncVRClassPeer(scName, cls[0], cl, sID, sIDcl, schedule)
			if rID == "" {
				if !isAsyncVSClassPeer(scName, cls[0], cl, sID, sIDcl) {
					continue
				}
			}

			peers = append(peers, peerInfo{
				storageClassName: scName,
				storageIDs:       []string{sID, sIDcl},
				clusterIDs:       []string{clusterID, cl.clusterID},
				replicationID:    rID,
			})

			break
		}
	}

	return peers
}

// getSyncPeers determines if scName passed has asynchronous peers in the passed in classLists.
// The clusterID and sID are the corresponding IDs for the passed in scName to find a match
func getSyncPeers(scName string, clusterID string, sID string, cls []classLists) []peerInfo {
	peers := []peerInfo{}

	for _, cl := range cls {
		for idx := range cl.sClasses {
			if cl.sClasses[idx].GetName() != scName {
				continue
			}

			if sID != cl.sClasses[idx].GetLabels()[StorageIDLabel] {
				break
			}

			// TODO: Check provisioner match?

			peers = append(peers, peerInfo{
				storageClassName: scName,
				storageIDs:       []string{sID},
				clusterIDs:       []string{clusterID, cl.clusterID},
			})

			break
		}
	}

	return peers
}

// findPeers finds all sync and async peers for the scName and cluster at the index startClsIdx of classLists,
// across other remaining elements post the startClsIdx in the classLists
func findPeers(cls []classLists, scName string, startClsIdx int, schedule string) ([]peerInfo, []peerInfo) {
	scIdx := 0
	for scIdx = range cls[startClsIdx].sClasses {
		if cls[startClsIdx].sClasses[scIdx].Name == scName {
			break
		}
	}

	if !util.HasLabel(cls[startClsIdx].sClasses[scIdx], StorageIDLabel) {
		return nil, nil
	}

	sID := cls[startClsIdx].sClasses[scIdx].Labels[StorageIDLabel]
	// TODO: Check if Sync is non-nil?
	syncPeers := getSyncPeers(scName, cls[startClsIdx].clusterID, sID, cls[startClsIdx+1:])

	asyncPeers := []peerInfo{}
	if schedule != "" {
		asyncPeers = getAsyncPeers(scName, cls[startClsIdx].clusterID, sID, cls[startClsIdx:], schedule)
	}

	return syncPeers, asyncPeers
}

// unionStorageClasses returns a union of all StorageClass names found in all clusters in the passed in classLists
func unionStorageClasses(cls []classLists) []string {
	allSCs := []string{}

	for clsIdx := range cls {
		for scIdx := range cls[clsIdx].sClasses {
			if slices.Contains(allSCs, cls[clsIdx].sClasses[scIdx].Name) {
				continue
			}

			allSCs = append(allSCs, cls[clsIdx].sClasses[scIdx].Name)
		}
	}

	return allSCs
}

// findAllPeers finds all PAIRs of peers in the passed in classLists. It does an exhaustive search for each scName in
// the prior index of classLists (starting at index 0) with all clusters from that index forward
func findAllPeers(cls []classLists, schedule string) ([]peerInfo, []peerInfo) {
	syncPeers := []peerInfo{}
	asyncPeers := []peerInfo{}

	if len(cls) <= 1 {
		return syncPeers, asyncPeers
	}

	sClassNames := unionStorageClasses(cls)

	for clsIdx := range cls[:len(cls)-1] {
		for scIdx := range cls[clsIdx].sClasses {
			if !slices.Contains(sClassNames, cls[clsIdx].sClasses[scIdx].Name) {
				continue
			}

			sPeers, aPeers := findPeers(cls, cls[clsIdx].sClasses[scIdx].Name, clsIdx, schedule)
			if len(sPeers) != 0 {
				syncPeers = append(syncPeers, sPeers...)
			}

			if len(aPeers) != 0 {
				asyncPeers = append(asyncPeers, aPeers...)
			}
		}
	}

	return syncPeers, asyncPeers
}

// pruneClassViews prunes existing views in mcvs, for classes that are not found in survivorClassNames
func pruneClassViews(
	m util.ManagedClusterViewGetter,
	log logr.Logger,
	clusterName string,
	survivorClassNames []string,
	mcvs *v1beta1.ManagedClusterViewList,
) error {
	for mcvIdx := range mcvs.Items {
		if slices.Contains(survivorClassNames, mcvs.Items[mcvIdx].Spec.Scope.Name) {
			continue
		}

		if err := m.DeleteManagedClusterView(clusterName, mcvs.Items[mcvIdx].Name, log); err != nil {
			return err
		}
	}

	return nil
}

func pruneVRClassViews(
	m util.ManagedClusterViewGetter,
	log logr.Logger,
	clusterName string,
	survivorClassNames []string,
) error {
	mcvList, err := m.ListVRClassMCVs(clusterName)
	if err != nil {
		return err
	}

	return pruneClassViews(m, log, clusterName, survivorClassNames, mcvList)
}

// getVRClassesFromCluster gets VolumeReplicationClasses that are claimed in the ManagedClusterInstance
func getVRClassesFromCluster(
	u *drpolicyUpdater,
	m util.ManagedClusterViewGetter,
	mc *util.ManagedClusterInstance,
	clusterName string,
) ([]*volrep.VolumeReplicationClass, error) {
	vrClasses := []*volrep.VolumeReplicationClass{}

	vrClassNames := mc.VolumeReplicationClassClaims()
	if len(vrClassNames) == 0 {
		return vrClasses, nil
	}

	annotations := make(map[string]string)
	annotations[AllDRPolicyAnnotation] = clusterName

	for _, vrcName := range vrClassNames {
		sClass, err := m.GetVRClassFromManagedCluster(vrcName, clusterName, annotations)
		if err != nil {
			return []*volrep.VolumeReplicationClass{}, err
		}

		vrClasses = append(vrClasses, sClass)
	}

	return vrClasses, pruneVRClassViews(m, u.log, clusterName, vrClassNames)
}

func pruneVSClassViews(
	m util.ManagedClusterViewGetter,
	log logr.Logger,
	clusterName string,
	survivorClassNames []string,
) error {
	mcvList, err := m.ListVSClassMCVs(clusterName)
	if err != nil {
		return err
	}

	return pruneClassViews(m, log, clusterName, survivorClassNames, mcvList)
}

// getVSClassesFromCluster gets VolumeSnapshotClasses that are claimed in the ManagedClusterInstance
func getVSClassesFromCluster(
	u *drpolicyUpdater,
	m util.ManagedClusterViewGetter,
	mc *util.ManagedClusterInstance,
	clusterName string,
) ([]*snapv1.VolumeSnapshotClass, error) {
	vsClasses := []*snapv1.VolumeSnapshotClass{}

	vsClassNames := mc.VolumeSnapshotClassClaims()
	if len(vsClassNames) == 0 {
		return vsClasses, nil
	}

	annotations := make(map[string]string)
	annotations[AllDRPolicyAnnotation] = clusterName

	for _, vscName := range vsClassNames {
		sClass, err := m.GetVSClassFromManagedCluster(vscName, clusterName, annotations)
		if err != nil {
			return []*snapv1.VolumeSnapshotClass{}, err
		}

		vsClasses = append(vsClasses, sClass)
	}

	return vsClasses, pruneVSClassViews(m, u.log, clusterName, vsClassNames)
}

func pruneSClassViews(
	m util.ManagedClusterViewGetter,
	log logr.Logger,
	clusterName string,
	survivorClassNames []string,
) error {
	mcvList, err := m.ListSClassMCVs(clusterName)
	if err != nil {
		return err
	}

	return pruneClassViews(m, log, clusterName, survivorClassNames, mcvList)
}

// getSClassesFromCluster gets StorageClasses that are claimed in the ManagedClusterInstance
func getSClassesFromCluster(
	u *drpolicyUpdater,
	m util.ManagedClusterViewGetter,
	mc *util.ManagedClusterInstance,
	clusterName string,
) ([]*storagev1.StorageClass, error) {
	sClasses := []*storagev1.StorageClass{}

	sClassNames := mc.StorageClassClaims()
	if len(sClassNames) == 0 {
		return sClasses, nil
	}

	annotations := make(map[string]string)
	annotations[AllDRPolicyAnnotation] = clusterName

	for _, scName := range sClassNames {
		sClass, err := m.GetSClassFromManagedCluster(scName, clusterName, annotations)
		if err != nil {
			return []*storagev1.StorageClass{}, err
		}

		sClasses = append(sClasses, sClass)
	}

	return sClasses, pruneSClassViews(m, u.log, clusterName, sClassNames)
}

// getClusterClasses inspects, using ManagedClusterView, the ManagedCluster claims for all storage related classes,
// and returns the set of classLists for the passed in clusters
func getClusterClasses(
	u *drpolicyUpdater,
	m util.ManagedClusterViewGetter,
	cluster string,
) (classLists, error) {
	mc, err := util.NewManagedClusterInstance(u.ctx, u.client, cluster)
	if err != nil {
		return classLists{}, err
	}

	clID, err := mc.ClusterID()
	if err != nil {
		return classLists{}, err
	}

	sClasses, err := getSClassesFromCluster(u, m, mc, cluster)
	if err != nil || len(sClasses) == 0 {
		return classLists{}, err
	}

	vsClasses, err := getVSClassesFromCluster(u, m, mc, cluster)
	if err != nil {
		return classLists{}, err
	}

	vrClasses, err := getVRClassesFromCluster(u, m, mc, cluster)
	if err != nil {
		return classLists{}, err
	}

	return classLists{
		clusterID: clID,
		sClasses:  sClasses,
		vrClasses: vrClasses,
		vsClasses: vsClasses,
	}, nil
}

// deleteViewsForClasses deletes all views created for classes for the passed in clusterName
func deleteViewsForClasses(m util.ManagedClusterViewGetter, log logr.Logger, clusterName string) error {
	if err := pruneSClassViews(m, log, clusterName, []string{}); err != nil {
		return err
	}

	if err := pruneVSClassViews(m, log, clusterName, []string{}); err != nil {
		return err
	}

	return pruneVRClassViews(m, log, clusterName, []string{})
}

// updatePeerClasses inspects required classes from the clusters that are part of the DRPolicy and updates DRPolicy
// status with the peer information across these clusters
func updatePeerClasses(u *drpolicyUpdater, m util.ManagedClusterViewGetter) error {
	cls := []classLists{}

	if len(u.object.Spec.DRClusters) <= 1 {
		return fmt.Errorf("cannot form peerClasses, insufficient clusters (%d) in policy", len(u.object.Spec.DRClusters))
	}

	for idx := range u.object.Spec.DRClusters {
		clusterClasses, err := getClusterClasses(u, m, u.object.Spec.DRClusters[idx])
		if err != nil {
			return err
		}

		if len(clusterClasses.sClasses) == 0 {
			continue
		}

		cls = append(cls, clusterClasses)
	}

	syncPeers, asyncPeers := findAllPeers(cls, u.object.Spec.SchedulingInterval)

	return updatePeerClassStatus(u, syncPeers, asyncPeers)
}
