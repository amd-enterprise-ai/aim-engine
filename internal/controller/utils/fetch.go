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
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// FetchResult wraps a fetched value and its error, simplifying fetch result handling.
// Use the Fetch and FetchList helpers to create FetchResults.
type FetchResult[T any] struct {
	Value T
	Error error
}

// IsNotFound returns true if the error is a NotFound error.
func (fr FetchResult[T]) IsNotFound() bool {
	return apierrors.IsNotFound(fr.Error)
}

// OK returns true if there was no error.
func (fr FetchResult[T]) OK() bool {
	return fr.Error == nil
}

// HasError returns true if there was an error.
func (fr FetchResult[T]) HasError() bool {
	return fr.Error != nil
}

// Fetch retrieves a single object from the Kubernetes API and wraps the result.
// This helper reduces boilerplate when building fetch structs.
//
// Example:
//
//	type MyFetch struct {
//	    Model    FetchResult[*aimv1.AIMModel]
//	    Template FetchResult[*aimv1.AIMServiceTemplate]
//	}
//
//	func FetchRemoteState(ctx, client, obj) (MyFetch, error) {
//	    return MyFetch{
//	        Model:    Fetch(ctx, client, modelKey, &aimv1.AIMModel{}),
//	        Template: Fetch(ctx, client, templateKey, &aimv1.AIMServiceTemplate{}),
//	    }, nil
//	}
func Fetch[T client.Object](ctx context.Context, c client.Client, key client.ObjectKey, obj T) FetchResult[T] {
	return FetchResult[T]{
		Value: obj,
		Error: c.Get(ctx, key, obj),
	}
}

// FetchList retrieves a list of objects from the Kubernetes API and wraps the result.
// This helper reduces boilerplate when building fetch structs for list operations.
//
// Example:
//
//	type MyFetch struct {
//	    Pods FetchResult[*corev1.PodList]
//	}
//
//	func FetchRemoteState(ctx, client, obj) (MyFetch, error) {
//	    return MyFetch{
//	        Pods: FetchList(ctx, client, &corev1.PodList{}, client.InNamespace(ns)),
//	    }, nil
//	}
//
//	// Access in ComposeState:
//	for _, pod := range fetch.Pods.Value.Items { ... }
func FetchList[T client.ObjectList](ctx context.Context, c client.Client, list T, opts ...client.ListOption) FetchResult[T] {
	return FetchResult[T]{
		Value: list,
		Error: c.List(ctx, list, opts...),
	}
}

// ToComponentHealth converts a FetchResult into ComponentHealth with automatic error handling.
// Fetch errors are passed through as raw errors (categorized later by the state engine).
// If the fetch succeeded, the inspector function determines the semantic state.
func (fr FetchResult[T]) ToComponentHealth(component string, inspector func(T) ComponentHealth) ComponentHealth {
	// Handle fetch errors - pass raw error for later categorization
	if fr.HasError() {
		return ComponentHealth{
			Component: component,
			Errors:    []error{fr.Error},
		}
	}

	// No fetch errors - inspect the value for semantic state
	health := inspector(fr.Value)

	// Override the component name
	health.Component = component

	return health
}

// ToComponentHealthWithContext converts a FetchResult into ComponentHealth with automatic error handling.
// This variant provides context and Kubernetes clientset to the inspector function.
// Use this for health inspectors that need to fetch additional information (like pod logs).
func (fr FetchResult[T]) ToComponentHealthWithContext(
	ctx context.Context,
	clientset kubernetes.Interface,
	component string,
	inspector func(context.Context, kubernetes.Interface, T) ComponentHealth,
) ComponentHealth {
	// Handle fetch errors - pass raw error for later categorization
	if fr.HasError() {
		return ComponentHealth{
			Component: component,
			Errors:    []error{fr.Error},
		}
	}

	// No fetch errors - inspect the value for semantic state
	health := inspector(ctx, clientset, fr.Value)

	// Override the component name
	health.Component = component

	return health
}

// ToUpstreamComponentHealth converts a FetchResult for an upstream dependency into ComponentHealth.
// Upstream dependencies are resources that this controller depends on (templates, configs, secrets, etc.).
// NotFound errors for upstream dependencies are categorized as MissingUpstreamDependency (non-retriable, but can be
// resolved when they are created via external actions).
func (fr FetchResult[T]) ToUpstreamComponentHealth(component string, inspector func(T) ComponentHealth) ComponentHealth {
	// Handle fetch errors
	if fr.HasError() {
		var wrappedErr error
		if fr.IsNotFound() {
			// NotFound for upstream dependency = non-retriable error (user must create it)
			wrappedErr = NewMissingUpstreamDependencyError(
				"ReferenceNotFound",
				"Upstream dependency not found",
				fr.Error,
			)
		} else {
			// Other errors get categorized later
			wrappedErr = fr.Error
		}

		return ComponentHealth{
			Component:      component,
			Errors:         []error{wrappedErr},
			DependencyType: DependencyTypeUpstream,
		}
	}

	// No fetch errors - inspect the value for semantic state
	health := inspector(fr.Value)

	// Override the component name and dependency type
	health.Component = component
	health.DependencyType = DependencyTypeUpstream

	return health
}

// ToDownstreamComponentHealth converts a FetchResult for a downstream dependency into ComponentHealth.
// Downstream dependencies are resources that this controller creates (pods, jobs, child resources, etc.).
// NotFound errors for downstream dependencies are categorized as MissingDownstreamDependency (retriable/expected).
func (fr FetchResult[T]) ToDownstreamComponentHealth(component string, inspector func(T) ComponentHealth) ComponentHealth {
	// Handle fetch errors
	if fr.HasError() {
		var wrappedErr error
		if fr.IsNotFound() {
			// NotFound for downstream dependency = retriable (controller may be creating it)
			wrappedErr = NewMissingDownstreamDependencyError(
				"ResourceNotReady",
				"Downstream resource not found or being created",
				fr.Error,
			)
		} else {
			// Other errors get categorized later
			wrappedErr = fr.Error
		}

		return ComponentHealth{
			Component:      component,
			Errors:         []error{wrappedErr},
			DependencyType: DependencyTypeDownstream,
		}
	}

	// No fetch errors - inspect the value for semantic state
	health := inspector(fr.Value)

	// Override the component name and dependency type
	health.Component = component
	health.DependencyType = DependencyTypeDownstream

	return health
}
