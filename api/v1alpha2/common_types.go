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

package v1alpha2

// Types in this file (AIMMetric, AIMPrecision, AIMProfileType, AIMModelSource) mirror
// v1alpha1 equivalents with v1alpha2-specific changes (e.g., AIMProfileType adds "general").
// Each API version is self-contained per Kubernetes convention.

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// AIMMetric enumerates supported optimization targets.
// +kubebuilder:validation:Enum=latency;throughput
type AIMMetric string

const (
	AIMMetricLatency    AIMMetric = "latency"
	AIMMetricThroughput AIMMetric = "throughput"
)

// AIMPrecision enumerates supported numeric precisions.
// +kubebuilder:validation:Enum=fp4;fp8;fp16;fp32;bf16;int4;int8
type AIMPrecision string

const (
	AIMPrecisionFP4  AIMPrecision = "fp4"
	AIMPrecisionFP8  AIMPrecision = "fp8"
	AIMPrecisionFP16 AIMPrecision = "fp16"
	AIMPrecisionFP32 AIMPrecision = "fp32"
	AIMPrecisionBF16 AIMPrecision = "bf16"
	AIMPrecisionInt4 AIMPrecision = "int4"
	AIMPrecisionInt8 AIMPrecision = "int8"
)

// AIMProfileType indicates the optimization level of a profile.
// Hierarchy: optimized > general > preview > unoptimized.
// +kubebuilder:validation:Enum=optimized;general;preview;unoptimized
type AIMProfileType string

const (
	AIMProfileTypeOptimized   AIMProfileType = "optimized"
	AIMProfileTypeGeneral     AIMProfileType = "general"
	AIMProfileTypePreview     AIMProfileType = "preview"
	AIMProfileTypeUnoptimized AIMProfileType = "unoptimized"
)

// AcceleratorType distinguishes CPU from GPU accelerators.
// Used by AIM Engine to determine the resource derivation strategy
// (e.g., gpu → amd.com/gpu, cpu → cpu).
// +kubebuilder:validation:Enum=gpu;cpu
type AcceleratorType string

const (
	AcceleratorTypeCPU AcceleratorType = "cpu"
	AcceleratorTypeGPU AcceleratorType = "gpu"
)

// AIMModelSource describes a downloadable model artifact with optional credentials.
type AIMModelSource struct {
	// ModelID is the canonical identifier in {org}/{name} format.
	// Determines the cache mount path: /workspace/cache/{modelId}
	// +required
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9_-]+/[a-zA-Z0-9._-]+$`
	ModelID string `json:"modelId"`

	// SourceURI is the location from which the model should be downloaded.
	// Supported schemes: hf:// (Hugging Face Hub), s3:// (S3-compatible storage).
	// +kubebuilder:validation:Pattern=`^(hf|s3)://[^ \t\r\n]+$`
	SourceURI string `json:"sourceUri"`

	// Size is the expected storage space required for this model artifact.
	// Optional — if not specified, the download job discovers the size automatically.
	// +optional
	Size *resource.Quantity `json:"size,omitempty"`

	// Precision describes the runtime precision this source is compatible with.
	// Used to match model sources to profiles during custom weight onboarding.
	// +optional
	// +kubebuilder:validation:Enum=fp4;fp8;fp16;fp32;bf16;int4;int8
	Precision AIMPrecision `json:"precision,omitempty"`

	// Env specifies per-source credential overrides.
	// Takes precedence over base-level env for the same variable name.
	// +optional
	// +listType=map
	// +listMapKey=name
	Env []corev1.EnvVar `json:"env,omitempty"`
}
