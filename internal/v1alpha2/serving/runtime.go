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

package serving

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strings"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kserveconstants "github.com/kserve/kserve/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/profileyaml"
)

const (
	// RuntimeNamePrefix is the reserved name prefix AIM Engine owns for the
	// KServe runtimes it projects from profiles. AIM Engine only ever reads,
	// creates, or modifies runtimes under this prefix, and is therefore
	// authoritative over every name beneath it: it force-applies the runtimes it
	// projects unconditionally (SSA + ForceOwnership). That is safe by the
	// reserved-prefix policy ALONE — a hand-authored runtime uses any other name,
	// so a force-apply can only ever clobber a runtime AIM Engine owns, regardless
	// of whether the name is hashed. See CONTEXT.md "Reserved `aim-` prefix" /
	// "Authoritative apply".
	//
	// Per-profile runtimes add one further margin ON TOP OF the prefix policy:
	// RuntimeName appends a deterministic hash of the profile name, so the exact
	// object name is also unguessable. That hash is EXTRA safety for the
	// per-profile scheme, not the basis of force-apply safety — the readable,
	// unhashed model-slug primary (ModelSlugRuntimeName) is force-applied just as
	// safely on the reserved-prefix policy alone.
	RuntimeNamePrefix = "aim-"

	// runtimeNameStem is the RuntimeNamePrefix without its trailing hyphen. It is
	// the first, never-truncated part fed to GenerateDerivedName so the reserved
	// aim- prefix stays intact regardless of how long the profile name is.
	runtimeNameStem = "aim"

	// RuntimeModelFormat is the single supportedModelFormat name every projected
	// runtime advertises. Consumers set the same modelFormat.name and reference
	// the runtime by its explicit name. autoSelect is off on every projected
	// runtime: since they all share this one format, turning it on would let
	// unrelated models (and any generic huggingface runtime) collide in KServe's
	// format-based auto-selection. See buildRuntimeSpec.
	RuntimeModelFormat = "huggingface"
)

// RuntimeName returns the reserved per-profile runtime name for a profile:
// aim-<truncated-profile>-<hash>. The runtime and its colocated profile
// ConfigMap share this name so both the eager profile reconciler and the lazy
// InferenceService watcher derive it deterministically.
//
// The name is routed through GenerateDerivedName so it is:
//   - always ≤63 chars (the reserved "aim" stem is never truncated, so the
//     aim- prefix always survives; only the profile part is shortened to fit),
//   - deterministic on profileName (same input → same name, every caller
//     agrees), and
//   - suffixed with an unguessable SHA-256-derived hash of profileName.
//
// The reserved aim- prefix already makes AIM Engine authoritative over this name
// (see RuntimeNamePrefix and CONTEXT.md "Authoritative apply"), so the
// unconditional force-apply is safe on the prefix policy alone. The hash suffix
// is an ADDITIONAL margin specific to the per-profile scheme: a hand-authored
// object cannot even land on the exact aim-<truncated-profile>-<hash> name by
// accident. (The readable model-slug primary has no such hash and is force-applied
// just as safely — see ModelSlugRuntimeName.) RuntimeName stays a pure, total
// func(string) string so every symmetric caller — the AIMService overlay, the
// profile projector, and the lazy watcher — computes the identical name. It is
// not reversible: resolution back to a profile goes through the managed-CSR
// ownerRef and the stamped profile annotation, never by parsing the name.
func RuntimeName(profileName string) string {
	// GenerateDerivedName only errors on an empty parts slice or an
	// out-of-range max length; neither is possible with these fixed arguments,
	// so RuntimeName can stay a total func(string) string.
	name, _ := utils.GenerateDerivedName(
		[]string{runtimeNameStem, profileName},
		utils.WithHashSource(profileName),
		utils.WithMaxLength(utils.MaxKubernetesNameLength),
	)
	return name
}

// ModelSlug derives the portable, vendor-independent slug used to name the
// Reduced-mode model-slug primary runtime. It is keyed on the profile's aimId
// (the model architecture identifier, which carries no accelerator or precision
// axis), so every hardware/precision variant of one model converges on a single
// slug. The aimId is normalised to an RFC-1123 name component (lower-cased,
// invalid characters folded to hyphens), e.g. "qwen/qwen3-32b" -> "qwen-qwen3-32b".
func ModelSlug(aimId string) string {
	return utils.MakeRFC1123Compliant(aimId)
}

