// MIT License
//
// Copyright (c) 2025 Advanced Micro Devices, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package utils

import (
	"reflect"
	"regexp"
	"strings"

	"dario.cat/mergo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ValueOrDefault returns the value pointed to by d, or the zero value of type T if d is nil.
// This is a generic helper to safely dereference pointers with a fallback to the type's zero value.
//
// Example:
//
//	var ptr *int = nil
//	val := ValueOrDefault(ptr)  // Returns 0
//
//	ptr2 := ptr.To(42)
//	val2 := ValueOrDefault(ptr2)  // Returns 42
func ValueOrDefault[T any](d *T) T {
	if d == nil {
		return *new(T)
	}
	return *d
}

// MakeRFC1123Compliant converts a string to be RFC 1123 compliant
// (lowercase, alphanumeric, hyphens, max 63 chars)
func MakeRFC1123Compliant(s string) string {
	// Convert to lowercase
	s = strings.ToLower(s)

	// Replace invalid characters with hyphens
	reg := regexp.MustCompile(`[^a-z0-9-]`)
	s = reg.ReplaceAllString(s, "-")

	// Remove leading/trailing hyphens
	s = strings.Trim(s, "-")

	// Truncate to 63 characters
	if len(s) > 63 {
		s = s[:63]
	}

	// Remove trailing hyphens after truncation
	s = strings.TrimRight(s, "-")

	return s
}

// CopyPullSecrets returns a deep copy of the provided image pull secrets slice.
func CopyPullSecrets(in []corev1.LocalObjectReference) []corev1.LocalObjectReference {
	if len(in) == 0 {
		return nil
	}
	out := make([]corev1.LocalObjectReference, len(in))
	copy(out, in)
	return out
}

// CopyEnvVars returns a deep copy of the provided environment variables slice.
func CopyEnvVars(in []corev1.EnvVar) []corev1.EnvVar {
	if len(in) == 0 {
		return nil
	}
	out := make([]corev1.EnvVar, len(in))
	copy(out, in)
	return out
}

// HasOwnerReference checks if the given UID exists in the owner references list.
func HasOwnerReference(refs []metav1.OwnerReference, uid types.UID) bool {
	for _, ref := range refs {
		if ref.UID == uid {
			return true
		}
	}
	return false
}

// BuildOwnerReference creates a controller owner reference for the given object.
func BuildOwnerReference(obj client.Object, scheme *runtime.Scheme) metav1.OwnerReference {
	gvk := obj.GetObjectKind().GroupVersionKind()

	return metav1.OwnerReference{
		APIVersion:         gvk.GroupVersion().String(),
		Kind:               gvk.Kind,
		Name:               obj.GetName(),
		UID:                obj.GetUID(),
		Controller:         ptr.To(true),
		BlockOwnerDeletion: ptr.To(true),
	}
}

// MergePullSecretRefs merges image pull secrets from base and extras, avoiding duplicates.
// Extras take precedence when there's a name collision.
func MergePullSecretRefs(base []corev1.LocalObjectReference, extras []corev1.LocalObjectReference) []corev1.LocalObjectReference {
	if len(extras) == 0 {
		return base
	}
	if len(base) == 0 {
		return CopyPullSecrets(extras)
	}

	index := make(map[string]struct{}, len(base))
	for _, secret := range base {
		index[secret.Name] = struct{}{}
	}

	for _, secret := range extras {
		if _, exists := index[secret.Name]; exists {
			continue
		}
		base = append(base, secret)
	}

	return base
}

// MergeEnvVars merges multiple env var slices with later slices taking precedence.
// Env vars are keyed by Name, matching the +listMapKey=name kubebuilder annotation.
// Pass slices in order of increasing precedence (e.g., cluster, namespace, resource).
//
// Example:
//
//	merged := MergeEnvVars(clusterEnv, namespaceEnv, resourceEnv)
//	// resourceEnv vars override namespaceEnv, which override clusterEnv
func MergeEnvVars(envSlices ...[]corev1.EnvVar) []corev1.EnvVar {
	// Build map keyed by name, later slices override earlier ones
	merged := make(map[string]corev1.EnvVar)
	for _, slice := range envSlices {
		for _, env := range slice {
			merged[env.Name] = env
		}
	}

	if len(merged) == 0 {
		return nil
	}

	result := make([]corev1.EnvVar, 0, len(merged))
	for _, env := range merged {
		result = append(result, env)
	}
	return result
}

// envVarSliceType is cached for transformer type comparison.
var envVarSliceType = reflect.TypeOf([]corev1.EnvVar{})

// envVarMergeTransformer implements mergo.Transformers to handle []corev1.EnvVar
// with key-based merging by Name, matching +listMapKey=name semantics.
type envVarMergeTransformer struct{}

// Transformer returns a merge function for []corev1.EnvVar that merges by Name key.
func (t envVarMergeTransformer) Transformer(typ reflect.Type) func(dst, src reflect.Value) error {
	if typ != envVarSliceType {
		return nil
	}
	return func(dst, src reflect.Value) error {
		if !src.IsValid() || src.IsNil() {
			return nil
		}
		if !dst.CanSet() {
			return nil
		}

		dstEnv, _ := dst.Interface().([]corev1.EnvVar)
		srcEnv, _ := src.Interface().([]corev1.EnvVar)
		merged := MergeEnvVars(dstEnv, srcEnv)
		dst.Set(reflect.ValueOf(merged))
		return nil
	}
}

// MergeOptions returns the standard mergo options for config merging.
// This includes WithOverride for scalar fields and the envVarMergeTransformer
// for key-based slice merging of []corev1.EnvVar fields.
func MergeOptions() []func(*mergo.Config) {
	return []func(*mergo.Config){
		mergo.WithOverride,
		mergo.WithTransformers(envVarMergeTransformer{}),
	}
}

// MergeConfigs merges multiple config structs with later configs taking precedence.
// Uses key-based merging for []corev1.EnvVar fields (by Name).
// The dst must be a pointer to the destination struct.
//
// Example:
//
//	var resolved AIMRuntimeConfigCommon
//	err := MergeConfigs(&resolved, clusterConfig, namespaceConfig, serviceConfig)
func MergeConfigs[T any](dst *T, srcs ...T) error {
	opts := MergeOptions()
	for _, src := range srcs {
		if err := mergo.Merge(dst, src, opts...); err != nil {
			return err
		}
	}
	return nil
}
