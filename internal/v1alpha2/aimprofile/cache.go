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

package aimprofile

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
)

// buildProfileOwnedCache materialises an AIMProfileCache that is 1:1 with the
// AIMProfile it derives from: same name, same namespace, owner-ref back to
// the profile so deleting the profile garbage-collects the cache (and its
// dedicated AIMArtifacts, when applicable).
//
// The cache always runs in Shared mode here: the artifacts it produces use
// the deterministic-by-weights name (SourceURI + env + storage class) so two
// profiles in the same namespace with the same SourceURI dedupe at the
// AIMArtifact layer regardless of how their respective caches are owned.
// Service-driven caches, when present, use mode=Dedicated to opt into a
// per-service artifact lifecycle.
//
// Mirrors v1alpha1's BuildTemplateCache shape so the two pipelines reason
// the same way about cache lifecycle.
func buildProfileOwnedCache(profile *aimv1alpha2.AIMProfile) *aimv1alpha2.AIMProfileCache {
	var cacheEnv []corev1.EnvVar
	if profile.Spec.Caching != nil {
		cacheEnv = profile.Spec.Caching.Env
	}

	return &aimv1alpha2.AIMProfileCache{
		TypeMeta: metav1.TypeMeta{
			APIVersion: aimv1alpha2.GroupVersion.String(),
			Kind:       "AIMProfileCache",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      profile.Name,
			Namespace: profile.Namespace,
		},
		Spec: aimv1alpha2.AIMProfileCacheSpec{
			ProfileName:  profile.Name,
			ProfileScope: aimv1alpha1.AIMResolutionScopeNamespace,
			Mode:         aimv1alpha2.ProfileCacheModeShared,
			Env:          cacheEnv,
		},
	}
}
