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

// Types in this file are shared between v1alpha1 and v1alpha2 CRDs. They are
// hosted in v1alpha1 because v1alpha1 is the older version and v1alpha2 is
// allowed to import v1alpha1 (but not vice versa). This mirrors the
// AIMService pattern where v1alpha2.AIMService reuses v1alpha1.AIMServiceSpec.

const (
	// ProfileSetAimIdIndexKey is the field index key for AIMProfileSet/AIMClusterProfileSet selector aimId.
	ProfileSetAimIdIndexKey = ".spec.selector.aimId"

	// ProfileSetSourceRefIndexKey is the field index key for AIMProfileSet/AIMClusterProfileSet sourceRef.
	ProfileSetSourceRefIndexKey = ".spec.sourceRef"
)

// AcceleratorType distinguishes CPU from GPU accelerators.
// Used by AIM Engine to determine the resource derivation strategy
// (e.g., gpu → a vendor-specific extended resource, cpu → cpu).
// +kubebuilder:validation:Enum=gpu;cpu
type AcceleratorType string

const (
	AcceleratorTypeCPU AcceleratorType = "cpu"
	AcceleratorTypeGPU AcceleratorType = "gpu"
)

// AcceleratorVendor identifies the vendor-specific device-plugin contract for
// an accelerator. It is intentionally independent of AcceleratorModel so a
// generic profile can target any GPU from one vendor without enumerating every
// possible card model.
// +kubebuilder:validation:Enum=amd;nvidia
type AcceleratorVendor string

const (
	AcceleratorVendorAMD    AcceleratorVendor = "amd"
	AcceleratorVendorNVIDIA AcceleratorVendor = "nvidia"
)

// ProfileVersionPolicy controls which matched profile versions a derivation request may copy.
// +kubebuilder:validation:Enum=pinned;latest;all
type ProfileVersionPolicy string

const (
	// ProfileVersionPolicyPinned matches the requested source version; requires version.
	ProfileVersionPolicyPinned ProfileVersionPolicy = "pinned"
	// ProfileVersionPolicyLatest selects the highest matching source version.
	ProfileVersionPolicyLatest ProfileVersionPolicy = "latest"
	// ProfileVersionPolicyAll selects all matching profiles, including versionless sources.
	ProfileVersionPolicyAll ProfileVersionPolicy = "all"
)

// ProfileSourceRef identifies an alternate catalog ConfigMap for derivation.
// It replaces default selection across model definitions and copyable profiles.
type ProfileSourceRef struct {
	// Name is the discovery cache ConfigMap name.
	Name string `json:"name"`
}

// ProfileSelectorScope determines how a ProfileSelector.ModelRef resolves the
// source AIMModel scope. The v1alpha2 AIMProfileSet reconciler honours this
// value when filtering source profiles by `aim.eai.amd.com/source-model[-scope]`
// labels.
// +kubebuilder:validation:Enum=Auto;Namespace;Cluster
type ProfileSelectorScope string

const (
	// ProfileSelectorScopeAuto selects fully matching namespace candidates
	// first, then falls back to cluster candidates with the same model name
	// when no local match exists. Cluster sets select cluster sources only.
	ProfileSelectorScopeAuto ProfileSelectorScope = "Auto"
	// ProfileSelectorScopeNamespace requires the source profile to come from
	// an AIMModel in the same namespace as the selecting AIMProfileSet.
	ProfileSelectorScopeNamespace ProfileSelectorScope = "Namespace"
	// ProfileSelectorScopeCluster requires the source profile to come from a
	// cluster-scoped AIMClusterModel.
	ProfileSelectorScopeCluster ProfileSelectorScope = "Cluster"
)

// ProfileSelectorModelRef narrows derivation candidates by their owning
// AIMModel / AIMClusterModel. Definitions carry model ownership before
// materialisation; profile resources expose that ownership through the
// `aim.eai.amd.com/source-model[-scope]` labels.
type ProfileSelectorModelRef struct {
	// Name is the owning AIM(Cluster)Model name. Required.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Scope controls how Name is resolved against AIMModel vs AIMClusterModel.
	// +optional
	// +kubebuilder:default=Auto
	Scope ProfileSelectorScope `json:"scope,omitempty"`
}

