// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func ObjectsMap[
	ObjectType any,
	ClientObject interface {
		*ObjectType
		client.Object
	},
](
	objects ...ObjectType,
) map[client.ObjectKey]ObjectType {
	m := make(map[client.ObjectKey]ObjectType, len(objects))

	for i := range objects {
		object := objects[i]
		m[client.ObjectKeyFromObject(ClientObject(&object))] = object
	}

	return m
}
