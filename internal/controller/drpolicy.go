// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"fmt"
	"sync"

	"github.com/go-logr/logr"
	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	rmn "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/util"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	cpcv1 "open-cluster-management.io/config-policy-controller/api/v1"
)

var drClustersMutex sync.Mutex

const ClusterRoleAggregateLabel = "open-cluster-management.io/aggregate-to-work"

func propagateS3Secret(
	drpolicy *rmn.DRPolicy,
	drclusters *rmn.DRClusterList,
	secretsUtil *util.SecretsUtil,
	hubOperatorRamenConfig *rmn.RamenConfig,
	log logr.Logger,
) error {
	drClustersMutex.Lock()
	defer drClustersMutex.Unlock()

	for _, clusterName := range util.DRPolicyClusterNames(drpolicy) {
		if err := drClusterSecretsDeploy(clusterName, drpolicy, drclusters, secretsUtil,
			hubOperatorRamenConfig, log); err != nil {
			return err
		}
	}

	return nil
}

//nolint:cyclop
func drClusterSecretsDeploy(
	clusterName string,
	drpolicy *rmn.DRPolicy,
	drclusters *rmn.DRClusterList,
	secretsUtil *util.SecretsUtil,
	rmnCfg *rmn.RamenConfig,
	log logr.Logger,
) error {
	if !rmnCfg.DrClusterOperator.DeploymentAutomationEnabled ||
		!rmnCfg.DrClusterOperator.S3SecretDistributionEnabled {
		return nil
	}

	drPolicySecrets, err := drPolicySecretNames(drpolicy, drclusters, rmnCfg)
	if err != nil {
		// For cluster deploy, it is ok to deploy only what available so far.
		// Fail it only if no secrets are available.
		if len(drPolicySecrets) == 0 {
			return err
		}

		log.Info("Received partial list", "err", err)
	}

	objects, err := drClusterPolicyObjectsToDeploy(rmnCfg)
	if err != nil {
		return fmt.Errorf("failed to deploy DRClusterPolicy objects: %w", err)
	}

	for _, secretName := range drPolicySecrets.List() {
		if err := secretsUtil.AddSecretToCluster(
			secretName,
			clusterName,
			RamenOperatorNamespace(),
			drClusterOperatorNamespaceNameOrDefault(rmnCfg),
			objects,
			util.SecretFormatRamen,
			"",
		); err != nil {
			return fmt.Errorf("cannot add secret '%v' to drcluster '%v': %w", secretName, clusterName, err)
		}

		if !rmnCfg.KubeObjectProtection.Disabled && rmnCfg.KubeObjectProtection.VeleroNamespaceName != "" {
			if err := secretsUtil.AddSecretToCluster(
				secretName,
				clusterName,
				RamenOperatorNamespace(),
				drClusterOperatorNamespaceNameOrDefault(rmnCfg),
				objects,
				util.SecretFormatVelero,
				rmnCfg.KubeObjectProtection.VeleroNamespaceName,
			); err != nil {
				return fmt.Errorf("cannot add secret '%v' to drcluster '%v' in format '%v': %w",
					secretName, clusterName, util.SecretFormatVelero, err)
			}
		}
	}

	return nil
}

func toUnstructuredWithoutStatus(obj runtime.Object) (*unstructured.Unstructured, error) {
	u := &unstructured.Unstructured{}

	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, err
	}

	delete(m, "status")

	u.SetUnstructuredContent(m)

	return u, nil
}

