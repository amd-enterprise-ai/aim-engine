package aimservicetemplate

import (
	"fmt"
	"strings"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// GenerateTemplateName creates an RFC1123-compliant name for a service template.
// Format: {truncated-image}-{count}x-{gpu}-{metric-shorthand}-{precision}-{hash4}
//
// The name is constructed to ensure uniqueness while preserving readability:
// - Image name is truncated to fit within the 63-character Kubernetes limit
// - Discovery parameters (GPU count, model, metric, precision) are always preserved
// - A 4-character hash suffix ensures uniqueness even when truncation occurs
//
// Example: llama-3-1-70b-instruct-1x-mi300x-lat-fp8-a7f3
func GenerateTemplateName(imageName string, deployment aimv1alpha1.RecommendedDeployment) string {
	// Build the distinguishing suffix components
	var suffixParts []string

	// Format: {count}x-{gpu}
	if deployment.GPUCount > 0 && deployment.GPUModel != "" {
		suffixParts = append(suffixParts, fmt.Sprintf("%dx-%s", deployment.GPUCount, deployment.GPUModel))
	} else if deployment.GPUModel != "" {
		suffixParts = append(suffixParts, deployment.GPUModel)
	} else if deployment.GPUCount > 0 {
		suffixParts = append(suffixParts, fmt.Sprintf("x%d", deployment.GPUCount))
	}

	// Add metric with shorthand
	if deployment.Metric != "" {
		suffixParts = append(suffixParts, getMetricShorthand(deployment.Metric))
	}

	// Add precision
	if deployment.Precision != "" {
		suffixParts = append(suffixParts, deployment.Precision)
	}

	// Create deterministic hash from all components for uniqueness
	hashInput := imageName
	if deployment.GPUModel != "" {
		hashInput += "-" + deployment.GPUModel
	}
	if deployment.GPUCount > 0 {
		hashInput += fmt.Sprintf("-x%d", deployment.GPUCount)
	}
	if deployment.Metric != "" {
		hashInput += "-" + deployment.Metric
	}
	if deployment.Precision != "" {
		hashInput += "-" + deployment.Precision
	}

	name, _ := utils.GenerateDerivedNameWithHashLength([]string{imageName, strings.Join(suffixParts, "-")}, 4, hashInput)
	return name
}

// metricShorthand maps metric values to their abbreviated forms for template naming
var metricShorthand = map[string]string{
	"latency":    "lat",
	"throughput": "thr",
}

// getMetricShorthand returns the abbreviated form of a metric, or the original if no mapping exists
func getMetricShorthand(metric string) string {
	if shorthand, ok := metricShorthand[metric]; ok {
		return shorthand
	}
	return metric
}

// TemplateRequiresGPU returns true if the template spec declares a GPU selector with a model.
func TemplateRequiresGPU(spec aimv1alpha1.AIMServiceTemplateSpecCommon) bool {
	if spec.GpuSelector == nil {
		return false
	}
	return strings.TrimSpace(spec.GpuSelector.Model) != ""
}