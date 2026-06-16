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

package aimservice

import (
	"testing"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

const testBaseCache = "base-cache"

func templateCacheWith(artifacts map[string]aimv1alpha1.AIMResolvedArtifact) controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache] {
	return controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]{
		Value: &aimv1alpha1.AIMTemplateCache{
			Status: aimv1alpha1.AIMTemplateCacheStatus{Artifacts: artifacts},
		},
	}
}

func namespaceTemplateWith(modelIDs ...string) controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplate] {
	sources := make([]aimv1alpha1.AIMModelSource, 0, len(modelIDs))
	for _, id := range modelIDs {
		sources = append(sources, aimv1alpha1.AIMModelSource{ModelID: id})
	}
	return controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplate]{
		Value: &aimv1alpha1.AIMServiceTemplate{
			Status: aimv1alpha1.AIMServiceTemplateStatus{ModelSources: sources},
		},
	}
}

func TestResolveAdapterParentNameHappyPath(t *testing.T) {
	result := ServiceFetchResult{
		template: namespaceTemplateWith("org/base"),
		templateCache: templateCacheWith(map[string]aimv1alpha1.AIMResolvedArtifact{
			testBaseCache: {Name: testBaseCache, Model: "org/base"},
		}),
	}

	got, err := resolveAdapterParentName(result)
	if err != nil {
		t.Fatalf("resolveAdapterParentName returned unexpected error: %v", err)
	}
	if got != testBaseCache {
		t.Errorf("resolveAdapterParentName = %q, want base-cache", got)
	}
}

func TestResolveAdapterParentNameMultiSourceRejected(t *testing.T) {
	// A template with multiple model sources is ambiguous for adapters (which
	// bind to a single base model) and must surface a terminal error rather than
	// silently picking the first source.
	result := ServiceFetchResult{
		template: namespaceTemplateWith("org/base", "org/other"),
		templateCache: templateCacheWith(map[string]aimv1alpha1.AIMResolvedArtifact{
			testBaseCache: {Name: testBaseCache, Model: "org/base"},
			"other-cache": {Name: "other-cache", Model: "org/other"},
		}),
	}

	got, err := resolveAdapterParentName(result)
	if err == nil {
		t.Fatalf("resolveAdapterParentName = %q, want error for multiple model sources", got)
	}
	if got != "" {
		t.Errorf("resolveAdapterParentName = %q, want empty on error", got)
	}
}

func TestResolveAdapterParentNameClusterTemplate(t *testing.T) {
	result := ServiceFetchResult{
		clusterTemplate: controllerutils.FetchResult[*aimv1alpha1.AIMClusterServiceTemplate]{
			Value: &aimv1alpha1.AIMClusterServiceTemplate{
				Status: aimv1alpha1.AIMServiceTemplateStatus{
					ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "org/base"}},
				},
			},
		},
		templateCache: templateCacheWith(map[string]aimv1alpha1.AIMResolvedArtifact{
			testBaseCache: {Name: testBaseCache, Model: "org/base"},
		}),
	}

	got, err := resolveAdapterParentName(result)
	if err != nil {
		t.Fatalf("resolveAdapterParentName returned unexpected error: %v", err)
	}
	if got != testBaseCache {
		t.Errorf("resolveAdapterParentName = %q, want base-cache", got)
	}
}

func TestResolveAdapterParentNameNoCache(t *testing.T) {
	result := ServiceFetchResult{
		template: namespaceTemplateWith("org/base"),
	}
	got, err := resolveAdapterParentName(result)
	if err != nil {
		t.Fatalf("resolveAdapterParentName returned unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("resolveAdapterParentName = %q, want empty (no cache)", got)
	}
}

func TestResolveAdapterParentNameNoMatch(t *testing.T) {
	result := ServiceFetchResult{
		template: namespaceTemplateWith("org/base"),
		templateCache: templateCacheWith(map[string]aimv1alpha1.AIMResolvedArtifact{
			"other-cache": {Name: "other-cache", Model: "org/other"},
		}),
	}
	got, err := resolveAdapterParentName(result)
	if err != nil {
		t.Fatalf("resolveAdapterParentName returned unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("resolveAdapterParentName = %q, want empty (no matching model)", got)
	}
}
