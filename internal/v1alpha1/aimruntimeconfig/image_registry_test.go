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

package aimruntimeconfig

import (
	"testing"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimimage"
)

func TestImageRegistryMappings_CompatibilityFallback(t *testing.T) {
	got, err := aimimage.ResolveReference(
		"docker.io/silogenai/aim-base:2026.11.0",
		ImageRegistryMappings(nil),
	)
	if err != nil {
		t.Fatalf("resolve compatibility mapping: %v", err)
	}
	if want := "docker.io/amdenterpriseai/aim-base:2026.11.0"; got != want {
		t.Fatalf("resolved image = %q, want %q", got, want)
	}
}

func TestImageRegistryMappings_ConfiguredPolicyWinsFallback(t *testing.T) {
	config := &aimv1alpha1.AIMRuntimeConfigCommon{
		ImageRegistry: &aimv1alpha1.AIMImageRegistryConfig{
			Mappings: []aimv1alpha1.AIMImageRegistryMapping{
				{
					From: "ghcr.io",
					To:   "registry.internal/ghcr",
				},
			},
		},
	}

	got, err := aimimage.ResolveReference(
		"ghcr.io/silogen/aim-base:0.13.1",
		ImageRegistryMappings(config),
	)
	if err != nil {
		t.Fatalf("resolve configured mapping: %v", err)
	}
	if want := "registry.internal/ghcr/silogen/aim-base:0.13.1"; got != want {
		t.Fatalf("resolved image = %q, want %q", got, want)
	}
}

func TestImageRegistryMappings_SelfMappingDisablesCompatibilityFallback(t *testing.T) {
	config := &aimv1alpha1.AIMRuntimeConfigCommon{
		ImageRegistry: &aimv1alpha1.AIMImageRegistryConfig{
			Mappings: []aimv1alpha1.AIMImageRegistryMapping{
				{
					From: "docker.io/silogenai",
					To:   "docker.io/silogenai",
				},
			},
		},
	}

	got, err := aimimage.ResolveReference(
		"docker.io/silogenai/aim-base:0.14.0-rc1",
		ImageRegistryMappings(config),
	)
	if err != nil {
		t.Fatalf("resolve configured self-mapping: %v", err)
	}
	if want := "docker.io/silogenai/aim-base:0.14.0-rc1"; got != want {
		t.Fatalf("resolved image = %q, want %q", got, want)
	}
}
