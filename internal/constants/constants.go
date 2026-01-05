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

import (
	"os"
	"strings"
	"sync"
)

const (
	// operatorNamespaceEnvVar is the environment variable the operator uses to determine its namespace.
	operatorNamespaceEnvVar = "AIM_SYSTEM_NAMESPACE"

	// DefaultRuntimeConfigName is the name of the default AIM runtime config
	DefaultRuntimeConfigName = "default"

	// MaxConcurrentDiscoveryJobs is the global limit for concurrent discovery jobs across all namespaces
	MaxConcurrentDiscoveryJobs = 10

	// AimLabelDomain is the base domain used for AIM-specific labels.
	AimLabelDomain = "aim.eai.amd.com"
)

// Label keys for AIM resources
const (
	// LabelTemplate is the label key for the template name
	LabelTemplate = AimLabelDomain + "/template"
	// LabelService is the label key for the service name
	LabelService = AimLabelDomain + "/service"
	// LabelModelID is the label key for the model ID
	LabelModelID = AimLabelDomain + "/model"
	// LabelMetric is the label key for the optimization metric
	LabelMetric = AimLabelDomain + "/metric"
	// LabelPrecision is the label key for the numeric precision
	LabelPrecision = AimLabelDomain + "/precision"
	// LabelCacheType indicates the type of cache (temp or persistent)
	LabelCacheType = AimLabelDomain + "/cache-type"
)

// Label values
const (
	// LabelValueManagedBy is the standard managed-by label value
	LabelValueManagedBy = "aim-engine"
	// LabelValueCacheTypeTemp indicates a temporary cache
	LabelValueCacheTypeTemp = "temp"
	// LabelValueCacheTypePersistent indicates a persistent cache
	LabelValueCacheTypePersistent = "persistent"
)

// Shared condition reasons used across multiple resource types
const (
	// Image-related reasons (used by AIMModel, AIMService, AIMServiceTemplate)
	ReasonImagePullAuthFailure = "ImagePullAuthFailure"
	ReasonImageNotFound        = "ImageNotFound"
	ReasonImagePullBackOff     = "ImagePullBackOff"

	// Resource resolution/reference reasons (used by multiple types)
	ReasonNotFound = "NotFound"
	ReasonNotReady = "NotReady"
	ReasonCreating = "Creating"
	ReasonResolved = "Resolved"

	// Storage/PVC reasons (used by AIMModelCache, AIMService)
	ReasonPVCProvisioning = "PVCProvisioning"
	ReasonPVCBound        = "PVCBound"
	ReasonPVCNotBound     = "PVCNotBound"
	ReasonPVCPending      = "PVCPending"
	ReasonPVCLost         = "PVCLost"

	// Generic failure/retry reasons
	ReasonRetryBackoff = "RetryBackoff"
	ReasonFailed       = "Failed"
)

type AIMStatus string

const (
	AIMStatusPending      AIMStatus = "Pending"
	AIMStatusStarting     AIMStatus = "Starting"
	AIMStatusProgressing  AIMStatus = "Progressing"
	AIMStatusReady        AIMStatus = "Ready"
	AIMStatusRunning      AIMStatus = "Running"
	AIMStatusDegraded     AIMStatus = "Degraded"
	AIMStatusNotAvailable AIMStatus = "NotAvailable"
	AIMStatusFailed       AIMStatus = "Failed"
)

// StatusProvider is implemented by status types that expose their AIMStatus.
type StatusProvider interface {
	GetAIMStatus() AIMStatus
}

// AIMStatusPriority maps AIMStatus values to priority levels.
// Higher values indicate more desirable statuses for sorting and filtering.
var AIMStatusPriority = map[AIMStatus]int{
	AIMStatusRunning:      7,
	AIMStatusReady:        6,
	AIMStatusProgressing:  5,
	AIMStatusStarting:     4,
	AIMStatusPending:      3,
	AIMStatusDegraded:     2,
	AIMStatusNotAvailable: 1,
	AIMStatusFailed:       0,
}

func CompareAIMStatus(a AIMStatus, b AIMStatus) int {
	priorityA := AIMStatusPriority[a]
	priorityB := AIMStatusPriority[b]
	if priorityA > priorityB {
		return 1 // a is better
	}
	if priorityA < priorityB {
		return -1 // a is worse
	}
	return 0 // equal
}

