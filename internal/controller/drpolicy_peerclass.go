// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"fmt"
	"slices"

	volrep "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	"github.com/go-logr/logr"
	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	"github.com/ramendr/ramen/internal/controller/util"
	storagev1 "k8s.io/api/storage/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// classLists contains [storage|snapshot|replication]classes from ManagedClusters with the required ramen storageID or,
// replicationID labels
type classLists struct {
	clusterID string
	sClasses  []*storagev1.StorageClass
	vsClasses []*snapv1.VolumeSnapshotClass
	vrClasses []*volrep.VolumeReplicationClass
}

// peerList contains a single peer relationship between a PAIR of clusters for a common storageClassName across
// these peers. This should directly translate to DRPolicy.Status.[Async|Sync] updates.
// NOTE: storageID discussed in comments relates to the value of the label "ramendr.openshift.io/storageid" for the
// respective class. replicationID in comments relates to the value of the label "ramendr.openshift.io/replicationid"
// for the respective class
type peerList struct {
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

// isAsyncVSClassPeer inspects provided pair of classLists for a matching VolumeSnapshotClass, that is linked to the
// StorageClass whose storageID is respectively sIDA or sIDB
func isAsyncVSClassPeer(clA, clB classLists, sIDA, sIDB string) bool {
	sidA := ""
	sidB := ""

	// No provisioner match as we can do cross provisioner VSC based protection
	for vscAidx := range clA.vsClasses {
		sidA = clA.vsClasses[vscAidx].GetLabels()[StorageIDLabel]
		if sidA == "" || sidA != sIDA {
			// reset on mismatch, to exit the loop with an empty string if there is no match
			sidA = ""

			continue
		}

		break
	}

	if sidA == "" {
		return false
	}

	for vscBidx := range clB.vsClasses {
		sidB = clB.vsClasses[vscBidx].GetLabels()[StorageIDLabel]
		if sidB == "" || sidB != sIDB {
			continue
		}

		return true
	}

	return false
}

// getVRID inspects VolumeReplicationClass in the passed in classLists at the specified index, and returns,
// - an empty string if the VRClass fails to match the passed in storageID or schedule, or
// - the value of replicationID on the VRClass
func getVRID(cl classLists, vrcIdx int, inSID string, schedule string) string {
	sID := cl.vrClasses[vrcIdx].GetLabels()[StorageIDLabel]
	if sID == "" || inSID != sID {
		return ""
	}

	if cl.vrClasses[vrcIdx].Spec.Parameters[VRClassScheduleKey] != schedule {
		return ""
	}

	rID := cl.vrClasses[vrcIdx].GetLabels()[VolumeReplicationIDLabel]

	return rID
}

// getAsyncVRClassPeer inspects if there is a common replicationID among the vrClasses in the passed in classLists,
// that relate to the corresponding storageIDs and schedule, and returns the replicationID or "" if there was no match
func getAsyncVRClassPeer(clA, clB classLists, sIDA, sIDB string, schedule string) string {
	for vrcAidx := range clA.vrClasses {
		ridA := getVRID(clA, vrcAidx, sIDA, schedule)
		if ridA == "" {
			continue
		}

		for vrcBidx := range clB.vrClasses {
			ridB := getVRID(clB, vrcBidx, sIDB, schedule)
			if ridB == "" {
				continue
			}

			if ridA != ridB { // TODO: Check provisioner match?
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
func getAsyncPeers(scName string, clusterID string, sID string, cls []classLists, schedule string) []peerList {
	peers := []peerList{}

	for _, cl := range cls[1:] {
		for scIdx := range cl.sClasses {
			if cl.sClasses[scIdx].GetName() != scName {
				continue
			}

			sIDcl := cl.sClasses[scIdx].GetLabels()[StorageIDLabel]
			if sID == sIDcl {
				break
			}

			rID := getAsyncVRClassPeer(cls[0], cl, sID, sIDcl, schedule)
			if rID == "" {
				if !isAsyncVSClassPeer(cls[0], cl, sID, sIDcl) {
					continue
				}
			}

			peers = append(peers, peerList{
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
func getSyncPeers(scName string, clusterID string, sID string, cls []classLists) []peerList {
	peers := []peerList{}

	for _, cl := range cls {
		for idx := range cl.sClasses {
			if cl.sClasses[idx].GetName() != scName {
				continue
			}

			if sID != cl.sClasses[idx].GetLabels()[StorageIDLabel] {
				break
			}

			// TODO: Check provisioner match?

			peers = append(peers, peerList{
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
func findPeers(cls []classLists, scName string, startClsIdx int, schedule string) ([]peerList, []peerList) {
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

	asyncPeers := []peerList{}
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
func findAllPeers(cls []classLists, schedule string) ([]peerList, []peerList) {
	syncPeers := []peerList{}
	asyncPeers := []peerList{}

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

// updatePeerClassStatus updates the DRPolicy.Status.[Async|Sync] peer lists based on passed in peerList values
func updatePeerClassStatus(u *drpolicyUpdater, syncPeers, asyncPeers []peerList) {
}

// getClusterClasses inspects, using ManagedClusterView, the ManagedCluster claims for all storage related classes,
// and returns the set of classLists for the passed in clusters
func getClusterClasses(
	ctx context.Context,
	log logr.Logger,
	client client.Client,
	cluster string,
) (classLists, error) {
	return classLists{}, nil
}

// updatePeerClasses inspects required classes from the clusters that are part of the DRPolicy and updates DRPolicy
// status with the peer information across these clusters
func updatePeerClasses(u *drpolicyUpdater) error {
	cls := []classLists{}

	if len(u.object.Spec.DRClusters) <= 1 {
		return fmt.Errorf("cannot form peerClasses, insufficient clusters (%d) in policy", len(u.object.Spec.DRClusters))
	}

	for idx := range u.object.Spec.DRClusters {
		clusterClasses, err := getClusterClasses(u.ctx, u.log, u.client, u.object.Spec.DRClusters[idx])
		if err != nil {
			return err
		}

		cls = append(cls, clusterClasses)
	}

	syncPeers, asyncPeers := findAllPeers(cls, u.object.Spec.SchedulingInterval)

	updatePeerClassStatus(u, syncPeers, asyncPeers)

	return nil
}
