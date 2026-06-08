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

package aimservice

import (
	"encoding/json"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

func mustJSON(t *testing.T, v any) *apiextensionsv1.JSON {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal helper failed: %v", err)
	}
	return &apiextensionsv1.JSON{Raw: raw}
}

func sampleProfileSpec() *aimv1alpha2.AIMProfileSpecCommon {
	return &aimv1alpha2.AIMProfileSpecCommon{
		AimId:            "qwen/qwen3-32b",
		ModelId:          testModelIDFP8,
		Engine:           "vllm",
		Metric:           aimv1alpha1.AIMMetric("latency"),
		Precision:        aimv1alpha1.AIMPrecision("fp8"),
		Type:             aimv1alpha1.AIMProfileType("optimized"),
		AcceleratorModel: "MI300X",
		AcceleratorType:  aimv1alpha1.AcceleratorType("gpu"),
		AcceleratorCount: 1,
		Image:            "ghcr.io/aim/qwen3-32b:1.0.0",
	}
}

func TestProfileConfigMapName_Deterministic(t *testing.T) {
	a, err := profileConfigMapName("svc-alpha")
	if err != nil {
		t.Fatalf("profileConfigMapName returned error: %v", err)
	}
	b, err := profileConfigMapName("svc-alpha")
	if err != nil {
		t.Fatalf("profileConfigMapName returned error: %v", err)
	}
	if a != b {
		t.Fatalf("profileConfigMapName should be deterministic, got %q vs %q", a, b)
	}
	other, err := profileConfigMapName("svc-beta")
	if err != nil {
		t.Fatalf("profileConfigMapName returned error: %v", err)
	}
	if a == other {
		t.Fatalf("different service names should produce different configmap names")
	}
}

func TestProfileFilename(t *testing.T) {
	cases := []struct {
		name      string
		accModel  string
		precision string
		accCount  int32
		metric    string
		want      string
	}{
		{"standard gpu profile", "MI300X", "fp8", 1, "latency", "vllm-mi300x-fp8-tp1-latency.yaml"},
		{"multi gpu", "MI300X", "bf16", 8, "throughput", "vllm-mi300x-bf16-tp8-throughput.yaml"},
		{"cpu profile empty accelerator", "", "fp16", 0, "latency", "vllm-none-fp16-tp0-latency.yaml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := profileFilename(tc.accModel, tc.precision, tc.accCount, tc.metric)
			if got != tc.want {
				t.Fatalf("profileFilename: got %q want %q", got, tc.want)
			}
		})
	}
}

func TestAssembleProfileYAML_NilSpec(t *testing.T) {
	if _, _, err := assembleProfileYAML(nil); err == nil {
		t.Fatalf("expected error for nil spec")
	}
}

func TestAssembleProfileYAML_RoundTrip(t *testing.T) {
	spec := sampleProfileSpec()
	spec.EngineArgs = mustJSON(t, map[string]any{
		"max-model-len": 8192.0,
		"dtype":         "auto",
	})
	spec.EngineEnv = map[string]string{"VLLM_FOO": "bar"}

	yamlBytes, filename, err := assembleProfileYAML(spec)
	if err != nil {
		t.Fatalf("assembleProfileYAML error: %v", err)
	}
	if filename != "vllm-mi300x-fp8-tp1-latency.yaml" {
		t.Fatalf("unexpected filename: %q", filename)
	}

	var parsed profileYAML
	if err := yaml.Unmarshal(yamlBytes, &parsed); err != nil {
		t.Fatalf("unmarshal profile yaml: %v", err)
	}

	if parsed.AimID != "qwen/qwen3-32b" {
		t.Errorf("aim_id mismatch: %q", parsed.AimID)
	}
	if parsed.ModelID != testModelIDFP8 {
		t.Errorf("model_id mismatch: %q", parsed.ModelID)
	}
	if parsed.Metadata.AcceleratorModel != "MI300X" {
		t.Errorf("accelerator_model mismatch: %q", parsed.Metadata.AcceleratorModel)
	}
	if parsed.Metadata.AcceleratorType != "gpu" {
		t.Errorf("accelerator_type mismatch: %q", parsed.Metadata.AcceleratorType)
	}
	if parsed.Metadata.AcceleratorCount != 1 {
		t.Errorf("accelerator_count mismatch: %d", parsed.Metadata.AcceleratorCount)
	}
	// Legacy aliases must mirror the accelerator fields so the runtime in
	// amdenterpriseai/aim-base:0.11 (which requires `gpu` / `gpu_count`)
	// validates the assembled profile.
	if parsed.Metadata.GPU != "MI300X" {
		t.Errorf("gpu mismatch: %q", parsed.Metadata.GPU)
	}
	if parsed.Metadata.GPUCount != 1 {
		t.Errorf("gpu_count mismatch: %d", parsed.Metadata.GPUCount)
	}
	if parsed.Metadata.Engine != "vllm" {
		t.Errorf("engine mismatch: %q", parsed.Metadata.Engine)
	}
	if parsed.EngineArgs["dtype"] != "auto" {
		t.Errorf("engine_args dtype mismatch: %v", parsed.EngineArgs["dtype"])
	}
	if parsed.EnvVars["VLLM_FOO"] != "bar" {
		t.Errorf("env_vars VLLM_FOO mismatch: %q", parsed.EnvVars["VLLM_FOO"])
	}
}

