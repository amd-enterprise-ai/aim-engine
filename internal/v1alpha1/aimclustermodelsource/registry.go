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
	"fmt"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// FilterResult captures the result of processing a single filter.
type FilterResult struct {
	Filter aimv1alpha1.ModelSourceFilter
	Images []RegistryImage // Images discovered (may be partial)
	Error  error           // Error encountered (nil = full success)
}

// RegistryImage represents a discovered image from a registry.
type RegistryImage struct {
	Registry   string
	Repository string
	Tag        string
}

// ToImageURI returns the full image URI in registry/repository:tag format.
// Special handling for docker.io which doesn't require the registry prefix.
func (img RegistryImage) ToImageURI() string {
	// Docker Hub special case - no registry prefix
	if img.Registry == DockerRegistry || img.Registry == "" {
		return fmt.Sprintf("%s:%s", img.Repository, img.Tag)
	}
	return fmt.Sprintf("%s/%s:%s", img.Registry, img.Repository, img.Tag)
}

// RegistryClient handles image discovery from container registries.
type RegistryClient struct {
	clientset         kubernetes.Interface
	operatorNamespace string
}

// NewRegistryClient creates a new RegistryClient.
func NewRegistryClient(clientset kubernetes.Interface, operatorNamespace string) *RegistryClient {
	return &RegistryClient{
		clientset:         clientset,
		operatorNamespace: operatorNamespace,
	}
}

// FetchFilter processes a single filter and returns discovered images.
// It uses the appropriate strategy based on the filter type:
// 1. Static images (exact versions, no registry query)
// 2. Tags list API (exact repos with version ranges)
//
// Wildcard filters are intentionally rejected - AIMClusterModelSource now
// requires explicit repository lists.
func (c *RegistryClient) FetchFilter(
	ctx context.Context,
	spec aimv1alpha1.AIMClusterModelSourceSpec,
	filter aimv1alpha1.ModelSourceFilter,
) FilterResult {
	result := FilterResult{Filter: filter}

	// Parse the filter to understand what strategy to use
	parsed := parseImageFilter(filter.Image)

	// Determine the registry
	registry := parsed.registry
	if registry == "" {
		registry = spec.Registry
		if registry == "" {
			registry = DockerRegistry
		}
	}

	// Strategy 1: Static images (exact tag in filter)
	if parsed.tag != "" && !parsed.hasWildcard {
		result.Images = []RegistryImage{{
			Registry:   registry,
			Repository: parsed.repository,
			Tag:        parsed.tag,
		}}
		return result
	}

	// Wildcards are no longer supported.
	if parsed.hasWildcard {
		result.Error = fmt.Errorf("wildcard filters are not supported: %q (use explicit image list entries)", filter.Image)
		return result
	}

	// Strategy 2: Tags list API (exact repo, version constraints)
	images, err := c.fetchImagesUsingTagsList(ctx, spec, filter, parsed, registry)
	if err != nil {
		result.Error = err
	}
	result.Images = images
	return result
}

// fetchImagesUsingTagsList queries specific repositories using the tags list API.
// This works on all registries (including ghcr.io) when you have exact repository names.
func (c *RegistryClient) fetchImagesUsingTagsList(
	ctx context.Context,
	spec aimv1alpha1.AIMClusterModelSourceSpec,
	filter aimv1alpha1.ModelSourceFilter,
	parsed parsedImageFilter,
	registry string,
) ([]RegistryImage, error) {
	var allImages []RegistryImage

	// Determine which versions to use
	versions := filter.Versions
	if len(versions) == 0 {
		versions = spec.Versions
	}

	// Build full repository reference
	var fullRepo string
	if registry == DockerRegistry {
		fullRepo = parsed.repository
	} else {
		fullRepo = fmt.Sprintf("%s/%s", registry, parsed.repository)
	}

	// Fetch all tags for this repository
	tags, err := c.fetchImageTags(ctx, fullRepo, spec.ImagePullSecrets)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch tags for %s: %w", fullRepo, err)
	}

	// Filter tags by version constraints and build RegistryImage list
	for _, tag := range tags {
		img := RegistryImage{
			Registry:   registry,
			Repository: parsed.repository,
			Tag:        tag,
		}

		// Check if this tag matches the version constraints and other filter criteria
		if MatchesFilters(img, []aimv1alpha1.ModelSourceFilter{filter}, versions) {
			allImages = append(allImages, img)
		}
	}

	return allImages, nil
}

// fetchImageTags fetches all tags for a repository using go-containerregistry.
func (c *RegistryClient) fetchImageTags(
	ctx context.Context,
	repository string,
	imagePullSecrets []corev1.LocalObjectReference,
) ([]string, error) {
	keychain, err := utils.BuildKeychain(ctx, c.clientset, c.operatorNamespace, imagePullSecrets)
	if err != nil {
		return nil, err
	}

	repoRef, err := name.NewRepository(repository)
	if err != nil {
		return nil, fmt.Errorf("invalid repository %s: %w", repository, err)
	}

	tags, err := remote.List(repoRef, remote.WithAuthFromKeychain(keychain), remote.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to list tags for %s: %w", repository, err)
	}

	return tags, nil
}
