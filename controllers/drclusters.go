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
	"sync"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	rmn "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/util"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

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

var drClustersMutex sync.Mutex

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

func drPolicySecretNames(drpolicy *rmn.DRPolicy,
	drclusters *rmn.DRClusterList,
	rmnCfg *rmn.RamenConfig) (sets.String, error) {
	secretNames := sets.String{}

	for _, managedCluster := range util.DrpolicyClusterNames(drpolicy) {
		mcProfileFound := false

		s3ProfileName := ""

		for i := range drclusters.Items {
			if drclusters.Items[i].Name == managedCluster {
				s3ProfileName = drclusters.Items[i].Spec.S3ProfileName
			}
		}

		for _, s3Profile := range rmnCfg.S3StoreProfiles {
			if s3ProfileName == s3Profile.S3ProfileName {
				secretNames.Insert(s3Profile.S3SecretRef.Name)

				mcProfileFound = true

				break
			}
		}

		if !mcProfileFound {
			return secretNames, fmt.Errorf("missing profile name (%s) in config for DRCluster (%s)",
				s3ProfileName, managedCluster)
		}
	}

	return secretNames, nil
}

func drClusterSecretsDeploy(
	clusterName string,
	drpolicy *rmn.DRPolicy,
	drclusters *rmn.DRClusterList,
	secretsUtil *util.SecretsUtil,
	rmnCfg *rmn.RamenConfig) error {
	if !rmnCfg.DrClusterOperator.DeploymentAutomationEnabled ||
		!rmnCfg.DrClusterOperator.S3SecretDistributionEnabled {
		return nil
	}

	drPolicySecrets, err := drPolicySecretNames(drpolicy, drclusters, rmnCfg)
	if err != nil {
		return err
	}

	for _, secretName := range drPolicySecrets.List() {
		if err := secretsUtil.AddSecretToCluster(
			secretName,
			clusterName,
			NamespaceName(),
			drClusterOperatorNamespaceNameOrDefault(rmnCfg)); err != nil {
			return fmt.Errorf("drcluster '%v' secret add '%v': %w", clusterName, secretName, err)
		}
	}

	return nil
}

func drPolicyDeploy(
	drpolicy *rmn.DRPolicy,
	drclusters *rmn.DRClusterList,
	secretsUtil *util.SecretsUtil,
	hubOperatorRamenConfig *rmn.RamenConfig) error {
	drClustersMutex.Lock()
	defer drClustersMutex.Unlock()

	for _, clusterName := range util.DrpolicyClusterNames(drpolicy) {
		if err := drClusterSecretsDeploy(clusterName, drpolicy, drclusters, secretsUtil, hubOperatorRamenConfig); err != nil {
			return err
		}
	}

	return nil
}

func drPolicyUndeploy(
	drpolicy *rmn.DRPolicy,
	drclusters *rmn.DRClusterList,
	secretsUtil *util.SecretsUtil,
	ramenConfig *rmn.RamenConfig) error {
	drpolicies := rmn.DRPolicyList{}

	drClustersMutex.Lock()
	defer drClustersMutex.Unlock()

	if err := secretsUtil.Client.List(secretsUtil.Ctx, &drpolicies); err != nil {
		return fmt.Errorf("drpolicies list: %w", err)
	}

	return drClustersUndeploySecrets(drpolicy, drclusters, drpolicies, secretsUtil, ramenConfig)
}

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

func drClusterListMustHaveS3Profiles(drpolicies rmn.DRPolicyList,
	drclusters *rmn.DRClusterList,
	clusterName string,
	ignorePolicy *rmn.DRPolicy) sets.String {
	mustHaveS3Profiles := sets.String{}

	for idx := range drpolicies.Items {
		// Skip the policy being ignored (used for delete)
		if (ignorePolicy != nil) && (ignorePolicy.ObjectMeta.Name == drpolicies.Items[idx].Name) {
			continue
		}

		for _, cluster := range util.DrpolicyClusterNames(&drpolicies.Items[idx]) {
			// Skip if not the current cluster
			if cluster != clusterName {
				continue
			}

			// Add all S3Profiles across clusters in this policy to the current cluster
			mustHaveS3Profiles = mustHaveS3Profiles.Union(util.DRPolicyS3Profiles(&drpolicies.Items[idx], drclusters.Items))

			break
		}
	}

	return mustHaveS3Profiles
}

// drClusterListMustHaveSecrets lists s3 secrets that must exist on the passed in clusterName
// It optionally ignores a specified ignorePolicy, which is typically useful when a policy is being
// deleted.
func drClusterListMustHaveSecrets(
	drpolicies rmn.DRPolicyList,
	drclusters *rmn.DRClusterList,
	clusterName string,
	ignorePolicy *rmn.DRPolicy,
	ramenConfig *rmn.RamenConfig) sets.String {
	mustHaveS3Secrets := sets.String{}

	mustHaveS3Profiles := drClusterListMustHaveS3Profiles(drpolicies, drclusters, clusterName, ignorePolicy)

	// Determine s3Secrets that must continue to exist on the cluster, based on other profiles
	// that should still be present. This is done as multiple profiles MAY point to the same secret
	for _, s3Profile := range ramenConfig.S3StoreProfiles {
		if mustHaveS3Profiles.Has(s3Profile.S3ProfileName) {
			mustHaveS3Secrets = mustHaveS3Secrets.Insert(s3Profile.S3SecretRef.Name)
		}
	}

	return mustHaveS3Secrets
}

func drClustersUndeploySecrets(
	drpolicy *rmn.DRPolicy,
	drclusters *rmn.DRClusterList,
	drpolicies rmn.DRPolicyList,
	secretsUtil *util.SecretsUtil,
	ramenConfig *rmn.RamenConfig) error {
	if !ramenConfig.DrClusterOperator.DeploymentAutomationEnabled ||
		!ramenConfig.DrClusterOperator.S3SecretDistributionEnabled {
		return nil
	}

	mustHaveS3Secrets := map[string]sets.String{}

	// Determine S3 secrets that must continue to exist per cluster in the policy being deleted
	for _, clusterName := range util.DrpolicyClusterNames(drpolicy) {
		mustHaveS3Secrets[clusterName] = drClusterListMustHaveSecrets(drpolicies, drclusters, clusterName,
			drpolicy, ramenConfig)
	}

	// Determine S3 secrets that maybe deleted, based on policy being deleted
	mayDeleteS3Secrets, err := drPolicySecretNames(drpolicy, drclusters, ramenConfig)
	if err != nil {
		return err
	}

	// For each cluster in the must have S3 secrets list, check and delete
	// S3Profiles that maybe deleted, iff absent in the must have list
	for clusterName, mustHaveS3Secrets := range mustHaveS3Secrets {
		for _, s3SecretToDelete := range mayDeleteS3Secrets.List() {
			if mustHaveS3Secrets.Has(s3SecretToDelete) {
				continue
			}

			// Delete s3profile secret from current cluster
			if err := secretsUtil.RemoveSecretFromCluster(s3SecretToDelete, clusterName, NamespaceName()); err != nil {
				return fmt.Errorf("drcluster '%v' s3Profile '%v' secrets delete: %w",
					clusterName, s3SecretToDelete, err)
			}
		}
	}

	return nil
}
