// MIT License
//
// Copyright (c) 2026 Advanced Micro Devices, Inc.
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
)

// AIMCompositionMode identifies the serving topology described by a composition.
// +kubebuilder:validation:Enum=Aggregated;PrefillDecode
type AIMCompositionMode string

const (
	// AIMCompositionModeAggregated serves requests through one main role.
	AIMCompositionModeAggregated AIMCompositionMode = "Aggregated"
	// AIMCompositionModePrefillDecode serves requests through cooperating prefill
	// and decode roles.
	AIMCompositionModePrefillDecode AIMCompositionMode = "PrefillDecode"
)

// AIMCompositionPlacementScope is the hard placement relationship between roles.
// +kubebuilder:validation:Enum=SameNode
type AIMCompositionPlacementScope string

const (
	// AIMCompositionPlacementSameNode requires every role in one serving
	// composition to remain on a common node.
	AIMCompositionPlacementSameNode AIMCompositionPlacementScope = "SameNode"
)

// AIMCompositionPlacement declares hard placement intent.
type AIMCompositionPlacement struct {
	// Scope is the required relationship between composition roles.
	// +optional
	Scope AIMCompositionPlacementScope `json:"scope,omitempty"`
}

// AIMCompositionTransportReference selects a registered runtime transport preset.
type AIMCompositionTransportReference struct {
	// Preset is the name of a transport preset in the effective runtime config.
	// +kubebuilder:validation:MinLength=1
	Preset string `json:"preset"`
}

// AIMCompositionProfileKind is a profile resource kind accepted by an exact
// composition binding.
// +kubebuilder:validation:Enum=AIMProfile;AIMClusterProfile
type AIMCompositionProfileKind string

const (
	// AIMCompositionProfileKindNamespace references an AIMProfile in the
	// consuming namespace.
	AIMCompositionProfileKindNamespace AIMCompositionProfileKind = "AIMProfile"
	// AIMCompositionProfileKindCluster references an AIMClusterProfile.
	AIMCompositionProfileKindCluster AIMCompositionProfileKind = "AIMClusterProfile"
)

