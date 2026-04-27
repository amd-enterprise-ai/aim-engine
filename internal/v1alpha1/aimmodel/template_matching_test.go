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
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

func makeTemplate(
	name, aimId, modelId, version string,
	metric aimv1alpha1.AIMMetric,
	precision aimv1alpha1.AIMPrecision,
) aimv1alpha1.AIMServiceTemplate {
	m := &metric
	p := &precision
	return aimv1alpha1.AIMServiceTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: aimv1alpha1.AIMServiceTemplateSpec{
			AIMServiceTemplateSpecCommon: aimv1alpha1.AIMServiceTemplateSpecCommon{
				ModelName: "official-model",
				AimId:     aimId,
				ModelId:   modelId,
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Metric:    m,
					Precision: p,
					Hardware: &aimv1alpha1.AIMHardwareRequirements{
						GPU: &aimv1alpha1.AIMGpuRequirements{
							Model:    "MI300X",
							Requests: 4,
						},
					},
				},
			},
		},
		Status: aimv1alpha1.AIMServiceTemplateStatus{
			Version: version,
		},
	}
}

func makeClusterTemplate(name, aimId, modelId, version string) aimv1alpha1.AIMClusterServiceTemplate {
	return aimv1alpha1.AIMClusterServiceTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: aimv1alpha1.AIMClusterServiceTemplateSpec{
			AIMServiceTemplateSpecCommon: aimv1alpha1.AIMServiceTemplateSpecCommon{
				ModelName: "official-cluster-model",
				AimId:     aimId,
				ModelId:   modelId,
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: &aimv1alpha1.AIMHardwareRequirements{
						GPU: &aimv1alpha1.AIMGpuRequirements{
							Model:    "MI300X",
							Requests: 4,
						},
					},
				},
			},
		},
		Status: aimv1alpha1.AIMServiceTemplateStatus{
			Version: version,
		},
	}
}

// ============================================================================
// VERSION FILTERING TESTS
// ============================================================================

func TestFilterTemplatesByVersion_Pinned(t *testing.T) {
	templates := []templateVersionAccessor{
		{&aimv1alpha1.AIMServiceTemplate{Status: aimv1alpha1.AIMServiceTemplateStatus{Version: "0.8.5"}}},
		{&aimv1alpha1.AIMServiceTemplate{Status: aimv1alpha1.AIMServiceTemplateStatus{Version: "0.9.0"}}},
		{&aimv1alpha1.AIMServiceTemplate{Status: aimv1alpha1.AIMServiceTemplateStatus{Version: "0.8.5"}}},
	}

	result := FilterTemplatesByVersion(templates, aimv1alpha1.AIMVersionPolicyPinned, "0.8.5")
	if len(result) != 2 {
		t.Fatalf("expected 2 templates for pinned=0.8.5, got %d", len(result))
	}
}

func TestFilterTemplatesByVersion_Latest(t *testing.T) {
	templates := []templateVersionAccessor{
		{&aimv1alpha1.AIMServiceTemplate{Status: aimv1alpha1.AIMServiceTemplateStatus{Version: "0.8.5"}}},
		{&aimv1alpha1.AIMServiceTemplate{Status: aimv1alpha1.AIMServiceTemplateStatus{Version: "0.9.0"}}},
		{&aimv1alpha1.AIMServiceTemplate{Status: aimv1alpha1.AIMServiceTemplateStatus{Version: "0.9.0"}}},
		{&aimv1alpha1.AIMServiceTemplate{Status: aimv1alpha1.AIMServiceTemplateStatus{Version: "0.7.0"}}},
	}

	result := FilterTemplatesByVersion(templates, aimv1alpha1.AIMVersionPolicyLatest, "")
	if len(result) != 2 {
		t.Fatalf("expected 2 templates at latest version 0.9.0, got %d", len(result))
	}
	for _, r := range result {
		if r.GetVersion() != "0.9.0" {
			t.Errorf("expected version 0.9.0, got %s", r.GetVersion())
		}
	}
}

func TestFilterTemplatesByVersion_Any(t *testing.T) {
	templates := []templateVersionAccessor{
		{&aimv1alpha1.AIMServiceTemplate{Status: aimv1alpha1.AIMServiceTemplateStatus{Version: "0.8.5"}}},
		{&aimv1alpha1.AIMServiceTemplate{Status: aimv1alpha1.AIMServiceTemplateStatus{Version: "0.9.0"}}},
	}

	result := FilterTemplatesByVersion(templates, aimv1alpha1.AIMVersionPolicyAny, "")
	if len(result) != 2 {
		t.Fatalf("expected all 2 templates for any policy, got %d", len(result))
	}
}