var (
	operatorNamespaceOnce sync.Once
	operatorNamespace     string
)

// GetOperatorNamespace returns the namespace where the AIM operator runs.
// The result is cached after the first call.
func GetOperatorNamespace() string {
	operatorNamespaceOnce.Do(func() {
		// Check if the env var is set
		if ns := os.Getenv(operatorNamespaceEnvVar); ns != "" {
			operatorNamespace = ns
			return
		}

		// If running in a pod, this should exist
		if data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
			if ns := strings.TrimSpace(string(data)); len(ns) > 0 {
				operatorNamespace = ns
				return
			}
		}

		// Default to aim-system
		operatorNamespace = "aim-system"
	})
	return operatorNamespace
}

// AMD GPU node label keys
const (
	// NodeLabelAMDGPUDeviceID is the primary node label for AMD GPU device IDs (e.g., "74a1" for MI300X)
	NodeLabelAMDGPUDeviceID = "amd.com/gpu.device-id"

	// NodeLabelBetaAMDGPUDeviceID is the legacy/beta node label for AMD GPU device IDs
	NodeLabelBetaAMDGPUDeviceID = "beta.amd.com/gpu.device-id"
)

// Standard Kubernetes label keys
const (
	// LabelK8sComponent is the standard Kubernetes component label
	LabelK8sComponent = "app.kubernetes.io/component"
	// LabelK8sManagedBy is the standard Kubernetes managed-by label
	LabelK8sManagedBy = "app.kubernetes.io/managed-by"
)

// InferenceService constants
const (
	// ContainerKServe is the name of the main inference container
	ContainerKServe = "kserve-container"
	// VolumeSharedMemory is the name of the shared memory volume
	VolumeSharedMemory = "dshm"
	// VolumeModelStorage is the name of the model storage volume
	VolumeModelStorage = "model-storage"
	// MountPathSharedMemory is the mount path for shared memory
	MountPathSharedMemory = "/dev/shm"
	// DefaultSharedMemorySize is the default size for /dev/shm
	DefaultSharedMemorySize = "8Gi"
	// DefaultHTTPPort is the default HTTP port for inference services
	DefaultHTTPPort = 8000
	// DefaultGatewayPort is the default gateway port
	DefaultGatewayPort = 80
	// DefaultGPUResourceName is the default resource name for AMD GPUs
	DefaultGPUResourceName = "amd.com/gpu"
	// AIMCacheBasePath is the base directory for cached models
	AIMCacheBasePath = "/workspace/model-cache"
)

// Component values for resource labels
const (
	// ComponentInference is the component value for inference-related resources
	ComponentInference = "inference"
	// ComponentRouting is the component value for routing-related resources
	ComponentRouting = "routing"
	// ComponentModelStorage is the component value for storage-related resources
	ComponentModelStorage = "model-storage"
)

// Environment variable names
const (
	// EnvAIMCachePath is the environment variable for the cache path
	EnvAIMCachePath = "AIM_CACHE_PATH"
	// EnvAIMMetric is the environment variable for the optimization metric
	EnvAIMMetric = "AIM_METRIC"
	// EnvAIMPrecision is the environment variable for the numeric precision
	EnvAIMPrecision = "AIM_PRECISION"
	// EnvVLLMEnableMetrics enables vLLM metrics
	EnvVLLMEnableMetrics = "VLLM_ENABLE_METRICS"
)

// KServe annotation keys and values
const (
	// AnnotationKServeAutoscalerClass is the annotation key for autoscaler class
	AnnotationKServeAutoscalerClass = "serving.kserve.io/autoscalerClass"
	// AutoscalerClassNone disables autoscaling
	AutoscalerClassNone = "none"
)

// Template-related constants
const (
	// TemplateNameMaxLength is the maximum length for template names (Kubernetes name limit)
	TemplateNameMaxLength = 63
	// DerivedTemplateSuffix is the suffix used for derived templates
	DerivedTemplateSuffix = "-ovr-"
	// PredictorServiceSuffix is the suffix added to InferenceService names for predictor services
	PredictorServiceSuffix = "-predictor"
)
