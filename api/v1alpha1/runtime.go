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

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
)

// AIMRuntimeParameters contains the runtime configuration parameters shared
// across templates and services. Fields use pointers to allow optional usage
// in different contexts (required in templates, optional in service overrides).
type AIMRuntimeParameters struct {
	// Metric selects the optimization goal.
	//
	// - `latency`: prioritize low end‑to‑end latency
	// - `throughput`: prioritize sustained requests/second
	//
	// +optional
	// +kubebuilder:validation:Enum=latency;throughput
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="metric is immutable"
	Metric *AIMMetric `json:"metric,omitempty"`

	// Precision selects the numeric precision used by the runtime.
	// +optional
	// +kubebuilder:validation:Enum=auto;fp4;fp8;fp16;fp32;bf16;int4;int8
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="precision is immutable"
	Precision *AIMPrecision `json:"precision,omitempty"`

	// Hardware specifies GPU and CPU requirements for each replica.
	// For GPU models, defines the GPU count and model types required for deployment.
	// For CPU-only models, defines CPU resource requirements.
	// This field is immutable after creation.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="hardware is immutable"
	Hardware *AIMHardwareRequirements `json:"hardware,omitempty"`
}

// AIMMetric enumerates the targeted service characteristic
// +kubebuilder:validation:Enum=latency;throughput
type AIMMetric string

const (
	AIMMetricLatency    AIMMetric = "latency"
	AIMMetricThroughput AIMMetric = "throughput"
)

// AIMPrecision enumerates supported numeric precisions
// +kubebuilder:validation:Enum=auto;fp4;fp8;fp16;fp32;bf16;int4;int8
type AIMPrecision string

const (
	AIMPrecisionAuto AIMPrecision = "auto"
	AIMPrecisionFP4  AIMPrecision = "fp4"
	AIMPrecisionFP8  AIMPrecision = "fp8"
	AIMPrecisionFP16 AIMPrecision = "fp16"
	AIMPrecisionFP32 AIMPrecision = "fp32"
	AIMPrecisionBF16 AIMPrecision = "bf16"
	AIMPrecisionInt4 AIMPrecision = "int4"
	AIMPrecisionInt8 AIMPrecision = "int8"
)

// AIMHardwareRequirements specifies compute resource requirements for custom models.
// Used in AIMModelSpec and AIMCustomTemplate to define GPU and CPU needs.
// +kubebuilder:validation:XValidation:rule="has(self.gpu) || has(self.cpu)",message="at least one of gpu or cpu must be specified"
type AIMHardwareRequirements struct {
	// GPU specifies GPU requirements. If not set, no GPUs are requested (CPU-only model).
	// +optional
	GPU *AIMGpuRequirements `json:"gpu,omitempty"`

	// CPU specifies CPU requirements.
	// +optional
	CPU *AIMCpuRequirements `json:"cpu,omitempty"`
}

// AIMGpuRequirements specifies GPU resource requirements.
type AIMGpuRequirements struct {
	// Requests is the number of GPUs to set as requests/limits.
	// Set to 0 to target GPU nodes without consuming GPU resources (useful for testing).
	// +optional
	// +kubebuilder:validation:Minimum=0
	Requests int32 `json:"requests,omitempty"`

	// Model limits deployment to a specific GPU model.
	// Example: "MI300X"
	// +optional
	// +kubebuilder:validation:MaxLength=64
	Model string `json:"model,omitempty"`

	// MinVRAM limits deployment to GPUs having at least this much VRAM.
	// Used for capacity planning when model size is known.
	// +optional
	MinVRAM *resource.Quantity `json:"minVram,omitempty"`

	// ResourceName is the Kubernetes resource name for GPU resources.
	// Defaults to "amd.com/gpu" if not specified.
	// +optional
	// +kubebuilder:default="amd.com/gpu"
	ResourceName string `json:"resourceName,omitempty"`
}

// AIMCpuRequirements specifies CPU resource requirements.
type AIMCpuRequirements struct {
	// Requests is the number of CPU cores to request. Required and must be > 0.
	// +required
	Requests resource.Quantity `json:"requests"`

	// Limits is the maximum number of CPU cores to allow.
	// +optional
	Limits *resource.Quantity `json:"limits,omitempty"`
}