func TestFilterTemplatesByVersion_Pinned_NoMatch(t *testing.T) {
	templates := []templateVersionAccessor{
		{&aimv1alpha1.AIMServiceTemplate{Status: aimv1alpha1.AIMServiceTemplateStatus{Version: "0.9.0"}}},
	}

	result := FilterTemplatesByVersion(templates, aimv1alpha1.AIMVersionPolicyPinned, "0.8.5")
	if len(result) != 0 {
		t.Fatalf("expected 0 templates for pinned=0.8.5 with no match, got %d", len(result))
	}
}

func TestFilterTemplatesByVersion_Latest_EmptyVersions(t *testing.T) {
	templates := []templateVersionAccessor{
		{&aimv1alpha1.AIMServiceTemplate{Status: aimv1alpha1.AIMServiceTemplateStatus{Version: ""}}},
		{&aimv1alpha1.AIMServiceTemplate{Status: aimv1alpha1.AIMServiceTemplateStatus{Version: ""}}},
	}

	result := FilterTemplatesByVersion(templates, aimv1alpha1.AIMVersionPolicyLatest, "")
	if len(result) != 0 {
		t.Fatalf("expected 0 templates when all versions empty, got %d", len(result))
	}
}

// ============================================================================
// TEMPLATE MATCHING TESTS
// ============================================================================

func TestMatchTemplatesForModel_BasicMatch(t *testing.T) {
	modelSpec := &aimv1alpha1.AIMModelSpec{
		Image: "amdenterpriseai/aim-base:0.8.5",
		AimId: "qwen/qwen3-32b",
		ModelSources: []aimv1alpha1.AIMModelSource{
			{ModelID: "qwen/qwen3-32b-fp8", SourceURI: "s3://my-bucket/weights/"},
		},
		Custom: &aimv1alpha1.AIMCustomModelSpec{
			VersionPolicy: aimv1alpha1.AIMVersionPolicyPinned,
		},
	}

	candidates := []aimv1alpha1.AIMServiceTemplate{
		makeTemplate("tpl-lat-fp8", "qwen/qwen3-32b", "qwen/qwen3-32b-fp8", "0.8.5", "latency", "fp8"),
		makeTemplate("tpl-thr-fp16", "qwen/qwen3-32b", "qwen/qwen3-32b-fp16", "0.8.5", "throughput", "fp16"),
		makeTemplate("tpl-other", "meta-llama/llama3-70b", "meta-llama/llama3-70b-fp8", "0.8.5", "latency", "fp8"),
	}

	matches := MatchTemplatesForModel(modelSpec, candidates)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].OriginalModelId != "qwen/qwen3-32b-fp8" {
		t.Errorf("expected match on qwen/qwen3-32b-fp8, got %s", matches[0].OriginalModelId)
	}
	if matches[0].MatchedModelSource.SourceURI != "s3://my-bucket/weights/" {
		t.Errorf("expected custom sourceUri, got %s", matches[0].MatchedModelSource.SourceURI)
	}
}

func TestMatchTemplatesForModel_PinnedVersionFilter(t *testing.T) {
	modelSpec := &aimv1alpha1.AIMModelSpec{
		Image: "amdenterpriseai/aim-base:0.8.5",
		AimId: "qwen/qwen3-32b",
		ModelSources: []aimv1alpha1.AIMModelSource{
			{ModelID: "qwen/qwen3-32b-fp8", SourceURI: "s3://bucket/"},
		},
		Custom: &aimv1alpha1.AIMCustomModelSpec{
			VersionPolicy: aimv1alpha1.AIMVersionPolicyPinned,
		},
	}

	candidates := []aimv1alpha1.AIMServiceTemplate{
		makeTemplate("tpl-old", "qwen/qwen3-32b", "qwen/qwen3-32b-fp8", "0.7.0", "latency", "fp8"),
		makeTemplate("tpl-new", "qwen/qwen3-32b", "qwen/qwen3-32b-fp8", "0.8.5", "latency", "fp8"),
	}

	matches := MatchTemplatesForModel(modelSpec, candidates)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match (pinned to 0.8.5), got %d", len(matches))
	}
	if matches[0].OriginalVersion != "0.8.5" {
		t.Errorf("expected version 0.8.5, got %s", matches[0].OriginalVersion)
	}
}

