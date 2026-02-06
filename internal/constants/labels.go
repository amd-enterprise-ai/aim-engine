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

package constants

// Label key naming conventions:
//   - Dots (.) separate category from attribute: gpu.model, template.metric
//   - Hyphens (-) separate words within a name: artifact, auto-generated
//
// Example: aim.eai.amd.com/gpu.model=MI300X
//          aim.eai.amd.com/cache.type=artifact

const (
	// ==========================================================================
	// Ownership labels - identify which AIM resource owns/created this resource
	// ==========================================================================

	// LabelKeyModel identifies the owning AIM(Cluster)Model name.
	// Used on: AIM(Cluster)ServiceTemplate, AIMService, discovery Jobs
	LabelKeyModel = AimLabelDomain + "/model"

	// LabelKeyTemplate identifies the owning AIM(Cluster)ServiceTemplate name.
	// Used on: AIMService, inference Pods
	LabelKeyTemplate = AimLabelDomain + "/template"

	// LabelKeyService identifies the owning AIMService name.
	// Used on: inference Pods, PVCs
	LabelKeyService = AimLabelDomain + "/service"

	// ==========================================================================
	// Origin labels - describe how/why this resource was created
	// ==========================================================================

	// LabelKeyOrigin indicates how a resource was created.
	// Values: auto-generated, derived, manual
	LabelKeyOrigin = AimLabelDomain + "/origin"

	// LabelKeyManagedBy indicates what tool/controller manages this resource.
	LabelKeyManagedBy = AimLabelDomain + "/managed-by"

	// LabelKeyComponent identifies the role of this resource in the architecture.
	// Values: inference, discovery, cache
	LabelKeyComponent = AimLabelDomain + "/component"

	// LabelKeyCustomModel indicates this is a custom model with inline model sources.
	// Value: "true"
	LabelKeyCustomModel = AimLabelDomain + "/custom-model"

	// LabelKeyTemplateAlias is the user-provided short-hand alias for a custom template.
	// Used to find templates by their alias before model prefix and hash are added.
	LabelKeyTemplateAlias = AimLabelDomain + "/template.alias"

	// ==========================================================================
	// Template configuration labels - queryable metadata for templates
	// ==========================================================================

	// LabelKeyGPUModel is the GPU model for this template (e.g., MI300X, MI325X).
	LabelKeyGPUModel = AimLabelDomain + "/gpu.model"

	// LabelKeyGPUCount is the number of GPUs for this template.
	LabelKeyGPUCount = AimLabelDomain + "/gpu.count"

	// LabelKeyTemplateMetric is the optimization metric (latency, throughput).
	LabelKeyTemplateMetric = AimLabelDomain + "/template.metric"

	// LabelKeyTemplatePrecision is the precision (fp8, fp16, bf16).
	LabelKeyTemplatePrecision = AimLabelDomain + "/template.precision"

	// ==========================================================================
	// Cache labels - for model and template caches
	// ==========================================================================

	// LabelKeyCacheType identifies the type of cache.
	// Values: artifact, template-cache
	LabelKeyCacheType = AimLabelDomain + "/cache.type"

	// LabelKeyCacheName identifies the cache resource name.
	LabelKeyCacheName = AimLabelDomain + "/cache.name"

	// ==========================================================================
	// Model source labels - for tracking model origins
	// ==========================================================================

	// LabelKeyModelSource identifies the source of the model (e.g., huggingface, s3).
	LabelKeyModelSource = AimLabelDomain + "/model.source"

	// ==========================================================================
	// Origin label values
	// ==========================================================================

	// LabelValueOriginAutoGenerated indicates the resource was auto-generated by the controller.
	LabelValueOriginAutoGenerated = "auto-generated"

	// LabelValueOriginDerived indicates the resource was derived from another resource.
	LabelValueOriginDerived = "derived"

	// LabelValueOriginManual indicates the resource was manually created by a user.
	LabelValueOriginManual = "manual"

	// ==========================================================================
	// Managed-by label values
	// ==========================================================================

	// LabelValueManagedByController indicates the resource is managed by the AIM controller.
	LabelValueManagedByController = "aim-controller"

	// ==========================================================================
	// Component label values
	// ==========================================================================

	// LabelValueComponentInference indicates an inference-related resource.
	LabelValueComponentInference = "inference"

	// LabelValueComponentDiscovery indicates a discovery-related resource.
	LabelValueComponentDiscovery = "discovery"

	// LabelValueComponentCache indicates a cache-related resource.
	LabelValueComponentCache = "cache"

	// ==========================================================================
	// Cache type label values
	// ==========================================================================

	// LabelValueCacheTypeModel indicates a artifact.
	LabelValueCacheTypeModel = "artifact"

	// LabelValueCacheTypeTemplate indicates a template cache.
	LabelValueCacheTypeTemplate = "template-cache"

	LabelValueCacheTypeTemplateCache = "template-cache"
	LabelKeySourceModel              = AimLabelDomain + "/source-model"
)