// ProfileSelectorRole filters source profiles by their role label
// (`aim.eai.amd.com/profile-role`). Discovery of a deployable AIM image
// stamps the `deployable` role; base-image discovery stamps `base` on
// profiles that carry no aimId/modelSources, which custom-model AIMModels
// derive into deployable copies.
// +kubebuilder:validation:Enum=base;deployable
type ProfileSelectorRole string

const (
	// ProfileSelectorRoleBase filters down to base profiles that are not yet
	// deployable (no aimId / modelSources, only image + base-image so they can
	// serve as derivation source material for custom-model AIMModels).
	ProfileSelectorRoleBase ProfileSelectorRole = "base"
	// ProfileSelectorRoleDeployable filters down to fully deployable profiles
	// (with aimId and modelSources). This is the default.
	ProfileSelectorRoleDeployable ProfileSelectorRole = "deployable"
)

// ProfileOrigin classifies how an AIMProfile / AIMClusterProfile was produced.
// Stamped by AIMProfile reconcilers via the `aim.eai.amd.com/profile-origin`
// label and the AIMProfile `status.origin` field. Iteration-1 producers
// stamp `discovered` (image discovery) and `derived` (AIMProfileSet /
// AIMModel.spec.profiles.derivedFrom); user-authored profiles are backfilled
// to `user-authored` by the AIMProfile reconciler when no AIM controller
// owns them.
// +kubebuilder:validation:Enum=discovered;derived;generated;user-authored
type ProfileOrigin string

const (
	// ProfileOriginDiscovered indicates the profile was produced by image
	// discovery (today: AIMModel.spec.image native discovery path).
	ProfileOriginDiscovered ProfileOrigin = "discovered"
	// ProfileOriginDerived indicates the profile was produced by a derivation
	// flow (AIMModel.spec.profiles.derivedFrom or an AIMProfileSet).
	ProfileOriginDerived ProfileOrigin = "derived"
	// ProfileOriginGenerated indicates the profile was generated from a
	// modelId declaration and a RuntimeConfig fallback.
	ProfileOriginGenerated ProfileOrigin = "generated"
	// ProfileOriginUserAuthored indicates the profile was created
	// independently by a user (no AIM controller owner reference).
	ProfileOriginUserAuthored ProfileOrigin = "user-authored"
)

