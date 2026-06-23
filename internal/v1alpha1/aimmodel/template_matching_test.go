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

	corev1 "k8s.io/api/core/v1"
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
							Model:    testGPUModel,
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
							Model:    testGPUModel,
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

func TestFilterTemplatesByVersion_All(t *testing.T) {
	templates := []templateVersionAccessor{
		{&aimv1alpha1.AIMServiceTemplate{Status: aimv1alpha1.AIMServiceTemplateStatus{Version: "0.8.5"}}},
		{&aimv1alpha1.AIMServiceTemplate{Status: aimv1alpha1.AIMServiceTemplateStatus{Version: "0.9.0"}}},
	}

	// `all` is the canonical spelling; it must behave identically to the
	// deprecated `any` alias and return every template.
	result := FilterTemplatesByVersion(templates, aimv1alpha1.AIMVersionPolicyAll, "")
	if len(result) != 2 {
		t.Fatalf("expected all 2 templates for all policy, got %d", len(result))
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
			},
			SourceProfile: &aimv1alpha1.AIMDiscoveredProfile{
				EngineArgs: &apiextensionsv1.JSON{Raw: []byte(`{"tensor-parallel-size":1}`)},
				Metadata: aimv1alpha1.AIMProfileMetadata{
					AimID:     "qwen/qwen3-32b",
					ModelID:   "qwen/qwen3-32b-fp8",
					Engine:    "vllm",
					GPU:       testGPUModel,
					GPUCount:  4,
					Metric:    aimv1alpha1.AIMMetric("latency"),
					Precision: aimv1alpha1.AIMPrecision("fp8"),
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
	if tpl.Spec.ModelName != "my-finetuned-qwen" {
		t.Errorf("expected modelName=my-finetuned-qwen, got %s", tpl.Spec.ModelName)
	}
	if tpl.Namespace != "default" {
		t.Errorf("expected namespace=default, got %s", tpl.Namespace)
	}

	if len(tpl.Spec.ModelSources) != 1 {
		t.Fatalf("expected 1 model source, got %d", len(tpl.Spec.ModelSources))
	}
	if tpl.Spec.ModelSources[0].SourceURI != "s3://my-bucket/weights/" {
		t.Errorf("expected custom sourceUri, got %s", tpl.Spec.ModelSources[0].SourceURI)
	}

	// Hardware comes from status.profile.metadata, not match.Spec — that's
	// the whole point of building from discovery output.
	if tpl.Spec.Hardware == nil || tpl.Spec.Hardware.GPU == nil {
		t.Fatal("expected hardware to be derived from discovered metadata")
	}
	if tpl.Spec.Hardware.GPU.Model != testGPUModel {
		t.Errorf("expected GPU model MI300X, got %s", tpl.Spec.Hardware.GPU.Model)
	}
	if tpl.Spec.Hardware.GPU.Requests != 4 {
		t.Errorf("expected 4 GPU requests, got %d", tpl.Spec.Hardware.GPU.Requests)
	}

	if tpl.Labels[constants.LabelKeyModel] != "my-finetuned-qwen" {
		t.Errorf("expected model label=my-finetuned-qwen, got %s", tpl.Labels[constants.LabelKeyModel])
	}
	if tpl.Labels[constants.LabelKeyOrigin] != LabelValueOriginFineTuned {
		t.Errorf("expected origin label=%s, got %s", LabelValueOriginFineTuned, tpl.Labels[constants.LabelKeyOrigin])
	}

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
					GPU: &aimv1alpha1.AIMGpuRequirements{Model: testGPUModel, Requests: 1},
				},
			},
		},
		SourceProfile: &aimv1alpha1.AIMDiscoveredProfile{
			EngineArgs: engineArgs,
			EnvVars:    envVars,
			Metadata:   discoveredMeta("meta-llama/Llama-3.2-1B-Instruct", "meta-llama/Llama-3.2-1B-Instruct"),
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
	// ProfileId is left unset so the kserve builder doesn't emit a built-in
	// AIM_PROFILE_ID that conflicts with the custom one — the rebased
	// aim-base image has no baked-in catalog to resolve it against.
	if tpl.Spec.ProfileId != "" {
		t.Errorf("expected ProfileId to be empty, got %q", tpl.Spec.ProfileId)
	}
}

