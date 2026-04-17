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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	httpClient        *http.Client
}

// NewRegistryClient creates a new RegistryClient.
func NewRegistryClient(clientset kubernetes.Interface, operatorNamespace string) *RegistryClient {
	return &RegistryClient{
		clientset:         clientset,
		operatorNamespace: operatorNamespace,
		httpClient:        &http.Client{},
	}
}

// FetchFilter processes a single filter and returns discovered images.
// It uses the appropriate strategy based on the filter type:
// 1. Static images (exact versions, no registry query)
// 2. Tags list API (exact repos with version ranges)
// 3. Catalog API (wildcards on Docker Hub/Harbor/etc)
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

	// Strategy 2: Tags list API (exact repo, version constraints)
	// For filters without wildcards but with version constraints
	if !parsed.hasWildcard {
		images, err := c.fetchImagesUsingTagsList(ctx, spec, filter, parsed, registry)
		if err != nil {
			result.Error = err
		}
		result.Images = images
		return result
	}

	// Strategy 3: Catalog API (wildcards)
	// Route to appropriate implementation based on registry
	var images []RegistryImage
	var err error

	if registry == DockerRegistry || strings.Contains(registry, "hub.docker.com") {
		images, err = c.listDockerHubImages(ctx, spec, filter, parsed)
	} else if registry == GHCRRegistry || strings.Contains(registry, "ghcr.io") {
		images, err = c.listGitHubContainerRegistryImages(ctx, spec, filter, parsed)
	} else {
		images, err = c.listRegistryV2Images(ctx, spec, filter, parsed, registry)
	}

	result.Images = images
	result.Error = err
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

// listDockerHubImages uses the Docker Hub API to list repositories and tags.
func (c *RegistryClient) listDockerHubImages(
	ctx context.Context,
	spec aimv1alpha1.AIMClusterModelSourceSpec,
	filter aimv1alpha1.ModelSourceFilter,
	parsed parsedImageFilter,
) ([]RegistryImage, error) {
	var allImages []RegistryImage

	// Extract namespace from filter pattern (e.g., "amdenterpriseai/aim-*" -> "amdenterpriseai")
	namespace := extractDockerHubNamespace(parsed.repository)
	if namespace == "" {
		return nil, fmt.Errorf("cannot extract namespace from pattern: %s", filter.Image)
	}

	// Fetch repositories for this namespace
	repos, err := c.fetchDockerHubRepositories(ctx, namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch repos for namespace %s: %w", namespace, err)
	}

	// For each repository, check if it matches the filter pattern
	for _, repo := range repos {
		// Match against the repository pattern
		if !matchesWildcard(parsed.repository, repo) {
			continue
		}

		tags, err := c.fetchImageTags(ctx, repo, spec.ImagePullSecrets)
		if err != nil {
			// Log but continue - some repos might be inaccessible
			continue
		}

		// Filter tags by version constraints and exclusions
		for _, tag := range tags {
			img := RegistryImage{
				Registry:   DockerRegistry,
				Repository: repo,
				Tag:        tag,
			}

			// Check if this tag matches version constraints and exclusions
			if MatchesFilters(img, []aimv1alpha1.ModelSourceFilter{filter}, spec.Versions) {
				allImages = append(allImages, img)
			}
		}
	}

	return allImages, nil
}

// extractDockerHubNamespace extracts the namespace from a repository pattern.
// For example, "amdenterpriseai/aim-*" -> "amdenterpriseai"
func extractDockerHubNamespace(pattern string) string {
	parts := strings.Split(pattern, "/")
	if len(parts) >= 1 {
		// Remove wildcards from namespace
		namespace := strings.TrimRight(parts[0], "*")
		if namespace != "" && namespace != "*" {
			return namespace
		}
	}
	return ""
}

// fetchDockerHubRepositories fetches all repositories for a namespace using Docker Hub API.
func (c *RegistryClient) fetchDockerHubRepositories(ctx context.Context, namespace string) ([]string, error) {
	var repos []string
	nextURL := fmt.Sprintf("https://hub.docker.com/v2/namespaces/%s/repositories", namespace)

	for nextURL != "" {
		var result struct {
			Results []struct {
				Name string `json:"name"`
			} `json:"results"`
			Next string `json:"next"`
		}

		if err := c.fetchJSON(ctx, nextURL, "", &result); err != nil {
			return nil, err
		}

		for _, r := range result.Results {
			repos = append(repos, fmt.Sprintf("%s/%s", namespace, r.Name))
		}

		nextURL = result.Next
	}

	return repos, nil
}

