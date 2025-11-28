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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// Shared runtime configuration types for both namespace and cluster-scoped configs

// AIMStorageConfig configures storage defaults for model caches and PVCs.
type AIMStorageConfig struct {
	// DefaultStorageClassName specifies the storage class to use for model caches and PVCs
	// when the consuming resource (AIMModelCache, AIMTemplateCache, AIMServiceTemplate) does not
	// specify a storage class. If this field is empty, the cluster's default storage class is used.
	// +optional
	DefaultStorageClassName *string `json:"defaultStorageClassName,omitempty"`

	// PVCHeadroomPercent specifies the percentage of extra space to add to PVCs
	// for model storage. This accounts for filesystem overhead and temporary files
	// during model loading. The value represents a percentage (e.g., 10 means 10% extra space).
	// If not specified, defaults to 10%.
	// +kubebuilder:default=10
	// +kubebuilder:validation:Minimum=0
	// +optional
	PVCHeadroomPercent *int32 `json:"pvcHeadroomPercent,omitempty"`
}

// AIMServiceRuntimeConfig contains runtime configuration fields that apply to services.
// This struct is shared between AIMService.spec (inlined) and AIMRuntimeConfigCommon,
// allowing services to override these specific runtime settings while inheriting defaults
// from namespace/cluster RuntimeConfigs.
type AIMServiceRuntimeConfig struct {
	// Storage configures storage defaults for this service's PVCs and caches.
	// When set, these values override namespace/cluster runtime config defaults.
	// +optional
	Storage *AIMStorageConfig `json:"storage,omitempty"`

	// Routing controls HTTP routing configuration for this service.
	// When set, these values override namespace/cluster runtime config defaults.
	// +optional
	Routing *AIMRuntimeRoutingConfig `json:"routing,omitempty"`
}

type AIMModelConfig struct {
	// AutoDiscovery controls whether models run discovery by default.
	// When true, models run discovery jobs to extract metadata and auto-create templates.
	// When false, discovery is skipped. Discovery failures are non-fatal and reported via conditions.
	// +kubebuilder:default=true
	// +optional
	AutoDiscovery *bool `json:"autoDiscovery,omitempty"`
}

// AIMRuntimeConfigCommon captures configuration fields shared across cluster and namespace scopes.
// These settings apply to both AIMRuntimeConfig (namespace-scoped) and AIMClusterRuntimeConfig (cluster-scoped).
// It embeds AIMServiceRuntimeConfig which contains fields that can also be overridden at the service level.
type AIMRuntimeConfigCommon struct {
	AIMServiceRuntimeConfig `json:",inline"`

	// Model controls model creation and discovery defaults.
	// This field only applies to RuntimeConfig/ClusterRuntimeConfig and is not available for services.
	// +optional
	Model *AIMModelConfig `json:"model,omitempty"`

	// DEPRECATED: Use Storage.DefaultStorageClassName instead. This field will be removed in a future version.
	// For backward compatibility, if this field is set and Storage.DefaultStorageClassName is not set,
	// the value will be automatically migrated.
	// +optional
	DefaultStorageClassName string `json:"defaultStorageClassName,omitempty"`

	// DEPRECATED: Use Storage.PVCHeadroomPercent instead. This field will be removed in a future version.
	// For backward compatibility, if this field is set and Storage.PVCHeadroomPercent is not set,
	// the value will be automatically migrated.
	// +optional
	PVCHeadroomPercent *int32 `json:"pvcHeadroomPercent,omitempty"`
}

// AIMClusterRuntimeConfigSpec defines cluster-wide defaults for AIM resources.
type AIMClusterRuntimeConfigSpec struct {
	AIMRuntimeConfigCommon `json:",inline"`
}

// AIMRuntimeConfigSpec defines namespace-scoped overrides for AIM resources.
type AIMRuntimeConfigSpec struct {
	AIMRuntimeConfigCommon `json:",inline"`
}

// AIMRuntimeRoutingConfig configures HTTP routing defaults for inference services.
// These settings control how Gateway API HTTPRoutes are created and configured.
type AIMRuntimeRoutingConfig struct {
	// Enabled controls whether HTTP routing is managed for inference services using this config.
	// When true, the operator creates HTTPRoute resources for services that reference this config.
	// When false or unset, routing must be explicitly enabled on each service.
	// This provides a namespace or cluster-wide default that individual services can override.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// GatewayRef specifies the Gateway API Gateway resource that should receive HTTPRoutes.
	// This identifies the parent gateway for routing traffic to inference services.
	// The gateway can be in any namespace (cross-namespace references are supported).
	// If routing is enabled but GatewayRef is not specified, service reconciliation will fail
	// with a validation error.
	// +optional
	GatewayRef *gatewayapiv1.ParentReference `json:"gatewayRef,omitempty"`

	// PathTemplate defines the HTTP path template for routes, evaluated using JSONPath expressions.
	// The template is rendered against the AIMService object to generate unique paths.
	//
	// Example templates:
	// - `/{.metadata.namespace}/{.metadata.name}` - namespace and service name
	// - `/{.metadata.namespace}/{.metadata.labels['team']}/inference` - with label
	// - `/models/{.spec.aimModelName}` - based on model name
	//
	// The template must:
	// - Use valid JSONPath expressions wrapped in {...}
	// - Reference fields that exist on the service
	// - Produce a path ≤ 200 characters after rendering
	// - Result in valid URL path segments (lowercase, RFC 1123 compliant)
	//
	// If evaluation fails, the service enters Degraded state with PathTemplateInvalid reason.
	// Individual services can override this template via spec.routing.pathTemplate.
	// +optional
	PathTemplate *string `json:"pathTemplate,omitempty"`

	// RequestTimeout defines the HTTP request timeout for routes.
	// This sets the maximum duration for a request to complete before timing out.
	// The timeout applies to the entire request/response cycle.
	// If not specified, no timeout is set on the route.
	// Individual services can override this value via spec.routing.requestTimeout.
	// +optional
	RequestTimeout *metav1.Duration `json:"requestTimeout,omitempty"`

	// Annotations defines default annotations to add to all HTTPRoute resources.
	// Services can add additional annotations or override these via spec.routingAnnotations.
	// When both are specified, service annotations take precedence for conflicting keys.
	// Common use cases include ingress controller settings, rate limiting, monitoring labels,
	// and security policies that should apply to all services using this config.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// AIMRuntimeConfigStatus records the resolved config reference surfaced to consumers.
type AIMRuntimeConfigStatus struct {
	// ObservedGeneration is the last reconciled generation.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions communicate reconciliation progress.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=aimcrcfg,categories=aim;all
// +kubebuilder:printcolumn:name="CacheBaseImages",type=boolean,JSONPath=`.spec.cacheBaseImages`
// +kubebuilder:printcolumn:name="DefaultStorageClass",type=string,JSONPath=`.spec.defaultStorageClassName`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
