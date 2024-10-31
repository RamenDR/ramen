// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ocmv1b1 "open-cluster-management.io/api/cluster/v1beta1"
	channelv1 "open-cluster-management.io/multicloud-operators-channel/pkg/apis/apps/v1"
	subscriptionv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/v1"
)

const DefaultDRPolicyName = "dr-policy"

func CreateNamespace(client client.Client, namespace string) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}

	err := client.Create(context.Background(), ns)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}
	}

	return nil
}

func DeleteNamespace(client client.Client, namespace string) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}

	err := client.Delete(context.Background(), ns)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}

		Ctx.Log.Info("namespace " + namespace + " not found")

		return nil
	}

	Ctx.Log.Info("waiting until namespace " + namespace + " is deleted")

	startTime := time.Now()
	key := types.NamespacedName{Name: namespace}

	for {
		if err := client.Get(context.Background(), key, ns); err != nil {
			if !errors.IsNotFound(err) {
				return err
			}

			Ctx.Log.Info("namespace " + namespace + " deleted")

			return nil
		}

		if time.Since(startTime) > 60*time.Second {
			return fmt.Errorf("timeout deleting namespace %q", namespace)
		}

		time.Sleep(time.Second)
	}
}

func createChannel() error {
	objChannel := &channelv1.Channel{
		ObjectMeta: metav1.ObjectMeta{
			Name:      GetChannelName(),
			Namespace: GetChannelNamespace(),
		},
		Spec: channelv1.ChannelSpec{
			Pathname: GetGitURL(),
			Type:     channelv1.ChannelTypeGitHub,
		},
	}

	err := Ctx.Hub.CtrlClient.Create(context.Background(), objChannel)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}

		Ctx.Log.Info("channel " + GetChannelName() + " already exists")
	} else {
		Ctx.Log.Info("channel " + GetChannelName() + " is created")
	}

	return nil
}

func deleteChannel() error {
	channel := &channelv1.Channel{
		ObjectMeta: metav1.ObjectMeta{
			Name:      GetChannelName(),
			Namespace: GetChannelNamespace(),
		},
	}

	err := Ctx.Hub.CtrlClient.Delete(context.Background(), channel)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}

		Ctx.Log.Info("channel " + GetChannelName() + " not found")
	} else {
		Ctx.Log.Info("channel " + GetChannelName() + " is deleted")
	}

	return nil
}

func EnsureChannel() error {
	// create channel namespace
	err := CreateNamespace(Ctx.Hub.CtrlClient, GetChannelNamespace())
	if err != nil {
		return err
	}

	return createChannel()
}

func EnsureChannelDeleted() error {
	if err := deleteChannel(); err != nil {
		return err
	}

	return DeleteNamespace(Ctx.Hub.CtrlClient, GetChannelNamespace())
}

// Problem: currently we must manually add an annotation to application’s namespace to make volsync work.
// See this link https://volsync.readthedocs.io/en/stable/usage/permissionmodel.html#controlling-mover-permissions
// Workaround: create ns in both drclusters and add annotation
func CreateNamespaceAndAddAnnotation(namespace string) error {
	if err := CreateNamespace(Ctx.C1.CtrlClient, namespace); err != nil {
		return err
	}

	if err := AddNamespaceAnnotationForVolSync(Ctx.C1.CtrlClient, namespace); err != nil {
		return err
	}

	if err := CreateNamespace(Ctx.C2.CtrlClient, namespace); err != nil {
		return err
	}

	return AddNamespaceAnnotationForVolSync(Ctx.C2.CtrlClient, namespace)
}

func AddNamespaceAnnotationForVolSync(client client.Client, namespace string) error {
	key := types.NamespacedName{Name: namespace}
	objNs := &corev1.Namespace{}

	if err := client.Get(context.Background(), key, objNs); err != nil {
		return err
	}

	annotations := objNs.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	annotations["volsync.backube/privileged-movers"] = "true"
	objNs.SetAnnotations(annotations)

	return client.Update(context.Background(), objNs)
}

// nolint:unparam
func GetDRPolicy(client client.Client, name string) (*ramen.DRPolicy, error) {
	drpolicy := &ramen.DRPolicy{}
	key := types.NamespacedName{Name: name}

	err := client.Get(context.Background(), key, drpolicy)
	if err != nil {
		return nil, err
	}

	return drpolicy, nil
}

func DeletePlacement(name, namespace string) error {
	placement := &ocmv1b1.Placement{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	err := Ctx.Hub.CtrlClient.Delete(context.Background(), placement)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}

		Ctx.Log.Info("placement " + name + " not found")
	}

	return nil
}

// Delete all the subscriptions that are associated with channel
func CleanUpWorkloads() error {
	channel := GetChannelNamespace() + "/" + GetChannelName()
	subList := &subscriptionv1.SubscriptionList{}
	err := Ctx.Hub.CtrlClient.List(context.Background(), subList)
	if err != nil {
		return err
	}
	for _, sub := range subList.Items {
		if sub.Spec.Channel == channel {
			// delete placement
			pName := sub.Spec.Placement.PlacementRef.Name
			pNamespace := sub.Namespace
			err = DeletePlacement(pName, pNamespace)
			if err != nil {
				Ctx.Log.Error(err, "error deleting placement")
				return err
			}
			// delete subscription
			err = DeleteSubscription(sub.Name, sub.Name)
			if err != nil {
				Ctx.Log.Error(err, "error deleting subscription")
				return err
			}
		}
	}
	return nil
}

func DeleteSubscription(name, ns string) error {
	subscription := &subscriptionv1.Subscription{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
	}

	err := Ctx.Hub.CtrlClient.Delete(context.Background(), subscription)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}

		Ctx.Log.Info("subscription " + name + " not found")
	}
	Ctx.Log.Info("subscription " + subscription.Name + " is deleted")

	return nil
}
