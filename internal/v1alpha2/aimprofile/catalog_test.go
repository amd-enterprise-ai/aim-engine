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

package aimprofile

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// testAimIDCustomTransformer is reused by the custom-model base-profile
// catalog parse tests.
const testAimIDCustomTransformer = "acme/custom-transformer"

func TestParseDiscoveryCatalog_FromRawYAMLs(t *testing.T) {
	t.Parallel()

	profileYAML := `aim_id: meta-llama/Llama-3-8B-Instruct
model_id: meta-llama/Llama-3-8B-Instruct
metadata:
  engine: vllm
  metric: latency
  precision: fp8
  type: standard
  primary: true
  accelerator_model: MI300X
  accelerator_count: 1
env_vars:
  VLLM_USE_TRITON_FLASH_ATTN: "1"
`

	configMap := &corev1.ConfigMap{
		Data: map[string]string{
			FlattenProfilePath("meta-llama/Llama-3-8B-Instruct/mi300x-fp8.yaml"): profileYAML,
			DiscoveryCacheMetadataKey: `{
  "aimId": "meta-llama/Llama-3-8B-Instruct",
  "sourceImage": "quay.io/amd/aim-llama:0.9.0",
  "baseImage": "quay.io/amd/aim-base:0.9.0",
  "sourceSpecHash": "abc123",
  "discoveryCommandVersion": "v1"
}`,
		},
	}

	parsed, err := ParseDiscoveryCatalog(configMap)
	if err != nil {
		t.Fatalf("ParseDiscoveryCatalog() error = %v", err)
	}
	if parsed.AimID != "meta-llama/Llama-3-8B-Instruct" {
		t.Fatalf("parsed.AimID = %q, want metadata aimId", parsed.AimID)
	}
	if parsed.BaseImage != "quay.io/amd/aim-base:0.9.0" {
		t.Fatalf("parsed.BaseImage = %q, want metadata baseImage", parsed.BaseImage)
	}
	if len(parsed.Profiles) != 1 {
		t.Fatalf("len(parsed.Profiles) = %d, want 1", len(parsed.Profiles))
	}
	item := parsed.Profiles[0]
	if item.Spec.ProfileId != "mi300x-fp8" {
		t.Fatalf("ProfileId = %q, want mi300x-fp8", item.Spec.ProfileId)
	}
	if item.Spec.Image != "quay.io/amd/aim-llama:0.9.0" {
		t.Fatalf("Image = %q, want metadata sourceImage", item.Spec.Image)
	}
	if item.Spec.AcceleratorModel != "MI300X" {
		t.Fatalf("AcceleratorModel = %q, want MI300X", item.Spec.AcceleratorModel)
	}
	if item.Spec.AcceleratorCount != 1 {
		t.Fatalf("AcceleratorCount = %d, want 1", item.Spec.AcceleratorCount)
	}
	if !item.Spec.Primary {
		t.Fatal("Primary = false, want true")
	}
	if !item.PrimaryExplicit {
		t.Fatal("PrimaryExplicit = false, want true (YAML stamps `primary: true`)")
	}

	candidates := parsed.ToCandidates()
	if len(candidates) != 1 {
		t.Fatalf("len(candidates) = %d, want 1", len(candidates))
	}
	if candidates[0].BaseImage != "quay.io/amd/aim-base:0.9.0" {
		t.Fatalf("candidate BaseImage = %q, want metadata baseImage", candidates[0].BaseImage)
	}
}

