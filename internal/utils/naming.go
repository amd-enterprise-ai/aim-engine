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

package utils

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	// MaxKubernetesNameLength is the maximum length for Kubernetes resource names
	MaxKubernetesNameLength  = 63
	MaxKubernetesLabelLength = 63
	// DefaultHashLength is the default length of the hash suffix
	DefaultHashLength = 8
)

// NameOption configures name generation behavior.
type NameOption func(*nameConfig)

type nameConfig struct {
	hashInputs []any
	hashLength int
	maxLength  int
}

// WithHashSource specifies the values to hash for the name suffix.
// Multiple values are combined for deterministic hashing.
// Slices and maps are sorted recursively to ensure determinism.
func WithHashSource(inputs ...any) NameOption {
	return func(c *nameConfig) {
		c.hashInputs = inputs
	}
}

// WithHashLength specifies the number of characters to use from the hash (default: 8).
// Set to 0 to disable hash suffix even if hash sources are provided.
func WithHashLength(length int) NameOption {
	return func(c *nameConfig) {
		c.hashLength = length
	}
}

// WithMaxLength specifies a custom maximum length for the generated name.
// This is useful when the name will have additional suffixes added by external systems.
// For example, KServe adds "-predictor-{namespace}" to InferenceService names.
// Default is 63 (MaxKubernetesNameLength).
func WithMaxLength(length int) NameOption {
	return func(c *nameConfig) {
		c.maxLength = length
	}
}

// GenerateDerivedName creates a deterministic name for a derived resource.
// It combines multiple name parts with an optional hash suffix, ensuring the result
// is a valid Kubernetes name (lowercase alphanumeric and hyphens).
//
// Format:
//   - With hash: {part1}-{part2}-...-{partN}-{hash}
//   - Without hash: {part1}-{part2}-...-{partN}
//
// If the combined name exceeds the max length, the longest part is iteratively truncated.
//
// Options:
//   - WithHashSource(...): Values to hash for the suffix (required for hash)
//   - WithHashLength(n): Number of hash characters (default: 8)
//   - WithMaxLength(n): Maximum name length (default: 63)
//
// Example:
//
//	name, _ := GenerateDerivedName([]string{"my-service", "temp"},
//	    WithHashSource("metric=latency", "precision=fp16"))
//	// Returns: "my-service-temp-a1b2c3d4"
//
//	name, _ := GenerateDerivedName([]string{"my-service", "temp-cache"})
//	// Returns: "my-service-temp-cache" (no hash)
//
//	name, _ := GenerateDerivedName([]string{"my-service"},
//	    WithHashSource(namespace), WithMaxLength(30))
//	// Returns truncated name to fit 30 chars
func GenerateDerivedName(nameParts []string, opts ...NameOption) (string, error) {
	if len(nameParts) == 0 {
		return "", fmt.Errorf("nameParts cannot be empty")
	}

	// Apply defaults
	cfg := &nameConfig{
		hashLength: DefaultHashLength,
		maxLength:  MaxKubernetesNameLength,
	}

	// Apply options
	for _, opt := range opts {
		opt(cfg)
	}

	// Validate
	if cfg.maxLength <= 0 || cfg.maxLength > MaxKubernetesNameLength {
		return "", fmt.Errorf("maxLength must be between 1 and %d, got %d", MaxKubernetesNameLength, cfg.maxLength)
	}

	// Sanitize all parts to be Kubernetes-compliant
	sanitizedParts := make([]string, len(nameParts))
	for i, part := range nameParts {
		sanitizedParts[i] = MakeRFC1123Compliant(part)
		if sanitizedParts[i] == "" {
			sanitizedParts[i] = "part"
		}
	}

	// Compute hash from inputs (if any)
	var hashSuffix string
	if cfg.hashLength > 0 && len(cfg.hashInputs) > 0 {
		hash := computeHash(cfg.hashInputs...)
		hashSuffix = hash[:cfg.hashLength]
	}

	// Calculate current total length
	calculateLength := func(parts []string) int {
		total := 0
		for _, part := range parts {
			total += len(part) + 1
		}
		if hashSuffix != "" {
			total += len(hashSuffix)
		} else {
			total--
		}
		return total
	}

	// Iteratively truncate the longest part until we fit
	for calculateLength(sanitizedParts) > cfg.maxLength {
		longestIdx := 0
		longestLen := len(sanitizedParts[0])
		for i := 1; i < len(sanitizedParts); i++ {
			if len(sanitizedParts[i]) > longestLen {
				longestIdx = i
				longestLen = len(sanitizedParts[i])
			}
		}

		if longestLen > 1 {
			sanitizedParts[longestIdx] = sanitizedParts[longestIdx][:longestLen-1]
			sanitizedParts[longestIdx] = strings.TrimRight(sanitizedParts[longestIdx], "-")
		} else {
			break
		}
	}

	result := strings.Join(sanitizedParts, "-")
	if hashSuffix != "" {
		result += "-" + hashSuffix
	}
	return result, nil
}

