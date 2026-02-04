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
	"fmt"
	"strings"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// IsGPUAvailableForSpec checks if the required GPU is available based on pre-fetched GPU resources.
// This is the fast-path check used during reconciliation when GPU resources have already been fetched.
// Returns true if no GPU is required, or if the required GPU is found in the provided resources.
// When gpu.requests > 0 but gpu.model is empty, any available GPU satisfies the requirement.
func IsGPUAvailableForSpec(spec aimv1alpha1.AIMServiceTemplateSpecCommon, gpuResources map[string]utils.GPUResourceInfo, gpuFetchErr error) bool {
	if !TemplateRequiresGPU(spec) {
		return true
	}
	if gpuFetchErr != nil {
		return false
	}
	normalizedModel := utils.NormalizeGPUModel(spec.Hardware.GPU.Model)
	// If no specific GPU model is required (just gpu.requests > 0), accept any available GPU
	if normalizedModel == "" {
		return len(gpuResources) > 0
	}
	_, available := gpuResources[normalizedModel]
	return available
}

// GetGPUHealthFromResources returns GPU availability as component health based on pre-fetched GPU resources.
// This is the shared implementation used by both namespace-scoped and cluster-scoped template reconcilers.
// It avoids re-fetching GPU resources by using the already-fetched gpuResources map.
// It checks both GPU model availability and minVRAM requirements.
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

	gpuModel := spec.Hardware.GPU.Model
	normalizedModel := utils.NormalizeGPUModel(gpuModel)

	// Check minVRAM requirement first (if specified)
	if spec.Hardware.GPU.MinVRAM != nil && !spec.Hardware.GPU.MinVRAM.IsZero() {
		vramHealth := checkVRAMAvailability(spec, gpuResources)
		if vramHealth.State == constants.AIMStatusNotAvailable {
			return vramHealth
		}
	}

	// If no specific GPU model is required (just gpu.requests > 0), accept any available GPU
	if normalizedModel == "" {
		if len(gpuResources) > 0 {
			// Any GPU is acceptable - pick one for the message
			availableGPUs := make([]string, 0, len(gpuResources))
			for model := range gpuResources {
				availableGPUs = append(availableGPUs, model)
			}
			return controllerutils.ComponentHealth{
				Component: "GPU",
				State:     constants.AIMStatusReady,
				Reason:    "GPUAvailable",
				Message:   "GPU available (any model accepted): " + strings.Join(availableGPUs, ", "),
			}
		}
		// No GPUs available at all
		return controllerutils.ComponentHealth{
			Component: "GPU",
			State:     constants.AIMStatusNotAvailable,
			Reason:    "GPUNotAvailable",
			Message:   "No GPUs available in cluster",
		}
	}

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

// checkVRAMAvailability checks if any GPUs meet the minVRAM requirement.
// Returns NotAvailable health if no GPUs have sufficient VRAM.
func checkVRAMAvailability(
	spec aimv1alpha1.AIMServiceTemplateSpecCommon,
	gpuResources map[string]utils.GPUResourceInfo,
) controllerutils.ComponentHealth {
	minVRAM := spec.Hardware.GPU.MinVRAM
	if minVRAM == nil || minVRAM.IsZero() {
		return controllerutils.ComponentHealth{
			Component: "GPU",
			State:     constants.AIMStatusReady,
		}
	}

	minVRAMBytes := minVRAM.Value()

	// Check if any GPU in the cluster meets the VRAM requirement
	var gpusWithSufficientVRAM []string
	var highestVRAM int64
	var highestVRAMModel string

	for model, info := range gpuResources {
		vramBytes := utils.ParseVRAMToBytes(info.VRAM)
		if vramBytes > highestVRAM {
			highestVRAM = vramBytes
			highestVRAMModel = model
		}
		if vramBytes >= minVRAMBytes {
			gpusWithSufficientVRAM = append(gpusWithSufficientVRAM, model+" ("+info.VRAM+")")
		}
	}

	if len(gpusWithSufficientVRAM) > 0 {
		return controllerutils.ComponentHealth{
			Component: "GPU",
			State:     constants.AIMStatusReady,
			Reason:    "VRAMAvailable",
			Message:   "GPUs meeting VRAM requirement: " + strings.Join(gpusWithSufficientVRAM, ", "),
		}
	}

	// No GPUs meet the VRAM requirement
	highestAvailableStr := "none detected"
	if highestVRAMModel != "" {
		highestAvailableStr = highestVRAMModel + " (" + formatVRAMBytes(highestVRAM) + ")"
	}

	return controllerutils.ComponentHealth{
		Component: "GPU",
		State:     constants.AIMStatusNotAvailable,
		Reason:    "VRAMNotAvailable",
		Message:   "Required minimum VRAM (" + formatVRAMBytes(minVRAMBytes) + ") exceeds available GPUs. Highest available: " + highestAvailableStr,
	}
}

// formatVRAMBytes formats bytes as a human-readable VRAM string (e.g., "192Gi").
func formatVRAMBytes(bytes int64) string {
	if bytes == 0 {
		return "0"
	}
	gi := bytes / (1024 * 1024 * 1024)
	return fmt.Sprintf("%dGi", gi)
}
