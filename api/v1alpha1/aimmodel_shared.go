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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

const (
	// AIMModelConditionMetadataExtracted captures whether image metadata extraction succeeded.
	AIMModelConditionMetadataExtracted = "MetadataExtracted"

	// AIMModelReasonMetadataExtracted indicates metadata extraction succeeded.
	AIMModelReasonMetadataExtracted = "MetadataExtracted"

	// AIMModelReasonMetadataExtractionFailed indicates metadata extraction failed (non-blocking, prevents retries).
	AIMModelReasonMetadataExtractionFailed = "MetadataExtractionFailed"

	// Runtime config resolution reasons
	AIMModelReasonConfigNotFound     = "ConfigNotFound"
	AIMModelReasonRuntimeConfigError = "RuntimeConfigError"
	AIMModelReasonUsingDefaults      = "UsingDefaults"
	AIMModelReasonResolved           = "Resolved"

	// Template status reasons
	AIMModelReasonAllTemplatesFailed                    = "AllTemplatesFailed"
	AIMModelReasonNoTemplatesAvailable                  = "NoTemplatesAvailable"
	AIMModelReasonSomeTemplatesDegraded                 = "SomeTemplatesDegraded"
	AIMModelReasonTemplatesProgressing                  = "TemplatesProgressing"
	AIMModelReasonAllTemplatesReady                     = "AllTemplatesReady"
	AIMModelReasonSomeTemplatesReady                    = "SomeTemplatesReady"
	AIMModelReasonNoTemplatesExpected                   = "NoTemplatesExpected"
	AIMModelReasonAwaitingMetadata                      = "AwaitingMetadata"
	AIMModelReasonCreatingTemplates                     = "CreatingTemplates"
	AIMModelReasonMetadataMissingRecommendedDeployments = "MetadataMissingRecommendedDeployments"
)

// AIMModelSourceType indicates how a model's artifacts are sourced.
//
// Only set by the v1alpha1 controller, which lumps fine-tunes and custom
// models together as "Custom" — losing the distinction users actually
// care about. The v1alpha2 controller intentionally does not populate
// this field on v1alpha2-shaped specs; v1alpha2 consumers should read
// AIMModelStatus.Kind instead, which is a three-way classifier
// (Image / Derived / Custom).
//
// +kubebuilder:validation:Enum=Image;Custom
type AIMModelSourceType string

const (
	// AIMModelSourceTypeImage indicates the model is discovered from container image labels.
	AIMModelSourceTypeImage AIMModelSourceType = "Image"
	// AIMModelSourceTypeCustom indicates the model uses explicit spec.modelSources.
	AIMModelSourceTypeCustom AIMModelSourceType = "Custom"
)

// AIMModelKind classifies the v1alpha2 AIMModel onboarding flow that
// produced this model's profiles. Populated by the v1alpha2 controller
// during reconciliation from the model's spec shape.
//
// The three kinds correspond 1:1 to the "three flows" documented in
// concepts/models.md:
//
//   - Image    — spec.image is set; profiles come from in-cluster image
//     discovery on that image. Covers both AMD-published official AIMs
//     and any private image with profile YAMLs baked in (including
//     base images used as source material for Custom-kind models).
//   - Derived  — spec.profiles.derivedFrom with selector.role unset or
//     "deployable"; profiles are re-derived from another deployable
//     model's profiles (e.g. fine-tunes that swap weights but keep
//     the original model's architecture, runtime, and accelerator
//     shapes).
//   - Custom   — spec.profiles.derivedFrom with selector.role=base;
//     profiles are derived by overlaying BYO weights + target identity
//     onto a base image's generic base profiles.
//
// Empty when the spec hasn't been classified yet (controller hasn't
// reconciled) or when the spec shape doesn't match any of the three
// flows (a misconfigured spec the CEL validators didn't catch).
//
// +kubebuilder:validation:Enum=Image;Derived;Custom
type AIMModelKind string

const (
	AIMModelKindImage   AIMModelKind = "Image"
	AIMModelKindDerived AIMModelKind = "Derived"
	AIMModelKindCustom  AIMModelKind = "Custom"
)

