// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"

	"go.uber.org/zap"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	channelv1 "open-cluster-management.io/multicloud-operators-channel/pkg/apis/apps/v1"

	"github.com/ramendr/ramen/e2e/types"
)

func EnsureChannel(hub types.Cluster, config *types.Config, log *zap.SugaredLogger) error {
	// create channel namespace
	err := CreateNamespace(hub, config.Channel.Namespace, log)
	if err != nil {
		return err
	}

	return createChannel(hub, config, log)
}

func EnsureChannelDeleted(hub types.Cluster, config *types.Config, log *zap.SugaredLogger) error {
	if err := deleteChannel(hub, config, log); err != nil {
		return err
	}

	return DeleteNamespace(hub, config.Channel.Namespace, log)
}

func createChannel(hub types.Cluster, config *types.Config, log *zap.SugaredLogger) error {
	objChannel := &channelv1.Channel{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.Channel.Name,
			Namespace: config.Channel.Namespace,
		},
		Spec: channelv1.ChannelSpec{
			Pathname: config.Repo.URL,
			Type:     channelv1.ChannelTypeGitHub,
		},
	}

	err := hub.Client.Create(context.Background(), objChannel)
	if err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return err
		}

		log.Debugf("Channel \"%s/%s\" already exists in cluster %q",
			config.Channel.Namespace, config.Channel.Name, hub.Name)
	} else {
		log.Infof("Created channel \"%s/%s\" in cluster %q",
			config.Channel.Namespace, config.Channel.Name, hub.Name)
	}

	return nil
}

func deleteChannel(hub types.Cluster, config *types.Config, log *zap.SugaredLogger) error {
	channel := &channelv1.Channel{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.Channel.Name,
			Namespace: config.Channel.Namespace,
		},
	}

	err := hub.Client.Delete(context.Background(), channel)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}

		log.Debugf("Channel \"%s/%s\" not found in cluster %q",
			config.Channel.Namespace, config.Channel.Name, hub.Name)
	} else {
		log.Infof("Deleted channel \"%s/%s\" in cluster %q",
			config.Channel.Namespace, config.Channel.Name, hub.Name)
	}

	return nil
}