// TestBuildFineTunedServiceTemplates_MergesModelEnvIntoTemplateSpecEnv
// reproduces the dash-app-dev failure where an AIMModel pointing at a
// custom s3:// source needs AWS credentials to reach the download /
// check-size pods of the resulting AIMArtifact. The credentials are
// declared on the fine-tuned AIMModel's spec.env, and we expect them
// to flow through to the cloned template's spec.Env in the same way as
// for non-fine-tuned models (buildAutoGeneratedCustomTemplate /
// buildCustomServiceTemplate), so they reach both the runtime container
// and the downloader. Per-source overrides
// (AIMModel.spec.modelSources[].env) carried on match.MatchedModelSource
// continue to flow through unchanged so they can override individual
// vars at the downloader.
func TestBuildFineTunedServiceTemplates_MergesModelEnvIntoTemplateSpecEnv(t *testing.T) {
	awsKey := corev1.EnvVar{
		Name: "AWS_ACCESS_KEY_ID",
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "minio-credentials"},
				Key:                  "minio-access-key",
			},
		},
	}
	awsEndpoint := corev1.EnvVar{
		Name:  "AWS_ENDPOINT_URL",
		Value: "http://minio.example.svc:80",
	}

	model := &aimv1alpha1.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "ft-llama", Namespace: "default"},
		Spec: aimv1alpha1.AIMModelSpec{
			Image: "ghcr.io/silogen/aim-base:0.11",
			AimId: "meta-llama/Llama-3.2-1B-Instruct",
			Env:   []corev1.EnvVar{awsKey, awsEndpoint},
			ModelSources: []aimv1alpha1.AIMModelSource{
				{
					ModelID:   "meta-llama/Llama-3.2-1B-Instruct",
					SourceURI: "s3://bucket/finetuned/checkpoint-final",
					// Per-source override: at the downloader, this
					// AWS_ENDPOINT_URL wins over the model-level value
					// via aimtemplatecache/reconcile.go's cache.Env merge.
					Env: []corev1.EnvVar{{Name: "AWS_ENDPOINT_URL", Value: "http://override.svc:80"}},
				},
			},
			Custom: &aimv1alpha1.AIMCustomModelSpec{VersionPolicy: aimv1alpha1.AIMVersionPolicyPinned},
		},
	}

	matches := []TemplateMatchResult{{
		OriginalAimId:   "meta-llama/Llama-3.2-1B-Instruct",
		OriginalModelId: "meta-llama/Llama-3.2-1B-Instruct",
		OriginalVersion: "0.11.0",
		MatchedModelSource: aimv1alpha1.AIMModelSource{
			ModelID:   "meta-llama/Llama-3.2-1B-Instruct",
			SourceURI: "s3://bucket/finetuned/checkpoint-final",
			Env:       []corev1.EnvVar{{Name: "AWS_ENDPOINT_URL", Value: "http://override.svc:80"}},
		},
		Spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
			ModelName: "official-base",
			// Source template carries an unrelated runtime env var that
			// is preserved on the cloned template, plus an AWS_ENDPOINT_URL
			// that the fine-tuned model is expected to override.
			Env: []corev1.EnvVar{
				{Name: "VLLM_ROCM_USE_AITER", Value: "1"},
				{Name: "AWS_ENDPOINT_URL", Value: "http://base-template-default.svc:80"},
			},
		},
		SourceProfile: &aimv1alpha1.AIMDiscoveredProfile{
			EngineArgs: &apiextensionsv1.JSON{Raw: []byte(`{"tensor-parallel-size":1}`)},
			Metadata:   discoveredMeta("meta-llama/Llama-3.2-1B-Instruct", "meta-llama/Llama-3.2-1B-Instruct"),
		},
	}}

	tpl := BuildFineTunedServiceTemplates(context.Background(), newFakeClient(), model, matches)[0]

	envByName := map[string]corev1.EnvVar{}
	for _, e := range tpl.Spec.Env {
		envByName[e.Name] = e
	}

	// Fine-tuned model's spec.env wins over source template on collision.
	if got, ok := envByName["AWS_ENDPOINT_URL"]; !ok {
		t.Errorf("expected AWS_ENDPOINT_URL on tpl.Spec.Env, got %+v", tpl.Spec.Env)
	} else if got.Value != "http://minio.example.svc:80" {
		t.Errorf("expected fine-tuned model's spec.env to win on collision, got %q", got.Value)
	}

	// New names from fine-tuned model are added (with valueFrom intact).
	if got, ok := envByName["AWS_ACCESS_KEY_ID"]; !ok {
		t.Errorf("expected AWS_ACCESS_KEY_ID on tpl.Spec.Env, got %+v", tpl.Spec.Env)
	} else if got.ValueFrom == nil || got.ValueFrom.SecretKeyRef == nil ||
		got.ValueFrom.SecretKeyRef.Name != "minio-credentials" {
		t.Errorf("expected AWS_ACCESS_KEY_ID to retain secret ref, got %+v", got)
	}

	// Source template vars not collided with are preserved.
	if got, ok := envByName["VLLM_ROCM_USE_AITER"]; !ok || got.Value != "1" {
		t.Errorf("expected source template's VLLM_ROCM_USE_AITER to be preserved, got %+v", tpl.Spec.Env)
	}

	// Per-source env continues to flow through unchanged on
	// modelSources[0].env so the downloader can still override
	// individual vars per-source via cache.Env in aimtemplatecache.
	if len(tpl.Spec.ModelSources) != 1 {
		t.Fatalf("expected 1 model source, got %d", len(tpl.Spec.ModelSources))
	}
	srcEnv := tpl.Spec.ModelSources[0].Env
	if len(srcEnv) != 1 || srcEnv[0].Name != "AWS_ENDPOINT_URL" || srcEnv[0].Value != "http://override.svc:80" {
		t.Errorf("expected per-source AWS_ENDPOINT_URL override to be preserved, got %+v", srcEnv)
	}
}

