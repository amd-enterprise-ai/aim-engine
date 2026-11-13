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

package profileyaml

import (
	"reflect"
	"strings"
	"testing"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
)

func TestContractRoundTrip(t *testing.T) {
	raw := []byte(`profile_schema_version: 1
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

	want, err := Inspect(raw)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	got, err := Parse(want.Encode())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Encode() != want.Encode() {
		t.Fatalf("round trip:\n got %s\nwant %s", got.Encode(), want.Encode())
	}

	extensions := got.Extensions()
	wantCapabilities := map[string]any{
		"tool_calling":       true,
		"structured_outputs": true,
		"reasoning":          true,
	}
	if !reflect.DeepEqual(extensions.Metadata["capabilities"], wantCapabilities) {
		t.Errorf("capabilities = %#v, want %#v", extensions.Metadata["capabilities"], wantCapabilities)
	}
	if _, ok := extensions.Metadata["primary"]; ok {
		t.Error("typed metadata.primary must not be duplicated in extensions")
	}
	if extensions.TopLevel["profile_schema_version"] != float64(1) {
		t.Errorf("profile_schema_version = %#v, want 1", extensions.TopLevel["profile_schema_version"])
	}
	if _, ok := extensions.TopLevel["future_top_level"]; !ok {
		t.Errorf("future_top_level missing from extensions: %#v", extensions.TopLevel)
	}

	extensions.Metadata["capabilities"] = "mutated"
	if got.Extensions().Metadata["capabilities"] == "mutated" {
		t.Fatal("Extensions returned mutable contract state")
	}
}

func TestInspectUsesExactSourceFieldPresence(t *testing.T) {
	legacy, err := Inspect([]byte(`metadata:
  engine: vllm
  gpu: MI300X
  gpu_count: 1
  manual_selection_only: true
  metric: latency
  precision: fp8
  type: optimized
`))
	if err != nil {
		t.Fatalf("Inspect legacy: %v", err)
	}
	for _, field := range []string{"gpu", "gpu_count", "manual_selection_only"} {
		if !legacy.HasMetadataField(field) {
			t.Errorf("legacy contract missing %q", field)
		}
	}
	if legacy.HasMetadataField("accelerator_model") {
		t.Error("legacy contract unexpectedly contains accelerator_model")
	}

	strict := DefaultContract()
	for _, field := range []string{"accelerator_model", "accelerator_type", "accelerator_count"} {
		if !strict.HasMetadataField(field) {
			t.Errorf("strict contract missing %q", field)
		}
	}
	if strict.HasMetadataField("gpu") {
		t.Error("strict contract unexpectedly contains gpu")
	}

	generic, err := Inspect([]byte(`metadata:
  engine: vllm
  metric: latency
  precision: fp8
  type: general
  manual_selection_only: true
`))
	if err != nil {
		t.Fatalf("Inspect generic: %v", err)
	}
	for _, field := range []string{"gpu", "gpu_count", "accelerator_model", "accelerator_type", "accelerator_count"} {
		if generic.HasMetadataField(field) {
			t.Errorf("accelerator-free source unexpectedly gained %q: %s", field, generic.Encode())
		}
	}
}

func TestCanonicalContractIncludesPopulatedOptionalFields(t *testing.T) {
	spec := &aimv1alpha2.AIMProfileSpecCommon{
		Variant:  "usp4",
		Features: []string{"adapters"},
	}
	contract := CanonicalContract(spec)
	for _, field := range []string{"accelerator_model", "accelerator_type", "accelerator_count", "variant", "features"} {
		if !contract.HasMetadataField(field) {
			t.Errorf("canonical contract missing %q: %s", field, contract.Encode())
		}
	}
}

func TestParseKeepsPreviouslyOpaqueFieldAuthoritative(t *testing.T) {
	contract, err := Parse(`{
			"codec": "aim-profile/v1",
			"metadataFields": ["engine", "metric", "precision", "type"],
			"extensions": {
				"metadata": {
					"variant": "opaque-old-value",
					"capabilities": {"reasoning": true}
				},
				"topLevel": {
					"future_top_level": true
				}
			}
	}`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if contract.HasMetadataField("variant") {
		t.Fatal("opaque metadata.variant must not be promoted without re-inspecting the source")
	}

	extensions := contract.Extensions()
	if extensions.Metadata["variant"] != "opaque-old-value" {
		t.Errorf("opaque metadata.variant was not preserved: %#v", extensions.Metadata)
	}
	if _, ok := extensions.Metadata["capabilities"]; !ok {
		t.Errorf("unmodeled capabilities were dropped: %#v", extensions.Metadata)
	}
	if _, ok := extensions.TopLevel["future_top_level"]; !ok {
		t.Errorf("unmodeled top-level field was dropped: %#v", extensions.TopLevel)
	}
}

func TestInspectDoesNotRecordEngineConsumedFieldAsExtension(t *testing.T) {
	contract, err := Inspect([]byte(`metadata:
  engine: vllm
  metric: latency
  precision: fp16
  type: optimized
download_filter:
  include: ["*.safetensors"]
future_top_level: true
`))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	extensions := contract.Extensions()
	if _, ok := extensions.TopLevel["download_filter"]; ok {
		t.Errorf("download_filter must not be projected to the runtime profile: %#v", extensions.TopLevel)
	}
	if _, ok := extensions.TopLevel["future_top_level"]; !ok {
		t.Errorf("unrelated top-level field was dropped: %#v", extensions.TopLevel)
	}
}

func TestParseKeepsEngineConsumedFieldFromOlderAnnotationsUntilReinspection(t *testing.T) {
	contract, err := Parse(`{
			"codec": "aim-profile/v1",
			"metadataFields": ["engine", "metric", "precision", "type"],
			"extensions": {
				"topLevel": {
					"download_filter": {}
				}
			}
	}`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, ok := contract.Extensions().TopLevel["download_filter"]; !ok {
		t.Errorf("a previously opaque download_filter must stay authoritative: %s", contract.Encode())
	}
}

func TestInspectRejectsUnknownExplicitSchema(t *testing.T) {
	raw := []byte(`profile_schema_version: 2
metadata:
  engine: vllm
`)
	if _, err := Inspect(raw); err == nil {
		t.Fatal("expected unsupported schema error")
	}
}

func TestInspectRejectsMissingOrNullMetadata(t *testing.T) {
	for _, raw := range []string{
		"aim_id: model\n",
		"metadata: null\n",
		"metadata:\n  engine: vllm\n",
	} {
		if _, err := Inspect([]byte(raw)); err == nil {
			t.Fatalf("Inspect(%q) unexpectedly succeeded", raw)
		}
	}
}

func TestParseRejectsUnknownOrInvalidContract(t *testing.T) {
	if _, err := Parse(`{"codec":"aim-profile/v2"}`); err == nil {
		t.Fatal("expected unknown codec error")
	}
	if _, err := Parse(`{"codec":"aim-profile/v1","metadataFields":["future_typed_field"]}`); err == nil {
		t.Fatal("expected unknown canonical metadata field error")
	}
	if _, err := Parse("accelerator,future-field"); err == nil {
		t.Fatal("expected non-JSON annotation error")
	}
	if _, err := Parse(`{"codec":"aim-profile/v1","metadataFields":["engine","metric","precision","type"],"future":true}`); err == nil {
		t.Fatal("expected unknown envelope field error")
	}
	if _, err := Parse(`{"codec":"aim-profile/v1","metadataFields":["engine","metric","precision"]}`); err == nil {
		t.Fatal("expected missing required field error")
	}
}

func TestInspectRejectsOversizedContract(t *testing.T) {
	raw := `metadata:
  engine: vllm
  metric: latency
  precision: fp8
  type: optimized
  capabilities: "` + strings.Repeat("x", MaxContractAnnotationBytes) + `"
`
	if _, err := Inspect([]byte(raw)); err == nil || !strings.Contains(err.Error(), "annotation budget") {
		t.Fatalf("Inspect oversized contract error = %v", err)
	}
}

func TestForProfileRequiresSourceContractButDefaultsCanonicalProfiles(t *testing.T) {
	spec := &aimv1alpha2.AIMProfileSpecCommon{Variant: "usp4"}
	if _, err := ForProfile(nil, aimv1alpha1.ProfileOriginDiscovered, spec); err == nil {
		t.Fatal("expected discovered profile without a contract to fail")
	}
	contract, err := ForProfile(nil, aimv1alpha1.ProfileOriginUserAuthored, spec)
	if err != nil {
		t.Fatalf("ForProfile user-authored: %v", err)
	}
	if !contract.HasMetadataField("variant") {
		t.Fatalf("canonical user-authored contract missing variant: %s", contract.Encode())
	}
}
