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

package aimclustermodelsource

import (
	"context"
	"strings"
	"testing"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

func TestMatchesSemver(t *testing.T) {
	tests := []struct {
		name        string
		tag         string
		constraints []string
		want        bool
	}{
		{
			name:        "exact version match",
			tag:         "1.0.0",
			constraints: []string{">=1.0.0"},
			want:        true,
		},
		{
			name:        "version with v prefix",
			tag:         "v1.0.0",
			constraints: []string{">=1.0.0"},
			want:        true,
		},
		{
			name:        "greater than match",
			tag:         "2.0.0",
			constraints: []string{">=1.0.0"},
			want:        true,
		},
		{
			name:        "less than match",
			tag:         "1.0.0",
			constraints: []string{"<2.0.0"},
			want:        true,
		},
		{
			name:        "multiple constraints all satisfied",
			tag:         "1.5.0",
			constraints: []string{">=1.0.0", "<2.0.0"},
			want:        true,
		},
		{
			name:        "multiple constraints not all satisfied",
			tag:         "2.5.0",
			constraints: []string{">=1.0.0", "<2.0.0"},
			want:        false,
		},
		{
			name:        "tilde range patch match",
			tag:         "1.2.3",
			constraints: []string{"~1.2.0"},
			want:        true,
		},
		{
			name:        "caret range minor match",
			tag:         "1.5.0",
			constraints: []string{"^1.2.0"},
			want:        true,
		},
		{
			name:        "non-semver tag skipped",
			tag:         "latest",
			constraints: []string{">=1.0.0"},
			want:        false,
		},
		{
			name:        "dev tag skipped",
			tag:         "dev",
			constraints: []string{">=1.0.0"},
			want:        false,
		},
		{
			name:        "stable tag skipped",
			tag:         "stable",
			constraints: []string{">=1.0.0"},
			want:        false,
		},
		{
			name:        "invalid constraint ignored",
			tag:         "1.0.0",
			constraints: []string{"invalid", ">=1.0.0"},
			want:        true,
		},
		{
			name:        "all constraints invalid",
			tag:         "1.0.0",
			constraints: []string{"invalid", "also-invalid"},
			want:        true,
		},
		{
			name:        "no constraints",
			tag:         "1.0.0",
			constraints: []string{},
			want:        true,
		},
		{
			name:        "prerelease version rc1",
			tag:         "0.8.1-rc1",
			constraints: []string{">=0.8.0"},
			want:        true,
		},
		{
			name:        "prerelease version with prerelease constraint",
			tag:         "0.8.1-rc1",
			constraints: []string{">=0.8.1-rc1"},
			want:        true,
		},
		{
			name:        "prerelease version alpha",
			tag:         "1.0.0-alpha.1",
			constraints: []string{">=1.0.0-alpha"},
			want:        true,
		},
		{
			name:        "prerelease no constraints allows all",
			tag:         "0.8.1-rc1",
			constraints: []string{},
			want:        true,
		},
		{
			name:        "version below minimum constraint",
			tag:         "0.8.4",
			constraints: []string{">=0.9"},
			want:        false,
		},
		{
			name:        "version below minimum with 0.9.0",
			tag:         "0.8.4",
			constraints: []string{">=0.9.0"},
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesSemver(tt.tag, tt.constraints)
			if got != tt.want {
				t.Errorf("matchesSemver(%q, %v) = %v, want %v",
					tt.tag, tt.constraints, got, tt.want)
			}
		})
	}
}