// discoveredMeta returns a fully-populated AIMProfileMetadata fixture for
// tests that just need the source's discovery output to be "complete enough"
// for buildFineTunedTemplateSpec to produce a copy. Tests that exercise
// individual fields should set them explicitly instead.
func discoveredMeta(aimID, modelID string) aimv1alpha1.AIMProfileMetadata {
	return aimv1alpha1.AIMProfileMetadata{
		AimID:     aimID,
		ModelID:   modelID,
		Engine:    "vllm",
		GPU:       testGPUModel,
		GPUCount:  1,
		Metric:    aimv1alpha1.AIMMetric("latency"),
		Precision: aimv1alpha1.AIMPrecision("fp16"),
	}
}

// TestBuildFineTunedServiceTemplates_BuildsFromProfileMetadata reproduces
// the real-world scenario observed against the dash-app-dev cluster where
// a catalog AIMClusterServiceTemplate (e.g. the meta-llama/Llama-3.2-1B
// 0.11.0 latency variant) has spec.precision=nil because the source image's
// `model.recommendedDeployments` OCI label entry didn't include precision.
// Discovery then populated status.profile.metadata.precision="fp16" but
// nothing promoted it back onto spec.
//
// The cloner has to lean on status.profile.metadata for identity / shape
// fields, otherwise the apply gets rejected by the API server with the CEL
// rule on AIMServiceTemplateSpecCommon:
//
//	"when customProfile is set, aimId, modelId, hardware, metric, and
//	 precision are required"
func TestBuildFineTunedServiceTemplates_BuildsFromProfileMetadata(t *testing.T) {
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

	// Source spec mirrors the catalog template observed on cluster: it
	// has aimId/modelId/hardware/metric and a profileId pointing at the
	// baked-in catalog, but spec.precision is nil. The discovered profile
	// metadata is fully populated though — that's what the cloner uses.
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
			ProfileId: "vllm-mi300x-fp16-tp1-latency",
		},
		SourceProfile: &aimv1alpha1.AIMDiscoveredProfile{
			EngineArgs: &apiextensionsv1.JSON{Raw: []byte(`{"tensor-parallel-size":1}`)},
			EnvVars:    map[string]string{"HIP_FORCE_DEV_KERNARG": "1"},
			Metadata: aimv1alpha1.AIMProfileMetadata{
				AimID:     "meta-llama/Llama-3.2-1B-Instruct",
				ModelID:   "meta-llama/Llama-3.2-1B-Instruct",
				Engine:    "vllm",
				GPU:       testGPUModel,
				GPUCount:  1,
				Metric:    aimv1alpha1.AIMMetric("latency"),
				Precision: aimv1alpha1.AIMPrecision("fp16"),
				Type:      aimv1alpha1.AIMProfileTypeUnoptimized,
			},
		},
	}}

	tpl := BuildFineTunedServiceTemplates(context.Background(), newFakeClient(), model, matches)[0]

	// CustomProfile carries the discovered engine config across to the
	// rebased aim-base image (which has no baked-in catalog).
	if tpl.Spec.CustomProfile == nil {
		t.Fatal("expected CustomProfile to be populated from source profile")
	}

	// Every CEL-required field must end up populated on the cloned spec.
	if tpl.Spec.AimId != "meta-llama/Llama-3.2-1B-Instruct" {
		t.Errorf("expected AimId from metadata, got %q", tpl.Spec.AimId)
	}
	if tpl.Spec.ModelId != "meta-llama/Llama-3.2-1B-Instruct" {
		t.Errorf("expected ModelId from metadata, got %q", tpl.Spec.ModelId)
	}
	if tpl.Spec.Hardware == nil || tpl.Spec.Hardware.GPU == nil ||
		tpl.Spec.Hardware.GPU.Model != testGPUModel {
		t.Errorf("expected Hardware from metadata, got %+v", tpl.Spec.Hardware)
	}
	if tpl.Spec.Metric == nil || *tpl.Spec.Metric != "latency" {
		t.Errorf("expected Metric from metadata, got %+v", tpl.Spec.Metric)
	}
	if tpl.Spec.Precision == nil || *tpl.Spec.Precision != "fp16" {
		t.Errorf("expected Precision from metadata, got %+v", tpl.Spec.Precision)
	}
	// ProfileId from the source spec is dropped — it points at the source
	// image's baked-in catalog, which the rebased aim-base image doesn't
	// ship; keeping it would emit a conflicting AIM_PROFILE_ID.
	if tpl.Spec.ProfileId != "" {
		t.Errorf("expected ProfileId to be empty on the clone, got %q", tpl.Spec.ProfileId)
	}
}

