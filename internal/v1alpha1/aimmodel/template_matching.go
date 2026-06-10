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

package aimmodel

import (
	"context"

	"github.com/blang/semver/v4"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimservicetemplate"
)

const (
	// LabelValueOriginFineTuned marks template copies created by aimId-based matching.
	LabelValueOriginFineTuned = "fine-tuned"
)

// TemplateMatchResult holds a matched template and the model source it was matched against.
type TemplateMatchResult struct {
	// OriginalAimId is the aimId from the matched template.
	OriginalAimId string
	// OriginalModelId is the modelId from the matched template.
	OriginalModelId string
	// OriginalVersion is the version from the matched template's status.
	OriginalVersion string
	// MatchedModelSource is the model source from the fine-tuned model that matched.
	MatchedModelSource aimv1alpha1.AIMModelSource
	// Spec is the matched template's common spec. Spec.ModelName identifies the
	// source owner; combined with OwnerNamespace it locates either an AIMModel
	// (namespace-scoped) or an AIMClusterModel (OwnerNamespace=="").
	Spec aimv1alpha1.AIMServiceTemplateSpecCommon
	// OwnerNamespace is the namespace of the source template's owning model.
	// Empty when the source is cluster-scoped (AIMClusterServiceTemplate →
	// AIMClusterModel).
	OwnerNamespace string
	// SourceProfile is the matched template's discovered profile (status.profile).
	// Copied into the fine-tuned template copy as spec.customProfile so the
	// aim-base runtime can find the profile at /workspace/aim-runtime/profiles/custom/
	// even though it has no baked-in profile catalog for the model family.
	// Nil when the source template hasn't completed discovery yet.
	SourceProfile *aimv1alpha1.AIMDiscoveredProfile
}

// FilterTemplatesByVersion filters templates according to the given version policy.
//
//   - pinned:   only templates where status.version == imageTag
//   - latest:   only templates at the newest status.version (by semver)
//   - all/any:  all templates (any is a deprecated alias of all)
func FilterTemplatesByVersion[T interface {
	GetVersion() string
}](
	templates []T,
	policy aimv1alpha1.AIMVersionPolicy,
	imageTag string,
) []T {
	switch policy {
	case aimv1alpha1.AIMVersionPolicyPinned:
		var result []T
		for _, t := range templates {
			if t.GetVersion() == imageTag {
				result = append(result, t)
			}
		}
		return result

	case aimv1alpha1.AIMVersionPolicyLatest:
		var latestVersion semver.Version
		var latestRaw string
		for _, t := range templates {
			v := t.GetVersion()
			if v == "" {
				continue
			}
			parsed, err := semver.ParseTolerant(v)
			if err != nil {
				continue
			}
			if parsed.GT(latestVersion) {
				latestVersion = parsed
				latestRaw = v
			}
		}
		if latestRaw == "" {
			return nil
		}
		var result []T
		for _, t := range templates {
			if t.GetVersion() == latestRaw {
				result = append(result, t)
			}
		}
		return result

	case aimv1alpha1.AIMVersionPolicyAll, aimv1alpha1.AIMVersionPolicyAny:
		return templates

	default:
		// Unknown policy — treat as pinned (safe default)
		return FilterTemplatesByVersion(templates, aimv1alpha1.AIMVersionPolicyPinned, imageTag)
	}
}

// templateVersionAccessor wraps an AIMServiceTemplate to satisfy the GetVersion interface.
type templateVersionAccessor struct {
	*aimv1alpha1.AIMServiceTemplate
}

func (t templateVersionAccessor) GetVersion() string { return t.Status.Version }

// clusterTemplateVersionAccessor wraps an AIMClusterServiceTemplate.
type clusterTemplateVersionAccessor struct {
	*aimv1alpha1.AIMClusterServiceTemplate
}

func (t clusterTemplateVersionAccessor) GetVersion() string { return t.Status.Version }

// isFineTunedCopy reports whether a template was created as a fine-tuned copy.
// Such templates are derivatives and must never participate as *sources* for
// matching: including them would create a feedback loop where each reconcile
// picks up the previous round's copies as new candidates, producing duplicates.
func isFineTunedCopy(labels map[string]string) bool {
	return labels[constants.LabelKeyOrigin] == LabelValueOriginFineTuned
}

