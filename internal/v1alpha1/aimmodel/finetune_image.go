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

// Per-copy deployment-image resolution for fine-tuned AIMModels.
//
// Each fine-tuned (Cluster)ServiceTemplate copy carries its deployment image
// as the constants.AnnotationDeploymentImageRef annotation, computed from the
// specific source template that produced the copy. Resolving per-match (rather
// than once per fine-tuned model) means heterogeneous matches stay correct:
//
//   - versionPolicy=any spans multiple versions → each copy ships at its own
//     version's base image
//   - same aimId across owners with different base images (e.g. aim-base vs
//     aim-epyc-base) → each copy ships at its source owner's base image
//
// Resolution per match:
//
//  1. versionPolicy=pinned with model.spec.image set → use that image directly
//     (the user's spec.image is the deployment image; the tag is also the
//     version filter, so by definition all matches share it).
//  2. Otherwise (latest/any, or pinned with empty image): fetch the source
//     owner identified by match.Spec.ModelName + match.OwnerNamespace, take
//     status.imageMetadata.baseImageRef, fall back to aim-base:MAJOR.MINOR
//     synthesized from the owner's spec.image tag for legacy installs whose
//     metadata was cached before AIM_BASE_IMAGE_REF extraction existed, and
//     rebase the result onto the owner's registry+org so private mirrors
//     work without extra plumbing.
//
// When resolution defers (owner not found, no baseImageRef and no semver tag
// to fall back to), the caller skips Apply for that copy. A subsequent
// reconcile triggered by the owner's update will retry.

package aimmodel

import (
	"context"
	"fmt"
	"strings"

	"github.com/blang/semver/v4"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

// imageResolver resolves the deployment image for fine-tuned template copies.
// It fetches each source owner at most once per reconcile via an in-memory
// cache so multiple matches sharing one owner don't trigger redundant GETs.
type imageResolver struct {
	c         client.Client
	modelSpec *aimv1alpha1.AIMModelSpec
	cache     map[ownerKey]ownerInfo
}

type ownerKey struct {
	namespace, name string // namespace=="" → cluster-scoped owner
}

type ownerInfo struct {
	specImage    string
	baseImageRef string
	found        bool
}

// newImageResolver constructs a resolver for the given fine-tuned model spec.
func newImageResolver(c client.Client, modelSpec *aimv1alpha1.AIMModelSpec) *imageResolver {
	return &imageResolver{
		c:         c,
		modelSpec: modelSpec,
		cache:     map[ownerKey]ownerInfo{},
	}
}

// Resolve returns the deployment image for one match, or an error when the
// source owner is not yet ready. Callers should treat the error as a deferral
// signal: log and skip the copy, then rely on the owner watch to trigger a
// retry.
func (r *imageResolver) Resolve(ctx context.Context, match TemplateMatchResult) (string, error) {
	// Pinned policy with an explicit spec.image is authoritative: that's the
	// image the user pinned to and the tag is the version filter, so all
	// matches share it.
	if getVersionPolicy(r.modelSpec) == aimv1alpha1.AIMVersionPolicyPinned && r.modelSpec.Image != "" {
		return r.modelSpec.Image, nil
	}

	owner, err := r.lookupOwner(ctx, match.OwnerNamespace, match.Spec.ModelName)
	if err != nil {
		return "", err
	}
	if !owner.found {
		return "", fmt.Errorf("source owner %s/%s not found", match.OwnerNamespace, match.Spec.ModelName)
	}

	baseRef := owner.baseImageRef
	if baseRef == "" {
		// Legacy fallback: status.imageMetadata was cached before
		// AIM_BASE_IMAGE_REF extraction existed and we don't re-inspect a
		// model that already has metadata. Synthesize aim-base:MAJOR.MINOR
		// from the owner's spec.image tag and let rebaseImageRegistry graft
		// the registry+org back on. When the tag isn't semver-parseable we
		// can't synthesize anything sensible — fail and let the caller defer.
		baseRef = legacyBaseImageFromSource(owner.specImage)
		if baseRef == "" {
			return "", fmt.Errorf("source owner %s/%s has no baseImageRef and no semver tag to fall back to",
				match.OwnerNamespace, match.Spec.ModelName)
		}
	}

	return rebaseImageRegistry(owner.specImage, baseRef), nil
}

func (r *imageResolver) lookupOwner(ctx context.Context, namespace, name string) (ownerInfo, error) {
	if name == "" {
		return ownerInfo{}, fmt.Errorf("source template has no spec.modelName")
	}
	key := ownerKey{namespace: namespace, name: name}
	if cached, ok := r.cache[key]; ok {
		return cached, nil
	}

	var info ownerInfo
	if namespace == "" {
		var owner aimv1alpha1.AIMClusterModel
		if err := r.c.Get(ctx, client.ObjectKey{Name: name}, &owner); err != nil {
			if client.IgnoreNotFound(err) == nil {
				r.cache[key] = ownerInfo{}
				return ownerInfo{}, nil
			}
			return ownerInfo{}, fmt.Errorf("get AIMClusterModel %s: %w", name, err)
		}
		info = ownerInfo{
			specImage:    owner.Spec.Image,
			baseImageRef: owner.Spec.GetBaseImageRef(&owner.Status),
			found:        true,
		}
	} else {
		var owner aimv1alpha1.AIMModel
		if err := r.c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &owner); err != nil {
			if client.IgnoreNotFound(err) == nil {
				r.cache[key] = ownerInfo{}
				return ownerInfo{}, nil
			}
			return ownerInfo{}, fmt.Errorf("get AIMModel %s/%s: %w", namespace, name, err)
		}
		info = ownerInfo{
			specImage:    owner.Spec.Image,
			baseImageRef: owner.Spec.GetBaseImageRef(&owner.Status),
			found:        true,
		}
	}
	r.cache[key] = info
	return info, nil
}

