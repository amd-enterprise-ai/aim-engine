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

	// LabelRuntimeProjection marks how a projected runtime under the reserved
	// aim- prefix was materialised. The lazy InferenceService-watch projection
	// stamps LabelValueRuntimeProjectionLazy on the shadow ServingRuntime (and
	// its colocated ConfigMap) it materialises; the eager per-profile / model-slug
	// projection stamps LabelValueRuntimeProjectionEager; hand-authored runtimes
	// leave it unset. The lazy reconciler primarily uses it to tell its own
	// shadow — which it must re-apply on every reconcile to self-heal drift / a
	// late cache / backing-profile changes — apart from an eager projection or
	// hand-authored runtime of the same name, which it defers to. Its SSA field
	// manager is fallback identity if an edit removes this marker. A
	// namespace-AIMProfile-backed
	// shadow and a namespace AIMProfile's eager projection carry the SAME
	// AIMProfile ownerReference, so the ownerRef kind alone cannot make this
	// distinction; this label can. The eager marker is what settles ownership
	// after a projection-mode flip turns eager projection on for a name a lazy
	// shadow already materialised: the eager force-apply reclaims the key and
	// overwrites lazy, so the lazy path defers (issue 08).
	LabelRuntimeProjection = AimLabelDomain + "/runtime-projection"

	// LabelRuntimeProjectionState surfaces the projection health of a runtime
	// (and its colocated ConfigMap) ON THE RUNTIME OBJECT ITSELF, so a native /
	// bring-your-own-KServe user holding a ServingRuntime / ClusterServingRuntime
	// sees a signal without hopping to the backing AIMProfile's status. KServe's
	// ServingRuntimeStatus is an empty struct with no status subresource, so the
	// state cannot live in .status; a label under our own domain is the only
	// surface a non-owning controller can honestly write. It is stamped whenever
	// AIM Engine projects the runtime, so LabelValueRuntimeProjectionStateProjected
	// is the only value it ever takes. There is deliberately no "degraded" value:
	// flipping a kept-but-ungated runtime in place would need a targeted metadata
	// write under a dedicated field manager (the plan stops emitting the object in
	// that state), which is out of scope — until then the authoritative degraded
	// signal is the backing profile's RuntimeProjected condition (reachable via
	// this runtime's ownerRef / aim.eai.amd.com/profile label). See
	// AnnotationRuntimeProjectionMessage for the human-readable note.
	LabelRuntimeProjectionState = AimLabelDomain + "/runtime-projection-state"
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

	// LabelValueRuntimeProjectionLazy marks a runtime (and its colocated
	// ConfigMap) materialised by the lazy InferenceService-watch projection — a
	// shadow that must be re-applied on every reconcile to self-heal. See
	// LabelRuntimeProjection.
	LabelValueRuntimeProjectionLazy = "lazy"

	// LabelValueRuntimeProjectionEager marks a per-profile / model-slug runtime
	// (and its colocated ConfigMap) materialised by the eager profile-reconciler
	// projection. The eager path stamps it under the SAME LabelRuntimeProjection
	// key that the lazy path uses, so when the eager force-apply (SSA +
	// ForceOwnership) lands on a name a lazy shadow already owns — the mode-flip
	// case where the operator previously only lazily materialised it — it
	// RECLAIMS the label key and overwrites the lazy marker with this value.
	// ownedByLazyProjection then reports false, so the lazy InferenceService-watch
	// reconciler treats the runtime as complete and defers, and managedFields
	// ownership settles on the single eager manager instead of ping-ponging
	// between the two force-appliers (issue 08). Simply omitting the label from
	// the eager apply would NOT remove a label the lazy field manager owns, hence
	// the explicit overwrite.
	LabelValueRuntimeProjectionEager = "eager"

	// LabelValueRuntimeProjectionStateProjected marks a runtime that AIM Engine
	// is actively projecting from a healthy, projectable profile. It is the only
	// value stamped: a runtime object only ever exists in the projected state,
	// because the projection plan builds it exclusively while the profile is
	// projectable (a gate flip stops re-emitting it rather than mutating it).
	// See LabelRuntimeProjectionState.
	LabelValueRuntimeProjectionStateProjected = "projected"
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
	// VLLMOmniSharedMemorySize is the /dev/shm size used by vLLM-Omni
	// diffusion workloads, whose multi-process USP/VAE workers require more
	// shared memory than the generic runtime default.
	VLLMOmniSharedMemorySize = "32Gi"
	// DefaultHTTPPort is the default HTTP port for inference services
	DefaultHTTPPort = 8000
	// DefaultGatewayPort is the default gateway port
	DefaultGatewayPort = 80
	// AMDGPUResourceName is the whole-device resource published by the AMD
	// Kubernetes device plugin.
	AMDGPUResourceName = "amd.com/gpu"
	// NVIDIAGPUResourceName is the whole-device resource published by the NVIDIA
	// Kubernetes device plugin.
	NVIDIAGPUResourceName = "nvidia.com/gpu"
	// DefaultGPUResourceName is retained for legacy v1alpha1 and profiles that
	// predate acceleratorVendor. Empty vendor continues to mean AMD.
	DefaultGPUResourceName = AMDGPUResourceName
	// DefaultCPURequestPerGPU is the default host CPU request for each GPU.
	DefaultCPURequestPerGPU int64 = 4
	// DefaultMemoryRequestGiPerGPU is the default host memory request, in GiB,
	// for each GPU.
	DefaultMemoryRequestGiPerGPU int64 = 32
	// DefaultMemoryLimitGiPerGPU is the default host memory limit, in GiB, for
	// each GPU.
	DefaultMemoryLimitGiPerGPU int64 = 48
	// AIMCacheBasePath is the base directory for cached models
	AIMCacheBasePath = "/workspace/cache"
	// AIMAdapterMountPath is the path inside the inference container where the
	// service's adapter subtree is mounted (read-only). The inference container
	// loads every directory under this path.
	AIMAdapterMountPath = "/adapters"
	// EnvAIMAdapterSource is the container-contract env var that tells the image
	// which directory to load adapters from. The controller sets it to
	// AIMAdapterMountPath so the image finds the mounted subtree.
	EnvAIMAdapterSource = "AIM_ADAPTER_SOURCE"
	// EnvAIMAdapterMode is the container-contract env var that selects the
	// adapter contract the image honours at boot ("static" or "dynamic").
	EnvAIMAdapterMode = "AIM_ADAPTER_MODE"
	// EnvAIMAdapterRefreshInterval is the dynamic-mode poll cadence (seconds) for
	// the image's filesystem watcher. Ignored in static mode.
	EnvAIMAdapterRefreshInterval = "AIM_ADAPTER_REFRESH_INTERVAL"
	// EnvAIMAdapterMaxCount is the max number of adapters the image should load
	// on-accelerator concurrently.
	EnvAIMAdapterMaxCount = "AIM_ADAPTER_MAX_COUNT"
	// EnvAIMAdapterMaxCPUCount is the max number of adapters the image should
	// cache in CPU memory (>= EnvAIMAdapterMaxCount).
	EnvAIMAdapterMaxCPUCount = "AIM_ADAPTER_MAX_CPU_COUNT"
	// EnvAIMAdapterMaxRank is the max LoRA rank the image should provision for.
	EnvAIMAdapterMaxRank = "AIM_ADAPTER_MAX_RANK"
	// AIMAdapterPVCRoot is the path where the shared adapter-disk PVC is mounted
	// (read-write) inside a staging Job. Staging/aside areas live at this root.
	AIMAdapterPVCRoot = "/adapter-disk"
	// VolumeAdapterDisk is the name of the adapter-disk volume on pods/jobs.
	VolumeAdapterDisk = "adapter-disk"

	// Conservative built-in defaults for the adapter container contract. The
	// dynamic max-rank default is used only when neither the service nor its
	// RuntimeConfig supplies one.
	DefaultAIMAdapterRefreshIntervalSeconds = 30
	DefaultAIMAdapterMaxCount               = 8
	DefaultAIMAdapterMaxCPUCount            = 16
	DefaultAIMAdapterMaxRank                = 32
	DefaultAIMAdapterRank                   = 16

	// LabelAdapterDynamicAllowed is the namespace label that opts a namespace in
	// to dynamic adapter mode. Enforcement is not yet wired (see
	// aimadapter.DynamicModeAllowed).
	LabelAdapterDynamicAllowed = "aim.eai.amd.com/adapter-dynamic-allowed"
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
	// EnvAIMEngine selects the runtime engine declared by the resolved profile
	EnvAIMEngine = "AIM_ENGINE"
	// EnvAIMMetric is the environment variable for the optimization metric
	EnvAIMMetric = "AIM_METRIC"
	// EnvAIMModelID is the environment variable for the model ID
	EnvAIMModelID = "AIM_MODEL_ID"
	// EnvAIMVLLMModel is the direct-vLLM model argument. Projected runtimes use
	// Kubernetes argument expansion ($(AIM_VLLM_MODEL)) so an InferenceService
	// overlay can switch an online model ID to a service-owned cache mount without
	// replacing the complete generated vLLM argument list.
	EnvAIMVLLMModel = "AIM_VLLM_MODEL"
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

	// EnvAIMGatewayActivationScope selects the label scheme the
	// scale-from-zero gateway activation trigger queries, matching the OTel
	// collector deployed on the cluster:
	//   - GatewayActivationScopeHTTPRoute: Envoy Gateway names the
	//     upstream cluster httproute/<ns>/<name>/rule/N; the collector labels
	//     the series by HTTPRoute.
	//   - GatewayActivationScopeDeployment: legacy kgateway names the cluster
	//     kube_<ns>_<svc>_<port>; the collector labels the series by the
	//     predictor Deployment.
	//   - GatewayActivationScopeCustom: every scale-to-zero service must resolve
	//     an activationMetricQueryTemplate from its service or RuntimeConfig.
	//   - GatewayActivationScopeNone: gateway activation is disabled; services
	//     requesting minReplicas=0 fail configuration validation.
	// JOINED INVARIANT with the collector: both must match the gateway
	// implementation, or activation silently never fires.
	EnvAIMGatewayActivationScope = "AIM_GATEWAY_ACTIVATION_SCOPE"
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
	// (0->1); this value is deliberately unreachable for an activation export
	// interval so the trigger never influences the 1->N decision. It is a
	// neutralizing ceiling, not a req/s target.
	DefaultGatewayActivationTargetValue = "1000000000"

	// DefaultGatewayActivationOperationOverTime is the aggregation the scaler
	// applies to the gateway series. Compiled-in, not operator-tunable: it must
	// be `avg` because gateway integrations emit delta values -- Envoy Gateway
	// at the source and kgateway through its collector. Applying `rate` again
	// would distort those deltas. This is pinned with the provider pipelines by
	// TestGatewayActivationInvariant; change them together.
	DefaultGatewayActivationOperationOverTime = "avg"

	// GatewayActivationScopeHTTPRoute scopes the gateway activation trigger by
	// the HTTPRoute name (Envoy Gateway cluster naming).
	GatewayActivationScopeHTTPRoute = "httproute"

	// GatewayActivationScopeDeployment scopes the gateway activation trigger by
	// the predictor Deployment (legacy kgateway cluster naming).
	GatewayActivationScopeDeployment = "deployment"

	// GatewayActivationScopeCustom requires a service or RuntimeConfig-provided
	// activation metric query and does not imply a bundled collector.
	GatewayActivationScopeCustom = "custom"

	// GatewayActivationScopeNone disables gateway activation. It is also the
	// default when the environment variable is unset or invalid, so non-Helm
	// installations remain gateway-neutral unless they opt in explicitly.
	GatewayActivationScopeNone = "none"
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

	// AnnotationRuntimeProfile records the profile (AIMClusterProfile or namespace
	// AIMProfile) that backs the runtime an InferenceService references. The
	// AIMService reconciler stamps it as a fast-path for the lazy
	// runtime-projection watcher: when the referenced runtime has not been created
	// yet (cross-scope, or Reduced mode where no eager per-profile runtime
	// exists), the watcher resolves the backing profile from this annotation
	// (namespace-first, then cluster) instead of the runtime's ownerRef/label. It
	// is purely an optimization — the native flow (a managed ClusterServingRuntime
	// → backing profile) needs no annotation.
	AnnotationRuntimeProfile = AimLabelDomain + "/runtime-profile"

	// Projected-runtime discoverability annotations (v1alpha2 runtime
	// projection). Per-profile ServingRuntime / ClusterServingRuntime names are
	// truncated + hashed (aim-<truncated-profile>-<hash>) and are therefore not
	// reversible. These annotations restore full-fidelity identity on the
	// projected object (and its colocated ConfigMap) so humans and tooling can
	// read the untruncated profile name and the profile axes straight off the
	// runtime without reversing the name. Values are carried verbatim (they are
	// not constrained to the label grammar); empty axes are omitted.

	// AnnotationProjectedProfile records the untruncated backing profile name of
	// a projected runtime, recovering the identity the hashed object name hides.
	AnnotationProjectedProfile = AimLabelDomain + "/projected.profile"

	// AnnotationProjectedAimID records the backing profile's aimId (model
	// architecture identifier) on a projected runtime.
	AnnotationProjectedAimID = AimLabelDomain + "/projected.aim-id"

	// AnnotationProjectedModelID records the backing profile's modelId on a
	// projected runtime.
	AnnotationProjectedModelID = AimLabelDomain + "/projected.model-id"

	// AnnotationProjectedPrecision records the backing profile's precision on a
	// projected runtime.
	AnnotationProjectedPrecision = AimLabelDomain + "/projected.precision"

	// AnnotationProjectedMetric records the backing profile's optimization
	// metric on a projected runtime.
	AnnotationProjectedMetric = AimLabelDomain + "/projected.metric"

	// AnnotationRuntimeProjectionMessage is the human-readable companion to the
	// LabelRuntimeProjectionState marker, stamped on a projected runtime (and its
	// colocated ConfigMap). It carries a stable, per-object-data-free note (so
	// re-applies never churn it) that explains the marker and points a native /
	// bring-your-own-KServe user at the authoritative, transitioning health on
	// the backing profile's RuntimeProjected condition.
	AnnotationRuntimeProjectionMessage = AimLabelDomain + "/runtime-projection-message"

	// AnnotationRuntimeProjectionContentHash identifies the workload-affecting
	// content of a namespaced ServingRuntime and its colocated ConfigMap. Both
	// objects carry the same hash, computed from ServingRuntime.spec and the
	// ConfigMap's data, binaryData, and immutable fields. Ephemeral metadata is
	// deliberately excluded. Consumers use it to wait until both projection
	// siblings represent the current profile and cache state.
	AnnotationRuntimeProjectionContentHash = AimLabelDomain + "/runtime-projection-content-hash"

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

	// AnnotationMigrationPreserveInferenceServiceGeneration pins the legacy
	// inline InferenceService during a no-rollout template-to-profile migration.
	// The profile pipeline honors it only when its decimal value equals the
	// AIMService generation and AnnotationMigrationPreserveInferenceServiceUID
	// matches an existing InferenceService with legacy template labels. A later
	// spec update advances generation and resumes normal profile rendering.
	AnnotationMigrationPreserveInferenceServiceGeneration = AimLabelDomain + "/migration-preserve-inference-service-generation"

	// AnnotationMigrationPreserveInferenceServiceUID binds an active migration
	// preservation generation to the exact legacy InferenceService UID.
	AnnotationMigrationPreserveInferenceServiceUID = AimLabelDomain + "/migration-preserve-inference-service-uid"

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
