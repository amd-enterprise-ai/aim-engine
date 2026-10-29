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

package aimimage

import "testing"

func TestResolveReference(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ref      string
		mappings []RegistryMapping
		want     string
		wantErr  bool
	}{
		{
			name: "no mappings preserves exact reference",
			ref:  "docker.io/amdenterpriseai/aim-base:0.11.2",
			want: "docker.io/amdenterpriseai/aim-base:0.11.2",
		},
		{
			name: "no match preserves exact reference",
			ref:  "docker.io/amdenterpriseai/aim-base@sha256:abcd",
			mappings: []RegistryMapping{
				{From: "quay.io/amd", To: "registry.internal/quay"},
			},
			want: "docker.io/amdenterpriseai/aim-base@sha256:abcd",
		},
		{
			name: "repository prefix mapping preserves tag",
			ref:  "docker.io/amdenterpriseai/aim-base:0.11.2",
			mappings: []RegistryMapping{
				{From: "docker.io/amdenterpriseai", To: "ghcr.io/private-team"},
			},
			want: "ghcr.io/private-team/aim-base:0.11.2",
		},
		{
			name: "repository prefix mapping preserves digest",
			ref:  "docker.io/amdenterpriseai/runtime/aim-base@sha256:abcd",
			mappings: []RegistryMapping{
				{From: "docker.io/amdenterpriseai", To: "registry.internal/amd"},
			},
			want: "registry.internal/amd/runtime/aim-base@sha256:abcd",
		},
		{
			name: "longest matching prefix wins",
			ref:  "docker.io/amdenterpriseai/aim-base:0.11",
			mappings: []RegistryMapping{
				{From: "docker.io", To: "registry.internal/docker"},
				{From: "docker.io/amdenterpriseai", To: "registry.internal/aim"},
			},
			want: "registry.internal/aim/aim-base:0.11",
		},
		{
			name: "fallback applies when no configured mapping matches",
			ref:  "ghcr.io/silogen/aim-base:0.11",
			mappings: []RegistryMapping{
				{
					From:     "ghcr.io/silogen",
					To:       "docker.io/amdenterpriseai",
					Fallback: true,
				},
			},
			want: "docker.io/amdenterpriseai/aim-base:0.11",
		},
		{
			name: "configured broad mapping wins over specific fallback",
			ref:  "ghcr.io/silogen/aim-base:0.11",
			mappings: []RegistryMapping{
				{From: "ghcr.io", To: "registry.internal/ghcr"},
				{
					From:     "ghcr.io/silogen",
					To:       "docker.io/amdenterpriseai",
					Fallback: true,
				},
			},
			want: "registry.internal/ghcr/silogen/aim-base:0.11",
		},
		{
			name: "longest matching fallback wins",
			ref:  "ghcr.io/silogen/aim-base:0.11",
			mappings: []RegistryMapping{
				{From: "ghcr.io", To: "registry.fallback/ghcr", Fallback: true},
				{
					From:     "ghcr.io/silogen",
					To:       "docker.io/amdenterpriseai",
					Fallback: true,
				},
			},
			want: "docker.io/amdenterpriseai/aim-base:0.11",
		},
		{
			name: "segment boundary prevents partial prefix match",
			ref:  "docker.io/amdenterpriseai-preview/aim-base:0.11",
			mappings: []RegistryMapping{
				{From: "docker.io/amdenterpriseai", To: "registry.internal/aim"},
			},
			want: "docker.io/amdenterpriseai-preview/aim-base:0.11",
		},
		{
			name: "full repository mapping preserves tag",
			ref:  "docker.io/amdenterpriseai/aim-base:0.11",
			mappings: []RegistryMapping{
				{From: "docker.io/amdenterpriseai/aim-base", To: "registry.internal/runtime/base"},
			},
			want: "registry.internal/runtime/base:0.11",
		},
		{
			name: "registry port is supported",
			ref:  "registry.example:5000/amd/aim-base:0.11",
			mappings: []RegistryMapping{
				{From: "registry.example:5000", To: "mirror.example:5443"},
			},
			want: "mirror.example:5443/amd/aim-base:0.11",
		},
		{
			name: "mapping from tag is rejected",
			ref:  "docker.io/amdenterpriseai/aim-base:0.11",
			mappings: []RegistryMapping{
				{From: "docker.io/amdenterpriseai:latest", To: "registry.internal/aim"},
			},
			wantErr: true,
		},
		{
			name: "mapping to digest is rejected",
			ref:  "docker.io/amdenterpriseai/aim-base:0.11",
			mappings: []RegistryMapping{
				{From: "docker.io/amdenterpriseai", To: "registry.internal/aim@sha256:abcd"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveReference(tt.ref, tt.mappings)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ResolveReference() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveReference() unexpected error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("ResolveReference() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractTag(t *testing.T) {
	cases := []struct {
		name     string
		imageRef string
		want     string
	}{
		{name: "tagged ref", imageRef: "amdenterpriseai/aim-qwen-qwen3-32b:0.11.0", want: "0.11.0"},
		{name: "untagged ref", imageRef: "amdenterpriseai/aim-qwen-qwen3-32b", want: ""},
		{name: "digest ref", imageRef: "amdenterpriseai/aim-qwen-qwen3-32b@sha256:abcd1234", want: ""},
		{name: "registry port without tag", imageRef: "registry.local:5000/image", want: ""},
		{name: "registry port with tag", imageRef: "registry.local:5000/image:0.11.0", want: "0.11.0"},
		{name: "trailing colon yields empty", imageRef: "amdenterpriseai/aim-qwen-qwen3-32b:", want: ""},
		{name: "empty input", imageRef: "", want: ""},
		{name: "single-segment tagged", imageRef: "aim-base:0.11", want: "0.11"},
		// Pre-release / build-metadata suffixes are preserved verbatim.
		// ExtractTag is a pure slice and does not interpret the tag.
		{name: "rc suffix preserved", imageRef: "ghcr.io/silogen/aim-base:0.11-rc21", want: "0.11-rc21"},
		{name: "full semver with rc preserved", imageRef: "ghcr.io/silogen/aim-base:0.11.0-rc1", want: "0.11.0-rc1"},
		{name: "semver with build metadata preserved", imageRef: "ghcr.io/silogen/aim-base:0.11.0+build123", want: "0.11.0+build123"},
		{name: "v-prefix preserved", imageRef: "ghcr.io/silogen/aim-base:v0.11.2", want: "v0.11.2"},
		{name: "date-style tag preserved", imageRef: "ghcr.io/silogen/aim-base:nightly-2026-05-27", want: "nightly-2026-05-27"},
		{name: "release-N tag preserved", imageRef: "ghcr.io/silogen/aim-base:release-10", want: "release-10"},
		{name: "latest tag preserved", imageRef: "ghcr.io/silogen/aim-base:latest", want: "latest"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractTag(tc.imageRef); got != tc.want {
				t.Fatalf("ExtractTag(%q) = %q, want %q", tc.imageRef, got, tc.want)
			}
		})
	}
}
