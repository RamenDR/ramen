// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"fmt"
	"sync"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/util"
	"k8s.io/apimachinery/pkg/util/sets"
)

var drClustersMutex sync.Mutex

func drPolicyDeploy(
	drpolicy *rmn.DRPolicy,
	drclusters *rmn.DRClusterList,
	secretsUtil *util.SecretsUtil,
	hubOperatorRamenConfig *rmn.RamenConfig,
) error {
	drClustersMutex.Lock()
	defer drClustersMutex.Unlock()

	for _, clusterName := range util.DrpolicyClusterNames(drpolicy) {
		if err := drClusterSecretsDeploy(clusterName, drpolicy, drclusters, secretsUtil, hubOperatorRamenConfig); err != nil {
			return err
		}
	}

	return nil
}

func drClusterSecretsDeploy(
	clusterName string,
	drpolicy *rmn.DRPolicy,
	drclusters *rmn.DRClusterList,
	secretsUtil *util.SecretsUtil,
	rmnCfg *rmn.RamenConfig,
) error {
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

func drPolicyUndeploy(
	drpolicy *rmn.DRPolicy,
	drclusters *rmn.DRClusterList,
	secretsUtil *util.SecretsUtil,
	ramenConfig *rmn.RamenConfig,
) error {
	drpolicies := rmn.DRPolicyList{}

	drClustersMutex.Lock()
	defer drClustersMutex.Unlock()

	if err := secretsUtil.Client.List(secretsUtil.Ctx, &drpolicies); err != nil {
		return fmt.Errorf("drpolicies list: %w", err)
	}

	return drClustersUndeploySecrets(drpolicy, drclusters, drpolicies, secretsUtil, ramenConfig)
}

func drClustersUndeploySecrets(
	drpolicy *rmn.DRPolicy,
	drclusters *rmn.DRClusterList,
	drpolicies rmn.DRPolicyList,
	secretsUtil *util.SecretsUtil,
	ramenConfig *rmn.RamenConfig,
) error {
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

func drPolicySecretNames(drpolicy *rmn.DRPolicy,
	drclusters *rmn.DRClusterList,
	rmnCfg *rmn.RamenConfig,
) (sets.String, error) {
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
