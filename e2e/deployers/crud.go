// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/ramendr/ramen/e2e/util"
	"github.com/ramendr/ramen/e2e/workloads"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	argocdv1alpha1hack "github.com/ramendr/ramen/e2e/argocd"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ocmv1b1 "open-cluster-management.io/api/cluster/v1beta1"
	ocmv1b2 "open-cluster-management.io/api/cluster/v1beta2"
	placementrulev1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	subscriptionv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/v1"
)

const (
	AppLabelKey    = "app"
	ClusterSetName = "default"
)

func CreateManagedClusterSetBinding(name, namespace string) error {
	labels := make(map[string]string)
	labels[AppLabelKey] = namespace
	mcsb := &ocmv1b2.ManagedClusterSetBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: ocmv1b2.ManagedClusterSetBindingSpec{
			ClusterSet: ClusterSetName,
		},
	}

	err := util.Ctx.Hub.CtrlClient.Create(context.Background(), mcsb)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}
	}

	return nil
}

func DeleteManagedClusterSetBinding(name, namespace string) error {
	mcsb := &ocmv1b2.ManagedClusterSetBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	err := util.Ctx.Hub.CtrlClient.Delete(context.Background(), mcsb)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}

		util.Ctx.Log.Info("managedClusterSetBinding " + name + " not found")
	}

	return nil
}

func CreatePlacement(name, namespace string) error {
	labels := make(map[string]string)
	labels[AppLabelKey] = name
	clusterSet := []string{ClusterSetName}

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
		},
	}

	err := util.Ctx.Hub.CtrlClient.Create(context.Background(), placement)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}

		util.Ctx.Log.Info("placement " + placement.Name + " already Exists")
	}

	return nil
}

func DeletePlacement(name, namespace string) error {
	placement := &ocmv1b1.Placement{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	err := util.Ctx.Hub.CtrlClient.Delete(context.Background(), placement)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}

		util.Ctx.Log.Info("placement " + name + " not found")
	}

	return nil
}

func CreateSubscription(s Subscription, w workloads.Workload) error {
	name := GetCombinedName(s, w)
	namespace := name

	labels := make(map[string]string)
	labels[AppLabelKey] = name

	annotations := make(map[string]string)
	annotations["apps.open-cluster-management.io/github-branch"] = w.GetRevision()
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
			Namespace:   namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: subscriptionv1.SubscriptionSpec{
			Channel:   util.GetChannelNamespace() + "/" + util.GetChannelName(),
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

	err := util.Ctx.Hub.CtrlClient.Create(context.Background(), subscription)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			util.Ctx.Log.Info(fmt.Sprintf("create subscription with error: %v", err))

			return err
		}

		util.Ctx.Log.Info("subscription " + subscription.Name + " already Exists")
	}

	return nil
}

func DeleteSubscription(s Subscription, w workloads.Workload) error {
	name := GetCombinedName(s, w)
	namespace := name

	subscription := &subscriptionv1.Subscription{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	err := util.Ctx.Hub.CtrlClient.Delete(context.Background(), subscription)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}

		util.Ctx.Log.Info("subscription " + name + " not found")
	}

	return nil
}

func GetCombinedName(d Deployer, w workloads.Workload) string {
	return strings.ToLower(d.GetName() + "-" + w.GetName() + "-" + w.GetAppName())
}

func getSubscription(ctrlClient client.Client, namespace, name string) (*subscriptionv1.Subscription, error) {
	subscription := &subscriptionv1.Subscription{}
	key := types.NamespacedName{Name: name, Namespace: namespace}

	err := ctrlClient.Get(context.Background(), key, subscription)
	if err != nil {
		return nil, err
	}

	return subscription, nil
}

func CreatePlacementDecisionConfigMap(cmName string, cmNamespace string) error {
	object := metav1.ObjectMeta{Name: cmName, Namespace: cmNamespace}

	data := map[string]string{
		"apiVersion":    "cluster.open-cluster-management.io/v1beta1",
		"kind":          "placementdecisions",
		"statusListKey": "decisions",
		"matchKey":      "clusterName",
	}

	configMap := &corev1.ConfigMap{ObjectMeta: object, Data: data}

	err := util.Ctx.Hub.CtrlClient.Create(context.Background(), configMap)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("could not create configMap " + cmName)
		}

		util.Ctx.Log.Info("configMap " + cmName + " already Exists")
	}

	return nil
}

func DeleteConfigMap(cmName string, cmNamespace string) error {
	object := metav1.ObjectMeta{Name: cmName, Namespace: cmNamespace}

	configMap := &corev1.ConfigMap{
		ObjectMeta: object,
	}

	err := util.Ctx.Hub.CtrlClient.Delete(context.Background(), configMap)
	if err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("could not delete configMap " + cmName)
		}

		util.Ctx.Log.Info("configMap " + cmName + " not found")
	}

	return nil
}