// TestParseDiscoveryCatalog_BoundProfilesWinOverGeneral pins the precedence
// rule that resolves the v1alpha2-profile-via-model-cpu-live oscillation:
// when an image leaks `general/<x>.yaml` from a base layer alongside a
// model-specific `$AIM_ID/<x>.yaml`, both YAMLs would otherwise produce
// catalog items with the same Name (basename-derived ProfileId), causing
// the AIMModel reconciler to oscillate the resulting AIMProfile spec.
// We expect the model-specific item to win and the leaked general/ item
// to be dropped.
func TestParseDiscoveryCatalog_BoundProfilesWinOverGeneral(t *testing.T) {
	t.Parallel()

	boundYAML := `aim_id: Qwen/Qwen3-0.6B
model_id: Qwen/Qwen3-0.6B
metadata:
  engine: vllm
  metric: latency
  precision: bf16
  type: unoptimized
  primary: true
  manual_selection_only: true
  accelerator_model: CPU
  accelerator_count: 1
env_vars: {}
`

	generalYAML := `metadata:
  engine: vllm
  metric: latency
  precision: bf16
  type: general
  primary: false
  manual_selection_only: false
  accelerator_model: CPU
  accelerator_count: 1
env_vars: {}
`

	configMap := &corev1.ConfigMap{
		Data: map[string]string{
			FlattenProfilePath("Qwen/Qwen3-0.6B/vllm-cpu-bf16-tp1-latency.yaml"): boundYAML,
			FlattenProfilePath("general/vllm-cpu-bf16-tp1-latency.yaml"):         generalYAML,
			DiscoveryCacheMetadataKey: `{
  "aimId": "Qwen/Qwen3-0.6B",
  "sourceImage": "ghcr.io/silogen/aim-cpu-qwen-qwen3-0-6b:0.12.0-rc16",
  "discoveryCommandVersion": "v1"
}`,
		},
	}

	parsed, err := ParseDiscoveryCatalog(configMap)
	if err != nil {
		t.Fatalf("ParseDiscoveryCatalog() error = %v", err)
	}
	if len(parsed.Profiles) != 1 {
		t.Fatalf("len(parsed.Profiles) = %d, want 1 (general/ leakage should be dropped); got %#v",
			len(parsed.Profiles), parsed.Profiles)
	}
	got := parsed.Profiles[0]
	if got.RelPath != "Qwen/Qwen3-0.6B/vllm-cpu-bf16-tp1-latency.yaml" {
		t.Fatalf("RelPath = %q, want the bound (Qwen/Qwen3-0.6B/...) item", got.RelPath)
	}
	if !got.Spec.Primary {
		t.Fatal("Primary = false, want bound spec (Primary=true)")
	}
	if got.Spec.ModelId != "Qwen/Qwen3-0.6B" {
		t.Fatalf("ModelId = %q, want Qwen/Qwen3-0.6B (bound item)", got.Spec.ModelId)
	}
	if len(got.Spec.ModelSources) == 0 {
		t.Fatal("ModelSources = empty, want non-empty (bound item must carry modelSources)")
	}
}

// TestParseDiscoveryCatalog_GeneralKeptForBaseImages pins the inverse case:
// an image without a bound AimID (a true base image, e.g. aim-base:X.Y) only
// emits general/ profiles. We must keep them — otherwise the v1alpha2 overlay
// path (AIMService profileOverrides supplying modelSources) has no source
// profile to materialise from.
func TestParseDiscoveryCatalog_GeneralKeptForBaseImages(t *testing.T) {
	t.Parallel()

	generalYAML := `metadata:
  engine: vllm
  metric: latency
  precision: bf16
  type: general
  accelerator_model: CPU
  accelerator_count: 1
env_vars: {}
`

	configMap := &corev1.ConfigMap{
		Data: map[string]string{
			FlattenProfilePath("general/vllm-cpu-bf16-tp1-latency.yaml"): generalYAML,
			DiscoveryCacheMetadataKey: `{
  "sourceImage": "quay.io/amd/aim-cpu-base:0.12.0",
  "discoveryCommandVersion": "v1"
}`,
		},
	}

	parsed, err := ParseDiscoveryCatalog(configMap)
	if err != nil {
		t.Fatalf("ParseDiscoveryCatalog() error = %v", err)
	}
	if len(parsed.Profiles) != 1 {
		t.Fatalf("len(parsed.Profiles) = %d, want 1 (general/ kept for unbound base images)", len(parsed.Profiles))
	}
	if parsed.Profiles[0].RelPath != "general/vllm-cpu-bf16-tp1-latency.yaml" {
		t.Fatalf("RelPath = %q, want the general/ item kept for base image",
			parsed.Profiles[0].RelPath)
	}
}

