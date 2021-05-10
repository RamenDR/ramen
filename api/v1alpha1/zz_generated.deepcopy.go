// +build !ignore_autogenerated

/*
Copyright 2021 The RamenDR authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Code generated by controller-gen. DO NOT EDIT.

package v1alpha1

import (
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ApplicationVolumeReplication) DeepCopyInto(out *ApplicationVolumeReplication) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ApplicationVolumeReplication.
func (in *ApplicationVolumeReplication) DeepCopy() *ApplicationVolumeReplication {
	if in == nil {
		return nil
	}
	out := new(ApplicationVolumeReplication)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ApplicationVolumeReplication) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ApplicationVolumeReplicationList) DeepCopyInto(out *ApplicationVolumeReplicationList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]ApplicationVolumeReplication, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ApplicationVolumeReplicationList.
func (in *ApplicationVolumeReplicationList) DeepCopy() *ApplicationVolumeReplicationList {
	if in == nil {
		return nil
	}
	out := new(ApplicationVolumeReplicationList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ApplicationVolumeReplicationList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ApplicationVolumeReplicationSpec) DeepCopyInto(out *ApplicationVolumeReplicationSpec) {
	*out = *in
	in.SubscriptionSelector.DeepCopyInto(&out.SubscriptionSelector)
	out.DRClusterPeersRef = in.DRClusterPeersRef
	in.PVCSelector.DeepCopyInto(&out.PVCSelector)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ApplicationVolumeReplicationSpec.
func (in *ApplicationVolumeReplicationSpec) DeepCopy() *ApplicationVolumeReplicationSpec {
	if in == nil {
		return nil
	}
	out := new(ApplicationVolumeReplicationSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ApplicationVolumeReplicationStatus) DeepCopyInto(out *ApplicationVolumeReplicationStatus) {
	*out = *in
	if in.Decisions != nil {
		in, out := &in.Decisions, &out.Decisions
		*out = make(SubscriptionPlacementDecisionMap, len(*in))
		for key, val := range *in {
			var outVal *SubscriptionPlacementDecision
			if val == nil {
				(*out)[key] = nil
			} else {
				in, out := &val, &outVal
				*out = new(SubscriptionPlacementDecision)
				**out = **in
			}
			(*out)[key] = outVal
		}
	}
	if in.LastKnownDRStates != nil {
		in, out := &in.LastKnownDRStates, &out.LastKnownDRStates
		*out = make(LastKnownDRStateMap, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	in.LastUpdateTime.DeepCopyInto(&out.LastUpdateTime)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ApplicationVolumeReplicationStatus.
func (in *ApplicationVolumeReplicationStatus) DeepCopy() *ApplicationVolumeReplicationStatus {
	if in == nil {
		return nil
	}
	out := new(ApplicationVolumeReplicationStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ClusterID) DeepCopyInto(out *ClusterID) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = in.Spec
	out.Status = in.Status
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ClusterID.
func (in *ClusterID) DeepCopy() *ClusterID {
	if in == nil {
		return nil
	}
	out := new(ClusterID)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ClusterID) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ClusterIDList) DeepCopyInto(out *ClusterIDList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]ClusterID, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ClusterIDList.
func (in *ClusterIDList) DeepCopy() *ClusterIDList {
	if in == nil {
		return nil
	}
	out := new(ClusterIDList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ClusterIDList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ClusterIDSpec) DeepCopyInto(out *ClusterIDSpec) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ClusterIDSpec.
func (in *ClusterIDSpec) DeepCopy() *ClusterIDSpec {
	if in == nil {
		return nil
	}
	out := new(ClusterIDSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ClusterIDStatus) DeepCopyInto(out *ClusterIDStatus) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ClusterIDStatus.
func (in *ClusterIDStatus) DeepCopy() *ClusterIDStatus {
	if in == nil {
		return nil
	}
	out := new(ClusterIDStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *DRClusterPeers) DeepCopyInto(out *DRClusterPeers) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	if in.Status != nil {
		in, out := &in.Status, &out.Status
		*out = new(DRClusterPeersStatus)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new DRClusterPeers.
func (in *DRClusterPeers) DeepCopy() *DRClusterPeers {
	if in == nil {
		return nil
	}
	out := new(DRClusterPeers)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *DRClusterPeers) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *DRClusterPeersList) DeepCopyInto(out *DRClusterPeersList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]DRClusterPeers, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new DRClusterPeersList.
func (in *DRClusterPeersList) DeepCopy() *DRClusterPeersList {
	if in == nil {
		return nil
	}
	out := new(DRClusterPeersList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *DRClusterPeersList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *DRClusterPeersReference) DeepCopyInto(out *DRClusterPeersReference) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new DRClusterPeersReference.
func (in *DRClusterPeersReference) DeepCopy() *DRClusterPeersReference {
	if in == nil {
		return nil
	}
	out := new(DRClusterPeersReference)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *DRClusterPeersSpec) DeepCopyInto(out *DRClusterPeersSpec) {
	*out = *in
	if in.ClusterNames != nil {
		in, out := &in.ClusterNames, &out.ClusterNames
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new DRClusterPeersSpec.
func (in *DRClusterPeersSpec) DeepCopy() *DRClusterPeersSpec {
	if in == nil {
		return nil
	}
	out := new(DRClusterPeersSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *DRClusterPeersStatus) DeepCopyInto(out *DRClusterPeersStatus) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new DRClusterPeersStatus.
func (in *DRClusterPeersStatus) DeepCopy() *DRClusterPeersStatus {
	if in == nil {
		return nil
	}
	out := new(DRClusterPeersStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in LastKnownDRStateMap) DeepCopyInto(out *LastKnownDRStateMap) {
	{
		in := &in
		*out = make(LastKnownDRStateMap, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LastKnownDRStateMap.
func (in LastKnownDRStateMap) DeepCopy() LastKnownDRStateMap {
	if in == nil {
		return nil
	}
	out := new(LastKnownDRStateMap)
	in.DeepCopyInto(out)
	return *out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *SubscriptionPlacementDecision) DeepCopyInto(out *SubscriptionPlacementDecision) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new SubscriptionPlacementDecision.
func (in *SubscriptionPlacementDecision) DeepCopy() *SubscriptionPlacementDecision {
	if in == nil {
		return nil
	}
	out := new(SubscriptionPlacementDecision)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in SubscriptionPlacementDecisionMap) DeepCopyInto(out *SubscriptionPlacementDecisionMap) {
	{
		in := &in
		*out = make(SubscriptionPlacementDecisionMap, len(*in))
		for key, val := range *in {
			var outVal *SubscriptionPlacementDecision
			if val == nil {
				(*out)[key] = nil
			} else {
				in, out := &val, &outVal
				*out = new(SubscriptionPlacementDecision)
				**out = **in
			}
			(*out)[key] = outVal
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new SubscriptionPlacementDecisionMap.
func (in SubscriptionPlacementDecisionMap) DeepCopy() SubscriptionPlacementDecisionMap {
	if in == nil {
		return nil
	}
	out := new(SubscriptionPlacementDecisionMap)
	in.DeepCopyInto(out)
	return *out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *VolumeReplicationGroup) DeepCopyInto(out *VolumeReplicationGroup) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	if in.Status != nil {
		in, out := &in.Status, &out.Status
		*out = new(VolumeReplicationGroupStatus)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new VolumeReplicationGroup.
func (in *VolumeReplicationGroup) DeepCopy() *VolumeReplicationGroup {
	if in == nil {
		return nil
	}
	out := new(VolumeReplicationGroup)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *VolumeReplicationGroup) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *VolumeReplicationGroupList) DeepCopyInto(out *VolumeReplicationGroupList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]VolumeReplicationGroup, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new VolumeReplicationGroupList.
func (in *VolumeReplicationGroupList) DeepCopy() *VolumeReplicationGroupList {
	if in == nil {
		return nil
	}
	out := new(VolumeReplicationGroupList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *VolumeReplicationGroupList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *VolumeReplicationGroupSpec) DeepCopyInto(out *VolumeReplicationGroupSpec) {
	*out = *in
	in.PVCSelector.DeepCopyInto(&out.PVCSelector)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new VolumeReplicationGroupSpec.
func (in *VolumeReplicationGroupSpec) DeepCopy() *VolumeReplicationGroupSpec {
	if in == nil {
		return nil
	}
	out := new(VolumeReplicationGroupSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *VolumeReplicationGroupStatus) DeepCopyInto(out *VolumeReplicationGroupStatus) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new VolumeReplicationGroupStatus.
func (in *VolumeReplicationGroupStatus) DeepCopy() *VolumeReplicationGroupStatus {
	if in == nil {
		return nil
	}
	out := new(VolumeReplicationGroupStatus)
	in.DeepCopyInto(out)
	return out
}
