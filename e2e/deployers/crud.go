// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"encoding/json"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	ocmv1b1 "open-cluster-management.io/api/cluster/v1beta1"
	ocmv1b2 "open-cluster-management.io/api/cluster/v1beta2"
	placementrulev1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	subscriptionv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/v1"
	"sigs.k8s.io/yaml"

	argocdv1alpha1hack "github.com/ramendr/ramen/e2e/argocd"
	"github.com/ramendr/ramen/e2e/types"
)

const (
	AppLabelKey = "app"

	fMode = 0o600
)

func CreateManagedClusterSetBinding(ctx types.TestContext, name, namespace string) error {
	log := ctx.Logger()
	config := ctx.Config()
	labels := make(map[string]string)
	labels[AppLabelKey] = namespace
	mcsb := &ocmv1b2.ManagedClusterSetBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: ocmv1b2.ManagedClusterSetBindingSpec{
			ClusterSet: config.ClusterSet,
		},
	}

	err := ctx.Env().Hub.Client.Create(ctx.Context(), mcsb)
	if err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return err
		}

		log.Debugf("ManagedClusterSetBinding \"%s/%s\" already exist in cluster %q", namespace, name, ctx.Env().Hub.Name)

		return nil
	}

	log.Debugf("Created ManagedClusterSetBinding \"%s/%s\" in cluster %q", namespace, name, ctx.Env().Hub.Name)

	return nil
}

func DeleteManagedClusterSetBinding(ctx types.TestContext, name, namespace string) error {
	log := ctx.Logger()
	mcsb := &ocmv1b2.ManagedClusterSetBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	err := ctx.Env().Hub.Client.Delete(ctx.Context(), mcsb)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}

		log.Debugf("ManagedClusterSetBinding \"%s/%s\" not found in cluster %q", namespace, name, ctx.Env().Hub.Name)

		return nil
	}

	log.Debugf("Deleted ManagedClusterSetBinding \"%s/%s\" in cluster %q", namespace, name, ctx.Env().Hub.Name)

	return nil
}

func CreatePlacement(ctx types.TestContext, name, namespace string, clusterName string) error {
	log := ctx.Logger()
	config := ctx.Config()
	labels := make(map[string]string)
	labels[AppLabelKey] = name
	clusterSet := []string{config.ClusterSet}

	var numClusters int32 = 1
	placement := &ocmv1b1.Placement{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: ocmv1b1.PlacementSpec{
			ClusterSets:      clusterSet,
			NumberOfClusters: &numClusters,
			// Restricts to the specified cluster using requiredClusterSelector.
			Predicates: []ocmv1b1.ClusterPredicate{
				{
					RequiredClusterSelector: ocmv1b1.ClusterSelector{
						LabelSelector: metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "name",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{clusterName},
								},
							},
						},
					},
				},
			},
		},
	}

	err := ctx.Env().Hub.Client.Create(ctx.Context(), placement)
	if err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return err
		}

		log.Debugf("Placement \"%s/%s\" already exists in cluster %q", namespace, name, ctx.Env().Hub.Name)

		return nil
	}

	log.Debugf("Created placement \"%s/%s\" in cluster %q", namespace, name, ctx.Env().Hub.Name)

	return nil
}

func DeletePlacement(ctx types.TestContext, name, namespace string) error {
	log := ctx.Logger()
	placement := &ocmv1b1.Placement{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	err := ctx.Env().Hub.Client.Delete(ctx.Context(), placement)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}

		log.Debugf("Placement \"%s/%s\" not found in cluster %q", namespace, name, ctx.Env().Hub.Name)

		return nil
	}

	log.Debugf("Deleted placement \"%s/%s\" in cluster %q", namespace, name, ctx.Env().Hub.Name)

	return nil
}

