// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"errors"
	"strings"
	"testing"

	publicgroupsnapv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumegroupsnapshot/v1"
	groupsnapv1beta1 "github.com/red-hat-storage/external-snapshotter/client/v8/apis/volumegroupsnapshot/v1beta1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func vgsCRD(name string, servedVersions ...string) *apiextensionsv1.CustomResourceDefinition {
	crd := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	for _, v := range servedVersions {
		crd.Spec.Versions = append(crd.Spec.Versions,
			apiextensionsv1.CustomResourceDefinitionVersion{Name: v, Served: true})
	}

	return crd
}

func crdGetter(crds ...*apiextensionsv1.CustomResourceDefinition) func(string) (
	*apiextensionsv1.CustomResourceDefinition, error,
) {
	return func(name string) (*apiextensionsv1.CustomResourceDefinition, error) {
		for _, crd := range crds {
			if crd.Name == name {
				return crd, nil
			}
		}

		return nil, nil
	}
}

// Selecting an API the installed CRD does not serve makes every informer on
// that GroupVersion fail cache sync, which kills the operator's manager about
// two minutes after startup. Detection must therefore verify the CRD serves
// the client's version, not merely that the CRD exists.
func TestResolveVGSPublicCRDServingOldVersionFallsBackToPrivate(t *testing.T) {
	gv, err := resolveVGSGroupVersion(crdGetter(
		vgsCRD(VGSCRDName, "v1beta1"), // public CRD present but stale: no v1
		vgsCRD(VGSCRDPrivateName, groupsnapv1beta1.SchemeGroupVersion.Version),
	))
	if err != nil {
		t.Fatal(err)
	}

	if gv != groupsnapv1beta1.SchemeGroupVersion {
		t.Fatalf("expected private fallback, got %s", gv)
	}
}

func TestResolveVGSPublicServingV1IsPreferred(t *testing.T) {
	gv, err := resolveVGSGroupVersion(crdGetter(
		vgsCRD(VGSCRDName, publicgroupsnapv1.SchemeGroupVersion.Version),
		vgsCRD(VGSCRDPrivateName, groupsnapv1beta1.SchemeGroupVersion.Version),
	))
	if err != nil {
		t.Fatal(err)
	}

	if gv != publicgroupsnapv1.SchemeGroupVersion {
		t.Fatalf("expected public API, got %s", gv)
	}
}

func TestResolveVGSPrivateOnly(t *testing.T) {
	gv, err := resolveVGSGroupVersion(crdGetter(
		vgsCRD(VGSCRDPrivateName, groupsnapv1beta1.SchemeGroupVersion.Version),
	))
	if err != nil {
		t.Fatal(err)
	}

	if gv != groupsnapv1beta1.SchemeGroupVersion {
		t.Fatalf("expected private API, got %s", gv)
	}
}

func TestResolveVGSVersionMismatchErrorNamesTheProblem(t *testing.T) {
	_, err := resolveVGSGroupVersion(crdGetter(
		vgsCRD(VGSCRDName, "v1beta1"), // present, wrong version, no private fallback
	))
	if err == nil {
		t.Fatal("expected an error")
	}

	for _, want := range []string{VGSCRDName, "v1beta1", publicgroupsnapv1.SchemeGroupVersion.Version} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should mention %q for debuggability, got: %v", want, err)
		}
	}
}

func TestResolveVGSNoCRDsErrors(t *testing.T) {
	_, err := resolveVGSGroupVersion(crdGetter())
	if err == nil {
		t.Fatal("expected an error when no VGS CRD is installed")
	}
}

func TestResolveVGSGetterErrorPropagates(t *testing.T) {
	boom := errors.New("boom")
	_, err := resolveVGSGroupVersion(func(string) (*apiextensionsv1.CustomResourceDefinition, error) {
		return nil, boom
	})

	if !errors.Is(err, boom) {
		t.Fatalf("expected wrapped getter error, got: %v", err)
	}
}