// AIMCustomTemplate defines a custom template configuration for a model.
// When modelSources are specified directly on AIMModel, customTemplates allow
// defining explicit hardware requirements and profiles, skipping the discovery job.
// This is an existing struct (not a CRD); it appears as an element of AIMModel.spec.customTemplates[].
//
// +kubebuilder:validation:XValidation:rule="!has(self.customProfile) || (has(self.aimId) && has(self.modelId) && has(self.hardware) && has(self.profile) && has(self.profile.metric) && has(self.profile.precision))",message="when customProfile is set, aimId, modelId, hardware, profile.metric, and profile.precision are required"
type AIMCustomTemplate struct {
	// Name is the template name. If not provided, auto-generated from model name + profile.
	// +optional
	// +kubebuilder:validation:MaxLength=63
	Name string `json:"name,omitempty"`

	// Type indicates the optimization status of this template.
	// - optimized: Template has been tuned for performance
	// - general: General-purpose tuning between optimized and preview
	// - preview: Template is experimental/pre-release
	// - unoptimized: Default, no specific optimizations applied
	// +optional
	// +kubebuilder:validation:Enum=optimized;general;preview;unoptimized
	// +kubebuilder:default=unoptimized
	Type AIMProfileType `json:"type,omitempty"`

	// Env specifies environment variable overrides when this template is selected.
	// These are container-level env vars applied to the AIM runtime container.
	// +optional
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=64
	Env []corev1.EnvVar `json:"env,omitempty"`

	// Hardware specifies GPU and CPU requirements for this template.
	// Optional when spec.hardware is set (inherits from spec).
	// When both are set, values are merged field-by-field with template taking precedence.
	// +optional
	Hardware *AIMHardwareRequirements `json:"hardware,omitempty"`

	// Profile declares runtime profile variables for template selection.
	// Used when multiple templates exist to select based on metric/precision.
	// +optional
	Profile *AIMTemplateProfile `json:"profile,omitempty"`

	// AimId is the AIM product family identifier (e.g., "meta-llama/Llama-3-8B").
	// Required when customProfile is set.
	// +optional
	AimId string `json:"aimId,omitempty"`

	// ModelId is the specific model identifier / HuggingFace URI (e.g., "Qwen/Qwen3-32B-FP8").
	// Required when customProfile is set.
	// +optional
	ModelId string `json:"modelId,omitempty"`

	// CustomProfile defines inline custom profile data for the inference engine.
	// When set, the resulting template will have a custom profile ConfigMap mounted.
	// Requires aimId, modelId, hardware, profile.metric, and profile.precision.
	// +optional
	CustomProfile *AIMCustomProfile `json:"customProfile,omitempty"`
}

// AIMTemplateProfile declares profile variables for template selection.
// Used in AIMCustomTemplate to specify optimization targets.
type AIMTemplateProfile struct {
	// Metric specifies the optimization target (e.g., latency, throughput).
	// +optional
	// +kubebuilder:validation:Enum=latency;throughput
	Metric AIMMetric `json:"metric,omitempty"`

	// Precision specifies the numerical precision (e.g., fp8, fp16, bf16).
	// +optional
	// +kubebuilder:validation:Enum=auto;fp4;fp8;fp16;fp32;bf16;int4;int8
	Precision AIMPrecision `json:"precision,omitempty"`
}

// AIMVersionPolicy controls how template versions are filtered during aimId-based matching.
// +kubebuilder:validation:Enum=pinned;latest;any
type AIMVersionPolicy string

const (
	// AIMVersionPolicyPinned matches templates whose status.version equals the model's image tag.
	AIMVersionPolicyPinned AIMVersionPolicy = "pinned"
	// AIMVersionPolicyLatest matches only templates at the newest available status.version.
	AIMVersionPolicyLatest AIMVersionPolicy = "latest"
	// AIMVersionPolicyAny matches templates at any version.
	AIMVersionPolicyAny AIMVersionPolicy = "any"
)

// AIMCustomModelSpec contains configuration for custom models.
// These fields are only used when modelSources is specified (custom models).
// For image-based models, these settings come from discovery.
type AIMCustomModelSpec struct {
	// Hardware specifies default hardware requirements for all templates.
	// Individual templates can override these defaults.
	// Required when modelSources is set and customTemplates is empty (unless aimId is set).
	// +optional
	Hardware *AIMHardwareRequirements `json:"hardware,omitempty"`

	// Type specifies default type for all templates.
	// Individual templates can override this default.
	// When nil, templates default to "unoptimized".
	// +optional
	// +kubebuilder:validation:Enum=optimized;general;preview;unoptimized
	Type *AIMProfileType `json:"type,omitempty"`

	// VersionPolicy controls how template versions are filtered during aimId-based matching.
	// - pinned (default): match templates whose status.version equals the model's image tag
	// - latest: match only templates at the newest available status.version
	// - any: match templates at any version
	// Only used when spec.aimId is set.
	// +optional
	// +kubebuilder:default=pinned
	VersionPolicy AIMVersionPolicy `json:"versionPolicy,omitempty"`
}