// TestParseDiscoveryCatalog_GeneralKeptWhenBoundEmpty pins graceful degradation:
// an image declares an AimID in its metadata but ships no $AIM_ID/ directory
// (mis-built or not yet bound to a model). We must keep general/ items so the
// user can still use the image via overrides; otherwise the catalog would be
// empty and the AIMModel would never publish a Ready managed profile.
func TestParseDiscoveryCatalog_GeneralKeptWhenBoundEmpty(t *testing.T) {
	t.Parallel()

	generalYAML := `metadata:
  engine: vllm
  metric: latency
  precision: bf16
  type: general
  accelerator_model: CPU
  accelerator_count: 1
env_vars: {}
`

	configMap := &corev1.ConfigMap{
		Data: map[string]string{
			FlattenProfilePath("general/vllm-cpu-bf16-tp1-latency.yaml"): generalYAML,
			DiscoveryCacheMetadataKey: `{
  "aimId": "Qwen/Qwen3-0.6B",
  "sourceImage": "ghcr.io/silogen/aim-cpu-qwen-qwen3-0-6b:0.12.0-rc16",
  "discoveryCommandVersion": "v1"
}`,
		},
	}

	parsed, err := ParseDiscoveryCatalog(configMap)
	if err != nil {
		t.Fatalf("ParseDiscoveryCatalog() error = %v", err)
	}
	if len(parsed.Profiles) != 1 {
		t.Fatalf("len(parsed.Profiles) = %d, want 1 (general kept when bound is empty)", len(parsed.Profiles))
	}
}

// TestParseDiscoveryCatalog_BaseImageGeneralYamlsAreBaseProfiles covers the
// base-image (custom-model source) producer path: a base image whose
// general/*.yaml YAMLs omit aim_id, model_id, and model_sources should
// produce catalog items with empty identity that classify as
// !IsProfileDeployable (so the AIMModel materialiser stamps them with
// role=base).
func TestParseDiscoveryCatalog_BaseImageGeneralYamlsAreBaseProfiles(t *testing.T) {
	t.Parallel()

	// No aim_id, no model_id, no env_vars beyond the bare metadata block.
	generalYAML := `metadata:
  engine: vllm
  metric: latency
  precision: bf16
  type: general
  accelerator_model: MI300X
  accelerator_count: 1
`

	configMap := &corev1.ConfigMap{
		Data: map[string]string{
			FlattenProfilePath("general/vllm-mi300x-bf16-tp1-latency.yaml"): generalYAML,
			DiscoveryCacheMetadataKey: `{
  "sourceImage": "ghcr.io/silogen/aim-base-vllm:0.1.0",
  "discoveryCommandVersion": "v1"
}`,
		},
	}

	parsed, err := ParseDiscoveryCatalog(configMap)
	if err != nil {
		t.Fatalf("ParseDiscoveryCatalog() error = %v", err)
	}
	if parsed.AimID != "" {
		t.Fatalf("catalog.AimID = %q, want empty for base-image discovery", parsed.AimID)
	}
	if len(parsed.Profiles) != 1 {
		t.Fatalf("len(parsed.Profiles) = %d, want 1", len(parsed.Profiles))
	}
	item := parsed.Profiles[0]
	if item.Spec.AimId != "" {
		t.Fatalf("item.Spec.AimId = %q, want empty for base-image base profile", item.Spec.AimId)
	}
	if item.Spec.ModelId != "" {
		t.Fatalf("item.Spec.ModelId = %q, want empty for base-image base profile", item.Spec.ModelId)
	}
	if len(item.Spec.ModelSources) != 0 {
		t.Fatalf("item.Spec.ModelSources = %#v, want nil/empty for base-image base profile", item.Spec.ModelSources)
	}
	if IsProfileDeployable(item.Spec) {
		t.Fatal("IsProfileDeployable(item.Spec) = true, want false for base-image base profile")
	}
	if got := ProfileRoleLabelValue(item.Spec); got != "base" {
		t.Fatalf("ProfileRoleLabelValue() = %q, want \"base\" for base-image base profile", got)
	}
}

