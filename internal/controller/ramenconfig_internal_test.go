// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"testing"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
)

func TestCopyDrClusterOperatorSubscriptionFieldsEmpty(t *testing.T) {
	savedControllerType := ControllerType
	ControllerType = ramendrv1alpha1.DRHubType

	defer func() { ControllerType = savedControllerType }()

	dst := &ramendrv1alpha1.RamenConfig{}
	dst.DrClusterOperator.CatalogSourceName = "ramen-catalog"
	dst.DrClusterOperator.CatalogSourceNamespaceName = "olm"
	dst.DrClusterOperator.ChannelName = "alpha"
	dst.DrClusterOperator.PackageName = "ramen-dr-cluster-operator"
	dst.DrClusterOperator.ClusterServiceVersionName = "ramen-dr-cluster-operator.v0.1.0"
	dst.DrClusterOperator.NamespaceName = "ramen-system"

	src := DefaultRamenConfig(ramendrv1alpha1.DRHubType)
	if src.DrClusterOperator.CatalogSourceName != "" {
		t.Fatalf("precondition: expected empty CatalogSourceName in defaults, got %q",
			src.DrClusterOperator.CatalogSourceName)
	}

	dco := dst.DrClusterOperator
	if dco.CatalogSourceName != "ramen-catalog" {
		t.Errorf("CatalogSourceName = %q, want %q", dco.CatalogSourceName, "ramen-catalog")
	}

	if dco.CatalogSourceNamespaceName != "olm" {
		t.Errorf("CatalogSourceNamespaceName = %q, want %q", dco.CatalogSourceNamespaceName, "olm")
	}

	if dco.ChannelName != "alpha" {
		t.Errorf("ChannelName = %q, want %q", dco.ChannelName, "alpha")
	}

	if dco.PackageName != "ramen-dr-cluster-operator" {
		t.Errorf("PackageName = %q, want %q", dco.PackageName, "ramen-dr-cluster-operator")
	}

	if dco.ClusterServiceVersionName != "ramen-dr-cluster-operator.v0.1.0" {
		t.Errorf("ClusterServiceVersionName = %q, want %q", dco.ClusterServiceVersionName, "ramen-dr-cluster-operator.v0.1.0")
	}

	if dco.NamespaceName != "ramen-system" {
		t.Errorf("NamespaceName = %q, want %q", dco.NamespaceName, "ramen-system")
	}

	copyDrClusterOperatorSubscriptionFields(dst, src)

	if dst.DrClusterOperator.CatalogSourceName != "" {
		t.Errorf("CatalogSourceName = %q, want %q", dst.DrClusterOperator.CatalogSourceName, "")
	}
}

func TestCopyDrClusterOperatorSubscriptionFieldsFound(t *testing.T) {
	savedControllerType := ControllerType
	ControllerType = ramendrv1alpha1.DRHubType

	defer func() { ControllerType = savedControllerType }()

	dst := &ramendrv1alpha1.RamenConfig{}
	dst.DrClusterOperator.CatalogSourceName = "old-catalog"
	dst.DrClusterOperator.ChannelName = "stable"
	dst.DrClusterOperator.PackageName = "ramen-dr-cluster-operator"

	src := &ramendrv1alpha1.RamenConfig{}
	src.DrClusterOperator.CatalogSourceName = "ramen-catalog"
	src.DrClusterOperator.CatalogSourceNamespaceName = "olm"
	src.DrClusterOperator.ChannelName = "alpha"
	src.DrClusterOperator.PackageName = "ramen-dr-cluster-operator"
	src.DrClusterOperator.ClusterServiceVersionName = "ramen-dr-cluster-operator.v0.1.0"
	src.DrClusterOperator.NamespaceName = "ramen-system"

	copyDrClusterOperatorSubscriptionFields(dst, src)

	dco := dst.DrClusterOperator
	if dco.CatalogSourceName != "ramen-catalog" {
		t.Errorf("CatalogSourceName = %q, want %q", dco.CatalogSourceName, "ramen-catalog")
	}

	if dco.ChannelName != "alpha" {
		t.Errorf("ChannelName = %q, want %q", dco.ChannelName, "alpha")
	}
}

func TestApplyDrClusterOperatorFromSubscription(t *testing.T) {
	tests := []struct {
		name        string
		namespace   string
		pkg         string
		startingCSV string
		currentCSV  string
		wantPackage string
		wantCSV     string
	}{
		{
			name:        "upstream",
			namespace:   "ramen-system",
			pkg:         "ramen-hub-operator",
			startingCSV: "ramen-hub-operator.v0.0.1",
			currentCSV:  "ramen-hub-operator.v0.1.0",
			wantPackage: "ramen-dr-cluster-operator",
			wantCSV:     "ramen-dr-cluster-operator.v0.1.0",
		},
		{
			name:        "upstream without current CSV",
			namespace:   "ramen-system",
			pkg:         "ramen-hub-operator",
			startingCSV: "ramen-hub-operator.v0.1.0",
			wantPackage: "ramen-dr-cluster-operator",
			wantCSV:     "ramen-dr-cluster-operator.v0.1.0",
		},
		{
			name:        "openshift",
			namespace:   openshiftOperatorsNamespace,
			pkg:         "odr-hub-operator",
			startingCSV: "odr-hub-operator.v4.20.0",
			currentCSV:  "odr-hub-operator.v4.20.1",
			wantPackage: "odr-cluster-operator",
			wantCSV:     "odr-cluster-operator.v4.20.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub := &operatorsv1alpha1.Subscription{
				ObjectMeta: metav1.ObjectMeta{Namespace: tt.namespace},
				Spec: &operatorsv1alpha1.SubscriptionSpec{
					Package:                tt.pkg,
					Channel:                "alpha",
					CatalogSource:          "ramen-catalog",
					CatalogSourceNamespace: "olm",
					StartingCSV:            tt.startingCSV,
				},
				Status: operatorsv1alpha1.SubscriptionStatus{CurrentCSV: tt.currentCSV},
			}
			cfg := &ramendrv1alpha1.RamenConfig{}

			applyDrClusterOperatorFromSubscription(sub, cfg)

			dco := cfg.DrClusterOperator
			if dco.PackageName != tt.wantPackage {
				t.Errorf("PackageName = %q, want %q", dco.PackageName, tt.wantPackage)
			}

			if dco.ClusterServiceVersionName != tt.wantCSV {
				t.Errorf("ClusterServiceVersionName = %q, want %q", dco.ClusterServiceVersionName, tt.wantCSV)
			}

			if dco.ChannelName != "alpha" || dco.CatalogSourceName != "ramen-catalog" ||
				dco.CatalogSourceNamespaceName != "olm" {
				t.Errorf("channel/catalog not copied from subscription: %+v", dco)
			}
		})
	}
}
