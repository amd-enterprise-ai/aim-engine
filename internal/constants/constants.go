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

	"github.com/amd-enterprise-ai/aim-engine/pkg/aimstatus"
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
	// LabelProfile is the label key for the profile name (v1alpha2 path)
	LabelProfile = AimLabelDomain + "/profile"
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
	// LabelTemplateCacheName is the label key for the template cache name (used on artifacts)
	LabelTemplateCacheName = AimLabelDomain + "/template-cache.name"
	// LabelProfileCacheName is the label key for the profile cache name (used on artifacts)
	LabelProfileCacheName = AimLabelDomain + "/profile-cache.name"
)

// Label values
const (
	// LabelValueManagedBy is the standard managed-by label value
	LabelValueManagedBy = "aim-engine"
	// LabelValueCacheTypeTemp indicates a temporary cache
	LabelValueCacheTypeTemp = "temp"
	// LabelValueCacheTypePersistent indicates a persistent cache
	LabelValueCacheTypePersistent = "persistent"
	// LabelValueCacheTypeDedicated indicates a dedicated cache owned by an AIMService.
	// These caches are created for non-cached modes (Never/Auto) to enable unified downloads.
	LabelValueCacheTypeDedicated = "dedicated"
)

// Discovery circuit breaker configuration
const (
	// DiscoveryBaseBackoffSeconds is the base backoff duration in seconds.
	// Actual backoff = base * 2^(attempts-1), capped at DiscoveryMaxBackoffSeconds.
	DiscoveryBaseBackoffSeconds = 60 // 1 minute

	// DiscoveryMaxBackoffSeconds is the maximum backoff duration in seconds.
	DiscoveryMaxBackoffSeconds = 3600 // 1 hour
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

	// Storage/PVC reasons (used by AIMArtifact, AIMService)
	ReasonPVCProvisioning = "PVCProvisioning"
	ReasonPVCBound        = "PVCBound"
	ReasonPVCNotBound     = "PVCNotBound"
	ReasonPVCPending      = "PVCPending"
	ReasonPVCLost         = "PVCLost"

	// Generic failure/retry reasons
	ReasonRetryBackoff = "RetryBackoff"
	ReasonFailed       = "Failed"
)

type AIMStatus = aimstatus.AIMStatus

const (
	AIMStatusPending      AIMStatus = aimstatus.AIMStatusPending
	AIMStatusStarting     AIMStatus = aimstatus.AIMStatusStarting
	AIMStatusProgressing  AIMStatus = aimstatus.AIMStatusProgressing
	AIMStatusReady        AIMStatus = aimstatus.AIMStatusReady
	AIMStatusRunning      AIMStatus = aimstatus.AIMStatusRunning
	AIMStatusDegraded     AIMStatus = aimstatus.AIMStatusDegraded
	AIMStatusNotAvailable AIMStatus = aimstatus.AIMStatusNotAvailable
	AIMStatusFailed       AIMStatus = aimstatus.AIMStatusFailed
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
	AIMCacheBasePath = "/workspace/cache"
)

// Component values for resource labels
const (
	// ComponentInference is the component value for inference-related resources
	ComponentInference = "inference"
	// ComponentRouting is the component value for routing-related resources
	ComponentRouting = "routing"
	// ComponentModelStorage is the component value for storage-related resources
	ComponentModelStorage = "model-storage"
	// ComponentAutoscaling is the component value for autoscaling resources.
	ComponentAutoscaling = "autoscaling"
)

