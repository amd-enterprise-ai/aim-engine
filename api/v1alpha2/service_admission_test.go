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

package v1alpha2_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/defaulting"
	structurallisttype "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/listtype"
	apiservervalidation "k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	kjson "sigs.k8s.io/json"
	"sigs.k8s.io/yaml"
)

func generatedServiceSchema(t *testing.T, version string) *schema.Structural {
	t.Helper()
	structural, err := schema.NewStructural(generatedServiceSchemaProps(t, version))
	if err != nil {
		t.Fatal(err)
	}
	return structural
}

func generatedServiceSchemaProps(t *testing.T, version string) *apiextensions.JSONSchemaProps {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "bases", "aim.eai.amd.com_aimservices.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatal(err)
	}
	for _, v := range crd.Spec.Versions {
		if v.Name != version {
			continue
		}
		var internal apiextensions.JSONSchemaProps
		if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(v.Schema.OpenAPIV3Schema, &internal, nil); err != nil {
			t.Fatal(err)
		}
		return &internal
	}
	t.Fatalf("missing %s schema", version)
	return nil
}

// serviceObject builds a defaulted AIMService object from a spec YAML, the
// way the API server sees it after decoding (including objects read back from
// storage, which are defaulted on read).
func serviceObject(t *testing.T, structural *schema.Structural, version, specYAML string) map[string]any {
	t.Helper()
	var spec map[string]any
	raw, err := yaml.YAMLToJSON([]byte(specYAML))
	if err != nil {
		t.Fatal(err)
	}
	if err := kjson.UnmarshalCaseSensitivePreserveInts(raw, &spec); err != nil {
		t.Fatal(err)
	}
	obj := map[string]any{
		"apiVersion": "aim.eai.amd.com/" + version,
		"kind":       "AIMService",
		"metadata":   map[string]any{"name": "test"},
		"spec":       spec,
	}
	defaulting.Default(obj, structural)
	return obj
}

func validateService(t *testing.T, structural *schema.Structural, obj, old map[string]any) field.ErrorList {
	t.Helper()
	validator := cel.NewValidator(structural, true, 1000000)
	var oldObj any
	if old != nil {
		oldObj = old
	}
	errs, _ := validator.Validate(context.Background(), field.NewPath(""), structural, obj, oldObj, 10000000)
	return errs
}

// validateServiceFully runs the create-time checks the API server applies to a
// custom resource beyond CEL: OpenAPI schema validation (required, length
// bounds) and list-type uniqueness.
func validateServiceFully(t *testing.T, version string, obj map[string]any) field.ErrorList {
	t.Helper()
	props := generatedServiceSchemaProps(t, version)
	structural, err := schema.NewStructural(props)
	if err != nil {
		t.Fatal(err)
	}
	validator, _, err := apiservervalidation.NewSchemaValidator(props)
	if err != nil {
		t.Fatal(err)
	}
	errs := apiservervalidation.ValidateCustomResource(nil, obj, validator)
	errs = append(errs, structurallisttype.ValidateListSetsAndMaps(nil, structural, obj)...)
	return append(errs, validateService(t, structural, obj, nil)...)
}

// assertServingAPIVerdict fails unless the object is accepted (valid) or
// rejected by a servingAPI rule, so an unrelated rule cannot satisfy an
// expected rejection.
func assertServingAPIVerdict(t *testing.T, errs field.ErrorList, valid bool) {
	t.Helper()
	if valid {
		if len(errs) != 0 {
			t.Fatalf("expected the object to be accepted: %v", errs)
		}
		return
	}
	for _, err := range errs {
		if strings.Contains(err.Error(), "servingAPI") {
			return
		}
	}
	t.Fatalf("expected a spec.servingAPI rejection, got: %v", errs)
}

