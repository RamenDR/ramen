// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"

	"github.com/go-logr/logr"
	publicgroupsnapv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumegroupsnapshot/v1"
	groupsnapv1beta1 "github.com/red-hat-storage/external-snapshotter/client/v8/apis/volumegroupsnapshot/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
)

// OwnsVolumeGroupSnapshot registers Owns for the active VolumeGroupSnapshot API.
// Returns true if a VGS API CRD is installed and Owns was registered.
func OwnsVolumeGroupSnapshot(ctx context.Context, b *builder.Builder, apiReader client.Reader) bool {
	switch {
	case UsePublicVGSAPI(ctx, apiReader):
		b.Owns(&publicgroupsnapv1.VolumeGroupSnapshot{})

		return true
	case UsePrivateVGSAPI(ctx, apiReader):
		b.Owns(&groupsnapv1beta1.VolumeGroupSnapshot{})

		return true
	default:
		return false
	}
}

// WatchesVolumeGroupSnapshotClass registers Watches for the active VolumeGroupSnapshotClass API.
// Returns the builder unchanged if no VGS API CRD is installed.
func WatchesVolumeGroupSnapshotClass(
	ctx context.Context,
	b *builder.Builder,
	apiReader client.Reader,
	log logr.Logger,
	eventHandler handler.EventHandler,
	opts ...builder.WatchesOption,
) *builder.Builder {
	switch {
	case UsePublicVGSAPI(ctx, apiReader):
		log.Info("Public VolumeGroupSnapshotClass CRD detected, watching public API")

		return b.Watches(&publicgroupsnapv1.VolumeGroupSnapshotClass{}, eventHandler, opts...)
	case UsePrivateVGSAPI(ctx, apiReader):
		log.Info("Private VolumeGroupSnapshotClass CRD detected, watching private API")

		return b.Watches(&groupsnapv1beta1.VolumeGroupSnapshotClass{}, eventHandler, opts...)
	default:
		return b
	}
}