func TestMatchTemplatesForModel_NoMatchingModelId(t *testing.T) {
	modelSpec := &aimv1alpha1.AIMModelSpec{
		Image: "amdenterpriseai/aim-base:0.8.5",
		AimId: "qwen/qwen3-32b",
		ModelSources: []aimv1alpha1.AIMModelSource{
			{ModelID: "qwen/qwen3-32b-int4", SourceURI: "s3://bucket/"},
		},
		Custom: &aimv1alpha1.AIMCustomModelSpec{
			VersionPolicy: aimv1alpha1.AIMVersionPolicyPinned,
		},
	}

	candidates := []aimv1alpha1.AIMServiceTemplate{
		makeTemplate("tpl-fp8", "qwen/qwen3-32b", "qwen/qwen3-32b-fp8", "0.8.5", "latency", "fp8"),
	}

	matches := MatchTemplatesForModel(modelSpec, candidates)
	if len(matches) != 0 {
		t.Fatalf("expected 0 matches (modelId mismatch), got %d", len(matches))
	}
}

// Guards against a feedback loop: the aimId index returns all templates
// carrying a given aimId, including fine-tuned copies the controller created
// on an earlier reconcile. Those copies must not become sources themselves —
// otherwise the fine-tuned template's own `status.version` (e.g. "0.11" from
// the base-image tag) races with the canonical `status.version` ("0.11.0"
// from the model image), versionPolicy=latest flip-flops, and we end up
// stamping out additional copies named after both version spellings.
func TestMatchTemplatesForModel_ExcludesFineTunedCopies(t *testing.T) {
	modelSpec := &aimv1alpha1.AIMModelSpec{
		Image: "ghcr.io/silogen/aim-base:0.11",
		AimId: "meta-llama/Llama-3.2-1B-Instruct",
		ModelSources: []aimv1alpha1.AIMModelSource{
			{ModelID: "meta-llama/Llama-3.2-1B-Instruct", SourceURI: "hf://meta-llama/Llama-3.2-1B-Instruct"},
		},
		Custom: &aimv1alpha1.AIMCustomModelSpec{
			VersionPolicy: aimv1alpha1.AIMVersionPolicyLatest,
		},
	}

	base := makeTemplate("base-auto", "meta-llama/Llama-3.2-1B-Instruct", "meta-llama/Llama-3.2-1B-Instruct", "0.11.0", "latency", "fp16")
	stale := makeTemplate("ft-copy-stale", "meta-llama/Llama-3.2-1B-Instruct", "meta-llama/Llama-3.2-1B-Instruct", "0.11", "latency", "fp16")
	stale.Labels = map[string]string{constants.LabelKeyOrigin: LabelValueOriginFineTuned}

	matches := MatchTemplatesForModel(modelSpec, []aimv1alpha1.AIMServiceTemplate{base, stale})
	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 match (base only), got %d", len(matches))
	}
	if matches[0].OriginalVersion != "0.11.0" {
		t.Errorf("expected match to carry base version 0.11.0, got %s", matches[0].OriginalVersion)
	}
}

func TestMatchClusterTemplatesForModel_ExcludesFineTunedCopies(t *testing.T) {
	modelSpec := &aimv1alpha1.AIMModelSpec{
		Image: "ghcr.io/silogen/aim-base:0.11",
		AimId: "meta-llama/Llama-3.2-1B-Instruct",
		ModelSources: []aimv1alpha1.AIMModelSource{
			{ModelID: "meta-llama/Llama-3.2-1B-Instruct", SourceURI: "hf://meta-llama/Llama-3.2-1B-Instruct"},
		},
		Custom: &aimv1alpha1.AIMCustomModelSpec{
			VersionPolicy: aimv1alpha1.AIMVersionPolicyLatest,
		},
	}

	base := makeClusterTemplate("base-auto", "meta-llama/Llama-3.2-1B-Instruct", "meta-llama/Llama-3.2-1B-Instruct", "0.11.0")
	stale := makeClusterTemplate("ft-copy-stale", "meta-llama/Llama-3.2-1B-Instruct", "meta-llama/Llama-3.2-1B-Instruct", "0.11")
	stale.Labels = map[string]string{constants.LabelKeyOrigin: LabelValueOriginFineTuned}

	matches := MatchClusterTemplatesForModel(modelSpec, []aimv1alpha1.AIMClusterServiceTemplate{base, stale})
	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 match (base only), got %d", len(matches))
	}
	if matches[0].OriginalVersion != "0.11.0" {
		t.Errorf("expected match to carry base version 0.11.0, got %s", matches[0].OriginalVersion)
	}
}