func TestParseImageFilter(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want parsedImageFilter
	}{
		{
			name: "simple repository pattern",
			in:   "silogen/aim-llama",
			want: parsedImageFilter{
				registry:    "",
				repository:  "silogen/aim-llama",
				tag:         "",
				hasWildcard: false,
			},
		},
		{
			name: "repository with wildcard",
			in:   "silogen/aim-*",
			want: parsedImageFilter{
				registry:    "",
				repository:  "silogen/aim-*",
				tag:         "",
				hasWildcard: true,
			},
		},
		{
			name: "repository with tag",
			in:   "silogen/aim-llama:1.0.0",
			want: parsedImageFilter{
				registry:    "",
				repository:  "silogen/aim-llama",
				tag:         "1.0.0",
				hasWildcard: false,
			},
		},
		{
			name: "full URI with ghcr.io registry",
			in:   "ghcr.io/silogen/aim-llama",
			want: parsedImageFilter{
				registry:    "ghcr.io",
				repository:  "silogen/aim-llama",
				tag:         "",
				hasWildcard: false,
			},
		},
		{
			name: "full URI with ghcr.io registry and tag",
			in:   "ghcr.io/silogen/aim-google-gemma-3-1b-it:0.8.1-rc1",
			want: parsedImageFilter{
				registry:    "ghcr.io",
				repository:  "silogen/aim-google-gemma-3-1b-it",
				tag:         "0.8.1-rc1",
				hasWildcard: false,
			},
		},
		{
			name: "docker.io registry (should be normalized away)",
			in:   "docker.io/library/ubuntu:latest",
			want: parsedImageFilter{
				registry:    "",
				repository:  "library/ubuntu",
				tag:         "latest",
				hasWildcard: false,
			},
		},
		{
			name: "ghcr.io with wildcard",
			in:   "ghcr.io/silogen/aim-*",
			want: parsedImageFilter{
				registry:    "ghcr.io",
				repository:  "silogen/aim-*",
				tag:         "",
				hasWildcard: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseImageFilter(tt.in)
			if got.registry != tt.want.registry {
				t.Errorf("registry = %q, want %q", got.registry, tt.want.registry)
			}
			if got.repository != tt.want.repository {
				t.Errorf("repository = %q, want %q", got.repository, tt.want.repository)
			}
			if got.tag != tt.want.tag {
				t.Errorf("tag = %q, want %q", got.tag, tt.want.tag)
			}
			if got.hasWildcard != tt.want.hasWildcard {
				t.Errorf("hasWildcard = %v, want %v", got.hasWildcard, tt.want.hasWildcard)
			}
		})
	}
}

