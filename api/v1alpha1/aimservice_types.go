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
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

const (
	// AIMServiceTemplateIndexKey is the field index key used by controller-runtime for
	// indexing AIMService resources by their template reference (.spec.template.name).
	// This enables efficient lookups of services that reference a specific template.
	AIMServiceTemplateIndexKey = ".spec.templateRef"

	// AIMServiceResolvedTemplateIndexKey is the field index key for resolved template name
	// Indexes by .status.resolvedTemplate.name for finding services using a specific template
	AIMServiceResolvedTemplateIndexKey = ".status.resolvedTemplate.name"

	// AIMServiceProfileIndexKey is the field index key for indexing AIMService resources
	// by their profile reference (.spec.profile.name).
	AIMServiceProfileIndexKey = ".spec.profileRef"

	// AIMServiceAdapterArtifactIndexKey is the field index key for indexing AIMService
	// resources by the names of the adapter artifacts they reference
	// (.spec.adapters[].name). Enables enqueueing services when an adapter artifact changes.
	AIMServiceAdapterArtifactIndexKey = ".spec.adapters.name"
)

// AIMServiceAdapterKind enumerates the kinds an adapter reference may target.
// Restricted to AIMArtifact in v1; reserved to admit a future AIMAdapter catalog kind.
// +kubebuilder:validation:Enum=AIMArtifact
type AIMServiceAdapterKind string

const (
	// AdapterKindAIMArtifact references an AIMArtifact (type=adapter).
	AdapterKindAIMArtifact AIMServiceAdapterKind = "AIMArtifact"
)

// AIMAdapterMode selects the adapter contract for the service. Its values are
// the lowercase tokens the inference container reads via the AIM_ADAPTER_MODE
// env, and the field is immutable after creation.
//   - static (default): the served set is fixed at creation — spec.adapters is
//     CEL-immutable. The adapter disk is mounted read-only only when the service
//     declares at least one adapter.
//   - dynamic: spec.adapters may be edited after creation; the runtime
//     hot-loads/unloads from the mounted subtree. The adapter disk is mounted
//     (immutably) whenever the service is in dynamic mode — even at zero adapters
//     — so add/remove never restarts the pod.
//
// A service serves no adapters by simply declaring none: the default-static,
// no-adapters case mounts nothing.
//
// +kubebuilder:validation:Enum=static;dynamic
type AIMAdapterMode string

const (
	// AdapterModeStatic fixes the adapter set at creation; the disk is mounted
	// only when adapters are declared.
	AdapterModeStatic AIMAdapterMode = "static"
	// AdapterModeDynamic permits editing spec.adapters and mounts the disk even
	// at zero adapters.
	AdapterModeDynamic AIMAdapterMode = "dynamic"
)

// AdapterModeDynamic reports whether the service uses dynamic adapter mode.
func (s *AIMServiceSpec) AdapterModeDynamic() bool {
	return s.AdapterMode == AdapterModeDynamic
}

// AdaptersEnabled reports whether the service needs the adapter disk mounted and
// its per-service subtree provisioned. True when the service declares at least
// one adapter, or is in dynamic mode (which mounts the disk even at zero adapters
// so adapters can be added later without restarting the pod). Deliberately
// independent of the adapter-list length in dynamic mode.
func (s *AIMServiceSpec) AdaptersEnabled() bool {
	return len(s.Adapters) > 0 || s.AdapterModeDynamic()
}

// AIMServiceAdapterReference is a typed reference to a LoRA adapter served by this
// service. Today it is resolved as a pure reference to an existing adapter
// artifact. The inline bootstrap fields (sourceUri/modelId/rank) are reserved:
// the schema accepts them, but create-if-missing self-healing is not yet wired,
// so a referenced adapter artifact must currently exist.
// +kubebuilder:validation:XValidation:rule="!has(self.sourceUri) || has(self.modelId)",message="modelId is required when sourceUri is set"
// +kubebuilder:validation:XValidation:rule="has(self.sourceUri) || (!has(self.modelId) && !has(self.rank))",message="modelId and rank are only allowed together with sourceUri"
type AIMServiceAdapterReference struct {
	// Name is the metadata.name of the referenced adapter artifact.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// Kind is the kind of the referenced adapter. Required; restricted to AIMArtifact in v1.
	Kind AIMServiceAdapterKind `json:"kind"`

	// SourceURI is an optional create-if-missing bootstrap source. When set and no
	// adapter artifact named Name exists, a later release will create one from this
	// source; a pre-existing artifact always wins. RESERVED: not yet acted on.
	// +optional
	SourceURI string `json:"sourceUri,omitempty"`

	// ModelID is the adapter's canonical model id. Required when SourceURI is set.
	// RESERVED: only meaningful alongside SourceURI.
	// +optional
	ModelID string `json:"modelId,omitempty"`

	// Rank is the optional LoRA rank for the bootstrapped adapter. RESERVED: only
	// meaningful alongside SourceURI.
	// +optional
	// +kubebuilder:validation:Minimum=1
	Rank *int32 `json:"rank,omitempty"`
}

