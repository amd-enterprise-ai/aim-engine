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
	"sort"

	"github.com/blang/semver/v4"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimservicetemplate"
)

const (
	// LabelValueOriginFineTuned marks template copies created by aimId-based matching.
	LabelValueOriginFineTuned = "fine-tuned"

	// EnvAIMBaseImageRef is the env var baked into AIM model images recording the base image.
	EnvAIMBaseImageRef = "AIM_BASE_IMAGE_REF"
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
	// Spec is the matched template's common spec.
	Spec aimv1alpha1.AIMServiceTemplateSpecCommon
}

// FilterTemplatesByVersion filters templates according to the given version policy.
//
//   - pinned: only templates where status.version == imageTag
//   - latest: only templates at the newest status.version (by semver)
//   - any:    all templates
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

	case aimv1alpha1.AIMVersionPolicyAny:
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

// MatchTemplatesForModel implements the aimId-based matching algorithm for namespace-scoped templates.
// Returns one TemplateMatchResult per (modelSource, template) pair where template.modelId == modelSource.modelId.
func MatchTemplatesForModel(
	modelSpec *aimv1alpha1.AIMModelSpec,
	candidates []aimv1alpha1.AIMServiceTemplate,
) []TemplateMatchResult {
	policy := getVersionPolicy(modelSpec)
	imageTag := aimservicetemplate.ExtractVersionFromImage(modelSpec.Image)

	wrapped := make([]templateVersionAccessor, len(candidates))
	for i := range candidates {
		wrapped[i] = templateVersionAccessor{&candidates[i]}
	}
	filtered := FilterTemplatesByVersion(wrapped, policy, imageTag)

	return matchByModelId(modelSpec.ModelSources, filtered, func(t templateVersionAccessor) aimv1alpha1.AIMServiceTemplateSpecCommon {
		return t.Spec.AIMServiceTemplateSpecCommon
	})
}

// MatchClusterTemplatesForModel implements the aimId-based matching algorithm for cluster-scoped templates.
func MatchClusterTemplatesForModel(
	modelSpec *aimv1alpha1.AIMModelSpec,
	candidates []aimv1alpha1.AIMClusterServiceTemplate,
) []TemplateMatchResult {
	policy := getVersionPolicy(modelSpec)
	imageTag := aimservicetemplate.ExtractVersionFromImage(modelSpec.Image)

	wrapped := make([]clusterTemplateVersionAccessor, len(candidates))
	for i := range candidates {
		wrapped[i] = clusterTemplateVersionAccessor{&candidates[i]}
	}
	filtered := FilterTemplatesByVersion(wrapped, policy, imageTag)

	return matchByModelId(modelSpec.ModelSources, filtered, func(t clusterTemplateVersionAccessor) aimv1alpha1.AIMServiceTemplateSpecCommon {
		return t.Spec.AIMServiceTemplateSpecCommon
	})
}

func matchByModelId[T interface{ GetVersion() string }](
	modelSources []aimv1alpha1.AIMModelSource,
	templates []T,
	getSpec func(T) aimv1alpha1.AIMServiceTemplateSpecCommon,
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
				})
			}
		}
	}
	return results
}

// BuildFineTunedServiceTemplates creates namespace-scoped template copies from match results.
func BuildFineTunedServiceTemplates(
	model *aimv1alpha1.AIMModel,
	matches []TemplateMatchResult,
) []*aimv1alpha1.AIMServiceTemplate {
	templates := make([]*aimv1alpha1.AIMServiceTemplate, 0, len(matches))
	for _, m := range matches {
		t := buildFineTunedServiceTemplate(model.Name, model.Namespace, &model.Spec, m)
		templates = append(templates, t)
	}
	return templates
}

// BuildFineTunedClusterServiceTemplates creates cluster-scoped template copies from match results.
func BuildFineTunedClusterServiceTemplates(
	model *aimv1alpha1.AIMClusterModel,
	matches []TemplateMatchResult,
) []*aimv1alpha1.AIMClusterServiceTemplate {
	templates := make([]*aimv1alpha1.AIMClusterServiceTemplate, 0, len(matches))
	for _, m := range matches {
		t := buildFineTunedClusterServiceTemplate(model.Name, &model.Spec, m)
		templates = append(templates, t)
	}
	return templates
}

