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
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/profileyaml"
)

const testVariant = "usp4"

func TestRuntimeYAMLHardwareOverridesRespectSourcePresence(t *testing.T) {
	contract := sourceContract(t, "  accelerator_vendor: amd\n  accelerator_partitioning_mode: unpartitioned\n")
	spec := sampleSpec()
	spec.AcceleratorVendor = aimv1alpha1.AcceleratorVendorNVIDIA
	spec.AcceleratorPartitioningMode = "CPX-NPS4"
	raw, _, err := AssembleProfileYAMLForContract(spec, contract)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	metadata := doc["metadata"].(map[string]any)
	if metadata["accelerator_vendor"] != "nvidia" || metadata["accelerator_partitioning_mode"] != "CPX-NPS4" {
		t.Fatalf("source extension values escaped overrides: %v", metadata)
	}
	raw, _, err = AssembleProfileYAMLForContract(spec, profileyaml.DefaultContract())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "accelerator_vendor") || strings.Contains(string(raw), "accelerator_partitioning_mode") {
		t.Fatal("renderer added fields absent from the source schema")
	}
}

func sampleSpec() *aimv1alpha2.AIMProfileSpecCommon {
	return &aimv1alpha2.AIMProfileSpecCommon{
		AimId:            "qwen/qwen3-32b",
		ModelId:          "qwen/qwen3-32b-fp8",
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

func sourceContract(t *testing.T, optionalMetadata string) profileyaml.Contract {
	t.Helper()
	contract, err := profileyaml.Inspect([]byte(`metadata:
  engine: vllm
  metric: latency
  precision: fp8
  type: optimized
` + optionalMetadata))
	if err != nil {
		t.Fatalf("Inspect source contract: %v", err)
	}
	return contract
}

func TestProfileFilename(t *testing.T) {
	cases := []struct {
		name string
		spec *aimv1alpha2.AIMProfileSpecCommon
		want string
	}{
		{
			name: "discovered profile id is authoritative",
			spec: &aimv1alpha2.AIMProfileSpecCommon{
				ProfileId: "vllm_omni-mi300x-fp16-tp4-latency-usp4",
				Engine:    "vllm_omni",
				Variant:   testVariant,
			},
			want: "vllm_omni-mi300x-fp16-tp4-latency-usp4.yaml",
		},
		{
			name: "engine-aware fallback includes variant",
			spec: &aimv1alpha2.AIMProfileSpecCommon{
				Engine:           "vllm_omni",
				Variant:          testVariant,
				AcceleratorModel: "MI300X",
				Precision:        aimv1alpha1.AIMPrecision("fp16"),
				AcceleratorCount: 4,
				Metric:           aimv1alpha1.AIMMetric("latency"),
			},
			want: "vllm_omni-mi300x-fp16-tp4-latency-usp4.yaml",
		},
		{
			name: "empty engine preserves legacy vllm fallback",
			spec: &aimv1alpha2.AIMProfileSpecCommon{
				Precision: aimv1alpha1.AIMPrecision("fp16"),
				Metric:    aimv1alpha1.AIMMetric("latency"),
			},
			want: "vllm-none-fp16-tp0-latency.yaml",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ProfileFilename(tc.spec)
			if err != nil {
				t.Fatalf("ProfileFilename error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("ProfileFilename = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProfileFilename_InvalidDiscoveredID(t *testing.T) {
	_, err := ProfileFilename(&aimv1alpha2.AIMProfileSpecCommon{ProfileId: "nested/profile"})
	if err == nil {
		t.Fatal("expected invalid ConfigMap key error")
	}
}

func TestAssembleProfileYAML_NilSpec(t *testing.T) {
	if _, _, err := AssembleProfileYAML(nil); err == nil {
		t.Fatalf("expected error for nil spec")
	}
}

func TestAssembleProfileYAML_PreservesWanIdentity(t *testing.T) {
	spec := sampleSpec()
	spec.ProfileId = "vllm_omni-mi300x-fp16-tp4-latency-usp4"
	spec.Engine = "vllm_omni"
	spec.Variant = testVariant
	spec.Precision = aimv1alpha1.AIMPrecision("fp16")
	spec.AcceleratorCount = 4

	yamlBytes, filename, err := AssembleProfileYAML(spec)
	if err != nil {
		t.Fatalf("AssembleProfileYAML error: %v", err)
	}
	if filename != spec.ProfileId+".yaml" {
		t.Fatalf("filename = %q, want %q", filename, spec.ProfileId+".yaml")
	}

	var parsed ProfileYAML
	if err := yaml.Unmarshal(yamlBytes, &parsed); err != nil {
		t.Fatalf("unmarshal profile yaml: %v", err)
	}
	if parsed.Metadata.Variant != testVariant {
		t.Errorf("metadata.variant = %q, want usp4", parsed.Metadata.Variant)
	}
}

func TestAssembleProfileYAMLForContract_EmitsSourceFieldSet(t *testing.T) {
	spec := sampleSpec()
	spec.ManualSelectionOnly = true //nolint:staticcheck // Compatibility serialization is under test.

	cases := []struct {
		name     string
		contract profileyaml.Contract
		present  []string
		absent   []string
	}{
		{
			name:     "legacy GPU schema",
			contract: sourceContract(t, "  gpu: MI300X\n  gpu_count: 1\n  manual_selection_only: true\n"),
			present:  []string{"gpu", "gpu_count", "manual_selection_only"},
			absent:   []string{"accelerator_model", "accelerator_type", "accelerator_count"},
		},
		{
			name:     "transitional accelerator schema",
			contract: sourceContract(t, "  accelerator_model: MI300X\n  accelerator_type: gpu\n  accelerator_count: 1\n  manual_selection_only: true\n"),
			present:  []string{"accelerator_model", "accelerator_type", "accelerator_count", "manual_selection_only"},
			absent:   []string{"gpu", "gpu_count"},
		},
		{
			name:     "strict accelerator schema",
			contract: profileyaml.DefaultContract(),
			present:  []string{"accelerator_model", "accelerator_type", "accelerator_count"},
			absent:   []string{"gpu", "gpu_count", "manual_selection_only"},
		},
		{
			name:     "accelerator-free source schema",
			contract: sourceContract(t, "  manual_selection_only: true\n"),
			present:  []string{"manual_selection_only"},
			absent: []string{
				"gpu", "gpu_count",
				"accelerator_model", "accelerator_type", "accelerator_count",
			},
		},
		{
			name:     "mixed source schema",
			contract: sourceContract(t, "  gpu: MI300X\n  gpu_count: 1\n  accelerator_model: MI300X\n  accelerator_type: gpu\n  accelerator_count: 1\n  manual_selection_only: true\n"),
			present: []string{
				"gpu", "gpu_count",
				"accelerator_model", "accelerator_type", "accelerator_count",
				"manual_selection_only",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			yamlBytes, _, err := AssembleProfileYAMLForContract(spec, tc.contract)
			if err != nil {
				t.Fatalf("AssembleProfileYAMLForContract error: %v", err)
			}

			var decoded struct {
				Metadata map[string]any `json:"metadata"`
			}
			if err := yaml.Unmarshal(yamlBytes, &decoded); err != nil {
				t.Fatalf("unmarshal profile YAML: %v", err)
			}
			for _, key := range tc.present {
				if _, ok := decoded.Metadata[key]; !ok {
					t.Errorf("metadata missing %q:\n%s", key, yamlBytes)
				}
			}
			for _, key := range tc.absent {
				if _, ok := decoded.Metadata[key]; ok {
					t.Errorf("metadata unexpectedly contains %q:\n%s", key, yamlBytes)
				}
			}
			if tc.contract.HasMetadataField("manual_selection_only") {
				if got, ok := decoded.Metadata["manual_selection_only"].(bool); !ok || !got {
					t.Errorf("manual_selection_only = %#v, want true", decoded.Metadata["manual_selection_only"])
				}
			}
		})
	}
}

func TestAssembleProfileYAMLForContract_DoesNotIntroduceAbsentOptionalFields(t *testing.T) {
	spec := sampleSpec()
	spec.Variant = testVariant
	spec.Features = []string{"adapters"}
	contract := sourceContract(t, "  accelerator_model: MI300X\n  accelerator_type: gpu\n  accelerator_count: 1\n")

	yamlBytes, _, err := AssembleProfileYAMLForContract(spec, contract)
	if err != nil {
		t.Fatalf("AssembleProfileYAMLForContract: %v", err)
	}
	var decoded struct {
		Metadata map[string]any `json:"metadata"`
	}
	if err := yaml.Unmarshal(yamlBytes, &decoded); err != nil {
		t.Fatalf("unmarshal profile YAML: %v", err)
	}
	for _, field := range []string{"variant", "features"} {
		if _, ok := decoded.Metadata[field]; ok {
			t.Errorf("projected YAML introduced absent source field %q:\n%s", field, yamlBytes)
		}
	}
}

func TestAssembleProfileYAMLForContract_PreservesEmptyFeaturesSequence(t *testing.T) {
	spec := sampleSpec()
	spec.Features = nil
	contract := sourceContract(t, "  features: []\n")

	yamlBytes, _, err := AssembleProfileYAMLForContract(spec, contract)
	if err != nil {
		t.Fatalf("AssembleProfileYAMLForContract: %v", err)
	}

	var decoded struct {
		Metadata map[string]any `json:"metadata"`
	}
	if err := yaml.Unmarshal(yamlBytes, &decoded); err != nil {
		t.Fatalf("unmarshal profile YAML: %v", err)
	}
	features, ok := decoded.Metadata["features"].([]any)
	if !ok {
		t.Fatalf("metadata.features = %#v (%T), want empty sequence", decoded.Metadata["features"], decoded.Metadata["features"])
	}
	if len(features) != 0 {
		t.Fatalf("metadata.features = %#v, want empty sequence", features)
	}
	if strings.Contains(string(yamlBytes), "features: null") {
		t.Fatalf("projected YAML rendered empty features as null:\n%s", yamlBytes)
	}
}

func TestAssembleProfileYAMLForContract_KeepsOpaqueValueUntilReinspection(t *testing.T) {
	contract, err := profileyaml.Parse(`{
		"codec": "aim-profile/v1",
		"metadataFields": ["engine", "metric", "precision", "type"],
		"extensions": {
			"metadata": {
				"variant": "opaque-source-value"
			}
		}
	}`)
	if err != nil {
		t.Fatalf("Parse contract: %v", err)
	}
	spec := sampleSpec()
	spec.Variant = "typed-value-not-yet-migrated"

	yamlBytes, _, err := AssembleProfileYAMLForContract(spec, contract)
	if err != nil {
		t.Fatalf("AssembleProfileYAMLForContract: %v", err)
	}
	var decoded struct {
		Metadata map[string]any `json:"metadata"`
	}
	if err := yaml.Unmarshal(yamlBytes, &decoded); err != nil {
		t.Fatalf("unmarshal profile YAML: %v", err)
	}
	if got := decoded.Metadata["variant"]; got != "opaque-source-value" {
		t.Fatalf("metadata.variant = %#v, want preserved opaque source value", got)
	}
}

func TestAssembleProfileYAMLForContract_PreservesUnmodeledSourceExtensions(t *testing.T) {
	source := []byte(`profile_schema_version: 1
aim_id: openai/gpt-oss-20b
model_id: openai/gpt-oss-20b
metadata:
  engine: vllm
  accelerator_type: gpu
  accelerator_model: MI300X
  accelerator_count: 1
  precision: fp4
  metric: latency
  type: optimized
  primary: true
  capabilities:
    tool_calling: true
    structured_outputs: true
    reasoning: true
  future_runtime_hint:
    mode: fast
engine_args: {}
env_vars: {}
future_top_level:
  enabled: true
`)
	contract, err := profileyaml.Inspect(source)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}

	spec := sampleSpec()
	spec.AcceleratorModel = "MI325X"
	spec.Primary = false

	yamlBytes, _, err := AssembleProfileYAMLForContract(spec, contract)
	if err != nil {
		t.Fatalf("AssembleProfileYAMLForContract: %v", err)
	}

	var decoded map[string]any
	if err := yaml.Unmarshal(yamlBytes, &decoded); err != nil {
		t.Fatalf("unmarshal projected profile YAML: %v", err)
	}
	metadata, ok := decoded["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata = %T, want object", decoded["metadata"])
	}
	if metadata["accelerator_model"] != "MI325X" {
		t.Errorf("accelerator_model = %#v, want normalized override", metadata["accelerator_model"])
	}
	if metadata["primary"] != false {
		t.Errorf("primary = %#v, want normalized false", metadata["primary"])
	}
	capabilities, ok := metadata["capabilities"].(map[string]any)
	if !ok || capabilities["reasoning"] != true || capabilities["tool_calling"] != true {
		t.Errorf("capabilities not preserved: %#v", metadata["capabilities"])
	}
	if _, ok := metadata["future_runtime_hint"]; !ok {
		t.Errorf("future_runtime_hint not preserved: %#v", metadata)
	}
	if decoded["profile_schema_version"] != float64(1) {
		t.Errorf("profile_schema_version = %#v, want 1", decoded["profile_schema_version"])
	}
	if _, ok := decoded["future_top_level"]; !ok {
		t.Errorf("future_top_level not preserved: %#v", decoded)
	}
	if _, ok := metadata["gpu"]; ok {
		t.Errorf("strict source unexpectedly gained legacy gpu field: %#v", metadata)
	}
}

func TestBuildProfileConfigMap_CarriesNameNamespaceLabelsData(t *testing.T) {
	cm := BuildProfileConfigMap("cm", "ns", map[string]string{"k": "v"}, "vllm-mi300x-fp8-tp1-latency.yaml", []byte("aim_id: x\n"))
	if cm.Name != "cm" || cm.Namespace != "ns" {
		t.Fatalf("unexpected name/namespace: %q/%q", cm.Name, cm.Namespace)
	}
	if cm.Labels["k"] != "v" {
		t.Errorf("labels not carried: %v", cm.Labels)
	}
	if len(cm.Data) != 1 || cm.Data["vllm-mi300x-fp8-tp1-latency.yaml"] != "aim_id: x\n" {
		t.Errorf("data not carried: %v", cm.Data)
	}
}

func TestBuildProfileVolumeMount_ReadOnlyUnderBase(t *testing.T) {
	mount := BuildProfileVolumeMount("qwen/qwen3-32b")
	if !strings.HasPrefix(mount.MountPath, ProfileMountBase+"/") {
		t.Errorf("mount path %q not under %q", mount.MountPath, ProfileMountBase)
	}
	if !mount.ReadOnly {
		t.Errorf("profile mount must be read-only")
	}
	if mount.Name != ProfileVolumePrefix {
		t.Errorf("mount name = %q, want %q", mount.Name, ProfileVolumePrefix)
	}
}

// TestBuildFrameworkEnvVars_NoModelSources asserts the standalone case:
// AIM_ENGINE, AIM_PROFILE_ID and AIM_ID (set to the profile's aimId) are
// emitted, so a per-AIM image can serve without a cache redirect.
func TestBuildFrameworkEnvVars_NoModelSources(t *testing.T) {
	env := envMap(BuildFrameworkEnvVars(sampleSpec(), "vllm-mi300x-fp8-tp1-latency.yaml"))

	if env[constants.EnvAIMProfileID] != "custom/qwen/qwen3-32b/vllm-mi300x-fp8-tp1-latency" {
		t.Errorf("AIM_PROFILE_ID = %q", env[constants.EnvAIMProfileID])
	}
	if env[constants.EnvAIMID] != "qwen/qwen3-32b" {
		t.Errorf("AIM_ID = %q, want the profile aimId when not redirecting", env[constants.EnvAIMID])
	}
	if env[constants.EnvAIMEngine] != sampleSpec().Engine {
		t.Errorf("AIM_ENGINE = %q, want %q", env[constants.EnvAIMEngine], sampleSpec().Engine)
	}
	if _, ok := env[constants.EnvAIMModelID]; ok {
		t.Errorf("AIM_MODEL_ID must be unset without modelSources")
	}
}

// TestBuildFrameworkEnvVars_WithModelSources asserts the cache-redirect case:
// AIM_ID is clobbered empty and AIM_MODEL_ID / AIM_CACHE_PATH point the runtime
// at the locally cached weights (mutual-exclusivity with AIM_ID).
func TestBuildFrameworkEnvVars_WithModelSources(t *testing.T) {
	spec := sampleSpec()
	spec.ModelSources = []aimv1alpha1.AIMModelSource{{ModelID: "org/model", SourceURI: "hf://org/model"}}

	env := envMap(BuildFrameworkEnvVars(spec, "vllm-mi300x-fp8-tp1-latency.yaml"))

	if env[constants.EnvAIMID] != "" {
		t.Errorf("AIM_ID must be cleared when redirecting, got %q", env[constants.EnvAIMID])
	}
	if env[constants.EnvAIMModelID] != "org/model" {
		t.Errorf("AIM_MODEL_ID = %q, want org/model", env[constants.EnvAIMModelID])
	}
	if env[constants.EnvAIMCachePath] != constants.AIMCacheBasePath {
		t.Errorf("AIM_CACHE_PATH = %q, want %q", env[constants.EnvAIMCachePath], constants.AIMCacheBasePath)
	}
	if env[constants.EnvAIMEngine] != spec.Engine {
		t.Errorf("AIM_ENGINE = %q, want %q", env[constants.EnvAIMEngine], spec.Engine)
	}
}

func TestBuildFrameworkEnvVars_EmptyEngineIsOmitted(t *testing.T) {
	spec := sampleSpec()
	spec.Engine = ""

	env := envMap(BuildFrameworkEnvVars(spec, "vllm-mi300x-fp8-tp1-latency.yaml"))
	if _, ok := env[constants.EnvAIMEngine]; ok {
		t.Errorf("AIM_ENGINE must be unset when the profile does not declare an engine")
	}
}

func envMap(vars []corev1.EnvVar) map[string]string {
	m := make(map[string]string, len(vars))
	for _, e := range vars {
		m[e.Name] = e.Value
	}
	return m
}