// ProfileSelector filters by source identity, runtime, hardware, and provenance.
// Derivation evaluates published model definitions and copyable profile resources;
// AIMService evaluates materialised deployable profile resources.
// All supplied criteria must match.
type ProfileSelector struct {
	// AimId filters by model architecture identifier (e.g., "qwen/qwen3-32b").
	// +optional
	AimId string `json:"aimId,omitempty"`

	// ModelId filters by the source profile's specific model identifier.
	// +optional
	ModelId string `json:"modelId,omitempty"`

	// ProfileId filters by the source profile's profile identifier.
	// +optional
	ProfileId string `json:"profileId,omitempty"`

	// Engine filters by inference engine.
	// +optional
	Engine string `json:"engine,omitempty"`

	// Metric filters by optimization target.
	// +optional
	Metric AIMMetric `json:"metric,omitempty"`

	// Precision filters by numeric precision.
	// +optional
	Precision AIMPrecision `json:"precision,omitempty"`

	// Type filters by optimization level (exact match).
	// +optional
	Type AIMProfileType `json:"type,omitempty"`

	// MinimumType filters by a minimum optimization level: candidates whose
	// type is this tier OR BETTER are accepted (hierarchy: optimized > general
	// > preview > unoptimized). This is the floor counterpart to the exact-match
	// Type field; the two AND together when both are set.
	//
	// The sentinel "any" disables the floor (accept every tier). When this
	// field is empty the AIMService resolver applies a default floor of
	// "optimized" so auto-selection prefers production-grade profiles and never
	// silently picks an unoptimized one; to opt a service into lower tiers
	// (e.g. CPU/EPYC profiles published as unoptimized) set minimumType
	// explicitly to "unoptimized" or "any". Derivation selectors
	// (AIMProfileSet / AIMModel.profiles) treat empty as "any" so copying is
	// never tier-restricted by default.
	// +optional
	MinimumType AIMProfileTypeFloor `json:"minimumType,omitempty"`

	// AcceleratorModel filters by accelerator identifier.
	// +optional
	AcceleratorModel string `json:"acceleratorModel,omitempty"`

	// AcceleratorVendor filters by the vendor-specific device-plugin contract.
	// +optional
	AcceleratorVendor AcceleratorVendor `json:"acceleratorVendor,omitempty"`

	// AcceleratorPartitioningMode filters candidates by their declared
	// partitioning mode. Partial-order match (NOT strict equality):
	//   ""              - no filter on this field.
	//   "unpartitioned" - matches profiles with mode "" or "unpartitioned".
	//   "partitioned"   - matches profiles whose mode is non-trivial (anything
	//                     other than "" / "unpartitioned").
	//   "<C>"           - selector-only convenience: matches profiles with mode
	//                     "<C>-*" (prefix on the scheme). Not a valid profile-spec
	//                     value (e.g. selector "CPX" matches "CPX-NPS1", "CPX-NPS4").
	//   "<C>-<M>"       - exact-string match on the scheme.
	// +optional
	AcceleratorPartitioningMode string `json:"acceleratorPartitioningMode,omitempty"`

	// AcceleratorType filters by accelerator resource type.
	// +optional
	AcceleratorType AcceleratorType `json:"acceleratorType,omitempty"`

	// AcceleratorCount filters by accelerator unit count.
	// +optional
	AcceleratorCount *int32 `json:"acceleratorCount,omitempty"`

	// EngineArgs partially matches source engineArgs: every provided top-level key must
	// exist in the source object with an equal value.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Type=object
	// +optional
	EngineArgs *apiextensionsv1.JSON `json:"engineArgs,omitempty"`

	// ModelRef optionally narrows candidates to definitions and profiles
	// published by a specific AIM(Cluster)Model. Omit to select across models.
	// +optional
	ModelRef *ProfileSelectorModelRef `json:"modelRef,omitempty"`

	// Role filters by the `aim.eai.amd.com/profile-role` label. Defaults to
	// `deployable`. `base` filters to base profiles emitted by base-image
	// discovery (custom-model derivation source material).
	// +optional
	// +kubebuilder:default=deployable
	Role ProfileSelectorRole `json:"role,omitempty"`

	// Origin filters source provenance across catalog and resource
	// representations.
	// Empty means no origin filter.
	// +optional
	Origin ProfileOrigin `json:"origin,omitempty"`
}

