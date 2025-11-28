package aimservicetemplate

import (
	"context"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	pkgutils "github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ============================================================================
// FETCH
// ============================================================================

type ServiceTemplateClusterFetchResult struct {
	AvailableGpuModels []string
}

func fetchServiceTemplateClusterResult(ctx context.Context, c client.Client) (ServiceTemplateClusterFetchResult, error) {
	result := ServiceTemplateClusterFetchResult{}

	availableGpus, err := pkgutils.ListAvailableGPUs(ctx, c)
	if err != nil {
		return result, err
	}
	result.AvailableGpuModels = availableGpus

	return result, nil
}

// ============================================================================
// OBSERVE
// ============================================================================

type ServiceTemplateClusterObservation struct {
	GpuModelRequested string
	GpuModelAvailable bool
}

func observeServiceTemplateCluster(result ServiceTemplateClusterFetchResult, templateSpec aimv1alpha1.AIMServiceTemplateSpecCommon) ServiceTemplateClusterObservation {
	observation := ServiceTemplateClusterObservation{}

	if templateSpec.GpuSelector == nil || templateSpec.GpuSelector.Model == "" {
		// TODO okay? For CPU?
		observation.GpuModelAvailable = true
		observation.GpuModelRequested = ""
	} else {
		normalizedGpuModel := pkgutils.NormalizeGPUModel(templateSpec.GpuSelector.Model)
		for _, clusterGpu := range result.AvailableGpuModels {
			if pkgutils.NormalizeGPUModel(clusterGpu) == normalizedGpuModel {
				observation.GpuModelRequested = normalizedGpuModel
				observation.GpuModelAvailable = true
			}
		}
	}

	return observation
}

// ============================================================================
// PROJECT
// ============================================================================

// TODO
func projectServiceTemplateCluster(status *aimv1alpha1.AIMServiceTemplateStatus, cm *controllerutils.ConditionManager, h *controllerutils.StatusHelper, observation ServiceTemplateClusterObservation) bool {
	if !observation.GpuModelAvailable {
		//status.Status = constants.AIMStatusNotAvailable
		return false
	}
	return true
}
