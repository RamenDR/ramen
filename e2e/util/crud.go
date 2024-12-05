// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	channelv1 "open-cluster-management.io/multicloud-operators-channel/pkg/apis/apps/v1"
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

func DeleteNamespace(client client.Client, namespace string, log *zap.SugaredLogger) error {
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

		log.Infof("Namespace %q not found", namespace)

		return nil
	}

	log.Infof("Waiting until namespace %q is deleted", namespace)

	startTime := time.Now()
	key := types.NamespacedName{Name: namespace}

	for {
		if err := client.Get(context.Background(), key, ns); err != nil {
			if !errors.IsNotFound(err) {
				return err
			}

			log.Infof("Namespace %q deleted", namespace)

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

	err := Ctx.Hub.Client.Create(context.Background(), objChannel)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}

		Ctx.Log.Infof("Channel %q already exists", GetChannelName())
	} else {
		Ctx.Log.Infof("Created channel %q", GetChannelName())
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

	err := Ctx.Hub.Client.Delete(context.Background(), channel)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}

		Ctx.Log.Infof("Channel %q not found", GetChannelName())
	} else {
		Ctx.Log.Infof("Channel %q is deleted", GetChannelName())
	}

	return nil
}

func EnsureChannel() error {
	// create channel namespace
	err := CreateNamespace(Ctx.Hub.Client, GetChannelNamespace())
	if err != nil {
		return err
	}

	return createChannel()
}

func EnsureChannelDeleted() error {
	if err := deleteChannel(); err != nil {
		return err
	}

	return DeleteNamespace(Ctx.Hub.Client, GetChannelNamespace(), Ctx.Log)
}

// Problem: currently we must manually add an annotation to application’s namespace to make volsync work.
// See this link https://volsync.readthedocs.io/en/stable/usage/permissionmodel.html#controlling-mover-permissions
// Workaround: create ns in both drclusters and add annotation
func CreateNamespaceAndAddAnnotation(namespace string) error {
	if err := CreateNamespace(Ctx.C1.Client, namespace); err != nil {
		return err
	}

	if err := AddNamespaceAnnotationForVolSync(Ctx.C1.Client, namespace); err != nil {
		return err
	}

	if err := CreateNamespace(Ctx.C2.Client, namespace); err != nil {
		return err
	}

	return AddNamespaceAnnotationForVolSync(Ctx.C2.Client, namespace)
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
