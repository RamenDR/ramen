// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/gomega"
	workv1 "github.com/open-cluster-management/api/work/v1"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
)

func getLatestDRCluster(cluster string) *ramen.DRCluster {
	drclusterLookupKey := types.NamespacedName{
		Name: cluster,
	}

	latestDRCluster := &ramen.DRCluster{}
	err := apiReader.Get(context.TODO(), drclusterLookupKey, latestDRCluster)
	Expect(err).NotTo(HaveOccurred())

	return latestDRCluster
}

func updateDRClusterParameters(drc *ramen.DRCluster) *ramen.DRCluster {
	key := types.NamespacedName{Name: drc.Name}
	latestdrc := &ramen.DRCluster{}

	retryErr := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		err := apiReader.Get(context.TODO(), key, latestdrc)
		if err != nil {
			return err
		}

		latestdrc.Spec.ClusterFence = drc.Spec.ClusterFence
		latestdrc.Spec.S3ProfileName = drc.Spec.S3ProfileName
		latestdrc.Spec.CIDRs = drc.Spec.CIDRs

		return k8sClient.Update(context.TODO(), latestdrc)
	})

	Expect(retryErr).NotTo(HaveOccurred())

	return latestdrc
}

func updateDRClusterManifestWorkStatus(clusterNamespace string) {
	manifestLookupKey := types.NamespacedName{
		Name:      util.DrClusterManifestWorkName,
		Namespace: clusterNamespace,
	}
	mw := &workv1.ManifestWork{}

	Eventually(func() bool {
		err := apiReader.Get(context.TODO(), manifestLookupKey, mw)

		return err == nil
	}, timeout, interval).Should(BeTrue(),
		fmt.Sprintf("failed to get manifest for DRCluster %s", clusterNamespace))

	timeOld := time.Now().Local()
	timeMostRecent := timeOld.Add(time.Second)
	DRClusterStatusConditions := workv1.ManifestWorkStatus{
		Conditions: []metav1.Condition{
			{
				Type:               workv1.WorkAvailable,
				LastTransitionTime: metav1.Time{Time: timeMostRecent},
				Status:             metav1.ConditionTrue,
				Reason:             "ResourceAvailable",
				Message:            "All resources are available",
			},
			{
				Type:               workv1.WorkApplied,
				LastTransitionTime: metav1.Time{Time: timeMostRecent},
				Status:             metav1.ConditionTrue,
				Reason:             "AppliedManifestworkComplete",
				Message:            "Apply Manifest Work Complete",
			},
		},
	}

	retryErr := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		err := apiReader.Get(context.TODO(), manifestLookupKey, mw)
		if err != nil {
			return err
		}

		mw.Status = DRClusterStatusConditions

		return k8sClient.Status().Update(context.TODO(), mw)
	})

	Expect(retryErr).NotTo(HaveOccurred())
}