// TestBuildFineTunedServiceTemplates_DefersWhenDiscoveryNotReady covers the
// deferral contract: if the source template's discovery hasn't produced any
// data yet, no template is emitted. A subsequent reconcile (triggered when
// the source's status updates) retries. Metadata is treated atomically —
// once any field is populated we trust the profile is as complete as it
// will be.
func TestBuildFineTunedServiceTemplates_DefersWhenDiscoveryNotReady(t *testing.T) {
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

	baseMatch := TemplateMatchResult{
		OriginalAimId:   "meta-llama/Llama-3.2-1B-Instruct",
		OriginalModelId: "meta-llama/Llama-3.2-1B-Instruct",
		OriginalVersion: "0.11.0",
		MatchedModelSource: aimv1alpha1.AIMModelSource{
			ModelID:   "meta-llama/Llama-3.2-1B-Instruct",
			SourceURI: "s3://bucket/weights/",
		},
		Spec: aimv1alpha1.AIMServiceTemplateSpecCommon{ModelName: "official-base"},
	}

	cases := []struct {
		name    string
		profile *aimv1alpha1.AIMDiscoveredProfile
	}{
		{
			name:    "no source profile",
			profile: nil,
		},
		{
			name:    "empty profile",
			profile: &aimv1alpha1.AIMDiscoveredProfile{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := baseMatch
			m.SourceProfile = tc.profile
			templates := BuildFineTunedServiceTemplates(
				context.Background(), newFakeClient(), model, []TemplateMatchResult{m})
			if len(templates) != 0 {
				t.Errorf("expected deferral (0 templates), got %d", len(templates))
			}
		})
	}
}

