// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

// white box testing desired for Recipe/KubeObject conversions
package controllers //nolint:testpackage

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ramendr/ramen/internal/controller/kubeobjects"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	Recipe "github.com/ramendr/recipe/api/v1alpha1"
)

const testNamespaceName = "my-ns"

// testData holds all the test fixtures
type testData struct {
	hook  *Recipe.Hook
	group *Recipe.Group
}

// newTestData creates a new instance of test data with default values
func newTestData() *testData {
	duration := 30
	essential := new(bool)

	hook := &Recipe.Hook{
		Namespace: testNamespaceName,
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
		Essential: essential,
	}

	group := &Recipe.Group{
		Name:                  "test-group",
		BackupRef:             "test-backup-ref",
		Type:                  "resource",
		IncludedNamespaces:    []string{testNamespaceName},
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

	return &testData{
		hook:  hook,
		group: group,
	}
}

// getExpectedHookCaptureSpec returns the expected CaptureSpec for a hook
func getExpectedHookCaptureSpec(hook *Recipe.Hook, opName string) *kubeobjects.CaptureSpec {
	return &kubeobjects.CaptureSpec{
		Name: hook.Name + "-" + opName,
		Spec: kubeobjects.Spec{
			KubeResourcesSpec: kubeobjects.KubeResourcesSpec{
				IncludedNamespaces: []string{hook.Namespace},
				IncludedResources:  []string{"pod"},
				ExcludedResources:  []string{},
				Hook: kubeobjects.HookSpec{
					Name:          hook.Name,
					Namespace:     hook.Namespace,
					Type:          hook.Type,
					LabelSelector: hook.LabelSelector,
					Op: kubeobjects.Operation{
						Name:      hook.Ops[0].Name,
						Command:   hook.Ops[0].Command,
						Container: hook.Ops[0].Container,
					},
				},
				IsHook: true,
			},
			LabelSelector:           hook.LabelSelector,
			IncludeClusterResources: new(bool),
		},
	}
}

// getExpectedHookRecoverSpec returns the expected RecoverSpec for a hook
func getExpectedHookRecoverSpec(hook *Recipe.Hook, opName string) *kubeobjects.RecoverSpec {
	return &kubeobjects.RecoverSpec{
		Spec: kubeobjects.Spec{
			KubeResourcesSpec: kubeobjects.KubeResourcesSpec{
				IncludedNamespaces: []string{hook.Namespace},
				IncludedResources:  []string{"pod"},
				ExcludedResources:  []string{},
				Hook: kubeobjects.HookSpec{
					Name:          hook.Name,
					Type:          hook.Type,
					Namespace:     hook.Namespace,
					LabelSelector: hook.LabelSelector,
					Op: kubeobjects.Operation{
						Name:      hook.Ops[0].Name,
						Command:   hook.Ops[0].Command,
						Container: hook.Ops[0].Container,
					},
				},
				IsHook: true,
			},
			LabelSelector:           hook.LabelSelector,
			IncludeClusterResources: new(bool),
		},
	}
}

// getExpectedGroupCaptureSpec returns the expected CaptureSpec for a group
func getExpectedGroupCaptureSpec(group *Recipe.Group) *kubeobjects.CaptureSpec {
	return &kubeobjects.CaptureSpec{
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
}

// getExpectedGroupRecoverSpec returns the expected RecoverSpec for a group
func getExpectedGroupRecoverSpec(group *Recipe.Group) *kubeobjects.RecoverSpec {
	return &kubeobjects.RecoverSpec{
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
}

var _ = Describe("VRG_KubeObjectProtection", func() {
	var td *testData

	BeforeEach(func() {
		td = newTestData()
	})

	Context("Conversion", func() {
		It("Hook to CaptureSpec", func() {
			expected := getExpectedHookCaptureSpec(td.hook, td.hook.Ops[0].Name)
			converted, err := convertRecipeHookToCaptureSpec(*td.hook, td.hook.Ops[0].Name)

			Expect(err).To(BeNil())
			Expect(converted).To(Equal(expected))
		})

		It("Hook to RecoverSpec", func() {
			expected := getExpectedHookRecoverSpec(td.hook, td.hook.Ops[0].Name)
			converted, err := convertRecipeHookToRecoverSpec(*td.hook, td.hook.Ops[0].Name)

			Expect(err).To(BeNil())
			Expect(converted).To(Equal(expected))
		})

		It("Group to CaptureSpec", func() {
			expected := getExpectedGroupCaptureSpec(td.group)
			converted, err := convertRecipeGroupToCaptureSpec(*td.group)

			Expect(err).To(BeNil())
			Expect(converted).To(Equal(expected))
		})

		It("Group to RecoverSpec", func() {
			expected := getExpectedGroupRecoverSpec(td.group)
			converted, err := convertRecipeGroupToRecoverSpec(*td.group)

			Expect(err).To(BeNil())
			Expect(converted).To(Equal(expected))
		})
	})
})