// MatchTemplatesForModel implements the aimId-based matching algorithm for namespace-scoped templates.
// Returns one TemplateMatchResult per (modelSource, template) pair where template.modelId == modelSource.modelId.
func MatchTemplatesForModel(
	modelSpec *aimv1alpha1.AIMModelSpec,
	candidates []aimv1alpha1.AIMServiceTemplate,
) []TemplateMatchResult {
	policy := getVersionPolicy(modelSpec)
	imageTag := aimservicetemplate.ExtractVersionFromImage(modelSpec.Image)

	wrapped := make([]templateVersionAccessor, 0, len(candidates))
	for i := range candidates {
		if isFineTunedCopy(candidates[i].Labels) {
			continue
		}
		wrapped = append(wrapped, templateVersionAccessor{&candidates[i]})
	}
	filtered := FilterTemplatesByVersion(wrapped, policy, imageTag)

	return matchByModelId(modelSpec.ModelSources, filtered,
		func(t templateVersionAccessor) aimv1alpha1.AIMServiceTemplateSpecCommon {
			return t.Spec.AIMServiceTemplateSpecCommon
		},
		func(t templateVersionAccessor) string { return t.Namespace },
		func(t templateVersionAccessor) *aimv1alpha1.AIMDiscoveredProfile {
			return t.Status.Profile
		},
	)
}

// MatchClusterTemplatesForModel implements the aimId-based matching algorithm for cluster-scoped templates.
func MatchClusterTemplatesForModel(
	modelSpec *aimv1alpha1.AIMModelSpec,
	candidates []aimv1alpha1.AIMClusterServiceTemplate,
) []TemplateMatchResult {
	policy := getVersionPolicy(modelSpec)
	imageTag := aimservicetemplate.ExtractVersionFromImage(modelSpec.Image)

	wrapped := make([]clusterTemplateVersionAccessor, 0, len(candidates))
	for i := range candidates {
		if isFineTunedCopy(candidates[i].Labels) {
			continue
		}
		wrapped = append(wrapped, clusterTemplateVersionAccessor{&candidates[i]})
	}
	filtered := FilterTemplatesByVersion(wrapped, policy, imageTag)

	return matchByModelId(modelSpec.ModelSources, filtered,
		func(t clusterTemplateVersionAccessor) aimv1alpha1.AIMServiceTemplateSpecCommon {
			return t.Spec.AIMServiceTemplateSpecCommon
		},
		// Cluster templates have no namespace; their owner is an AIMClusterModel.
		func(_ clusterTemplateVersionAccessor) string { return "" },
		func(t clusterTemplateVersionAccessor) *aimv1alpha1.AIMDiscoveredProfile {
			return t.Status.Profile
		},
	)
}

func matchByModelId[T interface{ GetVersion() string }](
	modelSources []aimv1alpha1.AIMModelSource,
	templates []T,
	getSpec func(T) aimv1alpha1.AIMServiceTemplateSpecCommon,
	getOwnerNamespace func(T) string,
	getProfile func(T) *aimv1alpha1.AIMDiscoveredProfile,
) []TemplateMatchResult {
	var results []TemplateMatchResult
	for _, src := range modelSources {
		for _, t := range templates {
			spec := getSpec(t)
			if spec.ModelId == src.ModelID {
				results = append(results, TemplateMatchResult{
					OriginalAimId:      spec.AimId,
					OriginalModelId:    spec.ModelId,
					OriginalVersion:    t.GetVersion(),
					MatchedModelSource: src,
					Spec:               *spec.DeepCopy(),
					OwnerNamespace:     getOwnerNamespace(t),
					// getProfile may return nil for templates that haven't
					// completed discovery yet; the generated DeepCopy is
					// nil-safe (see zz_generated.deepcopy.go).
					SourceProfile: getProfile(t).DeepCopy(),
				})
			}
		}
	}
	return results
}