// ModelSlugRuntimeName returns the reserved model-slug primary runtime name:
// aim-<model-slug>. Native KServe InferenceServices reference this stable,
// vendor-independent name explicitly (autoSelect stays off on every projected
// runtime — see buildRuntimeSpec). Unlike the per-profile RuntimeName it is
// deliberately kept READABLE — never hashed — so users can type it by hand. On
// the rare overflow it is truncated (and any trailing hyphen trimmed) to stay
// within the 63-char limit rather than being made opaque.
//
// Being readable, this name is GUESSABLE — so the per-profile RuntimeName's
// "unguessable hash cannot collide" argument does NOT apply here, and it does
// not need to. The model-slug primary is still force-applied authoritatively
// (SSA + ForceOwnership, like every projected runtime); that is safe by the
// reserved aim- prefix policy alone — AIM Engine owns everything under aim-
// exclusively, so a force-apply can only clobber a runtime it owns regardless of
// whether the name is hashed. See CONTEXT.md "Reserved `aim-` prefix" /
// "Authoritative apply".
func ModelSlugRuntimeName(aimId string) string {
	slug := ModelSlug(aimId)
	if maxSlug := utils.MaxKubernetesNameLength - len(RuntimeNamePrefix); len(slug) > maxSlug {
		slug = strings.TrimRight(slug[:maxSlug], "-")
	}
	return RuntimeNamePrefix + slug
}

// NamespaceRuntimeInput carries everything the shared builder needs to project a
// complete namespace ServingRuntime (+ colocated ConfigMap) from a resolved
// profile. Both the eager namespace projection (slice 06) and the lazy
// InferenceService-watch shadow (slice 04) assemble this from a resolved
// profile and, for caching profiles, the profile-owned AIMProfileCache.
type NamespaceRuntimeInput struct {
	// ProfileName is the backing profile's name. It always sources the profile
	// correlator label so the runtime traces back to its profile. By default it
	// also names the runtime and its colocated ConfigMap via RuntimeName
	// (aim-<truncated-profile>-<hash>); set Name to override that (the
	// Reduced-mode model-slug primary names the objects aim-<model-slug> while
	// still correlating to the backing primary profile).
	ProfileName string

	// Name optionally overrides the runtime (and colocated ConfigMap) object
	// name. Empty means RuntimeName(ProfileName) (the hashed per-profile
	// runtime). The model-slug primary sets aim-<model-slug>. Must stay under the
	// reserved aim- prefix so the authoritative force-apply policy holds.
	Name string

	// Namespace is where the ServingRuntime and its colocated ConfigMap live.
	// For an eager namespace projection this is the profile's own namespace;
	// for a lazy shadow it is the consuming InferenceService's namespace.
	Namespace string

	// Spec is the resolved profile spec (consumer overlays already
	// materialised). Required.
	Spec *aimv1alpha2.AIMProfileSpecCommon

	// YAMLContract is inferred from the source profile YAML and inherited by
	// derived profiles. Generated and hand-authored profiles pass an explicit
	// CanonicalContract. The zero value is invalid so missing propagation fails
	// during validation instead of silently changing the runtime schema.
	YAMLContract profileyaml.Contract

	// Resources is the resolved predictor resource requirements (the profile
	// status.resources the profile controller computed). Falls back to
	// spec.resources when nil.
	Resources *corev1.ResourceRequirements

	// NodeAffinity is the resolved node affinity (profile
	// status.resolvedNodeAffinity); nil for profiles with no accelerator
	// requirement.
	NodeAffinity *corev1.NodeAffinity

	// Cache is the profile-owned AIMProfileCache, present when the profile opts
	// into caching. Its ready artifacts contribute a PVC volume + mount that
	// redirects model loading to local weights (paired with the
	// modelSources-conditional AIM_CACHE_PATH / AIM_MODEL_ID framework env).
	// Nil when the profile does not cache.
	Cache *aimv1alpha2.AIMProfileCache
}

