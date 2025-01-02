// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
	recipe "github.com/ramendr/recipe/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const timeout = 300

type DiscoveredApp struct {
	IncludeRecipe bool
	IncludeHooks  bool
}

func (d DiscoveredApp) GetName() string {
	if d.IncludeRecipe {
		if d.IncludeHooks {
			return "disapp-recipe-hooks"
		}

		return "disapp-recipe"
	}

	return "disapp"
}

func (d DiscoveredApp) GetNamespace() string {
	return util.RamenOpsNamespace
}

// Deploy creates a workload on the first managed cluster.
func (d DiscoveredApp) Deploy(ctx types.Context) error {
	log := ctx.Logger()
	appNamespace := ctx.AppNamespace()

	log.Infof("Deploying workload in namespace %q", appNamespace)

	// create namespace in both dr clusters
	if err := util.CreateNamespaceAndAddAnnotation(appNamespace); err != nil {
		return err
	}

	tempDir, err := os.MkdirTemp("", "ramen-")
	if err != nil {
		return err
	}

	// Clean up by removing the temporary directory when done
	defer os.RemoveAll(tempDir)

	if err = CreateKustomizationFile(ctx, tempDir); err != nil {
		return err
	}

	drpolicy, err := util.GetDRPolicy(util.Ctx.Hub.Client, util.DefaultDRPolicyName)
	if err != nil {
		return err
	}

	cmd := exec.Command("kubectl", "apply", "-k", tempDir, "-n", appNamespace,
		"--context", drpolicy.Spec.DRClusters[0], "--timeout=5m")

	if out, err := cmd.Output(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("%w: stdout=%q stderr=%q", err, out, ee.Stderr)
		}

		return err
	}

	if err = WaitWorkloadHealth(ctx, util.Ctx.C1.Client, appNamespace); err != nil {
		return err
	}

	log.Info("Workload deployed")

	// recipe needs to be created based on flags
	if d.IncludeRecipe {
		recipeName := ctx.Name() + "-recipe"
		if err := createRecipe(recipeName, appNamespace, d.IncludeHooks); err != nil {
			log.Info("recipe creation failed")
		}

		log.Info("recipe created on both dr clusters")
	}

	return nil
}

// Undeploy deletes the workload from the managed clusters.
func (d DiscoveredApp) Undeploy(ctx types.Context) error {
	log := ctx.Logger()
	appNamespace := ctx.AppNamespace()

	log.Infof("Undeploying workload in namespace %q", appNamespace)

	drpolicy, err := util.GetDRPolicy(util.Ctx.Hub.Client, util.DefaultDRPolicyName)
	if err != nil {
		return err
	}

	log.Infof("Deleting discovered apps on cluster %q", drpolicy.Spec.DRClusters[0])

	// delete app on both clusters
	if err := DeleteDiscoveredApps(ctx, appNamespace, drpolicy.Spec.DRClusters[0]); err != nil {
		return err
	}

	log.Infof("Deletting discovered apps on cluster %q", drpolicy.Spec.DRClusters[1])

	if err := DeleteDiscoveredApps(ctx, appNamespace, drpolicy.Spec.DRClusters[1]); err != nil {
		return err
	}

	if d.IncludeRecipe {
		recipeName := ctx.Name() + "-recipe"

		log.Infof("Deleting recipe on cluster %q", drpolicy.Spec.DRClusters[0])

		if err := deleteRecipe(util.Ctx.C1.Client, recipeName, appNamespace); err != nil {
			return err
		}

		log.Infof("Deleting recipe on cluster %q", drpolicy.Spec.DRClusters[1])

		if err := deleteRecipe(util.Ctx.C2.Client, recipeName, appNamespace); err != nil {
			return err
		}
	}

	log.Infof("Deleting namespace %q on cluster %q", appNamespace, drpolicy.Spec.DRClusters[0])

	// delete namespace on both clusters
	if err := util.DeleteNamespace(util.Ctx.C1.Client, appNamespace, log); err != nil {
		return err
	}

	log.Infof("Deleting namespace %q on cluster %q", appNamespace, drpolicy.Spec.DRClusters[1])

	if err := util.DeleteNamespace(util.Ctx.C2.Client, appNamespace, log); err != nil {
		return err
	}

	log.Info("Workload undeployed")

	return nil
}