// AIMModelSpec defines the desired state of AIMModel.
// +kubebuilder:validation:XValidation:rule="!has(self.modelSources) || size(self.modelSources) == 0 || has(self.aimId) || (has(self.custom) && has(self.custom.hardware)) || !has(self.customTemplates) || size(self.customTemplates) == 0 || self.customTemplates.all(t, has(t.hardware) || (has(self.custom) && has(self.custom.hardware)))",message="when using modelSources without aimId, set custom.hardware or set hardware on each customTemplate"
// +kubebuilder:validation:XValidation:rule="(has(self.image) && size(self.image) > 0) || has(self.profileCopy) || has(self.derivedFrom) || has(self.profiles) || (has(self.aimId) && has(self.custom) && has(self.custom.versionPolicy) && self.custom.versionPolicy != 'pinned')",message="image is required unless aimId is set with versionPolicy latest or any, or profileCopy/derivedFrom/profiles is set"
// +kubebuilder:validation:XValidation:rule="!has(self.profileCopy) || (!has(self.discovery) && !has(self.defaultServiceTemplate) && !has(self.custom) && (!has(self.customTemplates) || size(self.customTemplates) == 0) && (!has(self.modelSources) || size(self.modelSources) == 0) && (!has(self.runtimeConfigName) || size(self.runtimeConfigName) == 0) && (!has(self.env) || size(self.env) == 0) && !has(self.imageMetadata))",message="profileCopy cannot be combined with deprecated legacy AIMModel fields"
//
// Per-version constraints (v1alpha1 forbids derivedFrom and profiles;
// v1alpha2 forbids profileCopy/custom/customTemplates/top-level modelSources,
// requires image XOR profiles, and forbids profiles mixed with legacy scalar
// fields) live on the version-specific AIMModel / AIMClusterModel root types
// in the per-version *_types.go files.
//
//nolint:lll // kubebuilder marker; CEL rule cannot be wrapped across lines
type AIMModelSpec struct {
	// Image is the container image URI for this AIM model.
	// This image is inspected by the operator to select runtime profiles used by templates.
	// Discovery behavior is controlled by the discovery field and runtime config's AutoDiscovery setting.
	// Required unless aimId is set with versionPolicy latest or any, or profileCopy is set.
	// +optional
	Image string `json:"image,omitempty"`

	// AimId is the AIM product family identifier (e.g., "qwen/qwen3-32b").
	// When set together with modelSources, enables aimId-based template matching:
	// the controller finds official templates by aimId, filters by versionPolicy,
	// matches by modelId, and creates copies with the custom weight source.
	// +optional
	AimId string `json:"aimId,omitempty"`

	// ProfileCopy reuses the AIMProfileSet derivation shape so an AIMModel can
	// publish derivative AIMProfiles directly. The controller may synthesize a
	// child AIMProfileSet and fill SourceRef when image-backed discovery is
	// involved. Mutually exclusive with all deprecated v1alpha1 fields.
	//
	// DEPRECATED on v1alpha2: use spec.profiles. v1alpha1 still accepts
	// ProfileCopy. v1alpha2 CRD CEL forbids ProfileCopy.
	// +optional
	ProfileCopy *AIMProfileSetSpec `json:"profileCopy,omitempty"`

	// DerivedFrom is the legacy flat shape of v1alpha2's profile-derivation
	// onboarding surface. New objects must use spec.profiles instead; the
	// field is retained so existing v1alpha2 objects (and the v1alpha2
	// reconciler reading them) round-trip cleanly.
	//
	// DEPRECATED: prefer spec.profiles.derivedFrom on v1alpha2.
	// +optional
	DerivedFrom *AIMProfileSetSpec `json:"derivedFrom,omitempty"`

	// Profiles is the v1alpha2 fine-tune / custom-model onboarding surface.
	// It groups the source descriptor (`profiles.derivedFrom.selector` /
	// `profiles.derivedFrom.sourceRef`), the version filter
	// (`profiles.versionPolicy` / `profiles.version`), and the modifications
	// applied to copies (`profiles.overrides`). When set, the AIMModel
	// reconciler synthesises a child AIMProfileSet from this block.
	//
	// Mutually exclusive with spec.image (exactly one of the two is required
	// for v1alpha2 AIMModel). v1alpha1 rejects spec.profiles via per-version
	// CEL.
	// +optional
	Profiles *AIMModelProfilesSpec `json:"profiles,omitempty"`

	// Discovery controls discovery behavior for this model.
	// When unset, uses runtime config defaults.
	// +optional
	Discovery *AIMModelDiscoveryConfig `json:"discovery,omitempty"`

	// DefaultServiceTemplate specifies the default AIMServiceTemplate to use when creating services for this model.
	// When set, services that reference this model will use this template if no template is explicitly specified.
	// If this is not set, a template will be automatically selected.
	// +optional
	DefaultServiceTemplate string `json:"defaultServiceTemplate,omitempty"`

	// Custom contains configuration for custom models (models with inline modelSources).
	// Only used when modelSources are specified; ignored for image-based models.
	// +optional
	Custom *AIMCustomModelSpec `json:"custom,omitempty"`

	// CustomTemplates defines explicit template configurations for this model.
	// These templates are created directly without running a discovery job.
	// Can be used with or without modelSources to define custom deployment configurations.
	// If omitted when modelSources is set, a single template is auto-generated
	// using the custom.hardware requirements.
	// +optional
	// +kubebuilder:validation:MaxItems=16
	CustomTemplates []AIMCustomTemplate `json:"customTemplates,omitempty"`

	// ModelSources specifies the model sources to use for this model.
	// When specified, these sources are used instead of auto-discovery from the container image.
	// This enables pre-creating custom models with explicit model sources.
	// The size field is optional - if not specified, it will be discovered by the download job.
	// AIM runtime currently supports only one model source.
	// +optional
	// +kubebuilder:validation:MaxItems=1
	ModelSources []AIMModelSource `json:"modelSources,omitempty"`

	// RuntimeConfigRef contains the runtime config reference for this model, and is used to control discovery behavior.
	RuntimeConfigRef `json:",inline"`

	// ImagePullSecrets lists secrets containing credentials for pulling the model container image.
	// These secrets are used for:
	// - OCI registry metadata extraction during discovery
	// - Pulling the image for inference services
	// The secrets are merged with any runtime config defaults.
	// For namespace-scoped models, secrets must exist in the same namespace.
	// For cluster-scoped models, secrets must exist in the operator namespace.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// Env specifies environment variables for authentication during model discovery and metadata extraction.
	// These variables are used for authentication with model registries (e.g., HuggingFace tokens).
	// +optional
	// +listType=map
	// +listMapKey=name
	Env []corev1.EnvVar `json:"env,omitempty"`

	// ServiceAccountName specifies the Kubernetes service account to use for workloads related to this model.
	// This includes metadata extraction jobs and any other model-related operations.
	// If empty, the default service account for the namespace is used.
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// Resources defines the default resource requirements for services using this model.
	// Template- or service-level values override these defaults.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// ImageMetadata is the metadata that is used to determine which recommended service templates to create,
	// and to drive clients with richer metadata regarding this particular model. For most cases the user does
	// not need to set this field manually, for images that have the supported labels embedded in them
	// the `AIM(Cluster)Model.status.imageMetadata` field is automatically filled from the container image labels.
	// This field is intended to be used when there are network restrictions, or in other similar situations.
	// If this field is set, the remote extraction will not be performed at all.
	ImageMetadata *ImageMetadata `json:"imageMetadata,omitempty"`
}