func TestServiceServingAPIAdmission_create(t *testing.T) {
	structural := generatedServiceSchema(t, "v1alpha2")
	for _, tc := range []struct {
		name  string
		spec  string
		valid bool
	}{
		{"defaulted auto", `profile: {name: qwen}`, true},
		{"explicit inference service", `{profile: {name: qwen}, servingAPI: InferenceService}`, true},
		{"llm inference service with profile", `{profile: {name: qwen}, servingAPI: LLMInferenceService}`, true},
		{"llm inference service without profile", `{model: {name: qwen}, servingAPI: LLMInferenceService}`, false},
		{"llm inference service with static adapters", `
profile: {name: qwen}
servingAPI: LLMInferenceService
adapters: [{kind: AIMArtifact, name: lora}]`, true},
		{"llm inference service with dynamic adapter mode", `
profile: {name: qwen}
servingAPI: LLMInferenceService
adapterMode: dynamic`, true},
		{"inference service keeps adapters", `
profile: {name: qwen}
servingAPI: InferenceService
adapters: [{kind: AIMArtifact, name: lora}]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := validateService(t, structural, serviceObject(t, structural, "v1alpha2", tc.spec), nil)
			assertServingAPIVerdict(t, errs, tc.valid)
		})
	}
}

func TestServiceServingAPIAdmission_defaultsToAuto(t *testing.T) {
	structural := generatedServiceSchema(t, "v1alpha2")
	obj := serviceObject(t, structural, "v1alpha2", `profile: {name: qwen}`)
	if got := obj["spec"].(map[string]any)["servingAPI"]; got != "Auto" {
		t.Fatalf("spec.servingAPI defaulted to %v, want Auto", got)
	}
}

func TestServiceServingAPIAdmission_immutable(t *testing.T) {
	structural := generatedServiceSchema(t, "v1alpha2")
	for _, tc := range []struct {
		name    string
		oldSpec string
		newSpec string
		valid   bool
	}{
		{
			"unchanged", `{profile: {name: qwen}, servingAPI: LLMInferenceService}`,
			`{profile: {name: qwen}, servingAPI: LLMInferenceService}`, true,
		},
		{
			"auto to llm inference service", `{profile: {name: qwen}, servingAPI: Auto}`,
			`{profile: {name: qwen}, servingAPI: LLMInferenceService}`, false,
		},
		{
			"llm inference service to inference service", `{profile: {name: qwen}, servingAPI: LLMInferenceService}`,
			`{profile: {name: qwen}, servingAPI: InferenceService}`, false,
		},
		// A service stored before the field existed is defaulted to Auto on
		// read, so it cannot be switched afterwards either.
		{
			"pre-existing service to llm inference service", `profile: {name: qwen}`,
			`{profile: {name: qwen}, servingAPI: LLMInferenceService}`, false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := serviceObject(t, structural, "v1alpha2", tc.oldSpec)
			errs := validateService(t, structural, serviceObject(t, structural, "v1alpha2", tc.newSpec), old)
			assertServingAPIVerdict(t, errs, tc.valid)
		})
	}
}

func TestServiceServingAPIAdmission_v1alpha1Fence(t *testing.T) {
	structural := generatedServiceSchema(t, "v1alpha1")
	llmSpec := `{profile: {name: qwen}, servingAPI: LLMInferenceService}`

	assertServingAPIVerdict(t, validateService(t, structural, serviceObject(t, structural, "v1alpha1", llmSpec), nil), false)

	// An object created through v1alpha2 shares storage with v1alpha1, so a
	// v1alpha1 update that keeps the value must still be accepted.
	old := serviceObject(t, structural, "v1alpha1", llmSpec)
	if errs := validateService(t, structural, serviceObject(t, structural, "v1alpha1", llmSpec), old); len(errs) != 0 {
		t.Fatalf("v1alpha1 update preserving servingAPI=LLMInferenceService was rejected: %v", errs)
	}

	auto := `{model: {name: qwen}}`
	if errs := validateService(t, structural, serviceObject(t, structural, "v1alpha1", auto), nil); len(errs) != 0 {
		t.Fatalf("v1alpha1 create with defaulted servingAPI was rejected: %v", errs)
	}
}

func TestServiceLLMInferenceServiceAdmission(t *testing.T) {
	structural := generatedServiceSchema(t, "v1alpha2")
	for _, tc := range []struct {
		name    string
		spec    string
		wantErr string
	}{
		{"baseRefs with LLMInferenceService", `
profile: {name: qwen}
servingAPI: LLMInferenceService
llmInferenceService: {baseRefs: [{name: team-tracing}, {name: team-args}]}`, ""},
		{"block requires LLMInferenceService", `
profile: {name: qwen}
llmInferenceService: {baseRefs: [{name: team-tracing}]}`, "requires spec.servingAPI=LLMInferenceService"},
		{"reserved aim- prefix", `
profile: {name: qwen}
servingAPI: LLMInferenceService
llmInferenceService: {baseRefs: [{name: aim-other-profile}]}`, "reserved aim- prefix"},
		{"empty name", `
profile: {name: qwen}
servingAPI: LLMInferenceService
llmInferenceService: {baseRefs: [{name: ""}]}`, "spec.llmInferenceService.baseRefs[0].name"},
		{"duplicate names", `
profile: {name: qwen}
servingAPI: LLMInferenceService
llmInferenceService: {baseRefs: [{name: team-tracing}, {name: team-tracing}]}`, "Duplicate value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := validateServiceFully(t, "v1alpha2", serviceObject(t, structural, "v1alpha2", tc.spec))
			if tc.wantErr == "" {
				if len(errs) != 0 {
					t.Fatalf("expected the object to be accepted: %v", errs)
				}
				return
			}
			for _, err := range errs {
				if strings.Contains(err.Error(), tc.wantErr) {
					return
				}
			}
			t.Fatalf("expected a rejection mentioning %q, got: %v", tc.wantErr, errs)
		})
	}

	v1alpha1 := generatedServiceSchema(t, "v1alpha1")
	spec := `{profile: {name: qwen}, llmInferenceService: {baseRefs: [{name: team-tracing}]}}`
	errs := validateService(t, v1alpha1, serviceObject(t, v1alpha1, "v1alpha1", spec), nil)
	found := false
	for _, err := range errs {
		found = found || strings.Contains(err.Error(), "spec.llmInferenceService is not supported on v1alpha1")
	}
	if !found {
		t.Fatalf("v1alpha1 create with spec.llmInferenceService was not fenced: %v", errs)
	}
}