func TestMatchClusterTemplatesForModel(t *testing.T) {
	modelSpec := &aimv1alpha1.AIMModelSpec{
		Image: "amdenterpriseai/aim-base:0.9.0",
		AimId: "qwen/qwen3-32b",
		ModelSources: []aimv1alpha1.AIMModelSource{
			{ModelID: "qwen/qwen3-32b-fp8", SourceURI: "s3://bucket/weights/"},
		},
		Custom: &aimv1alpha1.AIMCustomModelSpec{
			VersionPolicy: aimv1alpha1.AIMVersionPolicyPinned,
		},
	}

	candidates := []aimv1alpha1.AIMClusterServiceTemplate{
		makeClusterTemplate("cl-tpl", "qwen/qwen3-32b", "qwen/qwen3-32b-fp8", "0.9.0"),
		makeClusterTemplate("cl-tpl-wrong", "qwen/qwen3-32b", "qwen/qwen3-32b-fp16", "0.9.0"),
	}

	matches := MatchClusterTemplatesForModel(modelSpec, candidates)
	if len(matches) != 1 {
		t.Fatalf("expected 1 cluster match, got %d", len(matches))
	}
}

// ============================================================================
// TEMPLATE COPY BUILDER TESTS
// ============================================================================

func TestBuildFineTunedServiceTemplates(t *testing.T) {
	model := &aimv1alpha1.AIMModel{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-finetuned-qwen",
			Namespace: "default",
		},
		Spec: aimv1alpha1.AIMModelSpec{
			Image: "amdenterpriseai/aim-base:0.8.5",
			AimId: "qwen/qwen3-32b",
			ModelSources: []aimv1alpha1.AIMModelSource{
				{ModelID: "qwen/qwen3-32b-fp8", SourceURI: "s3://my-bucket/weights/"},
			},
			Custom: &aimv1alpha1.AIMCustomModelSpec{
				VersionPolicy: aimv1alpha1.AIMVersionPolicyPinned,
			},
		},
	}

	lat := aimv1alpha1.AIMMetric("latency")
	fp8 := aimv1alpha1.AIMPrecision("fp8")
	matches := []TemplateMatchResult{
		{
			OriginalAimId:   "qwen/qwen3-32b",
			OriginalModelId: "qwen/qwen3-32b-fp8",
			OriginalVersion: "0.8.5",
			MatchedModelSource: aimv1alpha1.AIMModelSource{
				ModelID:   "qwen/qwen3-32b-fp8",
				SourceURI: "s3://my-bucket/weights/",
			},
			Spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				ModelName: "official-model",
				AimId:     "qwen/qwen3-32b",
				ModelId:   "qwen/qwen3-32b-fp8",
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Metric:    &lat,
					Precision: &fp8,
					Hardware: &aimv1alpha1.AIMHardwareRequirements{
						GPU: &aimv1alpha1.AIMGpuRequirements{
							Model:    "MI300X",
							Requests: 4,
						},
					},
				},
			},
		},
	}

	// versionPolicy=pinned with model.spec.image set short-circuits owner
	// lookup, so a no-object client is sufficient.
	templates := BuildFineTunedServiceTemplates(context.Background(), newFakeClient(), model, matches)
	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}

	tpl := templates[0]

	if got := tpl.Annotations[constants.AnnotationDeploymentImageRef]; got != model.Spec.Image {
		t.Errorf("expected deployment image annotation %q, got %q", model.Spec.Image, got)
	}

	// Verify the copy points to the fine-tuned model
	if tpl.Spec.ModelName != "my-finetuned-qwen" {
		t.Errorf("expected modelName=my-finetuned-qwen, got %s", tpl.Spec.ModelName)
	}

	// Verify namespace matches the model
	if tpl.Namespace != "default" {
		t.Errorf("expected namespace=default, got %s", tpl.Namespace)
	}

	// Verify custom model sources are set
	if len(tpl.Spec.ModelSources) != 1 {
		t.Fatalf("expected 1 model source, got %d", len(tpl.Spec.ModelSources))
	}
	if tpl.Spec.ModelSources[0].SourceURI != "s3://my-bucket/weights/" {
		t.Errorf("expected custom sourceUri, got %s", tpl.Spec.ModelSources[0].SourceURI)
	}

	// Verify hardware is inherited
	if tpl.Spec.Hardware == nil || tpl.Spec.Hardware.GPU == nil {
		t.Fatal("expected hardware to be inherited from original")
	}
	if tpl.Spec.Hardware.GPU.Model != "MI300X" {
		t.Errorf("expected GPU model MI300X, got %s", tpl.Spec.Hardware.GPU.Model)
	}
	if tpl.Spec.Hardware.GPU.Requests != 4 {
		t.Errorf("expected 4 GPU requests, got %d", tpl.Spec.Hardware.GPU.Requests)
	}

	// Verify labels
	if tpl.Labels[constants.LabelKeyModel] != "my-finetuned-qwen" {
		t.Errorf("expected model label=my-finetuned-qwen, got %s", tpl.Labels[constants.LabelKeyModel])
	}
	if tpl.Labels[constants.LabelKeyOrigin] != LabelValueOriginFineTuned {
		t.Errorf("expected origin label=%s, got %s", LabelValueOriginFineTuned, tpl.Labels[constants.LabelKeyOrigin])
	}

	// Verify name is deterministic and non-empty
	if tpl.Name == "" {
		t.Error("expected non-empty template name")
	}
}

