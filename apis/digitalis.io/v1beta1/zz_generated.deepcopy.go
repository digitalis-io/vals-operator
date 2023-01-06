//go:build !ignore_autogenerated
// +build !ignore_autogenerated

/*
Copyright 2021 Digitalis.IO.

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

package v1beta1

import (
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *DbRolloutTarget) DeepCopyInto(out *DbRolloutTarget) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new DbRolloutTarget.
func (in *DbRolloutTarget) DeepCopy() *DbRolloutTarget {
	if in == nil {
		return nil
	}
	out := new(DbRolloutTarget)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *DbSecret) DeepCopyInto(out *DbSecret) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	out.Status = in.Status
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new DbSecret.
func (in *DbSecret) DeepCopy() *DbSecret {
	if in == nil {
		return nil
	}
	out := new(DbSecret)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *DbSecret) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *DbSecretList) DeepCopyInto(out *DbSecretList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]DbSecret, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new DbSecretList.
func (in *DbSecretList) DeepCopy() *DbSecretList {
	if in == nil {
		return nil
	}
	out := new(DbSecretList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *DbSecretList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *DbSecretRollout) DeepCopyInto(out *DbSecretRollout) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new DbSecretRollout.
func (in *DbSecretRollout) DeepCopy() *DbSecretRollout {
	if in == nil {
		return nil
	}
	out := new(DbSecretRollout)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *DbSecretSpec) DeepCopyInto(out *DbSecretSpec) {
	*out = *in
	out.Vault = in.Vault
	if in.Secret != nil {
		in, out := &in.Secret, &out.Secret
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	out.Rollout = in.Rollout
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new DbSecretSpec.
func (in *DbSecretSpec) DeepCopy() *DbSecretSpec {
	if in == nil {
		return nil
	}
	out := new(DbSecretSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *DbSecretStatus) DeepCopyInto(out *DbSecretStatus) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new DbSecretStatus.
func (in *DbSecretStatus) DeepCopy() *DbSecretStatus {
	if in == nil {
		return nil
	}
	out := new(DbSecretStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *DbVaultConfig) DeepCopyInto(out *DbVaultConfig) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new DbVaultConfig.
func (in *DbVaultConfig) DeepCopy() *DbVaultConfig {
	if in == nil {
		return nil
	}
	out := new(DbVaultConfig)
	in.DeepCopyInto(out)
	return out
}
