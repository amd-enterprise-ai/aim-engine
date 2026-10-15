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

// Package aimimage provides helpers for resolving fine-tuned AIM deployment
// images. The helpers are version-agnostic: they operate on raw image strings
// so v1alpha1 (legacy template-copy path) and v1alpha2 (profile-copy path) can
// share one resolver.
package aimimage

import (
	"fmt"
	"strings"
	"unicode"
)

// RegistryMapping rewrites one image repository prefix to another.
type RegistryMapping struct {
	From string
	To   string
}

// ResolveReference applies the longest matching repository-prefix mapping to
// ref while preserving its repository suffix, tag, and digest exactly. When no
// mapping matches, ref is returned unchanged.
func ResolveReference(ref string, mappings []RegistryMapping) (string, error) {
	if ref == "" || len(mappings) == 0 {
		return ref, nil
	}

	repository, suffix := splitReference(ref)
	best := -1
	bestFrom := ""
	bestTo := ""
	for i, mapping := range mappings {
		if err := validateRepositoryPrefix(mapping.From); err != nil {
			return "", fmt.Errorf("image registry mapping %d from %q: %w", i, mapping.From, err)
		}
		if err := validateRepositoryPrefix(mapping.To); err != nil {
			return "", fmt.Errorf("image registry mapping %d to %q: %w", i, mapping.To, err)
		}
		if repository != mapping.From && !strings.HasPrefix(repository, mapping.From+"/") {
			continue
		}
		if len(mapping.From) > best {
			best = len(mapping.From)
			bestFrom = mapping.From
			bestTo = mapping.To
		}
	}
	if best < 0 {
		return ref, nil
	}
	return bestTo + strings.TrimPrefix(repository, bestFrom) + suffix, nil
}

// splitReference separates the repository name from an optional tag/digest
// suffix without normalizing the caller's reference.
func splitReference(ref string) (string, string) {
	end := len(ref)
	if digest := strings.IndexByte(ref, '@'); digest >= 0 {
		end = digest
	}
	if tag := strings.LastIndex(ref[:end], ":"); tag > strings.LastIndex(ref[:end], "/") {
		end = tag
	}
	return ref[:end], ref[end:]
}

func validateRepositoryPrefix(prefix string) error {
	if prefix == "" {
		return fmt.Errorf("prefix is empty")
	}
	if prefix != strings.TrimSpace(prefix) {
		return fmt.Errorf("prefix has leading or trailing whitespace")
	}
	if strings.ContainsAny(prefix, "@ \t\r\n") {
		return fmt.Errorf("prefix contains a digest or whitespace")
	}
	if strings.HasPrefix(prefix, "/") || strings.HasSuffix(prefix, "/") || strings.Contains(prefix, "//") {
		return fmt.Errorf("prefix has an invalid repository path")
	}

	lastSlash := strings.LastIndex(prefix, "/")
	lastColon := strings.LastIndex(prefix, ":")
	if lastColon > lastSlash {
		// A colon before a slash is a registry port. For a host-only mapping,
		// accept a numeric suffix as a port as well.
		hostOnlyPort := lastSlash < 0 && lastColon > 0 &&
			strings.IndexFunc(prefix[lastColon+1:], func(r rune) bool { return !unicode.IsDigit(r) }) == -1
		if !hostOnlyPort {
			return fmt.Errorf("prefix must not include a tag")
		}
	}
	return nil
}

// ExtractTag returns the tag substring of imageRef, or "" when imageRef
// has no tag (either no `:`, the colon is the registry port separator,
// or imageRef is a digest reference like `image@sha256:...`).
//
// Examples:
//
//	"registry/image:0.8.5"            → "0.8.5"
//	"registry/image"                  → ""
//	"registry/image@sha256:abcd"      → ""
//	"registry.local:5000/image:0.11"  → "0.11"
//	"registry.local:5000/image"       → ""  (registry port, not a tag)
//
// Use this as the "image version" source-of-truth for printcolumns and
// status denormalisation.
func ExtractTag(imageRef string) string {
	if strings.Contains(imageRef, "@sha256:") {
		return ""
	}
	colon := tagColonIndex(imageRef)
	if colon < 0 {
		return ""
	}
	return imageRef[colon+1:]
}

// tagColonIndex returns the index of the `:` separating image and tag in
// imageRef, or -1 when imageRef has no tag. Guards against registry ports
// like `registry.local:5000/image` by treating the last `:` as the tag
// separator only when the part after it has no `/`.
func tagColonIndex(imageRef string) int {
	colon := strings.LastIndex(imageRef, ":")
	if colon < 0 || colon == len(imageRef)-1 {
		return -1
	}
	if strings.Contains(imageRef[colon+1:], "/") {
		return -1
	}
	return colon
}
