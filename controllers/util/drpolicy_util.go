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
	"errors"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
)

func DrpolicyClusterNames(drpolicy *rmn.DRPolicy) []string {
	clusterNames := make([]string, len(drpolicy.Spec.DRClusterSet))
	for i := range drpolicy.Spec.DRClusterSet {
		clusterNames[i] = drpolicy.Spec.DRClusterSet[i].Name
	}

	return clusterNames
}

func DrpolicyValidatedConditionGet(drpolicy *rmn.DRPolicy) *metav1.Condition {
	return meta.FindStatusCondition(drpolicy.Status.Conditions, rmn.DRPolicyValidated)
}

func DrpolicyValidated(drpolicy *rmn.DRPolicy) error {
	if condition := DrpolicyValidatedConditionGet(drpolicy); condition != nil {
		if condition.Status != metav1.ConditionTrue {
			return errors.New(condition.Message)
		}

		return nil
	}

	return errors.New(`validated condition absent`)
}

// Return a list of unique S3 profiles to upload the relevant cluster state
func S3UploadProfileList(drPolicy rmn.DRPolicy) (s3ProfileList []string) {
	for _, drCluster := range drPolicy.Spec.DRClusterSet {
		found := false

		for _, s3ProfileName := range s3ProfileList {
			if s3ProfileName == drCluster.S3ProfileName {
				found = true
			}
		}

		if !found {
			s3ProfileList = append(s3ProfileList, drCluster.S3ProfileName)
		}
	}

	return
}