// AIMAdapterState is the disk-side lifecycle state of an adapter within a service's
// subtree. The controller tracks staging and removal directly; the engine-reported
// states (Loaded/LoadRejected) are reserved until the inference container exposes a
// per-adapter load-status surface.
// +kubebuilder:validation:Enum=Pending;Downloading;Downloaded;Deleting;Loaded;LoadRejected
type AIMAdapterState string

const (
	// AdapterStatePending means the adapter is applied and waiting on a precondition.
	AdapterStatePending AIMAdapterState = "Pending"
	// AdapterStateDownloading means a staging Job is running for this adapter.
	AdapterStateDownloading AIMAdapterState = "Downloading"
	// AdapterStateDownloaded means the bytes are staged in this service's subtree.
	AdapterStateDownloaded AIMAdapterState = "Downloaded"
	// AdapterStateDeleting means the adapter was removed from spec.adapters and its
	// bytes are being reclaimed from the service subtree by the subtree-sync Job.
	// The entry is dropped from status once the prune completes.
	AdapterStateDeleting AIMAdapterState = "Deleting"
	// AdapterStateLoaded means the inference engine has the adapter in memory.
	// RESERVED: engine-reported, not yet populated by the controller.
	AdapterStateLoaded AIMAdapterState = "Loaded"
	// AdapterStateLoadRejected means the bytes are present but the engine declined
	// to load the adapter. RESERVED: engine-reported, not yet populated.
	AdapterStateLoadRejected AIMAdapterState = "LoadRejected"
)

// AIMServiceAdapterStatus is the per-adapter status aggregated onto an AIMService.
type AIMServiceAdapterStatus struct {
	// Name is the adapter reference name.
	Name string `json:"name"`

	// AdapterPath is the on-disk directory name (mirrored from the artifact).
	// +optional
	AdapterPath string `json:"adapterPath,omitempty"`

	// ModelID is the adapter's canonical model id (mirrored from the artifact).
	// +optional
	ModelID string `json:"modelId,omitempty"`

	// State is the disk-side state of the adapter for this service.
	// +optional
	State AIMAdapterState `json:"state,omitempty"`

	// LoadedReplicas reports how many serving replicas have the adapter loaded,
	// as "loaded/total" (e.g. "3/3"). RESERVED: engine-reported, not yet populated.
	// +optional
	LoadedReplicas string `json:"loadedReplicas,omitempty"`

	// LastObserved is when the controller last observed this adapter's state.
	// +optional
	LastObserved *metav1.Time `json:"lastObserved,omitempty"`

	// LastError carries the most recent error for this adapter (e.g. a mirrored
	// failing reason from the underlying artifact).
	// +optional
	LastError string `json:"lastError,omitempty"`
}

// AIMCachingMode controls caching behavior for a service.
// Canonical values are Dedicated and Shared.
// Legacy values are accepted for backward compatibility:
// - Always maps to Shared
// - Auto maps to Shared
// - Never maps to Dedicated
// +kubebuilder:validation:Enum=Dedicated;Shared;Auto;Always;Never
type AIMCachingMode string

const (
	// CachingModeDedicated always creates service-owned dedicated caches/artifacts.
	CachingModeDedicated AIMCachingMode = "Dedicated"

	// CachingModeShared reuses and creates shared caches/artifacts.
	CachingModeShared AIMCachingMode = "Shared"

	// CachingModeAuto is deprecated legacy value that maps to Shared.
	CachingModeAuto AIMCachingMode = "Auto"

	// CachingModeAlways is deprecated legacy value that maps to Shared.
	CachingModeAlways AIMCachingMode = "Always"

	// CachingModeNever is deprecated legacy value that maps to Dedicated.
	CachingModeNever AIMCachingMode = "Never"
)

// AIMServiceCachingConfig controls caching behavior for a service.
// +kubebuilder:validation:XValidation:rule="self.mode == oldSelf.mode",message="caching mode is immutable after creation"
type AIMServiceCachingConfig struct {
	// Mode controls when to use caching.
	// Canonical values:
	// - Shared (default): reuse/create shared cache assets
	// - Dedicated: create service-owned dedicated cache assets
	//
	// Legacy values are accepted and normalized:
	// - Always -> Shared
	// - Auto -> Shared
	// - Never -> Dedicated
	// +kubebuilder:default=Shared
	// +optional
	Mode AIMCachingMode `json:"mode,omitempty"`

	// Env supplies credentials for model downloads (for example a HuggingFace
	// token via secretKeyRef). Unlike the inference container env, these
	// variables reach only the model-download Job, so download-only secrets are
	// never injected into the serving container. They are also reachable for
	// cluster-scoped and overlay profiles, where the profile's own caching.env
	// does not exist. Merged over the profile's caching.env (service wins).
	// +optional
	// +listType=map
	// +listMapKey=name
	Env []corev1.EnvVar `json:"env,omitempty"`
}

