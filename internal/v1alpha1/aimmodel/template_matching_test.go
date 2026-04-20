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
	"testing"

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

	templates := BuildFineTunedServiceTemplates(model, matches)
	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}

	tpl := templates[0]

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

// ============================================================================
// HELPER TESTS
// ============================================================================

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

func TestSortByVersionDesc(t *testing.T) {
	versions := []string{"0.7.0", "0.9.0", "0.8.5", "1.0.0"}
	sortByVersionDesc(versions)

	expected := []string{"1.0.0", "0.9.0", "0.8.5", "0.7.0"}
	for i, v := range versions {
		if v != expected[i] {
			t.Errorf("index %d: expected %s, got %s", i, expected[i], v)
		}
	}
}