// BuildFineTunedServiceTemplates creates namespace-scoped template copies from
// match results. Each copy is stamped with the
// constants.AnnotationDeploymentImageRef annotation resolved from its match's
// source owner. Matches are skipped (and logged) when the source isn't ready
// yet — either because the deployment image can't be resolved (owner not
// found, no baseImageRef and no semver tag to fall back to) or because the
// source's discovery hasn't produced enough data to populate the cloned
// spec. An owner / source update will trigger a retry on the next reconcile.
func BuildFineTunedServiceTemplates(
	ctx context.Context,
	c client.Client,
	model *aimv1alpha1.AIMModel,
	matches []TemplateMatchResult,
) []*aimv1alpha1.AIMServiceTemplate {
	logger := log.FromContext(ctx)
	resolver := newImageResolver(c, &model.Spec)
	templates := make([]*aimv1alpha1.AIMServiceTemplate, 0, len(matches))
	for _, m := range matches {
		image, err := resolver.Resolve(ctx, m)
		if err != nil {
			logger.V(1).Info("deferring fine-tuned template copy: deployment image not resolvable yet",
				"aimId", m.OriginalAimId, "modelId", m.OriginalModelId,
				"version", m.OriginalVersion, "reason", err.Error())
			continue
		}
		spec, ok := buildFineTunedTemplateSpec(model.Name, &model.Spec, m)
		if !ok {
			logger.V(1).Info("deferring fine-tuned template copy: source profile not yet usable",
				"aimId", m.OriginalAimId, "modelId", m.OriginalModelId,
				"version", m.OriginalVersion)
			continue
		}
		templates = append(templates, &aimv1alpha1.AIMServiceTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:        generateFineTunedTemplateName(model.Name, m, spec),
				Namespace:   model.Namespace,
				Labels:      fineTunedTemplateLabels(model.Name),
				Annotations: fineTunedTemplateAnnotations(image),
			},
			Spec: aimv1alpha1.AIMServiceTemplateSpec{AIMServiceTemplateSpecCommon: spec},
		})
	}
	return templates
}

// BuildFineTunedClusterServiceTemplates creates cluster-scoped template copies
// from match results. See BuildFineTunedServiceTemplates for the per-copy
// image annotation rationale and the deferral conditions.
func BuildFineTunedClusterServiceTemplates(
	ctx context.Context,
	c client.Client,
	model *aimv1alpha1.AIMClusterModel,
	matches []TemplateMatchResult,
) []*aimv1alpha1.AIMClusterServiceTemplate {
	logger := log.FromContext(ctx)
	resolver := newImageResolver(c, &model.Spec)
	templates := make([]*aimv1alpha1.AIMClusterServiceTemplate, 0, len(matches))
	for _, m := range matches {
		image, err := resolver.Resolve(ctx, m)
		if err != nil {
			logger.V(1).Info("deferring fine-tuned cluster template copy: deployment image not resolvable yet",
				"aimId", m.OriginalAimId, "modelId", m.OriginalModelId,
				"version", m.OriginalVersion, "reason", err.Error())
			continue
		}
		spec, ok := buildFineTunedTemplateSpec(model.Name, &model.Spec, m)
		if !ok {
			logger.V(1).Info("deferring fine-tuned cluster template copy: source profile not yet usable",
				"aimId", m.OriginalAimId, "modelId", m.OriginalModelId,
				"version", m.OriginalVersion)
			continue
		}
		templates = append(templates, &aimv1alpha1.AIMClusterServiceTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:        generateFineTunedTemplateName(model.Name, m, spec),
				Labels:      fineTunedTemplateLabels(model.Name),
				Annotations: fineTunedTemplateAnnotations(image),
			},
			Spec: aimv1alpha1.AIMClusterServiceTemplateSpec{AIMServiceTemplateSpecCommon: spec},
		})
	}
	return templates
}