func (d DiscoveredApp) IsDiscovered() bool {
	return true
}

func createRecipe(name, namespace string, includeHooks bool) error {
	var recipe *recipe.Recipe
	if includeHooks {
		recipe = getRecipeWithHooks(name, namespace)
	} else {
		recipe = getRecipeWithoutHooks(name, namespace)
	}

	err := util.Ctx.C1.Client.Create(context.Background(), recipe)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}

		util.Ctx.Log.Info("recipe " + name + " already exists" + " in the cluster " + "C1")
	}

	err = util.Ctx.C2.Client.Create(context.Background(), recipe)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}

		util.Ctx.Log.Info("recipe " + name + " already exists" + " in the cluster " + "C2")
	}

	return nil
}

func getRecipeWithoutHooks(name, namespace string) *recipe.Recipe {
	return &recipe.Recipe{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Recipe",
			APIVersion: "ramendr.openshift.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: recipe.RecipeSpec{
			AppType: "busybox",
			Groups: []*recipe.Group{
				{
					Name:      "rg1",
					Type:      "resource",
					BackupRef: "rg1",
					IncludedNamespaces: []string{
						namespace,
					},
					LabelSelector: &metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      "appname",
								Operator: metav1.LabelSelectorOpIn,
								Values:   []string{"busybox"},
							},
						},
					},
				},
			},
			Workflows: []*recipe.Workflow{
				{
					Name: "backup",
					Sequence: []map[string]string{
						{
							"group": "rg1",
						},
					},
				},
				{
					Name: "restore",
					Sequence: []map[string]string{
						{
							"group": "rg1",
						},
					},
				},
			},
		},
	}
}

func getRecipeWithHooks(name, namespace string) *recipe.Recipe {
	return &recipe.Recipe{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Recipe",
			APIVersion: "ramendr.openshift.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: recipe.RecipeSpec{
			AppType: "busybox",
			Groups: []*recipe.Group{
				{
					Name:      "rg1",
					Type:      "resource",
					BackupRef: "rg1",
					IncludedNamespaces: []string{
						namespace,
					},
					LabelSelector: &metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      "appname",
								Operator: metav1.LabelSelectorOpIn,
								Values:   []string{"busybox"},
							},
						},
					},
				},
			},
			Hooks: []*recipe.Hook{
				getHookSpec(namespace, "backup"),
				getHookSpec(namespace, "restore"),
			},
			Workflows: []*recipe.Workflow{
				{
					Name: "backup",
					Sequence: []map[string]string{
						{
							"hook": "backup/check-replicas",
						},
						{
							"group": "rg1",
						},
					},
				},
				{
					Name: "restore",
					Sequence: []map[string]string{
						{
							"group": "rg1",
						},
						{
							"hook": "restore/check-replicas",
						},
					},
				},
			},
		},
	}
}

func getHookSpec(namespace, hookType string) *recipe.Hook {
	return &recipe.Hook{
		Name:           hookType,
		Type:           "check",
		Namespace:      namespace,
		NameSelector:   "busybox",
		SelectResource: "deployment",
		Timeout:        timeout,
		Chks: []*recipe.Check{
			{
				Name:      "check-replicas",
				Condition: "{$.spec.replicas} == {$.status.readyReplicas}",
			},
		},
	}
}

func deleteRecipe(client client.Client, name, namespace string) error {
	r := &recipe.Recipe{}
	key := k8stypes.NamespacedName{Namespace: namespace, Name: name}

	err := client.Get(context.Background(), key, r)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}

		return nil
	}

	return client.Delete(context.Background(), r)
}