// nolint:funlen
func CreateApplicationSet(a ApplicationSet, w workloads.Workload) error {
	var requeueSeconds int64 = 180

	name := GetCombinedName(a, w)
	namespace := util.ArgocdNamespace

	appset := &argocdv1alpha1hack.ApplicationSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
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
						RepoURL:        util.GetGitURL(),
						Path:           w.GetPath(),
						TargetRevision: w.GetRevision(),
					},
					Destination: argocdv1alpha1hack.ApplicationDestination{
						Server:    "{{server}}",
						Namespace: name,
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

	err := util.Ctx.Hub.CtrlClient.Create(context.Background(), appset)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}

		util.Ctx.Log.Info("applicationset " + appset.Name + " already Exists")
	}

	return nil
}

func DeleteApplicationSet(a ApplicationSet, w workloads.Workload) error {
	name := GetCombinedName(a, w)
	namespace := util.ArgocdNamespace

	appset := &argocdv1alpha1hack.ApplicationSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	err := util.Ctx.Hub.CtrlClient.Delete(context.Background(), appset)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}

		util.Ctx.Log.Info("applicationset " + appset.Name + " not found")
	}

	return nil
}

// check if only the last appset is in the argocd namespace
func isLastAppsetInArgocdNs(namespace string) (bool, error) {
	appsetList := &argocdv1alpha1hack.ApplicationSetList{}

	err := util.Ctx.Hub.CtrlClient.List(
		context.Background(), appsetList, client.InNamespace(namespace))
	if err != nil {
		util.Ctx.Log.Info("error in getting application sets")

		return false, err
	}

	return len(appsetList.Items) == 1, nil
}

func createDeployment(client client.Client, deploy *appsv1.Deployment, namespace string) error {
	deploy.Namespace = namespace

	err := client.Create(context.Background(), deploy)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}

		util.Ctx.Log.Info("deployment " + deploy.Name + " already Exists")
	}

	return nil
}

func getDeployment(client client.Client, namespace, name string) (*appsv1.Deployment, error) {
	deploy := &appsv1.Deployment{}
	key := types.NamespacedName{Name: name, Namespace: namespace}

	err := client.Get(context.Background(), key, deploy)
	if err != nil {
		return nil, err
	}

	return deploy, nil
}

func DeleteDeployment(client client.Client, namespace, name string) error {
	deploy, err := getDeployment(client, namespace, name)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}

		return nil
	}

	err = client.Delete(context.Background(), deploy)
	if err != nil {
		return err
	}

	return nil
}

func createPVC(client client.Client, pvc *corev1.PersistentVolumeClaim, namespace string) error {
	pvc.Namespace = namespace

	err := client.Create(context.Background(), pvc)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}

		util.Ctx.Log.Info("pvc " + pvc.Name + " already Exists")
	}

	return nil
}

func getPVC(client client.Client, namespace, name string) (*corev1.PersistentVolumeClaim, error) {
	pvc := &corev1.PersistentVolumeClaim{}
	key := types.NamespacedName{Name: name, Namespace: namespace}

	err := client.Get(context.Background(), key, pvc)
	if err != nil {
		return nil, err
	}

	return pvc, nil
}

func DeletePVC(client client.Client, namespace, name string) error {
	pvc, err := getPVC(client, namespace, name)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}

		return nil
	}

	err = client.Delete(context.Background(), pvc)
	if err != nil {
		return err
	}

	return nil
}

func GetPVCFromFile() (*corev1.PersistentVolumeClaim, error) {
	pvcFileName := "yamls/discoveredapps/pvc.yaml"

	pvcFileContents, err := os.ReadFile(pvcFileName)
	if err != nil {
		err = fmt.Errorf("unable to load file %s: %w",
			pvcFileName, err)

		return nil, err
	}

	pvc := &corev1.PersistentVolumeClaim{}

	err = yaml.Unmarshal(pvcFileContents, pvc)
	if err != nil {
		err = fmt.Errorf("unable to marshal file %s: %w",
			pvcFileName, err)

		return nil, err
	}

	return pvc, nil
}

func GetDeploymentFromFile() (*appsv1.Deployment, error) {
	deployFileName := "yamls/discoveredapps/busybox-deployment.yaml"

	deployFileContents, err := os.ReadFile(deployFileName)
	if err != nil {
		err = fmt.Errorf("unable to load file %s: %w",
			deployFileName, err)

		return nil, err
	}

	deploy := &appsv1.Deployment{}

	err = yaml.Unmarshal(deployFileContents, deploy)
	if err != nil {
		err = fmt.Errorf("unable to marshal file %s: %w",
			deployFileName, err)

		return nil, err
	}

	return deploy, nil
}

func DeleteDiscoveredApps(client client.Client, namespace string) error {
	pvc, err := GetPVCFromFile()
	if err != nil {
		return err
	}

	deploy, err := GetDeploymentFromFile()
	if err != nil {
		return err
	}

	if err = DeletePVC(client, namespace, pvc.Name); err != nil {
		return err
	}

	return DeleteDeployment(client, namespace, deploy.Name)
}