// Environment variable names
const (
	// EnvAIMCachePath is the environment variable for the cache path
	EnvAIMCachePath = "AIM_CACHE_PATH"
	// EnvAIMID is the environment variable for the AIM product family identifier
	EnvAIMID = "AIM_ID"
	// EnvAIMMetric is the environment variable for the optimization metric
	EnvAIMMetric = "AIM_METRIC"
	// EnvAIMModelID is the environment variable for the model ID
	EnvAIMModelID = "AIM_MODEL_ID"
	// EnvAIMPrecision is the environment variable for the numeric precision
	EnvAIMPrecision = "AIM_PRECISION"
	// EnvAIMProfileID is the environment variable for the profile ID
	EnvAIMProfileID = "AIM_PROFILE_ID"
	// EnvVLLMEnableMetrics enables vLLM metrics
	EnvVLLMEnableMetrics = "VLLM_ENABLE_METRICS"
	// EnvAIMBaseImageRef is the env var baked into AIM model images recording the base image.
	EnvAIMBaseImageRef = "AIM_BASE_IMAGE_REF"

	// EnvAIMKEDAOTelScalerAddress overrides the keda-otel-add-on scaler
	// endpoint written into ScaledObject trigger metadata. Format host:port.
	EnvAIMKEDAOTelScalerAddress = "AIM_KEDA_OTEL_SCALER_ADDRESS"

	// EnvAIMCooldownSecondsPerGiMemory tunes the per-GiB multiplier used
	// to derive scale-to-zero cooldownPeriod from the predictor's memory
	// request. Set to 0 to disable the memory contribution.
	EnvAIMCooldownSecondsPerGiMemory = "AIM_COOLDOWN_SECONDS_PER_GI_MEMORY"
)

// keda-otel-add-on defaults
const (
	// DefaultKEDAOTelScalerAddress is the in-cluster gRPC endpoint of the
	// keda-otel-add-on scaler. Matches the KServe inferenceservice-config
	// `opentelemetryCollector.metricScalerEndpoint` so warm and activation
	// triggers share a single scaler instance.
	DefaultKEDAOTelScalerAddress = "keda-otel-scaler.keda.svc:4318"

	// DefaultCooldownSecondsPerGiMemory is the seconds-per-GiB multiplier
	// in the cooldown heuristic
	//
	//	cooldownPeriod = clamp(300 + memGiB * perGiB, 300, 1200)
	//
	// 5 s/GiB budgets for ~1.6 GB/s warm-cache read throughput plus the
	// keda-otel-add-on scaler's ~120 s rate-decay window and vLLM/ROCm
	// stabilization tail. Errs on the over-cool side because scaling to
	// zero mid-warmup is far more expensive than a few extra idle seconds.
	// Operators on faster/slower storage tune via EnvAIMCooldownSecondsPerGiMemory.
	DefaultCooldownSecondsPerGiMemory = int32(5)

	// DefaultGatewayActivationTargetValue is the targetValue of the
	// gateway-rate activation trigger. Compiled-in, not operator-tunable: the
	// gateway trigger is activation-only
	// (0->1); this value is deliberately large so the trigger never influences
	// the 1->N decision -- it is a neutralizing ceiling, not a req/s target.
	DefaultGatewayActivationTargetValue = "1000"

	// DefaultGatewayActivationOperationOverTime is the aggregation the scaler
	// applies to the gateway series. Compiled-in, not operator-tunable: it must
	// be `avg` because the collector emits per-scrape deltas via
	// `cumulativetodelta` -- `rate` would go negative across Envoy counter
	// resets. This is a JOINED INVARIANT with the collector's processor
	// pipeline (pinned by TestGatewayActivationInvariant); change them together.
	DefaultGatewayActivationOperationOverTime = "avg"
)

// KServe annotation and label keys
const (
	// AnnotationKServeAutoscalerClass is the annotation key for autoscaler class
	AnnotationKServeAutoscalerClass = "serving.kserve.io/autoscalerClass"
	// AutoscalerClassNone disables autoscaling
	AutoscalerClassNone = "none"
	// AutoscalerClassKeda lets KServe author the ScaledObject from the ISVC's
	// AutoScaling spec.
	AutoscalerClassKeda = "keda"
	// AutoscalerClassExternal tells KServe an external system manages
	// autoscaling: KServe authors no ScaledObject and ignores Spec.Replicas
	// diffs. AIM Engine uses this to own the ScaledObject directly.
	AutoscalerClassExternal = "external"
	// LabelKServeInferenceService is the label key used by KServe on predictor pods
	LabelKServeInferenceService = "serving.kserve.io/inferenceservice"
	// AnnotationOTelSidecarInject is the annotation for OpenTelemetry sidecar injection
	AnnotationOTelSidecarInject = "sidecar.opentelemetry.io/inject"
	// AnnotationPrometheusPort is the annotation for Prometheus metrics port
	AnnotationPrometheusPort = "prometheus.kserve.io/port"
	// DefaultPrometheusPort is the default port for vLLM metrics
	DefaultPrometheusPort = "8000"
)

