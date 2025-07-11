// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	volrep "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	groupsnapv1beta1 "github.com/red-hat-storage/external-snapshotter/client/v8/apis/volumegroupsnapshot/v1beta1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// nolint:dupl
var _ = Describe("updatePeerClassesInternal", func() {
	DescribeTable("findAllPeers",
		func(
			cls []classLists,
			schedule string,
			syncPeers []peerInfo,
			asyncPeers []peerInfo,
		) {
			sPeers, aPeers := findAllPeers(cls, schedule)
			Expect(sPeers).Should(HaveExactElements(syncPeers))
			Expect(aPeers).Should(HaveExactElements(asyncPeers))
		},
		Entry("Empty classLists", []classLists{}, "1m", []peerInfo{}, []peerInfo{}),
		Entry("Not enough clusters",
			[]classLists{
				{
					clusterID: "cl-1",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "identical",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
				},
			},
			"1m",
			[]peerInfo{},
			[]peerInfo{},
		),
		Entry("Single sync peer",
			[]classLists{
				{
					clusterID: "cl-1",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "identical",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
				},
				{
					clusterID: "cl-2",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "identical",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
				},
			},
			"1m",
			[]peerInfo{
				{
					replicationID:    "",
					storageIDs:       []string{"identical"},
					storageClassName: "sc1",
					clusterIDs:       []string{"cl-1", "cl-2"},
				},
			},
			[]peerInfo{},
		),
		Entry("Multiple sync peer",
			[]classLists{
				{
					clusterID: "cl-1",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "identical1",
								},
							},
							Provisioner: "sample.csi.com",
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc2",
								Labels: map[string]string{
									StorageIDLabel: "identical2",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
				},
				{
					clusterID: "cl-2",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "identical1",
								},
							},
							Provisioner: "sample.csi.com",
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc2",
								Labels: map[string]string{
									StorageIDLabel: "identical2",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
				},
			},
			"1m",
			[]peerInfo{
				{
					replicationID:    "",
					storageIDs:       []string{"identical1"},
					storageClassName: "sc1",
					clusterIDs:       []string{"cl-1", "cl-2"},
				},
				{
					replicationID:    "",
					storageIDs:       []string{"identical2"},
					storageClassName: "sc2",
					clusterIDs:       []string{"cl-1", "cl-2"},
				},
			},
			[]peerInfo{},
		),
		Entry("Single async peer, missing related [VR|VS]Classes",
			[]classLists{
				{
					clusterID: "cl-1",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
				},
				{
					clusterID: "cl-2",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
				},
			},
			"1m",
			[]peerInfo{},
			[]peerInfo{},
		),
		Entry("Single async peer, having unrelated VRClasses",
			[]classLists{
				{
					clusterID: "cl-1",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vrClasses: []*volrep.VolumeReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-1-sID-mismatch",
									ReplicationIDLabel: "cl-1-2-rID",
								},
							},
							Spec: volrep.VolumeReplicationClassSpec{
								Provisioner: "sample.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
				},
				{
					clusterID: "cl-2",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vrClasses: []*volrep.VolumeReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-2-sID-mismatch",
									ReplicationIDLabel: "cl-1-2-rID",
								},
							},
							Spec: volrep.VolumeReplicationClassSpec{
								Provisioner: "sample.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
				},
			},
			"1m",
			[]peerInfo{},
			[]peerInfo{},
		),
		Entry("Single async peer, having unrelated VSClasses",
			[]classLists{
				{
					clusterID: "cl-1",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vsClasses: []*snapv1.VolumeSnapshotClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vsc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID-mismatch",
								},
							},
							Driver: "sample.csi.com",
						},
					},
				},
				{
					clusterID: "cl-2",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vsClasses: []*snapv1.VolumeSnapshotClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vsc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID-mismatch",
								},
							},
							Driver: "sample.csi.com",
						},
					},
				},
			},
			"1m",
			[]peerInfo{},
			[]peerInfo{},
		),
		Entry("Single async peer, having unrelated [VR|VS]Classes",
			[]classLists{
				{
					clusterID: "cl-1",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vrClasses: []*volrep.VolumeReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-1-sID-mismatch",
									ReplicationIDLabel: "cl-1-2-rID",
								},
							},
							Spec: volrep.VolumeReplicationClassSpec{
								Provisioner: "sample.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
					vsClasses: []*snapv1.VolumeSnapshotClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vsc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID-mismatch",
								},
							},
							Driver: "sample.csi.com",
						},
					},
				},
				{
					clusterID: "cl-2",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vrClasses: []*volrep.VolumeReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-2-sID-mismatch",
									ReplicationIDLabel: "cl-1-2-rID",
								},
							},
							Spec: volrep.VolumeReplicationClassSpec{
								Provisioner: "sample.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
					vsClasses: []*snapv1.VolumeSnapshotClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vsc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID-mismatch",
								},
							},
							Driver: "sample.csi.com",
						},
					},
				},
			},
			"1m",
			[]peerInfo{},
			[]peerInfo{},
		),
		Entry("Single async peer, having related VRClasses",
			[]classLists{
				{
					clusterID: "cl-1",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vrClasses: []*volrep.VolumeReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-1-sID",
									ReplicationIDLabel: "cl-1-2-rID",
								},
							},
							Spec: volrep.VolumeReplicationClassSpec{
								Provisioner: "sample.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
				},
				{
					clusterID: "cl-2",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vrClasses: []*volrep.VolumeReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-2-sID",
									ReplicationIDLabel: "cl-1-2-rID",
								},
							},
							Spec: volrep.VolumeReplicationClassSpec{
								Provisioner: "sample.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
				},
			},
			"1m",
			[]peerInfo{},
			[]peerInfo{
				{
					replicationID:    "cl-1-2-rID",
					storageIDs:       []string{"cl-1-sID", "cl-2-sID"},
					storageClassName: "sc1",
					clusterIDs:       []string{"cl-1", "cl-2"},
				},
			},
		),
		Entry("Single async peer, having related VSClasses",
			[]classLists{
				{
					clusterID: "cl-1",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vsClasses: []*snapv1.VolumeSnapshotClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vsc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID",
								},
							},
							Driver: "sample.csi.com",
						},
					},
				},
				{
					clusterID: "cl-2",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vsClasses: []*snapv1.VolumeSnapshotClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vsc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID",
								},
							},
							Driver: "sample.csi.com",
						},
					},
				},
			},
			"1m",
			[]peerInfo{},
			[]peerInfo{
				{
					replicationID:    "",
					storageIDs:       []string{"cl-1-sID", "cl-2-sID"},
					storageClassName: "sc1",
					clusterIDs:       []string{"cl-1", "cl-2"},
				},
			},
		),
		Entry("Single async peer, having related VSClasses on one but not the other cluster",
			[]classLists{
				{
					clusterID: "cl-1",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vsClasses: []*snapv1.VolumeSnapshotClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vsc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID",
								},
							},
							Driver: "sample.csi.com",
						},
					},
				},
				{
					clusterID: "cl-2",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vsClasses: []*snapv1.VolumeSnapshotClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vsc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID-mismatch",
								},
							},
							Driver: "sample.csi.com",
						},
					},
				},
			},
			"1m",
			[]peerInfo{},
			[]peerInfo{},
		),
		Entry("Single async peer, with VRClasses missing rID",
			[]classLists{
				{
					clusterID: "cl-1",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vrClasses: []*volrep.VolumeReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vrc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID",
								},
							},
							Spec: volrep.VolumeReplicationClassSpec{
								Provisioner: "sample.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
				},
				{
					clusterID: "cl-2",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vrClasses: []*volrep.VolumeReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-2-sID",
									ReplicationIDLabel: "cl-1-2-rID",
								},
							},
							Spec: volrep.VolumeReplicationClassSpec{
								Provisioner: "sample.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
				},
			},
			"1m",
			[]peerInfo{},
			[]peerInfo{},
		),
		Entry("Single async peer, with VRClasses mismatching schedule",
			[]classLists{
				{
					clusterID: "cl-1",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vrClasses: []*volrep.VolumeReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-1-sID",
									ReplicationIDLabel: "cl-1-2-rID",
								},
							},
							Spec: volrep.VolumeReplicationClassSpec{
								Provisioner: "sample.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "2m",
								},
							},
						},
					},
				},
				{
					clusterID: "cl-2",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vrClasses: []*volrep.VolumeReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-2-sID",
									ReplicationIDLabel: "cl-1-2-rID",
								},
							},
							Spec: volrep.VolumeReplicationClassSpec{
								Provisioner: "sample.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
				},
			},
			"1m",
			[]peerInfo{},
			[]peerInfo{},
		),
		Entry("Single async peer, having related VRClasses with mismatching rIDs",
			[]classLists{
				{
					clusterID: "cl-1",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vrClasses: []*volrep.VolumeReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-1-sID",
									ReplicationIDLabel: "cl-1-2-rID",
								},
							},
							Spec: volrep.VolumeReplicationClassSpec{
								Provisioner: "sample.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
				},
				{
					clusterID: "cl-2",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vrClasses: []*volrep.VolumeReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-2-sID",
									ReplicationIDLabel: "cl-1-2-rID-mismatch",
								},
							},
							Spec: volrep.VolumeReplicationClassSpec{
								Provisioner: "sample.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
				},
			},
			"1m",
			[]peerInfo{},
			[]peerInfo{},
		),
		Entry("Multiple async peer, having related [VR|VS]Classes",
			[]classLists{
				{
					clusterID: "cl-1",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID1",
								},
							},
							Provisioner: "sample.csi.com",
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc2",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID2",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vsClasses: []*snapv1.VolumeSnapshotClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vsc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID1",
								},
							},
							Driver: "sample.csi.com",
						},
					},
					vrClasses: []*volrep.VolumeReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-1-sID2",
									ReplicationIDLabel: "cl-1-2-rID",
								},
							},
							Spec: volrep.VolumeReplicationClassSpec{
								Provisioner: "sample.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
				},
				{
					clusterID: "cl-2",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID1",
								},
							},
							Provisioner: "sample.csi.com",
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc2",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID2",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vsClasses: []*snapv1.VolumeSnapshotClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vsc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID1",
								},
							},
							Driver: "sample.csi.com",
						},
					},
					vrClasses: []*volrep.VolumeReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-2-sID2",
									ReplicationIDLabel: "cl-1-2-rID",
								},
							},
							Spec: volrep.VolumeReplicationClassSpec{
								Provisioner: "sample.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
				},
			},
			"1m",
			[]peerInfo{},
			[]peerInfo{
				{
					replicationID:    "",
					storageIDs:       []string{"cl-1-sID1", "cl-2-sID1"},
					storageClassName: "sc1",
					clusterIDs:       []string{"cl-1", "cl-2"},
				},
				{
					replicationID:    "cl-1-2-rID",
					storageIDs:       []string{"cl-1-sID2", "cl-2-sID2"},
					storageClassName: "sc2",
					clusterIDs:       []string{"cl-1", "cl-2"},
				},
			},
		),
		Entry("Multiple sync and async peer, having Classes",
			[]classLists{
				{
					clusterID: "cl-1",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-[1|2]-sID1",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vrClasses: []*volrep.VolumeReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-[1|2]-sID1",
									ReplicationIDLabel: "cl-[1|2]-[3|4]-rID",
								},
							},
							Spec: volrep.VolumeReplicationClassSpec{
								Provisioner: "sample.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
				},
				{
					clusterID: "cl-2",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-[1|2]-sID1",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vrClasses: []*volrep.VolumeReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-[1|2]-sID1",
									ReplicationIDLabel: "cl-[1|2]-[3|4]-rID",
								},
							},
							Spec: volrep.VolumeReplicationClassSpec{
								Provisioner: "sample.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
				},
				{
					clusterID: "cl-3",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-[3|4]-sID1",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vrClasses: []*volrep.VolumeReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-[3|4]-sID1",
									ReplicationIDLabel: "cl-[1|2]-[3|4]-rID",
								},
							},
							Spec: volrep.VolumeReplicationClassSpec{
								Provisioner: "sample.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
				},
				{
					clusterID: "cl-4",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-[3|4]-sID1",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vrClasses: []*volrep.VolumeReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-[3|4]-sID1",
									ReplicationIDLabel: "cl-[1|2]-[3|4]-rID",
								},
							},
							Spec: volrep.VolumeReplicationClassSpec{
								Provisioner: "sample.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
				},
			},
			"1m",
			[]peerInfo{
				{
					replicationID:    "",
					storageIDs:       []string{"cl-[1|2]-sID1"},
					storageClassName: "sc1",
					clusterIDs:       []string{"cl-1", "cl-2"},
				},
				{
					replicationID:    "",
					storageIDs:       []string{"cl-[3|4]-sID1"},
					storageClassName: "sc1",
					clusterIDs:       []string{"cl-3", "cl-4"},
				},
			},
			[]peerInfo{
				{
					replicationID:    "cl-[1|2]-[3|4]-rID",
					storageIDs:       []string{"cl-[1|2]-sID1", "cl-[3|4]-sID1"},
					storageClassName: "sc1",
					clusterIDs:       []string{"cl-1", "cl-3"},
				},
				{
					replicationID:    "cl-[1|2]-[3|4]-rID",
					storageIDs:       []string{"cl-[1|2]-sID1", "cl-[3|4]-sID1"},
					storageClassName: "sc1",
					clusterIDs:       []string{"cl-1", "cl-4"},
				},
				{
					replicationID:    "cl-[1|2]-[3|4]-rID",
					storageIDs:       []string{"cl-[1|2]-sID1", "cl-[3|4]-sID1"},
					storageClassName: "sc1",
					clusterIDs:       []string{"cl-2", "cl-3"},
				},
				{
					replicationID:    "cl-[1|2]-[3|4]-rID",
					storageIDs:       []string{"cl-[1|2]-sID1", "cl-[3|4]-sID1"},
					storageClassName: "sc1",
					clusterIDs:       []string{"cl-2", "cl-4"},
				},
			},
		),
		Entry("Multiple async peer, having related [VR|VS]Classes with same storageIDs",
			[]classLists{
				{
					clusterID: "cl-1",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID1",
								},
							},
							Provisioner: "sample1.csi.com",
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc2",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID1",
								},
							},
							Provisioner: "sample2.csi.com",
						},
					},
					vsClasses: []*snapv1.VolumeSnapshotClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vsc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID1",
								},
							},
							Driver: "sample1.csi.com",
						},
					},
					vrClasses: []*volrep.VolumeReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-1-sID1",
									ReplicationIDLabel: "cl-1-2-rID",
								},
							},
							Spec: volrep.VolumeReplicationClassSpec{
								Provisioner: "sample2.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
				},
				{
					clusterID: "cl-2",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID1",
								},
							},
							Provisioner: "sample1.csi.com",
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc2",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID1",
								},
							},
							Provisioner: "sample2.csi.com",
						},
					},
					vsClasses: []*snapv1.VolumeSnapshotClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vsc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID1",
								},
							},
							Driver: "sample1.csi.com",
						},
					},
					vrClasses: []*volrep.VolumeReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-2-sID1",
									ReplicationIDLabel: "cl-1-2-rID",
								},
							},
							Spec: volrep.VolumeReplicationClassSpec{
								Provisioner: "sample2.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
				},
			},
			"1m",
			[]peerInfo{},
			[]peerInfo{
				{
					replicationID:    "",
					storageIDs:       []string{"cl-1-sID1", "cl-2-sID1"},
					storageClassName: "sc1",
					clusterIDs:       []string{"cl-1", "cl-2"},
				},
				{
					replicationID:    "cl-1-2-rID",
					storageIDs:       []string{"cl-1-sID1", "cl-2-sID1"},
					storageClassName: "sc2",
					clusterIDs:       []string{"cl-1", "cl-2"},
				},
			},
		),
		Entry("Multiple async peer, having mismatching VSClass Drivers with storageIDs matching SCs",
			[]classLists{
				{
					clusterID: "cl-1",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID1",
								},
							},
							Provisioner: "sample1.csi.com",
						},
					},
					vsClasses: []*snapv1.VolumeSnapshotClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vsc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID1",
								},
							},
							Driver: "sample2.csi.com",
						},
					},
				},
				{
					clusterID: "cl-2",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID1",
								},
							},
							Provisioner: "sample1.csi.com",
						},
					},
					vsClasses: []*snapv1.VolumeSnapshotClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vsc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID1",
								},
							},
							Driver: "sample2.csi.com",
						},
					},
				},
			},
			"1m",
			[]peerInfo{},
			[]peerInfo{},
		),
		Entry("Single async peer, having related VGRC where grouping is true",
			[]classLists{
				{
					clusterID: "cl-1",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID1",
								},
							},
							Provisioner: "sample1.csi.com",
						},
					},
					vrClasses: []*volrep.VolumeReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-1-sID1",
									ReplicationIDLabel: "cl-1-2-rID",
								},
							},
							Spec: volrep.VolumeReplicationClassSpec{
								Provisioner: "sample1.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
					vgrClasses: []*volrep.VolumeGroupReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vgrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-1-sID1",
									ReplicationIDLabel: "cl-1-2-rID",
								},
							},
							Spec: volrep.VolumeGroupReplicationClassSpec{
								Provisioner: "sample1.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
				},
				{
					clusterID: "cl-2",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID1",
								},
							},
							Provisioner: "sample1.csi.com",
						},
					},
					vrClasses: []*volrep.VolumeReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-2-sID1",
									ReplicationIDLabel: "cl-1-2-rID",
								},
							},
							Spec: volrep.VolumeReplicationClassSpec{
								Provisioner: "sample1.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
					vgrClasses: []*volrep.VolumeGroupReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vgrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-2-sID1",
									ReplicationIDLabel: "cl-1-2-rID",
								},
							},
							Spec: volrep.VolumeGroupReplicationClassSpec{
								Provisioner: "sample1.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
				},
			},
			"1m",
			[]peerInfo{},
			[]peerInfo{
				{
					replicationID:    "cl-1-2-rID",
					storageIDs:       []string{"cl-1-sID1", "cl-2-sID1"},
					storageClassName: "sc1",
					clusterIDs:       []string{"cl-1", "cl-2"},
					grouping:         true,
				},
			},
		),
		Entry("Single async peer, having related VGSC where grouping is true",
			[]classLists{
				{
					clusterID: "cl-1",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID1",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vgsClasses: []*groupsnapv1beta1.VolumeGroupSnapshotClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vgsc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID1",
								},
							},
							Driver: "sample.csi.com",
						},
					},
				},
				{
					clusterID: "cl-2",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID1",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vgsClasses: []*groupsnapv1beta1.VolumeGroupSnapshotClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vgsc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID1",
								},
							},
							Driver: "sample.csi.com",
						},
					},
				},
			},
			"1m",
			[]peerInfo{},
			[]peerInfo{
				{
					storageIDs:       []string{"cl-1-sID1", "cl-2-sID1"},
					storageClassName: "sc1",
					clusterIDs:       []string{"cl-1", "cl-2"},
					grouping:         true,
				},
			},
		),
		Entry("Single async peer, having different rID in VGRC and VRC where grouping is false",
			[]classLists{
				{
					clusterID: "cl-1",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID1",
								},
							},
							Provisioner: "sample1.csi.com",
						},
					},
					vrClasses: []*volrep.VolumeReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-1-sID1",
									ReplicationIDLabel: "cl-1-2-rID",
								},
							},
							Spec: volrep.VolumeReplicationClassSpec{
								Provisioner: "sample1.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
					vgrClasses: []*volrep.VolumeGroupReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vgrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-1-sID1",
									ReplicationIDLabel: "cl-1-2-rID-mismtach",
								},
							},
							Spec: volrep.VolumeGroupReplicationClassSpec{
								Provisioner: "sample1.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
				},
				{
					clusterID: "cl-2",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID1",
								},
							},
							Provisioner: "sample1.csi.com",
						},
					},
					vrClasses: []*volrep.VolumeReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-2-sID1",
									ReplicationIDLabel: "cl-1-2-rID",
								},
							},
							Spec: volrep.VolumeReplicationClassSpec{
								Provisioner: "sample1.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
					vgrClasses: []*volrep.VolumeGroupReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vgrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-2-sID1",
									ReplicationIDLabel: "cl-1-2-rID-mismatch",
								},
							},
							Spec: volrep.VolumeGroupReplicationClassSpec{
								Provisioner: "sample1.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
				},
			},
			"1m",
			[]peerInfo{},
			[]peerInfo{
				{
					replicationID:    "cl-1-2-rID",
					storageIDs:       []string{"cl-1-sID1", "cl-2-sID1"},
					storageClassName: "sc1",
					clusterIDs:       []string{"cl-1", "cl-2"},
					grouping:         false,
				},
			},
		),
		Entry("Multiple async peer, having related [VGR|VGS]Classes where grouping is true",
			[]classLists{
				{
					clusterID: "cl-1",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID1",
								},
							},
							Provisioner: "sample.csi.com",
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc2",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID2",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vsClasses: []*snapv1.VolumeSnapshotClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vsc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID1",
								},
							},
							Driver: "sample.csi.com",
						},
					},
					vgsClasses: []*groupsnapv1beta1.VolumeGroupSnapshotClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vgsc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-1-sID1",
								},
							},
							Driver: "sample.csi.com",
						},
					},
					vrClasses: []*volrep.VolumeReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-1-sID2",
									ReplicationIDLabel: "cl-1-2-rID",
								},
							},
							Spec: volrep.VolumeReplicationClassSpec{
								Provisioner: "sample.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
					vgrClasses: []*volrep.VolumeGroupReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vgrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-1-sID2",
									ReplicationIDLabel: "cl-1-2-rID",
								},
							},
							Spec: volrep.VolumeGroupReplicationClassSpec{
								Provisioner: "sample.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
				},
				{
					clusterID: "cl-2",
					sClasses: []*storagev1.StorageClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID1",
								},
							},
							Provisioner: "sample.csi.com",
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "sc2",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID2",
								},
							},
							Provisioner: "sample.csi.com",
						},
					},
					vsClasses: []*snapv1.VolumeSnapshotClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vsc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID1",
								},
							},
							Driver: "sample.csi.com",
						},
					},
					vgsClasses: []*groupsnapv1beta1.VolumeGroupSnapshotClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vgsc1",
								Labels: map[string]string{
									StorageIDLabel: "cl-2-sID1",
								},
							},
							Driver: "sample.csi.com",
						},
					},
					vrClasses: []*volrep.VolumeReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vrc1",
								Labels: map[string]string{
									StorageIDLabel:     "cl-2-sID2",
									ReplicationIDLabel: "cl-1-2-rID",
								},
							},
							Spec: volrep.VolumeReplicationClassSpec{
								Provisioner: "sample.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
					vgrClasses: []*volrep.VolumeGroupReplicationClass{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "vgrc2",
								Labels: map[string]string{
									StorageIDLabel:     "cl-2-sID2",
									ReplicationIDLabel: "cl-1-2-rID",
								},
							},
							Spec: volrep.VolumeGroupReplicationClassSpec{
								Provisioner: "sample.csi.com",
								Parameters: map[string]string{
									ReplicationClassScheduleKey: "1m",
								},
							},
						},
					},
				},
			},
			"1m",
			[]peerInfo{},
			[]peerInfo{
				{
					replicationID:    "",
					storageIDs:       []string{"cl-1-sID1", "cl-2-sID1"},
					storageClassName: "sc1",
					clusterIDs:       []string{"cl-1", "cl-2"},
					grouping:         true,
				},
				{
					replicationID:    "cl-1-2-rID",
					storageIDs:       []string{"cl-1-sID2", "cl-2-sID2"},
					storageClassName: "sc2",
					clusterIDs:       []string{"cl-1", "cl-2"},
					grouping:         true,
				},
			},
		),
	)
})
