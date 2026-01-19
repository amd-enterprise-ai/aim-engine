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
	"encoding/json"
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

// EnvVarAIMEngineArgs is the env var name for AIM engine arguments that should be JSON-merged.
const EnvVarAIMEngineArgs = "AIM_ENGINE_ARGS"

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

// MergeEnvVars merges two env var slices with overrides taking precedence over defaults.
// Env vars are keyed by Name, matching the +listMapKey=name kubebuilder annotation.
// If jsonMergeKeys is provided, env vars with those names are deep-merged as JSON objects
// instead of being replaced. This is useful for AIM_ENGINE_ARGS which should merge
// contributions from multiple sources.
//
// Example:
//
//	merged := MergeEnvVars(defaults, overrides)
//	// overrides take precedence over defaults
//
//	merged := MergeEnvVars(defaults, overrides, "AIM_ENGINE_ARGS")
//	// AIM_ENGINE_ARGS values are deep-merged as JSON, others are replaced
func MergeEnvVars(defaults, overrides []corev1.EnvVar, jsonMergeKeys ...string) []corev1.EnvVar {
	// Build set of JSON-mergeable keys
	jsonMerge := make(map[string]bool, len(jsonMergeKeys))
	for _, key := range jsonMergeKeys {
		jsonMerge[key] = true
	}

	// Create a map for quick lookup of overrides
	overrideMap := make(map[string]corev1.EnvVar)
	for _, env := range overrides {
		overrideMap[env.Name] = env
	}

	// Start with defaults, replacing or merging any that are overridden
	merged := make([]corev1.EnvVar, 0, len(defaults)+len(overrides))
	for _, env := range defaults {
		if override, exists := overrideMap[env.Name]; exists {
			// Check if this is a JSON-mergeable env var
			if jsonMerge[env.Name] {
				mergedValue := MergeJSONEnvVarValues(env.Value, override.Value)
				merged = append(merged, corev1.EnvVar{
					Name:  env.Name,
					Value: mergedValue,
				})
			} else {
				merged = append(merged, override)
			}
			delete(overrideMap, env.Name) // Mark as processed
		} else {
			merged = append(merged, env)
		}
	}

	// Add any remaining overrides that weren't in defaults
	for _, env := range overrides {
		if _, processed := overrideMap[env.Name]; !processed {
			continue // Already added in the loop above
		}
		merged = append(merged, env)
	}

	if len(merged) == 0 {
		return nil
	}

	return merged
}

// MergeJSONEnvVarValues deep-merges two JSON object strings.
// The higher precedence value (from overrides) takes priority in case of key conflicts.
// Non-JSON values or parsing errors result in the higher precedence value being used directly.
func MergeJSONEnvVarValues(base, higher string) string {
	if base == "" {
		return higher
	}
	if higher == "" {
		return base
	}

	var baseObj, higherObj map[string]any
	if err := json.Unmarshal([]byte(base), &baseObj); err != nil {
		// base is not valid JSON, use higher precedence value
		return higher
	}
	if err := json.Unmarshal([]byte(higher), &higherObj); err != nil {
		// higher is not valid JSON, use it as-is (overwrite)
		return higher
	}

	// Deep merge: higher takes precedence
	DeepMergeMap(baseObj, higherObj)

	result, err := json.Marshal(baseObj)
	if err != nil {
		// Merge failed, use higher precedence value
		return higher
	}

	return string(result)
}

// DeepMergeMap recursively merges src into dst.
// Values from src take precedence. Nested maps are merged recursively.
func DeepMergeMap(dst, src map[string]any) {
	for key, srcVal := range src {
		if dstVal, exists := dst[key]; exists {
			// Both have this key, check if both are maps for recursive merge
			srcMap, srcIsMap := srcVal.(map[string]any)
			dstMap, dstIsMap := dstVal.(map[string]any)
			if srcIsMap && dstIsMap {
				DeepMergeMap(dstMap, srcMap)
				continue
			}
		}
		// Not both maps, or key doesn't exist in dst - use src value
		dst[key] = srcVal
	}
}

// envVarSliceType is cached for transformer type comparison.
var envVarSliceType = reflect.TypeOf([]corev1.EnvVar{})

// envVarMergeTransformer implements mergo.Transformers to handle []corev1.EnvVar
// with key-based merging by Name, matching +listMapKey=name semantics.
// It also performs JSON deep-merge for AIM_ENGINE_ARGS.
type envVarMergeTransformer struct{}

// Transformer returns a merge function for []corev1.EnvVar that merges by Name key.
// AIM_ENGINE_ARGS values are deep-merged as JSON objects.
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
		merged := MergeEnvVars(dstEnv, srcEnv, EnvVarAIMEngineArgs)
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