// ProfileOverrides mutates selected source profiles when creating derived copies.
//
// Identity fields (`aimId`, `modelId`, `profileId`) and behavioural fields
// (`image`, `features`, `acceleratorModel`, `acceleratorCount`, env, args, modelSources)
// always WRITE onto the derived profile. They are the "stamp on the output"
// half of the derivation contract — the `selector` half FILTERS source
// candidates and never mutates anything. Keeping these halves separated is
// what lets a single YAML mean exactly one thing.
//
// For `selector.role=base` derivations, `overrides.aimId` and
// `overrides.modelId` are REQUIRED (enforced by CEL on the enclosing spec):
// base profiles ship with empty identity fields by design, so the derived
// profile would otherwise have no identity to bind against.
type ProfileOverrides struct {
	// AimId stamps the derived profile's spec.aimId. When set, wins over
	// the source profile's aimId. REQUIRED when the enclosing selector has
	// role=base (the source base profile carries no aimId of its own).
	// +optional
	AimId string `json:"aimId,omitempty"`

	// ModelId stamps the derived profile's spec.modelId. When set, wins
	// over both the source profile's modelId AND the auto-derivation from
	// modelSources[0].modelId. REQUIRED when the enclosing selector has
	// role=base.
	// +optional
	ModelId string `json:"modelId,omitempty"`

	// ProfileId stamps the derived profile's spec.profileId. Optional —
	// most callers leave this empty and let the source profile's profileId
	// carry through (or the reconciler synthesise one).
	// +optional
	ProfileId string `json:"profileId,omitempty"`

	// ModelSources replaces the copied profile's modelSources. When
	// modelSources[0].modelId is set and overrides.modelId is unset, the
	// derived profile's modelId is auto-derived from modelSources[0]; an
	// explicit overrides.modelId always wins.
	// +optional
	ModelSources []AIMModelSource `json:"modelSources,omitempty"`

	// Features adds optional runtime capabilities to the copied profile. Values
	// are unioned with the source profile's features; an override cannot remove
	// a capability advertised by the source.
	// +optional
	// +listType=set
	Features []string `json:"features,omitempty"`

	// Image overrides the runtime container image. With no explicit image
	// override, replacing modelSources on a deployable source resolves its
	// base runtime; hardware-only and environment-only overrides retain the
	// source image. This takes precedence over a profile set's top-level image.
	// +optional
	Image string `json:"image,omitempty"`

	// AcceleratorModel replaces the copied profile's acceleratorModel.
	// +optional
	AcceleratorModel string `json:"acceleratorModel,omitempty"`

	// AcceleratorVendor replaces the copied profile's acceleratorVendor.
	// +optional
	AcceleratorVendor AcceleratorVendor `json:"acceleratorVendor,omitempty"`

	// AcceleratorCount replaces the copied profile's acceleratorCount.
	// +optional
	AcceleratorCount *int32 `json:"acceleratorCount,omitempty"`

	// AcceleratorPartitioningMode replaces the source partitioning mode.
	// An empty override leaves the source mode unchanged. Uses the same values
	// as AIMProfileSpecCommon.acceleratorPartitioningMode. When set,
	// profile-set and model derivation require explicit source
	// selector.acceleratorCount and target overrides.acceleratorCount.
	// Tensor-parallel size remains an independent engine argument.
	// +optional
	AcceleratorPartitioningMode string `json:"acceleratorPartitioningMode,omitempty"`

	// ContainerEnv merges by env var name, overriding matching source entries.
	// +optional
	// +listType=map
	// +listMapKey=name
	ContainerEnv []corev1.EnvVar `json:"containerEnv,omitempty"`

	// EngineEnv merges by key, overriding matching source entries.
	// +optional
	EngineEnv map[string]string `json:"engineEnv,omitempty"`

	// EngineArgs shallow-merges on top of the source engineArgs, overriding matching keys.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Type=object
	// +optional
	EngineArgs *apiextensionsv1.JSON `json:"engineArgs,omitempty"`
}

// ProfileOutputPolicy controls whether profiles produced by a derivation may
// be used as sources for a later derivation.
type ProfileOutputPolicy struct {
	// Copyable publishes derived profiles as eligible sources for subsequent
	// profile derivations. It is disabled by default so each derivation boundary
	// remains explicit. Disabling copyability, deleting the producer, or changing
	// its selection can remove downstream derived profiles immediately.
	// +optional
	Copyable bool `json:"copyable,omitempty"`
}

