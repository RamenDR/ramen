// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	channelv1 "open-cluster-management.io/multicloud-operators-channel/pkg/apis/apps/v1"
)

func EnsureChannel() error {
	// create channel namespace
	err := CreateNamespace(Ctx.Hub, GetChannelNamespace(), Ctx.Log)
	if err != nil {
		return err
	}

	return createChannel()
}

func EnsureChannelDeleted() error {
	if err := deleteChannel(); err != nil {
		return err
	}

	return DeleteNamespace(Ctx.Hub, GetChannelNamespace(), Ctx.Log)
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

		Ctx.Log.Debugf("Channel \"%s/%s\" already exists", GetChannelNamespace(), GetChannelName())
	} else {
		Ctx.Log.Infof("Created channel \"%s/%s\"", GetChannelNamespace(), GetChannelName())
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

		Ctx.Log.Debugf("Channel \"%s/%s\" not found", GetChannelNamespace(), GetChannelName())
	} else {
		Ctx.Log.Infof("Deleted channel \"%s/%s\"", GetChannelNamespace(), GetChannelName())
	}

	return nil
}
