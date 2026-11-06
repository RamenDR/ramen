// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"errors"
	"testing"

	"github.com/go-logr/logr"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/yaml"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
)

type configMapSubscriptionCase struct {
	name             string
	controllerType   ramendrv1alpha1.ControllerType
	existing         *ramendrv1alpha1.RamenConfig
	withSubscription bool
	listFails        bool
	wantErr          bool
	wantCatalog      string
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

func TestApplyDrClusterOperatorDefaults(t *testing.T) {
	t.Setenv("POD_NAMESPACE", "ramen-system")

	cfg := &ramendrv1alpha1.RamenConfig{}
	if !drClusterOperatorFieldsEmpty(cfg) {
		t.Fatal("expected empty drClusterOperator fields")
	}

	applyDrClusterOperatorDefaults(cfg)

	dco := cfg.DrClusterOperator
	if dco.CatalogSourceName != drClusterOperatorCatalogSourceNameDefault {
		t.Errorf("CatalogSourceName = %q, want %q", dco.CatalogSourceName, drClusterOperatorCatalogSourceNameDefault)
	}

	if dco.CatalogSourceNamespaceName != "ramen-system" {
		t.Errorf("CatalogSourceNamespaceName = %q, want %q", dco.CatalogSourceNamespaceName, "ramen-system")
	}

	if dco.ChannelName != drClusterOperatorChannelNameDefault {
		t.Errorf("ChannelName = %q, want %q", dco.ChannelName, drClusterOperatorChannelNameDefault)
	}

	if dco.PackageName != drClusterOperatorPackageNameDefault {
		t.Errorf("PackageName = %q, want %q", dco.PackageName, drClusterOperatorPackageNameDefault)
	}

	if dco.ClusterServiceVersionName != drClusterOperatorClusterServiceVersionNameDefault {
		t.Errorf("ClusterServiceVersionName = %q, want %q",
			dco.ClusterServiceVersionName, drClusterOperatorClusterServiceVersionNameDefault)
	}

	if dco.NamespaceName != "ramen-system" {
		t.Errorf("NamespaceName = %q, want %q", dco.NamespaceName, "ramen-system")
	}

	if drClusterOperatorFieldsEmpty(cfg) {
		t.Error("expected drClusterOperator fields to be set")
	}
}

func TestCreateOrUpdateConfigMapSubscriptionLookup(t *testing.T) {
	const namespace = "ramen-system"

	savedControllerType := ControllerType

	t.Cleanup(func() { ControllerType = savedControllerType })
	t.Setenv("POD_NAMESPACE", namespace)

	scheme := configMapTestScheme(t)
	populated := populatedDrClusterConfig(namespace)
	subscription := hubOperatorSubscription(namespace)

	cases := append(configMapSubscriptionCases(populated), configMapSubscriptionErrorCases(populated)...)

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			assertConfigMapCatalog(t, scheme, namespace, subscription, tt)
		})
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

// Test helpers

func configMapTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	if err := operatorsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	return scheme
}

func populatedDrClusterConfig(namespace string) *ramendrv1alpha1.RamenConfig {
	cfg := &ramendrv1alpha1.RamenConfig{}
	cfg.DrClusterOperator.CatalogSourceName = "old-catalog"
	cfg.DrClusterOperator.CatalogSourceNamespaceName = "olm"
	cfg.DrClusterOperator.ChannelName = "stable"
	cfg.DrClusterOperator.PackageName = "ramen-dr-cluster-operator"
	cfg.DrClusterOperator.NamespaceName = namespace
	cfg.DrClusterOperator.ClusterServiceVersionName = "ramen-dr-cluster-operator.v0.0.1"

	return cfg
}

func hubOperatorSubscription(namespace string) *operatorsv1alpha1.Subscription {
	return &operatorsv1alpha1.Subscription{
		ObjectMeta: metav1.ObjectMeta{Name: "ramen-hub-operator", Namespace: namespace},
		Spec: &operatorsv1alpha1.SubscriptionSpec{
			Package:                "ramen-hub-operator",
			Channel:                "alpha",
			CatalogSource:          "ramen-catalog",
			CatalogSourceNamespace: "olm",
			StartingCSV:            "ramen-hub-operator.v0.1.0",
		},
	}
}