// AIMServiceTemplateConfig contains template selection configuration for AIMService.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="template selection is immutable after creation"
type AIMServiceTemplateConfig struct {
	// Name is the name of the AIMServiceTemplate or AIMClusterServiceTemplate to use.
	// The template selects the runtime profile and GPU parameters.
	// When not specified, a template will be automatically selected based on the model.
	// +optional
	Name string `json:"name,omitempty"`

	// AllowUnoptimized, if true, will allow automatic selection of templates
	// that resolve to an unoptimized profile.
	// +optional
	AllowUnoptimized bool `json:"allowUnoptimized,omitempty"`
}

// AIMServiceModel specifies which model to deploy. Exactly one field must be set.
// +kubebuilder:validation:XValidation:rule="(has(self.name) ? 1 : 0) + (has(self.image) ? 1 : 0) + (has(self.custom) ? 1 : 0) == 1",message="exactly one of name, image, or custom must be specified"
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="model selection is immutable after creation"
type AIMServiceModel struct {
	// Name references an existing AIMModel or AIMClusterModel by metadata.name.
	// The controller looks for a namespace-scoped AIMModel first, then falls back to cluster-scoped AIMClusterModel.
	// Example: `meta-llama-3-8b`
	// +optional
	Name *string `json:"name,omitempty"`

	// Image specifies a container image URI directly.
	// The controller searches for an existing model with this image, or creates one if none exists.
	// Auto-created models are namespace-scoped and can be reused by other services.
	// Example: `ghcr.io/silogen/llama-3-8b:v1.2.0`
	// +optional
	Image *string `json:"image,omitempty"`

	// Custom specifies a custom model configuration with explicit base image,
	// model sources, and hardware requirements. The controller will search for
	// an existing matching AIMModel or auto-create one if not found.
	// +optional
	Custom *AIMServiceModelCustom `json:"custom,omitempty"`
}

// AIMServiceModelCustom specifies a custom model configuration with explicit base image,
// model sources, and hardware requirements. Used for ad-hoc custom model deployments.
// +kubebuilder:validation:XValidation:rule="size(self.modelSources) >= 1",message="at least one model source must be specified"
type AIMServiceModelCustom struct {
	// BaseImage is the container image URI for the AIM base image.
	// This will be used as the image for the auto-created AIMModel.
	// Example: `ghcr.io/silogen/aim-base:0.7.0`
	// +required
	BaseImage string `json:"baseImage"`

	// ModelSources specifies the model sources to use.
	// The controller will search for or create an AIMModel with these sources.
	// The size field is optional - if not specified, it will be discovered by the download job.
	// AIM runtime currently supports only one model source.
	// +required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=1
	ModelSources []AIMModelSource `json:"modelSources"`

	// Hardware specifies the GPU and CPU requirements for this custom model.
	// GPU is optional - if not set, no GPUs are requested (CPU-only model).
	// +required
	Hardware AIMHardwareRequirements `json:"hardware"`
}

// AIMServiceOverrides allows overriding template parameters at the service level.
// All fields are optional. When specified, they override the corresponding values
// from the referenced AIMServiceTemplate.
type AIMServiceOverrides struct {
	AIMRuntimeParameters `json:",inline"`
}

// AIMServiceProfileConfig contains profile selection configuration for AIMService v1alpha2.
// When set, the service uses a profile-based reconciliation path instead of the template path.
//
// Exactly one of Name and Selector must be set. Name resolves an AIMProfile /
// AIMClusterProfile directly; Selector lists candidates by provenance and spec
// fields (typically combined with `spec.model.name`, which the controller
// treats as a shortcut for `selector.modelRef.name`).
// +kubebuilder:validation:XValidation:rule="!(has(self.name) && has(self.selector))",message="spec.profile.name and spec.profile.selector are mutually exclusive"
// +kubebuilder:validation:XValidation:rule="has(self.name) || has(self.selector)",message="spec.profile must set name or selector"
type AIMServiceProfileConfig struct {
	// Name is the name of the AIMProfile or AIMClusterProfile to use.
	// The controller looks for a namespace-scoped AIMProfile first, then falls back to AIMClusterProfile.
	// Mutually exclusive with Selector.
	// +optional
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name,omitempty"`

	// Selector narrows candidate AIMProfile / AIMClusterProfile objects via the
	// shared provenance labels (role, source-model, origin) and spec filters
	// (aimId, precision, acceleratorModel, ...). The controller forces
	// `selector.role = Deployable` at evaluation time; user-supplied values
	// for that field are rejected by CEL on v1alpha2.
	//
	// For every selector-driven AIMService the controller requires at least
	// one of `selector.aimId` or `selector.modelRef.name` so the watch
	// fan-out can reach the service via an O(1) index lookup. The top-level
	// `spec.model.name` shortcut is treated as if the user had set
	// `selector.modelRef.name` to the same value when not explicit.
	// +optional
	Selector *ProfileSelector `json:"selector,omitempty"`
}

