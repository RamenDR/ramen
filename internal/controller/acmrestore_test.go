// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	klusterletconfigv1alpha1 "github.com/stolostron/cluster-lifecycle-api/klusterletconfig/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/ramendr/ramen/internal/controller"
)

func TestEnsureKlusterletConfigForRestore(t *testing.T) {
	g := NewWithT(t)

	ctx := context.Background()

	newScheme := func() *runtime.Scheme {
		scheme := runtime.NewScheme()
		g.Expect(klusterletconfigv1alpha1.AddToScheme(scheme)).To(Succeed())

		return scheme
	}

	restoreGVK := schema.GroupVersionKind{
		Group:   "cluster.open-cluster-management.io",
		Version: "v1beta1",
		Kind:    "Restore",
	}

	// newRestores returns client.Object items, each carrying its GVK, that the fake
	// client can track and that EnsureKlusterletConfigForRestore lists as a RestoreList.
	newRestores := func(backups ...string) []client.Object {
		objs := make([]client.Object, 0, len(backups))

		for _, backup := range backups {
			item := &unstructured.Unstructured{}
			item.SetGroupVersionKind(restoreGVK)
			item.SetName("restore-" + backup)

			if err := unstructured.SetNestedField(item.Object, backup, "spec", "veleroManagedClustersBackupName"); err != nil {
				t.Fatalf("failed to set backup name: %v", err)
			}

			objs = append(objs, item)
		}

		return objs
	}

	t.Run("no Restore of managed clusters does nothing", func(t *testing.T) {
		g := NewWithT(t)
		scheme := newScheme()
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(newRestores("skip", "")...).Build()

		g.Expect(controllers.EnsureKlusterletConfigForRestore(ctx, c)).To(Succeed())

		kc := &klusterletconfigv1alpha1.KlusterletConfig{}
		err := c.Get(ctx, client.ObjectKey{Name: "global"}, kc)
		g.Expect(err).NotTo(BeNil())
		g.Expect(kc.Spec.AppliedManifestWorkEvictionGracePeriod).To(BeEmpty())
	})

	t.Run("Restore with managed clusters creates global KlusterletConfig", func(t *testing.T) {
		g := NewWithT(t)
		scheme := newScheme()
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(newRestores("latest")...).Build()

		g.Expect(controllers.EnsureKlusterletConfigForRestore(ctx, c)).To(Succeed())

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

		g.Expect(controllers.EnsureKlusterletConfigForRestore(ctx, c)).To(Succeed())

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

		g.Expect(controllers.EnsureKlusterletConfigForRestore(ctx, c)).To(Succeed())

		kc := &klusterletconfigv1alpha1.KlusterletConfig{}
		g.Expect(c.Get(ctx, client.ObjectKey{Name: "global"}, kc)).To(Succeed())
		g.Expect(kc.Spec.AppliedManifestWorkEvictionGracePeriod).To(Equal("24h"))
	})
}