func configMapSubscriptionCases(populated *ramendrv1alpha1.RamenConfig) []configMapSubscriptionCase {
	return []configMapSubscriptionCase{
		{
			name:           "hub keeps existing catalog when subscription is missing",
			controllerType: ramendrv1alpha1.DRHubType,
			existing:       populated,
			wantCatalog:    "old-catalog",
		},
		{
			name:           "hub stores defaults when config is empty and subscription is missing",
			controllerType: ramendrv1alpha1.DRHubType,
			existing:       &ramendrv1alpha1.RamenConfig{},
			wantCatalog:    drClusterOperatorCatalogSourceNameDefault,
		},
		{
			name:           "hub stores defaults when configmap is created without a subscription",
			controllerType: ramendrv1alpha1.DRHubType,
			wantCatalog:    drClusterOperatorCatalogSourceNameDefault,
		},
		{
			name:             "hub copies catalog from the subscription",
			controllerType:   ramendrv1alpha1.DRHubType,
			existing:         populated,
			withSubscription: true,
			wantCatalog:      "ramen-catalog",
		},
		{
			name:           "dr cluster does not apply local defaults",
			controllerType: ramendrv1alpha1.DRClusterType,
			existing:       &ramendrv1alpha1.RamenConfig{},
			wantCatalog:    "",
		},
	}
}

// configMapSubscriptionErrorCases covers a failed Subscription list and an
// absent Subscription CRD (OLM not installed).
func configMapSubscriptionErrorCases(populated *ramendrv1alpha1.RamenConfig) []configMapSubscriptionCase {
	return []configMapSubscriptionCase{
		{
			name:           "hub keeps existing catalog when subscription list fails",
			controllerType: ramendrv1alpha1.DRHubType,
			existing:       populated,
			listFails:      true,
			wantCatalog:    "old-catalog",
		},
		{
			name:           "hub returns error when config is empty and subscription list fails",
			controllerType: ramendrv1alpha1.DRHubType,
			existing:       &ramendrv1alpha1.RamenConfig{},
			listFails:      true,
			wantErr:        true,
		},
		{
			name:           "hub returns error when configmap is missing and subscription list fails",
			controllerType: ramendrv1alpha1.DRHubType,
			listFails:      true,
			wantErr:        true,
		},
	}
}

func assertConfigMapCatalog(
	t *testing.T,
	scheme *runtime.Scheme,
	namespace string,
	subscription *operatorsv1alpha1.Subscription,
	tt configMapSubscriptionCase,
) {
	t.Helper()

	ControllerType = tt.controllerType
	fakeClient := configMapFakeClient(t, scheme, namespace, subscription, tt)

	_, err := CreateOrUpdateConfigMap(
		context.Background(), fakeClient, fakeClient, DefaultRamenConfig(tt.controllerType), logr.Discard(),
	)
	if tt.wantErr {
		if err == nil {
			t.Fatal("expected subscription list error")
		}

		return
	}

	if err != nil {
		t.Fatal(err)
	}

	got := ramenConfigFromClient(t, fakeClient, namespace)
	if got.DrClusterOperator.CatalogSourceName != tt.wantCatalog {
		t.Errorf("CatalogSourceName = %q, want %q", got.DrClusterOperator.CatalogSourceName, tt.wantCatalog)
	}
}

func configMapFakeClient(
	t *testing.T,
	scheme *runtime.Scheme,
	namespace string,
	subscription *operatorsv1alpha1.Subscription,
	tt configMapSubscriptionCase,
) client.Client {
	t.Helper()

	builder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		configMapCaseObjects(t, namespace, subscription, tt)...,
	)
	if tt.listFails {
		builder = builder.WithInterceptorFuncs(interceptor.Funcs{
			List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
				return errors.New("subscription list failed")
			},
		})
	}

	return builder.Build()
}

func configMapCaseObjects(
	t *testing.T,
	namespace string,
	subscription *operatorsv1alpha1.Subscription,
	tt configMapSubscriptionCase,
) []client.Object {
	t.Helper()

	if tt.existing == nil {
		return subscriptionObject(nil, subscription, tt.withSubscription)
	}

	configMapName := HubOperatorConfigMapName
	if tt.controllerType == ramendrv1alpha1.DRClusterType {
		configMapName = DrClusterOperatorConfigMapName
	}

	configMap, err := ConfigMapNew(namespace, configMapName, tt.existing)
	if err != nil {
		t.Fatal(err)
	}

	return subscriptionObject([]client.Object{configMap}, subscription, tt.withSubscription)
}

func subscriptionObject(
	objects []client.Object,
	subscription *operatorsv1alpha1.Subscription,
	include bool,
) []client.Object {
	if !include {
		return objects
	}

	return append(objects, subscription)
}

func ramenConfigFromClient(t *testing.T, apiReader client.Reader, namespace string) *ramendrv1alpha1.RamenConfig {
	t.Helper()

	gotConfigMap := &corev1.ConfigMap{}

	err := apiReader.Get(context.Background(), types.NamespacedName{
		Namespace: namespace,
		Name:      ramenOperatorConfigMapName(),
	}, gotConfigMap)
	if err != nil {
		t.Fatal(err)
	}

	got := &ramendrv1alpha1.RamenConfig{}
	if err := yaml.Unmarshal([]byte(gotConfigMap.Data[ConfigMapRamenConfigKeyName]), got); err != nil {
		t.Fatal(err)
	}

	return got
}