// rebaseImageRegistry rewrites baseImageRef so its registry+org prefix matches
// sourceImage while preserving baseImageRef's image name and tag. This lets a
// fine-tuned deployment pull aim-base from the same registry/org as the source
// base model — so mirroring the base model to a private registry automatically
// covers the aim-base deployment too.
//
// Examples:
//
//	rebaseImageRegistry("ghcr.io/silogen/qwen3:0.11",
//	                    "docker.io/amdenterpriseai/aim-base:0.11")
//	  → "ghcr.io/silogen/aim-base:0.11"
//	rebaseImageRegistry("docker.io/amdenterpriseai/qwen3:0.11",
//	                    "ghcr.io/silogen/aim-base:0.11")
//	  → "docker.io/amdenterpriseai/aim-base:0.11"
//
// When sourceImage lacks a "/" (no registry+org to graft on) or is empty,
// baseImageRef is returned unchanged.
func rebaseImageRegistry(sourceImage, baseImageRef string) string {
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

// legacyBaseImageFromSource synthesizes an aim-base reference for AIMModels
// whose status.imageMetadata was populated before AIM_BASE_IMAGE_REF extraction
// existed. It returns `aim-base:MAJOR.MINOR` derived from the source image's
// tag, or "" when the tag isn't parseable as semver. The result has no
// registry+org prefix on purpose — rebaseImageRegistry grafts that on from
// the source image so the fallback inherits the same mirror the base model
// already uses.
//
// Truncating to MAJOR.MINOR matches how the aim-base image is actually
// published (e.g. `aim-base:0.11`, not `aim-base:0.11.0`).
func legacyBaseImageFromSource(sourceImage string) string {
	colon := strings.LastIndex(sourceImage, ":")
	if colon < 0 || colon == len(sourceImage)-1 {
		return ""
	}
	// Guard against registry ports like `registry.local:5000/image` by only
	// treating the last `:` as the tag separator when the part after it has
	// no `/`.
	tag := sourceImage[colon+1:]
	if strings.Contains(tag, "/") {
		return ""
	}
	v, err := semver.ParseTolerant(tag)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("aim-base:%d.%d", v.Major, v.Minor)
}