// AIMModelProfilesSpec is the v1alpha2 AIMModel onboarding surface for
// profile-derivation flows. It groups the source descriptor, version filter,
// and overrides under one block so the spec reads "the model's profiles,
// derived from <source>, with <overrides> applied".
//
// The reconciler translates this block into a child AIMProfileSet:
//   - DerivedFrom.Selector / DerivedFrom.SourceRef → child AIMProfileSet
//     spec.selector / spec.sourceRef.
//   - VersionPolicy / Version → child spec.versionPolicy / spec.version.
//   - Overrides → child spec.overrides (Image included via overrides.image).
//
// +kubebuilder:validation:XValidation:rule="has(self.derivedFrom)",message="profiles.derivedFrom is required when spec.profiles is set"
// +kubebuilder:validation:XValidation:rule="has(self.version) || (has(self.versionPolicy) && self.versionPolicy != 'pinned')",message="profiles.version is required when versionPolicy is pinned"
// +kubebuilder:validation:XValidation:rule="!has(self.versionPolicy) || (self.versionPolicy != 'latest' && self.versionPolicy != 'all') || !has(self.version)",message="latest/all versionPolicy must not set version"
// +kubebuilder:validation:XValidation:rule="!(has(self.derivedFrom.selector.role) && self.derivedFrom.selector.role == 'base') || (!has(self.derivedFrom.selector.aimId) && !has(self.derivedFrom.selector.modelId) && !has(self.derivedFrom.selector.profileId))",message="derivedFrom.selector.aimId/modelId/profileId are not allowed when selector.role=base; set overrides.aimId/modelId/profileId instead (base profiles have no source identity to filter on)"
// +kubebuilder:validation:XValidation:rule="!(has(self.derivedFrom.selector.role) && self.derivedFrom.selector.role == 'base') || (has(self.overrides) && has(self.overrides.aimId) && has(self.overrides.modelId))",message="derivedFrom.selector.role=base requires overrides.aimId and overrides.modelId (base profiles carry no identity of their own; overrides supply it)"
//
//nolint:lll // kubebuilder marker; CEL rule cannot be wrapped across lines
type AIMModelProfilesSpec struct {
	// DerivedFrom identifies the source profiles to copy from.
	DerivedFrom *AIMModelProfilesDerivedFrom `json:"derivedFrom,omitempty"`

	// VersionPolicy controls how matching profiles are filtered by version.
	// +optional
	// +kubebuilder:default=pinned
	VersionPolicy ProfileVersionPolicy `json:"versionPolicy,omitempty"`

	// Version pins matching to a specific source profile version when
	// VersionPolicy is `pinned`.
	// +optional
	Version string `json:"version,omitempty"`

	// Overrides mutates the copied profile spec after selection and version
	// filtering. Use overrides.image to override the deployment container
	// image used by the derived profiles.
	// +optional
	Overrides *ProfileOverrides `json:"overrides,omitempty"`
}

