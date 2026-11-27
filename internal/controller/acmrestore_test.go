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
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
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

func TestKlusterletConfigRestorePhase(t *testing.T) {
	ctx := context.Background()

	build := func(objs ...client.Object) client.Client {
		scheme := runtime.NewScheme()
		g := NewWithT(t)
		g.Expect(klusterletconfigv1alpha1.AddToScheme(scheme)).To(Succeed())
		g.Expect(restoresv1beta1.AddToScheme(scheme)).To(Succeed())

		return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	}

	restore := func(name, backup, phase string) *restoresv1beta1.Restore {
		return &restoresv1beta1.Restore{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       restoresv1beta1.RestoreSpec{VeleroManagedClustersBackupName: &backup},
			Status:     restoresv1beta1.RestoreStatus{Phase: restoresv1beta1.RestorePhase(phase)},
		}
	}

	run := func(c client.Client) error {
		err := controllers.EnsureKlusterletConfigForRestore(ctx, c, &rmn.RamenConfig{})
		if errors.Is(err, controllers.ErrNoManagedClustersRestore) {
			return nil
		}

		return err
	}

	getKC := func(c client.Client) (*klusterletconfigv1alpha1.KlusterletConfig, error) {
		kc := &klusterletconfigv1alpha1.KlusterletConfig{}
		err := c.Get(ctx, client.ObjectKey{Name: "global"}, kc)

		return kc, err
	}

	t.Run("phase matrix", func(t *testing.T) {
		tests := []struct {
			phase      string
			wantCreate bool
			wantErr    bool
		}{
			{"Started", true, false},
			{"Running", true, false},
			{"Unknown", true, false},
			{"", true, false},
			{"Enabled", true, false},
			{"EnabledWithErrors", true, false},
			{"Finished", false, false},
			{"FinishedWithErrors", false, false},
			{"Error", false, true},
		}

		for _, tt := range tests {
			t.Run(tt.phase, func(t *testing.T) {
				g := NewWithT(t)
				c := build(restore("r", "latest", tt.phase))

				err := run(c)
				if tt.wantErr {
					g.Expect(err).To(HaveOccurred())
				} else {
					g.Expect(err).NotTo(HaveOccurred())
				}

				_, err = getKC(c)
				g.Expect(err == nil).To(Equal(tt.wantCreate))
			})
		}
	})

	t.Run("a Restore with skip is ignored whatever its phase", func(t *testing.T) {
		g := NewWithT(t)
		c := build(restore("r", "skip", "Error"))

		g.Expect(run(c)).To(Succeed())

		_, err := getKC(c)
		g.Expect(k8serrors.IsNotFound(err)).To(BeTrue())
	})

	t.Run("Error takes precedence and leaves the KlusterletConfig unchanged", func(t *testing.T) {
		g := NewWithT(t)
		existing := &klusterletconfigv1alpha1.KlusterletConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "global"},
			Spec:       klusterletconfigv1alpha1.KlusterletConfigSpec{AppliedManifestWorkEvictionGracePeriod: "1h"},
		}
		c := build(restore("a", "latest", "Error"), restore("b", "latest", "Running"), existing)

		g.Expect(run(c)).NotTo(Succeed())

		kc, err := getKC(c)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(kc.Spec.AppliedManifestWorkEvictionGracePeriod).To(Equal("1h"))
	})

	t.Run("an active Restore among terminal ones keeps ensuring", func(t *testing.T) {
		g := NewWithT(t)
		c := build(restore("a", "latest", "Running"), restore("b", "latest", "Finished"))

		g.Expect(run(c)).To(Succeed())

		_, err := getKC(c)
		g.Expect(err).NotTo(HaveOccurred())
	})

	t.Run("a terminal Restore does not raise an existing KlusterletConfig again", func(t *testing.T) {
		g := NewWithT(t)
		existing := &klusterletconfigv1alpha1.KlusterletConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "global"},
			Spec:       klusterletconfigv1alpha1.KlusterletConfigSpec{AppliedManifestWorkEvictionGracePeriod: "1h"},
		}
		c := build(restore("r", "latest", "Finished"), existing)

		g.Expect(run(c)).To(Succeed())

		kc, err := getKC(c)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(kc.Spec.AppliedManifestWorkEvictionGracePeriod).To(Equal("1h"))
	})
}

