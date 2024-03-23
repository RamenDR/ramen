package imperativeapp_test

import (
	"context"
	"fmt"
	"time"

	ocmclusterv1beta1 "github.com/open-cluster-management-io/api/cluster/v1beta1"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func runWithTimeout(fn func() error, timeout time.Duration) error {
	timeoutTimer := time.NewTimer(timeout)
	defer timeoutTimer.Stop()

	delayTicker := time.NewTicker(timeout / 100)
	defer delayTicker.Stop()

	var lastErr error
	var tries int

	for {
		select {
		case <-timeoutTimer.C:
			// timed out
			if lastErr != nil {
				return lastErr
			}

			return fmt.Errorf("operation timed out after %s seconds and %d tries", timeout, tries)
		case <-delayTicker.C:
			// delay in progress
			continue
		default:
			err := fn()
			if err == nil {
				return nil
			}

			tries++
			lastErr = err
		}
	}
}

func disableOCMScheduler(hubclient client.Client, w *Workload) error {
	// We need to set the annotation on the placement
	// "cluster.open-cluster-management.io/experimental-scheduling-disable": "true",

	placement := &ocmclusterv1beta1.Placement{}

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := hubclient.Get(context.TODO(), client.ObjectKey{
			Name:      fmt.Sprintf("%s-placement", w.Name),
			Namespace: ramenOperandsNamespace,
		}, placement); err != nil {
			return err
		}

		if placement.Annotations == nil {
			placement.Annotations = map[string]string{}
		}

		placement.Annotations["cluster.open-cluster-management.io/experimental-scheduling-disable"] = "true"

		return hubclient.Update(context.TODO(), placement)
	})
	if err != nil {
		return err
	}

	return nil
}

func enableDR(hubclient client.Client, w *Workload) error {
	if err := createPlacement(hubclient, w); err != nil {
		return err
	}

	if err := waitForPlacementDecision(hubclient, w); err != nil {
		return err
	}

	if err := disableOCMScheduler(hubclient, w); err != nil {
		return err
	}

	if err := createDRPC(hubclient, w); err != nil {
		return err
	}

	return nil
}

func waitForPlacementDecision(k8sClient client.Client, w *Workload) error {
	placementDecision := &ocmclusterv1beta1.PlacementDecision{}

	err := runWithTimeout(func() error {
		return k8sClient.Get(context.TODO(), client.ObjectKey{
			Name:      fmt.Sprintf("%s-placement-decision-1", w.Name),
			Namespace: ramenOperandsNamespace,
		}, placementDecision)
	},
		60*time.Second)
	if err != nil {
		return err
	}

	return nil
}

func createPlacement(k8sclient client.Client, w *Workload) error {
	numberOfClusters := int32(1)

	placement := &ocmclusterv1beta1.Placement{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-placement", w.Name),
			Namespace: ramenOperandsNamespace,
			Labels:    map[string]string{"app": w.Name},
		},
		Spec: ocmclusterv1beta1.PlacementSpec{
			ClusterSets:      []string{"default"},
			NumberOfClusters: &numberOfClusters,
			PrioritizerPolicy: ocmclusterv1beta1.PrioritizerPolicy{
				Mode: "Additive",
			},
		},
	}

	return k8sclient.Create(context.TODO(), placement)
}

func createDRPC(k8sclient client.Client, w *Workload) error {
	// TODO: detect the type of workload and create drpc based on it
	// for now, it is always imperative workload and imperative drpc
	drpc := &ramendrv1alpha1.DRPlacementControl{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-drpc", w.Name),
			Namespace: ramenOperandsNamespace,
			Labels:    map[string]string{"app": w.Name},
		},
		Spec: ramendrv1alpha1.DRPlacementControlSpec{
			DRPolicyRef: corev1.ObjectReference{
				Name: "dr-policy",
			},
			FailoverCluster: "rdr-dr2",
			PlacementRef: corev1.ObjectReference{
				Kind:      "Placement",
				Name:      fmt.Sprintf("%s-placement", w.Name),
				Namespace: ramenOperandsNamespace,
			},
			PreferredCluster: "rdr-dr1",
			PVCSelector:      metav1.LabelSelector{},
			ProtectedNamespaces: []string{
				fmt.Sprintf("%s-namespace", w.Name),
			},
		},
	}

	return k8sclient.Create(context.TODO(), drpc)
}
