// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

// white box testing desired for Recipe/KubeObject conversions
package controllers //nolint:testpackage

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	Recipe "github.com/ramendr/recipe/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/kubeobjects"
)

var _ = Describe("VRG_KubeObjectProtection", func() {
	const namespaceName = "my-ns"

	var (
		hook  *Recipe.Hook
		group *Recipe.Group
	)

	BeforeEach(func() {
		duration := 30

		hook = &Recipe.Hook{
			Namespace: namespaceName,
			Name:      "hook-single",
			Type:      "exec",
			LabelSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"myapp": "testapp",
				},
			},
			SinglePodOnly: false,
			Ops: []*Recipe.Operation{
				{
					Name:      "checkpoint",
					Container: "main",
					Timeout:   duration,
					Command:   "bash /scripts/checkpoint.sh",
				},
			},
			Chks:      []*Recipe.Check{},
			Essential: new(bool),
		}

		group = &Recipe.Group{
			Name:                  "test-group",
			BackupRef:             "test-backup-ref",
			Type:                  "resource",
			IncludedNamespaces:    []string{namespaceName},
			IncludedResourceTypes: []string{"deployment", "replicaset"},
			ExcludedResourceTypes: nil,
			LabelSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "test",
						Operator: metav1.LabelSelectorOpNotIn,
						Values:   []string{"empty-on-backup notin", "ignore-on-backup"},
					},
				},
			},
		}
	})

	Context("Conversion", func() {
		It("Hook to CaptureSpec", func() {
			targetCaptureSpec := &kubeobjects.CaptureSpec{
				Name: hook.Name + "-" + hook.Ops[0].Name,
				Spec: kubeobjects.Spec{
					KubeResourcesSpec: kubeobjects.KubeResourcesSpec{
						IncludedNamespaces: []string{namespaceName},
						IncludedResources:  []string{"pod"},
						ExcludedResources:  []string{},
						Hook: kubeobjects.HookSpec{
							Name:          hook.Name,
							Namespace:     namespaceName,
							Type:          hook.Type,
							LabelSelector: hook.LabelSelector,
							Essential:     hook.Essential,
							Op: kubeobjects.Operation{
								Name:      hook.Ops[0].Name,
								Command:   hook.Ops[0].Command,
								Container: hook.Ops[0].Container,
								Timeout:   hook.Ops[0].Timeout,
								OnError:   hook.Ops[0].OnError,
							},
						},
						IsHook: true,
					},
					LabelSelector:           hook.LabelSelector,
					IncludeClusterResources: new(bool),
				},
			}
			converted, err := convertRecipeHookToCaptureSpec(*hook, hook.Ops[0].Name)

			Expect(err).To(BeNil())
			Expect(converted).To(Equal(targetCaptureSpec))
		})

		It("Hook to RecoverSpec", func() {
			targetRecoverSpec := &kubeobjects.RecoverSpec{
				Spec: kubeobjects.Spec{
					KubeResourcesSpec: kubeobjects.KubeResourcesSpec{
						IncludedNamespaces: []string{namespaceName},
						IncludedResources:  []string{"pod"},
						ExcludedResources:  []string{},
						Hook: kubeobjects.HookSpec{
							Name:          hook.Name,
							Type:          hook.Type,
							Namespace:     namespaceName,
							LabelSelector: hook.LabelSelector,
							Essential:     hook.Essential,
							Op: kubeobjects.Operation{
								Name:      hook.Ops[0].Name,
								Command:   hook.Ops[0].Command,
								Container: hook.Ops[0].Container,
								Timeout:   hook.Ops[0].Timeout,
								OnError:   hook.Ops[0].OnError,
							},
						},
						IsHook: true,
					},
					LabelSelector:           hook.LabelSelector,
					IncludeClusterResources: new(bool),
				},
			}
			converted, err := convertRecipeHookToRecoverSpec(*hook, hook.Ops[0].Name)

			Expect(err).To(BeNil())
			Expect(converted).To(Equal(targetRecoverSpec))
		})

		It("Group to CaptureSpec", func() {
			targetCaptureSpec := &kubeobjects.CaptureSpec{
				Name: group.Name,
				Spec: kubeobjects.Spec{
					KubeResourcesSpec: kubeobjects.KubeResourcesSpec{
						IncludedNamespaces: group.IncludedNamespaces,
						IncludedResources:  group.IncludedResourceTypes,
						ExcludedResources:  group.ExcludedResourceTypes,
					},
					LabelSelector:           group.LabelSelector,
					IncludeClusterResources: group.IncludeClusterResources,
					OrLabelSelectors:        []*metav1.LabelSelector{},
				},
			}
			converted, err := convertRecipeGroupToCaptureSpec(*group)

			Expect(err).To(BeNil())
			Expect(converted).To(Equal(targetCaptureSpec))
		})

		It("Group to RecoverSpec", func() {
			targetRecoverSpec := &kubeobjects.RecoverSpec{
				BackupName: group.BackupRef,
				Spec: kubeobjects.Spec{
					KubeResourcesSpec: kubeobjects.KubeResourcesSpec{
						IncludedNamespaces: group.IncludedNamespaces,
						IncludedResources:  group.IncludedResourceTypes,
						ExcludedResources:  group.ExcludedResourceTypes,
					},
					LabelSelector:           group.LabelSelector,
					IncludeClusterResources: group.IncludeClusterResources,
					OrLabelSelectors:        []*metav1.LabelSelector{},
				},
			}
			converted, err := convertRecipeGroupToRecoverSpec(*group)

			Expect(err).To(BeNil())
			Expect(converted).To(Equal(targetRecoverSpec))
		})
	})
})

