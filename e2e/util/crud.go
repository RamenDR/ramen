// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	channelv1 "open-cluster-management.io/multicloud-operators-channel/pkg/apis/apps/v1"
)

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
	}

	return nil
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