func TestBuildFineTunedServiceTemplates_PropagatesSourceProfileAsCustomProfile(t *testing.T) {
	model := &aimv1alpha1.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "ft-llama", Namespace: "default"},
		Spec: aimv1alpha1.AIMModelSpec{
			Image: "ghcr.io/silogen/aim-base:0.11",
			AimId: "meta-llama/Llama-3.2-1B-Instruct",
			ModelSources: []aimv1alpha1.AIMModelSource{
				{ModelID: "meta-llama/Llama-3.2-1B-Instruct", SourceURI: "s3://bucket/weights/"},
			},
			Custom: &aimv1alpha1.AIMCustomModelSpec{VersionPolicy: aimv1alpha1.AIMVersionPolicyPinned},
		},
	}

	lat := aimv1alpha1.AIMMetric("latency")
	fp16 := aimv1alpha1.AIMPrecision("fp16")
	engineArgs := &apiextensionsv1.JSON{Raw: []byte(`{"tensor-parallel-size":1}`)}
	envVars := map[string]string{"HIP_FORCE_DEV_KERNARG": "1"}

	matches := []TemplateMatchResult{{
		OriginalAimId:   "meta-llama/Llama-3.2-1B-Instruct",
		OriginalModelId: "meta-llama/Llama-3.2-1B-Instruct",
		OriginalVersion: "0.11.0",
		MatchedModelSource: aimv1alpha1.AIMModelSource{
			ModelID:   "meta-llama/Llama-3.2-1B-Instruct",
			SourceURI: "s3://bucket/weights/",
		},
		Spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
			ModelName: "official-base",
			AimId:     "meta-llama/Llama-3.2-1B-Instruct",
			ModelId:   "meta-llama/Llama-3.2-1B-Instruct",
			ProfileId: "vllm-mi300x-fp16-tp1-latency",
			AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
				Metric:    &lat,
				Precision: &fp16,
				Hardware: &aimv1alpha1.AIMHardwareRequirements{
					GPU: &aimv1alpha1.AIMGpuRequirements{Model: "MI300X", Requests: 1},
				},
			},
		},
		SourceProfile: &aimv1alpha1.AIMDiscoveredProfile{
			EngineArgs: engineArgs,
			EnvVars:    envVars,
		},
	}}

	tpl := BuildFineTunedServiceTemplates(context.Background(), newFakeClient(), model, matches)[0]

	if tpl.Spec.CustomProfile == nil {
		t.Fatal("expected CustomProfile to be populated from source profile")
	}
	if tpl.Spec.CustomProfile.EngineArgs == nil ||
		string(tpl.Spec.CustomProfile.EngineArgs.Raw) != `{"tensor-parallel-size":1}` {
		t.Errorf("expected engineArgs to carry over, got %+v", tpl.Spec.CustomProfile.EngineArgs)
	}
	if tpl.Spec.CustomProfile.EnvVars["HIP_FORCE_DEV_KERNARG"] != "1" {
		t.Errorf("expected envVars to carry over, got %+v", tpl.Spec.CustomProfile.EnvVars)
	}
	// ProfileId is cleared so the kserve builder doesn't emit a built-in
	// AIM_PROFILE_ID that conflicts with the custom one.
	if tpl.Spec.ProfileId != "" {
		t.Errorf("expected ProfileId to be cleared, got %q", tpl.Spec.ProfileId)
	}
}