// fetchJSON performs an HTTP GET and decodes the JSON response.
func (c *RegistryClient) fetchJSON(ctx context.Context, url, authToken string, target interface{}) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	if authToken != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", authToken))
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch %s: %w", url, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d from %s: %s", resp.StatusCode, url, string(body))
	}

	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("failed to decode JSON response: %w", err)
	}

	return nil
}

// listRegistryV2Images uses the Registry v2 API to list repositories and tags.
func (c *RegistryClient) listRegistryV2Images(
	ctx context.Context,
	spec aimv1alpha1.AIMClusterModelSourceSpec,
	filter aimv1alpha1.ModelSourceFilter,
	parsed parsedImageFilter,
	registry string,
) ([]RegistryImage, error) {
	var allImages []RegistryImage

	// Build keychain
	keychain, err := utils.BuildKeychain(ctx, c.clientset, c.operatorNamespace, spec.ImagePullSecrets)
	if err != nil {
		return nil, err
	}

	// Get catalog
	registryRef, err := name.NewRegistry(registry)
	if err != nil {
		return nil, fmt.Errorf("invalid registry %s: %w", registry, err)
	}

	repos, err := remote.Catalog(ctx, registryRef, remote.WithAuthFromKeychain(keychain))
	if err != nil {
		return nil, fmt.Errorf("failed to list catalog for %s: %w", registry, err)
	}

	// For each repository, check if it matches the filter pattern
	for _, repo := range repos {
		// Match against the repo path (without registry prefix)
		if !matchesWildcard(parsed.repository, repo) {
			continue
		}

		fullRepo := fmt.Sprintf("%s/%s", registry, repo)
		tags, err := c.fetchImageTags(ctx, fullRepo, spec.ImagePullSecrets)
		if err != nil {
			// Log but continue - some repos might be inaccessible
			continue
		}

		// Filter tags by version constraints and exclusions
		for _, tag := range tags {
			img := RegistryImage{
				Registry:   registry,
				Repository: repo,
				Tag:        tag,
			}

			// Check if this tag matches version constraints and exclusions
			if MatchesFilters(img, []aimv1alpha1.ModelSourceFilter{filter}, spec.Versions) {
				allImages = append(allImages, img)
			}
		}
	}

	return allImages, nil
}

// listGitHubContainerRegistryImages uses the GitHub API to list packages for organizations.
// GHCR does not support the Docker catalog API, so we use GitHub's REST API instead.
func (c *RegistryClient) listGitHubContainerRegistryImages(
	ctx context.Context,
	spec aimv1alpha1.AIMClusterModelSourceSpec,
	filter aimv1alpha1.ModelSourceFilter,
	parsed parsedImageFilter,
) ([]RegistryImage, error) {
	var allImages []RegistryImage
	var errs []error

	// Extract organization from filter pattern
	org := extractGitHubOrg(parsed.repository)
	if org == "" {
		return nil, fmt.Errorf("cannot extract GitHub organization from pattern: %s", filter.Image)
	}

	// Extract GitHub token from image pull secrets
	token, err := c.extractGitHubToken(ctx, spec.ImagePullSecrets)
	if err != nil {
		return nil, fmt.Errorf("failed to extract GitHub token: %w", err)
	}

	// Fetch packages for the organization
	packages, err := c.fetchGitHubPackages(ctx, token, org)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch packages for org %s: %w", org, err)
	}

	// Collect matching packages
	type packageInfo struct {
		org      string
		pkg      string
		fullRepo string
	}
	var matchingPackages []packageInfo

	for _, pkg := range packages {
		fullRepo := fmt.Sprintf("%s/%s", org, pkg)

		// Check if this package matches the filter pattern
		if matchesWildcard(parsed.repository, fullRepo) {
			matchingPackages = append(matchingPackages, packageInfo{
				org:      org,
				pkg:      pkg,
				fullRepo: fullRepo,
			})
		}
	}

	// Fetch tags for all matching packages concurrently
	const maxConcurrentFetches = 10
	var wg sync.WaitGroup
	var mu sync.Mutex
	semaphore := make(chan struct{}, maxConcurrentFetches)

	for _, pkgInfo := range matchingPackages {
		wg.Add(1)
		go func(pkg packageInfo) {
			defer wg.Done()

			// Acquire semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			fullRepoWithRegistry := fmt.Sprintf("%s/%s", GHCRRegistry, pkg.fullRepo)
			tags, imageTagFetchErr := c.fetchImageTags(ctx, fullRepoWithRegistry, spec.ImagePullSecrets)

			mu.Lock()
			defer mu.Unlock()

			if imageTagFetchErr != nil {
				// Track tag fetch errors but continue - other packages might succeed
				errs = append(errs, fmt.Errorf("package %q: %w", pkg.fullRepo, imageTagFetchErr))
				return
			}

			// Filter tags by version constraints
			for _, tag := range tags {
				img := RegistryImage{
					Registry:   GHCRRegistry,
					Repository: pkg.fullRepo,
					Tag:        tag,
				}

				// Check if this tag matches version constraints
				if MatchesFilters(img, []aimv1alpha1.ModelSourceFilter{filter}, spec.Versions) {
					allImages = append(allImages, img)
				}
			}
		}(pkgInfo)
	}

	wg.Wait()

	// Return any errors encountered, even if we got partial results
	if len(errs) > 0 {
		if len(allImages) == 0 {
			return nil, fmt.Errorf("no images found: %w", errors.Join(errs...))
		}
		return allImages, fmt.Errorf("partial results, encountered errors: %w", errors.Join(errs...))
	}

	return allImages, nil
}