// AIMModelProfilesDerivedFrom describes the source half of a profile-
// derivation request: which existing profiles (or discovery cache) the
// reconciler should copy from.
type AIMModelProfilesDerivedFrom struct {
	// Selector chooses which source profiles to derive from. Iteration-1
	// producers stamp role=deployable on every profile; selector.role=base
	// is reserved for the iteration-2 base-image producers (custom-model
	// derivation source material).
	// +optional
	Selector ProfileSelector `json:"selector,omitempty"`

	// SourceRef points to an alternate discovery cache source (a
	// pre-populated ConfigMap of profile YAMLs) instead of using the
	// visible AIMProfile / AIMClusterProfile objects.
	// +optional
	SourceRef *ProfileSourceRef `json:"sourceRef,omitempty"`
}

// AIMModelDiscoveryConfig controls discovery behavior for a model.
//
// The bool fields are pointers so the schema can distinguish "unset" from
// explicit false. With a plain bool + omitempty + default=true, the API
// server's OpenAPI defaulter cannot tell an explicit false apart from a
// missing field (both look like the Go zero value) and silently rewrites
// the explicit false to true. Pointers preserve the user's intent.
type AIMModelDiscoveryConfig struct {
	// ExtractMetadata controls whether metadata extraction runs for this model.
	// During metadata extraction, the controller connects to the image registry and
	// extracts the image's labels.
	// +optional
	// +kubebuilder:default=true
	ExtractMetadata *bool `json:"extractMetadata,omitempty"`

	// CreateServiceTemplates controls whether (cluster) service templates are auto-created from the image metadata.
	// +optional
	// +kubebuilder:default=true
	CreateServiceTemplates *bool `json:"createServiceTemplates,omitempty"`
}

// IsExtractMetadataEnabled reports whether metadata extraction is enabled.
// Treats unset (nil) as the schema default of true.
func (c *AIMModelDiscoveryConfig) IsExtractMetadataEnabled() bool {
	if c == nil || c.ExtractMetadata == nil {
		return true
	}
	return *c.ExtractMetadata
}

// IsCreateServiceTemplatesEnabled reports whether auto-creation of service
// templates from image metadata is enabled. Treats unset (nil) as the schema
// default of true.
func (c *AIMModelDiscoveryConfig) IsCreateServiceTemplatesEnabled() bool {
	if c == nil || c.CreateServiceTemplates == nil {
		return true
	}
	return *c.CreateServiceTemplates
}