// computeHash creates a deterministic hash from input values of any type.
// Arrays, slices, and maps are sorted recursively to ensure determinism.
func computeHash(inputs ...any) string {
	// Convert all inputs to deterministic JSON strings
	var jsonParts []string
	for _, input := range inputs {
		jsonStr := normalizeToJSON(input)
		jsonParts = append(jsonParts, jsonStr)
	}

	// Concatenate all JSON strings
	combined := strings.Join(jsonParts, "|")

	// Compute SHA256 hash
	hash := sha256.Sum256([]byte(combined))

	// Return hex-encoded hash
	return fmt.Sprintf("%x", hash[:])
}

// normalizeToJSON converts a value to a deterministic JSON string.
// Recursively sorts maps and slices to ensure consistent output.
func normalizeToJSON(v any) string {
	if v == nil {
		return "null"
	}

	// Normalize the value first (sort maps/slices recursively)
	normalized := normalizeDeterministic(v)

	// Marshal to JSON
	jsonBytes, err := json.Marshal(normalized)
	if err != nil {
		// Fallback to fmt.Sprintf for types that don't marshal well
		return fmt.Sprintf("%v", v)
	}

	return string(jsonBytes)
}

// normalizeDeterministic recursively normalizes a value for deterministic JSON output.
// Converts maps to sorted key-value pairs and processes nested structures.
func normalizeDeterministic(v any) any {
	if v == nil {
		return nil
	}

	switch val := v.(type) {
	case map[string]string:
		// Convert to map[string]any for processing
		m := make(map[string]any, len(val))
		for k, v := range val {
			m[k] = v
		}
		return normalizeDeterministicMap(m)
	case map[string]any:
		return normalizeDeterministicMap(val)
	case []any:
		return normalizeDeterministicSlice(val)
	default:
		// For other types (structs, primitives), return as-is
		// Structs will be marshaled with fields in declaration order
		return val
	}
}

// normalizeDeterministicMap converts a map to a sorted list of key-value pairs
func normalizeDeterministicMap(m map[string]any) []any {
	// Extract and sort keys
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build sorted result
	result := make([]any, 0, len(m)*2)
	for _, k := range keys {
		result = append(result, k, normalizeDeterministic(m[k]))
	}

	return result
}

// normalizeDeterministicSlice recursively normalizes elements in a slice
func normalizeDeterministicSlice(s []any) []any {
	result := make([]any, len(s))
	for i, elem := range s {
		result[i] = normalizeDeterministic(elem)
	}
	return result
}

var (
	labelValueRegex = regexp.MustCompile(`[^a-z0-9._-]+`)
	// invalidNameCharsRegex matches characters invalid for Kubernetes resource names
	invalidNameCharsRegex = regexp.MustCompile(`[^a-z0-9-]+`)
	// multiDashesRegex matches multiple consecutive dashes
	multiDashesRegex = regexp.MustCompile(`-+`)
)

// SanitizeLabelValue converts a string to a valid Kubernetes label value.
// Valid label values must:
// - Be empty or consist of alphanumeric characters, '-', '_' or '.'
// - Start and end with an alphanumeric character
// - Be at most 63 characters
// Returns an error if the sanitized value is empty.
func SanitizeLabelValue(s string) (string, error) {
	// Replace invalid characters with underscores
	sanitized := strings.ToLower(s)
	sanitized = labelValueRegex.ReplaceAllString(sanitized, "_")

	// Trim leading and trailing non-alphanumeric characters
	sanitized = strings.TrimLeft(sanitized, "_.-")
	sanitized = strings.TrimRight(sanitized, "_.-")

	// Truncate to maximum label value length
	if len(sanitized) > MaxKubernetesLabelLength {
		sanitized = sanitized[:MaxKubernetesLabelLength]
		// Trim trailing non-alphanumeric after truncation
		sanitized = strings.TrimRight(sanitized, "_.-")
	}

	if sanitized == "" {
		return "", fmt.Errorf("label value is empty after sanitization")
	}

	return sanitized, nil
}