// TestBuildFineTunedServiceTemplates_InlineModelSourcesNoEngineConfig covers
// the inline-modelSources flow: the source template has no discovery job, so
// status.profile carries identity / shape metadata synthesised from spec but
// no engineArgs / envVars. The clone gets the metadata fields but no
// customProfile (the rebased image is the same image family — the runtime
// resolves the original profile against the unchanged catalog).
func TestBuildFineTunedServiceTemplates_InlineModelSourcesNoEngineConfig(t *testing.T) {
	model := &aimv1alpha1.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "ft-dummy", Namespace: "default"},
		Spec: aimv1alpha1.AIMModelSpec{
			Image: "aim-dummy:0.1.10",
			AimId: "test/cluster-base-model",
			ModelSources: []aimv1alpha1.AIMModelSource{
				{ModelID: "test/model-fp8", SourceURI: "hf://my-org/cluster-finetuned-weights"},
			},
			Custom: &aimv1alpha1.AIMCustomModelSpec{VersionPolicy: aimv1alpha1.AIMVersionPolicyPinned},
		},
	}

	matches := []TemplateMatchResult{{
		OriginalAimId:   "test/cluster-base-model",
		OriginalModelId: "test/model-fp8",
		OriginalVersion: "0.1.10",
		MatchedModelSource: aimv1alpha1.AIMModelSource{
			ModelID:   "test/model-fp8",
			SourceURI: "hf://my-org/cluster-finetuned-weights",
		},
		Spec: aimv1alpha1.AIMServiceTemplateSpecCommon{ModelName: "official-base"},
		// Mirrors what buildProfileFromSpec emits for inline-modelSources
		// templates: identity / hardware from spec, no engine config.
		SourceProfile: &aimv1alpha1.AIMDiscoveredProfile{
			Metadata: aimv1alpha1.AIMProfileMetadata{
				AimID:    "test/cluster-base-model",
				ModelID:  "test/model-fp8",
				GPU:      testGPUModel,
				GPUCount: 1,
			},
		},
	}}

	tpl := BuildFineTunedServiceTemplates(context.Background(), newFakeClient(), model, matches)[0]

	if tpl.Spec.CustomProfile != nil {
		t.Errorf("expected no CustomProfile (inline-modelSources source has no engine config), got %+v", tpl.Spec.CustomProfile)
	}
	if tpl.Spec.AimId != "test/cluster-base-model" {
		t.Errorf("expected AimId from metadata, got %q", tpl.Spec.AimId)
	}
	if tpl.Spec.ModelId != "test/model-fp8" {
		t.Errorf("expected ModelId from metadata, got %q", tpl.Spec.ModelId)
	}
	if tpl.Spec.Hardware == nil || tpl.Spec.Hardware.GPU == nil ||
		tpl.Spec.Hardware.GPU.Model != testGPUModel || tpl.Spec.Hardware.GPU.Requests != 1 {
		t.Errorf("expected hardware from metadata, got %+v", tpl.Spec.Hardware)
	}
	if tpl.Spec.Metric != nil {
		t.Errorf("expected nil Metric (inline source has no metric), got %+v", tpl.Spec.Metric)
	}
	if tpl.Spec.Precision != nil {
		t.Errorf("expected nil Precision (inline source has no precision), got %+v", tpl.Spec.Precision)
	}
}

