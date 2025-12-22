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
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

const (
	// LabelKeyModelSource is the label key used to identify the model source that created a cluster model.
	LabelKeyModelSource = "aim.eai.amd.com/model-source"

	maxModelNameLength = 63
)

var (
	// invalidNameChars matches characters that aren't valid in Kubernetes names.
	invalidNameChars = regexp.MustCompile(`[^a-z0-9-]`)
	// multiDashes matches multiple consecutive dashes.
	multiDashes = regexp.MustCompile(`-+`)
)

// buildClusterModel creates an AIMClusterModel from a source and discovered image.
func buildClusterModel(
	source *aimv1alpha1.AIMClusterModelSource,
	img RegistryImage,
) *aimv1alpha1.AIMClusterModel {
	return &aimv1alpha1.AIMClusterModel{
		ObjectMeta: metav1.ObjectMeta{
			Name: generateModelName(img.ToImageURI()),
			Labels: map[string]string{
				LabelKeyModelSource: source.Name,
			},
		},
		Spec: aimv1alpha1.AIMModelSpec{
			Image: img.ToImageURI(),
		},
	}
}

// generateModelName creates a Kubernetes-valid name from an image URI.
// Format: <truncated-image-name>-<tag>-<hash>
//
// Examples:
//
//	ghcr.io/silogen/llama-3-8b:v1.2.0 -> llama-3-8b-v1-2-0-a1b2c3d4
//	registry.example.com/models/mistral:latest -> mistral-latest-e5f6g7h8
func generateModelName(imageURI string) string {
	// Extract last path component (e.g., "llama-3-8b:v1.2.0" from "ghcr.io/silogen/llama-3-8b:v1.2.0")
	lastSlash := strings.LastIndex(imageURI, "/")
	lastPart := imageURI[lastSlash+1:]

	// Parse image name and tag/digest
	imageName, imageTag := parseImageRef(lastPart)

	// Sanitize and apply defaults
	imageName = sanitizeNameComponent(imageName)
	imageTag = sanitizeNameComponent(imageTag)
	if imageName == "" {
		imageName = "model"
	}
	if imageTag == "" {
		imageTag = "notag"
	}

	// Hash ensures uniqueness even when truncated
	hash := sha256.Sum256([]byte(imageURI))
	suffix := fmt.Sprintf("-%s-%x", imageTag, hash[:4]) // 4 bytes = 8 hex chars

	// Truncate image name to fit within 63 chars
	maxLen := maxModelNameLength - len(suffix)
	if len(imageName) > maxLen {
		imageName = strings.TrimRight(imageName[:maxLen], "-")
	}

	return imageName + suffix
}

// parseImageRef extracts image name and tag from "name:tag" or "name@sha256:..." format.
func parseImageRef(ref string) (name, tag string) {
	if idx := strings.Index(ref, "@"); idx != -1 {
		// Digest reference: name@sha256:abc123...
		name = ref[:idx]
		if colonIdx := strings.Index(ref[idx:], ":"); colonIdx != -1 {
			start := idx + colonIdx + 1
			end := min(start+6, len(ref))
			tag = ref[start:end] // First 6 chars of digest hash
		}
		return
	}
	if idx := strings.LastIndex(ref, ":"); idx != -1 {
		// Tag reference: name:tag
		return ref[:idx], ref[idx+1:]
	}
	// No tag - implicit latest
	return ref, "latest"
}

// sanitizeNameComponent sanitizes a name component for Kubernetes resource names.
func sanitizeNameComponent(s string) string {
	s = strings.ToLower(s)
	s = invalidNameChars.ReplaceAllString(s, "-")
	s = multiDashes.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}