func TestKlusterletConfigMarkers(t *testing.T) {
	ctx := context.Background()

	const (
		createdLabel = "ramendr.openshift.io/created-by-ramen"
		originalAnn  = "ramendr.openshift.io/klusterletconfig-original"
	)

	build := func(objs ...client.Object) client.Client {
		scheme := runtime.NewScheme()
		g := NewWithT(t)
		g.Expect(klusterletconfigv1alpha1.AddToScheme(scheme)).To(Succeed())
		g.Expect(restoresv1beta1.AddToScheme(scheme)).To(Succeed())

		return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	}

	running := func() *restoresv1beta1.Restore {
		backup := "latest"

		return &restoresv1beta1.Restore{
			ObjectMeta: metav1.ObjectMeta{Name: "r"},
			Spec:       restoresv1beta1.RestoreSpec{VeleroManagedClustersBackupName: &backup},
			Status:     restoresv1beta1.RestoreStatus{Phase: restoresv1beta1.RestorePhaseStarted},
		}
	}

	existing := func(period string, labels, annotations map[string]string) *klusterletconfigv1alpha1.KlusterletConfig {
		return &klusterletconfigv1alpha1.KlusterletConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "global", Labels: labels, Annotations: annotations},
			Spec:       klusterletconfigv1alpha1.KlusterletConfigSpec{AppliedManifestWorkEvictionGracePeriod: period},
		}
	}

	ensure := func(c client.Client, ramenConfig *rmn.RamenConfig) {
		NewWithT(t).Expect(controllers.EnsureKlusterletConfigForRestore(ctx, c, ramenConfig)).To(Succeed())
	}

	getKC := func(c client.Client) *klusterletconfigv1alpha1.KlusterletConfig {
		kc := &klusterletconfigv1alpha1.KlusterletConfig{}
		NewWithT(t).Expect(c.Get(ctx, client.ObjectKey{Name: "global"}, kc)).To(Succeed())

		return kc
	}

	t.Run("create sets the label to true and no original", func(t *testing.T) {
		g := NewWithT(t)
		c := build(running())

		ensure(c, &rmn.RamenConfig{})

		kc := getKC(c)
		g.Expect(kc.Labels).To(HaveKeyWithValue(createdLabel, "true"))
		g.Expect(kc.Annotations).NotTo(HaveKey(originalAnn))
	})

	t.Run("modify sets the label to false and records original", func(t *testing.T) {
		g := NewWithT(t)
		c := build(running(), existing("1h", map[string]string{"a": "b"}, map[string]string{"c": "d"}))

		ensure(c, &rmn.RamenConfig{})

		kc := getKC(c)
		g.Expect(kc.Spec.AppliedManifestWorkEvictionGracePeriod).To(Equal("24h"))
		g.Expect(kc.Labels).To(Equal(map[string]string{"a": "b", createdLabel: "false"}))
		g.Expect(kc.Annotations).To(Equal(map[string]string{"c": "d", originalAnn: "1h"}))
	})

	t.Run("original is written once", func(t *testing.T) {
		g := NewWithT(t)
		c := build(running(), existing("1h", nil, nil))

		ensure(c, &rmn.RamenConfig{})

		cfg := &rmn.RamenConfig{}
		cfg.HubRecovery.AppliedManifestWorkEvictionGracePeriod = "48h"
		ensure(c, cfg)

		kc := getKC(c)
		g.Expect(kc.Spec.AppliedManifestWorkEvictionGracePeriod).To(Equal("48h"))
		g.Expect(kc.Labels).To(HaveKeyWithValue(createdLabel, "false"))
		g.Expect(kc.Annotations).To(HaveKeyWithValue(originalAnn, "1h"))
	})

	t.Run("original may be the empty string", func(t *testing.T) {
		g := NewWithT(t)
		c := build(running(), existing("", nil, nil))

		ensure(c, &rmn.RamenConfig{})

		kc := getKC(c)
		g.Expect(kc.Annotations).To(HaveKeyWithValue(originalAnn, ""))
	})

	t.Run("a created config keeps the true label when the value changes", func(t *testing.T) {
		g := NewWithT(t)
		c := build(running())

		ensure(c, &rmn.RamenConfig{})

		cfg := &rmn.RamenConfig{}
		cfg.HubRecovery.AppliedManifestWorkEvictionGracePeriod = "48h"
		ensure(c, cfg)

		kc := getKC(c)
		g.Expect(kc.Spec.AppliedManifestWorkEvictionGracePeriod).To(Equal("48h"))
		g.Expect(kc.Labels).To(HaveKeyWithValue(createdLabel, "true"))
		g.Expect(kc.Annotations).NotTo(HaveKey(originalAnn))
	})

	t.Run("an unchanged config is not marked", func(t *testing.T) {
		g := NewWithT(t)
		c := build(running(), existing("24h", nil, nil))

		ensure(c, &rmn.RamenConfig{})

		kc := getKC(c)
		g.Expect(kc.Labels).To(BeEmpty())
		g.Expect(kc.Annotations).To(BeEmpty())
	})
}