// BuildNamespaceServingRuntime projects a resolved profile into a complete,
// Path-A-ready namespace ServingRuntime named aim-<ProfileName> plus its
// colocated profile ConfigMap. "Complete" means the runtime carries everything
// a consumer needs to serve the profile without inlining a predictor: the
// image, base resources, resolved node affinity, the profile ConfigMap
// volume/mount, the full profile-derived framework env (AIM_PROFILE_ID plus the
// modelSources-conditional AIM_ID / AIM_MODEL_ID / AIM_CACHE_PATH set), and the
// profile-owned cache mount when the profile opts into caching.
//
// The returned runtime and ConfigMap are pure desired state; the caller owns
// applying them (with the reserved-prefix force-apply policy) and setting owner
// references for garbage collection.
func BuildNamespaceServingRuntime(input NamespaceRuntimeInput) (*kservev1alpha1.ServingRuntime, *corev1.ConfigMap, error) {
	projection, err := buildNamespaceProjection(input)
	if err != nil {
		return nil, nil, err
	}
	configMap := projection.configMap

	runtime := &kservev1alpha1.ServingRuntime{
		TypeMeta: metav1.TypeMeta{
			APIVersion: kservev1alpha1.SchemeGroupVersion.String(),
			Kind:       "ServingRuntime",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        projection.name,
			Namespace:   input.Namespace,
			Labels:      projection.labels,
			Annotations: projection.annotations,
		},
		Spec: projection.runtimeSpec,
	}

	contentHash, err := RuntimeProjectionContentHash(runtime, configMap)
	if err != nil {
		return nil, nil, fmt.Errorf("hash runtime projection content: %w", err)
	}
	runtime.Annotations[constants.AnnotationRuntimeProjectionContentHash] = contentHash
	configMap.Annotations[constants.AnnotationRuntimeProjectionContentHash] = contentHash

	return runtime, configMap, nil
}

// namespaceProjection is the kind-independent part of a namespace projection:
// the object name, correlator metadata, the colocated profile ConfigMap, and
// the serving pod shape. The KServe ServingRuntime and the
// LLMInferenceServiceConfig projected from one profile are both rendered from
// it, so they serve the profile identically and share one ConfigMap.
type namespaceProjection struct {
	name        string
	labels      map[string]string
	annotations map[string]string
	configMap   *corev1.ConfigMap
	runtimeSpec kservev1alpha1.ServingRuntimeSpec
}

func buildNamespaceProjection(input NamespaceRuntimeInput) (namespaceProjection, error) {
	if input.Spec == nil {
		return namespaceProjection{}, fmt.Errorf("profile spec is nil")
	}
	if input.ProfileName == "" {
		return namespaceProjection{}, fmt.Errorf("profile name is empty")
	}
	if input.Namespace == "" {
		return namespaceProjection{}, fmt.Errorf("namespace is empty")
	}

	spec := input.Spec

	yamlBytes, filename, err := AssembleProfileYAMLForContract(spec, input.YAMLContract)
	if err != nil {
		return namespaceProjection{}, fmt.Errorf("assemble profile YAML: %w", err)
	}

	name := input.Name
	if name == "" {
		name = RuntimeName(input.ProfileName)
	}
	labels := runtimeLabels(input.ProfileName, spec)
	annotations := runtimeAnnotations(input.ProfileName, spec)

	configMap := BuildProfileConfigMap(name, input.Namespace, maps.Clone(labels), filename, yamlBytes)
	configMap.Annotations = maps.Clone(annotations)

	modelReference := resolveVLLMModelReference(spec, input.Cache)
	envVars := buildRuntimeEnv(spec, filename, modelReference)

	volumes := []corev1.Volume{sharedMemoryVolume(spec.Engine), BuildProfileVolume(name)}
	mounts := []corev1.VolumeMount{sharedMemoryMount(), BuildProfileVolumeMount(spec.AimId)}

	cacheVolumes, cacheMounts := buildProfileCacheMounts(input.Cache)
	volumes = append(volumes, cacheVolumes...)
	mounts = append(mounts, cacheMounts...)

	runtimeSpec, err := buildRuntimeSpec(
		spec,
		input.Resources,
		envVars,
		volumes,
		mounts,
		input.NodeAffinity,
	)
	if err != nil {
		return namespaceProjection{}, fmt.Errorf("build runtime spec: %w", err)
	}

	return namespaceProjection{
		name:        name,
		labels:      labels,
		annotations: annotations,
		configMap:   configMap,
		runtimeSpec: runtimeSpec,
	}, nil
}

