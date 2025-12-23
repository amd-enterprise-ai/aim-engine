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

// DependencyType indicates whether a component is an upstream or downstream dependency.
type DependencyType string

const (
	// DependencyTypeUpstream indicates this component is an upstream dependency that this controller depends on.
	// Examples: templates, runtime configs, secrets, configmaps.
	// When upstream dependencies are not ready, the resource should be Pending.
	DependencyTypeUpstream DependencyType = "Upstream"

	// DependencyTypeDownstream indicates this component is a downstream resource that this controller creates.
	// Examples: model caches, pods, jobs, child resources.
	// When downstream dependencies are not ready (being created), the resource should be Progressing.
	DependencyTypeDownstream DependencyType = "Downstream"

	// DependencyTypeUnspecified means the dependency type is not specified (for backward compatibility).
	DependencyTypeUnspecified DependencyType = ""
)

// ComponentHealth describes the health of a component (dependency, child resource, or virtual component).
// It unifies tracking for logical components (model, template, cache), physical resources (pods, deployments),
// and virtual components (external API queries, image registry access, etc.).
type ComponentHealth struct {
	// Component is the logical name of this component:
	// - Physical dependencies: "Model", "Cache", "Template"
	// - Child resources: "Workload"
	// - Virtual components: "MetadataQuery", "ImageRegistry", "ExternalAPI"
	Component string

	// State is the current state of this component (optional).
	// If empty (""), the state will be derived from Errors using DeriveStateFromErrors.
	// Set explicitly when semantic meaning matters (e.g., Progressing vs Failed for missing deps).
	State constants.AIMStatus

	// Reason is a machine-readable reason code (optional).
	// If empty and Errors is non-empty, will be derived from the first categorized error.
	// Examples: "NotFound", "ImagePullBackOff", "InvalidCredentials"
	Reason string

	// Message is a human-readable description (optional).
	// If empty and Errors is non-empty, will be derived from the first categorized error.
	Message string

	// Errors are the raw errors that caused this state.
	// These will be categorized by the state engine to drive parent-level conditions
	// (ConfigValid, AuthValid, DependenciesReachable).
	Errors []error

	// DependencyType indicates whether this is an upstream or downstream dependency.
	// This is used to determine whether a not-ready component should result in Pending (upstream)
	// or Progressing (downstream) status.
	DependencyType DependencyType

	// ChildRef optionally identifies a specific child resource for fine-grained tracking.
	// When set, this ComponentHealth represents a specific pod/deployment/etc.
	// When nil, this represents an aggregated component view.
	ChildRef *ChildRef
}

// ChildRef identifies a child resource (e.g., Pod, Deployment, Service).
type ChildRef struct {
	Kind      string
	Namespace string
	Name      string
}

// ComponentHealthProvider is implemented by observation types that surface per-component health.
type ComponentHealthProvider interface {
	GetComponentHealth() []ComponentHealth
}

// GetState returns the component's state, deriving it from errors if not explicitly set.
func (ch ComponentHealth) GetState() constants.AIMStatus {
	if ch.State != "" {
		return ch.State
	}
	return DeriveStateFromErrors(ch.Errors)
}

// GetReason returns the component's reason, deriving it from the first error if not explicitly set.
// Note: Error-derived reason/message will be implemented by the state engine after categorization.
func (ch ComponentHealth) GetReason() string {
	if ch.Reason != "" {
		return ch.Reason
	}
	if len(ch.Errors) > 0 {
		// Categorize the first error to extract reason
		categorized := CategorizeError(ch.Errors[0])
		return categorized.Reason()
	}
	return string(constants.AIMStatusReady)
}

// GetMessage returns the component's message, deriving it from the first error if not explicitly set.
// Note: Error-derived reason/message will be implemented by the state engine after categorization.
func (ch ComponentHealth) GetMessage() string {
	if ch.Message != "" {
		return ch.Message
	}
	if len(ch.Errors) > 0 {
		// Categorize the first error to extract message
		categorized := CategorizeError(ch.Errors[0])
		return categorized.UserMessage()
	}
	return ""
}

// DeriveStateFromErrors infers an AIMStatus from a list of raw errors.
// This is used when ComponentHealth.State is nil.
// Errors are categorized on-the-fly to determine the appropriate state.
//
// Derivation rules:
//   - No errors → Ready
//   - User-fixable errors (InvalidSpec, MissingReference, Auth, Infrastructure) → Degraded
//   - MissingDependency errors → Progressing (waiting for internal deps)
//   - Multiple categories → "worst" status wins (Degraded > Progressing > Ready)
//
// Note: Failed is reserved for truly terminal states (e.g., all children permanently failed).
// User-fixable errors use Degraded because the resource can recover once the user fixes the issue.
func DeriveStateFromErrors(errs []error) constants.AIMStatus {
	if len(errs) == 0 {
		return constants.AIMStatusReady
	}

	// Categorize all errors first
	var categorized []StateEngineError
	for _, e := range errs {
		categorized = append(categorized, CategorizeError(e))
	}

	// Check for user-fixable errors → Degraded
	// These are issues the user can resolve (fix config, add missing resource, fix auth)
	for _, e := range categorized {
		switch e.Category() {
		case ErrorCategoryInvalidSpec, ErrorCategoryMissingUpstreamDependency, ErrorCategoryAuth, ErrorCategoryInfrastructure:
			return constants.AIMStatusDegraded
		}
	}

	// MissingDependency defaults to Progressing (waiting for internal deps we're creating)
	return constants.AIMStatusProgressing
}
