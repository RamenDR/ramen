// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/types"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vrgController "github.com/ramendr/ramen/controllers"

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

var _ = Describe("VolumeReplicationGroupController", func() {
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

			labels, err := vrgController.GetPVCLabelSelector(testCtx, k8sClient, *vrg, testLogger)
			Expect(err).To(BeNil())

			correctLabels := getVolumeGroupLabelSelector(recipe, volumeGroupName)
			Expect(labels).To(Equal(correctLabels))

			cleanupRecipe(testCtx, recipe)
		})

		It("when only Recipe exists, choose Recipe", func() {
			recipe := getRecipeDefinition(vrgTestNamespace)
			createRecipeAndGet(testCtx, recipe)

			vrg := getVRGDefinitionWithKubeObjectProtection(!addPVCSelectorLabels, vrgTestNamespace)

			labels, err := vrgController.GetPVCLabelSelector(testCtx, k8sClient, *vrg, testLogger)
			Expect(err).To(BeNil())

			correctLabels := getVolumeGroupLabelSelector(recipe, volumeGroupName)
			Expect(labels).To(Equal(correctLabels))

			cleanupRecipe(testCtx, recipe)
		})

		It("when only PVCSelector exists, choose PVCSelector", func() {
			vrg := getVRGDefinitionWithPVCSelectorLabels(vrgTestNamespace) // has PVCSelectorLabels, no Recipe info

			labels, err := vrgController.GetPVCLabelSelector(testCtx, k8sClient, *vrg, testLogger)
			Expect(err).To(BeNil())

			correctLabels := vrg.Spec.PVCSelector
			Expect(labels).To(Equal(correctLabels))
		})

		It("produce error when Recipe info is defined, but no recipe exists", func() {
			// do not create Recipe object for this test
			vrg := getVRGDefinitionWithKubeObjectProtection(!addPVCSelectorLabels, vrgTestNamespace)

			_, err := vrgController.GetPVCLabelSelector(testCtx, k8sClient, *vrg, testLogger)
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

func newStringPointerText(text string) *string {
	ptr := text

	return &ptr
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
		Recipe: &ramen.RecipeSpec{
			Name: newStringPointerText("test-recipe"),
			Workflow: &ramen.WorkflowSpec{
				VolumeGroupName: newStringPointerText(volumeGroupName),
			},
		},
	}

	return vrg
}

func getRecipeDefinition(namespace string) *Recipe.Recipe {
	hook := &Recipe.Hook{
		Name:          "hook-single",
		Namespace:     namespace,
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

	group := &Recipe.Group{
		Name:                  "test-group",
		BackupRef:             "test-backup-ref",
		Type:                  "resource",
		IncludedResourceTypes: []string{"deployment", "replicaset"},
		ExcludedResourceTypes: nil,
		LabelSelector:         "test/empty-on-backup notin (true),test/ignore-on-backup notin (true)",
	}

	volumeGroup := &Recipe.Group{
		Name:          volumeGroupName,
		Type:          "volume",
		LabelSelector: "test/empty-on-backup notin (true),test/ignore-on-backup notin (true)",
	}

	return &Recipe.Recipe{
		TypeMeta:   metav1.TypeMeta{Kind: "Recipe", APIVersion: "ramendr.openshift.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: "test-recipe", Namespace: namespace},
		Spec: Recipe.RecipeSpec{
			Groups: []*Recipe.Group{group, volumeGroup},
			Hooks:  []*Recipe.Hook{hook},
			Workflows: []*Recipe.Workflow{
				{
					Name: "test-workflow",
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

func getVolumeGroupLabelSelector(recipe *Recipe.Recipe, volumeGroupName string) metav1.LabelSelector {
	foundGroup := false

	for _, group := range recipe.Spec.Groups {
		if group.Name == volumeGroupName {
			labelSelector, err := metav1.ParseToLabelSelector(group.LabelSelector)

			Expect(err).ToNot(HaveOccurred())

			return *labelSelector
		}
	}

	Expect(foundGroup).To(Equal(true)) // should not run this

	return metav1.LabelSelector{}
}

func cleanupRecipe(ctx context.Context, recipe *Recipe.Recipe) {
	err := k8sClient.Delete(ctx, recipe)
	Expect(err).To(BeNil())
}
