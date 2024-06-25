// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/gomega"
	ocmv1 "github.com/open-cluster-management/api/cluster/v1"
	workv1 "github.com/open-cluster-management/api/work/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
)

func createManagedCluster(k8sClient client.Client, cluster string) {
	mc := ocmv1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: cluster,
		},
		Spec: ocmv1.ManagedClusterSpec{
			HubAcceptsClient: true,
		},
	}

	Expect(k8sClient.Create(context.TODO(), &mc)).To(Succeed())

	updateManagedClusterStatus(k8sClient, &mc)
}

func updateManagedClusterStatus(k8sClient client.Client, mc *ocmv1.ManagedCluster) {
	mc.Status = ocmv1.ManagedClusterStatus{
		Conditions: []metav1.Condition{
			{
				Type:               ocmv1.ManagedClusterConditionJoined,
				LastTransitionTime: metav1.Time{Time: time.Now()},
				Status:             metav1.ConditionTrue,
				Reason:             ocmv1.ManagedClusterConditionJoined,
				Message:            "Faked status",
			},
		},
		ClusterClaims: []ocmv1.ManagedClusterClaim{
			{
				Name:  "id.k8s.io",
				Value: "fake",
			},
		},
	}

	Expect(k8sClient.Status().Update(context.TODO(), mc)).To(Succeed())
}

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

func updateDRClusterManifestWorkStatus(k8sClient client.Client, apiReader client.Reader, clusterNamespace string) {
	drClusterkey := types.NamespacedName{
		Name:      util.DrClusterManifestWorkName,
		Namespace: clusterNamespace,
	}

	updateMWAsApplied(k8sClient, apiReader, drClusterkey)
}

func updateDRClusterConfigMWStatus(k8sClient client.Client, apiReader client.Reader, clusterNamespace string) {
	drClusterConfigkey := types.NamespacedName{
		Name:      fmt.Sprintf(util.ManifestWorkNameTypeFormat, util.MWTypeDRCConfig),
		Namespace: clusterNamespace,
	}

	updateMWAsApplied(k8sClient, apiReader, drClusterConfigkey)
}

func updateMWAsApplied(k8sClient client.Client, apiReader client.Reader, key types.NamespacedName) {
	mw := &workv1.ManifestWork{}

	Eventually(func() bool {
		err := apiReader.Get(context.TODO(), key, mw)

		return err == nil
	}, timeout, interval).Should(BeTrue(),
		fmt.Sprintf("failed to get manifest %s for DRCluster %s", key.Name, key.Namespace))

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
		err := apiReader.Get(context.TODO(), key, mw)
		if err != nil {
			return err
		}

		mw.Status = DRClusterStatusConditions

		return k8sClient.Status().Update(context.TODO(), mw)
	})

	Expect(retryErr).NotTo(HaveOccurred())
}