func CreateSubscription(ctx types.TestContext, s Subscription) error {
	name := ctx.Name()
	log := ctx.Logger()
	config := ctx.Config()
	w := ctx.Workload()
	managementNamespace := ctx.ManagementNamespace()

	labels := make(map[string]string)
	labels[AppLabelKey] = name

	annotations := make(map[string]string)
	annotations["apps.open-cluster-management.io/github-branch"] = w.GetBranch()
	annotations["apps.open-cluster-management.io/github-path"] = w.GetPath()

	placementRef := corev1.ObjectReference{
		Kind: "Placement",
		Name: name,
	}

	placementRulePlacement := &placementrulev1.Placement{}
	placementRulePlacement.PlacementRef = &placementRef

	subscription := &subscriptionv1.Subscription{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   managementNamespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: subscriptionv1.SubscriptionSpec{
			Channel:   config.Channel.Namespace + "/" + config.Channel.Name,
			Placement: placementRulePlacement,
		},
	}

	if w.Kustomize() != "" {
		subscription.Spec.PackageOverrides = []*subscriptionv1.Overrides{}
		subscription.Spec.PackageOverrides = append(subscription.Spec.PackageOverrides, &subscriptionv1.Overrides{
			PackageName: "kustomization",
			PackageOverrides: []subscriptionv1.PackageOverride{
				{RawExtension: runtime.RawExtension{Raw: []byte("{\"value\": " + w.Kustomize() + "}")}},
			},
		})
	}

	err := ctx.Env().Hub.Client.Create(ctx.Context(), subscription)
	if err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return err
		}

		log.Debugf("Subscription \"%s/%s\" already exists in cluster %q", managementNamespace, name, ctx.Env().Hub.Name)

		return nil
	}

	log.Debugf("Created subscription \"%s/%s\" in cluster %q", managementNamespace, name, ctx.Env().Hub.Name)

	return nil
}

func DeleteSubscription(ctx types.TestContext, s Subscription) error {
	name := ctx.Name()
	log := ctx.Logger()
	managementNamespace := ctx.ManagementNamespace()

	subscription := &subscriptionv1.Subscription{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: managementNamespace,
		},
	}

	err := ctx.Env().Hub.Client.Delete(ctx.Context(), subscription)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}

		log.Debugf("Subscription \"%s/%s\" not found in cluster %q", managementNamespace, name, ctx.Env().Hub.Name)

		return nil
	}

	log.Debugf("Deleted subscription \"%s/%s\" in cluster %q", managementNamespace, name, ctx.Env().Hub.Name)

	return nil
}

func getSubscription(ctx types.TestContext, namespace, name string) (*subscriptionv1.Subscription, error) {
	hub := ctx.Env().Hub

	subscription := &subscriptionv1.Subscription{}
	key := k8stypes.NamespacedName{Name: name, Namespace: namespace}

	err := hub.Client.Get(ctx.Context(), key, subscription)
	if err != nil {
		return nil, err
	}

	return subscription, nil
}

func CreatePlacementDecisionConfigMap(ctx types.TestContext, cmName string, cmNamespace string) error {
	log := ctx.Logger()
	object := metav1.ObjectMeta{Name: cmName, Namespace: cmNamespace}

	data := map[string]string{
		"apiVersion":    "cluster.open-cluster-management.io/v1beta1",
		"kind":          "placementdecisions",
		"statusListKey": "decisions",
		"matchKey":      "clusterName",
	}

	configMap := &corev1.ConfigMap{ObjectMeta: object, Data: data}

	err := ctx.Env().Hub.Client.Create(ctx.Context(), configMap)
	if err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return fmt.Errorf("could not create configMap %q", cmName)
		}

		log.Debugf("ConfigMap \"%s/%s\" already exists in cluster %q", cmNamespace, cmName, ctx.Env().Hub.Name)

		return nil
	}

	log.Debugf("Created configMap \"%s/%s\" in cluster %q", cmNamespace, cmName, ctx.Env().Hub.Name)

	return nil
}

func DeleteConfigMap(ctx types.TestContext, cmName string, cmNamespace string) error {
	log := ctx.Logger()
	object := metav1.ObjectMeta{Name: cmName, Namespace: cmNamespace}

	configMap := &corev1.ConfigMap{
		ObjectMeta: object,
	}

	err := ctx.Env().Hub.Client.Delete(ctx.Context(), configMap)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return fmt.Errorf("could not delete configMap %q in cluster %q", cmName, ctx.Env().Hub.Name)
		}

		log.Debugf("ConfigMap \"%s/%s\" not found in cluster %q", cmNamespace, cmName, ctx.Env().Hub.Name)

		return nil
	}

	log.Debugf("Deleted configMap \"%s/%s\" in cluster %q", cmNamespace, cmName, ctx.Env().Hub.Name)

	return nil
}

