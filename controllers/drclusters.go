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
	"context"
	"fmt"
	"sync"

	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	rmn "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/util"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func DrClustersDeployedSet(ctx context.Context, client client.Client, clusterNames *sets.String) error {
	manifestworks := &ocmworkv1.ManifestWorkList{}
	if err := client.List(ctx, manifestworks); err != nil {
		return fmt.Errorf("manifestworks list: %w", err)
	}

	for i := range manifestworks.Items {
		manifestwork := &manifestworks.Items[i]
		if manifestwork.ObjectMeta.Name == util.DrClusterManifestWorkName {
			*clusterNames = clusterNames.Insert(manifestwork.ObjectMeta.Namespace)
		}
	}

	return nil
}

var drClustersMutex sync.Mutex

func drClustersDeploy(drpolicy *rmn.DRPolicy, mwu *util.MWUtil, ramenConfig *rmn.RamenConfig) error {
	drClustersMutex.Lock()
	defer drClustersMutex.Unlock()

	for _, clusterName := range util.DrpolicyClusterNames(drpolicy) {
		if err := mwu.CreateOrUpdateDrClusterManifestWork(
			clusterName,
			ramenConfig,
			drClusterOperatorChannelNameOrDefault(ramenConfig),
			drClusterOperatorPackageNameOrDefault(ramenConfig),
			drClusterOperatorNamespaceNameOrDefault(ramenConfig),
			drClusterOperatorCatalogSourceNameOrDefault(ramenConfig),
			drClusterOperatorCatalogSourceNamespaceNameOrDefault(ramenConfig),
			drClusterOperatorClusterServiceVersionNameOrDefault(ramenConfig),
		); err != nil {
			return fmt.Errorf("drcluster '%v' manifest work create or update: %w", clusterName, err)
		}
	}

	return nil
}

func drClustersUndeploy(drpolicy *rmn.DRPolicy, mwu *util.MWUtil) error {
	drpolicies := rmn.DRPolicyList{}
	clusterNames := sets.String{}

	drClustersMutex.Lock()
	defer drClustersMutex.Unlock()

	if err := mwu.Client.List(mwu.Ctx, &drpolicies); err != nil {
		return fmt.Errorf("drpolicies list: %w", err)
	}

	for i := range drpolicies.Items {
		drpolicy1 := &drpolicies.Items[i]
		if drpolicy1.ObjectMeta.Name != drpolicy.ObjectMeta.Name {
			clusterNames = clusterNames.Insert(util.DrpolicyClusterNames(drpolicy1)...)
		}
	}

	for _, clusterName := range util.DrpolicyClusterNames(drpolicy) {
		if !clusterNames.Has(clusterName) {
			if err := mwu.DeleteManifestWork(util.DrClusterManifestWorkName, clusterName); err != nil {
				return fmt.Errorf("drcluster '%v' manifest work delete: %w", clusterName, err)
			}
		}
	}

	return nil
}