func drClusterPolicyObjectsToDeploy(
	hubOperatorRamenConfig *rmn.RamenConfig,
) ([]*cpcv1.ObjectTemplate, error) {
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
		return nil, fmt.Errorf("failed to create ConfigMap for DRClusterOperator: %w", err)
	}

	operatorGroupObject := operatorGroup(drClusterOperatorNamespaceName)

	unstructuredObj, err := toUnstructuredWithoutStatus(operatorGroupObject)
	if err != nil {
		return nil, fmt.Errorf("failed to convert OperatorGroup to unstructured object: %w", err)
	}

	objects := []*cpcv1.ObjectTemplate{
		{
			ComplianceType:   cpcv1.MustHave,
			ObjectDefinition: runtime.RawExtension{Object: util.Namespace(drClusterOperatorNamespaceName)},
		},
		{
			ComplianceType:   cpcv1.MustHave,
			ObjectDefinition: runtime.RawExtension{Object: olmClusterRole},
		},
		{
			ComplianceType:   cpcv1.MustHave,
			ObjectDefinition: runtime.RawExtension{Object: unstructuredObj},
		},
		{
			ComplianceType:   cpcv1.MustHave,
			ObjectDefinition: runtime.RawExtension{Object: drClusterOperatorConfigMap},
		},
		{
			ComplianceType:   cpcv1.MustHave,
			ObjectDefinition: runtime.RawExtension{Object: vrgClusterRole},
		},
		{
			ComplianceType:   cpcv1.MustHave,
			ObjectDefinition: runtime.RawExtension{Object: mModeClusterRole},
		},
	}

	if drClusterOperatorRamenConfig.RamenOpsNamespace != "" {
		objects = append(objects, &cpcv1.ObjectTemplate{
			ComplianceType:   cpcv1.MustHave,
			ObjectDefinition: runtime.RawExtension{Object: util.Namespace(drClusterOperatorRamenConfig.RamenOpsNamespace)},
		})
	}

	return objects, nil
}

func drPolicyUndeploy(
	drpolicy *rmn.DRPolicy,
	drclusters *rmn.DRClusterList,
	secretsUtil *util.SecretsUtil,
	ramenConfig *rmn.RamenConfig,
	log logr.Logger,
) error {
	drpolicies := rmn.DRPolicyList{}

	drClustersMutex.Lock()
	defer drClustersMutex.Unlock()

	if err := secretsUtil.Client.List(secretsUtil.Ctx, &drpolicies); err != nil {
		return fmt.Errorf("drpolicies list: %w", err)
	}

	return drClustersUndeploySecrets(drpolicy, drclusters, drpolicies, secretsUtil, ramenConfig, log)
}

func drClustersUndeploySecrets(
	drpolicy *rmn.DRPolicy,
	drclusters *rmn.DRClusterList,
	drpolicies rmn.DRPolicyList,
	secretsUtil *util.SecretsUtil,
	ramenConfig *rmn.RamenConfig,
	log logr.Logger,
) error {
	if !ramenConfig.DrClusterOperator.DeploymentAutomationEnabled ||
		!ramenConfig.DrClusterOperator.S3SecretDistributionEnabled {
		return nil
	}

	mustHaveS3Secrets := map[string]sets.String{}

	// Determine S3 secrets that must continue to exist per cluster in the policy being deleted
	for _, clusterName := range util.DRPolicyClusterNames(drpolicy) {
		mustHaveS3Secrets[clusterName] = drClusterListMustHaveSecrets(drpolicies, drclusters, clusterName,
			drpolicy, ramenConfig)
	}

	// Determine S3 secrets that maybe deleted, based on policy being deleted
	mayDeleteS3Secrets, err := drPolicySecretNames(drpolicy, drclusters, ramenConfig)
	if err != nil {
		log.Error(err, "error in retrieving secret names")
	}

	// For each cluster in the must have S3 secrets list, check and delete
	// S3Profiles that maybe deleted, iff absent in the must have list
	for clusterName, mustHaveS3Secrets := range mustHaveS3Secrets {
		for _, s3SecretToDelete := range mayDeleteS3Secrets.List() {
			if mustHaveS3Secrets.Has(s3SecretToDelete) {
				continue
			}

			// Delete s3profile secret from current cluster
			if err := deleteSecretFromCluster(s3SecretToDelete, clusterName, ramenConfig, secretsUtil); err != nil {
				return err
			}
		}
	}

	return nil
}

