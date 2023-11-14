// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	rmn "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/util"
	"github.com/ramendr/ramen/controllers/volsync"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
)

func drClusterDeploy(drClusterInstance *drclusterInstance, ramenConfig *rmn.RamenConfig) error {
	drcluster := drClusterInstance.object
	mwu := drClusterInstance.mwUtil

	objects := []interface{}{}

	if ramenConfig.DrClusterOperator.DeploymentAutomationEnabled {
		var err error

		objects, err = objectsToDeploy(ramenConfig)
		if err != nil {
			return err
		}

		objects, err = appendSubscriptionObject(drcluster, mwu, ramenConfig, objects)
		if err != nil {
			return err
		}

		// Deploy volsync to dr cluster
		err = volsync.DeployVolSyncToCluster(drClusterInstance.ctx, drClusterInstance.client, drcluster.GetName(),
			drClusterInstance.log)
		if err != nil {
			return fmt.Errorf("unable to deploy volsync to drcluster: %w", err)
		}
	}

	annotations := make(map[string]string)

	annotations["DRClusterName"] = mwu.InstName

	return mwu.CreateOrUpdateDrClusterManifestWork(drcluster.Name, objects, annotations)
}

func appendSubscriptionObject(
	drcluster *rmn.DRCluster,
	mwu *util.MWUtil,
	ramenConfig *rmn.RamenConfig,
	objects []interface{},
) ([]interface{}, error) {
	mwSub, err := SubscriptionFromDrClusterManifestWork(mwu, drcluster.Name)
	if err != nil {
		return nil, err
	}

	if mwSub != nil {
		// If Subscription spec, other than CSV version is the same, use existing Subscription object to allow
		// upgrades to later CSV versions as they appear on the managed clusters (instead of forcing it to
		// a later CSV version as the channel may not yet be up to date on the managed cluster).
		// As we create Subscriptions with automatic install plans, when a later version is available it would
		// automatically update to the same.
		if mwSub.Spec.Channel == drClusterOperatorChannelNameOrDefault(ramenConfig) &&
			mwSub.Spec.CatalogSource == drClusterOperatorCatalogSourceNameOrDefault(ramenConfig) &&
			mwSub.Spec.CatalogSourceNamespace == drClusterOperatorCatalogSourceNamespaceNameOrDefault(ramenConfig) &&
			mwSub.Spec.Package == drClusterOperatorPackageNameOrDefault(ramenConfig) {
			return append(objects, mwSub), nil
		}
	}

	return append(objects,
		subscription(
			drClusterOperatorNamespaceNameOrDefault(ramenConfig),
			drClusterOperatorChannelNameOrDefault(ramenConfig),
			drClusterOperatorPackageNameOrDefault(ramenConfig),
			drClusterOperatorCatalogSourceNameOrDefault(ramenConfig),
			drClusterOperatorCatalogSourceNamespaceNameOrDefault(ramenConfig),
			drClusterOperatorClusterServiceVersionNameOrDefault(ramenConfig),
		)), nil
}

var olmClusterRole = &rbacv1.ClusterRole{
	TypeMeta:   metav1.TypeMeta{Kind: "ClusterRole", APIVersion: "rbac.authorization.k8s.io/v1"},
	ObjectMeta: metav1.ObjectMeta{Name: "open-cluster-management:klusterlet-work-sa:agent:olm-edit"},
	Rules: []rbacv1.PolicyRule{
		{
			APIGroups: []string{"operators.coreos.com"},
			Resources: []string{"operatorgroups"},
			Verbs:     []string{"create", "get", "list", "update", "delete"},
		},
	},
}

func objectsToDeploy(hubOperatorRamenConfig *rmn.RamenConfig) ([]interface{}, error) {
	objects := []interface{}{}

	drClusterOperatorRamenConfig := *hubOperatorRamenConfig
	ramenConfig := &drClusterOperatorRamenConfig
	drClusterOperatorNamespaceName := drClusterOperatorNamespaceNameOrDefault(ramenConfig)
	ramenConfig.LeaderElection.ResourceName = drClusterLeaderElectionResourceName
	ramenConfig.RamenControllerType = rmn.DRClusterType

	drClusterOperatorConfigMap, err := ConfigMapNew(
		drClusterOperatorNamespaceName,
		DrClusterOperatorConfigMapName,
		ramenConfig,
	)
	if err != nil {
		return nil, err
	}

	return append(objects,
		util.Namespace(drClusterOperatorNamespaceName),
		olmClusterRole,
		olmRoleBinding(drClusterOperatorNamespaceName),
		operatorGroup(drClusterOperatorNamespaceName),
		drClusterOperatorConfigMap,
	), nil
}

