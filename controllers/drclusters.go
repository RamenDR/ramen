/*
Copyright 2022 The RamenDR authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"fmt"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	rmn "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/util"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

func drClusterDeploy(drcluster *rmn.DRCluster, mwu *util.MWUtil, ramenConfig *rmn.RamenConfig) error {
	objects := []interface{}{}

	if ramenConfig.DrClusterOperator.DeploymentAutomationEnabled {
		var err error

		objects, err = objectsToDeploy(ramenConfig)
		if err != nil {
			return err
		}
	}

	return mwu.CreateOrUpdateDrClusterManifestWork(drcluster.Name, objects...)
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
		drClusterOperatorConfigMapName,
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
		subscription(
			drClusterOperatorNamespaceName,
			drClusterOperatorChannelNameOrDefault(ramenConfig),
			drClusterOperatorPackageNameOrDefault(ramenConfig),
			drClusterOperatorCatalogSourceNameOrDefault(ramenConfig),
			drClusterOperatorCatalogSourceNamespaceNameOrDefault(ramenConfig),
			drClusterOperatorClusterServiceVersionNameOrDefault(ramenConfig),
		),
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

func drClusterUndeploy(drcluster *rmn.DRCluster, mwu *util.MWUtil) error {
	clusterNames := sets.String{}
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

	if err := mwu.DeleteManifestWork(util.DrClusterManifestWorkName, drcluster.Name); err != nil {
		return fmt.Errorf("drcluster '%v' manifest work delete: %w", drcluster.Name, err)
	}

	return nil
}
