// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"
	restoresv1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	klusterletconfigv1alpha1 "github.com/stolostron/cluster-lifecycle-api/klusterletconfig/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller"
)

func TestEnsureKlusterletConfigForRestore(t *testing.T) {
	g := NewWithT(t)

	ctx := context.Background()

	newScheme := func() *runtime.Scheme {
		scheme := runtime.NewScheme()
		g.Expect(klusterletconfigv1alpha1.AddToScheme(scheme)).To(Succeed())
		g.Expect(restoresv1beta1.AddToScheme(scheme)).To(Succeed())

		return scheme
	}

	// ensure runs EnsureKlusterletConfigForRestore and ignores ErrNoManagedClustersRestore.
	ensure := func(c client.Client, ramenConfig *rmn.RamenConfig) error {
		err := controllers.EnsureKlusterletConfigForRestore(ctx, c, ramenConfig)
		if errors.Is(err, controllers.ErrNoManagedClustersRestore) {
			return nil
		}

		return err
	}

	// newRestores returns Restore objects with the given veleroManagedClustersBackupName values.
	// An empty value leaves the field unset.
	newRestores := func(backups ...string) []client.Object {
		objs := make([]client.Object, 0, len(backups))

		for _, backup := range backups {
			item := &restoresv1beta1.Restore{ObjectMeta: metav1.ObjectMeta{Name: "restore-" + backup}}
			if backup != "" {
				item.Spec.VeleroManagedClustersBackupName = &backup
			}

			objs = append(objs, item)
		}

		return objs
	}

	t.Run("no Restore of managed clusters does nothing", func(t *testing.T) {
		g := NewWithT(t)
		scheme := newScheme()
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(newRestores("skip", "")...).Build()

		g.Expect(ensure(c, &rmn.RamenConfig{})).To(Succeed())

		kc := &klusterletconfigv1alpha1.KlusterletConfig{}
		err := c.Get(ctx, client.ObjectKey{Name: "global"}, kc)
		g.Expect(err).NotTo(BeNil())
		g.Expect(kc.Spec.AppliedManifestWorkEvictionGracePeriod).To(BeEmpty())
	})

	t.Run("reports whether a Restore of managed clusters was ensured", func(t *testing.T) {
		g := NewWithT(t)

		c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(newRestores("skip", "")...).Build()
		err := controllers.EnsureKlusterletConfigForRestore(ctx, c, &rmn.RamenConfig{})
		g.Expect(err).To(MatchError(controllers.ErrNoManagedClustersRestore))

		c = fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(newRestores("latest")...).Build()
		g.Expect(controllers.EnsureKlusterletConfigForRestore(ctx, c, &rmn.RamenConfig{})).To(Succeed())
	})

	t.Run("Restore with managed clusters creates global KlusterletConfig", func(t *testing.T) {
		g := NewWithT(t)
		scheme := newScheme()
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(newRestores("latest")...).Build()

		g.Expect(ensure(c, &rmn.RamenConfig{})).To(Succeed())

		kc := &klusterletconfigv1alpha1.KlusterletConfig{}
		g.Expect(c.Get(ctx, client.ObjectKey{Name: "global"}, kc)).To(Succeed())
		g.Expect(kc.Spec.AppliedManifestWorkEvictionGracePeriod).To(Equal("24h"))
	})

	t.Run("existing global KlusterletConfig with different grace period is updated", func(t *testing.T) {
		g := NewWithT(t)
		scheme := newScheme()
		existing := &klusterletconfigv1alpha1.KlusterletConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "global"},
			Spec: klusterletconfigv1alpha1.KlusterletConfigSpec{
				AppliedManifestWorkEvictionGracePeriod: "1h",
			},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(append(newRestores("latest"), existing)...).Build()

		g.Expect(ensure(c, &rmn.RamenConfig{})).To(Succeed())

		kc := &klusterletconfigv1alpha1.KlusterletConfig{}
		g.Expect(c.Get(ctx, client.ObjectKey{Name: "global"}, kc)).To(Succeed())
		g.Expect(kc.Spec.AppliedManifestWorkEvictionGracePeriod).To(Equal("24h"))
	})

	t.Run("existing global KlusterletConfig already correct is untouched", func(t *testing.T) {
		g := NewWithT(t)
		scheme := newScheme()
		existing := &klusterletconfigv1alpha1.KlusterletConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "global"},
			Spec: klusterletconfigv1alpha1.KlusterletConfigSpec{
				AppliedManifestWorkEvictionGracePeriod: "24h",
			},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(append(newRestores("latest"), existing)...).Build()

		g.Expect(ensure(c, &rmn.RamenConfig{})).To(Succeed())

		kc := &klusterletconfigv1alpha1.KlusterletConfig{}
		g.Expect(c.Get(ctx, client.ObjectKey{Name: "global"}, kc)).To(Succeed())
		g.Expect(kc.Spec.AppliedManifestWorkEvictionGracePeriod).To(Equal("24h"))
	})

	t.Run("nil Ramen config creates a missing KlusterletConfig with the default", func(t *testing.T) {
		g := NewWithT(t)
		c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(newRestores("latest")...).Build()

		g.Expect(ensure(c, nil)).To(Succeed())

		kc := &klusterletconfigv1alpha1.KlusterletConfig{}
		g.Expect(c.Get(ctx, client.ObjectKey{Name: "global"}, kc)).To(Succeed())
		g.Expect(kc.Spec.AppliedManifestWorkEvictionGracePeriod).To(Equal("24h"))
	})

	t.Run("nil Ramen config leaves an existing KlusterletConfig untouched", func(t *testing.T) {
		g := NewWithT(t)
		existing := &klusterletconfigv1alpha1.KlusterletConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "global"},
			Spec: klusterletconfigv1alpha1.KlusterletConfigSpec{
				AppliedManifestWorkEvictionGracePeriod: "48h",
			},
		}
		c := fake.NewClientBuilder().WithScheme(newScheme()).
			WithObjects(append(newRestores("latest"), existing)...).Build()

		g.Expect(ensure(c, nil)).To(Succeed())

		kc := &klusterletconfigv1alpha1.KlusterletConfig{}
		g.Expect(c.Get(ctx, client.ObjectKey{Name: "global"}, kc)).To(Succeed())
		g.Expect(kc.Spec.AppliedManifestWorkEvictionGracePeriod).To(Equal("48h"))
	})

	t.Run("configured, invalid and INFINITE grace periods", func(t *testing.T) {
		tests := []struct {
			name       string
			configured string
			want       string
		}{
			{"configured value is written", "90m", "90m"},
			{"INFINITE is accepted", "INFINITE", "INFINITE"},
			{"invalid value falls back to default", "1d", "24h"},
			{"lowercase infinite falls back to default", "infinite", "24h"},
			{"numeric without unit falls back to default", "24", "24h"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				g := NewWithT(t)
				c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(newRestores("latest")...).Build()

				ramenConfig := &rmn.RamenConfig{}
				ramenConfig.HubRecovery.AppliedManifestWorkEvictionGracePeriod = tt.configured

				g.Expect(ensure(c, ramenConfig)).To(Succeed())

				kc := &klusterletconfigv1alpha1.KlusterletConfig{}
				g.Expect(c.Get(ctx, client.ObjectKey{Name: "global"}, kc)).To(Succeed())
				g.Expect(kc.Spec.AppliedManifestWorkEvictionGracePeriod).To(Equal(tt.want))
			})
		}
	})

	t.Run("changed configured value updates existing KlusterletConfig", func(t *testing.T) {
		g := NewWithT(t)
		c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(newRestores("latest")...).Build()

		g.Expect(ensure(c, &rmn.RamenConfig{})).To(Succeed())

		ramenConfig := &rmn.RamenConfig{}
		ramenConfig.HubRecovery.AppliedManifestWorkEvictionGracePeriod = "48h"

		g.Expect(ensure(c, ramenConfig)).To(Succeed())

		kc := &klusterletconfigv1alpha1.KlusterletConfig{}
		g.Expect(c.Get(ctx, client.ObjectKey{Name: "global"}, kc)).To(Succeed())
		g.Expect(kc.Spec.AppliedManifestWorkEvictionGracePeriod).To(Equal("48h"))
	})

	t.Run("missing Restore CRD returns nil without error", func(t *testing.T) {
		g := NewWithT(t)
		scheme := newScheme()
		restMapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{})
		c := fake.NewClientBuilder().WithScheme(scheme).WithRESTMapper(restMapper).Build()

		g.Expect(ensure(c, &rmn.RamenConfig{})).To(Succeed())
	})
}
