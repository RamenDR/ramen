// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	channelv1 "open-cluster-management.io/multicloud-operators-channel/pkg/apis/apps/v1"

	"github.com/ramendr/ramen/e2e/types"
)

func EnsureChannel(ctx types.Context) error {
	// create channel namespace
	err := CreateNamespace(ctx, ctx.Env().Hub, ctx.Config().Channel.Namespace)
	if err != nil {
		return err
	}

	return createChannel(ctx)
}

func EnsureChannelDeleted(ctx types.Context) error {
	if err := deleteChannel(ctx); err != nil {
		return err
	}

	return DeleteNamespace(ctx, ctx.Env().Hub, ctx.Config().Channel.Namespace)
}

func createChannel(ctx types.Context) error {
	hub := ctx.Env().Hub
	config := ctx.Config()
	log := ctx.Logger()

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

	err := hub.Client.Create(ctx.Context(), objChannel)
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

func deleteChannel(ctx types.Context) error {
	hub := ctx.Env().Hub
	config := ctx.Config()
	log := ctx.Logger()

	channel := &channelv1.Channel{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.Channel.Name,
			Namespace: config.Channel.Namespace,
		},
	}

	err := hub.Client.Delete(ctx.Context(), channel)
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