// AIMServiceProfileOverrides allows overriding profile parameters at the service level.
// When specified, the controller materialises a service-owned overlay AIMProfile
// derived from the referenced profile with these overrides applied; the original
// profile is not modified. The downstream AIMProfileCache and InferenceService are
// then resolved from the overlay, so the override participates in cache key
// computation as well as inference-pod env wiring.
//
// This type is a SUBSET of `aimv1alpha1.ProfileOverrides` (the type
// AIMProfileSet derivation uses). Both go through the same internal apply
// primitive (`internal/v1alpha2/aimprofile.ApplyProfileCopyOverrides`) so
// the merge semantics match, but the service-level overlay intentionally
// omits the `Image` override that AIMProfileSet's overrides expose:
// changing the runtime container image per-service belongs at the profile
// level (via spec.profiles.overrides.image on the source AIMModel /
// AIMProfileSet), not at the consumer. Restricting the field set here
// keeps the per-service overlay focused on workload-shape changes
// (weights, env, args, hardware count) where service-level overrides are
// the right tool.
type AIMServiceProfileOverrides struct {
	// ModelSources replaces the referenced profile's modelSources entirely.
	// Use this to point a profile at user-supplied weights (e.g. a fine-tuned
	// checkpoint) without forking the profile itself. The first source's
	// modelId becomes the overlay profile's modelId.
	// +optional
	ModelSources []AIMModelSource `json:"modelSources,omitempty"`

	// AcceleratorModel replaces the referenced profile's acceleratorModel
	// (e.g. "MI300X" -> "MI325X"). Validation against actual cluster
	// availability is left to the AIMServiceTemplate / runtime layers.
	// +optional
	AcceleratorModel string `json:"acceleratorModel,omitempty"`

	// AcceleratorCount replaces the referenced profile's acceleratorCount.
	// +optional
	AcceleratorCount *int32 `json:"acceleratorCount,omitempty"`

	// AcceleratorPartitioningMode replaces the referenced profile's
	// acceleratorPartitioningMode. Complete replacement, not a merge — single
	// string, no substruct ambiguity. Empty string means "no override" (the
	// resolved overlay inherits the base profile's mode). Use this to deploy a
	// profile written for whole GPUs onto a partition slice. Whenever this
	// override is set (to any value, including "unpartitioned"), the CEL rule on
	// AIMService also requires an acceleratorCount override: partition mode
	// changes the per-unit interpretation of acceleratorCount, and CEL cannot
	// read the base profile to tell whether the meaning actually changed, so it
	// conservatively requires the count be restated. See
	// AcceleratorPartitioningMode on AIMProfileSpecCommon for the reserved
	// values ("unpartitioned", "partitioned", "<C>-<M>").
	// +optional
	AcceleratorPartitioningMode string `json:"acceleratorPartitioningMode,omitempty"`

	// ContainerEnv merges by env-var name on top of the profile's
	// containerEnv. Matching names override; new names are appended.
	// AIM framework variables (AIM_*) reserved for the controller are
	// applied after the overlay's containerEnv and cannot be overridden
	// here.
	// +optional
	// +listType=map
	// +listMapKey=name
	ContainerEnv []corev1.EnvVar `json:"containerEnv,omitempty"`

	// EngineEnv merges by key on top of the profile's engineEnv. These
	// variables flow into the inference engine's runtime configuration.
	// +optional
	EngineEnv map[string]string `json:"engineEnv,omitempty"`

	// EngineArgs shallow-merges on top of the profile's engineArgs,
	// overriding matching top-level keys. Values are passed verbatim
	// to the inference engine CLI.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Schemaless
	// +optional
	EngineArgs *apiextensionsv1.JSON `json:"engineArgs,omitempty"`
}

