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
func GetGPUAvailabilityHealth(ctx context.Context, k8sClient client.Client, spec aimv1alpha1.AIMServiceTemplateSpecCommon) controllerutils.ComponentHealth {
	// If no GPU required, return empty health (no component to track)
	if !TemplateRequiresGPU(spec) {
		return controllerutils.ComponentHealth{}
	}

	gpuModel := spec.Gpu.Model
	normalizedModel := utils.NormalizeGPUModel(gpuModel)

	available, err := utils.IsGPUAvailable(ctx, k8sClient, gpuModel)
	if err != nil {
		return controllerutils.ComponentHealth{
			Component: "GPU",
			State:     constants.AIMStatusDegraded,
			Reason:    "GPUCheckFailed",
			Message:   fmt.Sprintf("Failed to check GPU availability: %v", err),
			Errors:    []error{controllerutils.NewInfrastructureError("GPUCheckFailed", "Failed to check GPU availability", err)},
		}
	}
	if available {
		return controllerutils.ComponentHealth{
			Component: "GPU",
			State:     constants.AIMStatusReady,
			Reason:    "GPUAvailable",
			Message:   fmt.Sprintf("GPU model '%s' is available", normalizedModel),
		}
	}

	// GPU model is not available
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
		Message:   fmt.Sprintf("Required GPU model '%s' not available in cluster. Available: %s", gpuModel, availableStr),
	}
}

// CheckGPUAvailability checks whether the GPU model declared by the template exists in the cluster.
// Returns the normalized GPU model name and whether it's available.
func CheckGPUAvailability(
	ctx context.Context,
	k8sClient client.Client,
	spec aimv1alpha1.AIMServiceTemplateSpecCommon,
) (normalizedModel string, available bool, err error) {
	if !TemplateRequiresGPU(spec) {
		return "", true, nil
	}

	model := strings.TrimSpace(spec.Gpu.Model)
	normalizedModel = utils.NormalizeGPUModel(model)

	available, err = utils.IsGPUAvailable(ctx, k8sClient, model)
	if err != nil {
		return normalizedModel, false, err
	}
	return normalizedModel, available, nil
}

// IsGPUAvailableForSpec checks if the required GPU is available based on pre-fetched GPU resources.
// This is the fast-path check used during reconciliation when GPU resources have already been fetched.
// Returns true if no GPU is required, or if the required GPU is found in the provided resources.
func IsGPUAvailableForSpec(spec aimv1alpha1.AIMServiceTemplateSpecCommon, gpuResources map[string]utils.GPUResourceInfo, gpuFetchErr error) bool {
	if !TemplateRequiresGPU(spec) {
		return true
	}
	if gpuFetchErr != nil {
		return false
	}
	normalizedModel := utils.NormalizeGPUModel(spec.Gpu.Model)
	_, available := gpuResources[normalizedModel]
	return available
}

// GetGPUHealthFromResources returns GPU availability as component health based on pre-fetched GPU resources.
// This is the shared implementation used by both namespace-scoped and cluster-scoped template reconcilers.
// It avoids re-fetching GPU resources by using the already-fetched gpuResources map.
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

	gpuModel := spec.Gpu.Model
	normalizedModel := utils.NormalizeGPUModel(gpuModel)

	if _, available := gpuResources[normalizedModel]; available {
		return controllerutils.ComponentHealth{
			Component: "GPU",
			State:     constants.AIMStatusReady,
			Reason:    "GPUAvailable",
			Message:   "GPU model '" + normalizedModel + "' is available",
		}
	}

	// GPU not available - report error
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
		Message:   "Required GPU model '" + gpuModel + "' not available in cluster. Available: " + availableStr,
	}
}
