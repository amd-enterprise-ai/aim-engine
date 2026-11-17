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

func TestResolveBaseImage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		ref         string
		sourceImage string
		registry    string
		want        string
	}{
		{
			name:        "takes the registry and org from the source image",
			ref:         "docker.io/silogenai/aim-base:0.13.1",
			sourceImage: "docker.io/amdenterpriseai/aim-qwen-qwen3-32b:0.13.0",
			want:        "docker.io/amdenterpriseai/aim-base:0.13.1",
		},
		{
			name:        "follows a mirrored source image",
			ref:         "docker.io/silogenai/aim-base:0.13.1",
			sourceImage: "registry.example.com/mirror/aim-qwen-qwen3-32b:0.13.0",
			want:        "registry.example.com/mirror/aim-base:0.13.1",
		},
		{
			name:        "configured registry wins over the source image",
			ref:         "docker.io/silogenai/aim-base:0.13.1",
			sourceImage: "registry.example.com/models/aim-qwen-qwen3-32b:0.13.0",
			registry:    "registry.example.com/runtimes",
			want:        "registry.example.com/runtimes/aim-base:0.13.1",
		},
		{
			name:        "registry with a port",
			ref:         "docker.io/silogenai/aim-base:0.13.1",
			sourceImage: "docker.io/amdenterpriseai/aim-qwen-qwen3-32b:0.13.0",
			registry:    "registry.example:5000/amd",
			want:        "registry.example:5000/amd/aim-base:0.13.1",
		},
		{
			name:        "source image with a registry port",
			ref:         "docker.io/silogenai/aim-base:0.13.1",
			sourceImage: "registry.example:5000/amd/aim-qwen-qwen3-32b:0.13.0",
			want:        "registry.example:5000/amd/aim-base:0.13.1",
		},
		{
			name:        "source image pinned by digest",
			ref:         "docker.io/silogenai/aim-base:0.13.1",
			sourceImage: "registry.example.com/mirror/aim-qwen-qwen3-32b@sha256:abcd",
			want:        "registry.example.com/mirror/aim-base:0.13.1",
		},
		{
			name:        "source image without a registry keeps the recorded prefix",
			ref:         "docker.io/silogenai/aim-base:0.13.1",
			sourceImage: "aim-qwen-qwen3-32b:0.13.0",
			want:        "docker.io/silogenai/aim-base:0.13.1",
		},
		{
			name: "no source image keeps the recorded prefix",
			ref:  "docker.io/silogenai/aim-base:0.13.1",
			want: "docker.io/silogenai/aim-base:0.13.1",
		},
		{
			name:        "recorded reference without a registry gets one",
			ref:         "aim-base:0.13.1",
			sourceImage: "docker.io/amdenterpriseai/aim-qwen-qwen3-32b:0.13.0",
			want:        "docker.io/amdenterpriseai/aim-base:0.13.1",
		},
		{
			name:        "digest is kept exactly",
			ref:         "docker.io/silogenai/aim-base@sha256:abcd",
			sourceImage: "docker.io/amdenterpriseai/aim-qwen-qwen3-32b:0.13.0",
			want:        "docker.io/amdenterpriseai/aim-base@sha256:abcd",
		},
		{
			name:        "full release tag is kept exactly",
			ref:         "docker.io/silogenai/aim-base:2026.11.0",
			sourceImage: "docker.io/amdenterpriseai/aim-gemma4:2026.11.0",
			want:        "docker.io/amdenterpriseai/aim-base:2026.11.0",
		},
		{
			name:        "pre-release tag rolls to MAJOR.MINOR",
			ref:         "docker.io/silogenai/aim-base:2026.11.0-rc11",
			sourceImage: "docker.io/amdenterpriseai/aim-gemma4:2026.11.0-rc1",
			want:        "docker.io/amdenterpriseai/aim-base:2026.11",
		},
		{
			name:        "multi-part pre-release tag rolls to MAJOR.MINOR",
			ref:         "docker.io/silogenai/aim-base:2026.15.0-pr1656-rc2",
			sourceImage: "docker.io/amdenterpriseai/aim-qwen3:2026.15.0-rc1",
			want:        "docker.io/amdenterpriseai/aim-base:2026.15",
		},
		{
			name:        "short pre-release tag rolls to MAJOR.MINOR",
			ref:         "docker.io/silogenai/aim-base:0.11-rc21",
			sourceImage: "docker.io/amdenterpriseai/aim-qwen3:0.11.0",
			want:        "docker.io/amdenterpriseai/aim-base:0.11",
		},
		{
			name:        "build metadata is kept exactly",
			ref:         "docker.io/silogenai/aim-base:0.11.0+build.1",
			sourceImage: "docker.io/amdenterpriseai/aim-qwen3:0.11.0",
			want:        "docker.io/amdenterpriseai/aim-base:0.11.0+build.1",
		},
		{
			name:        "non-version tag is kept exactly",
			ref:         "docker.io/silogenai/aim-base:nightly-2026-05-27",
			sourceImage: "docker.io/amdenterpriseai/aim-qwen3:0.11.0",
			want:        "docker.io/amdenterpriseai/aim-base:nightly-2026-05-27",
		},
		{
			name: "empty reference stays empty",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ResolveBaseImage(tt.ref, tt.sourceImage, tt.registry); got != tt.want {
				t.Fatalf("ResolveBaseImage(%q, %q, %q) = %q, want %q", tt.ref, tt.sourceImage, tt.registry, got, tt.want)
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
		{name: "rc suffix preserved", imageRef: "ghcr.io/amdenterpriseai/aim-base:0.11-rc21", want: "0.11-rc21"},
		{name: "full semver with rc preserved", imageRef: "ghcr.io/amdenterpriseai/aim-base:0.11.0-rc1", want: "0.11.0-rc1"},
		{name: "semver with build metadata preserved", imageRef: "ghcr.io/amdenterpriseai/aim-base:0.11.0+build123", want: "0.11.0+build123"},
		{name: "v-prefix preserved", imageRef: "ghcr.io/amdenterpriseai/aim-base:v0.11.2", want: "v0.11.2"},
		{name: "date-style tag preserved", imageRef: "ghcr.io/amdenterpriseai/aim-base:nightly-2026-05-27", want: "nightly-2026-05-27"},
		{name: "release-N tag preserved", imageRef: "ghcr.io/amdenterpriseai/aim-base:release-10", want: "release-10"},
		{name: "latest tag preserved", imageRef: "ghcr.io/amdenterpriseai/aim-base:latest", want: "latest"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractTag(tc.imageRef); got != tc.want {
				t.Fatalf("ExtractTag(%q) = %q, want %q", tc.imageRef, got, tc.want)
			}
		})
	}
}
