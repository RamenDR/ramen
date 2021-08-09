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
	rmn "github.com/ramendr/ramen/api/v1alpha1"
)

// Return a list of unique S3 profiles to upload the relevant cluster state of
// the given home cluster
func s3UploadProfileList(drPolicy rmn.DRPolicy, homeCluster string) (s3ProfileList []string) {
	for _, drCluster := range drPolicy.Spec.DRClusterSet {
		if drCluster.Name != homeCluster {
			// This drCluster is not the home cluster and is hence, a candidate to
			// upload cluster state to if this S3 profile is not already on the list.
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
	}

	return
}

// Return the S3 profile to download the relevant cluster state to the given
// home cluster
func S3DownloadProfile(drPolicy rmn.DRPolicy, homeCluster string) (s3Profile string) {
	for _, drCluster := range drPolicy.Spec.DRClusterSet {
		if drCluster.Name == homeCluster {
			s3Profile = drCluster.S3ProfileName
		}
	}

	return
}