// AIMServiceSpec defines the desired state of AIMService.
//
// Binds a canonical model to an AIMServiceTemplate and configures replicas,
// caching behavior, and optional overrides. The template governs the base
// runtime selection knobs, while the overrides field allows service-specific
// customization.
//
// With v1alpha2, a Profile can be used instead of a Template. Template and Profile
// are mutually exclusive — at least one resolution path must be specified.
// +kubebuilder:validation:XValidation:rule="!has(self.minReplicas) || !has(self.maxReplicas) || self.minReplicas <= self.maxReplicas",message="minReplicas must be less than or equal to maxReplicas"
type AIMServiceSpec struct {
	// Model specifies which model to deploy using one of the available reference methods.
	// Use `name` to reference an existing AIMModel/AIMClusterModel by name, or use `image`
	// to specify a container image URI directly (which will auto-create a model if needed).
	// Required for v1alpha1 (template path), not permitted for v1alpha2 (profile path).
	// +optional
	Model *AIMServiceModel `json:"model,omitempty"`

	// Template contains template selection and configuration.
	// Use Template.Name to specify an explicit template, or omit to auto-select.
	// Mutually exclusive with Profile (v1alpha2).
	// +optional
	Template *AIMServiceTemplateConfig `json:"template,omitempty"`

	// Profile contains profile selection configuration (v1alpha2 only).
	// When set, the service uses a profile-based reconciliation path.
	// Mutually exclusive with Template.
	// +optional
	Profile *AIMServiceProfileConfig `json:"profile,omitempty"`

	// ProfileOverrides allows overriding specific profile parameters for this service.
	// Only valid when Profile is set.
	// +optional
	ProfileOverrides *AIMServiceProfileOverrides `json:"profileOverrides,omitempty"`

	// AdapterMode is the immutable adapter contract for the service, mapped
	// directly to the AIM_ADAPTER_MODE container env. static (default) freezes
	// spec.adapters and mounts the adapter disk only when adapters are declared;
	// dynamic allows editing spec.adapters and mounts the disk even at zero
	// adapters (so add/remove never restarts the pod). It is immutable after
	// creation.
	// +optional
	// +kubebuilder:default=static
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec.adapterMode is immutable after creation"
	AdapterMode AIMAdapterMode `json:"adapterMode,omitempty"`

	// Adapters is the load-bearing list of LoRA adapters this service serves.
	// The list may only be edited after creation when adapterMode is dynamic
	// (static freezes it). Omitting the list serves no adapters.
	// Supported on both the template (v1alpha1) and profile (v1alpha2) pipelines:
	// the base model the adapters attach to is resolved from the service's
	// template cache or profile cache respectively. In dynamic mode the list may
	// be edited after creation: adding an adapter stages it into the service's
	// subtree and the aim-runtime hot-loads it; removing one lets the runtime
	// unload it (subtree cleanup is reclaimed out-of-band). The InferenceService
	// is never modified for adapter changes — it mounts the whole per-service
	// subtree read-only. Entries are pure references; (kind, name) pairs must be
	// unique. Omitting the list serves no adapters.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=64
	Adapters []AIMServiceAdapterReference `json:"adapters,omitempty"`

	// Caching controls caching behavior for this service.
	// When nil, defaults to Shared mode.
	// +optional
	Caching *AIMServiceCachingConfig `json:"caching,omitempty"`

	// DEPRECATED: Use Caching.Mode instead. This field will be removed in a future version.
	// This field is no longer honored by the controller.
	// +optional
	// +kubebuilder:validation:Deprecated
	// +kubebuilder:validation:DeprecatedMessage="Use Caching.Mode instead. This field will be removed in a future version."
	CacheModel *bool `json:"cacheModel,omitempty"`

	// Replicas specifies the number of replicas for this service.
	// When not specified, defaults to 1 replica.
	// This value overrides any replica settings from the template.
	// For autoscaling, use MinReplicas and MaxReplicas instead.
	// +optional
	// +kubebuilder:default=1
	Replicas *int32 `json:"replicas,omitempty"`

	// MinReplicas specifies the minimum number of replicas for autoscaling.
	// Defaults to 1. Set to 0 to enable scale-to-zero: KEDA idles the predictor
	// to zero replicas when idle and brings it back up on the next request.
	// When specified with MaxReplicas, enables autoscaling for the service.
	// +optional
	// +kubebuilder:validation:Minimum=0
	MinReplicas *int32 `json:"minReplicas,omitempty"`

	// MaxReplicas specifies the maximum number of replicas for autoscaling.
	// Required when MinReplicas is set or when AutoScaling configuration is provided.
	// +optional
	// +kubebuilder:validation:Minimum=1
	MaxReplicas *int32 `json:"maxReplicas,omitempty"`

	// AutoScaling configures advanced autoscaling behavior using KEDA.
	// Supports custom metrics from OpenTelemetry backend.
	// When specified, MinReplicas and MaxReplicas should also be set.
	// +optional
	AutoScaling *AIMServiceAutoScaling `json:"autoScaling,omitempty"`

	// RuntimeConfigRef contains the runtime config reference for this service.
	// The result of the merged runtime configs is merged with the inline AIMServiceRuntimeConfig configuration.
	RuntimeConfigRef `json:",inline"`

	// Inline AIMServiceRuntimeConfig fields for cleaner access
	AIMServiceRuntimeConfig `json:",inline"`

	// Resources overrides the container resource requirements for this service.
	// When specified, these values take precedence over the template and image defaults.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// Overrides allows overriding specific template parameters for this service.
	// When specified, these values take precedence over the template values.
	// +optional
	Overrides *AIMServiceOverrides `json:"overrides,omitempty"`

	// ImagePullSecrets references secrets for pulling AIM container images.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// ServiceAccountName specifies the Kubernetes service account to use for the inference workload.
	// This service account is used by the deployed inference pods.
	// If empty, the default service account for the namespace is used.
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// PriorityClassName specifies the priority class for the inference pods.
	// This maps directly to the Kubernetes PriorityClassName field on the pod spec.
	// If empty, no priority class is set.
	// +optional
	PriorityClassName string `json:"priorityClassName,omitempty"`
}