// drClusterListMustHaveSecrets lists s3 secrets that must exist on the passed in clusterName
// It optionally ignores a specified ignorePolicy, which is typically useful when a policy is being
// deleted.
func drClusterListMustHaveSecrets(
	drpolicies rmn.DRPolicyList,
	drclusters *rmn.DRClusterList,
	clusterName string,
	ignorePolicy *rmn.DRPolicy,
	ramenConfig *rmn.RamenConfig,
) sets.String {
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

func drClusterListMustHaveS3Profiles(drpolicies rmn.DRPolicyList,
	drclusters *rmn.DRClusterList,
	clusterName string,
	ignorePolicy *rmn.DRPolicy,
) sets.String {
	mustHaveS3Profiles := sets.String{}

	for idx := range drpolicies.Items {
		// Skip the policy being ignored (used for delete)
		if (ignorePolicy != nil) && (ignorePolicy.ObjectMeta.Name == drpolicies.Items[idx].Name) {
			continue
		}

		if util.DrpolicyContainsDrcluster(&drpolicies.Items[idx], clusterName) {
			// Add all S3Profiles across clusters in this policy to the current cluster
			mustHaveS3Profiles = mustHaveS3Profiles.Union(util.DRPolicyS3Profiles(&drpolicies.Items[idx], drclusters.Items))
		}
	}

	return mustHaveS3Profiles
}

func drPolicySecretNames(drpolicy *rmn.DRPolicy,
	drclusters *rmn.DRClusterList,
	rmnCfg *rmn.RamenConfig,
) (sets.String, error) {
	secretNames := sets.String{}

	var err error

	for _, managedCluster := range util.DRPolicyClusterNames(drpolicy) {
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
			err = fmt.Errorf("missing profile name (%s) in config for DRCluster (%s)", s3ProfileName, managedCluster)
		}
	}

	return secretNames, err
}

// Delete s3profile secret from cluster
func deleteSecretFromCluster(
	s3SecretToDelete, clusterName string,
	ramenConfig *rmn.RamenConfig,
	secretsUtil *util.SecretsUtil,
) error {
	if err := secretsUtil.RemoveSecretFromCluster(
		s3SecretToDelete,
		clusterName,
		RamenOperatorNamespace(),
		util.SecretFormatRamen,
	); err != nil {
		return fmt.Errorf("unable to delete secret in format '%v' for s3Profile '%v' on drcluster '%v': %w",
			util.SecretFormatRamen, s3SecretToDelete, clusterName, err)
	}

	if !ramenConfig.KubeObjectProtection.Disabled && ramenConfig.KubeObjectProtection.VeleroNamespaceName != "" {
		if err := secretsUtil.RemoveSecretFromCluster(
			s3SecretToDelete,
			clusterName,
			RamenOperatorNamespace(),
			util.SecretFormatVelero,
		); err != nil {
			return fmt.Errorf("unable to delete secret in format '%v' for s3Profile '%v' on drcluster '%v': %w",
				util.SecretFormatRamen, s3SecretToDelete, clusterName, err)
		}
	}

	return nil
}

func operatorGroup(namespaceName string) *operatorsv1.OperatorGroup {
	return &operatorsv1.OperatorGroup{
		TypeMeta:   metav1.TypeMeta{Kind: "OperatorGroup", APIVersion: "operators.coreos.com/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "ramen-operator-group", Namespace: namespaceName},
		Spec:       operatorsv1.OperatorGroupSpec{UpgradeStrategy: operatorsv1.UpgradeStrategyDefault},
	}
}

var (
	olmClusterRole = &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{Kind: "ClusterRole", APIVersion: "rbac.authorization.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management:klusterlet-work-sa:agent:olm-edit",
			Labels: map[string]string{
				ClusterRoleAggregateLabel: "true",
			},
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"operators.coreos.com"},
				Resources: []string{"operatorgroups"},
				Verbs:     []string{"create", "get", "list", "update", "delete"},
			},
		},
	}

	vrgClusterRole = &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{Kind: "ClusterRole", APIVersion: "rbac.authorization.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management:klusterlet-work-sa:agent:volrepgroup-edit",
			Labels: map[string]string{
				ClusterRoleAggregateLabel: "true",
			},
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"ramendr.openshift.io"},
				Resources: []string{"volumereplicationgroups"},
				Verbs:     []string{"create", "get", "list", "update", "delete"},
			},
		},
	}

	mModeClusterRole = &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{Kind: "ClusterRole", APIVersion: "rbac.authorization.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management:klusterlet-work-sa:agent:mmode-edit",
			Labels: map[string]string{
				ClusterRoleAggregateLabel: "true",
			},
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"ramendr.openshift.io"},
				Resources: []string{"maintenancemodes"},
				Verbs:     []string{"create", "get", "list", "update", "delete"},
			},
		},
	}

	drClusterConfigRole = &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{Kind: "ClusterRole", APIVersion: "rbac.authorization.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management:klusterlet-work-sa:agent:drclusterconfig-edit",
			Labels: map[string]string{
				ClusterRoleAggregateLabel: "true",
			},
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"ramendr.openshift.io"},
				Resources: []string{"drclusterconfigs"},
				Verbs:     []string{"create", "get", "list", "update", "delete"},
			},
		},
	}
)