// TestBuildFineTunedServiceTemplates_SkipsEmptySourceProfile guards the
// v1alpha1 CEL rule on AIMServiceTemplateSpecCommon: `when customProfile is
// set, aimId, modelId, hardware, metric, and precision are required`. Source
// templates that declare identity via inline modelSources (e.g. aim-dummy
// fixtures) legitimately omit metric/precision and produce a status.Profile
// with no engineArgs/envVars. Stamping an empty customProfile onto the copy
// would push the API server to reject the apply. The match should leave
// CustomProfile unset in that case.
func TestBuildFineTunedServiceTemplates_SkipsEmptySourceProfile(t *testing.T) {
	model := &aimv1alpha1.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "ft-dummy", Namespace: "default"},
		Spec: aimv1alpha1.AIMModelSpec{
			Image: "ghcr.io/silogen/aim-base:0.1.10",
			AimId: "test/base-model-pinned",
			ModelSources: []aimv1alpha1.AIMModelSource{
				{ModelID: "test/model-fp8", SourceURI: "hf://my-org/finetuned-weights"},
			},
			Custom: &aimv1alpha1.AIMCustomModelSpec{VersionPolicy: aimv1alpha1.AIMVersionPolicyPinned},
		},
	}

	matches := []TemplateMatchResult{{
		OriginalAimId:   "test/base-model-pinned",
		OriginalModelId: "test/model-fp8",
		OriginalVersion: "0.1.10",
		MatchedModelSource: aimv1alpha1.AIMModelSource{
			ModelID:   "test/model-fp8",
			SourceURI: "hf://my-org/finetuned-weights",
		},
		Spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
			ModelName: "official-base",
			AimId:     "test/base-model-pinned",
			ModelId:   "test/model-fp8",
			ProfileId: "vllm-mi300x-fp16-tp1-latency",
			AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
				Hardware: &aimv1alpha1.AIMHardwareRequirements{
					GPU: &aimv1alpha1.AIMGpuRequirements{Model: "MI300X", Requests: 1},
				},
				// Metric / Precision deliberately omitted (source template
				// carries inline modelSources only — no discovered profile).
			},
		},
		// Profile built via buildProfileFromSpec for an inline-modelSources
		// template: non-nil but with no engineArgs/envVars.
		SourceProfile: &aimv1alpha1.AIMDiscoveredProfile{},
	}}

	tpl := BuildFineTunedServiceTemplates(context.Background(), newFakeClient(), model, matches)[0]

	if tpl.Spec.CustomProfile != nil {
		t.Errorf("expected CustomProfile to remain unset when source profile is empty, got %+v", tpl.Spec.CustomProfile)
	}
	// ProfileId should be preserved when no custom profile is being stamped;
	// otherwise we'd lose the link to the image's baked-in profile catalog.
	if tpl.Spec.ProfileId != "vllm-mi300x-fp16-tp1-latency" {
		t.Errorf("expected ProfileId to be preserved, got %q", tpl.Spec.ProfileId)
	}
}

func TestBuildFineTunedServiceTemplates_PreservesExistingCustomProfile(t *testing.T) {
	model := &aimv1alpha1.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "ft-llama", Namespace: "default"},
		Spec: aimv1alpha1.AIMModelSpec{
			Image: "ghcr.io/silogen/aim-base:0.11",
			AimId: "meta-llama/Llama-3.2-1B-Instruct",
			ModelSources: []aimv1alpha1.AIMModelSource{
				{ModelID: "meta-llama/Llama-3.2-1B-Instruct", SourceURI: "s3://bucket/weights/"},
			},
			Custom: &aimv1alpha1.AIMCustomModelSpec{VersionPolicy: aimv1alpha1.AIMVersionPolicyPinned},
		},
	}

	userArgs := &apiextensionsv1.JSON{Raw: []byte(`{"max-model-len":4096}`)}
	matches := []TemplateMatchResult{{
		OriginalAimId:   "meta-llama/Llama-3.2-1B-Instruct",
		OriginalModelId: "meta-llama/Llama-3.2-1B-Instruct",
		MatchedModelSource: aimv1alpha1.AIMModelSource{
			ModelID:   "meta-llama/Llama-3.2-1B-Instruct",
			SourceURI: "s3://bucket/weights/",
		},
		Spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
			ModelName: "official-base",
			AimId:     "meta-llama/Llama-3.2-1B-Instruct",
			ModelId:   "meta-llama/Llama-3.2-1B-Instruct",
			CustomProfile: &aimv1alpha1.AIMCustomProfile{
				EngineArgs: userArgs,
			},
		},
		SourceProfile: &aimv1alpha1.AIMDiscoveredProfile{
			EngineArgs: &apiextensionsv1.JSON{Raw: []byte(`{"tensor-parallel-size":1}`)},
		},
	}}

	tpl := BuildFineTunedServiceTemplates(context.Background(), newFakeClient(), model, matches)[0]

	if tpl.Spec.CustomProfile == nil ||
		string(tpl.Spec.CustomProfile.EngineArgs.Raw) != `{"max-model-len":4096}` {
		t.Errorf("expected pre-existing CustomProfile to be preserved, got %+v", tpl.Spec.CustomProfile)
	}
}

