// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/types"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vrgController "github.com/ramendr/ramen/internal/controller"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	Recipe "github.com/ramendr/recipe/api/v1alpha1"
)

var (
	testNamespace   *corev1.Namespace
	volumeGroupName = "test-group-volume"
	vrgName         = "test-vrg"
)

const (
	addPVCSelectorLabels bool   = true
	vrgTestNamespaceBase string = "test-vrg-namespace-"
)

var _ = Describe("VolumeReplicationGroupPVCSelector", func() {
	var testCtx context.Context
	var cancel context.CancelFunc
	var vrgTestNamespace string

	BeforeEach(func() {
		testCtx, cancel = context.WithCancel(context.TODO())
		Expect(k8sClient).NotTo(BeNil())
		vrgTestNamespace = createUniqueNamespace(testCtx)
	})

	AfterEach(func() {
		Expect(k8sClient.Delete(testCtx, testNamespace)).To(Succeed())

		cancel()
	})

	Context("LabelSelector selection on VRG and Recipe", func() {
		It("when both exist, choose Recipe", func() {
			recipe := getRecipeDefinition(vrgTestNamespace)
			vrg := getVRGDefinitionWithKubeObjectProtection(addPVCSelectorLabels, vrgTestNamespace)

			createRecipeAndGet(testCtx, recipe)

			pvcSelector, err := vrgController.GetPVCSelector(testCtx, k8sClient, *vrg, *ramenConfig, testLogger)
			Expect(err).To(BeNil())

			correctLabels := getVolumeGroupLabelSelector(recipe)
			Expect(pvcSelector.LabelSelector).To(Equal(correctLabels))
			Expect(pvcSelector.NamespaceNames).To(ConsistOf(vrg.Namespace))

			cleanupRecipe(testCtx, recipe)
		})

		It("when only Recipe exists, choose Recipe", func() {
			recipe := getRecipeDefinition(vrgTestNamespace)
			createRecipeAndGet(testCtx, recipe)

			vrg := getVRGDefinitionWithKubeObjectProtection(!addPVCSelectorLabels, vrgTestNamespace)

			pvcSelector, err := vrgController.GetPVCSelector(testCtx, k8sClient, *vrg, *ramenConfig, testLogger)
			Expect(err).To(BeNil())

			correctLabels := getVolumeGroupLabelSelector(recipe)
			Expect(pvcSelector.LabelSelector).To(Equal(correctLabels))
			Expect(pvcSelector.NamespaceNames).To(ConsistOf(vrg.Namespace))

			cleanupRecipe(testCtx, recipe)
		})

		It("when only PVCSelector exists, choose PVCSelector", func() {
			vrg := getVRGDefinitionWithPVCSelectorLabels(vrgTestNamespace) // has PVCSelectorLabels, no Recipe info

			pvcSelector, err := vrgController.GetPVCSelector(testCtx, k8sClient, *vrg, *ramenConfig, testLogger)
			Expect(err).To(BeNil())

			correctLabels := vrg.Spec.PVCSelector
			Expect(pvcSelector.LabelSelector).To(Equal(correctLabels))
			Expect(pvcSelector.NamespaceNames).To(ConsistOf(vrg.Namespace))
		})

		It("produce error when Recipe info is defined, but no recipe exists", func() {
			// do not create Recipe object for this test
			vrg := getVRGDefinitionWithKubeObjectProtection(!addPVCSelectorLabels, vrgTestNamespace)

			_, err := vrgController.GetPVCSelector(testCtx, k8sClient, *vrg, *ramenConfig, testLogger)
			Expect(err).NotTo(BeNil())
		})
	})
})

func createRecipeAndGet(ctx context.Context, recipeSource *Recipe.Recipe) {
	foundRecipe := false
	timeoutMax := (time.Second * 10)
	waitTime := (time.Second * 1)

	var counter time.Duration // default/zero value = 0

	lookupKey := types.NamespacedName{Name: recipeSource.Name, Namespace: recipeSource.Namespace}

	for !foundRecipe && counter < timeoutMax {
		_, err := getRecipeObject(ctx, lookupKey)
		if err == nil {
			foundRecipe = true
		} else {
			err = createRecipe(ctx, recipeSource)

			Expect(err).ToNot(HaveOccurred())

			time.Sleep(waitTime)
			counter += waitTime
		}
	}

	Expect(foundRecipe).To(Equal(true), "recipe could not be found")
}