func buildFineTunedServiceTemplate(
	modelName, namespace string,
	modelSpec *aimv1alpha1.AIMModelSpec,
	match TemplateMatchResult,
) *aimv1alpha1.AIMServiceTemplate {
	spec := buildFineTunedTemplateSpec(modelName, modelSpec, match)
	name := generateFineTunedTemplateName(modelName, match)

	return &aimv1alpha1.AIMServiceTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    fineTunedTemplateLabels(modelName),
		},
		Spec: aimv1alpha1.AIMServiceTemplateSpec{
			AIMServiceTemplateSpecCommon: spec,
		},
	}
}

func buildFineTunedClusterServiceTemplate(
	modelName string,
	modelSpec *aimv1alpha1.AIMModelSpec,
	match TemplateMatchResult,
) *aimv1alpha1.AIMClusterServiceTemplate {
	spec := buildFineTunedTemplateSpec(modelName, modelSpec, match)
	name := generateFineTunedTemplateName(modelName, match)

	return &aimv1alpha1.AIMClusterServiceTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: fineTunedTemplateLabels(modelName),
		},
		Spec: aimv1alpha1.AIMClusterServiceTemplateSpec{
			AIMServiceTemplateSpecCommon: spec,
		},
	}
}

// buildFineTunedTemplateSpec creates the common spec for a fine-tuned template copy.
// The copy inherits hardware, profile, engine config from the original and overrides modelSources.
func buildFineTunedTemplateSpec(
	modelName string,
	modelSpec *aimv1alpha1.AIMModelSpec,
	match TemplateMatchResult,
) aimv1alpha1.AIMServiceTemplateSpecCommon {
	spec := match.Spec

	// Point to the fine-tuned model
	spec.ModelName = modelName

	// Override model sources with the custom weight source.
	// Preserve the modelId so cache paths remain consistent.
	spec.ModelSources = []aimv1alpha1.AIMModelSource{match.MatchedModelSource}

	// Propagate pull secrets and service account from the fine-tuned model
	if len(modelSpec.ImagePullSecrets) > 0 {
		spec.ImagePullSecrets = modelSpec.ImagePullSecrets
	}
	if modelSpec.ServiceAccountName != "" {
		spec.ServiceAccountName = modelSpec.ServiceAccountName
	}

	return spec
}

func fineTunedTemplateLabels(modelName string) map[string]string {
	return map[string]string{
		constants.LabelKeyModel:  modelName,
		constants.LabelKeyOrigin: LabelValueOriginFineTuned,
	}
}

// generateFineTunedTemplateName creates a deterministic name for a fine-tuned template copy.
// Format: {modelName}-ft-{version}-{precision}-{gpu}-{hash}
func generateFineTunedTemplateName(modelName string, match TemplateMatchResult) string {
	nameParts := []string{modelName, "ft"}

	if match.OriginalVersion != "" {
		nameParts = append(nameParts, match.OriginalVersion)
	}
	if match.Spec.Precision != nil {
		nameParts = append(nameParts, string(*match.Spec.Precision))
	}
	if match.Spec.Hardware != nil && match.Spec.Hardware.GPU != nil && match.Spec.Hardware.GPU.Model != "" {
		nameParts = append(nameParts, match.Spec.Hardware.GPU.Model)
	}

	hashInputs := []any{
		modelName,
		match.OriginalAimId,
		match.OriginalModelId,
		match.OriginalVersion,
	}
	if match.Spec.Metric != nil {
		hashInputs = append(hashInputs, string(*match.Spec.Metric))
	}
	if match.Spec.Precision != nil {
		hashInputs = append(hashInputs, string(*match.Spec.Precision))
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

// sortByVersionDesc sorts version strings by semver descending.
// Non-parsable versions sort after valid ones.
func sortByVersionDesc(versions []string) {
	sort.Slice(versions, func(i, j int) bool {
		vi, errI := semver.ParseTolerant(versions[i])
		vj, errJ := semver.ParseTolerant(versions[j])
		if errI != nil && errJ != nil {
			return versions[i] > versions[j]
		}
		if errI != nil {
			return false
		}
		if errJ != nil {
			return true
		}
		return vi.GT(vj)
	})
}