// Two matches whose source owners ship different base images produce two
// copies stamped with two different deployment-image annotations. This is the
// scenario that motivated moving image resolution onto each copy: a single
// patched model.spec.image cannot honor heterogeneous owners.
func TestBuildFineTunedClusterServiceTemplates_StampsAnnotationPerMatch(t *testing.T) {
	mi300 := clusterOwner("qwen3-mi300", "ghcr.io/silogen/qwen3:0.11.0", "ghcr.io/silogen/aim-base:0.11")
	epyc := clusterOwner("qwen3-epyc", "ghcr.io/silogen/qwen3-epyc:0.11.0", "ghcr.io/silogen/aim-epyc-base:0.11")

	model := &aimv1alpha1.AIMClusterModel{
		ObjectMeta: metav1.ObjectMeta{Name: "ft-qwen"},
		Spec: aimv1alpha1.AIMModelSpec{
			AimId:        "qwen/qwen3-32b",
			ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "qwen/qwen3-32b-fp8", SourceURI: "pvc://weights"}},
			Custom:       &aimv1alpha1.AIMCustomModelSpec{VersionPolicy: aimv1alpha1.AIMVersionPolicyAny},
		},
	}

	// Identical (aimId, modelId, version, precision, gpu) across the two
	// matches: only the source owner differs. The name generator must hash
	// owner identity, otherwise the two copies collide and one overwrites
	// the other.
	matches := []TemplateMatchResult{
		match("qwen3-mi300", "", "0.11.0"),
		match("qwen3-epyc", "", "0.11.0"),
	}

	c := newFakeClient(mi300, epyc)
	templates := BuildFineTunedClusterServiceTemplates(context.Background(), c, model, matches)
	if len(templates) != 2 {
		t.Fatalf("expected 2 templates, got %d", len(templates))
	}
	if templates[0].Name == templates[1].Name {
		t.Fatalf("template copies collided on name %q despite different source owners", templates[0].Name)
	}

	gotImages := map[string]bool{}
	for _, tpl := range templates {
		ref := tpl.Annotations[constants.AnnotationDeploymentImageRef]
		if ref == "" {
			t.Errorf("template %s missing deployment-image-ref annotation", tpl.Name)
		}
		gotImages[ref] = true
	}
	for _, want := range []string{"ghcr.io/silogen/aim-base:0.11", "ghcr.io/silogen/aim-epyc-base:0.11"} {
		if !gotImages[want] {
			t.Errorf("expected at least one template stamped with %q, got %v", want, gotImages)
		}
	}
}

// Matches whose source owners cannot be resolved are skipped (no copy
// applied), so a transiently-broken owner doesn't keep a stale, image-less
// copy alive on the server.
func TestBuildFineTunedClusterServiceTemplates_SkipsUnresolvableMatches(t *testing.T) {
	resolvable := clusterOwner("qwen3-resolvable", "ghcr.io/silogen/qwen3:0.11.0", "ghcr.io/silogen/aim-base:0.11")
	// "deferred" has no baseImageRef and a non-semver tag -> resolver gives up.
	deferred := clusterOwner("qwen3-deferred", "ghcr.io/silogen/qwen3:latest", "")

	model := &aimv1alpha1.AIMClusterModel{
		ObjectMeta: metav1.ObjectMeta{Name: "ft-qwen"},
		Spec: aimv1alpha1.AIMModelSpec{
			AimId:        "qwen/qwen3-32b",
			ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "qwen/qwen3-32b-fp8", SourceURI: "pvc://weights"}},
			Custom:       &aimv1alpha1.AIMCustomModelSpec{VersionPolicy: aimv1alpha1.AIMVersionPolicyAny},
		},
	}

	matches := []TemplateMatchResult{
		match("qwen3-resolvable", "", "0.11.0"),
		match("qwen3-deferred", "", "latest"),
	}

	c := newFakeClient(resolvable, deferred)
	templates := BuildFineTunedClusterServiceTemplates(context.Background(), c, model, matches)
	if len(templates) != 1 {
		t.Fatalf("expected 1 template (deferred match skipped), got %d", len(templates))
	}
	if got := templates[0].Annotations[constants.AnnotationDeploymentImageRef]; got != "ghcr.io/silogen/aim-base:0.11" {
		t.Errorf("unexpected stamped image: %q", got)
	}
}

// ============================================================================
// HELPER TESTS
// ============================================================================