// TestAssembleProfileYAML_RendersResolvedSpec asserts that assembleProfileYAML
// is a pure renderer of the spec it is given — it must not re-apply user
// overrides. With the overlay-materialisation flow, ComposeState already
// hands assembleProfileYAML the resolved (post-overrides) spec, so the
// function's job is only to project that spec into the runtime profile YAML.
// Override-merge correctness lives in the aimprofile package's
// ApplyProfileCopyOverrides tests.
func TestAssembleProfileYAML_RendersResolvedSpec(t *testing.T) {
	spec := sampleProfileSpec()
	spec.EngineArgs = mustJSON(t, map[string]any{
		"max-model-len":        8192.0,
		"dtype":                "float16",
		"tensor-parallel-size": 2.0,
	})

	yamlBytes, _, err := assembleProfileYAML(spec)
	if err != nil {
		t.Fatalf("assembleProfileYAML error: %v", err)
	}

	var parsed profileYAML
	if err := yaml.Unmarshal(yamlBytes, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got := parsed.EngineArgs["dtype"]; got != "float16" {
		t.Errorf("dtype mismatch: got %v", got)
	}
	if _, ok := parsed.EngineArgs["max-model-len"]; !ok {
		t.Errorf("max-model-len should round-trip from spec")
	}
	if _, ok := parsed.EngineArgs["tensor-parallel-size"]; !ok {
		t.Errorf("tensor-parallel-size should round-trip from spec")
	}
}

func TestBuildProfileConfigMap(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
	}
	spec := sampleProfileSpec()

	cmName, err := profileConfigMapName(service.Name)
	if err != nil {
		t.Fatalf("profileConfigMapName returned error: %v", err)
	}
	yamlBytes, filename, err := assembleProfileYAML(spec)
	if err != nil {
		t.Fatalf("assembleProfileYAML returned error: %v", err)
	}

	cm := buildProfileConfigMap(service, cmName, filename, yamlBytes)
	if cm == nil {
		t.Fatalf("buildProfileConfigMap returned nil")
	}
	if cm.Namespace != "ns" {
		t.Errorf("unexpected namespace: %q", cm.Namespace)
	}
	if cm.Labels[constants.LabelService] != "svc" {
		t.Errorf("missing or incorrect service label: %v", cm.Labels)
	}
	if cm.Labels[constants.LabelK8sManagedBy] != constants.LabelValueManagedBy {
		t.Errorf("missing managed-by label: %v", cm.Labels)
	}
	if len(cm.Data) != 1 {
		t.Fatalf("expected exactly one data key, got %d", len(cm.Data))
	}
	for key := range cm.Data {
		if !strings.HasSuffix(key, ".yaml") {
			t.Errorf("data key should be a .yaml filename: %q", key)
		}
	}
}

func TestBuildProfileVolumeMount(t *testing.T) {
	mount := BuildProfileVolumeMount("qwen/qwen3-32b")
	if !strings.HasPrefix(mount.MountPath, profileMountBase+"/") {
		t.Errorf("mount path should be under %s, got %q", profileMountBase, mount.MountPath)
	}
	if !mount.ReadOnly {
		t.Errorf("profile mount should be read-only")
	}
}