// AIMModelStatus defines the observed state of AIMModel.
type AIMModelStatus struct {
	// ObservedGeneration is the most recent generation observed by the controller
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Status represents the overall status of the image based on its templates
	// +kubebuilder:default=Pending
	// +kubebuilder:validation:Enum=Pending;Progressing;Ready;Degraded;Failed;NotAvailable
	Status constants.AIMStatus `json:"status,omitempty"`

	// Conditions represent the latest available observations of the model's state
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ResolvedRuntimeConfig captures metadata about the runtime config that was resolved.
	// +optional
	ResolvedRuntimeConfig *AIMResolvedReference `json:"resolvedRuntimeConfig,omitempty"`

	// ImageMetadata is the metadata extracted from an AIM image
	// +optional
	ImageMetadata *ImageMetadata `json:"imageMetadata,omitempty"`

	// SourceType indicates how this model's artifacts are sourced.
	// - "Image": Model discovered from container image labels
	// - "Custom": Model uses explicit spec.modelSources
	// Set by the controller based on whether spec.modelSources is populated.
	//
	// Note: only populated by the v1alpha1 controller; v1alpha2 consumers
	// should read .status.kind instead, which distinguishes fine-tunes
	// (Derived) from BYO base-image overlays (Custom).
	// +optional
	SourceType AIMModelSourceType `json:"sourceType,omitempty"`

	// Kind classifies the v1alpha2 onboarding flow that produced this
	// model's profiles (Image / Derived / Custom). See AIMModelKind for
	// the per-value semantics. Populated by the v1alpha2 controller from
	// the spec shape; left empty by the v1alpha1 controller.
	// +optional
	Kind AIMModelKind `json:"kind,omitempty"`

	// AimId is the resolved model architecture identifier for this model.
	// Populated by the v1alpha2 controller from spec.aimId or discovered metadata.
	// +optional
	AimId string `json:"aimId,omitempty"`

	// BaseImage is the extracted AIM base image reference (AIM_BASE_IMAGE_REF) when known.
	// Used when resolving deployment images for fine-tuned models that have no spec.image.
	// +optional
	BaseImage string `json:"baseImage,omitempty"`

	// Version is the effective image version of the model. Populated from
	// the spec.image tag (`amdenterpriseai/aim-qwen-qwen3-32b:0.11.0` →
	// `0.11.0`) during reconciliation. Empty for models that have no
	// spec.image (e.g. fine-tuned models derived from a parent) or that
	// reference an image by digest.
	//
	// This mirrors AIMProfileStatus.Version so the two surfaces stay in
	// lock-step: a single image-tag extraction rule governs what users
	// see in the kubectl printcolumn for both kinds.
	//
	// The image-author-declared version (i.e. the
	// `org.opencontainers.image.version` OCI label) is preserved separately
	// under .status.imageMetadata.oci.version for users that want to inspect
	// what the image build pipeline stamped. The two values usually agree;
	// when the OCI label is missing or empty, this field still surfaces a
	// useful version from the tag itself.
	// +optional
	Version string `json:"version,omitempty"`

	// DiscoveryCacheRef points at the normalized discovery cache ConfigMap for image-backed flows.
	// Populated by the v1alpha2 controller after image inspection succeeds.
	// +optional
	DiscoveryCacheRef *DiscoveryCacheReference `json:"discoveryCacheRef,omitempty"`

	// DiscoveredProfiles summarizes the profiles found during image discovery.
	// +optional
	DiscoveredProfiles DiscoveredProfileCounts `json:"discoveredProfiles,omitempty"`

	// ProfileSetRef identifies the child profile set synthesized for derivation flows.
	// +optional
	ProfileSetRef *ProfileSetReference `json:"profileSetRef,omitempty"`

	// ManagedProfiles summarizes the direct promoted or derived profiles owned or managed by this model.
	// +optional
	ManagedProfiles ManagedProfileCounts `json:"managedProfiles,omitempty"`

	// Discovery tracks the state of the image-discovery Job used to populate the discovery cache.
	// +optional
	Discovery *ModelDiscoveryState `json:"discovery,omitempty"`
}

func (s *AIMModelStatus) GetConditions() []metav1.Condition {
	return s.Conditions
}

func (s *AIMModelStatus) SetConditions(conditions []metav1.Condition) {
	s.Conditions = conditions
}