// AIMServiceStatus defines the observed state of AIMService.
type AIMServiceStatus struct {
	// ObservedGeneration is the most recent generation observed by the controller.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest observations of template state.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ResolvedRuntimeConfig captures metadata about the runtime config that was resolved.
	// +optional
	ResolvedRuntimeConfig *AIMResolvedReference `json:"resolvedRuntimeConfig,omitempty"`

	// ResolvedModel captures metadata about the image that was resolved.
	// +optional
	ResolvedModel *AIMResolvedReference `json:"resolvedModel,omitempty"`

	// Status represents the current high‑level status of the service lifecycle.
	// Values: `Pending`, `Starting`, `Running`, `Degraded`, `Failed`.
	// +kubebuilder:default=Pending
	// +kubebuilder:validation:Enum=Pending;Starting;Running;Degraded;Failed
	Status constants.AIMStatus `json:"status,omitempty"`

	// Routing surfaces information about the configured HTTP routing, when enabled.
	// +optional
	Routing *AIMServiceRoutingStatus `json:"routing,omitempty"`

	// ResolvedTemplate captures metadata about the template that satisfied the reference.
	ResolvedTemplate *AIMResolvedReference `json:"resolvedTemplate,omitempty"`

	// ResolvedProfile captures metadata about the profile that satisfied the reference.
	// Set when the service uses a profile-based reconciliation path (v1alpha2).
	// +optional
	ResolvedProfile *AIMResolvedReference `json:"resolvedProfile,omitempty"`

	// Cache captures cache-related status for this service.
	// +optional
	Cache *AIMServiceCacheStatus `json:"cache,omitempty"`

	// Runtime captures runtime status including replica counts.
	// +optional
	Runtime *AIMServiceRuntimeStatus `json:"runtime,omitempty"`

	// Adapters reports the per-adapter disk-side status for services that declare
	// spec.adapters. One entry per declared adapter. Observation is
	// best-effort/eventual; the disk state the controller wrote is authoritative.
	// +optional
	// +listType=map
	// +listMapKey=name
	Adapters []AIMServiceAdapterStatus `json:"adapters,omitempty"`

	// AdapterSubtreeSyncKey records the declared adapter set most recently
	// reconciled onto the service's adapter subtree by the subtree-sync Job
	// (a hash of the sorted spec.adapters names). The controller re-runs the
	// sync Job — which creates the subtree and prunes adapter directories no
	// longer declared — whenever this drifts from the current desired set, so
	// editing spec.adapters reclaims removed adapters without re-run loops.
	// +optional
	AdapterSubtreeSyncKey string `json:"adapterSubtreeSyncKey,omitempty"`

	// AdapterDiskPersistentVolumeClaim is the resolved shared adapter-disk PVC
	// (from the base model artifact's status). It is recorded here once resolved
	// and reused when a transient parent-resolution gap would otherwise leave it
	// empty, so a blip never re-renders the InferenceService without its adapter
	// mount and restarts a running predictor. Never cleared once set.
	// +optional
	AdapterDiskPersistentVolumeClaim string `json:"adapterDiskPersistentVolumeClaim,omitempty"`
}

// AIMServiceCacheStatus captures cache-related status for an AIMService.
//
// Exactly one of TemplateCacheRef / ProfileCacheRef is populated, depending on
// which reconciliation path produced the cache:
//   - TemplateCacheRef is set by the v1alpha1 (template-based) path and points
//     to an AIMTemplateCache.
//   - ProfileCacheRef is set by the v1alpha2 (profile-based) path and points to
//     an AIMProfileCache.
type AIMServiceCacheStatus struct {
	// TemplateCacheRef references the AIMTemplateCache being used, if any.
	// Set by the v1alpha1 (template-based) reconciliation path.
	// +optional
	TemplateCacheRef *AIMResolvedReference `json:"templateCacheRef,omitempty"`

	// ProfileCacheRef references the AIMProfileCache being used, if any.
	// Set by the v1alpha2 (profile-based) reconciliation path.
	// +optional
	ProfileCacheRef *AIMResolvedReference `json:"profileCacheRef,omitempty"`

	// RetryAttempts tracks how many times this service has attempted to retry a failed cache.
	// Each service gets exactly one retry attempt. When a cache enters Failed state,
	// this counter is incremented from 0 to 1 after deleting failed Artifacts.
	// If the retry fails (cache enters Failed again with attempts == 1), the service degrades.
	// +optional
	RetryAttempts int `json:"retryAttempts,omitempty"`
}