// AIMProfileSetSpec defines the desired state of AIMProfileSet and is also reused by AIMModel.profileCopy.
//
// The selector-non-empty rule accepts every documented narrowing field
// (including modelRef and origin) and lets sourceRef-only specs through:
// when sourceRef is set, the discovery cache itself acts as the source
// scope and a separate selector is not required.
//
// Default selection combines image-backed model definitions with existing
// copyable profile resources. A model definition and its materialised profile
// represent one logical source. Namespace sets use fully matching local
// candidates, falling back to cluster sources only when no local match exists;
// cluster sets use cluster sources only.
//
// Partitioning overrides require both selector.acceleratorCount and
// overrides.acceleratorCount.
//
// +kubebuilder:validation:XValidation:rule="has(self.sourceRef) || has(self.selector.aimId) || has(self.selector.modelId) || has(self.selector.profileId) || has(self.selector.engine) || has(self.selector.metric) || has(self.selector.precision) || has(self.selector.type) || has(self.selector.minimumType) || has(self.selector.acceleratorModel) || has(self.selector.acceleratorVendor) || has(self.selector.acceleratorPartitioningMode) || has(self.selector.acceleratorType) || has(self.selector.acceleratorCount) || has(self.selector.engineArgs) || has(self.selector.modelRef) || has(self.selector.origin)",message="selector must set at least one matching field, or set sourceRef"
// +kubebuilder:validation:XValidation:rule="!has(self.sourceRef) || !has(self.selector) || !has(self.selector.modelRef)",message="sourceRef and selector.modelRef are mutually exclusive"
// +kubebuilder:validation:XValidation:rule="has(self.version) || (has(self.versionPolicy) && self.versionPolicy != 'pinned')",message="version is required when versionPolicy is pinned"
// +kubebuilder:validation:XValidation:rule="!has(self.versionPolicy) || (self.versionPolicy != 'latest' && self.versionPolicy != 'all') || !has(self.version)",message="latest/all versionPolicy must not set version"
// +kubebuilder:validation:XValidation:rule="!has(self.selector) || !(has(self.selector.role) && self.selector.role == 'base') || (!has(self.selector.aimId) && !has(self.selector.modelId) && !has(self.selector.profileId))",message="selector.aimId/modelId/profileId are not allowed when selector.role=base; set overrides.aimId/modelId/profileId instead (base profiles have no source identity to filter on)"
// +kubebuilder:validation:XValidation:rule="!has(self.selector) || !(has(self.selector.role) && self.selector.role == 'base') || (has(self.overrides) && has(self.overrides.aimId) && has(self.overrides.modelId))",message="selector.role=base requires overrides.aimId and overrides.modelId (base profiles carry no identity of their own; overrides supply it)"
// +kubebuilder:validation:XValidation:rule="!has(self.overrides) || !has(self.overrides.acceleratorPartitioningMode) || size(self.overrides.acceleratorPartitioningMode) == 0 || has(self.overrides.acceleratorCount)",message="overrides.acceleratorCount must be specified together with any overrides.acceleratorPartitioningMode; partition mode changes the per-unit interpretation of acceleratorCount"
//
//nolint:lll // kubebuilder marker; CEL rule cannot be wrapped across lines
type AIMProfileSetSpec struct {
	// SourceRef points to an alternate discovery cache source.
	// When omitted, derivation uses published image-model definitions and visible
	// copyable AIMProfile and AIMClusterProfile objects, with namespace-first fallback.
	// For AIMProfileSet, the referenced ConfigMap is read from the same namespace.
	// For AIMClusterProfileSet, it is read from the operator namespace.
	// Mutually exclusive with selector.modelRef; other selector fields may
	// filter the catalog contents.
	// +optional
	SourceRef *ProfileSourceRef `json:"sourceRef,omitempty"`

	// Selector chooses which source profiles to derive from.
	// +optional
	Selector ProfileSelector `json:"selector,omitempty"`

	// VersionPolicy controls how matching profiles are filtered by version.
	// +optional
	// +kubebuilder:default=pinned
	VersionPolicy ProfileVersionPolicy `json:"versionPolicy,omitempty"`

	// Version pins matching to a specific source profile version when VersionPolicy is pinned.
	// +optional
	Version string `json:"version,omitempty"`

	// Image overrides the runtime image used by the derived profiles.
	// +optional
	Image string `json:"image,omitempty"`

	// Overrides mutates the copied profile spec after selection and version filtering.
	// +optional
	Overrides *ProfileOverrides `json:"overrides,omitempty"`

	// OutputPolicy controls whether profiles produced by this set may be used
	// as sources for subsequent derivations.
	// +optional
	OutputPolicy *ProfileOutputPolicy `json:"outputPolicy,omitempty"`

	// ImagePullSecrets lists secrets used for inspecting and pulling container images.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// ServiceAccountName is propagated to managed profiles for downstream workloads.
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`
}

// ManagedProfileCounts summarizes managed derivative profiles. The count
// fields intentionally omit `omitempty` so that a zero count serializes as
// an explicit `0` rather than dropping the field. This keeps the status
// shape stable for both kubectl printcolumns and chainsaw assertions —
// callers can rely on `.status.managedProfiles.{total,ready,...}` always
// being present once the controller has observed the resource.
type ManagedProfileCounts struct {
	// Total is the number of derivative profiles the controller currently manages or intends to manage.
	Total int32 `json:"total"`

	// Ready is the number of derivative profiles whose own status is Ready.
	Ready int32 `json:"ready"`

	// NotAvailable is the number of derivative profiles whose own status is NotAvailable.
	NotAvailable int32 `json:"notAvailable"`

	// Deployable is the number of managed profiles whose spec is materialised
	// enough to back an AIMService (carries aimId and modelSources). For a
	// normal officially-discovered AIMModel this equals Total. For a
	// base-image AIMModel used as a source for custom-model derivation
	// this is 0 — the model only emits base profiles that callers must
	// derive into deployable profiles.
	Deployable int32 `json:"deployable"`

	// Base counts managed base profile resources with empty aimId and
	// modelSources. They supply custom-model derivation with selector.role=base.
	// Catalog base definitions remain selectable when this materialised count is zero.
	Base int32 `json:"base"`
}

// DiscoveryCacheReference identifies the cached discovery catalog produced from image inspection.
type DiscoveryCacheReference struct {
	// Name is the ConfigMap name.
	Name string `json:"name,omitempty"`

	// Namespace is the ConfigMap namespace.
	Namespace string `json:"namespace,omitempty"`
}

// DiscoveredProfileCounts summarizes the profiles found during image discovery.
type DiscoveredProfileCounts struct {
	// Total is the number of profiles discovered from the image.
	Total int32 `json:"total,omitempty"`

	// Supported is the number of discovered profiles currently supported by the cluster.
	Supported int32 `json:"supported,omitempty"`

	// Unsupported is the number of discovered profiles currently not supported by the cluster.
	Unsupported int32 `json:"unsupported,omitempty"`

	// ByHardware groups discovered profiles by their hardware footprint and
	// reports whether each group is currently supported by the cluster. The
	// groups are stable across reconciles (sorted by acceleratorType,
	// acceleratorModel, acceleratorCount) so kubectl/jq queries are cheap.
	// Useful when debugging "why doesn't the {metric, precision} profile I
	// expect appear in my cluster?" — the breakdown surfaces every shape the
	// image emits, not only the ones materialised as AIMProfile objects.
	// +optional
	// +listType=atomic
	ByHardware []ProfileHardwareGroup `json:"byHardware,omitempty"`
}

// ProfileHardwareGroup is one accelerator footprint within a model's
// discovery catalog, with the metric/precision combos shipped under it.
type ProfileHardwareGroup struct {
	// AcceleratorType is the resource family (gpu, cpu).
	// +optional
	AcceleratorType AcceleratorType `json:"acceleratorType,omitempty"`

	// AcceleratorModel is the accelerator identifier (e.g., "MI300X",
	// "EPYC_ZEN5"). Empty for profiles with no accelerator requirement.
	// +optional
	AcceleratorModel string `json:"acceleratorModel,omitempty"`

	// AcceleratorCount is the number of accelerator units the profile
	// requests. For gpu: device count (e.g., 1, 2, 4, 8 for tensor-parallel
	// sizes). For cpu: number of CPU cores (e.g., 128 for EPYC_ZEN5,
	// 192 for EPYC_9965).
	// +optional
	AcceleratorCount int32 `json:"acceleratorCount,omitempty"`

	// Supported reports whether this hardware footprint is currently
	// satisfied by at least one cluster node. When false, all profiles in
	// this group are skipped during materialisation.
	Supported bool `json:"supported"`

	// Profiles lists the {metric, precision} combinations discovered under
	// this hardware footprint. Reported even when the group is unsupported
	// so users can see what they're missing.
	// +optional
	// +listType=atomic
	Profiles []ProfileHardwareGroupEntry `json:"profiles,omitempty"`
}

// ProfileHardwareGroupEntry identifies one profile within a hardware group
// by its {metric, precision} pair. Sufficient for users to spot whether the
// optimization variant they want is shipped at all.
type ProfileHardwareGroupEntry struct {
	// Metric is the optimization target (latency, throughput).
	// +optional
	Metric AIMMetric `json:"metric,omitempty"`

	// Precision is the numeric precision (fp4, fp8, bf16, …).
	// +optional
	Precision AIMPrecision `json:"precision,omitempty"`
}

// ProfileSetReference identifies the profile set synthesized by a model for derivation flows.
type ProfileSetReference struct {
	// Name is the profile set name.
	Name string `json:"name,omitempty"`

	// Namespace is the profile set namespace.
	Namespace string `json:"namespace,omitempty"`
}

// AIMProfileSetStatus defines the observed state of AIMProfileSet / AIMClusterProfileSet.
type AIMProfileSetStatus struct {
	// ObservedGeneration is the most recent generation observed by the controller.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Status represents the overall high-level status for the profile set.
	// +kubebuilder:default=Pending
	// +kubebuilder:validation:Enum=Pending;Progressing;Ready;Degraded;Failed;NotAvailable
	Status constants.AIMStatus `json:"status,omitempty"`

	// Conditions represent the latest available observations of the profile set's state.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ManagedProfiles summarizes the direct derivative profiles owned or managed by this profile set.
	ManagedProfiles ManagedProfileCounts `json:"managedProfiles,omitempty"`
}

func (s *AIMProfileSetStatus) GetConditions() []metav1.Condition {
	return s.Conditions
}

func (s *AIMProfileSetStatus) SetConditions(conditions []metav1.Condition) {
	s.Conditions = conditions
}

func (s *AIMProfileSetStatus) SetObservedGeneration(generation int64) {
	s.ObservedGeneration = generation
}

func (s *AIMProfileSetStatus) SetStatus(status string) {
	s.Status = constants.AIMStatus(status)
}

func (s *AIMProfileSetStatus) GetAIMStatus() constants.AIMStatus {
	return s.Status
}

// ModelDiscoveryState tracks the Kubernetes Job that inspects an AIM image and
// writes its profile YAMLs into the discovery cache ConfigMap. Mirrors the
// v1alpha1 AIMServiceTemplate DiscoveryState but scoped to AIMModel semantics.
type ModelDiscoveryState struct {
	// Attempts is the number of discovery job attempts that have been made.
	// Increments each time a new discovery job is created after a failure.
	// +optional
	Attempts int32 `json:"attempts,omitempty"`

	// LastAttemptTime is the timestamp of the most recent discovery job creation.
	// Used to calculate exponential backoff before the next retry.
	// +optional
	LastAttemptTime *metav1.Time `json:"lastAttemptTime,omitempty"`

	// LastFailureReason captures the reason for the most recent discovery failure.
	// +optional
	LastFailureReason string `json:"lastFailureReason,omitempty"`

	// SpecHash is a hash of the model spec fields that invalidate cached discovery
	// (image, imagePullSecrets, serviceAccountName, discoveryCommandVersion).
	// When it changes, the operator drops the cache and re-runs the discovery Job.
	// +optional
	SpecHash string `json:"specHash,omitempty"`
}