func vrgCond(typ string, status metav1.ConditionStatus, reason string) metav1.Condition {
	return metav1.Condition{
		Type:               typ,
		Status:             status,
		Reason:             reason,
		Message:            reason,
		ObservedGeneration: 1,
		LastTransitionTime: metav1.Now(),
	}
}

func pvcWithReplicationHealthy(namespace, name string, status metav1.ConditionStatus, reason, message string,
) rmn.ProtectedPVC {
	return rmn.ProtectedPVC{
		Namespace: namespace,
		Name:      name,
		Conditions: []metav1.Condition{{
			Type:               VRGConditionTypeReplicationHealthy,
			Status:             status,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: 1,
		}},
	}
}

func vrgInstanceWithPVCs(pvcs ...rmn.ProtectedPVC) *VRGInstance {
	return &VRGInstance{
		instance: &rmn.VolumeReplicationGroup{
			ObjectMeta: metav1.ObjectMeta{Generation: 1},
			Status: rmn.VolumeReplicationGroupStatus{
				ProtectedPVCs: pvcs,
			},
		},
	}
}

func healthyPrimaryVRG() *rmn.VolumeReplicationGroup {
	const generation int64 = 1

	return &rmn.VolumeReplicationGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "app-vrg", Namespace: "app-ns", Generation: generation},
		Spec: rmn.VolumeReplicationGroupSpec{
			ReplicationState: rmn.Primary,
		},
		Status: rmn.VolumeReplicationGroupStatus{
			ObservedGeneration: generation,
			State:              rmn.PrimaryState,
			Conditions: []metav1.Condition{
				vrgCond(VRGConditionTypeClusterDataReady, metav1.ConditionTrue, VRGConditionReasonReady),
				vrgCond(VRGConditionTypeDataReady, metav1.ConditionTrue, VRGConditionReasonReady),
				vrgCond(VRGConditionTypeDataProtected, metav1.ConditionTrue, VRGConditionReasonDataProtected),
				vrgCond(VRGConditionTypeNoClusterDataConflict, metav1.ConditionTrue,
					VRGConditionReasonNoConflictDetected),
				vrgCond(VRGConditionTypeClusterDataProtected, metav1.ConditionTrue, VRGConditionReasonUploaded),
			},
		},
	}
}

