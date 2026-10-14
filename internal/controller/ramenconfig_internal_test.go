// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"testing"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
)

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
