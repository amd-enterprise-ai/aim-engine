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

package aimservice

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// FindMatchingCustomModel searches for an existing AIMModel that matches the custom model spec.
// Matching is based on:
// - spec.image == custom.baseImage
// - spec.modelSources match (including env vars for S3 endpoint differentiation)
//
// Only namespace-scoped models are searched since custom models from AIMService are always
// namespace-scoped.
//
// Returns nil if no matching model is found.
func FindMatchingCustomModel(
	ctx context.Context,
	c client.Client,
	namespace string,
	custom *aimv1alpha1.AIMServiceModelCustom,
) (*aimv1alpha1.AIMModel, error) {
	logger := log.FromContext(ctx)

	if custom == nil {
		return nil, nil
	}

	// List all models in the namespace
	var modelList aimv1alpha1.AIMModelList
	if err := c.List(ctx, &modelList, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("failed to list AIMModels: %w", err)
	}

	modelID := ""
	if len(custom.ModelSources) > 0 {
		modelID = custom.ModelSources[0].ModelID
	}
	logger.V(1).Info("searching for matching custom model", "modelId", modelID, "baseImage", custom.BaseImage)

	for i := range modelList.Items {
		model := &modelList.Items[i]

		// Check if base image matches
		if model.Spec.Image != custom.BaseImage {
			continue
		}

		// Check if modelSources match
		if !modelSourcesMatch(model.Spec.ModelSources, custom.ModelSources) {
			continue
		}

		logger.V(1).Info("found matching custom model", "name", model.Name)
		return model, nil
	}

	logger.V(1).Info("no matching custom model found")
	return nil, nil
}

// modelSourcesMatch compares two slices of AIMModelSource for equality.
// Order matters - sources must be in the same order.
func modelSourcesMatch(a, b []aimv1alpha1.AIMModelSource) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if !modelSourceEquals(a[i], b[i]) {
			return false
		}
	}

	return true
}

// modelSourceEquals compares two AIMModelSource for equality.
// Compares modelId, sourceUri, and env vars (for S3 endpoint differentiation).
func modelSourceEquals(a, b aimv1alpha1.AIMModelSource) bool {
	if a.ModelID != b.ModelID {
		return false
	}
	if a.SourceURI != b.SourceURI {
		return false
	}

	// Compare relevant env vars (AWS_ENDPOINT_URL is important for S3 differentiation)
	aEndpoint := getEnvValue(a.Env, "AWS_ENDPOINT_URL")
	bEndpoint := getEnvValue(b.Env, "AWS_ENDPOINT_URL")
	if aEndpoint != bEndpoint {
		return false
	}

	return true
}

// getEnvValue extracts the value of an environment variable by name.
// Returns empty string if not found or if it uses a valueFrom reference.
func getEnvValue(envVars []corev1.EnvVar, name string) string {
	for _, env := range envVars {
		if env.Name == name {
			return env.Value
		}
	}
	return ""
}

// GenerateCustomModelName generates a unique name for a custom model.
// Uses the existing utils.GenerateDerivedName to ensure consistent naming
// with proper sanitization and hash suffix.
// Format: {modelId-sanitized}-{hash}
func GenerateCustomModelName(custom *aimv1alpha1.AIMServiceModelCustom) string {
	if len(custom.ModelSources) == 0 {
		// No model sources - generate a generic name with hash
		name, _ := utils.GenerateDerivedName(
			[]string{"custom-model"},
			utils.WithHashSource(custom.BaseImage),
		)
		return name
	}

	// Use the first model source's modelId as the base name part
	modelID := custom.ModelSources[0].ModelID

	// Build hash inputs for uniqueness (includes endpoint for S3 differentiation)
	hashInputs := []any{custom.BaseImage, modelID, custom.ModelSources[0].SourceURI}
	if endpoint := getEnvValue(custom.ModelSources[0].Env, "AWS_ENDPOINT_URL"); endpoint != "" {
		hashInputs = append(hashInputs, endpoint)
	}

	// GenerateDerivedName handles sanitization, length limits, and hash suffix
	name, _ := utils.GenerateDerivedName(
		[]string{modelID},
		utils.WithHashSource(hashInputs...),
	)

	return name
}