// TestBuildFineTunedServiceTemplates_DefersWhenCustomProfileWouldTripCEL
// guards against a misbehaving (or future / third-party) discovery image
// emitting engineArgs alongside partial metadata. aim-build's
// ProfileMetadata schema requires all fields together, but if that
// invariant ever breaks we want a clean deferral rather than an apply
// the apiserver rejects on the CEL rule:
//
//	"when customProfile is set, aimId, modelId, hardware, metric, and
//	 precision are required"
func TestBuildFineTunedServiceTemplates_DefersWhenCustomProfileWouldTripCEL(t *testing.T) {
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

	baseMatch := TemplateMatchResult{
		OriginalAimId:   "meta-llama/Llama-3.2-1B-Instruct",
		OriginalModelId: "meta-llama/Llama-3.2-1B-Instruct",
		OriginalVersion: "0.11.0",
		MatchedModelSource: aimv1alpha1.AIMModelSource{
			ModelID:   "meta-llama/Llama-3.2-1B-Instruct",
			SourceURI: "s3://bucket/weights/",
		},
		Spec: aimv1alpha1.AIMServiceTemplateSpecCommon{ModelName: "official-base"},
	}

	// Engine config present (would stamp customProfile), metadata is
	// non-empty (passes discoveryReady) but each variant is missing one
	// of the CEL-required fields.
	cases := []struct {
		name     string
		metadata aimv1alpha1.AIMProfileMetadata
	}{
		{
			name: "missing precision",
			metadata: aimv1alpha1.AIMProfileMetadata{
				AimID:    "meta-llama/Llama-3.2-1B-Instruct",
				ModelID:  "meta-llama/Llama-3.2-1B-Instruct",
				GPU:      testGPUModel,
				GPUCount: 1,
				Metric:   aimv1alpha1.AIMMetric("latency"),
			},
		},
		{
			name: "missing metric",
			metadata: aimv1alpha1.AIMProfileMetadata{
				AimID:     "meta-llama/Llama-3.2-1B-Instruct",
				ModelID:   "meta-llama/Llama-3.2-1B-Instruct",
				GPU:       testGPUModel,
				GPUCount:  1,
				Precision: aimv1alpha1.AIMPrecision("fp16"),
			},
		},
		{
			name: "missing hardware",
			metadata: aimv1alpha1.AIMProfileMetadata{
				AimID:     "meta-llama/Llama-3.2-1B-Instruct",
				ModelID:   "meta-llama/Llama-3.2-1B-Instruct",
				Metric:    aimv1alpha1.AIMMetric("latency"),
				Precision: aimv1alpha1.AIMPrecision("fp16"),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := baseMatch
			m.SourceProfile = &aimv1alpha1.AIMDiscoveredProfile{
				EngineArgs: &apiextensionsv1.JSON{Raw: []byte(`{"tensor-parallel-size":1}`)},
				Metadata:   tc.metadata,
			}
			templates := BuildFineTunedServiceTemplates(
				context.Background(), newFakeClient(), model, []TemplateMatchResult{m})
			if len(templates) != 0 {
				t.Errorf("expected deferral (0 templates) for partial-metadata + customProfile, got %d", len(templates))
			}
		})
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
	metric := aimv1alpha1.AIMMetric("latency")
	precision := aimv1alpha1.AIMPrecision("fp8")
	cloned := aimv1alpha1.AIMServiceTemplateSpecCommon{
		AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
			Metric:    &metric,
			Precision: &precision,
			Hardware: &aimv1alpha1.AIMHardwareRequirements{
				GPU: &aimv1alpha1.AIMGpuRequirements{Model: testGPUModel, Requests: 1},
			},
		},
	}
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
			na := generateFineTunedTemplateName(ftModel, tc.a, cloned)
			nb := generateFineTunedTemplateName(ftModel, tc.b, cloned)
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
	metric := aimv1alpha1.AIMMetric("latency")
	precision := aimv1alpha1.AIMPrecision("fp8")
	cloned := aimv1alpha1.AIMServiceTemplateSpecCommon{
		AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
			Metric:    &metric,
			Precision: &precision,
			Hardware: &aimv1alpha1.AIMHardwareRequirements{
				GPU: &aimv1alpha1.AIMGpuRequirements{Model: testGPUModel, Requests: 1},
			},
		},
	}
	a := generateFineTunedTemplateName("ft-qwen", m, cloned)
	b := generateFineTunedTemplateName("ft-qwen", m, cloned)
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