// generateFineTunedTemplateName must disambiguate matches that differ only by
// source owner. Without owner identity in the hash, two siblings with the
// same (aimId, modelId, version, precision, gpu) — the heterogeneous-base
// case the per-copy annotation flow exists for — would collide on apply.
func TestGenerateFineTunedTemplateName_DisambiguatesByOwner(t *testing.T) {
	mkMatch := func(ownerName, ownerNs string) TemplateMatchResult {
		return TemplateMatchResult{
			OriginalAimId:   "qwen/qwen3-32b",
			OriginalModelId: "qwen/qwen3-32b-fp8",
			OriginalVersion: "0.11.0",
			Spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				ModelName: ownerName,
			},
			OwnerNamespace: ownerNs,
		}
	}

	cases := []struct {
		name string
		a, b TemplateMatchResult
	}{
		{
			name: "two cluster-scoped owners (same aimId, different base families)",
			a:    mkMatch("qwen3-mi300", ""),
			b:    mkMatch("qwen3-epyc", ""),
		},
		{
			name: "same owner name, different namespaces",
			a:    mkMatch("qwen3", "team-a"),
			b:    mkMatch("qwen3", "team-b"),
		},
		{
			name: "namespace-scoped vs cluster-scoped owner with same name",
			a:    mkMatch("qwen3", "team-a"),
			b:    mkMatch("qwen3", ""),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ftModel := "ft-qwen"
			na := generateFineTunedTemplateName(ftModel, tc.a)
			nb := generateFineTunedTemplateName(ftModel, tc.b)
			if na == nb {
				t.Fatalf("expected distinct names, both = %q", na)
			}
		})
	}
}

// Two calls with the same match must produce the same name (deterministic).
func TestGenerateFineTunedTemplateName_Deterministic(t *testing.T) {
	m := TemplateMatchResult{
		OriginalAimId:   "qwen/qwen3-32b",
		OriginalModelId: "qwen/qwen3-32b-fp8",
		OriginalVersion: "0.11.0",
		Spec:            aimv1alpha1.AIMServiceTemplateSpecCommon{ModelName: "qwen3-mi300"},
	}
	a := generateFineTunedTemplateName("ft-qwen", m)
	b := generateFineTunedTemplateName("ft-qwen", m)
	if a != b {
		t.Errorf("expected deterministic name, got %q vs %q", a, b)
	}
}

func TestGetVersionPolicy(t *testing.T) {
	tests := []struct {
		name     string
		spec     *aimv1alpha1.AIMModelSpec
		expected aimv1alpha1.AIMVersionPolicy
	}{
		{
			name:     "nil custom defaults to pinned",
			spec:     &aimv1alpha1.AIMModelSpec{},
			expected: aimv1alpha1.AIMVersionPolicyPinned,
		},
		{
			name: "empty policy defaults to pinned",
			spec: &aimv1alpha1.AIMModelSpec{
				Custom: &aimv1alpha1.AIMCustomModelSpec{},
			},
			expected: aimv1alpha1.AIMVersionPolicyPinned,
		},
		{
			name: "explicit latest",
			spec: &aimv1alpha1.AIMModelSpec{
				Custom: &aimv1alpha1.AIMCustomModelSpec{VersionPolicy: aimv1alpha1.AIMVersionPolicyLatest},
			},
			expected: aimv1alpha1.AIMVersionPolicyLatest,
		},
		{
			name: "explicit any",
			spec: &aimv1alpha1.AIMModelSpec{
				Custom: &aimv1alpha1.AIMCustomModelSpec{VersionPolicy: aimv1alpha1.AIMVersionPolicyAny},
			},
			expected: aimv1alpha1.AIMVersionPolicyAny,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getVersionPolicy(tt.spec)
			if got != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, got)
			}
		})
	}
}

func TestIsFineTunedModel(t *testing.T) {
	tests := []struct {
		name     string
		spec     aimv1alpha1.AIMModelSpec
		expected bool
	}{
		{
			name:     "no aimId no modelSources",
			spec:     aimv1alpha1.AIMModelSpec{Image: "test:v1"},
			expected: false,
		},
		{
			name:     "aimId but no modelSources",
			spec:     aimv1alpha1.AIMModelSpec{AimId: "qwen/qwen3-32b"},
			expected: false,
		},
		{
			name: "modelSources but no aimId",
			spec: aimv1alpha1.AIMModelSpec{
				ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "test/model", SourceURI: "s3://b/"}},
			},
			expected: false,
		},
		{
			name: "both aimId and modelSources",
			spec: aimv1alpha1.AIMModelSpec{
				AimId:        "qwen/qwen3-32b",
				ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "qwen/qwen3-32b-fp8", SourceURI: "s3://b/"}},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.spec.IsFineTunedModel()
			if got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}
