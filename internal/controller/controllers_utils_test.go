// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	gomegaTypes "github.com/onsi/gomega/types"
	ocmv1 "github.com/open-cluster-management/api/cluster/v1"
	workv1 "github.com/open-cluster-management/api/work/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	controllers "github.com/ramendr/ramen/internal/controller"
	"github.com/ramendr/ramen/internal/controller/util"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
)

func ensureManagedCluster(k8sClient client.Client, cluster string) {
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

func createManagedCluster(k8sClient client.Client, cluster string) *ocmv1.ManagedCluster {
	mc := ocmv1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: cluster,
		},
		Spec: ocmv1.ManagedClusterSpec{
			HubAcceptsClient: true,
		},
	}

	Expect(k8sClient.Create(context.TODO(), &mc)).To(Succeed())

	return &mc
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

func drclusterConditionExpectEventually(
	apiReader client.Reader,
	drcluster *ramen.DRCluster,
	disabled bool,
	status metav1.ConditionStatus,
	reasonMatcher,
	messageMatcher gomegaTypes.GomegaMatcher,
	conditionType string,
) {
	drclusterConditionExpect(
		apiReader,
		drcluster,
		disabled,
		status,
		reasonMatcher,
		messageMatcher,
		conditionType,
		false,
	)
}

func drclusterConditionExpectConsistently(
	apiReader client.Reader,
	drcluster *ramen.DRCluster,
	disabled bool,
	reasonMatcher,
	messageMatcher gomegaTypes.GomegaMatcher,
) {
	drclusterConditionExpect(
		apiReader,
		drcluster,
		disabled,
		metav1.ConditionTrue,
		reasonMatcher,
		messageMatcher,
		ramen.DRClusterValidated,
		true,
	)
}

func drclusterConditionExpect(
	apiReader client.Reader,
	drcluster *ramen.DRCluster,
	disabled bool,
	status metav1.ConditionStatus,
	reasonMatcher,
	messageMatcher gomegaTypes.GomegaMatcher,
	conditionType string,
	always bool,
) {
	testFunc := func() []metav1.Condition {
		Expect(apiReader.Get(context.TODO(), types.NamespacedName{
			Namespace: drcluster.Namespace,
			Name:      drcluster.Name,
		}, drcluster)).To(Succeed())

		return drcluster.Status.Conditions
	}

	matchElements := MatchElements(
		func(element interface{}) string {
			return element.(metav1.Condition).Type
		},
		IgnoreExtras,
		Elements{
			conditionType: MatchAllFields(Fields{
				`Type`:               Ignore(),
				`Status`:             Equal(status),
				`ObservedGeneration`: Equal(drcluster.Generation),
				`LastTransitionTime`: Ignore(),
				`Reason`:             reasonMatcher,
				`Message`:            messageMatcher,
			}),
		},
	)

	switch always {
	case false:
		Eventually(testFunc, timeout, interval).Should(matchElements)
	case true:
		Consistently(testFunc, timeout, interval).Should(matchElements)
	}

	// TODO: Validate finaliziers and labels
	if status == metav1.ConditionFalse {
		return
	}

	validateClusterManifest(apiReader, drcluster, disabled)
}

func validateClusterManifest(apiReader client.Reader, drcluster *ramen.DRCluster, disabled bool) {
	expectedCount := 12
	if disabled {
		expectedCount = 6
	}

	clusterName := drcluster.Name

	key := types.NamespacedName{
		Name:      util.DrClusterManifestWorkName,
		Namespace: clusterName,
	}

	manifestWork := &workv1.ManifestWork{}

	Eventually(
		func(g Gomega) []workv1.Manifest {
			g.Expect(apiReader.Get(context.TODO(), key, manifestWork)).To(Succeed())

			return manifestWork.Spec.Workload.Manifests
		}, timeout, interval,
	).Should(HaveLen(expectedCount))

	Expect(manifestWork.GetAnnotations()[controllers.DRClusterNameAnnotation]).Should(Equal(clusterName))
	// TODO: Validate fencing status
}

func verifyDRClusterConfigMW(k8sClient client.Client, managedCluster string) {
	mw := &workv1.ManifestWork{}

	Eventually(func() error {
		err := k8sClient.Get(
			context.TODO(),
			types.NamespacedName{
				Name:      fmt.Sprintf(util.ManifestWorkNameTypeFormat, util.MWTypeDRCConfig),
				Namespace: managedCluster,
			},
			mw,
		)

		return err
	}, timeout, interval).Should(BeNil())
	// TODO: Verify MW contents
}

func ensureDRClusterConfigMWNotFound(k8sClient client.Client, managedCluster string) {
	mw := &workv1.ManifestWork{}

	Consistently(func() bool {
		err := k8sClient.Get(
			context.TODO(),
			types.NamespacedName{
				Name:      fmt.Sprintf(util.ManifestWorkNameTypeFormat, util.MWTypeDRCConfig),
				Namespace: managedCluster,
			},
			mw,
		)

		return errors.IsNotFound(err)
	}, timeout, interval).Should(BeTrue())
}
