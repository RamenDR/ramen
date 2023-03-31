// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

// white box testing desired for Recipe/KubeObject conversions
package controllers //nolint: testpackage

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ramendr/ramen/controllers/kubeobjects"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	Recipe "github.com/ramendr/recipe/api/v1alpha1"
)

var _ = Describe("VRG_KubeObjectProtection", func() {
	var hook *Recipe.Hook
	var group *Recipe.Group

	BeforeEach(func() {
		hook = &Recipe.Hook{
			Name:          "hook-single",
			Namespace:     "recipe-test",
			Type:          "exec",
			LabelSelector: "myapp=testapp",
			SinglePodOnly: false,
			Ops: []*Recipe.Operation{
				{
					Name:      "checkpoint",
					Container: "main",
					Timeout:   30,
					Command:   []string{"bash", "/scripts/checkpoint.sh"},
				},
			},
			Chks:      []*Recipe.Check{},
			Essential: new(bool),
		}

		group = &Recipe.Group{
			Name:                  "test-group",
			BackupRef:             "test-backup-ref",
			Type:                  "resource",
			IncludedResourceTypes: []string{"deployment", "replicaset"},
			ExcludedResourceTypes: nil,
			LabelSelector:         "test/empty-on-backup notin (true),test/ignore-on-backup notin (true)",
		}
	})

	Context("Conversion", func() {
		It("Hook to CaptureSpec", func() {
			labelSelector, err := metav1.ParseToLabelSelector(hook.LabelSelector)
			Expect(err).To(BeNil())

			targetCaptureSpec := &kubeobjects.CaptureSpec{
				Name: hook.Name + "-" + hook.Ops[0].Name,
				Spec: kubeobjects.Spec{
					KubeResourcesSpec: kubeobjects.KubeResourcesSpec{
						IncludedResources: []string{"pod"},
						ExcludedResources: []string{},
						Hooks: []kubeobjects.HookSpec{
							{
								Name:          hook.Ops[0].Name,
								Type:          hook.Type,
								Command:       hook.Ops[0].Command,
								Timeout:       metav1.Duration{Duration: time.Duration(hook.Ops[0].Timeout * int(time.Second))},
								Container:     hook.Ops[0].Container,
								LabelSelector: *labelSelector,
							},
						},
					},
					LabelSelector:           labelSelector,
					IncludeClusterResources: new(bool),
				},
			}
			converted, err := convertRecipeHookToCaptureSpec(*hook, *hook.Ops[0])

			Expect(err).To(BeNil())
			Expect(converted).To(Equal(targetCaptureSpec))
		})

		It("Hook to RecoverSpec", func() {
			labelSelector, err := metav1.ParseToLabelSelector(hook.LabelSelector)
			Expect(err).To(BeNil())

			targetRecoverSpec := &kubeobjects.RecoverSpec{
				BackupName: ramen.ReservedBackupName,
				Spec: kubeobjects.Spec{
					KubeResourcesSpec: kubeobjects.KubeResourcesSpec{
						IncludedResources: []string{"pod"},
						ExcludedResources: []string{},
						Hooks: []kubeobjects.HookSpec{
							{
								Name:          hook.Ops[0].Name,
								Type:          hook.Type,
								Command:       hook.Ops[0].Command,
								Timeout:       metav1.Duration{Duration: time.Duration(hook.Ops[0].Timeout * int(time.Second))},
								Container:     hook.Ops[0].Container,
								LabelSelector: *labelSelector,
							},
						},
					},
					LabelSelector:           labelSelector,
					IncludeClusterResources: new(bool),
				},
			}
			converted, err := convertRecipeHookToRecoverSpec(*hook, *hook.Ops[0])

			Expect(err).To(BeNil())
			Expect(converted).To(Equal(targetRecoverSpec))
		})

		It("Group to CaptureSpec", func() {
			labelSelector, err := metav1.ParseToLabelSelector(group.LabelSelector)
			Expect(err).To(BeNil())

			targetCaptureSpec := &kubeobjects.CaptureSpec{
				Name: group.Name,
				Spec: kubeobjects.Spec{
					KubeResourcesSpec: kubeobjects.KubeResourcesSpec{
						IncludedResources: group.IncludedResourceTypes,
						ExcludedResources: group.ExcludedResourceTypes,
					},
					LabelSelector:           labelSelector,
					IncludeClusterResources: group.IncludeClusterResources,
					OrLabelSelectors:        []*metav1.LabelSelector{},
				},
			}
			converted, err := convertRecipeGroupToCaptureSpec(*group)

			Expect(err).To(BeNil())
			Expect(converted).To(Equal(targetCaptureSpec))
		})

		It("Group to RecoverSpec", func() {
			labelSelector, err := metav1.ParseToLabelSelector(group.LabelSelector)
			Expect(err).To(BeNil())

			targetRecoverSpec := &kubeobjects.RecoverSpec{
				BackupName: group.BackupRef,
				Spec: kubeobjects.Spec{
					KubeResourcesSpec: kubeobjects.KubeResourcesSpec{
						IncludedResources: group.IncludedResourceTypes,
						ExcludedResources: group.ExcludedResourceTypes,
					},
					LabelSelector:           labelSelector,
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
