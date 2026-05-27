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

import (
	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

// Aliases for shared types now hosted in v1alpha1. v1alpha1 owns the canonical
// definition (along with AIMServiceSpec, AIMModelSpec etc.) and v1alpha2 re-
// exports them as aliases so v1alpha2-package code stays terse. v1alpha2
// cannot import v1alpha1 cyclically because v1alpha1 does not import v1alpha2.

// AIMMetric enumerates supported optimization targets.
type AIMMetric = aimv1alpha1.AIMMetric

// AIMPrecision enumerates supported numeric precisions.
type AIMPrecision = aimv1alpha1.AIMPrecision

// AIMProfileType indicates the optimization level of a profile.
type AIMProfileType = aimv1alpha1.AIMProfileType

// AcceleratorType distinguishes CPU from GPU accelerators.
type AcceleratorType = aimv1alpha1.AcceleratorType

// AIMModelSource describes a downloadable model artifact with optional credentials.
type AIMModelSource = aimv1alpha1.AIMModelSource

// Re-exported enum constants for convenience.
const (
	AIMMetricLatency    = aimv1alpha1.AIMMetricLatency
	AIMMetricThroughput = aimv1alpha1.AIMMetricThroughput

	AIMPrecisionAuto = aimv1alpha1.AIMPrecisionAuto
	AIMPrecisionFP4  = aimv1alpha1.AIMPrecisionFP4
	AIMPrecisionFP8  = aimv1alpha1.AIMPrecisionFP8
	AIMPrecisionFP16 = aimv1alpha1.AIMPrecisionFP16
	AIMPrecisionFP32 = aimv1alpha1.AIMPrecisionFP32
	AIMPrecisionBF16 = aimv1alpha1.AIMPrecisionBF16
	AIMPrecisionInt4 = aimv1alpha1.AIMPrecisionInt4
	AIMPrecisionInt8 = aimv1alpha1.AIMPrecisionInt8

	AIMProfileTypeOptimized   = aimv1alpha1.AIMProfileTypeOptimized
	AIMProfileTypeGeneral     = aimv1alpha1.AIMProfileTypeGeneral
	AIMProfileTypePreview     = aimv1alpha1.AIMProfileTypePreview
	AIMProfileTypeUnoptimized = aimv1alpha1.AIMProfileTypeUnoptimized

	AcceleratorTypeCPU = aimv1alpha1.AcceleratorTypeCPU
	AcceleratorTypeGPU = aimv1alpha1.AcceleratorTypeGPU
)
