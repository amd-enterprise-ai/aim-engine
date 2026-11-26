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
// images. The helpers are version-agnostic: they operate on raw image
// strings.
package aimimage

import (
	"regexp"
	"strings"
)

// preReleaseTagRegex matches version tags with a pre-release suffix, such as
// `2026.11.0-rc11`, `0.11-rc21`, or `2026.15.0-pr1656-rc2`, and captures
// MAJOR and MINOR. Build metadata (`0.11.0+build.1`) is not a pre-release.
var preReleaseTagRegex = regexp.MustCompile(`^v?(\d+)\.(\d+)(?:\.\d+)?-[0-9A-Za-z.-]+$`)

// ResolveBaseImage returns the image a weight-replacing derivation deploys
// for the recorded aim-base reference baseImageRef.
//
// The image name, tag, and digest come from baseImageRef. The registry and
// org come from registry when it is set, and otherwise from sourceImage, the
// model image the derivation starts from, so a mirror that hosts both the
// model images and aim-base needs no configuration. A pre-release tag is
// replaced by its MAJOR.MINOR rolling tag, because pre-release aim-base tags
// recorded by model builds are not published; release tags and digests are
// kept exactly.
//
// Examples:
//
//	ResolveBaseImage("docker.io/silogenai/aim-base:0.13.1",
//	                 "registry.example.com/mirror/aim-qwen3:0.13.0", "")
//	  → "registry.example.com/mirror/aim-base:0.13.1"
//	ResolveBaseImage("docker.io/silogenai/aim-base:2026.11.0-rc11",
//	                 "docker.io/amdenterpriseai/aim-gemma4:2026.11.0-rc1",
//	                 "registry.example.com/aim")
//	  → "registry.example.com/aim/aim-base:2026.11"
func ResolveBaseImage(baseImageRef, sourceImage, registry string) string {
	if baseImageRef == "" {
		return ""
	}
	ref := rollPreReleaseTag(baseImageRef)
	prefix := registry
	if prefix == "" {
		prefix = repositoryPrefix(sourceImage)
	}
	if prefix == "" {
		return ref
	}
	return prefix + "/" + ref[strings.LastIndex(ref, "/")+1:]
}

// repositoryPrefix returns the registry and org of imageRef, everything
// before the last path segment, or "" when imageRef has no path.
func repositoryPrefix(imageRef string) string {
	name := imageRef
	if at := strings.IndexByte(name, '@'); at >= 0 {
		name = name[:at]
	}
	slash := strings.LastIndex(name, "/")
	if slash <= 0 {
		return ""
	}
	return name[:slash]
}

// rollPreReleaseTag replaces a pre-release tag on imageRef with its
// MAJOR.MINOR rolling tag. Digest references and other tags are returned
// unchanged.
func rollPreReleaseTag(imageRef string) string {
	if strings.Contains(imageRef, "@") {
		return imageRef
	}
	colon := tagColonIndex(imageRef)
	if colon < 0 {
		return imageRef
	}
	m := preReleaseTagRegex.FindStringSubmatch(imageRef[colon+1:])
	if m == nil {
		return imageRef
	}
	return imageRef[:colon] + ":" + m[1] + "." + m[2]
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