// AIM annotation keys
const (
	// AnnotationReconciliationPaused, when set to "true", pauses reconciliation for the resource.
	// The controller will skip all reconciliation logic and return immediately.
	// This is useful for testing or debugging purposes.
	AnnotationReconciliationPaused = AimLabelDomain + "/reconciliation-paused"

	// AnnotationDeploymentImageRef records the container image to deploy for a
	// given AIM(Cluster)ServiceTemplate copy. Stamped by the AIMModel controller
	// onto fine-tuned template copies at build time so each copy carries the
	// exact image it inherited from its specific source owner — which may differ
	// from sibling copies when matched templates span owners with different base
	// images (e.g. aim-base vs aim-epyc-base) or different versions
	// (versionPolicy=any). When present, AIMService prefers this annotation
	// over the resolved AIMModel's spec.image.
	AnnotationDeploymentImageRef = AimLabelDomain + "/deployment-image-ref"

	// AnnotationPrefixClusterAuth is the key prefix for auth annotations (e.g.
	// cluster-auth/allowed-group) propagated from an AIMService to its InferenceService.
	AnnotationPrefixClusterAuth = "cluster-auth/"

	// AnnotationModelId records the model id the user intends to serve, stamped
	// on every InferenceService the controller creates from the resolved profile/
	// template. It equals the name the runtime serves under (vLLM
	// --served-model-name, exposed at /v1/models).
	AnnotationModelId = AimLabelDomain + "/model-id"

	// AnnotationReconcilerPipeline forces an AIMService onto a specific
	// reconciliation pipeline, bypassing the default spec-shape dispatch.
	// Recognised values are ReconcilerPipelineTemplate (v1alpha1 template
	// pipeline) and ReconcilerPipelineProfile (v1alpha2 profile pipeline).
	// Unknown values and the absence of the annotation both fall through
	// to spec-shape dispatch. Used as an escape hatch for users that want
	// the v1alpha2 model→profile resolver shortcut on a service whose spec
	// shape would otherwise route to the v1alpha1 pipeline, and vice versa.
	AnnotationReconcilerPipeline = AimLabelDomain + "/reconciler-pipeline"

	// ReconcilerPipelineTemplate is the AnnotationReconcilerPipeline value
	// that forces the v1alpha1 template-based pipeline.
	ReconcilerPipelineTemplate = "template"

	// ReconcilerPipelineProfile is the AnnotationReconcilerPipeline value
	// that forces the v1alpha2 profile-based pipeline.
	ReconcilerPipelineProfile = "profile"

	// AnnotationForceRebind, when present (any non-empty value), forces
	// the v1alpha2 AIMService profile resolver to ignore the current
	// status.resolvedProfile sticky binding and re-rank candidates from
	// scratch on the next reconcile.
	//
	// The default binding model is sticky-once-bound: once the resolver
	// commits to a profile, subsequent reconciles keep that binding even
	// if a higher-ranked candidate appears (e.g. when a new AIMModel is
	// added with the same aimId). This protects running services from
	// silently switching weights / precision when unrelated resources
	// land in the same namespace.
	//
	// To opt out for a single rebind (e.g. to adopt a newly-published
	// profile from an image upgrade), set this annotation to any
	// non-empty value:
	//
	//	kubectl annotate aimservice my-svc \
	//	  aim.eai.amd.com/force-rebind=now --overwrite
	//
	// The resolver does NOT clear the annotation; remove it manually
	// once the desired rebind is complete to return to sticky behavior:
	//
	//	kubectl annotate aimservice my-svc \
	//	  aim.eai.amd.com/force-rebind-
	//
	// Leaving the annotation in place keeps the resolver in "always
	// re-rank" mode. This is safe (the ranker is deterministic and the
	// ProfileRebound event only fires when the winner actually changes)
	// but it forgoes the stability guarantee of the sticky default.
	AnnotationForceRebind = AimLabelDomain + "/force-rebind"
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