// extractGitHubOrg extracts the GitHub organization from a repository pattern.
// For example, "silogen/aim-*" -> "silogen"
func extractGitHubOrg(pattern string) string {
	// Remove leading slash if present
	pattern = strings.TrimPrefix(pattern, "/")

	// Split by / to get org
	parts := strings.Split(pattern, "/")
	if len(parts) >= 1 {
		// Remove wildcards and check if it's valid
		org := strings.TrimRight(parts[0], "*")
		// Skip if it looks like a registry (contains a dot) or is empty/wildcard
		if org != "" && org != "*" && !strings.Contains(org, ".") {
			return org
		}
	}
	return ""
}

// extractGitHubToken extracts the GitHub token from Kubernetes image pull secrets.
// It looks for credentials for ghcr.io in the Docker config JSON.
func (c *RegistryClient) extractGitHubToken(ctx context.Context, imagePullSecrets []corev1.LocalObjectReference) (string, error) {
	if c.clientset == nil || c.operatorNamespace == "" || len(imagePullSecrets) == 0 {
		return "", fmt.Errorf("no image pull secrets configured")
	}

	// Try each secret until we find one with ghcr.io credentials
	for _, secretRef := range imagePullSecrets {
		secret, err := c.clientset.CoreV1().Secrets(c.operatorNamespace).Get(ctx, secretRef.Name, metav1.GetOptions{})
		if err != nil {
			continue
		}

		// Parse .dockerconfigjson
		dockerConfigJSON, ok := secret.Data[corev1.DockerConfigJsonKey]
		if !ok {
			continue
		}

		var dockerConfig struct {
			Auths map[string]struct {
				Auth     string `json:"auth"`
				Username string `json:"username"`
				Password string `json:"password"`
			} `json:"auths"`
		}

		if err := json.Unmarshal(dockerConfigJSON, &dockerConfig); err != nil {
			continue
		}

		// Look for ghcr.io credentials
		ghcrAuth, ok := dockerConfig.Auths[GHCRRegistry]
		if !ok {
			continue
		}

		// If password is set directly, use it
		if ghcrAuth.Password != "" {
			return ghcrAuth.Password, nil
		}

		// Otherwise decode base64 auth string (format: "username:password")
		if ghcrAuth.Auth != "" {
			decoded, err := base64.StdEncoding.DecodeString(ghcrAuth.Auth)
			if err != nil {
				continue
			}

			parts := strings.SplitN(string(decoded), ":", 2)
			if len(parts) == 2 {
				return parts[1], nil
			}
		}
	}

	return "", fmt.Errorf("no ghcr.io credentials found in image pull secrets")
}

// fetchGitHubPackages fetches all container packages for a GitHub organization using the GitHub API.
func (c *RegistryClient) fetchGitHubPackages(ctx context.Context, token, org string) ([]string, error) {
	var packages []string
	page := 1
	perPage := 100

	for {
		url := fmt.Sprintf("https://api.github.com/orgs/%s/packages?package_type=container&per_page=%d&page=%d",
			org, perPage, page)

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch packages: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("unexpected status %d from GitHub API", resp.StatusCode)
		}

		var result []struct {
			Name string `json:"name"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("failed to decode JSON response: %w", err)
		}
		_ = resp.Body.Close()

		for _, pkg := range result {
			packages = append(packages, pkg.Name)
		}

		// If we got fewer than perPage results, we're done
		if len(result) < perPage {
			break
		}

		page++
	}

	return packages, nil
}
