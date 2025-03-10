// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"

	"go.uber.org/zap"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	channelv1 "open-cluster-management.io/multicloud-operators-channel/pkg/apis/apps/v1"

	"github.com/ramendr/ramen/e2e/config"
)

func EnsureChannel(log *zap.SugaredLogger) error {
	// create channel namespace
	err := CreateNamespace(Ctx.Hub, config.GetChannelNamespace(), log)
	if err != nil {
		return err
	}

	return createChannel(log)
}

func EnsureChannelDeleted(log *zap.SugaredLogger) error {
	if err := deleteChannel(log); err != nil {
		return err
	}

	return DeleteNamespace(Ctx.Hub, config.GetChannelNamespace(), log)
}

func createChannel(log *zap.SugaredLogger) error {
	objChannel := &channelv1.Channel{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.GetChannelName(),
			Namespace: config.GetChannelNamespace(),
		},
		Spec: channelv1.ChannelSpec{
			Pathname: config.GetGitURL(),
			Type:     channelv1.ChannelTypeGitHub,
		},
	}

	err := Ctx.Hub.Client.Create(context.Background(), objChannel)
	if err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return err
		}

		log.Debugf("Channel \"%s/%s\" already exists in cluster %q",
			config.GetChannelNamespace(), config.GetChannelName(), Ctx.Hub.Name)
	} else {
		log.Infof("Created channel \"%s/%s\" in cluster %q",
			config.GetChannelNamespace(), config.GetChannelName(), Ctx.Hub.Name)
	}

	return nil
}

func deleteChannel(log *zap.SugaredLogger) error {
	channel := &channelv1.Channel{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.GetChannelName(),
			Namespace: config.GetChannelNamespace(),
		},
	}

	err := Ctx.Hub.Client.Delete(context.Background(), channel)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}

		log.Debugf("Channel \"%s/%s\" not found in cluster %q",
			config.GetChannelNamespace(), config.GetChannelName(), Ctx.Hub.Name)
	} else {
		log.Infof("Deleted channel \"%s/%s\" in cluster %q",
			config.GetChannelNamespace(), config.GetChannelName(), Ctx.Hub.Name)
	}

	return nil
}
