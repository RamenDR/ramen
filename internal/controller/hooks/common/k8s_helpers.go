// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package common

import (
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
)

// PrepareMapForDefinedTypes creates a GVK map for common resource types
func PrepareMapForDefinedTypes() map[string]schema.GroupVersionKind {
	gvkMap := make(map[string]schema.GroupVersionKind)
	gvkMap["pod"] = schema.GroupVersionKind{
		Group:   "",
		Version: "v1",
		Kind:    "Pod",
	}
	gvkMap["deployment"] = schema.GroupVersionKind{
		Group:   "apps",
		Version: "v1",
		Kind:    "Deployment",
	}
	gvkMap["statefulset"] = schema.GroupVersionKind{
		Group:   "apps",
		Version: "v1",
		Kind:    "StatefulSet",
	}

	return gvkMap
}

// GetRestMapper creates a REST mapper for API discovery
func GetRestMapper() (meta.RESTMapper, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	dc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, err
	}

	apiGroupResources, err := restmapper.GetAPIGroupResources(dc)
	if err != nil {
		return nil, err
	}

	return restmapper.NewDiscoveryRESTMapper(apiGroupResources), nil
}

// ConvertGVRToGVK converts GroupVersionResource to GroupVersionKind
func ConvertGVRToGVK(mapper meta.RESTMapper, gvr schema.GroupVersionResource) (*schema.GroupVersionKind, error) {
	gvk, err := mapper.KindFor(gvr)
	if err != nil {
		return nil, err
	}

	return &gvk, nil
}