// AIMServiceRuntimeStatus captures runtime status including replica counts from HPA.
type AIMServiceRuntimeStatus struct {
	// CurrentReplicas is the current number of replicas as reported by the HPA.
	CurrentReplicas int32 `json:"currentReplicas"`

	// DesiredReplicas is the desired number of replicas as determined by the HPA.
	DesiredReplicas int32 `json:"desiredReplicas"`

	// MinReplicas is the minimum number of replicas configured for autoscaling.
	MinReplicas int32 `json:"minReplicas"`

	// MaxReplicas is the maximum number of replicas configured for autoscaling.
	MaxReplicas int32 `json:"maxReplicas"`

	// Replicas is a formatted display string for kubectl output.
	// Shows "current" for fixed replicas or "current/desired (min-max)" for autoscaling.
	// +optional
	Replicas string `json:"replicas,omitempty"`
}

func (s *AIMService) GetRuntimeConfigRef() RuntimeConfigRef {
	return s.Spec.RuntimeConfigRef
}

func (s *AIMServiceStatus) GetConditions() []metav1.Condition {
	return s.Conditions
}

func (s *AIMServiceStatus) SetConditions(conditions []metav1.Condition) {
	s.Conditions = conditions
}

func (s *AIMServiceStatus) SetStatus(status string) {
	// Map framework statuses to AIMService-specific statuses.
	// AIMService uses: Pending, Starting, Running, Failed, Degraded
	// Framework uses: Pending, Progressing, Ready, Failed, Degraded
	switch constants.AIMStatus(status) {
	case constants.AIMStatusProgressing:
		s.Status = constants.AIMStatusStarting
	case constants.AIMStatusReady:
		s.Status = constants.AIMStatusRunning
	default:
		s.Status = constants.AIMStatus(status)
	}
}

func (s *AIMServiceStatus) GetAIMStatus() constants.AIMStatus {
	return s.Status
}

// AIMServiceStatusEnum defines coarse-grained states for a service.
// +kubebuilder:validation:Enum=Pending;Starting;Running;Failed;Degraded
type AIMServiceStatusEnum string

// Condition reasons for AIMService
const (
	// Model Resolution
	AIMServiceReasonInvalidImageReference = "InvalidImageReference"
	AIMServiceReasonModelNotFound         = "ModelNotFound"
	AIMServiceReasonCreatingModel         = "CreatingModel"
	AIMServiceReasonModelNotReady         = "ModelNotReady"
	AIMServiceReasonModelResolved         = "ModelResolved"

	// Template Resolution
	AIMServiceReasonTemplateNotFound           = "TemplateNotFound"
	AIMServiceReasonTemplateNotReady           = "TemplateNotReady"
	AIMServiceReasonResolved                   = "Resolved"
	AIMServiceReasonTemplateSelectionAmbiguous = "TemplateSelectionAmbiguous"

	// Storage
	AIMServiceReasonPVCNotBound      = "PVCNotBound"
	AIMServiceReasonStorageReady     = "StorageReady"
	AIMServiceReasonStorageSizeError = "StorageSizeError"

	// Cache
	AIMServiceReasonCacheCreating = "CacheCreating"
	AIMServiceReasonCacheNotReady = "CacheNotReady"
	AIMServiceReasonCacheReady    = "CacheReady"
	AIMServiceReasonCacheFailed   = "CacheFailed"
	AIMServiceReasonCacheLost     = "CacheLost"

	// Runtime
	AIMServiceReasonCreatingRuntime = "CreatingRuntime"
	AIMServiceReasonRuntimeReady    = "RuntimeReady"
	AIMServiceReasonRuntimeScaling  = "RuntimeScaling"
	// AIMServiceReasonScaledToZero indicates the deployment is idled to zero
	// replicas by KEDA under scale-to-zero. Healthy state, not a failure.
	AIMServiceReasonScaledToZero = "ScaledToZero"
	// AIMServiceReasonRoutingRequired indicates an invalid scale-to-zero
	// configuration: minReplicas=0 with routing disabled. The 0->1 activation
	// trigger queries gateway-side Envoy metrics that only exist once an
	// HTTPRoute is wired up, so without routing the service idles to zero and
	// can never wake. Drives ConfigValid=False.
	AIMServiceReasonRoutingRequired = "RoutingRequiredForScaleToZero"
	// AIMServiceReasonAutoscalingRequiresMetrics indicates autoscaling was
	// configured (minReplicas/maxReplicas/autoScaling) but no scaling trigger
	// resolves: the controller stamps autoscalerClass=external yet KEDA only
	// manages replicas when a ScaledObject with at least one trigger exists, so
	// the declared bounds are never enforced. Drives ConfigValid=False.
	AIMServiceReasonAutoscalingRequiresMetrics = "AutoscalingRequiresMetrics"

	// Routing
	AIMServiceReasonPathTemplateInvalid = "PathTemplateInvalid"

	// Profile Resolution (v1alpha2)
	AIMServiceReasonProfileNotFound          = "ProfileNotFound"
	AIMServiceReasonProfileNotReady          = "ProfileNotReady"
	AIMServiceReasonProfileResolved          = "ProfileResolved"
	AIMServiceReasonBaseProfile              = "BaseProfile"
	AIMServiceReasonProfileSelectorAmbiguous = "ProfileSelectorAmbiguous"
	// AIMServiceReasonProfileRebound is emitted (Normal severity) when the
	// resolver picks a different profile than the one currently recorded
	// in status.resolvedProfile. Carries the previous and new profile
	// names plus the trigger reason in the event message.
	AIMServiceReasonProfileRebound = "ProfileRebound"
	// AIMServiceReasonProfileBindingStuck is emitted (Normal severity) at
	// most once per binding when the resolver honours the existing
	// status.resolvedProfile despite the candidate set having changed.
	// Surfaces the "sticky binding" behavior so operators understand why
	// a newly-added higher-ranked profile is not being adopted.
	AIMServiceReasonProfileBindingStuck = "ProfileBindingStuck"
)