// runtimeProjectionContent contains exactly the fields that can change what a
// pod receives from a namespaced ServingRuntime projection. Object metadata is
// intentionally absent: names are checked independently, while labels,
// annotations, owner references, resource versions, managed fields, and
// timestamps do not change the projected pod or profile file.
type runtimeProjectionContent struct {
	RuntimeSpec         kservev1alpha1.ServingRuntimeSpec `json:"runtimeSpec"`
	ConfigMapData       map[string]string                 `json:"configMapData,omitempty"`
	ConfigMapBinaryData map[string][]byte                 `json:"configMapBinaryData,omitempty"`
	ConfigMapImmutable  *bool                             `json:"configMapImmutable,omitempty"`
}

// RuntimeProjectionContentHash returns a deterministic SHA-256 hash of the
// workload-affecting content shared by a namespaced ServingRuntime and its
// colocated ConfigMap. It excludes ephemeral metadata and the hash annotation
// itself, so repeated builds from equivalent profile/cache inputs are stable.
func RuntimeProjectionContentHash(runtime *kservev1alpha1.ServingRuntime, configMap *corev1.ConfigMap) (string, error) {
	if runtime == nil || configMap == nil {
		return "", fmt.Errorf("ServingRuntime and ConfigMap must both be non-nil")
	}
	content := runtimeProjectionContent{
		RuntimeSpec:         runtime.Spec,
		ConfigMapData:       configMap.Data,
		ConfigMapBinaryData: configMap.BinaryData,
		ConfigMapImmutable:  configMap.Immutable,
	}
	data, err := json.Marshal(content)
	if err != nil {
		return "", fmt.Errorf("marshal projection content: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

// ClusterRuntimeInput carries everything the shared builder needs to project a
// bare ClusterServingRuntime from a resolved profile. A cluster runtime cannot
// colocate a namespaced profile ConfigMap, so the bare CSR (no ConfigMap, no
// profile volume/mount) is a SHADOW TARGET in all cases: it always carries
// AIM_PROFILE_ID=custom/<aimId>/<name>, which resolves under the operator-owned
// .../profiles/custom/ subtree that no image bakes, so the profile file exists
// only once the lazy InferenceService-watch reconciler completes it per-namespace
// with a complete namespace ServingRuntime + colocated ConfigMap. It is the
// cluster-scoped, native-referenceable runtime handle, not a standalone-servable
// runtime. See BuildClusterServingRuntime for the full lifecycle.
type ClusterRuntimeInput struct {
	// ProfileName is the backing AIMClusterProfile's name. It always sources the
	// profile correlator label; by default it also names the runtime via
	// RuntimeName (aim-<truncated-profile>-<hash>). Set Name to override (the
	// model-slug primary).
	ProfileName string

	// Name optionally overrides the runtime object name. Empty means
	// RuntimeName(ProfileName) (the hashed per-profile runtime); the model-slug
	// primary sets aim-<model-slug>. Must stay under the reserved aim- prefix.
	Name string

	// Spec is the resolved profile spec. Required.
	Spec *aimv1alpha2.AIMProfileSpecCommon

	// Resources is the resolved predictor resource requirements (the profile
	// status.resources the profile controller computed). Falls back to
	// spec.resources when nil.
	Resources *corev1.ResourceRequirements

	// NodeAffinity is the freshly-computed node affinity (from the profile match
	// result); nil for profiles with no accelerator requirement.
	NodeAffinity *corev1.NodeAffinity
}

// BuildClusterServingRuntime projects a resolved cluster profile into a bare
// ClusterServingRuntime named RuntimeName(ProfileName). "Bare" means it carries
// the image, base resources, resolved node affinity, and the full
// profile-derived framework env — including AIM_PROFILE_ID — but no colocated
// profile ConfigMap and no profile volume/mount (a cluster runtime cannot
// guarantee a namespaced ConfigMap in an arbitrary consumer namespace).
//
// The bare CSR is a SHADOW TARGET in ALL cases, not a standalone-servable
// runtime. BuildFrameworkEnvVars always sets AIM_PROFILE_ID=custom/<aimId>/<name>,
// which the runtime resolves under the operator-owned .../profiles/custom/
// subtree. No AIM image bakes profiles there — image-baked profiles live at
// .../profiles/<aim_id>/<profile_id>.yaml under a different filename convention —
// so the file AIM_PROFILE_ID names exists only once the lazy
// InferenceService-watch reconciler materializes the namespace shadow (a complete
// namespace ServingRuntime + ConfigMap of the same name). The runtime's profile
// lookup short-circuits on an explicit AIM_PROFILE_ID with no fallback to AIM_ID
// auto-selection, so a consumer that binds the bare CSR before the shadow lands
// resolves AIM_PROFILE_ID against a file that is not yet mounted. That is a
// bounded, self-healing transient — the ISVC-create event drives the lazy
// reconcile, the namespace SR shadows the CSR, and KServe re-renders onto it
// within a few seconds — not a standalone-serving guarantee.
//
// The returned runtime is pure desired state; the caller owns applying it (with
// the reserved-prefix force-apply policy) and setting the owner reference.
func BuildClusterServingRuntime(input ClusterRuntimeInput) (*kservev1alpha1.ClusterServingRuntime, error) {
	projection, err := buildClusterProjection(input)
	if err != nil {
		return nil, err
	}

	return &kservev1alpha1.ClusterServingRuntime{
		TypeMeta: metav1.TypeMeta{
			APIVersion: kservev1alpha1.SchemeGroupVersion.String(),
			Kind:       "ClusterServingRuntime",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        projection.name,
			Labels:      projection.labels,
			Annotations: projection.annotations,
		},
		Spec: projection.runtimeSpec,
	}, nil
}

// buildClusterProjection is the cluster-scope counterpart of
// buildNamespaceProjection: the bare serving pod shape (no profile ConfigMap,
// no profile volume or cache mount) shared by the ClusterServingRuntime and the
// system-namespace LLMInferenceServiceConfig projected from one
// AIMClusterProfile. The returned configMap is always nil.
func buildClusterProjection(input ClusterRuntimeInput) (namespaceProjection, error) {
	if input.Spec == nil {
		return namespaceProjection{}, fmt.Errorf("profile spec is nil")
	}
	if input.ProfileName == "" {
		return namespaceProjection{}, fmt.Errorf("profile name is empty")
	}

	spec := input.Spec
	name := input.Name
	if name == "" {
		name = RuntimeName(input.ProfileName)
	}

	filename, err := ProfileFilename(spec)
	if err != nil {
		return namespaceProjection{}, fmt.Errorf("resolve profile filename: %w", err)
	}
	modelReference := resolveVLLMModelReference(spec, nil)
	envVars := buildRuntimeEnv(spec, filename, modelReference)

	volumes := []corev1.Volume{sharedMemoryVolume(spec.Engine)}
	mounts := []corev1.VolumeMount{sharedMemoryMount()}

	runtimeSpec, err := buildRuntimeSpec(
		spec,
		input.Resources,
		envVars,
		volumes,
		mounts,
		input.NodeAffinity,
	)
	if err != nil {
		return namespaceProjection{}, fmt.Errorf("build runtime spec: %w", err)
	}

	return namespaceProjection{
		name:        name,
		labels:      runtimeLabels(input.ProfileName, spec),
		annotations: runtimeAnnotations(input.ProfileName, spec),
		runtimeSpec: runtimeSpec,
	}, nil
}

// buildRuntimeSpec assembles the ServingRuntimeSpec shared by the namespace
// ServingRuntime and the cluster ClusterServingRuntime: one predictor container
// (image, env, resources, ports, mounts), the supplied volumes, the standard
// supportedModelFormat, and the resolved node affinity when present. Legacy
// AIM runtimes retain their v2 protocol declaration; direct upstream vLLM
// exposes its native OpenAI API and does not claim KServe protocol v2.
// autoSelect is always OFF: every projected runtime — per-profile and
// model-slug primary alike — shares the single RuntimeModelFormat, so enabling
// autoSelect would make KServe's format-based auto-selection ambiguous across
// unrelated models (and hijack any co-installed generic huggingface runtime).
// Consumers reference the runtime by its explicit name instead.
func buildRuntimeSpec(
	spec *aimv1alpha2.AIMProfileSpecCommon,
	resources *corev1.ResourceRequirements,
	env []corev1.EnvVar,
	volumes []corev1.Volume,
	mounts []corev1.VolumeMount,
	nodeAffinity *corev1.NodeAffinity,
) (kservev1alpha1.ServingRuntimeSpec, error) {
	invocation, err := buildEngineInvocation(spec)
	if err != nil {
		return kservev1alpha1.ServingRuntimeSpec{}, err
	}

	container := corev1.Container{
		Name:            constants.ContainerKServe,
		Image:           spec.Image,
		ImagePullPolicy: utils.PullPolicyForImage(spec.Image),
		Env:             env,
		Resources:       resolveRuntimeResources(resources, spec.Resources),
		Ports: []corev1.ContainerPort{
			{
				ContainerPort: constants.DefaultHTTPPort,
				Name:          "http",
				Protocol:      corev1.ProtocolTCP,
			},
		},
		VolumeMounts: mounts,
		Command:      invocation.Command,
		Args:         invocation.Args,
	}

	runtimeSpec := kservev1alpha1.ServingRuntimeSpec{
		SupportedModelFormats: []kservev1alpha1.SupportedModelFormat{
			{
				Name:       RuntimeModelFormat,
				AutoSelect: ptr.To(false),
			},
		},
		ServingRuntimePodSpec: kservev1alpha1.ServingRuntimePodSpec{
			Containers:       []corev1.Container{container},
			Volumes:          volumes,
			ImagePullSecrets: utils.CopyPullSecrets(spec.ImagePullSecrets),
		},
	}
	if !usesDirectVLLM(spec) {
		runtimeSpec.ProtocolVersions = []kserveconstants.InferenceServiceProtocol{kserveconstants.ProtocolV2}
	}

	if nodeAffinity != nil {
		runtimeSpec.Affinity = &corev1.Affinity{NodeAffinity: nodeAffinity}
	}

	return runtimeSpec, nil
}

// sharedMemoryVolume returns the emptyDir-backed /dev/shm volume every predictor
// needs for multi-worker engines.
func sharedMemoryVolume(engine string) corev1.Volume {
	size := constants.DefaultSharedMemorySize
	if strings.EqualFold(engine, "vllm_omni") {
		size = constants.VLLMOmniSharedMemorySize
	}
	dshmSizeLimit := resource.MustParse(size)
	return corev1.Volume{
		Name: constants.VolumeSharedMemory,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{
				Medium:    corev1.StorageMediumMemory,
				SizeLimit: &dshmSizeLimit,
			},
		},
	}
}

// sharedMemoryMount returns the volume mount paired with sharedMemoryVolume.
func sharedMemoryMount() corev1.VolumeMount {
	return corev1.VolumeMount{
		Name:      constants.VolumeSharedMemory,
		MountPath: constants.MountPathSharedMemory,
	}
}

// runtimeLabels builds the correlator labels stamped on a projected runtime (and
// its colocated ConfigMap) so it can be traced back to its profile and filtered
// by hardware class, model, and precision. These are derived from the backing
// profile, so selectors keep working even though the hashed object name is opaque.
func runtimeLabels(profileName string, spec *aimv1alpha2.AIMProfileSpecCommon) map[string]string {
	profileLabelValue, _ := utils.SanitizeLabelValue(profileName)
	labels := map[string]string{
		constants.LabelK8sComponent: constants.ComponentInference,
		constants.LabelK8sManagedBy: constants.LabelValueManagedBy,
		constants.LabelProfile:      profileLabelValue,
		// Only "projected" is stamped today; see LabelRuntimeProjectionState.
		constants.LabelRuntimeProjectionState: constants.LabelValueRuntimeProjectionStateProjected,
	}
	// acceleratorModel is CRD-constrained to a valid label value
	// (^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$, ≤63 chars), so it is carried
	// verbatim — preserving case (MI300X) to match the gpu.model label.
	if spec.AcceleratorModel != "" {
		labels[constants.LabelKeyAcceleratorClass] = spec.AcceleratorModel
	}
	// Selectable model / precision labels: sanitized (aimId → model slug,
	// precision lower-cased) so operators can `kubectl get servingruntimes -l`
	// by the model a runtime serves and its precision. Skipped when the axis is
	// absent or cannot be reduced to a valid label value.
	if modelValue, err := utils.SanitizeLabelValue(ModelSlug(spec.AimId)); err == nil {
		labels[constants.LabelModelID] = modelValue
	}
	if precisionValue, err := utils.SanitizeLabelValue(string(spec.Precision)); err == nil {
		labels[constants.LabelPrecision] = precisionValue
	}
	return labels
}

// runtimeProjectionProjectedMessage is the stable, human-readable note stamped
// alongside the LabelRuntimeProjectionState marker. It is deliberately free of
// per-object data so repeated SSA applies never churn the annotation, and it
// points readers at the backing profile's RuntimeProjected condition for the
// authoritative, transitioning health signal.
const runtimeProjectionProjectedMessage = "Projected from an AIM profile by aim-engine; " +
	"see the backing profile's RuntimeProjected condition for authoritative state"

// runtimeAnnotations stamps the full-fidelity identity annotations on a projected
// runtime (and its colocated ConfigMap): because the object name is truncated +
// hashed, these carry the untruncated profile name and axes verbatim so tooling
// can recover the runtime's origin without reversing the name. Empty axes are omitted.
// It also carries the projection-state message companion to LabelRuntimeProjectionState.
func runtimeAnnotations(profileName string, spec *aimv1alpha2.AIMProfileSpecCommon) map[string]string {
	annotations := map[string]string{
		constants.AnnotationRuntimeProjectionMessage: runtimeProjectionProjectedMessage,
	}
	if profileName != "" {
		annotations[constants.AnnotationProjectedProfile] = profileName
	}
	if spec.AimId != "" {
		annotations[constants.AnnotationProjectedAimID] = spec.AimId
	}
	if spec.ModelId != "" {
		annotations[constants.AnnotationProjectedModelID] = spec.ModelId
	}
	if spec.Precision != "" {
		annotations[constants.AnnotationProjectedPrecision] = string(spec.Precision)
	}
	if spec.Metric != "" {
		annotations[constants.AnnotationProjectedMetric] = string(spec.Metric)
	}
	return annotations
}

// resolveRuntimeResources prefers the profile's resolved status.resources and
// falls back to spec.resources, matching the AIMService predictor resolution
// (minus the service-level override, which lives on the consumer overlay).
func resolveRuntimeResources(resolved, specResources *corev1.ResourceRequirements) corev1.ResourceRequirements {
	if resolved != nil {
		return *resolved
	}
	if specResources != nil {
		return *specResources
	}
	return corev1.ResourceRequirements{}
}

// buildProfileCacheMounts turns the ready artifacts of a profile-owned cache
// into PVC volumes and mounts, mirroring the AIMService predictor cache wiring.
// Artifacts are emitted in name order so the runtime spec is deterministic
// (stable SSA, stable tests). Returns nil slices when there is no ready cache.
func buildProfileCacheMounts(cache *aimv1alpha2.AIMProfileCache) ([]corev1.Volume, []corev1.VolumeMount) {
	if cache == nil || cache.Status.Status != constants.AIMStatusReady {
		return nil, nil
	}

	names := make([]string, 0, len(cache.Status.Artifacts))
	for name := range cache.Status.Artifacts {
		names = append(names, name)
	}
	sort.Strings(names)

	var volumes []corev1.Volume
	var mounts []corev1.VolumeMount
	for _, name := range names {
		resolved := cache.Status.Artifacts[name]
		if resolved.Status != constants.AIMStatusReady || resolved.PersistentVolumeClaim == "" {
			continue
		}

		volumeName := artifactVolumeName(resolved)

		volumes = append(volumes, corev1.Volume{
			Name: volumeName,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: resolved.PersistentVolumeClaim,
				},
			},
		})

		mounts = append(mounts, corev1.VolumeMount{
			Name:      volumeName,
			MountPath: resolvedArtifactMountPath(resolved, volumeName),
		})
	}

	return volumes, mounts
}

// upsertEnvVars returns base with overrides applied by name: an override
// replaces a same-named entry in place, otherwise it is appended. The result
// never aliases base.
func upsertEnvVars(base, overrides []corev1.EnvVar) []corev1.EnvVar {
	result := append([]corev1.EnvVar(nil), base...)
	for _, o := range overrides {
		replaced := false
		for i := range result {
			if result[i].Name == o.Name {
				result[i] = o
				replaced = true
				break
			}
		}
		if !replaced {
			result = append(result, o)
		}
	}
	return result
}