func TestMatchesFilter(t *testing.T) {
	tests := []struct {
		name           string
		img            RegistryImage
		filter         aimv1alpha1.ModelSourceFilter
		globalVersions []string
		want           bool
	}{
		{
			name: "exact match no wildcards",
			img: RegistryImage{
				Registry:   "docker.io",
				Repository: "amdenterpriseai/aim-llama3",
				Tag:        "1.0.0",
			},
			filter: aimv1alpha1.ModelSourceFilter{
				Image: "amdenterpriseai/aim-llama3",
			},
			want: true,
		},
		{
			name: "repository mismatch is not a wildcard match",
			img: RegistryImage{
				Registry:   "docker.io",
				Repository: "amdenterpriseai/aim-llama3",
				Tag:        "1.0.0",
			},
			filter: aimv1alpha1.ModelSourceFilter{
				Image: "amdenterpriseai/aim-*",
			},
			want: false,
		},
		{
			name: "exact mismatch",
			img: RegistryImage{
				Registry:   "docker.io",
				Repository: "otherorg/model",
				Tag:        "1.0.0",
			},
			filter: aimv1alpha1.ModelSourceFilter{
				Image: "amdenterpriseai/aim-llama3",
			},
			want: false,
		},
		{
			name: "excluded image",
			img: RegistryImage{
				Registry:   "docker.io",
				Repository: "amdenterpriseai/aim-base",
				Tag:        "1.0.0",
			},
			filter: aimv1alpha1.ModelSourceFilter{
				Image:   "amdenterpriseai/aim-base",
				Exclude: []string{"amdenterpriseai/aim-base"},
			},
			want: false,
		},
		{
			name: "not in exclusion list",
			img: RegistryImage{
				Registry:   "docker.io",
				Repository: "amdenterpriseai/aim-llama3",
				Tag:        "1.0.0",
			},
			filter: aimv1alpha1.ModelSourceFilter{
				Image:   "amdenterpriseai/aim-llama3",
				Exclude: []string{"amdenterpriseai/aim-base"},
			},
			want: true,
		},
		{
			name: "filter-specific version constraint",
			img: RegistryImage{
				Registry:   "docker.io",
				Repository: "amdenterpriseai/aim-llama3",
				Tag:        "1.5.0",
			},
			filter: aimv1alpha1.ModelSourceFilter{
				Image:    "amdenterpriseai/aim-llama3",
				Versions: []string{">=1.0.0", "<2.0.0"},
			},
			want: true,
		},
		{
			name: "filter-specific version constraint fails",
			img: RegistryImage{
				Registry:   "docker.io",
				Repository: "amdenterpriseai/aim-llama3",
				Tag:        "2.5.0",
			},
			filter: aimv1alpha1.ModelSourceFilter{
				Image:    "amdenterpriseai/aim-llama3",
				Versions: []string{">=1.0.0", "<2.0.0"},
			},
			want: false,
		},
		{
			name: "global version constraint",
			img: RegistryImage{
				Registry:   "docker.io",
				Repository: "amdenterpriseai/aim-llama3",
				Tag:        "1.5.0",
			},
			filter: aimv1alpha1.ModelSourceFilter{
				Image: "amdenterpriseai/aim-llama3",
			},
			globalVersions: []string{">=1.0.0"},
			want:           true,
		},
		{
			name: "filter version overrides global",
			img: RegistryImage{
				Registry:   "docker.io",
				Repository: "amdenterpriseai/aim-llama3",
				Tag:        "0.9.0",
			},
			filter: aimv1alpha1.ModelSourceFilter{
				Image:    "amdenterpriseai/aim-llama3",
				Versions: []string{">=0.8.0"},
			},
			globalVersions: []string{">=1.0.0"},
			want:           true, // Filter version allows 0.9.0 even though global doesn't
		},
		{
			name: "non-semver tag with version constraint",
			img: RegistryImage{
				Registry:   "docker.io",
				Repository: "amdenterpriseai/aim-llama3",
				Tag:        "latest",
			},
			filter: aimv1alpha1.ModelSourceFilter{
				Image:    "amdenterpriseai/aim-llama3",
				Versions: []string{">=1.0.0"},
			},
			want: false, // Non-semver tags are skipped
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesFilter(tt.img, tt.filter, tt.globalVersions)
			if got != tt.want {
				t.Errorf("matchesFilter() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMatchesFilter_FullURISupport(t *testing.T) {
	tests := []struct {
		name   string
		img    RegistryImage
		filter aimv1alpha1.ModelSourceFilter
		want   bool
	}{
		{
			name: "full URI with exact tag match",
			img: RegistryImage{
				Registry:   "ghcr.io",
				Repository: "silogen/aim-google-gemma-3-1b-it",
				Tag:        "0.8.1-rc1",
			},
			filter: aimv1alpha1.ModelSourceFilter{
				Image: "ghcr.io/silogen/aim-google-gemma-3-1b-it:0.8.1-rc1",
			},
			want: true,
		},
		{
			name: "full URI wrong tag",
			img: RegistryImage{
				Registry:   "ghcr.io",
				Repository: "silogen/aim-google-gemma-3-1b-it",
				Tag:        "0.8.2",
			},
			filter: aimv1alpha1.ModelSourceFilter{
				Image: "ghcr.io/silogen/aim-google-gemma-3-1b-it:0.8.1-rc1",
			},
			want: false,
		},
		{
			name: "full URI wrong registry",
			img: RegistryImage{
				Registry:   "docker.io",
				Repository: "silogen/aim-llama",
				Tag:        "1.0.0",
			},
			filter: aimv1alpha1.ModelSourceFilter{
				Image: "ghcr.io/silogen/aim-llama:1.0.0",
			},
			want: false,
		},
		{
			name: "full URI matches registry, no tag specified",
			img: RegistryImage{
				Registry:   "ghcr.io",
				Repository: "silogen/aim-llama",
				Tag:        "1.0.0",
			},
			filter: aimv1alpha1.ModelSourceFilter{
				Image: "ghcr.io/silogen/aim-llama",
			},
			want: true,
		},
		{
			name: "registry override exact repository",
			img: RegistryImage{
				Registry:   "ghcr.io",
				Repository: "silogen/aim-llama3",
				Tag:        "1.0.0",
			},
			filter: aimv1alpha1.ModelSourceFilter{
				Image: "ghcr.io/silogen/aim-llama3",
			},
			want: true,
		},
		{
			name: "registry override exact repository wrong registry",
			img: RegistryImage{
				Registry:   "docker.io",
				Repository: "silogen/aim-llama3",
				Tag:        "1.0.0",
			},
			filter: aimv1alpha1.ModelSourceFilter{
				Image: "ghcr.io/silogen/aim-llama3",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesFilter(tt.img, tt.filter, nil)
			if got != tt.want {
				t.Errorf("matchesFilter() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMatchesFilters(t *testing.T) {
	tests := []struct {
		name           string
		img            RegistryImage
		filters        []aimv1alpha1.ModelSourceFilter
		globalVersions []string
		want           bool
	}{
		{
			name: "matches first filter",
			img: RegistryImage{
				Registry:   "docker.io",
				Repository: "amdenterpriseai/aim-llama3",
				Tag:        "1.0.0",
			},
			filters: []aimv1alpha1.ModelSourceFilter{
				{Image: "amdenterpriseai/aim-llama3"},
				{Image: "otherorg/model"},
			},
			want: true,
		},
		{
			name: "matches second filter",
			img: RegistryImage{
				Registry:   "docker.io",
				Repository: "otherorg/model",
				Tag:        "1.0.0",
			},
			filters: []aimv1alpha1.ModelSourceFilter{
				{Image: "amdenterpriseai/aim-llama3"},
				{Image: "otherorg/model"},
			},
			want: true,
		},
		{
			name: "matches no filters",
			img: RegistryImage{
				Registry:   "docker.io",
				Repository: "thirdorg/model",
				Tag:        "1.0.0",
			},
			filters: []aimv1alpha1.ModelSourceFilter{
				{Image: "amdenterpriseai/aim-llama3"},
				{Image: "otherorg/model"},
			},
			want: false,
		},
		{
			name: "matches filter with different version constraints",
			img: RegistryImage{
				Registry:   "docker.io",
				Repository: "amdenterpriseai/aim-llama3",
				Tag:        "0.9.0",
			},
			filters: []aimv1alpha1.ModelSourceFilter{
				{
					Image:    "amdenterpriseai/aim-llama3",
					Versions: []string{">=1.0.0"},
				},
				{
					Image:    "amdenterpriseai/aim-llama3",
					Versions: []string{">=0.8.0", "<1.0.0"},
				},
			},
			want: true, // Matches second filter
		},
		{
			name:    "empty filters returns false",
			img:     RegistryImage{Registry: "docker.io", Repository: "any/image", Tag: "1.0.0"},
			filters: []aimv1alpha1.ModelSourceFilter{},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchesFilters(tt.img, tt.filters, tt.globalVersions)
			if got != tt.want {
				t.Errorf("MatchesFilters() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsVersionConstraint(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    bool
	}{
		{
			name:    "exact version",
			version: "1.0.0",
			want:    false,
		},
		{
			name:    "exact version with v prefix",
			version: "v1.0.0",
			want:    false,
		},
		{
			name:    "prerelease version",
			version: "0.9.0-rc2",
			want:    false,
		},
		{
			name:    "greater than or equal",
			version: ">=1.0.0",
			want:    true,
		},
		{
			name:    "greater than",
			version: ">1.0.0",
			want:    true,
		},
		{
			name:    "less than or equal",
			version: "<=2.0.0",
			want:    true,
		},
		{
			name:    "less than",
			version: "<2.0.0",
			want:    true,
		},
		{
			name:    "tilde range",
			version: "~1.2.0",
			want:    true,
		},
		{
			name:    "caret range",
			version: "^1.0.0",
			want:    true,
		},
		{
			name:    "or operator",
			version: ">=1.0.0 || <0.5.0",
			want:    true,
		},
		{
			name:    "hyphen range",
			version: "1.0.0 - 2.0.0",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isVersionConstraint(tt.version)
			if got != tt.want {
				t.Errorf("isVersionConstraint(%q) = %v, want %v", tt.version, got, tt.want)
			}
		})
	}
}

func TestExtractStaticImages(t *testing.T) {
	tests := []struct {
		name string
		spec aimv1alpha1.AIMClusterModelSourceSpec
		want []RegistryImage
	}{
		{
			name: "exact tag in filter",
			spec: aimv1alpha1.AIMClusterModelSourceSpec{
				Registry: "ghcr.io",
				Filters: []aimv1alpha1.ModelSourceFilter{
					{Image: "silogen/aim-llama:1.0.0"},
				},
			},
			want: []RegistryImage{
				{Registry: "ghcr.io", Repository: "silogen/aim-llama", Tag: "1.0.0"},
			},
		},
		{
			name: "exact versions in spec",
			spec: aimv1alpha1.AIMClusterModelSourceSpec{
				Registry: "ghcr.io",
				Versions: []string{"1.0.0", "1.1.0"},
				Filters: []aimv1alpha1.ModelSourceFilter{
					{Image: "silogen/aim-llama"},
				},
			},
			want: []RegistryImage{
				{Registry: "ghcr.io", Repository: "silogen/aim-llama", Tag: "1.0.0"},
				{Registry: "ghcr.io", Repository: "silogen/aim-llama", Tag: "1.1.0"},
			},
		},
		{
			name: "filter versions override spec versions",
			spec: aimv1alpha1.AIMClusterModelSourceSpec{
				Registry: "ghcr.io",
				Versions: []string{"1.0.0"},
				Filters: []aimv1alpha1.ModelSourceFilter{
					{Image: "silogen/aim-llama", Versions: []string{"2.0.0"}},
				},
			},
			want: []RegistryImage{
				{Registry: "ghcr.io", Repository: "silogen/aim-llama", Tag: "2.0.0"},
			},
		},
		{
			name: "skip wildcards",
			spec: aimv1alpha1.AIMClusterModelSourceSpec{
				Registry: "ghcr.io",
				Versions: []string{"1.0.0"},
				Filters: []aimv1alpha1.ModelSourceFilter{
					{Image: "silogen/aim-*"},
				},
			},
			want: nil,
		},
		{
			name: "skip version constraints",
			spec: aimv1alpha1.AIMClusterModelSourceSpec{
				Registry: "ghcr.io",
				Versions: []string{">=1.0.0"},
				Filters: []aimv1alpha1.ModelSourceFilter{
					{Image: "silogen/aim-llama"},
				},
			},
			want: nil,
		},
		{
			name: "default to docker.io",
			spec: aimv1alpha1.AIMClusterModelSourceSpec{
				Filters: []aimv1alpha1.ModelSourceFilter{
					{Image: "silogen/aim-llama:1.0.0"},
				},
			},
			want: []RegistryImage{
				{Registry: "docker.io", Repository: "silogen/aim-llama", Tag: "1.0.0"},
			},
		},
		{
			name: "filter registry overrides spec registry",
			spec: aimv1alpha1.AIMClusterModelSourceSpec{
				Registry: "docker.io",
				Filters: []aimv1alpha1.ModelSourceFilter{
					{Image: "ghcr.io/silogen/aim-llama:1.0.0"},
				},
			},
			want: []RegistryImage{
				{Registry: "ghcr.io", Repository: "silogen/aim-llama", Tag: "1.0.0"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractStaticImages(tt.spec)
			if len(got) != len(tt.want) {
				t.Fatalf("ExtractStaticImages() returned %d images, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("image[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestNormalizeConstraint(t *testing.T) {
	tests := []struct {
		name       string
		constraint string
		want       string
	}{
		{
			name:       "already normalized",
			constraint: ">=1.0.0",
			want:       ">=1.0.0",
		},
		{
			name:       "missing patch version",
			constraint: ">=0.9",
			want:       ">=0.9.0",
		},
		{
			name:       "missing patch with less than",
			constraint: "<2.0",
			want:       "<2.0.0",
		},
		{
			name:       "no operator",
			constraint: "1.0.0",
			want:       "1.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeConstraint(tt.constraint)
			if got != tt.want {
				t.Errorf("normalizeConstraint(%q) = %q, want %q", tt.constraint, got, tt.want)
			}
		})
	}
}

func TestEffectiveFilters(t *testing.T) {
	spec := aimv1alpha1.AIMClusterModelSourceSpec{
		Images: []string{
			"amdenterpriseai/aim-qwen-qwen3-32b:0.8.4",
			"amdenterpriseai/aim-meta-llama-llama-3-2-1b-instruct:0.8.4",
		},
		Filters: []aimv1alpha1.ModelSourceFilter{
			{Image: "ghcr.io/silogen/aim-llama:1.0.0"},
		},
	}

	got := EffectiveFilters(spec)
	if len(got) != 3 {
		t.Fatalf("EffectiveFilters() returned %d filters, want 3", len(got))
	}
	if got[0].Image != "amdenterpriseai/aim-qwen-qwen3-32b:0.8.4" {
		t.Fatalf("first normalized filter image = %q", got[0].Image)
	}
	if got[1].Image != "amdenterpriseai/aim-meta-llama-llama-3-2-1b-instruct:0.8.4" {
		t.Fatalf("second normalized filter image = %q", got[1].Image)
	}
	if got[2].Image != "ghcr.io/silogen/aim-llama:1.0.0" {
		t.Fatalf("third normalized filter image = %q", got[2].Image)
	}
}

func TestFetchFilter_RejectsWildcardFilters(t *testing.T) {
	client := NewRegistryClient(nil, "")
	spec := aimv1alpha1.AIMClusterModelSourceSpec{
		Registry: "docker.io",
	}
	filter := aimv1alpha1.ModelSourceFilter{
		Image: "amdenterpriseai/aim-*",
	}

	result := client.FetchFilter(context.Background(), spec, filter)

	if result.Error == nil {
		t.Fatalf("expected wildcard filter to be rejected")
	}
	if !strings.Contains(result.Error.Error(), "wildcard filters are not supported") {
		t.Fatalf("unexpected error: %v", result.Error)
	}
}

func TestExtractStaticImages_UsingImagesField(t *testing.T) {
	spec := aimv1alpha1.AIMClusterModelSourceSpec{
		Registry: "docker.io",
		Images: []string{
			"amdenterpriseai/aim-qwen-qwen3-32b:0.8.4",
			"ghcr.io/silogen/aim-google-gemma-3-1b-it:0.8.1-rc1",
		},
	}

	got := ExtractStaticImages(spec)
	if len(got) != 2 {
		t.Fatalf("ExtractStaticImages() returned %d images, want 2", len(got))
	}

	if got[0] != (RegistryImage{
		Registry:   "docker.io",
		Repository: "amdenterpriseai/aim-qwen-qwen3-32b",
		Tag:        "0.8.4",
	}) {
		t.Fatalf("first image = %+v", got[0])
	}

	if got[1] != (RegistryImage{
		Registry:   "ghcr.io",
		Repository: "silogen/aim-google-gemma-3-1b-it",
		Tag:        "0.8.1-rc1",
	}) {
		t.Fatalf("second image = %+v", got[1])
	}
}
