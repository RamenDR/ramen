// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

// This package is a workaround to not depend on argoCD go.mod, as that brings in the entire k8s as a dependency
// causing several valid and invalid security warnings, and in general polluting the ramen module dependencies
// as well. The issue this workaround package overcomes is: https://github.com/argoproj/argo-cd/issues/4055

// This version of the API is as per ArgoCD version:
//  https://github.com/argoproj/argo-cd/blob/release-2.10/pkg/apis/application/v1alpha1/applicationset_types.go

package argocdv1alpha1hack

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Below are just the bare minimum required fields that ramen uses for an ApplicationSet resource

type ApplicationSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
	Items           []ApplicationSet `json:"items" protobuf:"bytes,2,rep,name=items"`
}

type ApplicationSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata" protobuf:"bytes,1,opt,name=metadata"`
	Spec              ApplicationSetSpec   `json:"spec" protobuf:"bytes,2,opt,name=spec"`
	Status            ApplicationSetStatus `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}

type ApplicationSetSpec struct {
	Generators []ApplicationSetGenerator `json:"generators" protobuf:"bytes,2,name=generators"`
	Template   ApplicationSetTemplate    `json:"template" protobuf:"bytes,3,name=template"`
}

type ApplicationSetGenerator struct {
	//nolint:lll
	ClusterDecisionResource *DuckTypeGenerator `json:"clusterDecisionResource,omitempty" protobuf:"bytes,5,name=clusterDecisionResource"`
	// Matrix combines two generators by taking the cartesian product of their parameters.
	// The clusterDecisionResource generator may appear as one of the nested generators.
	Matrix *MatrixGenerator `json:"matrix,omitempty" protobuf:"bytes,10,name=matrix"`
	// Merge combines generators by merging their parameters.
	// The clusterDecisionResource generator may appear as one of the nested generators.
	Merge *MergeGenerator `json:"merge,omitempty" protobuf:"bytes,11,name=merge"`
}

// MatrixGenerator is the subset of the ArgoCD MatrixGenerator that Ramen cares about:
// the list of nested generators whose clusterDecisionResource entries must be inspected.
type MatrixGenerator struct {
	Generators []ApplicationSetNestedGenerator `json:"generators" protobuf:"bytes,1,name=generators"`
}

// MergeGenerator is the subset of the ArgoCD MergeGenerator that Ramen cares about.
type MergeGenerator struct {
	Generators []ApplicationSetNestedGenerator `json:"generators" protobuf:"bytes,1,name=generators"`
}

// ApplicationSetNestedGenerator is a generator that may appear inside a Matrix or Merge combinator.
// It holds only the fields that Ramen inspects.
type ApplicationSetNestedGenerator struct {
	//nolint:lll
	ClusterDecisionResource *DuckTypeGenerator `json:"clusterDecisionResource,omitempty" protobuf:"bytes,5,name=clusterDecisionResource"`
}

type DuckTypeGenerator struct {
	ConfigMapRef  string               `json:"configMapRef" protobuf:"bytes,1,name=configMapRef"`
	LabelSelector metav1.LabelSelector `json:"labelSelector,omitempty" protobuf:"bytes,4,name=labelSelector"`
}

type ApplicationSetTemplate struct {
	ApplicationSetTemplateMeta `json:"metadata" protobuf:"bytes,1,name=metadata"`
	Spec                       ApplicationSpec `json:"spec" protobuf:"bytes,2,name=spec"`
}

type ApplicationSetTemplateMeta struct{}

type ApplicationSpec struct {
	Destination ApplicationDestination `json:"destination" protobuf:"bytes,2,name=destination"`
	Project     string                 `json:"project" protobuf:"bytes,3,name=project"`
}

type ApplicationDestination struct {
	Namespace string `json:"namespace,omitempty" protobuf:"bytes,2,opt,name=namespace"`
}

type ApplicationSetStatus struct{}

// DeepCopy### interfaces required to use ApplicationSet as a runtime.Object
// Lifted from generated deep copy file for other resources

func (in *ApplicationSet) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *ApplicationSet) DeepCopy() *ApplicationSet {
	if in == nil {
		return nil
	}

	out := new(ApplicationSet)

	in.DeepCopyInto(out)

	return out
}

func (in *ApplicationSet) DeepCopyInto(out *ApplicationSet) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	out.Status = in.Status
}

func (in *ApplicationSetSpec) DeepCopyInto(out *ApplicationSetSpec) {
	*out = *in

	if in.Generators != nil {
		in, out := &in.Generators, &out.Generators
		*out = make([]ApplicationSetGenerator, len(*in))

		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}

	out.Template = in.Template
}

func (in *DuckTypeGenerator) DeepCopyInto(out *DuckTypeGenerator) {
	*out = *in
	in.LabelSelector.DeepCopyInto(&out.LabelSelector)
}

func (in *ApplicationSetNestedGenerator) DeepCopyInto(out *ApplicationSetNestedGenerator) {
	*out = *in

	if in.ClusterDecisionResource != nil {
		in, out := &in.ClusterDecisionResource, &out.ClusterDecisionResource
		*out = new(DuckTypeGenerator)
		(*in).DeepCopyInto(*out)
	}
}

func (in *ApplicationSetGenerator) DeepCopyInto(out *ApplicationSetGenerator) {
	*out = *in

	if in.ClusterDecisionResource != nil {
		in, out := &in.ClusterDecisionResource, &out.ClusterDecisionResource
		*out = new(DuckTypeGenerator)
		(*in).DeepCopyInto(*out)
	}

	if in.Matrix != nil {
		in, out := &in.Matrix, &out.Matrix
		*out = new(MatrixGenerator)
		(*in).DeepCopyInto(*out)
	}

	if in.Merge != nil {
		in, out := &in.Merge, &out.Merge
		*out = new(MergeGenerator)
		(*in).DeepCopyInto(*out)
	}
}

func (in *MatrixGenerator) DeepCopyInto(out *MatrixGenerator) {
	*out = *in

	if in.Generators != nil {
		in, out := &in.Generators, &out.Generators
		*out = make([]ApplicationSetNestedGenerator, len(*in))

		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

func (in *MergeGenerator) DeepCopyInto(out *MergeGenerator) {
	*out = *in

	if in.Generators != nil {
		in, out := &in.Generators, &out.Generators
		*out = make([]ApplicationSetNestedGenerator, len(*in))

		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy### interfaces required to use ApplicationSetList as a runtime.Object
// Lifted from generated deep copy file for other resources
func (in *ApplicationSetList) DeepCopyInto(out *ApplicationSetList) {
	*out = *in
	out.TypeMeta = in.TypeMeta

	in.ListMeta.DeepCopyInto(&out.ListMeta)

	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]ApplicationSet, len(*in))

		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

func (in *ApplicationSetList) DeepCopy() *ApplicationSetList {
	if in == nil {
		return nil
	}

	out := new(ApplicationSetList)
	in.DeepCopyInto(out)

	return out
}

func (in *ApplicationSetList) DeepCopyObject() runtime.Object {
	c := in.DeepCopy()

	return c
}

// Schema registration helpers, with appropriate Group/Version/Kind values from the ArgoCD project

var (
	SchemeGroupVersion = schema.GroupVersion{Group: "argoproj.io", Version: "v1alpha1"}
	SchemeBuilder      = runtime.NewSchemeBuilder(addKnownTypes)
	AddToScheme        = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion,
		&ApplicationSet{},
		&ApplicationSetList{},
	)
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)

	return nil
}
