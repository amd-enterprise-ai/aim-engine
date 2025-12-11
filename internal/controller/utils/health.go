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

package controllerutils

import (
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

// ComponentHealth describes the health of a sub-component (model, template, workload, etc.).
type ComponentHealth struct {
	Component string              // logical name: "model", "template", "workload", etc.
	State     constants.AIMStatus // Ready / Progressing / Degraded / Failed / Unknown
	Reason    string              // machine-readable reason, e.g. "ModelNotFound"
	Message   string              // human-readable message
	Level     ConditionLevel
}

// ConditionLevel mirrors your existing observability levels (Info/Warning/Error).
type ConditionLevel string

const (
	ConditionLevelInfo    ConditionLevel = "Info"
	ConditionLevelWarning ConditionLevel = "Warning"
	ConditionLevelError   ConditionLevel = "Error"
)

// ComponentHealthProvider is implemented by observation types that surface per-component health.
type ComponentHealthProvider interface {
	GetComponentHealth() []ComponentHealth
}

// ChildRef identifies a child workload object (e.g. Pod, Deployment, InferenceService).
type ChildRef struct {
	Kind      string
	Namespace string
	Name      string
}

// WorkloadIssueKind enumerates structured child-level failures.
type WorkloadIssueKind string

const (
	WorkloadImageError      WorkloadIssueKind = "ImageError"
	WorkloadAuthError       WorkloadIssueKind = "WorkloadAuthError"
	WorkloadConfigError     WorkloadIssueKind = "ConfigError"
	WorkloadSchedulingError WorkloadIssueKind = "SchedulingError"
	WorkloadRuntimeError    WorkloadIssueKind = "RuntimeError"
)

// WorkloadIssue describes a failure in a child/workload.
type WorkloadIssue struct {
	Kind    WorkloadIssueKind
	Reason  string
	Message string
	Child   ChildRef
}

// WorkloadIssueProvider is implemented by observations that expose workload issues.
type WorkloadIssueProvider interface {
	GetWorkloadIssues() []WorkloadIssue
}
