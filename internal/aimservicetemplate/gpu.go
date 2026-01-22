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

// Package aimservicetemplate provides GPU availability functions for the Pipeline pattern.
// Core GPU detection logic is provided by internal/utils/resources.go.
package aimservicetemplate

import (
	"context"
	"fmt"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// GetGPUAvailabilityHealth returns GPU availability as component health.
// This is called from GetComponentHealth to add GPU availability status.
// The template is Ready if ANY of the specified GPU models is available.
func GetGPUAvailabilityHealth(ctx context.Context, k8sClient client.Client, spec aimv1alpha1.AIMServiceTemplateSpecCommon) controllerutils.ComponentHealth {
	// If no GPU required, return empty health (no component to track)
	if !TemplateRequiresGPU(spec) {
		return controllerutils.ComponentHealth{}
	}

	gpuModels := spec.Gpu.Models

	// Check if any GPU model is available
	for _, model := range gpuModels {
		normalizedModel := utils.NormalizeGPUModel(model)
		available, err := utils.IsGPUAvailable(ctx, k8sClient, model)
		if err != nil {
			continue // Try next model
		}
		if available {
			return controllerutils.ComponentHealth{
				Component: "GPU",
				State:     constants.AIMStatusReady,
				Reason:    "GPUAvailable",
				Message:   fmt.Sprintf("GPU model '%s' is available", normalizedModel),
			}
		}
	}

	// None of the required GPU models are available
	availableGPUs, err := utils.ListAvailableGPUs(ctx, k8sClient)
	if err != nil {
		return controllerutils.ComponentHealth{
			Component: "GPU",
			State:     constants.AIMStatusDegraded,
			Reason:    "GPUCheckFailed",
			Message:   fmt.Sprintf("Failed to check GPU availability: %v", err),
			Errors:    []error{controllerutils.NewInfrastructureError("GPUCheckFailed", "Failed to check GPU availability", err)},
		}
	}

	availableStr := "none"
	if len(availableGPUs) > 0 {
		availableStr = strings.Join(availableGPUs, ", ")
	}

	return controllerutils.ComponentHealth{
		Component: "GPU",
		State:     constants.AIMStatusNotAvailable,
		Reason:    "GPUNotAvailable",
		Message:   fmt.Sprintf("Required GPU models '%s' not available in cluster. Available: %s", strings.Join(gpuModels, ", "), availableStr),
	}
}

// CheckGPUAvailability checks whether any GPU model declared by the template exists in the cluster.
// Returns the normalized GPU model name (first available one) and whether it's available.
func CheckGPUAvailability(
	ctx context.Context,
	k8sClient client.Client,
	spec aimv1alpha1.AIMServiceTemplateSpecCommon,
) (normalizedModel string, available bool, err error) {
	if !TemplateRequiresGPU(spec) {
		return "", true, nil
	}

	// Check each GPU model - template is ready if ANY model is available
	for _, model := range spec.Gpu.Models {
		model = strings.TrimSpace(model)
		normalizedModel = utils.NormalizeGPUModel(model)

		available, err = utils.IsGPUAvailable(ctx, k8sClient, model)
		if err != nil {
			continue // Try next model
		}
		if available {
			return normalizedModel, true, nil
		}
	}

	// Return the first model's normalized name for error reporting
	if len(spec.Gpu.Models) > 0 {
		normalizedModel = utils.NormalizeGPUModel(spec.Gpu.Models[0])
	}
	return normalizedModel, false, nil
}

// IsGPUAvailableForSpec checks if any required GPU is available based on pre-fetched GPU resources.
// This is the fast-path check used during reconciliation when GPU resources have already been fetched.
// Returns true if no GPU is required, or if ANY required GPU is found in the provided resources.
func IsGPUAvailableForSpec(spec aimv1alpha1.AIMServiceTemplateSpecCommon, gpuResources map[string]utils.GPUResourceInfo, gpuFetchErr error) bool {
	if !TemplateRequiresGPU(spec) {
		return true
	}
	if gpuFetchErr != nil {
		return false
	}
	// Check if ANY of the GPU models is available
	for _, model := range spec.Gpu.Models {
		normalizedModel := utils.NormalizeGPUModel(model)
		if _, available := gpuResources[normalizedModel]; available {
			return true
		}
	}
	return false
}

// GetGPUHealthFromResources returns GPU availability as component health based on pre-fetched GPU resources.
// This is the shared implementation used by both namespace-scoped and cluster-scoped template reconcilers.
// It avoids re-fetching GPU resources by using the already-fetched gpuResources map.
// The template is Ready if ANY of the specified GPU models is available.
func GetGPUHealthFromResources(
	spec aimv1alpha1.AIMServiceTemplateSpecCommon,
	gpuResources map[string]utils.GPUResourceInfo,
	gpuFetchErr error,
) controllerutils.ComponentHealth {
	// If no GPU required, return empty health (no component to track)
	if !TemplateRequiresGPU(spec) {
		return controllerutils.ComponentHealth{}
	}

	// Check for fetch error
	if gpuFetchErr != nil {
		return controllerutils.ComponentHealth{
			Component: "GPU",
			State:     constants.AIMStatusDegraded,
			Reason:    "GPUCheckFailed",
			Message:   "Failed to check GPU availability: " + gpuFetchErr.Error(),
			Errors:    []error{controllerutils.NewInfrastructureError("GPUCheckFailed", "Failed to check GPU availability", gpuFetchErr)},
		}
	}

	gpuModels := spec.Gpu.Models

	// Check if ANY GPU model is available in the pre-fetched resources
	for _, model := range gpuModels {
		normalizedModel := utils.NormalizeGPUModel(model)
		if _, available := gpuResources[normalizedModel]; available {
			return controllerutils.ComponentHealth{
				Component: "GPU",
				State:     constants.AIMStatusReady,
				Reason:    "GPUAvailable",
				Message:   "GPU model '" + normalizedModel + "' is available",
			}
		}
	}

	// None available - report error
	availableGPUs := make([]string, 0, len(gpuResources))
	for model := range gpuResources {
		availableGPUs = append(availableGPUs, model)
	}
	availableStr := "none"
	if len(availableGPUs) > 0 {
		availableStr = strings.Join(availableGPUs, ", ")
	}

	return controllerutils.ComponentHealth{
		Component: "GPU",
		State:     constants.AIMStatusNotAvailable,
		Reason:    "GPUNotAvailable",
		Message:   "Required GPU models '" + strings.Join(gpuModels, ", ") + "' not available in cluster. Available: " + availableStr,
	}
}
