/*
Copyright 2021 The RamenDR authors.

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

package util

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
)

func DrpolicyClusterNames(drpolicy *rmn.DRPolicy) []string {
	return drpolicy.Spec.DRClusters
}

func DrpolicyRegionNames(drpolicy *rmn.DRPolicy, drClusters []rmn.DRCluster) []string {
	regionNames := make([]string, len(DrpolicyClusterNames(drpolicy)))

	for i, v := range DrpolicyClusterNames(drpolicy) {
		regionName := ""

		for _, drCluster := range drClusters {
			if drCluster.Name == v {
				regionName = string(drCluster.Spec.Region)
			}
		}

		regionNames[i] = regionName
	}

	return regionNames
}

func DrpolicyRegionNamesAsASet(drpolicy *rmn.DRPolicy, drClusters []rmn.DRCluster) sets.String {
	return sets.NewString(DrpolicyRegionNames(drpolicy, drClusters)...)
}

func DrpolicyValidated(drpolicy *rmn.DRPolicy) error {
	// TODO: What if the DRPolicy is deleted!
	// A deleted DRPolicy should not be applied to a new DRPC
	if condition := meta.FindStatusCondition(drpolicy.Status.Conditions, rmn.DRPolicyValidated); condition != nil {
		if condition.Status != metav1.ConditionTrue {
			return errors.New(condition.Message)
		}

		return nil
	}

	return errors.New(`validated condition absent`)
}

func GetAllDRPolicies(ctx context.Context, client client.Reader) (rmn.DRPolicyList, error) {
	drpolicies := rmn.DRPolicyList{}

	if err := client.List(ctx, &drpolicies); err != nil {
		return drpolicies, fmt.Errorf("unable to fetch drpolicies: %w", err)
	}

	return drpolicies, nil
}

func DRPolicyS3Profiles(drpolicy *rmn.DRPolicy, drclusters []rmn.DRCluster) sets.String {
	mustHaveS3Profiles := sets.String{}

	for _, managedCluster := range DrpolicyClusterNames(drpolicy) {
		s3ProfileName := ""

		for i := range drclusters {
			if drclusters[i].Name == managedCluster {
				s3ProfileName = drclusters[i].Spec.S3ProfileName
			}
		}

		mustHaveS3Profiles = mustHaveS3Profiles.Insert(s3ProfileName)
	}

	return mustHaveS3Profiles
}
