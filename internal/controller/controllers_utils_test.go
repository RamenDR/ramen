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
	ocmv1 "open-cluster-management.io/api/cluster/v1"
	workv1 "open-cluster-management.io/api/work/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	controllers "github.com/ramendr/ramen/internal/controller"
	"github.com/ramendr/ramen/internal/controller/util"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
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

func objectConditionExpectEventually(
	apiReader client.Reader,
	obj client.Object,
	status metav1.ConditionStatus,
	reasonMatcher,
	messageMatcher gomegaTypes.GomegaMatcher,
	conditionType string,
	disabled ...bool,
) {
	switch objActual := obj.(type) {
	case *ramen.DRCluster:
		drclusterConditionExpect(
			apiReader,
			objActual,
			disabled[0],
			status,
			reasonMatcher,
			messageMatcher,
			conditionType,
			false,
		)
	case *ramen.DRClusterConfig:
		drclusterConfigConditionExpect(
			apiReader,
			objActual,
			status,
			reasonMatcher,
			messageMatcher,
			conditionType,
			false,
		)
	default:
		return
	}
}

func drclusterConfigConditionExpect(
	apiReader client.Reader,
	drclusterConfig *ramen.DRClusterConfig,
	status metav1.ConditionStatus,
	reasonMatcher,
	messageMatcher gomegaTypes.GomegaMatcher,
	conditionType string,
	always bool,
) {
	testFunc := func() []metav1.Condition {
		Expect(apiReader.Get(context.TODO(), types.NamespacedName{
			Namespace: drclusterConfig.Namespace,
			Name:      drclusterConfig.Name,
		}, drclusterConfig)).To(Succeed())

		return drclusterConfig.Status.Conditions
	}

	matchElements := MatchElements(
		func(element interface{}) string {
			cond, ok := element.(metav1.Condition)
			if !ok {
				return ""
			}

			return cond.Type
		},
		IgnoreExtras,
		Elements{
			conditionType: MatchAllFields(Fields{
				`Type`:               Ignore(),
				`Status`:             Equal(status),
				`ObservedGeneration`: Equal(drclusterConfig.Generation),
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
			cond, ok := element.(metav1.Condition)
			if !ok {
				return ""
			}

			return cond.Type
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

//nolint:unparam
func verifyDRClusterConfigMW(
	k8sClient client.Client,
	managedCluster, clusterID string,
	schedules []string,
	always bool,
) {
	mw := &workv1.ManifestWork{}

	testFunc := func() error {
		err := k8sClient.Get(
			context.TODO(),
			types.NamespacedName{
				Name:      fmt.Sprintf(util.ManifestWorkNameTypeFormat, util.MWTypeDRCConfig),
				Namespace: managedCluster,
			},
			mw,
		)
		if err != nil {
			return err
		}

		drcConfig, err := util.ExtractDRCConfigFromManifestWork(mw)
		if err != nil {
			return fmt.Errorf("error extracting ManifestWork from %v", mw)
		}

		if drcConfig.Spec.ClusterID != clusterID {
			return fmt.Errorf("clusterID mismatch, expected %s got %s", clusterID, drcConfig.Spec.ClusterID)
		}

		err = equalSet(schedules, drcConfig.Spec.ReplicationSchedules)

		return err
	}

	if always {
		Consistently(testFunc, timeout, interval).Should(Succeed())
	} else {
		Eventually(testFunc, timeout, interval).Should(Succeed())
	}
}

// equalSet determines if actual contains all and only all strings from desired
func equalSet(desired, actual []string) error {
	d := map[string]bool{}
	for _, value := range desired {
		d[value] = false
	}

	for _, value := range actual {
		if found, ok := d[value]; !ok || found {
			// Found a value not in desired map, or found a duplicate
			return fmt.Errorf("mismatch, desired %v actual %v", desired, actual)
		}

		d[value] = true
	}

	// Ensure that all desired strings are found
	for _, value := range d {
		if !value {
			return fmt.Errorf("mismatch, desired %v actual %v", desired, actual)
		}
	}

	return nil
}

func ensureDRClusterConfigMWNotFound(k8sClient client.Client, managedCluster string, always bool) {
	mw := &workv1.ManifestWork{}

	testFunc := func() bool {
		err := k8sClient.Get(
			context.TODO(),
			types.NamespacedName{
				Name:      fmt.Sprintf(util.ManifestWorkNameTypeFormat, util.MWTypeDRCConfig),
				Namespace: managedCluster,
			},
			mw,
		)

		return k8serrors.IsNotFound(err)
	}

	if always {
		Consistently(testFunc, timeout, interval).Should(BeTrue())
	} else {
		Eventually(testFunc, timeout, interval).Should(BeTrue())
	}
}