// buildFineTunedTemplateSpec assembles the common spec for a fine-tuned
// template copy from the source template's discovered profile
// (status.profile) and the fine-tuned model's overrides. status.profile is
// the authoritative output of the source's dry-run discovery and is the
// single source we read for identity / shape / engine config — the
// source's spec is intentionally not consulted for those fields because
// catalog templates auto-populate spec from a thin OCI label that
// routinely omits precision/metric.
//
// Discovery emits all metadata fields together (see aim-build's
// ProfileMetadata pydantic schema where engine/precision/metric/
// accelerator_count/aim_id/model_id are all required), so we treat
// status.profile.metadata as a single atom: any populated field means
// the data is as complete as it'll get; an entirely empty profile means
// discovery hasn't run yet and we defer until the next reconcile (the
// source's status update requeues us). The function also defers (as a
// safeguard against a misbehaving discovery image) when it would stamp
// a customProfile but metadata is missing one of the CEL-required
// fields — emitting in that case would only produce an apply the
// apiserver immediately rejects.
//
// customProfile is stamped only when discovery emitted engine config
// (engineArgs / envVars). The rebased aim-base image has no baked-in
// catalog so the runtime needs the engine config to resolve a profile;
// for inline-modelSources sources (which never run discovery and have
// no engine config in status), the rebased image is the same image
// family and the runtime resolves the original profile against the
// unchanged catalog — no customProfile needed.
//
// Non-discovery engine knobs (container resources, runtime config
// selection, pull secrets, service account) have no analogue in
// status.profile — they're operator-set on the source spec — so we
// inherit them from match.Spec.
//
// Container-level env vars (spec.Env) layer the fine-tuned model's
// spec.env on top of the source template's spec.env, with the
// fine-tuned model winning on name collision. This matches the
// "broader default, narrower scope wins" convention used by
// buildAutoGeneratedCustomTemplate and buildCustomServiceTemplate
// (where customTemplate.Env overrides model.Spec.Env), so a user
// setting AIMModel.spec.env on a fine-tuned model gets the same
// behavior as on a non-fine-tuned model: the vars reach both the
// runtime container and the download / check-size pods (the latter
// via AIMTemplateCache.spec.env in aimservice/caching.go and then
// AIMArtifact.spec.env in aimtemplatecache/reconcile.go). Per-source
// overrides (AIMModel.spec.modelSources[].env) carried in
// match.MatchedModelSource still win at the downloader specifically
// via the cache-env merge in aimtemplatecache/reconcile.go.
//
// Precedence note: the fine-tuned model's spec.env wins over the
// source template's runtime env on name collision. This is deliberate
// — the user's fine-tuned model is the more specific override — but
// it does mean a user can clobber a vetted runtime knob (e.g.
// VLLM_ROCM_USE_AITER) by setting the same name on AIMModel.spec.env.
// For env intended only to reach the downloader (e.g. AWS_*
// credentials for an s3:// source), prefer the per-source scope at
// AIMModel.spec.modelSources[].env, which doesn't appear on the
// runtime container at all.
func buildFineTunedTemplateSpec(
	modelName string,
	modelSpec *aimv1alpha1.AIMModelSpec,
	match TemplateMatchResult,
) (aimv1alpha1.AIMServiceTemplateSpecCommon, bool) {
	profile := match.SourceProfile
	if !discoveryReady(profile) {
		return aimv1alpha1.AIMServiceTemplateSpecCommon{}, false
	}
	meta := profile.Metadata

	// Identity falls back to match.Spec when discovery didn't emit it.
	// aim-build images (the v1alpha2 source-of-truth path) emit
	// metadata.aimId/modelId; older v1alpha1 dummy images and inline-
	// modelSources sources don't, but always carry them on spec because
	// the operator promoted them via maybePromoteDiscoveredIdentity or
	// the user set them up-front.
	aimID := meta.AimID
	if aimID == "" {
		aimID = match.Spec.AimId
	}
	modelID := meta.ModelID
	if modelID == "" {
		modelID = match.Spec.ModelId
	}
	spec := aimv1alpha1.AIMServiceTemplateSpecCommon{
		ModelName:    modelName,
		AimId:        aimID,
		ModelId:      modelID,
		ModelSources: []aimv1alpha1.AIMModelSource{match.MatchedModelSource},
	}
	if meta.GPU != "" || meta.GPUCount > 0 {
		spec.Hardware = &aimv1alpha1.AIMHardwareRequirements{
			GPU: &aimv1alpha1.AIMGpuRequirements{
				Model:    meta.GPU,
				Requests: meta.GPUCount,
			},
		}
	}
	if meta.Metric != "" {
		m := meta.Metric
		spec.Metric = &m
	}
	if meta.Precision != "" {
		p := meta.Precision
		spec.Precision = &p
	}
	if meta.Type != "" {
		t := meta.Type
		spec.Type = &t
	}

	if profile.EngineArgs != nil || len(profile.EnvVars) > 0 {
		spec.CustomProfile = &aimv1alpha1.AIMCustomProfile{
			EngineArgs: profile.EngineArgs,
			EnvVars:    profile.EnvVars,
		}
	}

	spec.Env = utils.MergeEnvVars(match.Spec.Env, modelSpec.Env)
	spec.Resources = match.Spec.Resources
	spec.RuntimeConfigRef = match.Spec.RuntimeConfigRef
	spec.ImagePullSecrets = match.Spec.ImagePullSecrets
	spec.ServiceAccountName = match.Spec.ServiceAccountName
	if len(modelSpec.ImagePullSecrets) > 0 {
		spec.ImagePullSecrets = modelSpec.ImagePullSecrets
	}
	if modelSpec.ServiceAccountName != "" {
		spec.ServiceAccountName = modelSpec.ServiceAccountName
	}

	// Belt-and-suspenders: if we'd stamp customProfile but discovery
	// metadata was incomplete, defer rather than emit an apply the
	// apiserver will reject on the CEL rule:
	//
	//   "when customProfile is set, aimId, modelId, hardware, metric,
	//    and precision are required"
	//
	// aim-build's ProfileMetadata schema emits all required fields
	// together (see godoc above), so this branch should not be reached
	// in practice; it guards against a future / third-party discovery
	// image that emits engineArgs alongside partial metadata. The
	// deferral mechanism is identical to discoveryReady=false: a
	// subsequent reconcile (triggered when the source's status
	// updates) retries.
	if spec.CustomProfile != nil &&
		(spec.AimId == "" || spec.ModelId == "" ||
			spec.Hardware == nil || spec.Metric == nil || spec.Precision == nil) {
		return aimv1alpha1.AIMServiceTemplateSpecCommon{}, false
	}

	return spec, true
}

