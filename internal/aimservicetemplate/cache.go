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

package aimservicetemplate

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// HasExistingTemplateCache checks if a template cache already exists for the given template.
// It checks owner references to determine if the cache belongs to this template.
func HasExistingTemplateCache(templateUID types.UID, cachesResult controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCacheList]) bool {
	if !cachesResult.OK() || cachesResult.Value == nil {
		return false
	}
	for _, cache := range cachesResult.Value.Items {
		for _, owner := range cache.OwnerReferences {
			if owner.UID == templateUID {
				return true
			}
		}
	}
	return false
}

// BuildTemplateCache creates an AIMTemplateCache resource for a namespace-scoped template.
func BuildTemplateCache(template *aimv1alpha1.AIMServiceTemplate, runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon) *aimv1alpha1.AIMTemplateCache {
	// Use Caching.Env only - these are download-specific environment variables
	// (e.g., HF_TOKEN, proxy settings) separate from inference runtime env vars
	var cacheEnv []corev1.EnvVar
	if template.Spec.Caching != nil {
		cacheEnv = template.Spec.Caching.Env
	}

	cache := &aimv1alpha1.AIMTemplateCache{
		TypeMeta: metav1.TypeMeta{
			APIVersion: aimv1alpha1.GroupVersion.String(),
			Kind:       "AIMTemplateCache",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      template.Name,
			Namespace: template.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: aimv1alpha1.GroupVersion.String(),
					Kind:       "AIMServiceTemplate",
					Name:       template.Name,
					UID:        template.UID,
					Controller: ptr.To(true),
				},
			},
		},
		Spec: aimv1alpha1.AIMTemplateCacheSpec{
			TemplateName:  template.Name,
			TemplateScope: aimv1alpha1.AIMServiceTemplateScopeNamespace,
			Env:           cacheEnv,
		},
	}

	// Set storage class from runtime config if available
	if runtimeConfig != nil && runtimeConfig.DefaultStorageClassName != "" {
		cache.Spec.StorageClassName = runtimeConfig.DefaultStorageClassName
	}

	return cache
}
