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

package aimclustermodelsource

import (
	"strings"

	"github.com/blang/semver/v4"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

// matchesWildcard checks if a string matches a pattern with * wildcard support.
// Only * is supported (matches any sequence of characters).
func matchesWildcard(pattern, str string) bool {
	// Split pattern by *
	parts := strings.Split(pattern, "*")

	// If no wildcards, must be exact match
	if len(parts) == 1 {
		return pattern == str
	}

	// Check prefix (before first *)
	if !strings.HasPrefix(str, parts[0]) {
		return false
	}
	str = str[len(parts[0]):]

	// Check suffix (after last *)
	if !strings.HasSuffix(str, parts[len(parts)-1]) {
		return false
	}
	str = str[:len(str)-len(parts[len(parts)-1])]

	// Check middle parts appear in order
	for i := 1; i < len(parts)-1; i++ {
		idx := strings.Index(str, parts[i])
		if idx == -1 {
			return false
		}
		str = str[idx+len(parts[i]):]
	}

	return true
}

// matchesFilters checks if an image matches any of the provided filters.
// Filters are combined with OR logic - if any filter matches, the image is included.
func matchesFilters(
	img RegistryImage,
	filters []aimv1alpha1.ModelSourceFilter,
	globalVersions []string,
) bool {
	for _, filter := range filters {
		if matchesFilter(img, filter, globalVersions) {
			return true
		}
	}
	return false
}

// matchesFilter checks if an image matches a single filter.
// The filter is applied in three stages:
// 1. Wildcard pattern matching on repository name
// 2. Exclusion list (exact match)
// 3. Semver version constraints
func matchesFilter(
	img RegistryImage,
	filter aimv1alpha1.ModelSourceFilter,
	globalVersions []string,
) bool {
	// Use repository name for matching (e.g., "amdenterpriseai/aim-llama3")
	imageName := img.Repository

	// 1. Wildcard pattern match (only * supported)
	if !matchesWildcard(filter.Image, imageName) {
		return false
	}

	// 2. Check exclusions (exact match)
	for _, exclude := range filter.Exclude {
		if imageName == exclude {
			return false
		}
	}

	// 3. Semver version constraints
	// Use filter-specific versions if provided, otherwise use global versions
	versionConstraints := filter.Versions
	if len(versionConstraints) == 0 {
		versionConstraints = globalVersions
	}

	// If version constraints are specified, apply them
	if len(versionConstraints) > 0 {
		return matchesSemver(img.Tag, versionConstraints)
	}

	// No version constraints - all versions match
	return true
}

// matchesSemver checks if a tag satisfies all provided semver constraints.
// Returns false if the tag is not a valid semver string (non-semver tags are skipped).
// The 'v' prefix is stripped automatically (v1.0.0 -> 1.0.0).
func matchesSemver(tag string, constraints []string) bool {
	// Strip 'v' prefix if present
	tagVersion := strings.TrimPrefix(tag, "v")

	// Parse the tag as semver
	parsedVersion, err := semver.Parse(tagVersion)
	if err != nil {
		// Not a valid semver tag - skip it (strict semver-only mode)
		return false
	}

	// Check all constraints - all must be satisfied
	for _, constraint := range constraints {
		versionRange, err := semver.ParseRange(constraint)
		if err != nil {
			// Invalid constraint - skip this constraint and continue
			// This allows the image to pass if other constraints are valid
			continue
		}

		// Check if version satisfies this constraint
		if !versionRange(parsedVersion) {
			return false
		}
	}

	return true
}
