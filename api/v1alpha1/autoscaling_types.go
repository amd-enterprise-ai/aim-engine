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

// AIMServiceAutoScaling configures KEDA-based autoscaling with custom metrics.
// This enables automatic scaling based on metrics collected from OpenTelemetry.
type AIMServiceAutoScaling struct {
	// Metrics is a list of metrics to be used for autoscaling.
	// Each metric defines a source (PodMetric) and target values.
	// +optional
	Metrics []AIMServiceMetricsSpec `json:"metrics,omitempty"`
}

// AIMServiceMetricsSpec defines a single metric for autoscaling.
// Specifies the metric source type and configuration.
type AIMServiceMetricsSpec struct {
	// Type is the type of metric source.
	// Valid values: "PodMetric" (per-pod custom metrics).
	// +kubebuilder:validation:Enum=PodMetric
	Type string `json:"type"`

	// PodMetric refers to a metric describing each pod in the current scale target.
	// Used when Type is "PodMetric". Supports backends like OpenTelemetry for custom metrics.
	// +optional
	PodMetric *AIMServicePodMetricSource `json:"podmetric,omitempty"`
}

// AIMServicePodMetricSource defines pod-level metrics configuration.
// Specifies the metric identification and target values for pod-based autoscaling.
type AIMServicePodMetricSource struct {
	// Metric contains the metric identification and backend configuration.
	// Defines which metrics to collect and how to query them.
	Metric *AIMServicePodMetric `json:"metric"`

	// Target specifies the target value for the metric.
	// The autoscaler will scale to maintain this target value.
	Target *AIMServiceMetricTarget `json:"target"`
}

// AIMServicePodMetric identifies the pod metric and its backend.
// Supports multiple metrics backends including OpenTelemetry.
type AIMServicePodMetric struct {
	// Backend defines the metrics backend to use.
	// If not specified, defaults to "opentelemetry".
	// +kubebuilder:validation:Enum=opentelemetry
	// +kubebuilder:default=opentelemetry
	// +optional
	Backend string `json:"backend,omitempty"`

	// ServerAddress specifies the address of the metrics backend server.
	// If not specified, defaults to "keda-otel-scaler.keda.svc:4317" for OpenTelemetry backend.
	// +optional
	ServerAddress string `json:"serverAddress,omitempty"`

	// MetricNames specifies which metrics to collect from pods and send to ServerAddress.
	// Example: ["vllm:num_requests_running"]
	// +optional
	MetricNames []string `json:"metricNames,omitempty"`

	// Query specifies the query to run to retrieve metrics from the backend.
	// The query syntax depends on the backend being used.
	// Example: "vllm:num_requests_running" for OpenTelemetry.
	// +optional
	Query string `json:"query,omitempty"`

	// OperationOverTime specifies the operation to aggregate metrics over time.
	// Valid values: "last_one", "avg", "max", "min", "rate", "count"
	// Default: "last_one"
	// +optional
	OperationOverTime string `json:"operationOverTime,omitempty"`
}

// AIMServiceMetricTarget defines the target value for a metric.
// Specifies how the metric value should be interpreted and what target to maintain.
type AIMServiceMetricTarget struct {
	// Type specifies how to interpret the metric value.
	// "Value": absolute value target (use Value field)
	// "AverageValue": average value across all pods (use AverageValue field)
	// "Utilization": percentage utilization for resource metrics (use AverageUtilization field)
	// +kubebuilder:validation:Enum=Value;AverageValue;Utilization
	Type string `json:"type"`

	// Value is the target value of the metric (as a quantity).
	// Used when Type is "Value".
	// Example: "1" for 1 request, "100m" for 100 millicores
	// +optional
	Value string `json:"value,omitempty"`

	// AverageValue is the target value of the average of the metric across all relevant pods (as a quantity).
	// Used when Type is "AverageValue".
	// Example: "100m" for 100 millicores per pod
	// +optional
	AverageValue string `json:"averageValue,omitempty"`

	// AverageUtilization is the target value of the average of the resource metric across all relevant pods,
	// represented as a percentage of the requested value of the resource for the pods.
	// Used when Type is "Utilization". Only valid for Resource metric source type.
	// Example: 80 for 80% utilization
	// +optional
	AverageUtilization *int32 `json:"averageUtilization,omitempty"`
}