// AIMCompositionProfileReference fixes one execution position to an exact profile.
type AIMCompositionProfileReference struct {
	// Kind identifies the profile scope.
	Kind AIMCompositionProfileKind `json:"kind"`

	// Name is the referenced profile's metadata.name.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// AIMCompositionProfileBinding selects a profile by exact reference or selector.
// +kubebuilder:validation:XValidation:rule="(has(self.ref) ? 1 : 0) + (has(self.selector) ? 1 : 0) == 1",message="profile must set exactly one of ref or selector"
// +kubebuilder:validation:XValidation:rule="!has(self.selector) || !has(self.selector.role) || self.selector.role == 'deployable'",message="composition profile selectors may only omit role or set it to deployable"
type AIMCompositionProfileBinding struct {
	// Ref selects an exact AIMProfile or AIMClusterProfile.
	// +optional
	Ref *AIMCompositionProfileReference `json:"ref,omitempty"`

	// Selector contributes restrictions for automatic profile selection. An
	// empty selector is valid.
	// +optional
	Selector *ProfileSelector `json:"selector,omitempty"`
}

// AIMCompositionLaunch overlays runtime launch settings for one execution position.
type AIMCompositionLaunch struct {
	// ContainerEnv merges by environment-variable name.
	// +optional
	// +listType=map
	// +listMapKey=name
	ContainerEnv []corev1.EnvVar `json:"containerEnv,omitempty"`

	// EngineEnv merges by key into the inference engine environment.
	// +optional
	EngineEnv map[string]string `json:"engineEnv,omitempty"`

	// EngineArgs shallow-merges by top-level key into inference engine arguments.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Type=object
	// +optional
	EngineArgs *apiextensionsv1.JSON `json:"engineArgs,omitempty"`
}

// AIMCompositionWorker describes the optional cooperating worker within a role.
type AIMCompositionWorker struct {
	// Profile selects the worker runtime profile. Omit for automatic selection.
	// +optional
	Profile *AIMCompositionProfileBinding `json:"profile,omitempty"`

	// Launch customizes the selected worker profile.
	// +optional
	Launch *AIMCompositionLaunch `json:"launch,omitempty"`

	// Resources overlays the selected worker profile's resource requirements.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
}

// AIMCompositionParallelism describes supported intra-group parallelism.
type AIMCompositionParallelism struct {
	// Tensor is tensor parallelism within one execution group.
	// +optional
	// +kubebuilder:validation:Minimum=1
	Tensor *int32 `json:"tensor,omitempty"`

	// Pipeline is pipeline parallelism within one execution group.
	// +optional
	// +kubebuilder:validation:Minimum=1
	Pipeline *int32 `json:"pipeline,omitempty"`
}

// AIMCompositionScaling configures role-aware autoscaling.
// +kubebuilder:validation:XValidation:rule="!has(self.minReplicas) || self.minReplicas <= self.maxReplicas",message="minReplicas must be less than or equal to maxReplicas"
// +kubebuilder:validation:XValidation:rule="(has(self.metrics) && size(self.metrics) > 0) || (has(self.minReplicas) && self.minReplicas == 0)",message="scaling requires at least one metric unless minReplicas is zero"
type AIMCompositionScaling struct {
	// MinReplicas is the minimum number of complete execution groups.
	// +optional
	// +kubebuilder:validation:Minimum=0
	MinReplicas *int32 `json:"minReplicas,omitempty"`

	// MaxReplicas is the maximum number of complete execution groups.
	// +kubebuilder:validation:Minimum=1
	MaxReplicas int32 `json:"maxReplicas"`

	// Metrics is the role-aware autoscaling metric set.
	// +optional
	// +kubebuilder:validation:MinItems=1
	Metrics []AIMServiceMetricsSpec `json:"metrics,omitempty"`

	// PollingInterval is the scaler polling interval in seconds.
	// +optional
	// +kubebuilder:validation:Minimum=1
	PollingInterval *int32 `json:"pollingInterval,omitempty"`

	// CooldownPeriod is the inactivity period before scaling down.
	// +optional
	// +kubebuilder:validation:Minimum=0
	CooldownPeriod *int32 `json:"cooldownPeriod,omitempty"`
}

// AIMCompositionRole describes one complete execution group.
// +kubebuilder:validation:XValidation:rule="!(has(self.replicas) && has(self.scaling))",message="replicas and scaling are mutually exclusive"
type AIMCompositionRole struct {
	// Profile selects the primary runtime profile. Omit for automatic selection.
	// +optional
	Profile *AIMCompositionProfileBinding `json:"profile,omitempty"`

	// Launch customizes the selected primary profile.
	// +optional
	Launch *AIMCompositionLaunch `json:"launch,omitempty"`

	// Resources overlays the selected primary profile's resource requirements.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// Replicas is the fixed number of complete execution groups.
	// +optional
	// +kubebuilder:validation:Minimum=1
	Replicas *int32 `json:"replicas,omitempty"`

	// Scaling configures role-aware autoscaling.
	// +optional
	Scaling *AIMCompositionScaling `json:"scaling,omitempty"`

	// Parallelism configures supported intra-group parallelism.
	// +optional
	Parallelism *AIMCompositionParallelism `json:"parallelism,omitempty"`

	// Worker requests a cooperating worker shape for each group.
	// +optional
	Worker *AIMCompositionWorker `json:"worker,omitempty"`
}

// AIMCompositionSpec is the reusable recipe contract shared by namespace and
// cluster composition resources.
// +kubebuilder:validation:XValidation:rule="has(self.mode) || has(self.main) || has(self.prefill) || has(self.decode)",message="mode must be specified or inferable from main, prefill, or decode"
// +kubebuilder:validation:XValidation:rule="!(has(self.main) && (has(self.prefill) || has(self.decode)))",message="main cannot be combined with prefill or decode"
// +kubebuilder:validation:XValidation:rule="!has(self.mode) || self.mode != 'Aggregated' || (!has(self.prefill) && !has(self.decode))",message="Aggregated mode cannot contain prefill or decode roles"
// +kubebuilder:validation:XValidation:rule="!has(self.mode) || self.mode != 'PrefillDecode' || !has(self.main)",message="PrefillDecode mode cannot contain a main role"
// +kubebuilder:validation:XValidation:rule="!has(self.transport) || ((!has(self.mode) || self.mode == 'PrefillDecode') && !has(self.main))",message="transport is only valid for PrefillDecode compositions"
// +kubebuilder:validation:XValidation:rule="!has(self.profileSelector) || !has(self.profileSelector.role) || self.profileSelector.role == 'deployable'",message="composition profile selectors may only omit role or set it to deployable"
type AIMCompositionSpec struct {
	// Mode is the serving topology. It may be inferred from authored roles.
	// +optional
	Mode AIMCompositionMode `json:"mode,omitempty"`

	// Placement declares hard placement intent. Effective placement defaults are
	// resolved later and are not persisted into the recipe spec.
	// +optional
	Placement *AIMCompositionPlacement `json:"placement,omitempty"`

	// Transport selects a local prefill/decode transport preset.
	// +optional
	Transport *AIMCompositionTransportReference `json:"transport,omitempty"`

	// ProfileSelector contributes restrictions to every primary and worker binding.
	// +optional
	ProfileSelector *ProfileSelector `json:"profileSelector,omitempty"`

	// Main is the aggregated execution role.
	// +optional
	Main *AIMCompositionRole `json:"main,omitempty"`

	// Prefill is the prompt-processing execution role.
	// +optional
	Prefill *AIMCompositionRole `json:"prefill,omitempty"`

	// Decode is the token-generation execution role.
	// +optional
	Decode *AIMCompositionRole `json:"decode,omitempty"`

	// InheritSelectorDefaults controls whether consumers inherit runtime-config
	// selector defaults. Omission is intentionally preserved for resolution.
	// +optional
	InheritSelectorDefaults *bool `json:"inheritSelectorDefaults,omitempty"`
}

// AIMCompositionSelectorDefaults contains gap-filling selector fields. Identity,
// provenance, eligibility tier, and engine arguments are intentionally excluded.
type AIMCompositionSelectorDefaults struct {
	// Common applies to every automatically selected composition position.
	// +optional
	Common *AIMCompositionSelectorDefaultFields `json:"common,omitempty"`

	// Main applies to the aggregated primary role.
	// +optional
	Main *AIMCompositionSelectorDefaultFields `json:"main,omitempty"`

	// Prefill applies to the prefill primary and worker positions.
	// +optional
	Prefill *AIMCompositionSelectorDefaultFields `json:"prefill,omitempty"`

	// Decode applies to the decode primary and worker positions.
	// +optional
	Decode *AIMCompositionSelectorDefaultFields `json:"decode,omitempty"`
}

// AIMCompositionSelectorDefaultFields are administrator-provided gap fillers
// for automatic profile selection.
type AIMCompositionSelectorDefaultFields struct {
	// Engine defaults the inference engine.
	// +optional
	// +kubebuilder:validation:MinLength=1
	Engine string `json:"engine,omitempty"`

	// Metric defaults the optimization target.
	// +optional
	Metric AIMMetric `json:"metric,omitempty"`

	// Precision defaults numeric precision.
	// +optional
	Precision AIMPrecision `json:"precision,omitempty"`

	// AcceleratorType defaults the accelerator resource type.
	// +optional
	AcceleratorType AcceleratorType `json:"acceleratorType,omitempty"`

	// AcceleratorVendor defaults the accelerator vendor.
	// +optional
	AcceleratorVendor AcceleratorVendor `json:"acceleratorVendor,omitempty"`

	// AcceleratorModel defaults the accelerator identifier.
	// +optional
	// +kubebuilder:validation:MinLength=1
	AcceleratorModel string `json:"acceleratorModel,omitempty"`

	// AcceleratorPartitioningMode defaults the partition scheme.
	// +optional
	// +kubebuilder:validation:MinLength=1
	AcceleratorPartitioningMode string `json:"acceleratorPartitioningMode,omitempty"`

	// AcceleratorCount defaults the accelerator unit count.
	// +optional
	// +kubebuilder:validation:Minimum=0
	AcceleratorCount *int32 `json:"acceleratorCount,omitempty"`
}

// AIMCompositionTransportPreset names a registered local-transfer integration.
type AIMCompositionTransportPreset struct {
	// Name is the preset key.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// Integration identifies the registered, versioned integration contract.
	// +kubebuilder:validation:MinLength=1
	Integration string `json:"integration"`
}

// AIMRuntimeCompositionConfig configures composition selection and transport
// defaults at namespace or cluster scope.
// +kubebuilder:validation:XValidation:rule="!has(self.transportPresets) || self.transportPresets.all(p, self.transportPresets.exists_one(q, q.name == p.name))",message="transportPresets entries must have unique names"
type AIMRuntimeCompositionConfig struct {
	// SelectorDefaults fill selector fields left unconstrained by authored intent.
	// +optional
	SelectorDefaults *AIMCompositionSelectorDefaults `json:"selectorDefaults,omitempty"`

	// DefaultTransportPreset is the default preset for PrefillDecode compositions.
	// +optional
	// +kubebuilder:validation:MinLength=1
	DefaultTransportPreset string `json:"defaultTransportPreset,omitempty"`

	// TransportPresets maps names to registered local-transfer integrations.
	// Namespace entries replace same-named cluster entries as complete values.
	// +optional
	// +kubebuilder:validation:MaxItems=64
	// +listType=map
	// +listMapKey=name
	TransportPresets []AIMCompositionTransportPreset `json:"transportPresets,omitempty"`
}

// AIMCompositionModelBindingType classifies whether a recipe is reusable across
// model identities.
// +kubebuilder:validation:Enum=ModelAgnostic;ModelSpecific;Unknown
type AIMCompositionModelBindingType string

const (
	// AIMCompositionModelAgnostic has no authored or referenced model identity.
	AIMCompositionModelAgnostic AIMCompositionModelBindingType = "ModelAgnostic"
	// AIMCompositionModelSpecific carries a model restriction.
	AIMCompositionModelSpecific AIMCompositionModelBindingType = "ModelSpecific"
	// AIMCompositionModelUnknown cannot yet be classified because a reference is unresolved.
	AIMCompositionModelUnknown AIMCompositionModelBindingType = "Unknown"
)

// AIMCompositionFindingCategory classifies a composition diagnostic.
// +kubebuilder:validation:Enum=Contradiction;MissingInput;Unverified;Drift;RuntimeFailure
type AIMCompositionFindingCategory string

// AIMCompositionFinding records a bounded, user-visible composition diagnostic.
type AIMCompositionFinding struct {
	// Category classifies the finding.
	Category AIMCompositionFindingCategory `json:"category"`

	// Reason is a stable machine-readable identifier.
	Reason string `json:"reason"`

	// Message is a human-readable explanation.
	Message string `json:"message"`

	// Position identifies the affected execution position.
	// +optional
	Position string `json:"position,omitempty"`

	// Rule identifies the interoperability or construction rule.
	// +optional
	Rule string `json:"rule,omitempty"`

	// SourcePath identifies the authored field that contributed to the finding.
	// +optional
	SourcePath string `json:"sourcePath,omitempty"`

	// IntentDigest identifies the desired service attempt when applicable.
	// +optional
	IntentDigest string `json:"intentDigest,omitempty"`

	// Revision identifies accepted effective content when applicable.
	// +optional
	Revision string `json:"revision,omitempty"`
}

// AIMCompositionStatus is the observed state shared by namespace and cluster
// composition recipes.
type AIMCompositionStatus struct {
	// ObservedGeneration is the latest recipe generation processed.
	// +optional
	// +kubebuilder:validation:Minimum=0
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// ObservedSpecDigest identifies the authored recipe content.
	// +optional
	ObservedSpecDigest string `json:"observedSpecDigest,omitempty"`

	// ResolvedMode is the locally resolved topology.
	// +optional
	ResolvedMode AIMCompositionMode `json:"resolvedMode,omitempty"`

	// ModelBindingType classifies recipe model reuse.
	// +optional
	ModelBindingType AIMCompositionModelBindingType `json:"modelBindingType,omitempty"`

	// Conditions report local validity and reference resolution.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Findings reports bounded composition diagnostics.
	// +optional
	// +listType=atomic
	Findings []AIMCompositionFinding `json:"findings,omitempty"`
}