func (s *AIMModelStatus) SetStatus(status string) {
	s.Status = constants.AIMStatus(status)
}

func (s *AIMModelStatus) GetAIMStatus() constants.AIMStatus {
	return s.Status
}

// GetEffectiveImageMetadata returns metadata from spec (if provided) or status (if extracted).
// Spec takes precedence over status since it represents user intent.
func (s *AIMModelSpec) GetEffectiveImageMetadata(status *AIMModelStatus) *ImageMetadata {
	if s.ImageMetadata != nil {
		return s.ImageMetadata
	}
	if status != nil {
		return status.ImageMetadata
	}
	return nil
}

// GetBaseImageRef returns the AIM_BASE_IMAGE_REF value extracted from this model's
// image (through spec-provided metadata first, falling back to status-extracted
// metadata). Returns empty string when the base image ref is not known.
func (s *AIMModelSpec) GetBaseImageRef(status *AIMModelStatus) string {
	md := s.GetEffectiveImageMetadata(status)
	if md == nil {
		return ""
	}
	return md.BaseImageRef
}

// HasLegacyFields reports whether any deprecated v1alpha1 compatibility fields are populated.
// Used by the v1alpha2 reconciler to choose between the legacy translator path and the
// native ProfileCopy path.
func (s *AIMModelSpec) HasLegacyFields() bool {
	if s == nil {
		return false
	}
	return s.Discovery != nil ||
		s.DefaultServiceTemplate != "" ||
		s.Custom != nil ||
		len(s.CustomTemplates) > 0 ||
		len(s.ModelSources) > 0 ||
		s.Name != "" ||
		len(s.Env) > 0 ||
		!resourceRequirementsEmpty(s.Resources) ||
		s.ImageMetadata != nil
}

func resourceRequirementsEmpty(resources corev1.ResourceRequirements) bool {
	return len(resources.Limits) == 0 &&
		len(resources.Requests) == 0 &&
		len(resources.Claims) == 0
}

// IsFineTunedModel returns true if the model uses aimId-based template matching.
// A fine-tuned model has spec.aimId set together with spec.modelSources.
func (s *AIMModelSpec) IsFineTunedModel() bool {
	return s.AimId != "" && len(s.ModelSources) > 0
}

// ShouldCreateTemplates returns whether template creation is enabled for this model.
// Returns true if discovery.createServiceTemplates is unset or true.
func (s *AIMModelSpec) ShouldCreateTemplates() bool {
	return s.Discovery.IsCreateServiceTemplatesEnabled()
}

// ExpectsTemplates returns whether this model should have auto-created templates.
// Returns:
//   - ptr to true: templates expected (has recommendedDeployments, creation enabled, customTemplates, or is custom model)
//   - ptr to false: no templates expected (no recommendedDeployments or creation disabled)
//   - nil: unknown (metadata not yet available for image-based models)
func (s *AIMModelSpec) ExpectsTemplates(status *AIMModelStatus) *bool {
	// Check if template creation is disabled
	if !s.ShouldCreateTemplates() {
		result := false
		return &result
	}

	// Fine-tuned models (aimId + modelSources) expect templates from matched official templates
	if s.IsFineTunedModel() {
		result := true
		return &result
	}

	// Custom models (with modelSources) always expect templates to be created
	// from customTemplates or auto-generated from hardware
	if len(s.ModelSources) > 0 {
		result := true
		return &result
	}

	// For image-based models, check metadata for recommended deployments
	// customTemplates are additive to discovered templates, so we still need metadata
	metadata := s.GetEffectiveImageMetadata(status)
	if metadata == nil {
		return nil // Unknown - still fetching
	}

	// Only GPU-shaped deployments materialise into AIMServiceTemplates in the
	// v1alpha1 path; CPU entries (RecommendedDeployment.IsGPUDeployment()==false)
	// are handled by the v1alpha2 native discovery pipeline as AIMProfiles.
	hasGPUDeployments := false
	if metadata.Model != nil {
		for i := range metadata.Model.RecommendedDeployments {
			if metadata.Model.RecommendedDeployments[i].IsGPUDeployment() {
				hasGPUDeployments = true
				break
			}
		}
	}
	hasCustomTemplates := len(s.CustomTemplates) > 0

	// Expect templates if we have discovered GPU deployments OR customTemplates
	result := hasGPUDeployments || hasCustomTemplates
	return &result
}
