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
	"regexp"
	"strings"
)

// majorMinorTagRegex matches semver-shaped image tags so we can extract
// MAJOR.MINOR consistently across the canonical forms aim-* images use:
//
//	0.11           (short — `blang/semver.ParseTolerant` accepts this)
//	0.11.0         (full)
//	v0.11.0        (v-prefixed)
//	0.11.0-rc1     (full + prerelease — `ParseTolerant` accepts this)
//	0.11-rc21      (short + prerelease — `ParseTolerant` REJECTS this with
//	                "Short version cannot contain PreRelease/Build meta data";
//	                aim-base build pipelines stamp this exact form so we
//	                cannot afford to lose it)
//	0.11.0+build.1 (build metadata)
//
// Tags that don't match (e.g. `latest`, `sha-abc123`, empty) yield no
// match and callers leave the original tag in place.
var majorMinorTagRegex = regexp.MustCompile(`^v?(\d+)\.(\d+)(?:\.\d+)?(?:[-+].*)?$`)

// RebaseRegistry rewrites baseImageRef so its registry+org prefix matches
// sourceImage while preserving baseImageRef's image name and tag. This lets a
// fine-tuned deployment pull aim-base from the same registry/org as the source
// base model — so mirroring the base model to a private registry automatically
// covers the aim-base deployment too.
//
// Examples:
//
//	RebaseRegistry("ghcr.io/silogen/qwen3:0.11",
//	               "docker.io/amdenterpriseai/aim-base:0.11")
//	  → "ghcr.io/silogen/aim-base:0.11"
//	RebaseRegistry("docker.io/amdenterpriseai/qwen3:0.11",
//	               "ghcr.io/silogen/aim-base:0.11")
//	  → "docker.io/amdenterpriseai/aim-base:0.11"
//
// When sourceImage lacks a "/" (no registry+org to graft on) or is empty,
// baseImageRef is returned unchanged.
func RebaseRegistry(sourceImage, baseImageRef string) string {
	srcSlash := strings.LastIndex(sourceImage, "/")
	if srcSlash <= 0 {
		return baseImageRef
	}
	prefix := sourceImage[:srcSlash]

	baseSlash := strings.LastIndex(baseImageRef, "/")
	if baseSlash < 0 {
		return prefix + "/" + baseImageRef
	}
	return prefix + baseImageRef[baseSlash:]
}

// LegacyBaseImageFromSource synthesizes an aim-base reference for AIMModels
// whose status.imageMetadata was populated before AIM_BASE_IMAGE_REF
// extraction existed. It returns `aim-base:MAJOR.MINOR` derived from the
// source image's tag, or "" when the tag isn't parseable as semver. The
// result has no registry+org prefix on purpose — RebaseRegistry grafts that
// on from the source image so the fallback inherits the same mirror the base
// model already uses.
//
// Truncating to MAJOR.MINOR matches how the aim-base image is actually
// published (e.g. `aim-base:0.11`, not `aim-base:0.11.0`).
func LegacyBaseImageFromSource(sourceImage string) string {
	colon := tagColonIndex(sourceImage)
	if colon < 0 {
		return ""
	}
	mm, ok := extractMajorMinor(sourceImage[colon+1:])
	if !ok {
		return ""
	}
	return "aim-base:" + mm
}

// NormalizeBaseImageTag rewrites baseImageRef's tag to MAJOR.MINOR when the
// tag parses as semver. This collapses release-candidate / patch tags
// (e.g. `aim-base:0.11-rc21`, `aim-base:0.11.2`) onto the stable MAJOR.MINOR
// tag aim-base actually publishes (e.g. `aim-base:0.11`).
//
// Applied to AIM_BASE_IMAGE_REF values extracted from the source image's OCI
// config: build pipelines often stamp the in-progress release tag
// (`0.11-rc21`), but the deployable aim-base image is always tagged with its
// MAJOR.MINOR rolling tag. Truncating here makes downstream rebases produce
// a tag that actually resolves in the registry.
//
// When baseImageRef has no tag, the tag isn't semver-parseable, or only the
// registry port (`registry.local:5000/image`) looks like a tag, baseImageRef
// is returned unchanged.
func NormalizeBaseImageTag(baseImageRef string) string {
	colon := tagColonIndex(baseImageRef)
	if colon < 0 {
		return baseImageRef
	}
	mm, ok := extractMajorMinor(baseImageRef[colon+1:])
	if !ok {
		return baseImageRef
	}
	return baseImageRef[:colon] + ":" + mm
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
// status denormalisation. The companion `NormalizeBaseImageTag` above
// truncates a SemVer-ish tag to MAJOR.MINOR; use that for rebase paths
// that must match a stable rolling tag.
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

// extractMajorMinor parses tag against majorMinorTagRegex and returns the
// MAJOR.MINOR substring (e.g. `0.11`) along with ok=true on match. Tags
// that don't look like a semver release tag yield ok=false and callers
// fall back to leaving the original tag alone.
func extractMajorMinor(tag string) (string, bool) {
	m := majorMinorTagRegex.FindStringSubmatch(tag)
	if m == nil {
		return "", false
	}
	return m[1] + "." + m[2], true
}