func olmRoleBinding(namespaceName string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		TypeMeta: metav1.TypeMeta{Kind: "RoleBinding", APIVersion: "rbac.authorization.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "open-cluster-management:klusterlet-work-sa:agent:olm-edit",
			Namespace: namespaceName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "klusterlet-work-sa",
				Namespace: "open-cluster-management-agent",
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "open-cluster-management:klusterlet-work-sa:agent:olm-edit",
		},
	}
}

func operatorGroup(namespaceName string) *operatorsv1.OperatorGroup {
	return &operatorsv1.OperatorGroup{
		TypeMeta:   metav1.TypeMeta{Kind: "OperatorGroup", APIVersion: "operators.coreos.com/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "ramen-operator-group", Namespace: namespaceName},
	}
}

func subscription(
	namespaceName string,
	channelName string,
	packageName string,
	catalogSourceName string,
	catalogSourceNamespaceName string,
	clusterServiceVersionName string,
) *operatorsv1alpha1.Subscription {
	return &operatorsv1alpha1.Subscription{
		TypeMeta:   metav1.TypeMeta{Kind: "Subscription", APIVersion: "operators.coreos.com/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: "ramen-dr-cluster-subscription", Namespace: namespaceName},
		Spec: &operatorsv1alpha1.SubscriptionSpec{
			CatalogSource:          catalogSourceName,
			CatalogSourceNamespace: catalogSourceNamespaceName,
			Package:                packageName,
			Channel:                channelName,
			StartingCSV:            clusterServiceVersionName,
			InstallPlanApproval:    "Automatic",
		},
	}
}

func SubscriptionFromDrClusterManifestWork(
	mwu *util.MWUtil,
	clusterName string,
) (*operatorsv1alpha1.Subscription, error) {
	mw, err := mwu.GetDrClusterManifestWork(clusterName)
	if err != nil {
		return nil, fmt.Errorf("failed fetching cluster manifest work %w", err)
	}

	if mw == nil {
		return nil, nil
	}

	gvk := schema.GroupVersionKind{
		Group:   operatorsv1alpha1.GroupName,
		Version: operatorsv1alpha1.GroupVersion,
		Kind:    "Subscription",
	}

	subRaw, err := util.GetRawExtension(mw.Spec.Workload.Manifests, gvk)
	if err != nil {
		return nil, fmt.Errorf("failed fetching subscription from cluster '%v' manifest %w", clusterName, err)
	}

	if subRaw == nil {
		return nil, nil
	}

	subscription := &operatorsv1alpha1.Subscription{}

	err = json.Unmarshal(subRaw.Raw, subscription)
	if err != nil {
		return nil, fmt.Errorf("failed unmarshaling subscription manifest for cluster '%v' %w", clusterName, err)
	}

	return subscription, nil
}

func drClusterUndeploy(
	drcluster *rmn.DRCluster,
	mwu *util.MWUtil,
	mcv util.ManagedClusterViewGetter,
	log logr.Logger,
) error {
	clusterNames := sets.Set[string]{}
	drpolicies := rmn.DRPolicyList{}

	if err := mwu.Client.List(mwu.Ctx, &drpolicies); err != nil {
		return fmt.Errorf("drpolicies list: %w", err)
	}

	for i := range drpolicies.Items {
		drpolicy1 := &drpolicies.Items[i]
		clusterNames = clusterNames.Insert(util.DrpolicyClusterNames(drpolicy1)...)
	}

	if clusterNames.Has(drcluster.Name) {
		return fmt.Errorf("drcluster '%v' referenced in one or more existing drPolicy resources", drcluster.Name)
	}

	if err := drClusterMModeCleanup(drcluster, mwu, mcv, log); err != nil {
		return err
	}

	if err := mwu.DeleteManifestWork(util.DrClusterManifestWorkName, drcluster.Name); err != nil {
		return fmt.Errorf("drcluster '%v' manifest work delete: %w", drcluster.Name, err)
	}

	return nil
}
