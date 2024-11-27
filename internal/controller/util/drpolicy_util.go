// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
)

func DRPolicyClusterNames(drpolicy *rmn.DRPolicy) []string {
	return drpolicy.Spec.DRClusters
}

func DRPolicyClusterNamesAsASet(drpolicy *rmn.DRPolicy) sets.String {
	return sets.NewString(DRPolicyClusterNames(drpolicy)...)
}

func DrpolicyRegionNames(drpolicy *rmn.DRPolicy, drClusters []rmn.DRCluster) []string {
	regionNames := make([]string, len(DRPolicyClusterNames(drpolicy)))

	for i, v := range DRPolicyClusterNames(drpolicy) {
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

	for _, managedCluster := range DRPolicyClusterNames(drpolicy) {
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

//nolint:mnd
func GetSecondsFromSchedulingInterval(drpolicy *rmn.DRPolicy) (float64, error) {
	schedulingInterval := drpolicy.Spec.SchedulingInterval
	if schedulingInterval == "" {
		return 0, nil
	}

	intervalFormat := schedulingInterval[len(schedulingInterval)-1:] // extracts m|h|d string
	interval := schedulingInterval[:len(schedulingInterval)-1]       // extracts numerical value of schedulingInterval
	dayInSeconds := 24 * 60 * 60

	switch intervalFormat {
	case "d":
		s, err := strconv.ParseFloat(interval, 64)

		return s * float64(dayInSeconds), err

	default:
		s, err := time.ParseDuration(schedulingInterval)

		return s.Seconds(), err
	}
}

func DrpolicyContainsDrcluster(drpolicy *rmn.DRPolicy, drcluster string) bool {
	for _, managedCluster := range DRPolicyClusterNames(drpolicy) {
		if managedCluster == drcluster {
			return true
		}
	}

	return false
}