var _ = Describe("aggregateVolRepReplicationHealthyCondition", func() {
	It("returns nil when no ProtectedPVC reports ReplicationHealthy", func() {
		v := vrgInstanceWithPVCs(rmn.ProtectedPVC{Name: "pvc-a", Namespace: "ns"})
		Expect(v.aggregateVolRepReplicationHealthyCondition()).To(BeNil())
	})

	It("returns True when every reported PVC is replicating", func() {
		v := vrgInstanceWithPVCs(
			pvcWithReplicationHealthy("ns-a", "pvc-a", metav1.ConditionTrue, VRGConditionReasonReady, "replicating"),
			pvcWithReplicationHealthy("ns-b", "pvc-b", metav1.ConditionTrue, VRGConditionReasonReady, "replicating"),
		)

		cond := v.aggregateVolRepReplicationHealthyCondition()
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(VRGConditionReasonReady))
	})

	It("returns False when any PVC is not replicating", func() {
		v := vrgInstanceWithPVCs(
			pvcWithReplicationHealthy("ns-a", "pvc-a", metav1.ConditionTrue, VRGConditionReasonReady, "replicating"),
			pvcWithReplicationHealthy("ns-b", "pvc-b", metav1.ConditionFalse, VRGConditionReasonError, "mirror down"),
		)

		cond := v.aggregateVolRepReplicationHealthyCondition()
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(VRGConditionReasonError))
		Expect(cond.Message).To(ContainSubstring("pvc-b"))
		Expect(cond.Message).To(ContainSubstring("mirror down"))
	})

	It("prefers False over Unknown", func() {
		v := vrgInstanceWithPVCs(
			pvcWithReplicationHealthy("ns-a", "pvc-a", metav1.ConditionUnknown, VRGConditionReasonErrorUnknown, "unknown"),
			pvcWithReplicationHealthy("ns-b", "pvc-b", metav1.ConditionFalse, VRGConditionReasonError, "mirror down"),
		)

		cond := v.aggregateVolRepReplicationHealthyCondition()
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(VRGConditionReasonError))
	})

	It("skips VolSync PVCs", func() {
		volsyncPVC := pvcWithReplicationHealthy("ns-vs", "pvc-vs", metav1.ConditionFalse, VRGConditionReasonError, "ignored")
		volsyncPVC.ProtectedByVolSync = true
		v := vrgInstanceWithPVCs(volsyncPVC)
		Expect(v.aggregateVolRepReplicationHealthyCondition()).To(BeNil())
	})
})

var _ = Describe("updateDRPCProtectedCondition ReplicationHealthy", func() {
	protectedCondition := func(drpc *rmn.DRPlacementControl) *metav1.Condition {
		return meta.FindStatusCondition(drpc.Status.Conditions, rmn.ConditionProtected)
	}

	It("keeps Protected True when ReplicationHealthy is missing", func() {
		drpc := &rmn.DRPlacementControl{ObjectMeta: metav1.ObjectMeta{Name: "drpc", Namespace: "ns", Generation: 1}}
		updateDRPCProtectedCondition(drpc, healthyPrimaryVRG(), "cluster-1")

		cond := protectedCondition(drpc)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(rmn.ReasonProtected))
	})

	It("keeps Protected True when ReplicationHealthy is True", func() {
		drpc := &rmn.DRPlacementControl{ObjectMeta: metav1.ObjectMeta{Name: "drpc", Namespace: "ns", Generation: 1}}
		vrg := healthyPrimaryVRG()
		vrg.Status.Conditions = append(vrg.Status.Conditions,
			vrgCond(VRGConditionTypeReplicationHealthy, metav1.ConditionTrue, VRGConditionReasonReady))

		updateDRPCProtectedCondition(drpc, vrg, "cluster-1")

		cond := protectedCondition(drpc)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(rmn.ReasonProtected))
	})

	It("sets Protected False when ReplicationHealthy is False", func() {
		drpc := &rmn.DRPlacementControl{ObjectMeta: metav1.ObjectMeta{Name: "drpc", Namespace: "ns", Generation: 1}}
		vrg := healthyPrimaryVRG()
		vrg.Status.Conditions = append(vrg.Status.Conditions,
			vrgCond(VRGConditionTypeReplicationHealthy, metav1.ConditionFalse, VRGConditionReasonError))

		updateDRPCProtectedCondition(drpc, vrg, "cluster-1")

		cond := protectedCondition(drpc)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(rmn.ReasonProtectedError))
		Expect(cond.Message).To(ContainSubstring("replication health"))
	})

	It("sets Protected Unknown when ReplicationHealthy is Unknown", func() {
		drpc := &rmn.DRPlacementControl{ObjectMeta: metav1.ObjectMeta{Name: "drpc", Namespace: "ns", Generation: 1}}
		vrg := healthyPrimaryVRG()
		vrg.Status.Conditions = append(vrg.Status.Conditions,
			vrgCond(VRGConditionTypeReplicationHealthy, metav1.ConditionUnknown, VRGConditionReasonErrorUnknown))

		updateDRPCProtectedCondition(drpc, vrg, "cluster-1")

		cond := protectedCondition(drpc)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal(rmn.ReasonProtectedUnknown))
	})
})