// AIMService manages a KServe-based AIM inference service for the selected model and template.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:deprecatedversion:warning="aim.eai.amd.com/v1alpha1 AIMService is deprecated; use aim.eai.amd.com/v1alpha2 (spec.profile) instead. The v1alpha1 model/template fields may be removed in a future release."
// +kubebuilder:resource:shortName=aimsvc,categories=aim;all
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.status`
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=`.status.resolvedModel.name`
// +kubebuilder:printcolumn:name="Template",type=string,JSONPath=`.status.resolvedTemplate.name`
// +kubebuilder:printcolumn:name="Profile",type=string,JSONPath=`.status.resolvedProfile.name`,priority=1
// +kubebuilder:printcolumn:name="Replicas",type=string,JSONPath=`.status.runtime.replicas`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:validation:XValidation:rule="!has(self.spec.profileOverrides) || has(self.spec.profile)",message="spec.profileOverrides requires spec.profile to be set"
// +kubebuilder:validation:XValidation:rule="!(has(self.spec.profile) && has(self.spec.template))",message="spec.profile and spec.template are mutually exclusive"
// +kubebuilder:validation:XValidation:rule="has(self.spec.model) || has(self.spec.profile)",message="one of spec.model or spec.profile must be specified"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.profileOverrides) || !has(self.spec.profileOverrides.acceleratorPartitioningMode) || size(self.spec.profileOverrides.acceleratorPartitioningMode) == 0 || has(self.spec.profileOverrides.acceleratorCount)",message="acceleratorCount must be specified together with any acceleratorPartitioningMode override; partition mode changes the per-unit interpretation of acceleratorCount"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.adapters) || self.spec.adapters.all(a, self.spec.adapters.exists_one(b, b.kind == a.kind && b.name == a.name))",message="spec.adapters entries must have unique (kind, name) pairs"
// +kubebuilder:validation:XValidation:rule="self.spec.adapterMode == 'dynamic' || (has(self.spec.adapters) == has(oldSelf.spec.adapters) && (!has(self.spec.adapters) || self.spec.adapters == oldSelf.spec.adapters))",message="spec.adapters is immutable unless spec.adapterMode is dynamic"
// Note: KServe uses {name}-{namespace} format which must not exceed 63 characters.
// This constraint is validated at runtime since CEL cannot access metadata.namespace.
//
//nolint:lll // kubebuilder marker; CEL rule cannot be wrapped across lines
type AIMService struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AIMServiceSpec   `json:"spec,omitempty"`
	Status AIMServiceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// AIMServiceList contains a list of AIMService.
type AIMServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AIMService `json:"items"`
}

// AIMServiceRoutingStatus captures observed routing details.
type AIMServiceRoutingStatus struct {
	// Path is the HTTP path prefix used when routing is enabled.
	// Example: `/tenant/svc-uuid`
	// +optional
	Path string `json:"path,omitempty"`
}

// GetStatus returns a pointer to the AIMService status.
func (svc *AIMService) GetStatus() *AIMServiceStatus {
	return &svc.Status
}

// GetCachingMode returns the effective canonical caching mode for this service.
// Legacy values are normalized for backward compatibility.
func (spec *AIMServiceSpec) GetCachingMode() AIMCachingMode {
	if spec.Caching == nil || spec.Caching.Mode == "" {
		return CachingModeShared
	}

	switch spec.Caching.Mode {
	case CachingModeDedicated, CachingModeNever:
		return CachingModeDedicated
	case CachingModeShared, CachingModeAlways, CachingModeAuto:
		return CachingModeShared
	default:
		// Defensive default for unknown values.
		return CachingModeShared
	}
}

func init() {
	SchemeBuilder.Register(&AIMService{}, &AIMServiceList{})
}