type ImageParts struct {
	Registry   string // e.g., "ghcr.io" or "docker.io"
	Repository string // e.g., "silogen/llama-3-8b" (full repository path)
	Name       string // e.g., "llama-3-8b" (just the image name, last component)
	Tag        string // e.g., "v1.2.0" or "abc123" (first 6 chars of digest)
}

func ExtractImageParts(image string) (ImageParts, error) {
	if image == "" {
		return ImageParts{}, fmt.Errorf("image reference is empty")
	}

	// Extract registry, repository path, and tag/digest
	// Examples:
	//   ghcr.io/silogen/llama-3-8b:v1.2.0
	//   docker.io/library/nginx:latest
	//   localhost:5000/my-image:dev
	//   nginx:latest (implicitly docker.io/nginx:latest)

	var registry, repository string

	// Check if there's an explicit registry
	firstSlash := strings.Index(image, "/")
	if firstSlash > 0 {
		firstPart := image[:firstSlash]
		// Registry has dots (domain) or colon (port) or is "localhost"
		if strings.Contains(firstPart, ".") || strings.Contains(firstPart, ":") || firstPart == "localhost" {
			registry = firstPart
			repository = image[firstSlash+1:]
		} else {
			// No explicit registry, e.g., "myorg/myimage:tag"
			registry = "docker.io"
			repository = image
		}
	} else {
		// No slash at all, e.g., "nginx:latest"
		registry = "docker.io"
		repository = image
	}

	// Extract just the image name (last component) and tag/digest from repository
	repositoryParts := strings.Split(repository, "/")
	lastPart := repositoryParts[len(repositoryParts)-1]

	var imageName, imageTag, repositoryPath string

	// Handle digest-based references (@sha256:...)
	if strings.Contains(lastPart, "@") {
		digestParts := strings.SplitN(lastPart, "@", 2)
		imageName = digestParts[0]

		if len(digestParts) != 2 || digestParts[1] == "" {
			return ImageParts{}, fmt.Errorf("malformed digest reference: %s", image)
		}

		digest := digestParts[1]
		// Extract first 6 chars after the colon (e.g., sha256:abc123 -> abc123)
		colonIdx := strings.Index(digest, ":")
		if colonIdx == -1 {
			return ImageParts{}, fmt.Errorf("malformed digest (missing colon): %s", digest)
		}

		hashStart := colonIdx + 1
		if hashStart >= len(digest) {
			return ImageParts{}, fmt.Errorf("malformed digest (no hash after colon): %s", digest)
		}

		hashEnd := hashStart + 6
		if hashEnd > len(digest) {
			hashEnd = len(digest)
		}
		imageTag = digest[hashStart:hashEnd]

	} else if strings.Contains(lastPart, ":") {
		// Handle tag-based references (:tag)
		tagParts := strings.SplitN(lastPart, ":", 2)
		imageName = tagParts[0]

		if len(tagParts) != 2 || tagParts[1] == "" {
			return ImageParts{}, fmt.Errorf("malformed tag reference (empty tag): %s", image)
		}
		imageTag = tagParts[1]

	} else {
		// No tag or digest specified - implicit :latest
		imageName = lastPart
		imageTag = "latest"
	}

	// Build full repository path (without tag/digest)
	// Replace the last part with just the image name (strip tag/digest)
	repositoryParts[len(repositoryParts)-1] = imageName
	repositoryPath = strings.Join(repositoryParts, "/")

	// Sanitize components
	registry = sanitizeNameComponent(registry)
	imageName = sanitizeNameComponent(imageName)
	imageTag = sanitizeNameComponent(imageTag)

	if registry == "" {
		return ImageParts{}, fmt.Errorf("registry is empty after sanitization")
	}
	if imageName == "" {
		return ImageParts{}, fmt.Errorf("image name is empty after sanitization")
	}
	if imageTag == "" {
		return ImageParts{}, fmt.Errorf("image tag is empty after sanitization")
	}

	return ImageParts{
		Registry:   registry,
		Repository: repositoryPath,
		Name:       imageName,
		Tag:        imageTag,
	}, nil
}

// sanitizeNameComponent sanitizes a name component for Kubernetes resource names
func sanitizeNameComponent(s string) string {
	s = strings.ToLower(s)
	s = invalidNameCharsRegex.ReplaceAllString(s, "-")
	s = multiDashesRegex.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}