func getRecipeObject(ctx context.Context, lookupKey types.NamespacedName) (*Recipe.Recipe, error) {
	recipe := &Recipe.Recipe{}

	err := k8sClient.Get(ctx, lookupKey, recipe)

	return recipe, err
}

func createRecipe(ctx context.Context, recipe *Recipe.Recipe) error {
	err := k8sClient.Create(ctx, recipe)

	return err
}

// since object names are reused, use unique namespaces
func createUniqueNamespace(testCtx context.Context) string {
	testNamespace = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: vrgTestNamespaceBase,
		},
	}
	Expect(k8sClient.Create(testCtx, testNamespace)).To(Succeed())
	Expect(testNamespace.GetName()).NotTo(BeEmpty())

	return testNamespace.Name
}

func getBaseVRG(namespace string) *ramen.VolumeReplicationGroup {
	return &ramen.VolumeReplicationGroup{
		TypeMeta:   metav1.TypeMeta{Kind: "VolumeReplicationGroup", APIVersion: "ramendr.openshift.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: vrgName, Namespace: namespace},
		Spec: ramen.VolumeReplicationGroupSpec{
			Async: &ramen.VRGAsyncSpec{
				SchedulingInterval: "5m",
			},
			ReplicationState: ramen.Primary,
			S3Profiles:       []string{"dummy-s3-profile"},
		},
	}
}

func getVRGDefinitionWithPVCSelectorLabels(namespace string) *ramen.VolumeReplicationGroup {
	vrg := getBaseVRG(namespace)

	vrg.Spec.PVCSelector = metav1.LabelSelector{
		MatchLabels: map[string]string{"appclass": "gold"},
	}

	return vrg
}

func getVRGDefinitionWithKubeObjectProtection(hasPVCSelectorLabels bool, namespace string,
) *ramen.VolumeReplicationGroup {
	var vrg *ramen.VolumeReplicationGroup

	if hasPVCSelectorLabels {
		vrg = getVRGDefinitionWithPVCSelectorLabels(namespace)
	} else {
		vrg = getBaseVRG(namespace)
	}

	vrg.Spec.KubeObjectProtection = &ramen.KubeObjectProtectionSpec{
		RecipeRef: &ramen.RecipeRef{
			Namespace: vrg.Namespace,
			Name:      "test-recipe",
		},
	}

	return vrg
}

func getTestHook() *Recipe.Hook {
	duration := 30

	return &Recipe.Hook{
		Name: "hook-single",
		Type: "exec",
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
}

func getTestGroup() *Recipe.Group {
	return &Recipe.Group{
		Name:                  "test-group",
		BackupRef:             "test-backup-ref",
		Type:                  "resource",
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
}

func getTestVolumeGroup() *Recipe.Group {
	return &Recipe.Group{
		Name: volumeGroupName,
		Type: "volume",
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
}

func getRecipeDefinition(namespace string) *Recipe.Recipe {
	return &Recipe.Recipe{
		TypeMeta:   metav1.TypeMeta{Kind: "Recipe", APIVersion: "ramendr.openshift.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: "test-recipe", Namespace: namespace},
		Spec: Recipe.RecipeSpec{
			Groups:  []*Recipe.Group{getTestGroup()},
			Volumes: getTestVolumeGroup(),
			Hooks:   []*Recipe.Hook{getTestHook()},
			Workflows: []*Recipe.Workflow{
				{
					Name: "backup",
					Sequence: []map[string]string{
						{
							"group": "test-group-volume",
						},
						{
							"group": "test-group",
						},
						{
							"hook": "test-hook",
						},
					},
				},
			},
		},
	}
}

func getVolumeGroupLabelSelector(recipe *Recipe.Recipe) metav1.LabelSelector {
	return *recipe.Spec.Volumes.LabelSelector
}

func cleanupRecipe(ctx context.Context, recipe *Recipe.Recipe) {
	err := k8sClient.Delete(ctx, recipe)
	Expect(err).To(BeNil())
}