// TestParseDiscoveryCatalog_AimIdOnlyYamlIsBaseProfile pins the mid-state:
// a base image's general/*.yaml may set aim_id alone (carrying the
// architecture identifier) but still omit model_sources. By
// IsProfileDeployable's definition that is still a base profile, and the
// AIMModel materialiser stamps it as role=base.
func TestParseDiscoveryCatalog_AimIdOnlyYamlIsBaseProfile(t *testing.T) {
	t.Parallel()

	yaml := `aim_id: ` + testAimIDCustomTransformer + `
metadata:
  engine: vllm
  metric: latency
  precision: bf16
  type: general
  accelerator_model: MI300X
  accelerator_count: 1
`

	configMap := &corev1.ConfigMap{
		Data: map[string]string{
			FlattenProfilePath("general/vllm-mi300x-bf16-tp1-latency.yaml"): yaml,
			DiscoveryCacheMetadataKey: `{
  "sourceImage": "ghcr.io/silogen/aim-base-vllm:0.1.0",
  "discoveryCommandVersion": "v1"
}`,
		},
	}

	parsed, err := ParseDiscoveryCatalog(configMap)
	if err != nil {
		t.Fatalf("ParseDiscoveryCatalog() error = %v", err)
	}
	if len(parsed.Profiles) != 1 {
		t.Fatalf("len(parsed.Profiles) = %d, want 1", len(parsed.Profiles))
	}
	item := parsed.Profiles[0]
	if item.Spec.AimId != testAimIDCustomTransformer {
		t.Fatalf("item.Spec.AimId = %q, want per-YAML aim_id to be preserved", item.Spec.AimId)
	}
	if len(item.Spec.ModelSources) != 0 {
		t.Fatalf("item.Spec.ModelSources = %#v, want empty (no model_id in YAML)", item.Spec.ModelSources)
	}
	if IsProfileDeployable(item.Spec) {
		t.Fatal("IsProfileDeployable = true; spec with aimId alone but no modelSources must remain a base profile")
	}
}

func TestParseDiscoveryCatalog_PrimaryUnsetMarksItemImplicit(t *testing.T) {
	t.Parallel()

	// Legacy image: profile YAML omits `primary`, so the catalog item must
	// carry PrimaryExplicit=false and Spec.Primary=false. The downstream
	// AIMModel reconciler is responsible for backfilling Primary from the
	// OCI `recommendedDeployments` label.
	profileYAML := `aim_id: Qwen/Qwen3-32B
model_id: Qwen/Qwen3-32B
metadata:
  engine: vllm
  metric: latency
  precision: fp16
  type: optimized
  accelerator_model: MI300X
  accelerator_count: 1
`

	cm := &corev1.ConfigMap{
		Data: map[string]string{
			FlattenProfilePath("Qwen/Qwen3-32B/vllm-mi300x-fp16-tp1-latency.yaml"): profileYAML,
			DiscoveryCacheMetadataKey: `{"aimId":"Qwen/Qwen3-32B","sourceImage":"amdenterpriseai/aim-qwen:0.8.5"}`,
		},
	}
	parsed, err := ParseDiscoveryCatalog(cm)
	if err != nil {
		t.Fatalf("ParseDiscoveryCatalog() error = %v", err)
	}
	if len(parsed.Profiles) != 1 {
		t.Fatalf("len(Profiles) = %d, want 1", len(parsed.Profiles))
	}
	item := parsed.Profiles[0]
	if item.PrimaryExplicit {
		t.Error("PrimaryExplicit = true, want false (YAML did not stamp `primary`)")
	}
	if item.Spec.Primary {
		t.Error("Spec.Primary = true, want false (YAML did not stamp `primary`)")
	}
}

func TestParseDiscoveryCatalog_RejectsEmptyConfigMap(t *testing.T) {
	t.Parallel()

	cm := &corev1.ConfigMap{Data: map[string]string{}}
	if _, err := ParseDiscoveryCatalog(cm); err == nil {
		t.Fatal("ParseDiscoveryCatalog() err = nil, want error on empty configmap")
	}
}

func TestFlattenProfilePath_RoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		rel string
		key string
	}{
		{"foo.yaml", "profiles.foo.yaml"},
		{"foo/bar.yaml", "profiles.foo__bar.yaml"},
		{"a/b/c.yaml", "profiles.a__b__c.yaml"},
	}
	for _, tc := range cases {
		got := FlattenProfilePath(tc.rel)
		if got != tc.key {
			t.Errorf("FlattenProfilePath(%q) = %q, want %q", tc.rel, got, tc.key)
		}
		back, ok := UnflattenProfileKey(got)
		if !ok {
			t.Errorf("UnflattenProfileKey(%q) !ok", got)
			continue
		}
		if back != tc.rel {
			t.Errorf("UnflattenProfileKey(%q) = %q, want %q", got, back, tc.rel)
		}
	}

	if _, ok := UnflattenProfileKey("metadata.json"); ok {
		t.Error("UnflattenProfileKey(metadata.json) should return false")
	}
}
