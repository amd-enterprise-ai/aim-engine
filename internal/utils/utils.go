/*
MIT License

Copyright (c) 2025 Advanced Micro Devices, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package utils

import (
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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

// MergeEnvVars merges env vars from template with service env vars.
// Service env vars take precedence over template env vars (by name).
// templateEnv are the base env vars from the template.
// serviceEnv are the override env vars from the service (these take precedence).
func MergeEnvVars(templateEnv []corev1.EnvVar, serviceEnv []corev1.EnvVar) []corev1.EnvVar {
	if len(templateEnv) == 0 {
		return serviceEnv
	}
	if len(serviceEnv) == 0 {
		return CopyEnvVars(templateEnv)
	}

	// Build index of service env var names (these override template vars)
	index := make(map[string]struct{}, len(serviceEnv))
	for _, env := range serviceEnv {
		index[env.Name] = struct{}{}
	}

	// Start with service env vars (they take precedence)
	result := CopyEnvVars(serviceEnv)

	// Add template env vars that aren't overridden by service
	for _, env := range templateEnv {
		if _, exists := index[env.Name]; !exists {
			result = append(result, env)
		}
	}

	return result
}