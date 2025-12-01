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

package aimservicetemplate

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	pkgutils "github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// ============================================================================
// FETCH
// ============================================================================

type serviceTemplateClusterFetchResult struct {
	availableGpuModels []string
}

func fetchServiceTemplateClusterResult(ctx context.Context, c client.Client) (serviceTemplateClusterFetchResult, error) {
	result := serviceTemplateClusterFetchResult{}

	availableGpus, err := pkgutils.ListAvailableGPUs(ctx, c)
	if err != nil {
		return result, err
	}
	result.availableGpuModels = availableGpus

	return result, nil
}

// ============================================================================
// OBSERVE
// ============================================================================

type serviceTemplateClusterObservation struct {
	gpuModelRequested string
	gpuModelAvailable bool
}

func observeServiceTemplateCluster(result serviceTemplateClusterFetchResult, templateSpec aimv1alpha1.AIMServiceTemplateSpecCommon) serviceTemplateClusterObservation {
	observation := serviceTemplateClusterObservation{}

	if templateSpec.GpuSelector == nil || templateSpec.GpuSelector.Model == "" {
		// TODO okay? For CPU?
		observation.gpuModelAvailable = true
		observation.gpuModelRequested = ""
	} else {
		normalizedGpuModel := pkgutils.NormalizeGPUModel(templateSpec.GpuSelector.Model)
		for _, clusterGpu := range result.availableGpuModels {
			if pkgutils.NormalizeGPUModel(clusterGpu) == normalizedGpuModel {
				observation.gpuModelRequested = normalizedGpuModel
				observation.gpuModelAvailable = true
			}
		}
	}

	return observation
}

// ============================================================================
// PROJECT
// ============================================================================

// projectServiceTemplateCluster projects the cluster GPU availability observation.
// Returns true if a fatal error occurred (should stop reconciliation), false otherwise.
func projectServiceTemplateCluster(_ *aimv1alpha1.AIMServiceTemplateStatus, _ *controllerutils.ConditionManager, h *controllerutils.StatusHelper, observation serviceTemplateClusterObservation) bool {
	if !observation.gpuModelAvailable {
		h.Degraded("GpuNotAvailable", fmt.Sprintf("GPU model '%s' not available in cluster", observation.gpuModelRequested))
		return true // Fatal - stop reconciliation
	}
	// TODOremovecondition otherwise
	return false // Continue
}