// nolint:funlen
func CreateApplicationSet(ctx types.TestContext, a ApplicationSet) error {
	var requeueSeconds int64 = 180

	name := ctx.Name()
	log := ctx.Logger()
	config := ctx.Config()
	w := ctx.Workload()
	managementNamespace := ctx.ManagementNamespace()
	appNamespace := ctx.AppNamespace()

	appset := &argocdv1alpha1hack.ApplicationSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: managementNamespace,
		},
		Spec: argocdv1alpha1hack.ApplicationSetSpec{
			Generators: []argocdv1alpha1hack.ApplicationSetGenerator{
				{
					ClusterDecisionResource: &argocdv1alpha1hack.DuckTypeGenerator{
						ConfigMapRef: name,
						LabelSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"cluster.open-cluster-management.io/placement": name,
							},
						},
						RequeueAfterSeconds: &requeueSeconds,
					},
				},
			},
			Template: argocdv1alpha1hack.ApplicationSetTemplate{
				ApplicationSetTemplateMeta: argocdv1alpha1hack.ApplicationSetTemplateMeta{
					Name: name + "-{{name}}",
				},
				Spec: argocdv1alpha1hack.ApplicationSpec{
					Source: &argocdv1alpha1hack.ApplicationSource{
						RepoURL:        config.Repo.URL,
						Path:           w.GetPath(),
						TargetRevision: w.GetBranch(),
					},
					Destination: argocdv1alpha1hack.ApplicationDestination{
						Server:    "{{server}}",
						Namespace: appNamespace,
					},
					Project: "default",
					SyncPolicy: &argocdv1alpha1hack.SyncPolicy{
						Automated: &argocdv1alpha1hack.SyncPolicyAutomated{
							Prune:    true,
							SelfHeal: true,
						},
						SyncOptions: []string{
							"CreateNamespace=true",
							"PruneLast=true",
						},
					},
				},
			},
		},
	}

	if w.Kustomize() != "" {
		patches := &argocdv1alpha1hack.ApplicationSourceKustomize{}

		err := yaml.Unmarshal([]byte(w.Kustomize()), patches)
		if err != nil {
			return fmt.Errorf("unable to unmarshal Patches (%v)", err)
		}

		appset.Spec.Template.Spec.Source.Kustomize = patches
	}

	err := ctx.Env().Hub.Client.Create(ctx.Context(), appset)
	if err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return err
		}

		log.Debugf("Applicationset \"%s/%s\" already exists in cluster %q", managementNamespace, name, ctx.Env().Hub.Name)

		return nil
	}

	log.Debugf("Created applicationset \"%s/%s\" in cluster %q", managementNamespace, name, ctx.Env().Hub.Name)

	return nil
}

func DeleteApplicationSet(ctx types.TestContext, a ApplicationSet) error {
	name := ctx.Name()
	log := ctx.Logger()
	managementNamespace := ctx.ManagementNamespace()

	appset := &argocdv1alpha1hack.ApplicationSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: managementNamespace,
		},
	}

	err := ctx.Env().Hub.Client.Delete(ctx.Context(), appset)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}

		log.Debugf("Applicationset \"%s/%s\" not found in cluster %q", managementNamespace, name, ctx.Env().Hub.Name)

		return nil
	}

	log.Debugf("Deleted applicationset \"%s/%s\" in cluster %q", managementNamespace, name, ctx.Env().Hub.Name)

	return nil
}

type CombinedData map[string]interface{}

func CreateKustomizationFile(ctx types.TestContext, dir string) error {
	w := ctx.Workload()
	config := ctx.Config()
	yamlData := `resources:
- ` + config.Repo.URL + `/` + w.GetPath() + `?ref=` + w.GetBranch()

	var yamlContent CombinedData

	err := yaml.Unmarshal([]byte(yamlData), &yamlContent)
	if err != nil {
		return err
	}

	patch := w.Kustomize()

	var jsonContent CombinedData

	err = json.Unmarshal([]byte(patch), &jsonContent)
	if err != nil {
		return err
	}

	// Merge JSON content into YAML content
	for key, value := range jsonContent {
		yamlContent[key] = value
	}

	// Convert the combined content back to YAML
	combinedYAML, err := yaml.Marshal(&yamlContent)
	if err != nil {
		return err
	}

	// Write the combined content to a new YAML file
	outputFile := dir + "/kustomization.yaml"

	return os.WriteFile(outputFile, combinedYAML, fMode)
}