// discoveryReady reports whether the source template's status.profile has
// produced any data yet. Metadata is treated as atomic per aim-build's
// ProfileMetadata schema (all required fields are emitted together), so
// any populated field — including engineArgs/envVars carried alongside
// it — means the data is as complete as it'll be. An entirely empty
// profile means discovery hasn't run / hasn't completed.
func discoveryReady(p *aimv1alpha1.AIMDiscoveredProfile) bool {
	if p == nil {
		return false
	}
	m := p.Metadata
	if m.AimID != "" || m.ModelID != "" || m.GPU != "" || m.Engine != "" {
		return true
	}
	if m.Metric != "" || m.Precision != "" || m.Type != "" {
		return true
	}
	if m.GPUCount > 0 {
		return true
	}
	return p.EngineArgs != nil || len(p.EnvVars) > 0
}

func fineTunedTemplateLabels(modelName string) map[string]string {
	return map[string]string{
		constants.LabelKeyModel:  modelName,
		constants.LabelKeyOrigin: LabelValueOriginFineTuned,
	}
}

// fineTunedTemplateAnnotations stamps the resolved deployment image onto a
// fine-tuned template copy. The returned map is always populated so SSA owns
// the annotation and overwrites stale values from earlier reconciles. Callers
// guarantee deploymentImage is non-empty by deferring the copy when the
// resolver can't produce one (see BuildFineTunedServiceTemplates).
func fineTunedTemplateAnnotations(deploymentImage string) map[string]string {
	return map[string]string{
		constants.AnnotationDeploymentImageRef: deploymentImage,
	}
}

// generateFineTunedTemplateName creates a deterministic name for a fine-tuned
// template copy. Format: {modelName}-ft-{version}-{precision}-{gpu}-{hash}.
// Identity / shape inputs come from the assembled spec (sourced from
// status.profile.metadata) rather than match.Spec because catalog templates
// routinely leave precision/metric unset on spec.
func generateFineTunedTemplateName(
	modelName string,
	match TemplateMatchResult,
	spec aimv1alpha1.AIMServiceTemplateSpecCommon,
) string {
	nameParts := []string{modelName, "ft"}

	if match.OriginalVersion != "" {
		nameParts = append(nameParts, match.OriginalVersion)
	}
	if spec.Precision != nil {
		nameParts = append(nameParts, string(*spec.Precision))
	}
	if spec.Hardware != nil && spec.Hardware.GPU != nil && spec.Hardware.GPU.Model != "" {
		nameParts = append(nameParts, spec.Hardware.GPU.Model)
	}

	hashInputs := []any{
		modelName,
		match.OriginalAimId,
		match.OriginalModelId,
		match.OriginalVersion,
		// Source owner identity disambiguates heterogeneous matches: the
		// same aimId can be served by sibling base models with different
		// base-image families (e.g. aim-base vs aim-cpu-base), each
		// contributing a template under the same (aimId, modelId, version,
		// precision, gpu) tuple. Without owner identity in the hash, those
		// copies would collide on apply and only one would survive.
		// match.Spec.ModelName is the source owner's name; OwnerNamespace
		// is empty for cluster-scoped owners.
		match.Spec.ModelName,
		match.OwnerNamespace,
	}
	if spec.Metric != nil {
		hashInputs = append(hashInputs, string(*spec.Metric))
	}
	if spec.Precision != nil {
		hashInputs = append(hashInputs, string(*spec.Precision))
	}

	name, _ := utils.GenerateDerivedName(nameParts, utils.WithHashSource(hashInputs...))
	return name
}

func getVersionPolicy(spec *aimv1alpha1.AIMModelSpec) aimv1alpha1.AIMVersionPolicy {
	if spec.Custom != nil && spec.Custom.VersionPolicy != "" {
		return spec.Custom.VersionPolicy
	}
	return aimv1alpha1.AIMVersionPolicyPinned
}
