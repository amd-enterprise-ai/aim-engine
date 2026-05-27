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

func TestRebaseRegistry(t *testing.T) {
	cases := []struct {
		name         string
		source       string
		baseImageRef string
		want         string
	}{
		{
			name:         "graft prefix with registry+org",
			source:       "ghcr.io/silogen/qwen3:0.11",
			baseImageRef: "docker.io/amdenterpriseai/aim-base:0.11",
			want:         "ghcr.io/silogen/aim-base:0.11",
		},
		{
			name:         "switch registry+org again",
			source:       "docker.io/amdenterpriseai/qwen3:0.11",
			baseImageRef: "ghcr.io/silogen/aim-base:0.11",
			want:         "docker.io/amdenterpriseai/aim-base:0.11",
		},
		{
			name:         "base ref without slash gets prefix grafted",
			source:       "ghcr.io/silogen/qwen3:0.11",
			baseImageRef: "aim-base:0.11",
			want:         "ghcr.io/silogen/aim-base:0.11",
		},
		{
			name:         "source without slash returns base unchanged",
			source:       "qwen3:0.11",
			baseImageRef: "ghcr.io/silogen/aim-base:0.11",
			want:         "ghcr.io/silogen/aim-base:0.11",
		},
		{
			name:         "empty source returns base unchanged",
			source:       "",
			baseImageRef: "ghcr.io/silogen/aim-base:0.11",
			want:         "ghcr.io/silogen/aim-base:0.11",
		},
		{
			name:         "same registry is a no-op",
			source:       "ghcr.io/silogen/qwen3:0.11",
			baseImageRef: "ghcr.io/silogen/aim-base:0.11",
			want:         "ghcr.io/silogen/aim-base:0.11",
		},
		{
			name:         "registry port in source is preserved",
			source:       "localhost:5000/myorg/qwen3:0.11",
			baseImageRef: "ghcr.io/silogen/aim-base:0.11",
			want:         "localhost:5000/myorg/aim-base:0.11",
		},
		{
			name:         "multi-segment source path keeps everything before last slash",
			source:       "registry.example.com/team/models/qwen3:0.11",
			baseImageRef: "ghcr.io/silogen/aim-base:0.11",
			want:         "registry.example.com/team/models/aim-base:0.11",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RebaseRegistry(tc.source, tc.baseImageRef)
			if got != tc.want {
				t.Fatalf("RebaseRegistry(%q, %q) = %q, want %q", tc.source, tc.baseImageRef, got, tc.want)
			}
		})
	}
}

func TestNormalizeBaseImageTag(t *testing.T) {
	cases := []struct {
		name         string
		baseImageRef string
		want         string
	}{
		{
			name:         "release-candidate tag truncates to major.minor",
			baseImageRef: "ghcr.io/silogen/aim-base:0.11-rc21",
			want:         "ghcr.io/silogen/aim-base:0.11",
		},
		{
			name:         "full semver tag truncates to major.minor",
			baseImageRef: "ghcr.io/silogen/aim-base:0.11.2",
			want:         "ghcr.io/silogen/aim-base:0.11",
		},
		{
			name:         "major.minor tag is preserved",
			baseImageRef: "ghcr.io/silogen/aim-base:0.11",
			want:         "ghcr.io/silogen/aim-base:0.11",
		},
		{
			name:         "v-prefixed semver tolerated",
			baseImageRef: "ghcr.io/silogen/aim-base:v0.11.2",
			want:         "ghcr.io/silogen/aim-base:0.11",
		},
		{
			name:         "prerelease with full semver truncates",
			baseImageRef: "ghcr.io/silogen/aim-base:0.11.0-rc1",
			want:         "ghcr.io/silogen/aim-base:0.11",
		},
		{
			name:         "non-semver tag left unchanged",
			baseImageRef: "ghcr.io/silogen/aim-base:latest",
			want:         "ghcr.io/silogen/aim-base:latest",
		},
		{
			name:         "no tag left unchanged",
			baseImageRef: "ghcr.io/silogen/aim-base",
			want:         "ghcr.io/silogen/aim-base",
		},
		{
			name:         "trailing colon left unchanged",
			baseImageRef: "ghcr.io/silogen/aim-base:",
			want:         "ghcr.io/silogen/aim-base:",
		},
		{
			name:         "registry port without tag left unchanged",
			baseImageRef: "registry.local:5000/aim-base",
			want:         "registry.local:5000/aim-base",
		},
		{
			name:         "registry port with semver tag normalised",
			baseImageRef: "registry.local:5000/aim-base:0.11-rc21",
			want:         "registry.local:5000/aim-base:0.11",
		},
		{
			name:         "untagged single-segment ref left unchanged",
			baseImageRef: "aim-base",
			want:         "aim-base",
		},
		{
			name:         "tagged single-segment ref normalised",
			baseImageRef: "aim-base:0.11-rc21",
			want:         "aim-base:0.11",
		},
		{name: "empty ref returns empty", baseImageRef: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeBaseImageTag(tc.baseImageRef)
			if got != tc.want {
				t.Fatalf("NormalizeBaseImageTag(%q) = %q, want %q", tc.baseImageRef, got, tc.want)
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
		// ExtractTag is a pure slice and does NOT normalise the tag.
		// Compare with NormalizeBaseImageTag (above) which intentionally
		// truncates `:0.11.0-rc21` → `:0.11` for aim-base rebases — that
		// truncation is a separate concern for base-image resolution and
		// must NOT leak into the user-visible Version column.
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

func TestLegacyBaseImageFromSource(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   string
	}{
		{name: "semver tag truncates to major.minor", source: "ghcr.io/silogen/qwen3:0.11.2", want: "aim-base:0.11"},
		{name: "two-segment semver kept as-is", source: "ghcr.io/silogen/qwen3:0.11", want: "aim-base:0.11"},
		{name: "v-prefixed semver is tolerated", source: "ghcr.io/silogen/qwen3:v0.11.0", want: "aim-base:0.11"},
		{name: "semver with prerelease suffix is accepted", source: "ghcr.io/silogen/qwen3:0.11.0-rc1", want: "aim-base:0.11"},
		{name: "short prerelease suffix is accepted", source: "ghcr.io/silogen/qwen3:0.11-rc21", want: "aim-base:0.11"},
		{name: "registry with port keeps tag parsing correct", source: "localhost:5000/myorg/qwen3:0.11.0", want: "aim-base:0.11"},
		{name: "non-semver tag yields empty", source: "ghcr.io/silogen/qwen3:latest", want: ""},
		{name: "no tag yields empty", source: "ghcr.io/silogen/qwen3", want: ""},
		{name: "trailing colon yields empty", source: "ghcr.io/silogen/qwen3:", want: ""},
		{name: "registry port without tag is ignored", source: "registry.local:5000/image", want: ""},
		{name: "empty source returns empty", source: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := LegacyBaseImageFromSource(tc.source)
			if got != tc.want {
				t.Fatalf("LegacyBaseImageFromSource(%q) = %q, want %q", tc.source, got, tc.want)
			}
		})
	}
}
