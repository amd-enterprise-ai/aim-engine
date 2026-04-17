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

package aimartifact

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

// ModelIDFromSourceURI extracts the model ID (org/model) from a source URI.
// For hf:// URIs, strips the scheme prefix. For other schemes, returns the path portion.
func ModelIDFromSourceURI(sourceUri string) string {
	if idx := strings.Index(sourceUri, "://"); idx != -1 {
		return sourceUri[idx+3:]
	}
	return sourceUri
}

// ComputeCacheKey produces a deterministic hash of the effective download filter.
// The model identity is captured in the S3 path via ModelIDFromSourceURI, so
// the cache key only differentiates filter variants of the same model.
func ComputeCacheKey(filter *aimv1alpha1.AIMDownloadFilter) string {
	var inc, exc []string
	if filter != nil {
		inc = append(inc, filter.Include...)
		exc = append(exc, filter.Exclude...)
	}
	sort.Strings(inc)
	sort.Strings(exc)

	input := strings.Join(inc, ",") + "\n" + strings.Join(exc, ",")
	hash := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", hash[:8]) // 16 hex chars
}

// CacheS3Path returns the S3 prefix for a cached artifact's data.
func CacheS3Path(baseUri, modelId, cacheKey string) string {
	return strings.TrimRight(baseUri, "/") + "/" + modelId + "/" + cacheKey
}

// ManifestS3Path returns the S3 path for a cached artifact's manifest.
func ManifestS3Path(baseUri, modelId, cacheKey string) string {
	return CacheS3Path(baseUri, modelId, cacheKey) + "/manifest.json"
}

var cacheHTTPClient = &http.Client{Timeout: 5 * time.Second}

// CheckCacheHit checks whether a manifest.json exists in the S3 cache for the
// given artifact. Returns the resolved S3 path on hit, empty string on miss.
// Uses a direct HTTP HEAD request against the S3 endpoint (path-style addressing).
func CheckCacheHit(ctx context.Context, cacheConfig *aimv1alpha1.ArtifactCacheConfig, sourceUri string, filter *aimv1alpha1.AIMDownloadFilter) (string, error) {
	if cacheConfig == nil || !cacheConfig.Enabled || cacheConfig.S3URI == "" {
		return "", nil
	}

	endpointURL := envValue(cacheConfig.Env, "AWS_ENDPOINT_URL")
	if endpointURL == "" {
		return "", fmt.Errorf("AWS_ENDPOINT_URL not set in artifact cache config")
	}

	modelId := ModelIDFromSourceURI(sourceUri)
	cacheKey := ComputeCacheKey(filter)
	manifestS3 := ManifestS3Path(cacheConfig.S3URI, modelId, cacheKey)

	httpURL := s3PathToHTTPURL(endpointURL, manifestS3)

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, httpURL, nil)
	if err != nil {
		return "", fmt.Errorf("building cache check request: %w", err)
	}

	resp, err := cacheHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("cache check request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusOK {
		return CacheS3Path(cacheConfig.S3URI, modelId, cacheKey), nil
	}
	return "", nil
}

// s3PathToHTTPURL converts an S3 URI (s3://bucket/key) to a path-style HTTP URL
// using the given endpoint (e.g. http://seaweedfs:8333).
func s3PathToHTTPURL(endpointURL, s3URI string) string {
	path := strings.TrimPrefix(s3URI, "s3://")
	return strings.TrimRight(endpointURL, "/") + "/" + path
}

func envValue(envs []corev1.EnvVar, name string) string {
	for _, e := range envs {
		if e.Name == name {
			return e.Value
		}
	}
	return ""
}
